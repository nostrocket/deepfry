---
phase: 03-query-engine
verified: 2026-06-13T08:45:00Z
status: gaps_found
score: 4/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 4/5
  gaps_closed:
    - "REVIEW CR-01 (empty-valid budget-cap → false end-of-stream): deepest_scanned: Option<(u64, LevId)> declared before loop; updated from last_merged each round before break-decision; new cursor branch 'else if valid.is_empty() && !exhausted { deepest_scanned.map(...) }' present at engine.rs:432-439. Regression test test_execute_query_budget_cap_empty_valid_returns_cursor passes and exercises the actual empty-valid path (2200 non-matching events exhaust 8 rounds, matching events at lower timestamps are reachable via cursor)."
  gaps_remaining:
    - "REVIEW CR-02 (fat-group events beyond emit_limit stranded): the ts-advance override (deepest_scanned = (stalled_ts - 1, u64::MAX)) silently abandons lev_id positions [1 .. FAT_COUNT - emit_limit] at stalled_ts when FAT_COUNT > emit_limit. These events are in the (created_at DESC, lev_id DESC) ordering at positions strictly below the page boundary — matching events that remain below the page boundary — and are permanently unreachable. The regression test test_execute_query_fat_timestamp_pagination_no_stall is pinned at FAT_COUNT = emit_limit = 260 exactly, so the merge floor lands at lev_id 1 and no events sit below the reachable window. The test does not exercise FAT_COUNT > emit_limit and provides false assurance."
    - "ts=0 non-termination: when stalled_ts == 0 the 'else' no-op at engine.rs:388-389 leaves deepest_scanned unchanged at (0, floor_lev). A fat group at created_at=0 larger than emit_limit causes cross-call non-termination: each page returns cursor: Some((0, floor_lev)) — an identical non-advancing cursor — and a caller paginating until cursor==None loops forever."
  regressions: []
gaps:
  - truth: "Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary, and pagination converges without stalling"
    status: failed
    reason: "03-11's CR-02 fix introduced a ts-advance override (deepest_scanned = (stalled_ts - 1, u64::MAX)) that silently abandons all events at stalled_ts with lev_id in [1 .. FAT_COUNT - emit_limit] when FAT_COUNT > emit_limit. These events are at positions strictly below the current page boundary (in (created_at DESC, lev_id DESC) order, (stalled_ts, 40) < (stalled_ts, 41)) and are matching events that remain below the page boundary — but they receive cursor: None (after the BELOW events are drained) rather than a resume cursor. Silent truncation: the caller observes a false end-of-stream. The regression test test_execute_query_fat_timestamp_pagination_no_stall is constructed with FAT_COUNT = emit_limit = 260 exactly; at this pin, the merge floor is lev_id=1 and the ts-advance fires only after the group is fully drained — no events are abandoned. The test passes but does not cover the defective case (FAT_COUNT > emit_limit). The 03-REVIEW.md proves this empirically: FAT_COUNT=300 > emit_limit=260 yields 265 of 305 events — 40 stranded."
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Lines 379-392: last_merged == round_boundary no-progress break + ts-advance override sets deepest_scanned = (stalled_ts - 1, u64::MAX), skipping past the entire fat timestamp and permanently stranding lev_ids [1 .. FAT_COUNT - emit_limit] at stalled_ts. Comment at lines 370-372 rationalizes this as 'architecturally unreachable' — incorrect: the limitation is this engine's timestamp-only round_until, not a property of LMDB DUPSORT."
      - path: "src/query/engine.rs"
        issue: "Lines 384-389: stalled_ts == 0 guard prevents underflow panic but the else no-op leaves deepest_scanned at (0, floor_lev) unchanged; a fat group at created_at=0 larger than emit_limit causes cross-call non-termination — each page returns the identical cursor: Some((0, floor_lev)) and never terminates."
      - path: "src/query/engine.rs"
        issue: "Line 2010: const FAT_COUNT: u64 = 260 // = emit_limit — regression test pinned at FAT_COUNT == emit_limit; does not exercise FAT_COUNT > emit_limit where the stranding occurs."
    missing:
      - "Fix the ts-advance override to NOT silently drop events: either (a) return cursor: Some((stalled_ts, floor_lev)) pointing within the fat group so subsequent pages can resume below floor_lev via lev_id-aware scan positioning, OR (b) if intra-group resume is genuinely not implementable in this version, return a QueryError::FatGroupTruncated (fail-closed: surface the truncation rather than claiming completeness), NOT a false-EOF cursor: None."
      - "Fix ts=0 non-termination: at stalled_ts == 0, set deepest_scanned = None (force EOF deterministically) instead of the current no-op that re-emits the identical cursor. Add a regression test with a fat group at created_at=0 larger than emit_limit asserting pagination terminates within a bounded page count."
      - "Replace the rigged regression test: change FAT_COUNT to emit_limit + N (e.g. 300) and assert all FAT_COUNT + BELOW_COUNT events are returned. This test must FAIL on the current code (confirming it exercises the defect path) and PASS after the fix."
      - "Remove or correct the false architectural claim at engine.rs:370-372 ('architecturally unreachable: lev_id is a DUPSORT value, not part of the key, so per-value positioning within a dup group is not possible via key-range-only scans'). This claim is inaccurate: strfry's own resume mechanism encodes lev_id into the resume position; the limitation is this engine's round_until being timestamp-only. The comment launders a fixable implementation limit into a hard invariant."
