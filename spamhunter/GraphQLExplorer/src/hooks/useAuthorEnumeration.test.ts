// Regression coverage for the enumeration no-progress detector (WR-03).
//
// The enumeration loop in useAuthorEnumeration is a React effect over the urql client; its
// termination *decision* — "is this page making no progress while the backend still claims
// more, so the loop MUST stop instead of spinning forever?" — is extracted into the pure
// `isStuckPage` helper so it can be exercised here in the Node test env (no DOM, no network),
// mirroring the shouldNudge pattern in useStatsPoll.test.ts.
//
// Combined with the TRIAGE.maxEnumPages hard ceiling wired into the loop, this guarantees the
// "no infinite loop" invariant: an empty page with hasMore:true, or a non-advancing cursor,
// terminates with a surfaced error rather than relying solely on the user clicking Stop.
import { describe, it, expect } from 'vitest'
import { isStuckPage } from './useAuthorEnumeration'
import { TRIAGE } from '../analysis/thresholds'

describe('isStuckPage (WR-03 no-progress detector)', () => {
  it('is STUCK: no new authors AND cursor did not advance, but hasMore is still true', () => {
    expect(isStuckPage(false, false, true)).toBe(true)
  })

  it('is NOT stuck while real progress is being made (new authors)', () => {
    expect(isStuckPage(true, false, true)).toBe(false)
    expect(isStuckPage(true, true, true)).toBe(false)
  })

  it('is NOT stuck while the cursor keeps advancing (even on an empty page)', () => {
    // a page that added no NEW distinct authors (all dups) but advanced the cursor is still
    // forward progress — the next page may hold new keys.
    expect(isStuckPage(false, true, true)).toBe(false)
  })

  it('is NEVER stuck on a final page (hasMore:false is a clean, allowed end)', () => {
    expect(isStuckPage(false, false, false)).toBe(false)
    expect(isStuckPage(true, true, false)).toBe(false)
  })
})

describe('TRIAGE.maxEnumPages (WR-03 hard ceiling backstop)', () => {
  it('is a positive, finite last-resort page ceiling', () => {
    expect(TRIAGE.maxEnumPages).toBeGreaterThan(0)
    expect(Number.isFinite(TRIAGE.maxEnumPages)).toBe(true)
  })

  it('is generously above any realistic corpus (does not cap real enumeration prematurely)', () => {
    // enumLimit-sized pages × the ceiling must admit millions of distinct authors.
    expect(TRIAGE.maxEnumPages * TRIAGE.enumLimit).toBeGreaterThanOrEqual(1_000_000)
  })
})
