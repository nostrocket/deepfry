# Roadmap: Web-of-Trust Crawler

## Milestones

- ✅ **v1.1 Write-Path Correctness** — Phases 1-4 (shipped)
- ✅ **v1.2 Crawler Reliability & Efficiency** — Phases 5-9 (shipped 2026-06-15)
- ✅ **v1.3 Unbounded Dgraph Retry Resilience** — Phase 10 (shipped 2026-06-15)
- ✅ **v1.4 Crawler Hang Fix (Relay-Query Liveness)** — Phase 11 (shipped 2026-06-16)
- ✅ **v1.5 Dgraph Follow-Update Timeout Resilience** — Phase 12 (shipped 2026-06-18)
- ◆ **v1.6 Crawl Throughput Optimization** — Phases 13-14 (planning)

## Phases

<details open>
<summary>◆ v1.6 Crawl Throughput Optimization (Phases 13-14) — PLANNING</summary>

**Goal:** Make the crawler expand the web of trust faster by reducing avoidable per-batch Dgraph/bookkeeping overhead and safely increasing useful work per loop.

- [x] Phase 13: Main-Loop Throughput Controls (1 plan) — decouple frontier batch size from relay filter cap, throttle count queries, update metrics/run records, and prove relay filter safety remains intact. (completed 2026-06-18)
- [x] Phase 14: Frontier Read-Path Throughput (`follower_count`) (1 plan) — eliminate the per-batch full-frontier `count(~follows)` sort in `GetStalePubkeys`. Live-verified: ~119s → ~1.3s (frontier via `eq(uncrawled,1)`; aged via `ge(follower_count,0)`). Crawler redeploy pending. (verified 2026-06-20)

16/16 requirements mapped (LOOP-01/02/03/04, COUNT-01/02/03, MEASURE-01/02/03, DSCALE-01/03, TEST-01/02/03).

### Phase 13: Main-Loop Throughput Controls

**Goal:** Increase useful crawl work per main loop by decoupling frontier batch size from the relay filter cap and throttling count queries, without weakening relay filter safety.
**Status:** Completed 2026-06-18
**Plans:** 1 plan

### Phase 14: Frontier Read-Path Throughput (`follower_count`)

**Goal:** Eliminate the dominant per-batch read-path overhead — `GetStalePubkeys` recomputes `count(~follows)` over the entire never-attempted frontier (~1.3M nodes) on every call to order the frontier, measured at ~39s/batch — by maintaining a stored, indexed `follower_count` predicate so frontier ordering reads a value instead of recomputing the aggregate. The predicate must stay correct as follow edges change (`AddFollowers`).
**Depends on:** Phase 13 main-loop throughput controls. Premise confirmed by production metrics: `GetStalePubkeys` ≈ 39s/batch dominates; write-path `MarkAttempted` ≈ 0.07s, so the write path (`AddFollowers`) does NOT dominate and is out of scope for this phase.
**Plans:** 1 plan

Plans:
- [x] 14-01-PLAN.md — schema `follower_count` + `uncrawled` predicates, GetStalePubkeys index-entry rewrite (frontier `eq(uncrawled,1)`, aged `ge(follower_count,0)`), AddFollowers delta + marker maintenance, uid-cursor backfill CLI, tests (DSCALE-01, DSCALE-03, TEST-03) — live-verified 2026-06-20

</details>

<details>
<summary>✅ v1.5 Dgraph Follow-Update Timeout Resilience (Phase 12) — SHIPPED 2026-06-18</summary>

Full detail archived in [`milestones/v1.5-ROADMAP.md`](./milestones/v1.5-ROADMAP.md) · requirements in [`milestones/v1.5-REQUIREMENTS.md`](./milestones/v1.5-REQUIREMENTS.md) · phase artifacts in [`milestones/v1.5-phases/`](./milestones/v1.5-phases/).

- [x] Phase 12: Dgraph Follow-Update Resilience (1/1 plan) — completed 2026-06-18

6/6 requirements delivered (DWRITE-01/02/03/04, OBS-02, TEST-06). Dgraph follow updates now fail transiently per pubkey, preserve atomic kind-3 graph writes, and leave skipped pubkeys retry-eligible.

</details>

<details>
<summary>✅ v1.4 Crawler Hang Fix (Relay-Query Liveness) (Phase 11) — SHIPPED 2026-06-16</summary>

