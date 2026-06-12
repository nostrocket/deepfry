---
phase: 03-query-engine
reviewed: 2026-06-12T07:03:11Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - Cargo.toml
  - src/lib.rs
  - src/lmdb/scan.rs
  - src/query/mod.rs
  - src/query/engine.rs
  - src/query/filter.rs
  - src/query/hydrate.rs
  - src/query/merge.rs
  - src/query/router.rs
findings:
  critical: 3
  warning: 4
  info: 5
  total: 12
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-12T07:03:11Z
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

This is a re-review after gap-closure plans 03-05/06/07 (lev_id-join hydration, prefix-guarded
merge + since stop-bound + key-granular exclusive-resume windowing, strfry-exact tag decode +
NIP-01 AND tag residual). Those fixes are present and correct as far as they go: the lev_id join
(hydrate.rs / engine.rs), the prefix guards, the dup-group-draining `collect_window`, the tag
AND-across-fields residual, and the strfry-exact 64-lowercase-hex tag value decode all check out
against the code and the fixture tests.

However, the review found **three remaining correctness blockers**, all in the query-engine layer:

1. The CR-01 fix for heed's reverse-DUPSORT `Included`-bound asymmetry was applied only to window
   *resume*, not to the *first* window. Any reverse scan whose start bound is an **existing** key
   (which is true by construction for every cursor-resumed page, and true whenever `filter.until`
   equals a real event timestamp) silently drops the higher duplicates of the boundary key. The
   repo's own proof test (`tests/dupsort_resume_test.rs::test_old_code_reverse_drops_levid_nonvacuity`)
   empirically demonstrates the exact heed behavior that makes this a data-loss path.
2. The engine's multi-stream over-fetch loop is not a real k-way merge: it sorts each iteration's
   combined batch in isolation, so results can be emitted out of `(created_at DESC)` order across
   iterations, which corrupts the page cursor (duplicates and skips across pages).
3. The CR-02 `since` stop-bound is global: the first stream to dip below `since` terminates the
   whole loop, silently starving other streams that still have matching events.

Findings 2 and 3 share a root cause — the engine bypasses `merge.rs`'s heap merge and substitutes
a sort-each-batch approximation. The fixture (11 events, dup groups of size 2) is too small to
expose any of the three blockers in the existing test suite; all three are reachable on a real
strfry database.

## Critical Issues

### CR-01: Reverse first-window `Bound::Included` on an existing key drops the boundary key's higher duplicates — events lost at `until` and at every cursor page boundary

**File:** `src/lmdb/scan.rs:370-374` (collect_bounded Reverse), `src/lmdb/scan.rs:461-469` (collect_window, `first_batch` arm), `src/query/engine.rs:155-165` (cursor → until override)

**Issue:** The gap-closure fix (key-granular exclusive resume) made *resumed* windows use
`Bound::Excluded(drained_key)`, which is correct. But the **first** window — and every
`scan_index_bounded` Reverse call — still uses `Bound::Included(start_key)`. The repo's own
empirical proof (`tests/dupsort_resume_test.rs`, non-vacuity test, lines 331-425) demonstrates
heed 0.22.1's behavior when the `rev_range` end bound is `Included(K)` and **K exists**:
`move_on_range_end` positions at the **smallest** dup of K, yields it, then `MDB_PREV` steps to
the previous KEY — the higher dups of K are **never yielded**.

This bound key exists in two production-reachable cases:

1. **`filter.until` equals a real event timestamp.** Reproducible against the committed fixture
   today: `kinds=[1], until=1700000256` should return levIds `[8,7,6,5,4]`; the key
   `kind=1‖ts=1700000256` exists with dup group `[7,8]`, so the scan yields only the smallest dup
   `7` and levId **8 — a matching event at exactly `until` — is silently dropped** with no
   residual or cursor logic to recover it. NIP-01 `until` is inclusive; this violates it.
