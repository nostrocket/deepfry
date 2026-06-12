---
phase: 03-query-engine
verified: 2026-06-12T13:30:00Z
status: gaps_found
score: 4/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 3/5
  gaps_closed:
    - "CR-01 first-window Bound::Included dup drop: reverse_upper_bound helper + Bound::Excluded(ts+1) in collect_bounded and collect_window; test_scan_reverse_until_existing_ts_keeps_both_dups passes"
    - "CR-02 sort-per-batch non-merge: execute_query_internal now routes through BinaryHeap merge_windowed; sort_unstable_by absent from production code; test_execute_query_multistream_cross_iteration_order passes"
    - "CR-03 global since_cutoff starvation: per-stream since truncation in refill_stream; since_cutoff variable absent from engine.rs; test_execute_query_multistream_since_per_stream passes"
  gaps_remaining:
    - "New REVIEW CR-01: execute_query_internal makes one merge_windowed call (emit_limit = 2*limit+256); when residual-heavy or expiry-heavy filters drop >emit_limit-limit consecutive entries before limit survivors, the query returns < limit events with next_cursor = None — reachable events are silently stranded and unreachable by any caller"
  regressions: []
gaps:
  - truth: "Cursor pagination is correct — a filter that would return more than limit events gets a non-None next_cursor on any result page where matching events remain below the page boundary"
    status: failed
    reason: "New REVIEW CR-01 (no backfill loop): execute_query_internal calls merge_windowed exactly once with emit_limit = limit*2 + DEFAULT_WINDOW_SIZE (= 2*limit+256). When the residual/expiry/cursor-exclusion drop rate exceeds the headroom (more than limit+256 consecutive merge entries filtered before limit survivors accumulate), the function returns < limit events and next_cursor = None (engine.rs:302-309 sets next_cursor = None when valid.len() < limit). The remaining matching events are unreachable — no resume cursor is returned. The comment block at engine.rs:172-190 explicitly describes a round-loop the code does not implement. Production-reachable cases: (1) kinds=[1]+#p tag filter (router picks Event__kind; tag checked only post-hydration; if the newest 2*limit+256 kind-1 events carry no #p match, results under-return with no cursor); (2) a relay with > limit+256 expired events at the newest end of an index partition; (3) dense-second cursor resume where cursor exclusion consumes the headroom before new events are reached."
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Lines 191-197: emit_limit = limit*2 + DEFAULT_WINDOW_SIZE (= 2*limit+256); merge_windowed called exactly once; no loop. Lines 302-309: next_cursor = None when valid.len() < limit — strands remaining events with no resume handle."
      - path: "src/query/engine.rs"
        issue: "Lines 172-190: comment describes a round-loop ('If valid.len() < limit after a round, the engine calls merge_windowed again') that is not present in the code ('a single merge_windowed call ... handles virtually all cases' — but 'all cases' is false)."
    missing:
      - "Restore a bounded round-loop with a budget: loop { merge_batch = merge_windowed(env, short_name, &start_keys_for(boundary), batch_size, emit_limit, since)?; ...; if valid.len() >= limit || exhausted || rounds >= MAX_ROUNDS { break; } boundary = last; } where exhausted = merge_batch.len() < emit_limit."
      - "When the budget cap (MAX_ROUNDS) stops the loop early before valid.len() == limit, still build a resume cursor from valid.last() (if any) so reachable events are not stranded."
      - "Update engine.rs:6-7 and 172-190 comments to match the real control flow."
      - "Add a regression test: small batch_size, a residual tag filter that only matches events deeper than emit_limit, assert the events are still returned or reachable via the returned cursor."
human_verification: []
---

# Phase 3: Query Engine Verification Report

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-12T13:30:00Z
**Status:** gaps_found
**Re-verification:** Yes — after gap-closure plans 03-08 and 03-09; also accounts for fresh code review (03-REVIEW.md, 2026-06-12)

## Gap-Closure Progress Since Previous Verification (score 3/5)

Three blockers were identified in the previous verification (CR-01, CR-02, CR-03). Plans 03-08 and 03-09 addressed them. The fresh code review (03-REVIEW.md) confirmed all three prior blockers closed and identified one new critical issue (new REVIEW CR-01: no backfill loop).

**Plan 03-08 (prior CR-01) — CLOSED:** `reverse_upper_bound` helper added to `scan.rs`; both `collect_bounded` and `collect_window` Reverse first-batch arms now use `Bound::Excluded(ts+1 key)`. `test_scan_reverse_until_existing_ts_keeps_both_dups` proves kinds=[1], until=1700000256 returns both levId=7 and levId=8 (5 kind=1 events total). 3 regression tests pass. All prior golden-vector tests preserved.

