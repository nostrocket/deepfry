// useAuthorWindow — the author drill-down's events window: a cursor-paginated,
// classify-gated fetch against the lens (DRILL-05 / DRILL-06).
//
// Source: RESEARCH § Pattern 2 (author window) + Pattern 5; 02-PATTERNS hook-anatomy
// map (analog: useStatsPoll.ts). Reuses Phase 1's transport boundary verbatim —
// client (POST-only), classify() (7-kind union, branch BEFORE reading data), and the
// opaque-cursor discipline documented in paginate.ts.
//
// Cursor discipline (contract §6.1 / §6.4): `after` is the opaque endCursor passed
// back VERBATIM — never parsed or constructed. On INVALID_CURSOR the recovery is to
// DROP the cursor (after = null) and restart from page 1; we never hand-build one.
//
// Honesty (DRILL-05): deriveWindowMeta turns the fetched page set into a denominator
// (count + time range + hasMore), never a verdict. hasMore === true means the window
// is PARTIAL, not that the author is clean.
//
// Discipline inherited from useStatsPoll (WR-04): the MANDATORY .toPromise().catch(()
// => 'THREW') guard so a thrown exchange defect classifies as NETWORK instead of
// killing the fetch silently; classify() runs BEFORE result.data is read.
import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { EventsDocument } from '../queries/events.graphql'

// Explicit per-page limit (contract §6 / Pitfall 7 — always pass an explicit limit;
// the server silently clamps to [1, 500]). 100 is the contract default page size.
export const PAGE_LIMIT = 100

/**
 * One rendered event row — the six fields EventsDocument selects (id, pubkey, kind,
 * createdAt, content, and the Phase-3 `tags`). The large canonical `raw` payload is NOT
 * here: it is fetched lazily, per event, by the raw inspector (rawEvent.graphql.ts).
 */
export interface WindowEvent {
  id: string
  pubkey: string
  kind: number
  createdAt: number
  content: string
  tags: string[][]
}

/**
 * The window denominator (DRILL-05). count = number of fetched events; hasMore =
 * whether the lens reports further pages; oldest/newest = min/max createdAt across
 * the fetched set (both null when the window is empty). Never a verdict — a
 * denominator the analyst reads alongside any signal.
 */
export interface WindowMeta {
  count: number
  hasMore: boolean
  oldest: number | null
  newest: number | null
}

export interface UseAuthorWindow {
  /** The fetched events, newest-first (server order, never re-sorted). */
  events: WindowEvent[]
  /** The live window denominator derived from events + hasMore. */
  windowMeta: WindowMeta
  /** The classified error of the most recent fetch, or null when clean. */
  error: ApiError | null
  /** True while a page fetch is in flight. */
  loading: boolean
  /** Whether the lens reports another page (drives the Load more affordance). */
  hasMore: boolean
  /** Fetch exactly ONE next page (DRILL-06); gated so a double-click can't double-append. */
  loadMore: () => void
}

/**
 * Pure window-denominator derivation (extracted for clarity + testability). oldest =
 * min createdAt, newest = max createdAt, count = events.length; oldest/newest are
 * null when the set is empty. Pure — no React/network.
 */
export function deriveWindowMeta(events: WindowEvent[], hasMore: boolean): WindowMeta {
  if (events.length === 0) {
    return { count: 0, hasMore, oldest: null, newest: null }
  }
  let oldest = events[0].createdAt
  let newest = events[0].createdAt
  for (const e of events) {
    if (e.createdAt < oldest) oldest = e.createdAt
    if (e.createdAt > newest) newest = e.createdAt
  }
  return { count: events.length, hasMore, oldest, newest }
}

