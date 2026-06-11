---
phase: 03-query-engine
reviewed: 2026-06-11T16:49:16Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - spam/Cargo.toml
  - spam/src/lib.rs
  - spam/src/query/engine.rs
  - spam/src/query/filter.rs
  - spam/src/query/hydrate.rs
  - spam/src/query/merge.rs
  - spam/src/query/mod.rs
  - spam/src/query/router.rs
findings:
  critical: 7
  warning: 5
  info: 6
  total: 18
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-11T16:49:16Z
**Depth:** standard
**Files Reviewed:** 8
**Status:** issues_found

## Summary

The Phase-3 query engine (filter contracts, index routing, k-way merge, hydration, public API) was reviewed at standard depth, including tracing into the Phase-2 primitives it builds on (`scan.rs`, `payload.rs`) and the CR-01 DUPSORT proof tests. The read-only invariant (T-03-RDONLY) holds, the cursor decode path fails closed (T-03-CUR), and hex parsing never panics (T-03-HEX). All 26 unit tests pass.

However, `execute_query` returns **silently wrong results** in several real-world scenarios. Three of the critical findings were **empirically reproduced against the committed fixture** with a standalone harness (no repo modification):

```text
kinds=[2] limit=10        -> 10 events, kinds: [2, 2, 1, 1, 1, 1, 1, 1, 1, 2]   (7 wrong-kind events + 1 duplicate)
kinds=[1] since=1715000000 -> 7 events, ts: [1720000000, ..., 1700000000]        (5 events older than `since`)
authors=[pk1, pk1] limit=6 -> 6 events, ids: [e7c4c1b0, e7c4c1b0, 2c4ca2cb, 2c4ca2cb, f7ae1f95, f7ae1f95]  (every event doubled)
```

The unit tests mask these defects because they query the lexicographically-lowest prefix in the fixture (`kinds=[1]`, where kind=1 is the smallest kind, so the reverse scan has nothing below it to contaminate). Given this component's stated crux — *"serve correct, rich queries"* — silently-wrong query results are ship-blocking.

## Critical Issues

### CR-01: Reverse scans walk past the filter prefix — wrong-kind/wrong-author/wrong-id events returned, plus duplicate emission

**File:** `spam/src/query/engine.rs:203-292` (root cause spans `engine.rs`, `merge.rs:103-157`, and the `scan_index_bounded` contract in `src/lmdb/scan.rs:288-306`)
**Issue:** `scan_index_bounded(Reverse, start_key, limit)` iterates `rev_range(Unbounded, Included(start_key))` — it walks down the **entire index** from the start key with no prefix bound. `build_start_keys` constructs `prefix ‖ ts` start keys, so once a prefix's entries are exhausted the scan continues into lexicographically smaller prefixes: smaller kinds on `Event__kind`, smaller pubkeys on `Event__pubkey`/`Event__pubkeyKind`, smaller event ids on `Event__id`. `execute_query` applies **no residual predicate** on kind/author/id (only `is_expired` and the tag residual), so those foreign events are hydrated and returned as matches. Empirically proven: `kinds=[2]` returns 7 kind-1 events.

Two secondary corruptions follow from the same root cause:
1. **Merge order violation:** `merge_prefixes` assumes each per-prefix stream is `(created_at, lev_id)` DESC sorted, which is only true *within* a prefix. After a stream crosses a prefix boundary, `created_at` jumps arbitrarily, breaking the heap invariant and the D-10 output order.
2. **Duplicate emission:** the windowing restart key `prefix ‖ wb_ts` re-walks contaminated regions; the window-exclusion filter (`ts == wb_ts && lev_id >= wb_lev`) re-admits an already-emitted event from a *different* prefix at the same ts with a lower levId. Proven: the trailing `2` in the `kinds=[2]` repro is levId=1's event emitted twice (positions 2 and 10).