**Plan 03-09 (prior CR-02/CR-03) — CLOSED at the mechanism level, but introduced a new blocker:**
- `merge_windowed` added to `merge.rs`: true BinaryHeap k-way merge with per-stream frontier and per-stream since truncation in `refill_stream`.
- `execute_query_internal` rewired to call `merge_windowed`; `sort_unstable_by` absent from production code; `since_cutoff` variable absent.
- `merge_prefixes` made a thin delegate to `merge_windowed` (WR-04 closed).
- Dead `stream_prefixes` removed (IN-01); empty `since_cutoff` block removed (IN-02); `skip_count` debug log added (IN-03).
- 5 new regression tests: `test_merge_windowed_cross_iteration_global_desc_order`, `test_merge_windowed_per_stream_since_exhaustion`, `test_execute_query_multistream_cross_iteration_order`, `test_execute_query_multistream_page_union_no_loss`, `test_execute_query_multistream_since_per_stream`.
- The 03-09 rewrite traded the CR-02/CR-03 ordering/since blockers for a new structural gap: `execute_query_internal` now makes exactly ONE `merge_windowed` call. When residual filter drops exceed headroom, the query silently under-returns with `next_cursor = None`.

**Test results: 89 lib tests pass; 16 integration tests pass; 0 failed (105 total).**

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A NostrFilter routes to the most selective index per D-02 priority order (ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; tag→Event__tag; empty→Event__created_at) | ✓ VERIFIED | `select_index()` in router.rs implements the 6-arm priority chain; `test_select_index_all_six_arms` and `test_select_index_ids_highest_priority` pass; `test_execute_query_kinds_routing_and_order` proves end-to-end routing for kind=1 |
| 2 | A tag filter (Event__tag) returns events matching the given tag name and values with NIP-01 AND semantics across distinct fields | ✓ VERIFIED | 64-char lowercase hex decode rule in router.rs; `tags_filter.iter().all(...)` AND residual in engine.rs; `test_execute_query_multi_tag_and_semantics`, `test_execute_query_tag_values_or_within_field`, `test_execute_query_single_tag_still_matches` pass |
| 3 | `latestPerAuthor` returns the latest N events per pubkey via Event__pubkeyKind prefix scans | ✓ VERIFIED | `latest_per_author()` in engine.rs; `test_latest_per_author_two_buckets`, `test_latest_per_author_per_author_one`, `test_latest_per_author_no_matching_events` pass |
| 4 | Events with expiration != 0 and expiration <= now are excluded from all query results at query time | ✓ VERIFIED | `is_expired()` applied in `execute_query_internal` and `latest_per_author`; `test_is_expired_predicate` covers past/future/zero/absent/malformed/short-tag cases |
| 5 | events() returns correctly ordered results in (created_at DESC, lev_id DESC) total order across ALL multi-stream query patterns, and cursor pagination returns non-overlapping pages with no cursor stranding reachable events | ✗ FAILED | Prior blockers (CR-01 dup drop, CR-02 sort-per-batch, CR-03 global since_cutoff) are all FIXED and regression-tested. NEW REVIEW CR-01: the single fixed-headroom merge_windowed call (emit_limit = 2*limit+256, engine.rs:191) with no backfill loop means any filter whose drop rate exceeds headroom returns < limit events with next_cursor = None — stranding reachable events. Confirmed at engine.rs:197 (one call), engine.rs:302-309 (cursor only when valid.len() == limit). The comment block at engine.rs:172-190 describes a round-loop the code does not contain. |

