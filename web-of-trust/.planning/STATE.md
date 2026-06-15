---
gsd_state_version: 1.0
milestone: v1.3
milestone_name: Unbounded Dgraph Retry Resilience
status: planning
last_updated: "2026-06-15T04:01:06.694Z"
last_activity: 2026-06-15
progress:
  total_phases: 0
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State: Web-of-Trust Crawler — v1.2 Crawler Reliability & Efficiency

**Last updated:** 2026-06-11

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 09 — phase-8-hardening-resilience-follow-ups

## Current Position

Phase: Not started (defining requirements)
Plan: —
Status: Defining requirements
Last activity: 2026-06-15 — Milestone v1.3 started

## Performance Metrics

- Phases complete (v1.2): 5 / 5
- Requirements delivered (v1.2): 21 / 21 (VALID-01/02/03, FILTER-01/02, RELAY-01/02/03, LOG-01/02/03, PERF-01/02, TIMEOUT-01/02, METRIC-01, HARD-01/02/03/04, RESIL-01)
- Plans complete (v1.2): 11 / 11

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| Continue numbering from Phase 5 | v1.1 ended at Phase 4; sequential numbering per config |
| 4-phase coarse structure | Natural coupling clusters: VALID (3), FILTER (2), RELAY (2), PERF+TIMEOUT+METRIC (5); coarse granularity compresses into minimum viable boundaries |
| Phase 5 = pubkey validation hardening | VALID-01/02/03 are tightly coupled — fixing the validator (01), purging existing garbage (02), and stamping invalid nodes via UID (03) must ship together or the bug re-enters on every batch |
| Phase 6 = filter size + per-relay cap | FILTER-01 reduces the default; FILTER-02 adds intelligence to detect per-relay limits; must ship together since FILTER-02's filter-rejection class informs Phase 7 |
| Phase 7 = relay health management | Depends on Phase 6 because FILTER-02 introduces the filter-rejection failure class that RELAY-02 needs to classify; RELAY-01/02 are coupled (persistence + classification) |
| Phase 8 = frontier + timeout + observability | PERF-01/02 touch GetStalePubkeys/MarkAttempted; TIMEOUT-01/02 and METRIC-01 all touch cmd/crawler or pkg/crawler event loop; all independent of relay health, can run in parallel with Phase 7 but grouped for coarse granularity; depends on Phase 5 for valid MarkAttempted UID stamps |
| Phase 05-pubkey-validation-hardening P01 | 1200 | 3 tasks | 3 files |
| Phase 06-filter-size-per-relay-cap-detection P01 | 117 | 2 tasks | 3 files |
| Phase 06 P02 | 89 | 2 tasks | 2 files |
| Phase 06 CR-01 fix | filterCap changed to atomic.Int32 after code review identified data race; CAS loop added to handleFilterNotice; filterCap reset on reconnect added (WR-03) |
| RELAY-03 added (2026-06-12) | Phase 6's filterCap reset-on-reconnect (WR-03) makes caps re-learned every batch: 50→25→12→10 cascade repeats, floor-capped relays re-marked dead forever, logs flooded. RELAY-03 persists caps across reconnects with probe-up/decay recovery |
| LOG-01/02/03 added (2026-06-12) | Production logs dominated by per-relay noise (~100 reconnect lines/sweep, 6-line cap cascades, duplicate dead/timeout pairs). Folded into Phase 7 since all touch the relay state machine Phase 7 rewrites |
| Phase 07-relay-health-management P01 | 2 | 2 tasks | 2 files |
| Phase 07-relay-health-management P02 | 35 | 3 tasks | 3 files |
| Phase 07-relay-health-management P03 (gap closure) | 7 | 2 tasks | 2 files |

### Important Facts

