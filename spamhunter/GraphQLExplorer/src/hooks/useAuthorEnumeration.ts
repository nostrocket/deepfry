// useAuthorEnumeration — the corpus author-enumeration loop (BATCH-04). Pages the
// authors(after, limit) query byte-ascending, accumulating distinct hexes into a Set until
// the lens reports hasMore === false. A Stop control aborts the loop while retaining the
// partial snapshot; the discovered set feeds the SAME useLatestPerAuthor pipeline (one code
// path — BATCH-04).
//
// Reuses the Phase-2 transport discipline from useAuthorWindow VERBATIM:
//   - the MANDATORY .toPromise().catch(() => 'THREW') throw-guard (NETWORK, never silent);
//   - classify() runs BEFORE result.data is read (errors arrive on HTTP 200);
//   - requestPolicy: 'network-only' (the corpus is actively ingesting; never a cached page);
//   - opaque cursor: endCursor is passed back verbatim as the next `after`, never parsed
//     or constructed (contract §6.4);
//   - bounded INVALID_CURSOR restart: exactly one cursor-drop retry; a second consecutive
//     INVALID_CURSOR — or INVALID_CURSOR on a null cursor — surfaces the error instead of
//     spinning forever (mirrors useAuthorWindow's cursorRetry guard, T-04-02).
import { useCallback, useRef, useState } from 'react'
import type { OperationResult } from '@urql/core'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { AuthorsDocument } from '../queries/authors.graphql'
import { TRIAGE } from '../analysis/thresholds'

/** One authors page result, or the throw-guard sentinel. The explicit annotation keeps TS
 *  inference from going circular inside the enumeration loop. */
type FetchOutcome = OperationResult | 'THREW'

export interface UseAuthorEnumeration {
  /** The distinct authors discovered so far (snapshot; feeds useLatestPerAuthor). */
  authors: string[]
  /** Live count of distinct authors discovered — updated per page. */
  runningCount: number
  /** True while the enumeration loop is running. */
  enumerating: boolean
  /** True when the loop exited via the Stop control (partial, deliberate). */
  stopped: boolean
  /** True when the loop exited naturally (hasMore === false — full keyspace traversed). */
  complete: boolean
  /** The classified error that ended the loop early, or null. */
  error: ApiError | null
  /** Start (or restart) enumeration from the top of the keyspace. */
  start: () => void
  /** Request the loop stop after the current page; keeps the partial snapshot. */
  stop: () => void
}

export function useAuthorEnumeration(): UseAuthorEnumeration {
  const [authors, setAuthors] = useState<string[]>([])
  const [runningCount, setRunningCount] = useState(0)
  const [enumerating, setEnumerating] = useState(false)
  const [stopped, setStopped] = useState(false)
  const [complete, setComplete] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)

  // Stop control — checked at the top of every iteration.
  const stopRequested = useRef(false)
  // Stale-drop run token so a restart drops a previous loop's late resolvers.
  const runId = useRef(0)

  const start = useCallback(() => {
    runId.current += 1
    const myRun = runId.current
    stopRequested.current = false

    setAuthors([])
    setRunningCount(0)
    setStopped(false)
    setComplete(false)
    setError(null)
    setEnumerating(true)

    void (async () => {
      let after: string | null = null
      const seen = new Set<string>()
      // Bounded INVALID_CURSOR restart budget: exactly one cursor-drop retry.
      let cursorRetry = 0

      // Fetch one page; returns the typed page on success, or an ApiError sentinel the loop
      // branches on. Extracted so the awaited result has an explicit type — keeping TS
      // inference out of the loop's back-edge (TS7022 on a `while(true)` body).
      const fetchPage = async (
        cursor: string | null,
      ): Promise<{ authors: readonly string[]; endCursor: string | null; hasMore: boolean } | ApiError> => {
        const result: FetchOutcome = await client
          .query(AuthorsDocument, { after: cursor, limit: TRIAGE.enumLimit }, { requestPolicy: 'network-only' })
          .toPromise()
          .catch(() => 'THREW' as const)
        if (result === 'THREW') return { kind: 'NETWORK' }
        const apiError = classify(result)
        if (apiError) return apiError
        const page = result.data?.authors
        if (!page) return { kind: 'NETWORK' }
        return { authors: page.authors, endCursor: page.endCursor ?? null, hasMore: page.hasMore }
      }

      let running = true
      while (running) {
        if (stopRequested.current) {
          if (myRun === runId.current) {
            setStopped(true)
            setEnumerating(false)
          }
          return
        }

        const outcome = await fetchPage(after)
        if (myRun !== runId.current) return // superseded — drop entirely

        if (!('authors' in outcome)) {
          const apiError = outcome
          if (apiError.kind === 'INVALID_CURSOR' && after !== null && cursorRetry < 1) {
            // Bounded restart: drop the cursor and re-page from the top exactly once.
            cursorRetry += 1
            after = null
            seen.clear()
            setAuthors([])
            setRunningCount(0)
            continue
          }
          // A rejected null cursor, a second consecutive rejection, or any other error —
          // surface it and keep the partial snapshot (T-04-02 / T-04-03).
          setError(apiError)
          setEnumerating(false)
          return
        }

        for (const pk of outcome.authors) seen.add(pk)
        cursorRetry = 0 // a clean page resets the restart budget
        setAuthors([...seen])
        setRunningCount(seen.size)
        after = outcome.endCursor // opaque — stored verbatim

        if (!after || !outcome.hasMore) {
          setComplete(true)
          setEnumerating(false)
          running = false
        }
      }
    })()
  }, [])

  const stop = useCallback(() => {
    stopRequested.current = true
  }, [])

  return { authors, runningCount, enumerating, stopped, complete, error, start, stop }
}