human_verification: []
---

# Phase 3: Query Engine Verification Report (Re-verification 4)

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-13T08:45:00Z
**Status:** gaps_found
**Re-verification:** Yes — fourth verification; reviewing 03-11 gap-closure against 03-REVIEW.md findings

## Re-verification Mode: Gap-Closure Review

Previous VERIFICATION.md (third verification, score 4/5) had two gaps: REVIEW CR-01 (empty-valid budget-cap → false EOF) and REVIEW CR-02 (fat-timestamp stall). Plan 03-11 executed and claimed to close both. A deep code review (03-REVIEW.md, committed 2026-06-13 after 03-11) found that 03-11's CR-02 fix introduced a new stranding defect by DEVIATING from the plan's specified pure no-progress break.

**Focus:** Full 3-level verification on truth #5 (cursor pagination), with direct code inspection to independently verify the review's claims.

### Summary of 03-11 Changes (Confirmed in Actual Code)

03-11 modified only `src/query/engine.rs`. Changes confirmed present:

- `deepest_scanned: Option<(u64, LevId)>` declared at engine.rs:218
- `deepest_scanned` updated from `last_merged` each round at engine.rs:349-351 (before break-decision)
- CR-01 cursor branch `else if valid.is_empty() && !exhausted` at engine.rs:432-439 returning `deepest_scanned.map(...)`
- CR-02 no-progress break `if last_merged == round_boundary` at engine.rs:379-392
- ts-advance override: `deepest_scanned = Some((stalled_ts - 1, u64::MAX))` at engine.rs:385-386
- ts=0 guard `if stalled_ts > 0` at engine.rs:385; else no-op at engine.rs:388-389
- Regression test `test_execute_query_budget_cap_empty_valid_returns_cursor` at engine.rs:1880
- Regression test `test_execute_query_fat_timestamp_pagination_no_stall` at engine.rs:2003
- Comment at engine.rs:370-372 asserting events beyond emit_limit are "architecturally unreachable"

`grep -c "deepest_scanned" src/query/engine.rs` = 21 (>= 3 required).
`grep -c "last_merged == round_boundary" src/query/engine.rs` = 4 (>= 1 required).
`merge.rs` and `scan.rs` unmodified (confirmed in git diff).

### Previously Passing Truths — Regression Check

Truths #1-4 are unchanged from prior verifications. 03-11 modified only engine.rs, touching only `execute_query_internal` and the test module. No changes to routing, hydration, tag/expiry residual, or `latest_per_author`. Full test suite: 92 lib + 16 integration = 108 total, 0 failed.

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A NostrFilter routes to the most selective index per D-02 priority order (ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; tag→Event__tag; empty→Event__created_at) | ✓ VERIFIED | `select_index()` in router.rs implements the 6-arm priority chain. Tests: `test_select_index_all_six_arms`, `test_select_index_ids_highest_priority`, `test_execute_query_kinds_routing_and_order`. Unchanged by 03-11. |
| 2 | A tag filter (Event__tag) returns events matching the given tag name and values with NIP-01 AND semantics across distinct fields | ✓ VERIFIED | `tags_filter.iter().all(...)` AND residual in engine.rs:327-338. Tests: `test_execute_query_multi_tag_and_semantics`, `test_execute_query_tag_values_or_within_field`. Unchanged by 03-11. |
| 3 | `latestPerAuthor` returns the latest N events per pubkey via Event__pubkeyKind prefix scans | ✓ VERIFIED | `latest_per_author()` in engine.rs:448-537. Tests: `test_latest_per_author_two_buckets`, `test_latest_per_author_per_author_one`, `test_latest_per_author_no_matching_events`. Untouched by 03-11. |
| 4 | Events with expiration != 0 and expiration <= now are excluded from all query results at query time | ✓ VERIFIED | `is_expired()` applied in both execute_query_internal (engine.rs:290) and latest_per_author (engine.rs:528). Test: `test_is_expired_predicate`. Unchanged by 03-11. |
| 5 | Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary, and pagination converges without stalling | ✗ FAILED | CR-01 (empty-valid budget-cap) is FIXED. CR-02 no-progress break is PRESENT but the ts-advance override silently strands events within the fat timestamp group when FAT_COUNT > emit_limit. Regression test is rigged at FAT_COUNT == emit_limit = 260, masking the defect. ts=0 non-termination bug also present. See detail below. |

