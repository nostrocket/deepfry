---
gsd_state_version: 1.0
milestone: v1.5
milestone_name: Dgraph Follow-Update Timeout Resilience
current_phase: null
status: Awaiting next milestone
last_updated: "2026-06-18T12:24:57.844Z"
last_activity: 2026-06-18
last_activity_desc: Milestone v1.5 completed and archived
progress:
  total_phases: 1
  completed_phases: 1
  total_plans: 1
  completed_plans: 1
  percent: 100
---

# Project State: Web-of-Trust Crawler — v1.5 Dgraph Follow-Update Timeout Resilience

**Last updated:** 2026-06-18

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Planning next milestone

## Current Position

Phase: Milestone v1.5 complete
Plan: —
Status: Awaiting next milestone
Last activity: 2026-06-18 — Milestone v1.5 completed and archived

## Performance Metrics

- Phases complete (v1.5): 1 / 1
- Requirements delivered (v1.5): 6 / 6 (DWRITE-01/02/03/04, OBS-02, TEST-06)
- Plans complete (v1.5): 1 / 1

- Phases complete (v1.4): 1 / 1
- Requirements delivered (v1.4): 4 / 4 (HANG-01/02/03, TEST-02)
- Plans complete (v1.4): 1 / 1

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
| v1.5 scoped to Dgraph follow-update timeout resilience | The 2026-06-18 production failure is a write-path `DeadlineExceeded` after relay fetch succeeded; fix the abort condition before broader throughput tuning. |
| Phase 12 = single Dgraph write-path resilience phase | Requirements all touch `AddFollowers` / Dgraph write classification / observability / tests, with no useful intermediate user-visible slice. |
| Phase 12 AddFollowers keeps one transaction with bounded child contexts | Preserves kind-3 all-or-nothing replacement while bounding each Dgraph query/mutation/commit window for diagnostics and timeout behavior. |
| Phase 12 transient AddFollowers failures use FetchResult.SkipAttempt | A transient per-pubkey write failure no longer aborts the batch and is omitted from MarkAttempted so it remains retry-eligible. |
| Phase 12 ResourceExhausted remains fatal through dgraph.IsTransientError | Keeps the Phase 10 anti-livelock decision while sharing classifier logic between Dgraph follow writes and main-loop retry code. |

### Important Facts

- Production failure that opened v1.5: `failed to add follows: query follower failed: rpc error: code = DeadlineExceeded desc = context deadline exceeded` for pubkey `314072c16fa9433e1374f62e5b02c8163946ed298a9cde3b1541513c29d19fff`.
- Batch context: queried 1000 pubkeys, 567 had events, `batch_ms=61166`, `fetch_ms=13773`, `overhead_ms=47393`; Dgraph/bookkeeping dominated the batch after relay fetch returned.
- Likely affected code: Dgraph follow update path (`pkg/dgraph` `AddFollowers` and caller path from `pkg/crawler` / `cmd/crawler`), especially follower lookup and edge mutation behavior for large contact lists.
- The fix must preserve Dgraph as an ID-only graph store; no event payloads outside StrFry.
- Live config remains `~/deepfry/web-of-trust.yaml`; never edit it in tests.
- `make test` runs short tests without live Dgraph. Add live-Dgraph integration coverage only behind the existing integration-test pattern.

### Todos

- [x] Plan Phase 12 (`/gsd-plan-phase 12`)
- [x] Execute Phase 12 (`/gsd-execute-phase 12`)

### Roadmap Evolution

- Phase 11 added 2026-06-16: "Relay-Query Liveness" — v1.4 opens a single phase fixing the 48-minute hang: dispatcher returns on relay-query timeout (HANG-01), queryRelay bounded against context-ignoring Fire() (HANG-02), websocket write deadline / keepalive hardening (HANG-03), regression test gate (TEST-02).
- Phase 12 completed 2026-06-18: "Dgraph Follow-Update Resilience" — v1.5 delivered transient timeout handling, bounded large-list writes, partial-progress safety, observability, and regression coverage.

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |
| 260611-ott | Document crawler logic flow for cross-language reimplementation in fable_logic_flow.md | 2026-06-11 | b105f84 | [260611-ott-document-crawler-logic-flow-for-cross-la](./quick/260611-ott-document-crawler-logic-flow-for-cross-la/) |
| 260617-doc | Refresh README + STATE for resumability (config, clusterscan, metrics, optimization backlog) | 2026-06-17 | 9084927 | — |

## Session Continuity

**To resume:** v1.5 Phase 12 is complete and archived. Load `.planning/milestones/v1.5-phases/12-dgraph-follow-update-resilience/12-01-SUMMARY.md` for implementation details, or run `$gsd-new-milestone` to start the next milestone.

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
- [Phase 12-01]: dgraph.IsTransientError is the shared Dgraph/gRPC classifier; DeadlineExceeded/Unavailable are transient, ResourceExhausted is fatal
- [Phase 12-01]: FetchAndUpdateFollows returns FetchResult.Hits plus FetchResult.SkipAttempt; main excludes SkipAttempt from MarkAttempted
- [Phase 12-01]: AddFollowers wraps failures in FollowUpdateError with pubkey/follows/chunk/elapsed/retry_count/outcome while preserving error unwrapping

## Operator Next Steps

- Start the next milestone with /gsd-new-milestone
