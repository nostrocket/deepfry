package crawler

import (
	"math"
	"testing"
)

// TestQuorumReached_BelowThreshold verifies that quorumReached returns false
// when done < ceil(q * queried).
func TestQuorumReached_BelowThreshold(t *testing.T) {
	// q=0.70, queried=10 → threshold = ceil(7.0) = 7; done=6 → false
	if quorumReached(6, 10, 0.70) {
		t.Fatal("expected false when done=6 < ceil(0.70*10)=7")
	}
}

// TestQuorumReached_AtThreshold verifies that quorumReached returns true
// exactly at the ceil threshold.
func TestQuorumReached_AtThreshold(t *testing.T) {
	// q=0.70, queried=10 → threshold = ceil(7.0) = 7; done=7 → true
	if !quorumReached(7, 10, 0.70) {
		t.Fatal("expected true when done=7 == ceil(0.70*10)=7")
	}
}

// TestQuorumReached_AboveThreshold verifies that quorumReached returns true
// when done exceeds the ceil threshold.
func TestQuorumReached_AboveThreshold(t *testing.T) {
	// q=0.70, queried=10 → threshold = ceil(7.0) = 7; done=9 → true
	if !quorumReached(9, 10, 0.70) {
		t.Fatal("expected true when done=9 > ceil(0.70*10)=7")
	}
}

// TestQuorumReached_ZeroFraction verifies that quorumReached returns false
// when q <= 0 (quorum disabled — T-08-EARLY guard).
func TestQuorumReached_ZeroFraction(t *testing.T) {
	if quorumReached(10, 10, 0.0) {
		t.Fatal("expected false when q=0 (quorum disabled)")
	}
	if quorumReached(10, 10, -1.0) {
		t.Fatal("expected false when q=-1 (quorum disabled)")
	}
}

// TestQuorumReached_ZeroQueried verifies that quorumReached returns false
// when queried == 0, preventing an immediate cancel before any relay responds
// (T-08-EARLY guard: ceil(q * 0) == 0, which would always trigger).
func TestQuorumReached_ZeroQueried(t *testing.T) {
	if quorumReached(0, 0, 0.70) {
		t.Fatal("expected false when queried=0 (no relays launched)")
	}
}

// TestQuorumReached_CeilRounding verifies that non-integer thresholds are
// rounded up correctly. q=0.70, queried=3 → threshold = ceil(2.1) = 3;
// done=2 → false, done=3 → true.
func TestQuorumReached_CeilRounding(t *testing.T) {
	// ceil(0.70 * 3) = ceil(2.1) = 3
	expected := int32(int(math.Ceil(0.70 * 3)))
	if expected != 3 {
		t.Fatalf("test invariant: expected threshold=3, got %d", expected)
	}
	if quorumReached(2, 3, 0.70) {
		t.Fatal("expected false when done=2 < ceil(0.70*3)=3")
	}
	if !quorumReached(3, 3, 0.70) {
		t.Fatal("expected true when done=3 == ceil(0.70*3)=3")
	}
}

// TestQuorumReached_FullFraction verifies that q=1.0 requires all relays to
// respond before the quorum is reached.
func TestQuorumReached_FullFraction(t *testing.T) {
	// q=1.0, queried=5 → threshold = ceil(5.0) = 5
	if quorumReached(4, 5, 1.0) {
		t.Fatal("expected false when done=4 < ceil(1.0*5)=5")
	}
	if !quorumReached(5, 5, 1.0) {
		t.Fatal("expected true when done=5 == ceil(1.0*5)=5")
	}
}

// TestQuorumReached_SingleRelay verifies behaviour with a single queried relay:
// q=0.70, queried=1 → threshold = ceil(0.70) = 1; done=1 → true.
func TestQuorumReached_SingleRelay(t *testing.T) {
	// ceil(0.70 * 1) = ceil(0.70) = 1
	if quorumReached(0, 1, 0.70) {
		t.Fatal("expected false when done=0 < ceil(0.70*1)=1")
	}
	if !quorumReached(1, 1, 0.70) {
		t.Fatal("expected true when done=1 == ceil(0.70*1)=1")
	}
}
