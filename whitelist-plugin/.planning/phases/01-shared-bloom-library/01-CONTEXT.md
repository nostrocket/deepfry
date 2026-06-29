# Phase 1: Shared Bloom Library - Context

**Gathered:** 2026-06-29
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver a reusable Go `pkg/bloom` package that:
1. Builds a correctly-sized bloom filter from a set of 32-byte pubkeys at a build-time false-positive rate (default 1e-6) — BLOOM-01.
2. Serializes to / deserializes from a portable binary format carrying its parameters and a generation/version marker — BLOOM-02.
3. Answers membership queries distinguishing "definitely not present" from "possibly present", with zero false negatives — BLOOM-03.

This is a pure library phase. It is consumed by the server (Phase 2, build + serve) and the `cmd/bloom` plugin (Phase 3, load + query). No server, plugin, or ops wiring is built here.

</domain>

<decisions>
## Implementation Decisions

### Bloom Implementation Source
- **D-01:** Use `github.com/bits-and-blooms/bloom/v3` (v3.7.1 confirmed) — do NOT hand-roll. Adds one dependency to the otherwise dep-light module, but de-risks the FP-rate math and gives a verified `[]byte`-based `Add`/`Test` API. Construct via `NewWithEstimates(n, fp)`.
- **D-02:** ⚠️ The module must NEVER call `bitset.LittleEndian()` anywhere — it is a global package-level switch that silently changes the on-disk byte order and would break the portable big-endian format. This is a hard invariant for Phases 1–3.

### Generation / Version Marker (BLOOM-02)
- **D-03:** The generation marker is a **content hash of the filter bits**: `sha256.Sum256(bf.MarshalBinary())` (32 bytes). It is deterministic — an unchanged keyset produces identical filter bits and therefore an identical marker across server restarts.
- **D-04:** This same marker is the canonical identity used downstream: the HTTP **ETag** in Phase 2 (`GET /bloom` conditional GET) and the **on-disk staleness signal** in Phase 3. Phase 1 just produces and exposes it; it does not implement HTTP/persistence.

### Serialized Wire / Disk Format (BLOOM-02)
- **D-05:** Custom fixed binary header wrapping the library's own payload. Layout, all big-endian:
  ```
  magic[4]="DFBF" | formatVersion:uint8 | fpRate:float64 | gen[32] | payloadLen:uint64 | payload
  ```
  where `payload` is `bloom.BloomFilter.WriteTo` / `MarshalBinary` output verbatim.
- **D-06:** **Do NOT re-store `m` (bit count) or `k` (hash count) in the custom header** — the bits-and-blooms payload already encodes them (big-endian `uint64(m)`, `uint64(k)`, then bitset) and is self-describing. The header carries only what the library *drops*: the **fp-rate** (use `math.Float64bits`/`Float64frombits` for the `float64`), plus our own `formatVersion` byte and the generation marker. Re-storing m/k risks divergence on a library upgrade.
- **D-07:** The `payloadLen` `uint64` prefix provides explicit framing so the reader can bound-check / detect a truncated `/bloom` fetch before consuming the payload (defensive against partial downloads in Phase 3). `formatVersion` lets us gate our own format changes independent of upstream (which gives no formal cross-version on-disk guarantee).

