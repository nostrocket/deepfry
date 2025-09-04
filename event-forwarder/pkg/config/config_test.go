package config

import (
	"event-forwarder/pkg/testutil"
	"flag"
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	t.Run("valid env vars", func(t *testing.T) {
		os.Setenv(KeySourceRelayURL, "wss://source.relay")
		os.Setenv(KeyDeepFryRelayURL, "wss://deepfry.relay")
		os.Setenv(KeyNostrSecretKey, testutil.TestSKHex)
		defer func() {
			os.Unsetenv(KeySourceRelayURL)
			os.Unsetenv(KeyDeepFryRelayURL)
			os.Unsetenv(KeyNostrSecretKey)
		}()

		// Set args to empty to avoid parsing flags
		os.Args = []string{"test"}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.SourceRelayURL != "wss://source.relay" {
			t.Errorf("expected SourceRelayURL 'wss://source.relay', got %s", cfg.SourceRelayURL)
		}
		if cfg.DeepFryRelayURL != "wss://deepfry.relay" {
			t.Errorf("expected DeepFryRelayURL 'wss://deepfry.relay', got %s", cfg.DeepFryRelayURL)
		}
		if cfg.NostrSecretKey != testutil.TestSKHex {
			t.Errorf("expected NostrSecretKey '%s', got %s", testutil.TestSKHex, cfg.NostrSecretKey)
		}

		// Test default values
		if cfg.Sync.WindowSeconds != DefaultSyncWindowSeconds {
			t.Errorf("expected WindowSeconds %d, got %d", DefaultSyncWindowSeconds, cfg.Sync.WindowSeconds)
		}
		if cfg.Network.BackoffJitter != DefaultNetworkBackoffJitter {
			t.Errorf("expected BackoffJitter %f, got %f", DefaultNetworkBackoffJitter, cfg.Network.BackoffJitter)
		}
	})

	t.Run("CLI override env vars", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Setenv(KeySourceRelayURL, "wss://env.relay")
		os.Setenv(KeyDeepFryRelayURL, "wss://deepfry.relay")
		os.Setenv(KeyNostrSecretKey, testutil.TestSKHex)
		os.Setenv(KeySyncWindowSeconds, "10")
		defer func() {
			os.Unsetenv(KeySourceRelayURL)
			os.Unsetenv(KeyDeepFryRelayURL)
			os.Unsetenv(KeyNostrSecretKey)
			os.Unsetenv(KeySyncWindowSeconds)
		}()

		// Set CLI args to override some env vars
		os.Args = []string{"test", "--source=wss://cli.relay", "--sync-window-seconds=20"}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// CLI should override env
		if cfg.SourceRelayURL != "wss://cli.relay" {
			t.Errorf("expected CLI override 'wss://cli.relay', got %s", cfg.SourceRelayURL)
		}
		if cfg.Sync.WindowSeconds != 20 {
			t.Errorf("expected CLI override WindowSeconds 20, got %d", cfg.Sync.WindowSeconds)
		}

		// Env should be used where no CLI override
		if cfg.DeepFryRelayURL != "wss://deepfry.relay" {
			t.Errorf("expected env value 'wss://deepfry.relay', got %s", cfg.DeepFryRelayURL)
		}
	})

	t.Run("missing required env vars", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Unsetenv(KeySourceRelayURL)
		os.Unsetenv(KeyDeepFryRelayURL)
		os.Unsetenv(KeyNostrSecretKey)

		os.Args = []string{"test"}

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing env vars, got nil")
		}
	})

	t.Run("validation error", func(t *testing.T) {
		// Reset flag for testing
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

		os.Setenv(KeySourceRelayURL, "wss://source.relay")
		os.Setenv(KeyDeepFryRelayURL, "") // Missing required field
		os.Setenv(KeyNostrSecretKey, testutil.TestSKHex)
		defer func() {
			os.Unsetenv(KeySourceRelayURL)
			os.Unsetenv(KeyDeepFryRelayURL)
			os.Unsetenv(KeyNostrSecretKey)
		}()

		os.Args = []string{"test"}

		_, err := Load()
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
	})
}

func TestLoadWithInvalidCrypto(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	os.Setenv(KeySourceRelayURL, "wss://source.relay")
	os.Setenv(KeyDeepFryRelayURL, "wss://deepfry.relay")
	os.Setenv(KeyNostrSecretKey, "invalid_key")
	defer func() {
		os.Unsetenv(KeySourceRelayURL)
		os.Unsetenv(KeyDeepFryRelayURL)
		os.Unsetenv(KeyNostrSecretKey)
	}()

	os.Args = []string{"test"}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid crypto key, got nil")
	}
}

