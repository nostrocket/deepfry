// Command spam-explorer scores every reachable pubkey in the web-of-trust
// follow graph by its seed-relative valid-follower count and emits the
// suspected spam / sybil candidates as JSONL.
//
// Given a trusted seed pubkey it assigns every reachable account a level equal
// to its shortest follow-hop distance from the seed, counts a follower as valid
// only if it sits on a strictly shallower level, then writes every account whose
// valid-follower count is below a threshold to the output file.
//
// This file is the Phase-1 CLI skeleton: it wires the flags and leaves the
// resolve -> BFS -> score -> output spine as a stub for Plan 02.
package main

import (
	"flag"
	"log"
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

	// Phase-1 skeleton: Plan 02 fills in the resolve -> BFS -> score -> output
	// spine. For now, log the parsed configuration and return.
	log.Printf("spam-explorer %s (commit %s, built %s)", Version, Commit, Built)
	log.Printf("config: seed=%q threshold=%d exclude-shells=%d dgraph=%q max-level=%d out=%q",
		opts.seed, opts.threshold, opts.excludeShells, opts.dgraphAddr, opts.maxLevel, opts.out)
}
