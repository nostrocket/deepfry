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
}

func New(cfg *config.Config, logger *log.Logger, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	f := &Forwarder{
		cfg:     cfg,
		logger:  logger,
		eventCh: make(chan telemetry.TelemetryEvent, 100), // Buffered channel
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
		cfg:          cfg,
		logger:       logger,
		sourceRelay:  sourceRelay,
		deepfryRelay: deepfryRelay,
		syncTracker:  nsync.NewSyncTracker(deepfryRelay, cfg),
		eventCh:      make(chan telemetry.TelemetryEvent, 100),
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
	windowDuration := time.Duration(f.cfg.Sync.WindowSeconds) * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we should move to next window
		if time.Now().UTC().After(currentWindow.To.Add(time.Duration(f.cfg.Sync.MaxCatchupLagSeconds) * time.Second)) {
			if err := f.syncWindow(ctx, currentWindow); err != nil {
				f.logger.Printf("error syncing window %s to %s: %v", currentWindow.From, currentWindow.To, err)
				time.Sleep(time.Second)
				continue
			}
			currentWindow = currentWindow.Next(windowDuration)
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
