---
phase: 03-query-engine
plan: "06"
subsystem: query
tags: [gap-closure, cr-01, cr-02, cr-03, cr-04, wr-01, in-01, in-04, merge, engine, since, prefix-guard, exclusive-resume]
dependency_graph:
  requires:
    - 03-05-SUMMARY.md  # lev_id-keyed HashMap join in engine
    - 03-04-SUMMARY.md  # execute_query, PageCursor, merge_prefixes baseline
  provides:
    - merge_prefixes with take_while starts_with prefix boundary guard (CR-01)
    - build_start_keys with sort+dedup (WR-01)
    - scan_index_one_window — single dup-group-complete window with exclusive-resume
    - execute_query_internal with since stop-bound (CR-02), residual kind/author/id (CR-01),
      exclusive-resume windowing (CR-03), IN-04 cursor.unwrap() fix
  affects:
    - src/query/merge.rs (prefix guard, new test)
    - src/query/router.rs (dedup, new test)
    - src/query/filter.rs (limit doc fix IN-01)
    - src/lmdb/scan.rs (new scan_index_one_window function)
    - src/query/engine.rs (full execute_query_internal rewrite)
tech_stack:
  added: []
  patterns:
    - take_while starts_with per-prefix boundary guard in K-way merge
    - sort+dedup on start keys before merge
    - scan_index_one_window — key-granular exclusive-resume per stream
    - since stop-bound with since_cutoff flag breaking the backfill loop
    - post-hydration kind/author/id residual as belt-and-braces after prefix guard
    - struct StreamState per-start-key resume state in execute_query_internal
key_files:
  created: []
  modified:
    - src/query/merge.rs
    - src/query/router.rs
    - src/query/filter.rs
    - src/lmdb/scan.rs
    - src/query/engine.rs
decisions:
  - "CR-03 windowing fix: per-stream StreamState with scan_index_one_window instead of merge_prefixes + Included-restart; avoids modifying merge_prefixes signature"
  - "scan_index_one_window added to scan.rs as new pub function; reuses collect_window"
  - "merge_prefixes retained unchanged (tests pass, and router tests use it); engine no longer calls it in execute_query_internal"
  - "since_cutoff flag: process survivors from the first below-since batch, then break loop"
  - "Post-hydration residual on kinds/authors/ids is belt-and-braces; primary guard is prefix (merge.rs)"
metrics:
  duration: ~31 minutes
  completed: "2026-06-12"
  tasks: 2
  files_modified: 5
requirements: [QRY-01]
---

# Phase 03 Plan 06: Query Engine Gap Closure (CR-01/02/03/04/WR-01) Summary

**One-liner:** Fixed five query correctness bugs (cross-prefix contamination, missing since enforcement, broken DUPSORT windowing, stale lev_id, duplicate start keys) by adding a per-prefix boundary guard to merge.rs, deduping start keys in router.rs, adding scan_index_one_window with key-granular exclusive-resume to scan.rs, and rewriting execute_query_internal to use per-stream ExclusiveResume windowing with since stop-bound and post-hydration residual.

## What Was Built

### Task 1: Prefix-guard merge_prefixes + dedup start keys

**src/query/merge.rs** — CR-01 prefix boundary guard:
- For each `start_key` in `merge_prefixes`, compute `prefix = start_key[..len-8]` (strips trailing 8 LE bytes = `created_at`). This gives the logical value-partition prefix (kind bytes for `Event__kind`, pubkey bytes for `Event__pubkey`, etc.).
- After `scan_index_bounded(Reverse, key, scan_limit)`, apply `.take_while(|(k,_)| k.starts_with(prefix.as_slice()))` BEFORE pushing into the stream Vec.
- `take_while` is correct (not `filter`): in a reverse walk, all entries below the prefix are contiguous. `take_while` terminates the stream at the first out-of-prefix entry.
- For `Event__created_at` (8-byte key, prefix len=0), `starts_with([])` is vacuously true — no entries are rejected.
- New test: `test_merge_prefix_guard_no_contamination` — kind=2 start key with limit=20 returns exactly 2 events (levIds 1,9); asserts zero kind=1 contamination.

