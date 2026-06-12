---
phase: 03-query-engine
verified: 2026-06-12T09:00:00Z
status: gaps_found
score: 3/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 2/5
  gaps_closed:
    - "merge_prefixes prefix-guard (take_while starts_with) prevents cross-prefix contamination"
    - "since enforced as stop-bound in execute_query_internal"
    - "post-hydration kind/author/id residual filters contaminated events"
    - "key-granular exclusive-resume windowing (scan_index_one_window) replaces Included-restart"
    - "start-key dedup in build_start_keys eliminates WR-01 doubling"
    - "hydrate_lev_ids returns Vec<(LevId, DecodedEvent)> pairs; engine joins on lev_id"
    - "tag decode restricted to 64-char lowercase hex; single-char tag name validation"
    - "NIP-01 AND-across-fields tag residual (tags_filter.iter().all)"
    - "42 query module tests pass; 81 total lib tests pass; 17 integration tests pass"
  gaps_remaining:
    - "CR-01: first-window Bound::Included asymmetry on existing keys drops higher dup-group levIds"
    - "CR-02: sort-per-batch per iteration is not a true k-way merge — emits out of order across iterations in multi-stream"
    - "CR-03: global since_cutoff terminates all streams when one stream crosses since boundary"
  regressions: []
gaps:
  - truth: "An events() filter returns correctly ordered results — (created_at DESC, lev_id DESC) total order is guaranteed across ALL multi-stream query patterns"
    status: failed
    reason: "CR-02: execute_query_internal sorts each iteration's combined batch in isolation (not a true k-way merge). When stream A contributes ts=1000 and stream B ts=500 in iteration 1, and stream A returns ts=900 in iteration 2, valid ends up [ts=1000, ts=500, ts=900] — out of DESC order. CR-03 compounds: if any stream's batch hits ts < since, the loop breaks before other streams have returned all their events >= since. Both bugs are latent on the 11-event fixture but reachable on production databases with multi-kind or multi-author queries where streams have different time densities."
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Lines 267-269: merged_batch.sort_unstable_by sorts ONE iteration's combined batch in isolation. This is not a true k-way merge — results can be out of (created_at DESC) order across iterations when streams have different time densities."
      - path: "src/query/engine.rs"
        issue: "Lines 272-297: since_cutoff is global — set if ANY entry in the iteration's combined batch is < since. The outer break (line 380) terminates ALL streams. A sparse stream crossing since terminates a dense stream that still has events >= since."
    missing:
      - "True k-way merge using merge.rs BinaryHeap with per-stream frontier: only emit entries with (ts, lev_id) >= low watermark of all non-exhausted streams"
      - "Per-stream since exhaustion flag instead of global since_cutoff: when a stream's guarded batch contains entries below since, mark that stream exhausted but continue the loop for other streams"
  - truth: "Cursor pagination is correct — a filter with until equal to a real event timestamp returns ALL matching events at that timestamp, and cursor page boundaries lose no levId from dup groups"
    status: failed
    reason: "CR-01 first-window Bound::Included asymmetry: scan_index_one_window and collect_bounded both use Bound::Included(start_key) for the first window/call. The committed proof test (tests/dupsort_resume_test.rs::test_old_code_reverse_drops_levid_nonvacuity) empirically demonstrates that heed 0.22.1's rev_range(Bound::Included(K)) with move_through_duplicate_values(), when K EXISTS in the index, positions at the SMALLEST dup of K and steps to the previous key — higher dups are never yielded. For filter.until=1700000256 (an existing kind=1 timestamp with levIds 7 and 8 in the fixture), the scan yields only levId=7 (smallest dup) and drops levId=8. NIP-01 until is inclusive — this violates it. No test covers this case."
    artifacts:
      - path: "src/lmdb/scan.rs"
        issue: "collect_window Reverse first-batch arm (lines 464-468): Bound::Included(resume_key). When resume_key exists in the index, rev_range positions at the smallest dup of resume_key and steps away. The drain loop continues only while the key matches, but the iterator never yields higher dups — they are skipped by LMDB's MDB_SET_RANGE behavior. The fix applied to resumed windows (Bound::Excluded) was not applied to the first window."
      - path: "src/lmdb/scan.rs"
        issue: "collect_bounded Reverse arm (lines 370-374): also uses Bound::Included(start_key). Inherited by merge_prefixes (merge.rs) and latest_per_author. Safe for latest_per_author (start ts = u64::MAX, key cannot exist) but affects merge_prefixes when filter.until equals a real timestamp."
    missing:
      - "For Reverse scans with a finite until/cursor ts: build start_key with ts+1 as trailing created_at (when ts < u64::MAX), use Bound::Excluded(key_with_ts_plus_one). SET_RANGE positions above the boundary, walks back to the LAST (largest) dup of the real ts."
      - "Add fixture regression test: kinds=[1], until=1700000256 must return exactly 5 events including BOTH levId=7 AND levId=8 (currently returns only levId=7, missing levId=8)"
