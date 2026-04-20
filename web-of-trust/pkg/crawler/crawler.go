package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr"
)

type subscriptionError struct {
	err error
}

func (e *subscriptionError) Error() string { return e.err.Error() }
func (e *subscriptionError) Unwrap() error { return e.err }

type transportError struct {
	err error
}

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

const (
	initialBackoff         = 30 * time.Second
	maxBackoff             = 5 * time.Minute
	maxConsecutiveFailures = 5
)

type relayState struct {
	url      string
	conn     *nostr.Relay
	alive    bool
	backoff  time.Duration
	retryAt  time.Time
	failures atomic.Int32
}

type Crawler struct {
	relays        []*relayState
	forwardRelay  *relayState
	dgClient      *dgraph.Client
	timeout       time.Duration
	debug         bool
	dbUpdateMutex sync.Mutex
	onConnectFail func(url string)
}

type Config struct {
	RelayURLs       []string
	DgraphAddr      string
	Timeout         time.Duration
	Debug           bool
	ForwardRelayURL string
	OnConnectFail   func(url string)
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
	var relays []*relayState
	connected := 0

	for _, url := range cfg.RelayURLs {
		relay, err := nostr.RelayConnect(context.Background(), url)
		if err != nil {
			log.Printf("WARN: Failed to connect to relay %s, removing from config: %v", url, err)
			if cfg.OnConnectFail != nil {
				cfg.OnConnectFail(url)
			}
			continue
		}
		rs := &relayState{url: url, backoff: initialBackoff, conn: relay, alive: true}
		relays = append(relays, rs)
		connected++
		if cfg.Debug {
			log.Printf("Connected to relay: %s", url)
		}
	}

	if connected == 0 {
		dgClient.Close()
		return nil, fmt.Errorf("failed to connect to any relays")
	}

	log.Printf("Connected to %d/%d relays", connected, len(cfg.RelayURLs))

	c := &Crawler{
		relays:        relays,
		dgClient:      dgClient,
		timeout:       cfg.Timeout,
		debug:         cfg.Debug,
		onConnectFail: cfg.OnConnectFail,
	}

	// Connect to forward relay if configured
	if cfg.ForwardRelayURL != "" {
		rs := &relayState{url: cfg.ForwardRelayURL, backoff: initialBackoff}
		relay, err := nostr.RelayConnect(context.Background(), cfg.ForwardRelayURL)
		if err != nil {
			log.Printf("WARN: Failed to connect to forward relay %s: %v (will retry later)", cfg.ForwardRelayURL, err)
		} else {
			rs.conn = relay
			rs.alive = true
			log.Printf("Connected to forward relay: %s", cfg.ForwardRelayURL)
		}
		c.forwardRelay = rs
	}

	return c, nil
}

func (c *Crawler) Close() {
	for _, rs := range c.relays {
		if rs.conn != nil {
			rs.conn.Close()
		}
	}
	if c.forwardRelay != nil && c.forwardRelay.conn != nil {
		c.forwardRelay.conn.Close()
	}
	if c.dgClient != nil {
		c.dgClient.Close()
	}
}

func (c *Crawler) forwardEvent(ctx context.Context, event *nostr.Event) {
	if c.forwardRelay == nil || !c.forwardRelay.alive {
		return
	}
	err := c.forwardRelay.conn.Publish(ctx, *event)
	if err != nil {
		log.Printf("WARN: Failed to forward event %s to %s: %v", event.ID, c.forwardRelay.url, err)
		if c.forwardRelay.conn != nil {
			c.forwardRelay.conn.Close()
		}
		c.forwardRelay.conn = nil
		c.forwardRelay.alive = false
		c.forwardRelay.retryAt = time.Now().Add(c.forwardRelay.backoff)
		c.forwardRelay.backoff *= 2
		if c.forwardRelay.backoff > maxBackoff {
			c.forwardRelay.backoff = maxBackoff
		}
	} else if c.debug {
		log.Printf("Forwarded event %s to %s", event.ID, c.forwardRelay.url)
	}
}

