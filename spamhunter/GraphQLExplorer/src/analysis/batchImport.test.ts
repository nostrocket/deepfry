// TDD RED → GREEN for the batch-import tokenizer (BATCH-01).
//
// parseBatchInput splits pasted/uploaded text on whitespace/newlines/commas, routes EACH
// token through parseIdentifier (the single sanctioned nip19 site — never re-implemented),
// normalizes accepted authors to lowercase hex, dedupes via a Set, and counts valid /
// duplicates / unparseable. Unparseable tokens (note, nsec, garbage) are PRESERVED verbatim
// in the result — never silently dropped — so the import summary is an honest surface.
//
// Tests run in the existing Node vitest env (no DOM/network) — the module is pure.
import { describe, it, expect } from 'vitest'
import { parseBatchInput, type BatchImportResult } from './batchImport'
import { nip19 } from 'nostr-tools'

const HEX_A = 'a'.repeat(64)
const HEX_B = 'b'.repeat(64)
const NPUB_A = nip19.npubEncode(HEX_A)
const NOTE = nip19.noteEncode('c'.repeat(64))

describe('parseBatchInput (tokenize + normalize + dedupe + count)', () => {
  it('parses a mix of npub + hex + duplicate + note + garbage correctly', () => {
    // HEX_A and NPUB_A normalize to the SAME hex (one valid, one duplicate). HEX_B is a
    // second distinct valid. NOTE and "garbage" are unparseable and preserved verbatim.
    const text = [HEX_A, NPUB_A, HEX_B, NOTE, 'garbage'].join('\n')
    const result: BatchImportResult = parseBatchInput(text)

    expect(result.validHexes.sort()).toEqual([HEX_A, HEX_B].sort())
    expect(result.duplicateCount).toBe(1) // NPUB_A re-introduces HEX_A
    expect(result.unparseable).toContain(NOTE)
    expect(result.unparseable).toContain('garbage')
  })

  it('splits on whitespace, newlines, and commas', () => {
    const text = `${HEX_A}, ${HEX_B}\n${'d'.repeat(64)}`
    const result = parseBatchInput(text)
    expect(result.validHexes.length).toBe(3)
  })

  it('normalizes accepted hex to lowercase', () => {
    const result = parseBatchInput('A'.repeat(64))
    expect(result.validHexes).toEqual([HEX_A])
  })

  it('counts a repeated valid hex as a duplicate, not a second valid', () => {
    const result = parseBatchInput(`${HEX_A} ${HEX_A} ${HEX_A}`)
    expect(result.validHexes).toEqual([HEX_A])
    expect(result.duplicateCount).toBe(2)
  })

  it('drops empty tokens (extra separators) without counting them', () => {
    const result = parseBatchInput(`  ${HEX_A} ,,  \n\n , `)
    expect(result.validHexes).toEqual([HEX_A])
    expect(result.duplicateCount).toBe(0)
    expect(result.unparseable).toEqual([])
  })

  it('rejects an nsec into unparseable (never normalized or echoed as valid)', () => {
    const nsec = nip19.nsecEncode(new Uint8Array(32).fill(1))
    const result = parseBatchInput(nsec)
    expect(result.validHexes).toEqual([])
    expect(result.unparseable).toContain(nsec)
  })

  it('returns the empty result for empty input', () => {
    expect(parseBatchInput('')).toEqual<BatchImportResult>({
      validHexes: [],
      duplicateCount: 0,
      unparseable: [],
    })
  })
})