Note that `latest_per_author` (engine.rs:404-420) gets this right — it filters scan results to the exact `pubkey ‖ kind` key prefix. `execute_query` has no equivalent.
**Fix:** Bound every per-prefix stream to its prefix before merging. Cheapest correct form — drop entries whose key bytes do not start with the prefix, inside `merge_prefixes` (key bytes are already available; no hydration needed):
```rust
// In merge_prefixes: pair each start_key with its prefix (start_key minus trailing 8 ts bytes)
let prefix = &start_key[..start_key.len() - 8];
let batch = scan_index_bounded(env, short_name, ScanDirection::Reverse, start_key, scan_limit)?
    .into_iter()
    .take_while(|(key, _)| key.starts_with(prefix))
    .collect::<Vec<_>>();
```
`take_while` (not `filter`) is correct because entries below the prefix are contiguous — it also terminates the stream instead of scanning garbage. For `Event__created_at` (no prefix) the prefix is empty and `starts_with` is vacuously true. A belt-and-braces residual check on the hydrated event (`kinds.contains(&ev.kind)` etc.) in `execute_query` is also advisable.

### CR-02: `filter.since` is never enforced — results include events older than `since`

**File:** `spam/src/query/engine.rs:127-313` (contract defined at `spam/src/query/filter.rs:51-53`, `spam/src/query/router.rs:111-137`)
**Issue:** `filter.rs` documents `since` as "pushed into scan bounds (D-03) — not a residual predicate," but `build_start_keys` only uses `since` for `ScanDirection::Forward`, and `execute_query` always scans `Reverse` (using `until` as the bound). Nothing in the merge loop or post-hydration filtering checks `since`, so the reverse scan walks arbitrarily far past it. Empirically proven: `kinds=[1], since=1715000000` returned all 7 kind-1 events, 5 of them older than `since`. Beyond wrong results, this also wastes scan/hydration work walking history the caller excluded.
**Fix:** In `execute_query_internal`, treat `since` as a stop bound on the merged stream:
```rust
let since = filter.since.unwrap_or(0);
// in the filtered_batch construction:
if ts < since { return None; }
// and after the batch: if the batch's oldest ts < since, break the loop —
// everything further is older.
if window_boundary.map_or(false, |(wb_ts, _)| wb_ts < since) { /* drain current batch then */ break; }
```

### CR-03: DUPSORT batch-boundary windowing in `execute_query` silently drops events — reintroduces the proven CR-01 (Phase 2) data-loss bug

**File:** `spam/src/query/engine.rs:178-200` (windowing restart), contradicted by `spam/src/lmdb/scan.rs:157-180` and proven by `spam/tests/dupsort_resume_test.rs:331-425`
**Issue:** The engine's windowing comment claims: *"Set the next start key's ts to prev_window_boundary.ts (NOT ts-1) so the scan restarts at ts=T and sees any remaining dups."* This is **directly contradicted** by the empirical proof in `tests/dupsort_resume_test.rs` (`test_old_code_reverse_drops_levid_nonvacuity`): heed 0.22.1's `rev_range` with `Bound::Included(boundary_key)` positions at the **smallest** dup of that key, yields only it, and steps to the previous key — dups between the smallest and the last-emitted are **never yielded**, so no exclusion filter can recover them. `scan_index_windowed` was specifically rewritten with key-granular windowing (drain the dup group, resume `Excluded`) to fix exactly this; `execute_query_internal` reimplements its own windowing loop using `scan_index_bounded` with the broken `Included`-restart pattern.

Concrete loss: dup group `{5,6,7}` at `ts=T` (3 events, same prefix, same created_at), `batch_size` boundary lands after emitting levId 7. Restart at `prefix‖T` yields only levId 5; **levId 6 is silently dropped** from the query results. The fixture's dup groups are all size ≤ 2, so the `batch_size=2` over-fetch test (`test_execute_query_overfetch_backfill`) cannot catch this. Real strfry data routinely has ≥3 events with identical `(kind, created_at)`.
**Fix:** Do not reimplement windowing on top of `Included` restarts. Either (a) extend `merge_prefixes`/`scan_index_bounded` to accept an exclusive `(key, lev_id)` resume boundary using the key-granular drain technique already proven in `collect_window` (drain the boundary dup group so each batch ends on a key boundary, then resume `Bound::Excluded`), or (b) have `merge_prefixes` use `scan_index_windowed`-style collection internally. Add a regression test mirroring `test_reverse_window_straddle_non_first_group_no_drop` but driving `execute_query_with_batch`.

### CR-04: Stuck-window advance corrupts the exclusion boundary — events at `ts-1` with high levIds are wrongly excluded

