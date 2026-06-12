---
phase: 03-query-engine
reviewed: 2026-06-12T12:58:49Z
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
  critical: 1
  warning: 6
  info: 4
  total: 11
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-12T12:58:49Z
**Depth:** standard
**Files Reviewed:** 6
**Status:** issues_found

## Summary

Re-review after gap-closure plans 03-08 (prior CR-01: reverse first-window `Bound::Excluded(ts+1)`)
and 03-09 (prior CR-02/CR-03: windowed k-way merge as the single merge path, per-stream since).

**Prior findings verified RESOLVED:**

- **Prior CR-01** (reverse first-window `Included` drops boundary dups): fixed. `reverse_upper_bound`
  (scan.rs:359-381) rebuilds the bound as `prefix ‖ (ts+1)` with `Bound::Excluded`, applied in both
  `collect_bounded` Reverse (scan.rs:439-444) and `collect_window` first-batch Reverse
  (scan.rs:546-561). Fixture regression (`test_scan_reverse_until_existing_ts_keeps_both_dups`) and
  synthetic 3-dup tests (scan.rs:991-1055) prove it, including the previously-invisible size-≥3 case.
- **Prior CR-02** (sort-per-batch pseudo-merge): fixed. `merge_windowed` (merge.rs:227-296) is a true
  BinaryHeap frontier merge with per-stream windowed refill; the engine's sort-per-iteration loop is
  gone and engine.rs:197 routes through `merge_windowed` exclusively. Cross-iteration order and
  page-union regressions exist (engine.rs:1258-1358).
- **Prior CR-03** (global since cutoff starving streams): fixed. `refill_stream` truncates per stream
  at the first `ts < since` and exhausts only that stream (merge.rs:157-168); regression at
  engine.rs:1382-1434.
- **Prior WR-04** (dead/divergent merge implementations): resolved — `merge_prefixes` is now a thin
  wrapper over `merge_windowed` (merge.rs:324-343); one merge implementation remains.
- **Prior IN-01** (dead `stream_prefixes`), **IN-02** (empty `if since_cutoff` block), **IN-03**
  (unused `skip_count` aggregate): all gone (rewritten merge; `tracing::debug!` at engine.rs:297-299).

**However, the 03-09 rewrite traded one blocker for another.** The engine's documented over-fetch
*loop* (D-07) no longer exists: `execute_query_internal` makes exactly **one** `merge_windowed` call
with fixed headroom (`2*limit + 256`) and never goes back for more. Any filter whose drop rate
(residual tag/kind/author mismatch, NIP-40 expiry, cursor exclusion) exceeds that headroom returns
fewer than `limit` events **and `next_cursor = None`** — the remaining matches become silently
unreachable. The 15-line comment above the call (engine.rs:172-190) describes a round-loop the code
does not contain.

Prior WR-01, WR-02, IN-04, IN-05 remain unresolved and are carried forward with fresh IDs below.

## Critical Issues

### CR-01: No backfill loop — one fixed-headroom `merge_windowed` call; residual-heavy / expiry-heavy filters silently under-return with NO resume cursor

**File:** `src/query/engine.rs:191-198` (single call), `src/query/engine.rs:172-190` (comment describing a loop that does not exist), `src/query/engine.rs:302-309` (cursor only when `valid.len() == limit`)

**Issue:** `execute_query_internal` computes `emit_limit = limit + DEFAULT_WINDOW_SIZE + limit`
(engine.rs:191) and calls `merge_windowed` **once** (engine.rs:197). Survivors of cursor exclusion,
hydration skips, NIP-40 expiry, and the residual kind/author/id/tag predicates are pushed into
`valid` until `limit`. There is no second round. The accompanying comment block explicitly describes
the intended behavior — *"If `valid.len() < limit` after a round, the engine calls merge_windowed
again from the resume point"* (engine.rs:178-180) — and then admits the implementation substitutes a
single call hoping headroom suffices (engine.rs:185-190). The module header's claim *"over-fetches +
hydrates + drops … until `limit` valid events are collected (D-07)"* (engine.rs:6-7) is therefore
false.

Consequence whenever more than `emit_limit - limit` (= `limit + 256`) consecutive merge entries are
dropped before `limit` survivors accumulate:

1. The page returns `< limit` events even though more matching events exist deeper in the index.
2. Because the cursor is built **only** when `valid.len() == limit` (engine.rs:302-309),
   `next_cursor` is `None` — the caller cannot paginate to the missing events. They are silently
   unreachable, not merely deferred.

Production-reachable cases on a real strfry DB:

