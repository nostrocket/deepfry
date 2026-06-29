# Phase 1: Shared Bloom Library - Pattern Map

**Mapped:** 2026-06-29
**Files analyzed:** 3 new (`pkg/bloom/bloom.go`, `pkg/bloom/bloom_test.go`, `pkg/bloom/bloom_bench_test.go`)
**Analogs found:** 3 / 3 (all role-match within the same module)

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `pkg/bloom/bloom.go` | library (Builder + Filter) | transform + file-I/O (serialize/deserialize) | `pkg/whitelist/whitelist.go` | role-match (set membership over `[32]byte`); no serialization analog exists |
| `pkg/bloom/bloom_test.go` | test | transform | `pkg/whitelist/whitelist_test.go` | exact (table-ish unit test, same package, hex helpers) |
| `pkg/bloom/bloom_bench_test.go` | test (benchmark) | transform | `pkg/whitelist/whitelist_bench_test.go` | exact (size-swept `b.Run`, `ReportAllocs`, deterministic key gen) |

**Notes on classification:**
- `bloom.go` carries two roles in one file: `Builder` (mutable, server-side, construction = transform) and `Filter` (immutable, plugin-side, query + serialize/deserialize = file-I/O). The CONTEXT (D-09) explicitly splits these by struct, not by file.
- No existing file in this module does any binary marshalling (`encoding/binary`, `MarshalBinary`, `WriteTo`, `sha256`) — confirmed by grep. The custom header in D-05/D-06/D-07 is net-new; the planner should use the bits-and-blooms API + Go stdlib `encoding/binary` directly here, NOT an internal analog.

## Pattern Assignments

### `pkg/bloom/bloom.go` (library: Builder + Filter)

**Analog:** `pkg/whitelist/whitelist.go`

**Imports / package-decl pattern** (`pkg/whitelist/whitelist.go:1-7`) — flat package name = dir name, stdlib-first import block:
```go
package whitelist

import (
	"encoding/hex"
	"strings"
	"sync/atomic"
)
```
For `bloom.go` the import set will instead be roughly: `bytes`, `crypto/sha256`, `encoding/binary`, `encoding/hex`, `errors`, `io`, `math`, `strings`, plus `github.com/bits-and-blooms/bloom/v3`. Keep stdlib grouped first, third-party after (the existing module does not use blank-line-separated import groups — single block, gofmt-sorted — so match that).

**Key-type + alloc-free `[]byte` view pattern** (the load-bearing D-08 convention) — mirror how `whitelist.go` holds `[32]byte` and slices it as `k[:]` for downstream APIs. From `whitelist.go:26-34`:
```go
var k [32]byte
if _, err := hex.Decode(k[:], []byte(strings.ToLower(key))); err != nil {
	return false, nil
}
mp := wl.list.Load()
_, ok := (*mp)[k]
return ok, nil
```
In bloom this becomes `bf.Add(k[:])` (Builder) and `bf.Test(k[:])` (Filter). `k[:]` is a stack slice header over the array — no heap alloc — which is exactly what keeps `Filter.Contains` at 0 allocs/op (the D-08 / `<specifics>` benchmark gate). Do NOT take `[]byte(string)` or copy into a fresh slice on the hot path.

**Hex-decode-at-boundary pattern — invalid = not-present, no panic** (D-10). Two analogs, pick per error contract:

1. `whitelist.go:22-29` — *invalid treated as not-present, returns `(false, nil)`*, len==64 guard, lowercase before decode:
```go
func (wl *Whitelist) IsWhitelisted(key string) (bool, error) {
	if len(key) != 64 {
		return false, nil
	}
	var k [32]byte
	if _, err := hex.Decode(k[:], []byte(strings.ToLower(key))); err != nil {
		return false, nil
	}
	...
}
```

2. `pkg/repository/repository.go:15-23` — *strict variant that returns a real error*, decodes then checks `len(data) != 32`:
```go
func hexTo32ByteArray(hexStr string) ([32]byte, error) {
	var arr [32]byte
	data, err := hex.DecodeString(hexStr)
	if err != nil || len(data) != 32 {
		return arr, fmt.Errorf("invalid hex string for 32-byte array: %s", data)
	}
	copy(arr[:], data)
	return arr, nil
}
```

D-10 says `ContainsHex`/`AddHex` "return an error (or treat as not-present)". Recommended mapping: `Builder.AddHex(string) error` follows analog #2 (strict — a bad key in a build feed is a real fault worth surfacing); `Filter.ContainsHex(string) (bool, error)` may follow analog #1's not-present-on-invalid for hot-path safety. Either way the decode is isolated to these `*Hex` helpers and never runs per-event — `Contains`/`Add` take `[32]byte` directly.

