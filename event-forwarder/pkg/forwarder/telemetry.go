package forwarder

import (
	"context"
	"time"

	"event-forwarder/pkg/telemetry"
)

// telemetrySinkImpl is a buffered adapter around TelemetryPublisher that provides
// typed emit helpers and manages its own publishing goroutine.
type telemetrySinkImpl struct {
	pub    telemetry.TelemetryPublisher
	ch     chan telemetry.TelemetryEvent
	ctx    context.Context
	cancel context.CancelFunc
}

// NewTelemetrySink constructs a TelemetrySink for the provided publisher.
func NewTelemetrySink(pub telemetry.TelemetryPublisher) TelemetrySink {
	return &telemetrySinkImpl{
		pub: pub,
		ch:  make(chan telemetry.TelemetryEvent, 200),
	}
}

func (t *telemetrySinkImpl) Start() {
	if t.pub == nil || t.ctx != nil {
		return // nothing to do or already started
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case ev := <-t.ch:
				// Best-effort publish; publisher is expected to be non-blocking
				t.pub.Publish(ev)
			case <-t.ctx.Done():
				return
			}
		}
	}()
}

func (t *telemetrySinkImpl) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *telemetrySinkImpl) EmitRaw(event telemetry.TelemetryEvent) {
	if t == nil {
		return
	}
	select {
	case t.ch <- event:
	default:
		// drop on full to avoid blocking
	}
}

func (t *telemetrySinkImpl) EmitConnection(relayName string, connected bool) {
	t.EmitRaw(telemetry.NewConnectionStatusChanged(relayName, connected))
}

func (t *telemetrySinkImpl) EmitEventReceived(relayURL string, kind int, id string) {
	t.EmitRaw(telemetry.NewEventReceived(relayURL, kind, id))
}

func (t *telemetrySinkImpl) EmitEventForwarded(relayURL string, kind int, latency time.Duration) {
	t.EmitRaw(telemetry.NewEventForwarded(relayURL, kind, latency))
}

func (t *telemetrySinkImpl) EmitError(err error, where string, severity telemetry.ErrorSeverity) {
	t.EmitRaw(telemetry.NewForwarderError(err, where, severity))
}

func (t *telemetrySinkImpl) EmitSyncProgress(from, to int64) {
	t.EmitRaw(telemetry.NewSyncProgressUpdated(from, to))
}

func (t *telemetrySinkImpl) EmitModeChanged(mode, reason string) {
	t.EmitRaw(telemetry.NewSyncModeChanged(mode, reason))
}
