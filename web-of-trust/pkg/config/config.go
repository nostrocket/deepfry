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
	RelayFilterBatchSize int           `mapstructure:"relay_filter_batch_size"`
	ForwardRelayURL      string        `mapstructure:"forward_relay_url"`

	// Spam-cluster scan (clusterscan CLI) settings.
	SeedPubkeys     []string `mapstructure:"seed_pubkeys"`      // trusted roots; trust flows out along follows
	TrustK          int      `mapstructure:"trust_k"`           // endorsements from the trusted set needed to join it
	ClusterDepth    int      `mapstructure:"cluster_depth"`     // follows-hops to walk when measuring a cluster
	MaxBridgeWeight int      `mapstructure:"max_bridge_weight"` // a candidate is a "weak bridge" if 1..N edges cross into trusted
	MinClusterSize  int      `mapstructure:"min_cluster_size"`  // ignore bridges whose cluster is smaller than this
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
	viper.SetDefault("relay_filter_batch_size", 100)

	// clusterscan defaults: the admin/forwarder keys used by the whitelist
	// plugin (whitelist-plugin/pkg/repository getHardcodedPubkeys) form the
	// trusted roots. The crawler seed is added at load time.
	viper.SetDefault("seed_pubkeys", []string{
		"f6b07746e51d757fce1a030ef6fbe5dae6805df857f26ddce4e414bc3f983c4d", // live event forwarder
		"de6a2fe67d4407511f23d5d8f8dbfd29967b9a345cfed912fdfedf7fbabf570d", // history forwarder
		"d91191e30e00444b942c0e82cad470b32af171764c2275bee0bd99377efd4075", // gsov
		"a0dda882fb89732b04793a2c989435fcd89ee559e81291074450edbd9b15621b", // rocketdog8
		"ba1838441e720ee91360d38321a19cbf8596e6540cfa045c9c5d429f1a2b9e3a", // macro88
	})
	viper.SetDefault("trust_k", 2)
	viper.SetDefault("cluster_depth", 3)
	viper.SetDefault("max_bridge_weight", 2)
	viper.SetDefault("min_cluster_size", 5)

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

	// Normalize cluster-scan seeds to hex and fold in the crawler seed, deduped.
	cfg.SeedPubkeys = normalizeSeedPubkeys(append(cfg.SeedPubkeys, cfg.SeedPubkey))

	return &cfg, nil
}

// normalizeSeedPubkeys decodes any npub-formatted entries to hex, drops empties,
// and removes duplicates while preserving order.
func normalizeSeedPubkeys(pubkeys []string) []string {
	seen := make(map[string]struct{}, len(pubkeys))
	out := make([]string, 0, len(pubkeys))
	for _, pk := range pubkeys {
		if pk == "" {
			continue
		}
		if _, data, err := nip19.Decode(pk); err == nil {
			if hex, ok := data.(string); ok {
				pk = hex
			}
		}
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	return out
}

// SaveForwardRelayURL persists the forward_relay_url to the config file
func SaveForwardRelayURL(url string) error {
	viper.Set("forward_relay_url", url)
	return viper.WriteConfig()
}

// RemoveRelayURL removes a relay URL from relay_urls in the config file.
func RemoveRelayURL(url string) error {
	current := viper.GetStringSlice("relay_urls")
	filtered := make([]string, 0, len(current))
	for _, u := range current {
		if u != url {
			filtered = append(filtered, u)
		}
	}
	if len(filtered) == len(current) {
		return nil
	}
	viper.Set("relay_urls", filtered)
	return viper.WriteConfig()
}