- **Residual-only predicates:** `kinds=[1]` + `#p` tag filter routes to `Event__kind` (router.rs:99);
  the tag is checked only post-hydration (engine.rs:275-285). If fewer than `limit` of the newest
  `2*limit+256` kind-1 events carry the tag but plenty of older ones do, the query under-returns and
  dead-ends. Same for `authors` + `tags`, `ids` + residuals, etc.
- **NIP-40 expiry runs:** a stretch of >`limit+256` expired events at the newest end of the partition
  (routine for relays carrying ephemeral/expiring content) empties or truncates the page with no
  cursor, while valid older events exist.
- **Cursor-resume on dense seconds:** with `Event__created_at` default-feed paging, every event in
  the cursor's second is a dup of one key; all dups with `lev_id >= cursor.lev_id` are emitted by the
  merge and then discarded by cursor exclusion (engine.rs:203-212), consuming headroom before any new
  event is seen.

The fixture (11 events, no expirations) cannot exercise any of these; all existing tests fit in one
call.

Note the flip side: the single bounded call is what currently keeps prior WR-03 (unbounded scan
query-of-death) closed. The fix must preserve a budget, not just restore an unbounded loop.

**Fix:** Restore the round-loop with an explicit budget and a partial-result cursor:

```rust
let mut boundary: Option<(u64, LevId)> = cursor_boundary; // exclusion boundary, advances per round
let mut rounds = 0;
loop {
    let merge_batch = merge_windowed(env, short_name, &start_keys_for(boundary), batch_size, emit_limit, since)?;
    let exhausted = merge_batch.len() < emit_limit;
    let last = merge_batch.last().map(|(ts, lev, _)| (*ts, *lev));
    // ... existing filter/hydrate/push loop, using `boundary` for exclusion ...
    rounds += 1;
    if valid.len() >= limit || exhausted || rounds >= MAX_ROUNDS { break; }
    boundary = last; // resume strictly below the last merged entry
}
// Build next_cursor from valid.last() whenever the merge was NOT exhausted,
// even if valid.len() < limit (budget hit) — never strand reachable events.
```

Two essential properties: (a) loop until `limit` or true merge exhaustion (`merge_batch.len() <
emit_limit` is the exhaustion signal), capped by a `MAX_ROUNDS`/scanned-entries budget for DoS;
(b) when the budget stops the loop early, still return a resume cursor so the remainder is reachable.
Update the engine.rs:6-7 and 172-190 comments to match the real control flow. Add a regression test:
small `batch_size`, a residual tag filter that only matches events deeper than `emit_limit`, assert
the events are still returned (or reachable via the returned cursor).

## Warnings

### WR-01: `hydrate_lev_ids` hard-fails the entire query on a missing levId — guaranteed transient failures against a live strfry (carried from prior WR-01, unresolved)

**File:** `src/query/hydrate.rs:53-56`; blast radius `src/query/engine.rs:220`, `src/query/engine.rs:410`
**Issue:** Unchanged from the prior review. The index scan and payload lookup run in separate read
txns (by design, D-08). Against a live strfry, an event deleted between the two txns (replaceable
kind overwrite, NIP-09, NIP-40 pruning) makes `get_event_payload` return `LevIdNotFound`, which `?`
propagates and fails the whole query — including all buckets of `latest_per_author`. The doc
comment's premise ("a levId present in a real index scan must exist in EventPayload") holds only
within a single txn, which this code deliberately does not use.
**Fix:** Match on the error: treat `LevIdNotFound` specifically like the decode-failure path
(`tracing::warn!`, `*skip_count += 1`, omit the slot); propagate all other LMDB errors. The
lev_id-join architecture already makes absent slots safe for every caller.

### WR-02: Unvalidated `filter.limit` flows into `Vec::with_capacity` — capacity-overflow panic / multi-GB allocation, and an effectively unbounded merge (carried from prior WR-02, unresolved)

**File:** `src/query/engine.rs:131-135, 169, 191`
**Issue:** `filter.limit` is caller-supplied `usize` with no engine-side ceiling.
`Vec::with_capacity(limit)` (engine.rs:169) panics with "capacity overflow" at `usize::MAX` and
pre-allocates `limit * sizeof((u64, u64, DecodedEvent))` for any large value. `emit_limit` saturates
to `usize::MAX` (engine.rs:191), so `merge_windowed` will walk and the engine will hydrate the
entire index partition. `merge_windowed` itself caps its own allocation (`min(256)`, merge.rs:270)
— the engine is the remaining hole. "Phase 4 enforces the hard ceiling" (filter.rs:60-61) still
refers to a phase that does not exist; the engine is the trust boundary for T-03-DOS.
**Fix:** `const MAX_LIMIT: usize = 1_000;` (or similar) and clamp:
`let limit = if filter.limit == 0 { DEFAULT_WINDOW_SIZE } else { filter.limit.min(MAX_LIMIT) };`
— at minimum `Vec::with_capacity(limit.min(DEFAULT_WINDOW_SIZE))`.

