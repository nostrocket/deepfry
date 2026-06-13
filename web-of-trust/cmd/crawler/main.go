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

// Retry parameters for transient Dgraph gRPC errors (RESIL-01).
// Consistent with the relay backoff constants in pkg/crawler/crawler.go (initialBackoff=30s, maxBackoff=5m).
const (
	dgraphRetryInitial  = 5 * time.Second
	dgraphRetryMax      = 2 * time.Minute
	dgraphRetryAttempts = 5
)

// isDgraphTransient returns true for gRPC status codes that indicate a
// transient condition (network blip, overload) that may resolve on retry.
// The observed "code = Unavailable desc = error reading from server: EOF"
// surfaces as codes.Unavailable. Fatal codes (InvalidArgument, NotFound,
// PermissionDenied, Internal, Unimplemented) and non-gRPC errors return false
// so they still exit the loop loudly (RESIL-01).
func isDgraphTransient(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
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

		// Get stale pubkeys to process (RESIL-01: retry on transient gRPC errors).
		var pubkeys map[string]int64
		{
			var retryDelay = dgraphRetryInitial
			for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
				pubkeys, err = dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
				if err == nil {
					break
				}
				if !isDgraphTransient(err) {
					log.Printf("Fatal Dgraph error getting stale pubkeys: %v", err)
					break mainLoop
				}
				log.Printf("Transient Dgraph error getting stale pubkeys (attempt %d/%d): %v; retrying in %v",
					attempt+1, dgraphRetryAttempts, err, retryDelay)
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					break mainLoop
				}
				retryDelay *= 2
				if retryDelay > dgraphRetryMax {
					retryDelay = dgraphRetryMax
				}
			}
			if err != nil {
				log.Printf("Dgraph unavailable after %d attempts getting stale pubkeys, exiting: %v", dgraphRetryAttempts, err)
				break mainLoop
			}
		}

		// Count total pubkeys (RESIL-01: retry on transient gRPC errors).
		var totalPubkeys int
		{
			var retryDelay = dgraphRetryInitial
			for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
				totalPubkeys, err = dgraphClient.CountPubkeys(ctx)
				if err == nil {
					break
				}
				if !isDgraphTransient(err) {
					log.Printf("Fatal Dgraph error counting pubkeys: %v", err)
					break mainLoop
				}
				log.Printf("Transient Dgraph error counting pubkeys (attempt %d/%d): %v; retrying in %v",
					attempt+1, dgraphRetryAttempts, err, retryDelay)
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					break mainLoop
				}
				retryDelay *= 2
				if retryDelay > dgraphRetryMax {
					retryDelay = dgraphRetryMax
				}
			}
			if err != nil {
				log.Printf("Dgraph unavailable after %d attempts counting pubkeys, exiting: %v", dgraphRetryAttempts, err)
				break mainLoop
			}
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
		// (RESIL-01: retry on transient gRPC errors.)
		var totalStale int
		{
			var retryDelay = dgraphRetryInitial
			for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
				totalStale, err = dgraphClient.CountStalePubkeys(ctx)
				if err == nil {
					break
				}
				if !isDgraphTransient(err) {
					log.Printf("Fatal Dgraph error counting stale pubkeys: %v", err)
					break mainLoop
				}
				log.Printf("Transient Dgraph error counting stale pubkeys (attempt %d/%d): %v; retrying in %v",
					attempt+1, dgraphRetryAttempts, err, retryDelay)
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					break mainLoop
				}
				retryDelay *= 2
				if retryDelay > dgraphRetryMax {
					retryDelay = dgraphRetryMax
				}
			}
			if err != nil {
				log.Printf("Dgraph unavailable after %d attempts counting stale pubkeys, exiting: %v", dgraphRetryAttempts, err)
				break mainLoop
			}
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
		// RESIL-01: MarkAttempted retry is best-effort (PERF-02 stamping) — on
		// persistent transient failure, log WARN and continue; do NOT exit the loop.
		{
			var markErr error
			var retryDelay = dgraphRetryInitial
			for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
				markErr = dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix(), hitSet, backoffParams)
				if markErr == nil {
					break
				}
				if !isDgraphTransient(markErr) {
					log.Printf("Warning: non-transient error marking batch attempted: %v", markErr)
					break // best-effort: log and continue
				}
				log.Printf("Transient Dgraph error marking attempted (attempt %d/%d): %v; retrying in %v",
					attempt+1, dgraphRetryAttempts, markErr, retryDelay)
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					break mainLoop
				}
				retryDelay *= 2
				if retryDelay > dgraphRetryMax {
					retryDelay = dgraphRetryMax
				}
			}
			if markErr != nil {
				log.Printf("Warning: failed to mark batch attempted after %d attempts (best-effort): %v", dgraphRetryAttempts, markErr)
			}
		}

		// Clamp at 0 (WR-01): totalStale is recounted before this batch is stamped,
		// so on a shrinking frontier totalStale-len(pubkeys) can go negative.
		staleRemaining := max(0, totalStale-len(pubkeys))
		log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d stale remaining | %d total in DB",
			len(pubkeys), len(hitSet), staleRemaining, totalPubkeys)
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
