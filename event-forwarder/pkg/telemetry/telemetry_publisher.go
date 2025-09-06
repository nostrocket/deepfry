package telemetry

import "time"

type TelemetryEvent interface {
	Timestamp() time.Time // When the event occurred
	EventType() string    // For categorization/filtering
}

// Enhanced event types
type EventReceived struct {
	timestamp time.Time
	RelayURL  string
	EventKind int    // Add Nostr event kind
	EventID   string // For debugging specific events
}

func (e EventReceived) Timestamp() time.Time { return e.timestamp }
func (e EventReceived) EventType() string    { return "event_received" }

func NewEventReceived(relayURL string, eventKind int, eventID string) EventReceived {
	return EventReceived{
		timestamp: time.Now(),
		RelayURL:  relayURL,
		EventKind: eventKind,
		EventID:   eventID,
	}
}

type EventForwarded struct {
	timestamp time.Time
	RelayURL  string
	Kind      int
	Latency   time.Duration // Time from receive to forward
}

func (e EventForwarded) Timestamp() time.Time { return e.timestamp }
func (e EventForwarded) EventType() string    { return "event_forwarded" }

func NewEventForwarded(relayURL string, kind int, latency time.Duration) EventForwarded {
	return EventForwarded{
		timestamp: time.Now(),
		RelayURL:  relayURL,
		Kind:      kind,
		Latency:   latency,
	}
}

type SyncProgressUpdated struct {
	timestamp time.Time
	From      int64
	To        int64
}

func (e SyncProgressUpdated) Timestamp() time.Time { return e.timestamp }
func (e SyncProgressUpdated) EventType() string    { return "sync_progress_updated" }

func NewSyncProgressUpdated(from, to int64) SyncProgressUpdated {
	return SyncProgressUpdated{
		timestamp: time.Now(),
		From:      from,
		To:        to,
	}
}

type ConnectionStatusChanged struct {
	timestamp time.Time
	RelayURL  string
	Connected bool
}

func (e ConnectionStatusChanged) Timestamp() time.Time { return e.timestamp }
func (e ConnectionStatusChanged) EventType() string    { return "connection_status_changed" }

func NewConnectionStatusChanged(relayURL string, connected bool) ConnectionStatusChanged {
	return ConnectionStatusChanged{
		timestamp: time.Now(),
		RelayURL:  relayURL,
		Connected: connected,
	}
}

type ForwarderError struct {
	timestamp time.Time
	Err       error
	Context   string // Additional context (e.g., "relay_publish", "window_sync")
	Severity  ErrorSeverity
}

func (e ForwarderError) Timestamp() time.Time { return e.timestamp }
func (e ForwarderError) EventType() string    { return "forwarder_error" }

func NewForwarderError(err error, context string, severity ErrorSeverity) ForwarderError {
	return ForwarderError{
		timestamp: time.Now(),
		Err:       err,
		Context:   context,
		Severity:  severity,
	}
}

type SyncModeChanged struct {
	timestamp time.Time
	Mode      string // "windowed" or "realtime"
	Reason    string // Why the mode changed
}

func (e SyncModeChanged) Timestamp() time.Time { return e.timestamp }
func (e SyncModeChanged) EventType() string    { return "sync_mode_changed" }

func NewSyncModeChanged(mode, reason string) SyncModeChanged {
	return SyncModeChanged{
		timestamp: time.Now(),
		Mode:      mode,
		Reason:    reason,
	}
}

type RealtimeProgressUpdated struct {
	timestamp         time.Time
	EventsSinceUpdate int
}

func (e RealtimeProgressUpdated) Timestamp() time.Time { return e.timestamp }
func (e RealtimeProgressUpdated) EventType() string    { return "realtime_progress_updated" }

func NewRealtimeProgressUpdated(eventsSinceUpdate int) RealtimeProgressUpdated {
	return RealtimeProgressUpdated{
		timestamp:         time.Now(),
		EventsSinceUpdate: eventsSinceUpdate,
	}
}

type ErrorSeverity int

const (
	ErrorSeverityInfo ErrorSeverity = iota
	ErrorSeverityWarning
	ErrorSeverityError
	ErrorSeverityCritical
)

type TelemetryPublisher interface {
	// Publish sends a telemetry event to the aggregator.
	// This is a non-blocking, fire-and-forget call.
	Publish(event TelemetryEvent)
}