**src/query/router.rs** — WR-01 start-key dedup:
- Changed `build_start_keys` to collect into `let mut keys: Vec<Vec<u8>> = match selected { ... };` and then apply `keys.sort(); keys.dedup();` before returning.
- Sorting is safe because `merge_prefixes` re-orders by `(created_at, lev_id)` via the heap.
- New test: `test_build_start_keys_dedup_duplicate_authors` — asserts authors=[PK1,PK1] produces 1 key, kinds=[1,1,1] produces 1 key, distinct authors=[PK1,PK2] still produces 2 keys.

**src/query/filter.rs** — IN-01 doc fix:
- Corrected `NostrFilter::limit` doc comment from "0 means unbounded" to "0 → uses DEFAULT_WINDOW_SIZE (256) as the per-prefix scan limit", matching actual engine behavior.

### Task 2: since stop-bound + kind/author/id residual + exclusive-resume windowing + IN-04 fix

**src/lmdb/scan.rs** — new `scan_index_one_window`:
- New `pub fn scan_index_one_window(env, short_name, direction, resume_key, first_batch, window_size) -> Result<(Vec<(Vec<u8>, LevId)>, Vec<u8>, bool), IndexError>` function.
- Collects exactly ONE dup-group-complete window using `collect_window` (private helper already proven in `scan_index_windowed`).
- First call: `first_batch=true` → `Bound::Included(resume_key)`. Subsequent: `first_batch=false` → `Bound::Excluded(resume_key)`.
- Returns `(batch, next_resume_key, next_first_batch)` tuple. When stream exhausted, returns `(vec![], unchanged_resume_key, false)`.
- Drops the `RoTxn` immediately after collecting (D-08 short transaction guarantee preserved).

**src/query/engine.rs** — complete rewrite of `execute_query_internal`:

_CR-03 exclusive-resume windowing (replaces `Included`-restart):_
- Introduced `struct StreamState { resume_key, first_batch, prefix, exhausted }` per start_key.
- Loop calls `scan_index_one_window` per stream → updates `s.resume_key = next_resume`, `s.first_batch = next_first`.
- Each per-stream batch ends on a KEY boundary (collect_window drains trailing dup group).
- `Bound::Excluded` on resume → boundary key never re-scanned → no levId dropped or re-emitted.
- `update_start_keys_ts` helper removed (no longer needed with exclusive-resume design).
- `window_boundary` + `prev_window_boundary` state removed entirely (CR-04 — stuck-window branch disappears with correct exclusive-resume).

_CR-02 since stop-bound:_
- Added `let since = filter.since.unwrap_or(0);` at the start of `execute_query_internal`.
- In the `filter_map` on `merged_batch`: `if ts < since { since_cutoff = true; return None; }`.
- After `filtered_batch` is processed: `if since_cutoff { break; }` — stops the loop since all further events are older.

_CR-01 residual:_
- After hydration, added post-hydration residual checks:
  - `if let Some(kinds) = &filter.kinds { if !kinds.contains(&decoded.event.kind) { continue; } }`
  - `if let Some(authors) = &filter.authors { if !authors.iter().any(|a| a == &decoded.event.pubkey) { continue; } }`
  - `if let Some(ids) = &filter.ids { if !ids.iter().any(|id| id == &decoded.event.id) { continue; } }`

_IN-04 cursor.unwrap() fix:_
- Replaced `if cursor.is_some() { ... cursor.unwrap() ... }` with `if let Some(c) = cursor { ... }`.

_Imports:_
- Removed `use crate::query::merge::merge_prefixes` (no longer called in production).
- Added `scan_index_one_window` and `created_at_from_key` imports.