**Atomic-pointer swap for lock-free reads** (`whitelist.go:9-11, 30, 46-52`) — NOT built in Phase 1, but the seam Phase 2 will use to hot-swap a freshly built `*Filter`. The `Filter` produced by `Builder.Build()` must therefore be safe to store behind an `atomic.Pointer[Filter]` and query concurrently (i.e. immutable after Build — no post-Build mutation of internal state). Reference shape:
```go
type Whitelist struct {
	list atomic.Pointer[map[[32]byte]struct{}]
}
// ...
func (wl *Whitelist) UpdateKeys(keys [][32]byte) {
	nm := make(map[[32]byte]struct{}, len(keys))
	for _, k := range keys { nm[k] = struct{}{} }
	wl.list.Store(&nm)
}
```
Builder integration point: `UpdateKeys(keys [][32]byte)` (`whitelist.go:46`) and `repository.KeyRepository.GetAll(ctx) ([][32]byte, error)` (`repository.go:11`) already emit `[][32]byte` — that is the exact input shape `Builder.Add`/`Build` should consume so Phase 2 can feed it directly.

**Error sentinels & wrapping convention** (module-wide grep result). The module uses two patterns; match them:
- Sentinel `var Err... = errors.New(...)` for typed/comparable errors (the planner names e.g. `ErrBadFormat`, `ErrTruncated` per D-09/D-07 framing) — module currently only uses `errors.New` inline (e.g. tests at `whitelist_refresher_test.go:98`), so package-level sentinels are a reasonable, idiomatic extension.
- `fmt.Errorf("...: %w", err)` for contextual wrapping — the dominant pattern: `pkg/handler/messages.go:74,83,92,101`, `pkg/repository/dgraph_repository.go:78,113,157,163`. Use `%w` when wrapping a decode/IO failure in `ReadFilter` so callers can `errors.Is(err, ErrBadFormat)`.

**Serialization — NO internal analog; build from stdlib + library API.** Per D-05/D-06/D-07 the custom big-endian header is:
```
magic[4]="DFBF" | formatVersion:uint8 | fpRate:float64 | gen[32] | payloadLen:uint64 | payload
```
Implementation guidance (load-bearing invariants from CONTEXT, not from any analog):
- Use `encoding/binary` with `binary.BigEndian` for every multi-byte field. The fp-rate `float64` goes through `math.Float64bits` on write / `math.Float64frombits` on read (D-06).
- `payload` is `bf.WriteTo(w)` / `bf.MarshalBinary()` output verbatim; do NOT re-store `m`/`k` — the payload self-describes them (D-06).
- **HARD INVARIANT (D-02):** never call `bitset.LittleEndian()` anywhere in the package — it is a global switch that corrupts the on-disk byte order.
- `gen` = `sha256.Sum256(bf.MarshalBinary())` (D-03), computed at `Build()` freeze time, exposed via `Generation() [32]byte` and `ETag() string`.
- Reader (`ReadFilter`) must validate magic, gate `formatVersion`, and bound-check `payloadLen` before consuming the payload (D-07 — defends against truncated `/bloom` fetch) — return `ErrBadFormat`/`ErrTruncated` rather than panicking.

---

### `pkg/bloom/bloom_test.go` (test)

**Analog:** `pkg/whitelist/whitelist_test.go`

**Same-package white-box test, deterministic key helper** (`whitelist_test.go:1-14`):
```go
package whitelist

import (
	"encoding/hex"
	"testing"
)

func makeKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}
```
Use `package bloom` (white-box) so tests can reach unexported header constants if needed.

**Hit / invalid-input / empty-store test triad** (`whitelist_test.go:16-70`) — mirror these three shapes for `ContainsHex`:
- valid lowercase + uppercase hex accepted (`:16-32`)
- too-short / too-long / non-hex rejected (`:34-54`) — reuse the `make64CharsWithChar('z')` helper idea (`:99-105`)
- empty + zero-value store returns false safely (`:56-70`)

**Phase-1-specific tests the planner must add (from `<specifics>`):**
1. Round-trip: `Builder.Add(...) → Build() → WriteTo(buf) → ReadFilter(buf) → Contains` returns identical membership AND identical `Generation()`/`FalsePositiveRate()`.
2. Determinism: identical keyset → identical `Generation()` across two independent builds (D-03).
3. Zero false negatives: every added member returns `Contains == true`.
4. Measured FP rate at default 1e-6: build, query a large non-member sample (≥1e7), assert measured FP ≤ target (ROADMAP success criterion). Generate non-members with the deterministic pattern from the bench helper to keep it reproducible.