**File:** `spam/src/query/engine.rs:243-251`
**Issue:** When a batch is fully excluded and the boundary hasn't advanced, the code does:
```rust
window_boundary = window_boundary.map(|(ts, lev)| (ts.saturating_sub(1), lev));
```
This moves the boundary ts to `ts-1` but **keeps the old levId**. On the next iteration the window-exclusion filter drops any event with `ts == wb_ts && lev_id >= wb_lev` — i.e. every event at `ts-1` whose levId is ≥ the stale levId is silently discarded even though it was never emitted. levIds are insertion-order counters with no correlation to `created_at`, so an event at `ts-1` with a higher levId (late-arriving backdated event) is entirely normal. Reachable via the cursor path: a cursor pointing into a dup group causes the first batch to be fully cursor-excluded, then the second identical batch triggers this stuck-advance.
**Fix:** When advancing the ts, reset the levId component so nothing at the new ts is pre-excluded:
```rust
window_boundary = window_boundary.map(|(ts, _)| (ts.saturating_sub(1), u64::MAX));
```
(With `u64::MAX`, `lev_id >= wb_lev` matches nothing real at the new boundary ts.) This whole branch disappears if CR-03's fix (exclusive resume boundaries) is adopted.

### CR-05: Hydration-skip misaligns the `zip` — events get the wrong `(created_at, lev_id)` keys and the page cursor is corrupted

**File:** `spam/src/query/engine.rs:259-292` (enabled by the API shape of `spam/src/query/hydrate.rs:32-58`)
**Issue:** `hydrate_lev_ids` skips undecodable payloads (by design, D-11), so its output can be **shorter** than the input `lev_ids`. The engine then zips positionally:
```rust
for ((ts, lev_id), decoded) in filtered_batch.iter().zip(hydrated.into_iter()) {
```
After the first skip, every subsequent event is paired with the **previous entry's** `(ts, lev_id)`. Consequences: (1) the `PageCursor` built from `valid.last()` carries the wrong `(created_at, lev_id)`, so the next page either re-emits or skips events; (2) the `(ts, lev_id)` ordering keys stored in `valid` are wrong; (3) the last `filtered_batch` entry is silently dropped (zip ends at the shorter iterator). The skip path is the *designed-for* corrupt-payload scenario — exactly when correctness should degrade gracefully, it instead corrupts pagination silently.
**Fix:** Make `hydrate_lev_ids` return the association explicitly so misalignment is impossible:
```rust
pub fn hydrate_lev_ids(...) -> Result<Vec<(LevId, DecodedEvent)>, QueryError>
```
and join on `lev_id` in the engine (or return `Vec<Option<DecodedEvent>>` slot-for-slot). `latest_per_author` should be updated to the same contract.

### CR-06: Multiple tag filters evaluated with OR semantics — NIP-01 requires AND across filter fields

**File:** `spam/src/query/engine.rs:270-286` (scan-side contributor: `spam/src/query/router.rs:221-248`)
**Issue:** `NostrFilter` is documented as "NIP-01 REQ filter — the query engine's input contract" (filter.rs:18). Under NIP-01, distinct filter fields (`#e`, `#p`, ...) are ANDed; only values *within* a field are ORed. The residual predicate sets `passes = true` as soon as **any one** `TagFilter` matches (`break 'outer` on first hit) — OR semantics. The router compounds this by building start keys for the union of all tag filters' values, scanning events that match any tag. A query for `{#e: [X], #p: [Y]}` therefore returns events that have X **or** are tagged to Y, instead of both. Silently wrong results for any multi-tag query.
**Fix:** Scan on one tag filter (ideally the most selective) and require every other filter to match as a residual:
```rust
let passes = tags_filter.iter().all(|tf| {
    decoded.event.tags.iter().any(|ev_tag| {
        ev_tag.len() >= 2 && ev_tag[0] == tf.name
            && tf.values.iter().any(|v| v == &ev_tag[1])
    })
});
```
In the router, build start keys from a single `TagFilter`'s values only (values within one filter are legitimately ORed/merged).

### CR-07: `Event__tag` start key hex-decodes ANY even-length hex-looking value — diverges from strfry's documented 64-char rule, silently missing results for literal tag values

