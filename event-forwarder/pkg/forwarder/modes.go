package forwarder

import (
	"time"

	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/telemetry"
)

// shouldSwitchToRealtime determines if we should switch from windowed to real-time mode
func (f *Forwarder) shouldSwitchToRealtime(window nsync.Window) bool {
	now := time.Now().UTC()
	tolerance := time.Duration(RealtimeToleranceSeconds) * time.Second

	// Only switch to real-time if window.To >= (now - small tolerance)
	// This matches the user requirement: "when window.To >= time.Now() with sensible tolerance"
	return window.To.After(now.Add(-tolerance)) || window.To.After(now) || window.To.Equal(now)
}

// switchToRealtimeMode changes the sync mode to real-time and emits telemetry
func (f *Forwarder) switchToRealtimeMode(reason string) {
	f.currentSyncMode = SyncModeRealtime
	f.eventsSinceUpdate = 0
	f.logger.Printf("switching to real-time sync mode: %s", reason)
	if f.tsink != nil {
		f.tsink.EmitModeChanged(SyncModeRealtime, reason)
	} else {
		f.emitTelemetry(telemetry.NewSyncModeChanged(SyncModeRealtime, reason))
	}
}

// switchToWindowedMode changes the sync mode to windowed and emits telemetry
func (f *Forwarder) switchToWindowedMode(reason string) {
	f.currentSyncMode = SyncModeWindowed
	f.eventsSinceUpdate = 0
	f.logger.Printf("switching to windowed sync mode: %s", reason)
	if f.tsink != nil {
		f.tsink.EmitModeChanged(SyncModeWindowed, reason)
	} else {
		f.emitTelemetry(telemetry.NewSyncModeChanged(SyncModeWindowed, reason))
	}
}
