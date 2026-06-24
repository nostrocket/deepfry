package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_GoldenFiltering(t *testing.T) {
	// levels: seed=0, a=1, b=2, c=2, d=3
	// k (exclude-shells) = 1  -> exclude seed(0) and shell 1 (a). Keep level > 1.
	// threshold = 2           -> emit only vfc < 2.
	//
	// Candidates after level filter (level > 1): b, c, d.
	//   b: vfc 1  (<2)  -> emit
	//   c: vfc 2  (>=2) -> excluded by threshold
	//   d: vfc 1  (<2)  -> emit
	// seed excluded (level 0); a excluded (level 1 == k shell).
	// Emitted sorted by pubkey: pk-b before pk-d.
	scored := map[string]int{"a": 5, "b": 1, "c": 2, "d": 1}
	levels := map[string]int{"seed": 0, "a": 1, "b": 2, "c": 2, "d": 3}
	pubkeys := map[string]string{
		"seed": "pk-seed",
		"a":    "pk-a",
		"b":    "pk-b",
		"c":    "pk-c",
		"d":    "pk-d",
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")

	emitted, err := Write(path, scored, levels, pubkeys, 2, 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 2 {
		t.Errorf("emitted = %d, want 2", emitted)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	want := `{"pubkey":"pk-b","valid_follower_count":1}` + "\n" +
		`{"pubkey":"pk-d","valid_follower_count":1}` + "\n"
	if string(got) != want {
		t.Errorf("output mismatch.\n got: %q\nwant: %q", string(got), want)
	}
}

func TestWrite_SortedByPubkey(t *testing.T) {
	// Insert in non-sorted key order; output must be sorted by pubkey.
	scored := map[string]int{"u3": 0, "u1": 0, "u2": 0}
	levels := map[string]int{"u3": 2, "u1": 2, "u2": 2}
	pubkeys := map[string]string{"u3": "zzz", "u1": "aaa", "u2": "mmm"}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	emitted, err := Write(path, scored, levels, pubkeys, 5, 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 3 {
		t.Fatalf("emitted = %d, want 3", emitted)
	}

	got, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	var order []string
	for _, ln := range lines {
		var r Record
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", ln, err)
		}
		order = append(order, r.Pubkey)
	}
	want := []string{"aaa", "mmm", "zzz"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (sorted by pubkey)", i, order[i], want[i])
		}
	}
}

func TestWrite_ExcludesSeedAndShells(t *testing.T) {
	// k = 2 -> exclude levels 0,1,2. Only level 3 survives.
	scored := map[string]int{"seed": 0, "a": 0, "b": 0, "deep": 0}
	levels := map[string]int{"seed": 0, "a": 1, "b": 2, "deep": 3}
	pubkeys := map[string]string{"seed": "pk-seed", "a": "pk-a", "b": "pk-b", "deep": "pk-deep"}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	emitted, err := Write(path, scored, levels, pubkeys, 5, 2)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (only level 3 survives k=2)", emitted)
	}
	got, _ := os.ReadFile(path)
	want := `{"pubkey":"pk-deep","valid_follower_count":0}` + "\n"
	if string(got) != want {
		t.Errorf("output = %q, want %q", string(got), want)
	}
}

func TestWrite_ThresholdIsStrict(t *testing.T) {
	// A node with vfc == threshold must be EXCLUDED (strict <).
	scored := map[string]int{"eq": 2, "below": 1}
	levels := map[string]int{"eq": 2, "below": 2}
	pubkeys := map[string]string{"eq": "pk-eq", "below": "pk-below"}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	emitted, err := Write(path, scored, levels, pubkeys, 2, 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (vfc==threshold excluded)", emitted)
	}
	got, _ := os.ReadFile(path)
	want := `{"pubkey":"pk-below","valid_follower_count":1}` + "\n"
	if string(got) != want {
		t.Errorf("output = %q, want %q", string(got), want)
	}
}

func TestWrite_EachLineHasExactlyTwoKeys(t *testing.T) {
	scored := map[string]int{"x": 0}
	levels := map[string]int{"x": 2}
	pubkeys := map[string]string{"x": "pk-x"}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	if _, err := Write(path, scored, levels, pubkeys, 5, 1); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, _ := os.ReadFile(path)
	line := strings.TrimRight(string(got), "\n")
	var generic map[string]any
	if err := json.Unmarshal([]byte(line), &generic); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(generic) != 2 {
		t.Errorf("line has %d keys, want 2: %v", len(generic), generic)
	}
	if _, ok := generic["pubkey"]; !ok {
		t.Errorf("missing pubkey key: %v", generic)
	}
	if _, ok := generic["valid_follower_count"]; !ok {
		t.Errorf("missing valid_follower_count key: %v", generic)
	}
}

func TestWrite_SkipsNodesWithoutPubkey(t *testing.T) {
	// The web-of-trust graph contains follows-edges pointing to UIDs that have no
	// pubkey predicate (uncrawled stub nodes). BFS still levels them, but they are
	// not usable spam candidates — emitting {"pubkey":"",...} pollutes the JSONL
	// with unidentifiable rows. Write must skip any node whose resolved pubkey is
	// empty.
	scored := map[string]int{"good": 1, "stub": 1}
	levels := map[string]int{"good": 2, "stub": 2}
	pubkeys := map[string]string{"good": "pk-good"} // "stub" has no pubkey entry -> ""

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	emitted, err := Write(path, scored, levels, pubkeys, 5, 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 1 {
		t.Fatalf("emitted = %d, want 1 (empty-pubkey stub skipped)", emitted)
	}
	got, _ := os.ReadFile(path)
	want := `{"pubkey":"pk-good","valid_follower_count":1}` + "\n"
	if string(got) != want {
		t.Errorf("output = %q, want %q (no empty-pubkey line)", string(got), want)
	}
	if strings.Contains(string(got), `"pubkey":""`) {
		t.Errorf("output contains an empty-pubkey record: %q", string(got))
	}
}

func TestWrite_EmptyWhenAllFiltered(t *testing.T) {
	// Everything is at level <= k or vfc >= threshold => empty file, emitted 0.
	scored := map[string]int{"seed": 0, "a": 9}
	levels := map[string]int{"seed": 0, "a": 1}
	pubkeys := map[string]string{"seed": "pk-seed", "a": "pk-a"}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	emitted, err := Write(path, scored, levels, pubkeys, 2, 1)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}
	got, _ := os.ReadFile(path)
	if len(got) != 0 {
		t.Errorf("file = %q, want empty", string(got))
	}
}