func (c *Crawler) markRelayDead(url string) {
	kept := c.relays[:0]
	for _, rs := range c.relays {
		if rs.url != url {
			kept = append(kept, rs)
			continue
		}
		if rs.conn != nil {
			rs.conn.Close()
		}
		rs.conn = nil
		rs.alive = false
		failures := int(rs.failures.Add(1))
		if failures >= maxConsecutiveFailures {
			log.Printf("Relay %s failed %d consecutive times, removing from config", url, failures)
			if c.onConnectFail != nil {
				c.onConnectFail(url)
			}
			continue
		}
		rs.retryAt = time.Now().Add(rs.backoff)
		log.Printf("Relay %s marked dead (failure %d/%d), next retry in %v", url, failures, maxConsecutiveFailures, rs.backoff)
		rs.backoff *= 2
		if rs.backoff > maxBackoff {
			rs.backoff = maxBackoff
		}
		kept = append(kept, rs)
	}
	c.relays = kept
}

func (c *Crawler) ReconnectRelays(ctx context.Context) {
	kept := c.relays[:0]
	for _, rs := range c.relays {
		if rs.alive {
			kept = append(kept, rs)
			continue
		}
		if time.Now().Before(rs.retryAt) {
			if c.debug {
				log.Printf("Skipping reconnect for %s, next retry at %v", rs.url, rs.retryAt.Format(time.RFC3339))
			}
			kept = append(kept, rs)
			continue
		}
		relay, err := nostr.RelayConnect(ctx, rs.url)
		if err != nil {
			log.Printf("WARN: Reconnect to %s failed, removing from config: %v", rs.url, err)
			if c.onConnectFail != nil {
				c.onConnectFail(rs.url)
			}
			continue
		}
		rs.conn = relay
		rs.alive = true
		rs.backoff = initialBackoff
		rs.failures.Store(0)
		kept = append(kept, rs)
		log.Printf("Reconnected to relay: %s", rs.url)
	}
	c.relays = kept

	// Reconnect forward relay if needed
	if c.forwardRelay != nil && !c.forwardRelay.alive {
		rs := c.forwardRelay
		if time.Now().Before(rs.retryAt) {
			if c.debug {
				log.Printf("Skipping reconnect for forward relay %s, next retry at %v", rs.url, rs.retryAt.Format(time.RFC3339))
			}
			return
		}
		relay, err := nostr.RelayConnect(ctx, rs.url)
		if err != nil {
			rs.retryAt = time.Now().Add(rs.backoff)
			log.Printf("WARN: Reconnect to forward relay %s failed, next retry in %v: %v", rs.url, rs.backoff, err)
			rs.backoff *= 2
			if rs.backoff > maxBackoff {
				rs.backoff = maxBackoff
			}
			return
		}
		rs.conn = relay
		rs.alive = true
		rs.backoff = initialBackoff
		log.Printf("Reconnected to forward relay: %s", rs.url)
	}
}

