---
phase: 01-shared-bloom-library
reviewed: 2026-06-30T00:00:00Z
depth: standard
files_reviewed: 3
files_reviewed_list:
  - pkg/bloom/bloom.go
  - pkg/bloom/bloom_test.go
  - pkg/bloom/bloom_bench_test.go
findings:
  critical: 1
  warning: 3
  info: 3
  total: 7
status: partially_resolved
resolution:
  resolved: [CR-01, WR-03]
  resolved_in: 326c894
  deferred: [WR-01, WR-02, IN-01, IN-02, IN-03]
---

# Phase 1: Code Review Report

> **Resolution (2026-06-30, commit `326c894`):** CR-01 (unbounded `ReadFilter`
> allocation) and WR-03 (unvalidated header fp-rate) are FIXED — `maxPayloadBytes`
> (1 GiB) cap checked before allocation + `io.CopyN` incremental read; fp-rate
> rejected if NaN/Inf/<=0/>=1. Regression tests added
> (`TestReadFilterRejectsOversizedPayload` incl. above-max-int no-panic case,
> `TestReadFilterRejectsInvalidFPRate`). WR-01 (dead `ContainsHex` error return),
> WR-02 (Builder/Filter aliasing), and the Info items are accepted as known debt
> for a follow-up — none block the Phase 1 goal or are security-critical.

**Reviewed:** 2026-06-30T00:00:00Z
**Depth:** standard
**Files Reviewed:** 3
**Status:** issues_found

## Summary

Reviewed the `pkg/bloom` library: a `Builder`/`Filter` pair wrapping
`bits-and-blooms/bloom/v3` with a custom big-endian DFBF wire format. The
serialization layout, generation marker (sha256 over `MarshalBinary`), hex
boundary helpers, and the `k[:]` alloc-free path are all sound and well-tested.
The `bitset.LittleEndian()` invariant (D-02) is honored — confirmed it is never
called anywhere in `pkg/`.

One BLOCKER stands out and it is the exact issue the prompt asked to scrutinize:
`ReadFilter` allocates a slice sized by an unvalidated attacker-controlled
`uint64` length, with a code comment that falsely claims a bound-check is
performed. This is a denial-of-service / crash vector in the Phase 3 plugin,
which fetches this format over HTTP from a remote `/bloom` endpoint. The
remaining findings concern an unreachable error return, an unenforced
immutability contract, and a dead defensive error path.

## Narrative Findings (AI reviewer)

## Critical Issues

### CR-01: `ReadFilter` allocates an unbounded attacker-controlled slice — comment claims a bound-check that does not exist

**File:** `pkg/bloom/bloom.go:254-259`
**Issue:**
`payloadLen` is a `uint64` read verbatim from the input stream (line 250). It is
then used directly as the size argument to `make`:

```go
// Bound-check before allocation (D-07 defence against malicious/truncated payloadLen).
// We read exactly payloadLen bytes; if the stream ends early ReadFull will return an error.
payload := make([]byte, payloadLen)
```

The comment is wrong. There is **no bound-check** before the allocation — the
declared length is passed straight to `make`. The `io.ReadFull` on the next line
only protects against *consuming* more bytes than exist; it runs *after* the
allocation has already happened. The threat model in D-07 (defending the Phase 3
plugin against a malicious or truncated `/bloom` download) is therefore unmet by
the very line that cites it.

Consequences for a hostile/corrupt header:
- `payloadLen = 0xFFFFFFFFFFFFFFFF` (or any value above platform `maxInt`):
  `make([]byte, payloadLen)` **panics** with `runtime: makeslice: len out of
  range`. `ReadFilter` has no `recover`, so the panic unwinds into the caller
  and crashes the plugin process. This is a remote DoS — a single malformed
  response kills the gate.
- A merely large-but-allocatable value (e.g. `payloadLen = 8 GiB`) succeeds in
  `make`, commits gigabytes of RAM, and only *then* fails in `io.ReadFull` with
  `ErrTruncated` — by which point the OOM damage (or OOM-kill) may already be
  done. Memory is committed before a single payload byte is validated.

A legitimate filter for 500k members at 1e-6 is on the order of ~1.7 MB, so a
generous hard ceiling is both safe and tight.

