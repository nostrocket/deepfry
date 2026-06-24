// Asymmetric posting-rate / burst analyzer (DRILL-01) — a PURE function over the
// fetched window's author-claimed createdAt values. No React / transport / network.
//
// ASYMMETRY (RESEARCH § Pattern 3, Anti-pattern): the result is suspicious-when-present,
// inconclusive-when-absent. A burst (tight interarrival clustering) is the strongest
// single temporal automation signal, so burstDetected: true is a signal to investigate.
// But quiet timing proves nothing — createdAt is author-claimed and forgeable — so
// burstDetected: false means INCONCLUSIVE, never "clean". RateResult therefore carries
// NO clean/ok/safe field by construction; the absence is structural, not an oversight.
//
// FORGEABLE / 64-bit BOUNDS (contract §8, Pitfall 3): createdAt is author-claimed and
// 64-bit, so values can be negative, far-future, or beyond JS safe-integer precision.
// isSaneTs bounds-checks every value; out-of-range ones are COUNTED in rejectedCount
// and excluded from the math — never silently dropped and never used to compute a time
// range or interval (a forged value must not corrupt the bars or yield a negative gap).
// Sane timestamps are sorted ascending before any interval math so gaps are never
// negative regardless of input order.
//
// thresholds.ts is the single tunable home for the burst constants (CONTEXT discretion).
import { BURST } from './thresholds'

/** Lowest accepted createdAt (epoch seconds). */
export const MIN_TS = 0
/** Highest accepted createdAt — 2100-01-01T00:00:00Z (epoch seconds). Beyond = forged. */
export const MAX_TS = 4_102_444_800

/**
 * True when t is a usable author-claimed epoch-seconds value: a safe integer within
 * [MIN_TS, MAX_TS]. Rejects negatives, far-future, non-integers, NaN, and values beyond
 * Number.MAX_SAFE_INTEGER (where 64-bit precision is lost). Out-of-range values are
 * flagged (rejectedCount), never mis-computed.
 */
export function isSaneTs(t: number): boolean {
  return Number.isSafeInteger(t) && t >= MIN_TS && t <= MAX_TS
}

export interface RateResult {
  /** Denominator — events with a SANE createdAt that fed the analysis. */
  analyzedCount: number
  /** Out-of-range timestamps flagged and excluded (never silently dropped). */
  rejectedCount: number
  /** Fixed-width activity bins for the hand-rolled CSS/SVG bars. */
  bins: { start: number; count: number }[]
  /** TRUE = suspicious burst present; FALSE = INCONCLUSIVE (never "clean"). */
  burstDetected: boolean
  /** Smallest gap (seconds) between consecutive sane events; null when < 2 sane. */
  tightestIntervalSec: number | null
}

/**
 * Group sane (ascending) timestamps into fixed-width bins of `binSec` seconds, anchored
 * at the first timestamp. Each bin is { start, count }; empty spans produce no bin.
 */
function binByInterval(saneAscending: number[], binSec: number): { start: number; count: number }[] {
  if (saneAscending.length === 0) return []
  const origin = saneAscending[0]
  const bins: { start: number; count: number }[] = []
  let currentStart = origin
  let count = 0
  for (const t of saneAscending) {
    // Advance the bin window until t falls inside [currentStart, currentStart + binSec).
    while (t >= currentStart + binSec) {
      if (count > 0) bins.push({ start: currentStart, count })
      currentStart += binSec
      count = 0
    }
    count++
  }
  if (count > 0) bins.push({ start: currentStart, count })
  return bins
}

/**
 * Analyze a set of author-claimed createdAt values (epoch seconds) for a burst pattern.
 * Asymmetric + bounds-checked (see file header). Order-insensitive: input is filtered to
 * sane values and sorted ascending before any interval math.
 */
export function analyzeRate(createdAts: number[]): RateResult {
  // (1) Keep only sane values, sorted ascending; flag the rest in rejectedCount.
  const sane = createdAts.filter(isSaneTs).sort((a, b) => a - b)
  const rejectedCount = createdAts.length - sane.length

  // (2) Fewer than 2 sane points → nothing to compare. Inconclusive, no crash, no
  //     negative interval, no bins.
  if (sane.length < 2) {
    return {
      analyzedCount: sane.length,
      rejectedCount,
      bins: [],
      burstDetected: false,
      tightestIntervalSec: null,
    }
  }

  // (3) Tightest interarrival gap — sorted ascending so gaps are never negative.
  let tightest = Infinity
  for (let i = 1; i < sane.length; i++) {
    const gap = sane[i] - sane[i - 1]
    if (gap < tightest) tightest = gap
  }

  // (4) Sliding-window burst: >= BURST.minEvents within any BURST.windowSec window.
  let burst = false
  for (let i = 0; i < sane.length; i++) {
    let j = i
    while (j < sane.length && sane[j] - sane[i] <= BURST.windowSec) j++
    if (j - i >= BURST.minEvents) {
      burst = true
      break
    }
  }

  // (5) Fixed-width bins for the bars.
  return {
    analyzedCount: sane.length,
    rejectedCount,
    bins: binByInterval(sane, BURST.binSec),
    burstDetected: burst,
    tightestIntervalSec: tightest === Infinity ? null : tightest,
  }
}
