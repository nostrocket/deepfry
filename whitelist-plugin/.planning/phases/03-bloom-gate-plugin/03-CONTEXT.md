# Phase 3: Bloom Gate Plugin - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning

<domain>
## Phase Boundary

Build a new standalone `cmd/bloom` StrFry writePolicy plugin (a fourth binary alongside `whitelist`/`router`) that makes every accept/reject decision from a locally-held `pkg/bloom` `*Filter` with **zero per-event HTTP**, keeps that filter fresh from the server's `GET /bloom` endpoint, and continues gating correctly when the server is unreachable.

The plugin:
1. Reuses the existing JSONL `Handler`/`IOAdapter` protocol abstractions; a new `Checker` implementation queries the local filter (GATE-01/02).
2. Fetches the filter from `/bloom` at startup and on a periodic conditional-GET interval, atomically swapping in each new filter (GATE-03).
3. Persists each successfully fetched filter under `~/deepfry/` and loads it when the server is unreachable (GATE-04/05).
4. Blocks (emits no decisions) at cold start only when there is neither a reachable server nor a persisted filter (GATE-06).
5. Is configured via `~/deepfry/` YAML (GATE-07).

**Locked by REQUIREMENTS.md (GATE-01..07) + Phase 1/2 context — NOT re-discussed:** sole local gate (not-in-set → reject, maybe-in-set → accept), zero per-event HTTP in steady state, ~6h refresh cadence, persist to `~/deepfry/`, the GATE-06 cold-start blocking rule, reuse of `Handler`/`IOAdapter`, `whitelist`/`router` binaries left byte-identical, and the Phase-2 wire contract (`application/octet-stream` body + `ETag`/`If-None-Match` conditional GET, `503` = "no filter yet").

This phase builds the plugin only — no Makefile/Docker/`strfry.conf`/README work (that is Phase 4).

</domain>

<decisions>
## Implementation Decisions

