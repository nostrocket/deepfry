---
phase: 03-query-engine
verified: 2026-06-13T06:29:32Z
status: gaps_found
score: 4/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 4/5
  gaps_closed:
    - "Previous truth-5 gap (no round-loop): execute_query_internal now loops up to MAX_ROUNDS calling merge_windowed from an advancing resume boundary; MAX_ROUNDS constant defined; partial-result cursor present for !valid.is_empty() && !exhausted case; test_execute_query_residual_deep_match_reachable added and passes."
    - "scan.rs reverse_upper_bound: fail-soft sub-8-byte guard added at line 370."
    - "merge.rs MergeCandidate: manual PartialEq/Eq over (created_at, lev_id) replacing derive — Eq/Ord consistency restored."
  gaps_remaining:
    - "REVIEW CR-01: When MAX_ROUNDS budget exhausts before any survivor accumulates, valid is empty and exhausted is false, but the cursor builder's else-if guard (!valid.is_empty() && !exhausted) is not taken — None is returned. The caller interprets (events: [], cursor: None) as end-of-stream while matching events exist below the budget horizon. Source: engine.rs:361 — requires deepest_scanned fallback cursor."
    - "REVIEW CR-02: round_until drops lev_id from round_boundary (engine.rs:208-210: .map(|(ts, _)| ts)). When a single created_at second holds >= emit_limit (>= 256) entries, each round re-scans the same prefix; cursor-exclusion drops all of it; last_merged never decreases; loop spins to MAX_ROUNDS making zero progress — stranding everything below that timestamp."
  regressions: []
gaps:
  - truth: "Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary, and pagination converges without stalling"
    status: failed
    reason: "Two independent stranding defects in the 03-10 round-loop survive in the actual code. REVIEW CR-01: the partial-result cursor is gated on !valid.is_empty() (engine.rs:361), so when the budget cap fires before any survivor accumulates, valid is empty, !exhausted is true, but the else-if is not taken and None is returned — false end-of-stream. REVIEW CR-02: round_until discards lev_id from round_boundary (engine.rs:208-210, .map(|(ts, _)| ts)), so when >= emit_limit events share one created_at, every round re-emits the same prefix, cursor-exclusion drops all of it, last_merged does not decrease, and the loop stalls to MAX_ROUNDS stranding everything below. The existing test (test_execute_query_residual_deep_match_reachable) passes because the 11-event fixture has matching events spread across different timestamps and the round budget is never exhausted with zero survivors — the test does not cover the empty-valid case."
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Lines 361: else if !valid.is_empty() && !exhausted — partial-result cursor omitted when valid IS empty and !exhausted (REVIEW CR-01). Lines 208-210: round_until = round_boundary.map(|(ts, _)| ts) drops lev_id, causing fat-timestamp re-scan stall (REVIEW CR-02)."
    missing:
      - "REVIEW CR-01 fix: track deepest_scanned: Option<(u64, LevId)> = None inside the loop; update it from last_merged each round; add new cursor-builder branch: else if valid.is_empty() && !exhausted { deepest_scanned.map(|(ts, lev_id)| PageCursor { created_at: ts, lev_id }) }. This ensures a resume cursor is returned even when all survivors were filtered before any accumulated."
      - "REVIEW CR-02 fix: detect no-progress condition (last_merged == round_boundary) and break rather than spinning; OR thread lev_id into the merge upper bound so progress is monotonic within a fat timestamp."
      - "Add regression test for REVIEW CR-01: construct a synthetic env with matching events only BEYOND MAX_ROUNDS x emit_limit non-matching entries; assert that page 1 returns (events: [], cursor: Some(...)) rather than (events: [], cursor: None)."
      - "Add regression test for REVIEW CR-02: construct a synthetic env with >= emit_limit events at one created_at; assert full pagination with a limit smaller than that group returns all events below that timestamp."
human_verification: []
---

# Phase 3: Query Engine Verification Report (Re-verification 3)

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-13T06:29:32Z
**Status:** gaps_found
**Re-verification:** Yes — third verification; reviewing 03-10 gap-closure and 03-REVIEW.md code review findings

## Re-verification Mode: Gap-Closure Review

Previous VERIFICATION.md (second verification, score 4/5) had one remaining gap: no round-loop in execute_query_internal. Plan 03-10 executed and claimed to close it. A code review (03-REVIEW.md, committed 2026-06-13 14:26 +0800, AFTER 03-10) found 2 BLOCKER defects in the 03-10 implementation.

**Focus:** Full 3-level verification on the failed truth #5 (cursor pagination); quick regression-check on the 4 previously-passing truths.

### Previously Passing Truths — Regression Check

