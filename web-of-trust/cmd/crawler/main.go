package main

import (
	"context"
	"log"
	"time"

	"web-of-trust/pkg/config"
	"web-of-trust/pkg/crawler"
	"web-of-trust/pkg/dgraph"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create dgraph client for startup stats
	dgraphClient, err := dgraph.NewClient(cfg.DgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create dgraph client: %v", err)
	}
	defer dgraphClient.Close()

	// Print graph statistics on startup
	ctx := context.Background()

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

	// Fetch follow list
	ctx = context.Background()
	for {
		pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold)
		if err != nil {
			panic(err)
		}
		totalPubkeys, err := dgraphClient.CountPubkeys(ctx)
		if err != nil {
			panic(err)
		}
		if totalPubkeys == 0 {
			pubkeys[cfg.SeedPubkey] = 0
		}
		if len(pubkeys) == 0 {
			break
		}
		if len(pubkeys) > 20 {
			limitedPubkeys := make(map[string]int64)
			count := 0
			for pk, timestamp := range pubkeys {
				if count >= 20 {
					break
				}
				limitedPubkeys[pk] = timestamp
				count++
			}
			pubkeys = limitedPubkeys
		}

		if err := crawler.FetchAndUpdateFollows(ctx, pubkeys); err != nil {
			log.Printf("Failed to fetch and update follows: %v", err)
			break
		}
	}

	log.Printf("Successfully updated follow list for pubkey: %s", cfg.SeedPubkey)
}
