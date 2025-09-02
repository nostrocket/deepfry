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
		SourceRelayURL:  resolver.ResolveString(KeySourceRelayURL, ""),
		DeepFryRelayURL: resolver.ResolveString(KeyDeepFryRelayURL, ""),
		NostrSecretKey:  resolver.ResolveString(KeyNostrSecretKey, ""),
		Sync: SyncConfig{
			WindowSeconds:        resolver.ResolveInt(KeySyncWindowSeconds, DefaultSyncWindowSeconds),
			MaxBatch:             resolver.ResolveInt(KeySyncMaxBatch, DefaultSyncMaxBatch),
			MaxCatchupLagSeconds: resolver.ResolveInt(KeySyncMaxCatchupLagSeconds, DefaultSyncMaxCatchupLagSeconds),
		},
		Network: NetworkConfig{
			InitialBackoffSeconds: resolver.ResolveInt(KeyNetworkInitialBackoffSeconds, DefaultNetworkInitialBackoffSeconds),
			MaxBackoffSeconds:     resolver.ResolveInt(KeyNetworkMaxBackoffSeconds, DefaultNetworkMaxBackoffSeconds),
			BackoffJitter:         resolver.ResolveFloat(KeyNetworkBackoffJitter, DefaultNetworkBackoffJitter),
		},
		Timeouts: TimeoutConfig{
			PublishSeconds:   resolver.ResolveInt(KeyTimeoutPublishSeconds, DefaultTimeoutPublishSeconds),
			SubscribeSeconds: resolver.ResolveInt(KeyTimeoutSubscribeSeconds, DefaultTimeoutSubscribeSeconds),
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
