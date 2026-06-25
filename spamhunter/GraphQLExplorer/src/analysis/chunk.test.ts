// TDD RED → GREEN for the dual-axis chunk-sizing math (BATCH-02).
//
// chunk.ts is a PURE module: it computes the static chunk size from the author cap and a
// conservative byte budget, and slices a deduped hex list into chunks. The 413 halve-and-
// retry recursion lives in the HOOK (useLatestPerAuthor), never here — chunk.ts is
// network-free. The load-bearing finding (RESEARCH): the <=1000-author cap binds long
// before the 256 KiB body budget at perAuthor=5, so chunkSize() resolves to TRIAGE.chunkAuthors.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import {
  chunkAuthors,
  chunkSize,
  byteBudgetAuthors,
  SAFE_BYTES_PER_AUTHOR,
  BODY_LIMIT_BYTES,
} from './chunk'
import { TRIAGE } from './thresholds'

describe('byteBudgetAuthors (the non-binding axis)', () => {
  it('is far above the 1000-author cap at perAuthor=5 (the cap binds first)', () => {
    expect(byteBudgetAuthors()).toBeGreaterThan(1000)
  })

  it('derives from the body limit and per-author byte constant', () => {
    // sanity: the budget must be strictly smaller than (limit / per-author) and positive
    expect(byteBudgetAuthors()).toBeLessThanOrEqual(BODY_LIMIT_BYTES / SAFE_BYTES_PER_AUTHOR)
    expect(byteBudgetAuthors()).toBeGreaterThan(0)
  })
})

describe('chunkSize (static, dual-axis min)', () => {
  it('resolves to the conservative author cap (500), not the byte budget', () => {
    expect(chunkSize()).toBe(TRIAGE.chunkAuthors)
    expect(chunkSize()).toBe(500)
  })

  it('never exceeds the hard 1000-author cap', () => {
    expect(chunkSize()).toBeLessThanOrEqual(1000)
  })
})

describe('chunkAuthors (pure slice loop)', () => {
  it('slices 1100 hexes at size 500 into lengths [500, 500, 100]', () => {
    const hexes = Array.from({ length: 1100 }, (_, i) => String(i))
    const chunks = chunkAuthors(hexes, 500)
    expect(chunks.map((c) => c.length)).toEqual([500, 500, 100])
  })

  it('returns [] for empty input', () => {
    expect(chunkAuthors([], 500)).toEqual([])
  })

  it('preserves order and partitions every element exactly once', () => {
    const hexes = ['a', 'b', 'c', 'd', 'e']
    const chunks = chunkAuthors(hexes, 2)
    expect(chunks).toEqual([['a', 'b'], ['c', 'd'], ['e']])
    expect(chunks.flat()).toEqual(hexes)
  })
})
