// TDD RED → GREEN for the asymmetric posting-rate / burst analyzer (DRILL-01).
//
// analyzeRate is a PURE function over the fetched window's author-claimed createdAt
// values. Its contract is asymmetric by design (RESEARCH § Pattern 3, Anti-pattern):
//   - a tight cluster (burst) is a SUSPICIOUS signal → burstDetected: true
//   - sparse/quiet timing is INCONCLUSIVE, never "clean" → burstDetected: false, and
//     the result type structurally carries NO clean/ok/safe field.
// Because createdAt is author-claimed and forgeable (contract §8, 64-bit), the analyzer
// bounds-checks every value via isSaneTs and COUNTS the out-of-range ones in
// rejectedCount rather than silently dropping or mis-computing them (Pitfall 3).
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { analyzeRate, isSaneTs, MIN_TS, MAX_TS, type RateResult } from './rate'
import { BURST } from './thresholds'

// A fixed base epoch (seconds) well inside the sane range for building fixtures.
const BASE = 1_700_000_000

describe('isSaneTs (forgeable createdAt bounds-check)', () => {
  it('accepts a plausible epoch-seconds value', () => {
    expect(isSaneTs(BASE)).toBe(true)
    expect(isSaneTs(0)).toBe(true)
  })

  it('rejects negatives, far-future, and beyond MAX_SAFE_INTEGER', () => {
    expect(isSaneTs(-1)).toBe(false)
    expect(isSaneTs(4_102_444_801)).toBe(false) // just past 2100-01-01
    expect(isSaneTs(Number.MAX_SAFE_INTEGER + 2)).toBe(false)
    expect(isSaneTs(NaN)).toBe(false)
    expect(isSaneTs(1.5)).toBe(false) // not a safe integer
  })
})

describe('analyzeRate — asymmetry (no clean verdict)', () => {
  it('does not expose any clean/ok/safe field (asymmetry is structural)', () => {
    const result: RateResult = analyzeRate([BASE, BASE + 100000, BASE + 200000])
    const keys = Object.keys(result)
    expect(keys).not.toContain('clean')
    expect(keys).not.toContain('ok')
    expect(keys).not.toContain('safe')
  })
})

describe('analyzeRate — burst detection (DRILL-01)', () => {
  it('flags a tight cluster of minEvents within windowSec as a burst', () => {
    // minEvents timestamps packed inside windowSec → burst.
    const cluster: number[] = []
    for (let i = 0; i < BURST.minEvents; i++) {
      cluster.push(BASE + i * Math.floor(BURST.windowSec / BURST.minEvents))
    }
    const result = analyzeRate(cluster)
    expect(result.burstDetected).toBe(true)
    expect(result.analyzedCount).toBe(BURST.minEvents)
  })

  it('does NOT flag sparse timestamps spaced well beyond windowSec', () => {
    const sparse = [
      BASE,
      BASE + BURST.windowSec * 100,
      BASE + BURST.windowSec * 200,
      BASE + BURST.windowSec * 300,
      BASE + BURST.windowSec * 400,
      BASE + BURST.windowSec * 500,
    ]
    const result = analyzeRate(sparse)
    expect(result.burstDetected).toBe(false)
    expect(result.analyzedCount).toBe(sparse.length)
  })
})

describe('analyzeRate — degenerate input (no crash, no negative interval)', () => {
  it('returns an inconclusive empty result for 0 timestamps', () => {
    const result = analyzeRate([])
    expect(result.burstDetected).toBe(false)
    expect(result.bins).toEqual([])
    expect(result.tightestIntervalSec).toBeNull()
    expect(result.analyzedCount).toBe(0)
    expect(result.rejectedCount).toBe(0)
  })

  it('returns an inconclusive empty result for a single timestamp', () => {
    const result = analyzeRate([BASE])
    expect(result.burstDetected).toBe(false)
    expect(result.bins).toEqual([])
    expect(result.tightestIntervalSec).toBeNull()
    expect(result.analyzedCount).toBe(1)
  })
})