All four truths that scored VERIFIED in the previous verification remain stable. The 03-10 commit modified only engine.rs (round-loop rewrite), scan.rs (reverse_upper_bound fail-soft), and merge.rs (MergeCandidate Eq/Ord). No changes to router.rs, hydrate.rs, filter.rs, or the index-selection logic. The existing 89 lib tests plus 16 integration tests (all green in the prior run) are now 90 lib + 16 integration = 106 total, all passing.

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A NostrFilter routes to the most selective index per D-02 priority order (ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; tag→Event__tag; empty→Event__created_at) | ✓ VERIFIED | `select_index()` in router.rs implements the 6-arm priority chain. Tests: `test_select_index_all_six_arms`, `test_select_index_ids_highest_priority`, `test_execute_query_kinds_routing_and_order`. Unchanged by 03-10. |
| 2 | A tag filter (Event__tag) returns events matching the given tag name and values with NIP-01 AND semantics across distinct fields | ✓ VERIFIED | `tags_filter.iter().all(...)` AND residual in engine.rs:310-321. Tests: `test_execute_query_multi_tag_and_semantics`, `test_execute_query_tag_values_or_within_field`, `test_execute_query_single_tag_still_matches`. Unchanged by 03-10. |
| 3 | `latestPerAuthor` returns the latest N events per pubkey via Event__pubkeyKind prefix scans | ✓ VERIFIED | `latest_per_author()` in engine.rs:399-488. Tests: `test_latest_per_author_two_buckets`, `test_latest_per_author_per_author_one`, `test_latest_per_author_no_matching_events`. Untouched by 03-10. |
| 4 | Events with expiration != 0 and expiration <= now are excluded from all query results at query time | ✓ VERIFIED | `is_expired()` applied in both execute_query_internal (line 273) and latest_per_author (line 478). Test: `test_is_expired_predicate`. Unchanged by 03-10. |
| 5 | Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary, and pagination converges without stalling | ✗ FAILED | 03-10 added the round-loop and partial-result cursor, but two data-stranding defects survive in the actual code per 03-REVIEW.md. See detail below. |

**Score: 4/5 truths verified** (unchanged from previous verification)

### What 03-10 Fixed (Confirmed)

03-10 genuinely closed the previous gap (no round-loop). The following are verified present in the actual code:

- `const MAX_ROUNDS: usize = 8` defined at engine.rs:46.
- `loop {}` with `round_boundary`, `rounds`, `exhausted`, `last_merged` tracking at engine.rs:203-340.
- `merge_windowed` called once per round inside the loop at engine.rs:226.
- Partial-result cursor branch `else if !valid.is_empty() && !exhausted` at engine.rs:361.
- `rounds >= MAX_ROUNDS` unconditional break at engine.rs:333.
- `reverse_upper_bound` fail-soft `if start_key.len() < 8 { return (start_key.to_vec(), false); }` at scan.rs:370-372.
- Manual `impl PartialEq for MergeCandidate` and `impl Eq for MergeCandidate` over `(created_at, lev_id)` at merge.rs:57-63.
- `test_execute_query_residual_deep_match_reachable` test at engine.rs:1551-1675 — passes.
- Full test suite: 90 lib + 16 integration = 106 total, 0 failed.

### What 03-REVIEW.md Found in the 03-10 Code (Confirmed in Actual Source)

The review was committed AFTER 03-10 (03-10 at 14:15, review at 14:26 on 2026-06-13). Both defects are independently confirmed by reading the source code.

#### REVIEW CR-01 Confirmed — BLOCKER

**Location:** engine.rs:356-370 (cursor-builder block)

```
let next_cursor = if valid.len() == limit {
    ...
} else if !valid.is_empty() && !exhausted {   // guard: !valid.is_empty()
    ...
} else {
    None   // taken when valid IS empty, even if !exhausted
};
```

When `MAX_ROUNDS` is hit before any survivor accumulates (`valid` is empty, `exhausted` is false), the `else if !valid.is_empty()` guard is NOT satisfied (valid IS empty), so execution falls to `else { None }`. The caller receives `(events: [], cursor: None)` and correctly interprets this as end-of-stream. But `!exhausted` means the merge still had `emit_limit` entries available below the last scanned position. Every matching event below the budget horizon is permanently stranded and unreachable.

**Test coverage gap:** `test_execute_query_residual_deep_match_reachable` uses a 11-event fixture with 3 matching events scattered across timestamps and batch_size=1. With MAX_ROUNDS=8 rounds, the loop collects at least 1 survivor before exhausting the budget — the empty-valid scenario never triggers. The test passes but does not exercise the defect.

**Triggering condition:** A production relay where the routed index is large and residual filter drop rate is high enough that the first MAX_ROUNDS x emit_limit scanned entries yield zero survivors.

