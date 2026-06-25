// TDD RED → GREEN for the per-author triage adapter (BATCH-03).
//
// triageAuthor is a PURE fan-in over the three existing analyzers (analyzeRate / nearDup /
// analyzeTags) producing four transparent indicators: eventCount, burst, nearDup, tagFanOut.
// It carries NO clean/ok/safe/score field — the asymmetry inherited from Phases 2–3 (a
// tripped indicator is suspicious-when-present; absence is INCONCLUSIVE, never "clean").
// eventCount is a neutral 0..perAuthor denominator, never "worse". tagFanOut is
// massMention OR stuffing.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { triageAuthor, type TriageIndicators } from './triage'
import { TAGS, BURST } from './thresholds'
import type { WindowEvent } from '../hooks/useAuthorWindow'

function ev(overrides: Partial<WindowEvent>): WindowEvent {
  return {
    id: 'id-' + Math.random(),
    pubkey: 'author',
    kind: 1,
    createdAt: 1_700_000_000,
    content: '',
    tags: [],
    ...overrides,
  }
}

describe('triageAuthor (4 analyzers → 4 indicators)', () => {
  it('a 0-event input is the neutral all-false denominator', () => {
    expect(triageAuthor([])).toEqual<TriageIndicators>({
      eventCount: 0,
      burst: false,
      nearDup: false,
      tagFanOut: false,
    })
  })

  it('counts events as the neutral denominator', () => {
    const events = [ev({}), ev({}), ev({})]
    expect(triageAuthor(events).eventCount).toBe(3)
  })

  it('trips burst when enough events cluster inside the burst window', () => {
    // minEvents within windowSec at second-spaced timestamps trips burstDetected.
    const base = 1_700_000_000
    const events = Array.from({ length: BURST.minEvents }, (_, i) => ev({ createdAt: base + i }))
    expect(triageAuthor(events).burst).toBe(true)
  })

  it('trips nearDup when identical content repeats', () => {
    const events = [
      ev({ id: 'a', content: 'buy now buy now buy now' }),
      ev({ id: 'b', content: 'buy now buy now buy now' }),
    ]
    expect(triageAuthor(events).nearDup).toBe(true)
  })

  it('trips tagFanOut when mass-mention OR stuffing trips', () => {
    const pTags: string[][] = Array.from({ length: TAGS.massMention + 1 }, (_, i) => ['p', 'pk' + i])
    const massMentioner = ev({ id: 'm', tags: pTags })
    expect(triageAuthor([massMentioner]).tagFanOut).toBe(true)

    const tTags: string[][] = Array.from({ length: TAGS.stuffing + 1 }, (_, i) => ['t', 'tag' + i])
    const stuffer = ev({ id: 's', tags: tTags })
    expect(triageAuthor([stuffer]).tagFanOut).toBe(true)
  })

  it('carries NO clean/ok/safe/score field on the result', () => {
    const result = triageAuthor([ev({})]) as Record<string, unknown>
    expect(Object.keys(result).sort()).toEqual(['burst', 'eventCount', 'nearDup', 'tagFanOut'])
    expect(result.clean).toBeUndefined()
    expect(result.safe).toBeUndefined()
    expect(result.score).toBeUndefined()
  })
})
