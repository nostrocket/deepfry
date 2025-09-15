package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/spf13/viper"
)

type Config struct {
	RelayURL   string        `mapstructure:"relay_url"`
	DgraphAddr string        `mapstructure:"dgraph_addr"`
	PubkeyHex  string        `mapstructure:"pubkey"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize Dgraph client
	dgClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		log.Fatalf("Failed to connect to Dgraph: %v", err)
	}
	defer dgClient.Close()

	// Ensure schema
	ctx := context.Background()
	if err := dgClient.EnsureSchema(ctx); err != nil {
		log.Fatalf("Failed to ensure schema: %v", err)
	}

	// Connect to relay
	relay, err := nostr.RelayConnect(context.Background(), cfg.RelayURL)
	if err != nil {
		log.Fatalf("Failed to connect to relay %s: %v", cfg.RelayURL, err)
	}
	defer relay.Close()

	log.Printf("Connected to relay: %s", cfg.RelayURL)

	// Fetch follow list
	if err := fetchAndUpdateFollows(ctx, relay, dgClient, cfg); err != nil {
		log.Fatalf("Failed to fetch and update follows: %v", err)
	}

	log.Printf("Successfully updated follow list for pubkey: %s", cfg.PubkeyHex)
}

func loadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/web-of-trust/")

	// Set defaults
	viper.SetDefault("relay_url", "wss://relay.damus.io")
	viper.SetDefault("dgraph_addr", "localhost:9080")
	viper.SetDefault("timeout", "30s")

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		log.Printf("No config file found, using defaults and flags")
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// Validate required fields
	if cfg.PubkeyHex == "" {
		return nil, fmt.Errorf("pubkey is required in configuration")
	}

	// Handle both hex and npub formats
	if _, data, err := nip19.Decode(cfg.PubkeyHex); err == nil {
		cfg.PubkeyHex = data.(string)
	}
	// If decode fails, assume it's already hex

	return &cfg, nil
}

func fetchAndUpdateFollows(ctx context.Context, relay *nostr.Relay, dgClient *dgraph.Client, cfg *Config) error {
	// Create filter for kind 3 events from the specified pubkey
	filter := nostr.Filter{
		Authors: []string{cfg.PubkeyHex},
		Kinds:   []int{3},
		Limit:   1, // We only want the most recent follow list
	}

	// Set timeout context
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	// Subscribe to events
	sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Unsub()

	// Wait for events
	select {
	case event := <-sub.Events:
		if event == nil {
			return fmt.Errorf("no kind 3 event found for pubkey: %s", cfg.PubkeyHex)
		}

		log.Printf("Found kind 3 event: %s, created_at: %d", event.ID, event.CreatedAt)

		// Parse and update follows
		return updateFollowsFromEvent(ctx, dgClient, event)

	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for kind 3 event")
	}
}

func updateFollowsFromEvent(ctx context.Context, dgClient *dgraph.Client, event *nostr.Event) error {
	// Parse follows from p tags
	var rawFollows []string
	followsMap := make(map[string]struct{})

	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			rawFollows = append(rawFollows, tag[1])
			followsMap[tag[1]] = struct{}{}
		}
	}

	uniqueFollowsCount := len(followsMap)
	duplicatesCount := len(rawFollows) - uniqueFollowsCount

	log.Printf("Found %d follows in event (%d unique, %d duplicates)", len(rawFollows), uniqueFollowsCount, duplicatesCount)

	// Process all follows in one batch operation
	if uniqueFollowsCount > 0 {
		err := dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap)
		if err != nil {
			return fmt.Errorf("failed to add follows batch: %w", err)
		}

		log.Printf("Processed %d/%d follows", uniqueFollowsCount, uniqueFollowsCount)
	}

	// Log metrics
	logMetrics(event.PubKey, uniqueFollowsCount, duplicatesCount)

	return nil
}

func logMetrics(pubkey string, followsCount int, duplicatesCount int) {
	metrics := map[string]interface{}{
		"pubkey":           pubkey,
		"follows_count":    followsCount,
		"duplicates_count": duplicatesCount,
		"processed_at":     time.Now().Format(time.RFC3339),
		"component":        "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("METRICS: %s", string(metricsJSON))
}