---

### `pkg/bloom/bloom_bench_test.go` (benchmark)

**Analog:** `pkg/whitelist/whitelist_bench_test.go`

**Deterministic key generator** (`whitelist_bench_test.go:9-22`) — copy verbatim (rename to local helper):
```go
func genKeys(n int) [][32]byte {
	keys := make([][32]byte, n)
	for i := 0; i < n; i++ {
		var k [32]byte
		v := uint64(i + 1)
		for j := 0; j < 32; j++ {
			k[j] = byte(v >> ((j % 8) * 8))
		}
		keys[i] = k
	}
	return keys
}
```

**Size-swept sub-benchmarks with alloc reporting** (`whitelist_bench_test.go:34-50, 53-84`):
```go
sizes := []int{1_000, 10_000, 100_000, 500_000}
for _, n := range sizes {
	n := n
	b.Run(fmt.Sprintf("Contains/n=%d", n), func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = filter.Contains(keys[i%len(keys)])
		}
	})
}
```

**Phase-1-specific bench gate (`<specifics>` item 2):** a `Filter.Contains` benchmark with `-benchmem` / `b.ReportAllocs()` that asserts **0 allocs/op** — this validates the `k[:]` alloc-free claim (D-08). Include both a hit-path and miss-path bench mirroring `BenchmarkIsWhitelisted`'s hit/miss split (`whitelist_bench_test.go:68-82`). Also benchmark `Builder.Build()` at the 500k size (mirrors `BenchmarkNewWhiteList`).

---

## Shared Patterns

### `[32]byte` key + alloc-free `k[:]` view
**Source:** `pkg/whitelist/whitelist.go:9-11, 26-34`
**Apply to:** all of `bloom.go` (Builder.Add, Filter.Contains) and both test files.
The canonical key type across the whole module. Pass `k[:]` to bits-and-blooms `Add`/`Test`; never copy onto the heap on the query path.

### Hex-decode-at-boundary (lowercase, len-guard, no panic)
**Source:** `pkg/whitelist/whitelist.go:22-29` (lenient, false-on-invalid) and `pkg/repository/repository.go:15-23` (strict, errors-on-invalid)
**Apply to:** the `*Hex` helpers only (`AddHex`, `ContainsHex`). Keep hex out of the hot path.

### Error wrapping with `%w`
**Source:** `pkg/handler/messages.go:74,83,92,101`; `pkg/repository/dgraph_repository.go:78,113`
**Apply to:** `ReadFilter`, `Build`, `AddHex` — wrap underlying decode/IO errors as `fmt.Errorf("...: %w", err)`; expose comparable sentinels (`ErrBadFormat`, `ErrTruncated`) for format faults.

### Atomic-pointer-swap-compatible immutability
**Source:** `pkg/whitelist/whitelist.go:9-11, 46-52`
**Apply to:** `Filter`. Build() must yield a value safe to publish behind `atomic.Pointer[Filter]` and read concurrently (Phase 2 seam). No mutation after Build.

### Co-located `_test.go` + `_bench_test.go`, same package
**Source:** `pkg/whitelist/{whitelist_test.go, whitelist_bench_test.go}`
**Apply to:** `pkg/bloom/`. White-box (`package bloom`), deterministic key gen shared in concept between the two.

## No Analog Found

| File / Concern | Role | Data Flow | Reason |
|----------------|------|-----------|--------|
| Custom binary header (`DFBF` magic, fp-rate float64, gen[32], payloadLen framing) in `bloom.go` | library | file-I/O | No file in the module performs any `encoding/binary` / `MarshalBinary` / `WriteTo` / `sha256` serialization (confirmed by grep). Planner builds this from Go stdlib + the bits-and-blooms API per CONTEXT D-05/D-06/D-07; there is no internal pattern to copy. |
| `sha256` content-hash generation marker (D-03) | library | transform | Same — no existing crypto-hashing of payloads in this module. Use `crypto/sha256.Sum256(bf.MarshalBinary())` directly. |

## Metadata

**Analog search scope:** `pkg/**`, `cmd/**` (vendor excluded)
**Files scanned:** 14 Go test files + source tree enumerated; deep-read: `whitelist.go`, `whitelist_test.go`, `whitelist_bench_test.go`, `whitelist_refresher.go`, `repository.go`; grep across all `pkg`/`cmd` for serialization + error conventions.
**Pattern extraction date:** 2026-06-29
