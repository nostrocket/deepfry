package crawler

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"
)

// processFollowsInChunks breaks a large follow list into smaller chunks and processes them
// sequentially to avoid timeouts. This function is called for follow lists with more than
// 500 entries.
func (c *Crawler) processFollowsInChunks(
	ctx context.Context,
	pubkey string,
	createdAt int64,
	follows map[string]struct{},
) error {
	const chunkSize = 200 // Process 200 follows at a time

	// Convert map to slice for chunking
	followsList := make([]string, 0, len(follows))
	for follow := range follows {
		followsList = append(followsList, follow)
	}

	// Calculate number of chunks
	numChunks := int(math.Ceil(float64(len(followsList)) / float64(chunkSize)))

	if c.debug {
		log.Printf("Processing large follow list for %s in %d chunks of max %d follows each",
			pubkey, numChunks, chunkSize)
	}

	// Process each chunk
	for i := 0; i < numChunks; i++ {
		// Create a new context with timeout for each chunk
		chunkCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()

		// Calculate chunk bounds
		start := i * chunkSize
		end := (i + 1) * chunkSize
		if end > len(followsList) {
			end = len(followsList)
		}

		// Create chunk map
		chunkMap := make(map[string]struct{})
		for _, follow := range followsList[start:end] {
			chunkMap[follow] = struct{}{}
		}

		// Log chunk progress
		if c.debug {
			log.Printf("Processing chunk %d/%d with %d follows (%d-%d of %d) for pubkey %s",
				i+1, numChunks, len(chunkMap), start+1, end, len(follows), pubkey)
		}

		// Process this chunk
		startTime := time.Now()
		err := c.dgClient.AddFollowers(chunkCtx, pubkey, createdAt, chunkMap, c.debug)
		if err != nil {
			return fmt.Errorf("failed to process chunk %d/%d: %w", i+1, numChunks, err)
		}

		if c.debug {
			log.Printf("Chunk %d/%d processed successfully in %v",
				i+1, numChunks, time.Since(startTime))
		}

		// Small pause between chunks to let the system breathe
		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("Successfully processed large follow list (%d follows) in %d chunks for pubkey %s",
		len(follows), numChunks, pubkey)

	return nil
}
