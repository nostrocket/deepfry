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
- [x] **Phase 3: Query Engine** - Compose scan primitives into full query semantics (filter routing, latestPerAuthor, NIP-40 expiration, cursor pagination) — 11 plans incl. gap-closures 03-05..03-11; final fat-group cursor-stranding + ts=0 non-termination blockers closed via debug fix (lev_id_floor in merge_windowed, commit f4ec868); verification PASSED 5/5 must-haves (completed 2026-06-13)
- [x] **Phase 4: GraphQL API** - Expose the query engine as a read-only GraphQL endpoint with hard limit ceilings (completed 2026-06-13)
- [x] **Phase 5: Hardening & Docker Packaging** - Add health/ready gates, CI fixture assertions, and docker-compose integration for DeepFry deployment (2 plans executed 2026-06-15; verification gaps_found 6/7 — OPS-01 /ready 503 branch unreachable in production; gap-closure plan 05-03 created) (completed 2026-06-15)

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

**Plans**: 11 plans (11 waves; 03-05/06/07 + 03-08/09 + 03-10 + 03-11 gap closure for the verification gaps in 03-VERIFICATION.md)

**Wave 1**

- [x] 03-01-PLAN.md — Query module skeleton + NostrFilter/TagFilter/PageCursor/QueryError contract types + cursor encode/decode (QRY-01)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 03-02-PLAN.md — Index selection (D-02 priority) + per-prefix start_key construction (D-03) + k-way merge over reverse scans on (created_at, levId) DESC (QRY-01, QRY-02)

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 03-03-PLAN.md — hydrate_lev_ids: post-merge EventPayload point-lookup + decode with skip-warn-count (QRY-04) — COMPLETE 2026-06-12

**Wave 4** *(blocked on Waves 2-3 completion)*

- [x] 03-04-PLAN.md — engine: execute_query (route+merge+over-fetch+NIP-40+cursor) and latest_per_author grouped buckets (QRY-01, QRY-02, QRY-03, QRY-05) — COMPLETE 2026-06-12

**Wave 5** *(gap closure — Cluster 2 / CR-05; blocked on Wave 4)*

- [x] 03-05-PLAN.md — hydrate_lev_ids returns lev_id-associated pairs; engine joins on lev_id (not positional zip) so a corrupt-payload skip cannot corrupt the cursor or shift keys (QRY-04 / CR-05)

**Wave 6** *(gap closure — Cluster 1 / CR-01,02,03,04 + WR-01; blocked on Wave 5: shares engine.rs)*

- [x] 03-06-PLAN.md — merge prefix guard (take_while starts_with), since stop-bound + kind/author/id residual, exclusive-resume windowing (proven scan.rs pattern), lev_id reset on stuck advance, deduped start keys (QRY-01 / CR-01,CR-02,CR-03,CR-04,WR-01,IN-01,IN-04)

**Wave 7** *(gap closure — Cluster 3 / CR-06,07 + WR-04; blocked on Wave 6: shares engine.rs/router.rs)*

- [x] 03-07-PLAN.md — Event__tag decode restricted to 64-char lowercase hex; single-char tag-name validation; tag residual changed to NIP-01 AND across distinct fields (QRY-02 / CR-06,CR-07,WR-04,IN-02)

**Wave 8** *(gap closure — CR-01 scan-layer; blocked on Wave 7: touches src/lmdb/scan.rs only)*

- [x] 03-08-PLAN.md — reverse first-window/bounded Bound::Included on an existing key drops higher dup-group levIds; fix builds the reverse upper bound from ts+1 (saturating) with Bound::Excluded so the largest dup is the landing point; fixture regression until=1700000256 returns both levId 7 and 8 (QRY-01 / CR-01)

**Wave 9** *(gap closure — CR-02/CR-03 engine k-way merge; blocked on Wave 8: consumes the corrected scan bound)*

- [x] 03-09-PLAN.md — replace sort-per-batch with a windowed k-way merge (merge_windowed) routed through the engine for true (created_at DESC, lev_id DESC) order across iterations; per-stream since exhaustion instead of global since_cutoff; delete orphaned merge path + IN-01/02/03 cleanups (QRY-01,QRY-02 / CR-02,CR-03,WR-04,IN-01,IN-02,IN-03)

**Wave 10** *(gap closure — new REVIEW CR-01 no-backfill-loop; blocked on Wave 9: shares engine.rs)*

