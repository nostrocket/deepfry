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
		{7, 168 * time.Hour}, // capped at 7d
		{8, 168 * time.Hour}, // stays capped
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
