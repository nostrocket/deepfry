// Unit tests for the single error-classifier boundary (FND-03).
// Each contract §7 code/status maps to exactly one ApiError kind. Fixtures are
// hand-built OperationResult-shaped objects — no network, no urql client.
//
// The error shapes mirror what @urql/core@6 produces:
//   - transport non-2xx → error.networkError (statusText) + error.response.status
//   - GraphQL errors[]   → error.graphQLErrors[0] with optional extensions.code
import { describe, expect, it } from 'vitest'
import { CombinedError, type OperationResult } from '@urql/core'
import { classify } from './errors'

// Build a result whose CombinedError carries a transport Response with `status`.
// Mirrors @urql/core's makeErrorResult: networkError = Error(statusText), and the
// raw Fetch Response attached on the sibling `response` property.
function transportResult(status: number): OperationResult {
  const error = new CombinedError({
    networkError: new Error(`HTTP ${status}`),
    response: { status },
  })
  return { error } as unknown as OperationResult
}

// Build a result carrying a single GraphQL error with an optional extensions.code.
function graphQLResult(message: string, code?: string): OperationResult {
  const error = new CombinedError({
    graphQLErrors: [code ? { message, extensions: { code } } : { message }],
  })
  return { error } as unknown as OperationResult
}

describe('classify', () => {
  it('returns null for a clean result (no error) — safe to read data', () => {
    const result = { data: { stats: {} } } as unknown as OperationResult
    expect(classify(result)).toBeNull()
  })

  it('maps HTTP 503 → NOT_READY', () => {
    expect(classify(transportResult(503))).toEqual({ kind: 'NOT_READY' })
  })

  it('maps HTTP 413 → PAYLOAD_TOO_LARGE', () => {
    expect(classify(transportResult(413))).toEqual({ kind: 'PAYLOAD_TOO_LARGE' })
  })

  it('maps a networkError with no resolvable status → NETWORK', () => {
    const error = new CombinedError({ networkError: new Error('Failed to fetch') })
    const result = { error } as unknown as OperationResult
    expect(classify(result)).toEqual({ kind: 'NETWORK' })
  })

  it('maps extensions.code INVALID_CURSOR → INVALID_CURSOR', () => {
    const result = graphQLResult('invalid cursor: expected 16 bytes, got 3', 'INVALID_CURSOR')
    expect(classify(result)).toEqual({ kind: 'INVALID_CURSOR' })
  })

  it('maps extensions.code TOO_MANY_AUTHORS → TOO_MANY_AUTHORS', () => {
    const result = graphQLResult('too many authors', 'TOO_MANY_AUTHORS')
    expect(classify(result)).toEqual({ kind: 'TOO_MANY_AUTHORS' })
  })

  it('maps a code-less "internal error" message → INTERNAL', () => {
    const result = graphQLResult('internal error', undefined)
    expect(classify(result)).toEqual({ kind: 'INTERNAL' })
  })

  it('INTERNAL does NOT carry the raw server internal-error message (T-01-04)', () => {
    // A realistic leak attempt: the server message embeds an LMDB path / detail.
    const leaky = 'internal error: lmdb MDB_BAD_VALSIZE at /var/lib/strfry/data.mdb'
    const classified = classify(graphQLResult(leaky, undefined))
    expect(classified).toEqual({ kind: 'INTERNAL' })
    // The classified union must not carry the raw string anywhere.
    expect(JSON.stringify(classified)).not.toContain('lmdb')
    expect(JSON.stringify(classified)).not.toContain('data.mdb')
    expect(classified).not.toHaveProperty('message')
  })

  it('maps a code-less validation message → VALIDATION with the message (safe to show)', () => {
    const msg = 'kind must be a non-negative integer'
    const result = graphQLResult(msg, undefined)
    expect(classify(result)).toEqual({ kind: 'VALIDATION', message: msg })
  })

  it('prioritizes transport status over graphQLErrors when both present', () => {
    // A 503 with a stray graphQLError still classifies as NOT_READY (transport first).
    const error = new CombinedError({
      networkError: new Error('HTTP 503'),
      response: { status: 503 },
      graphQLErrors: [{ message: 'internal error' }],
    })
    const result = { error } as unknown as OperationResult
    expect(classify(result)).toEqual({ kind: 'NOT_READY' })
  })
})
