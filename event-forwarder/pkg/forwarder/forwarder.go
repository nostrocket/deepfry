package forwarder

// import (
// 	"context"
// 	"fmt"
// 	"log"
// 	"time"

// 	"event-forwarder/pkg/config"
// 	"event-forwarder/pkg/sync"

// 	"github.com/nbd-wtf/go-nostr"
// )

// type Forwarder struct {
// 	cfg          *config.Config
// 	logger       *log.Logger
// 	sourceRelay  *nostr.Relay
// 	deepfryRelay *nostr.Relay
// 	syncTracker  *sync.SyncTracker
// }

// func New(cfg *config.Config, logger *log.Logger) *Forwarder {
// 	return &Forwarder{
// 		cfg:    cfg,
// 		logger: logger,
// 	}
// }

// func (f *Forwarder) Start(ctx context.Context) error {
// 	// Connect to relays
// 	if err := f.connectRelays(ctx); err != nil {
// 		return fmt.Errorf("failed to connect to relays: %w", err)
// 	}
// 	defer f.closeRelays()

// 	f.syncTracker = sync.NewSyncTracker(f.deepfryRelay, f.cfg.NostrSecretKey, f.cfg.SourceRelayURL)

// 	// Get last sync window or create new one
// 	window, err := f.getOrCreateWindow(ctx)
// 	if err != nil {
// 		return fmt.Errorf("failed to get sync window: %w", err)
// 	}

// 	f.logger.Printf("starting sync from window: %s to %s", window.From, window.To)

// 	// Start syncing
// 	return f.syncLoop(ctx, window)
// }

// func (f *Forwarder) connectRelays(ctx context.Context) error {
// 	var err error

// 	f.sourceRelay, err = nostr.RelayConnect(ctx, f.cfg.SourceRelayURL)
// 	if err != nil {
// 		return fmt.Errorf("failed to connect to source relay: %w", err)
// 	}

// 	f.deepfryRelay, err = nostr.RelayConnect(ctx, f.cfg.DeepFryRelayURL)
// 	if err != nil {
// 		return fmt.Errorf("failed to connect to deepfry relay: %w", err)
// 	}

// 	return nil
// }

// func (f *Forwarder) closeRelays() {
// 	if f.sourceRelay != nil {
// 		f.sourceRelay.Close()
// 	}
// 	if f.deepfryRelay != nil {
// 		f.deepfryRelay.Close()
// 	}
// }

// func (f *Forwarder) getOrCreateWindow(ctx context.Context) (*sync.Window, error) {
// 	lastWindow, err := f.syncTracker.GetLastWindow(ctx)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if lastWindow == nil {
// 		// Create initial window
// 		window := sync.NewWindow(time.Duration(f.cfg.Sync.WindowSeconds) * time.Second)
// 		return &window, nil
// 	}

// 	// Continue from last window
// 	nextWindow := lastWindow.Next(time.Duration(f.cfg.Sync.WindowSeconds) * time.Second)
// 	return &nextWindow, nil
// }

// func (f *Forwarder) syncLoop(ctx context.Context, startWindow *sync.Window) error {
// 	currentWindow := *startWindow
// 	windowDuration := time.Duration(f.cfg.Sync.WindowSeconds) * time.Second

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return ctx.Err()
// 		default:
// 		}

// 		// Check if we should move to next window
// 		if time.Now().UTC().After(currentWindow.To.Add(time.Duration(f.cfg.Sync.MaxCatchupLagSeconds) * time.Second)) {
// 			if err := f.syncWindow(ctx, currentWindow); err != nil {
// 				f.logger.Printf("error syncing window %s to %s: %v", currentWindow.From, currentWindow.To, err)
// 				time.Sleep(time.Second)
// 				continue
// 			}
// 			currentWindow = currentWindow.Next(windowDuration)
// 		} else {
// 			// Wait a bit before checking again
// 			time.Sleep(time.Second)
// 		}
// 	}
// }

// func (f *Forwarder) syncWindow(ctx context.Context, window sync.Window) error {
// 	f.logger.Printf("syncing window: %s to %s", window.From, window.To)

// 	filter := nostr.Filter{
// 		Since: &nostr.Timestamp{Time: window.From},
// 		Until: &nostr.Timestamp{Time: window.To},
// 		Limit: f.cfg.Sync.MaxBatch,
// 	}

// 	events, err := f.sourceRelay.QuerySync(ctx, filter)
// 	if err != nil {
// 		return fmt.Errorf("failed to query events: %w", err)
// 	}

// 	f.logger.Printf("forwarding %d events from window", len(events))

// 	// Forward events to deepfry relay
// 	for _, event := range events {
// 		if err := f.deepfryRelay.Publish(ctx, *event); err != nil {
// 			f.logger.Printf("failed to forward event %s: %v", event.ID, err)
// 			continue
// 		}
// 	}

// 	// Update sync progress
// 	if err := f.syncTracker.UpdateWindow(ctx, window); err != nil {
// 		return fmt.Errorf("failed to update sync window: %w", err)
// 	}

// 	f.logger.Printf("completed window sync: %d events forwarded", len(events))
// 	return nil
// }
