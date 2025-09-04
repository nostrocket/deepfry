package config

import (
	"event-forwarder/pkg/crypto"
	"time"
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
	StartTime            string // RFC3339 format
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
			StartTime:            resolver.ResolveString(KeySyncStartTime, DefaultSyncStartTime),
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

// GetStartTime returns the parsed start time or zero time if not set
func (s *SyncConfig) GetStartTime() (time.Time, error) {
	if s.StartTime == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s.StartTime)
}
