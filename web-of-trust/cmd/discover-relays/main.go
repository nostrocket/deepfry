package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip11"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type RelayTestResult struct {
	URL            string
	NIP11Latency   time.Duration
	ConnectLatency time.Duration
	SubLatency     time.Duration
	TotalLatency   time.Duration
	Passed         bool
	Error          string
}

const (
	apiURL         = "https://api.nostr.watch/v1/online"
	nip11Timeout   = 5 * time.Second
	connectTimeout = 5 * time.Second
	subTimeout     = 10 * time.Second
	apiTimeout     = 15 * time.Second
)

// Seed relays used for NIP-65 discovery fallback when the API is unavailable.
var seedRelays = []string{
	"wss://relay.damus.io",
	"wss://nos.lol",
	"wss://relay.nostr.band",
	"wss://relay.primal.net",
	"wss://purplepag.es",
	"wss://relay.snort.social",
}

func main() {
	count := flag.Int("count", 50, "number of fastest relays to add")
	maxTest := flag.Int("max-test", 500, "max number of discovered relays to test (0 = all)")
	concurrency := flag.Int("concurrency", 50, "number of concurrent relay tests")
	replace := flag.Bool("replace", false, "replace existing relay_urls instead of merging")
	dryRun := flag.Bool("dry-run", false, "print results without modifying config")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	// Step 1: Find config file
	configPath, existingRelays := loadExistingConfig()
	log.Printf("Config file: %s", configPath)
	log.Printf("Existing relays: %d", len(existingRelays))

	// Step 2: Discover relays (API first, NIP-65 fallback)
	discovered, err := discoverRelays(ctx)
	if err != nil {
		log.Fatalf("Failed to discover relays: %v", err)
	}
	log.Printf("Discovered %d unique relays", len(discovered))

	if *maxTest > 0 && len(discovered) > *maxTest {
		rand.Shuffle(len(discovered), func(i, j int) {
			discovered[i], discovered[j] = discovered[j], discovered[i]
		})
		log.Printf("Randomly sampling %d relays for testing (use --max-test 0 to test all)", *maxTest)
		discovered = discovered[:*maxTest]
	}

	// Step 3 & 4: Test relays (NIP-11 ping + connect + kind 3 subscription)
	log.Printf("Testing %d relays with %d concurrent workers...", len(discovered), *concurrency)
	results := testRelays(ctx, discovered, *concurrency)

	var passed []RelayTestResult
	for _, r := range results {
		if r.Passed {
			passed = append(passed, r)
		}
	}
	log.Printf("Relays passed: %d / %d tested", len(passed), len(results))

	if len(passed) == 0 {
		log.Fatal("No relays passed testing. Config not modified.")
	}

	// Step 5: Rank and select top N
	sort.Slice(passed, func(i, j int) bool {
		return passed[i].TotalLatency < passed[j].TotalLatency
	})
	if len(passed) > *count {
		passed = passed[:*count]
	}

	// Print results table
	fmt.Printf("\n%-4s %-50s %10s %10s %10s %10s\n", "Rank", "Relay", "NIP-11", "Connect", "Sub", "Total")
	fmt.Println(strings.Repeat("-", 98))
	for i, r := range passed {
		fmt.Printf("%-4d %-50s %10s %10s %10s %10s\n",
			i+1, r.URL,
			r.NIP11Latency.Round(time.Millisecond),
			r.ConnectLatency.Round(time.Millisecond),
			r.SubLatency.Round(time.Millisecond),
			r.TotalLatency.Round(time.Millisecond))
	}

	if *dryRun {
		fmt.Println("\n[dry-run] Config not modified.")
		return
	}

	// Step 6: Merge and write config
	newURLs := make([]string, len(passed))
	for i, r := range passed {
		newURLs[i] = r.URL
	}

	var finalURLs []string
	if *replace {
		// In replace mode, keep existing ws:// (local) relays
		for _, u := range existingRelays {
			if strings.HasPrefix(u, "ws://") {
				finalURLs = append(finalURLs, u)
			}
		}
		finalURLs = append(finalURLs, newURLs...)
	} else {
		existingSet := make(map[string]bool)
		for _, u := range existingRelays {
			existingSet[nostr.NormalizeURL(u)] = true
		}
		finalURLs = append(finalURLs, existingRelays...)
		for _, u := range newURLs {
			if !existingSet[nostr.NormalizeURL(u)] {
				finalURLs = append(finalURLs, u)
			}
		}
	}

	if err := writeRelayURLsToConfig(configPath, finalURLs); err != nil {
		log.Fatalf("Failed to write config: %v", err)
	}

	added := len(finalURLs) - len(existingRelays)
	if *replace {
		log.Printf("Wrote %d relay URLs to %s (replaced)", len(finalURLs), configPath)
	} else {
		log.Printf("Added %d new relay URLs to %s (total: %d)", added, configPath, len(finalURLs))
	}
}