**Score: 4/5 truths verified** (unchanged from previous verification)

## Detailed Findings

### REVIEW CR-01 (Empty-Valid Budget-Cap) — FIXED

The `deepest_scanned` fallback cursor is confirmed present and correct. The CR-01 regression test exercises the actual defect path:

- 2200 non-matching events exhaust all 8 MAX_ROUNDS rounds with 0 survivors
- `valid` is empty, `exhausted` is false (3 matching events remain below)
- `else if valid.is_empty() && !exhausted` branch at engine.rs:432 is taken
- Returns `Some(deepest_scanned)` — a valid resume cursor
- Subsequent pages follow the cursor and surface all 3 matching events (no stranding)
- Test `test_execute_query_budget_cap_empty_valid_returns_cursor`: PASS

This gap is closed. No defect remains in the CR-01 path.

### New Defect: ts-Advance Override Strands Fat-Group Events (BLOCKER)

**Location:** engine.rs:379-392 (the no-progress break + ts-advance override)

**Defect:** When `last_merged == round_boundary` (no-progress stall), the override at lines 384-386 sets `deepest_scanned = Some((stalled_ts - 1, u64::MAX))`. This skips past the ENTIRE fat timestamp. When `FAT_COUNT > emit_limit`, events at `stalled_ts` with lev_id in `[1 .. FAT_COUNT - emit_limit]` are permanently unreachable. These events are at positions strictly below the page boundary in `(created_at DESC, lev_id DESC)` ordering — (stalled_ts, 40) < (stalled_ts, 41) — and constitute "matching events that remain below the page boundary" in the truth statement.

**How the defect manifests:**

With `FAT_COUNT=300`, `emit_limit=260`, `limit=2`:
1. Pages 1..130: cursor descends from `(FAT_TS, 300)` to `(FAT_TS, 41)` — 260 events emitted across pages via normal pagination
2. Page 131: cursor=`(FAT_TS, 41)`, merge returns lev_ids 300..41 (top 260 of the dup group, same entries as always because `round_until` is timestamp-only and re-scans from the dup group top). All 260 are cursor-excluded (`lev_id >= 41`). `last_merged = (FAT_TS, 41) = round_boundary`.
3. No-progress break fires. Override: `deepest_scanned = Some((FAT_TS - 1, u64::MAX))`. The page returns `cursor: Some((FAT_TS - 1, u64::MAX))`.
4. Lev_ids 1..40 at FAT_TS are permanently abandoned — they are never scanned again because subsequent pages use `round_until = FAT_TS - 1`.

**Probe evidence from 03-REVIEW.md:** FAT_COUNT=300, BELOW_COUNT=5, TOTAL=305. Full cursor pagination to None collects 265 unique events — `emit_limit (260) + below (5)` — not 305. 40 fat-group events stranded. Reviewed independently and confirmed by code trace.

**Why the regression test does not catch this:**

The test at engine.rs:2010 pins `const FAT_COUNT: u64 = 260; // = emit_limit`. With FAT_COUNT == emit_limit exactly, the merge floor is lev_id=1. After pages 1..130, cursor=`(FAT_TS, 1)`. All 260 fat-ts events are cursor-excluded. `last_merged = (FAT_TS, 1) = round_boundary`. No-progress break fires. Override: `deepest_scanned = (FAT_TS - 1, u64::MAX)`. But lev_id=1 IS the floor — no events exist below position 1. The override is correct in this pinned case because the group was genuinely drained before the stall fired. The test comment acknowledges the pin is deliberate: "= emit_limit; triggers the stall after all fat-ts events emitted." Green suite is misleading.

