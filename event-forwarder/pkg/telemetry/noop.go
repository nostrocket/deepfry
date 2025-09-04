package telemetry

// NoopPublisher is a telemetry publisher that does nothing
// Useful for testing or when telemetry is disabled
type NoopPublisher struct{}

// NewNoopPublisher creates a new no-op telemetry publisher
func NewNoopPublisher() *NoopPublisher {
	return &NoopPublisher{}
}

// Publish does nothing
func (n *NoopPublisher) Publish(event TelemetryEvent) {
	// No-op
}
