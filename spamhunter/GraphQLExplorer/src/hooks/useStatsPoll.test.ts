// TDD RED → GREEN for the useStatsPoll nudge semantics (STATS-02).
//
// The hook itself is a React effect that touches `document` (Page Visibility)
// and the urql client; its *decision logic* — "does a maxLevId observation flip
// the corpus-changed nudge?" — is extracted into the pure `shouldNudge` helper so
// it can be exercised here in the existing Node test environment without a DOM or
// a network. This is the behavior the contract cares about (contract §9 / Pitfall
// "aggressive polling"): the nudge flips ONLY on a strict increase versus the last
// seen value, and the FIRST observation (no prior value) must NOT nudge.
import { describe, it, expect } from 'vitest'
import { shouldNudge, POLL_INTERVAL_MS } from './useStatsPoll'

describe('shouldNudge (maxLevId-diff nudge flag)', () => {
  it('does not nudge on the first observation (no prior value)', () => {
    expect(shouldNudge(null, 100)).toBe(false)
  })

  it('nudges when maxLevId strictly increases', () => {
    expect(shouldNudge(100, 101)).toBe(true)
    expect(shouldNudge(47928105, 47928200)).toBe(true)
  })

  it('does not nudge when maxLevId is unchanged', () => {
    expect(shouldNudge(100, 100)).toBe(false)
  })

  it('does not nudge when maxLevId decreases (defensive — monotonic counter)', () => {
    expect(shouldNudge(100, 99)).toBe(false)
  })
})

describe('POLL_INTERVAL_MS (tunable default)', () => {
  it('defaults to a seconds-scale interval (5000ms)', () => {
    expect(POLL_INTERVAL_MS).toBe(5000)
  })

  it('is seconds-scale, never sub-second (anti-aggressive-polling)', () => {
    expect(POLL_INTERVAL_MS).toBeGreaterThanOrEqual(2000)
    expect(POLL_INTERVAL_MS).toBeLessThanOrEqual(10000)
  })
})