### WR-03: Forged or stale cursor widens the scan ABOVE `filter.until` — events newer than `until` are returned (no post-hydration until check)

**File:** `src/query/engine.rs:148-161` (cursor overrides `until`), `src/query/engine.rs:245-285` (residuals check kinds/authors/ids/tags but never `until`)
**Issue:** When a cursor is present, `effective_filter.until = Some(c.created_at)` unconditionally
**replaces** `filter.until`. `PageCursor` is untrusted caller input (T-03-CUR validates only base64
format and length, filter.rs:125-139) — a forged cursor with `created_at = u64::MAX` makes the scan
start from the top of the index, and since `until` is enforced only via the start key (D-03
pushdown), every event newer than `filter.until` passes the cursor-exclusion and residual checks and
is returned. This directly contradicts the stated invariant *"cursor … out-of-range values yield
empty/older pages"* (engine.rs:19-20, 98). Legitimately reachable too: a client replaying a cursor
against a tightened `until`.
**Fix:** Clamp instead of replace:
```rust
until: Some(match filter.until {
    Some(u) => u.min(c.created_at),
    None => c.created_at,
}),
```
Optionally add a belt-and-braces `ts <= filter.until` residual next to the existing since/kind
checks.

### WR-04: `reverse_upper_bound` panics on a `start_key` shorter than 8 bytes — public Reverse scan API aborts instead of erroring

