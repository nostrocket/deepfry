package forwarder

import (
	"context"
	"time"

	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/relay"
	"event-forwarder/pkg/telemetry"
)

// ConnectionManager abstracts relay connection lifecycle for source and deepfry relays.
type ConnectionManager interface {
	Connect(ctx context.Context) error
	Reconnect(ctx context.Context) error
	Close()
	Source() relay.Relay
	Deepfry() relay.Relay
}

// WindowManager abstracts sync window discovery and updates.
type WindowManager interface {
	GetOrCreate(ctx context.Context) (*nsync.Window, error)
	Advance(window nsync.Window) nsync.Window
	Update(ctx context.Context, window nsync.Window) error
}

// SyncStrategy represents a sync mode runner (windowed or realtime).
type SyncStrategy interface {
	Run(ctx context.Context) error
	Mode() string
}

// TelemetrySink is a thin adapter for emitting structured telemetry.
type TelemetrySink interface {
	Start()
	Stop()
	EmitConnection(relayName string, connected bool)
	EmitEventReceived(relayURL string, kind int, id string)
	EmitEventForwarded(relayURL string, kind int, latency time.Duration)
	EmitError(err error, where string, severity telemetry.ErrorSeverity)
	EmitSyncProgress(from, to int64)
	EmitModeChanged(mode, reason string)
}
