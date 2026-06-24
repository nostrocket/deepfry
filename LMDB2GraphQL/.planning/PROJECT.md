# LMDB2GraphQL — Queryable GraphQL Adapter over strfry's LMDB

> **Naming:** This project is **LMDB2GraphQL**, a component of the **DeepFry** stack. "LMDB2GraphQL" always refers to this adapter; "DeepFry" / "deepfry" always refers to the parent project. Do not conflate the two.

## What This Is

LMDB2GraphQL is a read-only adapter that exposes a [strfry](https://github.com/hoytech/strfry) Nostr relay's LMDB database as a standard, directly queryable GraphQL endpoint. It lets consumers run rich queries the Nostr `REQ` protocol cannot express — e.g. *"latest 20 kind-1 events per pubkey"* — without going through the strfry relay process. It is a **query lens over strfry's live data, not a copy of it**: LMDB2GraphQL reads strfry's existing on-disk indexes directly and never replicates event data into a separate store. It ships as one component of the DeepFry stack.

## Core Value

Serve correct, rich queries over strfry's events by reading strfry's **live** on-disk state directly — never copying event data or indexes out of strfry (honoring the DeepFry stack's data-separation rule: *no event payloads outside StrFry*), while never writing to strfry's database.

## Current Milestone: v1.2 Distinct Author Enumeration

**Goal:** Let a consumer paginate the complete set of distinct pubkeys that have authored at least one event, served directly from strfry's live `Event__pubkey` index in O(distinct authors).

**Delivered (Phase 7, completed 2026-06-24):**
- Engine-layer `distinct_authors` seek-skip enumeration over `Event__pubkey` — one B-tree seek per distinct pubkey via `increment_be` of the 40-byte composite key (`pubkey(32)‖created_at(8)`), not a per-event walk
- Read-only GraphQL `authors(after, limit)` query returning a paginated `AuthorsPage` of distinct hex pubkeys with `hasMore` and an opaque `endCursor`
- `limit` clamped to the same hard ceiling as `events()`; malformed `after` cursor fails closed without echoing offending bytes; resolver runs via `spawn_blocking`

**Prior milestone — v1.1 CORS Support (Phase 6, completed 2026-06-24):** wildcard CORS (`Access-Control-Allow-Origin: *`, no credentials) with correct preflight handling, layered into `build_router` via `tower-http`'s `CorsLayer`, so a cross-host browser frontend can query `/graphql`.

## Requirements

### Validated

- [x] Read-only LMDB access to strfry's environment (`MDB_RDONLY`), never opening a write transaction — *Validated in Phase 1: LMDB Foundation & Comparator Proof*
- [x] Startup gates: refuse to run (loudly) if `Meta.dbVersion != 3` or `Meta.endianness` ≠ host — *Validated in Phase 1*
- [x] Reimplement strfry/golpe's custom comparators (`StringUint64`, `Uint64Uint64`, `StringUint64Uint64`) in Rust and register them via `mdb_set_compare` on the index sub-DBs LMDB2GraphQL scans — *Validated in Phase 1 (C++ FFI link to golpe comparators; registered via heed's `Comparator` trait)*
- [x] Comparator self-check at startup against a pinned fixture; **fail-closed** if our scan order disagrees with strfry's — *Validated in Phase 1 (forward-scan physical-order integrity + MDB_SET_RANGE comparator seek gate on adversarial pairs; CR-01 closed in plan 01-04)*
- [x] Decode `EventPayload` (`0x00` raw + `0x01` zstd-dictionary) to return full event JSON — *Validated in Phase 2 (LMDB-07/08: `decode_event_payload` 0x00 path with exact retained raw bytes, `decode_event_payload_with_cache` 0x01 path via lazy Send+Sync `DictCache` with a 4 MiB decompression-bomb ceiling; malformed input skipped+warned+counted, never panics)*
- [x] Bounded, reversible, resumable cursor scans over each `Event__*` index (DUPSORT-aware, short per-call txns) — *Validated in Phase 2 (LMDB-09: `scan_index_bounded`/`scan_index_windowed`; code-review BLOCKER CR-01 — silent levId drop at DUPSORT window boundaries — fixed via key-granular windowing with a non-vacuity regression proof)*
- [x] Query engine that resolves filters by scanning strfry's `Event__*` indexes and hydrating JSON via `EventPayload[levId]` point-lookups, incl. `latestPerAuthor` and NIP-40 expiration filtering — *Validated in Phase 3 (QRY-01..05)*
- [x] GraphQL API: `events()`, `latestPerAuthor()`, `stats`, with pagination and hard limit ceilings, read-only (no mutations) — *Validated in Phase 4 (API-01..06)*
- [x] Operations & deployment: `/health` + `/ready` probes, Docker subsystem + docker-compose with read-only `strfry-db` mount, pinned-strfry-version surfacing — *Validated in Phase 5 (OPS-01..04)*
- [x] Permissive CORS on the HTTP surface (`Access-Control-Allow-Origin: *`, no credentials) with correct preflight (`OPTIONS`) handling, via `tower-http` `CorsLayer` layered into `build_router` — *Validated in Phase 6: CORS Support*
- [x] Distinct-author enumeration: a paginated read-only GraphQL `authors(after, limit)` query over the live `Event__pubkey` index, O(distinct authors) via seek-skip, fail-closed cursor, `events()`-equivalent limit ceiling — *Validated in Phase 7: Distinct Author Enumeration (QRY-06, API-07)*

### Active

- _None — all v1.2 requirements validated._

### Out of Scope

- **Approach A (derived store / index replication)** — rejected: duplicates data and violates the "no event payloads outside StrFry" data-separation rule. LMDB2GraphQL reads strfry's live state instead.
- **Approach C (shell out to `strfry scan`/`export`)** — prototype-only, not the production path
- Any write transaction against strfry's environment — strfry is the sole writer; a second writer corrupts the env
- A separate sync engine (full import / incremental tailer / reconciler) — unnecessary under Approach B; LMDB2GraphQL reads live, so there is no staleness window for deletions or replaceable-supersession
- Replaceable / parameterized-replaceable collapse logic — strfry already enforces this at write time, so its physical index set is already collapsed; LMDB2GraphQL inherits it for free
- Live subscriptions / push — request/response only for v1
- Mutations / acting as a relay — LMDB2GraphQL is a query surface, not a relay
- Cross-architecture / big-endian support — assumed co-located with strfry on the same little-endian arch (assert, don't byte-swap) for v1
- Re-verifying event signatures on the hot path — strfry already validated on ingest
- Generalization to non-strfry LMDB schemas — v1 targets strfry's golpe schema specifically (despite the generic name)

## Context

- LMDB2GraphQL is one component of the **DeepFry** modular backend stack for Humble Horse (which surrounds a stock, unmodified strfry relay with sidecar services). It follows DeepFry's "never fork strfry" principle and the stack's hard **data-separation rule**: canonical events live only in StrFry's LMDB; **no event payloads outside StrFry**. Approach B honors this literally — LMDB2GraphQL copies nothing.
- **Dependency is on strfry's files, not its process.** LMDB2GraphQL opens the `strfry-db/` LMDB directory (`data.mdb` + `lock.mdb`) directly; it never connects to strfry over WebSocket/NIP-01. So strfry need not be running — if stopped, LMDB2GraphQL reads the last committed on-disk state. When strfry IS running, LMDB's MVCC lets read-only transactions coexist with strfry's single writer and reflect live writes with no sync lag. (CI exercises this by reading a static fixture DB with no strfry process.) The one unsafe overlap is a strfry compaction/upgrade running concurrently with reads — guarded by the `dbVersion` gate + comparator self-check on next open.
- Target strfry DB version: **3** (`src/constants.h: CURR_DB_VERSION = 3`). The on-disk format AND the index key layouts/comparator semantics are strfry-internal and may change between releases with no compatibility guarantee — must pin and CI-test against a specific strfry commit with a fixture DB.
- **Version-pin risk (must coordinate with parent):** the parent DeepFry stack builds strfry from `dockurr/strfry:**latest**` (`Dockerfile.strfry:15`) — an **unpinned** tag. Its strfry version, and therefore the on-disk `dbVersion`/migration schema, can shift silently on any rebuild. LMDB2GraphQL must (a) pin the exact strfry version/digest the parent deploys as a shared contract, (b) generate its CI fixture + comparators from that version, and (c) verify the detected `dbVersion` matches the pin at startup, failing closed otherwise. Recommend the parent pin `dockurr/strfry` to a digest so the schema cannot move under either project. As a read-only consumer, LMDB2GraphQL never triggers a migration; it only refuses to serve against an unexpected schema.
- Approach B reads strfry's secondary indexes directly. This is viable only because the custom comparators (declared in `golpe.yaml`) are small and reimplementable; correctness depends on them being **byte-exact**. The subtlety: trailing native-endian integers (`created_at`, `kind`) do not sort under `memcmp` on little-endian — that is exactly why strfry uses a custom comparator, and exactly what LMDB2GraphQL must reproduce.
- Full spec with verified on-disk encodings, required LMDB operations, and caveats lives in `spec.md`. NOTE: `spec.md` recommends Approach A; this project deliberately chose **Approach B** (listed in spec §2 as the higher-correctness, higher-coupling option).
- The broader DeepFry stack is written in Go; **LMDB2GraphQL is intentionally Rust** — a deliberate divergence chosen for this component.
- Existing infrastructure: strfry at `ws://localhost:7777` with LMDB at `./strfry-db/` (default), 10 TiB sparse `mapsize`. DeepFry config convention: per-component config under `~/deepfry/`.

## Constraints

- **Tech stack**: Rust — needs LMDB read bindings with **custom comparator support** (`mdb_set_compare`), zstd (dictionary decompression for payload hydration), and a GraphQL server. No SQLite / no derived store. Diverges from the Go stack by deliberate choice.
- **Correctness (the crux)**: LMDB2GraphQL's reimplemented comparators must be byte-identical to strfry's, or range scans return silently-wrong data. Mitigate with a pinned-fixture self-check that fails closed at startup.
- **Coupling**: Approach B depends on strfry's internal index key formats AND comparator semantics (broader surface than the payload format alone). Pin a strfry version; treat as a private API.
- **Safety**: read-only LMDB access only (`MDB_RDONLY`); never open a write txn. Keep read txns short (per-query, bounded by limit) so strfry can reclaim pages and `data.mdb` doesn't grow unbounded.
- **Compatibility**: hard version gate — refuse to run if `Meta.dbVersion != 3`. Assert `Meta.endianness` matches host; refuse on mismatch (co-located assumption).
- **Deployment**: packaged as its own Docker subsystem within the DeepFry stack, with a docker-compose service, co-located with strfry, mounting `strfry-db` read-only.
- **Compression**: must handle `0x01` zstd-dictionary payloads (appears after strfry offline compaction) even though default deployments are `0x00` uncompressed.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Name = LMDB2GraphQL (a DeepFry component, distinct from the parent) | Avoid conflating this adapter with the parent DeepFry project | — Pending |
| Approach B (query strfry's live indexes; zero replication) | Honors "no payloads outside StrFry"; always fresh (deletions/supersession reflected instantly); eliminates the entire sync/reconcile/staleness subsystem | — Pending |
| Reimplement golpe comparators in Rust + register via `mdb_set_compare` | Required to range-scan `Event__*` correctly from a foreign process; comparators are small and documented in `golpe.yaml` | — Pending |
| Pinned-fixture comparator self-check, fail-closed | Concentrated correctness risk lives in ~3 comparators; verify scan order matches strfry before serving | — Pending |
| Pin strfry version to the parent's deployed version (shared contract) | Parent uses `dockurr/strfry:latest` (unpinned); schema/dbVersion could shift silently. Pin + verify at startup + flag parent to pin to a digest | — Pending |
| No derived store, no SQLite, no sync engine | Direct consequence of Approach B; nothing to import, tail, or reconcile | — Pending |
| Query API = GraphQL | Self-documenting, flexible filtering; expresses `latestPerAuthor` cleanly | — Pending |
| Request/response only (no subscriptions) for v1 | Keeps v1 simple | — Pending |
| Co-located, same arch (assert endianness) | Avoids byte-swap/cross-arch complexity; matches deployment reality | — Pending |
| Language = Rust | Deliberate divergence from the Go DeepFry stack for this component | — Pending |
| Packaged as Docker subsystem + compose, read-only `strfry-db` mount | Matches existing DeepFry infra conventions | — Pending |
| v1.1: Wildcard CORS (`Allow-Origin: *`, no credentials) | Corpus is public Nostr relay data; same-origin was the only cross-origin barrier. Wildcard lets any browser frontend query it; no credentials means no allowlist needed and no conflict with `*`. Orthogonal to `bind_address` network-exposure control | ✓ Shipped (Phase 6) |
| v1.2: 40-byte composite seek key for distinct-author seek-skip | `StringUint64Cmp` strips the last 8 bytes as the uint64 suffix before comparing the string prefix; a 32-byte seek key would present only a 24-byte string prefix and mis-position. The seek key must be `increment_be(pubkey)‖0x00*8` (40 bytes) to jump cleanly to the next distinct pubkey in O(1) seeks | ✓ Shipped (Phase 7) |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-24 — Phase 7 (Distinct Author Enumeration, QRY-06/API-07) complete; verification PASSED 5/5. v1.2 milestone delivered: a paginated read-only GraphQL `authors` query over the live `Event__pubkey` index, O(distinct authors) via seek-skip. v1.1 shipped wildcard CORS (Phase 6). v1.0 shipped Phases 1–5 (all 25 requirements): read-only LMDB + byte-exact golpe comparators (P1), payload decode + bounded scans (P2), query engine over live indexes (P3), GraphQL API (P4), hardening + Docker packaging (P5).*
