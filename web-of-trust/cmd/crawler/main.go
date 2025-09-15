package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type Config struct {
	RelayURL   string
	DgraphAddr string
	PubkeyHex  string
	Timeout    time.Duration
}

func main() {
	cfg := loadConfig()

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

func loadConfig() Config {
	relayURL := os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "wss://relay.damus.io"
	}

	dgraphAddr := os.Getenv("DGRAPH_ADDR")
	if dgraphAddr == "" {
		dgraphAddr = "localhost:9080"
	}

	pubkeyInput := os.Getenv("PUBKEY")
	if pubkeyInput == "" {
		log.Fatal("PUBKEY environment variable is required")
	}

	// Handle both hex and npub formats
	var pubkeyHex string
	if _, data, err := nip19.Decode(pubkeyInput); err == nil {
		pubkeyHex = data.(string)
	} else {
		pubkeyHex = pubkeyInput // assume it's already hex
	}

	timeout := 30 * time.Second
	if timeoutStr := os.Getenv("TIMEOUT"); timeoutStr != "" {
		if t, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = t
		}
	}

	return Config{
		RelayURL:   relayURL,
		DgraphAddr: dgraphAddr,
		PubkeyHex:  pubkeyHex,
		Timeout:    timeout,
	}
}

func fetchAndUpdateFollows(ctx context.Context, relay *nostr.Relay, dgClient *dgraph.Client, cfg Config) error {
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
	var follows []string
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "p" {
			follows = append(follows, tag[1])
		}
	}

	log.Printf("Found %d follows in event", len(follows))

	// Update each follow relationship
	for i, followeePubkey := range follows {
		err := dgClient.AddFollower(ctx, event.PubKey, int64(event.CreatedAt), followeePubkey)
		if err != nil {
			log.Printf("Failed to add follower %s -> %s: %v", event.PubKey, followeePubkey, err)
			continue
		}

		if (i+1)%100 == 0 {
			log.Printf("Processed %d/%d follows", i+1, len(follows))
		}
	}

	// Log metrics
	logMetrics(event.PubKey, len(follows))

	return nil
}

func logMetrics(pubkey string, followsCount int) {
	metrics := map[string]interface{}{
		"pubkey":        pubkey,
		"follows_count": followsCount,
		"processed_at":  time.Now().Format(time.RFC3339),
		"component":     "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("METRICS: %s", string(metricsJSON))
}