human_verification: []
---

# Phase 3: Query Engine Verification Report

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-12T09:00:00Z
**Status:** gaps_found
**Re-verification:** Yes — after gap-closure plans 03-05/06/07

## Gap-Closure Progress Since Previous Verification

The previous verification (score 2/5) identified two gap clusters. Gap-closure plans 03-05, 03-06, and 03-07 addressed them. A subsequent code review (03-REVIEW.md, 2026-06-12) then identified three remaining correctness blockers not visible in the fixture-based test suite.

**Plan 03-05 (CR-05) — CLOSED:** `hydrate_lev_ids` now returns `Vec<(LevId, DecodedEvent)>`; both `execute_query` and `latest_per_author` join on `lev_id` instead of positional zip. Corrupt-payload skips no longer corrupt the PageCursor or shift keys. Verified with regression tests `test_hydrate_skips_corrupt_payload_slot_aligned` and `test_execute_query_cursor_stable_after_corrupt_skip`.

**Plan 03-06 (CR-01/02/03/04/WR-01) — PARTIALLY CLOSED:** `merge_prefixes` has a per-prefix `take_while(starts_with(prefix))` guard; `build_start_keys` deduplicates output; `execute_query_internal` enforces `since` as a stop-bound; post-hydration kind/author/id residual added; windowing replaced with `scan_index_one_window` (key-granular exclusive-resume). Regression tests cover all five issues against the fixture. However, the review found that three correctness issues survive the fixture-based test suite (see gaps).

**Plan 03-07 (CR-06/07/WR-04) — CLOSED:** Event__tag start keys decode only 64-char lowercase hex; non-single-char tag names produce zero start keys with a warn; tag residual uses `tags_filter.iter().all(...)` (NIP-01 AND across distinct fields). Regression tests cover all three issues.

