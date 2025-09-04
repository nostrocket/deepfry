package forwarder

import (
	"context"
	"fmt"
	"log"
	"time"

	"event-forwarder/pkg/config"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/relay"
	"event-forwarder/pkg/telemetry"

	"github.com/nbd-wtf/go-nostr"
)

// Sync mode constants
const (
	SyncModeWindowed = "windowed"
	SyncModeRealtime = "realtime"

	// Real-time mode tolerance - if window.To is within this much of now, switch to real-time
	RealtimeToleranceSeconds = 5

	// Event count tracking for window updates
	EventsPerWindowUpdate = 250
)

type Forwarder struct {
	cfg          *config.Config
	logger       *log.Logger
	sourceRelay  relay.Relay
	deepfryRelay relay.Relay
	syncTracker  *nsync.SyncTracker

	// Internal event channel for telemetry
	eventCh         chan telemetry.TelemetryEvent
	publisherCtx    context.Context
	publisherCancel context.CancelFunc

	// Sync mode tracking
	currentSyncMode   string
	eventsSinceUpdate int
	currentWindow     *nsync.Window
}

func New(cfg *config.Config, logger *log.Logger, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	f := &Forwarder{
		cfg:             cfg,
		logger:          logger,
		eventCh:         make(chan telemetry.TelemetryEvent, 100), // Buffered channel
		currentSyncMode: SyncModeWindowed,                         // Start in windowed mode
	}

	// Start telemetry publisher if provided
	if telemetryPublisher != nil {
		f.StartTelemetryPublisher(telemetryPublisher)
	}

	return f
}

// NewWithRelays creates a new Forwarder with injected relay dependencies for testing
func NewWithRelays(cfg *config.Config, logger *log.Logger, sourceRelay, deepfryRelay relay.Relay, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	f := &Forwarder{
		cfg:             cfg,
		logger:          logger,
		sourceRelay:     sourceRelay,
		deepfryRelay:    deepfryRelay,
		syncTracker:     nsync.NewSyncTracker(deepfryRelay, cfg),
		eventCh:         make(chan telemetry.TelemetryEvent, 100),
		currentSyncMode: SyncModeWindowed, // Start in windowed mode
	}

	// Start telemetry publisher if provided
	if telemetryPublisher != nil {
		f.StartTelemetryPublisher(telemetryPublisher)
	}

	return f
}

// StartTelemetryPublisher starts a goroutine that publishes events to the telemetry publisher
func (f *Forwarder) StartTelemetryPublisher(publisher telemetry.TelemetryPublisher) {
	f.publisherCtx, f.publisherCancel = context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case event := <-f.eventCh:
				publisher.Publish(event)
			case <-f.publisherCtx.Done():
				return
			}
		}
	}()
}

// emitTelemetry sends an event to the internal channel (non-blocking)
func (f *Forwarder) emitTelemetry(event telemetry.TelemetryEvent) {
	select {
	case f.eventCh <- event:
	default:
		// Channel full, drop event to avoid blocking
	}
}

// Close stops the telemetry publisher goroutine
func (f *Forwarder) Close() {
	if f.publisherCancel != nil {
		f.publisherCancel()
	}
	close(f.eventCh)
}

func (f *Forwarder) Start(ctx context.Context) error {
	// Connect to relays
	if err := f.connectRelays(ctx); err != nil {
		return fmt.Errorf("failed to connect to relays: %w", err)
	}
	defer f.closeRelays()

	f.syncTracker = nsync.NewSyncTracker(f.deepfryRelay, f.cfg)

	// Get last sync window or create new one
	window, err := f.getOrCreateWindow(ctx)
	if err != nil {
		return fmt.Errorf("failed to get sync window: %w", err)
	}

	f.logger.Printf("starting sync from window: %s to %s", window.From, window.To)

	// Start syncing
	return f.syncLoop(ctx, window)
}

