---
phase: 03-query-engine
reviewed: 2026-06-13T00:00:00Z
depth: standard
files_reviewed: 6
files_reviewed_list:
  - src/lmdb/scan.rs
  - src/query/engine.rs
  - src/query/filter.rs
  - src/query/hydrate.rs
  - src/query/merge.rs
  - src/query/router.rs
findings:
  critical: 2
  warning: 4
  info: 3
  total: 9
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-13
**Depth:** standard
**Files Reviewed:** 6
**Status:** issues_found

## Summary

Reviewed the Phase-3 query engine: scan primitives (`scan.rs`), the bounded round-loop engine (`engine.rs`), filter/cursor types (`filter.rs`), hydration (`hydrate.rs`), the k-way windowed merge (`merge.rs`), and index selection/start-key construction (`router.rs`). The code is unusually well-documented and carries a dense regression suite; the merge frontier (CR-02 lineage), per-stream `since` exhaustion (CR-03 lineage), DUPSORT key-granular windowing (CR-01 lineage), the lev-id join in hydration (CR-05 lineage), and the `MergeCandidate` Eq/Ord consistency (the prompt's focus) are all **correct**.

However, the plan 03-10 round-loop rewrite in `execute_query_internal` introduces **two stranding/non-progress defects**. Both cause reachable events to become silently and permanently unreachable through pagination — the worst failure mode for an engine whose value proposition is correct, complete result sets over strfry's live data. Neither surfaces an error; the caller simply receives a truncated stream and a `None` (or stalled) cursor and concludes the stream ended.

Verified sound and explicitly cleared: the `MergeCandidate` manual `Eq`/`PartialEq` compares the same `(created_at, lev_id)` key that `Ord::cmp` uses, so Eq and Ord are consistent (the derived `Eq` over `key_bytes`/`stream_idx` would have been the inconsistency, correctly avoided); `reverse_upper_bound`'s sub-8-byte fail-soft guard returns the Included-fallback tuple without panicking; the merge frontier emits each lev-id exactly once per `merge_windowed` call; the `take_while(starts_with(prefix))` prefix guard handles the empty-prefix `Event__created_at` default feed correctly.

## Critical Issues

### CR-01: Round-loop strands all events below the page when `valid` is empty but the merge is not exhausted

**File:** `src/query/engine.rs:356-370` (loop break at `333`)
**Issue:**
When the round budget (`MAX_ROUNDS`) is hit before a single survivor accumulates, `valid` is empty and `exhausted` is `false` (matching events exist further down the index; the loop just ran out of budget reaching them). The cursor builder then falls through to the final `else` and returns `next_cursor = None`:

```rust
let next_cursor = if valid.len() == limit {
    ...
} else if !valid.is_empty() && !exhausted {   // guards on !valid.is_empty()
    ...
} else {
    None   // taken when valid IS empty, even though !exhausted
};
```

A caller receiving `(events: [], cursor: None)` correctly interprets that as end-of-stream and stops paging. But it is NOT end-of-stream: `!exhausted` means the merge still had `emit_limit` entries available below the last scanned position. Every matching event below the budget horizon is permanently stranded.

This triggers whenever the routed index is large and the residual filter (post-hydration `tags`/`is_expired`/`kinds`/`authors`/`ids` mismatch, or cursor exclusion) is selective enough that the first `MAX_ROUNDS × emit_limit` scanned entries all fail. It is the same shape as `test_execute_query_residual_deep_match_reachable` — but that test only exercises the case where page 1 collects ≥1 survivor. Push the matching events deeper (or raise `limit`) so page 1 collects zero and the bug appears.

The comment at line 355 ("OR valid empty → None (true end of stream)") encodes the bug as intent: `valid` empty is true end-of-stream only when `exhausted` is also true.

**Fix:** When the budget cap stops the loop with `!exhausted`, return a resume cursor at the deepest position actually scanned, even when `valid` is empty. Track the last round's `last_merged` outside the loop and use it as the fallback:

```rust
let mut deepest_scanned: Option<(u64, LevId)> = None;
// inside the loop, after computing last_merged:
if let Some(lm) = last_merged { deepest_scanned = Some(lm); }
// cursor builder, new branch:
} else if valid.is_empty() && !exhausted {
    // Budget cap hit before any survivor — resume from the deepest scanned boundary so
    // the caller can page past the residual-heavy region instead of seeing a false EOF.
    deepest_scanned.map(|(ts, lev_id)| PageCursor { created_at: ts, lev_id })
}
```

Add a regression test mirroring `test_execute_query_residual_deep_match_reachable` but forcing page 1 to collect zero survivors (residual matches only the oldest events, `MAX_ROUNDS`-deep, `batch_size=1`), asserting the cursor is `Some` and a follow-up page reaches the matches.

### CR-02: Round-loop cannot advance past a `created_at` holding ≥ `emit_limit` entries — pagination stalls and strands the remainder

**File:** `src/query/engine.rs:208-210, 226-234, 337-339`
**Issue:**
The merge's upper bound is `round_until`, which is **timestamp-granular only** — it discards the `lev_id` of `round_boundary`:

```rust
let round_until: u64 = round_boundary
    .map(|(ts, _)| ts)                      // lev_id dropped
    .unwrap_or_else(|| filter.until.unwrap_or(u64::MAX));
```

So the merge re-scans every entry at `ts == round_until` each round (the start key carries `until = round_until`, and the Reverse scan includes that ts via the `ts+1` Excluded bound in `reverse_upper_bound`). The heap is `(created_at DESC, lev_id DESC)`, so the first `emit_limit` triples are deterministically the highest-`lev_id` entries at that ts — identical every round when a single timestamp holds `≥ emit_limit` entries.

Failure trace:
1. Round N: merge returns `emit_limit` triples, all at `ts_L`, ending at `last_merged = (ts_L, lev_L)` (`lev_L` = smallest lev in the batch). `valid` under-full (residual drops, or all cursor-excluded).
2. `round_boundary = (ts_L, lev_L)`, `round_until = ts_L`.
3. Round N+1: merge re-scans `ts <= ts_L`, returns the **same** `emit_limit` highest-lev entries at `ts_L`, ending again at `(ts_L, lev_L)`.
4. Cursor-exclusion (`ts == cur_ts && lev_id >= cur_lev`) drops all of them → `filtered_batch` empty → `valid` unchanged → `last_merged == round_boundary`.
5. The loop makes **zero forward progress**; it exits only via `rounds >= MAX_ROUNDS`. Any partial cursor points at `(ts_L, lev_L)` again, so the next page repeats the identical stall. Everything below `ts_L` is permanently unreachable.

Requires `≥ emit_limit` events at one `created_at` second. `emit_limit = 2*limit + DEFAULT_WINDOW_SIZE ≥ 256`, so ~256+ events at the same second — plausible on a busy relay or during bulk import, and exactly the silently-wrong range-scan behavior CLAUDE.md flags as the core correctness risk.

**Fix:** Advance the boundary on the full `(ts, lev_id)`, not just `ts`, so progress is monotonic within a fat timestamp — thread `lev_id` into the merge/`scan_index_one_window` exclusive-resume bound (the windowing already supports `Bound::Excluded` resume). As a minimum safety net, detect the no-progress condition and break instead of spinning to `MAX_ROUNDS`:

```rust
// after computing last_merged, before `round_boundary = last_merged`:
if last_merged == round_boundary {
    // No progress at ts granularity — the round re-scanned the same prefix. The ts-only
    // `until` cannot descend below a fat timestamp; push lev_id into the scan bound, or
    // stop. Spinning to MAX_ROUNDS here strands everything below ts_L.
    break;
}
```

Add a regression test using a synthetic env with `> emit_limit` events at one `created_at` and a `limit` smaller than that group, asserting full pagination returns the events below it.

## Warnings

### WR-01: `latest_per_author` reverse scan capped at `per_author` before the `(pubkey,kind)` filter — brittle, can under-fill

**File:** `src/query/engine.rs:437-462`
**Issue:** `scan_index_bounded(..., Reverse, &start_key, per_author)` caps at `per_author` entries, then `matching` filters to the exact `(pubkey, kind)` pair. Correctness relies on a non-obvious ordering argument: the trailing `u64::MAX` created_at puts larger kinds above the start (never visited in reverse), and the target kind's events form the first contiguous run before smaller kinds. Today the bucket is never under-filled, but the cap-before-filter pattern is fragile — any comparator change or interleaved-kind layout could silently return fewer than `per_author` events with no error.
**Fix:** Drain only the target `(pubkey, kind)` prefix via a windowed scan with `take_while(starts_with(prefix))` (mirroring the merge), or assert inline that every scanned key before the filter shares the target kind prefix. Add a test where a pubkey's smaller-kind events outnumber `per_author`.

### WR-02: First-round merge bound drops the caller cursor's `lev_id`; page-boundary correctness has no defense-in-depth

**File:** `src/query/engine.rs:166, 208-210, 243-247`
**Issue:** On round 1, `round_boundary = (cursor.created_at, cursor.lev_id)` but `round_until` uses only `cursor.created_at`, so the merge re-scans the full `ts == cursor.created_at` dup group. Correctness then depends entirely on the in-engine exclusion filter (`ts == cur_ts && lev_id >= cur_lev`) to drop already-emitted siblings. That filter is present and correct, so no double-emit occurs today — but the boundary is encoded in two places with inverted strictness, and weakening the comparison (`>` instead of `>=`) would silently re-emit the cursor event. No second layer protects the page boundary.
**Fix:** Push the cursor `lev_id` into the scan resume bound so the merge itself never yields already-emitted entries (same lev-id-in-bound work as CR-02), making the residual exclusion a true belt-and-braces.

### WR-03: `exhausted` reflects only the final round and is unreliable as the documented "true end of stream" signal

**File:** `src/query/engine.rs:201, 231, 356-370`
**Issue:** `exhausted = merge_batch.len() < emit_limit` is recomputed each round. Because every round rebuilds `start_keys` and re-scans from the top of `round_until`, a round can return a full `emit_limit` batch (`exhausted = false`) even when nearly drained. The cursor decision at line 356 reads only the breaking round's value. Under CR-02's stall the breaking round is always full, so `exhausted` stays false and the engine never recognizes the true end — coupling this WARNING to CR-02. The logic is correct in the common case but the `exhausted` semantics are fragile and undocumented as last-round-only.
**Fix:** After fixing CR-01/CR-02, document that `exhausted` is the last-round signal meaningful only with no-progress detection. Consider a sticky `ever_made_progress` flag to disambiguate the empty-page case.

### WR-04: `merge_prefixes` passes `scan_limit` as `batch_size`, defeating windowing for any non-test caller

**File:** `src/query/merge.rs:335-354`
**Issue:** `merge_prefixes` sets `batch_size = scan_limit` (= `limit`, default 256) so each `scan_index_one_window` materializes up to `limit` entries per stream in one window. For a large `limit` this is a much larger per-txn working set than the `DEFAULT_WINDOW_SIZE` windowing is meant to bound (LMDB-09 short-txn / page-reclaim pressure). It is `pub fn`; a non-test caller with a large limit reintroduces the large-window behavior the windowed path was designed to avoid.
**Fix:** Pass `DEFAULT_WINDOW_SIZE` (or a small constant) as `batch_size` and keep `emit_limit = scan_limit`; the frontier merge already refills across windows correctly, so single-window materialization is unnecessary.

## Info

### IN-01: Dead `unwrap_or` after a length guard in `reverse_upper_bound`

**File:** `src/lmdb/scan.rs:376`
**Issue:** After `if start_key.len() < 8 { return ... }`, the slice `start_key[len - 8..len]` is always exactly 8 bytes, so `try_into()` cannot fail and `.unwrap_or([0u8; 8])` is unreachable — it implies a fallback that can never occur.
**Fix:** Use `.expect("len checked >= 8")` to document the invariant, or keep `unwrap_or` with a comment marking it defensive-only.

### IN-02: `decode_hex_32` double-validates length

**File:** `src/query/engine.rs:500-508`
**Issue:** `decode_hex_32` checks `s.len() != 64`, then calls `decode_hex` (rejects odd length / invalid nibbles) and `try_into::<[u8;32]>` (rejects non-32-byte results). The explicit length check is redundant with the downstream `try_into`.
**Fix:** Drop the explicit check and rely on `decode_hex` + `try_into`, or keep it as a documented fast-path.

### IN-03: `merge_windowed` pre-allocation capped at 256 may under-allocate the large-query path

**File:** `src/query/merge.rs:281`
**Issue:** `results` is pre-sized to `emit_limit.min(256)`. Since the engine's `emit_limit = 2*limit + 256 ≥ 256`, the cap forces a realloc mid-merge for larger queries. Allocation-efficiency only (out of v1 perf scope), noted because `.min(256)` reads like a deliberate cap that may surprise future readers.
**Fix:** Use `Vec::with_capacity(emit_limit)`, or comment that 256 is a deliberate guard against a hostile `emit_limit`.

---

_Reviewed: 2026-06-13_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
