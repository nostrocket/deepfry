---
gsd_state_version: 1.0
milestone: v1.4
milestone_name: Crawler Hang Fix
status: verifying
last_updated: "2026-06-16T06:43:47.196Z"
last_activity: 2026-06-16 -- Phase 11 execution started
progress:
  total_phases: 1
  completed_phases: 1
  total_plans: 1
  completed_plans: 1
  percent: 100
---

# Project State: Web-of-Trust Crawler — v1.4 Crawler Hang Fix (Relay-Query Liveness)

**Last updated:** 2026-06-16

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 11 — relay-query-liveness

## Current Position

Phase: 11 (relay-query-liveness) — EXECUTING
Plan: 1 of 1
Status: Phase complete — ready for verification
Last activity: 2026-06-16 -- Phase 11 execution started

## Performance Metrics

- Phases complete (v1.4): 0 / 1
- Requirements delivered (v1.4): 0 / 4
- Plans complete (v1.4): 0 / TBD

- Phases complete (v1.3): 1 / 1
- Requirements delivered (v1.3): 8 / 8 (RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, OBS-01, TEST-01)
- Plans complete (v1.3): 1 / 1

- Phases complete (v1.2): 5 / 5
- Requirements delivered (v1.2): 21 / 21 (VALID-01/02/03, FILTER-01/02, RELAY-01/02/03, LOG-01/02/03, PERF-01/02, TIMEOUT-01/02, METRIC-01, HARD-01/02/03/04, RESIL-01)
- Plans complete (v1.2): 11 / 11

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| Continue numbering from Phase 10 | v1.3 ended at Phase 10; sequential numbering per config |
| Single phase for all 4 v1.4 requirements | Coarse granularity + inseparable coupling: HANG-01 (dispatcher exit) and HANG-02 (bound Subscribe) neither produces a useful intermediate state alone; HANG-03 targets the same subsystem; TEST-02 validates all three. No natural delivery boundary splits them. |
| Phase 11 = dispatcher fix + queryRelay bound + websocket hardening + regression test | The regression test (TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks) is the explicit acceptance gate for the entire milestone; it validates HANG-01+HANG-02 together. HANG-03 ships in the same pass as hardening. |
| Dispatcher abandons stuck goroutines (HANG-01) rather than waiting | Fix #1 from HANG-FINDINGS.md: return on relayQueryContext done + drain buffered events; stuck goroutines bounded but may leak until reconnect / relay eject. Acceptable per HANG-FINDINGS analysis. |
| queryRelay wraps relay.Subscribe in child goroutine with ctx-select (HANG-02) | Fix #2: reduces goroutine leak frequency; goroutine still leaks but no longer blocks the batch. Combined with #1 so leaks are bounded. |
| Websocket write deadline / keepalive hardening (HANG-03) | Fix #3: makes half-open connections die, cancelling connectionContext, unblocking Fire() — reduces leak frequency. Does not replace #1+#2 but shortens the window. |
| Phase 11-relay-query-liveness P01 | 25 | 3 tasks | 2 files |

### Important Facts

- Root cause confirmed via SIGQUIT goroutine dump: two query goroutines wedged 48 minutes in go-nostr `subscription.go:187` (bare channel receive on write queue, no ctx select).
- Affected files: `pkg/crawler/crawler.go` (FetchAndUpdateFollows dispatcher ~lines 432–642, queryRelay ~line 686+).
- The `queryRelayFn` seam already exists on `Crawler` (WR-05 precedent from Phase 7); the regression test uses it to inject a blocking stub.
- `make test` runs with `-short` flag; no live Dgraph required for these unit tests — pure `pkg/crawler` logic.
- go-nostr v0.52.0's `Subscription.Fire()` (subscription.go:187) ignores caller ctx; only `r.connectionContext` (relay's own lifetime ctx) can unblock it.
- EOSE-quorum early-exit (TIMEOUT-02) is orthogonal; do not modify quorum logic unless the liveness fix requires it.
- Relay health / auto-ejection thresholds (RELAY-01/02/03) are out of scope; ejection already works.

### Todos

- [ ] Plan Phase 11 (`/gsd-plan-phase 11`)

### Roadmap Evolution

- Phase 11 added 2026-06-16: "Relay-Query Liveness" — v1.4 opens a single phase fixing the 48-minute hang: dispatcher returns on relay-query timeout (HANG-01), queryRelay bounded against context-ignoring Fire() (HANG-02), websocket write deadline / keepalive hardening (HANG-03), regression test gate (TEST-02).

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |
| 260611-ott | Document crawler logic flow for cross-language reimplementation in fable_logic_flow.md | 2026-06-11 | b105f84 | [260611-ott-document-crawler-logic-flow-for-cross-la](./quick/260611-ott-document-crawler-logic-flow-for-cross-la/) |

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md`. v1.4 has one phase (Phase 11) covering all 4 requirements (HANG-01, HANG-02, HANG-03, TEST-02). All work targets `pkg/crawler/crawler.go`. The acceptance gate is `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` passing GREEN under `make test`. Run `/gsd-plan-phase 11` to produce the execution plan.

## Decisions

- [Phase 06-01]: Floor for filterCap halving fixed at 10 (D-05); minCap=10 hardcoded in WithNoticeHandler closures; not config-driven
- [Phase 06-CR]: filterCap uses atomic.Int32 (not plain int) — concurrent access from NOTICE handler goroutine required this
- [Phase 07-02]: Per-class counters use named atomic.Int32 fields (failTransport/failFilterRej/failSubFlap) not an array, matching Phase 6 filterCap pattern
- [Phase 07-02]: markRelayDead is single log-line owner; FetchAndUpdateFollows callers must not emit WARN before calling it (LOG-03/D-15)
- [Phase 07-03]: filterRejectionError dedicated type (not annotated subscriptionError) so errors.As can distinguish it without string heuristics (D-07)
- [Phase 07-03]: markRelayDead removed from queryRelay (per-relay goroutine); structural single-threaded fix for data race CR-02 — no mutex needed
- [Phase 10-01]: Generic retryDgraph[T] helper with injected sleep fn; ResourceExhausted reclassified fatal to prevent indefinite-retry livelock on ~4MB gRPC limit
- [Phase ?]: Wrap relay.Subscribe in child goroutine with buffered result channel; select on result vs ctx.Done() to bound context-ignoring Fire() (HANG-02)
- [Phase ?]: completedThisBatch atomic.Bool on relayState resets at batch start, set by goroutines on both success/error paths, read by dispatcher to identify outstanding relays (HANG-03)
- [Phase ?]: relayQueryDoneCh case now independent exit: non-blocking eventsChan drain then return, no wg.Wait dependency (HANG-01)
- [Phase ?]: mark-dead only on context.DeadlineExceeded not context.Canceled — EOSE-quorum early exit is normal operation, not a fault

## Operator Next Steps

- Run `/gsd-plan-phase 11` to produce the Phase 11 execution plan
