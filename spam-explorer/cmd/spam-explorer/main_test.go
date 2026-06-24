package main

import (
	"flag"
	"testing"
)

// TestFlags asserts the documented Phase-1 flag defaults are wired (CLI-01).
// It parses an empty argument set through a fresh FlagSet so the assertion does
// not depend on os.Args or the global flag.CommandLine state.
func TestFlags(t *testing.T) {
	fs := flag.NewFlagSet("spam-explorer", flag.ContinueOnError)
	opts := registerFlags(fs)

	if err := fs.Parse([]string{}); err != nil {
		t.Fatalf("parsing empty args failed: %v", err)
	}

	if opts.seed != "" {
		t.Errorf("seed default = %q, want %q", opts.seed, "")
	}
	if opts.threshold != 2 {
		t.Errorf("threshold default = %d, want 2", opts.threshold)
	}
	if opts.excludeShells != 1 {
		t.Errorf("exclude-shells default = %d, want 1", opts.excludeShells)
	}
	if opts.dgraphAddr != "localhost:9080" {
		t.Errorf("dgraph default = %q, want %q", opts.dgraphAddr, "localhost:9080")
	}
	if opts.maxLevel != 4 {
		t.Errorf("max-level default = %d, want 4", opts.maxLevel)
	}
	if opts.out != "spam-candidates.jsonl" {
		t.Errorf("out default = %q, want %q", opts.out, "spam-candidates.jsonl")
	}
}

// TestFlagsAllSixRegistered asserts all six flags exist on the FlagSet (CLI-01).
func TestFlagsAllSixRegistered(t *testing.T) {
	fs := flag.NewFlagSet("spam-explorer", flag.ContinueOnError)
	registerFlags(fs)

	want := []string{"seed", "threshold", "exclude-shells", "dgraph", "max-level", "out"}
	for _, name := range want {
		if fs.Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}
