package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"web-of-trust/pkg/dgraph"
)

// backfill-follower-count is the one-time operator-run CLI that populates the
// Phase 14 follower_count predicate (follower_count = count(~follows)) over the
// existing graph (DSCALE-03). It calls EnsureSchema first so the follower_count
// int index exists before any backfill writes — that Alter triggers the int-index
// build over the ~1.38M live nodes, which is the operator-visible step. The
// backfill is idempotent and safe to re-run.
func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			`backfill-follower-count: one-time operator backfill that sets
follower_count = count(~follows) on every node (DSCALE-03).

It pages the graph by UID CURSOR (after: <last-uid>) and runs one bounded
upsert per page: each page computes count(~follows) into a val() variable and
writes it to follower_count for the whole page in a single index pass (O(n)
overall). Zero-follower nodes are written follower_count = 0 (required so the
read path's follower_count index sees every node). Page size is 100000, below
Dgraph's ~1M-UID var limit. EnsureSchema runs first so the follower_count int
index exists before any write. The backfill is idempotent and safe to re-run.

PRECONDITION: run this to completion BEFORE relying on follower_count
ordering (GetStalePubkeys frontier ordering). Pre-backfill nodes read 0;
crawler writes during/after the backfill apply a +/-1 maintenance that
self-heals once this overwrite lands, so it is safe to run the crawler
concurrently — but the read-path ordering is only trustworthy once the
backfill has finished.

Usage:
`)
		flag.PrintDefaults()
	}

	dgraphAddr := flag.String("dgraph-addr", "localhost:9080", "Dgraph gRPC address")
	dryRun := flag.Bool("dry-run", false, "Count the nodes that would be updated without writing")
	flag.Parse()

	ctx := context.Background()

	client, err := dgraph.NewClient(*dgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create Dgraph client: %v", err)
	}
	defer client.Close()

	// Ensure the follower_count predicate + index exist before backfilling.
	// This Alter builds the int index over all existing nodes.
	if err := client.EnsureSchema(ctx); err != nil {
		log.Fatalf("Failed to ensure schema: %v", err)
	}

	if *dryRun {
		total, err := client.CountPubkeys(ctx)
		if err != nil {
			log.Fatalf("Failed to count pubkeys: %v", err)
		}
		fmt.Printf("Dry run: would backfill follower_count on %d nodes.\n", total)
		return
	}

	updated, err := client.BackfillFollowerCount(ctx)
	if err != nil {
		log.Fatalf("Backfill failed: %v", err)
	}
	fmt.Printf("Backfilled follower_count on %d nodes.\n", updated)
}
