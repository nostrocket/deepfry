---
phase: 03-query-engine
plan: "08"
subsystem: lmdb
tags: [lmdb, heed, dupsort, scan, reverse-scan, cr-01, bound-excluded, ts-plus-one]

requires:
  - phase: 03-query-engine
    provides: scan primitives (scan_index_bounded, scan_index_one_window, collect_window, collect_bounded) in src/lmdb/scan.rs
provides:
  - reverse_upper_bound(start_key) helper: ts+1 saturating Bound::Excluded construction for Reverse arms
  - collect_bounded Reverse arm: uses Bound::Excluded(ts+1 key) for finite start keys (CR-01 fix)
  - collect_window Reverse first_batch arm: uses Bound::Excluded(ts+1 key) for finite start keys (CR-01 fix)
  - test_reverse_first_window_existing_key_keeps_all_dups: synthetic 3-dup key proves all dups returned
  - test_reverse_bounded_existing_key_keeps_all_dups: scan_index_bounded Reverse full dup group
  - test_scan_reverse_until_existing_ts_keeps_both_dups: fixture regression for kinds=[1], until=1700000256
affects:
  - 03-query-engine (plan 03-09 k-way merge builds on corrected scan primitives)
  - Phase 04 GraphQL API (uses scan_index_bounded / scan_index_one_window for Reverse queries)

tech-stack:
  added: []
  patterns:
    - "CR-01 ts+1 Excluded bound: Reverse rev_range upper bound built as Bound::Excluded(prefix‖(ts+1).to_le_bytes()) when ts < u64::MAX; keeps Bound::Included when ts == u64::MAX to handle existing unbounded-high scans"
    - "reverse_upper_bound helper: single place decodes trailing 8-byte created_at, computes checked_add(1), returns rebuilt key + is_excluded flag; both Reverse arms share it"
    - "Build synthetic DUPSORT+INTEGERDUP env in unit tests via EnvOpenOptions+DatabaseFlags for TDD regression proofs without integration test file"

key-files:
  created: []
  modified:
    - src/lmdb/scan.rs

key-decisions:
  - "Use Bound::Excluded(ts+1 key) rather than Bound::Included(start_key) for Reverse first-batch: heed 0.22.1 rev_range Included positions at the SMALLEST dup and steps away; Excluded positions ABOVE the boundary landing on the LARGEST dup"
  - "Saturate at u64::MAX: when ts==u64::MAX, checked_add(1) returns None and the Included path is kept — this covers the existing kind_reverse_high_key() pattern used in golden-vector tests"
  - "Do NOT apply ts+1 on resumed windows (first_batch=false): the boundary dup group was fully drained in the prior window; Excluded(resume_key) skips it correctly already"
  - "Forward arms unchanged: forward Bound::Included(start_key) re-walks the full dup group ascending and is correct for NIP-01 since semantics"

patterns-established:
  - "reverse_upper_bound pattern: all Reverse scan arms that accept a finite start key MUST go through this helper to avoid CR-01 data loss"
  - "TDD synthetic env in unit tests: build_synthetic_kind_env helper creates a write-txn env with DUPSORT+INTEGERDUP using Uint64Uint64Cmp; enables unit-test-level proofs without integration test files"

requirements-completed: [QRY-01]

duration: 5min
completed: 2026-06-12
---

# Phase 03 Plan 08: CR-01 Reverse Bound Fix Summary

**CR-01 closed at scan layer: Reverse arms now use Bound::Excluded(ts+1) so rev_range positions above the boundary key and yields ALL dups of the boundary timestamp, not just the smallest**

## Performance

- **Duration:** 5 min
- **Started:** 2026-06-12T08:52:06Z
- **Completed:** 2026-06-12T08:57:47Z
- **Tasks:** 2 (TDD: RED→GREEN for each)
- **Files modified:** 1

## Accomplishments

- Added `reverse_upper_bound(start_key)` private helper that decodes the trailing 8-byte `created_at`, computes `ts.checked_add(1)`, and returns the rebuilt key + `is_excluded` flag
- Fixed `collect_bounded` Reverse arm: replaced `Bound::Included(start_key)` with `Bound::Excluded(ts+1 key)` for finite start keys
- Fixed `collect_window` Reverse `first_batch` arm: same `ts+1 Excluded` construction; resumed windows keep `Excluded(resume_key)` unchanged
- Added 3 regression tests proving the fix: synthetic 3-dup key tests + real fixture regression for kinds=[1], until=1700000256

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: failing CR-01 tests** - `35b076d` (test)
2. **Task 1 GREEN: reverse_upper_bound + Reverse arm fix** - `a8cff5d` (feat)
3. **Task 2: fixture regression test** - `22a88f4` (feat)

## Files Created/Modified

- `src/lmdb/scan.rs` — Added `reverse_upper_bound` helper; fixed `collect_bounded` and `collect_window` Reverse arms; added 3 regression tests + `build_synthetic_kind_env` test helper

## Decisions Made

- Used `Bound::Excluded(ts+1 key)` for Reverse first-batch bound — heed 0.22.1 `rev_range(Bound::Included(K))` positions at the SMALLEST dup of K and steps away; `Excluded(K+1)` positions strictly above K and lands on its LARGEST dup
- Saturating at `u64::MAX`: `checked_add(1)` returns `None` → keep `Included` path (no overflow; `u64::MAX` start keys are unbounded-high scans used by golden-vector tests)
- Forward arms left unchanged: `Bound::Included(start_key)` is correct for forward direction (re-walks full dup group ascending)
- Resumed windows (`first_batch=false`) unchanged: boundary dup group already drained; `Excluded(resume_key)` is already correct

## Deviations from Plan

None — plan executed exactly as written.

The TDD flow worked cleanly: RED tests failed with `[5]` (only smallest dup returned), GREEN fix yielded `[5, 6, 7]` (full dup group). Task 2 fixture test passed immediately on first run after Task 1's fix was applied.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- CR-01 closed at scan layer; `collect_bounded` and `collect_window` Reverse arms are now correct for all finite start keys
- Fixture regression: `test_scan_reverse_until_existing_ts_keeps_both_dups` proves kinds=[1], until=1700000256 returns 5 events including both levId=7 and levId=8
- u64::MAX start keys retain existing behavior (Included path, golden-vector tests pass)
- Plan 03-09 (CR-02/CR-03 k-way merge) can build on corrected scan primitives
- All 84 lib tests + 14 integration tests pass (`cargo test --all-targets` clean)

---
*Phase: 03-query-engine*
*Completed: 2026-06-12*
