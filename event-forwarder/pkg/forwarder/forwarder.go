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

	// New: decoupled connection manager (backed by go-nostr)
	connMgr ConnectionManager

	// New: window manager abstraction
	winMgr WindowManager

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

	// Initialize connection manager
	f.connMgr = NewConnectionManager(cfg.SourceRelayURL, cfg.DeepFryRelayURL, f.emitTelemetry)

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

	// Initialize connection manager only with URLs, but since tests inject relays
	// we won't use connMgr in this constructor to avoid overriding injected values.

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

// lightweight helpers used by strategies to reduce duplication
func (f *Forwarder) emitTelemetryError(err error, where string) {
	f.emitTelemetry(telemetry.NewForwarderError(err, where, telemetry.ErrorSeverityError))
}
func (f *Forwarder) emitTelemetryErrorSev(err error, where string, sev telemetry.ErrorSeverity) {
	f.emitTelemetry(telemetry.NewForwarderError(err, where, sev))
}
func (f *Forwarder) emitTelemetryMsgSev(msg string, where string, sev telemetry.ErrorSeverity) {
	// avoid non-constant format string vet warning by not using fmt.Errorf with variable format
	f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("%s", msg), where, sev))
}
func (f *Forwarder) emitTelemetryRealtimeProgress(n int) {
	f.emitTelemetry(telemetry.NewRealtimeProgressUpdated(n))
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
	f.winMgr = NewWindowManager(f.cfg, f.syncTracker)

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
	// If tests injected relays, keep them and skip connMgr to preserve behavior
	if f.sourceRelay != nil && f.deepfryRelay != nil {
		return nil
	}
	// Otherwise, use the connection manager to establish connections
	if f.connMgr == nil {
		f.connMgr = NewConnectionManager(f.cfg.SourceRelayURL, f.cfg.DeepFryRelayURL, f.emitTelemetry)
	}
	if err := f.connMgr.Connect(ctx); err != nil {
		return err
	}
	f.sourceRelay = f.connMgr.Source()
	f.deepfryRelay = f.connMgr.Deepfry()
	return nil
}

func (f *Forwarder) closeRelays() {
	// Prefer connection manager when available to ensure symmetric telemetry
	if f.connMgr != nil && f.sourceRelay == f.connMgr.Source() && f.deepfryRelay == f.connMgr.Deepfry() {
		f.connMgr.Close()
		return
	}
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
	// Prefer window manager if available (set during Start). Fallback to inline logic for tests
	if f.winMgr != nil {
		return f.winMgr.GetOrCreate(ctx)
	}

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
	// Delegate to strategy to improve separation of concerns
	strat := NewWindowedStrategy(f, *startWindow)
	return strat.Run(ctx)
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
			// Do not reconnect/panic here; allow sync event to proceed (tests expect success when sync publish succeeds)
			continue
		}

		// Record successful forward with latency
		latency := time.Since(startTime)
		f.emitTelemetry(telemetry.NewEventForwarded(f.cfg.DeepFryRelayURL, event.Kind, latency))
		eventCount++
	}

	// Update sync progress (publishes sync event)
	// Update sync progress (publishes sync event) via window manager when available
	var updateErr error
	if f.winMgr != nil {
		updateErr = f.winMgr.Update(ctx, window)
	} else {
		updateErr = f.syncTracker.UpdateWindow(ctx, window)
	}
	if updateErr != nil {
		f.emitTelemetry(telemetry.NewForwarderError(updateErr, "sync_update", telemetry.ErrorSeverityWarning))
		// Force reconnect; this will panic if reconnect fails (expected by tests)
		f.forceReconnect(ctx)
		return fmt.Errorf("failed to update sync window: %w", updateErr)
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
	strat := NewRealtimeStrategy(f)
	return strat.Run(ctx)
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

	var err error
	if f.winMgr != nil {
		err = f.winMgr.Update(ctx, updatedWindow)
	} else {
		err = f.syncTracker.UpdateWindow(ctx, updatedWindow)
	}
	if err != nil {
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

// forceReconnect ensures the connection manager is initialized and performs a reconnect.
// It always uses the ConnectionManager path (ignores any injected relays) and will panic
// if reconnect fails after retries (matching attemptConnect behaviour).
func (f *Forwarder) forceReconnect(ctx context.Context) {
	if f.connMgr == nil {
		f.connMgr = NewConnectionManager(f.cfg.SourceRelayURL, f.cfg.DeepFryRelayURL, f.emitTelemetry)
	}
	// Reconnect will panic inside if attempts are exhausted
	_ = f.connMgr.Reconnect(ctx)
	f.sourceRelay = f.connMgr.Source()
	f.deepfryRelay = f.connMgr.Deepfry()
}
