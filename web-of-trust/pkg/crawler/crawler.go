package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
)

type subscriptionError struct {
	err error
}

func (e *subscriptionError) Error() string { return e.err.Error() }
func (e *subscriptionError) Unwrap() error { return e.err }

type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// filterRejectionError is returned by queryRelay when a filter-cap rejection
// occurs (at-cap or floor-reached). The FetchAndUpdateFollows dispatcher maps
// this to classFilterRej (threshold 3), distinct from classTransport (threshold 10).
// This ensures floor-reached and at-cap rejections are correctly classified as
// filter_rejection, not transport (closes WR-01).
type filterRejectionError struct {
	err error
}

func (e *filterRejectionError) Error() string { return e.err.Error() }
func (e *filterRejectionError) Unwrap() error { return e.err }

const (
	initialBackoff = 30 * time.Second
	maxBackoff     = 5 * time.Minute
)

// failureClass identifies which per-relay counter to increment.
type failureClass int

const (
	classTransport failureClass = iota // transport errors, connection drops
	classFilterRej                     // filter rejections at or below learned cap
	classSubFlap                       // Subscribe refused (not filter-size related)
)

func (fc failureClass) String() string {
	switch fc {
	case classTransport:
		return "transport"
	case classFilterRej:
		return "filter_rejection"
	case classSubFlap:
		return "subscription_flap"
	default:
		return "unknown"
	}
}

type relayState struct {
	url     string
	conn    *nostr.Relay
	alive   bool
	backoff time.Duration
	retryAt time.Time

	// Per-class failure counters (D-05). Halved on reconnect (D-01),
	// reset to 0 on completed query (D-02). In-memory only (D-04).
	failTransport atomic.Int32
	failFilterRej atomic.Int32
	failSubFlap   atomic.Int32

	// Phase 6: filterCap NOT reset on reconnect (D-09).
	filterCap atomic.Int32

	// Probe-up state (D-10/D-11).
	successStreak atomic.Int32 // incremented once per successful queryRelay call
	probing       atomic.Bool  // true while a probe chunk is in flight; exempt from filter_rejection counting

	// completedGen records the batch generation (Crawler.batchSeq) in which this
	// relay's per-relay goroutine last returned (success or error). The dispatcher
	// treats a relay as outstanding for the current batch when completedGen != the
	// batch's generation, so it can close and mark such relays dead (HANG-01/HANG-03).
	//
	// WR-01 (iteration 4): a monotonically-increasing per-batch generation replaces
	// the prior reset-to-false boolean. The boolean had a benign cross-batch race — a
	// leftover batch-N goroutine could Store(true) AFTER batch N+1's reset Store(false),
	// deferring one stuck relay's close by a single batch. With a generation token there
	// is no reset to race: a stale goroutine stamps its OWN (older) generation, which can
	// never equal the current batch's generation, so it is always correctly seen as
	// outstanding. Zero value (0) means "never completed any batch".
	completedGen atomic.Int64
}

type Crawler struct {
	relays          []*relayState
	forwardRelay    *relayState
	dgClient        *dgraph.Client
	timeout         time.Duration
	debug           bool
	dbUpdateMutex   sync.Mutex
	onConnectFail   func(url string)
	filterBatchSize int
	// Phase 7: per-class ejection thresholds from config (D-06)
	ejectionThresholds map[failureClass]int32
	// Phase 8: EOSE-quorum fraction (D-12/D-13). 0 disables early exit.
	quorum float64

	// batchSeq is a monotonic per-batch generation counter incremented once at the
	// top of every FetchAndUpdateFollows call. Per-relay goroutines stamp the current
	// generation into relayState.completedGen on return; the dispatcher compares against
	// it to identify relays still outstanding for the batch (WR-01 — replaces the
	// race-prone reset-to-false boolean marker).
	batchSeq atomic.Int64

	// queryRelayFn is the per-relay query step invoked by FetchAndUpdateFollows.
	// It defaults to (*Crawler).queryRelay; production code never reassigns it.
	// This is a testable seam (WR-05 precedent): tests inject a function that
	// blocks indefinitely — ignoring the relay-query context exactly as go-nostr's
	// Subscription.Fire does (see HANG-FINDINGS.md) — to prove FetchAndUpdateFollows
	// still returns on its own timeout. A nil value falls back to c.queryRelay so
	// Crawlers built as struct literals (not via New) behave unchanged.
	queryRelayFn func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error
}

type Config struct {
	RelayURLs          []string
	DgraphAddr         string
	Timeout            time.Duration
	Debug              bool
	ForwardRelayURL    string
	FilterBatchSize    int
	OnConnectFail      func(url string)
	EjectionThresholds config.EjectionThresholds // Phase 7
	// Phase 8: EOSE quorum fraction (D-12). 0 disables early exit (full-timeout preserved).
	RelayEOSEQuorum float64
	// MissBackoff provides the hit-refresh cadence for BackfillNextAttempt (HARD-01/IN-03).
	MissBackoff config.MissBackoffParams
}