func loadExistingConfig() (string, []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Could not determine home directory: ", err)
	}

	viper.SetConfigName("web-of-trust")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(filepath.Join(homeDir, "deepfry"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Fatal("No config file found. Run the crawler first to generate one, or create web-of-trust.yaml manually.")
		}
		log.Fatalf("Error reading config: %v", err)
	}

	configPath := viper.ConfigFileUsed()
	relays := viper.GetStringSlice("relay_urls")
	return configPath, relays
}

// discoverRelays tries the nostr.watch API first, falls back to NIP-65 relay
// list discovery by querying seed relays for kind 10002 events.
func discoverRelays(ctx context.Context) ([]string, error) {
	urls, err := discoverFromAPI(ctx)
	if err != nil {
		log.Printf("API discovery failed (%v), falling back to NIP-65 relay list discovery...", err)
		urls, err = discoverFromNIP65(ctx)
		if err != nil {
			return nil, fmt.Errorf("all discovery methods failed: %w", err)
		}
	}
	return urls, nil
}

func discoverFromAPI(ctx context.Context) ([]string, error) {
	log.Println("Trying nostr.watch API...")
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching relay list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var rawURLs []string
	if err := json.Unmarshal(body, &rawURLs); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return normalizeAndDedup(rawURLs), nil
}

// discoverFromNIP65 connects to seed relays and fetches kind 10002 (relay list
// metadata) events to discover relay URLs that real users actually publish.
func discoverFromNIP65(ctx context.Context) ([]string, error) {
	log.Println("Discovering relays via NIP-65 (kind 10002) from seed relays...")

	var allURLs []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, seed := range seedRelays {
		wg.Add(1)
		go func(seedURL string) {
			defer wg.Done()

			connCtx, connCancel := context.WithTimeout(ctx, connectTimeout)
			defer connCancel()

			relay, err := nostr.RelayConnect(connCtx, seedURL)
			if err != nil {
				log.Printf("  seed %s: connect failed: %v", seedURL, err)
				return
			}
			defer relay.Close()

			subCtx, subCancel := context.WithTimeout(ctx, 15*time.Second)
			defer subCancel()

			sub, err := relay.Subscribe(subCtx, nostr.Filters{nostr.Filter{
				Kinds: []int{10002},
				Limit: 500,
			}})
			if err != nil {
				log.Printf("  seed %s: subscribe failed: %v", seedURL, err)
				return
			}
			defer sub.Unsub()

			var relayURLs []string
			for {
				select {
				case event, ok := <-sub.Events:
					if !ok {
						mu.Lock()
						allURLs = append(allURLs, relayURLs...)
						mu.Unlock()
						log.Printf("  seed %s: found %d relay URLs", seedURL, len(relayURLs))
						return
					}
					for _, tag := range event.Tags {
						if len(tag) >= 2 && tag[0] == "r" {
							relayURLs = append(relayURLs, tag[1])
						}
					}
				case <-sub.EndOfStoredEvents:
					mu.Lock()
					allURLs = append(allURLs, relayURLs...)
					mu.Unlock()
					log.Printf("  seed %s: found %d relay URLs", seedURL, len(relayURLs))
					return
				case <-subCtx.Done():
					mu.Lock()
					allURLs = append(allURLs, relayURLs...)
					mu.Unlock()
					log.Printf("  seed %s: timeout, got %d relay URLs so far", seedURL, len(relayURLs))
					return
				}
			}
		}(seed)
	}

	wg.Wait()

	if len(allURLs) == 0 {
		return nil, fmt.Errorf("no relays discovered from any seed relay")
	}

	return normalizeAndDedup(allURLs), nil
}

