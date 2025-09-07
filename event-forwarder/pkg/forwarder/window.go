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
	windowDuration := time.Duration(cfg.Sync.WindowSeconds) * time.Second
	
	// Note: We can't return an error here due to interface, but validation
	// will happen during GetOrCreate operations
	return &windowManagerImpl{
		cfg:            cfg,
		tracker:        tracker,
		windowDuration: windowDuration,
	}
}

func (w *windowManagerImpl) GetOrCreate(ctx context.Context) (*nsync.Window, error) {
	// Validate window duration configuration first
	if err := nsync.ValidateDuration(w.windowDuration); err != nil {
		return nil, fmt.Errorf("invalid window configuration (duration=%v): %w", w.windowDuration, err)
	}

	if w.cfg.Sync.StartTime != "" {
		startTime, err := time.Parse(time.RFC3339, w.cfg.Sync.StartTime)
		if err != nil {
			return nil, fmt.Errorf("invalid start time format '%s' in window manager (expected RFC3339): %w", 
				w.cfg.Sync.StartTime, err)
		}
		
		// Create window from configured start time with validation
		window, err := nsync.SafeNewWindowFromStart(startTime.UTC(), w.windowDuration)
		if err != nil {
			return nil, fmt.Errorf("failed to create window from start time: %w", err)
		}
		return &window, nil
	}

	lastWindow, err := w.tracker.GetLastWindow(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get last sync window: %w", err)
	}

	if lastWindow == nil {
		// Create initial window aligned to duration with validation
		window, err := nsync.SafeNewWindow(w.windowDuration)
		if err != nil {
			return nil, fmt.Errorf("failed to create initial window: %w", err)
		}
		return &window, nil
	}

	// Continue from last window with validation
	nextWindow, err := lastWindow.SafeNext(w.windowDuration)
	if err != nil {
		return nil, fmt.Errorf("failed to advance from last window: %w", err)
	}
	return &nextWindow, nil
}

func (w *windowManagerImpl) Advance(window nsync.Window) nsync.Window {
	// Use safe advancement - if it fails, fall back to unsafe version
	// (maintaining backward compatibility since interface doesn't return error)
	if nextWindow, err := window.SafeNext(w.windowDuration); err == nil {
		return nextWindow
	}
	
	// Fallback to original behavior if validation fails
	return window.Next(w.windowDuration)
}

func (w *windowManagerImpl) Update(ctx context.Context, window nsync.Window) error {
	// Validate window before updating
	if err := window.Validate(); err != nil {
		return fmt.Errorf("cannot update invalid window: %w", err)
	}
	
	if err := w.tracker.UpdateWindow(ctx, window); err != nil {
		return fmt.Errorf("failed to update sync window: %w", err)
	}
	return nil
}