func New(cfg Config) (*Crawler, error) {
	// Initialize Dgraph client
	dgClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Dgraph: %w", err)
	}

	// Ensure schema
	ctx := context.Background()
	if err := dgClient.EnsureSchema(ctx); err != nil {
		dgClient.Close()
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	// D-06: one-time backfill of next_attempt for existing attempted nodes.
	// Non-fatal: a failed backfill leaves those nodes selectable until their
	// next_attempt is set; the crawler can still run.
	cadenceSec := int64(cfg.MissBackoff.HitRefreshCadence.Seconds())
	if count, err := dgClient.BackfillNextAttempt(ctx, cadenceSec); err != nil {
		log.Printf("WARN: BackfillNextAttempt failed (non-fatal, crawler will continue): %v", err)
	} else {
		log.Printf("BackfillNextAttempt: seeded %d nodes with initial next_attempt", count)
	}

	// Connect to all relays
	var relays []*relayState
	connected := 0

	for _, url := range cfg.RelayURLs {
		rs := &relayState{url: url, backoff: initialBackoff}
		rs.filterCap.Store(int32(cfg.FilterBatchSize))
		noticeHandler := nostr.WithNoticeHandler(func(notice string) {
			handleFilterNotice(rs, notice, 10)
		})
		relay, err := nostr.RelayConnect(context.Background(), url, noticeHandler)
		if err != nil {
			// CR-03: keep the relay in the pool with alive=false so ReconnectRelays
			// can retry under threshold governance. Do NOT call cfg.OnConnectFail here —
			// a transient boot/DNS outage must not permanently eject all relays (T-07-DOS).
			log.Printf("WARN: Failed to connect to relay %s at startup, will retry: %v", url, err)
			rs.alive = false
			rs.conn = nil
			rs.failTransport.Add(1)
			rs.retryAt = time.Now().Add(rs.backoff)
			rs.backoff *= 2
			if rs.backoff > maxBackoff {
				rs.backoff = maxBackoff
			}
			relays = append(relays, rs)
			continue
		}
		rs.conn = relay
		rs.alive = true
		relays = append(relays, rs)
		connected++
		if cfg.Debug {
			log.Printf("Connected to relay: %s", url)
		}
	}

	if connected == 0 {
		dgClient.Close()
		return nil, fmt.Errorf("failed to connect to any relays")
	}

	log.Printf("Connected to %d/%d relays", connected, len(cfg.RelayURLs))

	c := &Crawler{
		relays:          relays,
		dgClient:        dgClient,
		timeout:         cfg.Timeout,
		debug:           cfg.Debug,
		onConnectFail:   cfg.OnConnectFail,
		filterBatchSize: cfg.FilterBatchSize,
		quorum:          cfg.RelayEOSEQuorum,
		ejectionThresholds: map[failureClass]int32{
			classTransport: int32(cfg.EjectionThresholds.Transport),
			classFilterRej: int32(cfg.EjectionThresholds.FilterRej),
			classSubFlap:   int32(cfg.EjectionThresholds.SubFlap),
		},
	}

	// Connect to forward relay if configured
	if cfg.ForwardRelayURL != "" {
		rs := &relayState{url: cfg.ForwardRelayURL, backoff: initialBackoff}
		relay, err := nostr.RelayConnect(context.Background(), cfg.ForwardRelayURL)
		if err != nil {
			log.Printf("WARN: Failed to connect to forward relay %s: %v (will retry later)", cfg.ForwardRelayURL, err)
		} else {
			rs.conn = relay
			rs.alive = true
			log.Printf("Connected to forward relay: %s", cfg.ForwardRelayURL)
		}
		c.forwardRelay = rs
	}

	return c, nil
}

func (c *Crawler) Close() {
	for _, rs := range c.relays {
		if rs.conn != nil {
			rs.conn.Close()
		}
	}
	if c.forwardRelay != nil && c.forwardRelay.conn != nil {
		c.forwardRelay.conn.Close()
	}
	if c.dgClient != nil {
		c.dgClient.Close()
	}
}

func (c *Crawler) forwardEvent(ctx context.Context, event *nostr.Event) {
	if c.forwardRelay == nil || !c.forwardRelay.alive {
		return
	}
	// Wrap publish in a short bounded context (c.timeout) so a hung forward
	// relay cannot stall the single-threaded drain loop (HARD-03/WR-04).
	pubCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.forwardRelay.conn.Publish(pubCtx, *event)
	if err != nil {
		log.Printf("WARN: Failed to forward event %s to %s: %v", event.ID, c.forwardRelay.url, err)
		if c.forwardRelay.conn != nil {
			c.forwardRelay.conn.Close()
		}
		c.forwardRelay.conn = nil
		c.forwardRelay.alive = false
		c.forwardRelay.retryAt = time.Now().Add(c.forwardRelay.backoff)
		c.forwardRelay.backoff *= 2
		if c.forwardRelay.backoff > maxBackoff {
			c.forwardRelay.backoff = maxBackoff
		}
	} else if c.debug {
		log.Printf("Forwarded event %s to %s", event.ID, c.forwardRelay.url)
	}
}

// markRelayDead increments the per-class failure counter for url, applies
// threshold-driven ejection (via onConnectFail), or reschedules with backoff.
// Exactly one log line is emitted: either ejection or dead+retry.
// LOG-03/D-15: this is the single authoritative dead-state log line; callers
// must not emit their own WARN before calling markRelayDead.
func (c *Crawler) markRelayDead(url string, class failureClass) {
	kept := c.relays[:0]
	for _, rs := range c.relays {
		if rs.url != url {
			kept = append(kept, rs)
			continue
		}
		if rs.conn != nil {
			rs.conn.Close()
		}
		rs.conn = nil
		rs.alive = false

		var count int32
		switch class {
		case classTransport:
			count = rs.failTransport.Add(1)
		case classFilterRej:
			count = rs.failFilterRej.Add(1)
		case classSubFlap:
			count = rs.failSubFlap.Add(1)
		}

		threshold := c.ejectionThresholds[class]
		if threshold <= 0 {
			threshold = 10 // safety: never eject immediately on misconfigured threshold
		}
		if count >= threshold {
			log.Printf("Relay %s ejected (%s %d/%d)", url, class, count, threshold)
			if c.onConnectFail != nil {
				c.onConnectFail(url)
			}
			continue // do NOT re-append to kept
		}

		rs.retryAt = time.Now().Add(rs.backoff)
		log.Printf("Relay %s dead (%s %d/%d), retry in %v", url, class, count, threshold, rs.backoff)
		rs.backoff *= 2
		if rs.backoff > maxBackoff {
			rs.backoff = maxBackoff
		}
		kept = append(kept, rs)
	}
	c.relays = kept
}

