# Roadmap: strfry-router-conversion

## Overview

Replace the 174-process `strfry stream` fan-out (`stream-relays.sh`) with a single `strfry router` process, driven by dockur's `ROUTER=` entrypoint env var on the pinned `dockurr/strfry 1.1.0` image. The journey: first generate a standalone, streams-only router config from the existing Web-of-Trust relay list (with mandatory per-stream bloom gating); next wire that config through the `ROUTER=` entrypoint and de-risk the project's highest-unknown — whether config actually loads on this build (the `260701-l5s` blocker); then verify the whole path on the live arm64 host (config loads, streams connect, bloom `decision=` flows, one process not 174, no `MDB_BAD_RSLOT`, relay stays up); finally retire `stream-relays.sh` once the router path is proven, keeping the WoT relay list as the single source of truth. Throughout: never move the LMDB, never fork StrFry, never ship an unverified or ungated ingestion path.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Generate Router Config** - Emit a standalone streams-only router config from the WoT relay list with mandatory per-stream bloom gating (completed 2026-07-02)
- [x] **Phase 2: Wire & Prove Config Loads** - Wire the router via dockur's `ROUTER=` entrypoint and prove the config actually loads (break the `260701-l5s` blocker) as one process (completed 2026-07-02)
- [x] **Phase 3: Verify Gating & Stability On-Host** - Confirm bloom gating flows and the relay stays stable (single process, no `MDB_BAD_RSLOT`) on the live host before cutover (completed 2026-07-02)
- [ ] **Phase 4: Retire the Fan-Out** - Retire `stream-relays.sh` once the router path is proven, keeping the WoT relay list intact as source of truth

## Phase Details

### Phase 1: Generate Router Config

**Goal**: A standalone, streams-only router config file is generated from the same Web-of-Trust relay list `stream-relays.sh` already loads, with bloom gating baked into every stream.
**Depends on**: Nothing (first phase)
**Requirements**: CFG-01, ING-02 (config-side gating)
**Success Criteria** (what must be TRUE):

  1. Running the generator produces a router config whose top-level key is `streams { ... }` and contains NO `db`/`relay` keys (standalone file — golpe would silently drop `streams{}` if merged into `--config`)
  2. The config's `urls` array is populated from the same WoT relay list source `stream-relays.sh` loads (single source of truth — no hand-maintained second list)
  3. Every stream in the generated config carries `pluginDown = "/app/plugins/bloom"` (omitting it silently bypasses the whitelist — release blocker)
  4. The generated file parses as valid taocpp::config (no `Failed to parse router config` when fed to the binary)

**Plans**: 1/1 plans complete

- [x] 01-01-PLAN.md — Generate the standalone streams-only router config from the WoT relay list, with mandatory per-stream bloom gating and discover-then-fail-hard on empty list

### Phase 2: Wire & Prove Config Loads

**Goal**: The router starts with the stack via `docker-compose.strfry.yml` through dockur's `ROUTER=` entrypoint, and its config demonstrably loads on `dockurr/strfry 1.1.0` — breaking the `260701-l5s` blocker where `docker exec ... router <file>` never loaded the positional config.
**Depends on**: Phase 1
**Requirements**: OPS-01, ING-01
**Success Criteria** (what must be TRUE):

  1. `docker-compose.strfry.yml` sets the `ROUTER=` env var pointing at the bind-mounted router config (the untried entrypoint path — `./strfry router "$ROUTER"` — not raw `docker exec`)
  2. `Loading router config file:` appears in the strfry logs (the exact line that NEVER appeared in `260701-l5s` — proves the config was consumed)
  3. `New stream group [ingest]` and `Connecting to wss://...` lines follow, confirming the router activated streams from the generated config
  4. Exactly one `strfry router` process runs (not the 174-process `strfry stream` fan-out)
  5. The LMDB stays at its host bind-mount path — no named-volume migration, no DB relocation (HARD RULE)

**Plans**: 3/3 plans complete
**Wave 1**

- [x] 02-01-PLAN.md — De-risk (D-04): read dockur strfry.sh/Dockerfile/cmd_router.cpp at f31a1b9 and confirm the ROUTER= invocation on paper

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 02-02-PLAN.md — Wire ROUTER=/app/strfry-router.config + a single-file router-config bind-mount into the strfry service (D-01/D-02/D-03)

**Wave 3** *(blocked on Wave 2 completion)*

- [x] 02-03-PLAN.md — On-host proof (D-06/D-07): config loads, streams activate, one router process, LMDB unmoved — recorded as evidence

### Phase 3: Verify Gating & Stability On-Host

**Goal**: On the live arm64 host, the router path is proven end-to-end: whitelist gating actually runs on downloaded events and the canonical relay stays healthy — the verification gate that must pass before cutover.
**Depends on**: Phase 2
**Requirements**: ING-02, OPS-02
**Success Criteria** (what must be TRUE):

  1. Live bloom `decision=accept` / `decision=reject` lines flow for router-down events (the `pluginDown` process is spawned and gating downloaded events — not silently bypassed)
  2. strfry stays up with no `MDB_BAD_RSLOT` under the single-router process (the reader-slot contention that 174 `strfry stream` env-openers caused is gone)
  3. `strfry`, `strfry-quarantine`, and `lmdb2graphql` all remain healthy alongside the running router
  4. Verification is performed on the live host and evidence recorded before any cutover step is taken (no config rewrite ships unverified)

**Plans**: 1/1 plans complete
**Wave 1**

- [x] 03-01-PLAN.md — Verify live bloom gating (accept + reject + not-stored), soak stability (no MDB_BAD_RSLOT), and neighbor-service health on the live host; record 03-VERIFICATION-EVIDENCE.md

### Phase 4: Retire the Fan-Out

**Goal**: The legacy `stream-relays.sh` fan-out is retired now that the router path is verified, leaving no orphaned per-relay `strfry stream` processes and the WoT relay list intact as the single source of truth.
**Depends on**: Phase 3
**Requirements**: OPS-03
**Success Criteria** (what must be TRUE):

  1. `stream-relays.sh` is retired (removed or reduced to a no-op/redirect) and no longer spawns per-relay `strfry stream` processes
  2. No orphaned `strfry stream` processes remain on the host after cutover — only the single `strfry router`
  3. The Web-of-Trust relay list remains in place and unchanged, still driving the router config as the single source of truth

**Plans**: 0/1 plans complete
**Wave 1**

- [ ] 04-01-PLAN.md — Delete `stream-relays.sh` (`git rm`) + replace the README "Useful commands" block with the router ingestion path, then re-verify fresh on the live host (1 router / 0 stream procs, no `strfry-streams` session, WoT relay list unchanged) and record 04-VERIFICATION-EVIDENCE.md

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Generate Router Config | 1/1 | Complete    | 2026-07-02 |
| 2. Wire & Prove Config Loads | 3/3 | Complete    | 2026-07-02 |
| 3. Verify Gating & Stability On-Host | 1/1 | Complete   | 2026-07-02 |
| 4. Retire the Fan-Out | 0/1 | Not started | - |
