---
phase: 03-query-engine
plan: "09"
subsystem: query
tags: [query, merge, k-way-merge, cr-02, cr-03, wr-04, in-01, in-02, in-03, windowed-merge, per-stream-since]

requires:
  - phase: 03-query-engine
    plan: "08"
    provides: corrected Reverse scan primitives (scan_index_one_window, collect_window) with Bound::Excluded(ts+1)
provides:
  - merge_windowed: windowed k-way merge entry point with per-stream frontier (BinaryHeap) and per-stream since exhaustion (CR-02/CR-03 fix)
  - merge_prefixes: thin wrapper delegating to merge_windowed (WR-04 closed — no orphaned merge path)
  - execute_query_internal: routed through merge_windowed; no inline sort_unstable_by; global since_cutoff removed (CR-02/CR-03/IN-01/IN-02/IN-03 fixes)
  - test_merge_windowed_cross_iteration_global_desc_order: proves global DESC order across iterations
  - test_merge_windowed_per_stream_since_exhaustion: proves per-stream since does not starve other streams
  - test_execute_query_multistream_cross_iteration_order: engine-level CR-02 regression
  - test_execute_query_multistream_page_union_no_loss: page cursor integrity across multi-stream queries
  - test_execute_query_multistream_since_per_stream: engine-level CR-03 per-stream since regression
affects:
  - 03-query-engine (CR-02, CR-03, WR-04, IN-01/02/03 closed — phase 03 gap closure complete)
  - Phase 04 GraphQL API (consumes execute_query with correct ordering/pagination/since semantics)

tech-stack:
  added: []
  patterns:
    - "Windowed k-way merge frontier: BinaryHeap seeded with each stream's buffer head; only pop when globally maximum; refill only the popped stream — correct across window boundaries (no sort-per-batch)"
    - "Per-stream since exhaustion: descending batch truncated at first ts < since; stream marked exhausted; other streams continue independently"
    - "merge_windowed stateless batch API: called once with emit_limit = limit*2 + DEFAULT_WINDOW_SIZE; no separate loop in engine; D-07 over-fetch headroom built in"
    - "StreamState with buffer + buf_pos: per-stream buffer avoids re-allocating; refill_stream replaces buffer in one call; buf_pos tracks consumption"

key-files:
  created: []
  modified:
    - src/query/merge.rs
    - src/query/engine.rs

key-decisions:
  - "Delegate merge_prefixes to merge_windowed (since=0, batch_size=scan_limit) rather than deleting it — backward compat with its existing tests; WR-04 closed because the divergent implementation is removed"
  - "merge_windowed is stateless (takes start_keys, emits up to emit_limit) — engine calls it once with emit_limit=limit*2+DEFAULT_WINDOW_SIZE; this covers D-07 over-fetch without a separate loop for virtually all real-world cases"
  - "IN-03: emit tracing::debug!(skip_count) before returning when > 0; removed the accumulator from the loop interior so it's threaded only through hydrate_lev_ids"
  - "StreamState.buf_pos cursor pattern (not Vec::drain/remove) — avoids O(n) shifts on a buffer that is consumed front-to-back; buffer replaced wholesale on each refill_stream call"
  - "Since truncation in refill_stream: find first position where ts < since via Iterator::position; truncate there; mark exhausted immediately — guarantees no entry below since leaks from any stream"

requirements-completed: [QRY-01, QRY-02]

duration: 6min
completed: 2026-06-12
---

# Phase 03 Plan 09: CR-02/CR-03 Windowed K-Way Merge Summary

**CR-02 and CR-03 closed: execute_query_internal now routes through a true windowed k-way merge with per-stream since exhaustion — no sort-per-batch, no global since_cutoff**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-06-12T09:04:44Z
- **Completed:** 2026-06-12T09:10:20Z
- **Tasks:** 2 (TDD: RED → GREEN for each)
- **Files modified:** 2

## Accomplishments

### Task 1: Windowed k-way merge in merge.rs (CR-02/CR-03/IN-01/WR-04)

