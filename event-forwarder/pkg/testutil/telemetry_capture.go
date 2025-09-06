package testutil

import (
	"sync"

	"event-forwarder/pkg/telemetry"
)

// CapturingPublisher collects telemetry events for assertions in tests.
type CapturingPublisher struct {
	mu     sync.Mutex
	Events []telemetry.TelemetryEvent
}

func NewCapturingPublisher() *CapturingPublisher { return &CapturingPublisher{} }

func (c *CapturingPublisher) Publish(event telemetry.TelemetryEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Events = append(c.Events, event)
}

func (c *CapturingPublisher) Snapshot() []telemetry.TelemetryEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]telemetry.TelemetryEvent, len(c.Events))
	copy(out, c.Events)
	return out
}
