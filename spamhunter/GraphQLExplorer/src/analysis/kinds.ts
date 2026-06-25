// Kind-distribution histogram + bounds-check analyzer (DRILL-04) — a PURE function over
// the fetched window's author-claimed kind/createdAt. No React, no network I/O of any kind.
//
// ASYMMETRY (mirrors RateResult): the histogram is a neutral data view, not a verdict; a
// forged/out-of-range kind or createdAt is flagged into outOfRangeCount rather than
// mis-charted. KindsResult carries NO clean/ok/safe field — its absence is structural.
//
// FORGEABLE / 64-bit BOUNDS (T-03-01): both kind and createdAt are author-claimed 64-bit
// values. createdAt reuses isSaneTs from ./rate (single source for the timestamp bounds —
// NOT re-implemented here); kind is bounds-checked as Number.isSafeInteger(kind) && kind
// >= 0 (kinds are non-negative). An event failing EITHER check is COUNTED in
// outOfRangeCount and excluded from the histogram — a forged value never gets charted.
//
// HOSTILE INPUT (WR-04, parity with analyzeTags): kind/createdAt reach this analyzer via an
// unchecked `page.events as WindowEvent[]` cast — a partial-error payload can deliver
// null/undefined/non-number values the type checker cannot see. Both guards are
// Number.isSafeInteger-based, which returns false for null, undefined, NaN, and non-numbers,
// so a malformed value is COUNTED into outOfRangeCount and skipped, never thrown. The
// asymmetry with tags.ts (which is explicitly hardened) is closed: no input shape escapes.
//
// bins mirror rate.ts's { ... ; count }[] shape so the slice-02 KindsPanel can reuse the
// hand-rolled RatePanel bar JSX.
import { isSaneTs } from './rate'

export interface KindsResult {
  /** Denominator — events that passed BOTH the kind and createdAt bounds-checks. */
  analyzedCount: number
  /** Events with a forged/out-of-range kind OR createdAt, flagged and excluded. */
  outOfRangeCount: number
  /** Histogram bins, sorted descending by count, ties broken by ascending kind. */
  bins: { kind: number; count: number }[]
}

/** True when kind is a usable author-claimed value: a non-negative safe integer. */
function isSaneKind(kind: number): boolean {
  return Number.isSafeInteger(kind) && kind >= 0
}

/**
 * Build a kind histogram over the window. Asymmetric (see file header): NO clean field.
 * Forged kind/createdAt are flagged into outOfRangeCount, never bucketed. No crash on 0
 * events.
 */
export function analyzeKinds(events: { kind: number; createdAt: number }[]): KindsResult {
  const counts = new Map<number, number>()
  let outOfRangeCount = 0
  let analyzedCount = 0

  for (const ev of events) {
    if (!isSaneKind(ev.kind) || !isSaneTs(ev.createdAt)) {
      outOfRangeCount++
      continue
    }
    counts.set(ev.kind, (counts.get(ev.kind) ?? 0) + 1)
    analyzedCount++
  }

  const bins = [...counts.entries()]
    .map(([kind, count]) => ({ kind, count }))
    // Descending by count; ties broken by ascending kind for a stable, documented order.
    .sort((a, b) => (b.count - a.count) || (a.kind - b.kind))

  return { analyzedCount, outOfRangeCount, bins }
}