### Query API Surface (BLOOM-01/03)
- **D-08:** `[32]byte` is the canonical key type (mirrors `pkg/whitelist`'s `map[[32]byte]struct{}`). `bbloom.Add`/`Test` take `[]byte`; passing `k[:]` from a `[32]byte` is alloc-free (stack slice header, no copy), keeping the ~10k-events/sec plugin hot path at zero allocs/op.
- **D-09:** Split construction from query by role:
  - **`Builder`** (server side): `NewBuilder(n uint, fp float64)`, `Add([32]byte)`, `AddHex(string) error`, `Build() (*Filter, error)` (computes the generation marker at freeze time).
  - **`Filter`** (plugin side, immutable / query-only): `Contains([32]byte) bool`, `ContainsHex(string) (bool, error)`, `Generation() [32]byte`, `ETag() string`, `FalsePositiveRate() float64`, `WriteTo(io.Writer)` / `MarshalBinary()`, plus package-level `ReadFilter(io.Reader) (*Filter, error)`.
- **D-10:** Hex decode (64-char lowercase hex from JSONL) is isolated to the `*Hex` boundary helpers — never per-event in the hot path. Invalid-length / non-hex keys return an error (or treat as not-present) at the boundary rather than panicking.

### Claude's Discretion
- Exact hashing path inside the library is owned by bits-and-blooms — no custom hashing of the 32-byte (already high-entropy) pubkey is needed.
- Internal field layout of `Builder`/`Filter`, error sentinel naming (e.g. `ErrBadFormat`), and whether `MarshalBinary` is implemented via a `bytes.Buffer` over `WriteTo` are left to the planner/executor.
- Build-time sizing knobs beyond `(n, fp)` were not discussed — standard `NewWithEstimates` sizing is assumed.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements & Roadmap (this milestone)
- `.planning/REQUIREMENTS.md` §"Shared Bloom Library" — BLOOM-01/02/03 definitions of done.
- `.planning/ROADMAP.md` §"Phase 1: Shared Bloom Library" — goal + 4 success criteria (measured FP rate ≤ target on a large non-member sample; serialize→deserialize identical query + params/marker carried; zero false negatives; FP rate is a build-time parameter not a constant).
- `.planning/PROJECT.md` — milestone v1.1 framing, constraints (single self-contained module, no StrFry fork, ID-only/`[32]byte` whitelist data model).

### Existing code to match against
- `pkg/whitelist/whitelist.go` — canonical `[32]byte` key type, `map[[32]byte]struct{}` store, hex-decode-at-boundary pattern (`hex.Decode(k[:], ...)`, lowercase, len==64 guard). Mirror this for API consistency.

### External library
- `github.com/bits-and-blooms/bloom/v3` (v3.7.1) — `NewWithEstimates(n, fp)`, `Add([]byte)`, `Test([]byte) bool`, `WriteTo`/`ReadFrom`/`MarshalBinary`/`UnmarshalBinary`. Wire payload is big-endian `uint64(m)`, `uint64(k)`, then `bitset.WriteTo`. fp-rate is NOT serialized by the library.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/whitelist`: `[32]byte` key representation, atomic-pointer swap pattern for lock-free reads (informs how Phase 2 will swap the built `*Filter`), and the hex-decode-at-boundary convention to mirror in `*Hex` helpers.
- Module already vendors `secp256k1`/nostr crypto deps; adding `bits-and-blooms/bloom/v3` is the only new direct dependency for this phase.

### Established Patterns
- Hex pubkeys validated as len==64, lowercased, decoded into `[32]byte` (`pkg/whitelist/whitelist.go:IsWhitelisted`). Invalid input is treated as not-present, not an error/panic — consider matching for `ContainsHex`.
- Per-package `_test.go` + `_bench_test.go` co-located (e.g. `pkg/whitelist/whitelist_bench_test.go`). Phase 1 should add both.

### Integration Points
- `Builder` consumes `[][32]byte` — the exact shape the server's whitelist refresh already produces (`UpdateKeys(keys [][32]byte)`), so Phase 2 wiring is a direct feed.
- `Filter.WriteTo`/`ReadFilter` + `ETag()`/`Generation()` are the seams Phase 2 (HTTP serve + ETag) and Phase 3 (fetch, persist, load) attach to.

</code_context>

<specifics>
## Specific Ideas

- **Verification gap flagged by research (for the planner):** library signatures/format were confirmed against pkg.go.dev v3.7.1 + bitset source but NOT compiled locally (module not yet in repo cache). The FIRST execution-phase check must be:
  1. A round-trip test: `Builder.Add` → `Build` → `WriteTo` → `ReadFilter` → `Contains` returns identical membership.
  2. A `testing.B` with `-benchmem` asserting `Filter.Contains` is `0 allocs/op` (validates the `k[:]` alloc-free claim).
- Success-criterion test: build at default 1e-6, then query a large sample (e.g. ≥1e7) of known non-members and assert the measured false-positive rate is at or below target.

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope. (Faster refresh cadence and a bloom metrics endpoint are already captured as GATE-F1/GATE-F2 in REQUIREMENTS.md "v2 / Future".)

</deferred>

---

*Phase: 1-Shared Bloom Library*
*Context gathered: 2026-06-29*