**Comment inaccuracy:** engine.rs:370-372 asserts "architecturally unreachable: lev_id is a DUPSORT value, not part of the key, so per-value positioning within a dup group is not possible via key-range-only scans." This is inaccurate — strfry's own resume mechanism encodes lev_id into the resume position. The limitation is this engine's timestamp-only `round_until`, not a hard constraint of LMDB DUPSORT. The comment launders a fixable implementation limit into a claimed architectural invariant.

### Secondary Defect: ts=0 Non-Termination

**Location:** engine.rs:384-389

```rust
if let Some((stalled_ts, _)) = last_merged {
    if stalled_ts > 0 {
        deepest_scanned = Some((stalled_ts - 1, u64::MAX));
    }
    // stalled_ts == 0: no-op — deepest_scanned unchanged at (0, floor_lev)
}
break;
```

**Defect:** When `stalled_ts == 0`, `deepest_scanned` is left at `(0, floor_lev)`. The page returns `cursor: Some((0, floor_lev))`. On the next call, `round_boundary = (0, floor_lev)`, the merge re-scans the same top-emit_limit entries, cursor-exclusion drops all, `last_merged == round_boundary` again. The override is skipped again (`stalled_ts == 0`). Identical cursor returned forever. A caller paginating until `cursor == None` loops without bound across calls.

**Trigger:** A fat group at `created_at=0` larger than emit_limit. `created_at=0` is not a valid Nostr timestamp, but the engine accepts arbitrary `u64` values from the on-disk index. A corrupt index entry or crafted cursor can trigger this. Within a single `execute_query` call MAX_ROUNDS still holds; the danger is cross-call divergence violating the D-11 cursor-convergence invariant.

**Severity:** BLOCKER — the D-11 invariant (pagination always terminates) is violated for a production-reachable (if uncommon) input.

### Root-Cause Architectural Note (for Next Planning Cycle)

