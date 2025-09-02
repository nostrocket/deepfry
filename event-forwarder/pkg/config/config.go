package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"event-forwarder/pkg/crypto"
)

// ConfigSource represents a source of configuration values
type ConfigSource interface {
	GetString(key string) (string, bool)
	GetInt(key string) (int, bool)
	GetFloat(key string) (float64, bool)
}

// EnvSource implements ConfigSource for environment variables
type EnvSource struct{}

func (e *EnvSource) GetString(key string) (string, bool) {
	value := os.Getenv(key)
	return value, value != ""
}

func (e *EnvSource) GetInt(key string) (int, bool) {
	value := os.Getenv(key)
	if value == "" {
		return 0, false
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i, true
	}
	return 0, false
}

func (e *EnvSource) GetFloat(key string) (float64, bool) {
	value := os.Getenv(key)
	if value == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f, true
	}
	return 0, false
}

// FlagSource implements ConfigSource for command-line flags
type FlagSource struct {
	values map[string]interface{}
}

func NewFlagSource() *FlagSource {
	return &FlagSource{values: make(map[string]interface{})}
}

func (f *FlagSource) Set(key string, value interface{}) {
	f.values[key] = value
}

func (f *FlagSource) GetString(key string) (string, bool) {
	if value, exists := f.values[key]; exists {
		if str, ok := value.(string); ok && str != "" {
			return str, true
		}
	}
	return "", false
}

func (f *FlagSource) GetInt(key string) (int, bool) {
	if value, exists := f.values[key]; exists {
		if i, ok := value.(int); ok {
			return i, true
		}
	}
	return 0, false
}

func (f *FlagSource) GetFloat(key string) (float64, bool) {
	if value, exists := f.values[key]; exists {
		if fl, ok := value.(float64); ok {
			return fl, true
		}
	}
	return 0, false
}

// ConfigResolver resolves configuration values from multiple sources with precedence
type ConfigResolver struct {
	sources []ConfigSource
}

func NewConfigResolver(sources ...ConfigSource) *ConfigResolver {
	return &ConfigResolver{sources: sources}
}

// ResolveString resolves string value from sources in order of precedence
func (r *ConfigResolver) ResolveString(key, defaultValue string) string {
	for _, source := range r.sources {
		if value, found := source.GetString(key); found {
			return value
		}
	}
	return defaultValue
}

// ResolveInt resolves int value from sources in order of precedence
func (r *ConfigResolver) ResolveInt(key string, defaultValue int) int {
	for _, source := range r.sources {
		if value, found := source.GetInt(key); found {
			return value
		}
	}
	return defaultValue
}

// ResolveFloat resolves float value from sources in order of precedence
func (r *ConfigResolver) ResolveFloat(key string, defaultValue float64) float64 {
	for _, source := range r.sources {
		if value, found := source.GetFloat(key); found {
			return value
		}
	}
	return defaultValue
}

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
		os.Exit(0)
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

