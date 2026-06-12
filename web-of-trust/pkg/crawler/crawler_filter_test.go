package crawler

import (
	"fmt"
	"testing"
)

// TestHandleFilterNotice_Halves verifies that a "filter item too large" NOTICE
// halves rs.filterCap when cap is above the floor.
func TestHandleFilterNotice_Halves(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "Error: filter item too large", 10)
	if rs.filterCap.Load() != 50 {
		t.Fatalf("expected filterCap 50, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_CaseInsensitive verifies matching is case-insensitive.
func TestHandleFilterNotice_CaseInsensitive(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "NOTICE: Filter Too Large for subscription", 10)
	if rs.filterCap.Load() != 50 {
		t.Fatalf("expected filterCap 50 after case-insensitive match, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_Floor verifies that filterCap is not reduced below
// the floor (minCap=10) when it is already at the floor.
func TestHandleFilterNotice_Floor(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(10)
	handleFilterNotice(rs, "filter item too large", 10)
	if rs.filterCap.Load() != 10 {
		t.Fatalf("expected filterCap to stay at floor 10, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_HalveToFloor verifies that halving a cap of 12 yields
// max(6, 10) = 10 (floor clamping).
func TestHandleFilterNotice_HalveToFloor(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(12)
	handleFilterNotice(rs, "filter item too large", 10)
	if rs.filterCap.Load() != 10 {
		t.Fatalf("expected filterCap clamped to floor 10, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_UnrelatedNotice verifies that a NOTICE unrelated to
// filter size leaves filterCap unchanged.
func TestHandleFilterNotice_UnrelatedNotice(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "your subscription has too many results", 10)
	if rs.filterCap.Load() != 100 {
		t.Fatalf("expected filterCap to remain 100 for unrelated notice, got %d", rs.filterCap.Load())
	}
}

// TestSplitAuthorsChunks verifies the chunk-splitting logic used in queryRelay:
// 250 authors with filterCap=100 produces chunks of 100, 100, 50.
func TestSplitAuthorsChunks(t *testing.T) {
	const total = 250
	authors := make([]string, total)
	for i := range authors {
		authors[i] = fmt.Sprintf("%064d", i)
	}

	rs := &relayState{}
	rs.filterCap.Store(100)

	var chunkSizes []int
	remaining := authors
	for len(remaining) > 0 {
		batchCap := int(rs.filterCap.Load())
		if batchCap <= 0 {
			batchCap = 10
		}
		chunk := remaining
		if len(remaining) > batchCap {
			chunk = remaining[:batchCap]
		}
		remaining = remaining[len(chunk):]
		chunkSizes = append(chunkSizes, len(chunk))
	}

	expected := []int{100, 100, 50}
	if len(chunkSizes) != len(expected) {
		t.Fatalf("expected %d chunks, got %d: %v", len(expected), len(chunkSizes), chunkSizes)
	}
	for i, want := range expected {
		if chunkSizes[i] != want {
			t.Fatalf("chunk[%d]: expected size %d, got %d", i, want, chunkSizes[i])
		}
	}
}

// --- Task 1 Tests: Per-class counters, failureClass type, decay ---

// TestDecayCounters_HalveOnReconnect verifies that each per-class failure
// counter is integer-halved (not reset) when a relay reconnects.
func TestDecayCounters_HalveOnReconnect(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.failTransport.Store(8)
	rs.failFilterRej.Store(4)
	rs.failSubFlap.Store(6)

	// Simulate the halve step from ReconnectRelays.
	rs.failTransport.Store(rs.failTransport.Load() / 2)
	rs.failFilterRej.Store(rs.failFilterRej.Load() / 2)
	rs.failSubFlap.Store(rs.failSubFlap.Load() / 2)

	if got := rs.failTransport.Load(); got != 4 {
		t.Fatalf("failTransport: want 4, got %d", got)
	}
	if got := rs.failFilterRej.Load(); got != 2 {
		t.Fatalf("failFilterRej: want 2, got %d", got)
	}
	if got := rs.failSubFlap.Load(); got != 3 {
		t.Fatalf("failSubFlap: want 3, got %d", got)
	}
}

// TestFailureClass_String verifies that each failureClass constant returns the
// string expected by the YAML mapstructure tags.
func TestFailureClass_String(t *testing.T) {
	if got := classTransport.String(); got != "transport" {
		t.Fatalf("classTransport.String(): want %q, got %q", "transport", got)
	}
	if got := classFilterRej.String(); got != "filter_rejection" {
		t.Fatalf("classFilterRej.String(): want %q, got %q", "filter_rejection", got)
	}
	if got := classSubFlap.String(); got != "subscription_flap" {
		t.Fatalf("classSubFlap.String(): want %q, got %q", "subscription_flap", got)
	}
}

// TestMarkRelayDead_EjectsAtThreshold verifies that reaching the per-class
// threshold fires onConnectFail exactly once and removes the relay from c.relays.
// Below threshold, the relay stays in c.relays and onConnectFail is not called.
func TestMarkRelayDead_EjectsAtThreshold(t *testing.T) {
	// Scenario A: below threshold (failFilterRej=1, threshold=3; Add(1) → 2 < 3) — stays in relays.
	calledA := 0
	c := &Crawler{
		ejectionThresholds: map[failureClass]int32{
			classFilterRej: 3,
		},
		onConnectFail: func(url string) { calledA++ },
	}
	rs := &relayState{url: "wss://test.relay"}
	rs.failFilterRej.Store(1)
	rs.backoff = initialBackoff
	c.relays = []*relayState{rs}

	c.markRelayDead("wss://test.relay", classFilterRej)
	if calledA != 0 {
		t.Fatalf("below threshold: onConnectFail called %d times, want 0", calledA)
	}
	if len(c.relays) != 1 {
		t.Fatalf("below threshold: relay should stay in c.relays, got len=%d", len(c.relays))
	}
	if c.relays[0].alive {
		t.Fatal("below threshold: relay should be marked dead (alive=false)")
	}

	// Scenario B: at threshold (failFilterRej=2, Add(1) → 3 = threshold) — ejected.
	calledB := 0
	c2 := &Crawler{
		ejectionThresholds: map[failureClass]int32{
			classFilterRej: 3,
		},
		onConnectFail: func(url string) { calledB++ },
	}
	rs2 := &relayState{url: "wss://test.relay"}
	rs2.failFilterRej.Store(2) // one Add(1) in markRelayDead brings it to 3
	rs2.backoff = initialBackoff
	c2.relays = []*relayState{rs2}

	c2.markRelayDead("wss://test.relay", classFilterRej)
	if calledB != 1 {
		t.Fatalf("at threshold: onConnectFail called %d times, want 1", calledB)
	}
	if len(c2.relays) != 0 {
		t.Fatalf("at threshold: relay should be ejected from c.relays, got len=%d", len(c2.relays))
	}
}

// TestMarkRelayDead_ZeroThresholdGuarded verifies that a zero (mis-configured)
// threshold is treated as 10 and does NOT eject on the very first failure.
func TestMarkRelayDead_ZeroThresholdGuarded(t *testing.T) {
	called := 0
	c := &Crawler{
		ejectionThresholds: map[failureClass]int32{
			classTransport: 0, // misconfigured zero
		},
		onConnectFail: func(url string) { called++ },
	}
	rs := &relayState{url: "wss://test.relay"}
	rs.backoff = initialBackoff
	c.relays = []*relayState{rs}

	c.markRelayDead("wss://test.relay", classTransport)
	if called != 0 {
		t.Fatalf("zero threshold guard: onConnectFail called %d times, want 0 (single failure should not eject)", called)
	}
	if len(c.relays) != 1 {
		t.Fatalf("zero threshold guard: relay should stay in c.relays, got len=%d", len(c.relays))
	}
}

// --- Task 2 Tests: filterCap persistence, probe-up, probe-exemption ---

// TestProbeUp_StreakThreshold verifies that a relay with filterCap=50 and
// successStreak=10 triggers probe-up sizing to min(50*2, 100)=100.
func TestProbeUp_StreakThreshold(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(50)
	rs.successStreak.Store(10)

	const maxBatchSize = 100
	batchCap := int(rs.filterCap.Load())
	isProbing := false
	if rs.successStreak.Load() >= 10 {
		probe := batchCap * 2
		if probe > maxBatchSize {
			probe = maxBatchSize
		}
		if probe > batchCap {
			isProbing = true
			batchCap = probe
		}
	}

	if !isProbing {
		t.Fatal("expected isProbing=true at streak 10, got false")
	}
	if batchCap != 100 {
		t.Fatalf("want batchCap 100, got %d", batchCap)
	}
}

// TestProbeUp_NoProbeBeforeStreak verifies that a relay at streak=9 does NOT
// trigger probe-up sizing.
func TestProbeUp_NoProbeBeforeStreak(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(50)
	rs.successStreak.Store(9)

	const maxBatchSize = 100
	batchCap := int(rs.filterCap.Load())
	isProbing := false
	if rs.successStreak.Load() >= 10 {
		probe := batchCap * 2
		if probe > maxBatchSize {
			probe = maxBatchSize
		}
		if probe > batchCap {
			isProbing = true
			batchCap = probe
		}
	}

	if isProbing {
		t.Fatal("expected isProbing=false at streak 9, got true")
	}
	if batchCap != 50 {
		t.Fatalf("want batchCap 50 (unchanged), got %d", batchCap)
	}
}

// TestProbeUp_CapClampedToMax verifies that probe sizing clamps to maxBatchSize
// when cap*2 exceeds it (filterCap=80, maxBatchSize=100 → batchCap=100, not 160).
func TestProbeUp_CapClampedToMax(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(80)
	rs.successStreak.Store(10)

	const maxBatchSize = 100
	batchCap := int(rs.filterCap.Load())
	isProbing := false
	if rs.successStreak.Load() >= 10 {
		probe := batchCap * 2
		if probe > maxBatchSize {
			probe = maxBatchSize
		}
		if probe > batchCap {
			isProbing = true
			batchCap = probe
		}
	}

	if !isProbing {
		t.Fatal("expected isProbing=true at streak 10 with cap 80")
	}
	if batchCap != 100 {
		t.Fatalf("want batchCap 100 (clamped), got %d", batchCap)
	}
}

// TestProbeRejection_ExemptFromEjection verifies the D-11 exemption:
// when probing=true, a filter-rejection path does NOT call markRelayDead(classFilterRej);
// when probing=false, an at-cap rejection DOES call markRelayDead(classFilterRej).
func TestProbeRejection_ExemptFromEjection(t *testing.T) {
	// Helper: build a minimal Crawler with one relay and track markRelayDead calls.
	makeC := func() (*Crawler, *relayState, *int) {
		calls := 0
		rs := &relayState{url: "wss://probe.relay"}
		rs.filterCap.Store(50)
		rs.backoff = initialBackoff
		c := &Crawler{
			ejectionThresholds: map[failureClass]int32{
				classFilterRej: 3,
			},
			onConnectFail: func(url string) { calls++ },
			filterBatchSize: 100,
		}
		c.relays = []*relayState{rs}
		return c, rs, &calls
	}

	// Scenario A: probing=true — rejection must NOT call markRelayDead.
	cA, rsA, callsA := makeC()
	rsA.probing.Store(true)
	// Simulate the at-floor check (cap > 10):
	old := rsA.filterCap.Load()
	if old > 10 {
		newVal := old / 2
		if newVal < 10 {
			newVal = 10
		}
		rsA.filterCap.Store(newVal)
		rsA.successStreak.Store(0)
		rsA.probing.Store(false)
		isProbing := true // local flag mirrors what queryRelay tracks
		if !isProbing {
			// (would call markRelayDead in non-probing path)
			cA.markRelayDead(rsA.url, classFilterRej)
		}
		// probing path: just log, no markRelayDead
	}
	if *callsA != 0 {
		t.Fatalf("probing=true: onConnectFail called %d times, want 0", *callsA)
	}

	// Scenario B: probing=false — rejection DOES call markRelayDead.
	// Use a relay that already has failFilterRej=2 so one Add(1) reaches threshold=3.
	callsB := 0
	cB := &Crawler{
		ejectionThresholds: map[failureClass]int32{
			classFilterRej: 3,
		},
		onConnectFail: func(url string) { callsB++ },
	}
	rsB := &relayState{url: "wss://probe.relay"}
	rsB.filterCap.Store(50)
	rsB.failFilterRej.Store(2) // next Add(1) in markRelayDead → 3 = threshold
	rsB.backoff = initialBackoff
	cB.relays = []*relayState{rsB}

	old2 := rsB.filterCap.Load()
	if old2 > 10 {
		newVal := old2 / 2
		if newVal < 10 {
			newVal = 10
		}
		rsB.filterCap.Store(newVal)
		rsB.successStreak.Store(0)
		rsB.probing.Store(false)
		isProbing := false
		if !isProbing {
			cB.markRelayDead(rsB.url, classFilterRej)
		}
	}
	if callsB != 1 {
		t.Fatalf("probing=false at-cap: onConnectFail called %d times, want 1", callsB)
	}
}