**Score: 4/5 truths verified**

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/lmdb/scan.rs` | reverse_upper_bound helper; collect_bounded Reverse arm Bound::Excluded(ts+1); collect_window Reverse first-batch arm Bound::Excluded(ts+1) | ✓ VERIFIED | `reverse_upper_bound` at lines 359-381; both Reverse arms verified at lines 435-461 and 535-581; 3 regression tests pass including `test_scan_reverse_until_existing_ts_keeps_both_dups` |
| `src/query/merge.rs` | merge_windowed: windowed k-way BinaryHeap merge with per-stream frontier and per-stream since exhaustion | ✓ VERIFIED | `merge_windowed` at lines 227-296; `StreamState` + `refill_stream` implement per-stream since truncation; `merge_prefixes` is a thin wrapper delegate (WR-04 closed); `stream_prefixes` absent |
| `src/query/engine.rs` | execute_query_internal routed through merge_windowed; no sort_unstable_by; since_cutoff absent; cursor built from valid.last() | ✓ VERIFIED (structure) / ✗ PARTIAL (pagination correctness) | `merge_windowed` called at line 197; `sort_unstable_by` absent; `since_cutoff` absent as variable; cursor built at lines 302-309. Structural rewire complete; pagination gap: single call with fixed headroom, no backfill loop — cursor strands events when drop rate exceeds headroom. |
| `src/query/filter.rs` | NostrFilter, TagFilter, PageCursor, QueryError types + cursor encode/decode | ✓ VERIFIED | All four types present; base64 encode/decode round-trips; 4 unit tests pass |
| `src/query/router.rs` | select_index + build_start_keys + created_at_from_key | ✓ VERIFIED | D-02 priority chain; D-03 time-bound pushdown; dedup output; 11 unit tests pass |
| `src/query/hydrate.rs` | hydrate_lev_ids returning Vec<(LevId, DecodedEvent)> | ✓ VERIFIED | lev_id-associated pairs; skip-warn-count on decode failures; 4 unit tests pass |
| `src/query/mod.rs` | engine, filter, hydrate, merge, router all declared | ✓ VERIFIED | All 5 submodules present |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `src/lib.rs` | `src/query/mod.rs` | `pub mod query` | ✓ WIRED | Confirmed |
| `src/query/engine.rs execute_query_internal` | `src/query/merge.rs merge_windowed` | production call line 197 | ✓ WIRED | `merge_windowed` called in production path; no longer orphaned (WR-04 closed) |
| `src/query/engine.rs` | `hydrate_lev_ids` | post-merge hydration | ✓ WIRED | Called at line 220 in execute_query_internal and inside latest_per_author |
| `src/query/engine.rs` | `is_expired` | NIP-40 residual | ✓ WIRED | Called in both execute_query_internal and latest_per_author |
| `src/lmdb/scan.rs collect_window Reverse first_batch` | `reverse_upper_bound` | Bound::Excluded(ts+1) | ✓ WIRED | Lines 546-557: first_batch branch calls reverse_upper_bound, uses Bound::Excluded when is_excluded |
| `src/lmdb/scan.rs collect_bounded Reverse` | `reverse_upper_bound` | Bound::Excluded(ts+1) | ✓ WIRED | Lines 439-444: reverse_upper_bound called, Bound::Excluded applied |
| `src/query/merge.rs merge_prefixes` | `merge_windowed` | thin delegate (WR-04) | ✓ WIRED | Lines 337-342: merge_prefixes calls merge_windowed with since=0 |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `execute_query_internal` | `valid` Vec of DecodedEvents | merge_windowed → scan_index_one_window → EventPayload LMDB | Yes — real LMDB scans; 89 lib tests + 16 integration tests confirm hydrated events | ✓ FLOWING |
| `latest_per_author` | per-author bucket `Vec<DecodedEvent>` | scan_index_bounded → EventPayload LMDB | Yes — real LMDB reverse scans per pubkey; fixture-backed tests confirm correct buckets | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full lib + integration suite | `cargo test --all-targets` | 89 lib + 16 integration = 105 total, 0 failed | ✓ PASS |
| CR-01 fixture regression: kinds=[1] until=1700000256 returns levIds 7 AND 8 | `cargo test --lib lmdb::scan::tests::test_scan_reverse_until_existing_ts_keeps_both_dups` | PASS — output shows `[8, 7, 6, 5, 4]` | ✓ PASS |
| CR-02 regression: multistream cross-iteration global DESC order | `cargo test --lib query::engine::tests::test_execute_query_multistream_cross_iteration_order` | PASS | ✓ PASS |
| CR-03 regression: per-stream since (dense stream not starved by sparse) | `cargo test --lib query::engine::tests::test_execute_query_multistream_since_per_stream` | PASS | ✓ PASS |
| No sort_unstable_by in engine production code | `grep sort_unstable_by src/query/engine.rs` (non-comment lines) | Only appears in a comment ("No sort_unstable_by here") — absent as code | ✓ PASS |
| No since_cutoff variable in engine | `grep since_cutoff src/query/engine.rs` | Only in comments and test strings, not as a variable | ✓ PASS |
| merge_windowed called once with no backfill loop (new REVIEW CR-01 confirmed) | `grep -n "merge_windowed\|for.*round\|MAX_ROUNDS" engine.rs` | One call at line 197; no round-loop; comment at 172-190 describes absent loop | ✗ FAIL (BLOCKER) |
| next_cursor = None when valid.len() < limit regardless of remaining events | Review engine.rs:302-309 | `if valid.len() == limit { Some(cursor) } else { None }` — confirmed unconditional | ✗ FAIL (BLOCKER) |

### Probe Execution

No probes declared or found. Step 7c: SKIPPED (no runnable probe scripts in scripts/).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| QRY-01 | 03-01, 03-02, 03-04, 03-06, 03-08, 03-09 | events() resolved by scanning most selective index | PARTIAL | Index selection, routing, since enforcement, kind/author/id residual, dup-correct Reverse bounds, and k-way ordering all implemented and tested; pagination under residual-heavy filters broken (new REVIEW CR-01) |
| QRY-02 | 03-02, 03-04, 03-07 | Tag filters via Event__tag with NIP-01 AND semantics | SATISFIED | 64-char lowercase hex decode; AND residual; 5 regression tests pass |
| QRY-03 | 03-04 | latestPerAuthor via Event__pubkeyKind | SATISFIED | latest_per_author() implemented and fixture-tested; REQUIREMENTS.md "Pending" is stale tracking |
| QRY-04 | 03-03, 03-05 | Hydrate EventPayload[levId] for matched levIds | SATISFIED | hydrate_lev_ids returns lev_id-associated pairs; lev_id-join in engine; 4 tests pass |
| QRY-05 | 03-04 | NIP-40 expiration filtering | SATISFIED | is_expired() present and applied in both query paths; unit test covers all NIP-40 cases; REQUIREMENTS.md "Pending" is stale tracking |

**Documentation note:** REQUIREMENTS.md marks QRY-03 and QRY-05 as "Pending" but the implementations exist and tests pass. This is stale tracking, not an implementation gap.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `src/query/engine.rs` | 172-190 | Comment describes a round-loop ("the engine calls merge_windowed again from the resume point") that is not implemented in the code that follows | WARNING | Comment and code diverge; the comment creates a false sense that the D-07 over-fetch guarantee is met in all cases. The single-call implementation makes it a blocker for pagination correctness (new REVIEW CR-01). |
| `src/query/merge.rs` | 42-62 | `#[derive(Eq, PartialEq)]` compares all 4 fields; `Ord::cmp` compares only `(created_at, lev_id)` — Eq/Ord inconsistency (carried IN-01 from REVIEW) | INFO | Harmless for BinaryHeap today; latent trap for ordered collections. |
| `src/lmdb/scan.rs` | 359-362 | `debug_assert!` guards `start_key.len() >= 8` in `reverse_upper_bound` — release builds panic on short keys (carried WR-04 from REVIEW) | WARNING | Reachable from any public Reverse scan entry point with a key under 8 bytes; current callers always build keys >= 8 bytes but Phase 4+ callers get a process abort instead of an Err. |

