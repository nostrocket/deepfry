---
phase: 03-query-engine
verified: 2026-06-13T12:00:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 4/5
  gaps_closed:
    - "Cursor pagination correctness (fat-group stranding + ts=0 non-termination): lev_id_floor: Option<(u64, LevId)> parameter added to merge_windowed/refill_stream in merge.rs; engine.rs passes round_boundary as the floor on every merge_windowed call; test_execute_query_fat_timestamp_pagination_exceeds_emit_limit (FAT_COUNT=300 > emit_limit=260) collects 305/305, no duplicates, terminates; test_execute_query_fat_timestamp_at_ts_zero_terminates collects 10/10 events at created_at=0 and terminates; test_execute_query_fat_timestamp_pagination_at_limit_boundary (previously rigged FAT_COUNT=260=emit_limit, now a legitimate boundary guard) collects 265/265; false architectural claim at engine.rs:370-372 ('architecturally unreachable') corrected to a factually accurate description of the safety-net no-progress break."
  gaps_remaining: []
  regressions: []
human_verification: []
---

# Phase 3: Query Engine Verification Report (Re-verification 5)

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-13T12:00:00Z
**Status:** passed
**Re-verification:** Yes — fifth verification; confirming debug-session fix (commit f4ec868, `lev_id_floor` parameter)

## Re-verification Mode: Gap-Closure Confirmation

Previous VERIFICATION.md (fourth verification, score 4/5) had two gaps:
- Fat-group stranding: `FAT_COUNT > emit_limit` stranded the lowest `F - emit_limit` events via `ts-advance override`.
- ts=0 non-termination: `stalled_ts == 0` no-op left `deepest_scanned` unchanged, causing identical cursor re-emission.

The debug session (`cursor-fat-group-stranding.md`) concluded that heed 0.22.1 does not expose `MDB_GET_BOTH`, so DUPSORT value-level cursor positioning was not available. The chosen fix: pass `lev_id_floor: Option<(u64, LevId)>` into `merge_windowed`/`refill_stream` and filter entries at `floor_ts` with `lev_id >= floor_lev`, exposing lower lev_ids within the same fat group on each successive round.

**Focus:** Full 3-level verification on truth #5 (cursor pagination) — independently confirm the fix is genuinely correct for `FAT_COUNT > emit_limit`, ts=0, and boundary cases. Regression check truths #1-4.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A NostrFilter routes to the most selective index per D-02 priority order (ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; tag→Event__tag; empty→Event__created_at) | ✓ VERIFIED | `select_index()` in router.rs unchanged. Tests: `test_select_index_all_six_arms`, `test_select_index_ids_highest_priority`, `test_execute_query_kinds_routing_and_order`. 94 lib tests pass. |
| 2 | A tag filter (Event__tag) returns events matching the given tag name and values with NIP-01 AND semantics across distinct fields | ✓ VERIFIED | `tags_filter.iter().all(...)` AND residual at engine.rs:333-344 unchanged. Tests: `test_execute_query_multi_tag_and_semantics`, `test_execute_query_tag_values_or_within_field`, `test_execute_query_single_tag_still_matches`. |
| 3 | `latestPerAuthor` returns the latest N events per pubkey via Event__pubkeyKind prefix scans | ✓ VERIFIED | `latest_per_author()` at engine.rs:468-557 unchanged. Tests: `test_latest_per_author_two_buckets`, `test_latest_per_author_per_author_one`, `test_latest_per_author_no_matching_events`. |
| 4 | Events with expiration != 0 and expiration <= now are excluded from all query results at query time | ✓ VERIFIED | `is_expired()` applied at engine.rs:296 and engine.rs:547 unchanged. Test: `test_is_expired_predicate`. |
| 5 | Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary, and pagination converges without stalling | ✓ VERIFIED | `lev_id_floor` fix in merge.rs+engine.rs confirmed present and substantive. Three regression tests (FAT_COUNT=300, FAT_COUNT=260=boundary, ts=0) all pass against genuine defect scenarios. Test suite 110/110 green. See detail below. |

**Score: 5/5 truths verified**

---

