---
gsd_state_version: 1.0
milestone: v1.1
milestone_name: Bloom Filter Gate Plugin
current_phase: 1
current_phase_name: Shared Bloom Library
status: roadmapped
stopped_at: Phase 1 context gathered
last_updated: "2026-06-29T13:10:39.632Z"
last_activity: 2026-06-29
last_activity_desc: Roadmap created (4 phases, 17/17 requirements mapped)
progress:
  total_phases: 4
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State: Whitelist Plugin — milestone v1.1 (Bloom Filter Gate Plugin)

**Last updated:** 2026-06-29 after creating the roadmap

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-29)

**Core value:** Every event written to the relay comes from a pubkey in the web of trust — enforced cheaply, reliably, and without forking StrFry.

**Current focus:** v1.1 — add a standalone bloom-filter gate plugin (sole local gate, zero per-event HTTP) fed by a new server `/bloom` endpoint, with on-disk persistence for resilience.

## Current Position

Phase: 1 — Shared Bloom Library
Plan: — (not yet planned)
Status: Roadmapped — ready to plan Phase 1
Progress: [          ] 0% (0/4 phases)
Last activity: 2026-06-29 — Roadmap created (4 phases, 17/17 requirements mapped)

## Roadmap Summary

| Phase | Goal | Requirements |
|-------|------|--------------|
| 1. Shared Bloom Library | `pkg/bloom` build/serialize/query, no false negatives, configurable FP rate | BLOOM-01, BLOOM-02, BLOOM-03 |
| 2. Server Bloom Endpoint | Rebuild filter per refresh + conditional `GET /bloom` | SRV-01, SRV-02, SRV-03, SRV-04 |
| 3. Bloom Gate Plugin | `cmd/bloom` local gate, periodic fetch, persist + survive outages | GATE-01..07 |
| 4. Ops & Integration | Build targets, Docker/`strfry.conf`, docs | OPS-01, OPS-02, OPS-03 |

## Accumulated Context

### Decisions

- Sole local gate, no per-event HTTP (maybe-in-set → accept)
- 0.0001% (1e-6) false-positive target, configurable via server YAML
- Separate `cmd/bloom` binary; `whitelist`/`router` stay byte-identical
- Persist filter to `~/deepfry/`; serve from it when server unreachable
- New `GET /bloom` on existing server with conditional GET (ETag)

### Todos

- (none yet)

### Blockers

- (none)

## Session Continuity

**Last session:** 2026-06-29T13:10:39.627Z
**Stopped at:** Phase 1 context gathered
**Resume file:** .planning/phases/01-shared-bloom-library/01-CONTEXT.md

Next action: plan Phase 1 (`/gsd-plan-phase 1`).
