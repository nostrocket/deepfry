// useLatestPerAuthor — the batch chunk loop (BATCH-02/03). Splits a deduped input set into
// chunks (chunkAuthors/chunkSize), fetches each chunk through the lens, and incrementally
// re-merges the accumulated groups against the FULL input set (mergeByAuthor) so the table
// fills progressively with explicit "0 events" rows for not-yet-matched authors.
//
// Reuses the Phase-2 transport discipline from useAuthorWindow VERBATIM:
//   - the MANDATORY .toPromise().catch(() => 'THREW') throw-guard (a rejected exchange
//     classifies as NETWORK, never kills the loop silently);
//   - classify() runs BEFORE result.data is read (GraphQL errors arrive on HTTP 200);
//   - requestPolicy: 'network-only' (a re-triage must hit the network, never a cached page);
//   - a runId ref so a restarted batch drops late chunk resolvers (stale-drop).
//
// NEW logic concentrated here:
//   - 413 PAYLOAD_TOO_LARGE graceful degrade: halve the chunk and re-issue both halves,
//     bottoming out at chunk length 1 (a runtime safety net atop the static chunk size).
//   - partial-on-error: a recoverable chunk error (NETWORK/NOT_READY/INVALID_CURSOR) records
//     a per-chunk error and leaves that chunk's authors retryable — it NEVER kills the batch.
//   - INTERNAL is a hard chunk error carrying no server message (T-04-03 — no leak).
//   - sequential pacing (never parallel-flood the backend — T-04-02).
import { useCallback, useRef, useState } from 'react'
import { client } from '../transport/client'
import { classify, type ApiError } from '../transport/errors'
import { LatestPerAuthorDocument } from '../queries/latestPerAuthor.graphql'
import { chunkAuthors, chunkSize } from '../analysis/chunk'
import { mergeByAuthor, type TriageRow } from '../analysis/mergeByAuthor'
import type { WindowEvent } from './useAuthorWindow'
import { TRIAGE } from '../analysis/thresholds'

/** One author group as returned by the lens (and accumulated across chunks). */
interface AuthorGroup {
  author: string
  events: WindowEvent[]
}

/** A per-chunk error surfaced to the UI for a Retry affordance. */
export interface ChunkError {
  index: number
  error: ApiError
}

export interface UseLatestPerAuthor {
  /** The left-joined rows (one per input author), re-merged after each resolved chunk. */
  rows: TriageRow[]
  /** Authors covered by resolved chunks so far — the "N" in "triaged N of M". */
  triagedCount: number
  /** The full deduped input count — the "M" in "triaged N of M". */
  totalCount: number
  /** True while any chunk in the current run is in flight. */
  loading: boolean
  /** A fatal error for the run as a whole (currently unused; chunk errors are per-chunk). */
  error: ApiError | null
  /** Per-chunk errors that retained partial results; each is independently retryable. */
  chunkErrors: ChunkError[]
  /** Start a fresh batch over the deduped input hexes. */
  run: (dedupedInputHexes: string[]) => void
  /** Re-issue only the authors of a single failed chunk. */
  retryChunk: (index: number) => void
}

