// Per-author triage adapter (BATCH-03) — a PURE fan-in over the three existing analyzers.
// No React, no transport, no network. It maps one author's small event window to four
// transparent indicators; it does NOT re-implement any detection (the analyzer signatures
// are reused verbatim).
//
// ASYMMETRY (carried from Phases 2–3): TriageIndicators has NO clean/ok/safe/score field by
// construction. burst / nearDup / tagFanOut are signal-PRESENT flags — a tripped flag is
// suspicious-when-present; absence is INCONCLUSIVE, never a "clean" verdict. eventCount is a
// neutral 0..perAuthor denominator (a quiet or zero-event window is inconclusive, never
// "worse"). isSaneTs is applied INSIDE analyzeRate, so raw createdAt is passed through.
import { analyzeRate } from './rate'
import { nearDup } from './nearDup'
import { analyzeTags } from './tags'
import type { WindowEvent } from '../hooks/useAuthorWindow'

/** The four transparent per-author indicators — signal-present flags + a neutral count. */
export interface TriageIndicators {
  /** Neutral denominator (0..perAuthor); never a verdict, never "worse" when low. */
  eventCount: number
  /** TRUE = tight interarrival burst present; FALSE = inconclusive (never a verdict). */
  burst: boolean
  /** TRUE = at least one near-duplicate cluster present; FALSE = inconclusive. */
  nearDup: boolean
  /** TRUE = mass-mention OR hashtag stuffing tripped on some event; FALSE = inconclusive. */
  tagFanOut: boolean
}

/**
 * Run the three analyzers over one author's window and collapse their outputs to the four
 * indicators. Pure — order-insensitive analyzers; empty input yields the neutral all-false row.
 */
export function triageAuthor(events: WindowEvent[]): TriageIndicators {
  const rate = analyzeRate(events.map((e) => e.createdAt))
  const dup = nearDup(events.map((e) => ({ id: e.id, content: e.content })))
  const tags = analyzeTags(events.map((e) => ({ id: e.id, tags: e.tags })))
  return {
    eventCount: events.length,
    burst: rate.burstDetected,
    nearDup: dup.duplicateCount > 0,
    tagFanOut: tags.massMention || tags.stuffing,
  }
}
