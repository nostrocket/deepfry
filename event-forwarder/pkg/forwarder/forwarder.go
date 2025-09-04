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
	telemetry    telemetry.TelemetryPublisher
}

func New(cfg *config.Config, logger *log.Logger, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	return &Forwarder{
		cfg:       cfg,
		logger:    logger,
		telemetry: telemetryPublisher,
	}
}

// NewWithRelays creates a new Forwarder with injected relay dependencies for testing
func NewWithRelays(cfg *config.Config, logger *log.Logger, sourceRelay, deepfryRelay relay.Relay, telemetryPublisher telemetry.TelemetryPublisher) *Forwarder {
	return &Forwarder{
		cfg:          cfg,
		logger:       logger,
		sourceRelay:  sourceRelay,
		deepfryRelay: deepfryRelay,
		syncTracker:  nsync.NewSyncTracker(deepfryRelay, cfg),
		telemetry:    telemetryPublisher,
	}
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
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewConnectionStatusChanged("source", false))
			f.telemetry.Publish(telemetry.NewForwarderError(err, "source_relay_connect", telemetry.ErrorSeverityError))
		}
		return fmt.Errorf("failed to connect to source relay: %w", err)
	}
	f.sourceRelay = sourceRelay
	if f.telemetry != nil {
		f.telemetry.Publish(telemetry.NewConnectionStatusChanged("source", true))
	}

	deepfryRelay, err := nostr.RelayConnect(ctx, f.cfg.DeepFryRelayURL)
	if err != nil {
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewConnectionStatusChanged("deepfry", false))
			f.telemetry.Publish(telemetry.NewForwarderError(err, "deepfry_relay_connect", telemetry.ErrorSeverityError))
		}
		return fmt.Errorf("failed to connect to deepfry relay: %w", err)
	}
	f.deepfryRelay = deepfryRelay
	if f.telemetry != nil {
		f.telemetry.Publish(telemetry.NewConnectionStatusChanged("deepfry", true))
	}

	return nil
}

func (f *Forwarder) closeRelays() {
	if f.sourceRelay != nil {
		f.sourceRelay.Close()
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewConnectionStatusChanged("source", false))
		}
	}
	if f.deepfryRelay != nil {
		f.deepfryRelay.Close()
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewConnectionStatusChanged("deepfry", false))
		}
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
	if f.telemetry != nil {
		f.telemetry.Publish(telemetry.NewSyncProgressUpdated(window.From.Unix(), window.To.Unix()))
	}

	since := nostr.Timestamp(window.From.Unix())
	until := nostr.Timestamp(window.To.Unix())

	filter := nostr.Filter{
		Since: &since,
		Until: &until,
		Limit: f.cfg.Sync.MaxBatch,
	}

	events, err := f.sourceRelay.QuerySync(ctx, filter)
	if err != nil {
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewForwarderError(err, "relay_query", telemetry.ErrorSeverityWarning))
		}
		return fmt.Errorf("failed to query events: %w", err)
	}

	f.logger.Printf("forwarding %d events from window", len(events))

	// Forward events to deepfry relay
	for _, event := range events {
		if event == nil {
			f.logger.Printf("skipping nil event")
			if f.telemetry != nil {
				f.telemetry.Publish(telemetry.NewForwarderError(fmt.Errorf("nil event"), "event_validation", telemetry.ErrorSeverityInfo))
			}
			continue
		}

		// Record event received
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewEventReceived(f.cfg.SourceRelayURL, event.Kind, event.ID))
		}

		startTime := time.Now()
		if err := f.deepfryRelay.Publish(ctx, *event); err != nil {
			f.logger.Printf("failed to forward event %s: %v", event.ID, err)
			if f.telemetry != nil {
				f.telemetry.Publish(telemetry.NewForwarderError(err, "relay_publish", telemetry.ErrorSeverityWarning))
			}
			continue
		}

		// Record successful forward with latency
		if f.telemetry != nil {
			latency := time.Since(startTime)
			f.telemetry.Publish(telemetry.NewEventForwarded(f.cfg.DeepFryRelayURL, event.Kind, latency))
		}
	}

	// Update sync progress
	if err := f.syncTracker.UpdateWindow(ctx, window); err != nil {
		if f.telemetry != nil {
			f.telemetry.Publish(telemetry.NewForwarderError(err, "sync_update", telemetry.ErrorSeverityWarning))
		}
		return fmt.Errorf("failed to update sync window: %w", err)
	}

	f.logger.Printf("completed window sync: %d events forwarded", len(events))
	return nil
}
