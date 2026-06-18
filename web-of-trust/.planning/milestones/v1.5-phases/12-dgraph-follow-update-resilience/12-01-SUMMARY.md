---
phase: 12-dgraph-follow-update-resilience
plan: 01
subsystem: crawler-dgraph-write-path
tags: [dgraph, crawler, retries, observability, nostr]

requires:
  - phase: 10-unbounded-retry-backoff-hardening
    provides: Dgraph transient/fatal retry rationale
  - phase: 11-relay-query-liveness
    provides: bounded FetchAndUpdateFollows dispatcher behavior
provides:
  - Shared Dgraph transient/fatal classifier for follow writes and main-loop retries
  - Bounded AddFollowers Dgraph windows with progress diagnostics
  - FetchResult SkipAttempt handling so transient write failures remain retry-eligible
  - Regression tests for transient skip, fatal passthrough, progress accounting, and MarkAttempted filtering
affects: [crawler, dgraph, retry-scheduling, observability]

tech-stack:
  added: []
  patterns:
    - One all-or-nothing Dgraph transaction with bounded child contexts per write window
    - Per-pubkey transient follow-write failures surfaced through FetchResult.SkipAttempt

key-files:
  created:
    - pkg/crawler/crawler_dgraph_write_test.go
  modified:
    - pkg/dgraph/dgraph.go
    - pkg/dgraph/dgraph_chunks_test.go
    - pkg/dgraph/dgraph_stale_test.go
    - pkg/crawler/crawler.go
    - cmd/crawler/main.go
    - cmd/crawler/main_test.go

key-decisions:
  - "Transient AddFollowers failures are batch-local pubkey failures: they are logged, added to SkipAttempt, and left unstamped by MarkAttempted."
  - "AddFollowers keeps the existing single transaction for kind-3 replacement semantics; only internal query/mutation/commit windows receive bounded child contexts."
  - "ResourceExhausted remains fatal through the shared Dgraph classifier."

patterns-established:
  - "Use dgraph.IsTransientError for Dgraph/gRPC transient classification across packages."
  - "Use FetchResult.Hits for successful kind-3 handling and FetchResult.SkipAttempt for retry scheduling exclusions."

requirements-completed: [DWRITE-01, DWRITE-02, DWRITE-03, DWRITE-04, OBS-02, TEST-06]

duration: 38 min
completed: 2026-06-18
status: complete
---

# Phase 12 Plan 01: Dgraph Follow-Update Resilience Summary

**Dgraph follow updates now fail transiently per pubkey, preserve atomic kind-3 graph writes, and leave skipped pubkeys retry-eligible.**

## Performance

- **Duration:** 38 min
- **Started:** 2026-06-18T08:00:00Z
- **Completed:** 2026-06-18T08:37:56Z
- **Tasks:** 3
- **Files modified:** 7

## Accomplishments

- Added `dgraph.IsTransientError`, `dgraph.FollowUpdateError`, AddFollowers progress accounting, and bounded child contexts around Dgraph query/mutation/commit units.
- Changed `FetchAndUpdateFollows` to return `FetchResult{Hits, SkipAttempt}` and convert transient AddFollowers failures into retry scheduling instead of batch aborts.
- Updated the main loop to call `attemptableBatchKeys` before `MarkAttempted`, so transient follow-write failures receive no clean hit/miss stamp.
- Added deterministic short tests for Dgraph classification/progress, crawler transient/fatal write behavior, and MarkAttempted input filtering.

## Task Commits

1. **Task 1: Bound and instrument AddFollowers without partial durable commits** - `0b646d5` (feat)
2. **Task 2: Treat transient follow-write failures as retryable pubkey failures, not batch failures** - `429f7ce` (feat)
3. **Task 3: Regression coverage for classification, progress, retry scheduling, and fatal passthrough** - `055e55d` (test)
4. **Deviation fix: Keep integration backfill tests current** - `fa05b88` (test)

## Files Created/Modified

- `pkg/dgraph/dgraph.go` - Shared transient classifier, FollowUpdateError, per-window AddFollowers deadlines, progress/final outcome logs.
- `pkg/dgraph/dgraph_chunks_test.go` - Unit tests for transient/fatal status classification and progress accounting.
- `pkg/dgraph/dgraph_stale_test.go` - Integration test call sites updated for current BackfillNextAttempt signature.
- `pkg/crawler/crawler.go` - `followStore` seam, `FetchResult`, transient AddFollowers skip-and-continue behavior, fatal passthrough.
- `pkg/crawler/crawler_dgraph_write_test.go` - Signed-event tests for transient skip scheduling and fatal passthrough.
- `cmd/crawler/main.go` - Shared classifier delegation, `attemptableBatchKeys`, MarkAttempted filtering, hit metrics from `result.Hits`.
- `cmd/crawler/main_test.go` - ResourceExhausted fatal classifier test and attemptable-key filtering test.

## Decisions Made

- `AddFollowers` does not retry inside the batch. A transient write failure leaves the pubkey stale/eligible so the normal crawler selection path retries later without wedging the current batch.
- Follow-list replacement remains all-or-nothing: no per-chunk durable commits were introduced.
- Slow/failure logs use key/value-style fields: `pubkey`, `follows`, `chunk`, `elapsed`, `retry_count`, and `outcome`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Integration-tag package build used stale BackfillNextAttempt signature**
- **Found during:** Plan verification
- **Issue:** `go test -tags=integration ./pkg/dgraph -run TestAddFollowersLargeKind3 -count=1` failed before running the target test because older integration tests still called `BackfillNextAttempt(ctx)`.
- **Fix:** Updated those integration-test call sites to pass the existing 86400-second hit-refresh cadence expected by the test.
- **Files modified:** `pkg/dgraph/dgraph_stale_test.go`
- **Verification:** `go test -tags=integration ./pkg/dgraph -run TestAddFollowersLargeKind3 -count=1`
- **Committed in:** `fa05b88`

---

**Total deviations:** 1 auto-fixed (1 blocking test-build issue)
**Impact on plan:** No production scope change. The fix kept integration verification compatible with the current API.

## Issues Encountered

- The installed `~/.codex/gsd-core/bin/gsd-tools.cjs` copy was missing package metadata. Used the cached npm package copy for GSD helper queries and normal git commits for close-out.

## Verification

- `make test`
- `go test ./pkg/dgraph ./pkg/crawler ./cmd/crawler -count=1`
- `go test -tags=integration ./pkg/dgraph -run TestAddFollowersLargeKind3 -count=1`

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

Phase 12 satisfies the v1.5 Dgraph follow-update resilience requirements. Broader crawl-throughput tuning remains deferred to the existing spike backlog.

---
*Phase: 12-dgraph-follow-update-resilience*
*Completed: 2026-06-18*