// FetchAndUpdateFollows queries relays for kind 3 events for the given pubkeys
// and updates the database. Returns the number of pubkeys that had events.
func (c *Crawler) FetchAndUpdateFollows(relayContext context.Context, pubkeys map[string]int64) (int, error) {

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

	type relayError struct {
		url string
		err error
	}

	// Query all alive relays concurrently
	var wg sync.WaitGroup
	eventsChan := make(chan *nostr.Event, len(pubkeys)*len(c.relays))
	errorsChan := make(chan relayError, len(c.relays))

	// Set timeout context for relay operations only
	relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
	defer cancel()

	// Launch goroutines for each alive relay
	for _, rs := range c.relays {
		if !rs.alive {
			continue
		}
		wg.Add(1)
		go func(rs *relayState) {
			defer wg.Done()
			err := c.queryRelay(relayQueryContext, rs.conn, rs.url, filter, eventsChan)
			if err != nil {
				errorsChan <- relayError{url: rs.url, err: err}
				return
			}
			rs.failures.Store(0)
		}(rs)
	}

	// Close channels when all goroutines complete
	go func() {
		wg.Wait()
		close(eventsChan)
		close(errorsChan)
	}()

	// Map to keep track of processed event IDs
	processedEventIDs := make(map[string]struct{})
	// Track unique pubkeys that had events returned
	pubkeysWithEvents := make(map[string]struct{})
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
				return len(pubkeysWithEvents), relayQueryContext.Err()
			}

		case <-relayContext.Done():
			// The main context was cancelled (external cancellation)
			if c.debug {
				log.Printf("Main context cancelled while processing events: %v", relayContext.Err())
			}
			return len(pubkeysWithEvents), relayContext.Err()

		case event, ok := <-eventsChan:
			c.dbUpdateMutex.Lock()
			if !ok {
				// Channel closed, exit the loop
				if c.debug {
					log.Printf("Processed follows for %d pubkeys across %d relays", len(processedEventIDs), len(c.relays))
				}
				c.dbUpdateMutex.Unlock()
				return len(pubkeysWithEvents), nil
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

			c.forwardEvent(relayContext, event)

			pubkeysWithEvents[event.PubKey] = struct{}{}

			if event.CreatedAt <= nostr.Timestamp(pubkeys[event.PubKey]) {
				if c.debug {
					fmt.Println("already have newer event for " + event.PubKey)
				}
				c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey)
				c.dbUpdateMutex.Unlock()
				continue
			}

			// Process the event using original context (no relay timeout)
			if err := c.updateFollowsFromEvent(relayContext, event); err != nil {
				c.dbUpdateMutex.Unlock()
				return len(pubkeysWithEvents), fmt.Errorf("failed to update follows for pubkey %s: %w", event.PubKey, err)
			}
			processedEventIDs[event.ID] = struct{}{}
			c.dbUpdateMutex.Unlock()

		case re, ok := <-errorsChan:
			if !ok {
				// Error channel closed
				continue
			}

			// Don't report context cancellation as relay error
			if strings.Contains(re.err.Error(), "context canceled") ||
				strings.Contains(re.err.Error(), "context deadline exceeded") {
				if c.debug {
					log.Printf("Relay query interrupted: %v", re.err)
				}
				continue
			}

			var subErr *subscriptionError
			var transErr *transportError
			switch {
			case errors.As(re.err, &subErr):
				log.Printf("WARN: Subscription failed: %v", re.err)
			case errors.As(re.err, &transErr):
				log.Printf("WARN: Connection timed out: %v", re.err)
				c.markRelayDead(re.url)
			default:
				log.Printf("WARN: Relay error: %v", re.err)
			}
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
		// Strip the verbose filter dump that go-nostr embeds in Subscribe errors
		cleanErr := cleanSubscribeError(err)
		// "not connected" / "failed to write" means the underlying websocket died —
		// treat as transport failure so the relay gets marked dead and queued for reconnection
		if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write") {
			c.logRelayError("connection_lost", fmt.Errorf("relay %s: %s", relayURL, cleanErr))
			return &transportError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}
		}
		c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %s", relayURL, cleanErr))
		return &subscriptionError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}
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
			return &transportError{err: fmt.Errorf("relay %s: %w", relayURL, err)}
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

// cleanSubscribeError extracts the root cause from a go-nostr Subscribe error,
// stripping the verbose filter dump that go-nostr includes via %v formatting.
// e.g. "couldn't subscribe to [{kinds:[3] authors:[...]}] at wss://...: failed to write: connection closed"
// becomes "failed to write: connection closed"
func cleanSubscribeError(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "couldn't subscribe to"); idx != -1 {
		if atIdx := strings.Index(msg[idx:], "]: "); atIdx != -1 {
			return strings.TrimSpace(msg[idx+atIdx+3:])
		}
	}
	return msg
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
