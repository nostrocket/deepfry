package main

//todo: add multiple relay support
import (
	"context"
	"fmt"
	"log"
	"time"

	"web-of-trust/pkg/crawler"
	"web-of-trust/pkg/dgraph"

	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/spf13/viper"
)

type Config struct {
	RelayURLs  []string      `mapstructure:"relay_urls"` // Changed from RelayURL to RelayURLs
	DgraphAddr string        `mapstructure:"dgraph_addr"`
	PubkeyHex  string        `mapstructure:"pubkey"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

func main() {
	cfg, err := loadConfig()
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

	// Default to 24 hours for stale threshold
	threshold := time.Now().Unix() - (24 * 60 * 60)

	// Create crawler
	crawlerCfg := crawler.Config{
		RelayURLs:  cfg.RelayURLs, // Changed from RelayURL to RelayURLs
		DgraphAddr: cfg.DgraphAddr,
		Timeout:    cfg.Timeout,
	}

	c, err := crawler.New(crawlerCfg)
	if err != nil {
		log.Fatalf("Failed to create crawler: %v", err)
	}
	defer c.Close()

	// Fetch follow list
	ctx = context.Background()
	for {
		pubkeys, err := dgraphClient.GetStalePubkeys(ctx, threshold)
		if err != nil {
			panic(err)
		}
		totalPubkeys, err := dgraphClient.CountPubkeys(ctx)
		if err != nil {
			panic(err)
		}
		if totalPubkeys == 0 {
			pubkeys = append(pubkeys, cfg.PubkeyHex)
		}
		if len(pubkeys) == 0 {
			break
		}
		// if len(pubkeys) > 20 {
		// 	pubkeys = pubkeys[0:20]
		// }

		if err := c.FetchAndUpdateFollows(ctx, pubkeys); err != nil {
			log.Printf("Failed to fetch and update follows: %v", err)
			break
		}
	}

	log.Printf("Successfully updated follow list for pubkey: %s", cfg.PubkeyHex)
}

func loadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/web-of-trust/")

	// Set defaults with popular Nostr relays
	viper.SetDefault("relay_urls", []string{
		"wss://relay.damus.io",
		"wss://nos.lol",
		"wss://relay.nostr.band",
		"wss://nostr.wine",
		"wss://relay.current.fyi",
		"wss://nostr-pub.wellorder.net",
		"wss://relay.nostr.info",
		"wss://offchain.pub",
		"wss://brb.io",
		"wss://relay.primal.net",
	})
	viper.SetDefault("dgraph_addr", "localhost:9080")
	viper.SetDefault("timeout", "30s")
	viper.SetDefault("pubkey", "npub1mygerccwqpzyh9pvp6pv44rskv40zutkfs38t0hqhkvnwlhagp6s3psn5p")

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		log.Printf("No config file found, using defaults and flags")
	} else {
		log.Printf("Using config file: %s", viper.ConfigFileUsed())
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	// Validate required fields - now optional since we have a default
	// if cfg.PubkeyHex == "" {
	// 	return nil, fmt.Errorf("pubkey is required in configuration")
	// }

	// Ensure at least one relay URL is provided
	if len(cfg.RelayURLs) == 0 {
		return nil, fmt.Errorf("at least one relay URL is required")
	}

	// Handle both hex and npub formats
	if _, data, err := nip19.Decode(cfg.PubkeyHex); err == nil {
		cfg.PubkeyHex = data.(string)
	}
	// If decode fails, assume it's already hex

	return &cfg, nil
}