export function useLatestPerAuthor(): UseLatestPerAuthor {
  const [rows, setRows] = useState<TriageRow[]>([])
  const [triagedCount, setTriagedCount] = useState(0)
  const [totalCount, setTotalCount] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error] = useState<ApiError | null>(null)
  const [chunkErrors, setChunkErrors] = useState<ChunkError[]>([])

  // Stale-drop run token: a restarted batch bumps this so late resolvers are dropped.
  const runId = useRef(0)
  // The full deduped input set for the active run — the left-join base for every re-merge.
  const inputHexes = useRef<string[]>([])
  // Accumulated author groups across resolved chunks (re-merged after each).
  const accumulated = useRef<AuthorGroup[]>([])
  // The authors of each chunk, keyed by index, so retryChunk can re-issue exactly one.
  const chunkAuthorsByIndex = useRef<string[][]>([])
  // Authors already covered by resolved chunks (the triaged numerator).
  const coveredAuthors = useRef<Set<string>>(new Set())

  // Issue a single chunk request; classify BEFORE data; on 413 halve-and-retry (bottoms at
  // length 1). Returns the groups for this chunk, or an ApiError for a non-413 failure.
  const fetchChunk = useCallback(
    async (chunk: string[], myRun: number): Promise<AuthorGroup[] | ApiError> => {
      const result = await client
        .query(
          LatestPerAuthorDocument,
          { kind: TRIAGE.kind, perAuthor: TRIAGE.perAuthor, authors: chunk },
          { requestPolicy: 'network-only' },
        )
        .toPromise()
        .catch(() => 'THREW' as const)

      // A newer run superseded this fetch — drop it. Signal via a NETWORK sentinel that the
      // caller will discard because myRun !== runId.current is re-checked there too.
      if (myRun !== runId.current) return { kind: 'NETWORK' }

      if (result === 'THREW') return { kind: 'NETWORK' }

      const apiError = classify(result)
      if (apiError) {
        // 413: the chunk body exceeded the limit despite static sizing — halve and re-issue.
        if (apiError.kind === 'PAYLOAD_TOO_LARGE' && chunk.length > 1) {
          const mid = Math.ceil(chunk.length / 2)
          const left = await fetchChunk(chunk.slice(0, mid), myRun)
          if (!Array.isArray(left)) return left
          const right = await fetchChunk(chunk.slice(mid), myRun)
          if (!Array.isArray(right)) return right
          return [...left, ...right]
        }
        return apiError
      }

      const groups = (result.data?.latestPerAuthor ?? []) as AuthorGroup[]
      return groups
    },
    [],
  )

  const run = useCallback(
    (dedupedInputHexes: string[]) => {
      runId.current += 1
      const myRun = runId.current

      inputHexes.current = dedupedInputHexes
      accumulated.current = []
      chunkAuthorsByIndex.current = chunkAuthors(dedupedInputHexes, chunkSize())
      coveredAuthors.current = new Set()

      setTotalCount(dedupedInputHexes.length)
      setTriagedCount(0)
      setChunkErrors([])
      setRows(mergeByAuthor(dedupedInputHexes, []))
      setLoading(dedupedInputHexes.length > 0)

      // Sequential chunk loop (pacing — never parallel-flood the backend, T-04-02).
      void (async () => {
        const chunks = chunkAuthorsByIndex.current
        for (let index = 0; index < chunks.length; index++) {
          if (myRun !== runId.current) return // superseded — stop the loop entirely
          const chunk = chunks[index]
          const outcome = await fetchChunk(chunk, myRun)
          if (myRun !== runId.current) return

          if (Array.isArray(outcome)) {
            accumulated.current.push(...outcome)
            for (const hex of chunk) coveredAuthors.current.add(hex)
            setRows(mergeByAuthor(inputHexes.current, accumulated.current))
            setTriagedCount(coveredAuthors.current.size)
          } else {
            // Recoverable or hard chunk error — retain partial results, offer Retry. The
            // chunk's authors are NOT marked covered, so "triaged N of M" stays honest.
            setChunkErrors((prev) => [...prev, { index, error: outcome }])
          }
        }
        if (myRun === runId.current) setLoading(false)
      })()
    },
    [fetchChunk],
  )

  const retryChunk = useCallback(
    (index: number) => {
      const myRun = runId.current
      const chunk = chunkAuthorsByIndex.current[index]
      if (!chunk) return
      setChunkErrors((prev) => prev.filter((c) => c.index !== index))
      setLoading(true)
      void (async () => {
        const outcome = await fetchChunk(chunk, myRun)
        if (myRun !== runId.current) return
        if (Array.isArray(outcome)) {
          accumulated.current.push(...outcome)
          for (const hex of chunk) coveredAuthors.current.add(hex)
          setRows(mergeByAuthor(inputHexes.current, accumulated.current))
          setTriagedCount(coveredAuthors.current.size)
        } else {
          setChunkErrors((prev) => [...prev, { index, error: outcome }])
        }
        setLoading(false)
      })()
    },
    [fetchChunk],
  )

  return { rows, triagedCount, totalCount, loading, error, chunkErrors, run, retryChunk }
}
