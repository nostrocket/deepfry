package forwarder

import (
	"testing"
	"time"

	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/testutil"
)

func TestTelemetrySink_StartStopAndEmit(t *testing.T) {
	cap := testutil.NewCapturingPublisher()
	sink := NewTelemetrySink(cap)
	sink.Start()
	defer sink.Stop()

	sink.EmitConnection("wss://src", true)
	sink.EmitEventReceived("wss://src", 1, "id")
	sink.EmitEventForwarded("wss://dst", 1, 5*time.Millisecond)
	sink.EmitError(nil, "unit", telemetry.ErrorSeverityInfo)
	sink.EmitSyncProgress(10, 20)
	sink.EmitModeChanged("windowed", "test")

	// Drain
	time.Sleep(20 * time.Millisecond)
	got := cap.Snapshot()
	if len(got) < 6 {
		t.Fatalf("expected at least 6 telemetry events, got %d", len(got))
	}
	// Spot check a couple types
	foundConn := false
	foundMode := false
	for _, e := range got {
		switch e.EventType() {
		case "connection_status_changed":
			foundConn = true
		case "sync_mode_changed":
			foundMode = true
		}
	}
	if !foundConn || !foundMode {
		t.Fatalf("missing expected telemetry types: conn=%v mode=%v", foundConn, foundMode)
	}
}