- [x] 03-10-PLAN.md — restore a bounded round-loop (MAX_ROUNDS budget) in execute_query_internal calling merge_windowed from an advancing resume boundary, build a partial-result cursor when the budget stops the loop early so reachable events are never stranded, rewrite the divergent comments; fold in scan.rs reverse_upper_bound fail-soft + merge.rs MergeCandidate Eq/Ord consistency (QRY-01,QRY-02 / new REVIEW CR-01; WR-03 budget preserved) — COMPLETE 2026-06-13

**Wave 11** *(gap closure — REVIEW CR-01/CR-02 cursor stranding; blocked on Wave 10: shares engine.rs)*

- [x] 03-11-PLAN.md — close REVIEW CR-01 (empty-valid budget cap → false EOF: add deepest_scanned fallback cursor) + CR-02 (fat-timestamp pagination stall: detect no-progress and break) in execute_query_internal; add two regression tests the 11-event fixture missed (QRY-01, QRY-05 / REVIEW CR-01,CR-02; WR-03 budget preserved)

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

**Plans**: 3 plans (2 waves + gap closure)

**Wave 1**

- [x] 04-01-PLAN.md — Add GraphQL crates (async-graphql/-axum, axum, tokio), `bind_address` config field, and all GraphQL schema types (Event/EventsPage/AuthorGroup/StatsResult + EventFilterInput/TagFilterInput) + DecodedEvent→Event mapping (API-01..API-05)

**Wave 2** *(blocked on Wave 1: shares src/lib.rs + consumes 04-01 types)*

- [x] 04-02-PLAN.md — AppState + query-only schema (EmptyMutation), the three resolvers (events/latestPerAuthor/stats) composing the Phase-3 engine via spawn_blocking, limit/perAuthor clamps, error mapping, axum router (POST /graphql + GraphiQL), and main.rs server startup after the gates (API-01..API-06)

### Phase 5: Hardening & Docker Packaging

**Goal**: The service is operationally safe for DeepFry deployment: health/readiness gates are live, CI asserts correctness against the pinned strfry fixture, and docker-compose brings it up co-located with strfry mounting `strfry-db` read-only
**Depends on**: Phase 4
**Requirements**: OPS-01, OPS-02, OPS-03, OPS-04
**Success Criteria** (what must be TRUE):

  1. `/health` responds 200 when the process is alive; `/ready` responds 200 only after the LMDB environment opens and the comparator self-check passes (503 otherwise)
  2. The service runs as a `docker-compose` service in the DeepFry stack, co-located with strfry, with `strfry-db` mounted `:ro`
  3. CI generates a fixture `strfry-db` from the pinned strfry version/digest, asserts both `0x00` and `0x01` payload decoding succeed, and asserts LMDB2GraphQL's comparator scan order matches strfry's
  4. Startup output and `stats` surface the expected (pinned) strfry version alongside the detected on-disk `dbVersion`, so operators can immediately spot drift if the parent's `dockurr/strfry` image moves

**Plans**: 2 plans (2 waves)

**Wave 1**

- [x] 05-01-PLAN.md — /health + /ready probes (Arc<AtomicBool> readiness flag set after the comparator self-check) and pinnedStrfryVersion surfaced through the stats query (OPS-01, OPS-04) — COMPLETE 2026-06-13

**Wave 2** *(blocked on Wave 1: docker-compose healthcheck consumes /health)*

- [x] 05-02-PLAN.md — Alpine multi-stage Dockerfile + docker-compose service mounting strfry-db :ro co-located with strfry, and CI workflow + fixture-generation script asserting comparator scan order + 0x00/0x01 decode against the pinned strfry digest (OPS-02, OPS-03)

**Gap closure** *(closes the OPS-01 BLOCKER from 05-VERIFICATION.md)*

- [x] 05-03-PLAN.md — Bind the listener and serve a probe-only /health+/ready router BEFORE the gate chain; store(true) only after the comparator self-check passes; full GraphQL router on a re-bound listener after gates — makes the /ready 503→200 window observable to a real orchestrator (OPS-01) [code-review fix CR-01/CR-02: redesigned as bind-once gated router — single TcpListener::bind, one axum::serve for process lifetime, POST /graphql gated behind Arc<OnceCell<AppSchema>>, eliminates connection-refused window and ephemeral-port re-bind bug]

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. LMDB Foundation & Comparator Proof | 4/4 | Complete    | 2026-06-11 |
| 2. Payload Decoding & Index Scan Primitives | 3/3 | Complete    | 2026-06-11 |
| 3. Query Engine | 11/11 | Complete    | 2026-06-13 |
| 4. GraphQL API | 2/2 | Complete    | 2026-06-13 |
| 5. Hardening & Docker Packaging | 3/3 | Complete   | 2026-06-15 |
