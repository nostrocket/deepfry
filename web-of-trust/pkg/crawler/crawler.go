package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
)

type Crawler struct {
	relay         *nostr.Relay
	dgClient      *dgraph.Client
	timeout       time.Duration
	dbUpdateMutex sync.Mutex
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
		relay:         relay,
		dgClient:      dgClient,
		timeout:       cfg.Timeout,
		dbUpdateMutex: sync.Mutex{},
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

func (c *Crawler) FetchAndUpdateFollows(ctx context.Context, pubkeys []string) error {
	// Create filter for kind 3 events from all specified pubkeys
	filter := nostr.Filter{
		Authors: pubkeys,
		Kinds:   []int{3},
		Limit:   len(pubkeys), // Allow one event per pubkey
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
	processed := 0
	for processed < len(pubkeys) {
		select {
		case event := <-sub.Events:
			if event == nil {
				log.Printf("Received nil event, continuing...")
				continue
			}

			log.Printf("Found kind 3 event: %s, created_at: %d, pubkey: %s", event.ID, event.CreatedAt, event.PubKey)

			// Validate event signature
			if ok, err := event.CheckSignature(); !ok {
				log.Printf("WARN: Invalid signature for event %s from pubkey %s: %v", event.ID, event.PubKey, err)
				c.logSignatureValidationMetrics(event.PubKey, false)
				processed++
				continue //This continue statement is a control flow keyword in Go that skips the rest of the current iteration in a loop and jumps directly to the next iteration.
			}
			c.logSignatureValidationMetrics(event.PubKey, true)

			// Parse and update follows
			c.dbUpdateMutex.Lock()
			if err := c.updateFollowsFromEvent(ctx, event); err != nil {
				c.dbUpdateMutex.Unlock()
				return err
			}
			c.dbUpdateMutex.Unlock()
			processed++
		case <-ctx.Done():
			if processed == 0 {
				return fmt.Errorf("timeout waiting for kind 3 events")
			}
			log.Printf("Timeout reached, processed %d/%d pubkeys", processed, len(pubkeys))
			return nil
		}
	}

	return nil
}

func (c *Crawler) updateFollowsFromEvent(ctx context.Context, event *nostr.Event) error {
	// Check if this event is newer than what we already have
	existingTimestamp, err := c.dgClient.GetKind3CreatedAt(ctx, event.PubKey)
	if err != nil {
		return fmt.Errorf("failed to get existing kind3CreatedAt: %w", err)
	}

	if existingTimestamp >= int64(event.CreatedAt) {
		log.Printf("Skipping event %s (created_at: %d) - not newer than existing (created_at: %d)",
			event.ID, event.CreatedAt, existingTimestamp)
		return nil
	}

	log.Printf("Processing newer event %s (created_at: %d) - existing was (created_at: %d)",
		event.ID, event.CreatedAt, existingTimestamp)

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

	err = c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap)
	if err != nil {
		return fmt.Errorf("failed to add follows batch: %w", err)
	}

	log.Printf("Processed %d/%d follows", uniqueFollowsCount, uniqueFollowsCount)

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

func (c *Crawler) logSignatureValidationMetrics(pubkey string, valid bool) {
	metrics := map[string]interface{}{
		"pubkey":          pubkey,
		"signature_valid": valid,
		"validated_at":    time.Now().Format(time.RFC3339),
		"component":       "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("SIGNATURE_VALIDATION: %s", string(metricsJSON))
}