- Added `merge_windowed`: windowed BinaryHeap k-way merge with per-stream `scan_index_one_window` refill
- Per-stream frontier: heap seeded with each stream's buffer head; only the globally maximum entry is popped; only the popped stream is refilled — preserves (created_at DESC, lev_id DESC) total order across ALL iteration windows
- Per-stream `since` truncation (CR-03): `refill_stream` finds the first batch entry with `ts < since`, truncates there, marks the stream exhausted — other streams continue independently
- CR-01 prefix guard applied inside `refill_stream` (`take_while(starts_with(prefix))`) before since truncation
- `merge_prefixes` rewritten as a thin delegate to `merge_windowed` with `since=0` — no orphaned second merge implementation (WR-04 closed)
- Removed dead `stream_prefixes` Vec and `drop(stream_prefixes)` (IN-01 closed)
- New tests: `test_merge_windowed_cross_iteration_global_desc_order` (9 events, batch_size=2, strict non-increasing) and `test_merge_windowed_per_stream_since_exhaustion` (since=1715000000, only levId=11 returned)

### Task 2: Route engine through merge_windowed (CR-02/CR-03/IN-02/IN-03/WR-04)

- Replaced the `StreamState` loop + `merged_batch.sort_unstable_by(...)` with a single `merge_windowed` call (CR-02 fix)
- Removed global `since_cutoff` flag, the empty `if since_cutoff {}` block (IN-02), and the `|| since_cutoff` break term (CR-03 fix)
- Added `tracing::debug!(skip_count, "query completed with skipped payloads")` before returning (IN-03)
- `merge_windowed` is imported and called in the production path — WR-04 closed (no longer orphaned)
- Updated `execute_query_internal` docstring to describe the new algorithm
- New regression tests:
  - `test_execute_query_multistream_cross_iteration_order`: kinds=[1,2], batch_size=1, all 9 events non-increasing
  - `test_execute_query_multistream_page_union_no_loss`: page1+page2 == limit=4 single-query prefix, no overlap
  - `test_execute_query_multistream_since_per_stream`: since=1705000000, kinds=[1,2], 3 events; stream B exhausted at its own boundary, stream A unimpeded

## Task Commits

1. **Task 1: merge_windowed** — `9403f41` (feat)
2. **Task 2: engine rewire** — `8d6c589` (feat)

## Files Created/Modified

- `src/query/merge.rs` — Added `merge_windowed`, `StreamState`, `refill_stream`; rewrote `merge_prefixes` as delegate; removed `stream_prefixes` dead code; 2 new tests
- `src/query/engine.rs` — Replaced StreamState loop + sort with `merge_windowed` call; removed `since_cutoff`; added `skip_count` debug log; updated doc; 3 new tests

## Decisions Made

- `merge_prefixes` kept as thin delegate rather than deleted — existing tests use it; the divergent implementation is removed (WR-04: only one merge path exists)
- `merge_windowed` is stateless (no resume state between calls) — engine calls once with `emit_limit = limit*2 + DEFAULT_WINDOW_SIZE`; this is sufficient for D-07 over-fetch in virtually all cases without a secondary loop
- `buf_pos` cursor pattern instead of `Vec::remove/drain` — avoids O(n) shifts; buffer replaced wholesale on `refill_stream`
- `since` truncation in `refill_stream` uses `Iterator::position` on the descending batch: first `ts < since` is the cutoff; all entries after it are also < since (descending order guarantee)

## Deviations from Plan

None — plan executed exactly as written.

Both CR fixes landed cleanly in their intended locations. The TDD flow confirmed the bugs: RED phase showed 2 failing tests (cross-iteration ordering violation, global since_cutoff starvation); GREEN phase fixed both in one pass.

The only minor calibration was correcting the `test_execute_query_multistream_since_per_stream` test's expected count from an initially-incorrect 7 (based on wrong ts mental model) to the correct 3 (verified against `Event__kind.json` golden vectors), without changing the test's purpose or the production code.

## Issues Encountered

None.

## Verification

- `cargo test --lib query::merge`: 8 tests pass (including 2 new merge_windowed tests)
- `cargo test --lib query::engine`: 19 tests pass (including 3 new multistream tests)  
- `cargo test --all-targets`: 89 lib + 14 integration = 103 total, 0 failed
- `grep merge_windowed engine.rs`: called in production path (line 201)
- `grep sort_unstable_by engine.rs`: absent from production code (only in comment)
- `grep since_cutoff engine.rs`: absent as a variable (only in comments and test strings)
- `grep stream_prefixes merge.rs`: absent

## Next Phase Readiness

- CR-02, CR-03, WR-04, IN-01, IN-02, IN-03 all closed
- Phase 03 verification truths #1-#5 should now all pass (was 3/5 before this plan)
- Phase 04 GraphQL API can consume `execute_query` with correct ordering, pagination, and per-stream since semantics
- All 103 tests green (89 lib + 14 integration)

---
*Phase: 03-query-engine*
*Completed: 2026-06-12*