Full detail archived in [`milestones/v1.4-ROADMAP.md`](./milestones/v1.4-ROADMAP.md) · requirements in [`milestones/v1.4-REQUIREMENTS.md`](./milestones/v1.4-REQUIREMENTS.md) · audit in [`milestones/v1.4-MILESTONE-AUDIT.md`](./milestones/v1.4-MILESTONE-AUDIT.md) · phase artifacts in [`milestones/v1.4-phases/`](./milestones/v1.4-phases/).

- [x] Phase 11: Relay-Query Liveness (1/1 plan) — completed 2026-06-16

4/4 requirements delivered (HANG-01/02/03, TEST-02). Eliminated the 48-minute crawler hang: `FetchAndUpdateFollows` now returns on its own relay-query timeout regardless of a context-ignoring go-nostr `Subscribe`/`Fire()`.

</details>

<details>
<summary>✅ v1.3 Unbounded Dgraph Retry Resilience (Phase 10) — SHIPPED 2026-06-15</summary>

Full detail archived in [`milestones/v1.3-ROADMAP.md`](./milestones/v1.3-ROADMAP.md) · requirements in [`milestones/v1.3-REQUIREMENTS.md`](./milestones/v1.3-REQUIREMENTS.md) · audit in [`milestones/v1.3-MILESTONE-AUDIT.md`](./milestones/v1.3-MILESTONE-AUDIT.md) · phase artifacts in [`milestones/v1.3-phases/`](./milestones/v1.3-phases/).

- [x] Phase 10: Unbounded Retry & Backoff Hardening (1/1 plan) — completed 2026-06-15

8/8 requirements delivered (RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, OBS-01, TEST-01). Replaced four bounded 5-attempt Dgraph retry blocks with a single generic indefinite retry helper.

</details>

<details>
<summary>✅ v1.2 Crawler Reliability & Efficiency (Phases 5-9) — SHIPPED 2026-06-15</summary>

Full detail archived in [`milestones/v1.2-ROADMAP.md`](./milestones/v1.2-ROADMAP.md) · requirements in [`milestones/v1.2-REQUIREMENTS.md`](./milestones/v1.2-REQUIREMENTS.md).

- [x] Phase 5: Pubkey Validation Hardening (2/2 plans) — completed 2026-06-10
- [x] Phase 6: Filter Size & Per-Relay Cap Detection (2/2 plans) — completed 2026-06-11
- [x] Phase 7: Relay Health Management (3/3 plans) — completed 2026-06-13
- [x] Phase 8: Frontier Prioritization, Timeout & Observability (2/2 plans) — completed 2026-06-13
- [x] Phase 9: Phase 8 Hardening & Resilience Follow-ups (2/2 plans) — completed 2026-06-15

21/21 requirements delivered (VALID, FILTER, RELAY, LOG, PERF, TIMEOUT, METRIC, HARD, RESIL).

</details>

<details>
<summary>✅ v1.1 Write-Path Correctness (Phases 1-4) — SHIPPED</summary>

- [x] Phases 1-4 — write-path integrity, chunked-write correctness, regression coverage (CHUNK-01/02, LEAK-01, TEST-03/04). SEC-01/02 deferred as a future idea.

</details>

## Progress

| Phase | Milestone | Plans | Status | Completed |
|-------|-----------|-------|--------|-----------|
| 5. Pubkey Validation Hardening | v1.2 | 2/2 | Complete | 2026-06-10 |
| 6. Filter Size & Per-Relay Cap Detection | v1.2 | 2/2 | Complete | 2026-06-11 |
| 7. Relay Health Management | v1.2 | 3/3 | Complete | 2026-06-13 |
| 8. Frontier Prioritization, Timeout & Observability | v1.2 | 2/2 | Complete | 2026-06-13 |
| 9. Phase 8 Hardening & Resilience Follow-ups | v1.2 | 2/2 | Complete | 2026-06-15 |
| 10. Unbounded Retry & Backoff Hardening | v1.3 | 1/1 | Complete | 2026-06-15 |
| 11. Relay-Query Liveness | v1.4 | 1/1 | Complete | 2026-06-16 |
| 12. Dgraph Follow-Update Resilience | v1.5 | 1/1 | Complete | 2026-06-18 |
| 13. Main-Loop Throughput Controls | v1.6 | 1/1 | Complete    | 2026-06-18 |
| 14. Frontier Read-Path Throughput (`follower_count`) | v1.6 | 1/1 | Verified (live) — crawler redeploy pending | 2026-06-20 |
