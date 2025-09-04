package telemetry

import (
	"context"
	"testing"
	"time"
)

// Mock clock for deterministic testing
type MockClock struct {
	current time.Time
}

func (m *MockClock) Now() time.Time {
	return m.current
}

func (m *MockClock) Advance(d time.Duration) {
	m.current = m.current.Add(d)
}

func TestAggregator_SyncModeTracking(t *testing.T) {
	clock := &MockClock{current: time.Unix(1640995200, 0)}
	cfg := DefaultConfig()
	aggregator := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aggregator.Start(ctx)
	defer aggregator.Stop()

	// Initial state should be windowed mode
	snapshot := aggregator.Snapshot()
	if snapshot.CurrentSyncMode != "windowed" {
		t.Errorf("expected initial sync mode to be 'windowed', got '%s'", snapshot.CurrentSyncMode)
	}

	// Publish sync mode change to realtime
	aggregator.Publish(NewSyncModeChanged("realtime", "caught_up_to_current_time"))

	// Give aggregator time to process
	time.Sleep(10 * time.Millisecond)

	// Check sync mode was updated
	snapshot = aggregator.Snapshot()
	if snapshot.CurrentSyncMode != "realtime" {
		t.Errorf("expected sync mode to be 'realtime', got '%s'", snapshot.CurrentSyncMode)
	}

	// Switch back to windowed mode
	aggregator.Publish(NewSyncModeChanged("windowed", "error_fallback"))

	// Give aggregator time to process
	time.Sleep(10 * time.Millisecond)

	// Check sync mode was updated back
	snapshot = aggregator.Snapshot()
	if snapshot.CurrentSyncMode != "windowed" {
		t.Errorf("expected sync mode to be 'windowed', got '%s'", snapshot.CurrentSyncMode)
	}
}

func TestAggregator_RealtimeProgressTracking(t *testing.T) {
	clock := &MockClock{current: time.Unix(1640995200, 0)}
	cfg := DefaultConfig()
	aggregator := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aggregator.Start(ctx)
	defer aggregator.Stop()

	// Initial state should have 0 events since update
	snapshot := aggregator.Snapshot()
	if snapshot.EventsSinceUpdate != 0 {
		t.Errorf("expected initial events since update to be 0, got %d", snapshot.EventsSinceUpdate)
	}

	// Publish real-time progress update
	aggregator.Publish(NewRealtimeProgressUpdated(125))

	// Give aggregator time to process
	time.Sleep(10 * time.Millisecond)

	// Check events since update was updated
	snapshot = aggregator.Snapshot()
	if snapshot.EventsSinceUpdate != 125 {
		t.Errorf("expected events since update to be 125, got %d", snapshot.EventsSinceUpdate)
	}

	// Update to full window (250 events, should reset to 0)
	aggregator.Publish(NewRealtimeProgressUpdated(0))

	// Give aggregator time to process
	time.Sleep(10 * time.Millisecond)

	// Check events since update was reset
	snapshot = aggregator.Snapshot()
	if snapshot.EventsSinceUpdate != 0 {
		t.Errorf("expected events since update to be reset to 0, got %d", snapshot.EventsSinceUpdate)
	}
}

func TestAggregator_EventCounting(t *testing.T) {
	clock := &MockClock{current: time.Unix(1000, 0)}
	cfg := DefaultConfig()
	agg := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg.Start(ctx)
	defer agg.Stop()

	// Send events
	agg.Publish(NewEventReceived("relay1", 1, "event1"))
	agg.Publish(NewEventForwarded("relay2", 1, 10*time.Millisecond))

	// Give some time for processing
	time.Sleep(10 * time.Millisecond)

	// Verify snapshot
	snapshot := agg.Snapshot()
	if snapshot.EventsReceived != 1 {
		t.Errorf("expected EventsReceived to be 1, got %d", snapshot.EventsReceived)
	}
	if snapshot.EventsForwarded != 1 {
		t.Errorf("expected EventsForwarded to be 1, got %d", snapshot.EventsForwarded)
	}
	if snapshot.EventsForwardedByKind[1] != 1 {
		t.Errorf("expected EventsForwardedByKind[1] to be 1, got %d", snapshot.EventsForwardedByKind[1])
	}
}

