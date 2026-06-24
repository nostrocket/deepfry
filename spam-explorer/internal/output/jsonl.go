// Package output writes the scored spam-candidate pubkeys as JSONL. It is PURE
// I/O: it touches only the supplied output path and never accesses Dgraph.
//
// Each surviving node becomes one line {"pubkey":..., "valid_follower_count":...}.
// Records are sorted by pubkey before writing so the output is byte-stable —
// trivially golden-file testable and pre-positioning Phase-2 determinism
// (RESEARCH Open Question 2).
package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Record is one emitted JSONL line. The json tags fix the on-disk key names and
// order is irrelevant for JSON objects, but the struct field order keeps
// encoding deterministic: pubkey then valid_follower_count.
type Record struct {
	Pubkey             string `json:"pubkey"`
	ValidFollowerCount int    `json:"valid_follower_count"`
}

// Write emits one JSONL Record per surviving node and returns the count emitted.
//
// A node survives when BOTH:
//   - levels[uid] > k  — excludes the seed (level 0) and the first k shells
//     (levels 1..k), i.e. OUT-01.
//   - scored[uid] < threshold — emits only valid_follower_count strictly below
//     the threshold, i.e. OUT-02 (strict <, so vfc == threshold is excluded).
//
// Records are collected, sorted by pubkey, then streamed through a buffered
// json.Encoder (Encode appends a newline per object == JSONL). The output file
// is created at path; errors are wrapped with %w.
func Write(path string, scored, levels map[string]int, pubkeys map[string]string, threshold, k int) (emitted int, err error) {
	records := make([]Record, 0, len(scored))
	for uid, vfc := range scored {
		if levels[uid] <= k {
			continue // OUT-01: exclude seed (level 0) and shells 1..k
		}
		if vfc >= threshold {
			continue // OUT-02: emit only vfc < threshold (strict)
		}
		records = append(records, Record{Pubkey: pubkeys[uid], ValidFollowerCount: vfc})
	}

	// Byte-stable output (Open Question 2): sort by pubkey before writing.
	sort.Slice(records, func(i, j int) bool {
		return records[i].Pubkey < records[j].Pubkey
	})

	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create output %q: %w", path, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	enc := json.NewEncoder(w) // Encode writes one object + "\n" == JSONL
	for _, rec := range records {
		if encErr := enc.Encode(rec); encErr != nil {
			return emitted, fmt.Errorf("encode record %q: %w", rec.Pubkey, encErr)
		}
		emitted++
	}
	return emitted, nil
}
