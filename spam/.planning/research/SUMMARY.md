# Project Research Summary

**Project:** deepfry — Queryable Endpoint over strfry's LMDB
**Domain:** Read-only derived-index / query layer over an embedded KV event store (Nostr relay-adjacent)
**Researched:** 2026-06-10
**Confidence:** HIGH

## Executive Summary

deepfry is a read-only sidecar service that scans strfry's LMDB `EventPayload` sub-database, maintains a derived SQLite index, and serves a GraphQL query surface. The core constraint driving the entire architecture: strfry's `Event__*` index sub-databases use custom runtime-registered LMDB comparators that LMDB never persists to disk — a foreign reader doing range scans on them gets silently wrong results. The canonical approach (Approach A) is to read **only** `EventPayload`, which uses the standard `MDB_INTEGERKEY` comparator on `levId`, and build all query indexes in a derived SQLite store. This sidesteps the comparator trap entirely while never writing to strfry's DB.

The recommended Rust stack is well-established and version-verified: `heed` 0.22.1 (LMDB, actively maintained by Meilisearch), `rusqlite` 0.40.1 with `bundled`+`window` features, `async-graphql` 7.2.1 + `axum` 0.8.x, and `zstd` 0.13.x for dictionary decompression. The sync engine runs on Tokio's `spawn_blocking` pool throughout — LMDB's synchronous C FFI must never be called from async context.

The two highest risks produce **silent wrong results, not errors**: (1) opening any `Event__*` sub-DB causes range scans to return wrong subsets due to the absent comparator; (2) shipping the incremental tailer without the reconciler means deleted events are served forever, because `levId` is monotonic and strfry's deletes emit no new levId. Both must be addressed structurally in early phases, not deferred to hardening.

## Key Findings

### Recommended Stack

A single-process Rust async service (tokio) with a background sync engine and an axum-hosted GraphQL API. LMDB is accessed synchronously via `heed` inside `spawn_blocking`; the derived store is embedded SQLite in WAL mode. Full detail in [STACK.md](STACK.md).

**Core technologies:**
- `heed` 0.22.1 — read-only LMDB access; only actively-maintained crate; `IntegerComparator` for `MDB_INTEGERKEY` keys; supports unnamed-root dbi enumeration and `open_database` (no `MDB_CREATE`). Does NOT force a comparator on existing DBs.
- `rusqlite` 0.40.1 (`bundled`, `window`) — embedded SQLite; `bundled` required for Alpine static build; `window` enables `ROW_NUMBER() OVER` for `latestPerAuthor`; run inside `spawn_blocking`.
- `async-graphql` 7.2.1 + `async-graphql-axum` 7.2.1 — GraphQL with built-in complexity/depth limits and Relay cursor connections; keep both at the same minor version; axum 0.8.x compatible.
- `zstd` 0.13.x — `Decoder::with_dictionary` for `0x01` payloads; cache decoded dictionaries by `dictId`.
- `serde_json` only (no `nostr` crate) — local `#[derive(Deserialize)] struct NostrEvent`; sigs are not re-verified.
- `notify` 8.2.0 + `notify-debouncer-mini` 0.7.0 — file-watch change probe (avoid 9.x RC); bridge to tokio via `spawn_blocking`.
- Avoid: `lmdb-rkv` (dormant), raw `lmdb` 0.8 (abandoned), `sqlx` SQLite (unnecessary async ceremony), `nostr` crate (pulls secp256k1/bech32).

### Expected Features

Detail in [FEATURES.md](FEATURES.md). `latestPerAuthor()` is the **sole genuine differentiator** — the query REQ cannot express; everything else in `events()` is a translation of existing REQ filters. Eight features are correctness-critical (omission → silently wrong data, not degraded perf).

**Must have (table stakes / correctness-critical):**
- DB-version (`==3`) + endianness hard gate — refuse to run loudly on mismatch
- Dual-format payload decoder (`0x00` raw + `0x01` zstd-dictionary)
- Full importer (windowed levId scan, short read txns)
- Incremental tailer **+** periodic/on-demand full reconciler (deletion catch-up) — ship together
- Replaceable / parameterized-replaceable collapse + NIP-40 expiration filtering
- `events()`, `latestPerAuthor()`, `stats` GraphQL queries with hard limit ceiling
- Read-only safety (never open a write txn; `:ro` mount)

