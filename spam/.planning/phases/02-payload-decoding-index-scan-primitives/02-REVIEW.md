---
phase: 02-payload-decoding-index-scan-primitives
reviewed: 2026-06-11T00:00:00Z
depth: standard
files_reviewed: 7
files_reviewed_list:
  - src/lmdb/payload.rs
  - src/lmdb/scan.rs
  - src/lmdb/types.rs
  - src/lmdb/mod.rs
  - tests/payload_test.rs
  - tests/scan_test.rs
  - Cargo.toml
findings:
  critical: 1
  warning: 5
  info: 4
  total: 10
status: issues_found
---

# Phase 02: Code Review Report

**Reviewed:** 2026-06-11T00:00:00Z
**Depth:** standard
**Files Reviewed:** 7
**Status:** issues_found

## Summary

Reviewed the EventPayload decode path (`payload.rs`), the index scan primitives
(`scan.rs`), shared domain types (`types.rs`), and the two integration test files.
The code is well-documented and the read-only / short-txn / byte-copy-out invariants
are correctly honored across the board (`open` never `create`, `MDB_RDONLY`, `.to_vec()`
before `drop(rtxn)`, host-endianness enforced at compile time in `meta.rs`). Panic-freedom
on the public decode surface is solid (empty slice, unknown tag, bad JSON all return `Err`).

The principal defect is a **correctness bug in the reverse-direction windowed (`limit=0`)
scan resume logic**: when a DUPSORT duplicate-key group straddles a window boundary in
reverse, the resume skip predicate drops unemitted levIds. This path is reachable from the
public `scan_index_bounded(..., Reverse, ..., 0)` entry point but is **not exercised by any
test** — every reverse test uses the default window of 256, which collapses the fixture into
a single batch and never triggers resume. This is exactly the class of silently-wrong range
result the project's correctness constraint warns about.

Remaining findings are robustness/quality issues (an `unwrap()` reachable on adversarial
key lengths via FFI abort semantics, `forget`-the-resume-emits-duplicates edge in forward
windowing, and several documentation/cleanliness items).

## Critical Issues

### CR-01: Reverse windowed scan can silently drop levIds at a DUPSORT group boundary

**File:** `src/lmdb/scan.rs:384-409` (and the resume cursor update at `255-258`)

**Issue:**
In `collect_window`, the reverse branch records the resume cursor as the **last emitted**
`(key, lev_id)` of the batch and then resumes with:

```rust
if !first_batch && key == resume_key && lev_id >= resume_lev_id {
    continue;
}
```

The doc comment justifies the `>=` skip by asserting "we saw the LAST (highest) levId of the
group in the previous batch." That assumption only holds when the **entire** DUPSORT group
fits inside the previous window. `move_through_duplicate_values()` yields duplicate VALUEs
(levIds) within a single key in `MDB_INTEGERDUP` **ascending** order, even under `rev_range`
(only the *key* traversal is reversed, not the per-key dup-cursor order). So for a key with
levIds `{5, 6, 7}`:

- If `window_size` is hit after emitting `5` and `6`, the batch's last emitted pair is
  `(key, 6)`, so `resume_lev_id = 6`.
- Next batch resumes at `Bound::Included(key)` and skips every `lev_id >= 6`. That skips
  **levId 7, which was never emitted** — silent data loss.

The forward branch does not have this bug because it skips `lev_id <= resume_lev_id`, and the
forward dup order is ascending, so the boundary levId and everything before it (already
emitted) is correctly skipped while later levIds are retained.

The reverse skip direction is inverted relative to the actual dup-iteration order. To resume
a reverse scan past a mid-group boundary you must skip the **already-emitted** dups, which in
ascending dup order are the ones with `lev_id <= resume_lev_id` *only if the cursor walked the
group ascending* — but then the "last emitted" is the highest emitted, and the correct skip is
`lev_id <= resume_lev_id`, identical to forward. The current `>=` predicate is wrong in both
interpretations: it discards the unemitted tail of the group.

This is reachable from the public API: `scan_index_bounded(env, name, ScanDirection::Reverse,
start_key, 0)` delegates to `scan_index_windowed`. It only manifests when a duplicate-key
group spans a window boundary, which requires `window_size` small relative to a dup group —
hence the fixture (dup groups of size 2, default window 256) never trips it.

**Fix:**
1. Correct the reverse resume predicate to skip already-emitted dups, matching the actual
   ascending dup-iteration order:

```rust
// Reverse range reverses KEY order, but dups within a key are still ascending by INTEGERDUP.
// The previous batch emitted dups in ascending levId order; resume must skip those already seen.
if !first_batch && key == resume_key && lev_id <= resume_lev_id {
    continue;
}
```

   (Confirm the actual per-key dup ordering empirically before settling on the predicate —
   if heed yields dups descending under `rev_range`, then the resume cursor must capture the
   *minimum* emitted levId of the boundary group and skip `lev_id >= resume_min`. Either way
   the current "skip `>=` last-emitted" is incorrect.)

