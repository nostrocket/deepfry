package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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
	debug         bool
	dbUpdateMutex sync.Mutex
}

type Config struct {
	RelayURLs  []string // Changed from RelayURL to RelayURLs
	DgraphAddr string
	Timeout    time.Duration
	Debug      bool
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
		if cfg.Debug {
			log.Printf("Connected to relay: %s", url)
		}
	}

	if len(relays) == 0 {
		dgClient.Close()
		return nil, fmt.Errorf("failed to connect to any relays")
	}

	log.Printf("Connected to %d/%d relays", len(relays), len(cfg.RelayURLs))

	return &Crawler{
		relays:        relays,
		relayURLs:     connectedURLs,
		dgClient:      dgClient,
		timeout:       cfg.Timeout,
		debug:         cfg.Debug,
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

func (c *Crawler) FetchAndUpdateFollows(relayContext context.Context, pubkeys map[string]int64) error {
	// Extract pubkey strings from the map
	authors := make([]string, 0, len(pubkeys))
	for pubkey := range pubkeys {
		authors = append(authors, pubkey)
	}

	// Create filter for kind 3 events from all specified pubkeys
	filter := nostr.Filter{
		Authors: authors,
		Kinds:   []int{3},
		Limit:   len(pubkeys), // Allow one event per pubkey
	}

	// Query all relays concurrently
	var wg sync.WaitGroup
	eventsChan := make(chan *nostr.Event, len(pubkeys)*len(c.relays))
	errorsChan := make(chan error, len(c.relays))

	// Set timeout context
	relayContext, cancel := context.WithTimeout(relayContext, c.timeout)
	defer cancel()

	// Launch goroutines for each relay
	for i, relay := range c.relays {
		wg.Add(1)
		go func(relay *nostr.Relay, relayURL string) {
			defer wg.Done()
			err := c.queryRelay(relayContext, relay, relayURL, filter, eventsChan)
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

	// Map to keep track of processed event IDs
	processedEventIDs := make(map[string]struct{})
	// Process events from all relays using a switch loop
	for {
		select {
		case event, ok := <-eventsChan:
			c.dbUpdateMutex.Lock()
			if !ok {
				// Channel closed, exit the loop
				if c.debug {
					log.Printf("Processed follows for %d pubkeys across %d relays", len(processedEventIDs), len(c.relays))
				}
				c.dbUpdateMutex.Unlock()
				return nil
			}

			if event == nil {
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Skip if we've already processed this event ID
			if _, exists := processedEventIDs[event.ID]; exists {
				if c.debug {
					log.Printf("Skipping already processed event: %s", event.ID)
				}
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Validate event signature
			if ok, err := event.CheckSignature(); !ok {
				log.Printf("WARN: Invalid signature for event %s from pubkey %s: %v", event.ID, event.PubKey, err)
				c.logSignatureValidationMetrics(event.PubKey, false)
				c.dbUpdateMutex.Unlock()
				continue
			}
			c.logSignatureValidationMetrics(event.PubKey, true)

			if event.CreatedAt <= nostr.Timestamp(pubkeys[event.PubKey]) {
				fmt.Println("already have newer event for " + event.PubKey)
				c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey)
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Process the event

			if err := c.updateFollowsFromEvent(relayContext, event); err != nil {
				c.dbUpdateMutex.Unlock()
				return fmt.Errorf("failed to update follows for pubkey %s: %w", event.PubKey, err)
			}
			processedEventIDs[event.ID] = struct{}{}
			c.dbUpdateMutex.Unlock()

		case err, ok := <-errorsChan:
			if !ok {
				// Error channel closed
				continue
			}
			log.Printf("WARN: Relay error: %v", err)
			log.Printf("subscription filters: \n %s", filter)

			// case <-relayContext.Done():
			// 	return relayContext.Err()
		}
	}
}

func (c *Crawler) queryRelay(ctx context.Context, relay *nostr.Relay, relayURL string, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
	sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %w", relayURL, err))
		return err
	}
	defer sub.Unsub()

	if c.debug {
		log.Printf("Querying relay %s for %d pubkeys", relayURL, len(filter.Authors))
	}

	for {
		select {
		case event := <-sub.Events:
			if event != nil {
				if c.debug {
					log.Printf("Found kind 3 event from relay %s: %s, created_at: %d, pubkey: %s",
						relayURL, event.ID, event.CreatedAt, event.PubKey)
				}
				eventsChan <- event
			}
		case <-sub.EndOfStoredEvents:
			if c.debug {
				log.Printf("EOSE received from relay %s", relayURL)
			}
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

	if c.debug {
		log.Printf("Found %d follows in event (%d unique, %d duplicates)", len(rawFollows), uniqueFollowsCount, duplicatesCount)
	}

	// Warn if this is an unusually large follow list
	if uniqueFollowsCount > 1000 && c.debug {
		log.Printf("WARN: Large follow list detected (%d follows) for pubkey %s - this may cause timeouts", uniqueFollowsCount, event.PubKey)
	}

	// For very large follow lists, we might want to chunk the processing
	// But for now, try the single batch approach with longer timeout
	// Process all follows in one batch operation
	err := c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, c.debug)
	if err != nil {
		// If we get a timeout error and have a large follow list, we could implement chunking here
		if uniqueFollowsCount > 500 && strings.Contains(err.Error(), "DeadlineExceeded") {
			log.Printf("WARN: Timeout detected for large follow list (%d follows). Consider implementing chunked processing.", uniqueFollowsCount)
		}
		return fmt.Errorf("failed to add follows batch: %w", err)
	}

	if c.debug {
		log.Printf("Processed %d/%d follows", uniqueFollowsCount, uniqueFollowsCount)
		c.logMetrics(event.PubKey, uniqueFollowsCount, duplicatesCount)
	}

	// Log metrics

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
	// Only log signature validation metrics in debug mode
	if !c.debug {
		return
	}

	metrics := map[string]interface{}{
		"pubkey":          pubkey,
		"signature_valid": valid,
		"validated_at":    time.Now().Format(time.RFC3339),
		"component":       "web-of-trust-crawler",
		"metric_type":     "signature_validation",
	}

	metricsJSON, _ := json.Marshal(metrics)
	log.Printf("DEBUG_METRICS: %s", string(metricsJSON))
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