**Should have (competitive / operational):**
- Change probe (`negentropyModificationCounter` poll + filesystem watch) to trigger reconcile cheaply
- Cursor pagination on `(created_at, lev_id)`; query complexity/depth limits
- `/health`, `/ready`, Prometheus metrics, staleness reporting

**Defer (v2+ / out of scope):**
- Live subscriptions / push (request-response only for v1)
- Cross-architecture / big-endian support (co-located, same-arch assumption)
- Mutations / relay behavior; hot-path sig re-verification

### Architecture Approach

Five isolated modules with clean ownership and no circular dependencies; one process runs a background sync engine plus the async GraphQL API. Detail in [ARCHITECTURE.md](ARCHITECTURE.md).

**Major components:**
1. `lmdb/` — all unsafe LMDB interaction; `MDB_RDONLY`, short `RoTxn`s, windowed levId scans; copies bytes out of the mmap before txn drop; exposes only `EventPayload`/`Meta`/`CompressionDictionary`.
2. `decoder/` — pure function layer (no I/O); `0x00`/`0x01` payload decode; independently fixture-testable.
3. `store/` — all SQLite; single write path (sync engine) + pooled read path (GraphQL); WAL mode set at schema creation.
4. `sync/` — Full Importer → Incremental Tailer → Reconciler + Change Probe, orchestrated as tokio tasks.
5. `api/` — axum + async-graphql; resolvers call `store/` query layer only; never touch LMDB.

### Critical Pitfalls

Top items from [PITFALLS.md](PITFALLS.md) (12 total):

1. **`Event__*` sub-DB access** — silently wrong range scans (no error). Whitelist allowed dbi names; assert at startup via unnamed-root enumeration; only ever open `EventPayload` (+ `Meta`/`CompressionDictionary`).
2. **Tailer without reconciler** — deleted events served forever. Treat tailer + reconciler as one correctness unit; ship in the same phase.
3. **Long LMDB read txn during import** — pins pages, `data.mdb` grows unbounded on the 10 TiB sparse file. Windowed scan, abort txn per window, copy bytes out of mmap before drop.
4. **`map_size` < strfry's 10 TiB** — env open fails (`MDB_INVALID`). Set map_size ≥ strfry's configured size explicitly.
5. **Tokio calling synchronous LMDB directly** — executor starvation under load (not a compile error). All LMDB/SQLite cursor work in `spawn_blocking`.
6. **Missing `0x01` zstd path / SQLite WAL set late** — zero results after compaction; WAL cannot be retrofitted to an existing file. Implement both decode paths in Phase 1; set WAL at schema creation in Phase 2.

## Implications for Roadmap

Based on research, suggested phase structure (the dependency graph dictates ordering; granularity is **coarse** per config — expect ~5-6 phases collapsible if desired):

### Phase 1: Decoder Library & LMDB Access Layer
**Rationale:** Every other component consumes its output; most pitfall-dense phase (addresses 6 of 12). Fully testable against a fixture `strfry-db` with no live strfry.
**Delivers:** read-only env open (map_size, `READ_ONLY`), dbVersion==3 + endianness gate, dbi whitelist + unnamed-root enumeration, `0x00`/`0x01` payload decoder.
**Avoids:** comparator trap, write-txn corruption, map_size open failure, missing zstd path.

### Phase 2: Derived Store Schema & Full Importer
**Rationale:** The watermark is only meaningful after initial population; WAL and `spawn_blocking` cannot be retrofitted cheaply.
**Delivers:** SQLite schema (`events` + `event_tags` + indexes per spec §4.3) in WAL mode, windowed scan → upsert loop, levId watermark, `/ready` gating on import completion.
**Uses:** `rusqlite` (`bundled`,`window`), `heed` cursors. **Implements:** `store/` + first half of `sync/`.

### Phase 3: Incremental Tailer, Reconciler & Change Probe
**Rationale:** First phase needing a live strfry; deletion correctness lives here; tailer alone is a correctness anti-pattern.
**Delivers:** `MDB_SET_RANGE` tail from `lastLevId+1`, periodic/on-demand full reconcile (diff levId sets, delete stale rows), change probe via `negentropyModificationCounter` + `notify` with a guaranteed interval fallback.

