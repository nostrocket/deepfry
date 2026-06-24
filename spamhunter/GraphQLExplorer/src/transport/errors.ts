// Single error-classifier boundary (FND-03) — Source: contract §7 (error table),
// RESEARCH § "Error classifier (discriminated union)" / Pattern 2.
//
// GraphQL errors arrive on HTTP 200 (contract §7): res.ok / status === 200 is NOT
// success. Every query result passes through classify() and views branch on the
// returned ApiError BEFORE reading result.data — they never read errors[] directly.
// This centralizes the contract's failure semantics in exactly one place so Phases
// 2–4 inherit the same taxonomy without re-deriving it at each call site (Pitfall 1).
//
// SECURITY (V7, T-01-04): the INTERNAL branch maps a code-less server "internal
// error" to a generic kind that carries NO message — the raw server string is
// deliberately not propagated into the union, so the UI cannot leak backend
// internals. Validation messages (code-less, user-safe per contract §7) ARE carried
// verbatim on the VALIDATION kind because the contract guarantees they are safe to show.
import type { OperationResult } from '@urql/core'

// The seven contract-fixed kinds (contract §7). The taxonomy is fixed; only the
// urql status-extraction detail was open (RESEARCH Assumption A2, resolved below).
export type ApiError =
  | { kind: 'INVALID_CURSOR' } // extensions.code — drop cursor, restart page 1 (Phase 2; Phase 4 authors)
  | { kind: 'TOO_MANY_AUTHORS' } // extensions.code — chunk authors (Phase 4)
  | { kind: 'VALIDATION'; message: string } // code-less validation message — safe to show verbatim
  | { kind: 'INTERNAL' } // code-less "internal error" — generic; raw message NOT surfaced
  | { kind: 'NOT_READY' } // HTTP 503 — recoverable; the connecting/backoff state
  | { kind: 'PAYLOAD_TOO_LARGE' } // HTTP 413 — request body exceeded 256 KiB
  | { kind: 'NETWORK' } // fetch failed outright (genuine transport failure; not a CORS artifact — CORS is wildcard)

// RESEARCH Assumption A2 / Open Question 2 — RESOLVED against the installed
// @urql/core@6.0.3. The fetch source builds a CombinedError via makeErrorResult
// (node_modules/@urql/core/dist/urql-core-chunk.js): on a non-2xx HTTP response it
// sets `networkError = new Error(response.statusText)` AND attaches the raw Fetch
// `Response` on the error's SIBLING `response` property — i.e. the HTTP status lives
// at `result.error.response.status`, NOT at `result.error.networkError.response.status`
// (the RESEARCH example's path). CombinedError.response is typed `any`, so we read it
// defensively. The taxonomy (which statuses/codes exist) is fixed by contract §7.
function httpStatus(error: OperationResult['error']): number | undefined {
  if (!error) return undefined
  const response = (error as { response?: { status?: unknown } }).response
  const status = response?.status
  return typeof status === 'number' ? status : undefined
}

/**
 * Turn the contract's three failure shapes (transport status, GraphQL errors[]
 * with extensions.code, code-less validation/"internal error") into one
 * discriminated union. Returns null when the result is clean — only then is it
 * safe to read result.data.
 */
export function classify(result: OperationResult): ApiError | null {
  const error = result.error
  if (!error) return null // OK — safe to read result.data

  // 1. Transport-level HTTP status (contract §7 "Transport-level statuses").
  //    A non-2xx fetch surfaces as a networkError with the Response attached.
  const status = httpStatus(error)
  if (status === 503) return { kind: 'NOT_READY' }
  if (status === 413) return { kind: 'PAYLOAD_TOO_LARGE' }

  // 2. GraphQL-level errors[] (arrive on HTTP 200 — contract §7).
  const gqlErr = error.graphQLErrors?.[0]
  if (gqlErr) {
    const code = gqlErr.extensions?.code as string | undefined
    if (code === 'INVALID_CURSOR') return { kind: 'INVALID_CURSOR' }
    if (code === 'TOO_MANY_AUTHORS') return { kind: 'TOO_MANY_AUTHORS' }
    // Code-less "internal error" → generic INTERNAL. The raw server message is
    // deliberately dropped here (T-01-04) — never surfaced to the UI.
    if (/internal error/i.test(gqlErr.message)) return { kind: 'INTERNAL' }
    // Any other code-less GraphQL error is a validation message (contract §7) —
    // user-safe, so it is carried verbatim for the UI to show.
    return { kind: 'VALIDATION', message: gqlErr.message }
  }

  // 3. A networkError with no resolvable HTTP status: the request failed outright
  //    (relay unreachable, DNS/connection refused). Under wildcard CORS this is a
  //    genuine transport failure, not a masked CORS rejection.
  if (error.networkError) return { kind: 'NETWORK' }

  // A CombinedError with neither graphQLErrors nor a networkError is degenerate;
  // treat it as a generic network failure rather than reading data on an error.
  return { kind: 'NETWORK' }
}
