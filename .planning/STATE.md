---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
current_phase: 04
current_phase_name: retire-the-fan-out
status: verifying
stopped_at: Phase 3 verification gate PASSED 4/4 — ready for Phase 4 cutover
last_updated: "2026-07-02T11:02:15.170Z"
last_activity: 2026-07-02
last_activity_desc: Phase 04 execution started
progress:
  total_phases: 4
  completed_phases: 4
  total_plans: 6
  completed_plans: 6
  percent: 100
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-01)

**Core value:** One `strfry router` process replaces the 174-process fan-out — without dropping whitelist gating and without crashing the canonical relay (`MDB_BAD_RSLOT`).
**Current focus:** Phase 04 — retire-the-fan-out

## Current Position

Phase: 04 (retire-the-fan-out) — EXECUTING
Plan: 1 of 1
Status: Phase complete — ready for verification
Last activity: 2026-07-02 — Phase 04 execution started

Progress: [██████████] 100%

## Performance Metrics

**Velocity:**

- Total plans completed: 4
- Average duration: - min
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01 | 1 | - | - |
| 02 | 3 | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
| Phase 01 P01 | 3 | 2 tasks | 2 files |
| Phase 02 P01 | 6 | 1 tasks | 1 files |
| Phase 02 P02 | 4min | 1 tasks | 1 files |
| Phase 02 P03 | 9min | 1 tasks | 1 files |
| Phase 03 P01 | 25min | 3 tasks | 1 files |
| Phase 04 P01 | 2min | 3 tasks | 3 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Setup]: Single `strfry router` replaces the per-relay `strfry stream` fan-out (174 env-openers exhaust the LMDB reader-slot table → `MDB_BAD_RSLOT`)
- [Setup]: Drive the router via dockur's `ROUTER=` entrypoint env var, not raw `docker exec ... router <file>` (positional path silently failed to load on 1.1.0/`f31a1b9`)
- [Setup]: Router config is a standalone streams-only file (golpe silently ignores unknown top-level keys, so `streams{}` won't load from the main `--config`)
- [Setup]: `pluginDown=/app/plugins/bloom` mandatory on every stream (global `writePolicy` doesn't gate router-down events)
- [Phase ?]: CONFIRMED: ROUTER=<path> runs ./strfry router positional arg from cwd /app in same container as relay (dockur v1.1.0 strfry.sh + hoytech f31a1b9)
- [Phase ?]: Deployed dockurr/strfry:1.1.0 maps to dockur v1.1.0 TAG (Dockerfile CMD [/app/strfry.sh]); master reverted to plain relay entrypoint and is NOT representative — read the tag
- [Phase 02]: Wired router via ROUTER=/app/strfry-router.config env + :ro bind-mount additively on the existing strfry service (D-01/D-02/D-03); db/relay stay in /etc/strfry.conf, LMDB mount untouched
- [Phase ?]: Live arm64 host proof (Gs-Mac-mini) confirms ROUTER= loads the router config: 'Loading router config file:' appears, breaking the 260701-l5s blocker (OPS-01)
- [Phase ?]: One strfry router process replaces the 144-proc strfry stream fan-out; LMDB stayed at host bind-mount /Volumes/BACKUP/nostr/strfry_database after recreate (ING-01, HARD RULE)
- [Phase ?]: [Phase 03]: Live gating+stability gate PASSED 4/4 on Gs-Mac-mini — bloom accept+reject(not_in_wot) on sourceType=Stream, rejected id not-stored (scan empty) + accepted id stored, 0 MDB_BAD_RSLOT, neighbors healthy; router left running (D-08), Phase 4 unblocked
- [Phase 04]: Retired stream-relays.sh via clean git rm (D-01, no stub) + README rewrite to the strfry router path; re-verified on Gs-Mac-mini (1 router / 0 stream, no strfry-streams tmux session, WoT relay_urls: 174 == strfry-router.config set) — OPS-03 satisfied

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

See .planning/todos/pending/ (carried from prior tasks).

### Blockers/Concerns

[Issues that affect future work]

- **KEY UNKNOWN (de-risked in Phase 2):** On `dockurr/strfry 1.1.0` (`f31a1b9`), `docker exec strfry /app/strfry router <file>` never loads the positional router config (`Loading router config file:` never logs, 0 streams). The untried lead is dockur's `ROUTER=` entrypoint (`./strfry router "$ROUTER"`). Phase 2 must prove config actually loads via this path — highest-risk unknown; do not defer to the end.
- **Gating gotcha:** the global `writePolicy` does NOT gate router-down events — `pluginDown` per stream is mandatory or the whitelist is silently bypassed (release blocker).
- **HARD RULE:** never move the LMDB or migrate to a named volume; verify on the live host before cutover; retire `stream-relays.sh` only AFTER the router path is verified.

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

Last session: 2026-07-02T11:01:25.597Z
Stopped at: Phase 3 context gathered
Resume file: .planning/phases/03-verify-gating-stability-on-host/03-CONTEXT.md
