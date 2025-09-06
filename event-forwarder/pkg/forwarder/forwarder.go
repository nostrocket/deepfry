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

	// Telemetry adapter (keeps channels/goroutine out of orchestrator)
	tsink TelemetrySink

	// Sync mode tracking
	currentSyncMode   string
	eventsSinceUpdate int
	currentWindow     *nsync.Window
}

func New(cfg *config.Config, logger *log.Logger, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	f := &Forwarder{
		cfg:             cfg,
		logger:          logger,
		currentSyncMode: SyncModeWindowed, // Start in windowed mode
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
	f.tsink = NewTelemetrySink(publisher)
	f.tsink.Start()
}

// emitTelemetry sends an event to the internal channel (non-blocking)
func (f *Forwarder) emitTelemetry(event telemetry.TelemetryEvent) {
	if f.tsink != nil {
		f.tsink.EmitRaw(event)
	}
}

// lightweight helpers used by strategies to reduce duplication
func (f *Forwarder) emitTelemetryError(err error, where string) {
	if f.tsink != nil {
		f.tsink.EmitError(err, where, telemetry.ErrorSeverityError)
		return
	}
	f.emitTelemetry(telemetry.NewForwarderError(err, where, telemetry.ErrorSeverityError))
}
func (f *Forwarder) emitTelemetryErrorSev(err error, where string, sev telemetry.ErrorSeverity) {
	if f.tsink != nil {
		f.tsink.EmitError(err, where, sev)
		return
	}
	f.emitTelemetry(telemetry.NewForwarderError(err, where, sev))
}
func (f *Forwarder) emitTelemetryMsgSev(msg string, where string, sev telemetry.ErrorSeverity) {
	// avoid non-constant format string vet warning by not using fmt.Errorf with variable format
	if f.tsink != nil {
		f.tsink.EmitError(fmt.Errorf("%s", msg), where, sev)
		return
	}
	f.emitTelemetry(telemetry.NewForwarderError(fmt.Errorf("%s", msg), where, sev))
}
func (f *Forwarder) emitTelemetryRealtimeProgress(n int) {
	if f.tsink != nil {
		f.tsink.EmitRaw(telemetry.NewRealtimeProgressUpdated(n))
		return
	}
	f.emitTelemetry(telemetry.NewRealtimeProgressUpdated(n))
}

// emitTelemetryModeChanged emits a sync mode change event
func (f *Forwarder) emitTelemetryModeChanged(mode string, reason string) {
	if f.tsink != nil {
		f.tsink.EmitModeChanged(mode, reason)
		return
	}
	f.emitTelemetry(telemetry.NewSyncModeChanged(mode, reason))
}

// emitTelemetrySyncProgress emits sync progress update
func (f *Forwarder) emitTelemetrySyncProgress(from, to int64) {
	if f.tsink != nil {
		f.tsink.EmitSyncProgress(from, to)
		return
	}
	f.emitTelemetry(telemetry.NewSyncProgressUpdated(from, to))
}

// emitTelemetryEventReceived emits event received telemetry
func (f *Forwarder) emitTelemetryEventReceived(relayURL string, kind int, eventID string) {
	if f.tsink != nil {
		f.tsink.EmitEventReceived(relayURL, kind, eventID)
		return
	}
	f.emitTelemetry(telemetry.NewEventReceived(relayURL, kind, eventID))
}

// emitTelemetryEventForwarded emits event forwarded telemetry
func (f *Forwarder) emitTelemetryEventForwarded(relayURL string, kind int, latency time.Duration) {
	if f.tsink != nil {
		f.tsink.EmitEventForwarded(relayURL, kind, latency)
		return
	}
	f.emitTelemetry(telemetry.NewEventForwarded(relayURL, kind, latency))
}

// emitTelemetryConnectionStatus emits connection status change telemetry
func (f *Forwarder) emitTelemetryConnectionStatus(relayType string, connected bool) {
	if f.tsink != nil {
		f.tsink.EmitRaw(telemetry.NewConnectionStatusChanged(relayType, connected))
		return
	}
	f.emitTelemetry(telemetry.NewConnectionStatusChanged(relayType, connected))
}

// forwardEvent handles the complete event forwarding process with telemetry and error handling.
// Returns true if the event was successfully forwarded, false if it should be skipped/retried.
func (f *Forwarder) forwardEvent(ctx context.Context, event *nostr.Event, context string) bool {
	// Validate event
	if event == nil {
		f.logger.Printf("skipping nil event")
		f.emitTelemetryMsgSev("nil event", context+"_event_validation", telemetry.ErrorSeverityInfo)
		return false
	}

	// Record event received
	f.emitTelemetryEventReceived(f.cfg.SourceRelayURL, event.Kind, event.ID)

	// Forward event with latency measurement
	startTime := time.Now()
	if err := f.deepfryRelay.Publish(ctx, *event); err != nil {
		f.logger.Printf("failed to forward event %s: %v", event.ID, err)
		f.emitTelemetryErrorSev(err, context+"_publish", telemetry.ErrorSeverityWarning)
		return false
	}

	// Record successful forward with latency
	latency := time.Since(startTime)
	f.emitTelemetryEventForwarded(f.cfg.DeepFryRelayURL, event.Kind, latency)
	return true
}

// Close stops the telemetry publisher goroutine
func (f *Forwarder) Close() {
	if f.tsink != nil {
		f.tsink.Stop()
	}
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
		f.emitTelemetryConnectionStatus("source", false)
	}
	if f.deepfryRelay != nil {
		f.deepfryRelay.Close()
		f.emitTelemetryConnectionStatus("deepfry", false)
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
	f.emitTelemetrySyncProgress(window.From.Unix(), window.To.Unix())

	since := nostr.Timestamp(window.From.Unix())
	until := nostr.Timestamp(window.To.Unix())

	filter := nostr.Filter{
		Since: &since,
		Until: &until,
		Limit: f.cfg.Sync.MaxBatch,
	}

	eventCh, err := f.sourceRelay.QueryEvents(ctx, filter)
	if err != nil {
		f.emitTelemetryErrorSev(err, "relay_query", telemetry.ErrorSeverityWarning)
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

		if f.forwardEvent(ctx, event, "relay") {
			eventCount++
		}
		// Continue processing regardless of forward success/failure
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
		f.emitTelemetryErrorSev(updateErr, "sync_update", telemetry.ErrorSeverityWarning)
		// Force reconnect; this will panic if reconnect fails (expected by tests)
		f.forceReconnect(ctx)
		return fmt.Errorf("failed to update sync window: %w", updateErr)
	}

	f.logger.Printf("completed window sync: %d events forwarded", eventCount)
	return nil
}

// Mode helpers moved to modes.go

// realtimeLoop handles real-time event forwarding
func (f *Forwarder) realtimeLoop(ctx context.Context) error {
	strat := NewRealtimeStrategy(f)
	return strat.Run(ctx)
}

// processRealtimeEvent forwards a single event in real-time mode
func (f *Forwarder) processRealtimeEvent(ctx context.Context, event *nostr.Event) error {
	if f.forwardEvent(ctx, event, "realtime") {
		return nil
	}
	return fmt.Errorf("failed to forward event")
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
		f.emitTelemetryErrorSev(err, "realtime_window_update", telemetry.ErrorSeverityWarning)
		return fmt.Errorf("failed to update real-time window: %w", err)
	}

	f.currentWindow = &updatedWindow
	f.emitTelemetrySyncProgress(updatedWindow.From.Unix(), updatedWindow.To.Unix())
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
