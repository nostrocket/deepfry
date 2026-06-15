package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/crawler"
	"web-of-trust/pkg/dgraph"
)

// Retry parameters for transient Dgraph gRPC errors (RETRY-01/BACKOFF-01/02).
// Consistent with the relay backoff constants in pkg/crawler/crawler.go (maxBackoff=5m).
// dgraphRetryAttempts removed — retry is indefinite for transient errors (D-04).
const (
	dgraphRetryInitial = 1 * time.Minute // D-04: first wait 1m
	dgraphRetryMax     = 5 * time.Minute // D-04: cap at 5m; aligns with relay maxBackoff
)

// isDgraphTransient returns true for gRPC status codes that indicate a
// transient condition (network blip, overload) that may resolve on retry.
// The observed "code = Unavailable desc = error reading from server: EOF"
// surfaces as codes.Unavailable. Fatal codes (InvalidArgument, NotFound,
// PermissionDenied, Internal, Unimplemented) and non-gRPC errors return false
// so they still exit the loop loudly (RESIL-01).
//
// WR-01: codes.ResourceExhausted is treated as FATAL, not transient. It is the
// code Dgraph/grpc emits when a message exceeds the ~4MB gRPC limit (CLAUDE.md
// "Large Follow-Lists" anti-pattern). That condition is structurally fixed for a
// given payload — the same oversized request fails identically on every retry —
// so under indefinite retry it would livelock instead of surfacing the error.
func isDgraphTransient(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default: // ResourceExhausted and all other codes fall through to fatal
		return false
	}
}

// callMetrics accumulates cumulative call duration per call type (D-06/D-07/D-08).
// Records successful calls only; retried/failed attempts are excluded so the average
// reflects normal-op latency, not outage stalls. Single-threaded main loop — no mutex.
type callMetrics struct {
	sum   map[string]time.Duration
	count map[string]int
}

// newCallMetrics constructs an empty callMetrics accumulator.
func newCallMetrics() *callMetrics {
	return &callMetrics{
		sum:   make(map[string]time.Duration),
		count: make(map[string]int),
	}
}

// record adds a successful call duration for the named call type (D-07: success-only).
func (m *callMetrics) record(callName string, d time.Duration) {
	m.sum[callName] += d
	m.count[callName]++
}

// avg returns the cumulative average duration for the named call type.
// Returns 0 when no successful calls have been recorded to avoid divide-by-zero.
func (m *callMetrics) avg(callName string) time.Duration {
	c := m.count[callName]
	if c == 0 {
		return 0
	}
	return m.sum[callName] / time.Duration(c)
}

// retryDgraph executes fn, retrying indefinitely on transient gRPC errors with
// exponential backoff (dgraphRetryInitial→dgraphRetryMax, doubling). Fatal errors
// and context cancellation return immediately — the caller decides whether to
// break or continue (D-01/D-02). Successful call duration is recorded in metrics
// (D-07/D-08). sleepFn is injectable for deterministic testing (D-03).
func retryDgraph[T any](
	ctx context.Context,
	callName string,
	fn func() (T, error),
	metrics *callMetrics,
	sleepFn func(time.Duration) <-chan time.Time,
) (T, error) {
	var zero T
	delay := dgraphRetryInitial
	for {
		start := time.Now()
		v, err := fn()
		if err == nil {
			metrics.record(callName, time.Since(start)) // D-07: success-only timing
			return v, nil
		}
		if !isDgraphTransient(err) {
			return zero, err // D-02: fatal/non-transient — let caller decide
		}
		// Transient: log with literal "retrying in %v" so SC#2 is observable in console.
		log.Printf("Transient Dgraph error %s: %v; retrying in %v", callName, err, delay)
		select {
		case <-sleepFn(delay): // D-03: injectable; time.After in production
		case <-ctx.Done():
			return zero, ctx.Err() // SHUTDOWN-01: ctx-cancel exits mid-backoff
		}
		delay *= 2 // BACKOFF-02: doubling 1m→2m→4m→…
		if delay > dgraphRetryMax {
			delay = dgraphRetryMax // cap at 5m
		}
	}
}