New regression tests (4 new):
- `test_execute_query_kind2_no_contamination` — kinds=[2] returns exactly 2 events, all kind==2 (CR-01 e2e)
- `test_execute_query_since_stop_bound` — since=1715000000 returns only levId=11 (ts=1720000000); since=1705000000 excludes levId=4 (CR-02)
- `test_execute_query_duplicate_authors_no_doubling` — authors=[PK1,PK1] limit=20 returns 9 events, all unique ids (WR-01 e2e)
- `test_execute_query_dupgroup_straddle_no_drop` — kinds=[1] limit=7 batch_size=1 returns all 7 kind=1 events, all unique ids, non-increasing ts (CR-03)

## Test Results

- `cargo test --lib query::merge`: 6 passed (5 existing + 1 new)
- `cargo test --lib query::router`: 6 passed (5 existing + 1 new)
- `cargo test --lib query::engine`: 13 passed (9 existing + 4 new CR/WR regression tests)
- `cargo test --all-targets`: **73 lib tests + 16 integration tests = all passed, zero failures**

## Deviations from Plan

**1. [Rule 2 - Auto-add] scan_index_one_window added to scan.rs**
- **Found during:** Task 2 design
- **Issue:** Plan said to either extend `merge_prefixes` to accept an exclusive-resume parameter, or "call scan_index_windowed-style collection per prefix". To avoid changing `merge_prefixes`'s signature (which would break its existing tests and force a major refactor of the K-way merge), I added a new `scan_index_one_window` function to `scan.rs` that exposes the `collect_window` primitive publicly. This is a cleaner API than modifying `merge_prefixes`.
- **Fix:** `scan_index_one_window` reuses the private `collect_window` function directly. The engine uses it per-stream in the `StreamState` loop.
- **Files modified:** `src/lmdb/scan.rs`
- **Commit:** 1573c74

**2. [Rule 1 - Behavior] merge_prefixes no longer called from execute_query_internal**
- The plan expected `merge_prefixes` to be extended (or kept). After the redesign, `execute_query_internal` calls `scan_index_one_window` directly per stream and sorts the merged_batch inline. `merge_prefixes` is kept unchanged for its own tests. The production code path no longer goes through `merge_prefixes` — it uses `scan_index_one_window` directly. This is consistent with the plan's "OR call scan_index_windowed-style collection per prefix" option.

No other deviations. All plan correctness requirements (CR-01/02/03/04, WR-01, IN-01, IN-04) satisfied.

## Decisions Made

| Decision | Rationale |
|----------|-----------|
| scan_index_one_window in scan.rs (not merge_prefixes extension) | Avoids changing merge_prefixes signature and breaking existing tests; cleaner single-responsibility API for "one dup-group-complete window" primitive |
| struct StreamState per-start-key in execute_query_internal | Self-contained; explicit `exhausted` flag avoids repeated empty checks; easy to understand and test |
| since_cutoff: process survivors then break (not pre-filter before hydration) | Ensures events at exactly `since` boundary are included; simpler than pre-computing ts from key without hydration |
| Post-hydration residual as belt-and-braces | Since some index scans may emit events that match the start-key prefix but not the filter value (e.g., golpe comparator edge cases), a residual provides defense-in-depth without performance cost |
| merge_prefixes kept unchanged | Its own tests pass and it's used by merge.rs tests; the engine bypass is an implementation detail |

## Known Stubs

None — all production code paths fully implemented. No hardcoded empty values, placeholder text, or unconnected data sources.

## Threat Flags

No new security-relevant surface introduced. All changes are read-only (no write_txn, no .create()). The new `scan_index_one_window` function opens only a `RoTxn` (short-lived, dropped before returning).

## Self-Check: PASSED

- FOUND: src/query/merge.rs (take_while starts_with guard, dedup test)
- FOUND: src/query/router.rs (sort+dedup, dedup test)
- FOUND: src/query/filter.rs (DEFAULT_WINDOW_SIZE doc)
- FOUND: src/lmdb/scan.rs (scan_index_one_window function)
- FOUND: src/query/engine.rs (since, residual, StreamState, exclusive-resume loop)
- FOUND: commit f5fe0ef (Task 1 — prefix-guard + dedup)
- FOUND: commit 1573c74 (Task 2 — since + residual + exclusive-resume)
- All 73 lib tests + 16 integration tests pass (cargo test --all-targets)
