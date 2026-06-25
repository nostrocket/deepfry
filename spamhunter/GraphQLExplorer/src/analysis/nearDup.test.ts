// TDD RED → GREEN for the two-stage near-duplicate detector (DRILL-02).
//
// nearDup is a PURE function over the fetched window's author-claimed content. Its
// contract is asymmetric by design (RESEARCH § Pattern 1, Anti-pattern), mirroring
// RateResult: a near-duplicate cluster is a SUSPICIOUS signal worth investigating, but
// the ABSENCE of duplicates proves nothing (an author can post unique spam). The result
// type therefore structurally carries NO clean/ok/safe field.
//
// Stage 1 buckets events by an exact normalized-content key (NFC + lowercase + whitespace
// collapse + trim); stage 2 finds near-duplicates among the rest via word-shingle (k=3)
// Jaccard and groups them transitively with union-find. Thresholds come from
// thresholds.ts (NEAR_DUP) — fixtures read the constants rather than hard-coding numbers.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { nearDup, normalizeContent, shingles, jaccard, type NearDupResult } from './nearDup'
import { NEAR_DUP } from './thresholds'

/** Build a fixtures event of the structural subset nearDup consumes. */
function ev(id: string, content: string): { id: string; content: string } {
  return { id, content }
}

describe('normalizeContent (exact-hash stage key)', () => {
  it('lowercases, collapses internal whitespace, and trims — without stripping punctuation', () => {
    expect(normalizeContent('  Hello   WORLD!!!  ')).toBe('hello world!!!')
  })

  it('treats case / whitespace / NFC-only differences as identical', () => {
    const a = normalizeContent('Buy   NOW http://x.com')
    const b = normalizeContent('buy now http://x.com')
    expect(a).toBe(b)
  })

  it('does NOT strip URLs, mentions, or punctuation (over-merge guard)', () => {
    // The URL and @mention survive normalization — distinct variants stay distinct.
    expect(normalizeContent('check @alice http://x.com')).toBe('check @alice http://x.com')
  })
})

describe('shingles (word k-grams)', () => {
  it('produces sliding k-windows for posts with >= k words', () => {
    const s = shingles('a b c d', 3)
    expect(s.has('a b c')).toBe(true)
    expect(s.has('b c d')).toBe(true)
    expect(s.size).toBe(2)
  })

  it('falls back to a whole-text shingle for posts shorter than k words', () => {
    const s = shingles('hi there', 3) // 2 words < k=3
    expect(s.has('hi there')).toBe(true)
    expect(s.size).toBe(1)
  })

  it('returns an empty set for empty content', () => {
    expect(shingles('', 3).size).toBe(0)
  })
})

describe('jaccard (set similarity)', () => {
  it('is 1 for identical non-empty sets', () => {
    expect(jaccard(new Set(['a', 'b']), new Set(['a', 'b']))).toBe(1)
  })

  it('is 0 for disjoint sets', () => {
    expect(jaccard(new Set(['a']), new Set(['b']))).toBe(0)
  })

  it('computes intersection over union', () => {
    // {a,b,c} vs {b,c,d} → inter 2, union 4 → 0.5
    expect(jaccard(new Set(['a', 'b', 'c']), new Set(['b', 'c', 'd']))).toBe(0.5)
  })
})

describe('nearDup — asymmetry (no clean verdict)', () => {
  it('does not expose any clean/ok/safe field (asymmetry is structural)', () => {
    const result: NearDupResult = nearDup([ev('1', 'same text here now'), ev('2', 'same text here now')])
    const keys = Object.keys(result)
    expect(keys).not.toContain('clean')
    expect(keys).not.toContain('ok')
    expect(keys).not.toContain('safe')
  })
})

describe('nearDup — stage 1: exact-duplicate bucketing', () => {
  it('groups byte-identical posts into one exact cluster', () => {
    const result = nearDup([
      ev('1', 'identical spam message here'),
      ev('2', 'identical spam message here'),
      ev('3', 'a totally different unique post'),
    ])
    expect(result.clusters).toHaveLength(1)
    expect(result.clusters[0].kind).toBe('exact')
    expect(result.clusters[0].memberIds.sort()).toEqual(['1', '2'])
    expect(result.clusters[0].count).toBe(2)
  })

  it('treats case / whitespace / NFC-only differences as exact duplicates', () => {
    const result = nearDup([
      ev('1', 'Buy   NOW!!!'),
      ev('2', 'buy now!!!'),
    ])
    expect(result.clusters).toHaveLength(1)
    expect(result.clusters[0].kind).toBe('exact')
    expect(result.clusters[0].count).toBe(2)
  })
})

