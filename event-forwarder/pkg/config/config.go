package config

import (
	"fmt"
	"os"
	"strconv"

	"event-forwarder/pkg/crypto"
)

type Config struct {
	SourceRelayURL  string
	DeepFryRelayURL string
	NostrSecretKey  string
	NostrPublicKey  string
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

func Load() (*Config, error) {
	cfg := &Config{
		SourceRelayURL:  getEnv("SOURCE_RELAY_URL", ""),
		DeepFryRelayURL: getEnv("DEEPFRY_RELAY_URL", ""),
		NostrSecretKey:  getEnv("NOSTR_SYNC_SECKEY", ""),
		Sync: SyncConfig{
			WindowSeconds:        getEnvInt("SYNC_WINDOW_SECONDS", 5),
			MaxBatch:             getEnvInt("SYNC_MAX_BATCH", 1000),
			MaxCatchupLagSeconds: getEnvInt("SYNC_MAX_CATCHUP_LAG_SECONDS", 10),
		},
		Network: NetworkConfig{
			InitialBackoffSeconds: getEnvInt("NETWORK_INITIAL_BACKOFF_SECONDS", 1),
			MaxBackoffSeconds:     getEnvInt("NETWORK_MAX_BACKOFF_SECONDS", 30),
			BackoffJitter:         getEnvFloat("NETWORK_BACKOFF_JITTER", 0.2),
		},
		Timeouts: TimeoutConfig{
			PublishSeconds:   getEnvInt("TIMEOUT_PUBLISH_SECONDS", 10),
			SubscribeSeconds: getEnvInt("TIMEOUT_SUBSCRIBE_SECONDS", 10),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	pubKey, err := crypto.DerivePublicKey(cfg.NostrSecretKey)
	if err != nil {
		return nil, err
	}
	cfg.NostrPublicKey = pubKey

	return cfg, nil
}

func (c *Config) validate() error {
	if c.SourceRelayURL == "" {
		return fmt.Errorf("SOURCE_RELAY_URL is required")
	}
	if c.DeepFryRelayURL == "" {
		return fmt.Errorf("DEEPFRY_RELAY_URL is required")
	}
	if c.NostrSecretKey == "" {
		return fmt.Errorf("NOSTR_SYNC_SECKEY is required")
	}
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}