### Human Verification Required

None — all items resolved programmatically.

## Gaps Summary

### Progress

Plans 03-08 and 03-09 closed all three prior blockers (CR-01 dup drop, CR-02 sort-per-batch, CR-03 global since_cutoff). Code evidence and dedicated regression tests confirm each fix. The test suite has grown from 81 lib + 17 integration to 89 lib + 16 integration (105 total), all passing.

### Remaining Blocker: New REVIEW CR-01 — No Backfill Loop

The 03-09 rewrite achieved correct k-way merge ordering (CR-02/CR-03) by replacing the `StreamState` iteration loop with a stateless `merge_windowed` call. However, it replaced the iteration loop with a single call bounded by `emit_limit = 2*limit + 256`, with no second round.

**Structural evidence:**
- `engine.rs:197`: `let merge_batch = merge_windowed(..., emit_limit, ...)` — one call only.
- `engine.rs:302-309`: `next_cursor = if valid.len() == limit { Some(...) } else { None }` — no cursor returned when under-full.
- `engine.rs:172-190`: 19-line comment block describes a round-loop that does not exist in the code below it.

**Failure conditions on real data (not reachable in the 11-event fixture):**
1. **Residual-only predicates:** A `kinds=[1] + #p tag` filter routes to `Event__kind` (tag checked only post-hydration). If the newest `2*limit+256` kind-1 events contain no #p matches, the filter returns 0 events with `next_cursor = None` even though millions of older matching events exist.
2. **NIP-40 expiry runs:** A stretch of more than `limit+256` expired events at the newest end of a partition (common on relays carrying ephemeral content) empties the page with no cursor.
3. **Cursor-resume on dense seconds:** In `Event__created_at` default-feed paging, all events sharing the cursor's second are emitted by the merge and then discarded by cursor exclusion (engine.rs:203-212), consuming headroom before any new event is seen.

**Fix direction (from 03-REVIEW.md CR-01):** Restore a bounded round-loop with an explicit budget and a partial-result cursor. Essential properties: (a) loop until `limit` or true merge exhaustion (`merge_batch.len() < emit_limit` is the exhaustion signal), capped by a `MAX_ROUNDS`/entries budget; (b) when the budget stops the loop early, still return a resume cursor so the remainder is reachable. The `emit_limit` single-call model is what currently keeps the DoS boundary (WR-03) from reopening — the fix must preserve a budget.

---

_Verified: 2026-06-12T13:30:00Z_
_Verifier: Claude (gsd-verifier)_
