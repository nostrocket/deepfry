# Phase 2: Server Bloom Endpoint - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning

<domain>
## Phase Boundary

The existing whitelist server (`cmd/server` / `pkg/server`) gains the ability to:
1. Rebuild a `pkg/bloom` filter from its in-memory whitelist on **every** refresh and swap it in atomically alongside the existing map (SRV-01).
2. Serve the current serialized filter over a new `GET /bloom` endpoint (SRV-02).
3. Support conditional GET — `If-None-Match` against the filter's ETag → `304 Not Modified` while unchanged, fresh `200` + new ETag after a refresh changes it (SRV-03).
4. Size the filter from a server-YAML false-positive-rate setting, default 1e-6 (SRV-04).

The `Filter`/`Builder` types, DFBF wire format, ETag derivation, and FP-rate-as-parameter all already exist from Phase 1 — this phase **wires the server to them**. It builds no plugin (Phase 3) and no ops/Docker/docs (Phase 4).

</domain>

<decisions>
## Implementation Decisions

### Rebuild Trigger & Ownership
- **D-01:** **Refresher callback.** Add an `onRefresh` hook to `WhitelistRefresher` (e.g. `SetOnRefresh(func(keys [][32]byte))` set before `Start()`). After a successful `UpdateKeys(keys)` inside `refresh()`, the refresher invokes the callback. The server registers a callback that rebuilds the filter and swaps it in. This keeps whitelist and bloom concerns separated and gives a single, well-defined rebuild trigger point. Chosen over "Whitelist-owns-bloom" (avoids putting build cost inside the `pkg/whitelist` swap and keeps that proven package focused) and over "server polls generation" (avoids a second timer + change-detection machinery).
- **D-02:** The callback fires **only on a successful refresh** (after `UpdateKeys`), matching the existing refresher contract where the last good snapshot is preserved on failure. A failed refresh leaves the prior filter in place — no swap, no error to the endpoint.

### Atomic Swap Coupling
- **D-03:** **Two independent atomic pointers.** Leave `whitelist.list atomic.Pointer[map[...]]` exactly as-is (the proven `/check` hot path is untouched). Add a **separate** `atomic.Pointer[Filter]` on the server. The filter is built from the same `keys` the callback receives, so map and filter converge to the same generation each cycle.
- **D-04:** Eventual consistency across the two pointers is acceptable: a brief window where `/check` observes the new map while `/bloom` still serves the prior filter (or vice-versa) is fine — both reads stay lock-free and never stall or tear. A single combined `{map, filter}` snapshot was explicitly rejected to avoid refactoring the `Whitelist` internals the `/check` path depends on.

