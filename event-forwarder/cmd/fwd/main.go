package main

import (
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/version"
	"fmt"
	"os"
)

func main() {
	// Check for version flag first
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		info := version.Info()
		fmt.Printf("fwd version %s, commit %s, built %s\n", info.Version, info.Commit, info.Built)
		return
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg != nil {
		fmt.Println("Event Forwarder started successfully!")
		fmt.Printf("Version: %s\n", version.Info().Version)
		fmt.Printf("Source Relay: %s\n", cfg.SourceRelayURL)
		fmt.Printf("DeepFry Relay: %s\n", cfg.DeepFryRelayURL)
		fmt.Printf("Public Key: %s\n", cfg.NostrKeyPair.PublicKeyHex)
	}
}