## Truth #5: Detailed Verification

### Fix Mechanism (merge.rs)

`refill_stream` (merge.rs:136-253) accepts `lev_id_floor: Option<(u64, LevId)>`. When set, it filters entries with `ts == floor_ts && lev_id >= floor_lev` from each batch before any other processing (lines 196-206). This happens inside the scan loop before the prefix guard and `since` truncation. When the floor filter eliminates an entire window, the function iterates once more (bounded at 2 attempts) to reach entries at `ts < floor_ts` — preventing false stream exhaustion when the floor boundary falls exactly at a key boundary.

`merge_windowed` (merge.rs:303-373) accepts the same parameter and passes it through to every `refill_stream` call (lines 334, 362).

Both functions verified: substantive implementation, not a stub.

### Fix Wiring (engine.rs)

`execute_query_internal` (engine.rs:146-443) passes `round_boundary` directly as `lev_id_floor` on every `merge_windowed` call at line 249:

```rust
let merge_batch = merge_windowed(env, short_name, &start_keys, batch_size, emit_limit, since, round_boundary)
```

`round_boundary` is initialized from `cursor_boundary` (the caller's page cursor) and advances each round to `last_merged`. On round 1 with no cursor, `round_boundary = None` → `lev_id_floor = None` → no filtering (correct for first page). On subsequent rounds, `round_boundary = Some((ts, lev_id))` → floor filters out already-emitted entries at that ts/lev_id.

### No-Progress Break: Safety Net, Not Main Path

The no-progress break at engine.rs:381-389 is correctly described in its comment as "dead-code for fat groups after the lev_id_floor fix, but retained as a safety net." With `lev_id_floor` exposing lower lev_ids on each round, `last_merged` advances below `round_boundary` on every fat-group round, so `last_merged == round_boundary` never fires for fat groups. The false architectural claim ("architecturally unreachable: lev_id is a DUPSORT value...") that appeared in previous versions is **gone** — replaced with a factually accurate comment. Verified at lines 366-380.

The `ts=0` non-termination path through the no-progress break is also eliminated: for fat groups at `created_at=0`, the `lev_id_floor` fix causes `last_merged` to advance each round, so the no-progress break never fires at `ts=0`. The ts=0 guard (lines 383-387) is now truly dead code in the fat-group case. `test_execute_query_fat_timestamp_at_ts_zero_terminates` confirms: 10 events at `created_at=0`, limit=2 → all 10 collected, terminates. PASS.

### Regression Tests: Genuinely De-Rigged

**`test_execute_query_fat_timestamp_pagination_exceeds_emit_limit`** (engine.rs:2035-2117):
- `FAT_COUNT = 300`, `emit_limit = 260` (limit=2 → 2+256+2=260)
- Confirms the OLD defect: the comment at line 2033 documents "OLD CODE RESULT: collected 265/305 (40 stranded)"
- TOTAL=305; asserts `all_collected.len() == 305` and no duplicates
- Subdivides assertion: 300 fat-ts events present (lev_ids 1..=300), 5 below-ts events present
- PASS — independently confirmed by running the test: 1 passed, 0 failed

**`test_execute_query_fat_timestamp_at_ts_zero_terminates`** (engine.rs:2133-2162):
- 10 events at `created_at=0`, limit=2, safety cap=20 pages
- Asserts all 10 collected, no duplicates, terminates
- PASS

**`test_execute_query_fat_timestamp_pagination_at_limit_boundary`** (engine.rs:1994-2019):
- `FAT_COUNT = 260 = emit_limit exactly` — the previously rigged test, now a legitimate boundary guard
- TOTAL=265 (260 fat-ts + 5 below); asserts 265/265 collected, no duplicates
- Comment at line 1989-1991 explains why this is now a legitimate boundary case (lev_id_floor transition)
- PASS

**`test_execute_query_budget_cap_empty_valid_returns_cursor`** (engine.rs:1877-1983):
- CR-01 (empty-valid budget-cap → false EOF): unchanged from previous verification, still PASS

### Full Suite

110 tests (94 lib + 16 integration), 0 failed. All four fat-group tests and CR-01 test pass.

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/merge.rs` | `lev_id_floor: Option<(u64, LevId)>` in `refill_stream` + `merge_windowed` | ✓ VERIFIED | Lines 142, 196-206, 310, 334, 362. Filter drops `ts == floor_ts && lev_id >= floor_lev`. Two-attempt loop for boundary exhaustion. |
| `src/query/engine.rs` | `execute_query_internal` passes `round_boundary` as `lev_id_floor` | ✓ VERIFIED | Line 249: `merge_windowed(..., round_boundary)`. |
| `src/query/engine.rs` | `test_execute_query_fat_timestamp_pagination_exceeds_emit_limit` (FAT_COUNT=300) | ✓ VERIFIED | Not rigged; asserts 305/305 collected; documents old-code result (265/305). PASS. |
| `src/query/engine.rs` | `test_execute_query_fat_timestamp_at_ts_zero_terminates` | ✓ VERIFIED | New test; 10 events at ts=0; terminates, all collected. PASS. |
| `src/query/engine.rs` | `test_execute_query_fat_timestamp_pagination_at_limit_boundary` (FAT_COUNT=260) | ✓ VERIFIED | Previously rigged; now legitimate boundary guard with accurate comment. PASS. |
| `src/query/engine.rs` | No false "architecturally unreachable" claim at former lines 370-372 | ✓ VERIFIED | Comment at lines 366-380 replaced with accurate description of safety-net behavior. |
| `src/query/router.rs` | `select_index` + `build_start_keys` | ✓ VERIFIED | Unchanged from previous verifications. |
| `src/query/hydrate.rs` | `hydrate_lev_ids` | ✓ VERIFIED | Unchanged. |
| `src/query/filter.rs` | `NostrFilter`, `TagFilter`, `PageCursor`, `QueryError` | ✓ VERIFIED | Unchanged. |

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `execute_query_internal` → `merge_windowed` | `lev_id_floor` param | `round_boundary` passed at engine.rs:249 | ✓ WIRED | First page: `round_boundary=None` → no floor. Subsequent pages: `round_boundary=Some((ts,lev))` → floor active. |
| `refill_stream` → floor filter | `lev_id_floor` | `filter(ts != floor_ts || lev_id < floor_lev)` at merge.rs:196-206 | ✓ WIRED | Drops already-emitted entries; exposes lower lev_ids. |
| `refill_stream` → second-attempt loop | floor-eliminated window | `continue` in `for _attempt in 0..2` at merge.rs:152-253 | ✓ WIRED | When floor drops entire window, loops once more to reach ts < floor_ts. Marks exhausted after 2 empty attempts. |
| `merge_windowed` → `refill_stream` | both call sites | Lines 334, 362 pass `lev_id_floor` | ✓ WIRED | Initial fill and mid-merge refill both apply floor consistently. |
| `execute_query_internal` → `deepest_scanned` | CR-01 fallback cursor | Lines 355-357 update from `last_merged` before break; lines 429-436 return `deepest_scanned.map(...)` | ✓ WIRED | Unchanged from previous verification. |
| `no-progress break` → `deepest_scanned` | Safety-net ts-advance | Lines 381-388 | ✓ WIRED (dead-code for fat groups) | Now truly dead for fat groups; retained as safety net for pathological non-fat cases. |

## Data-Flow Trace (Level 4)

Not applicable to query engine library (no rendering/UI). The engine returns `Vec<DecodedEvent>` to callers; data flow verified through test assertions that check event counts, ids, and ordering.

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full lib + integration suite | `cargo test --all-targets` | 94 lib + 16 integration = 110 total, 0 failed | ✓ PASS |
| Fat-group FAT_COUNT=300 > emit_limit | `cargo test --lib query::engine::tests::test_execute_query_fat_timestamp_pagination_exceeds_emit_limit` | 1 passed, 0 failed; collects 305/305 | ✓ PASS |
| ts=0 non-termination | `cargo test --lib query::engine::tests::test_execute_query_fat_timestamp_at_ts_zero_terminates` | 1 passed, 0 failed; collects 10/10, terminates | ✓ PASS |
| FAT_COUNT=emit_limit boundary | `cargo test --lib query::engine::tests::test_execute_query_fat_timestamp_pagination_at_limit_boundary` | 1 passed, 0 failed; collects 265/265 | ✓ PASS |
| CR-01 empty-valid budget-cap | `cargo test --lib query::engine::tests::test_execute_query_budget_cap_empty_valid_returns_cursor` | 1 passed, 0 failed | ✓ PASS |
| lev_id_floor in merge.rs | `grep -n "lev_id_floor" src/query/merge.rs` | 9 occurrences: param decl, filter body, both call sites | ✓ PASS |
| round_boundary passed as floor | `grep -n "lev_id_floor\|round_boundary" src/query/engine.rs` | line 249: `merge_windowed(..., round_boundary)` | ✓ PASS |
| False "architecturally unreachable" comment removed | `grep -n "architecturally unreachable" src/query/engine.rs` | 0 matches | ✓ PASS |

## Probe Execution

No probe scripts declared or found. Step 7c: SKIPPED.

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| QRY-01 | 03-01 through 03-11 | events() resolved by scanning most selective index | SATISFIED | Index routing, k-way merge, round-loop, NIP-40, cursor all correct. 110 tests pass. |
| QRY-02 | 03-02, 03-04, 03-07 | Tag filters via Event__tag with NIP-01 AND semantics | SATISFIED | AND residual; `tags_filter.iter().all(...)` confirmed; tests pass. |
| QRY-03 | 03-04 | latestPerAuthor via Event__pubkeyKind | SATISFIED | `latest_per_author()` implemented, fixture-tested, NIP-40-filtered. |
| QRY-04 | 03-03, 03-05 | Hydrate EventPayload[levId] | SATISFIED | `hydrate_lev_ids` returns lev_id-associated pairs; tests pass. |
| QRY-05 | 03-04, fat-group debug | NIP-40 expiration filtering + cursor convergence | SATISFIED | `is_expired()` in both paths; fat-group + ts=0 pagination now correct via lev_id_floor fix. |

**Documentation note:** REQUIREMENTS.md marks QRY-03 and QRY-05 as "Pending" — stale tracking. Both are implemented and tested.

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `src/query/engine.rs` | 381-388 | No-progress break with `ts-advance` override — now dead-code for fat groups, safety net retained | INFO | No impact: with `lev_id_floor` fix, fat groups advance `last_merged` each round so `last_merged != round_boundary` and this break never fires. Accurately documented in comment. No data loss risk. |

No `TBD`, `FIXME`, or `XXX` markers found in modified files. No stubs. No hardcoded empty returns in execution paths.

## Hardening Recommendation (Non-Blocking)

The debug session noted: all fat-group regression tests paginate with `batch_size=2` (`paginate_all` hardcodes `execute_query_with_batch(..., 2)`), not the production `DEFAULT_WINDOW_SIZE=256`. The orchestrator's probe (FAT_COUNT=800, production path, 805/805 collected) confirmed correctness under the production window, but no permanent test exercises the production window size against a fat group. This is a hardening gap, not a correctness gap — the fix is logically correct independent of batch size. Recommend adding a permanent test with `execute_query` (not `execute_query_with_batch`) and FAT_COUNT > DEFAULT_WINDOW_SIZE when convenient.

## Human Verification Required

None — all items resolved programmatically.

## Gaps Summary

No gaps. All five must-haves are verified. The single outstanding gap from the previous verification (truth #5: cursor pagination correctness) is now closed. The fix is genuinely correct: `lev_id_floor` filters already-emitted entries within a fat DUPSORT group on each successive round, exposing lower lev_ids without requiring `MDB_GET_BOTH` cursor positioning. The regression tests are not rigged — `test_execute_query_fat_timestamp_pagination_exceeds_emit_limit` exercises the exact stranding scenario (`FAT_COUNT=300 > emit_limit=260`) that the previous two "fixed" attempts failed to cover.

---

_Verified: 2026-06-13T12:00:00Z_
_Verifier: Claude (gsd-verifier)_
