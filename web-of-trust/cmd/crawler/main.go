package main

import (
	"context"
	"log"
	"os"
	"os/signal"
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

	// Create crawler
	crawlerCfg := crawler.Config{
		RelayURLs:  cfg.RelayURLs,
		DgraphAddr: cfg.DgraphAddr,
		Timeout:    cfg.Timeout,
		Debug:      cfg.Debug,
	}

	crawler, err := crawler.New(crawlerCfg)
	if err != nil {
		log.Fatalf("Failed to create crawler: %v", err)
	}
	defer crawler.Close()

	// Statistics for final report
	processedPubkeys := 0
	var startTime = time.Now()

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
		if len(pubkeys) > 500 {
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
			log.Printf("Limited batch to 500 pubkeys (from %d total stale pubkeys)", len(pubkeys))
		}

		// Process the batch
		if err := crawler.FetchAndUpdateFollows(ctx, pubkeys); err != nil {
			if ctx.Err() != nil {
				log.Printf("Interrupted while fetching follows: %v", err)
				break
			}
			log.Printf("Failed to fetch and update follows: %v", err)
			break
		}

		processedPubkeys += len(pubkeys)
		log.Printf("Processed %d pubkeys in this batch, %d total so far", len(pubkeys), processedPubkeys)
	}

	// Generate final report
	generateFinalReport(processedPubkeys, startTime, cfg.SeedPubkey)

	// Wait for any background tasks to complete
	log.Println("Waiting for background tasks to complete...")
	wg.Wait()

	log.Println("Shutdown complete")
}

// generateFinalReport outputs statistics about the crawler run
func generateFinalReport(processedPubkeys int, startTime time.Time, seedPubkey string) {
	duration := time.Since(startTime)
	log.Printf("--- Final Report ---")
	log.Printf("Seed pubkey: %s", seedPubkey)
	log.Printf("Total pubkeys processed: %d", processedPubkeys)
	log.Printf("Total runtime: %s", duration)
	log.Printf("Average processing rate: %.2f pubkeys/second", float64(processedPubkeys)/duration.Seconds())
	log.Printf("Crawler shutdown gracefully")
}
