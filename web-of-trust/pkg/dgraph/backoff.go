package dgraph

import "time"

// BackoffParams carries the config-driven parameters for PERF-02 hit/miss
// stamping. It is passed as a value to MarkAttempted so the dgraph package
// never needs to import pkg/config (which would create an import cycle).
//
// Default values (matching the locked Phase 8 schedule):
//
//	Base:              2h
//	Ratio:             2
//	Cap:               168h (7 days)
//	HitRefreshCadence: 24h (StalePubkeyThreshold repurposed as hit cadence)
type BackoffParams struct {
	Base              time.Duration // minimum backoff interval (first-miss delay)
	Ratio             int           // multiplicative growth factor per miss
	Cap               time.Duration // maximum backoff interval (never exceeded)
	HitRefreshCadence time.Duration // next_attempt offset for a HIT pubkey
}

// DefaultBackoffParams returns the locked Phase 8 defaults so callers that
// have not yet wired config can get safe values without importing pkg/config.
func DefaultBackoffParams() BackoffParams {
	return BackoffParams{
		Base:              2 * time.Hour,
		Ratio:             2,
		Cap:               168 * time.Hour,
		HitRefreshCadence: 24 * time.Hour,
	}
}

// BackoffInterval computes the geometric backoff interval for a given miss count.
//
// The formula is: base * ratio^missCount, capped at cap.
//
// For the standard Phase 8 PERF-02 schedule (base=2h, ratio=2, cap=168h):
//
//	miss 0 → 2h, miss 1 → 4h, ..., miss 6 → 128h, miss 7+ → 168h (7 days).
//
// Overflow guard (T-08-OVF): if missCount is large enough that the shifted
// interval would meet or exceed cap, cap is returned directly before performing
// the shift — preventing integer overflow in ratio^missCount arithmetic.
func BackoffInterval(missCount int, base time.Duration, ratio int, cap time.Duration) time.Duration {
	if missCount <= 0 {
		return min64(base, cap)
	}
	if ratio <= 1 {
		// Non-positive or unit ratio: no growth, return base (clamped).
		return min64(base, cap)
	}

	// Geometric growth: result = base * ratio^missCount, clamped at cap.
	// Iterate (instead of computing ratio^missCount) to avoid int64 overflow.
	//
	// Overflow guard (T-08-OVF): before each multiply, if the next step would
	// reach or exceed cap we are already pinned at cap, so return it directly.
	// Test that overflow-safely via division — result*ratio >= cap is equivalent
	// to result > (cap-1)/ratio for positive integers.
	//
	// CR-01 fix: the prior guard compared `power` against the *truncated*
	// int64(cap/base) threshold, which fired one ratio-step early whenever
	// cap/base was not ratio-aligned (e.g. base=2h, cap=5h returned 5h instead
	// of 4h). Comparing the actual running product against cap removes that
	// off-by-one; the locked 2h/168h defaults are power-aligned, which is why
	// the original table tests passed despite the bug.
	r := time.Duration(ratio)
	result := base
	for i := 0; i < missCount; i++ {
		if result > (cap-1)/r {
			return cap
		}
		result *= r
	}
	return min64(result, cap)
}

// min64 returns the smaller of two time.Duration values.
func min64(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
