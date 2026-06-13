---
phase: 08-frontier-prioritization-timeout-observability
plan: "02"
subsystem: crawler
tags: [crawler, eose-quorum, early-exit, timeout, atomic, hit-set, staleRemaining, backfill, observability]

dependency_graph:
  requires:
    - phase: 08-01
      provides: [MarkAttempted hit/miss signature + BackoffParams, CountStalePubkeys, BackfillNextAttempt, follower-ordered GetStalePubkeys]
    - phase: 07-relay-health-management
      provides: [relayState per-class counters, markRelayDead single-threaded dispatcher, queryRelay typed errors]
  provides:
    - "Crawler.quorum field + RelayEOSEQuorum on crawler Config"
    - "quorumReached(done, queried int32, q float64) pure helper"
    - "FetchAndUpdateFollows returns (map[string]struct{}, error) hit-set"
    - "EOSE-quorum early cancel() at ceil(quorum*queried) completed relays"
    - "startup BackfillNextAttempt call after EnsureSchema in New()"
    - "honest staleRemaining via CountStalePubkeys in cmd/crawler main loop"
    - "hit-set wired into MarkAttempted (PERF-02 end-to-end)"
  affects: [crawler-runtime, observability, any-future-relay-perf-work]

tech_stack:
  added: []
  patterns:
    - "function-local atomic.Int32 quorum counter per batch (lock-free, Phase 6/7 precedent)"
    - "idempotent cancel() from many goroutines (Go context semantics, T-08-CANCEL accepted)"
    - "pure-helper extraction for unit-testability (quorumReached, WR-05 real-seam convention)"
    - "nil-the-done-channel after first fire to stop select spinning"

key_files:
  created:
    - pkg/crawler/crawler_quorum_test.go
  modified:
    - pkg/crawler/crawler.go
    - cmd/crawler/main.go

decisions:
  - "EOSE-quorum: each relay goroutine calls done.Add(1) exactly once on BOTH success and error paths (D-14 — mid-batch death counts toward quorum); cancel() fires when quorumReached (float64(done) >= ceil(queried*quorum))."
  - "quorumReached returns false when q<=0 or queried==0 (T-08-EARLY guard) so a zero/unset fraction disables early exit and falls back to the 15s hard timeout — never an immediate cancel before any relay responds."
  - "relayQueryDoneCh is nilled after first fire to prevent select-spin; the loop then drains buffered events and exits when eventsChan closes (goroutines finish via wg.Wait())."
  - "Phase 7's markRelayDead single-threaded dispatcher and per-class counters are untouched — the quorum increment is purely additive in the goroutine tail; it never calls markRelayDead."
  - "TIMEOUT-01 required no code change here: c.timeout already drives context.WithTimeout and the 15s default ships from Plan 01's config change; confirmed only."
  - "Startup BackfillNextAttempt error is non-fatal-but-logged — a failed backfill leaves nodes selectable until next attempt rather than aborting crawler startup."
  - "staleRemaining = CountStalePubkeys(ctx) - len(pubkeys), recomputed every batch (D-16/D-17), replacing the always-zero totalStale := len(pubkeys)."

metrics:
  duration: "~6 min active executor time"
  completed: "2026-06-13"
  tasks: 2
  files: 3
---

# Phase 08 Plan 02: Crawler + Main Wiring Summary

**Wired Plan 01's data layer into the live crawler: 15s timeout (config-driven), EOSE-quorum early-exit at ≥70% queried-relay completion, hit-set-driven PERF-02 stamping, honest non-zero `staleRemaining` via `CountStalePubkeys`, and one-time startup backfill — verified live against production Dgraph + 148 relays.**

## Performance

- **Duration:** ~6 min active executor time
- **Tasks:** 2 of 2 implementation tasks complete (Task 3 was the live-host checkpoint)
- **Files modified:** 3 (1 created, 2 modified)

## Accomplishments