2. **Every cursor-resumed page.** `execute_query` sets `until = cursor.created_at`
   (engine.rs:155-165), and the cursor's timestamp is *by construction* the timestamp of a real
   emitted event, so for the stream that produced the cursor the bound key always exists. With a
   dup group of size ≥ 3 at the cursor timestamp (e.g. levIds `[a,b,c]`, cursor at `c`), page 2
   positions at `a`, yields it, and skips `b` — **`b` is lost from pagination forever**. The
   fixture's dup groups are size 2, which is exactly the size at which this is invisible (smallest
   dup yielded + larger dup already cursor-excluded), so the existing cursor tests pass while the
   ≥3 case loses data. Real strfry databases have many events per `(kind, created_at)` second —
   and for the `Event__created_at` default feed, the dup group is *all events in that second*,
   making loss at page boundaries routine.

**Fix:** For Reverse first-batch bounds derived from a finite `until`/cursor timestamp, position
*above* the boundary key instead of on it, and let the prefix guard / per-entry filters do the
trimming:

```rust
// build the reverse start bound as Excluded(prefix ‖ (ts + 1)) instead of Included(prefix ‖ ts)
let upper = if ts == u64::MAX {
    Bound::Included(resume_key)           // ts=MAX key cannot meaningfully exist; Included is safe
} else {
    // bound_key = prefix ‖ (ts + 1): SET_RANGE finds the first key >= it, steps back, and lands
    // on the LAST dup of prefix‖ts — the full dup group is then walked descending (proven order).
    Bound::Excluded(bound_key_with_ts_plus_one)
};
```

Apply the same treatment to `collect_bounded`'s Reverse arm (`scan_index_bounded` is used by
`latest_per_author` — currently safe only because its start ts is `u64::MAX` — and by
`merge_prefixes`, which inherits the bug whenever `until` is set). Add a fixture regression test:
`kinds=[1], until=1700000256` must return 5 events including levId 8.

### CR-02: Multi-stream over-fetch loop emits out of `(created_at DESC)` order across iterations — page cursor then duplicates/skips events across pages

**File:** `src/query/engine.rs:203-383`

**Issue:** The loop fetches one window per stream per iteration, sorts the **combined batch of
that iteration only** (engine.rs:267-269), and appends survivors to `valid`. This is not a k-way
merge: each stream's iteration-2 window contains entries older than *that stream's* iteration-1
window, but possibly **newer than another stream's iteration-1 entries that were already emitted**.

Concrete failure: `kinds=[1,2]`, stream A (kind 1) has 600 entries at ts 1000→900, stream B
(kind 2) has 3 entries at ts 500, `limit=300` (or any limit the first iteration's survivors can't
fill — residual drops, NIP-40 expiry, and cursor exclusions make multi-iteration common even with
limit < 256). Iteration 1 emits A's ts 1000→~950 then B's ts 500. Iteration 2 emits A's
ts ~950→900 — *after* the ts-500 entries. Consequences:

- The returned page violates the documented D-10 `(created_at DESC, lev_id DESC)` contract.
- The next-page cursor is built from `valid.last()` (e.g. ts≈900). On page 2, B's ts-500 entries
  pass the cursor exclusion (`ts < cur_ts`) and are **re-emitted — duplicates across pages**.
  Symmetrically, events can be skipped depending on where truncation lands.

The fixture cannot expose this: all multi-stream tests either fit in one iteration or use a single
stream with `batch_size` overrides.

