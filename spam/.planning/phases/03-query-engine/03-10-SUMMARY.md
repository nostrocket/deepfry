---
phase: 03-query-engine
plan: "10"
subsystem: query
tags: [query, engine, round-loop, pagination, correctness, dos-bound, hardening, wr-03, cr-01-review, verification-truth-5]

requires:
  - phase: 03-query-engine
    plan: "09"
    provides: merge_windowed windowed k-way merge + per-stream since exhaustion
provides:
  - execute_query_internal: bounded round-loop calling merge_windowed from advancing resume boundary; MAX_ROUNDS budget; partial-result cursor when budget-capped with reachable events remaining
  - MAX_ROUNDS: const usize = 8 — round budget constant; total LMDB entries/query bounded by MAX_ROUNDS x emit_limit
  - test_execute_query_residual_deep_match_reachable: regression test proving reachability under kinds=[1]+#e residual filter across pages
  - reverse_upper_bound (hardening): fail-soft guard for sub-8-byte keys returns Included-fallback instead of release-build panic
  - MergeCandidate (hardening): manual PartialEq/Eq over (created_at, lev_id) matching Ord relation — Eq/Ord consistency

affects:
  - 03-query-engine (VERIFICATION truth #5 flipped FAILED → VERIFIED; phase 03 all 5 truths now verified)
  - Phase 04 GraphQL API (consumes execute_query with correct pagination correctness guarantee)

tech-stack:
  added: []
  patterns:
    - "Bounded round-loop: loop { merge_windowed(..) once per round; break on valid.len() >= limit || exhausted || rounds >= MAX_ROUNDS; advance round_boundary = last_merged }"
    - "Exhaustion signal: merge_batch.len() < emit_limit (true exhaustion = merge had fewer entries than asked); captured BEFORE consuming the batch"
    - "Partial-result cursor: build from valid.last() when !exhausted even if valid.len() < limit (budget-capped) — never strand reachable events"
    - "Round boundary rebuild: NostrFilter.until = round_boundary.map(|(ts,_)| ts).unwrap_or(filter.until || u64::MAX); build_start_keys called fresh each round"

key-files:
  created: []
  modified:
    - src/query/engine.rs
    - src/lmdb/scan.rs
    - src/query/merge.rs

key-decisions:
  - "MAX_ROUNDS = 8: round budget preserves WR-03 DoS boundary; total LMDB entries/query bounded by 8 x (2*limit + DEFAULT_WINDOW_SIZE). The break is unconditional — a residual/expiry-heavy filter cannot spin unbounded."
  - "Partial-result cursor: return cursor from valid.last() when !exhausted regardless of valid.len(); omit only when exhausted is true AND valid is under-full (true end of stream or empty)"
  - "round_boundary initialized from cursor_boundary so round 1 reproduces original cursor-start behavior exactly"
  - "emit_limit per round unchanged: limit*2 + DEFAULT_WINDOW_SIZE — DoS bound preserved at the per-round level"

requirements-completed: [QRY-01, QRY-02]

duration: ~20min
completed: 2026-06-13
---

# Phase 03 Plan 10: Bounded Round-Loop + Partial-Result Cursor Summary

**VERIFICATION truth #5 flipped FAILED → VERIFIED: execute_query_internal now loops up to MAX_ROUNDS calling merge_windowed from an advancing resume boundary; a partial-result cursor is returned whenever reachable events remain below the page boundary**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-06-13
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

### Task 1: Bounded round-loop + partial-result cursor (engine.rs, scan.rs, merge.rs)

**Core fix (engine.rs):**
- Added `const MAX_ROUNDS: usize = 8` — round budget constant; total LMDB entries/query bounded by MAX_ROUNDS × emit_limit (= 8 × (2×limit + DEFAULT_WINDOW_SIZE)). Preserves the WR-03 DoS boundary the single-call model held.
- Rewrote `execute_query_internal`: the single `merge_windowed` call is now inside a `loop {}` with `round_boundary: Option<(u64, LevId)>` and `rounds: usize` tracking state.
- Each round: rebuild `NostrFilter.until` from `round_boundary.ts` (or caller's `until`/`u64::MAX` on first round); call `build_start_keys` fresh; call `merge_windowed` once; capture `exhausted = merge_batch.len() < emit_limit` BEFORE consuming; capture `last_merged` BEFORE consuming; run existing cursor-exclusion + hydrate + residual + push logic; `rounds += 1`; break when `valid.len() >= limit || exhausted || rounds >= MAX_ROUNDS`; else advance `round_boundary = last_merged`.
- Partial-result cursor: `else if !valid.is_empty() && !exhausted { build cursor from valid.last() }` — the cursor is returned whenever the budget cap stops the loop with reachable events remaining. `None` only when truly exhausted and under-full (end of stream).
- Updated module header (lines ~6-7), `execute_query` doc's Algorithm section, and the Invariants section to match the real control flow. The 19-line false single-call comment at the prior lines 172-190 is replaced with accurate per-round descriptions.
- No public API signature changes; `latest_per_author` untouched.

**Hardening fix 1 (scan.rs `reverse_upper_bound`):**
- Added fail-soft guard: `if start_key.len() < 8 { return (start_key.to_vec(), false); }` at the top of the helper. This returns the Included-fallback tuple instead of a release-build slice panic/abort on sub-8-byte keys. The `debug_assert!` is retained for dev-build visibility (T-03-PANIC).

**Hardening fix 2 (merge.rs `MergeCandidate`):**
- Removed `#[derive(Eq, PartialEq)]` which compared all 4 fields including `key_bytes`/`stream_idx`.
- Added manual `impl PartialEq` and `impl Eq` over `(created_at, lev_id)` only, matching the `Ord::cmp` relation. `lev_id` is unique per event so this is a valid equivalence. Comment documents the reason.

### Task 2: Regression test `test_execute_query_residual_deep_match_reachable`

- Added `#[test] fn test_execute_query_residual_deep_match_reachable()` in the engine.rs tests module.
- Uses `kinds=[1] + #e=TAG_VALUE_64A` filter with `execute_query_with_batch(batch_size=1)` and `limit=2`.
- Asserts page1 (cursor=None) returns 2 tagged events newest-first AND a non-None cursor (3 matching events exist, only 2 returned).
- Asserts page2 (with page1 cursor) returns the remaining 1 tagged event, non-overlapping with page1.
- Completeness assertion: page1+page2 id union equals all 3 tagged-event ids from a single limit=10 ground-truth query (no stranding).
- Test termination under `cargo test` proves the round-loop budget terminates correctly with batch_size=1.

## Task Commits

1. **Task 1** — `ab59a53` feat(03-10): restore bounded round-loop + partial-result cursor in execute_query_internal
2. **Task 2** — `b5b48de` test(03-10): add test_execute_query_residual_deep_match_reachable regression test

## Files Created/Modified

- `src/query/engine.rs` — Bounded round-loop in execute_query_internal; MAX_ROUNDS constant; partial-result cursor branch; updated module header + doc comments; new regression test
- `src/lmdb/scan.rs` — reverse_upper_bound: fail-soft sub-8-byte guard
- `src/query/merge.rs` — MergeCandidate: manual PartialEq/Eq impl replacing derive

## Decisions Made

- `MAX_ROUNDS = 8`: matches the plan spec; large enough to reach matches 8 rounds deep while keeping the per-query LMDB scan budget bounded.
- `round_boundary` initialized from `cursor_boundary`: ensures round 1 behaves identically to the pre-fix single-call behavior — no regression on existing cursor tests.
- Partial-result cursor condition `!valid.is_empty() && !exhausted`: `!exhausted` is the key gate; if the merge was truly exhausted (all entries seen) and we're still under-full, no more events exist, so `None` is correct. When the budget cap stops us while the merge had more, `Some` prevents stranding.
- `emit_limit` unchanged per round: `limit*2 + DEFAULT_WINDOW_SIZE` — the per-round DoS bound is preserved; the loop multiplies total scan budget by MAX_ROUNDS without changing per-round headroom.

## Deviations from Plan

None — plan executed exactly as written.

All specified changes were applied byte-for-byte to the three target files:
- The loop structure matches the plan's concrete pseudocode.
- The exhaustion signal (`merge_batch.len() < emit_limit`), `last_merged` capture, and round_boundary advancement are exactly as specified.
- The `else if !valid.is_empty() && !exhausted` partial-result cursor branch matches the plan's specification.
- The two hardening items (scan.rs fail-soft, merge.rs manual Eq) are applied as specified.
- Comments updated at engine.rs module header, doc sections, and the former 172-190 block.
- No existing tests modified; 5 plans (03-01..09) untouched.

## Verification

- `cargo build --all-targets` exits 0 (Task 1 acceptance).
- `cargo test --all-targets` exits 0; 90 lib + 16 integration = 106 total, 0 failed (Task 2 acceptance).
  - Previous: 89 lib + 16 integration = 105; new test adds 1 (106 total).
- `grep -c "MAX_ROUNDS" src/query/engine.rs` = 17 (>= 2; constant definition + loop-break + comment uses).
- `grep -v '^#' src/query/engine.rs | grep -c "handles virtually all cases"` = 0 (stale comment removed).
- `grep -n "merge_windowed(" src/query/engine.rs` — one call site at line 226, inside the `loop {}` block.
- `grep -n "else if.*!valid.*!exhausted" src/query/engine.rs` — partial-result cursor branch exists at line 361.
- scan.rs: `if start_key.len() < 8 { return (start_key.to_vec(), false); }` present before any slice index.
- merge.rs: `impl PartialEq for MergeCandidate` and `impl Eq for MergeCandidate {}` present; `#[derive(Eq, PartialEq)]` absent.
- New test `test_execute_query_residual_deep_match_reachable` passes; VERIFICATION truth #5 reachability confirmed.

## Self-Check

- `src/query/engine.rs` modified — verified in git diff and read back
- `src/lmdb/scan.rs` modified — verified in git diff
- `src/query/merge.rs` modified — verified in git diff
- Commits ab59a53 and b5b48de exist in git log

## Self-Check: PASSED

All files modified and committed; `cargo build --all-targets` exits 0; `cargo test --all-targets` 106 passed, 0 failed; all acceptance criteria met.

---
*Phase: 03-query-engine*
*Completed: 2026-06-13*
