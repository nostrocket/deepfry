---
phase: 13-main-loop-throughput-controls
plan: 13-01
subsystem: crawler
tags: [go, config, crawler-loop, metrics, dgraph, nostr-relays]

requires:
  - phase: 12-dgraph-follow-update-resilience
    provides: transient follow-update failures remain retry-eligible through SkipAttempt
provides:
  - independent Dgraph frontier selection via frontier_batch_size
  - scheduled count-query sampling via count_sample_interval
  - exact selected/queried/hit/skipped/marked batch accounting
  - sampled/cached count context in BATCH_METRICS and crawler-metrics.jsonl
  - relay chunk regression coverage for frontier sizes larger than relay caps
affects: [main-loop, metrics, crawler-config, relay-filter-safety]

tech-stack:
  added: []
  patterns:
    - temp-HOME config tests for ~/deepfry isolation
    - main-loop count sampling helper with cached snapshot reporting
    - exact relay queried count sourced from validated authors

key-files:
  created:
    - .planning/phases/13-main-loop-throughput-controls/13-01-SUMMARY.md
  modified:
    - pkg/config/config.go
    - pkg/config/config_test.go
    - pkg/crawler/crawler.go
    - pkg/crawler/crawler_filter_test.go
    - cmd/crawler/main.go
    - cmd/crawler/main_test.go
    - cmd/crawler/metrics.go
    - cmd/crawler/metrics_test.go
    - .planning/phases/13-main-loop-throughput-controls/13-01-PLAN.md
    - .planning/phases/13-main-loop-throughput-controls/13-REVIEW.md

key-decisions:
  - "Count sampling uses batch 1, then every N completed batches; interval 1 preserves previous every-batch behavior."
  - "Unsampled batches emit cached total/stale counts and encode new_pubkeys as JSON null because no fresh CountPubkeys value exists."
  - "FetchResult.Queried is the validated author count submitted to relay filters, not the selected Dgraph frontier size."

patterns-established:
  - "Use cfg.FrontierBatchSize only at the Dgraph GetStalePubkeys boundary."
  - "Keep cfg.RelayFilterBatchSize as the crawler relay FilterBatchSize and learned relay chunk cap ceiling."
  - "Record selected, queried, skipped, and marked counts separately instead of inferring one from another."

requirements-completed:
  - LOOP-01
  - LOOP-02
  - LOOP-03
  - LOOP-04
  - COUNT-01
  - COUNT-02
  - COUNT-03
  - MEASURE-01
  - MEASURE-02
  - MEASURE-03
  - TEST-01
  - TEST-02

duration: 34 min
completed: 2026-06-18
status: complete
---

# Phase 13 Plan 13-01: Main-Loop Throughput Controls Summary

**Dgraph frontier sizing, sampled count queries, exact batch accounting, and relay-cap safety coverage for crawler throughput tuning**

## Performance

- **Duration:** 34 min
- **Started:** 2026-06-18T13:28:00Z
- **Completed:** 2026-06-18T14:02:41Z
- **Tasks:** 3
- **Files modified:** 10

## Accomplishments

- Added `frontier_batch_size` and `count_sample_interval` config controls with safe defaults and temp-HOME tests.
- Changed the main loop to select stale pubkeys with `cfg.FrontierBatchSize` while relay requests remain governed by `cfg.RelayFilterBatchSize`.
- Added `FetchResult.Queried` from validated relay authors, so selected frontier size, queried relay authors, hits, skipped attempts, and marked attempts are tracked independently.
- Added `countSampleState` so `CountPubkeys` and `CountStalePubkeys` run on configured sample intervals through `retryDgraph`, with cached count metadata on skipped intervals.
- Expanded `BATCH_METRICS` and `crawler-metrics.jsonl` run records with frontier/relay/count-sampling settings and exact accounting totals.
- Added regression coverage proving a 250-author frontier is still split into 100/100/50 relay chunks.

## Task Commits

1. **Task 1: Add independent frontier and count-sampling config** - `3950904` (`feat(13-01)`)
2. **Task 2: Refactor main-loop accounting, count sampling, and metrics** - `3cc997e` (`feat(13-01)`)
3. **Task 3: Prove relay chunk safety and document operator measurement procedure** - `87bbb73` (`docs(13-01)`)
4. **Code review fixes: Resolve throughput review findings** - `708232f` (`fix(13-01)`)

## Files Created/Modified

- `pkg/config/config.go` - Added throughput config fields, defaults, and non-positive guards.
- `pkg/config/config_test.go` - Added temp-HOME tests for defaults, explicit YAML, and guard behavior.
- `pkg/crawler/crawler.go` - Added exact validated-author queried count to `FetchResult`.
- `pkg/crawler/crawler_filter_test.go` - Added queried-count and frontier-vs-relay chunk safety regressions.
- `cmd/crawler/main.go` - Added count sampling, frontier-size selection, exact batch accounting, and sampled/cached logs.
- `cmd/crawler/main_test.go` - Added count-sampling schedule tests.
- `cmd/crawler/metrics.go` - Expanded batch metrics and run records with throughput-control context.
- `cmd/crawler/metrics_test.go` - Added run-record and BATCH_METRICS JSON coverage for sampled/cached batches.
- `.planning/phases/13-main-loop-throughput-controls/13-01-PLAN.md` - Clarified operator measurement procedure.
- `.planning/phases/13-main-loop-throughput-controls/13-REVIEW.md` - Captured code review findings and resolution state.

## Decisions Made

- Count sampling is batch-indexed: batch 1 samples, then the next sample occurs after `count_sample_interval` completed batches. This makes interval 1 exactly match prior every-loop behavior.
- Unsampled batches keep cached `total_pubkeys` and `stale_remaining` but emit `new_pubkeys:null`, avoiding false precision when no fresh total count exists.
- Relay query throughput uses validated authors as the queried denominator. Invalid selected pubkeys are observable as selected-not-queried instead of inflating relay throughput.

## Deviations from Plan

Post-implementation code review found one blocker and two warnings. They were fixed in `708232f` without changing the phase scope:

- Normal crawler completion now cancels the signal context before `wg.Wait`, so the signal goroutine cannot hang shutdown.
- Cached `stale_remaining` estimates are adjusted after each marked batch until the next sampled count.
- Relay chunk safety coverage now tests the production chunk helper used by `queryRelay`.

**Total deviations:** 3 auto-fixed after review.
**Impact on plan:** Review fixes tightened the planned behavior and tests; no scope expansion.

## Issues Encountered

Code review initially reported unresolved findings. All were fixed in `708232f`, and the full automated verification suite passed afterward.

## User Setup Required

None - no external service configuration required.

## Verification

- `go test ./pkg/config -count=1` - passed
- `go test ./cmd/crawler -count=1` - passed
- `go test ./pkg/crawler -run 'Test.*(SplitAuthorsChunks|Frontier.*Relay|Relay.*Chunk)' -count=1` - passed
- `go test ./... -short -cover` - passed
- Post-review rerun of the same verification suite - passed

## Next Phase Readiness

Phase 13 can now be verified against the main-loop throughput goal. Operators can run the documented `WOT_ROUND` baseline and optimized rounds to decide whether Phase 14 should proceed with deeper Dgraph write-path throughput work.

---
*Phase: 13-main-loop-throughput-controls*
*Completed: 2026-06-18*
