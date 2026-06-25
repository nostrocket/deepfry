// TDD RED → GREEN for the kind histogram + bounds-check analyzer (DRILL-04).
//
// analyzeKinds is a PURE function over the fetched window's author-claimed kind/createdAt.
// Its contract is asymmetric (mirrors RateResult): the histogram is a neutral data view,
// and a forged/out-of-range kind or createdAt is flagged into outOfRangeCount rather than
// mis-charted. KindsResult carries NO clean/ok/safe field.
//
// BOUNDS DISCIPLINE (T-03-01): both kind and createdAt are forgeable 64-bit values. The
// analyzer REUSES isSaneTs from ./rate for createdAt (no re-implementation) and applies
// Number.isSafeInteger(kind) && kind >= 0 for kind; either failing flags the event into
// outOfRangeCount and excludes it from the histogram.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { analyzeKinds, type KindsResult } from './kinds'
import { KIND_NAMES } from './kindNames'
import { MAX_TS } from './rate'

const BASE = 1_700_000_000

/** Build a fixtures event of the structural subset analyzeKinds consumes. */
function ev(kind: number, createdAt: number): { kind: number; createdAt: number } {
  return { kind, createdAt }
}

describe('analyzeKinds — asymmetry (no clean verdict)', () => {
  it('does not expose any clean/ok/safe field (asymmetry is structural)', () => {
    const result: KindsResult = analyzeKinds([ev(1, BASE), ev(1, BASE + 10)])
    const keys = Object.keys(result)
    expect(keys).not.toContain('clean')
    expect(keys).not.toContain('ok')
    expect(keys).not.toContain('safe')
  })
})

describe('analyzeKinds — histogram', () => {
  it('buckets sane events by kind, sorted descending by count', () => {
    const result = analyzeKinds([
      ev(1, BASE),
      ev(1, BASE + 1),
      ev(1, BASE + 2),
      ev(7, BASE + 3),
      ev(7, BASE + 4),
      ev(0, BASE + 5),
    ])
    expect(result.analyzedCount).toBe(6)
    expect(result.outOfRangeCount).toBe(0)
    expect(result.bins[0]).toEqual({ kind: 1, count: 3 })
    expect(result.bins[1]).toEqual({ kind: 7, count: 2 })
    expect(result.bins[2]).toEqual({ kind: 0, count: 1 })
  })

  it('breaks count ties by ascending kind', () => {
    const result = analyzeKinds([ev(7, BASE), ev(1, BASE + 1)])
    // both count 1 → ascending kind: 1 before 7
    expect(result.bins).toEqual([
      { kind: 1, count: 1 },
      { kind: 7, count: 1 },
    ])
  })
})

describe('analyzeKinds — forged/out-of-range bounds-check (T-03-01)', () => {
  it('flags a forged kind (negative / non-safe-integer) and does NOT bucket it', () => {
    const result = analyzeKinds([
      ev(1, BASE),
      ev(-1, BASE + 1), // negative kind
      ev(1.5, BASE + 2), // non-safe-integer kind
      ev(Number.MAX_SAFE_INTEGER + 2, BASE + 3), // beyond safe integer
    ])
    expect(result.outOfRangeCount).toBe(3)
    expect(result.analyzedCount).toBe(1)
    expect(result.bins).toEqual([{ kind: 1, count: 1 }])
  })

  it('flags a forged createdAt (past MAX_TS) via the reused isSaneTs and does NOT bucket it', () => {
    const result = analyzeKinds([
      ev(1, BASE),
      ev(1, MAX_TS + 1), // forged far-future timestamp
      ev(1, -5), // negative timestamp
    ])
    expect(result.outOfRangeCount).toBe(2)
    expect(result.analyzedCount).toBe(1)
    expect(result.bins).toEqual([{ kind: 1, count: 1 }])
  })
})

describe('analyzeKinds — degenerate input (no crash)', () => {
  it('returns an empty result for 0 events', () => {
    const result = analyzeKinds([])
    expect(result).toEqual({ analyzedCount: 0, outOfRangeCount: 0, bins: [] })
  })
})

describe('analyzeKinds — hostile/malformed kind & createdAt (WR-04, parity with tags.ts)', () => {
  // The `page.events as WindowEvent[]` cast in useAuthorWindow can deliver kind/createdAt
  // that are null/undefined/non-number (partial-error payload) the type checker cannot see.
  // Number.isSafeInteger returns false for all of those, so the event is flagged into
  // outOfRangeCount and excluded — never thrown, never mis-charted.

  it('flags null/undefined kind into outOfRangeCount without throwing', () => {
    const events = [
      { kind: 1, createdAt: BASE },
      { kind: null, createdAt: BASE + 1 },
      { kind: undefined, createdAt: BASE + 2 },
    ]
    let result: KindsResult
    expect(() => {
      result = analyzeKinds(events as unknown as { kind: number; createdAt: number }[])
    }).not.toThrow()
    expect(result!.outOfRangeCount).toBe(2)
    expect(result!.analyzedCount).toBe(1)
    expect(result!.bins).toEqual([{ kind: 1, count: 1 }])
  })

  it('flags null/undefined/non-number createdAt into outOfRangeCount without throwing', () => {
    const events = [
      { kind: 1, createdAt: BASE },
      { kind: 1, createdAt: null },
      { kind: 1, createdAt: undefined },
      { kind: 1, createdAt: 'not-a-number' },
    ]
    let result: KindsResult
    expect(() => {
      result = analyzeKinds(events as unknown as { kind: number; createdAt: number }[])
    }).not.toThrow()
    expect(result!.outOfRangeCount).toBe(3)
    expect(result!.analyzedCount).toBe(1)
    expect(result!.bins).toEqual([{ kind: 1, count: 1 }])
  })

  it('flags non-number (object) kind without throwing', () => {
    const events = [
      { kind: { evil: true }, createdAt: BASE },
      { kind: 1, createdAt: BASE + 1 },
    ]
    let result: KindsResult
    expect(() => {
      result = analyzeKinds(events as unknown as { kind: number; createdAt: number }[])
    }).not.toThrow()
    expect(result!.outOfRangeCount).toBe(1)
    expect(result!.analyzedCount).toBe(1)
  })
})

describe('KIND_NAMES — NIP lookup', () => {
  it('labels known kinds and omits unknown ones', () => {
    expect(KIND_NAMES[1]).toBe('Short Text Note')
    expect(KIND_NAMES[0]).toBe('Metadata')
    expect(KIND_NAMES[9735]).toBe('Zap')
    expect(KIND_NAMES[424242]).toBeUndefined()
  })
})