func main() {
	// Create a context that can be cancelled for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Use a WaitGroup to track background operations
	var wg sync.WaitGroup

	// Start signal handler in a goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		sig := <-sigChan
		log.Printf("Received signal: %v, initiating graceful shutdown...", sig)
		cancel() // Cancel the context to stop all operations
	}()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create dgraph client for startup stats and processing
	dgraphClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create dgraph client: %v", err)
	}
	defer dgraphClient.Close()

	// Prompt for forward relay if not configured
	if cfg.ForwardRelayURL == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("No forward relay configured. Enter a relay URL to forward events to (or press Enter to skip): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			cfg.ForwardRelayURL = input
			if err := config.SaveForwardRelayURL(input); err != nil {
				log.Printf("Warning: could not save forward_relay_url to config: %v", err)
			} else {
				log.Printf("Saved forward_relay_url to config file")
			}
		}
	}

	// Create crawler
	crawlerCfg := crawler.Config{
		RelayURLs:       cfg.RelayURLs,
		DgraphAddr:      cfg.DgraphAddr,
		Timeout:         cfg.Timeout,
		Debug:           cfg.Debug,
		ForwardRelayURL: cfg.ForwardRelayURL,
		FilterBatchSize: cfg.RelayFilterBatchSize,
		// Phase 7: per-class ejection thresholds from config (D-06).
		EjectionThresholds: cfg.RelayEjectionThresholds,
		// Phase 8 TIMEOUT-02 (D-12): EOSE quorum fraction threaded from config.
		RelayEOSEQuorum: cfg.RelayEOSEQuorum,
		// HARD-01/IN-03: thread MissBackoff so BackfillNextAttempt uses the real cadence.
		MissBackoff: cfg.MissBackoff,
		OnConnectFail: func(url string) {
			// markRelayDead already emits the single ejection log line with class/count/threshold (LOG-03/D-15).
			if err := config.EjectRelayURL(url); err != nil {
				log.Printf("Warning: could not eject relay %s from config: %v", url, err)
			}
		},
	}

	crawler, err := crawler.New(crawlerCfg)
	if err != nil {
		log.Fatalf("Failed to create crawler: %v", err)
	}
	defer crawler.Close()

	// Statistics for final report
	startTime := time.Now()
	startingPubkeys, _ := dgraphClient.CountPubkeys(ctx)

	// Cumulative per-call-type duration accumulator (D-06/D-08; OBS-01).
	// Created once here and threaded into every retryDgraph call.
	metrics := newCallMetrics()

	// Main processing loop