func TestAggregator_ConnectionStatus(t *testing.T) {
	clock := &MockClock{current: time.Unix(1000, 0)}
	cfg := DefaultConfig()
	agg := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg.Start(ctx)
	defer agg.Stop()

	// Test connection status changes
	agg.Publish(NewConnectionStatusChanged("source", true))
	agg.Publish(NewConnectionStatusChanged("deepfry", false))

	// Give some time for processing
	time.Sleep(10 * time.Millisecond)

	snapshot := agg.Snapshot()
	if !snapshot.SourceRelayConnected {
		t.Error("expected SourceRelayConnected to be true")
	}
	if snapshot.DeepFryRelayConnected {
		t.Error("expected DeepFryRelayConnected to be false")
	}
}

func TestAggregator_ErrorTracking(t *testing.T) {
	clock := &MockClock{current: time.Unix(1000, 0)}
	cfg := DefaultConfig()
	agg := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg.Start(ctx)
	defer agg.Stop()

	// Send error events
	err1 := NewForwarderError(context.DeadlineExceeded, "relay_timeout", ErrorSeverityWarning)
	err2 := NewForwarderError(context.Canceled, "context_cancel", ErrorSeverityError)

	agg.Publish(err1)
	agg.Publish(err2)

	// Give some time for processing
	time.Sleep(10 * time.Millisecond)

	snapshot := agg.Snapshot()
	if snapshot.ErrorsTotal != 2 {
		t.Errorf("expected ErrorsTotal to be 2, got %d", snapshot.ErrorsTotal)
	}
	if snapshot.ErrorsByType["relay_timeout"] != 1 {
		t.Errorf("expected ErrorsByType[relay_timeout] to be 1, got %d", snapshot.ErrorsByType["relay_timeout"])
	}
	if snapshot.ErrorsBySeverity[ErrorSeverityWarning] != 1 {
		t.Errorf("expected ErrorsBySeverity[Warning] to be 1, got %d", snapshot.ErrorsBySeverity[ErrorSeverityWarning])
	}
}

func TestAggregator_SyncProgress(t *testing.T) {
	clock := &MockClock{current: time.Unix(1000, 0)}
	cfg := DefaultConfig()
	agg := NewAggregator(clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agg.Start(ctx)
	defer agg.Stop()

	// Update sync progress
	from := int64(500)
	to := int64(600)
	agg.Publish(NewSyncProgressUpdated(from, to))

	// Give some time for processing
	time.Sleep(10 * time.Millisecond)

	snapshot := agg.Snapshot()
	if snapshot.SyncWindowFrom != from {
		t.Errorf("expected SyncWindowFrom to be %d, got %d", from, snapshot.SyncWindowFrom)
	}
	if snapshot.SyncWindowTo != to {
		t.Errorf("expected SyncWindowTo to be %d, got %d", to, snapshot.SyncWindowTo)
	}

	// Check sync lag calculation (current time is 1000, window ends at 600)
	expectedLag := 400.0 // 1000 - 600
	if snapshot.SyncLagSeconds != expectedLag {
		t.Errorf("expected SyncLagSeconds to be %.1f, got %.1f", expectedLag, snapshot.SyncLagSeconds)
	}
}

func TestNoopPublisher(t *testing.T) {
	noop := NewNoopPublisher()

	// Should not panic
	noop.Publish(NewEventReceived("test", 1, "test"))
	noop.Publish(NewEventForwarded("test", 1, time.Millisecond))
}

func TestEventTypes(t *testing.T) {
	testCases := []struct {
		name      string
		event     TelemetryEvent
		eventType string
	}{
		{"EventReceived", NewEventReceived("test", 1, "test"), "event_received"},
		{"EventForwarded", NewEventForwarded("test", 1, time.Millisecond), "event_forwarded"},
		{"SyncProgressUpdated", NewSyncProgressUpdated(1, 2), "sync_progress_updated"},
		{"ConnectionStatusChanged", NewConnectionStatusChanged("test", true), "connection_status_changed"},
		{"ForwarderError", NewForwarderError(context.DeadlineExceeded, "test", ErrorSeverityInfo), "forwarder_error"},
		{"SyncModeChanged", NewSyncModeChanged("realtime", "test"), "sync_mode_changed"},
		{"RealtimeProgressUpdated", NewRealtimeProgressUpdated(125), "realtime_progress_updated"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.event.EventType() != tc.eventType {
				t.Errorf("expected event type %s, got %s", tc.eventType, tc.event.EventType())
			}
			if tc.event.Timestamp().IsZero() {
				t.Error("expected non-zero timestamp")
			}
		})
	}
}
