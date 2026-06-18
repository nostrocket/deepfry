---
phase: 13
reviewed: 2026-06-18T14:05:54Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - pkg/config/config.go
  - pkg/config/config_test.go
  - pkg/crawler/crawler.go
  - pkg/crawler/crawler_filter_test.go
  - cmd/crawler/main.go
  - cmd/crawler/main_test.go
  - cmd/crawler/metrics.go
  - cmd/crawler/metrics_test.go
findings:
  critical: 1
  warning: 2
  info: 0
  total: 3
status: resolved
resolved_by: 708232f
---

# Phase 13: Code Review Report

**Reviewed:** 2026-06-18T14:05:54Z
**Depth:** standard
**Files Reviewed:** 8
**Status:** resolved by `708232f`

## Summary

Reviewed the Phase 13 throughput-control changes in config loading, crawler main-loop accounting, relay query counting, and metrics serialization. The initial pass found one shutdown blocker and two warnings. All findings were fixed in `708232f`.

## Resolution

- `CR-01` fixed: normal shutdown now cancels the context and stops signal delivery before `wg.Wait`, allowing the signal goroutine to exit.
- `WR-01` fixed: cached stale estimates are adjusted after marked attempts until the next sampled count.
- `WR-02` fixed: relay chunk regression now exercises the production `nextAuthorChunk` helper used by `queryRelay`.

Post-fix verification:

- `go test ./pkg/config -count=1` - passed
- `go test ./cmd/crawler -count=1` - passed
- `go test ./pkg/crawler -count=1` - passed
- `go test ./... -short -cover` - passed

## Narrative Findings (AI reviewer)

## Critical Issues

### CR-01: BLOCKER - Normal completion can hang forever waiting for the signal goroutine

**File:** `cmd/crawler/main.go:194`

**Issue:** The signal goroutine blocks forever on `sig := <-sigChan` and is included in the `WaitGroup`. On normal loop completion, for example when `len(pubkeys) == 0` at `cmd/crawler/main.go:348`, execution reaches `wg.Wait()` at `cmd/crawler/main.go:470` without cancelling the context or otherwise unblocking that goroutine. The deferred `cancel()` cannot help because it runs only after `main` returns, and `main` is waiting on the goroutine.

**Impact:** The crawler can log "No stale pubkeys found, work complete", write final metrics, then never exit unless an operator sends SIGINT/SIGTERM. This breaks unattended runs and any supervisor or script that expects the crawler to terminate when work is complete.

**Recommendation:** Make the signal goroutine also exit on context cancellation, and call `cancel()` before waiting during normal shutdown.

```go
go func() {
    defer wg.Done()
    select {
    case sig := <-sigChan:
        log.Printf("Received signal: %v, initiating graceful shutdown...", sig)
        cancel()
    case <-ctx.Done():
        return
    }
}()

// before wg.Wait(), after final report/metrics are written
cancel()
signal.Stop(sigChan)
wg.Wait()
```

## Warnings

### WR-01: WARNING - Cached stale_remaining subtracts only the current batch, not prior cached batches

**File:** `cmd/crawler/main.go:405`

**Issue:** `staleRemaining := max(0, countSnapshot.totalStale-markedAttempted)` uses `countSnapshot.totalStale`, which is the last sampled stale count on unsampled batches. Because `countSampleState` never decrements the cached stale count after each completed batch, unsampled batches ignore attempts marked in earlier cached batches. With a sample of 1,000 stale rows and two cached batches that mark 500 each, both cached batches can report 500 remaining even though the second should be near 0.

**Impact:** Phase 13's BATCH_METRICS and logs can overstate `stale_remaining` during `count_sample_interval > 1` runs. That weakens the operator comparison this phase is meant to support and can make the optimized loop look less effective than it is.

**Recommendation:** Track marked attempts since the last successful sample, or update the cached stale estimate after each batch. For example, add a method on `countSampleState` that clamps and subtracts `markedAttempted` after metrics are computed, then have cached snapshots use the adjusted value.

```go
func (s *countSampleState) applyMarked(marked int) {
    s.totalStale = max(0, s.totalStale-marked)
}
```

### WR-02: WARNING - Relay chunking regression test reimplements the chunking logic instead of exercising production code

**File:** `pkg/crawler/crawler_filter_test.go:82`

**Issue:** `TestFrontierBatchLargerThanRelayCapSplitsRelayChunks` constructs `chunkSizes` with a local loop that duplicates the production logic from `queryRelay`, but it never calls `queryRelay` or a shared helper. If production chunking regresses to use `frontier_batch_size` as the relay cap, this test would still pass because it is testing its own copy.

**Impact:** The test does not actually protect against the Phase 6 oversized-filter failure mode called out by Phase 13. It gives false confidence around the highest-risk safety requirement for this change.

**Recommendation:** Extract the author chunking decision into a small production helper used by `queryRelay`, then test that helper, or add an injectable subscribe/query seam that records each production relay filter's author count.

```go
func splitAuthorsByCap(authors []string, cap int) [][]string {
    // production helper used by queryRelay and tested directly
}
```

## Verification

- `go test ./pkg/config -count=1` - passed
- `go test ./cmd/crawler -count=1` - passed
- `go test ./pkg/crawler -count=1` - passed

---

_Reviewed: 2026-06-18T14:05:54Z_
_Reviewer: the agent (gsd-code-reviewer)_
_Depth: standard_
