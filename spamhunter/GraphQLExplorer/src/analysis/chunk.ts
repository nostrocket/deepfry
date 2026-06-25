// Dual-axis chunk sizing for the batch fan-out (BATCH-02) — a PURE module. No React,
// no transport, no network of any kind. It computes the static per-request chunk size
// from two axes (the <=1000-author cap and a conservative request-body byte budget) and
// slices a deduped hex list into chunks.
//
// LOAD-BEARING FINDING (RESEARCH § Pattern 2): at perAuthor=5 the <=1000-author cap binds
// long before the 256 KiB request-body limit. Measured empirically, a 1000-author request
// body is only ~66 KiB; the byte budget would not bind until ~3225 authors — which the
// author cap forbids anyway. So chunkSize() resolves to TRIAGE.chunkAuthors (500) in
// practice, and the byte axis is a defensive lower bound, never the binding term here.
//
// thresholds.ts is the single tunable home for chunkAuthors (CONTEXT discretion).
//
// The 413 halve-and-retry recursion is a RUNTIME safety net and lives in the HOOK
// (useLatestPerAuthor) — re-issuing a halved chunk requires a network call, which this
// pure module must not perform. chunk.ts only owns the static math + slicing.
import { TRIAGE } from './thresholds'

/** Conservative per-author request-body cost (bytes). Measured marginal is ~67 (64-hex +
 *  JSON array framing); 80 adds a ~19% margin for UTF-8 / whitespace / proxy framing. */
export const SAFE_BYTES_PER_AUTHOR = 80
/** Generous fixed query-doc + header allowance (measured ~322; 4 KiB is deliberately ample). */
export const SAFE_FIXED_OVERHEAD = 4096
/** The lens request-body limit (contract §7 — 413 PAYLOAD_TOO_LARGE above this). */
export const BODY_LIMIT_BYTES = 256 * 1024

/**
 * The maximum authors that fit in the body budget, derived purely from the byte constants.
 * ~3225 at the constants above — far above the 1000-author cap, so this axis never binds
 * at perAuthor=5. Kept as a defensive lower bound in chunkSize().
 */
export function byteBudgetAuthors(): number {
  return Math.floor((BODY_LIMIT_BYTES - SAFE_FIXED_OVERHEAD) / SAFE_BYTES_PER_AUTHOR)
}

/**
 * The static per-request chunk size: the minimum of the conservative configured chunk, the
 * hard 1000-author cap, and the byte budget. The configured cap (500) binds first in
 * practice — the 1000 cap and the byte budget are defensive upper bounds.
 */
export function chunkSize(): number {
  return Math.min(TRIAGE.chunkAuthors, 1000, byteBudgetAuthors())
}

/**
 * Slice a deduped hex list into chunks of at most `size`, preserving order. A plain slice
 * loop — pure, allocation-only. Empty input yields []. The 413 degrade (halving a chunk)
 * is the hook's concern, not this function's.
 */
export function chunkAuthors(hexes: string[], size: number): string[][] {
  const chunks: string[][] = []
  for (let i = 0; i < hexes.length; i += size) {
    chunks.push(hexes.slice(i, i + size))
  }
  return chunks
}
