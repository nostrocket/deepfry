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
		flagSource.Set(KeySourceRelayURL, *sourceRelayURL)
	}
	if *deepFryRelayURL != "" {
		flagSource.Set(KeyDeepFryRelayURL, *deepFryRelayURL)
	}
	if *nostrSecretKey != "" {
		flagSource.Set(KeyNostrSecretKey, *nostrSecretKey)
	}
	if *syncWindowSeconds != 0 {
		flagSource.Set(KeySyncWindowSeconds, *syncWindowSeconds)
	}
	if *syncMaxBatch != 0 {
		flagSource.Set(KeySyncMaxBatch, *syncMaxBatch)
	}
	if *syncMaxCatchupLagSeconds != 0 {
		flagSource.Set(KeySyncMaxCatchupLagSeconds, *syncMaxCatchupLagSeconds)
	}
	if *networkInitialBackoffSeconds != 0 {
		flagSource.Set(KeyNetworkInitialBackoffSeconds, *networkInitialBackoffSeconds)
	}
	if *networkMaxBackoffSeconds != 0 {
		flagSource.Set(KeyNetworkMaxBackoffSeconds, *networkMaxBackoffSeconds)
	}
	if *networkBackoffJitter != 0 {
		flagSource.Set(KeyNetworkBackoffJitter, *networkBackoffJitter)
	}
	if *timeoutPublishSeconds != 0 {
		flagSource.Set(KeyTimeoutPublishSeconds, *timeoutPublishSeconds)
	}
	if *timeoutSubscribeSeconds != 0 {
		flagSource.Set(KeyTimeoutSubscribeSeconds, *timeoutSubscribeSeconds)
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
	fmt.Printf("  --source-relay-url string            Source relay URL (required)\n")
	fmt.Printf("  --deepfry-relay-url string           DeepFry relay URL (required)\n")
	fmt.Printf("  --nostr-secret-key string            Nostr secret key (required)\n")
	fmt.Printf("  --sync-window-seconds int            Sync window in seconds (default: %d)\n", DefaultSyncWindowSeconds)
	fmt.Printf("  --sync-max-batch int                 Max sync batch size (default: %d)\n", DefaultSyncMaxBatch)
	fmt.Printf("  --sync-max-catchup-lag-seconds int   Max catchup lag in seconds (default: %d)\n", DefaultSyncMaxCatchupLagSeconds)
	fmt.Printf("  --network-initial-backoff-seconds int Initial backoff in seconds (default: %d)\n", DefaultNetworkInitialBackoffSeconds)
	fmt.Printf("  --network-max-backoff-seconds int   Max backoff in seconds (default: %d)\n", DefaultNetworkMaxBackoffSeconds)
	fmt.Printf("  --network-backoff-jitter float      Backoff jitter (default: %.1f)\n", DefaultNetworkBackoffJitter)
	fmt.Printf("  --timeout-publish-seconds int       Publish timeout in seconds (default: %d)\n", DefaultTimeoutPublishSeconds)
	fmt.Printf("  --timeout-subscribe-seconds int     Subscribe timeout in seconds (default: %d)\n", DefaultTimeoutSubscribeSeconds)
	fmt.Println("  --help                               Show this help message")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Printf("  %-36s %s\n", KeySourceRelayURL, "Source relay URL")
	fmt.Printf("  %-36s %s\n", KeyDeepFryRelayURL, "DeepFry relay URL")
	fmt.Printf("  %-36s %s\n", KeyNostrSecretKey, "Nostr secret key")
	fmt.Printf("  %-36s %s\n", KeySyncWindowSeconds, "Sync window in seconds")
	fmt.Printf("  %-36s %s\n", KeySyncMaxBatch, "Max sync batch size")
	fmt.Printf("  %-36s %s\n", KeySyncMaxCatchupLagSeconds, "Max catchup lag in seconds")
	fmt.Printf("  %-36s %s\n", KeyNetworkInitialBackoffSeconds, "Initial backoff in seconds")
	fmt.Printf("  %-36s %s\n", KeyNetworkMaxBackoffSeconds, "Max backoff in seconds")
	fmt.Printf("  %-36s %s\n", KeyNetworkBackoffJitter, "Backoff jitter")
	fmt.Printf("  %-36s %s\n", KeyTimeoutPublishSeconds, "Publish timeout in seconds")
	fmt.Printf("  %-36s %s\n", KeyTimeoutSubscribeSeconds, "Subscribe timeout in seconds")
	fmt.Println()
	fmt.Println("Note: CLI options override environment variables")
}
