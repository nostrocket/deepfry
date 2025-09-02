package config

import (
	"flag"
	"fmt"
)

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
