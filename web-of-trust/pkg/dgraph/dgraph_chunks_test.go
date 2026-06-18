package dgraph

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// makeStrings returns a slice of n unique strings ("0".."n-1").
func makeStrings(n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%d", i)
	}
	return out
}

func TestIsTransientError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "deadline exceeded",
			err:  status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			want: true,
		},
		{
			name: "unavailable",
			err:  status.Error(codes.Unavailable, "transport unavailable"),
			want: true,
		},
		{
			name: "resource exhausted fatal",
			err:  status.Error(codes.ResourceExhausted, "message too large"),
			want: false,
		},
		{
			name: "unauthenticated fatal",
			err:  status.Error(codes.Unauthenticated, "bad credentials"),
			want: false,
		},
		{
			name: "plain error fatal",
			err:  fmt.Errorf("plain failure"),
			want: false,
		},
		{
			name: "nil fatal",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientError(tc.err); got != tc.want {
				t.Fatalf("IsTransientError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestFollowUpdateProgress(t *testing.T) {
	p := newFollowUpdateProgress("abc123", 501, 5)

	p.beginChunk("resolve_followees", 1)
	p.completeChunk()
	if p.currentPhase != "resolve_followees" || p.currentChunk != 1 || p.completedChunks != 1 {
		t.Fatalf("after first chunk: phase=%s current=%d completed=%d",
			p.currentPhase, p.currentChunk, p.completedChunks)
	}

	p.beginChunk("create_follow_edges", 2)
	p.completeChunk()
	if p.currentPhase != "create_follow_edges" || p.currentChunk != 2 || p.completedChunks != 2 {
		t.Fatalf("after second chunk: phase=%s current=%d completed=%d",
			p.currentPhase, p.currentChunk, p.completedChunks)
	}
	if p.totalChunks != 5 {
		t.Fatalf("totalChunks = %d, want 5", p.totalChunks)
	}

	err := p.finish("transient_error", status.Error(codes.DeadlineExceeded, "slow query"))
	if err == nil {
		t.Fatal("finish with error returned nil")
	}
	if err.Pubkey != "abc123" || err.FollowCount != 501 || err.Phase != "create_follow_edges" {
		t.Fatalf("unexpected error context: %#v", err)
	}
	if err.ChunkIndex != 2 || err.ChunkTotal != 5 || err.Outcome != "transient_error" {
		t.Fatalf("unexpected progress in error: chunk=%d/%d outcome=%s",
			err.ChunkIndex, err.ChunkTotal, err.Outcome)
	}
	if !IsTransientError(err) {
		t.Fatal("FollowUpdateError should preserve transient status through Unwrap")
	}
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
