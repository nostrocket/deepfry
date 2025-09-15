package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"web-of-trust/pkg/dgraph"
)

func main() {
	ctx := context.Background()

	// Connect to Dgraph (default address)
	client, err := dgraph.NewClient("localhost:9080")
	if err != nil {
		log.Fatalf("Failed to create Dgraph client: %v", err)
	}
	defer client.Close()

	// Query pubkeys with at least 3 followers
	popularPubkeys, err := client.GetPubkeysWithMinFollowers(ctx, 3)
	if err != nil {
		log.Fatalf("Failed to query popular pubkeys: %v", err)
	}

	// Generate CSV filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("popular_pubkeys_%s.csv", timestamp)
	csvPath := filepath.Join(".", filename)

	// Create CSV file
	file, err := os.Create(csvPath)
	if err != nil {
		log.Fatalf("Failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	if err := writer.Write([]string{"pubkey"}); err != nil {
		log.Fatalf("Failed to write CSV header: %v", err)
	}

	// Write pubkeys to CSV
	for pubkey := range popularPubkeys {
		if err := writer.Write([]string{pubkey}); err != nil {
			log.Fatalf("Failed to write pubkey to CSV: %v", err)
		}
	}

	// Print CSV location and exit
	absPath, _ := filepath.Abs(csvPath)
	fmt.Printf("CSV exported to: %s\n", absPath)
	fmt.Printf("Found %d pubkeys with 3+ followers\n", len(popularPubkeys))
}
