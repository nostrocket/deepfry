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
		// Validate pubkey format before querying
		if _, err := nostr.GetPublicKey(pubkey); err != nil {
			if c.debug {
				log.Printf("Skipping invalid pubkey: %s, error: %v", pubkey, err)
			}
			continue
		}
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

	// Set timeout context for relay operations only
	relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
	defer cancel()

	// Launch goroutines for each relay
	for i, relay := range c.relays {
		wg.Add(1)
		go func(relay *nostr.Relay, relayURL string) {
			defer wg.Done()
			err := c.queryRelay(relayQueryContext, relay, relayURL, filter, eventsChan)
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
		case <-relayQueryContext.Done():
			// The relay query context was cancelled (timeout or external cancellation)
			if c.debug {
				log.Printf("Relay query context cancelled while processing events: %v", relayQueryContext.Err())
			}
			// Don't return error if it's just relay timeout - we can still process events we already got
			if relayQueryContext.Err() == context.DeadlineExceeded {
				if c.debug {
					log.Printf("Relay query timeout reached, but continuing to process received events")
				}
				// Continue processing events that were already received
			} else {
				return relayQueryContext.Err()
			}

		case <-relayContext.Done():
			// The main context was cancelled (external cancellation)
			if c.debug {
				log.Printf("Main context cancelled while processing events: %v", relayContext.Err())
			}
			return relayContext.Err()

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

			// Process the event using original context (no relay timeout)
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

			// Don't report context cancellation as relay error
			if strings.Contains(err.Error(), "context canceled") ||
				strings.Contains(err.Error(), "context deadline exceeded") {
				if c.debug {
					log.Printf("Relay query interrupted: %v", err)
				}
				continue
			}

			log.Printf("WARN: Relay error: %v", err)
			log.Printf("subscription filters: \n %s", filter)
		}
	}
}

func (c *Crawler) queryRelay(ctx context.Context, relay *nostr.Relay, relayURL string, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
	// Create a subscription with the context that can be cancelled from the main thread
	sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
	if err != nil {
		// Skip logging for context cancellation, as it's expected during graceful shutdown
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %w", relayURL, err))
		return err
	}
	// Ensure subscription is unsubscribed when this function returns
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
				// Check for context cancellation before sending to channel to avoid blocking
				select {
				case eventsChan <- event:
					// Event sent successfully
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case <-sub.EndOfStoredEvents:
			if c.debug {
				log.Printf("EOSE received from relay %s", relayURL)
			}
			return nil
		case <-sub.Context.Done():
			err := sub.Context.Err()
			if err != nil && err != context.Canceled {
				c.logRelayError("subscription_context_error", fmt.Errorf("relay %s: %w", relayURL, err))
			}
			return err
		case <-ctx.Done():
			// External cancellation (ctrl+c or timeout) - return without error logging
			if c.debug {
				log.Printf("Relay query for %s cancelled externally", relayURL)
			}
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
			pubkey := tag[1]

			// Validate pubkey format using nbd-wtf/go-nostr
			if _, err := nostr.GetPublicKey(pubkey); err != nil {
				if c.debug {
					log.Printf("Invalid pubkey in follow list: %s, error: %v", pubkey, err)
				}
				continue
			}

			rawFollows = append(rawFollows, pubkey)
			followsMap[pubkey] = struct{}{}
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

	// For very large follow lists, we'll use chunked processing to avoid timeouts
	var err error
	if uniqueFollowsCount > 10000 {
		// Process large follow lists in chunks
		err = c.processFollowsInChunks(ctx, event.PubKey, int64(event.CreatedAt), followsMap)
	} else {
		// For smaller follow lists, process all follows in one batch operation
		err = c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, c.debug)
	}

	if err != nil {
		return fmt.Errorf("failed to add follows: %w", err)
	}

	if c.debug {
		log.Printf("Processed %d follows for pubkey %s", uniqueFollowsCount, event.PubKey)
		c.logMetrics(event.PubKey, uniqueFollowsCount, duplicatesCount)
	}

	// Log metrics

	return nil
}

func (c *Crawler) logMetrics(pubkey string, followsCount int, duplicatesCount int) {
	chunked := followsCount > 500
	processingType := "batch"
	if chunked {
		processingType = "chunked"
	}

	metrics := map[string]interface{}{
		"pubkey":           pubkey,
		"follows_count":    followsCount,
		"duplicates_count": duplicatesCount,
		"chunked":          chunked,
		"processing_type":  processingType,
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