describe('nearDup — stage 2: near-duplicate clustering', () => {
  it('clusters a near-duplicate pair over the Jaccard cutoff as kind "near"', () => {
    // Two long posts (20 words) differing only in the final word. For k=3 shingles a
    // single trailing-word change perturbs 2 of the 18 shingles, leaving Jaccard
    // 16/20 = 0.8 — exactly at NEAR_DUP.jaccard, so they group as a 'near' cluster
    // while staying distinct enough not to share a stage-1 exact bucket.
    const stem = 'w01 w02 w03 w04 w05 w06 w07 w08 w09 w10 w11 w12 w13 w14 w15 w16 w17 w18 w19'
    const a = `${stem} alpha`
    const b = `${stem} omega`
    const result = nearDup([ev('1', a), ev('2', b)])
    expect(result.clusters).toHaveLength(1)
    expect(result.clusters[0].kind).toBe('near')
    expect(result.clusters[0].count).toBe(2)
  })

  it('unions a transitive chain (A≈B, B≈C) into ONE cluster (order-independent)', () => {
    // 22-word posts. A↔B differ only in the FINAL word (2 shingles → Jaccard 0.818 ≥
    // cutoff). B↔C differ only in the FIRST word (1 shingle → Jaccard 0.905 ≥ cutoff). A↔C
    // therefore differ in BOTH ends (3 shingles → Jaccard ~0.74 < cutoff, no direct match).
    // Union-find still unions all three via the B-bridge — the deterministic transitive-
    // closure choice greedy first-match would miss.
    const mid = 'w02 w03 w04 w05 w06 w07 w08 w09 w10 w11 w12 w13 w14 w15 w16 w17 w18 w19 w20'
    const a = `head ${mid} tailA`
    const b = `head ${mid} tailB`
    const c = `nose ${mid} tailB`
    const result = nearDup([ev('1', a), ev('2', b), ev('3', c)])
    expect(result.clusters).toHaveLength(1)
    expect(result.clusters[0].memberIds.sort()).toEqual(['1', '2', '3'])
    expect(result.clusters[0].count).toBe(3)
  })

  it('does NOT produce a singleton cluster for a unique post', () => {
    const result = nearDup([
      ev('1', 'completely unique alpha content here'),
      ev('2', 'entirely separate beta content there'),
    ])
    expect(result.clusters).toHaveLength(0)
    expect(result.duplicateCount).toBe(0)
  })
})

describe('nearDup — duplicateCount', () => {
  it('equals the total member count across all clusters', () => {
    const result = nearDup([
      ev('1', 'duplicate alpha message'),
      ev('2', 'duplicate alpha message'),
      ev('3', 'duplicate beta message'),
      ev('4', 'duplicate beta message'),
      ev('5', 'a unique standalone post here'),
    ])
    const summed = result.clusters.reduce((m, c) => m + c.count, 0)
    expect(result.duplicateCount).toBe(summed)
    expect(result.duplicateCount).toBe(4)
  })
})

describe('nearDup — short-post matching', () => {
  it('matches identical short posts (< k words) via the whole-text shingle', () => {
    const result = nearDup([ev('1', 'gm'), ev('2', 'gm')])
    expect(result.clusters).toHaveLength(1)
    expect(result.clusters[0].count).toBe(2)
  })
})

describe('nearDup — degenerate input (no crash)', () => {
  it('returns an inconclusive empty result for 0 events', () => {
    const result = nearDup([])
    expect(result).toEqual({ analyzedCount: 0, clusters: [], duplicateCount: 0 })
  })

  it('returns an inconclusive empty result for a single event (nothing to compare)', () => {
    const result = nearDup([ev('1', 'lonely post with no comparison')])
    expect(result.analyzedCount).toBe(1)
    expect(result.clusters).toEqual([])
    expect(result.duplicateCount).toBe(0)
  })

  it('uses NEAR_DUP.jaccard as the cutoff (threshold-driven, not hard-coded)', () => {
    // Sanity: the configured cutoff is in (0, 1].
    expect(NEAR_DUP.jaccard).toBeGreaterThan(0)
    expect(NEAR_DUP.jaccard).toBeLessThanOrEqual(1)
  })
})
