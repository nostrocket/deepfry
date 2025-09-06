package forwarder

import (
    "context"
    "strings"
    "testing"
    "time"

    "event-forwarder/pkg/telemetry"
    "event-forwarder/pkg/testutil"
)

// This test validates that connection manager emits telemetry on failed attempts and panics after max attempts.
func TestConnectionManager_AttemptConnect_PanicsAfterRetriesAndEmits(t *testing.T) {
    cap := testutil.NewCapturingPublisher()
    emitted := func(ev telemetry.TelemetryEvent) { cap.Publish(ev) }

    // Use an invalid URL scheme so RelayConnect fails immediately without network IO.
    impl := &connectionManagerImpl{cfgSourceURL: "bad://src", cfgDeepfryURL: "bad://dst", telemetryEmit: emitted}

    // Use a short context to ensure no hangs
    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()

    // Expect panic; capture it
    didPanic := false
    func() {
        defer func() {
            if r := recover(); r != nil {
                didPanic = true
            }
        }()
        _ = impl.Connect(ctx)
    }()
    if !didPanic {
        t.Fatalf("expected panic after failed retries")
    }

    // Ensure we saw error telemetry for attempts
    got := cap.Snapshot()
    foundErr := false
    for _, e := range got {
        if e.EventType() == "forwarder_error" && strings.Contains(e.(telemetry.ForwarderError).Context, "attempt") {
            foundErr = true
            break
        }
    }
    if !foundErr {
        t.Fatalf("expected attempt error telemetry before panic; events=%#v", got)
    }
}