### /bloom Serving
- **D-05:** **Cache serialized bytes per generation.** When a new filter is swapped in (in the callback / `SwapFilter`), pre-serialize the full DFBF bytes once (`Filter.MarshalBinary()` / `WriteTo`) and store them alongside the `*Filter`. Each `GET /bloom` request writes the cached `[]byte` with an explicit `Content-Length` — no per-request `WriteTo`. (`/bloom` is polled ~6h so request volume is tiny, but caching keeps the handler trivial and allocation-free per request.)
- **D-06:** Response: `Content-Type: application/octet-stream`, `ETag: <Filter.ETag()>` (already a quoted lowercase-hex string of the 32-byte generation marker — D-04 from Phase 1). Body is the cached DFBF bytes.
- **D-07:** **Conditional GET:** compare the request's `If-None-Match` header against the current filter's `ETag()`. On match → `304 Not Modified` (ETag header set, no body). On mismatch/absent → `200` with ETag + body.
- **D-08:** **Not-ready / build-failure response:** when no filter exists yet (during initial whitelist load) or a build has not produced a filter, `GET /bloom` returns `503 Service Unavailable` with a JSON `{"status":"loading"}` body — mirrors the existing `/health` loading behavior. (Phase 3's plugin will treat a 503 as "no filter available → fall back to persisted on-disk filter.")

### Config Surface (SRV-04)
- **D-09:** **Single new YAML key.** Add `bloom_fp_rate` (float64) to `ServerConfig` in `pkg/config/config.go` with viper default `0.000001` (1e-6). The filter is sized via `bloom.NewBuilder(uint(wl.Len()), cfg.BloomFPRate)` on each rebuild — `n` is the live whitelist count, so sizing tracks the real key set. No capacity floor / extra sizing knobs (rejected as unnecessary surface; the whitelist is ~1.5M keys in practice, never tiny).

### Stats Staleness (sanctioned scope addition)
- **D-10:** **Fix `/stats` while we're here.** The same `onRefresh` callback also updates the server's `entries` + `last_refresh` (e.g. a `SetStats(n, now)` analogous to the existing `SetReady`). Today these are set **only once at startup** (`SetReady` in `cmd/server/main.go`) and go stale because per-refresh updates never reach the server. The callback is the natural fix.
  - ⚠️ **Deviation note for the planner/verifier:** ROADMAP success-criterion 5 says `/stats` "behaves exactly as before." The JSON **shape is unchanged** (`{entries, last_refresh}`); only the **values become accurate per-refresh** instead of frozen at startup. This is a deliberate, user-approved correctness improvement, not a regression. `/check`, `/health`, `/version` remain byte-for-byte unchanged.

### Claude's Discretion
- Exact method/field names (`SetOnRefresh` vs `OnRefresh`, `SwapFilter`, `SetStats`), and whether the cached bytes live in a small `{filter, bytes}` struct behind one `atomic.Pointer` or as a parallel pointer, are left to the planner/executor — provided D-03's "leave `whitelist.list` untouched" invariant holds.
- Whether `NewBuilder`'s `n` uses `wl.Len()` captured before or after `UpdateKeys` (both yield the current count) is immaterial.
- Mux registration style for `GET /bloom` should match the existing `mux.HandleFunc("GET /bloom", s.handleBloom)` pattern in `Handler()`.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements & Roadmap (this milestone)
- `.planning/REQUIREMENTS.md` §"Server Bloom Endpoint" — SRV-01/02/03/04 definitions of done.
- `.planning/ROADMAP.md` §"Phase 2: Server Bloom Endpoint" — goal + 5 success criteria (rebuild matches whitelist after refresh; atomic swap, no torn/stalled reads; ETag/`If-None-Match` 304↔200; YAML-configurable FP rate; existing endpoints unchanged).
- `.planning/PROJECT.md` — v1.1 milestone framing, constraints (single self-contained module, no StrFry fork, `~/deepfry/` config), Key Decisions table.
- `.planning/phases/01-shared-bloom-library/01-CONTEXT.md` — Phase 1 decisions D-01..D-10, esp. the DFBF format (D-05/06/07), ETag = generation marker (D-03/04), and the alloc-free query contract.

### Existing code this phase modifies / wires
- `pkg/server/server.go` — `WhitelistServer` struct, `Handler()` mux registration, the `SetReady`/`entries`/`lastRefresh` pattern to mirror for `SetStats`, and the `/health` 503-while-loading pattern to mirror for `/bloom` not-ready.
- `pkg/whitelist/whitelist_refresher.go` — `refresh()` (where the `onRefresh` callback fires after `UpdateKeys`), `Start()`/`Stop()` lifecycle, success-only-after-retries semantics.
- `pkg/whitelist/whitelist.go` — `UpdateKeys`/`Len`; the `atomic.Pointer[map[[32]byte]struct{}]` that MUST stay untouched (D-03).
- `cmd/server/main.go` — wiring: where `refresher` and `srv` are constructed and where the callback must be registered (before `refresher.Start()`).
- `pkg/config/config.go` — `ServerConfig` + viper defaults; where `bloom_fp_rate` is added (SRV-04).

### Bloom package (Phase 1 — the seams this phase attaches to)
- `pkg/bloom/bloom.go` — `NewBuilder(n uint, fp float64)`, `Builder.Add([32]byte)`, `Builder.Build() (*Filter, error)`, `Filter.ETag() string`, `Filter.WriteTo(io.Writer)`, `Filter.MarshalBinary() ([]byte, error)`, `Filter.Generation() [32]byte`. The server builds via `NewBuilder`→`Add`(loop over keys)→`Build`, then serializes once and serves.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`SetReady(entries int)` pattern** (`pkg/server/server.go:36`): stores into `atomic.Int64`/`atomic.Pointer[time.Time]` + flips `ready atomic.Bool`. Mirror this exactly for the per-refresh `SetStats(n, now)` (D-10) and for an `atomic.Pointer[Filter]` swap (D-03/D-05).
- **`/health` 503-while-loading** (`pkg/server/server.go:166`): exact template for `/bloom`'s not-ready 503 + JSON `{status:loading}` body (D-08).
- **`Builder`→`Build`→`MarshalBinary`** from Phase 1: server feeds `[][32]byte` (the same shape the refresher already produces) straight into `NewBuilder`/`Add`.

### Established Patterns
- Mux uses Go 1.22 method-prefixed routes: `mux.HandleFunc("GET /check/{pubkey}", ...)` (`pkg/server/server.go:46`). Add `"GET /bloom"` the same way.
- Handlers set `Content-Type` then write — for `/bloom` set `Content-Type`+`ETag`+`Content-Length` then `w.Write(bytes)`.
- Config via viper with `SetDefault` + `mapstructure` tags (`pkg/config/config.go`); `bloom_fp_rate` follows the existing key style.
- Refresher runs on its own goroutine; the callback executes **on that goroutine**, so the bloom build happens off the request path (no `/bloom` request ever triggers a build).

### Integration Points
- **Refresher → server callback** is the new seam (D-01): registered in `cmd/server/main.go` before `refresher.Start()`. The callback closes over the server (`SwapFilter` + `SetStats`) and the configured `bloom_fp_rate`.
- The server's new `atomic.Pointer[Filter]` (+ cached bytes) is read by `handleBloom`; written only by the callback. Single-writer / many-reader → lock-free, matches the existing whitelist model.
- `SetReady` at startup (`cmd/server/main.go:50`) still marks readiness; the **initial** `refresher.Start()` refresh will fire the callback and build the first filter, so `/bloom` becomes ready in lockstep with the whitelist.

</code_context>

<specifics>
## Specific Ideas

- The user explicitly flagged and approved fixing the latent `/stats` staleness as part of this phase (D-10) — keep the JSON shape identical, make values live.
- `/bloom` polled ~6h ⇒ optimize for correctness/simplicity over per-request throughput; the per-generation byte cache (D-05) is for clean, alloc-free handlers, not for QPS.
- Phase 3's plugin contract depends on D-08 (503 = "no filter, use persisted") and D-06/07 (octet-stream body + ETag conditional GET) — keep these stable; they are the wire contract the gate plugin consumes.

</specifics>

<deferred>
## Deferred Ideas

- **Per-cycle accurate `/stats` beyond entries/last_refresh** (e.g. filter generation age, bloom size) — not in scope; GATE-F2 already captures a bloom metrics endpoint as v2/future in REQUIREMENTS.md.
- **gzip/`Accept-Encoding` on `/bloom`** — not discussed; the DFBF payload is a few MB and polled rarely. Out of scope unless a later phase needs it.
- **Capacity floor / extra sizing knobs** — rejected for this phase (D-09); revisit only if a tiny-whitelist deployment ever appears.

None of these block planning — discussion stayed within phase scope.

</deferred>

---

*Phase: 2-Server Bloom Endpoint*
*Context gathered: 2026-06-30*
