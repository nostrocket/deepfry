package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	RelayURLs            []string      `mapstructure:"relay_urls"`
	DgraphAddr           string        `mapstructure:"dgraph_addr"`
	SeedPubkey           string        `mapstructure:"pubkey"`
	Timeout              time.Duration `mapstructure:"timeout"`
	Debug                bool          `mapstructure:"debug"`
	StalePubkeyThreshold int64         `mapstructure:"stale_pubkey_threshold"`
	ForwardRelayURL      string        `mapstructure:"forward_relay_url"`
}

// LoadConfig loads the application configuration from various sources
func LoadConfig() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, "deepfry")
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}

	viper.SetConfigName("web-of-trust")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configDir)

	// Set defaults with popular Nostr relays
	viper.SetDefault("relay_urls", []string{
		"wss://relay.damus.io",
		"wss://nos.lol",
		"wss://relay.nostr.band",
		"wss://nostr-pub.wellorder.net",
		"wss://relay.primal.net",
	})
	viper.SetDefault("dgraph_addr", "localhost:9080")
	viper.SetDefault("timeout", "30s")
	viper.SetDefault("debug", false)
	viper.SetDefault("pubkey", "npub1mygerccwqpzyh9pvp6pv44rskv40zutkfs38t0hqhkvnwlhagp6s3psn5p")
	viper.SetDefault("stale_pubkey_threshold", 24*60*60) // 24 hours in seconds

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		log.Printf("No config file found, using defaults and flags")

		configPath := filepath.Join(configDir, "web-of-trust.yaml")
		// viper.SafeWriteConfigAs does not write SetDefault values,
		// so promote them to explicit values before writing.
		for _, key := range viper.AllKeys() {
			viper.Set(key, viper.Get(key))
		}
		if err := viper.SafeWriteConfigAs(configPath); err != nil {
			log.Printf("Warning: Failed to write default config to %s: %v", configPath, err)
		} else {
			log.Printf("Created default configuration file at %s", configPath)
		}
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// Ensure at least one relay URL is provided
	if len(cfg.RelayURLs) == 0 {
		return nil, fmt.Errorf("at least one relay URL is required")
	}

	// Handle both hex and npub formats
	if _, data, err := nip19.Decode(cfg.SeedPubkey); err == nil {
		cfg.SeedPubkey = data.(string)
	}
	// If decode fails, assume it's already hex

	return &cfg, nil
}

// SaveForwardRelayURL persists the forward_relay_url to the config file
func SaveForwardRelayURL(url string) error {
	viper.Set("forward_relay_url", url)
	return viper.WriteConfig()
}
