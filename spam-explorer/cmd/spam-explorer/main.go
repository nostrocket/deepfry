// Command spam-explorer scores every reachable pubkey in the web-of-trust
// follow graph by its seed-relative valid-follower count and emits the
// suspected spam / sybil candidates as JSONL.
//
// Given a trusted seed pubkey it assigns every reachable account a level equal
// to its shortest follow-hop distance from the seed, counts a follower as valid
// only if it sits on a strictly shallower level, then writes every account whose
// valid-follower count is below a threshold to the output file.
//
// This file wires the full Phase-1 spine: resolve the seed pubkey to a UID,
// BFS-level the reachable subgraph one frontier at a time off live Dgraph, score
// each node by its strictly-upstream follower count, and write the
// threshold/k-shell-filtered JSONL candidate file.
package main

import (
	"context"
	"flag"
	"log"

	"spam-explorer/internal/bfs"
	"spam-explorer/internal/dgraph"
	"spam-explorer/internal/output"
	"spam-explorer/internal/score"
)

// Version, Commit, and Built are injected at build time via -ldflags
// (see Makefile LDFLAGS). They default to empty for `go run`.
var (
	Version string
	Commit  string
	Built   string
)

// options holds the parsed CLI flags for the run.
type options struct {
	seed          string
	threshold     int
	excludeShells int
	dgraphAddr    string
	maxLevel      int
	out           string
}

// registerFlags wires every CLI flag with its documented Phase-1 default onto
// the supplied FlagSet, returning a pointer to the options the FlagSet will
// populate on Parse. Extracted so flag defaults are unit-testable (CLI-01)
// without touching the global flag.CommandLine or os.Args.
func registerFlags(fs *flag.FlagSet) *options {
	opts := &options{}
	fs.StringVar(&opts.seed, "seed", "", "trusted seed pubkey (64-char hex) to anchor BFS leveling")
	fs.IntVar(&opts.threshold, "threshold", 2, "emit accounts with valid_follower_count < N")
	fs.IntVar(&opts.excludeShells, "exclude-shells", 1, "exclude the seed and its first k shells (levels 1..k)")
	fs.StringVar(&opts.dgraphAddr, "dgraph", "localhost:9080", "Dgraph gRPC endpoint")
	fs.IntVar(&opts.maxLevel, "max-level", 4, "TEMPORARY Phase-1 bounding cap: stop BFS past this level (D-03; flagged for removal/retention review at Phase 2)")
	fs.StringVar(&opts.out, "out", "spam-candidates.jsonl", "output JSONL path")
	return opts
}

func main() {
	opts := registerFlags(flag.CommandLine)
	flag.Parse()

	log.Printf("spam-explorer %s (commit %s, built %s)", Version, Commit, Built)

	ctx := context.Background()

	// Connect to Dgraph (read-only). internal/dgraph is the only tier on the wire.
	client, err := dgraph.NewClient(opts.dgraphAddr)
	if err != nil {
		log.Fatalf("Failed to create Dgraph client at %q: %v", opts.dgraphAddr, err)
	}
	defer client.Close()

	// Resolve the seed pubkey to its internal UID. ResolveSeed already returns a
	// clear error when the seed is absent from the graph (missing-seed guard), so
	// main just propagates it as a fatal exit.
	seedUID, err := client.ResolveSeed(ctx, opts.seed)
	if err != nil {
		log.Fatalf("Failed to resolve seed: %v", err)
	}

	// BFS-level the reachable subgraph one frontier at a time, injecting the live
	// Dgraph expander. bfs.Level keys on UID and accumulates the follows adjacency
	// + a uid->pubkey map for scoring and output. ExpandFrontier is the Phase-2
	// pagination seam.
	levels, adjacency, pubkeys, err := bfs.Level(ctx, seedUID, client.ExpandFrontier, opts.maxLevel)
	if err != nil {
		log.Fatalf("BFS leveling failed: %v", err)
	}

	// Score: invert the in-memory follows adjacency, counting strictly-upstream
	// followers (D-02 — no ~follows query, no follower_count read).
	vfc := score.Score(levels, adjacency)

	// Write the threshold/k-shell-filtered JSONL candidate file.
	emitted, err := output.Write(opts.out, vfc, levels, pubkeys, opts.threshold, opts.excludeShells)
	if err != nil {
		log.Fatalf("Failed to write output %q: %v", opts.out, err)
	}

	// Basic Phase-1 summary to stderr (full OUT-03/OPS logging is Phase 3): how
	// many accounts were leveled, scored, emitted, and the deepest level reached.
	deepest := 0
	for _, lvl := range levels {
		if lvl > deepest {
			deepest = lvl
		}
	}
	log.Printf("done: leveled=%d scored=%d emitted=%d deepest-level=%d -> %s",
		len(levels), len(vfc), emitted, deepest, opts.out)
}