func (f *Forwarder) connectRelays(ctx context.Context) error {
	var err error

	sourceRelay, err := nostr.RelayConnect(ctx, f.cfg.SourceRelayURL)
	if err != nil {
		f.emitTelemetry(telemetry.NewConnectionStatusChanged("source", false))
		f.emitTelemetry(telemetry.NewForwarderError(err, "source_relay_connect", telemetry.ErrorSeverityError))
		return fmt.Errorf("failed to connect to source relay: %w", err)
	}
	f.sourceRelay = sourceRelay
	f.emitTelemetry(telemetry.NewConnectionStatusChanged("source", true))

	deepfryRelay, err := nostr.RelayConnect(ctx, f.cfg.DeepFryRelayURL)
	if err != nil {
		f.emitTelemetry(telemetry.NewConnectionStatusChanged("deepfry", false))
		f.emitTelemetry(telemetry.NewForwarderError(err, "deepfry_relay_connect", telemetry.ErrorSeverityError))
		return fmt.Errorf("failed to connect to deepfry relay: %w", err)
	}
	f.deepfryRelay = deepfryRelay
	f.emitTelemetry(telemetry.NewConnectionStatusChanged("deepfry", true))

	return nil
}

func (f *Forwarder) closeRelays() {
	if f.sourceRelay != nil {
		f.sourceRelay.Close()
		f.emitTelemetry(telemetry.NewConnectionStatusChanged("source", false))
	}
	if f.deepfryRelay != nil {
		f.deepfryRelay.Close()
		f.emitTelemetry(telemetry.NewConnectionStatusChanged("deepfry", false))
	}
}

func (f *Forwarder) getOrCreateWindow(ctx context.Context) (*nsync.Window, error) {

	if f.cfg.Sync.StartTime != "" {
		startTime, err := time.Parse(time.RFC3339, f.cfg.Sync.StartTime)
		if err != nil {
			return nil, fmt.Errorf("invalid start time format: %w", err)
		}
		window := nsync.NewWindowFromStart(startTime.UTC(), time.Duration(f.cfg.Sync.WindowSeconds)*time.Second)
		return &window, nil
	}

	lastWindow, err := f.syncTracker.GetLastWindow(ctx)
	if err != nil {
		return nil, err
	}

	if lastWindow == nil {
		// Create initial window
		window := nsync.NewWindow(time.Duration(f.cfg.Sync.WindowSeconds) * time.Second)
		return &window, nil
	}

	// Continue from last window
	nextWindow := lastWindow.Next(time.Duration(f.cfg.Sync.WindowSeconds) * time.Second)
	return &nextWindow, nil
}

func (f *Forwarder) syncLoop(ctx context.Context, startWindow *nsync.Window) error {
	currentWindow := *startWindow
	f.currentWindow = &currentWindow
	windowDuration := time.Duration(f.cfg.Sync.WindowSeconds) * time.Second

	// Emit initial sync mode
	f.emitTelemetry(telemetry.NewSyncModeChanged(f.currentSyncMode, "initial_mode"))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we should switch to real-time mode
		if f.currentSyncMode == SyncModeWindowed && f.shouldSwitchToRealtime(currentWindow) {
			f.switchToRealtimeMode("caught_up_to_current_time")
			return f.realtimeLoop(ctx)
		}

		// Check if we should move to next window (windowed mode)
		if f.currentSyncMode == SyncModeWindowed && time.Now().UTC().After(currentWindow.To.Add(time.Duration(f.cfg.Sync.MaxCatchupLagSeconds)*time.Second)) {
			if err := f.syncWindow(ctx, currentWindow); err != nil {
				f.logger.Printf("error syncing window %s to %s: %v", currentWindow.From, currentWindow.To, err)
				time.Sleep(time.Second)
				continue
			}
			currentWindow = currentWindow.Next(windowDuration)
			f.currentWindow = &currentWindow
		} else {
			// Wait a bit before checking again
			time.Sleep(time.Second)
		}
	}
}