mainLoop:
	for {
		// Check if shutdown was requested
		select {
		case <-ctx.Done():
			log.Println("Shutdown requested, breaking main loop")
			break mainLoop
		default:
			// Continue execution
		}

		// Get stale pubkeys to process (RETRY-01: indefinite transient retry).
		pubkeys, err := retryDgraph(ctx, "GetStalePubkeys",
			func() (map[string]int64, error) {
				return dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
			}, metrics, time.After)
		if err != nil {
			// WR-02: distinguish clean shutdown (ctx cancelled) from a real Dgraph
			// failure so SIGINT/SIGTERM does not log as an outage (SHUTDOWN-01).
			if ctx.Err() != nil {
				log.Println("Shutdown requested during GetStalePubkeys, breaking main loop")
			} else {
				log.Printf("Dgraph getting stale pubkeys failed: %v", err)
			}
			break mainLoop
		}

		// Count total pubkeys (RETRY-01: indefinite transient retry).
		totalPubkeys, err := retryDgraph(ctx, "CountPubkeys",
			func() (int, error) {
				return dgraphClient.CountPubkeys(ctx)
			}, metrics, time.After)
		if err != nil {
			// WR-02: clean shutdown vs real failure (SHUTDOWN-01).
			if ctx.Err() != nil {
				log.Println("Shutdown requested during CountPubkeys, breaking main loop")
			} else {
				log.Printf("Dgraph counting pubkeys failed: %v", err)
			}
			break mainLoop
		}

		// Initialize with seed if database is empty
		if totalPubkeys == 0 {
			pubkeys[cfg.SeedPubkey] = 0
			log.Printf("Database is empty, starting with seed pubkey: %s", cfg.SeedPubkey)
		}

		// Exit if no pubkeys to process
		if len(pubkeys) == 0 {
			log.Println("No stale pubkeys found, work complete")
			break
		}

		// METRIC-01 (D-15/D-16/D-17): query the honest stale count every batch.
		// CountStalePubkeys counts frontier + aged-eligible, matching GetStalePubkeys
		// selection semantics, so staleRemaining is never the always-zero (totalStale - batch).
		// (RETRY-01: indefinite transient retry.)
		totalStale, err := retryDgraph(ctx, "CountStalePubkeys",
			func() (int, error) {
				return dgraphClient.CountStalePubkeys(ctx)
			}, metrics, time.After)
		if err != nil {
			// WR-02: clean shutdown vs real failure (SHUTDOWN-01).
			if ctx.Err() != nil {
				log.Println("Shutdown requested during CountStalePubkeys, breaking main loop")
			} else {
				log.Printf("Dgraph counting stale pubkeys failed: %v", err)
			}
			break mainLoop
		}

		// Reconnect any dead relays before processing
		crawler.ReconnectRelays(ctx)

		// Process the batch; hitSet contains pubkeys that returned a kind-3 event.
		hitSet, err := crawler.FetchAndUpdateFollows(ctx, pubkeys)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("Interrupted while fetching follows: %v", err)
				break
			}
			log.Printf("Failed to fetch and update follows: %v", err)
			break
		}

		// Mark every queried pubkey as attempted so un-fetchable ones age out of the
		// frontier instead of being re-selected every cycle.
		// D-05: pass the real hit-set so MarkAttempted applies hit vs miss backoff stamping.
		batchKeys := make([]string, 0, len(pubkeys))
		for pk := range pubkeys {
			batchKeys = append(batchKeys, pk)
		}
		// Construct BackoffParams from config (cfg.MissBackoff) so PERF-02 intervals
		// are runtime-tunable without rebuilding (D-07). pkg/dgraph never imports
		// pkg/config to avoid import cycles; params are threaded here as a value struct.
		backoffParams := dgraph.BackoffParams{
			Base:              cfg.MissBackoff.Base,
			Ratio:             cfg.MissBackoff.Ratio,
			Cap:               cfg.MissBackoff.Cap,
			HitRefreshCadence: cfg.MissBackoff.HitRefreshCadence,
		}
		// RETRY-02/D-09: MarkAttempted retries transient errors indefinitely; on fatal
		// or ctx-cancel, log WARN and continue (best-effort write — do NOT break mainLoop).
		if _, err := retryDgraph(ctx, "MarkAttempted",
			func() (struct{}, error) {
				return struct{}{}, dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix(), hitSet, backoffParams)
			}, metrics, time.After); err != nil {
			log.Printf("Warning: failed to mark batch attempted (best-effort): %v", err)
		}

		// Clamp at 0 (WR-01): totalStale is recounted before this batch is stamped,
		// so on a shrinking frontier totalStale-len(pubkeys) can go negative.
		staleRemaining := max(0, totalStale-len(pubkeys))
		log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d stale remaining | %d total in DB",
			len(pubkeys), len(hitSet), staleRemaining, totalPubkeys)
		// OBS-01 (D-05/D-06): cumulative avg per call type, success-only, since process start.
		log.Printf("Avg Dgraph call duration (cumulative): GetStalePubkeys=%v CountPubkeys=%v CountStalePubkeys=%v MarkAttempted=%v",
			metrics.avg("GetStalePubkeys"), metrics.avg("CountPubkeys"), metrics.avg("CountStalePubkeys"), metrics.avg("MarkAttempted"))
	}

	// Generate final report
	endingPubkeys, _ := dgraphClient.CountPubkeys(ctx)
	generateFinalReport(startingPubkeys, endingPubkeys, startTime, cfg.SeedPubkey)

	// Wait for any background tasks to complete
	log.Println("Waiting for background tasks to complete...")
	wg.Wait()

	log.Println("Shutdown complete")
}

// generateFinalReport outputs statistics about the crawler run
func generateFinalReport(startingPubkeys, endingPubkeys int, startTime time.Time, seedPubkey string) {
	duration := time.Since(startTime)
	log.Printf("--- Final Report ---")
	log.Printf("Seed pubkey: %s", seedPubkey)
	log.Printf("Pubkeys in DB: %d at start, %d at end (%d new)", startingPubkeys, endingPubkeys, endingPubkeys-startingPubkeys)
	log.Printf("Total runtime: %s", duration)
	log.Printf("Crawler shutdown gracefully")
}
