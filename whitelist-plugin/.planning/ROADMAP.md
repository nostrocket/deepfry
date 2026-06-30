# Roadmap: Whitelist Plugin — v1.1 Bloom Filter Gate Plugin

**Created:** 2026-06-29
**Milestone:** v1.1 (Bloom Filter Gate Plugin)
**Granularity:** coarse
**Core Value:** Every event written to the relay comes from a pubkey in the web of trust — enforced cheaply, reliably, and without forking StrFry.

The v1.0 whitelist server + `whitelist`/`router` plugins are the validated production baseline (see MILESTONES.md); they have no GSD phases and remain unchanged. This roadmap covers only the v1.1 milestone, which adds a standalone bloom-filter gate plugin that eliminates per-event HTTP, fed by a new server `/bloom` endpoint, with on-disk persistence for resilience.

Phases follow the hard dependency chain implied by the architecture: the shared `pkg/bloom` package must exist before the server can serialize a filter; the server `/bloom` endpoint must exist before the plugin can consume it; ops/Docker/docs land last.

## Phases

- [x] **Phase 1: Shared Bloom Library** - `pkg/bloom` builds, serializes, and queries a false-positive-rate-sized filter (completed 2026-06-29)
- [ ] **Phase 2: Server Bloom Endpoint** - Server rebuilds the filter on each refresh and serves it via conditional `GET /bloom`
- [ ] **Phase 3: Bloom Gate Plugin** - Standalone `cmd/bloom` plugin gates writes from a local filter with zero per-event HTTP, persisting and surviving server outages
- [ ] **Phase 4: Ops & Integration** - Build targets, Docker/`strfry.conf` wiring, and documentation for the bloom gate

## Phase Details

### Phase 1: Shared Bloom Library

**Goal**: A reusable `pkg/bloom` package can build a correctly-sized bloom filter from a set of pubkeys and round-trip it through a portable binary format, with membership queries that never produce false negatives.
**Depends on**: Nothing (first phase)
**Requirements**: BLOOM-01, BLOOM-02, BLOOM-03
**Success Criteria** (what must be TRUE):

  1. Building a filter from a set of pubkeys at the default 0.0001% (1e-6) target yields a measured false-positive rate at or below that target on a large sample of non-member keys
  2. A built filter serialized to bytes and deserialized back queries identically, and the serialized form carries its parameters (bit size, hash count) and a generation/version marker
  3. Every pubkey that was added to the filter queries as "possibly present" (zero false negatives), and known non-members query as "definitely not present" except for the bounded false-positive leak
  4. The target false-positive rate is a build-time parameter, not a hardcoded constant

**Plans**: 2/2 plans complete
**Wave 1**

- [x] 01-01-PLAN.md — pkg/bloom Builder + Filter + DFBF serialization + ReadFilter + generation marker; round-trip, determinism, zero-false-negative, measured-FP-rate, invalid-hex tests

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 01-02-PLAN.md — size-swept Contains hit/miss benchmarks + Build benchmark + 0-allocs guard test (D-08 alloc-free hot path)

### Phase 2: Server Bloom Endpoint

**Goal**: The existing whitelist server rebuilds a bloom filter from its in-memory whitelist on every refresh and exposes it over HTTP with cheap conditional polling, without disturbing existing endpoints or read latency.
**Depends on**: Phase 1
**Requirements**: SRV-01, SRV-02, SRV-03, SRV-04
**Success Criteria** (what must be TRUE):

  1. After each whitelist refresh, `GET /bloom` returns a serialized filter whose membership matches the current whitelist (a newly-added pubkey is "possibly present" after the next refresh)
  2. The bloom filter is swapped atomically alongside the existing map — concurrent `/check` and `/bloom` reads never stall or observe a torn filter
  3. A client that sends `If-None-Match` with the ETag from its last fetch receives `304 Not Modified` while the filter is unchanged, and a fresh `200` with a new ETag after a refresh changes it
  4. An operator can set the false-positive rate / sizing in the server YAML and the served filter reflects that setting (default 0.0001%)
  5. The existing `/check`, `/health`, `/stats`, and `/version` endpoints behave exactly as before

**Plans**: 2 plans
**Wave 1**

- [ ] 02-01-PLAN.md — producer seams: `bloom_fp_rate` config (SRV-04), refresher `SetOnRefresh` callback (D-01/02), server filter atomic pointer + `SetStats`/`SwapFilter`/`handleBloom` + `GET /bloom` route with ETag conditional GET and 503-while-loading (SRV-02/03, D-03/05/06/07/08/10)

**Wave 2** *(blocked on Wave 1 completion)*

- [ ] 02-02-PLAN.md — wire `SetOnRefresh` in `cmd/server/main.go` to rebuild + swap the filter (sized by `bloom_fp_rate`) and keep `/stats` live on each refresh (SRV-01, D-09/10); end-to-end test that `/bloom` membership tracks the whitelist and ETag changes after a refresh

### Phase 3: Bloom Gate Plugin

**Goal**: A new standalone `cmd/bloom` StrFry writePolicy plugin makes every accept/reject decision from a locally-held bloom filter with zero per-event HTTP, keeps the filter fresh from the server, and continues gating correctly when the server is unreachable.
**Depends on**: Phase 2
**Requirements**: GATE-01, GATE-02, GATE-03, GATE-04, GATE-05, GATE-06, GATE-07
**Success Criteria** (what must be TRUE):

  1. Running `cmd/bloom` as a StrFry writePolicy plugin, an event from a whitelisted pubkey is accepted and an event from a non-whitelisted pubkey is rejected, using only the local filter — no per-event HTTP request is made
  2. The plugin fetches the filter from the server `/bloom` endpoint at startup and on its configured interval (~6h, conditional GET), atomically swapping in each new filter without dropping events
  3. Each successfully fetched filter is written to the configured directory under `~/deepfry/`, and on a restart with the server unreachable the plugin loads that persisted filter and keeps making decisions
  4. At cold start the plugin blocks (emits no decisions) only when there is neither a reachable server nor a persisted filter on disk; otherwise it serves immediately
  5. Server URL, refresh interval, and persisted-filter path are all configurable via `~/deepfry/` YAML
  6. The plugin reuses the existing `Handler`/`IOAdapter` JSONL abstractions, and the `whitelist`/`router` binaries are left byte-identical

**Plans**: TBD

### Phase 4: Ops & Integration

**Goal**: The bloom gate is buildable, deployable, and documented the same way the existing plugins are, so an operator can select it as the writePolicy plugin and understand the `/bloom` endpoint.
**Depends on**: Phase 3
**Requirements**: OPS-01, OPS-02, OPS-03
**Success Criteria** (what must be TRUE):

  1. `make` targets build the bloom plugin both natively and as a static Alpine binary, matching the existing plugins' build conventions
  2. The Docker image bakes the bloom binary alongside `whitelist`/`router`, and `strfry.conf` can select it as the writePolicy plugin by changing a single line
  3. The README documents the bloom plugin (config, behavior, resilience) and the server's `/bloom` endpoint, consistent with the existing plugin/endpoint docs

**Plans**: TBD

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Shared Bloom Library | 2/2 | Complete    | 2026-06-29 |
| 2. Server Bloom Endpoint | 0/2 | Not started | - |
| 3. Bloom Gate Plugin | 0/? | Not started | - |
| 4. Ops & Integration | 0/? | Not started | - |

---
*Roadmap created: 2026-06-29*
*Coverage: 17/17 v1.1 requirements mapped*
