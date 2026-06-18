# Roadmap: Web-of-Trust Crawler

## Milestones

- ✅ **v1.1 Write-Path Correctness** — Phases 1–4 (shipped)
- ✅ **v1.2 Crawler Reliability & Efficiency** — Phases 5–9 (shipped 2026-06-15)
- ✅ **v1.3 Unbounded Dgraph Retry Resilience** — Phase 10 (shipped 2026-06-15)
- ✅ **v1.4 Crawler Hang Fix (Relay-Query Liveness)** — Phase 11 (shipped 2026-06-16)
- ✅ **v1.5 Dgraph Follow-Update Timeout Resilience** — Phase 12 (shipped 2026-06-18)

## Phases

<details open>
<summary>✅ v1.5 Dgraph Follow-Update Timeout Resilience (Phase 12) — SHIPPED 2026-06-18</summary>

Requirements in [`REQUIREMENTS.md`](./REQUIREMENTS.md).

- [x] Phase 12: Dgraph Follow-Update Resilience (1/1 plan) — completed 2026-06-18

Goal: A slow or oversized Dgraph follow-update must not abort the crawler batch or stop crawl progress.

Requirements covered: DWRITE-01, DWRITE-02, DWRITE-03, DWRITE-04, OBS-02, TEST-06.

Success criteria:
1. A simulated or real Dgraph `DeadlineExceeded` from the follow-update path is handled as a transient pubkey/update failure, not a crawler-process abort.
2. Large follow lists are written in bounded units with clear partial-progress behavior and no duplicate/corrupt follow-edge state.
3. Fatal Dgraph write errors still surface loudly and are not retried indefinitely under the transient path.
4. Production logs show enough timing and chunk/pubkey context to diagnose slow `AddFollowers` operations.
5. `make test` passes with regression coverage for timeout classification, partial progress, retry scheduling, and fatal passthrough.

</details>

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

<details>
<summary>✅ v1.4 Crawler Hang Fix (Relay-Query Liveness) (Phase 11) — SHIPPED 2026-06-16</summary>

Full detail archived in [`milestones/v1.4-ROADMAP.md`](./milestones/v1.4-ROADMAP.md) · requirements in [`milestones/v1.4-REQUIREMENTS.md`](./milestones/v1.4-REQUIREMENTS.md) · audit in [`milestones/v1.4-MILESTONE-AUDIT.md`](./milestones/v1.4-MILESTONE-AUDIT.md).

- [x] Phase 11: Relay-Query Liveness (1/1 plan) — completed 2026-06-16

4/4 requirements delivered (HANG-01/02/03, TEST-02). Eliminated the 48-minute crawler hang: `FetchAndUpdateFollows` now returns on its own relay-query timeout regardless of a context-ignoring go-nostr `Subscribe`/`Fire()`; the dispatcher bounds `Subscribe` in a child goroutine, exits independently of `wg.Wait()`, and closes + marks-dead (timeout) or closes (quorum-cancel) outstanding relays to reap leaked goroutines. An adversarial code-review loop fixed 2 critical concurrency defects found in the first cut.

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
| 11. Relay-Query Liveness | v1.4 | 1/1 | Complete    | 2026-06-16 |
| 12. Dgraph Follow-Update Resilience | v1.5 | 1/1 | Complete | 2026-06-18 |
