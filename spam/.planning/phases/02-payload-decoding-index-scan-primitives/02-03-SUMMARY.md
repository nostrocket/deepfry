---
phase: 02-payload-decoding-index-scan-primitives
plan: "03"
subsystem: lmdb
tags: [scan, lmdb, dupsort, pagination, windowing, lmdb-09]
dependency_graph:
  requires: ["02-01"]
  provides: ["scan_index_bounded", "scan_index_windowed", "ScanDirection"]
  affects: ["Phase 3 query engine (filter routing, latestPerAuthor, cursor pagination)"]
tech_stack:
  added: []
  patterns:
    - "Bounded forward/reverse scan over DUPSORT indexes via heed range/rev_range + move_through_duplicate_values"
    - "Windowed unbounded scan: per-batch short RoTxn, Included resume + levId skip for DUPSORT mid-group boundaries"
    - "index-specific start key lengths to avoid golpe comparator SIGABRT on oversized keys"
key_files:
  created:
    - src/lmdb/scan.rs
    - tests/scan_test.rs
  modified:
    - src/lmdb/mod.rs
decisions:
  - "DUPSORT resume uses Bound::Included + lev_id skip (not Bound::Excluded) to avoid silently dropping mid-group duplicate levIds when a batch boundary falls within a DUPSORT key group"
  - "scan_index_windowed exposed as pub fn (not just called via limit=0) to allow test-only small-window override that proves multi-batch behavior"
  - "Smoke test opens a fresh env per index to avoid LMDB comparator conflict when iterating all six indexes with different comparator types in the same process"
metrics:
  duration: "6m 17s"
  completed_date: "2026-06-11"
  tasks_completed: 3
  files_created: 2
  files_modified: 1
  commits: 2
---

# Phase 02 Plan 03: Index Scan Primitives Summary

Bounded, direction-aware, DUPSORT-correct index scan primitives over all six Event__* indexes, with per-call short read transactions and windowed unbounded scanning.

## What Was Built

**`src/lmdb/scan.rs`** implements `ScanDirection` (Forward/Reverse), `scan_index_bounded` (the public API), `scan_index_windowed` (windowed unbounded path), and the generic `collect_bounded`/`collect_window` helpers.

**`tests/scan_test.rs`** provides integration tests: resume-cursor continuation, DUPSORT duplicate-value coverage (levIds 5&6 and 7&8), per-index smoke (all six indexes), and a tiny-window windowed completeness test.

## Key Design Decisions

### DUPSORT resume: Included + levId skip (not Excluded)

The research doc (RESEARCH.md Pitfall 5) recommended `Bound::Excluded(last_key)` for windowed resume. However, `Event__*` indexes use `MDB_DUPSORT + MDB_INTEGERDUP`, and a batch boundary may land mid-group (e.g., batch ends at levId=5 when key (kind=1,ts=1700000255) also has levId=6). Using `Excluded` drops ALL VALUEs for that key on resume, silently skipping levId=6.

**Fix:** Resume with `Bound::Included(last_key)` and skip entries where `key == resume_key && lev_id <= resume_lev_id` (forward) or `lev_id >= resume_lev_id` (reverse). For non-duplicate keys this is a no-op; for DUPSORT groups it correctly resumes past the last-seen levId.

This is a Rule 1 auto-fix (bug in the research-prescribed approach when applied to DUPSORT indexes).

### Index-specific start key lengths

The smoke test initially used a generic 48-byte zero key for all indexes. The golpe `Uint64Uint64Cmp` comparator (used by `Event__kind`) reads exactly 16 bytes and SIGABRT'd when given a 48-byte key in the range bound. Fixed by using index-specific key lengths (16 bytes for Event__kind, 40 for Event__id/pubkey, 48 for Event__pubkeyKind, etc.).

This is a Rule 1 auto-fix (test safety bug discovered during Task 3 execution).

## LMDB-09 Satisfaction

- **Structural guarantee**: `scan_index_bounded` takes `&heed::Env` not `&heed::RoTxn` — callers cannot pass a long-lived transaction.
- **Behavioral guarantee**: `scan_index_windowed` opens a fresh `RoTxn` per batch and drops it before accumulating results. The `test_windowed_with_small_window_no_gaps_no_dupes` test (window=4 < 11 total) proves multi-batch operation completes correctly.

## Test Results

All 53 tests pass:
- 9 lib unit tests in `lmdb::scan::tests`
- 4 integration tests in `tests/scan_test.rs`
- All 40 prior tests still passing

## Acceptance Criteria

- `scan_index_bounded` signature takes `&heed::Env` (not `&RoTxn`): PASS
- `grep -c 'move_through_duplicate_values' src/lmdb/scan.rs` = 8 (>= 2): PASS
- `.create(` count = 0; `write_txn` count = 0: PASS
- `rev_range` present (no `.rev()` on RoRange): PASS
- `Bound::Excluded` present (windowed resume skip logic): PASS
- Forward Event__kind limit=3 = [4,5,6]: PASS
- Reverse Event__kind limit=3 = [2,3,9]: PASS
- Unknown index returns `IndexError::SubDbNotFound`: PASS
- DUPSORT coverage: levIds 5,6,7,8 all present in full scan: PASS
- All six indexes non-empty, levIds in 1..=11: PASS
- `pub mod scan` in mod.rs: PASS

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] DUPSORT mid-group batch boundary drops levIds with Bound::Excluded**
- **Found during:** Task 2 windowed scan test (`test_windowed_with_small_window_no_gaps_no_dupes` returned 10 instead of 11)
- **Issue:** Research doc prescribed `Bound::Excluded(last_key)` for windowing resume, but DUPSORT `Event__*` indexes have multiple VALUEs per key. `Excluded` skips all VALUEs for the last key, not just the last-seen one.
- **Fix:** Resume with `Bound::Included(last_key)` + skip entries where `key == resume_key && lev_id <= resume_lev_id`. Requires tracking both `resume_key` and `resume_lev_id` across batches.
- **Files modified:** `src/lmdb/scan.rs` (collect_window signature + logic)

**2. [Rule 1 - Bug] SIGABRT from golpe comparator on oversized start key in smoke test**
- **Found during:** Task 3 per-index smoke test
- **Issue:** Using 48-byte all-zero start key for all indexes caused SIGABRT in the golpe `Uint64Uint64Cmp` C comparator when given a 48-byte key where it expected 16 bytes (unsafe C memory read).
- **Fix:** `index_low_start_key(short_name)` helper returns correctly-sized zero keys per index. Smoke test opens a fresh env per index to prevent comparator state conflicts.
- **Files modified:** `tests/scan_test.rs`

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes. `scan.rs` is a pure read-only LMDB primitive; no new trust boundaries introduced.

## Known Stubs

None — all scan primitives return live data from the fixture LMDB.

## Self-Check: PASSED

- `src/lmdb/scan.rs` exists: FOUND
- `tests/scan_test.rs` exists: FOUND
- `src/lmdb/mod.rs` contains `pub mod scan;`: FOUND
- Commit 482cab6 exists: FOUND
- Commit 5e326e6 exists: FOUND
- `cargo test --all-targets`: 53 passed, 0 failed
