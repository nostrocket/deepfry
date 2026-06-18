# Project Research Summary

**Project:** LMDB2GraphQL — Queryable GraphQL Adapter over strfry's LMDB (a DeepFry component)
**Domain:** Read-only query lens over an embedded KV event store (Nostr relay-adjacent)
**Researched:** 2026-06-10
**Confidence:** HIGH (stack) / MEDIUM (comparator reproduction — needs spike)

> **ARCHITECTURE NOTE:** The project chose **Approach B** (query strfry's live indexes directly; zero replication), not Approach A (derived store). The detailed research files (`STACK.md`, `FEATURES.md`, `ARCHITECTURE.md`, `PITFALLS.md`) were written assuming Approach A and remain useful for the shared parts (Rust stack, LMDB access discipline, payload decoding, pitfalls). Where they describe a derived SQLite store, sync engine, tailer, or reconciler, those are **out of scope** under Approach B. This summary supersedes them for roadmap purposes.

## Executive Summary

LMDB2GraphQL is a read-only sidecar that exposes strfry's LMDB as a GraphQL query endpoint **without copying any data out of strfry**. Instead of building a derived index, it reads strfry's *own* secondary indexes (`Event__pubkeyKind`, `Event__kind`, `Event__id`, `Event__created_at`, `Event__tag`) live, then hydrates full event JSON via `EventPayload[levId]` point-lookups. This honors the stack's hard data-separation rule ("no event payloads outside StrFry") and means LMDB2GraphQL is always perfectly fresh — deletions and replaceable-supersession are reflected instantly because strfry's physical index already reflects them.

The enabling — and risk-bearing — technique is reproducing strfry/golpe's custom LMDB comparators. strfry's index sub-DBs sort by comparators (`StringUint64`, `Uint64Uint64`, `StringUint64Uint64`) that LMDB never persists to disk; a naive `memcmp` reader gets silently-wrong results. LMDB2GraphQL must reimplement these three comparators in Rust and register them via `mdb_set_compare`. The subtlety: trailing native-endian integers (`created_at`, `kind`) must be compared **numerically**, not by `memcmp` — that is precisely why strfry needs a custom comparator. Correctness depends on these being byte-exact, mitigated by a startup self-check against a pinned fixture that **fails closed**.

The recommended Rust stack is well-established and version-verified: `heed` 0.22.1 (LMDB, with custom comparator support), `zstd` 0.13.x (dictionary decompression for payload hydration), `async-graphql` 7.2.1 + `axum` 0.8.x, `serde_json` (no `nostr` crate). There is **no SQLite, no sync engine, no `notify` file-watch** under Approach B — those drop out entirely.

## Key Findings

### Recommended Stack

Single-process Rust async service (tokio): an axum-hosted GraphQL API whose resolvers run synchronous `heed` LMDB scans inside `spawn_blocking`. Full crate detail in [STACK.md](STACK.md) (ignore its `rusqlite`/`notify`/derived-store sections).

**Core technologies:**
- `heed` 0.22.1 — read-only LMDB access **with custom key-comparator support** (`mdb_set_compare` / `key_comparator`). This capability is the feasibility crux of Approach B — verify it in the first spike.
- `zstd` 0.13.x — `Decoder::with_dictionary` for `0x01` payload hydration; cache decoded dictionaries by `dictId`.
- `async-graphql` 7.2.1 + `async-graphql-axum` 7.2.1 — GraphQL with built-in complexity/depth limits and cursor connections; keep both at the same minor version; axum 0.8.x compatible.
- `serde_json` only (no `nostr` crate) — local `#[derive(Deserialize)] struct NostrEvent`; sigs are not re-verified.
- **Dropped vs Approach A:** `rusqlite` (no derived store), `notify` (no change-watch needed — reads are live), `deadpool-sqlite`.

### Expected Features

Detail in [FEATURES.md](FEATURES.md). `latestPerAuthor()` is the **sole genuine differentiator** (REQ cannot express it); under Approach B it is a `Event__pubkeyKind` prefix scan. Correctness-critical features:

**Must have (table stakes / correctness-critical):**
- DB-version (`==3`) + endianness hard gate
- Comparator reimplementation (`StringUint64`, `Uint64Uint64`, `StringUint64Uint64`) + fail-closed startup self-check
- Dual-format payload decoder (`0x00` raw + `0x01` zstd-dictionary) for hydration
- Query engine over strfry indexes + `EventPayload[levId]` hydration
- NIP-40 expiration filtering at query time
- `events()`, `latestPerAuthor()`, `stats` with hard limit ceiling + read-only safety

**Should have (operational):**
- `/health`, `/ready` (ready gates on comparator self-check), cursor pagination, query complexity/depth limits

**Defer (v2+ / out of scope):**
- Prometheus metrics; live subscriptions/push; REST facade; cross-arch/byte-swap
- (No longer applicable under B: derived store, sync engine, reconciler, replaceable-collapse logic, deletion-staleness window)

### Architecture Approach

Four modules, one process. Detail in [ARCHITECTURE.md](ARCHITECTURE.md) (ignore its `store/`/`sync/` sections — those are Approach A).

**Major components:**
1. `lmdb/` — all unsafe LMDB interaction; `MDB_RDONLY`; opens `EventPayload`, `Meta`, `CompressionDictionary`, and the `Event__*` indexes **with the reimplemented comparators registered**; short per-query `RoTxn`s; copies bytes out of the mmap before txn drop.
2. `comparators/` — pure Rust reimplementations of golpe's three comparators + the fixture self-check.
3. `decoder/` — pure function layer; `0x00`/`0x01` payload decode; fixture-testable.
4. `query/` — translates GraphQL filters → index selection + scan → ordered `levId`s → `EventPayload` hydration → NIP-40 filter.
5. `api/` — axum + async-graphql; resolvers call `query/` only.

### Critical Pitfalls

From [PITFALLS.md](PITFALLS.md), reframed for Approach B:

1. **Non-byte-exact comparators** — silently wrong query results (no error). The dominant risk under B. Reimplement carefully; self-check against a pinned fixture; fail closed. Remember: trailing native-endian ints compare numerically, not via `memcmp`.
2. **`heed` cannot register a custom comparator** — would block Approach B entirely. Verify in the first spike before committing the roadmap.
3. **`map_size` < strfry's 10 TiB** — env open fails (`MDB_INVALID`). Set explicitly.
4. **Tokio calling synchronous LMDB directly** — executor starvation; wrap all scans in `spawn_blocking`.
5. **Long read txn during a large scan** — pins pages, `data.mdb` grows; bound scans by query limit + pagination, copy bytes out before drop.
6. **Version coupling** — B depends on strfry's index key layouts + comparator semantics, not just the payload format. Pin a strfry commit; the self-check + dbVersion gate guard upgrades.

## Implications for Roadmap

Suggested phase structure (granularity **coarse** per config — ~4-5 phases). Phase 1 is a de-risking spike because Approach B's whole feasibility rests on the comparator technique.

### Phase 1: LMDB Foundation & Comparator Proof
**Rationale:** Everything depends on (a) `heed` supporting a custom comparator and (b) a byte-exact reimplementation. De-risk first.
**Delivers:** read-only env open (map_size, `READ_ONLY`), dbVersion==3 + endianness gates, the three golpe comparators reimplemented + registered, and a fixture self-check that proves scan order matches strfry's — fail-closed.
**Avoids:** the silent-wrong-data comparator trap; write-txn corruption; map_size open failure.

### Phase 2: Payload Decoding & Index Scan Primitives
**Rationale:** With trustworthy ordering proven, build the read primitives.
**Delivers:** `0x00`/`0x01` payload decoder (zstd dict), `EventPayload[levId]` point-lookup hydration, and bounded cursor scans over each `Event__*` index (prefix + range, newest-first).

### Phase 3: Query Engine
**Rationale:** Compose primitives into the actual query semantics.
**Delivers:** filter → index-selection → ordered levIds for `events()`; `Event__tag` scans; `latestPerAuthor` via `Event__pubkeyKind` prefix scan; NIP-40 expiration filtering; cursor pagination on `(created_at, lev_id)`.

### Phase 4: GraphQL API
**Rationale:** Expose the query engine.
**Delivers:** `events()`, `latestPerAuthor()`, `stats` (count via `mdb_stat`); hard limit ceiling + complexity/depth limits; read-only (no mutations); `spawn_blocking` bridge.

### Phase 5: Hardening, Observability & Docker Packaging
**Rationale:** Operational coupling and robustness after the functional surface exists.
**Delivers:** `/health`, `/ready`, `docker-compose.lmdb2graphql.yml` with `:ro` strfry-db mount, fixture-pinned CI asserting both payload decode and comparator scan-order correctness, load test for read-txn page growth.

### Phase Ordering Rationale
- Comparator proof first — if `heed` can't register a custom comparator or the reimplementation can't be made byte-exact, the whole approach must be revisited; find out on day one.
- Decode + scan primitives before the query engine; query engine before the API.
- No sync/import/reconcile phases — they don't exist under Approach B.

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 1:** Confirm `heed`'s custom-comparator API surface and that `mdb_set_compare` semantics hold for read-only handles. Read golpe's actual comparator source (not just `golpe.yaml` names) to get byte-exact semantics (string segment length handling, native-int numeric compare). HIGH-priority spike.
- **Phase 3:** strfry's tie-break and prefix-boundary handling when scanning `Event__pubkeyKind` (e.g. seeking to the next pubkey); verify against `src/DBQuery.h`.

Phases with standard patterns (skip research-phase):
- **Phase 2/4:** `zstd` dictionary decode and `async-graphql`+axum are well-documented.
- **Phase 5:** Docker compose is standard.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Crate versions verified; `heed` custom-comparator support indicated but spike-confirm |
| Features | HIGH | Derived from spec §3-§6; Approach B surface is a subset (no sync) |
| Architecture | HIGH | Simpler than A — no derived store/sync engine |
| Comparator reproduction | MEDIUM | The crux risk; must verify `heed` capability + byte-exactness against fixture |

**Overall confidence:** HIGH on stack/architecture; MEDIUM pending the comparator spike.

### Gaps to Address
- `heed` custom-comparator API + read-only `mdb_set_compare` behavior — **spike in Phase 1**.
- Byte-exact golpe comparator semantics — read strfry/golpe source, validate against fixture scan order.
- `Event__pubkeyKind` prefix-boundary seeking for `latestPerAuthor` across all pubkeys — verify vs `DBQuery.h`.
- Alpine static-link flags for `heed`/lmdb-sys (`LMDB_STATIC` / feature) — confirm in Phase 1.

## Sources

### Primary (HIGH confidence)
- `spec.md` (project) — verified strfry on-disk encodings, comparator declarations (§3.1, §6.2), required operations; Approach B is spec §2's higher-correctness/higher-coupling option
- crates.io / docs.rs — `heed` 0.22.1, `async-graphql` 7.2.1, `zstd` 0.13.x version/API verification
- Context7 — `heed` `RoTxn`/comparator API, `async-graphql` cursor connections
- LMDB official docs — comparators are never persisted; `mdb_set_compare` semantics

### Secondary (MEDIUM confidence)
- `golpe.yaml` comparator names — exact byte semantics must be confirmed from golpe source
- strfry `src/DBQuery.h` — reference for live index scan semantics

---
*Research completed: 2026-06-10 (revised for Approach B pivot)*
*Ready for roadmap: yes*
