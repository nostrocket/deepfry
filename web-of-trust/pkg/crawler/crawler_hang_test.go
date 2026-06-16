package crawler

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// newTestCrawler returns a minimal Crawler suitable for hang/timeout unit tests.
// It sets up ejection thresholds high enough that a single transport failure
// does not eject the relay (threshold 10), and uses the provided relayQueryTimeout
// and quorum. queryRelayFn is injected by the caller.
func newTestCrawler(relays []*relayState, timeout time.Duration, quorum float64, qfn func(context.Context, *relayState, nostr.Filter, chan<- *nostr.Event) error) *Crawler {
	return &Crawler{
		relays:       relays,
		timeout:      timeout,
		quorum:       quorum,
		queryRelayFn: qfn,
		ejectionThresholds: map[failureClass]int32{
			classTransport: 10,
			classFilterRej: 3,
			classSubFlap:   5,
		},
	}
}

// TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks is a regression test for
// the 48-minute crawler hang documented in HANG-FINDINGS.md.
//
// Root cause: go-nostr's Subscription.Fire() (subscription.go:187) blocks on a
// bare channel receive over the relay write queue and ignores the context passed
// to Relay.Subscribe. When a relay's connection is half-open, that write never
// completes, so the per-relay query goroutine never returns. Because
// FetchAndUpdateFollows gates its exit on wg.Wait()/eventsChan close (every query
// goroutine must finish), a single stuck relay wedges the whole dispatcher
// FOREVER — the 15s relay-query timeout fires but cannot unstick the goroutine.
//
// The invariant under test: FetchAndUpdateFollows must return within a small
// multiple of its own relay-query timeout (c.timeout) even when a relay query
// blocks indefinitely and ignores the relay-query context.
//
// We inject the stuck query via the queryRelayFn seam. The injected function
// blocks until an explicit release channel — it deliberately does NOT select on
// ctx, faithfully reproducing go-nostr's context-ignoring Fire().
//
// Pre-fix: this test fails (times out at the 2s budget) because the dispatcher
// waits on wg.Wait() forever. Post-fix (dispatcher returns on relay-query
// timeout instead of gating on eventsChan close): it passes in ~c.timeout.
// TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks tests partial-progress
// preservation (HANG-01). Two alive relays are queried: relay A returns nil quickly
// (simulating a successful EOSE with no kind-3 events for the queried pubkeys), while
// relay B blocks indefinitely ignoring its context. FetchAndUpdateFollows must return
// within budget AND must not block on relay B — proving that a returning relay's
// completion (and absence of hits) is preserved while a stuck relay does not stall
// the dispatcher.
//
// Note: asserting pubkeysWithEvents contains a specific pubkey would require a real
// dgClient (AddFollowers/TouchLastDBUpdate) since dgraph.Client is a concrete struct
// with no mock interface. The meaningful assertion here is that the dispatcher returns
// within budget despite relay B blocking, and that relay A's goroutine actually ran
// to completion — proving partial-progress is not blocked by a stuck peer.
func TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks(t *testing.T) {
	const relayQueryTimeout = 100 * time.Millisecond
	const returnBudget = 2 * time.Second

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	var goodRelayCalled, stuckRelayCalled atomic.Bool
	var goodRelayCompleted atomic.Bool

	queryFn := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		switch rs.url {
		case "wss://good.example":
			goodRelayCalled.Store(true)
			// Simulate a fast EOSE: return nil immediately without sending any events.
			// The partial-progress guarantee is that this relay's completion does not
			// require the stuck relay to also complete.
			goodRelayCompleted.Store(true)
			return nil
		default: // wss://stuck.example
			stuckRelayCalled.Store(true)
			// Block indefinitely, ignoring ctx — faithfully reproducing go-nostr
			// Subscription.Fire() ignoring the per-call context.
			<-release
			return nil
		}
	}

	goodRS := &relayState{url: "wss://good.example", alive: true}
	goodRS.filterCap.Store(10)
	stuckRS := &relayState{url: "wss://stuck.example", alive: true}
	stuckRS.filterCap.Store(10)

	c := newTestCrawler([]*relayState{goodRS, stuckRS}, relayQueryTimeout, 0.70, queryFn)

	// A single valid 64-hex pubkey so at least one goroutine is launched per relay.
	pubkeys := map[string]int64{strings.Repeat("b", 64): 0}

	done := make(chan struct{})
	go func() {
		_, _ = c.FetchAndUpdateFollows(context.Background(), pubkeys)
		close(done)
	}()

	select {
	case <-done:
		// Returned within budget — partial progress not blocked by stuck relay.
		if !goodRelayCalled.Load() {
			t.Fatal("good relay goroutine was never launched")
		}
		if !stuckRelayCalled.Load() {
			t.Fatal("stuck relay goroutine was never launched")
		}
		if !goodRelayCompleted.Load() {
			t.Fatal("good relay did not complete before FetchAndUpdateFollows returned")
		}
	case <-time.After(returnBudget):
		t.Fatalf("FetchAndUpdateFollows did not return within %v despite one relay returning "+
			"and a %v relay-query timeout: stuck relay is blocking partial progress. "+
			"See HANG-FINDINGS.md.", returnBudget, relayQueryTimeout)
	}
}