**File:** `src/lmdb/scan.rs:359-381` (`debug_assert!` at 360, slice at 368, prefix slice at 373)
**Issue:** `start_key[len - 8..len]` with `len < 8`: in debug builds the `debug_assert!` fires; in
release builds `len - 8` wraps (usize overflow) and the slice index panics ("range start index out of
range"). Introduced by the 03-08 fix — before it, the Reverse arms used `Bound::Included(start_key)`
which tolerated any byte string. Reachable from every public Reverse entry point
(`scan_index_bounded`, `scan_index_windowed`, `scan_index_one_window`) with a key under 8 bytes, e.g.
`scan_index_bounded(env, "Event__kind", Reverse, &[], 5)`. Current engine/router callers always build
keys ≥ 8 bytes, but these are `pub` primitives and Phase 4+ callers get a process abort instead of an
`Err`.
**Fix:** Fail soft at the top of the helper:
```rust
if start_key.len() < 8 {
    return (start_key.to_vec(), false); // Included fallback — old, non-panicking behavior
}
```
(keep the `debug_assert!` for development visibility), or return `IndexError::MalformedKey`.

### WR-05: Residual id/author checks are case-sensitive while hex decoding is case-insensitive — uppercase-hex filters scan correctly, then drop every match

**File:** `src/query/engine.rs:250-259` (residuals), `src/query/router.rs:51-58` (`nibble` accepts `A-F`), `src/query/engine.rs:434-447` (`decode_hex_32` doc: "accepts both upper- and lower-case")
**Issue:** `decode_hex` deliberately accepts uppercase hex, so `authors=["ABCD…"]` or
`ids=["ABCD…"]` produce correct start keys and the scan finds the right events. The post-hydration
residual then compares the raw filter string against the event's lowercase-hex `pubkey`/`id`
(`a == &decoded.event.pubkey`, `id == &decoded.event.id`) — case-sensitive — so **every** match is
dropped and the query silently returns empty. One layer of the same pipeline accepts an input form
that another layer rejects; the "belt-and-braces" residual converts valid results into none. (Tag
values are exempt: router.rs:257-264 already treats uppercase as raw bytes, consistently with the
residual's exact string compare.)
**Fix:** Normalize once at the boundary — lowercase `ids`/`authors` (or reject non-lowercase per
strict NIP-01) before routing, and compare normalized values in the residual:
`authors.iter().any(|a| a.eq_ignore_ascii_case(&decoded.event.pubkey))` is the minimal fix.

### WR-06: `latest_per_author` filters NIP-40 expiry AFTER capping the scan at `per_author` — buckets under-fill or vanish when the newest events are expired

**File:** `src/query/engine.rs:376-383` (scan capped at `per_author`), `src/query/engine.rs:415-419` (expiry filter applied afterwards)
**Issue:** The reverse scan returns at most `per_author` raw entries; `is_expired` events are then
removed from that fixed set. If any of the author's `per_author` newest events are expired, the
bucket returns fewer than `per_author` valid events — or is omitted entirely — even though older
non-expired events of the same kind exist. NIP-40 semantics are that expired events are invisible, so
callers expect `per_author` *valid* events when they exist. On a relay carrying expiring content this
under-fills buckets routinely. (The same applies to the cross-kind contamination filter at
engine.rs:388-401: filtered-out smaller-kind entries also consume the `per_author` raw budget,
though that only triggers after the target kind is exhausted, where it is harmless.)
**Fix:** Over-fetch with headroom and truncate after filtering:
scan with `per_author + EXPIRY_HEADROOM` (or loop with `scan_index_one_window` until `per_author`
survivors or prefix exhaustion), apply the (pubkey, kind) and `is_expired` filters, then
`events.truncate(per_author)`.

## Info

### IN-01: `MergeCandidate` `Ord` inconsistent with derived `Eq` (carried from prior IN-04, unresolved)

**File:** `src/query/merge.rs:42-62`
**Issue:** `#[derive(Eq, PartialEq)]` compares all four fields (including `key_bytes`, `stream_idx`)
while `Ord::cmp` compares only `(created_at, lev_id)`. Two candidates can be `cmp == Equal` yet
`!=`, violating the documented `Ord`/`Eq` consistency contract. Harmless for `BinaryHeap` today; a
latent trap for ordered collections.
**Fix:** Implement `PartialEq`/`Eq` manually over `(created_at, lev_id)` (valid equivalence —
`lev_id` is unique).

### IN-02: `collect_window` with `window_size = 0` returns an empty batch on a non-empty index — caller misreads it as stream exhaustion (carried from prior IN-05, unresolved)

**File:** `src/lmdb/scan.rs:527-529, 575-577`; callers `src/lmdb/scan.rs:182-241, 276-323`, `src/query/merge.rs:116-123`
**Issue:** With `window_size = 0` the drain condition fires on the first item (`results.last()` is
`None`), so the batch is empty and `refill_stream`/`scan_index_windowed` mark the stream exhausted —
a silently-empty scan. Now also reachable through `merge_windowed(.., batch_size = 0, ..)`, which is
`pub`. Production paths always pass `DEFAULT_WINDOW_SIZE`, but the footgun widened with the new API.
**Fix:** `let window_size = window_size.max(1);` at the top of `collect_window` (or guard in
`merge_windowed`).

### IN-03: `refill_stream` does not mark a stream exhausted when the prefix guard truncates a non-empty batch — one wasted full window scan per stream end

**File:** `src/query/merge.rs:136-152`
**Issue:** When `take_while(starts_with(prefix))` truncates mid-batch, the stream has provably left
its prefix region (descending scan, contiguous prefix), but `exhausted` is only set when `guarded`
is empty. `resume_key` is set to the last key of the *raw* batch (outside the prefix), so the next
`refill_stream` issues a full `batch_size` LMDB window below the prefix only to have the guard
discard everything and exhaust then. Correct, but every multi-prefix query pays one dead window per
stream.
**Fix:** Capture `let raw_len = batch.len();` before the guard and set
`if guarded.len() < raw_len { stream.exhausted = true; }`.

### IN-04: `ts == u64::MAX` Included fallback re-opens the boundary-dup drop for a real key at `created_at == u64::MAX`

**File:** `src/lmdb/scan.rs:377-380, 551-557`
**Issue:** When the trailing timestamp is `u64::MAX`, `reverse_upper_bound` cannot build `ts+1` and
falls back to `Bound::Included(start_key)`. If an event actually exists with
`created_at == u64::MAX` (strfry accepts arbitrary u64 `created_at` unless
`rejectEventsNewerThanSeconds` is enforced; `until` defaults to `u64::MAX` for every reverse scan),
the Included path reproduces the original prior-CR-01 behavior for exactly that key: only the
smallest dup is yielded and the rest of the group is skipped. Narrow but real on a relay that
ingests hostile timestamps.
**Fix:** Special-case it: for `ts == u64::MAX` use `(Bound::Unbounded)` as the rev_range end within
the prefix — i.e., bound by `Excluded(prefix_successor)` (increment the last prefix byte with carry)
or `Bound::Unbounded` when the prefix is empty — so the scan starts above ANY key with this prefix.

---

_Reviewed: 2026-06-12T12:58:49Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
