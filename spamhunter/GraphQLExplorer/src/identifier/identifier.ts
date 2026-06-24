// The single sanctioned identifier normalizer (ID-01 / ID-02 / ID-03).
// Source: contract §3 (identifier semantics — ids/pubkeys are lowercase hex),
// RESEARCH § Pattern 1 (verified nip19 return shapes + throw-on-invalid).
//
// parseIdentifier turns any pasted free text into a discriminated ParseResult:
// a normalized lowercase-hex pubkey + both display forms (the ok arm), OR an
// explicit parse failure (the fail arm). It mirrors the errors.ts ApiError
// discriminated-union + defensive-parse convention; callers branch on `ok` and
// never inspect raw input themselves.
//
// ID-03 (load-bearing): a parse failure is the ONLY error this module produces.
// "valid identifier, zero matching events" is decided later by the query (never
// here), so a typo can never surface as an ok arm and read as a clean author.
//
// SECURITY (V5 input validation):
//   - nsec decodes successfully but is a SECRET key. It is rejected via a distinct
//     REJECTED_NSEC arm and is NEVER normalized to hex/npub, routed into the URL/
//     history, or stored. The fail arm carries no decoded material.
//   - note decodes to an EVENT id, not an author. Routing it as authors:[id] would
//     read as a clean-author zero-match (the ID-03 failure mode), so a note is
//     rejected. Resolving event->author is out of scope for this slice.
//
// This module is pure: it imports only nip19 from nostr-tools (narrow import for
// tree-shaking) and nothing from React/urql/transport, so it runs in the Node
// vitest environment.
import { nip19 } from 'nostr-tools'

// The discriminated result. Success carries the normalized hex + both display
// forms + which input shape it came from. Failure carries only a reason.
export type ParseResult =
  | { ok: true; hex: string; npub: string; sourceKind: 'hex' | 'npub' | 'nprofile' }
  | { ok: false; reason: 'EMPTY' | 'NOT_RECOGNIZED' | 'REJECTED_NSEC' }

// A canonical lowercase 64-char hex pubkey. Normalize case before testing.
const HEX64 = /^[0-9a-f]{64}$/

// True iff `s` is already a canonical lowercase 64-hex string. Callers that need
// to gate on hex-shape (e.g. the hash router) reuse this rather than re-deriving.
export function isHexPubkey(s: string): boolean {
  return HEX64.test(s)
}

export function parseIdentifier(raw: string): ParseResult {
  const trimmed = raw.trim()
  if (trimmed.length === 0) return { ok: false, reason: 'EMPTY' }

  // Case-normalize before validating: humans paste mixed-case hex from various
  // clients; the corpus speaks lowercase hex only.
  const input = trimmed.toLowerCase()
  if (HEX64.test(input)) {
    return { ok: true, hex: input, npub: nip19.npubEncode(input), sourceKind: 'hex' }
  }

  // nip19.decode throws on malformed input (verified against nostr-tools@2.23.8);
  // the catch arm below IS the genuine parse-failure branch.
  try {
    const decoded = nip19.decode(input)
    switch (decoded.type) {
      case 'npub': {
        // decoded.data is the hex pubkey string.
        const hex = decoded.data
        return { ok: true, hex, npub: nip19.npubEncode(hex), sourceKind: 'npub' }
      }
      case 'nprofile': {
        // NESTED read — the pubkey lives at .data.pubkey, never at .data.
        const hex = decoded.data.pubkey
        return { ok: true, hex, npub: nip19.npubEncode(hex), sourceKind: 'nprofile' }
      }
      case 'nsec':
        // SECRET key — reject explicitly; never normalize or echo it.
        return { ok: false, reason: 'REJECTED_NSEC' }
      default:
        // note (event id) and every other type are not author identifiers here.
        return { ok: false, reason: 'NOT_RECOGNIZED' }
    }
  } catch {
    return { ok: false, reason: 'NOT_RECOGNIZED' }
  }
}
