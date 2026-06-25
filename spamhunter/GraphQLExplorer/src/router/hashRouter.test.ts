// Unit tests for the pure parseHash matcher (Wave-0 gap — the hash router gained a
// #/batch top-level route this phase). parseHash is pure (no React, no DOM), so it runs
// in the existing Node vitest env. The matcher is a discriminated union; callers switch on
// `name` and never parse the raw hash themselves, so pinning the mapping here pins the
// whole routing contract.
//
// SECURITY (T-02-06 carried forward): the author matcher accepts LOWERCASE 64-hex ONLY —
// an uppercase / npub / wrong-length / junk hash resolves to `notfound`, never a silent
// drill-down against an un-normalized identifier. The added #/batch branch is an EXACT
// match, so #/batch/anything still falls through to notfound.
import { describe, it, expect } from 'vitest'
import { parseHash } from './hashRouter'

// A canonical lowercase 64-hex pubkey (the only author shape the matcher accepts).
const HEX64 = 'a'.repeat(64)

describe('parseHash', () => {
  it('maps the empty / # / #/ hashes to home', () => {
    expect(parseHash('')).toEqual({ name: 'home' })
    expect(parseHash('#')).toEqual({ name: 'home' })
    expect(parseHash('#/')).toEqual({ name: 'home' })
  })

  it('maps #/batch exactly to the batch route', () => {
    expect(parseHash('#/batch')).toEqual({ name: 'batch' })
  })

  it('maps a valid lowercase 64-hex author hash to the author route', () => {
    expect(parseHash('#/a/' + HEX64)).toEqual({ name: 'author', hex: HEX64 })
  })

  it('falls through to notfound for an unknown hash', () => {
    expect(parseHash('#/nope')).toEqual({ name: 'notfound' })
  })

  it('treats #/batch as an EXACT match — a trailing segment is notfound', () => {
    expect(parseHash('#/batch/extra')).toEqual({ name: 'notfound' })
  })

  it('rejects an uppercase or wrong-length author hash as notfound', () => {
    expect(parseHash('#/a/' + 'A'.repeat(64))).toEqual({ name: 'notfound' })
    expect(parseHash('#/a/' + 'a'.repeat(63))).toEqual({ name: 'notfound' })
  })
})
