# Roadmap: Web-of-Trust Crawler

## Milestones

- ✅ **v1.1 Write-Path Correctness** — Phases 1–4 (shipped)
- ✅ **v1.2 Crawler Reliability & Efficiency** — Phases 5–9 (shipped 2026-06-15)
- ✅ **v1.3 Unbounded Dgraph Retry Resilience** — Phase 10 (shipped 2026-06-15)
- **v1.4 Crawler Hang Fix (Relay-Query Liveness)** — Phase 11 (active)

## Phases

<details>
<summary>✅ v1.3 Unbounded Dgraph Retry Resilience (Phase 10) — SHIPPED 2026-06-15</summary>

Full detail archived in [`milestones/v1.3-ROADMAP.md`](./milestones/v1.3-ROADMAP.md) · requirements in [`milestones/v1.3-REQUIREMENTS.md`](./milestones/v1.3-REQUIREMENTS.md) · audit in [`milestones/v1.3-MILESTONE-AUDIT.md`](./milestones/v1.3-MILESTONE-AUDIT.md).

- [x] Phase 10: Unbounded Retry & Backoff Hardening (1/1 plan) — completed 2026-06-15

8/8 requirements delivered (RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, OBS-01, TEST-01). Replaced four bounded 5-attempt Dgraph retry blocks with a single generic `retryDgraph[T]` helper: indefinite transient-error retry, 1m→2m→4m→5m capped backoff, ctx-cancel-aware sleep, and per-call-type cumulative-average observability.

</details>

<details>
<summary>✅ v1.2 Crawler Reliability & Efficiency (Phases 5–9) — SHIPPED 2026-06-15</summary>

Full detail archived in [`milestones/v1.2-ROADMAP.md`](./milestones/v1.2-ROADMAP.md) · requirements in [`milestones/v1.2-REQUIREMENTS.md`](./milestones/v1.2-REQUIREMENTS.md).

- [x] Phase 5: Pubkey Validation Hardening (2/2 plans) — completed 2026-06-10
- [x] Phase 6: Filter Size & Per-Relay Cap Detection (2/2 plans) — completed 2026-06-11
- [x] Phase 7: Relay Health Management (3/3 plans) — completed 2026-06-13
- [x] Phase 8: Frontier Prioritization, Timeout & Observability (2/2 plans) — completed 2026-06-13
- [x] Phase 9: Phase 8 Hardening & Resilience Follow-ups (2/2 plans) — completed 2026-06-15

21/21 requirements delivered (VALID, FILTER, RELAY, LOG, PERF, TIMEOUT, METRIC, HARD, RESIL).

</details>

<details>
<summary>✅ v1.1 Write-Path Correctness (Phases 1–4) — SHIPPED</summary>

- [x] Phases 1–4 — write-path integrity, chunked-write correctness, regression coverage (CHUNK-01/02, LEAK-01, TEST-03/04). SEC-01/02 deferred as a future idea.

</details>

### v1.4 Crawler Hang Fix (Relay-Query Liveness)

- [x] **Phase 11: Relay-Query Liveness** - Fix dispatcher to return on relay-query timeout; bound queryRelay against context-ignoring go-nostr Fire(); close+mark-dead stuck relays on timeout; pass regression test. (completed 2026-06-16)

## Phase Details

### Phase 11: Relay-Query Liveness

**Goal**: A stuck or half-open relay can never wedge FetchAndUpdateFollows — the dispatcher always returns within a bounded multiple of its relay-query timeout.
**Depends on**: Phase 10
**Requirements**: HANG-01, HANG-02, HANG-03, TEST-02
**Success Criteria** (what must be TRUE):

  1. `FetchAndUpdateFollows` returns within a small bounded multiple of `c.timeout` even when every per-relay query goroutine blocks indefinitely and ignores its context.
  2. `queryRelay` returns when the relay-query context expires regardless of whether the underlying `relay.Subscribe` / go-nostr `Fire()` respects that context.
  3. A half-open relay connection cannot park its write-loop goroutine indefinitely — on relay-query timeout the dispatcher closes the stuck relay's connection (cancelling its connectionContext) and marks it dead, bounding how long any single relay can stay wedged.
  4. `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` (pkg/crawler/crawler_hang_test.go) passes GREEN, and `make test` (run from the web-of-trust module directory) is fully green with no failures or skips in the unit suite.

**Plans**: 1 plan

- [x] 11-01-PLAN.md — Bound Subscribe (HANG-02) + dispatcher timeout-exit & close-mark-dead stuck relays (HANG-01/HANG-03) + regression/partial/close tests (TEST-02)

## Progress

| Phase | Milestone | Plans | Status | Completed |
|-------|-----------|-------|--------|-----------|
| 5. Pubkey Validation Hardening | v1.2 | 2/2 | Complete | 2026-06-10 |
| 6. Filter Size & Per-Relay Cap Detection | v1.2 | 2/2 | Complete | 2026-06-11 |
| 7. Relay Health Management | v1.2 | 3/3 | Complete | 2026-06-13 |
| 8. Frontier Prioritization, Timeout & Observability | v1.2 | 2/2 | Complete | 2026-06-13 |
| 9. Phase 8 Hardening & Resilience Follow-ups | v1.2 | 2/2 | Complete | 2026-06-15 |
| 10. Unbounded Retry & Backoff Hardening | v1.3 | 1/1 | Complete | 2026-06-15 |
| 11. Relay-Query Liveness | v1.4 | 1/1 | Complete   | 2026-06-16 |
