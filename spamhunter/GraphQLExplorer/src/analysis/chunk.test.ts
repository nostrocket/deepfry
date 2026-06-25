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
  SAFE_FIXED_OVERHEAD,
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

// WR-06: BODY_LIMIT_BYTES is documented as the load-bearing 413 trigger, but the author cap
// always binds first so the byte axis never binds at runtime and nothing measures the actual
// serialized body. These tests PIN the real worst-case serialized request body for a max
// chunk below 256 KiB, so the constant is not silently inert: a future field / perAuthor /
// hex-length change that pushes a max chunk over the limit fails HERE rather than in
// production (where, per WR-04/WR-05, the runtime 413 degrade has terminal gaps).
describe('BODY_LIMIT_BYTES (WR-06 — the byte axis is pinned, not inert)', () => {
  // A worst-case 64-hex author string (the longest a parseIdentifier-normalized author can be).
  const WORST_AUTHOR = 'f'.repeat(64)

  // The actual JSON request body urql POSTs: { query, variables }. The query document is a
  // fixed string; the variables carry kind / perAuthor / the authors array (the dominant term).
  // A deliberately over-long query-doc stand-in (SAFE_FIXED_OVERHEAD bytes) bounds the real
  // ~322-byte document from above, so this is a strict worst-case for the fixed overhead too.
  function serializedBodyBytes(authorCount: number): number {
    const authors = Array.from({ length: authorCount }, () => WORST_AUTHOR)
    const body = JSON.stringify({
      query: 'q'.repeat(SAFE_FIXED_OVERHEAD), // upper-bounds the real query document
      variables: { kind: TRIAGE.kind, perAuthor: TRIAGE.perAuthor, authors },
    })
    return new TextEncoder().encode(body).length // UTF-8 byte length, as the wire sees it
  }

  it('a full chunkSize() chunk of worst-case 64-hex authors serializes under BODY_LIMIT_BYTES', () => {
    expect(serializedBodyBytes(chunkSize())).toBeLessThan(BODY_LIMIT_BYTES)
  })

  it('even a 1000-author worst-case chunk (the hard cap) stays under BODY_LIMIT_BYTES', () => {
    // Defends the hard cap too — if chunkAuthors ever resolved to 1000, the body still fits.
    expect(serializedBodyBytes(1000)).toBeLessThan(BODY_LIMIT_BYTES)
  })

  it('byteBudgetAuthors is a sound lower bound: that many authors really do fit the body', () => {
    // The static budget must not over-promise — a budget-sized chunk must serialize under limit.
    expect(serializedBodyBytes(byteBudgetAuthors())).toBeLessThan(BODY_LIMIT_BYTES)
  })

  it('the per-author byte constant is conservative vs the measured marginal cost', () => {
    // Marginal cost of one more worst-case author must not exceed the SAFE_BYTES_PER_AUTHOR
    // budget — otherwise the byte axis would silently under-count.
    const marginal = serializedBodyBytes(1001) - serializedBodyBytes(1000)
    expect(marginal).toBeLessThanOrEqual(SAFE_BYTES_PER_AUTHOR)
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
