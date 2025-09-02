package nsync_test

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/testutil"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	testRelay = "ws://127.0.0.1:7777"
)

// createMockConfig creates a mock config without calling config.Load()
func createMockConfig(t *testing.T, randomSource ...bool) *config.Config {
	t.Helper()

	// Default randomSource to false if not provided
	useRandomSource := false
	if len(randomSource) > 0 {
		useRandomSource = randomSource[0]
	}

	var sourceURL string
	if useRandomSource {
		sourceURL = fmt.Sprintf("ws://random.%d", rand.Intn(900000)+1000)
	} else {
		sourceURL = testRelay
	}

	// Create crypto keypair from test key
	keyPair, err := crypto.DeriveKeyPair(testutil.TestSKHex)
	if err != nil {
		t.Fatalf("failed to derive key pair: %v", err)
	}

	return &config.Config{
		SourceRelayURL:  sourceURL,
		DeepFryRelayURL: testRelay,
		NostrSecretKey:  testutil.TestSKHex,
		NostrKeyPair:    *keyPair,
		Sync: config.SyncConfig{
			WindowSeconds:        5,
			MaxBatch:             1000,
			MaxCatchupLagSeconds: 10,
		},
		Network: config.NetworkConfig{
			InitialBackoffSeconds: 1,
			MaxBackoffSeconds:     30,
			BackoffJitter:         0.2,
		},
		Timeouts: config.TimeoutConfig{
			PublishSeconds:   10,
			SubscribeSeconds: 10,
		},
	}
}

// setupNsyncer creates a new SyncTracker with a mock config
func setupNsyncer(t *testing.T, randomSource ...bool) (context.Context, *nsync.SyncTracker) {
	t.Helper()

	cfg := createMockConfig(t, randomSource...)

	ctx := context.Background()
	nrelay, err := nostr.RelayConnect(ctx, cfg.DeepFryRelayURL)
	if err != nil {
		t.Fatalf("failed to connect to relay: %v", err)
	}

	return ctx, nsync.NewSyncTracker(nrelay, cfg)
}

func TestNsyncIntegration(t *testing.T) {
	t.Run("new relayer, no existing event", func(t *testing.T) {
		ctx, nsyncer := setupNsyncer(t, true) // randomSource = true

		window, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if window != nil {
			t.Errorf("expected no window, got: %v", window)
		}
	})

	t.Run("new relayer no existing event publish", func(t *testing.T) {
		ctx, nsyncer := setupNsyncer(t, true) // randomSource = true

		window, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if window != nil {
			t.Errorf("expected no window, got: %v", window)
		}

		err = nsyncer.UpdateWindow(ctx, nsync.NewWindow(time.Minute))
		if err != nil {
			t.Errorf("failed to update window: %v", err)
		}
	})

	t.Run("publish and recall", func(t *testing.T) {
		ctx, nsyncer := setupNsyncer(t, true) // Uses default randomSource = false

		noWindow, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if noWindow != nil {
			t.Errorf("expected no window, got: %v", noWindow)
		}

		targetWindow := nsync.NewWindow(time.Minute)

		err = nsyncer.UpdateWindow(ctx, targetWindow)
		if err != nil {
			t.Errorf("failed to update window: %v", err)
		}

		actualWindow, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if !reflect.DeepEqual(targetWindow, *actualWindow) {
			t.Errorf("expected %v got: %v", targetWindow, actualWindow)
		}
	})

	t.Run("publish multiple windows", func(t *testing.T) {
		ctx, nsyncer := setupNsyncer(t, true) // Uses default randomSource = false

		noWindow, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if noWindow != nil {
			t.Errorf("expected no window, got: %v", noWindow)
		}

		// Start with initial window and use Next() to advance
		currentWindow := nsync.NewWindow(time.Minute)
		var lastWindow nsync.Window

		// Loop to simulate publishing multiple windows with longer delay to avoid relay conflicts
		for i := 0; i < 5; i++ {
			err = nsyncer.UpdateWindow(ctx, currentWindow)
			if err != nil {
				t.Errorf("failed to update window %d: %v", i, err)
			}

			lastWindow = currentWindow
			currentWindow = currentWindow.Next(time.Minute) // Advance to next window

			time.Sleep(200 * time.Millisecond) // Increased delay to avoid "newer event" conflicts
		}

		// After loop, check the last published window (should be lastWindow)
		expectedWindow := lastWindow
		actualWindow, err := nsyncer.GetLastWindow(ctx)
		if err != nil {
			t.Errorf("failed to get last window: %v", err)
		}

		if !reflect.DeepEqual(expectedWindow, *actualWindow) {
			t.Errorf("expected %v got: %v", expectedWindow, actualWindow)
		}
	})

}
