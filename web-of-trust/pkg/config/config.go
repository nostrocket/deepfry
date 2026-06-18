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

// EjectionThresholds holds per-failure-class ejection thresholds (D-06).
// A relay is ejected when its failure counter for a given class reaches the
// corresponding threshold. Non-positive values are corrected to hardcoded
// defaults (Transport=10, FilterRej=3, SubFlap=5) after unmarshal.
type EjectionThresholds struct {
	Transport int `mapstructure:"transport"`
	FilterRej int `mapstructure:"filter_rejection"`
	SubFlap   int `mapstructure:"subscription_flap"`
}

// MissBackoffParams holds the exponential backoff configuration for
// chronic-miss pubkeys (Phase 8 PERF-02, D-07). Non-positive values are
// corrected to hardcoded defaults after unmarshal (T-08-CFG mitigation).
type MissBackoffParams struct {
	// Base is the initial backoff interval applied on the first miss.
	Base time.Duration `mapstructure:"base"`
	// Ratio is the geometric growth factor per miss (e.g. 2 → doubles each time).
	Ratio int `mapstructure:"ratio"`
	// Cap is the maximum backoff interval (7d = 168h); misses beyond this point
	// are retried on the cap schedule indefinitely (never permanently abandoned).
	Cap time.Duration `mapstructure:"cap"`
	// HitRefreshCadence is the next_attempt interval stamped on a HIT (D-03):
	// pubkeys that return a kind-3 event are re-queried after this duration.
	HitRefreshCadence time.Duration `mapstructure:"hit_refresh_cadence"`
}