**File:** `spam/src/query/router.rs:234-239`
**Issue:** The code's own comment (router.rs:225-229) states strfry decodes **64-char hex** tag values (32-byte ids) to raw bytes and stores everything else as raw UTF-8. The implementation instead hex-decodes *any* value `decode_hex` accepts — any even-length string of `[0-9a-fA-F]`. A literal topic tag like `#t = "beef"`, `"face"`, `"deed"`, or `"decade"` is hex-decoded (`"beef"` → `0xBE 0xEF`) while strfry indexed it as raw UTF-8 (`0x62 0x65 0x65 0x66`). The start key lands at the wrong position in the index; with limit-bounded scans the real entries are typically outside the scanned window, so matching events are **silently missing** from results. Uppercase 64-char hex is also decoded, which likely diverges from strfry's lowercase-hex id rule. This violates the project's hard constraint that key-byte semantics must be byte-identical to strfry's.
**Fix:** Restrict decoding to the documented rule and verify against strfry's actual `Event__tag` write path (pin the rule with a fixture golden vector containing a literal even-length-hex-looking tag value like `"beef"`):
```rust
let value_bytes: Vec<u8> = if value.len() == 64
    && value.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f'))
{
    decode_hex(value).expect("validated lowercase hex")
} else {
    value.as_bytes().to_vec()
};
```

## Warnings

### WR-01: Duplicate filter values produce duplicate result events — no start-key or levId dedup anywhere

**File:** `spam/src/query/router.rs:128-261`, `spam/src/query/merge.rs:103-157`, `spam/src/query/engine.rs:127-313`
**Issue:** `build_start_keys` maps filter values 1:1 to start keys with no dedup; `merge_prefixes` merges identical streams candidate-by-candidate; the engine never dedups levIds. Caller-supplied duplicates (`authors: [pk1, pk1]`, repeated ids, overlapping tag values across filters) duplicate every result. Empirically proven: `authors=[pk1, pk1] limit=6` returned 3 events, each twice — also halving the effective page size. Filter values come from untrusted GraphQL callers in Phase 4.
**Fix:** Dedup start keys in `build_start_keys` (e.g. sort + `Vec::dedup` before returning), and/or dedup emitted levIds in `merge_prefixes` as defense in depth.

### WR-02: Cursor can widen the time window beyond `filter.until`

**File:** `spam/src/query/engine.rs:150-159`
**Issue:** When a cursor is present, `until` is unconditionally replaced with `cursor.created_at`. A forged or stale cursor (untrusted, T-03-CUR) with `created_at > filter.until` makes the scan start *above* the filter's upper bound; the exclusion filter only drops entries above the cursor, not above `filter.until`, so events newer than `until` are returned — violating the filter contract. The doc comment ("out-of-range values yield empty/older pages") is wrong for this case: out-of-range yields *newer* pages.
**Fix:** Clamp: `until: Some(filter.until.map_or(c.created_at, |u| u.min(c.created_at)))`.

### WR-03: `MergeCandidate` Ord/Eq inconsistency violates the `Ord` contract

**File:** `spam/src/query/merge.rs:44-70`
**Issue:** `#[derive(Eq, PartialEq)]` compares all four fields (including `key_bytes` and `stream_idx`), but the manual `Ord::cmp` compares only `(created_at, lev_id)`. Two candidates can be `cmp == Equal` yet `!=`, violating the documented consistency requirement of `Ord` (`a.cmp(b) == Equal` ⟺ `a == b`). With duplicate streams (WR-01) equal `(created_at, lev_id)` pairs across streams actually occur. `BinaryHeap` tolerates this today, but it is a latent logic hazard for any future use of these impls (sorting, dedup, maps).
**Fix:** Make `Eq` match the ordering key — implement `PartialEq` manually over `(created_at, lev_id)`, or include the remaining fields as deterministic tie-breakers in `cmp`.

### WR-04: Tag name silently truncated to first byte; empty tag name silently becomes `'_'`

**File:** `spam/src/query/router.rs:233`
**Issue:** `tag.name.as_bytes().first().copied().unwrap_or(b'_')` — a multi-character tag name (e.g. `"emoji"`) silently scans the `'e'` index prefix (strfry only indexes single-char tags, so the correct behavior is to reject or return empty *without scanning*); the residual then rejects everything, so the caller burns a full bounded scan + hydration to get a silently empty result. An empty tag name scans the literal `'_'` tag index — querying data the caller never asked about. Both should be loud, not silent.
**Fix:** Validate `tag.name.len() == 1` in `build_start_keys` (or earlier, in Phase-4 input validation); skip with `tracing::warn!` like the malformed-hex path, producing zero start keys for that filter.

### WR-05: `skip_count` is collected then discarded — QRY-04's skip-count contract never reaches callers