func TestComplexConfigValues(t *testing.T) {
	// Save original command line args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Reset flag for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Test all configuration values with CLI override
	os.Setenv(KeySourceRelayURL, "wss://env.source.relay")
	os.Setenv(KeyDeepFryRelayURL, "wss://env.deepfry.relay")
	os.Setenv(KeyNostrSecretKey, testutil.TestSKHex)
	os.Setenv(KeySyncWindowSeconds, "10")
	os.Setenv(KeySyncMaxBatch, "500")
	os.Setenv(KeySyncMaxCatchupLagSeconds, "15")
	os.Setenv(KeyNetworkInitialBackoffSeconds, "2")
	os.Setenv(KeyNetworkMaxBackoffSeconds, "60")
	os.Setenv(KeyNetworkBackoffJitter, "0.3")
	os.Setenv(KeyTimeoutPublishSeconds, "15")
	os.Setenv(KeyTimeoutSubscribeSeconds, "20")
	defer func() {
		os.Unsetenv(KeySourceRelayURL)
		os.Unsetenv(KeyDeepFryRelayURL)
		os.Unsetenv(KeyNostrSecretKey)
		os.Unsetenv(KeySyncWindowSeconds)
		os.Unsetenv(KeySyncMaxBatch)
		os.Unsetenv(KeySyncMaxCatchupLagSeconds)
		os.Unsetenv(KeyNetworkInitialBackoffSeconds)
		os.Unsetenv(KeyNetworkMaxBackoffSeconds)
		os.Unsetenv(KeyNetworkBackoffJitter)
		os.Unsetenv(KeyTimeoutPublishSeconds)
		os.Unsetenv(KeyTimeoutSubscribeSeconds)
	}()

	// Override some values with CLI
	os.Args = []string{"test",
		"--source=wss://cli.source.relay",
		"--sync-window-seconds=25",
		"--network-backoff-jitter=0.5",
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check CLI overrides
	if cfg.SourceRelayURL != "wss://cli.source.relay" {
		t.Errorf("expected CLI override 'wss://cli.source.relay', got %s", cfg.SourceRelayURL)
	}
	if cfg.Sync.WindowSeconds != 25 {
		t.Errorf("expected CLI override WindowSeconds 25, got %d", cfg.Sync.WindowSeconds)
	}
	if cfg.Network.BackoffJitter != 0.5 {
		t.Errorf("expected CLI override BackoffJitter 0.5, got %f", cfg.Network.BackoffJitter)
	}

	// Check env values where no CLI override
	if cfg.DeepFryRelayURL != "wss://env.deepfry.relay" {
		t.Errorf("expected env value 'wss://env.deepfry.relay', got %s", cfg.DeepFryRelayURL)
	}
	if cfg.Sync.MaxBatch != 500 {
		t.Errorf("expected env value MaxBatch 500, got %d", cfg.Sync.MaxBatch)
	}
	if cfg.Sync.MaxCatchupLagSeconds != 15 {
		t.Errorf("expected env value MaxCatchupLagSeconds 15, got %d", cfg.Sync.MaxCatchupLagSeconds)
	}
	if cfg.Network.InitialBackoffSeconds != 2 {
		t.Errorf("expected env value InitialBackoffSeconds 2, got %d", cfg.Network.InitialBackoffSeconds)
	}
	if cfg.Network.MaxBackoffSeconds != 60 {
		t.Errorf("expected env value MaxBackoffSeconds 60, got %d", cfg.Network.MaxBackoffSeconds)
	}
	if cfg.Timeouts.PublishSeconds != 15 {
		t.Errorf("expected env value PublishSeconds 15, got %d", cfg.Timeouts.PublishSeconds)
	}
	if cfg.Timeouts.SubscribeSeconds != 20 {
		t.Errorf("expected env value SubscribeSeconds 20, got %d", cfg.Timeouts.SubscribeSeconds)
	}

	// Check that crypto key pair is properly derived
	if cfg.NostrKeyPair.PrivateKeyHex == "" {
		t.Error("expected non-empty PrivateKeyHex")
	}
	if cfg.NostrKeyPair.PublicKeyHex == "" {
		t.Error("expected non-empty PublicKeyHex")
	}
	if cfg.NostrKeyPair.PrivateKeyBech32 == "" {
		t.Error("expected non-empty PrivateKeyBech32")
	}
	if cfg.NostrKeyPair.PublicKeyBech32 == "" {
		t.Error("expected non-empty PublicKeyBech32")
	}
}
