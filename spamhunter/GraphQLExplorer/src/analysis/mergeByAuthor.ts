// Left-join merge-by-author (BATCH-03) — a PURE module, the single most important
// correctness pin in Phase 4. No React, no transport, no network.
//
// This mirrors the "extracted pure derive for testability" convention of
// deriveWindowMeta (useAuthorWindow.ts) and reuses WindowEvent as the row event type —
// it deliberately does NOT define a second event interface.
//
// THE LOAD-BEARING RATIONALE: the latestPerAuthor lens omits any author with zero matching
// events from its response (contract §5/§8). Therefore the response array does NOT line up
// positionally with the input array — the i-th returned group's author is generally not the
// i-th input hex. Joining by array index would silently misattribute one author's events to
// another and would hide every zero-match author entirely. The merge instead builds a
// Map keyed strictly by `author` and iterates the FULL deduped INPUT set, so a missing
// author resolves to events:[] and renders as an explicit "0 events" row. Pinned by a unit
// test where the response order differs from the input order and omits some input authors.
import type { WindowEvent } from '../hooks/useAuthorWindow'

/** One triage table row: an input author plus its (possibly empty) event window. */
export interface TriageRow {
  author: string
  events: WindowEvent[]
}

/**
 * Left-join the full deduped input set against the returned author groups, keyed strictly by
 * `author`. Every input author yields exactly one row, in input order; an author absent from
 * `groups` yields events:[]. Never positionally zipped against `groups`.
 */
export function mergeByAuthor(
  inputHexes: string[],
  groups: { author: string; events: WindowEvent[] }[],
): TriageRow[] {
  const byAuthor = new Map<string, WindowEvent[]>()
  for (const g of groups) byAuthor.set(g.author, g.events) // key strictly by author
  return inputHexes.map((hex) => ({ author: hex, events: byAuthor.get(hex) ?? [] })) // left join
}
