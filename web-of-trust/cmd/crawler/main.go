package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/crawler"
	"web-of-trust/pkg/dgraph"
)

func main() {
	// Create a context that can be cancelled for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Use a WaitGroup to track background operations
	var wg sync.WaitGroup

	// Start signal handler in a goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		sig := <-sigChan
		log.Printf("Received signal: %v, initiating graceful shutdown...", sig)
		cancel() // Cancel the context to stop all operations
	}()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create dgraph client for startup stats and processing
	dgraphClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create dgraph client: %v", err)
	}
	defer dgraphClient.Close()

	// Prompt for forward relay if not configured
	if cfg.ForwardRelayURL == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("No forward relay configured. Enter a relay URL to forward events to (or press Enter to skip): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			cfg.ForwardRelayURL = input
			if err := config.SaveForwardRelayURL(input); err != nil {
				log.Printf("Warning: could not save forward_relay_url to config: %v", err)
			} else {
				log.Printf("Saved forward_relay_url to config file")
			}
		}
	}

	// Create crawler
	crawlerCfg := crawler.Config{
		RelayURLs:       cfg.RelayURLs,
		DgraphAddr:      cfg.DgraphAddr,
		Timeout:         cfg.Timeout,
		Debug:           cfg.Debug,
		ForwardRelayURL: cfg.ForwardRelayURL,
	}

	crawler, err := crawler.New(crawlerCfg)
	if err != nil {
		log.Fatalf("Failed to create crawler: %v", err)
	}
	defer crawler.Close()

	// Statistics for final report
	startTime := time.Now()
	startingPubkeys, _ := dgraphClient.CountPubkeys(ctx)

	// Main processing loop
mainLoop:
	for {
		// Check if shutdown was requested
		select {
		case <-ctx.Done():
			log.Println("Shutdown requested, breaking main loop")
			break mainLoop
		default:
			// Continue execution
		}

		// Get stale pubkeys to process
		pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold)
		if err != nil {
			log.Printf("Error getting stale pubkeys: %v", err)
			break
		}

		totalPubkeys, err := dgraphClient.CountPubkeys(ctx)
		if err != nil {
			log.Printf("Error counting pubkeys: %v", err)
			break
		}

		// Initialize with seed if database is empty
		if totalPubkeys == 0 {
			pubkeys[cfg.SeedPubkey] = 0
			log.Printf("Database is empty, starting with seed pubkey: %s", cfg.SeedPubkey)
		}

		// Exit if no pubkeys to process
		if len(pubkeys) == 0 {
			log.Println("No stale pubkeys found, work complete")
			break
		}

		// Limit batch size to avoid overload
		totalStale := len(pubkeys)
		if totalStale > 500 {
			limitedPubkeys := make(map[string]int64)
			count := 0
			for pk, timestamp := range pubkeys {
				if count >= 500 {
					break
				}
				limitedPubkeys[pk] = timestamp
				count++
			}
			pubkeys = limitedPubkeys
		}

		// Reconnect any dead relays before processing
		crawler.ReconnectRelays(ctx)

		// Process the batch
		hadEvents, err := crawler.FetchAndUpdateFollows(ctx, pubkeys)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("Interrupted while fetching follows: %v", err)
				break
			}
			log.Printf("Failed to fetch and update follows: %v", err)
			break
		}

		staleRemaining := totalStale - len(pubkeys)
		log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d stale remaining | %d total in DB",
			len(pubkeys), hadEvents, staleRemaining, totalPubkeys)
	}

	// Generate final report
	endingPubkeys, _ := dgraphClient.CountPubkeys(ctx)
	generateFinalReport(startingPubkeys, endingPubkeys, startTime, cfg.SeedPubkey)

	// Wait for any background tasks to complete
	log.Println("Waiting for background tasks to complete...")
	wg.Wait()

	log.Println("Shutdown complete")
}

// generateFinalReport outputs statistics about the crawler run
func generateFinalReport(startingPubkeys, endingPubkeys int, startTime time.Time, seedPubkey string) {
	duration := time.Since(startTime)
	log.Printf("--- Final Report ---")
	log.Printf("Seed pubkey: %s", seedPubkey)
	log.Printf("Pubkeys in DB: %d at start, %d at end (%d new)", startingPubkeys, endingPubkeys, endingPubkeys-startingPubkeys)
	log.Printf("Total runtime: %s", duration)
	log.Printf("Crawler shutdown gracefully")
}
