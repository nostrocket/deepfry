// TDD RED → GREEN for parseIdentifier (ID-01 / ID-02 / ID-03).
//
// parseIdentifier is the single sanctioned normalizer that turns any pasted free
// text into a discriminated ParseResult: a normalized lowercase-hex pubkey + both
// display forms, OR an explicit parse failure. The contract this suite locks:
//
//   accept  → bare 64-hex (any case), npub, nprofile
//   reject  → '' / whitespace (the empty-input arm), nsec (a SECRET key — never
//             normalized or echoed), note (an event id, not an author), and any
//             malformed bech32 / non-hex garbage (the genuine parse-failure arm).
//
// ID-03 is the load-bearing distinction exercised here: a parse failure is the ONLY
// error this module produces. "valid identifier, zero matching events" is decided
// later by the query, never here — so a typo must never surface as an ok-arm.
//
// The suite is pure (Node vitest env): it builds its own valid fixtures via nip19
// and asserts on the union, mirroring the useStatsPoll.test.ts convention.
import { describe, it, expect } from 'vitest'
import { nip19 } from 'nostr-tools'
import { parseIdentifier, type ParseResult } from './identifier'

// A known, valid 64-char lowercase hex pubkey used to build encoded fixtures.
const HEX = 'a'.repeat(64)

describe('parseIdentifier — accepted forms (ID-01 / ID-02)', () => {
  it('accepts a bare 64-char lowercase hex and derives the npub', () => {
    const r = parseIdentifier(HEX)
    expect(r.ok).toBe(true)
    if (!r.ok) throw new Error('expected ok')
    expect(r.hex).toBe(HEX)
    expect(r.sourceKind).toBe('hex')
    expect(r.npub).toBe(nip19.npubEncode(HEX))
    expect(r.npub.startsWith('npub1')).toBe(true)
  })

  it('normalizes uppercase 64-hex to lowercase before validating', () => {
    const r = parseIdentifier(HEX.toUpperCase())
    expect(r.ok).toBe(true)
    if (!r.ok) throw new Error('expected ok')
    expect(r.hex).toBe(HEX)
    expect(r.sourceKind).toBe('hex')
  })

  it('accepts an npub and returns the decoded lowercase hex', () => {
    const npub = nip19.npubEncode(HEX)
    const r = parseIdentifier(npub)
    expect(r.ok).toBe(true)
    if (!r.ok) throw new Error('expected ok')
    expect(r.sourceKind).toBe('npub')
    expect(r.hex).toBe(HEX)
    expect(r.npub).toBe(npub)
  })

  it('accepts an nprofile and reads the nested .data.pubkey (not .data)', () => {
    const nprofile = nip19.nprofileEncode({ pubkey: HEX, relays: [] })
    const r = parseIdentifier(nprofile)
    expect(r.ok).toBe(true)
    if (!r.ok) throw new Error('expected ok')
    expect(r.sourceKind).toBe('nprofile')
    expect(r.hex).toBe(HEX)
  })
})

describe('parseIdentifier — rejected forms (ID-03 / security)', () => {
  it('rejects a note (event id is not an author identifier)', () => {
    const note = nip19.noteEncode('b'.repeat(64))
    const r = parseIdentifier(note)
    expect(r.ok).toBe(false)
    if (r.ok) throw new Error('expected fail')
    expect(r.reason).toBe('NOT_RECOGNIZED')
  })

  it('rejects an nsec and never carries the secret hex', () => {
    const sk = new Uint8Array(32).fill(7)
    const nsec = nip19.nsecEncode(sk)
    const r = parseIdentifier(nsec)
    expect(r.ok).toBe(false)
    if (r.ok) throw new Error('expected fail')
    expect(r.reason).toBe('REJECTED_NSEC')
    // The failure arm must not smuggle any decoded secret material.
    expect((r as Record<string, unknown>).hex).toBeUndefined()
    expect((r as Record<string, unknown>).npub).toBeUndefined()
  })

  it('rejects empty and whitespace-only input', () => {
    for (const raw of ['', '   ', '\t\n ']) {
      const r = parseIdentifier(raw)
      expect(r.ok).toBe(false)
      if (r.ok) throw new Error('expected fail')
      expect(r.reason).toBe('EMPTY')
    }
  })

  it('rejects malformed bech32 / non-hex garbage as NOT_RECOGNIZED', () => {
    const cases = [
      'npub1garbage',
      'not bech32',
      'a'.repeat(63), // 63-char hex
      'a'.repeat(65), // 65-char hex
      'g'.repeat(64), // 64 chars but non-hex
    ]
    for (const raw of cases) {
      const r = parseIdentifier(raw)
      expect(r.ok).toBe(false)
      if (r.ok) throw new Error(`expected fail for ${raw}`)
      expect(r.reason).toBe('NOT_RECOGNIZED')
    }
  })
})

describe('parseIdentifier — round-trip', () => {
  it('npub-derived hex equals hex-input hex for the same pubkey', () => {
    const viaHex = parseIdentifier(HEX)
    expect(viaHex.ok).toBe(true)
    if (!viaHex.ok) throw new Error('expected ok')
    const viaNpub = parseIdentifier(viaHex.npub)
    expect(viaNpub.ok).toBe(true)
    if (!viaNpub.ok) throw new Error('expected ok')
    expect(viaNpub.hex).toBe(viaHex.hex)
  })
})

// Type-level guard: ParseResult is a usable discriminated union export.
const _typecheck: ParseResult = { ok: false, reason: 'EMPTY' }
void _typecheck
