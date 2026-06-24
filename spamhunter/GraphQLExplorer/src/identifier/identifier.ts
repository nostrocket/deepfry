// TDD stub — real implementation lands in the GREEN task.
// See identifier.test.ts for the contract this module must satisfy.

export type ParseResult =
  | { ok: true; hex: string; npub: string; sourceKind: 'hex' | 'npub' | 'nprofile' }
  | { ok: false; reason: 'EMPTY' | 'NOT_RECOGNIZED' | 'REJECTED_NSEC' }

export function isHexPubkey(_s: string): boolean {
  return false
}

export function parseIdentifier(_raw: string): ParseResult {
  return { ok: false, reason: 'NOT_RECOGNIZED' }
}
