package forwarder

import (
	"context"
	"fmt"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/nsync"
)

// windowManagerImpl implements WindowManager using nsync.SyncTracker and config.
type windowManagerImpl struct {
	cfg            *config.Config
	tracker        *nsync.SyncTracker
	windowDuration time.Duration
}

// NewWindowManager constructs a WindowManager given config and an nsync tracker.
func NewWindowManager(cfg *config.Config, tracker *nsync.SyncTracker) WindowManager {
	return &windowManagerImpl{
		cfg:            cfg,
		tracker:        tracker,
		windowDuration: time.Duration(cfg.Sync.WindowSeconds) * time.Second,
	}
}

func (w *windowManagerImpl) GetOrCreate(ctx context.Context) (*nsync.Window, error) {
	if w.cfg.Sync.StartTime != "" {
		startTime, err := time.Parse(time.RFC3339, w.cfg.Sync.StartTime)
		if err != nil {
			return nil, fmt.Errorf("invalid start time format: %w", err)
		}
		window := nsync.NewWindowFromStart(startTime.UTC(), w.windowDuration)
		return &window, nil
	}

	lastWindow, err := w.tracker.GetLastWindow(ctx)
	if err != nil {
		return nil, err
	}

	if lastWindow == nil {
		// Create initial window aligned to duration
		window := nsync.NewWindow(w.windowDuration)
		return &window, nil
	}

	// Continue from last window
	nextWindow := lastWindow.Next(w.windowDuration)
	return &nextWindow, nil
}

func (w *windowManagerImpl) Advance(window nsync.Window) nsync.Window {
	return window.Next(w.windowDuration)
}

func (w *windowManagerImpl) Update(ctx context.Context, window nsync.Window) error {
	return w.tracker.UpdateWindow(ctx, window)
}
