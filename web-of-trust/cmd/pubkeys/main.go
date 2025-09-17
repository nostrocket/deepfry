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

	// Track total count
	totalCount := 0

	// Query pubkeys with at least 1 follower using pagination (batch size 1000)
	batchSize := 1000
	err = client.GetPubkeysWithMinFollowersPaginated(ctx, 1, batchSize, func(batch []string) error {
		// Write this batch to CSV
		for _, pubkey := range batch {
			if err := writer.Write([]string{pubkey}); err != nil {
				return fmt.Errorf("failed to write pubkey to CSV: %w", err)
			}
		}

		totalCount += len(batch)
		fmt.Printf("Processed %d pubkeys (total: %d)\n", len(batch), totalCount)

		return nil
	})

	if err != nil {
		log.Fatalf("Failed to query popular pubkeys: %v", err)
	}

	// Print CSV location and exit
	absPath, _ := filepath.Abs(csvPath)
	fmt.Printf("CSV exported to: %s\n", absPath)
	fmt.Printf("Found %d pubkeys with 1+ followers\n", totalCount)
}