func (c *Crawler) ReconnectRelays(ctx context.Context) {
	var reconnected, removed, stillDead int
	kept := c.relays[:0]
	for _, rs := range c.relays {
		if rs.alive {
			kept = append(kept, rs)
			continue
		}
		if time.Now().Before(rs.retryAt) {
			if c.debug {
				log.Printf("Skipping reconnect for %s, next retry at %v", rs.url, rs.retryAt.Format(time.RFC3339))
			}
			kept = append(kept, rs)
			continue
		}
		noticeHandler := nostr.WithNoticeHandler(func(notice string) {
			handleFilterNotice(rs, notice, 10)
		})
		relay, err := nostr.RelayConnect(ctx, rs.url, noticeHandler)
		if err != nil {
			if c.debug {
				log.Printf("Reconnect to %s failed: %v", rs.url, err)
			}
			// D-03: failed reconnect counts as transport failure; threshold governs ejection.
			rs.failTransport.Add(1)
			threshold := c.ejectionThresholds[classTransport]
			if threshold <= 0 {
				threshold = 10
			}
			if rs.failTransport.Load() >= threshold {
				log.Printf("Relay %s ejected (%s %d/%d) after repeated reconnect failures",
					rs.url, classTransport, rs.failTransport.Load(), threshold)
				if c.onConnectFail != nil {
					c.onConnectFail(rs.url)
				}
				removed++
				continue
			}
			rs.retryAt = time.Now().Add(rs.backoff)
			rs.backoff *= 2
			if rs.backoff > maxBackoff {
				rs.backoff = maxBackoff
			}
			stillDead++
			kept = append(kept, rs)
			continue
		}
		reconnected++
		rs.conn = relay
		rs.alive = true
		rs.backoff = initialBackoff
		// D-01: halve all class counters on reconnect (not reset to 0).
		rs.failTransport.Store(rs.failTransport.Load() / 2)
		rs.failFilterRej.Store(rs.failFilterRej.Load() / 2)
		rs.failSubFlap.Store(rs.failSubFlap.Load() / 2)
		// D-09: filterCap is NOT reset on reconnect — learned cap survives.
		kept = append(kept, rs)
		if c.debug {
			log.Printf("Reconnected to relay: %s", rs.url)
		}
	}
	c.relays = kept

	// D-13/LOG-01: emit one sweep-summary line only when something changed.
	if reconnected > 0 || removed > 0 || stillDead > 0 {
		total := len(c.relays) + removed
		log.Printf("Reconnected %d/%d relays, %d removed, %d still dead",
			reconnected, total, removed, stillDead)
	}

	// Reconnect forward relay if needed (UNCHANGED — does not route through markRelayDead).
	if c.forwardRelay != nil && !c.forwardRelay.alive {
		rs := c.forwardRelay
		if time.Now().Before(rs.retryAt) {
			if c.debug {
				log.Printf("Skipping reconnect for forward relay %s, next retry at %v", rs.url, rs.retryAt.Format(time.RFC3339))
			}
			return
		}
		relay, err := nostr.RelayConnect(ctx, rs.url)
		if err != nil {
			rs.retryAt = time.Now().Add(rs.backoff)
			log.Printf("WARN: Reconnect to forward relay %s failed, next retry in %v: %v", rs.url, rs.backoff, err)
			rs.backoff *= 2
			if rs.backoff > maxBackoff {
				rs.backoff = maxBackoff
			}
			return
		}
		rs.conn = relay
		rs.alive = true
		rs.backoff = initialBackoff
		log.Printf("Reconnected to forward relay: %s", rs.url)
	}
}

// quorumReached returns true when the done count has reached the ceil of
// (q * queried), indicating that enough relays have responded (EOSE or error)
// to cancel the batch early (D-13/D-14).
//
// Returns false when:
//   - q <= 0 (quorum disabled, full-timeout behaviour preserved — T-08-EARLY)
//   - queried == 0 (no relays launched; ceil of 0 == 0 would fire immediately)
//   - done < ceil(q * queried) (threshold not yet reached)
//
// This is an exported-by-test seam (internal package) so tests can verify the
// threshold math without launching real relays (WR-05 precedent).
func quorumReached(done, queried int32, q float64) bool {
	if q <= 0 || queried == 0 {
		return false
	}
	threshold := math.Ceil(float64(queried) * q)
	return float64(done) >= threshold
}

