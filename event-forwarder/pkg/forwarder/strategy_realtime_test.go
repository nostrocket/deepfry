package forwarder

import (
    "context"
    "errors"
    "testing"
    "time"

    "event-forwarder/pkg/testutil"
    nostr "github.com/nbd-wtf/go-nostr"
)

func TestRealtimeStrategy_SubscribeAndProcessEvents(t *testing.T) {
    cfg := createTestConfig()
    logger := createTestLogger()
    // Source will emit two events via Subscribe
    evs := []*nostr.Event{{ID: "a", Kind: 1}, {ID: "b", Kind: 1}}
    src := &testutil.MockRelay{QuerySyncReturn: evs}
    dst := &testutil.MockRelay{}
    cap := testutil.NewCapturingPublisher()

    f := NewWithRelays(cfg, logger, src, dst, cap)
    strat := NewRealtimeStrategy(f)

    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()
    _ = strat.Run(ctx)

    // Expect both events forwarded
    if len(dst.PublishCalls) < len(evs) {
        t.Fatalf("expected at least %d forwarded events, got %d", len(evs), len(dst.PublishCalls))
    }
}

func TestRealtimeStrategy_SubscribeError_FallbackToWindowed(t *testing.T) {
    cfg := createTestConfig()
    logger := createTestLogger()
    src := &testutil.MockRelay{SubscribeError: errors.New("subfail")}
    dst := &testutil.MockRelay{}
    cap := testutil.NewCapturingPublisher()

    f := NewWithRelays(cfg, logger, src, dst, cap)
    strat := NewRealtimeStrategy(f)

    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    _ = strat.Run(ctx)

    // Fallback emits an error then switches modes; assert we saw any sync_mode_changed
    foundMode := false
    for _, e := range cap.Snapshot() {
        if e.EventType() == "sync_mode_changed" {
            foundMode = true
            break
        }
    }
    if !foundMode {
        t.Fatalf("expected mode change telemetry after fallback")
    }
}
