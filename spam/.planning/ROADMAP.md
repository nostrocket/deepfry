# Roadmap: LMDB2GraphQL

## Overview

LMDB2GraphQL is built in five dependency-ordered horizontal layers. Phase 1 is a de-risking spike: the entire Approach B strategy rests on reimplementing strfry/golpe's custom LMDB comparators in Rust and registering them via `mdb_set_compare`. If `heed` cannot support custom comparators, or the reimplementation cannot be made byte-exact, the approach must be revisited before any other work begins. Phases 2-4 build upward: raw read primitives, then the query engine that composes them, then the GraphQL API that exposes the engine. Phase 5 adds the operational shell (health gates, Docker packaging, CI fixture assertions) that makes the service production-worthy inside the DeepFry stack.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: LMDB Foundation & Comparator Proof** - De-risk the comparator technique; open strfry's LMDB safely and prove scan order is byte-exact (4 plans complete 2026-06-11; CR-01 gap closed — seek gate added, LMDB-06 correctness restored)
- [x] **Phase 2: Payload Decoding & Index Scan Primitives** - Decode EventPayload in both formats and build bounded cursor scans over every Event__* index (3 plans complete 2026-06-11; LMDB-07/08/09 satisfied)
- [ ] **Phase 3: Query Engine** - Compose scan primitives into full query semantics (filter routing, latestPerAuthor, NIP-40 expiration, cursor pagination)
- [ ] **Phase 4: GraphQL API** - Expose the query engine as a read-only GraphQL endpoint with hard limit ceilings
- [ ] **Phase 5: Hardening & Docker Packaging** - Add health/ready gates, CI fixture assertions, and docker-compose integration for DeepFry deployment

## Phase Details

### Phase 1: LMDB Foundation & Comparator Proof

**Goal**: golpe's three custom comparators are linked via C++ FFI and registered through heed's `Comparator` trait (`mdb_set_compare`, per D-03), their scan order is verified byte-exact against a pinned strfry fixture, and the service refuses to open an incompatible environment
**Depends on**: Nothing (first phase)
**Requirements**: LMDB-01, LMDB-02, LMDB-03, LMDB-04, LMDB-05, LMDB-06, LMDB-10
**Success Criteria** (what must be TRUE):

  1. The service opens strfry's LMDB environment with `MDB_RDONLY` and the correct `map_size` (no write transactions are ever opened)
  2. The service exits loudly if `Meta.dbVersion != 3` or `Meta.endianness` does not match the host
  3. Comparator self-check passes against the pinned fixture DB: scan order over each `Event__*` index matches strfry's known-correct order
  4. If the self-check fails (scan order mismatch), the service refuses to start (fail-closed, not silently wrong)
  5. The pinned strfry version/digest is recorded in config/docs as a shared contract with the parent DeepFry stack**Plans**: 4 plans (4 waves; 01-04 gap closure)

**Wave 1**

- [x] 01-01-PLAN.md — Crate scaffold, vendor golpe comparators (exception-free), build.rs FFI compile, and the heed comparator-hook go/no-go smoke proof (LMDB-05, LMDB-01) — COMPLETE 2026-06-10

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 01-02-PLAN.md — Pin strfry digest + parent Dockerfile, generate adversarial fixture, hand-compute golden vectors, minimal config (LMDB-10, LMDB-04) — COMPLETE 2026-06-10

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 01-03-PLAN.md — Meta version/endianness gate, open all six Event__* indexes with correct comparators, fail-closed self-check, main startup gate (LMDB-01, LMDB-02, LMDB-03, LMDB-06)

**Wave 4** *(gap closure — blocked on Wave 3 completion)*

- [x] 01-04-PLAN.md — Close CR-01: make the startup comparator self-check exercise the golpe comparator via MDB_SET_RANGE seeks on the adversarial pairs (fail-closed, non-vacuous); honesty fix for overstated docs (LMDB-06, LMDB-05) — COMPLETE 2026-06-11

### Phase 2: Payload Decoding & Index Scan Primitives

**Goal**: Full event JSON can be hydrated from both `0x00` and `0x01` EventPayload formats, and bounded cursor scans over each `Event__*` index are tested in isolation
**Depends on**: Phase 1
**Requirements**: LMDB-07, LMDB-08, LMDB-09
**Success Criteria** (what must be TRUE):

  1. A `0x00` (raw JSON) EventPayload is decoded to full event JSON
  2. A `0x01` (zstd-dictionary-compressed) EventPayload is decoded using the correct `CompressionDictionary[dictId]`
  3. Read transactions are opened and closed per-query (not held open across queries), so strfry can reclaim free pages without `data.mdb` growth

**Plans**: 3 plans (2 waves)

**Wave 1**