// FetchAndUpdateFollows queries relays for kind 3 events for the given pubkeys
// and updates the database. Returns the set of pubkeys that had events (hits).
func (c *Crawler) FetchAndUpdateFollows(relayContext context.Context, pubkeys map[string]int64) (map[string]struct{}, error) {

	// Extract pubkey strings from the map
	authors := make([]string, 0, len(pubkeys))
	for pubkey := range pubkeys {
		// Validate pubkey format: must be exactly 64 lowercase hex chars.
		if err := dgraph.ValidatePubkey(pubkey); err != nil {
			if c.debug {
				log.Printf("Skipping invalid pubkey: %s, error: %v", pubkey, err)
			}
			continue
		}
		authors = append(authors, pubkey)
	}

	// Create filter for kind 3 events from all specified pubkeys
	filter := nostr.Filter{
		Authors: authors,
		Kinds:   []int{3},
		Limit:   len(pubkeys), // Allow one event per pubkey
	}

	type relayError struct {
		url string
		err error
	}

	// Query all alive relays concurrently
	var wg sync.WaitGroup
	eventsChan := make(chan *nostr.Event, len(pubkeys)*len(c.relays))
	errorsChan := make(chan relayError, len(c.relays))

	// Set timeout context for relay operations only
	batchStart := time.Now()
	relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
	defer cancel()

	// WR-01: bump the per-batch generation. Per-relay goroutines stamp this value into
	// rs.completedGen on return; the dispatcher treats rs.completedGen != currentGen as
	// "outstanding this batch". No reset loop is needed (and none is raceable): a
	// leftover goroutine from a prior batch stamps that batch's older generation, which
	// can never equal currentGen, so it is always correctly seen as outstanding.
	currentGen := c.batchSeq.Add(1)

	// Count alive relays being launched for the quorum denominator (D-14).
	//
	// WR-04 invariant: queriedRelays and the set of launched goroutines are FIXED for
	// the duration of this batch. markRelayDead (the only thing that flips rs.alive to
	// false) runs exclusively in the single-threaded dispatcher below — never from a
	// per-relay goroutine (CR-02) — and only AFTER the quorum loop has finished
	// dispatching, so the live set cannot shrink mid-batch. If that invariant is ever
	// broken (e.g. a relay flipped dead while goroutines are in flight), queriedRelays
	// would no longer match the goroutine set and quorumReached could fire early or
	// never. Keep relay-set mutation out of the batch window.
	//
	// WR-03 (iteration 3): make that invariant STRUCTURAL rather than relying on a
	// comment. Capture the alive set ONCE into launchSet, derive the quorum denominator
	// from len(launchSet), and launch goroutines exclusively over launchSet. The count
	// and the launched set are now provably the same pass, so a future edit that mutates
	// relay state between "count" and "launch" cannot silently desynchronise the
	// denominator from the goroutine set.
	launchSet := make([]*relayState, 0, len(c.relays))
	for _, rs := range c.relays {
		if rs.alive {
			launchSet = append(launchSet, rs)
		}
	}
	queriedRelays := int32(len(launchSet))

	// Per-batch EOSE-quorum counter (D-13). Function-local — not shared across batches.
	var done atomic.Int32

	// Resolve the per-relay query step (test seam; defaults to the real method).
	queryRelay := c.queryRelayFn
	if queryRelay == nil {
		queryRelay = c.queryRelay
	}

	// Launch goroutines for each alive relay.
	//
	// WR-03 (iteration 3): launch over launchSet — the SAME captured slice the quorum
	// denominator was derived from — so the launched goroutine set and queriedRelays
	// cannot drift. (Previously this ranged c.relays again with a separate rs.alive
	// gate, a second pass that could desynchronise from the count if relay state were
	// ever mutated between the two passes.)
	//
	// WR-05: rs is passed explicitly as a goroutine argument (safe under any Go
	// version). The timeout-exit / quorum-close loops (below) instead rely on Go 1.22+
	// per-iteration loop-variable semantics; this module targets Go 1.24.1 so that is
	// correct today. Do not downgrade the toolchain `go` directive below 1.22 without
	// revisiting those loops, and prefer passing rs as an explicit arg wherever a
	// goroutine captures it.
	for _, rs := range launchSet {
		wg.Add(1)
		go func(rs *relayState) {
			defer wg.Done()
			err := queryRelay(relayQueryContext, rs, filter, eventsChan)
			// Mark this relay's query as complete (on both success and error paths) so
			// the dispatcher can distinguish outstanding relays on the timeout exit
			// (HANG-01/HANG-03). This write races with the dispatcher reading it only
			// after relayQueryContext.Done() fires; since Done() is the signal that the
			// timeout elapsed, any relay that did not yet stamp the current generation by
			// that point is correctly identified as outstanding.
			rs.completedGen.Store(currentGen)
			if err != nil {
				errorsChan <- relayError{url: rs.url, err: err}
				// D-14: errors count toward quorum (move batch forward, not stall it).
				if quorumReached(done.Add(1), queriedRelays, c.quorum) {
					if c.debug {
						log.Printf("EOSE quorum reached (error path), cancelling relay query context")
					}
					cancel()
				}
				return
			}
			// D-02: successful query resets all class counters to 0 and increments streak.
			rs.failTransport.Store(0)
			rs.failFilterRej.Store(0)
			rs.failSubFlap.Store(0)
			rs.successStreak.Add(1)
			// D-13: EOSE (success path) also counts toward quorum.
			if quorumReached(done.Add(1), queriedRelays, c.quorum) {
				if c.debug {
					log.Printf("EOSE quorum reached (success path), cancelling relay query context")
				}
				cancel()
			}
		}(rs)
	}

	// Close channels when all goroutines complete
	go func() {
		wg.Wait()
		close(eventsChan)
		close(errorsChan)
	}()

	// Map to keep track of processed event IDs
	processedEventIDs := make(map[string]struct{})
	// Track unique pubkeys that had events returned
	pubkeysWithEvents := make(map[string]struct{})
	// relayQueryDoneCh is the relay-query context's Done channel. When it fires,
	// the dispatcher takes the independent timeout/quorum-exit path (HANG-01).
	relayQueryDoneCh := relayQueryContext.Done()
	// Process events from all relays using a switch loop
	for {
		select {
		case <-relayQueryDoneCh:
			// The relay query context was cancelled — either by the c.timeout deadline
			// (DeadlineExceeded) or by the EOSE-quorum early exit (Canceled / cancel()).
			//
			// HANG-01: independent exit path — do NOT block on wg.Wait() or eventsChan
			// close. Drain only events already buffered in eventsChan (non-blocking), then
			// return pubkeysWithEvents with nil error. This ensures FetchAndUpdateFollows
			// always returns within a bounded multiple of c.timeout regardless of whether
			// any per-relay query goroutine ever returns.
			//
			// NOTE (WR-06): this drain is NOT lossless. A goroutine sitting between
			// receiving from sub.Events and sending to eventsChan returns ctx.Err()
			// on cancellation and drops that event. Under the 70% EOSE-quorum this is
			// by design (latency over completeness) — the slow ~30% of relays may have
			// their events discarded, and the affected pubkeys are then stamped as
			// misses (backed off). Already-buffered events in eventsChan still drain.
			if c.debug {
				log.Printf("Relay query context cancelled while processing events: %v", relayQueryContext.Err())
				if relayQueryContext.Err() == context.DeadlineExceeded {
					log.Printf("Relay query timeout reached, draining buffered events and returning")
				}
			}

			// WR-02 (iteration 3): capture the budget/timeout-vs-quorum decision NOW —
			// the moment the relay-query context fired — BEFORE the drain phase runs. The
			// drain (below) acquires dbUpdateMutex and performs DB writes / forwards whose
			// own wall-clock cost, plus scheduling jitter, can push time.Since(batchStart)
			// across c.timeout even on a legitimate quorum early-exit. Reading the budget
			// after the drain would then mis-classify a healthy late quorum exit as a
			// timeout and markRelayDead(classTransport) every slow-but-alive relay,
			// over-penalising them toward ejection. Capturing here makes the decision
			// reflect why the dispatcher woke (deadline vs quorum cancel), not how long the
			// drain subsequently took.
			budgetExhausted := relayQueryContext.Err() == context.DeadlineExceeded ||
				time.Since(batchStart) >= c.timeout

			// Non-blocking drain of events already buffered in eventsChan.
			// Process each through the same signature-check / forward / update path.
			//
			// WR-03: derive ONE shared deadline for the entire drain phase rather than
			// letting forwardEvent spend a fresh full c.timeout per event. The drain is
			// entered because the relay-query budget already elapsed; with a full buffer,
			// a per-event c.timeout would multiply the worst-case return time well beyond
			// the bounded-return guarantee. drainCtx caps the total time the drain (and
			// the bounded forwardEvent publishes nested under it) can spend at one
			// c.timeout across all buffered events.
			drainCtx, drainCancel := context.WithTimeout(relayContext, c.timeout)
			c.dbUpdateMutex.Lock()
		drainLoop:
			for {
				select {
				case ev, ok := <-eventsChan:
					if !ok || ev == nil {
						break drainLoop
					}
					if _, exists := processedEventIDs[ev.ID]; exists {
						continue
					}
					if ok2, err2 := ev.CheckSignature(); !ok2 {
						log.Printf("WARN: Invalid signature for event %s from pubkey %s: %v", ev.ID, ev.PubKey, err2)
						c.logSignatureValidationMetrics(ev.PubKey, false)
						continue
					}
					c.logSignatureValidationMetrics(ev.PubKey, true)
					c.forwardEvent(drainCtx, ev)
					pubkeysWithEvents[ev.PubKey] = struct{}{}
					if ev.CreatedAt > nostr.Timestamp(pubkeys[ev.PubKey]) {
						if err2 := c.updateFollowsFromEvent(drainCtx, ev); err2 != nil {
							c.dbUpdateMutex.Unlock()
							drainCancel()
							return pubkeysWithEvents, fmt.Errorf("failed to update follows for pubkey %s: %w", ev.PubKey, err2)
						}
						processedEventIDs[ev.ID] = struct{}{}
					} else {
						// WR-02: a failed TouchLastDBUpdate leaves last_db_update unadvanced,
						// keeping the pubkey in the stale frontier to be re-queried forever.
						// Surface it (debug) instead of silently discarding the error.
						if _, err2 := c.dgClient.TouchLastDBUpdate(drainCtx, ev.PubKey); err2 != nil && c.debug {
							log.Printf("WARN: TouchLastDBUpdate failed for %s: %v", ev.PubKey, err2)
						}
					}
				default:
					break drainLoop
				}
			}
			c.dbUpdateMutex.Unlock()
			drainCancel()

			// HANG-03: on the timeout path, close outstanding relay connections and mark
			// them dead (classTransport) so the existing threshold ejection (RELAY-01/02)
			// governs repeat offenders. This runs in the single-threaded dispatcher
			// (CR-02) — never from per-relay goroutines. The EOSE-quorum-cancel path is a
			// normal early exit; those relays are NOT marked dead (but see WR-01 below —
			// their connections are still closed to reap leaked goroutines).
			//
			// WR-01: do NOT discriminate on relayQueryContext.Err() alone. When the
			// c.timeout deadline and a quorum-triggered cancel() fire near-simultaneously,
			// the context records whichever cancellation won the race (first cancel wins,
			// per the stdlib), so a genuinely-timed-out batch can mis-report
			// context.Canceled and skip marking truly-stuck relays dead. Instead key off
			// the actual wall-clock budget (budgetExhausted, captured BEFORE the drain per
			// WR-02): if the batch consumed its full c.timeout, any relay still outstanding
			// is genuinely stuck regardless of which cancel cause the context surfaced. A
			// quorum early-exit (which fires well before the budget elapses) correctly does
			// NOT enter this branch.
			if budgetExhausted {
				// CR-01: snapshot the stuck relay URLs BEFORE calling markRelayDead.
				// markRelayDead reassigns c.relays via in-place compaction
				// (kept := c.relays[:0]; ...; c.relays = kept), writing into the same
				// backing array this loop would otherwise be ranging. Iterating c.relays
				// directly while it is compacted mid-loop can skip an outstanding relay
				// (its connection never gets closed — the exact leak this phase prevents)
				// or double-process one. Decoupling the iteration source (this snapshot)
				// from the mutation target (c.relays) makes the pass correct for any
				// number of outstanding relays and any ejection outcome.
				var stuck []string
				for _, rs := range c.relays {
					if rs.alive && rs.completedGen.Load() != currentGen {
						if c.debug {
							log.Printf("Relay %s timed out with outstanding query, closing and marking dead", rs.url)
						}
						stuck = append(stuck, rs.url)
					}
				}
				for _, url := range stuck {
					c.markRelayDead(url, classTransport)
				}
			} else if relayQueryContext.Err() != nil {
				// WR-01 (iteration 3): EOSE-quorum early-exit path (context cancelled but
				// the wall-clock budget was NOT exhausted). This completes the CR-02 fix.
				//
				// A relay still outstanding at quorum-exit may be wedged on a half-open TCP
				// write inside go-nostr's Subscription.Fire(): relay.Write only unblocks on
				// the *connection* context (relay.go:319-327), never on the per-query
				// relayQueryContext we just cancelled. The keepalive ping that would detect
				// the dead peer and Close() shares one select with the wedged WriteMessage
				// (relay.go:168-211), so it can't fire either. Nothing reliably closes the
				// connection — so the abandoned Subscribe child AND the CR-02 cleanup
				// goroutine (which blocks until that child delivers) both park indefinitely,
				// and the relay stays alive=true and is re-queried next batch, parking ~2
				// more goroutines each time. That per-batch accumulation is the WR-01 leak.
				//
				// Fix: close the connection of any genuinely-outstanding relay here too, so
				// the parked relay.Write returns "connection closed", the Subscribe child
				// delivers, and both goroutines reap. We do NOT markRelayDead / increment a
				// failure counter on this path: a quorum-cancelled relay is slow this batch,
				// not transport-failed, and over-penalising it (WR-02 concern) would push
				// healthy slow relays toward ejection.
				//
				// Tradeoff (documented per the task): closing the connection of a relay that
				// merely lost the quorum race (would have completed microseconds later) is a
				// minor cost — ReconnectRelays brings it back next loop with no failure
				// penalty, and the only loss is the in-flight query for THIS batch, whose
				// events were already going to be discarded by the lossy quorum drain
				// (WR-06). We deliberately prefer unblocking the goroutine over preserving a
				// racing relay's connection, because the alternative (leaving it open) is the
				// goroutine leak this phase exists to contain. We close but do NOT penalise.
				//
				// completedGen != currentGen is the same outstanding test the timeout path
				// uses; a relay that stamped the current generation is done and excluded. The
				// snapshot-then-act split is not needed here because we never call
				// markRelayDead (no c.relays compaction) — we mutate per-relay fields in place
				// while ranging, which is safe.
				for _, rs := range c.relays {
					if rs.alive && rs.completedGen.Load() != currentGen && rs.conn != nil {
						if c.debug {
							log.Printf("Relay %s outstanding at quorum exit, closing connection to reap stuck goroutines (no penalty)", rs.url)
						}
						rs.conn.Close()
						rs.conn = nil
						rs.alive = false // ReconnectRelays will bring it back with no failure penalty
					}
				}
			}

			return pubkeysWithEvents, nil

		case <-relayContext.Done():
			// The main context was cancelled (external cancellation — e.g. SIGINT).
			if c.debug {
				log.Printf("Main context cancelled while processing events: %v", relayContext.Err())
			}
			return pubkeysWithEvents, relayContext.Err()

		case event, ok := <-eventsChan:
			c.dbUpdateMutex.Lock()
			if !ok {
				// Channel closed, exit the loop
				if c.debug {
					log.Printf("Processed follows for %d pubkeys across %d relays", len(processedEventIDs), len(c.relays))
				}
				c.dbUpdateMutex.Unlock()
				return pubkeysWithEvents, nil
			}

			if event == nil {
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Skip if we've already processed this event ID
			if _, exists := processedEventIDs[event.ID]; exists {
				if c.debug {
					log.Printf("Skipping already processed event: %s", event.ID)
				}
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Validate event signature
			if ok, err := event.CheckSignature(); !ok {
				log.Printf("WARN: Invalid signature for event %s from pubkey %s: %v", event.ID, event.PubKey, err)
				c.logSignatureValidationMetrics(event.PubKey, false)
				c.dbUpdateMutex.Unlock()
				continue
			}
			c.logSignatureValidationMetrics(event.PubKey, true)

			c.forwardEvent(relayContext, event)

			pubkeysWithEvents[event.PubKey] = struct{}{}

			if event.CreatedAt <= nostr.Timestamp(pubkeys[event.PubKey]) {
				if c.debug {
					fmt.Println("already have newer event for " + event.PubKey)
				}
				// WR-02: surface a failed TouchLastDBUpdate (debug) rather than dropping
				// it — a discarded error keeps the pubkey stale and re-queried forever.
				if _, err2 := c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey); err2 != nil && c.debug {
					log.Printf("WARN: TouchLastDBUpdate failed for %s: %v", event.PubKey, err2)
				}
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Process the event using original context (no relay timeout)
			if err := c.updateFollowsFromEvent(relayContext, event); err != nil {
				c.dbUpdateMutex.Unlock()
				return pubkeysWithEvents, fmt.Errorf("failed to update follows for pubkey %s: %w", event.PubKey, err)
			}
			processedEventIDs[event.ID] = struct{}{}
			c.dbUpdateMutex.Unlock()

		case re, ok := <-errorsChan:
			if !ok {
				// Error channel closed
				continue
			}

			// Don't report context cancellation as relay error
			if strings.Contains(re.err.Error(), "context canceled") ||
				strings.Contains(re.err.Error(), "context deadline exceeded") {
				if c.debug {
					log.Printf("Relay query interrupted: %v", re.err)
				}
				continue
			}

			// LOG-03/D-15: markRelayDead emits the single dead-state log line.
			// Do NOT emit a WARN here before calling markRelayDead.
			// CR-02: markRelayDead is called only here (single-threaded dispatcher) and
			// in ReconnectRelays (main loop) — never from per-relay goroutines. This
			// makes c.relays mutation single-threaded, eliminating the data race.
			class := classifyRelayError(re.err)
			if class == classTransport && isUnclassified(re.err) && c.debug {
				log.Printf("Relay %s: unclassified error: %v", re.url, re.err)
			}
			c.markRelayDead(re.url, class)
		}
	}
}

// drainSubscription reads events from sub until EOSE, context cancellation, or
// connection drop. The caller is responsible for calling sub.Unsub() on return.
// Returns nil on EOSE, ctx.Err() on external cancellation, or &transportError
// when the subscription context is done (relay connection dropped).
func (c *Crawler) drainSubscription(ctx context.Context, sub *nostr.Subscription, relayURL string, eventsChan chan<- *nostr.Event) error {
	for {
		select {
		case event := <-sub.Events:
			if event != nil {
				if c.debug {
					log.Printf("Found kind 3 event from relay %s: %s, created_at: %d, pubkey: %s",
						relayURL, event.ID, event.CreatedAt, event.PubKey)
				}
				// Check for context cancellation before sending to channel to avoid blocking
				select {
				case eventsChan <- event:
					// Event sent successfully
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case <-sub.EndOfStoredEvents:
			if c.debug {
				log.Printf("EOSE received from relay %s", relayURL)
			}
			return nil
		case <-sub.Context.Done():
			err := sub.Context.Err()
			if err != nil && err != context.Canceled {
				c.logRelayError("subscription_context_error", fmt.Errorf("relay %s: %w", relayURL, err))
			}
			return &transportError{err: fmt.Errorf("relay %s: %w", relayURL, err)}
		case <-ctx.Done():
			// External cancellation (ctrl+c or timeout) - return without error logging
			if c.debug {
				log.Printf("Relay query for %s cancelled externally", relayURL)
			}
			return ctx.Err()
		}
	}
}

func (c *Crawler) queryRelay(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
	relay := rs.conn
	relayURL := rs.url

	if c.debug {
		log.Printf("Querying relay %s for %d pubkeys", relayURL, len(filter.Authors))
	}

	authors := filter.Authors

	// WR-03: hoist the probing defer BEFORE the loop so it registers exactly once
	// per queryRelay call and clears the flag on every exit path (including ctx cancel).
	// The per-iteration rs.probing.Store(true) inside the loop still sets the flag;
	// explicit rs.probing.Store(false) clears in rejection branches for immediate reads
	// by handleFilterNotice; this deferred clear is the catch-all for normal/cancel exits.
	defer rs.probing.Store(false)

	for len(authors) > 0 {
		batchCap := int(rs.filterCap.Load())
		if batchCap <= 0 {
			batchCap = 10 // safety guard
		}

		// Probe-up (D-10): after 10 successful batches, try doubling the cap.
		isProbing := false
		if rs.successStreak.Load() >= 10 {
			probe := batchCap * 2
			if probe > c.filterBatchSize {
				probe = c.filterBatchSize
			}
			if probe > batchCap {
				isProbing = true
				batchCap = probe
				rs.probing.Store(true)
			}
		}

		chunk := authors
		if len(authors) > batchCap {
			chunk = authors[:batchCap]
		}
		authors = authors[len(chunk):]

		chunkFilter := filter
		chunkFilter.Authors = chunk

		// HANG-02: go-nostr's Subscription.Fire() (subscription.go:187) blocks on a
		// bare channel receive over the relay write queue and ignores the context
		// passed to relay.Subscribe. Wrap Subscribe in a child goroutine and select
		// on the result vs ctx.Done() so queryRelay returns promptly on timeout even
		// when Fire() is wedged by a half-open TCP connection.
		//
		// The result channel is buffered (size 1) so the abandoned child goroutine can
		// send its result and exit once Subscribe eventually unblocks (e.g. when the
		// dispatcher closes the connection on timeout — HANG-03). Without this buffer
		// the goroutine would block on the send forever.
		//
		// CR-02: the buffered send prevents the child from blocking on its own send, but
		// it does NOT bound a subscription leak on the ctx.Done() abandonment path. If
		// queryRelay has already returned ctx.Err() (the parent stopped consuming
		// subResultCh) and relay.Subscribe then succeeds — the connection was merely
		// slow, not dead — the returned *nostr.Subscription would sit unconsumed in the
		// buffer and never be Unsub()'d, leaking a live subscription on the
		// quorum-cancel path (which never closes the connection via markRelayDead).
		//
		// When the parent abandons the call on ctx.Done() it hands ownership of the
		// (still pending) result to a short cleanup goroutine: that goroutine blocks on
		// the buffered channel until the Subscribe child eventually delivers, then
		// Unsub()s any subscription it obtained. Handing cleanup to a dedicated reader —
		// rather than a flag the child re-checks — avoids a TOCTOU race where the child
		// could read the flag before the parent sets it and skip the unsub. The cleanup
		// goroutine's lifetime is bounded by the Subscribe child completing (which the
		// buffered send guarantees it eventually will, even on a wedged Fire(), once the
		// connection is closed).
		subscribeStart := time.Now()
		type subscribeResult struct {
			sub *nostr.Subscription
			err error
		}
		subResultCh := make(chan subscribeResult, 1)
		go func() {
			s, e := relay.Subscribe(ctx, []nostr.Filter{chunkFilter})
			subResultCh <- subscribeResult{sub: s, err: e}
		}()

		var sub *nostr.Subscription
		var err error
		select {
		case res := <-subResultCh:
			sub, err = res.sub, res.err
		case <-ctx.Done():
			// relayQueryContext expired while Subscribe was blocked (e.g. Fire() parked
			// on a half-open TCP write queue). Spawn a cleanup goroutine that owns the
			// abandoned result so a slow-but-successful Subscribe does not leak a live
			// Subscription (the quorum-cancel path never closes the connection via
			// markRelayDead), then return ctx.Err() immediately.
			go func() {
				res := <-subResultCh
				if res.sub != nil {
					res.sub.Unsub()
				}
			}()
			return ctx.Err()
		}

		if err != nil {
			// Skip logging for context cancellation, as it's expected during graceful shutdown
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Connection-drop-on-REQ attribution (D-09): if the drop happened within
			// 500ms of Subscribe, treat as a filter-rejection and halve the cap.
			if time.Since(subscribeStart) < 500*time.Millisecond &&
				(strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write")) {
				return c.handleCapRejection(rs, relayURL, batchCap, isProbing)
			}
			// Strip the verbose filter dump that go-nostr embeds in Subscribe errors
			cleanErr := cleanSubscribeError(err)
			// "not connected" / "failed to write" means the underlying websocket died —
			// treat as transport failure so the relay gets marked dead and queued for reconnection
			if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write") {
				c.logRelayError("connection_lost", fmt.Errorf("relay %s: %s", relayURL, cleanErr))
				return &transportError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}
			}
			c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %s", relayURL, cleanErr))
			return &subscriptionError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}
		}

		if err := c.drainSubscription(ctx, sub, relayURL, eventsChan); err != nil {
			sub.Unsub()
			return err
		}
		sub.Unsub()

		// D-10/D-14: probe succeeded — update cap and log.
		if isProbing && batchCap > int(rs.filterCap.Load()) {
			rs.filterCap.Store(int32(batchCap))
			rs.successStreak.Store(0)
			log.Printf("Relay %s: probe-up to %d succeeded, new cap", relayURL, batchCap)
			isProbing = false
		}
	}
	return nil
}

func (c *Crawler) updateFollowsFromEvent(ctx context.Context, event *nostr.Event) error {
	// Parse follows from p tags
	var rawFollows []string
	followsMap := make(map[string]struct{})

	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			pubkey := tag[1]

			// Validate pubkey format: must be exactly 64 lowercase hex chars.
			if err := dgraph.ValidatePubkey(pubkey); err != nil {
				if c.debug {
					log.Printf("Invalid pubkey in follow list: %s, error: %v", pubkey, err)
				}
				continue
			}

			rawFollows = append(rawFollows, pubkey)
			followsMap[pubkey] = struct{}{}
		}
	}

	uniqueFollowsCount := len(followsMap)
	duplicatesCount := len(rawFollows) - uniqueFollowsCount

	if c.debug {
		log.Printf("Found %d follows in event (%d unique, %d duplicates)", len(rawFollows), uniqueFollowsCount, duplicatesCount)
	}

	// Warn if this is an unusually large follow list
	if uniqueFollowsCount > 1000 && c.debug {
		log.Printf("WARN: Large follow list detected (%d follows) for pubkey %s - this may cause timeouts", uniqueFollowsCount, event.PubKey)
	}

	// Single write path regardless of follow-list size: AddFollowers batches
	// internally to stay under the gRPC cap (see pkg/dgraph).
	var err error
	err = c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, c.debug)

	if err != nil {
		return fmt.Errorf("failed to add follows: %w", err)
	}

	if c.debug {
		log.Printf("Processed %d follows for pubkey %s", uniqueFollowsCount, event.PubKey)
		c.logMetrics(event.PubKey, uniqueFollowsCount, duplicatesCount)
	}

	// Log metrics

	return nil
}