func normalizeAndDedup(urls []string) []string {
	seen := make(map[string]bool)
	var filtered []string
	for _, u := range urls {
		normalized := nostr.NormalizeURL(u)
		if !strings.HasPrefix(normalized, "wss://") {
			continue
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		filtered = append(filtered, normalized)
	}
	return filtered
}

func testRelays(ctx context.Context, urls []string, concurrency int) []RelayTestResult {
	results := make([]RelayTestResult, len(urls))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var tested int64
	var mu sync.Mutex
	total := len(urls)

	for i, url := range urls {
		wg.Add(1)
		go func(idx int, relayURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = testSingleRelay(ctx, relayURL)
			mu.Lock()
			tested++
			if tested%50 == 0 || tested == int64(total) {
				log.Printf("  progress: %d / %d relays tested", tested, total)
			}
			mu.Unlock()
		}(i, url)
	}

	wg.Wait()
	return results
}

func testSingleRelay(ctx context.Context, url string) RelayTestResult {
	result := RelayTestResult{URL: url}

	// Test 1: NIP-11 info document fetch
	nip11Ctx, nip11Cancel := context.WithTimeout(ctx, nip11Timeout)
	start := time.Now()
	_, err := nip11.Fetch(nip11Ctx, url)
	nip11Cancel()
	result.NIP11Latency = time.Since(start)

	if err != nil {
		result.Error = fmt.Sprintf("NIP-11 failed: %v", err)
		return result
	}

	// Test 2: WebSocket connect
	connCtx, connCancel := context.WithTimeout(ctx, connectTimeout)
	start = time.Now()
	relay, err := nostr.RelayConnect(connCtx, url)
	connCancel()
	result.ConnectLatency = time.Since(start)

	if err != nil {
		result.Error = fmt.Sprintf("connect failed: %v", err)
		return result
	}
	defer relay.Close()

	// Test 3: Kind 3 subscription
	subCtx, subCancel := context.WithTimeout(ctx, subTimeout)
	defer subCancel()

	start = time.Now()
	sub, err := relay.Subscribe(subCtx, nostr.Filters{nostr.Filter{
		Kinds: []int{3},
		Limit: 1,
	}})
	if err != nil {
		result.Error = fmt.Sprintf("subscribe failed: %v", err)
		return result
	}
	defer sub.Unsub()

	select {
	case _, ok := <-sub.Events:
		if ok {
			result.SubLatency = time.Since(start)
		} else {
			result.Error = "events channel closed"
			return result
		}
	case <-sub.EndOfStoredEvents:
		result.SubLatency = time.Since(start)
	case <-subCtx.Done():
		result.Error = "subscription timeout"
		return result
	}

	result.TotalLatency = result.NIP11Latency + result.ConnectLatency + result.SubLatency
	result.Passed = true
	return result
}

func writeRelayURLsToConfig(configPath string, urls []string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config file: %w", err)
	}

	var config yaml.Node
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	if config.Kind != yaml.DocumentNode || len(config.Content) == 0 {
		return fmt.Errorf("unexpected YAML structure")
	}
	root := config.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at root")
	}

	// Build new sequence node for relay_urls
	seq := &yaml.Node{
		Kind: yaml.SequenceNode,
		Tag:  "!!seq",
	}
	for _, u := range urls {
		seq.Content = append(seq.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: u,
		})
	}

	// Find existing relay_urls key and replace its value
	found := false
	for i := 0; i < len(root.Content)-1; i += 2 {
		if root.Content[i].Value == "relay_urls" {
			root.Content[i+1] = seq
			found = true
			break
		}
	}

	if !found {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "relay_urls"},
			seq,
		)
	}

	out, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("marshaling YAML: %w", err)
	}

	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	return nil
}
