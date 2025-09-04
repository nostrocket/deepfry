package config

import (
	"flag"
	"fmt"
)

// parseCLIFlags parses command-line flags and returns a FlagSource and help flag
func parseCLIFlags() (*FlagSource, bool) {
	flagSource := NewFlagSource()

	// Define CLI flags
	sourceRelayURL := flag.String(FlagSourceRelayURL, "", HelpSourceRelayURL)
	deepFryRelayURL := flag.String(FlagDeepFryRelayURL, "", HelpDeepFryRelayURL)
	nostrSecretKey := flag.String(FlagNostrSecretKey, "", HelpNostrSecretKey)
	syncWindowSeconds := flag.Int(FlagSyncWindowSeconds, 0, HelpSyncWindowSeconds)
	syncMaxBatch := flag.Int(FlagSyncMaxBatch, 0, HelpSyncMaxBatch)
	syncMaxCatchupLagSeconds := flag.Int(FlagSyncMaxCatchupLagSeconds, 0, HelpSyncMaxCatchupLagSeconds)
	syncStartTime := flag.String(FlagSyncStartTime, "", HelpSyncStartTime)
	networkInitialBackoffSeconds := flag.Int(FlagNetworkInitialBackoffSeconds, 0, HelpNetworkInitialBackoffSeconds)
	networkMaxBackoffSeconds := flag.Int(FlagNetworkMaxBackoffSeconds, 0, HelpNetworkMaxBackoffSeconds)
	networkBackoffJitter := flag.Float64(FlagNetworkBackoffJitter, 0, HelpNetworkBackoffJitter)
	timeoutPublishSeconds := flag.Int(FlagTimeoutPublishSeconds, 0, HelpTimeoutPublishSeconds)
	timeoutSubscribeSeconds := flag.Int(FlagTimeoutSubscribeSeconds, 0, HelpTimeoutSubscribeSeconds)
	help := flag.Bool(FlagHelp, false, HelpShowHelp)

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
	if *syncStartTime != "" {
		flagSource.Set(KeySyncStartTime, *syncStartTime)
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
	fmt.Printf("%s - %s\n", AppName, AppDescription)
	fmt.Println()
	fmt.Printf("%s\n", HelpUsage)
	fmt.Printf("  %s\n", UsageFormat)
	fmt.Println()
	fmt.Printf("%s\n", HelpOptions)
	fmt.Printf("  --%s string            %s\n", FlagSourceRelayURL, HelpSourceRelayURL)
	fmt.Printf("  --%s string           %s\n", FlagDeepFryRelayURL, HelpDeepFryRelayURL)
	fmt.Printf("  --%s string            %s\n", FlagNostrSecretKey, HelpNostrSecretKey)
	fmt.Printf("  --%s int            %s (default: %d)\n", FlagSyncWindowSeconds, HelpSyncWindowSeconds, DefaultSyncWindowSeconds)
	fmt.Printf("  --%s int                 %s (default: %d)\n", FlagSyncMaxBatch, HelpSyncMaxBatch, DefaultSyncMaxBatch)
	fmt.Printf("  --%s int   %s (default: %d)\n", FlagSyncMaxCatchupLagSeconds, HelpSyncMaxCatchupLagSeconds, DefaultSyncMaxCatchupLagSeconds)
	fmt.Printf("  --%s string                %s\n", FlagSyncStartTime, HelpSyncStartTime)
	fmt.Printf("  --%s int %s (default: %d)\n", FlagNetworkInitialBackoffSeconds, HelpNetworkInitialBackoffSeconds, DefaultNetworkInitialBackoffSeconds)
	fmt.Printf("  --%s int   %s (default: %d)\n", FlagNetworkMaxBackoffSeconds, HelpNetworkMaxBackoffSeconds, DefaultNetworkMaxBackoffSeconds)
	fmt.Printf("  --%s float      %s (default: %.1f)\n", FlagNetworkBackoffJitter, HelpNetworkBackoffJitter, DefaultNetworkBackoffJitter)
	fmt.Printf("  --%s int       %s (default: %d)\n", FlagTimeoutPublishSeconds, HelpTimeoutPublishSeconds, DefaultTimeoutPublishSeconds)
	fmt.Printf("  --%s int     %s (default: %d)\n", FlagTimeoutSubscribeSeconds, HelpTimeoutSubscribeSeconds, DefaultTimeoutSubscribeSeconds)
	fmt.Printf("  --%s                               %s\n", FlagHelp, HelpShowHelp)
	fmt.Println()
	fmt.Printf("%s\n", HelpEnvironmentVars)
	fmt.Printf("  %-36s %s\n", KeySourceRelayURL, EnvDescSourceRelayURL)
	fmt.Printf("  %-36s %s\n", KeyDeepFryRelayURL, EnvDescDeepFryRelayURL)
	fmt.Printf("  %-36s %s\n", KeyNostrSecretKey, EnvDescNostrSecretKey)
	fmt.Printf("  %-36s %s\n", KeySyncWindowSeconds, EnvDescSyncWindowSeconds)
	fmt.Printf("  %-36s %s\n", KeySyncMaxBatch, EnvDescSyncMaxBatch)
	fmt.Printf("  %-36s %s\n", KeySyncMaxCatchupLagSeconds, EnvDescSyncMaxCatchupLagSeconds)
	fmt.Printf("  %-36s %s\n", KeySyncStartTime, EnvDescSyncStartTime)
	fmt.Printf("  %-36s %s\n", KeyNetworkInitialBackoffSeconds, EnvDescNetworkInitialBackoffSeconds)
	fmt.Printf("  %-36s %s\n", KeyNetworkMaxBackoffSeconds, EnvDescNetworkMaxBackoffSeconds)
	fmt.Printf("  %-36s %s\n", KeyNetworkBackoffJitter, EnvDescNetworkBackoffJitter)
	fmt.Printf("  %-36s %s\n", KeyTimeoutPublishSeconds, EnvDescTimeoutPublishSeconds)
	fmt.Printf("  %-36s %s\n", KeyTimeoutSubscribeSeconds, EnvDescTimeoutSubscribeSeconds)
	fmt.Println()
	fmt.Printf("%s\n", HelpNote)
}