export function useAuthorWindow(hex: string): UseAuthorWindow {
  const [events, setEvents] = useState<WindowEvent[]>([])
  const [error, setError] = useState<ApiError | null>(null)
  const [loading, setLoading] = useState(true)
  const [hasMore, setHasMore] = useState(false)

  // Opaque cursor for the NEXT page — out-of-band (useRef), passed back verbatim.
  const after = useRef<string | null>(null)
  // In-flight guard so loadMore() can't append twice on a rapid double-click (Pitfall 4).
  const inFlight = useRef(false)
  // Ref mirror of `hasMore` (WR-02): loadMore's gate must read always-current values,
  // not values captured in a stale closure. The state drives rendering; the ref is the
  // single source of truth for the guard. Kept in sync at every setHasMore call site.
  const hasMoreRef = useRef(false)
  // Cancellation token for the active author — bumped on hex change / unmount so a
  // late-resolving fetch for a previous author can never write stale state.
  const runId = useRef(0)
  // Bounded INVALID_CURSOR retry guard (WR-01). The recovery for an expired/rejected
  // cursor is to drop it and restart page 1 — but if page 1 ITSELF returns
  // INVALID_CURSOR (a looping lens, or a server rejecting even a null cursor), the
  // naive recovery self-recurses forever with cursor === null and the user sees a
  // permanent spinner. We allow exactly one cursor-drop retry; a second consecutive
  // INVALID_CURSOR — or any INVALID_CURSOR on an already-null cursor — surfaces the
  // error instead of recursing. Reset to 0 on every successful page fetch.
  const cursorRetry = useRef(0)

  // The filter is CONSTANT across every page of one author (contract §6.4 — a cursor
  // is only valid against the identical filter it was issued for).
  const fetchPage = useCallback(
    async (cursor: string | null, myRun: number) => {
      if (inFlight.current) return
      inFlight.current = true
      setLoading(true)

      // MANDATORY throw-guard (WR-04): a rejected exchange promise must classify as
      // NETWORK, never kill the fetch silently.
      //
      // requestPolicy: 'network-only' (WR-03 / DRILL-05 honesty): the default
      // document cacheExchange would replay a cached first page when an analyst
      // re-drills into an author they visited earlier, silently serving a stale
      // window against a corpus that is actively ingesting. The window-honesty
      // contract requires the fetched set to reflect CURRENT corpus state, so the
      // events window must always hit the network and re-derive its denominator.
      // Scoped to this query only — the shared client default is left untouched.
      const result = await client
        .query(
          EventsDocument,
          { filter: { authors: [hex] }, after: cursor, limit: PAGE_LIMIT },
          { requestPolicy: 'network-only' },
        )
        .toPromise()
        .catch(() => 'THREW' as const)

      // A newer author (or unmount) superseded this fetch — drop the result entirely.
      if (myRun !== runId.current) {
        inFlight.current = false
        return
      }

      if (result === 'THREW') {
        setError({ kind: 'NETWORK' })
        setLoading(false)
        inFlight.current = false
        return
      }

      // classify() BEFORE reading data (errors arrive on HTTP 200 — contract §7).
      const apiError = classify(result)
      if (apiError) {
        // INVALID_CURSOR: the opaque cursor expired/was rejected. Drop it, clear the
        // window, and restart from page 1 — NEVER hand-build a replacement cursor.
        if (apiError.kind === 'INVALID_CURSOR') {
          // WR-01: if page 1 itself was rejected (cursor === null) or we already
          // retried once, the restart cannot help — surface the error rather than
          // recurse forever into a permanent spinner.
          if (cursor === null || cursorRetry.current >= 1) {
            setError(apiError)
            setHasMore(false)
            hasMoreRef.current = false
            setLoading(false)
            inFlight.current = false
            return
          }
          cursorRetry.current += 1
          after.current = null
          setEvents([])
          setHasMore(false)
          hasMoreRef.current = false
          inFlight.current = false
          // Restart page 1 under the same run token.
          void fetchPage(null, myRun)
          return
        }
        setError(apiError)
        setLoading(false)
        inFlight.current = false
        return
      }

      const page = result.data?.events
      if (page) {
        const rows = page.events as WindowEvent[]
        // cursor === null is page 1 (replace); otherwise append.
        setEvents((prev) => (cursor === null ? rows : [...prev, ...rows]))
        after.current = page.endCursor ?? null // opaque — stored verbatim
        setHasMore(page.hasMore)
        hasMoreRef.current = page.hasMore
        cursorRetry.current = 0 // WR-01: a clean page resets the INVALID_CURSOR budget.
        setError(null)
      }
      setLoading(false)
      inFlight.current = false
    },
    [hex],
  )

  // Reset + fetch page 1 whenever the author changes. The run token invalidates any
  // in-flight fetch for the previous author so its result is dropped (no stale state).
  useEffect(() => {
    runId.current += 1
    const myRun = runId.current
    after.current = null
    inFlight.current = false
    cursorRetry.current = 0 // WR-01: fresh author starts with a full retry budget.
    setEvents([])
    setHasMore(false)
    hasMoreRef.current = false
    setError(null)
    setLoading(true)
    void fetchPage(null, myRun)
    return () => {
      // Invalidate this author's run on unmount / hex change.
      runId.current += 1
    }
  }, [hex, fetchPage])

  // DRILL-06: exactly ONE next page per click. Gated SOLELY on refs (WR-02) so the
  // guard always reads current values — `inFlight.current` is the single source of
  // truth for the in-flight check, `hasMoreRef.current` for exhaustion. The previous
  // `loading` term was a stale closure capture (redundant with inFlight) and forced
  // loadMore's identity to churn on every load/idle transition; both are dropped.
  const loadMore = useCallback(() => {
    if (inFlight.current || !hasMoreRef.current) return
    void fetchPage(after.current, runId.current)
  }, [fetchPage])

  return {
    events,
    windowMeta: deriveWindowMeta(events, hasMore),
    error,
    loading,
    hasMore,
    loadMore,
  }
}