### Phase 4: Replaceable Collapse & Expiration Filtering
**Rationale:** API correctness depends on a semantically accurate store; serving superseded/expired events is a query-correctness bug.
**Delivers:** replaceable + parameterized-replaceable collapse (highest `created_at` per `(pubkey,kind)` / `(pubkey,kind,d-tag)`), NIP-40 expiration filtering at query and reconcile time.

### Phase 5: GraphQL API
**Rationale:** Depends on a correct derived store from Phase 4.
**Delivers:** `events()`, `latestPerAuthor()`, `stats`; cursor pagination on `(created_at, lev_id)`; hard limit ceiling + complexity/depth limits; read-only (no mutations).

### Phase 6: Hardening, Observability & Docker Packaging
**Rationale:** Operational coupling and robustness belong after the functional surface exists.
**Delivers:** `/health`, `/ready`, Prometheus metrics, staleness reporting, `POST /reconcile`, fixture-pinned CI against a strfry commit, `docker-compose.deepfry.yml` with `:ro` strfry-db mount, LMDB page-growth load test.

### Phase Ordering Rationale
- Decoder-first because every component consumes its output; Phases 1-2 are fixture-only (no live strfry).
- Full importer before tailer/reconciler because the watermark requires initial population.
- Replaceable collapse before GraphQL because API correctness depends on store correctness.
- The reconciler cannot slip past Phase 3 — tailer-without-reconciler ships known-stale deletes.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 3:** `notify` file-watch behavior on Docker overlay2 bind mounts (inotify may be suppressed); confirm `negentropyModificationCounter` poll as the guaranteed trigger (MEDIUM confidence on this interaction).
- **Phase 4:** strfry's exact tiebreak when two replaceable events share `created_at` — verify against `src/events.cpp:274-350`, don't infer from NIPs alone.

Phases with standard patterns (skip research-phase):
- **Phase 1:** `heed` API documented (docs.rs + heed cookbook).
- **Phase 2:** `rusqlite` + WAL + `spawn_blocking` is a standard pattern.
- **Phase 5:** `async-graphql` macros / complexity limits / axum integration well-documented.
- **Phase 6:** Docker compose + Prometheus are standard.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All crate versions verified against crates.io / docs.rs; alternatives' dormancy confirmed |
| Features | HIGH | Derived from spec §4-§6 with citations; ecosystem gap (no existing windowed query layer) confirmed |
| Architecture | HIGH | Component boundaries verified via Context7; `RoTxn` drop / `spawn_blocking` semantics confirmed |
| Pitfalls | HIGH | Primary source spec §6, cross-checked vs LMDB/SQLite/Tokio docs |

**Overall confidence:** HIGH

### Gaps to Address
- `notify` + Docker bind-mount inotify propagation: MEDIUM — treat `negentropyModificationCounter` poll as the guaranteed reconcile trigger; validate in CI.
- `deadpool-sqlite` pool sizing: MEDIUM — start at ~5, tune under load in Phase 6.
- Replaceable collapse tiebreak at equal `created_at`: verify against strfry source before Phase 4.
- `EnvFlags::NO_LOCK` vs normal read locking: confirm whether the Docker `:ro` mount grants `lock.mdb` write; prefer `READ_ONLY` without `NO_LOCK` when possible. Validate in Phase 1 env-open helper.
- heed native-endian u64 key type name + Alpine static-link flags (`LMDB_STATIC` / feature): confirm during Phase 1.

## Sources

### Primary (HIGH confidence)
- `spec.md` (project) — verified strfry on-disk encodings, required LMDB operations, §6 caveats (source of truth)
- crates.io / docs.rs — `heed` 0.22.1, `rusqlite` 0.40.1, `async-graphql` 7.2.1, `zstd` 0.13.x, `notify` 8.2.0 version/API verification
- Context7 — `heed` `RoTxn`/`IntegerComparator`, `rusqlite` WAL + `spawn_blocking`, `async-graphql` cursor connections
- LMDB / SQLite WAL / Tokio `spawn_blocking` official docs — comparator non-persistence, WAL-before-first-use, blocking-pool requirement

### Secondary (MEDIUM confidence)
- Community practice — `notify` → tokio bridge pattern; `deadpool-sqlite` `interact()` usage
- Ecosystem scan — nostrdb / nostr-sqlite / Nostrify confirm no existing windowed query layer

---
*Research completed: 2026-06-10*
*Ready for roadmap: yes*
