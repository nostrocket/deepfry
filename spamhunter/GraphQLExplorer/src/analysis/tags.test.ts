// TDD RED → GREEN for the defensive p/e/t tag aggregator (DRILL-03).
//
// analyzeTags is a PURE function over the fetched window's author-claimed tags. Its
// contract is asymmetric by design (mirrors RateResult): mass-mention fan-out and
// hashtag stuffing are suspicious-when-present signals; a benign window proves nothing,
// so TagsResult carries NO clean/ok/safe field. The aggregate massMention/stuffing
// booleans are signal-PRESENT flags, never a "clean" verdict.
//
// tags is attacker-controlled (schema [[String!]!] but values are author-supplied), so
// malformed rows are COUNTED in malformedTagRows (parity with rejectedCount) and skipped,
// never thrown on (Pitfall 3 / T-03-02). Thresholds come from thresholds.ts (TAGS) —
// fixtures build from the constants rather than hard-coding numbers.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { analyzeTags, type TagsResult } from './tags'
import { TAGS } from './thresholds'

/** Build a fixtures event of the structural subset analyzeTags consumes. */
function ev(id: string, tags: string[][]): { id: string; tags: string[][] } {
  return { id, tags }
}

describe('analyzeTags — asymmetry (no clean verdict)', () => {
  it('does not expose any clean/ok/safe field (asymmetry is structural)', () => {
    const result: TagsResult = analyzeTags([ev('1', [['p', 'abc']])])
    const keys = Object.keys(result)
    expect(keys).not.toContain('clean')
    expect(keys).not.toContain('ok')
    expect(keys).not.toContain('safe')
  })
})

describe('analyzeTags — p/t counting + top-N order', () => {
  it('counts p mentions and t hashtags, sorted descending by count', () => {
    const result = analyzeTags([
      ev('1', [['p', 'alice'], ['p', 'bob'], ['t', 'spam']]),
      ev('2', [['p', 'alice'], ['t', 'spam'], ['t', 'crypto']]),
      ev('3', [['p', 'alice'], ['t', 'spam']]),
    ])
    // alice 3, bob 1
    expect(result.topMentions[0]).toEqual({ value: 'alice', count: 3 })
    expect(result.topMentions.find((m) => m.value === 'bob')?.count).toBe(1)
    // spam 3, crypto 1
    expect(result.topHashtags[0]).toEqual({ value: 'spam', count: 3 })
    expect(result.topHashtags.find((h) => h.value === 'crypto')?.count).toBe(1)
  })

  it('sums e references into eventRefCount', () => {
    const result = analyzeTags([
      ev('1', [['e', 'id1'], ['e', 'id2']]),
      ev('2', [['e', 'id3']]),
    ])
    expect(result.eventRefCount).toBe(3)
  })
})

describe('analyzeTags — defensive parsing (Pitfall 3, never throws)', () => {
  it('counts malformed rows and excludes them from counts', () => {
    const result = analyzeTags([
      ev('1', [
        [], // empty row → malformed
        ['p'], // p with no value → malformed
        ['p', 'realpubkey'], // valid
        // a non-array row, forced past the type system to mirror hostile input
        42 as unknown as string[],
      ]),
    ])
    expect(result.malformedTagRows).toBe(3)
    expect(result.topMentions).toEqual([{ value: 'realpubkey', count: 1 }])
  })

  it('never throws on hostile tag shapes', () => {
    expect(() =>
      analyzeTags([
        ev('1', [null as unknown as string[], ['t'], ['p', undefined as unknown as string]]),
      ]),
    ).not.toThrow()
  })
})

describe('analyzeTags — high-tag-count outlier (TAGS.highTagCount)', () => {
  it('flags an event whose total tag count exceeds TAGS.highTagCount', () => {
    // Build from the constant, not a literal: one over the threshold.
    const tags: string[][] = []
    for (let i = 0; i < TAGS.highTagCount + 1; i++) tags.push(['e', `id${i}`])
    const result = analyzeTags([ev('big', tags), ev('small', [['e', 'x']])])
    const outlier = result.outlierEvents.find((o) => o.id === 'big')
    expect(outlier).toBeDefined()
    expect(outlier?.tagCount).toBe(TAGS.highTagCount + 1)
    expect(result.outlierEvents.find((o) => o.id === 'small')).toBeUndefined()
  })
})

describe('analyzeTags — massMention signal (TAGS.massMention)', () => {
  it('sets the outlier entry + aggregate massMention when p fan-out exceeds the threshold', () => {
    const tags: string[][] = []
    for (let i = 0; i < TAGS.massMention + 1; i++) tags.push(['p', `pk${i}`])
    const result = analyzeTags([ev('fanout', tags)])
    const outlier = result.outlierEvents.find((o) => o.id === 'fanout')
    expect(outlier?.massMention).toBe(true)
    expect(result.massMention).toBe(true)
  })
})

describe('analyzeTags — stuffing signal (TAGS.stuffing)', () => {
  it('sets the outlier entry + aggregate stuffing when t count exceeds the threshold', () => {
    const tags: string[][] = []
    for (let i = 0; i < TAGS.stuffing + 1; i++) tags.push(['t', `tag${i}`])
    const result = analyzeTags([ev('stuffer', tags)])
    const outlier = result.outlierEvents.find((o) => o.id === 'stuffer')
    expect(outlier?.stuffing).toBe(true)
    expect(result.stuffing).toBe(true)
  })
})

describe('analyzeTags — benign + degenerate input', () => {
  it('leaves both aggregate flags false for a benign window', () => {
    const result = analyzeTags([
      ev('1', [['p', 'alice'], ['t', 'gm']]),
      ev('2', [['e', 'id1']]),
    ])
    expect(result.massMention).toBe(false)
    expect(result.stuffing).toBe(false)
    expect(result.outlierEvents).toEqual([])
  })

  it('returns an all-zero/empty result for 0 events (flags false)', () => {
    const result = analyzeTags([])
    expect(result.analyzedCount).toBe(0)
    expect(result.malformedTagRows).toBe(0)
    expect(result.topMentions).toEqual([])
    expect(result.topHashtags).toEqual([])
    expect(result.eventRefCount).toBe(0)
    expect(result.outlierEvents).toEqual([])
    expect(result.massMention).toBe(false)
    expect(result.stuffing).toBe(false)
  })
})
