// Regression coverage for the chunk-degrade decision (WR-01 / WR-04 / WR-05).
//
// The 413 halve-and-retry + TOO_MANY_AUTHORS degrade logic lives inside the React hook
// useLatestPerAuthor; its *decision* — "given an error kind and a chunk length, do we shrink,
// offer a plain Retry, or present a terminal non-retryable hard failure?" — is extracted into
// the pure `chunkDegradeDecision` helper so it can be exercised here in the Node test env
// (no DOM, no network), mirroring the shouldNudge pattern in useStatsPoll.test.ts.
//
// The behaviors pinned here are the review findings:
//   WR-04: TOO_MANY_AUTHORS degrades by SHRINKING (like 413), not a futile Retry, while > 1.
//   WR-05: a single-author chunk that still 413s is TERMINAL (cannot shrink further).
//   WR-04 terminal: TOO_MANY_AUTHORS at length 1 (backend cap below one author) is TERMINAL.
import { describe, it, expect } from 'vitest'
import { chunkDegradeDecision } from './useLatestPerAuthor'

describe('chunkDegradeDecision (413 / TOO_MANY_AUTHORS degrade)', () => {
  it('shrinks a multi-author 413 chunk (halve-and-retry, not a Retry)', () => {
    expect(chunkDegradeDecision('PAYLOAD_TOO_LARGE', 500)).toBe('shrink')
    expect(chunkDegradeDecision('PAYLOAD_TOO_LARGE', 2)).toBe('shrink')
  })

  it('WR-04: shrinks a multi-author TOO_MANY_AUTHORS chunk like a 413 (never a futile Retry)', () => {
    expect(chunkDegradeDecision('TOO_MANY_AUTHORS', 500)).toBe('shrink')
    expect(chunkDegradeDecision('TOO_MANY_AUTHORS', 2)).toBe('shrink')
  })

  it('WR-05: a single-author chunk that still 413s is terminal (nothing left to shrink)', () => {
    expect(chunkDegradeDecision('PAYLOAD_TOO_LARGE', 1)).toBe('terminal')
  })

  it('WR-04 terminal: TOO_MANY_AUTHORS at length 1 (cap below one author) is terminal', () => {
    expect(chunkDegradeDecision('TOO_MANY_AUTHORS', 1)).toBe('terminal')
  })

  it('every other classified error is a plain retryable per-chunk error', () => {
    for (const kind of ['NETWORK', 'NOT_READY', 'INVALID_CURSOR', 'VALIDATION', 'INTERNAL'] as const) {
      expect(chunkDegradeDecision(kind, 500)).toBe('retryable')
      expect(chunkDegradeDecision(kind, 1)).toBe('retryable')
    }
  })
})