- **TIMEOUT-02 — EOSE-quorum early exit:** A function-local `atomic.Int32` counts completed relays (success + error); once `quorumReached` (≥ `ceil(queried*0.70)`) the batch's relay-query context is cancelled, so batches finish well before the deadline when the fast majority has responded. The 15s timeout (TIMEOUT-01) remains the hard backstop.
- **PERF-02 end-to-end:** `FetchAndUpdateFollows` now returns the real hit-set (`map[string]struct{}`), which flows into `MarkAttempted` so hits get `next_attempt = now+24h` / `miss_count=0` and misses back off geometrically.
- **METRIC-01 — honest metric:** `staleRemaining` is computed from `CountStalePubkeys` every batch — live run showed real, changing values (~594.9k) instead of 0.
- **D-06 — startup backfill:** `BackfillNextAttempt` runs once after `EnsureSchema`; live verification confirmed it is idempotent (0 on a subsequent restart) with zero stranded `last_attempt`-without-`next_attempt` nodes.
- **No Phase 7 regression:** quorum logic is additive; `markRelayDead` flow untouched; `-race` clean.

## Task Commits

1. **Task 1: EOSE-quorum early exit + hit-set return + quorumReached helper** — `ddcb6b1` (feat)
2. **Task 2: Startup backfill + staleRemaining fix + hit-set MarkAttempted wiring** — `5fe061e` (feat)

## Files Created/Modified

- `pkg/crawler/crawler_quorum_test.go` (created) — 8 `quorumReached` unit cases (threshold, zero-fraction, zero-queried, ceil-rounding, full-fraction, single-relay), `-race` clean
- `pkg/crawler/crawler.go` — `quorum` field + `RelayEOSEQuorum` config, `quorumReached` helper, `done atomic.Int32` + `cancel()` in `FetchAndUpdateFollows`, hit-set return type, startup `BackfillNextAttempt` in `New()`
- `cmd/crawler/main.go` — `RelayEOSEQuorum` threaded into `crawler.Config`, `CountStalePubkeys`-based `staleRemaining`, hit-set captured and passed to `MarkAttempted` with `BackoffParams`

## Decisions Made

See frontmatter `decisions`. Headline: quorum counts both success and error completions (mid-batch relay death advances quorum, never stalls), with a zero-fraction guard that falls back to the 15s hard timeout.

## Deviations from Plan

None — plan executed as written. The `cmd/crawler/main.go` TODO placeholder left by Plan 01 was replaced with the real hit-set + `BackoffParams` wiring as planned.

## Issues Encountered

None during implementation. During the live-host checkpoint, a transient Dgraph gRPC `Unavailable`/EOF ended the run via the pre-existing `Count*` break-on-error pattern (not a Phase 8 defect). Captured as a follow-up todo (`.planning/todos/pending/crawler-retry-transient-stale-count-errors.md`) for optional resilience hardening.

## Live Verification Outcome

**APPROVED (live, production host).** Crawler built via `make build-crawler` and run against live Dgraph + 148 relays:
- `staleRemaining` honest and non-zero (~594.9k), changing across batches (METRIC-01 ✓)
- Batches completed in ~12–14s, under the old 30s ceiling and near the 15s cap (TIMEOUT-01/02 ✓)
- `BackfillNextAttempt` idempotent: `seeded 0` on restart; direct DQL confirmed **0 stranded nodes** (`has(last_attempt) ∧ ¬has(next_attempt)` = 0; `has(last_attempt)`=368,359 ≈ `has(next_attempt)`=368,360) (PERF-02/D-06 ✓)
- Live config `~/deepfry/web-of-trust.yaml` not modified by testing ✓

## Next Phase Readiness

Phase 8 implementation complete: frontier prioritization, exponential backoff, 15s timeout, EOSE-quorum early exit, and honest observability are all live. Optional follow-up: transient-Dgraph-error retry in the crawl loop (logged as a pending todo).

---
*Phase: 08-frontier-prioritization-timeout-observability*
*Completed: 2026-06-13*
