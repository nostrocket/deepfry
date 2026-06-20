---
gsd_state_version: 1.0
milestone: v1.6
milestone_name: Crawl Throughput Optimization
current_phase: 14
current_phase_name: frontier-read-path-throughput-follower-count
status: awaiting-verification
stopped_at: Phase 14 executed + code-reviewed; awaiting operator live-verify (TEST-03)
last_updated: "2026-06-20T00:00:00.000Z"
last_activity: 2026-06-20
last_activity_desc: Phase 14 code complete (5/6 tasks), reviewed + CR-01 fixed; live-verify checkpoint pending
progress:
  total_phases: 2
  completed_phases: 1
  total_plans: 2
  completed_plans: 1
  percent: 50
---

# Project State: Web-of-Trust Crawler — v1.6 Crawl Throughput Optimization

**Last updated:** 2026-06-18

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 14 — Frontier Read-Path Throughput (`follower_count`)

## Current Position

Phase: 14 (Frontier Read-Path Throughput — `follower_count`) executed; awaiting live verification
Plan: 14-01 — code complete (tasks 1–5), reviewed, CR-01 fixed
Status: Phase 14 code shipped to `main` (commits 2bab80d → fa1c743). TEST-03 live-verify checkpoint PENDING — operator must run `backfill-follower-count` + before/after `GetStalePubkeys` latency on the strfry host. Milestone v1.6 intentionally left OPEN.
Last activity: 2026-06-20 — Phase 14 executed + code-reviewed (CR-01 invalid backfill DQL fixed); live-verify pending

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
| v1.6 starts with loop overhead before Dgraph write concurrency | Codebase-memory analysis and the speed spike show lower-risk wins in frontier-batch decoupling and count-query throttling before changing `AddFollowers` semantics. |
| Phase 13 P13-01 | 34 min | 3 tasks | 9 files |
| Phase 13 throughput controls verified | Frontier selection is now independent from relay filter chunk caps; count sampling and exact metrics are implemented. Use WOT_ROUND baseline/optimized evidence before deciding whether Phase 14 Dgraph write-path work is still justified. |

### Important Facts

- Production failure that opened v1.5: `failed to add follows: query follower failed: rpc error: code = DeadlineExceeded desc = context deadline exceeded` for pubkey `314072c16fa9433e1374f62e5b02c8163946ed298a9cde3b1541513c29d19fff`.
- Batch context: queried 1000 pubkeys, 567 had events, `batch_ms=61166`, `fetch_ms=13773`, `overhead_ms=47393`; Dgraph/bookkeeping dominated the batch after relay fetch returned.
- v1.6 likely affected code: `cmd/crawler.main` for frontier selection/count throttling/metrics, `pkg/config` for new runtime knobs, `pkg/crawler.queryRelay` only as a relay-filter-safety boundary, and `pkg/dgraph.AddFollowers` only if Phase 13 measurements still show write-path dominance.
- The fix must preserve Dgraph as an ID-only graph store; no event payloads outside StrFry.
- Live config remains `~/deepfry/web-of-trust.yaml`; never edit it in tests.
- `make test` runs short tests without live Dgraph. Add live-Dgraph integration coverage only behind the existing integration-test pattern.

### Todos

- [x] Plan Phase 13 (`/gsd-plan-phase 13`)
- [x] Execute Phase 13 (`/gsd-execute-phase 13`)
- [x] Decide Phase 14 scope from metrics — redefined from write-path (DWRITE) to read-path `follower_count` (DSCALE-01/03); write-path closed as not-dominant.
- [x] Plan + execute Phase 14 (code: schema predicate, GetStalePubkeys rewrite, AddFollowers delta maintenance, backfill CLI, tests). Reviewed; CR-01 (invalid backfill DQL) fixed.
- [ ] **TEST-03 operator live-verify** on the strfry host: run `make build-backfill-follower-count`, `./bin/backfill-follower-count` (idempotent seed of ~1.38M nodes — MUST complete before trusting `follower_count` ordering), then before/after `GetStalePubkeys` latency + stored-vs-recomputed spot-check. Record numbers, then close Phase 14 / v1.6.
- [ ] Operator config tuning (from the same analysis, independent of Phase 14): bump `count_sample_interval` (1→~20) and `frontier_batch_size` (100→~1000) in `~/deepfry/web-of-trust.yaml` — mechanisms already shipped in Phase 13.

### Roadmap Evolution

- Phase 11 added 2026-06-16: "Relay-Query Liveness" — v1.4 opens a single phase fixing the 48-minute hang: dispatcher returns on relay-query timeout (HANG-01), queryRelay bounded against context-ignoring Fire() (HANG-02), websocket write deadline / keepalive hardening (HANG-03), regression test gate (TEST-02).
- Phase 12 completed 2026-06-18: "Dgraph Follow-Update Resilience" — v1.5 delivered transient timeout handling, bounded large-list writes, partial-progress safety, observability, and regression coverage.
- Phase 13 planned 2026-06-18: "Main-Loop Throughput Controls" — v1.6 starts with frontier-batch decoupling, count-query throttling, metrics updates, and relay filter safety tests.
- Phase 14 planned 2026-06-18: "Dgraph Write-Path Throughput Decision" — only optimize `AddFollowers` further if measured Phase 13 runs still show Dgraph write overhead dominating.

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |
| 260611-ott | Document crawler logic flow for cross-language reimplementation in fable_logic_flow.md | 2026-06-11 | b105f84 | [260611-ott-document-crawler-logic-flow-for-cross-la](./quick/260611-ott-document-crawler-logic-flow-for-cross-la/) |
| 260617-doc | Refresh README + STATE for resumability (config, clusterscan, metrics, optimization backlog) | 2026-06-17 | 9084927 | — |

## Session Continuity

**Last session:** 2026-06-20
**Stopped at:** Phase 14 executed + code-reviewed; awaiting operator live-verify (TEST-03)
**Resume file:** `.planning/phases/14-frontier-read-path-throughput-follower-count/14-01-SUMMARY.md` (operator procedure)

**To resume:** Phase 14 code is shipped to `main` (commits 2bab80d → fa1c743) and passed code review (CR-01 invalid backfill DQL fixed). Run the TEST-03 live verification on the strfry host per 14-01-SUMMARY.md — backfill the follower_count predicate, then measure before/after `GetStalePubkeys` latency. Once verified, close Phase 14 and complete milestone v1.6 (`/gsd-verify-work 14` then `/gsd-complete-milestone v1.6`). Milestone v1.6 is intentionally OPEN until live verification passes.

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

- Run the Phase 13 baseline/optimized `WOT_ROUND` comparison from `13-01-PLAN.md`
- Decide whether Phase 14 is still needed from the measured overhead profile