func (f *Forwarder) syncWindow(ctx context.Context, window nsync.Window) error {
	f.logger.Printf("syncing window: %s to %s", window.From, window.To)

	// Emit sync progress
	f.emitTelemetry(telemetry.NewSyncProgressUpdated(window.From.Unix(), window.To.Unix()))

	since := nostr.Timestamp(window.From.Unix())
	until := nostr.Timestamp(window.To.Unix())

	filter := nostr.Filter{
		Since: &since,
		Until: &until,
		Limit: f.cfg.Sync.MaxBatch,
	}

	eventCh, err := f.sourceRelay.QueryEvents(ctx, filter)
	if err != nil {
		f.emitTelemetry(telemetry.NewForwarderError(err, "relay_query", telemetry.ErrorSeverityWarning))
		return fmt.Errorf("failed to query events: %w", err)
	}

	eventCount := 0
	f.logger.Printf("starting to forward events from window")

	// Stream events from channel and forward them
	for event := range eventCh {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if event == nil {
			f.logger.Printf("skipping nil event")
			f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("nil event"), "event_validation", telemetry.ErrorSeverityInfo))
			continue
		}

		// Record event received
		f.emitTelemetry(telemetry.NewEventReceived(f.cfg.SourceRelayURL, event.Kind, event.ID))

		startTime := time.Now()
		if err := f.deepfryRelay.Publish(ctx, *event); err != nil {
			f.logger.Printf("failed to forward event %s: %v", event.ID, err)
			f.emitTelemetry(telemetry.NewForwarderError(err, "relay_publish", telemetry.ErrorSeverityWarning))
			continue
		}

		// Record successful forward with latency
		latency := time.Since(startTime)
		f.emitTelemetry(telemetry.NewEventForwarded(f.cfg.DeepFryRelayURL, event.Kind, latency))
		eventCount++
	}

	// Update sync progress
	if err := f.syncTracker.UpdateWindow(ctx, window); err != nil {
		f.emitTelemetry(telemetry.NewForwarderError(err, "sync_update", telemetry.ErrorSeverityWarning))
		return fmt.Errorf("failed to update sync window: %w", err)
	}

	f.logger.Printf("completed window sync: %d events forwarded", eventCount)
	return nil
}

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
	f.emitTelemetry(telemetry.NewSyncModeChanged(SyncModeRealtime, reason))
}

// switchToWindowedMode changes the sync mode to windowed and emits telemetry
func (f *Forwarder) switchToWindowedMode(reason string) {
	f.currentSyncMode = SyncModeWindowed
	f.eventsSinceUpdate = 0
	f.logger.Printf("switching to windowed sync mode: %s", reason)
	f.emitTelemetry(telemetry.NewSyncModeChanged(SyncModeWindowed, reason))
}