// parseCLIFlags parses command-line flags and returns a FlagSource and help flag
func parseCLIFlags() (*FlagSource, bool) {
	flagSource := NewFlagSource()

	// Define CLI flags
	sourceRelayURL := flag.String("source-relay-url", "", "Source relay URL")
	deepFryRelayURL := flag.String("deepfry-relay-url", "", "DeepFry relay URL")
	nostrSecretKey := flag.String("nostr-secret-key", "", "Nostr secret key")
	syncWindowSeconds := flag.Int("sync-window-seconds", 0, "Sync window in seconds")
	syncMaxBatch := flag.Int("sync-max-batch", 0, "Max sync batch size")
	syncMaxCatchupLagSeconds := flag.Int("sync-max-catchup-lag-seconds", 0, "Max catchup lag in seconds")
	networkInitialBackoffSeconds := flag.Int("network-initial-backoff-seconds", 0, "Initial backoff in seconds")
	networkMaxBackoffSeconds := flag.Int("network-max-backoff-seconds", 0, "Max backoff in seconds")
	networkBackoffJitter := flag.Float64("network-backoff-jitter", 0, "Backoff jitter")
	timeoutPublishSeconds := flag.Int("timeout-publish-seconds", 0, "Publish timeout in seconds")
	timeoutSubscribeSeconds := flag.Int("timeout-subscribe-seconds", 0, "Subscribe timeout in seconds")
	help := flag.Bool("help", false, "Show help message")

	flag.Parse()

	if *help {
		return flagSource, true
	}

	// Store non-zero/non-empty values in flag source
	if *sourceRelayURL != "" {
		flagSource.Set("SOURCE_RELAY_URL", *sourceRelayURL)
	}
	if *deepFryRelayURL != "" {
		flagSource.Set("DEEPFRY_RELAY_URL", *deepFryRelayURL)
	}
	if *nostrSecretKey != "" {
		flagSource.Set("NOSTR_SYNC_SECKEY", *nostrSecretKey)
	}
	if *syncWindowSeconds != 0 {
		flagSource.Set("SYNC_WINDOW_SECONDS", *syncWindowSeconds)
	}
	if *syncMaxBatch != 0 {
		flagSource.Set("SYNC_MAX_BATCH", *syncMaxBatch)
	}
	if *syncMaxCatchupLagSeconds != 0 {
		flagSource.Set("SYNC_MAX_CATCHUP_LAG_SECONDS", *syncMaxCatchupLagSeconds)
	}
	if *networkInitialBackoffSeconds != 0 {
		flagSource.Set("NETWORK_INITIAL_BACKOFF_SECONDS", *networkInitialBackoffSeconds)
	}
	if *networkMaxBackoffSeconds != 0 {
		flagSource.Set("NETWORK_MAX_BACKOFF_SECONDS", *networkMaxBackoffSeconds)
	}
	if *networkBackoffJitter != 0 {
		flagSource.Set("NETWORK_BACKOFF_JITTER", *networkBackoffJitter)
	}
	if *timeoutPublishSeconds != 0 {
		flagSource.Set("TIMEOUT_PUBLISH_SECONDS", *timeoutPublishSeconds)
	}
	if *timeoutSubscribeSeconds != 0 {
		flagSource.Set("TIMEOUT_SUBSCRIBE_SECONDS", *timeoutSubscribeSeconds)
	}

	return flagSource, false
}

// printUsage prints the usage message
func printUsage() {
	fmt.Println("Event Forwarder - Forward events between Nostr relays")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  fwd [OPTIONS]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --source-relay-url string            Source relay URL (required)")
	fmt.Println("  --deepfry-relay-url string           DeepFry relay URL (required)")
	fmt.Println("  --nostr-secret-key string            Nostr secret key (required)")
	fmt.Println("  --sync-window-seconds int            Sync window in seconds (default: 5)")
	fmt.Println("  --sync-max-batch int                 Max sync batch size (default: 1000)")
	fmt.Println("  --sync-max-catchup-lag-seconds int   Max catchup lag in seconds (default: 10)")
	fmt.Println("  --network-initial-backoff-seconds int Initial backoff in seconds (default: 1)")
	fmt.Println("  --network-max-backoff-seconds int   Max backoff in seconds (default: 30)")
	fmt.Println("  --network-backoff-jitter float      Backoff jitter (default: 0.2)")
	fmt.Println("  --timeout-publish-seconds int       Publish timeout in seconds (default: 10)")
	fmt.Println("  --timeout-subscribe-seconds int     Subscribe timeout in seconds (default: 10)")
	fmt.Println("  --help                               Show this help message")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  SOURCE_RELAY_URL                     Source relay URL")
	fmt.Println("  DEEPFRY_RELAY_URL                    DeepFry relay URL")
	fmt.Println("  NOSTR_SYNC_SECKEY                    Nostr secret key")
	fmt.Println("  SYNC_WINDOW_SECONDS                  Sync window in seconds")
	fmt.Println("  SYNC_MAX_BATCH                       Max sync batch size")
	fmt.Println("  SYNC_MAX_CATCHUP_LAG_SECONDS         Max catchup lag in seconds")
	fmt.Println("  NETWORK_INITIAL_BACKOFF_SECONDS      Initial backoff in seconds")
	fmt.Println("  NETWORK_MAX_BACKOFF_SECONDS          Max backoff in seconds")
	fmt.Println("  NETWORK_BACKOFF_JITTER               Backoff jitter")
	fmt.Println("  TIMEOUT_PUBLISH_SECONDS              Publish timeout in seconds")
	fmt.Println("  TIMEOUT_SUBSCRIBE_SECONDS            Subscribe timeout in seconds")
	fmt.Println()
	fmt.Println("Note: CLI options override environment variables")
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