- v1.2 motivated by 40-batch production run: 172 relays, 38s avg batch time, 789 pubkeys/min, 0.76% event hit rate, 43 new nodes added.
- VALID root cause: `nostr.GetPublicKey` is a private-key→public-key derivation function, not a validator; used as a validator it silently accepts garbage pubkeys (uppercase hex, relay URL blobs, truncated values). 19 garbage nodes already in DB re-enter every batch.
- FILTER root cause: batchSize 500 exceeds relay limits; 40% of relays reject or crash on every batch REQ.
- PERF root cause: stale frontier ordered by age only, yielding 99.24% wasted cycles (stubs with 0 followers queried as eagerly as high-follower accounts).
- RELAY root cause: failure counter reset to 0 on reconnect; flapping relays never reach ejection threshold.
- TIMEOUT: 44% of relays exceed 30s EOSE timeout; EOSE-quorum early exit can reclaim that time.
- METRIC: staleRemaining always 0 due to off-by-one in cmd/crawler/main.go metric formula.
- Live config at `~/deepfry/web-of-trust.yaml` must not be edited for testing; use a temp `HOME`.
- Integration tests gate on live Dgraph via `//go:build integration` / `make test-integration`.
- Phase 06 used atomic.Int32 (not plain int) for filterCap after CR-01 review finding; go test -race passes.

### Todos

- [x] Phase 9 planned, executed, and verified (live-approved 2026-06-15)

### Roadmap Evolution

- Phase 9 added (2026-06-13): "Phase 8 Hardening & Resilience Follow-ups" — opened at v1.2 milestone-close gate to resolve the two deferred follow-up todos (08-REVIEW.md WR-02/03/04/05 + transient-Dgraph-error retry) rather than carry them as tech debt. Phase 08 was already Complete/verified, so a follow-up phase was chosen over a `--force` replan. New requirements: HARD-01..04, RESIL-01. v1.2 grows from 4 → 5 phases (16 → 21 requirements).

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |
| 260611-ott | Document crawler logic flow for cross-language reimplementation in fable_logic_flow.md | 2026-06-11 | b105f84 | [260611-ott-document-crawler-logic-flow-for-cross-la](./quick/260611-ott-document-crawler-logic-flow-for-cross-la/) |

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md` for full context. v1.2 roadmap defines Phases 5–8 covering 12 requirements. Phase 5 and 6 are complete. Phase 7 (relay health management) is next — it depends on Phase 6's filter-rejection failure class for RELAY-02 failure classification.

## Decisions

- [Phase ?]: Replace nostr.GetPublicKey (key-derivation) with dgraph.ValidatePubkey (hex-regex) at both crawler call sites
- [Phase ?]: Inline recover-or-purge in MarkAttempted subsumes VALID-02 — no separate startup purge step needed; garbage nodes self-clean on first encounter
- [Phase 06-01]: Floor for filterCap halving fixed at 10 (D-05); minCap=10 hardcoded in WithNoticeHandler closures; not config-driven
- [Phase 06-01]: rs created before nostr.RelayConnect in New() to allow safe closure capture over pointer (avoids loop-variable bug)
- [Phase 06-02]: rs.conn and rs.url used inside function; single call site updated
- [Phase 06-02]: Caller manages sub.Unsub() per chunk — drainSubscription does not defer it, keeping per-chunk lifecycle explicit
- [Phase 06-02]: Uses time.Since(subscribeStart) on Subscribe error return; no goroutine needed
- [Phase 06-CR]: filterCap uses atomic.Int32 (not plain int) — concurrent access from NOTICE handler goroutine required this
- [Phase 07-02]: Per-class counters use named atomic.Int32 fields (failTransport/failFilterRej/failSubFlap) not an array, matching Phase 6 filterCap pattern
- [Phase 07-02]: markRelayDead is single log-line owner; FetchAndUpdateFollows callers must not emit WARN before calling it (LOG-03/D-15)
- [Phase 07-02]: Probe-up flag probing atomic.Bool force-cleared via defer on all queryRelay exits including context cancel (Pitfall 3)
- [Phase 07-02]: Forward relay confirmed exempt from markRelayDead routing (Pitfall 6) — self-contained failure path in ReconnectRelays
- [Phase 07-03]: filterRejectionError dedicated type (not annotated subscriptionError) so errors.As can distinguish it without string heuristics (D-07)
- [Phase 07-03]: markRelayDead removed from queryRelay (per-relay goroutine); structural single-threaded fix for data race CR-02 — no mutex needed
- [Phase 07-03]: New() startup connect failure: relay kept in pool alive=false, failTransport++, no OnConnectFail call — T-07-DOS mitigation
- [Phase 07-03]: handleCapRejection extracted as testable seam for at-cap/floor rejection path (WR-05: real-seam tests, not inline copies)

## Operator Next Steps

- Start the next milestone with /gsd-new-milestone