- [x] 02-01-PLAN.md — Add zstd dep (legitimacy gate), NostrEvent/DecodedEvent types, open EventPayload/CompressionDictionary read-only, 0x00 raw-JSON decode (LMDB-07) — COMPLETE 2026-06-11

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 02-02-PLAN.md — DictCache (lazy, concurrency-safe) + 0x01 zstd-dictionary decode path with capacity ceiling, synthetic round-trip test (LMDB-08) — COMPLETE 2026-06-11
- [x] 02-03-PLAN.md — Bounded forward/reverse/windowed Event__* scan primitives (DUPSORT-aware, short per-call txns), golden-vector tests (LMDB-09) — COMPLETE 2026-06-11

### Phase 3: Query Engine

**Goal**: Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Depends on**: Phase 2
**Requirements**: QRY-01, QRY-02, QRY-03, QRY-04, QRY-05
**Success Criteria** (what must be TRUE):

  1. An `events()` filter (ids, authors, kinds, since/until) selects the most selective applicable index and returns correctly ordered results hydrated to full event JSON
  2. A tag filter (`Event__tag`) returns events matching the given tag name and values
  3. `latestPerAuthor` returns the latest N events per pubkey via `Event__pubkeyKind` prefix scans (including across all requested pubkeys)
  4. Events with `expiration != 0 && expiration <= now` are excluded from all query results at query time, even if physically present in the index

**Plans**: 4 plans (4 waves)

**Wave 1**

- [ ] 03-01-PLAN.md — Query module skeleton + NostrFilter/TagFilter/PageCursor/QueryError contract types + cursor encode/decode (QRY-01)

**Wave 2** *(blocked on Wave 1 completion)*

- [ ] 03-02-PLAN.md — Index selection (D-02 priority) + per-prefix start_key construction (D-03) + k-way merge over reverse scans on (created_at, levId) DESC (QRY-01, QRY-02)

**Wave 3** *(blocked on Wave 2 completion)*

- [ ] 03-03-PLAN.md — hydrate_lev_ids: post-merge EventPayload point-lookup + decode with skip-warn-count (QRY-04)

**Wave 4** *(blocked on Waves 2-3 completion)*

- [ ] 03-04-PLAN.md — engine: execute_query (route+merge+over-fetch+NIP-40+cursor) and latest_per_author grouped buckets (QRY-01, QRY-02, QRY-03, QRY-05)


### Phase 4: GraphQL API

**Goal**: The query engine is exposed as a read-only GraphQL endpoint with all v1 query types, a hard limit ceiling, cursor pagination, and no mutation surface
**Depends on**: Phase 3
**Requirements**: API-01, API-02, API-03, API-04, API-05, API-06
**Success Criteria** (what must be TRUE):

  1. A consumer can send a GraphQL query for `events()` filtered by ids, authors, kinds, since, until, and limit and receive matching events
  2. A consumer can query `events()` with a tag filter (name + values) and receive matching events
  3. A consumer can query `latestPerAuthor(kind, perAuthor, authors)` and receive the latest N events per pubkey
  4. A consumer can query `stats` and receive event count, max levId, and dbVersion
  5. Queries exceeding the hard limit ceiling are capped, not rejected; cursor pagination on `(created_at, lev_id)` allows traversal without scanning the full DB
  6. The GraphQL schema exposes no mutations

**Plans**: TBD

### Phase 5: Hardening & Docker Packaging

**Goal**: The service is operationally safe for DeepFry deployment: health/readiness gates are live, CI asserts correctness against the pinned strfry fixture, and docker-compose brings it up co-located with strfry mounting `strfry-db` read-only
**Depends on**: Phase 4
**Requirements**: OPS-01, OPS-02, OPS-03, OPS-04
**Success Criteria** (what must be TRUE):

  1. `/health` responds 200 when the process is alive; `/ready` responds 200 only after the LMDB environment opens and the comparator self-check passes (503 otherwise)
  2. The service runs as a `docker-compose` service in the DeepFry stack, co-located with strfry, with `strfry-db` mounted `:ro`
  3. CI generates a fixture `strfry-db` from the pinned strfry version/digest, asserts both `0x00` and `0x01` payload decoding succeed, and asserts LMDB2GraphQL's comparator scan order matches strfry's
  4. Startup output and `stats` surface the expected (pinned) strfry version alongside the detected on-disk `dbVersion`, so operators can immediately spot drift if the parent's `dockurr/strfry` image moves

**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. LMDB Foundation & Comparator Proof | 4/4 | Complete    | 2026-06-11 |
| 2. Payload Decoding & Index Scan Primitives | 3/3 | Complete    | 2026-06-11 |
| 3. Query Engine | 0/4 | Planned     | - |
| 4. GraphQL API | 0/TBD | Not started | - |
| 5. Hardening & Docker Packaging | 0/TBD | Not started | - |
