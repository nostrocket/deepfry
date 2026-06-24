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
import { analyzeRate, isSaneTs, type RateResult } from './rate'
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
