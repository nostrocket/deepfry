---
gsd_state_version: 1.0
milestone: v1.3
milestone_name: Unbounded Dgraph Retry Resilience
status: Roadmapped — awaiting `/gsd-plan-phase 10`
last_updated: "2026-06-15T04:55:18.278Z"
last_activity: 2026-06-15 — v1.3 roadmap created
progress:
  total_phases: 1
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State: Web-of-Trust Crawler — v1.3 Unbounded Dgraph Retry Resilience

**Last updated:** 2026-06-15

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 10 — unbounded-retry-backoff-hardening (roadmapped, not yet planned)

## Current Position

Phase: 10 (Unbounded Retry & Backoff Hardening)
Plan: —
Status: Roadmapped — awaiting `/gsd-plan-phase 10`
Last activity: 2026-06-15 — v1.3 roadmap created

```
Progress: [          ] 0% (0/1 phases complete)
```

## Performance Metrics

- Phases complete (v1.3): 0 / 1
- Requirements delivered (v1.3): 0 / 8
- Plans complete (v1.3): 0 / TBD

- Phases complete (v1.2): 5 / 5
- Requirements delivered (v1.2): 21 / 21 (VALID-01/02/03, FILTER-01/02, RELAY-01/02/03, LOG-01/02/03, PERF-01/02, TIMEOUT-01/02, METRIC-01, HARD-01/02/03/04, RESIL-01)
- Plans complete (v1.2): 11 / 11

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| Continue numbering from Phase 10 | v1.2 ended at Phase 9; sequential numbering per config |
| Single phase for all 8 v1.3 requirements | Coarse granularity + tight coupling: all 8 requirements touch the main-loop retry/backoff code in cmd/crawler/main.go; no natural delivery boundary splits them |
| Phase 10 = unbounded retry + backoff + shutdown + observability + tests | RETRY/BACKOFF/SHUTDOWN are inseparable (removing the attempt cap while keeping bounded backoff produces no useful intermediate state); OBS-01 and TEST-01 ship in the same pass to validate the new behavior |
| v1.2 decisions | (see v1.2 archived state below) |

### Important Facts

- v1.3 target: `cmd/crawler/main.go` only — retry constants at lines 26-28, four near-identical retry blocks, `isDgraphTransient` already at lines 37-51.
- RESIL-01 (Phase 9) added the retry skeleton with 5s initial, 2m cap, 5 attempts max — v1.3 removes the attempt cap and changes timing to 1m initial / 5m cap.
- The four main-loop Dgraph calls requiring indefinite retry: `GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, `MarkAttempted`.
- Context-cancel shutdown must interrupt `time.Sleep`-style waits — use `select` on `ctx.Done()` vs a timer channel, not a bare sleep.
- OBS-01 requires per-call-type average duration logging; implementation likely uses a sliding window or running average per call type.
- TEST-01 covers the retry/backoff helper in isolation: indefinite transient retry, context-cancel interruption, 1m→2m→4m→5m cap sequence, fatal-code passthrough.
- Live config at `~/deepfry/web-of-trust.yaml` must not be edited for testing; use a temp `HOME`.
- Integration tests gate on live Dgraph via `//go:build integration` / `make test-integration`.

### Todos

- [ ] Plan Phase 10 (`/gsd-plan-phase 10`)

### Roadmap Evolution

- Phase 10 added 2026-06-15: "Unbounded Retry & Backoff Hardening" — v1.3 opens a single phase extending RESIL-01 to remove the 5-attempt cap, raise backoff timing to 1m→5m, add context-cancel-aware sleep, call-duration observability, and unit tests for the retry helper.

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |
| 260611-ott | Document crawler logic flow for cross-language reimplementation in fable_logic_flow.md | 2026-06-11 | b105f84 | [260611-ott-document-crawler-logic-flow-for-cross-la](./quick/260611-ott-document-crawler-logic-flow-for-cross-la/) |

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md`. v1.3 has one phase (Phase 10) covering all 8 requirements (RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, OBS-01, TEST-01). All work targets `cmd/crawler/main.go`. Run `/gsd-plan-phase 10` to produce the execution plan.

## Decisions

- [Phase 06-01]: Floor for filterCap halving fixed at 10 (D-05); minCap=10 hardcoded in WithNoticeHandler closures; not config-driven
- [Phase 06-CR]: filterCap uses atomic.Int32 (not plain int) — concurrent access from NOTICE handler goroutine required this
- [Phase 07-02]: Per-class counters use named atomic.Int32 fields (failTransport/failFilterRej/failSubFlap) not an array, matching Phase 6 filterCap pattern
- [Phase 07-02]: markRelayDead is single log-line owner; FetchAndUpdateFollows callers must not emit WARN before calling it (LOG-03/D-15)
- [Phase 07-03]: filterRejectionError dedicated type (not annotated subscriptionError) so errors.As can distinguish it without string heuristics (D-07)
- [Phase 07-03]: markRelayDead removed from queryRelay (per-relay goroutine); structural single-threaded fix for data race CR-02 — no mutex needed

## Operator Next Steps

- Run `/gsd-plan-phase 10` to plan Phase 10
