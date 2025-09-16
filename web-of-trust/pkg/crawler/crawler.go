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
	relays        []*nostr.Relay
	relayURLs     []string
	dgClient      *dgraph.Client
	timeout       time.Duration
	dbUpdateMutex sync.Mutex
}

type Config struct {
	RelayURLs  []string // Changed from RelayURL to RelayURLs
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

	// Connect to all relays
	var relays []*nostr.Relay
	var connectedURLs []string

	for _, url := range cfg.RelayURLs {
		relay, err := nostr.RelayConnect(context.Background(), url)
		if err != nil {
			log.Printf("WARN: Failed to connect to relay %s: %v", url, err)
			continue
		}
		relays = append(relays, relay)
		connectedURLs = append(connectedURLs, url)
		log.Printf("Connected to relay: %s", url)
	}

	if len(relays) == 0 {
		dgClient.Close()
		return nil, fmt.Errorf("failed to connect to any relay")
	}

	log.Printf("Connected to %d/%d relays", len(relays), len(cfg.RelayURLs))

	return &Crawler{
		relays:        relays,
		relayURLs:     connectedURLs,
		dgClient:      dgClient,
		timeout:       cfg.Timeout,
		dbUpdateMutex: sync.Mutex{},
	}, nil
}

func (c *Crawler) Close() {
	for _, relay := range c.relays {
		if relay != nil {
			relay.Close()
		}
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

	// Query all relays concurrently
	var wg sync.WaitGroup
	eventsChan := make(chan *nostr.Event, len(pubkeys)*len(c.relays))
	errorsChan := make(chan error, len(c.relays))

	// Set timeout context
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Launch goroutines for each relay
	for i, relay := range c.relays {
		wg.Add(1)
		go func(relay *nostr.Relay, relayURL string) {
			defer wg.Done()
			err := c.queryRelay(ctx, relay, relayURL, filter, eventsChan)
			if err != nil {
				errorsChan <- fmt.Errorf("relay %s: %w", relayURL, err)
			}
		}(relay, c.relayURLs[i])
	}

	// Close channels when all goroutines complete
	go func() {
		wg.Wait()
		close(eventsChan)
		close(errorsChan)
	}()

	// Collect and deduplicate events by pubkey (keeping the newest)
	eventsByPubkey := make(map[string]*nostr.Event)
	processed := 0

	// Process events from all relays
	for event := range eventsChan {
		if event == nil {
			continue
		}

		// Validate event signature
		if ok, err := event.CheckSignature(); !ok {
			log.Printf("WARN: Invalid signature for event %s from pubkey %s: %v", event.ID, event.PubKey, err)
			c.logSignatureValidationMetrics(event.PubKey, false)
			continue
		}
		c.logSignatureValidationMetrics(event.PubKey, true)

		// Keep only the newest event per pubkey
		existing, exists := eventsByPubkey[event.PubKey]
		if !exists || event.CreatedAt > existing.CreatedAt {
			eventsByPubkey[event.PubKey] = event
		}
	}

	// Log any relay errors (non-blocking)
	for err := range errorsChan {
		log.Printf("WARN: Relay error: %v", err)
	}

	// Process the deduplicated events
	for pubkey, event := range eventsByPubkey {
		c.dbUpdateMutex.Lock()
		if err := c.updateFollowsFromEvent(ctx, event); err != nil {
			c.dbUpdateMutex.Unlock()
			return fmt.Errorf("failed to update follows for pubkey %s: %w", pubkey, err)
		}
		c.dbUpdateMutex.Unlock()
		processed++
	}

	log.Printf("Processed follows for %d/%d pubkeys across %d relays", processed, len(pubkeys), len(c.relays))
	return nil
}

func (c *Crawler) queryRelay(ctx context.Context, relay *nostr.Relay, relayURL string, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
	sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %w", relayURL, err))
		return err
	}
	defer sub.Unsub()

	log.Printf("Querying relay %s for %d pubkeys", relayURL, len(filter.Authors))

	for {
		select {
		case event := <-sub.Events:
			if event != nil {
				log.Printf("Found kind 3 event from relay %s: %s, created_at: %d, pubkey: %s",
					relayURL, event.ID, event.CreatedAt, event.PubKey)
				eventsChan <- event
			}
		case <-sub.EndOfStoredEvents:
			log.Printf("EOSE received from relay %s", relayURL)
			return nil
		case <-sub.Context.Done():
			if err := sub.Context.Err(); err != nil && err != context.Canceled {
				c.logRelayError("subscription_context_error", fmt.Errorf("relay %s: %w", relayURL, err))
				return err
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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

func (c *Crawler) logRelayError(errorType string, err error) {
	metrics := map[string]interface{}{
		"error_type":  errorType,
		"error":       err.Error(),
		"occurred_at": time.Now().Format(time.RFC3339),
		"component":   "web-of-trust-crawler",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("RELAY_ERROR: %s", string(metricsJSON))
}
