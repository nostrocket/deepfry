// Opaque-cursor accumulation loop — SCAFFOLD ONLY this phase (FND-03).
// Source: contract §6.1 / §6.4 (opaque cursors, INVALID_CURSOR, hasMore/endCursor),
// RESEARCH § "Recommended Project Structure" (paginate.ts is scaffold-only).
//
// ── Scope ──────────────────────────────────────────────────────────────────────
// This helper is NOT wired to any live query in Phase 1. It is FIRST EXERCISED in
// Phase 2 (events cursor pagination) and REUSED by the Phase 4 `authors`
// distinct-pubkey enumeration (BATCH-04). Building it now keeps transport generic so
// later phases inherit the cursor loop without rework — but nothing in this phase
// calls accumulatePages against a real query.
//
// ── Cursor discipline (contract §6.1 / §6.4) ─────────────────────────────────────
// Treat `endCursor` as OPAQUE: it is base64 of internal sort keys for `events`, and
// a query-specific value (not the same format) for `authors`. NEVER parse, construct,
// cross-use, or inspect it — pass it back verbatim as the next page's `after`.
// A malformed/cross-used cursor returns an error classified as INVALID_CURSOR
// (transport/errors.ts). The recovery for INVALID_CURSOR is: DROP the cursor and
// RESTART pagination from page 1 (after = null) — never hand-build a replacement.
// The error never echoes the offending cursor bytes (contract §7).
//
// ── Explicit-limit convention (FND-03, contract §6 / Pitfall 7) ──────────────────
// CONVENTION for every paginated query Phases 2–4 add: pass an EXPLICIT `limit` on
// every page request. The server silently clamps `limit` (and `perAuthor`) to the
// range [1, 500] (values > 500 become 500; <= 0 become 1, contract §6/§12), so treat
// 500 as a hard per-page ceiling and drive completeness through pagination (this
// loop), NOT through an ever-larger `limit`. Oversized request bodies return HTTP 413
// (PAYLOAD_TOO_LARGE), handled in the classifier's transport-status branch. The
// `stats` query correctly takes no `limit` (it returns four scalars, not a page) — so
// this convention is RECORDED here for later phases without changing the stats query.

/** One page returned by a fetch-page fn: rows plus the opaque cursor + hasMore flag. */
export interface Page<T> {
  /** The rows for this page (e.g. events, or author pubkeys). */
  items: T[]
  /** Opaque cursor for the NEXT page — pass back verbatim. Null when exhausted. */
  endCursor: string | null
  /** Whether another page exists. When false, the loop stops. */
  hasMore: boolean
}

/**
 * Fetch one page given an opaque `after` cursor (null for page 1) and the explicit
 * per-page `limit` (the convention above). Implemented per-query in Phases 2/4.
 */
export type FetchPage<T> = (after: string | null, limit: number) => Promise<Page<T>>

/**
 * Accumulate every page by looping `fetchPage`, passing each page's `endCursor` back
 * verbatim as the next `after`. Stops when `hasMore` is false OR `endCursor` is null.
 * The cursor is treated as opaque throughout — it is never parsed or constructed.
 *
 * SCAFFOLD ONLY this phase — exercised first in Phase 2. INVALID_CURSOR recovery
 * (drop cursor, restart from page 1) is the caller's responsibility once this is
 * wired to a classified query; documented above so the convention is inherited.
 *
 * @param fetchPage per-query page fetcher
 * @param limit explicit per-page limit (clamped server-side to [1, 500])
 */
export async function accumulatePages<T>(
  fetchPage: FetchPage<T>,
  limit: number,
): Promise<T[]> {
  const all: T[] = []
  let after: string | null = null
  for (;;) {
    const page = await fetchPage(after, limit)
    all.push(...page.items)
    if (!page.hasMore || page.endCursor === null) break
    after = page.endCursor // opaque — passed back verbatim, never inspected
  }
  return all
}
