package dgraph

import (
	"fmt"
	"testing"
)

// makeStrings returns a slice of n unique strings ("0".."n-1").
func makeStrings(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%d", i)
	}
	return out
}

// TestChunkSlice pins chunkSlice's chunk COUNT and membership UNION at the
// boundaries that matter for the internal AddFollowers batching (batchSize=200).
// It runs under `make test` / `-short` with no Dgraph dependency (TEST-04, D-10).
func TestChunkSlice(t *testing.T) {
	const size = 200
	cases := []struct {
		name       string
		input      int // generate a slice of this length
		wantChunks int
	}{
		{"empty", 0, 0},
		{"exactly one chunk", 200, 1},
		{"one over boundary", 201, 2},
		{"500", 500, 3}, // ceil(500/200)=3: [200,200,100]
		{"501", 501, 3}, // ceil(501/200)=3: [200,200,101]
		{"10000", 10000, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := makeStrings(tc.input)
			chunks := chunkSlice(in, size)

			// (a) chunk count
			if len(chunks) != tc.wantChunks {
				t.Fatalf("want %d chunks, got %d", tc.wantChunks, len(chunks))
			}

			// (b) membership union equals the input length, and no chunk
			// exceeds size or is empty (no trailing empty chunk).
			total := 0
			for i, ch := range chunks {
				if len(ch) == 0 {
					t.Fatalf("chunk %d is empty — chunkSlice must not emit empty chunks", i)
				}
				if len(ch) > size {
					t.Fatalf("chunk %d has %d items, exceeds size %d", i, len(ch), size)
				}
				total += len(ch)
			}
			if total != tc.input {
				t.Fatalf("want union total %d items, got %d — items dropped or duplicated", tc.input, total)
			}
		})
	}
}
