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

/** A per-chunk error surfaced to the UI for a Retry affordance.
 *  `retryable === false` marks a terminal hard failure that re-issuing cannot fix
 *  (WR-04/WR-05): a single-author chunk that still 413s cannot shrink further, and a
 *  TOO_MANY_AUTHORS at chunk length 1 means the backend cap is below one author — Retry
 *  would deterministically re-fail, so the UI must present it as a non-retryable hard fail. */
export interface ChunkError {
  index: number
  error: ApiError
  retryable: boolean
}

/** Pure decision for how to handle a classified chunk error (extracted for unit testing,
 *  mirroring the shouldNudge convention in useStatsPoll). Given the error kind and the chunk
 *  length, decide whether to SHRINK (halve-and-retry inside fetchChunk), present a RETRYABLE
 *  per-chunk error, or present a TERMINAL non-retryable hard failure.
 *
 *  - 413 / TOO_MANY_AUTHORS with length > 1  → 'shrink'   (WR-04: TOO_MANY shrinks like 413)
 *  - 413 / TOO_MANY_AUTHORS with length <= 1 → 'terminal' (WR-05: nothing left to shrink)
 *  - any other classified error              → 'retryable'(NETWORK / NOT_READY / etc.)
 */
export type ChunkDegrade = 'shrink' | 'retryable' | 'terminal'
export function chunkDegradeDecision(kind: ApiError['kind'], chunkLength: number): ChunkDegrade {
  const shrinkable = kind === 'PAYLOAD_TOO_LARGE' || kind === 'TOO_MANY_AUTHORS'
  if (shrinkable) return chunkLength > 1 ? 'shrink' : 'terminal'
  return 'retryable'
}

/** The outcome of fetching one chunk. `groups` carries every author group that resolved —
 *  INCLUDING partial work from a successful 413-split sub-half even when a sibling sub-half
 *  later fails (WR-01: partial work inside a chunk is never discarded). `covered` is the set
 *  of authors whose sub-request fully resolved (the honest "triaged N" numerator — authors
 *  in a failed sub-half are NOT covered). `error`/`retryable` are present iff a sub-request
 *  failed. */
interface ChunkOutcome {
  groups: AuthorGroup[]
  covered: string[]
  error?: ApiError
  retryable?: boolean
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

  // Issue a single chunk request; classify BEFORE data; on 413 / TOO_MANY_AUTHORS halve-and-
  // retry (bottoms at length 1). Always returns a ChunkOutcome that carries whatever groups
  // resolved (partial sub-half work is NEVER discarded — WR-01) plus an optional error.
  const fetchChunk = useCallback(
    async (chunk: string[], myRun: number): Promise<ChunkOutcome> => {
      const result = await client
        .query(
          LatestPerAuthorDocument,
          { kind: TRIAGE.kind, perAuthor: TRIAGE.perAuthor, authors: chunk },
          { requestPolicy: 'network-only' },
        )
        .toPromise()
        .catch(() => 'THREW' as const)

      // A newer run superseded this fetch — drop it. The NETWORK sentinel is discarded by the
      // caller because myRun !== runId.current is re-checked there too.
      if (myRun !== runId.current) return { groups: [], covered: [], error: { kind: 'NETWORK' }, retryable: true }

      if (result === 'THREW') return { groups: [], covered: [], error: { kind: 'NETWORK' }, retryable: true }

      const apiError = classify(result)
      if (apiError) {
        const degrade = chunkDegradeDecision(apiError.kind, chunk.length)
        if (degrade === 'shrink') {
          // WR-04: 413 AND TOO_MANY_AUTHORS are both degraded by shrinking, not surfaced as a
          // futile Retry. Halve and re-issue both halves.
          const mid = Math.ceil(chunk.length / 2)
          const left = await fetchChunk(chunk.slice(0, mid), myRun)
          // WR-01: retain the left half's resolved groups even if the right half fails —
          // partial work inside a chunk is never thrown away.
          const right = left.error
            ? { groups: [], covered: [], error: left.error, retryable: left.retryable }
            : await fetchChunk(chunk.slice(mid), myRun)
          const merged: ChunkOutcome = {
            groups: [...left.groups, ...right.groups],
            covered: [...left.covered, ...right.covered],
          }
          // Surface the first error encountered (left precedence) so the UI offers a Retry
          // of the still-missing authors; the retried chunk re-splits and skips covered work.
          const firstError = left.error ?? right.error
          if (firstError) {
            merged.error = firstError
            merged.retryable = (left.error ? left.retryable : right.retryable) ?? true
          }
          return merged
        }
        // WR-05 / WR-04 terminal: a single-author chunk that still 413s cannot shrink any
        // further, and a TOO_MANY_AUTHORS at length 1 means the backend cap is below one
        // author. Either way there is nothing left to shrink — present a NON-retryable hard
        // failure rather than a Retry that deterministically re-fails.
        return { groups: [], covered: [], error: apiError, retryable: degrade !== 'terminal' }
      }

      const groups = (result.data?.latestPerAuthor ?? []) as AuthorGroup[]
      // A fully-resolved request covers ALL its authors (zero-event authors resolve as an
      // explicit "0 events" left-join row, not an omission) — the honest "triaged N" numerator.
      return { groups, covered: chunk }
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

          // Always absorb whatever resolved — including partial sub-half work (WR-01). Only
          // the authors that actually resolved are marked covered, so "triaged N of M" stays
          // honest when a chunk partially failed.
          if (outcome.groups.length > 0 || outcome.covered.length > 0) {
            accumulated.current.push(...outcome.groups)
            for (const hex of outcome.covered) coveredAuthors.current.add(hex)
            setRows(mergeByAuthor(inputHexes.current, accumulated.current))
            setTriagedCount(coveredAuthors.current.size)
          }
          if (outcome.error) {
            const error = outcome.error
            const retryable = outcome.retryable ?? true
            setChunkErrors((prev) => [...prev, { index, error, retryable }])
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
      // Re-issue only the authors of this chunk that are NOT already covered — a prior
      // partial 413-split may have resolved some of them (WR-01); re-fetching them would be
      // wasted work and could re-413 the same body.
      const pending = chunk.filter((hex) => !coveredAuthors.current.has(hex))
      if (pending.length === 0) {
        setChunkErrors((prev) => prev.filter((c) => c.index !== index))
        return
      }
      setChunkErrors((prev) => prev.filter((c) => c.index !== index))
      setLoading(true)
      void (async () => {
        const outcome = await fetchChunk(pending, myRun)
        // WR-02: gate ALL state writes on run identity. A stale retry from a superseded batch
        // must never clear/overwrite a freshly started run's loading or results.
        if (myRun !== runId.current) return
        if (outcome.groups.length > 0 || outcome.covered.length > 0) {
          accumulated.current.push(...outcome.groups)
          for (const hex of outcome.covered) coveredAuthors.current.add(hex)
          setRows(mergeByAuthor(inputHexes.current, accumulated.current))
          setTriagedCount(coveredAuthors.current.size)
        }
        if (outcome.error) {
          const error = outcome.error
          const retryable = outcome.retryable ?? true
          setChunkErrors((prev) => [...prev, { index, error, retryable }])
        }
        setLoading(false)
      })()
    },
    [fetchChunk],
  )

  return { rows, triagedCount, totalCount, loading, error, chunkErrors, run, retryChunk }
}
