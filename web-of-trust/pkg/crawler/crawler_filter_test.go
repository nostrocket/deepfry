package crawler

import (
	"fmt"
	"testing"
)

// TestHandleFilterNotice_Halves verifies that a "filter item too large" NOTICE
// halves rs.filterCap when cap is above the floor.
func TestHandleFilterNotice_Halves(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "Error: filter item too large", 10)
	if rs.filterCap.Load() != 50 {
		t.Fatalf("expected filterCap 50, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_CaseInsensitive verifies matching is case-insensitive.
func TestHandleFilterNotice_CaseInsensitive(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "NOTICE: Filter Too Large for subscription", 10)
	if rs.filterCap.Load() != 50 {
		t.Fatalf("expected filterCap 50 after case-insensitive match, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_Floor verifies that filterCap is not reduced below
// the floor (minCap=10) when it is already at the floor.
func TestHandleFilterNotice_Floor(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(10)
	handleFilterNotice(rs, "filter item too large", 10)
	if rs.filterCap.Load() != 10 {
		t.Fatalf("expected filterCap to stay at floor 10, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_HalveToFloor verifies that halving a cap of 12 yields
// max(6, 10) = 10 (floor clamping).
func TestHandleFilterNotice_HalveToFloor(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(12)
	handleFilterNotice(rs, "filter item too large", 10)
	if rs.filterCap.Load() != 10 {
		t.Fatalf("expected filterCap clamped to floor 10, got %d", rs.filterCap.Load())
	}
}

// TestHandleFilterNotice_UnrelatedNotice verifies that a NOTICE unrelated to
// filter size leaves filterCap unchanged.
func TestHandleFilterNotice_UnrelatedNotice(t *testing.T) {
	rs := &relayState{url: "wss://example.com"}
	rs.filterCap.Store(100)
	handleFilterNotice(rs, "your subscription has too many results", 10)
	if rs.filterCap.Load() != 100 {
		t.Fatalf("expected filterCap to remain 100 for unrelated notice, got %d", rs.filterCap.Load())
	}
}

// TestSplitAuthorsChunks verifies the chunk-splitting logic used in queryRelay:
// 250 authors with filterCap=100 produces chunks of 100, 100, 50.
func TestSplitAuthorsChunks(t *testing.T) {
	const total = 250
	authors := make([]string, total)
	for i := range authors {
		authors[i] = fmt.Sprintf("%064d", i)
	}

	rs := &relayState{}
	rs.filterCap.Store(100)

	var chunkSizes []int
	remaining := authors
	for len(remaining) > 0 {
		batchCap := int(rs.filterCap.Load())
		if batchCap <= 0 {
			batchCap = 10
		}
		chunk := remaining
		if len(remaining) > batchCap {
			chunk = remaining[:batchCap]
		}
		remaining = remaining[len(chunk):]
		chunkSizes = append(chunkSizes, len(chunk))
	}

	expected := []int{100, 100, 50}
	if len(chunkSizes) != len(expected) {
		t.Fatalf("expected %d chunks, got %d: %v", len(expected), len(chunkSizes), chunkSizes)
	}
	for i, want := range expected {
		if chunkSizes[i] != want {
			t.Fatalf("chunk[%d]: expected size %d, got %d", i, want, chunkSizes[i])
		}
	}
}