2. Add a regression test that exercises the reverse path with a window smaller than a dup
   group. Construct or extend a fixture so a dup group of size >= 3 (e.g. levIds `{5,6,7}`
   sharing one key) straddles the boundary, then assert
   `scan_index_windowed(env, "Event__kind", Reverse, high_key, 2)` returns the full reversed
   golden vector with no missing levIds. The existing reverse tests
   (`test_windowed_reverse_full_golden_vector_reversed`) use the default 256 window and cannot
   catch this.

## Warnings

### WR-01: `value[0..8].try_into().unwrap()` is reachable and panics on a malformed 8..N value boundary

**File:** `src/lmdb/scan.rs:307, 325, 375, 399`

**Issue:**
The length guard checks `value.len() < 8` and skips, so `value[0..8]` is in-bounds when the
`unwrap()` runs — the `unwrap()` itself cannot fail here. That part is fine. The real concern
is the **skip+warn+count policy (D-11) is not applied in `scan.rs`**: malformed VALUEs are
`tracing::warn!`-logged and silently `continue`d, but unlike `decode_payload_skip_on_error`
in `payload.rs`, there is no skip **counter** surfaced to the caller. A query that silently
drops half its index entries to short VALUEs returns wrong results with no signal beyond a log
line. The project's stated policy is "skip+warn+**count**."

**Fix:** Thread a `skip_count: &mut usize` (or return a `(results, skipped)` tuple / struct)
through `scan_index_bounded` / `scan_index_windowed` / `collect_bounded` / `collect_window`,
incrementing on each short-VALUE skip, consistent with `decode_payload_skip_on_error`. Phase 3
query observability needs this count.

### WR-02: Forward windowed scan re-emits the boundary levId when the same composite key recurs across non-adjacent positions

**File:** `src/lmdb/scan.rs:344-382`

**Issue:**
The forward resume skip (`key == resume_key && lev_id <= resume_lev_id`) assumes a composite
key is contiguous in the index. For `Event__*` indexes this is true (B-tree key order groups
identical keys), so in practice it holds. However, the skip only fires when `key == resume_key`
**byte-for-byte**. If `start_key` passed by a caller is a *prefix* shorter than the stored key
(e.g. the `Event__tag` low-bound uses a 10-byte all-zero key vs. variable-length stored keys),
`resume_key` after the first batch becomes a full stored key, and the comparison is correct —
but the very first window's `first_batch=true` path emits the entry at `Bound::Included(start_key)`
with no de-dup, which is intended. The latent risk: `resume_key` is compared with `==` (raw
byte equality) rather than via the registered comparator. Two keys the golpe comparator treats
as equal but that differ in byte representation would defeat the skip. For the current fixed-width
integer composite keys this cannot happen, but it is an unstated coupling to "comparator-equal
implies byte-equal."