**Fix:** Add an explicit maximum before allocating, and read defensively. A
streaming `io.LimitReader` + `io.Copy` avoids pre-committing the full size at
all and naturally bounds memory:

```go
// maxPayloadBytes caps the declared payload to defend the plugin-side loader
// against a malicious or corrupt /bloom response (D-07). Sized well above a
// realistic 500k-member 1e-6 filter (~1.7 MB) with generous headroom.
const maxPayloadBytes = 256 << 20 // 256 MiB

// payloadLen:uint64
var payloadLen uint64
if err := binary.Read(r, binary.BigEndian, &payloadLen); err != nil {
    return nil, fmt.Errorf("bloom: ReadFilter: read payloadLen: %w", ErrTruncated)
}
if payloadLen > maxPayloadBytes {
    return nil, fmt.Errorf("bloom: ReadFilter: declared payload %d exceeds max %d: %w",
        payloadLen, maxPayloadBytes, ErrBadFormat)
}

// Bounded read: never commit more than payloadLen bytes, and fail if the
// stream ends early.
var payloadBuf bytes.Buffer
payloadBuf.Grow(int(payloadLen))
read, err := io.CopyN(&payloadBuf, r, int64(payloadLen))
if err != nil || read != int64(payloadLen) {
    return nil, fmt.Errorf("bloom: ReadFilter: read payload (declared %d bytes): %w",
        payloadLen, ErrTruncated)
}
payload := payloadBuf.Bytes()
```

(If retaining `make` + `io.ReadFull`, the cap check above is the minimum
required fix; `io.CopyN` is preferred because it never pre-allocates the full
declared size.) Add a regression test that feeds a header with
`payloadLen = math.MaxUint64` and asserts an `ErrBadFormat` (not a panic).

## Warnings

### WR-01: `ContainsHex` error return is unreachable dead code — misleading API contract

**File:** `pkg/bloom/bloom.go:124-133`
**Issue:**
`ContainsHex(s string) (bool, error)` is documented as lenient (D-10): every
failure path returns `(false, nil)`. Tracing all branches:
- wrong length → `return false, nil`
- hex decode error → `return false, nil`
- success → `return f.Contains(k), nil`

The `error` return value is therefore **always `nil`** — it is dead. The
signature advertises a failure mode that can never occur, which invites callers
to write `if _, err := f.ContainsHex(s); err != nil { ... }` dead branches and
obscures the lenient contract at the call site. The matching test
(`TestContainsHexLenient`) only ever asserts `err == nil`, confirming the return
is vestigial.

**Fix:** Drop the `error` from the signature to make the lenient contract
explicit in the type:

```go
func (f *Filter) ContainsHex(s string) bool {
    if len(s) != 64 {
        return false
    }
    var k [32]byte
    if _, err := hex.Decode(k[:], []byte(strings.ToLower(s))); err != nil {
        return false
    }
    return f.Contains(k)
}
```

If D-09's published API surface must keep the two-value shape for downstream
Phase 2/3 symmetry with `AddHex`, then leave the signature but document
explicitly that the error is always nil — and ideally add a test asserting that,
so the contract is intentional rather than accidental.

### WR-02: `Filter` immutability is documented but not enforced — `Build` leaves a live alias to the underlying filter

**File:** `pkg/bloom/bloom.go:90-102, 106-111`
**Issue:**
`Build()` constructs the `Filter` with `bf: b.bf` — the returned `Filter` and the
originating `Builder` share the **same** `*bbloom.BloomFilter` pointer. The
`Filter` doc (D-09) promises it is "immutable" and "safe to store behind an
`atomic.Pointer[Filter]` and read concurrently." That guarantee holds only if the
Builder is genuinely abandoned. Because the alias is live, a caller who violates
the "Builder must not be used after Build" comment (an easy mistake — nothing
prevents it) will mutate the bitset via `Builder.Add` while plugin goroutines
call `Filter.Contains`, producing an unsynchronized data race on a structure
sold as concurrency-safe. The safety contract rests entirely on a comment.

**Fix:** Either nil out the Builder's reference after freezing so accidental
reuse fails loudly rather than silently corrupting a live Filter:

