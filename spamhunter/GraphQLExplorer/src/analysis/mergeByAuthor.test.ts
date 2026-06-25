// TDD RED → GREEN for the load-bearing left-join merge-by-author (BATCH-03).
//
// mergeByAuthor is the single most important correctness pin in Phase 4: latestPerAuthor
// OMITS authors with zero matching events (contract §5/§8), so the response array does NOT
// line up positionally with the input array. The merge MUST left-join from the full deduped
// INPUT set keyed strictly by `author` — an input author absent from the response becomes a
// row with events:[] (an explicit "0 events" row), and index-zipping is forbidden because it
// silently misattributes one author's events to another and hides zero-match authors.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { mergeByAuthor, type TriageRow } from './mergeByAuthor'
import type { WindowEvent } from '../hooks/useAuthorWindow'

/** Build a minimal WindowEvent for a given author/id. */
function ev(id: string, pubkey: string): WindowEvent {
  return { id, pubkey, kind: 1, createdAt: 1_700_000_000, content: '', tags: [] }
}

describe('mergeByAuthor (left-join keyed by author)', () => {
  it('yields an explicit 0-events row for an input author absent from the response', () => {
    const rows = mergeByAuthor(['aaa', 'bbb'], [{ author: 'aaa', events: [ev('e1', 'aaa')] }])
    const bbb = rows.find((r) => r.author === 'bbb')
    expect(bbb).toBeDefined()
    expect(bbb!.events).toEqual([])
  })

  it('matches by author even when response order differs AND omits some input authors (NOT index-zip)', () => {
    // Input order: A, B, C, D. Response: only C and A, in REVERSE order, B and D omitted.
    // An index-zip would attribute C's events to A and A's to B — this pins match-by-author.
    const input = ['A', 'B', 'C', 'D']
    const groups = [
      { author: 'C', events: [ev('c1', 'C'), ev('c2', 'C')] },
      { author: 'A', events: [ev('a1', 'A')] },
    ]
    const rows = mergeByAuthor(input, groups)

    // Every output row's author equals its input hex and carries that author's OWN events.
    const byAuthor = new Map(rows.map((r) => [r.author, r]))
    expect(byAuthor.get('A')!.events.map((e) => e.id)).toEqual(['a1'])
    expect(byAuthor.get('C')!.events.map((e) => e.id)).toEqual(['c1', 'c2'])
    expect(byAuthor.get('B')!.events).toEqual([])
    expect(byAuthor.get('D')!.events).toEqual([])
    // Each event belongs to the row keyed by its own pubkey (no cross-attribution).
    for (const r of rows) {
      for (const e of r.events) expect(e.pubkey).toBe(r.author)
    }
  })

  it('produces exactly one row per input author, in input order', () => {
    const input = ['A', 'B', 'C', 'D']
    const rows: TriageRow[] = mergeByAuthor(input, [{ author: 'B', events: [ev('b1', 'B')] }])
    expect(rows.length).toBe(input.length)
    expect(rows.map((r) => r.author)).toEqual(input)
  })

  it('returns [] for an empty input set regardless of groups', () => {
    expect(mergeByAuthor([], [{ author: 'X', events: [ev('x1', 'X')] }])).toEqual([])
  })
})
