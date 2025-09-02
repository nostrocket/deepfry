package config

import (
	"event-forwarder/pkg/crypto"
)

type Config struct {
	SourceRelayURL  string
	DeepFryRelayURL string
	NostrSecretKey  string
	NostrKeyPair    crypto.KeyPair
	Sync            SyncConfig
	Network         NetworkConfig
	Timeouts        TimeoutConfig
}

type SyncConfig struct {
	WindowSeconds        int
	MaxBatch             int
	MaxCatchupLagSeconds int
}

type NetworkConfig struct {
	InitialBackoffSeconds int
	MaxBackoffSeconds     int
	BackoffJitter         float64
}

type TimeoutConfig struct {
	PublishSeconds   int
	SubscribeSeconds int
}

// Load loads configuration from CLI flags and environment variables
// CLI flags take precedence over environment variables
func Load() (*Config, error) {
	// Parse CLI flags
	flagSource, showHelp := parseCLIFlags()

	if showHelp {
		printUsage()
		return nil, nil // Return nil to indicate help was shown
	}

	// Create resolver with precedence: CLI flags > Environment variables
	resolver := NewConfigResolver(flagSource, &EnvSource{})

	// Build configuration using resolver
	cfg := &Config{
		SourceRelayURL:  resolver.ResolveString("SOURCE_RELAY_URL", ""),
		DeepFryRelayURL: resolver.ResolveString("DEEPFRY_RELAY_URL", ""),
		NostrSecretKey:  resolver.ResolveString("NOSTR_SYNC_SECKEY", ""),
		Sync: SyncConfig{
			WindowSeconds:        resolver.ResolveInt("SYNC_WINDOW_SECONDS", 5),
			MaxBatch:             resolver.ResolveInt("SYNC_MAX_BATCH", 1000),
			MaxCatchupLagSeconds: resolver.ResolveInt("SYNC_MAX_CATCHUP_LAG_SECONDS", 10),
		},
		Network: NetworkConfig{
			InitialBackoffSeconds: resolver.ResolveInt("NETWORK_INITIAL_BACKOFF_SECONDS", 1),
			MaxBackoffSeconds:     resolver.ResolveInt("NETWORK_MAX_BACKOFF_SECONDS", 30),
			BackoffJitter:         resolver.ResolveFloat("NETWORK_BACKOFF_JITTER", 0.2),
		},
		Timeouts: TimeoutConfig{
			PublishSeconds:   resolver.ResolveInt("TIMEOUT_PUBLISH_SECONDS", 10),
			SubscribeSeconds: resolver.ResolveInt("TIMEOUT_SUBSCRIBE_SECONDS", 10),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	keyPair, err := crypto.DeriveKeyPair(cfg.NostrSecretKey)
	if err != nil {
		return nil, err
	}
	cfg.NostrKeyPair = *keyPair

	return cfg, nil
}
