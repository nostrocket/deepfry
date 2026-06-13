package dgraph

import (
	"testing"
	"time"
)

// TestBackoffInterval verifies the pure geometric backoff math helper:
//   - miss 0 → base (2h)
//   - miss 1 → 4h, miss 2 → 8h, miss 3 → 16h, miss 6 → 128h
//   - miss 7+ → capped at 7d (168h)
//   - large missCount (overflow guard) → returns cap without overflow
func TestBackoffInterval(t *testing.T) {
	const (
		base  = 2 * time.Hour
		cap_  = 168 * time.Hour // 7 days
		ratio = 2
	)

	cases := []struct {
		missCount int
		want      time.Duration
	}{
		{0, 2 * time.Hour},
		{1, 4 * time.Hour},
		{2, 8 * time.Hour},
		{3, 16 * time.Hour},
		{4, 32 * time.Hour},
		{5, 64 * time.Hour},
		{6, 128 * time.Hour},
		{7, 168 * time.Hour},   // capped at 7d
		{8, 168 * time.Hour},   // stays capped
		{20, 168 * time.Hour},  // large value — overflow guard
		{63, 168 * time.Hour},  // near int64 boundary — overflow guard
		{100, 168 * time.Hour}, // well past int64 boundary — overflow guard
	}

	for _, tc := range cases {
		got := BackoffInterval(tc.missCount, base, ratio, cap_)
		if got != tc.want {
			t.Errorf("BackoffInterval(missCount=%d) = %v; want %v", tc.missCount, got, tc.want)
		}
	}
}

// TestBackoffIntervalRatio3 verifies that a ratio other than 2 works correctly.
func TestBackoffIntervalRatio3(t *testing.T) {
	// ratio=3, base=1h, cap=100h
	base := 1 * time.Hour
	cap_ := 100 * time.Hour
	ratio := 3

	// miss 0 → 1h, miss 1 → 3h, miss 2 → 9h, miss 3 → 27h, miss 4 → 81h, miss 5 → capped at 100h
	cases := []struct {
		missCount int
		want      time.Duration
	}{
		{0, 1 * time.Hour},
		{1, 3 * time.Hour},
		{2, 9 * time.Hour},
		{3, 27 * time.Hour},
		{4, 81 * time.Hour},
		{5, 100 * time.Hour}, // 243h > 100h → cap
	}

	for _, tc := range cases {
		got := BackoffInterval(tc.missCount, base, ratio, cap_)
		if got != tc.want {
			t.Errorf("BackoffInterval(ratio=3, missCount=%d) = %v; want %v", tc.missCount, got, tc.want)
		}
	}
}

// TestBackoffIntervalNonAlignedCap is the CR-01 regression: when cap/base is
// NOT ratio-aligned, the interval must still grow geometrically and only clamp
// once it actually reaches/exceeds cap. The prior truncated-threshold guard
// (int64(cap/base)) capped one ratio-step early on non-aligned configs.
func TestBackoffIntervalNonAlignedCap(t *testing.T) {
	cases := []struct {
		name      string
		missCount int
		base      time.Duration
		ratio     int
		cap       time.Duration
		want      time.Duration
	}{
		// base=2h, ratio=2, cap=5h: 2h→4h→(8h clamps to 5h). 4h must NOT cap early.
		{"miss1 under non-aligned cap", 1, 2 * time.Hour, 2, 5 * time.Hour, 4 * time.Hour},
		{"miss2 over non-aligned cap", 2, 2 * time.Hour, 2, 5 * time.Hour, 5 * time.Hour},
		// base=3h, ratio=2, cap=13h: 3h→6h→12h→(24h clamps). 12h must NOT cap early.
		{"miss2 just under non-aligned cap", 2, 3 * time.Hour, 2, 13 * time.Hour, 12 * time.Hour},
		{"miss3 over non-aligned cap", 3, 3 * time.Hour, 2, 13 * time.Hour, 13 * time.Hour},
		// Exact aligned boundary: base=2h, cap=8h, miss2 → 8h (== cap, not over).
		{"miss2 equals aligned cap", 2, 2 * time.Hour, 2, 8 * time.Hour, 8 * time.Hour},
	}

	for _, tc := range cases {
		got := BackoffInterval(tc.missCount, tc.base, tc.ratio, tc.cap)
		if got != tc.want {
			t.Errorf("%s: BackoffInterval(%d, base=%v, ratio=%d, cap=%v) = %v; want %v",
				tc.name, tc.missCount, tc.base, tc.ratio, tc.cap, got, tc.want)
		}
	}
}
