//go:build integration
// +build integration

package forwarder

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"event-forwarder/pkg/nsync"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/testutil"
	"log"
	"os"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	sourceRelay   = "wss://relay.primal.net"
	deepfryRelay  = "ws://localhost:7777"
	testDuration  = 30 * time.Second
	windowSeconds = 10
)

// createIntegrationConfig creates a config for real relay integration tests
func createIntegrationConfig(t *testing.T) *config.Config {
	t.Helper()

	// Create crypto keypair from test key
	keyPair, err := crypto.DeriveKeyPair(testutil.TestSKHex)
	if err != nil {
		t.Fatalf("failed to derive key pair: %v", err)
	}

	return &config.Config{
		SourceRelayURL:  sourceRelay,
		DeepFryRelayURL: deepfryRelay,
		NostrSecretKey:  testutil.TestSKHex,
		NostrKeyPair:    *keyPair,
		Sync: config.SyncConfig{
			WindowSeconds:        windowSeconds,
			MaxBatch:             100, // Smaller batch for testing
			MaxCatchupLagSeconds: 5,
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

func createIntegrationTestLogger() *log.Logger {
	return log.New(os.Stdout, "[INTEGRATION] ", log.LstdFlags)
}

func TestForwarderRealRelayIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("real relay synchronization", func(t *testing.T) {
		cfg := createIntegrationConfig(t)
		logger := createIntegrationTestLogger()

		// Create forwarder (will connect to real relays)
		forwarder := New(cfg, logger, telemetry.NewNoopPublisher())

		// Create a context with timeout for the entire test
		ctx, cancel := context.WithTimeout(context.Background(), testDuration)
		defer cancel()

		t.Logf("Starting integration test with:")
		t.Logf("  Source relay: %s", cfg.SourceRelayURL)
		t.Logf("  Deepfry relay: %s", cfg.DeepFryRelayURL)
		t.Logf("  Window size: %d seconds", cfg.Sync.WindowSeconds)
		t.Logf("  Test duration: %v", testDuration)

		// Start the forwarder - this will run until context is cancelled
		err := forwarder.Start(ctx)

		// We expect context.DeadlineExceeded when the test timeout occurs
		if err != nil && err != context.DeadlineExceeded {
			t.Fatalf("unexpected error from forwarder: %v", err)
		}

		if err == context.DeadlineExceeded {
			t.Logf("Integration test completed successfully after %v", testDuration)
		} else {
			t.Logf("Forwarder completed without timeout")
		}
	})

	t.Run("relay connectivity test", func(t *testing.T) {
		cfg := createIntegrationConfig(t)

		// Test individual relay connections
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		t.Log("Testing source relay connection...")
		sourceConn, err := nostr.RelayConnect(ctx, cfg.SourceRelayURL)
		if err != nil {
			t.Fatalf("failed to connect to source relay %s: %v", cfg.SourceRelayURL, err)
		}
		defer sourceConn.Close()
		t.Logf("✅ Successfully connected to source relay: %s", cfg.SourceRelayURL)

		t.Log("Testing deepfry relay connection...")
		deepfryConn, err := nostr.RelayConnect(ctx, cfg.DeepFryRelayURL)
		if err != nil {
			t.Fatalf("failed to connect to deepfry relay %s: %v", cfg.DeepFryRelayURL, err)
		}
		defer deepfryConn.Close()
		t.Logf("✅ Successfully connected to deepfry relay: %s", cfg.DeepFryRelayURL)

		// Test a simple query to source relay
		t.Log("Testing source relay query...")
		since := nostr.Timestamp(time.Now().Add(-1 * time.Hour).Unix())
		until := nostr.Timestamp(time.Now().Unix())

		filter := nostr.Filter{
			Since: &since,
			Until: &until,
			Limit: 5, // Just get a few events
		}

		events, err := sourceConn.QuerySync(ctx, filter)
		if err != nil {
			t.Logf("⚠️  Query failed (this may be normal): %v", err)
		} else {
			t.Logf("✅ Successfully queried source relay, got %d events", len(events))
		}
	})

	t.Run("single window sync test", func(t *testing.T) {
		cfg := createIntegrationConfig(t)
		logger := createIntegrationTestLogger()

		// Create forwarder with real relays
		forwarder := New(cfg, logger, telemetry.NewNoopPublisher())

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Connect to relays
		err := forwarder.connectRelays(ctx)
		if err != nil {
			t.Fatalf("failed to connect to relays: %v", err)
		}
		defer forwarder.closeRelays()

		// Initialize sync tracker
		forwarder.syncTracker = nsync.NewSyncTracker(forwarder.deepfryRelay, cfg)

		// Get or create a window
		window, err := forwarder.getOrCreateWindow(ctx)
		if err != nil {
			t.Fatalf("failed to get or create window: %v", err)
		}

		t.Logf("Syncing window: %s to %s", window.From, window.To)

		// Sync a single window
		err = forwarder.syncWindow(ctx, *window)
		if err != nil {
			t.Fatalf("failed to sync window: %v", err)
		}

		t.Log("✅ Single window sync completed successfully")
	})
}