**Fix:** Document the invariant explicitly ("resume de-dup relies on comparator-equal keys being
byte-identical, which holds for all six golpe Event__* key encodings"), or compare via the
comparator. At minimum add an assertion/comment so a future variable-encoding index does not
silently regress.

### WR-03: `decode_event_payload_with_cache_and_limit` empty-slice error reports a misleading tag value

**File:** `src/lmdb/payload.rs:257-260` and `400-405`

**Issue:**
When `raw` is empty, both decode entry points return `PayloadError::UnknownTypeTag { tag: 0 }`.
`tag: 0` is indistinguishable from a real `0x00` (raw-JSON) tag byte and is actively misleading
in logs/metrics — a `0x00` payload that is *present but otherwise malformed* would be reported
identically to an *empty value*. The skip+warn log line (`decode_payload_skip_on_error`) will
read `UnknownTypeTag 0x00`, which contradicts the fact that `0x00` is a *known* tag.

**Fix:** Add a dedicated `PayloadError::EmptyPayload` variant (or
`UnknownTypeTag { tag: Option<u8> }`) so an empty value is distinguishable from a real tag byte
in diagnostics. This matters for the "count + diagnose corrupt payloads" operational story.

### WR-04: `serde_json::from_slice` on untrusted payloads has no nesting-depth bound (stack-exhaustion DoS)

**File:** `src/lmdb/payload.rs:265, 297, 410`

**Issue:**
The module header and `types.rs` correctly mark decoded JSON as UNTRUSTED (D-12). The 0x01
path guards against a decompression bomb via `MAX_EVENT_DECOMPRESSED_SIZE`, but **after**
decompression (and on the 0x00 path) the bytes go straight into `serde_json::from_slice`.
`serde_json`'s recursive descent parser has a default recursion limit (128) that protects
against deeply-nested-array stack overflow, so this is bounded — but the `tags` field is typed
`Vec<Vec<String>>` and a pathological event with an enormous flat `tags` array or `content`
string within the 4 MiB ceiling will allocate proportionally. Within the 4 MiB ceiling this is
acceptable, but the 0x00 path has **no size ceiling at all** — `get_event_payload` returns the
full stored value regardless of size, and strfry's `maxEventSize` (cited as 65536) is assumed,
not enforced here.

**Fix:** Apply a sanity ceiling on the 0x00 raw-JSON length too (reject `raw.len() >
MAX_EVENT_DECOMPRESSED_SIZE` before parsing), so both paths share one untrusted-input size
bound rather than relying on strfry's ingest-time `maxEventSize` remaining in force for all
historical events.

### WR-05: `DictCache` lock-poisoning policy panics instead of failing closed

**File:** `src/lmdb/payload.rs:162, 190, 203, 212`

**Issue:**
Every `RwLock` access uses `.expect("DictCache RwLock poisoned")`. If any thread panics while
holding the write lock (e.g. an allocation failure inside `DecoderDictionary::copy` on the
slow path), the lock is poisoned and **every subsequent decode of any 0x01 payload panics the
calling thread**. In an axum/tokio resolver context a poisoned cache turns a one-off failure
into a process-wide decode outage. The project's stated philosophy is skip+warn (never panic)
on untrusted input.

**Fix:** Recover from poisoning instead of panicking — e.g.
`self.inner.read().unwrap_or_else(|e| e.into_inner())` — or map the poison error to a
`PayloadError` variant so the failure degrades to a per-request error rather than a panic.
Since the cache only ever holds immutable `Arc<DecoderDictionary>` values, reading through a
poisoned lock is safe.

## Info

### IN-01: `Cargo.toml` declares dependencies not used by any reviewed source

**File:** `Cargo.toml:23-29, 37-38`

**Issue:** `serde_yaml_ng`, `dirs`, and the `cc` build-dependency are not referenced by any of
the Phase 02 files under review (`payload.rs`, `scan.rs`, `types.rs`). `cc` is presumably used
by `build.rs` for the comparator FFI (out of scope here) and `serde_yaml_ng`/`dirs` are for
config loading in other modules, so these are likely legitimate project-wide deps rather than
dead weight. Flagging for confirmation that each still has a live consumer.

**Fix:** Run `cargo machete` / `cargo-udeps` to confirm no genuinely unused crate has accreted;
none of the reviewed files import these.

### IN-02: Spec-vs-code comment drift on the 0x01 dictId slice range

**File:** `src/lmdb/payload.rs:277-278`

**Issue:** The inline comment notes the spec text says `[1..4]` but the code reads `raw[1..5]`,
attributing the discrepancy to an "inclusive-to typo" in the spec. The code is correct
(4 bytes = `u32`), but the comment relitigates a spec ambiguity inline. This is fine to keep,
but the authoritative spec (§3.2) should be corrected at the source so future readers do not
re-derive this each time.

**Fix:** Update spec §3.2 to read `bytes[1..5]` (exclusive end) and trim the defensive comment.

### IN-03: Magic constant `8` for levId width repeated across scan/index helpers

**File:** `src/lmdb/scan.rs:300, 307, 318, 325, 364, 375, 392, 399`

**Issue:** The 8-byte levId VALUE width is hard-coded as the literal `8` and `value[0..8]` in
eight places across `scan.rs` (and again in `indexes.rs`). A future change to the levId width
(unlikely, but it is strfry-coupled) would require editing every site.

**Fix:** Introduce `const LEV_ID_BYTES: usize = std::mem::size_of::<LevId>();` and use it for
the guard (`value.len() < LEV_ID_BYTES`) and the slice, centralizing the contract.

### IN-04: `MAX_EVENT_DECOMPRESSED_SIZE` doc references `Decompressor::decompress` returning `Err`, but maps it to `ZstdError` while a dedicated `DecompressedTooLarge` variant exists unused

**File:** `src/lmdb/payload.rs:87-103, 292-294`

**Issue:** The enum defines `PayloadError::DecompressedTooLarge { limit }` specifically for the
decompression-bomb ceiling, but the over-ceiling case in
`decode_event_payload_with_cache_and_limit` maps the failure to `PayloadError::ZstdError(io)`
(line 294) instead. The dedicated variant is therefore never constructed, and an over-limit
decompression is reported as a generic zstd error rather than the precise
"exceeds limit of N bytes" message. The test (`test_decode_0x01_over_ceiling_returns_error`)
only asserts `is_err()`, so it does not catch the variant mismatch.

**Fix:** Either inspect the zstd error and translate the size-limit failure to
`DecompressedTooLarge { limit: max_decompressed }`, or remove the now-dead variant. Tighten the
over-ceiling test to assert the specific variant once decided.

---

_Reviewed: 2026-06-11T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