### Config Layout (GATE-07)
- **D-01:** **Reuse the shared `~/deepfry/whitelist.yaml`** rather than a dedicated `bloom.yaml`. One file for ops to manage. Add a new `BloomConfig` struct + `LoadBloomConfig()` in `pkg/config/config.go` binding viper to the existing `SetConfigName("whitelist")`.
- **D-02:** **Reuse the existing `server_url` key** — the bloom plugin talks to the *same* whitelist server as the `whitelist`/`router` clients, so it shares that key (default `http://localhost:8081`).
- **D-03:** **`bloom_`-prefixed keys** for everything bloom-specific, to stay unambiguous in the shared file and avoid collision with the server's Dgraph `refresh_interval`:
  - `bloom_refresh_interval` (Duration, default `6h`) — the plugin's own periodic fetch cadence, distinct from the server's Dgraph `refresh_interval`.
  - `bloom_path` (string, default `~/deepfry/bloom.dfbf`) — persisted-filter location.
  - `bloom_fetch_timeout` (Duration) — HTTP timeout for the `/bloom` fetch (its own key; do not overload the client's `check_timeout`).
  - Follow the existing `mapstructure` tag + viper `SetDefault` style.

### Cold-Start Order & Blocking Semantics (GATE-05/06)
- **D-04:** **Disk-first, then background refresh.** At startup, load the persisted `bloom_path` filter immediately so the plugin is ready and serving decisions at once; then start the background fetcher which conditionally GETs `/bloom` and atomically swaps in anything newer. Optimizes time-to-ready and survives a slow/down server on restart.
- **D-05:** If there is **no disk file**, the startup path attempts the server fetch as the first source.
- **D-06:** **Block = wait-until-ready, holding events.** When neither disk nor server has a filter (GATE-06), the plugin does **not** consume/respond to stdin events. It keeps retrying the server (with backoff) in the background; once a filter loads it begins draining and deciding. No event is ever decided without a filter — fail-closed by **withholding** decisions (StrFry's submission stalls / backpressures), exactly as GATE-06's "emits no decisions" specifies. Explicitly NOT chosen: exit-nonzero (relies on config-dependent StrFry crash behavior) and reject-all-until-ready (would drop legit events).

### Persistence — Fetch / Validate / Swap / Write Ordering (GATE-04/05)
- **D-07:** **Parse before persist.** After a `200`, run `bloom.ReadFilter` on the response bytes FIRST (its magic + `formatVersion` + `payloadLen` framing from Phase 1 D-05/D-07 detects a truncated/corrupt download). Only on a clean parse do we atomically swap the new `*Filter` in-memory **and** write it to disk. A garbage/partial download is discarded; the prior in-memory filter and prior disk file stay intact. Guarantees the persisted file is always loadable (protects the GATE-05 server-down restart).
- **D-08:** **Atomic write via temp + rename.** Write bytes to a temp file (e.g. `${bloom_path}.tmp`) then `os.Rename` to `bloom_path` (atomic on the same filesystem). A crash mid-write leaves only the stale-but-valid old file or the orphan `.tmp`, never a torn `bloom.dfbf`.
- **D-09:** **Persist on `200` only.** A `304 Not Modified` means nothing changed — no swap, no disk write. (Conditional GET sends the current filter's `ETag` as `If-None-Match`; ETag = the Phase-1 generation marker.)

### Refresh Failure / Staleness Policy (GATE-03/05)
- **D-10:** **Keep last good filter, serve indefinitely.** A failed mid-life refresh (server briefly down, network blip, `503` loading) leaves the last good in-memory filter in place and the plugin keeps gating with it — mirrors the server refresher's own contract (Phase 2 D-02: last snapshot preserved on failure) and the system-wide tolerance of ~6h whitelist staleness. Decisions never degrade on staleness; only the data ages. Log the failure.
- **D-11:** **A few quick retries per cycle, then wait.** On a failed fetch, retry a small number of times with short backoff (mirrors the server refresher's `refresh_retry_count` pattern); if still failing, log and wait for the next `bloom_refresh_interval` tick. Recovers fast from a blip without hammering a down server.

### Handler / Checker Wiring (GATE-01)
- **D-12:** The `Handler` interface is checker-agnostic, so the plugin reuses `handler.NewWhitelistHandler(checker, logger)` and `handler.NewJSONLIOAdapter` **as-is** — the only new decision logic is a `Checker` implementation backed by the atomically-swappable `*bloom.Filter` (e.g. `IsWhitelisted(pubkey string)` → `filter.ContainsHex(pubkey)`; maybe-in-set → accept, not-in-set → reject). This keeps `whitelist`/`router` byte-identical (GATE-01 / PROJECT.md constraint). `cmd/bloom/main.go` mirrors `cmd/whitelist/main.go`'s event-loop scaffolding.

### Claude's Discretion
- Exact type/method/field names (`BloomChecker`, the atomic-pointer holder, the fetcher struct, `LoadBloomConfig` return shape) — follow existing conventions in `pkg/client`, `pkg/whitelist`, `pkg/config`.
- Exact retry count / backoff durations for D-11 — follow the server refresher's conventions.
- How the "ready" gate for D-06 is implemented (e.g. a `sync.Once`/channel/atomic the checker or event loop waits on) — provided no event is decided before a filter exists.
- Whether the periodic fetcher lives in `cmd/bloom` or a small reusable `pkg/` fetcher — provided it stays out of the per-event hot path.
- Logging surface/cadence (stderr `[bloom-plugin]`-prefixed logger like the existing plugins) beyond the failures called out in D-10.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements & Roadmap (this milestone)
- `.planning/REQUIREMENTS.md` §"Bloom Gate Plugin" — GATE-01..07 definitions of done; §"Out of Scope" (no per-event HTTP fallback in steady state; `whitelist`/`router` unmodified).
- `.planning/ROADMAP.md` §"Phase 3: Bloom Gate Plugin" — goal + 6 success criteria (local-only decisions/no per-event HTTP; startup+periodic conditional-GET atomic swap; persist + server-down restart loads disk; cold-start blocks only when neither source; YAML-configurable URL/interval/path; reuse `Handler`/`IOAdapter`, `whitelist`/`router` byte-identical).
- `.planning/PROJECT.md` — v1.1 framing, constraints (single self-contained module, no StrFry fork, per-process YAML under `~/deepfry/`, fail-closed), Key Decisions table.

### Prior phase context (the seams this phase consumes)
- `.planning/phases/02-server-bloom-endpoint/02-CONTEXT.md` — the server-side wire contract this plugin consumes: D-06/D-07 (`application/octet-stream` body + `ETag` from the generation marker + `If-None-Match` 304↔200), **D-08 (`503` + `{"status":"loading"}` = "no filter yet → fall back to persisted on-disk filter")**.
- `.planning/phases/01-shared-bloom-library/01-CONTEXT.md` — DFBF wire/disk format (D-05/06/07: `magic="DFBF"|formatVersion|fpRate|gen[32]|payloadLen|payload`, `payloadLen` framing detects truncated downloads), ETag = `sha256` generation marker (D-03/04), `[32]byte` canonical key + alloc-free `ContainsHex` boundary (D-08/10), and the **hard invariant: never call `bitset.LittleEndian()`** (D-02).

### `pkg/bloom` API (what the plugin calls)
- `pkg/bloom/bloom.go` — `ReadFilter(io.Reader) (*Filter, error)` (parse+validate fetched/persisted bytes), `Filter.Contains([32]byte) bool` / `Filter.ContainsHex(string) (bool, error)` (per-event query), `Filter.ETag() string` (the `If-None-Match` value), `Filter.WriteTo(io.Writer)` / `MarshalBinary()` (persist to disk), `Filter.Generation() [32]byte`.

### Existing code to mirror / wire into
- `cmd/whitelist/main.go` — the event-loop scaffolding (`signal.NotifyContext`, bufio scanner with 64KiB→10MiB buffer, `processLine`/`safeOutput`, stderr logger) for `cmd/bloom/main.go` to mirror.
- `pkg/handler/handler.go` — `Checker` / `Handler` / `IOAdapter` interfaces (the new bloom `Checker` implements `IsWhitelisted(pubkey string) (bool, error)`).
- `pkg/handler/whitelist_handler.go` — `NewWhitelistHandler(checker, logger)` reused as-is; shows the accept/reject + malformed/check-failed mapping.
- `pkg/handler/jsonl_io_adapter.go` — `NewJSONLIOAdapter` reused as-is.
- `pkg/client/client.go` — HTTP-to-server pattern (`http.Client` with timeout, `serverURL` base, `/health` check at startup) to model the `/bloom` fetcher's transport on.
- `pkg/config/config.go` — `LoadClientConfig`/`LoadServerConfig`, viper `SetConfigName("whitelist")` + `SetDefault` + `mapstructure`, `ensureConfigDir()`; add `BloomConfig` + `LoadBloomConfig` here (D-01).
- `pkg/whitelist/whitelist_refresher.go` — the periodic-refresh + retry-with-backoff + atomic-swap goroutine pattern to mirror for the bloom fetcher (D-04/D-11); `refresh()` retry loop and `Start()`/`Stop()` lifecycle.
- `pkg/whitelist/whitelist.go` — `atomic.Pointer[...]` lock-free single-writer/many-reader swap model to mirror for the in-memory `*Filter` swap.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`handler.NewWhitelistHandler` + `handler.NewJSONLIOAdapter`** — checker-agnostic; reused verbatim. The only new decision logic is a `Checker` over `*bloom.Filter` (D-12).
- **`cmd/whitelist/main.go` event loop** — copy the scaffolding (signal context, scanner buffer sizing, `processLine`/`safeOutput`, `[…]`-prefixed stderr logger) into `cmd/bloom/main.go`.
- **`pkg/client` HTTP transport + `pkg/whitelist` refresher** — together model the periodic conditional-GET fetcher (transport from client, retry/backoff/atomic-swap goroutine from refresher).
- **`pkg/config` viper plumbing** — `LoadBloomConfig` slots in beside `LoadClientConfig`/`LoadServerConfig`, same `whitelist.yaml` source.

### Established Patterns
- Viper config: `SetConfigName("whitelist")` + `SetDefault` + `mapstructure` tags + `ensureConfigDir()` writing `~/deepfry/`. `bloom_`-prefixed keys follow this (D-03).
- `atomic.Pointer[T]` single-writer/many-reader for lock-free reads (whitelist map, Phase-2 server filter). The plugin's in-memory `*Filter` swap follows the same model — fetcher writes, hot path reads.
- Plugins fail closed: the existing `whitelist` plugin rejects until the server is reachable. The bloom plugin's GATE-06 "block/hold" (D-06) is the fail-closed analog for the no-filter case.
- Per-event hot path stays alloc-free: query via `ContainsHex` (hex-decode-at-boundary, `k[:]` alloc-free) — Phase 1 D-08/10.

### Integration Points
- **New seam:** the in-memory `atomic.Pointer[*bloom.Filter]` is written only by the background fetcher and read by the bloom `Checker` on every event — single-writer/many-reader, lock-free.
- **Wire-in to server:** the fetcher consumes Phase-2's `GET /bloom` (200 + `ETag` + octet-stream body; `304` on unchanged; `503` = no filter yet → fall back to disk). It sends `If-None-Match: <Filter.ETag()>`.
- **Disk seam:** `bloom_path` under `~/deepfry/` is written (temp+rename, D-08) by the fetcher and read at cold start (D-04). Never delete/overwrite other `~/deepfry/` files.

</code_context>

<specifics>
## Specific Ideas

- The Phase-2 `503 {"status":"loading"}` response is the explicit signal for "server has no filter yet" — the plugin must treat it identically to an unreachable server for fallback purposes (use disk; if no disk, hold per D-06). Do NOT treat `503` as a hard error that aborts the plugin.
- `If-None-Match` must carry the *current* in-memory filter's `ETag()` so a `304` correctly means "what I already hold is current" — pairs with persist-on-200-only (D-09).
- Keep the `whitelist`/`router` binaries byte-identical: all new code is `cmd/bloom/` + additive `pkg/config` (`BloomConfig`/`LoadBloomConfig`) + possibly a small new fetcher package; do NOT touch existing plugin code paths.

</specifics>

<deferred>
## Deferred Ideas

- **Faster (minutes-scale) refresh** for near-real-time propagation — already captured as GATE-F1 (v2/future) in REQUIREMENTS.md. Not this phase.
- **Bloom metrics endpoint / stderr hit-miss-accept counters + filter-generation age** — already captured as GATE-F2 (v2/future). The D-10 staleness-ceiling escalation idea also belongs here if ever wanted.
- **Makefile targets, Docker baking, `strfry.conf` selection, README docs** — explicitly Phase 4 (OPS-01..03), not this phase.

None of these block planning — discussion stayed within phase scope.

</deferred>

---

*Phase: 3-Bloom Gate Plugin*
*Context gathered: 2026-06-30*