func (c *Crawler) logMetrics(pubkey string, followsCount int, duplicatesCount int) {
	chunked := followsCount > 500
	processingType := "batch"
	if chunked {
		processingType = "chunked"
	}

	metrics := map[string]interface{}{
		"pubkey":           pubkey,
		"follows_count":    followsCount,
		"duplicates_count": duplicatesCount,
		"chunked":          chunked,
		"processing_type":  processingType,
		"processed_at":     time.Now().Format(time.RFC3339),
		"component":        "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("METRICS: %s", string(metricsJSON))
}

func (c *Crawler) logSignatureValidationMetrics(pubkey string, valid bool) {
	// Only log signature validation metrics in debug mode
	if !c.debug {
		return
	}

	metrics := map[string]interface{}{
		"pubkey":          pubkey,
		"signature_valid": valid,
		"validated_at":    time.Now().Format(time.RFC3339),
		"component":       "web-of-trust-crawler",
		"metric_type":     "signature_validation",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("DEBUG_METRICS: %s", string(metricsJSON))
}

// cleanSubscribeError extracts the root cause from a go-nostr Subscribe error,
// stripping the verbose filter dump that go-nostr includes via %v formatting.
// e.g. "couldn't subscribe to [{kinds:[3] authors:[...]}] at wss://...: failed to write: connection closed"
// becomes "failed to write: connection closed"
func cleanSubscribeError(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "couldn't subscribe to"); idx != -1 {
		if atIdx := strings.Index(msg[idx:], "]: "); atIdx != -1 {
			return strings.TrimSpace(msg[idx+atIdx+3:])
		}
	}
	return msg
}

// handleCapRejection processes a connection-drop-on-REQ that was attributed as a
// filter-cap rejection (occurred within 500ms of Subscribe, "not connected" or
// "failed to write" error text). It halves the cap, resets the streak, clears the
// probing flag, and returns:
//   - nil if isProbing (D-11: probe rejection is not a failure event)
//   - *filterRejectionError if !isProbing and cap was above floor (at-cap rejection)
//   - *filterRejectionError if cap was already at floor (WR-01: floor-reached must
//     not return transportError)
//
// This is the testable seam for Test D (WR-05): tests call handleCapRejection
// directly with a constructed relayState to assert the returned error type and the
// relay's post-rejection state without needing a live relay Subscribe call.
func (c *Crawler) handleCapRejection(rs *relayState, relayURL string, batchCap int, isProbing bool) error {
	old := rs.filterCap.Load()
	if old > 10 {
		newVal := old / 2
		if newVal < 10 {
			newVal = 10
		}
		rs.filterCap.Store(newVal)
		rs.successStreak.Store(0)
		rs.probing.Store(false)
		if isProbing {
			log.Printf("Relay %s: probe-up to %d rejected, reverting to %d", relayURL, batchCap, newVal)
			return nil
		}
		if c.debug {
			log.Printf("Relay %s: filter rejection at cap %d, halved to %d", relayURL, old, newVal)
		}
		return &filterRejectionError{err: fmt.Errorf("relay %s: filter rejection at cap %d, halved to %d", relayURL, old, newVal)}
	}
	// filterCap is already at floor=10.
	return &filterRejectionError{err: fmt.Errorf("relay %s: filter cap floor reached", relayURL)}
}

// classifyRelayError maps a queryRelay error to the appropriate failureClass for
// markRelayDead. It drives the FetchAndUpdateFollows dispatcher and is the seam
// exercised by Task 2 tests (WR-05 — tests drive the real classification logic,
// not an inline copy).
//
// Priority: filterRejectionError → classFilterRej (threshold 3);
//
//	subscriptionError   → classSubFlap   (threshold 5);
//	transportError      → classTransport  (threshold 10);
//	unknown             → classTransport  (conservative fallback, D-07).
func classifyRelayError(err error) failureClass {
	var filterErr *filterRejectionError
	if errors.As(err, &filterErr) {
		return classFilterRej
	}
	var subErr *subscriptionError
	if errors.As(err, &subErr) {
		return classSubFlap
	}
	return classTransport // transportError or unknown
}

// isUnclassified returns true when the error is not one of the three typed relay
// error kinds — used to gate the debug log for unknown errors in the dispatcher.
func isUnclassified(err error) bool {
	var fe *filterRejectionError
	var se *subscriptionError
	var te *transportError
	return !errors.As(err, &fe) && !errors.As(err, &se) && !errors.As(err, &te)
}

// handleFilterNotice inspects a relay NOTICE message and halves the relay's
// filterCap when the notice indicates the filter is too large. The cap is
// never reduced below minCap (per D-05: floor = 10).
// D-14: per-step halving is debug-only; the caller (queryRelay) decides whether
// to call markRelayDead(classFilterRej) based on rs.probing (D-11 exemption).
func handleFilterNotice(rs *relayState, notice string, minCap int) {
	lower := strings.ToLower(notice)
	if strings.Contains(lower, "filter") && strings.Contains(lower, "too large") {
		for {
			old := rs.filterCap.Load()
			if old <= int32(minCap) {
				log.Printf("Relay %s NOTICE filter-too-large: cap already at floor %d", rs.url, minCap)
				return
			}
			newVal := old / 2
			if newVal < int32(minCap) {
				newVal = int32(minCap)
			}
			if rs.filterCap.CompareAndSwap(old, newVal) {
				rs.successStreak.Store(0)
				rs.probing.Store(false)
				// D-14: one human-readable line; the CAS succeeded so the cap changed.
				log.Printf("Relay %s: cap learned at %d (NOTICE)", rs.url, newVal)
				// D-11: probing flag cleared here; ejection decision made at queryRelay call site.
				return
			}
		}
	}
}

// logRelayError emits a structured RELAY_ERROR log line, demoted to debug-only
// per D-15 (LOG-03) so RELAY_ERROR JSON blobs do not flood production logs.
func (c *Crawler) logRelayError(errorType string, err error) {
	if !c.debug {
		return // D-15: RELAY_ERROR JSON blobs are debug-only
	}
	metrics := map[string]interface{}{
		"error_type":  errorType,
		"error":       err.Error(),
		"occurred_at": time.Now().Format(time.RFC3339),
		"component":   "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("RELAY_ERROR: %s", string(metricsJSON))
}
