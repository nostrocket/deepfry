# Requirements: strfry-router-conversion

**Defined:** 2026-07-01
**Core Value:** One `strfry router` process replaces the 174-process fan-out — without dropping whitelist gating and without crashing the canonical relay (`MDB_BAD_RSLOT`).

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Ingestion & Gating

- [ ] **ING-01**: A single `strfry router` process ingests events from the full Web-of-Trust relay list, replacing the 174-process `strfry stream` fan-out
- [x] **ING-02**: Every router-down stream is gated by the bloom whitelist plugin via per-stream `pluginDown=/app/plugins/bloom`, so no ingestion path bypasses the whitelist

### Configuration

- [x] **CFG-01**: The streams-only router config file is generated from the same Web-of-Trust relay list `stream-relays.sh` loads, keeping that list the single source of truth

### Operations

- [x] **OPS-01**: The router starts with the stack via `docker-compose.strfry.yml` using dockur's `ROUTER=` entrypoint, and its config actually loads on `dockurr/strfry 1.1.0` (`Loading router config file:` appears)
- [ ] **OPS-02**: On-host verification confirms config-load, stream connection, live bloom `decision=accept/reject` flow, a single process (not 174), and a stable relay (no `MDB_BAD_RSLOT`) before cutover
- [ ] **OPS-03**: `stream-relays.sh` is retired after the router path is verified, leaving no orphaned per-relay fan-out and the WoT relay list intact as source of truth

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

(None — the project is deliberately narrow and wraps when the conversion ships.)

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Moving the LMDB / migrating to a Docker named volume | HARD RULE (CLAUDE.md) — DB stays at host bind-mount path for cross-host interop |
| Forking or patching StrFry | Stock StrFry only; extend via config/plugins/entrypoint |
| Upgrading off `dockurr/strfry 1.1.0` (`f31a1b9`) | Latest release == pinned commit; won't fix the config-load blocker; separate decision |
| Reusing the legacy `STREAMS=` per-relay fan-out entrypoint | That is the mechanism being replaced, not reused |
| Broad StrFry deploy/orchestration charter | Project deliberately scoped to the router conversion only |
| Work in web-of-trust / LMDB2GraphQL / whitelist-plugin / quarantine-rescuer | Separate subprojects; this project governs the root StrFry deploy only |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| CFG-01 | Phase 1 | Complete |
| ING-02 | Phase 1 (config-side) + Phase 3 (verified live) | Complete |
| OPS-01 | Phase 2 | Complete |
| ING-01 | Phase 2 | Pending |
| OPS-02 | Phase 3 | Pending |
| OPS-03 | Phase 4 | Pending |

**Coverage:**

- v1 requirements: 6 total
- Mapped to phases: 6 ✓
- Unmapped: 0 ✓

*Note: ING-02 (bloom gating) is authored config-side in Phase 1 and verified running-live in Phase 3; its primary/owning phase for delivery accounting is Phase 3 (verification is where the release-blocker gate is proven). No requirement is duplicated as an owned deliverable.*

---
*Requirements defined: 2026-07-01*
*Last updated: 2026-07-01 after roadmap creation (traceability mapped)*
