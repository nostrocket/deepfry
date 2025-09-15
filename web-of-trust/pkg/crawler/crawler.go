package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
)

type Crawler struct {
	relay    *nostr.Relay
	dgClient *dgraph.Client
	timeout  time.Duration
}

type Config struct {
	RelayURL   string
	DgraphAddr string
	Timeout    time.Duration
}

func New(cfg Config) (*Crawler, error) {
	// Initialize Dgraph client
	dgClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Dgraph: %w", err)
	}

	// Ensure schema
	ctx := context.Background()
	if err := dgClient.EnsureSchema(ctx); err != nil {
		dgClient.Close()
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	// Connect to relay
	relay, err := nostr.RelayConnect(context.Background(), cfg.RelayURL)
	if err != nil {
		dgClient.Close()
		return nil, fmt.Errorf("failed to connect to relay %s: %w", cfg.RelayURL, err)
	}

	log.Printf("Connected to relay: %s", cfg.RelayURL)

	return &Crawler{
		relay:    relay,
		dgClient: dgClient,
		timeout:  cfg.Timeout,
	}, nil
}

func (c *Crawler) Close() {
	if c.relay != nil {
		c.relay.Close()
	}
	if c.dgClient != nil {
		c.dgClient.Close()
	}
}

func (c *Crawler) FetchAndUpdateFollows(ctx context.Context, pubkeyHex string) error {
	// Create filter for kind 3 events from the specified pubkey
	filter := nostr.Filter{
		Authors: []string{pubkeyHex},
		Kinds:   []int{3},
		Limit:   1, // We only want the most recent follow list
	}

	// Set timeout context
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Subscribe to events
	sub, err := c.relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Unsub()

	// Wait for events
	select {
	case event := <-sub.Events:
		if event == nil {
			return fmt.Errorf("no kind 3 event found for pubkey: %s", pubkeyHex)
		}

		log.Printf("Found kind 3 event: %s, created_at: %d", event.ID, event.CreatedAt)

		// Parse and update follows
		return c.updateFollowsFromEvent(ctx, event)

	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for kind 3 event")
	}
}

func (c *Crawler) updateFollowsFromEvent(ctx context.Context, event *nostr.Event) error {
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
		err := c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap)
		if err != nil {
			return fmt.Errorf("failed to add follows batch: %w", err)
		}

		log.Printf("Processed %d/%d follows", uniqueFollowsCount, uniqueFollowsCount)
	}

	// Log metrics
	c.logMetrics(event.PubKey, uniqueFollowsCount, duplicatesCount)

	return nil
}

func (c *Crawler) logMetrics(pubkey string, followsCount int, duplicatesCount int) {
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