**Fix:** Implement a true incremental merge with a per-stream **frontier**: after fetching a
window for each non-exhausted stream, only entries with
`(ts, lev_id) >= max(low_watermark of every non-exhausted stream)` may be emitted (where a
stream's low watermark is the sort key of the last entry in its current fetched window); buffer
the remainder for the next round and refill only the stream(s) whose buffer ran dry. The
`BinaryHeap` machinery in `merge.rs` is the right shape — extend it with windowed refill instead
of maintaining a second, divergent merge implementation (see WR-04). Add a regression test with
two synthetic streams of different time densities and a small `batch_size`, asserting global
DESC order and exact page-1 ∪ page-2 == single-query prefix.

### CR-03: Global `since_cutoff` terminates ALL streams when one stream passes below `since` — matching events from other streams silently omitted

**File:** `src/query/engine.rs:272-303, 380`

**Issue:** `since_cutoff` is set if **any** entry in the iteration's *combined* batch has
`ts < since` (engine.rs:282-286), and `if valid.len() >= limit || since_cutoff { break; }`
(engine.rs:380) then aborts the **entire** loop. Streams progress through time at unrelated
rates: a sparse stream reaches pre-`since` territory while a dense stream still has unfetched
entries `>= since`.

Concrete failure: `kinds=[1,2], since=T, limit=300`. Kind-2 has only events at `ts < T`; kind-1
has 300+ events at `ts >= T`. Iteration 1: kind-1 contributes 256 valid entries, kind-2's window
contains a `ts < T` entry → `since_cutoff = true` → loop breaks after this batch. The remaining
kind-1 events `>= since` are **never returned**, even though `limit` was not reached. No error,
no warning — silent under-reporting. (The per-entry `ts < since` filter at engine.rs:282-287
keeps the *returned* set correct; the defect is purely missing results.)

**Fix:** Make the cutoff per-stream. Each stream's guarded batch is internally time-descending,
so the first `ts < since` entry within a stream means *that stream* is done:

```rust
// inside the per-stream loop, after the prefix guard:
let mut guarded = guarded;
if let Some(pos) = guarded.iter().position(|(ts, _, _)| *ts < since) {
    guarded.truncate(pos);
    s.exhausted = true;   // only THIS stream is past since
}
```

Remove `since_cutoff` from the outer break condition entirely; loop termination then falls out of
all-streams-exhausted. Add a regression test with two kinds of different time ranges and a
`since` between them.

## Warnings

### WR-01: `hydrate_lev_ids` hard-fails the entire query on a missing levId — guaranteed transient failures against a live strfry database

**File:** `src/query/hydrate.rs:54-56`
**Issue:** The index scan and the payload lookup run in **separate** read transactions (by design,
D-08). Against a live strfry, an event can be deleted between the two txns — strfry deletes events
routinely (replaceable kinds 0/3/1xxxx overwrite, NIP-09 deletion requests, NIP-40 expiry pruning).
A levId harvested from the index scan then misses in `EventPayload`, `get_event_payload` returns
`LevIdNotFound`, and the **whole query fails**. The doc comment's premise ("a levId present in a
real index scan must exist in EventPayload") only holds within a single txn — which this code
deliberately does not use. This also takes down all buckets in `latest_per_author` (engine.rs:496).
**Fix:** Treat `LevIdNotFound` (specifically — not other LMDB errors) like the decode-failure
path: `tracing::warn!`, `*skip_count += 1`, omit the slot. The lev_id-join architecture (CR-05
fix from the prior cycle) already makes absent slots safe for callers.

### WR-02: Unvalidated `filter.limit` flows into `Vec::with_capacity` — capacity-overflow panic / multi-GB allocation from caller input

**File:** `src/query/engine.rs:200`, `src/query/merge.rs:163`
**Issue:** `filter.limit` is caller-supplied `usize` with no engine-side ceiling;
`Vec::with_capacity(limit)` with `limit = usize::MAX` panics with "capacity overflow", and any
large value pre-allocates `limit * sizeof((u64, u64, DecodedEvent))` bytes up front. The comment
says "Phase 4 enforces the hard ceiling before calling the engine" — Phase 4 does not exist yet,
and the engine is the trust boundary for its own DoS claims (T-03-DOS). Defense-in-depth is absent.
**Fix:** Clamp in the engine: `const MAX_LIMIT: usize = 1_000;` (or similar) and
`let limit = filter.limit.clamp(1, MAX_LIMIT)` — or at minimum
`Vec::with_capacity(limit.min(DEFAULT_WINDOW_SIZE))`.

### WR-03: T-03-DOS claim is false for residual-heavy filters — over-fetch loop walks and hydrates an entire index partition with no scan budget

**File:** `src/query/engine.rs:203-383`
**Issue:** The loop terminates on `valid.len() >= limit` or stream exhaustion. A filter whose
residual predicates drop everything — e.g. `kinds=[1]` plus a tag filter no kind-1 event satisfies
(routing uses kinds; the tag is residual-only, engine.rs:358-369) — windows through and **hydrates
every kind-1 event in the database** before returning empty. On a production relay that is
millions of payload decodes for one query. The header comment "bounded by `limit`" (engine.rs:99)
is incorrect for this class of input; this is a query-of-death vector, not merely a performance
nit.
**Fix:** Add a per-query budget (e.g. max total scanned entries, or max loop iterations derived
from `limit` and `batch_size`); on budget exhaustion return the partial `valid` plus a resume
cursor so callers can continue explicitly.

### WR-04: `merge_prefixes` is dead code — the engine reimplements merging differently (and incorrectly), while comments still describe merge.rs as the live path

**File:** `src/query/merge.rs:103-185`, `src/query/engine.rs:239-241`
**Issue:** `merge_prefixes` (and `MergeCandidate`) are referenced only by merge.rs's own tests —
verified by grep: no production call sites. The engine's `execute_query_internal` does its own
sort-per-batch pseudo-merge instead, which is the root cause of CR-02. Comments in engine.rs
("Redundant with merge.rs guard if called through merge_prefixes") and router.rs:289 imply
merge_prefixes is in the execution path; it is not. Two divergent merge implementations — one
correct-but-unused, one used-but-wrong — is exactly how CR-02 survived prior review cycles.
`merge_prefixes` also inherits CR-01 via `scan_index_bounded` Reverse when `until` is set.
**Fix:** Resolve in one direction as part of the CR-02 fix: extend merge.rs's heap merge with
windowed per-stream refill and route the engine through it, then delete the engine's inline merge;
or delete merge.rs. Update the stale comments either way.

## Info

### IN-01: Dead variable `stream_prefixes` built and immediately dropped

**File:** `src/query/merge.rs:137-151`
**Issue:** `stream_prefixes` is populated in the loop and then `drop(stream_prefixes)` with a
comment admitting it was never needed (prefixes are consumed inline during guarded collection).
**Fix:** Delete the vector and the `drop`.

### IN-02: Empty `if since_cutoff { }` block containing only comments

**File:** `src/query/engine.rs:293-298`
**Issue:** A conditional whose body is entirely comments ("We still process the survivors below
before breaking") — dead code that obscures the actual control flow and will confuse the CR-03
rework.
**Fix:** Delete the block; move the comment to the `if valid.len() >= limit || since_cutoff`
break site (or remove it entirely once CR-03 makes the cutoff per-stream).

### IN-03: `skip_count` accumulated across the query but never surfaced

**File:** `src/query/engine.rs:201, 307`
**Issue:** `execute_query_internal` threads `&mut skip_count` through every hydrate call but never
logs, returns, or otherwise uses the total. Per-skip warns exist in hydrate.rs, but the per-query
aggregate (useful for detecting payload-corruption trends) is computed and discarded.
**Fix:** Either `tracing::debug!(skip_count, "query completed with skipped payloads")` before
returning when `skip_count > 0`, or remove the accumulator.

### IN-04: `MergeCandidate` Ord is inconsistent with its derived Eq

**File:** `src/query/merge.rs:44-70`
**Issue:** `#[derive(Eq, PartialEq)]` compares all four fields (including `key_bytes` and
`stream_idx`), but `Ord::cmp` compares only `(created_at, lev_id)`. Two candidates can be
`cmp == Equal` yet `!=`, violating the documented consistency requirement between `Ord` and `Eq`.
Harmless for `BinaryHeap` today, but a latent trap for any future use in ordered collections.
**Fix:** Implement `PartialEq`/`Eq` manually over `(created_at, lev_id)` to match `Ord` (lev_id
is unique, so this is still a valid equivalence).

### IN-05: `collect_window` with `window_size = 0` returns an empty batch on a non-empty index — caller misreads it as stream exhaustion

**File:** `src/lmdb/scan.rs:417-493`, callers at `src/lmdb/scan.rs:182-241, 276-323`
**Issue:** With `window_size = 0`, the drain condition `results.len() >= window_size && last != key`
fires on the very first item (`results.last()` is `None`), so the batch is empty and
`scan_index_windowed` / the engine mark the stream exhausted — a silently-empty scan over a
non-empty index. Only reachable via the public `scan_index_windowed`/`scan_index_one_window`
parameters today, but it is a footgun for future callers.
**Fix:** `let window_size = window_size.max(1);` at the top of `collect_window`, or
`debug_assert!(window_size > 0)`.

---

_Reviewed: 2026-06-12T07:03:11Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