#### REVIEW CR-02 Confirmed — BLOCKER

**Location:** engine.rs:208-210 (round_until construction)

```rust
let round_until: u64 = round_boundary
    .map(|(ts, _)| ts)      // lev_id is DROPPED
    .unwrap_or_else(|| filter.until.unwrap_or(u64::MAX));
```

`lev_id` is discarded from `round_boundary`. The merge re-scans every entry at `ts == round_until` each round because `round_until` is timestamp-granular only. The BinaryHeap is `(created_at DESC, lev_id DESC)`, so the first `emit_limit` entries are deterministically the highest-lev_id events at that timestamp — identical every round when a single timestamp holds `>= emit_limit` events (emit_limit = 2*limit + DEFAULT_WINDOW_SIZE = minimum 256).

Round N returns emit_limit triples all at `ts_L`. Round N+1 rebuilds with `round_until = ts_L`, re-scans the same prefix, cursor-exclusion drops all of them (all have `lev_id >= cur_lev` from the previous round's boundary). `last_merged` equals `round_boundary`. The loop makes zero progress and exits at MAX_ROUNDS. Every event below `ts_L` is permanently unreachable.

**Triggering condition:** A relay with >= 256 events at the same `created_at` second — plausible during bulk import or on a high-traffic relay.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/engine.rs` | Bounded round-loop; MAX_ROUNDS; partial-result cursor on budget cap; comments match code | ✓ VERIFIED (structure) / ✗ PARTIAL (cursor correctness) | Round-loop and MAX_ROUNDS present; partial-result cursor present but gated on !valid.is_empty() — REVIEW CR-01 stranding when valid is empty. round_until drops lev_id — REVIEW CR-02 fat-timestamp stall. |
| `src/query/engine.rs` | test_execute_query_residual_deep_match_reachable | ✓ VERIFIED | Test exists at line 1551; passes; confirms the common-case cursor behavior (valid non-empty). Does NOT cover empty-valid budget-cap scenario. |
| `src/lmdb/scan.rs` | reverse_upper_bound fail-soft sub-8-byte guard | ✓ VERIFIED | `if start_key.len() < 8 { return (start_key.to_vec(), false); }` at line 370-372. |
| `src/query/merge.rs` | MergeCandidate manual PartialEq/Eq over (created_at, lev_id) | ✓ VERIFIED | impl PartialEq and impl Eq at merge.rs:57-63; derive removed. |
| `src/query/filter.rs` | NostrFilter, TagFilter, PageCursor, QueryError | ✓ VERIFIED | Unchanged from previous verification; all types present. |
| `src/query/router.rs` | select_index + build_start_keys + created_at_from_key | ✓ VERIFIED | Unchanged from previous verification. |
| `src/query/hydrate.rs` | hydrate_lev_ids returning Vec<(LevId, DecodedEvent)> | ✓ VERIFIED | Unchanged from previous verification. |
| `src/query/mod.rs` | All 5 submodules declared | ✓ VERIFIED | Unchanged from previous verification. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `execute_query_internal` | `merge_windowed` | loop call at engine.rs:226 | ✓ WIRED | One call per round, inside `loop {}` block. |
| `execute_query_internal` | `PageCursor next_cursor` | `valid.last()` at engine.rs:357/364 | ✓ PARTIAL | Cursor built from valid.last() when !valid.is_empty() && !exhausted; REVIEW CR-01: no cursor when valid IS empty && !exhausted. |
| `round_boundary` | `round_until` | engine.rs:208-210 | ✗ DEFECTIVE | lev_id discarded — REVIEW CR-02 fat-timestamp stall. |
| Other links (routing, hydration, expiry, scan) | Unchanged from previous verification | ✓ WIRED | Not modified by 03-10. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `execute_query_internal` | `valid` Vec | merge_windowed → scan_index_one_window → EventPayload LMDB | Yes — real LMDB scans; 106 tests confirm | ✓ FLOWING |
| `latest_per_author` | per-author bucket | scan_index_bounded → EventPayload LMDB | Yes — unchanged from previous verification | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full lib + integration suite | `cargo test --all-targets` | 90 lib + 16 integration = 106 total, 0 failed | ✓ PASS |
| Round-loop MAX_ROUNDS constant present | `grep -c "MAX_ROUNDS" src/query/engine.rs` | 17 (definition + loop break + comment uses) | ✓ PASS |
| merge_windowed called inside loop | `grep -n "merge_windowed(" src/query/engine.rs` | Line 226, inside `loop {}` block | ✓ PASS |
| Stale single-call comment absent | `grep "handles virtually all cases" src/query/engine.rs` | No match | ✓ PASS |
| Partial-result cursor present | `grep "else if.*valid.*exhausted" src/query/engine.rs` | Line 361: `else if !valid.is_empty() && !exhausted` | ✓ PASS (but REVIEW CR-01 guard incomplete) |
| REVIEW CR-01: empty-valid budget-cap → cursor None | Read engine.rs:356-370: `else { None }` taken when valid IS empty regardless of exhausted | Confirmed — (events: [], cursor: None) returned for empty-valid + !exhausted | ✗ FAIL (BLOCKER) |
| REVIEW CR-02: round_until drops lev_id | Read engine.rs:208-210: `.map(|(ts, _)| ts)` | Confirmed — lev_id discarded; fat-timestamp stall real | ✗ FAIL (BLOCKER) |
| Regression test passes | `cargo test --lib query::engine::tests::test_execute_query_residual_deep_match_reachable` | PASS | ✓ PASS (but does not cover empty-valid case) |

### Probe Execution

No probes declared. Step 7c: SKIPPED.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| QRY-01 | 03-01 through 03-10 | events() resolved by scanning most selective index | PARTIAL | Index selection, routing, k-way ordering, NIP-40, and round-loop all implemented and tested; REVIEW CR-01/CR-02 cursor stranding defects remain — pagination correctness not complete |
| QRY-02 | 03-02, 03-04, 03-07 | Tag filters via Event__tag with NIP-01 AND semantics | SATISFIED | AND residual; 5 regression tests pass |
| QRY-03 | 03-04 | latestPerAuthor via Event__pubkeyKind | SATISFIED | latest_per_author() implemented and fixture-tested |
| QRY-04 | 03-03, 03-05 | Hydrate EventPayload[levId] | SATISFIED | hydrate_lev_ids returns lev_id-associated pairs; 4 tests pass |
| QRY-05 | 03-04 | NIP-40 expiration filtering | SATISFIED | is_expired() applied in both query paths; unit test passes |

**Documentation note:** REQUIREMENTS.md marks QRY-03 and QRY-05 as "Pending" — stale tracking, implementations are present and tested.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `src/query/engine.rs` | 361 | `else if !valid.is_empty() && !exhausted` — partial-result cursor gated on non-empty valid; empty-valid + !exhausted falls to None | BLOCKER | REVIEW CR-01: false end-of-stream when budget cap fires before any survivor |
| `src/query/engine.rs` | 208-210 | `round_boundary.map(|(ts, _)| ts)` — lev_id discarded from round boundary | BLOCKER | REVIEW CR-02: fat-timestamp stall and zero-progress loop when >= emit_limit events at same created_at |

### Human Verification Required

None — all items resolved programmatically.

## Gaps Summary

### Progress Since Previous Verification

Plan 03-10 genuinely closed the structural gap: execute_query_internal now loops up to MAX_ROUNDS calling merge_windowed from an advancing resume boundary. The round-loop, MAX_ROUNDS constant, partial-result cursor branch, scan.rs fail-soft, and merge.rs Eq/Ord fix are all confirmed present and correct. The regression test passes and the full 106-test suite is green.

### Remaining Blockers

Two defects introduced by the 03-10 round-loop rewrite survive, both found by 03-REVIEW.md (committed after 03-10). Both cause silent data stranding — the worst failure mode for a correctness-first query engine.

**REVIEW CR-01 — Empty-valid budget-cap → false end-of-stream:**

The partial-result cursor branch at engine.rs:361 (`else if !valid.is_empty() && !exhausted`) is not satisfied when `valid` is empty. When MAX_ROUNDS fires before any survivor accumulates, the function returns `(events: [], cursor: None)`. Callers conclude the stream ended. Matching events below the budget horizon are permanently unreachable.

Fix: track the deepest merge position scanned (independent of whether any survivors were collected) and return a resume cursor from that position when `!exhausted`, even when `valid` is empty.

**REVIEW CR-02 — Fat-timestamp stall:**

`round_until` at engine.rs:208-210 drops `lev_id` from `round_boundary`. When a single `created_at` second holds `>= emit_limit` (minimum 256) events, every round re-scans the same timestamp prefix, cursor-exclusion drops all of it, `last_merged` does not change, and the loop spins to MAX_ROUNDS making zero forward progress. Everything below that timestamp is permanently unreachable.

Fix: detect no-progress (`last_merged == round_boundary`) and break rather than spinning; or thread `lev_id` into the scan resume bound so scans advance strictly within a fat timestamp.

Both defects are production-reachable on real relay data. Neither causes an error — both cause silent truncation. The phase goal text includes "cursor pagination" and these defects falsify the "correct cursor pagination" property.

---

_Verified: 2026-06-13T06:29:32Z_
_Verifier: Claude (gsd-verifier)_