The cursor is `(created_at, lev_id)` and `lev_id` is the LMDB DUPSORT value — not part of the key. `build_start_keys` positions scans by `created_at` only; it cannot position within a dup group by lev_id. The `merge_windowed` window is capped at `emit_limit` entries, so a single `created_at` second holding more than `emit_limit` events cannot be fully paginated within a single merge call. Any real fix must address this: either (a) encode lev_id into the reverse scan resume position (mirroring strfry's resume model — this IS possible, the comment at 370-372 is wrong to call it architecturally impossible), or (b) if not feasible in v1, return a diagnostic error instead of silently truncating. The ts-advance override is a symptom-relocation that sacrifices data correctness to avoid an infinite loop.

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/engine.rs` | execute_query_internal with deepest_scanned fallback cursor (CR-01) and no-progress break (CR-02) | ✓ VERIFIED (structure) / ✗ DEFECTIVE (ts-advance override) | deepest_scanned and no-progress break present; ts-advance override abandons fat-group events when FAT_COUNT > emit_limit; ts=0 guard is a no-op not an EOF |
| `src/query/engine.rs` | test_execute_query_budget_cap_empty_valid_returns_cursor (CR-01 regression) | ✓ VERIFIED | Exercises actual defect path; correctly constructed; PASS |
| `src/query/engine.rs` | test_execute_query_fat_timestamp_pagination_no_stall (CR-02 regression) | ✗ STUB | Pinned at FAT_COUNT=emit_limit=260; does not exercise FAT_COUNT > emit_limit; provides false assurance |
| `src/lmdb/scan.rs` | reverse_upper_bound fail-soft | ✓ VERIFIED | Unchanged from previous verification |
| `src/query/merge.rs` | MergeCandidate manual PartialEq/Eq | ✓ VERIFIED | Unchanged from previous verification |
| `src/query/filter.rs` | NostrFilter, TagFilter, PageCursor, QueryError | ✓ VERIFIED | Unchanged |
| `src/query/router.rs` | select_index + build_start_keys | ✓ VERIFIED | Unchanged |
| `src/query/hydrate.rs` | hydrate_lev_ids | ✓ VERIFIED | Unchanged |

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `execute_query_internal round-loop` | `deepest_scanned update` | `if let Some(lm) = last_merged { deepest_scanned = Some(lm); }` at engine.rs:349-351 | ✓ WIRED | Updated before break-decision each round |
| `execute_query_internal round-loop` | `no-progress break` | `if last_merged == round_boundary` at engine.rs:379 | ✓ WIRED | Present; fires correctly on fat-timestamp stall |
| `no-progress break` | `ts-advance override` | `deepest_scanned = Some((stalled_ts - 1, u64::MAX))` at engine.rs:386 | ✗ DEFECTIVE | Override skips past entire fat timestamp; strands FAT_COUNT - emit_limit events when FAT_COUNT > emit_limit |
| `no-progress break stalled_ts==0` | `EOF` | `else { /* no-op */ }` at engine.rs:388-389 | ✗ DEFECTIVE | No-op leaves deepest_scanned unchanged; cross-call non-termination for ts=0 fat groups |
| `next_cursor builder` | `CR-01 branch` | `else if valid.is_empty() && !exhausted` at engine.rs:432 | ✓ WIRED | Correctly wired; returns deepest_scanned.map(...) |

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full lib + integration suite | `cargo test --all-targets` | 92 lib + 16 integration = 108 total, 0 failed | ✓ PASS |
| CR-01 regression test | `cargo test --lib query::engine::tests::test_execute_query_budget_cap_empty_valid_returns_cursor` | PASS | ✓ PASS |
| CR-02 regression test | `cargo test --lib query::engine::tests::test_execute_query_fat_timestamp_pagination_no_stall` | PASS (but rigged — FAT_COUNT=emit_limit) | ✓ PASS (rigged) |
| deepest_scanned present | `grep -c "deepest_scanned" src/query/engine.rs` | 21 | ✓ PASS |
| no-progress break present | `grep -c "last_merged == round_boundary" src/query/engine.rs` | 4 | ✓ PASS |
| CR-01 cursor branch present | `grep -n "else if valid.is_empty.*exhausted" src/query/engine.rs` | Line 432 | ✓ PASS |
| ts-advance override strands events (FAT_COUNT > emit_limit) | Code trace: FAT_COUNT=300, emit_limit=260 → lev_ids 1..40 at stalled_ts abandoned after ts-advance | Confirmed by code trace + 03-REVIEW.md probe evidence (265 of 305 events collected) | ✗ FAIL (BLOCKER) |
| ts=0 no-progress break → non-termination | Code trace: stalled_ts=0, else no-op at engine.rs:388-389 → identical cursor re-emitted each call | Confirmed by code trace | ✗ FAIL (BLOCKER) |
| CR-02 test covers FAT_COUNT > emit_limit | `grep "FAT_COUNT" src/query/engine.rs` | Line 2010: `const FAT_COUNT: u64 = 260; // = emit_limit` — pinned at threshold | ✗ FAIL (rigged test) |

## Probe Execution

No probe scripts declared or found. Step 7c: SKIPPED.

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| QRY-01 | 03-01 through 03-11 | events() resolved by scanning most selective index | PARTIAL | Index selection, routing, k-way ordering, NIP-40, round-loop all implemented; CR-01 fixed; CR-02 no-progress break present but ts-advance override silently strands fat-group events; pagination correctness not complete |
| QRY-02 | 03-02, 03-04, 03-07 | Tag filters via Event__tag with NIP-01 AND semantics | SATISFIED | AND residual; tests pass |
| QRY-03 | 03-04 | latestPerAuthor via Event__pubkeyKind | SATISFIED | latest_per_author() implemented and fixture-tested |
| QRY-04 | 03-03, 03-05 | Hydrate EventPayload[levId] | SATISFIED | hydrate_lev_ids returns lev_id-associated pairs; tests pass |
| QRY-05 | 03-04 | NIP-40 expiration filtering | SATISFIED | is_expired() applied in both query paths; unit test passes |

**Documentation note:** REQUIREMENTS.md marks QRY-03 and QRY-05 as "Pending" — stale tracking, implementations are present and tested.

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `src/query/engine.rs` | 384-390 | ts-advance override `deepest_scanned = (stalled_ts - 1, u64::MAX)` on no-progress break | BLOCKER | Silently abandons lev_ids [1 .. FAT_COUNT - emit_limit] at stalled_ts when FAT_COUNT > emit_limit; events below page boundary permanently stranded; false end-of-stream after BELOW events drained |
| `src/query/engine.rs` | 388-389 | `else { /* no-op */ }` when stalled_ts == 0 | BLOCKER | Cross-call non-termination for fat groups at created_at=0 larger than emit_limit; identical non-advancing cursor re-emitted; caller paginating until cursor==None loops forever |
| `src/query/engine.rs` | 2010 | `const FAT_COUNT: u64 = 260; // = emit_limit` | BLOCKER | Regression test pinned at FAT_COUNT=emit_limit; does not exercise the defective FAT_COUNT > emit_limit path; provides false assurance that the defect is covered |
| `src/query/engine.rs` | 370-372 | Comment "architecturally unreachable: lev_id is a DUPSORT value, not part of the key, so per-value positioning within a dup group is not possible via key-range-only scans" | WARNING | Factually inaccurate: strfry encodes lev_id into resume position; limitation is this engine's timestamp-only round_until; false claim launders a fixable bug into a hard invariant |

## Human Verification Required

None — all items resolved programmatically.

## Gaps Summary

### Progress Since Previous Verification

Plan 03-11 genuinely closed REVIEW CR-01 (empty-valid budget-cap → false EOF). The `deepest_scanned` fallback cursor is correctly implemented, correctly wired, and has a regression test that exercises the actual defect path. This gap is fully closed and will not regress without breaking the test.

### Remaining Blockers

**BLOCKER 1: ts-advance override strands fat-group events (FAT_COUNT > emit_limit)**

The no-progress break at engine.rs:379 is correct. The ts-advance override at engine.rs:385-386 (`deepest_scanned = (stalled_ts - 1, u64::MAX)`) is the defect. When a fat timestamp holds more events than emit_limit, paginating exhausts the top emit_limit positions and hits the stall. The override then skips the entire remaining tail of the dup group at stalled_ts. These events — in the (created_at DESC, lev_id DESC) ordering, at positions strictly below the page boundary — receive a false end-of-stream (after the BELOW events are drained, cursor becomes None and the caller concludes the stream is complete).

The 03-11 SUMMARY documents this explicitly as an accepted "architectural limitation" and explains why a pure no-progress break (without ts-advance) would cause infinite cross-page stalling. Both claims are correct: a pure break does cause cross-page stalling AND the ts-advance does strand events. The plan presented this as a forced choice, but the correct option — which the plan itself suggested — is to return a QueryError or diagnostic rather than silent truncation. Silent data loss is worse than a recoverable error.

Root cause: `round_until` is built from `round_boundary.map(|(ts, _)| ts)` (engine.rs:225-227), discarding lev_id. This makes intra-group resume impossible from the engine's loop. The comment at 370-372 incorrectly calls this an LMDB architectural limit; it is this engine's own design limit.

The regression test is constructed to avoid testing this scenario: `FAT_COUNT = emit_limit = 260` pins the group at exactly the emit_limit boundary, so the floor is lev_id=1 and the ts-advance fires only when the group is genuinely drained. A test asserting all events for `FAT_COUNT > emit_limit` would fail on the current code.

**BLOCKER 2: ts=0 non-termination**

The `else` no-op at engine.rs:388-389 (when stalled_ts == 0) leaves deepest_scanned pointing at `(0, floor_lev)`. Any caller paginating until cursor == None loops forever. This is a cross-call divergence violation of D-11. `created_at=0` is unusual in practice but the engine must handle all u64 values in on-disk indexes.

**Fix path for the next plan:**

Option A (preferred): implement lev_id-aware reverse scan resume in build_start_keys / scan so the next page can start strictly below (stalled_ts, floor_lev) within the dup group, mirroring strfry's own resume model. Remove the ts-advance override; a pure break + deepest_scanned at (stalled_ts, floor_lev) then correctly resumes within the dup group on the next page.

Option B (fail-closed fallback for v1): when the no-progress break fires and FAT_COUNT > emit_limit is detected (i.e., the group at stalled_ts is not drained), return `QueryError::FatGroupTruncated` with the stalled_ts and floor_lev — never return false-EOF. Callers can retry with a smaller limit or skip the fat timestamp explicitly. This option preserves the fail-closed correctness requirement without requiring intra-group scan changes.

Either option must also fix the ts=0 case: set `deepest_scanned = None` (force EOF) when `stalled_ts == 0`, and add a regression test for a fat group at created_at=0.

---

_Verified: 2026-06-13T08:45:00Z_
_Verifier: Claude (gsd-verifier)_
