# Roadmap: Web-of-Trust Crawler

## Milestones

- ✅ **v1.1 Write-Path Correctness** — Phases 1–4 (shipped)
- ✅ **v1.2 Crawler Reliability & Efficiency** — Phases 5–9 (shipped 2026-06-15)
- 🔄 **v1.3 Unbounded Dgraph Retry Resilience** — Phase 10 (active)

## Phases

### v1.3 Unbounded Dgraph Retry Resilience

- [ ] **Phase 10: Unbounded Retry & Backoff Hardening** — Replace bounded 5-attempt Dgraph retry with indefinite transient-error retry (1 min→5 min exponential backoff), context-cancel shutdown, and call-duration observability across all four main-loop Dgraph calls

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

## Phase Details

### Phase 10: Unbounded Retry & Backoff Hardening

**Goal**: The crawler survives any-length Dgraph outage without exiting — retrying transient gRPC errors indefinitely with exponential backoff, shutting down immediately on context cancellation, and surfacing call-duration metrics during normal operation
**Depends on**: Phase 9 (extends RESIL-01's retry skeleton in cmd/crawler/main.go)
**Requirements**: RETRY-01, RETRY-02, RETRY-03, BACKOFF-01, BACKOFF-02, SHUTDOWN-01, OBS-01, TEST-01
**Success Criteria** (what must be TRUE):

  1. Crawler survives a multi-minute Dgraph outage and resumes crawling automatically once Dgraph returns, without operator intervention or process restart
  2. During a sustained outage, retry log lines show waits of 1 min, 2 min, 4 min, then 5 min (capped) — the sequence is observable in the console
  3. A fatal non-transient Dgraph error (e.g. `codes.Unauthenticated`) still exits the crawler immediately with a logged error, unchanged from v1.2 behavior
  4. Pressing Ctrl-C (or sending SIGTERM) while the crawler is mid-backoff causes clean exit within seconds, not after the full wait interval elapses
  5. Console periodically logs average call duration per Dgraph call type (`GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, `MarkAttempted`) during normal operation**Plans**: 1 plan
- [ ] 10-01-PLAN.md — Extract generic retryDgraph helper (indefinite transient retry, 1m→5m backoff, ctx-cancel-aware wait, cumulative per-call-type duration metrics) + unit tests

## Progress

| Phase | Milestone | Plans | Status | Completed |
|-------|-----------|-------|--------|-----------|
| 5. Pubkey Validation Hardening | v1.2 | 2/2 | Complete | 2026-06-10 |
| 6. Filter Size & Per-Relay Cap Detection | v1.2 | 2/2 | Complete | 2026-06-11 |
| 7. Relay Health Management | v1.2 | 3/3 | Complete | 2026-06-13 |
| 8. Frontier Prioritization, Timeout & Observability | v1.2 | 2/2 | Complete | 2026-06-13 |
| 9. Phase 8 Hardening & Resilience Follow-ups | v1.2 | 2/2 | Complete | 2026-06-15 |
| 10. Unbounded Retry & Backoff Hardening | v1.3 | 0/1 | Not started | - |
