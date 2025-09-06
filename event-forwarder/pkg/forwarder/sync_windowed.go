package forwarder

import (
	"context"
	"time"

	"event-forwarder/pkg/nsync"
)

// windowedStrategy implements SyncStrategy for windowed catch-up mode.
type windowedStrategy struct {
	f       *Forwarder
	window  nsync.Window
	started bool
}

func NewWindowedStrategy(f *Forwarder, start nsync.Window) SyncStrategy {
	return &windowedStrategy{f: f, window: start}
}

func (s *windowedStrategy) Mode() string { return SyncModeWindowed }

func (s *windowedStrategy) Run(ctx context.Context) error {
	f := s.f
	currentWindow := s.window
	f.currentWindow = &currentWindow
	windowDuration := time.Duration(f.cfg.Sync.WindowSeconds) * time.Second

	// Emit initial mode telemetry once
	if !s.started {
		f.emitTelemetryModeChanged(f.currentSyncMode, "initial_mode")
		s.started = true
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we should switch to real-time mode
		if f.shouldSwitchToRealtime(currentWindow) {
			f.switchToRealtimeMode("caught_up_to_current_time")
			// Delegate to realtime strategy via forwarder wrapper
			return f.realtimeLoop(ctx)
		}

		// When window fully in the past beyond lag, sync then advance
		if time.Now().UTC().After(currentWindow.To.Add(time.Duration(f.cfg.Sync.MaxCatchupLagSeconds) * time.Second)) {
			if err := f.syncWindow(ctx, currentWindow); err != nil {
				f.logger.Printf("error syncing window %s to %s: %v", currentWindow.From, currentWindow.To, err)
				time.Sleep(time.Second)
				continue
			}
			if f.winMgr != nil {
				currentWindow = f.winMgr.Advance(currentWindow)
			} else {
				currentWindow = currentWindow.Next(windowDuration)
			}
			f.currentWindow = &currentWindow
			continue
		}

		// prevent busy wait
		time.Sleep(100 * time.Millisecond)
	}
}