describe('analyzeRate — forged / out-of-range rejection (Pitfall 3)', () => {
  it('excludes negative, far-future, and unsafe values and counts them in rejectedCount', () => {
    const input = [
      BASE,
      -1, // negative
      4_102_444_801, // post-2100
      Number.MAX_SAFE_INTEGER + 2, // beyond safe integer
      BASE + 30,
    ]
    const result = analyzeRate(input)
    expect(result.rejectedCount).toBe(3)
    expect(result.analyzedCount).toBe(2)
  })

  it('derives the burst from the sane subset; tightestIntervalSec is never negative', () => {
    // A forged far-future value mixed into a valid tight burst.
    const cluster: number[] = []
    for (let i = 0; i < BURST.minEvents; i++) {
      cluster.push(BASE + i * Math.floor(BURST.windowSec / BURST.minEvents))
    }
    const input = [...cluster, 4_102_444_801, -5]
    const result = analyzeRate(input)
    expect(result.rejectedCount).toBe(2)
    expect(result.burstDetected).toBe(true)
    expect(result.tightestIntervalSec).not.toBeNull()
    expect(result.tightestIntervalSec as number).toBeGreaterThanOrEqual(0)
  })

  it('does not produce a negative interval even when input is unsorted', () => {
    const result = analyzeRate([BASE + 300, BASE, BASE + 100, BASE + 50])
    expect(result.tightestIntervalSec).not.toBeNull()
    expect(result.tightestIntervalSec as number).toBeGreaterThanOrEqual(0)
  })
})

describe('analyzeRate — tightestIntervalSec', () => {
  it('equals the smallest gap between consecutive sane timestamps', () => {
    // Gaps after sorting: 50, 100, 200 → tightest 50.
    const result = analyzeRate([BASE, BASE + 250, BASE + 50, BASE + 150])
    expect(result.tightestIntervalSec).toBe(50)
  })
})

describe('analyzeRate — binning over a large-but-sane gap (WR-04 regression)', () => {
  it('bins two events ~4.1e9s apart without iterating the empty span', () => {
    // MIN_TS=0, MAX_TS=4_102_444_800: two SANE timestamps can be the full sane
    // range apart. The old loop advanced one empty bin (binSec) at a time — ~1.14M
    // iterations for this gap. The integer-division jump bins it in O(events).
    // Each event lands alone in its own bin, anchored at the first timestamp.
    const start = MIN_TS
    const end = MAX_TS // exactly the far edge of the sane range
    const result = analyzeRate([start, end])

    // Both timestamps are sane and analyzed; no empty bins are emitted.
    expect(result.analyzedCount).toBe(2)
    expect(result.rejectedCount).toBe(0)
    expect(result.bins.length).toBe(2)

    // First bin is anchored at the origin (start) with the single first event.
    expect(result.bins[0]).toEqual({ start, count: 1 })
    // Second bin's start is origin + k*binSec for the bin CONTAINING end.
    const expectedSecondStart = start + Math.floor((end - start) / BURST.binSec) * BURST.binSec
    expect(result.bins[1]).toEqual({ start: expectedSecondStart, count: 1 })
    // The two events are too far apart to be a burst.
    expect(result.burstDetected).toBe(false)
  })

  it('preserves multi-event-per-bin grouping across a large gap', () => {
    // A tight pair at the origin, then a tight pair ~MAX_TS away. Each pair shares
    // a bin (same integer bin index); the empty span between produces no bin.
    const farBase = MAX_TS - 10
    const result = analyzeRate([BASE, BASE + 5, farBase, farBase + 5])
    expect(result.analyzedCount).toBe(4)
    expect(result.bins.length).toBe(2)
    expect(result.bins[0].count).toBe(2)
    expect(result.bins[1].count).toBe(2)
  })
})