```go
func (b *Builder) Build() (*Filter, error) {
    payload, err := b.bf.MarshalBinary()
    if err != nil {
        return nil, fmt.Errorf("bloom: Build: MarshalBinary: %w", err)
    }
    gen := sha256.Sum256(payload)
    f := &Filter{bf: b.bf, fp: b.fp, gen: gen, payload: payload}
    b.bf = nil // a subsequent Add will nil-panic instead of mutating a live Filter
    return f, nil
}
```

(A nil-panic on misuse is far preferable to a silent race on the hot path.)
Alternatively reconstruct the Filter's `bf` from `payload` via `UnmarshalBinary`
so it owns a distinct instance — at the cost of one extra unmarshal at build
time, which is off the hot path.

### WR-03: `ReadFilter` does not validate the deserialized fp-rate

**File:** `pkg/bloom/bloom.go:236-240, 267-272`
**Issue:**
`fp` is decoded via `math.Float64frombits` from 8 attacker-controllable bytes and
stored without any sanity check. A crafted header can set `fp` to `NaN`,
`+Inf`, a negative number, or a value `> 1`. `FalsePositiveRate()` then returns
that garbage to downstream consumers, and since the generation marker is computed
only over the *payload* (not the header), the bogus fp-rate rides along
undetected by the content hash. Phase 2 exposes this value (ETag/metadata) and
Phase 3 may surface or act on it. It is not a memory-safety issue (the filter
bits are independent of `fp`), hence WARNING not BLOCKER, but it propagates
malformed data past the deserialization boundary that is supposed to validate
input.

**Fix:** Reject non-finite or out-of-range fp values:

```go
fp := math.Float64frombits(fpBits)
if math.IsNaN(fp) || math.IsInf(fp, 0) || fp <= 0 || fp >= 1 {
    return nil, fmt.Errorf("bloom: ReadFilter: invalid fp-rate %v: %w", fp, ErrBadFormat)
}
```

## Info

### IN-01: `Build`'s `MarshalBinary` error path is effectively unreachable defensive code

**File:** `pkg/bloom/bloom.go:90-94`
**Issue:**
`bbloom.MarshalBinary` writes to an internal `bytes.Buffer` (confirmed in the
v3.7.1 source); a `bytes.Buffer` write never returns an error, so the wrapped
error branch on lines 92-94 cannot trigger in practice. This is harmless,
correctly-written defensive code — noting it only so it is not mistaken for a
tested path. No change required; leaving it is reasonable forward-proofing.

### IN-02: The `bitset.LittleEndian()` hard invariant (D-02) is unenforceable at compile time and global to the process

**File:** `pkg/bloom/bloom.go:12-14` (package doc)
**Issue:**
Confirmed: `pkg/bloom` never calls `bitset.LittleEndian()` — the invariant holds
for this package. However, `bitset.binaryOrder` is **package-global mutable
process state** (verified in `bits-and-blooms/bitset@v1.24.2:75-76`). Any *other*
package linked into the same binary (a transitive dependency, a future sibling
package) that calls `bitset.LittleEndian()` would silently flip the byte order
for this format too, corrupting every subsequent `WriteTo`/`ReadFilter` with no
error. The comment documents the rule but nothing detects a violation.
**Fix:** Consider a cheap guard test that asserts
`bitset.BinaryOrder() == binary.BigEndian` (the package exposes `BinaryOrder()`),
or a round-trip golden-bytes test, so a process-wide flip is caught in CI rather
than in production serialization.

### IN-03: Round-trip test does not assert payload byte-equality or a stable golden vector

**File:** `pkg/bloom/bloom_test.go:45-86`
**Issue:**
`TestRoundTrip` verifies membership, generation, and fp-rate survive a
serialize/deserialize cycle, but it does not assert that the re-serialized bytes
are identical to the original (`f.MarshalBinary() == f2.MarshalBinary()`), nor
does it pin a golden byte vector for the DFBF format. Since this format is the
cross-process / cross-version contract for Phases 2 and 3 (and explicitly
big-endian for portability), a golden vector would catch an accidental
endianness or layout regression — including the global `bitset.LittleEndian()`
flip from IN-02 — that the current behavioral assertions would miss.
**Fix:** Add `f.MarshalBinary()` vs `f2.MarshalBinary()` byte-equality, and a
small golden-bytes test asserting the exact header layout for a fixed tiny
keyset.

---

_Reviewed: 2026-06-30T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
