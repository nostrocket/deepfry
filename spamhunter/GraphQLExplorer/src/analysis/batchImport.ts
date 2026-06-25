// Batch-import tokenizer (BATCH-01) — a PURE module. No React, no transport, no network.
//
// It splits pasted/uploaded free text into tokens (whitespace / newlines / commas), routes
// EACH token through parseIdentifier — the single sanctioned bech32-decode site; a second
// decode site is forbidden — normalizes accepted authors to lowercase hex, dedupes them via
// a Set, and counts valid / duplicates / unparseable.
//
// HONESTY (V5 input validation): unparseable tokens (note, nsec, garbage, etc.) are PRESERVED
// verbatim in `unparseable` — never silently dropped — so the import summary can list exactly
// what was rejected. note and nsec land here because parseIdentifier already rejects them
// (an event id is not an author; a secret key must never be normalized or echoed).
import { parseIdentifier } from '../identifier/identifier'

/** The import outcome: the deduped lowercase-hex authors plus the two honesty counters. */
export interface BatchImportResult {
  /** Deduped, lowercase-hex authors accepted by parseIdentifier (insertion order). */
  validHexes: string[]
  /** Count of accepted tokens that resolved to an already-seen hex (deduped out). */
  duplicateCount: number
  /** Tokens parseIdentifier rejected, preserved verbatim for the import summary. */
  unparseable: string[]
}

/**
 * Tokenize + normalize + dedupe + count. Empty tokens (from adjacent separators) are dropped
 * without counting. Order of validHexes follows first appearance. Pure.
 */
export function parseBatchInput(text: string): BatchImportResult {
  const tokens = text.split(/[\s,]+/).filter((t) => t.length > 0)

  const seen = new Set<string>()
  let duplicateCount = 0
  const unparseable: string[] = []

  for (const token of tokens) {
    const parsed = parseIdentifier(token)
    if (parsed.ok) {
      // parseIdentifier already normalizes to lowercase hex.
      if (seen.has(parsed.hex)) duplicateCount++
      else seen.add(parsed.hex)
    } else {
      unparseable.push(token)
    }
  }

  return { validHexes: [...seen], duplicateCount, unparseable }
}
