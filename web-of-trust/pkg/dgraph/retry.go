package dgraph

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queryWithRetries executes a Dgraph query with automatic retries for transient errors
func (c *Client) queryWithRetries(
	ctx context.Context,
	txn *dgo.Txn,
	query string,
	maxRetries int,
	debug bool,
) (resp *api.Response, err error) {
	var attempt int
	var lastErr error

	for attempt = 1; attempt <= maxRetries; attempt++ {
		attemptStart := time.Now()

		// Create a shorter timeout for each attempt to allow for retries
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err = txn.Query(attemptCtx, query)
		cancel()

		if err == nil {
			if debug && attempt > 1 {
				log.Printf("DEBUG: Query succeeded on attempt %d after %v",
					attempt, time.Since(attemptStart))
			}
			return resp, nil
		}

		lastErr = err
		errStatus, ok := status.FromError(err)

		// Only retry on specific error codes that indicate transient issues
		if !ok || (errStatus.Code() != codes.DeadlineExceeded &&
			errStatus.Code() != codes.Unavailable &&
			errStatus.Code() != codes.ResourceExhausted) {
			if debug {
				log.Printf("DEBUG: Query failed with non-retryable error on attempt %d: %v",
					attempt, err)
			}
			break
		}

		if debug {
			log.Printf("DEBUG: Query failed on attempt %d with retryable error: %v. Retrying...",
				attempt, err)
		}

		// Exponential backoff before retry
		if attempt < maxRetries {
			backoff := time.Duration(50*(1<<attempt)) * time.Millisecond // 100ms, 200ms, 400ms...
			time.Sleep(backoff)
		}
	}

	return nil, fmt.Errorf("query failed after %d attempts: %w", attempt-1, lastErr)
}

// mutateWithRetries executes a Dgraph mutation with automatic retries for transient errors
func (c *Client) mutateWithRetries(
	ctx context.Context,
	txn *dgo.Txn,
	mutation *api.Mutation,
	maxRetries int,
	debug bool,
) (resp *api.Response, err error) {
	var attempt int
	var lastErr error

	for attempt = 1; attempt <= maxRetries; attempt++ {
		attemptStart := time.Now()

		// Create a shorter timeout for each attempt to allow for retries
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err = txn.Mutate(attemptCtx, mutation)
		cancel()

		if err == nil {
			if debug && attempt > 1 {
				log.Printf("DEBUG: Mutation succeeded on attempt %d after %v",
					attempt, time.Since(attemptStart))
			}
			return resp, nil
		}

		lastErr = err
		errStatus, ok := status.FromError(err)

		// Only retry on specific error codes that indicate transient issues
		if !ok || (errStatus.Code() != codes.DeadlineExceeded &&
			errStatus.Code() != codes.Unavailable &&
			errStatus.Code() != codes.ResourceExhausted) {
			if debug {
				log.Printf("DEBUG: Mutation failed with non-retryable error on attempt %d: %v",
					attempt, err)
			}
			break
		}

		if debug {
			log.Printf("DEBUG: Mutation failed on attempt %d with retryable error: %v. Retrying...",
				attempt, err)
		}

		// Exponential backoff before retry
		if attempt < maxRetries {
			backoff := time.Duration(50*(1<<attempt)) * time.Millisecond // 100ms, 200ms, 400ms...
			time.Sleep(backoff)
		}
	}

	return nil, fmt.Errorf("mutation failed after %d attempts: %w", attempt-1, lastErr)
}

// commitWithRetries attempts to commit a transaction with retries for transient errors
func (c *Client) commitWithRetries(
	ctx context.Context,
	txn *dgo.Txn,
	maxRetries int,
	debug bool,
) error {
	var attempt int
	var lastErr error

	for attempt = 1; attempt <= maxRetries; attempt++ {
		attemptStart := time.Now()

		// Create a shorter timeout for each attempt to allow for retries
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := txn.Commit(attemptCtx)
		cancel()

		if err == nil {
			if debug && attempt > 1 {
				log.Printf("DEBUG: Transaction commit succeeded on attempt %d after %v",
					attempt, time.Since(attemptStart))
			}
			return nil
		}

		lastErr = err
		errStatus, ok := status.FromError(err)

		// Only retry on specific error codes that indicate transient issues
		if !ok || (errStatus.Code() != codes.DeadlineExceeded &&
			errStatus.Code() != codes.Unavailable &&
			errStatus.Code() != codes.ResourceExhausted) {
			if debug {
				log.Printf("DEBUG: Transaction commit failed with non-retryable error on attempt %d: %v",
					attempt, err)
			}
			break
		}

		if debug {
			log.Printf("DEBUG: Transaction commit failed on attempt %d with retryable error: %v. Retrying...",
				attempt, err)
		}

		// Exponential backoff before retry
		if attempt < maxRetries {
			backoff := time.Duration(50*(1<<attempt)) * time.Millisecond // 100ms, 200ms, 400ms...
			time.Sleep(backoff)
		}
	}

	return fmt.Errorf("transaction commit failed after %d attempts: %w", attempt-1, lastErr)
}