// Config holds the application configuration
type Config struct {
	RelayURLs            []string      `mapstructure:"relay_urls"`
	DgraphAddr           string        `mapstructure:"dgraph_addr"`
	SeedPubkey           string        `mapstructure:"pubkey"`
	Timeout              time.Duration `mapstructure:"timeout"`
	Debug                bool          `mapstructure:"debug"`
	StalePubkeyThreshold int64         `mapstructure:"stale_pubkey_threshold"`
	RelayFilterBatchSize int           `mapstructure:"relay_filter_batch_size"`
	FrontierBatchSize    int           `mapstructure:"frontier_batch_size"`
	CountSampleInterval  int           `mapstructure:"count_sample_interval"`
	ForwardRelayURL      string        `mapstructure:"forward_relay_url"`

	// Spam-cluster scan (clusterscan CLI) settings.
	SeedPubkeys     []string `mapstructure:"seed_pubkeys"`      // trusted roots; trust flows out along follows
	TrustK          int      `mapstructure:"trust_k"`           // endorsements from the trusted set needed to join it
	ClusterDepth    int      `mapstructure:"cluster_depth"`     // follows-hops to walk when measuring a cluster
	MaxBridgeWeight int      `mapstructure:"max_bridge_weight"` // a candidate is a "weak bridge" if 1..N edges cross into trusted
	MinClusterSize  int      `mapstructure:"min_cluster_size"`  // ignore bridges whose cluster is smaller than this

	// Relay health management (Phase 7) settings.
	RelayEjectionThresholds EjectionThresholds `mapstructure:"relay_ejection_thresholds"`
	EjectedRelays           []string           `mapstructure:"ejected_relays"`

	// Phase 8 TIMEOUT-02: fraction of queried relays that must reach EOSE or
	// error before the batch cancels early. Default 0.70 (70%).
	RelayEOSEQuorum float64 `mapstructure:"relay_eose_quorum"`

	// Phase 8 PERF-02: miss-backoff parameters for chronic-miss pubkeys.
	MissBackoff MissBackoffParams `mapstructure:"miss_backoff"`
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
	viper.SetDefault("timeout", "15s") // TIMEOUT-01 (D-11): was "30s"
	viper.SetDefault("debug", false)
	viper.SetDefault("pubkey", "npub1mygerccwqpzyh9pvp6pv44rskv40zutkfs38t0hqhkvnwlhagp6s3psn5p")
	viper.SetDefault("stale_pubkey_threshold", 24*60*60) // 24 hours in seconds
	viper.SetDefault("relay_filter_batch_size", 100)
	viper.SetDefault("frontier_batch_size", 100)
	viper.SetDefault("count_sample_interval", 1)

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

	// Relay health management defaults (D-06).
	viper.SetDefault("relay_ejection_thresholds", map[string]interface{}{
		"transport":         10,
		"filter_rejection":  3,
		"subscription_flap": 5,
	})
	viper.SetDefault("ejected_relays", []string{})

	// Phase 8 TIMEOUT-02 (D-12): EOSE quorum fraction.
	viper.SetDefault("relay_eose_quorum", 0.70)

	// Phase 8 PERF-02 (D-07): miss-backoff parameter group.
	viper.SetDefault("miss_backoff", map[string]interface{}{
		"base":                "2h",
		"ratio":               2,
		"cap":                 "168h", // 7 days
		"hit_refresh_cadence": "24h",  // StalePubkeyThreshold re-used for HIT path (D-03)
	})

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

	// Guard: non-positive throughput controls can stall useful work or hide
	// progress metrics. Preserve existing behavior by falling back safely.
	if cfg.FrontierBatchSize <= 0 {
		if cfg.RelayFilterBatchSize > 0 {
			cfg.FrontierBatchSize = cfg.RelayFilterBatchSize
		} else {
			cfg.FrontierBatchSize = 100
		}
	}
	if cfg.CountSampleInterval <= 0 {
		cfg.CountSampleInterval = 1
	}

	// Guard: zero or negative thresholds would eject relays on the first failure
	// (STRIDE DoS threat T-07-DOS). Correct to hardcoded defaults.
	if cfg.RelayEjectionThresholds.Transport <= 0 {
		cfg.RelayEjectionThresholds.Transport = 10
	}
	if cfg.RelayEjectionThresholds.FilterRej <= 0 {
		cfg.RelayEjectionThresholds.FilterRej = 3
	}
	if cfg.RelayEjectionThresholds.SubFlap <= 0 {
		cfg.RelayEjectionThresholds.SubFlap = 5
	}

	// Guard: non-positive miss-backoff values would produce zero/negative intervals,
	// causing resource-exhaustion (T-08-CFG). Correct to hardcoded defaults.
	if cfg.MissBackoff.Base <= 0 {
		cfg.MissBackoff.Base = 2 * time.Hour
	}
	if cfg.MissBackoff.Ratio <= 1 {
		cfg.MissBackoff.Ratio = 2
	}
	if cfg.MissBackoff.Cap <= 0 {
		cfg.MissBackoff.Cap = 168 * time.Hour
	}
	if cfg.MissBackoff.HitRefreshCadence <= 0 {
		cfg.MissBackoff.HitRefreshCadence = 24 * time.Hour
	}

	// Ensure EjectedRelays is non-nil for safe slice operations.
	if cfg.EjectedRelays == nil {
		cfg.EjectedRelays = []string{}
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

// EjectRelayURL removes a relay URL from relay_urls and appends it to
// ejected_relays in the config file (D-08). The reason, failure class,
// count, and timestamp are logged by the caller (markRelayDead); this
// function persists only the URL-only ejected list.
//
// Operates on the package-global viper instance populated by LoadConfig;
// do not call before LoadConfig has run.
func EjectRelayURL(url string) error {
	// Remove from relay_urls (mirror RemoveRelayURL pattern).
	current := viper.GetStringSlice("relay_urls")
	filtered := make([]string, 0, len(current))
	for _, u := range current {
		if u != url {
			filtered = append(filtered, u)
		}
	}
	viper.Set("relay_urls", filtered)

	// Append to ejected_relays (URL only; no metadata in YAML per D-08).
	ejected := viper.GetStringSlice("ejected_relays")
	ejected = append(ejected, url)
	viper.Set("ejected_relays", ejected)

	return viper.WriteConfig()
}