**Test results: 42/42 query module tests pass; 81/81 lib tests pass; 17/17 integration tests pass.**

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A NostrFilter routes to the most selective index per D-02 priority order (ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; tag→Event__tag; empty→Event__created_at) | ✓ VERIFIED | `select_index()` in router.rs implements the 6-arm priority chain exactly; 6 unit tests cover all arms; `test_execute_query_kinds_routing_and_order` verifies end-to-end routing and ordering for kind=1 |
| 2 | A tag filter (Event__tag) returns events matching the given tag name and values with NIP-01 AND semantics across distinct fields | ✓ VERIFIED | 64-char lowercase hex decode rule in router.rs; `tags_filter.iter().all(...)` AND residual in engine.rs; regression tests: `test_execute_query_multi_tag_and_semantics` (returns 0 for {#e,#p} with no #p events), `test_execute_query_tag_values_or_within_field`, `test_execute_query_single_tag_still_matches` all pass |
| 3 | `latestPerAuthor` returns the latest N events per pubkey via Event__pubkeyKind prefix scans | ✓ VERIFIED | `latest_per_author()` in engine.rs; 3 fixture-backed tests pass: two-bucket (pk1=2 events, pk2=2 events), per_author=1 (newest only), no-match author (absent bucket, no error) |
| 4 | Events with expiration != 0 and expiration <= now are excluded from all query results at query time | ✓ VERIFIED | `is_expired()` in engine.rs using direct SystemTime::now() (D-09); applied in both execute_query_internal and latest_per_author; unit test covers past/future/zero/absent/malformed/short-tag cases |
| 5 | events() returns correctly ordered results in (created_at DESC, lev_id DESC) total order across ALL multi-stream query patterns, and cursor pagination returns non-overlapping pages with no lost events at dup-group boundaries | ✗ FAILED | CR-02: sort-per-batch is not a true k-way merge (out-of-order across iterations for multi-stream). CR-03: global since_cutoff terminates all streams when one stream crosses since. CR-01: first-window Bound::Included drops higher dup-group levIds when filter.until equals a real event timestamp. All three are reviewed against code; fixture too small (11 events, dup groups of size 2) to expose them. |

**Score: 3/5 truths verified**

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/filter.rs` | NostrFilter, TagFilter, PageCursor, QueryError types + cursor encode/decode | ✓ VERIFIED | 105 lines; all four types present; base64 encode/decode round-trips; CursorDecode error on malformed input; 4 unit tests pass |
| `src/query/router.rs` | select_index + build_start_keys + created_at_from_key | ✓ VERIFIED | 760 lines; D-02 priority chain; D-03 time-bound pushdown; dedup output; 11 unit tests pass including CR-07/WR-04 regression tests |
| `src/query/merge.rs` | MergeCandidate + merge_prefixes k-way heap merge with prefix guard | ✓ VERIFIED | 425 lines; Ord on (created_at DESC, lev_id DESC); take_while prefix guard; 5 unit tests pass including CR-01 contamination test |
| `src/query/hydrate.rs` | hydrate_lev_ids returning Vec<(LevId, DecodedEvent)> | ✓ VERIFIED | 308 lines; lev_id-associated pairs; skip-warn-count on decode failures; structural errors propagated; 4 unit tests pass including CR-05 regression |
| `src/query/engine.rs` | execute_query + latest_per_author (QRY-01/02/03/05) | ✓ VERIFIED (structure) / ✗ PARTIAL (correctness) | 1360 lines; both functions present and tested; multi-stream ordering incorrect (CR-02/CR-03); first-window dup drop (CR-01) not tested/fixed |
| `src/query/mod.rs` | engine, filter, hydrate, merge, router all declared | ✓ VERIFIED | All 5 submodules present in alphabetical order |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `src/lib.rs` | `src/query/mod.rs` | `pub mod query` | ✓ WIRED | Confirmed |
| `src/query/engine.rs` | `scan_index_one_window` | per-stream windowed scan | ✓ WIRED | Called in execute_query_internal for each stream iteration |
| `src/query/engine.rs` | `hydrate_lev_ids` | post-merge hydration | ✓ WIRED | Called in execute_query_internal and latest_per_author |
| `src/query/engine.rs` | `is_expired` | NIP-40 residual | ✓ WIRED | Called in execute_query_internal and latest_per_author |
| `src/query/engine.rs` | `merge_prefixes` | k-way merge (planned) | ✗ ORPHANED | Imported via use statement but NOT called in execute_query_internal. Engine uses its own sort-per-batch instead. merge_prefixes is only invoked from merge.rs's own tests. |
| `src/query/merge.rs` | `scan_index_bounded` | per-prefix reverse scans | ✓ WIRED | Called in merge_prefixes |
| `src/query/router.rs` | `src/query/filter.rs` | NostrFilter/TagFilter | ✓ WIRED | NostrFilter consumed by select_index and build_start_keys |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `execute_query_internal` | `valid` Vec of DecodedEvents | scan_index_one_window → EventPayload LMDB | Yes — real LMDB scans, fixture-backed tests confirm hydrated events | ✓ FLOWING |
| `latest_per_author` | per-author bucket `Vec<DecodedEvent>` | scan_index_bounded → EventPayload LMDB | Yes — real LMDB scans, fixture-backed tests confirm correct bucket contents | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Query module: 42 tests | `cargo test --lib query::` | 42 passed, 0 failed, 1.05s | ✓ PASS |
| Full lib + integration suite | `cargo test --all-targets` | 81 lib + 17 integration = 98 total, 0 failed, 1.92s | ✓ PASS |
| No write_txn in production query paths | grep `write_txn` in src/query/ non-test | Only inside `#[cfg(test)]` blocks | ✓ PASS |
| No .create() in production query paths | grep `.create(` in src/query/ non-test | None | ✓ PASS |
| merge_prefixes called in execute_query production path | grep `merge_prefixes` in engine.rs body | In use statement only; NOT in execute_query_internal function body | ✗ ORPHANED |
| until=existing_ts test coverage | grep `until=1700000256` in tests/ | Not found — CR-01 first-window drop is untested | ✗ MISSING TEST |

### Probe Execution

No probes declared or found. Step 7c: SKIPPED (no runnable probe scripts in scripts/).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| QRY-01 | 03-01, 03-02, 03-04, 03-06 | events() resolved by scanning most selective index | PARTIAL | Index selection, routing, since enforcement, kind/author/id residual all implemented and single-stream tested; multi-stream ordering incorrect (CR-02/CR-03 blockers) |
| QRY-02 | 03-02, 03-04, 03-07 | Tag filters via Event__tag with NIP-01 AND semantics | SATISFIED | 64-char lowercase hex decode; AND residual; 5 regression tests pass against fixture |
| QRY-03 | 03-04 | latestPerAuthor via Event__pubkeyKind | SATISFIED | latest_per_author() implemented; 3 fixture tests pass; REQUIREMENTS.md "Pending" is stale documentation tracking |
| QRY-04 | 03-03, 03-05 | Hydrate EventPayload[levId] for matched levIds | SATISFIED | hydrate_lev_ids returns lev_id-associated pairs; lev_id-join in engine; 4 tests pass including CR-05 regression |
| QRY-05 | 03-04 | NIP-40 expiration filtering | SATISFIED | is_expired() present and applied in both query paths; unit test covers all NIP-40 cases; REQUIREMENTS.md "Pending" is stale documentation tracking |

**Documentation note:** REQUIREMENTS.md marks QRY-03 and QRY-05 as "Pending" but the implementations exist and tests pass. This is a stale tracking artifact, not an implementation gap.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `src/query/engine.rs` | 27 | `merge_prefixes` in use statement but not called in production | WARNING | WR-04: merge.rs's correct k-way heap merge is imported but unused in the execution path. The engine uses an incorrect sort-per-batch instead (root cause of CR-02). |
| `src/query/engine.rs` | 293-297 | Empty `if since_cutoff { }` block (only comments in body) | INFO | IN-02: dead code that obscures control flow. |
| `src/query/engine.rs` | 201, 307 | `skip_count` accumulated but never logged or returned | INFO | IN-03: per-query aggregate discarded. |
| `src/query/merge.rs` | 137-151 | `stream_prefixes` built then immediately `drop`ped | INFO | IN-01: dead vector; admittedly dead per comment. |
| `src/query/merge.rs` | 44-70 | `#[derive(Eq, PartialEq)]` compares all 4 fields; `Ord::cmp` compares only (created_at, lev_id) | INFO | IN-04: Eq/Ord inconsistency. Harmless for BinaryHeap. |

### Human Verification Required

None — all items resolved programmatically.

## Gaps Summary

The gap-closure plans addressed the previous 3 gap clusters (8 specific issues). All 42 query module tests pass. However, the code review (03-REVIEW.md) independently identified three remaining correctness blockers that survive the 11-event, dup-group-size-2 fixture. These are verified below against the actual code.

### Blocker 1: CR-01 — First-Window Bound::Included Drops Higher Dup-Group LevIds

`collect_window` (scan.rs:462-468) and `collect_bounded` (scan.rs:370-374) both use `Bound::Included(start_key)` for the Reverse first call. The committed proof test `tests/dupsort_resume_test.rs::test_old_code_reverse_drops_levid_nonvacuity` empirically demonstrates that heed 0.22.1's `rev_range(Bound::Included(K))` with `move_through_duplicate_values()` — when K EXISTS in the index — positions at the SMALLEST dup of K then steps to the previous key. Higher dups of K are never yielded. The drain loop in `collect_window` does not recover them because the iterator does not visit them.

**Observable failure:** A query with `kinds=[1], until=1700000256` against the fixture should return 5 events (levIds 4, 5, 6, 7, 8 — all kind=1 events at ts <= 1700000256). The key `kind=1||ts=1700000256` exists with dup group [7, 8]. The scan positions at levId=7 (smallest) and steps away — levId=8 is silently dropped. **No test covers this case.**

**Fix:** Build the Reverse start key with `ts + 1` (if ts < u64::MAX) and use `Bound::Excluded(key_with_ts_plus_one)`. Positions SET_RANGE above the boundary, walks back to land on the LAST (largest) dup of the real ts. Apply to both `collect_window` first-batch and `collect_bounded` Reverse arms.

### Blocker 2: CR-02 — Sort-Per-Batch Is Not a True K-Way Merge

`execute_query_internal` (engine.rs:267-269) sorts each iteration's `merged_batch` in isolation. The correct `merge_prefixes` BinaryHeap from `merge.rs` is imported but not called in the execution path. When stream A delivers ts=1000→900 in batch 1 and stream B delivers ts=500 in batch 1, batch 1 is correctly sorted. But if limit is not reached, batch 2 from stream A delivers ts=900→800 — these are sorted among themselves and appended after the ts=500 from batch 1, creating `[..., ts=500, ts=900, ...]` in `valid[]`. This violates (created_at DESC) and corrupts the cursor.

**Fix:** Use a true incremental merge with per-stream frontier: only emit entries with (ts, lev_id) >= the low watermark of all non-exhausted streams. Extend merge.rs's BinaryHeap with windowed per-stream refill, route the engine through it, delete the inline sort.

### Blocker 3: CR-03 — Global since_cutoff Terminates All Streams Prematurely

`since_cutoff` (engine.rs:272) is set if ANY entry in the iteration's combined batch has `ts < since`. The outer break (engine.rs:380) then terminates the ENTIRE loop. A sparse stream (kind B with one event at ts < since) silently terminates a dense stream (kind A with many events >= since) before kind A is exhausted. No error is returned; `limit` is not reached; missing results are undetectable by the caller.

**Fix:** Per-stream since exhaustion. Detect when a stream's guarded batch contains entries below since, truncate the batch and mark that stream exhausted, continue the outer loop for other streams. Loop termination falls out naturally from all streams exhausted.

### Relationship to Phase Goal

The phase goal requires "correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination." Blockers 1-3 directly violate the "correct" qualifier:
- CR-01: data loss at `until` boundaries and cursor page transitions (NIP-01 violation)
- CR-02: ordering guarantee violated for multi-stream queries; cursor built from wrong position causes page duplicates/skips
- CR-03: silent under-reporting for multi-stream queries with heterogeneous time ranges

Single-stream code paths (single kind, single author, single tag value, default feed) are correct and fully tested. Multi-stream paths (multiple kinds, multiple authors) are structurally implemented but not correctly ordered.

---

_Verified: 2026-06-12T09:00:00Z_
_Verifier: Claude (gsd-verifier)_
