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
//   miss 0 → 2h, miss 1 → 4h, ..., miss 6 → 128h, miss 7+ → 168h (7 days).
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

	// Overflow guard: determine how many doublings (or ratio-ings) fit before
	// hitting cap. If missCount >= that threshold, return cap directly.
	//
	// We want to check: base * ratio^missCount >= cap
	// i.e.: ratio^missCount >= cap/base
	// We iterate instead of computing ratio^missCount to avoid overflow.
	threshold := int64(cap / base)
	power := int64(1)
	for i := 0; i < missCount; i++ {
		power *= int64(ratio)
		if power >= threshold {
			return cap
		}
	}

	result := base * time.Duration(power)
	return min64(result, cap)
}

// min64 returns the smaller of two time.Duration values.
func min64(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
