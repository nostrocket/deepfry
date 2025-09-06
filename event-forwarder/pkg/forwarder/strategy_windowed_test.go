package forwarder

import (
    "context"
    "testing"
    "time"

    "event-forwarder/pkg/nsync"
    "event-forwarder/pkg/testutil"
    nostr "github.com/nbd-wtf/go-nostr"
)

// Verifies initial mode telemetry and that syncWindow runs and advances the window at least once.
func TestWindowedStrategy_EmitsInitialMode_AndAdvances(t *testing.T) {
    cfg := createTestConfig()
    logger := createTestLogger()
    // Use mocks that complete quickly; provide one event to ensure publish path exercised
    src := &testutil.MockRelay{QuerySyncReturn: []*nostr.Event{{ID: "e1"}}}
    dst := &testutil.MockRelay{}
    cap := testutil.NewCapturingPublisher()

    f := NewWithRelays(cfg, logger, src, dst, cap)
    // Start window far in the past to force sync-and-advance branch
    duration := time.Duration(cfg.Sync.WindowSeconds) * time.Second
    start := nsync.Window{From: time.Now().UTC().Add(-30 * time.Minute).Truncate(duration), To: time.Now().UTC().Add(-30 * time.Minute).Truncate(duration).Add(duration)}

    strat := NewWindowedStrategy(f, start)

    // Run briefly and then cancel to stop after at least one iteration
    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    _ = strat.Run(ctx)

    // Initial mode should be emitted once
    foundInitialMode := false
    for _, e := range cap.Snapshot() {
        if e.EventType() == "sync_mode_changed" {
            foundInitialMode = true
            break
        }
    }
    if !foundInitialMode {
        t.Fatalf("expected initial sync_mode_changed telemetry to be emitted")
    }

    // Current window should be updated on the forwarder (advanced at least once)
    if f.currentWindow == nil {
        t.Fatalf("expected forwarder.currentWindow to be set")
    }
    if !f.currentWindow.From.After(start.From) {
        t.Fatalf("expected window to have advanced; got from=%v start.from=%v", f.currentWindow.From, start.From)
    }
}