// realtimeLoop handles real-time event forwarding
func (f *Forwarder) realtimeLoop(ctx context.Context) error {
	f.logger.Printf("starting real-time sync mode")

	// Create filter without time constraints for real-time streaming
	filter := nostr.Filter{
		Limit: f.cfg.Sync.MaxBatch,
	}

	// Use Subscribe directly for real-time streaming (doesn't close on EOSE)
	sub, err := f.sourceRelay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		f.emitTelemetry(telemetry.NewForwarderError(err, "realtime_subscribe", telemetry.ErrorSeverityError))
		// Fallback to windowed mode
		f.switchToWindowedMode("realtime_subscribe_failed")
		return f.fallbackToWindowedMode(ctx)
	}

	f.logger.Printf("real-time event stream established")

	// Stream events in real-time, ignoring EOSE
	for {
		select {
		case <-ctx.Done():
			if sub != nil {
				// Try to close gracefully, but don't panic if it fails
				func() {
					defer func() {
						if r := recover(); r != nil {
							f.logger.Printf("error closing subscription: %v", r)
						}
					}()
					sub.Close()
				}()
			}
			return ctx.Err()
		case <-sub.ClosedReason:
			// Subscription was closed by relay, this is an error in real-time mode
			f.logger.Printf("real-time subscription closed by relay, attempting to reconnect")
			f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("subscription closed by relay"), "realtime_disconnect", telemetry.ErrorSeverityWarning))

			// Try to restart the real-time loop
			time.Sleep(2 * time.Second)
			return f.realtimeLoop(ctx)
		case <-sub.EndOfStoredEvents:
			// EOSE received - ignore it in real-time mode, keep listening for new events
			f.logger.Printf("received EOSE in real-time mode, continuing to listen for new events")
			continue
		case event, ok := <-sub.Events:
			if !ok {
				// Events channel closed, attempt to reconnect
				f.logger.Printf("real-time event channel closed, attempting to reconnect")
				f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("event channel closed"), "realtime_disconnect", telemetry.ErrorSeverityWarning))

				// Try to restart the real-time loop
				time.Sleep(2 * time.Second)
				return f.realtimeLoop(ctx)
			}

			if event == nil {
				f.logger.Printf("skipping nil event in real-time mode")
				f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("nil event"), "realtime_event_validation", telemetry.ErrorSeverityInfo))
				continue
			}

			// Process real-time event
			if err := f.processRealtimeEvent(ctx, event); err != nil {
				f.logger.Printf("error processing real-time event %s: %v", event.ID, err)
				// Don't fail the entire loop for individual event errors
				continue
			}

			// Update sync window every 250 events
			f.eventsSinceUpdate++
			f.emitTelemetry(telemetry.NewRealtimeProgressUpdated(f.eventsSinceUpdate))
			if f.eventsSinceUpdate >= EventsPerWindowUpdate {
				if err := f.updateRealtimeWindow(ctx); err != nil {
					f.logger.Printf("error updating real-time window: %v", err)
					// Continue without failing - this is not critical
				}
				f.eventsSinceUpdate = 0
				f.emitTelemetry(telemetry.NewRealtimeProgressUpdated(f.eventsSinceUpdate))
			}
		}
	}
}

// processRealtimeEvent forwards a single event in real-time mode
func (f *Forwarder) processRealtimeEvent(ctx context.Context, event *nostr.Event) error {
	// Record event received
	f.emitTelemetry(telemetry.NewEventReceived(f.cfg.SourceRelayURL, event.Kind, event.ID))

	startTime := time.Now()
	if err := f.deepfryRelay.Publish(ctx, *event); err != nil {
		f.emitTelemetry(telemetry.NewForwarderError(err, "realtime_publish", telemetry.ErrorSeverityWarning))
		return fmt.Errorf("failed to forward event: %w", err)
	}

	// Record successful forward with latency
	latency := time.Since(startTime)
	f.emitTelemetry(telemetry.NewEventForwarded(f.cfg.DeepFryRelayURL, event.Kind, latency))
	return nil
}

// updateRealtimeWindow updates the sync window to reflect current progress in real-time mode
func (f *Forwarder) updateRealtimeWindow(ctx context.Context) error {
	if f.currentWindow == nil {
		return fmt.Errorf("current window is nil")
	}

	// Update window to current time (keeping same size)
	now := time.Now().UTC()
	windowDuration := time.Duration(f.cfg.Sync.WindowSeconds) * time.Second

	updatedWindow := nsync.Window{
		From: now.Add(-windowDuration),
		To:   now,
	}

	if err := f.syncTracker.UpdateWindow(ctx, updatedWindow); err != nil {
		f.emitTelemetry(telemetry.NewForwarderError(err, "realtime_window_update", telemetry.ErrorSeverityWarning))
		return fmt.Errorf("failed to update real-time window: %w", err)
	}

	f.currentWindow = &updatedWindow
	f.emitTelemetry(telemetry.NewSyncProgressUpdated(updatedWindow.From.Unix(), updatedWindow.To.Unix()))
	return nil
}

// fallbackToWindowedMode attempts to fallback to windowed sync mode when real-time fails
func (f *Forwarder) fallbackToWindowedMode(ctx context.Context) error {
	f.logger.Printf("attempting fallback to windowed sync mode")

	// Get current window or create a new one
	window, err := f.getOrCreateWindow(ctx)
	if err != nil {
		return fmt.Errorf("failed to get window for fallback: %w", err)
	}

	// Resume windowed sync
	return f.syncLoop(ctx, window)
}