// TestFetchAndUpdateFollows_ClosesAndMarksStuckRelayDead tests HANG-03: when the
// relay-query timeout fires with a relay's query still outstanding, the dispatcher
// must close that relay's connection and mark it dead (alive=false, failTransport>=1)
// via markRelayDead(classTransport). The relay is NOT immediately ejected because
// ejectionThresholds[classTransport]=10 and a single timeout counts as 1.
func TestFetchAndUpdateFollows_ClosesAndMarksStuckRelayDead(t *testing.T) {
	const relayQueryTimeout = 100 * time.Millisecond
	const returnBudget = 2 * time.Second

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	var queryCalled atomic.Bool
	blockingQuery := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		queryCalled.Store(true)
		// Block indefinitely, ignoring ctx — faithfully reproducing go-nostr Fire().
		<-release
		return nil
	}

	rs := &relayState{url: "wss://stuck-dead.example", alive: true}
	rs.filterCap.Store(10)
	// conn is nil: markRelayDead's nil-conn guard (rs.conn != nil check) handles this
	// safely, and we verify the alive/failTransport effects without a live connection.

	c := newTestCrawler([]*relayState{rs}, relayQueryTimeout, 0.70, blockingQuery)

	pubkeys := map[string]int64{strings.Repeat("c", 64): 0}

	done := make(chan struct{})
	go func() {
		_, _ = c.FetchAndUpdateFollows(context.Background(), pubkeys)
		close(done)
	}()

	select {
	case <-done:
		if !queryCalled.Load() {
			t.Fatal("query goroutine was never launched — test did not exercise the stuck-relay path")
		}
		// HANG-03: the dispatcher must have called markRelayDead(classTransport) on
		// the still-outstanding relay, setting alive=false and incrementing failTransport.
		if rs.alive {
			t.Errorf("stuck relay still alive after timeout — markRelayDead was not called (HANG-03)")
		}
		if rs.failTransport.Load() < 1 {
			t.Errorf("stuck relay failTransport=%d, want >=1 — markRelayDead was not called (HANG-03)",
				rs.failTransport.Load())
		}
	case <-time.After(returnBudget):
		t.Fatalf("FetchAndUpdateFollows did not return within %v despite a %v relay-query timeout",
			returnBudget, relayQueryTimeout)
	}
}

func TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks(t *testing.T) {
	const relayQueryTimeout = 100 * time.Millisecond
	const returnBudget = 2 * time.Second

	// release unblocks the stuck query goroutine at test teardown so it does not
	// leak past the test (the real bug leaks it for the process lifetime; here we
	// clean it up explicitly once the assertion has been made).
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	var queryCalled atomic.Bool
	blockingQuery := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		queryCalled.Store(true)
		// Block, ignoring ctx — mimics go-nostr Subscription.Fire() ignoring the
		// per-call context. The relay-query timeout must NOT be what frees us.
		<-release
		return nil
	}

	rs := &relayState{url: "wss://stuck.example", alive: true}
	rs.filterCap.Store(10)

	c := &Crawler{
		relays:       []*relayState{rs},
		timeout:      relayQueryTimeout,
		quorum:       0.70,
		queryRelayFn: blockingQuery,
		ejectionThresholds: map[failureClass]int32{
			classTransport: 10,
			classFilterRej: 3,
			classSubFlap:   5,
		},
	}

	// A single valid 64-hex-char pubkey so it survives FetchAndUpdateFollows'
	// input validation and one relay query goroutine is actually launched.
	pubkeys := map[string]int64{strings.Repeat("a", 64): 0}

	done := make(chan struct{})
	go func() {
		_, _ = c.FetchAndUpdateFollows(context.Background(), pubkeys)
		close(done)
	}()

	select {
	case <-done:
		if !queryCalled.Load() {
			t.Fatal("query goroutine was never launched — test did not exercise the stuck-relay path")
		}
		// Returned within budget despite a permanently-stuck relay query. Fix is in place.
	case <-time.After(returnBudget):
		t.Fatalf("FetchAndUpdateFollows did not return within %v despite a %v relay-query timeout: "+
			"a stuck relay query wedges the dispatcher (wg.Wait never completes; eventsChan never closes). "+
			"See HANG-FINDINGS.md.", returnBudget, relayQueryTimeout)
	}
}

// TestFetchAndUpdateFollows_ClosesStuckRelayOnQuorumExit covers the WR-01 quorum-cancel
// branch (closes IN-02 coverage gap). When EOSE-quorum is reached and a relay's query is
// still outstanding, the dispatcher must close that relay's connection — to reap the
// abandoned go-nostr Subscribe child + CR-02 cleanup goroutine — but must NOT mark it dead
// or increment a failure counter (a quorum-cancelled relay is slow this batch, not failed).
//
// Setup: 4 alive relays, quorum 0.70 → ceil(0.70*4)=3 EOSEs reach quorum. Three relays
// return immediately (fast EOSE) so quorum fires (cancel) well before the 5s relay-query
// timeout; the fourth blocks ignoring its context. The stuck relay is given a real (never
// connected) *nostr.Relay as its conn so the close branch (which requires conn != nil) is
// exercised; nostr.Relay.Close() is safe on an unconnected relay (it cancels the connection
// context and returns a benign "not connected" error the dispatcher ignores).
func TestFetchAndUpdateFollows_ClosesStuckRelayOnQuorumExit(t *testing.T) {
	const relayQueryTimeout = 5 * time.Second // high on purpose: quorum must fire first, not the timeout
	const returnBudget = 2 * time.Second

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	queryFn := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		if rs.url == "wss://stuck.example" {
			<-release // block, ignoring ctx — reproduces go-nostr Subscription.Fire()
			return nil
		}
		return nil // fast EOSE (no events) — drives the quorum counter
	}

	relays := make([]*relayState, 0, 4)
	for _, u := range []string{"wss://g1.example", "wss://g2.example", "wss://g3.example"} {
		rs := &relayState{url: u, alive: true}
		rs.filterCap.Store(10)
		relays = append(relays, rs)
	}
	stuck := &relayState{
		url:   "wss://stuck.example",
		alive: true,
		conn:  nostr.NewRelay(context.Background(), "wss://stuck.example"),
	}
	stuck.filterCap.Store(10)
	relays = append(relays, stuck)

	c := newTestCrawler(relays, relayQueryTimeout, 0.70, queryFn)

	pubkeys := map[string]int64{strings.Repeat("d", 64): 0}

	done := make(chan struct{})
	go func() {
		_, _ = c.FetchAndUpdateFollows(context.Background(), pubkeys)
		close(done)
	}()

	select {
	case <-done:
		// WR-01 quorum-cancel close path: stuck relay's connection closed and marked
		// not-alive so ReconnectRelays brings it back — but NOT penalized.
		if stuck.conn != nil {
			t.Error("stuck relay conn not closed on quorum exit (WR-01) — expected nil")
		}
		if stuck.alive {
			t.Error("stuck relay still alive after quorum-exit close (WR-01)")
		}
		if got := stuck.failTransport.Load(); got != 0 {
			t.Errorf("stuck relay failTransport=%d on quorum-exit path, want 0 — "+
				"quorum-cancel must close without penalizing (WR-01)", got)
		}
	case <-time.After(returnBudget):
		t.Fatalf("FetchAndUpdateFollows did not return within %v on the quorum-exit path", returnBudget)
	}
}
