// Defensive p/e/t tag aggregator (DRILL-03) — a PURE function over the fetched window's
// author-claimed tags. No React, no network I/O of any kind.
//
// ASYMMETRY (mirrors RateResult): mass-mention fan-out (many `p` rows on one event) and
// hashtag stuffing (many `t` rows) are suspicious-when-present automation/spam signals.
// A benign window proves nothing, so TagsResult carries NO clean/ok/safe field; the
// aggregate massMention/stuffing booleans are signal-PRESENT flags, never a "clean"
// verdict when false. Framing is always "X of N fetched".
//
// HOSTILE INPUT (Pitfall 3 / T-03-02): `tags` is `[[String!]!]!` in the schema but every
// value is author-supplied — a row may be empty, non-array, or omit tag[1]. Every access
// is guarded; malformed rows are COUNTED in malformedTagRows (parity with rejectedCount)
// and skipped — the aggregator stays total over hostile input, raising no exception.
//
// Thresholds (highTagCount / massMention / stuffing) live in thresholds.ts (TAGS).
import { TAGS } from './thresholds'

/** Top-N cap for the most-mentioned / most-used lists (UI-SPEC summary lists). */
const TOP_N = 10

export interface TagsResult {
  /** Denominator — events whose tags were read. */
  analyzedCount: number
  /** Malformed tag rows skipped but counted (never silently dropped). */
  malformedTagRows: number
  /** Most-mentioned pubkeys (`p`), descending by count, top-N. */
  topMentions: { value: string; count: number }[]
  /** Most-used hashtags (`t`), descending by count, top-N. */
  topHashtags: { value: string; count: number }[]
  /** Total `e` event references across the window. */
  eventRefCount: number
  /** Per-event outliers (highTagCount OR massMention OR stuffing); flags disambiguate. */
  outlierEvents: { id: string; tagCount: number; massMention: boolean; stuffing: boolean }[]
  /** TRUE = at least one event tripped mass-mention; FALSE = INCONCLUSIVE (never "clean"). */
  massMention: boolean
  /** TRUE = at least one event tripped hashtag stuffing; FALSE = INCONCLUSIVE. */
  stuffing: boolean
}

/** Increment a counter Map in place. */
function bump(m: Map<string, number>, key: string): void {
  m.set(key, (m.get(key) ?? 0) + 1)
}

/** Sort a counter Map's entries descending by count and slice the top-N. */
function topN(m: Map<string, number>, n: number): { value: string; count: number }[] {
  return [...m.entries()]
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => b.count - a.count)
    .slice(0, n)
}

/**
 * Aggregate p/e/t tags across the window. Asymmetric (see file header): NO clean field.
 * Defensive — a malformed tag row is counted, not raised; stays total. No crash on 0 events.
 */
export function analyzeTags(events: { id: string; tags: string[][] }[]): TagsResult {
  const mentions = new Map<string, number>()
  const hashtags = new Map<string, number>()
  let malformedTagRows = 0
  let eventRefCount = 0
  const outlierEvents: TagsResult['outlierEvents'] = []
  let anyMassMention = false
  let anyStuffing = false

  for (const ev of events) {
    let tagCount = 0 // total well-formed-enough rows counted toward the per-event total
    let pCount = 0 // per-event p-mention (fan-out) count
    let tCount = 0 // per-event t-hashtag count

    const rows = Array.isArray(ev.tags) ? ev.tags : []
    for (const tag of rows) {
      if (!Array.isArray(tag) || typeof tag[0] !== 'string') {
        malformedTagRows++
        continue
      }
      const name = tag[0]
      if (name === 'e') {
        eventRefCount++
        tagCount++
        continue
      }
      const value = tag[1]
      if (typeof value !== 'string') {
        malformedTagRows++
        continue
      }
      if (name === 'p') {
        bump(mentions, value)
        pCount++
        tagCount++
      } else if (name === 't') {
        bump(hashtags, value)
        tCount++
        tagCount++
      } else {
        // A well-formed but non-p/e/t row still counts toward the per-event tag total.
        tagCount++
      }
    }

    const massMention = pCount > TAGS.massMention
    const stuffing = tCount > TAGS.stuffing
    if (massMention) anyMassMention = true
    if (stuffing) anyStuffing = true

    if (tagCount > TAGS.highTagCount || massMention || stuffing) {
      outlierEvents.push({ id: ev.id, tagCount, massMention, stuffing })
    }
  }

  return {
    analyzedCount: events.length,
    malformedTagRows,
    topMentions: topN(mentions, TOP_N),
    topHashtags: topN(hashtags, TOP_N),
    eventRefCount,
    outlierEvents,
    massMention: anyMassMention,
    stuffing: anyStuffing,
  }
}