**File:** `spam/src/query/engine.rs:168,257` and `spam/src/query/engine.rs:428-429`
**Issue:** `hydrate_lev_ids` takes `&mut usize skip_count` specifically so callers can surface corrupt-payload skips (QRY-04 "skip-warn-count"). Both `execute_query` and `latest_per_author` accumulate it into a local and drop it — the count is observable nowhere except per-event `tracing::warn!` lines. Phase-4 resolvers cannot report degraded results, and the count also masks CR-05 (a nonzero skip count is exactly when the zip misaligns).
**Fix:** Return the skip count (e.g. include it in the result tuple or a small `QueryStats` struct) so the GraphQL layer can expose it as an extension/diagnostic.

## Info

### IN-01: `limit == 0` contract contradiction between filter.rs and engine/merge

**File:** `spam/src/query/filter.rs:59-61` vs `spam/src/query/engine.rs:134-138`, `spam/src/query/merge.rs:113-115`
**Issue:** `filter.rs` documents `limit: 0` as "unbounded (D-04 engine side)"; the engine and merge clamp it to `DEFAULT_WINDOW_SIZE` (256). Phase-4 implementers reading the contract type will get 256, not unbounded.
**Fix:** Update the `NostrFilter::limit` doc to state `0 → DEFAULT_WINDOW_SIZE`.

### IN-02: Duplicated hex-decoding helpers in engine.rs and router.rs

**File:** `spam/src/query/engine.rs:450-471` vs `spam/src/query/router.rs:34-55,287-308`
**Issue:** `decode_hex_32`/`nibble` in engine.rs re-implement router.rs's `decode_hex`/`nibble`/`decode_pubkey_warn` with slightly different signatures (Option vs Result). Two code paths to keep byte-identical.
**Fix:** Make router's helpers `pub(crate)` and reuse them in engine.rs.

### IN-03: Vacuous test `assert!(true)` in merge.rs

**File:** `spam/src/query/merge.rs:329-339`
**Issue:** `test_no_payload_imports_compile_time_proof` asserts `true` and claims to be a "compile-time proof" that no payload import exists. It proves nothing and would keep passing if someone added a payload import. Clippy flags it (`assertions_on_constants`).
**Fix:** Delete the test and keep the D-06 note as a module comment, or replace with a real check (e.g. a CI grep).

### IN-04: Clippy warnings in the new query files — CI is documented to run `clippy -- -D warnings`

**File:** `spam/src/query/engine.rs:151-153,261`, `spam/src/query/router.rs:35,332`, all five query files (module docs)
**Issue:** New warnings introduced by this phase: `unnecessary_unwrap` (engine.rs:153 — `cursor.unwrap()` after `is_some()`), `useless_conversion` (engine.rs:261), unused `const PK2` in router tests (router.rs:332), `manual_is_multiple_of` (router.rs:35), and `empty_line_after_doc_comments` in all five files — the file-top `///` comments attach to the first `use` statement instead of the module (should be `//!`), so rustdoc module pages lose their documentation. CLAUDE.md specifies clippy runs with `-D warnings` in CI, where these fail the build.
**Fix:** Convert file-header `///` blocks to `//!`; apply the clippy suggestions; remove or use `PK2`.

### IN-05: `PageCursor::decode` decodes attacker-sized base64 before length check

**File:** `spam/src/query/filter.rs:124-138`
**Issue:** The base64 decode allocates and processes the full untrusted input before the 16-byte check. A valid 16-byte cursor is always exactly 24 base64 chars; a cheap pre-check bounds the work. Low risk (Phase-4 transport limits apply), but the fix is one line.
**Fix:** `if s.len() != 24 { return Err(QueryError::CursorDecode { reason: ... }); }` before decoding.

### IN-06: `SelectedIndex::Single("Event__id")` is misnamed — the ids arm produces one start key per id

**File:** `spam/src/query/router.rs:63-72,90-91,140-171`
**Issue:** `Single` is documented as "a single start_key spans the whole index," but the `Event__id` arm returns one key per id and requires the merge path exactly like `Multi`. Harmless today (the engine ignores the distinction), but the enum communicates a false invariant to future callers.
**Fix:** Route `ids` as `Multi("Event__id")`, or rename/redocument the variants to describe what they actually distinguish.

---

_Reviewed: 2026-06-11T16:49:16Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
