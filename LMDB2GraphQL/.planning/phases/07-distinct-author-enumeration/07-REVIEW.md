---
phase: 07-distinct-author-enumeration
reviewed: 2026-06-24T00:00:00Z
depth: standard
files_reviewed: 5
files_reviewed_list:
  - src/query/authors.rs
  - src/query/mod.rs
  - src/graphql/types.rs
  - src/graphql/resolvers.rs
  - tests/authors_test.rs
findings:
  critical: 0
  warning: 2
  info: 5
  total: 7
status: issues_found
---

# Phase 7: Code Review Report

**Reviewed:** 2026-06-24
**Depth:** standard
**Files Reviewed:** 5
**Status:** issues_found

## Summary

Reviewed the distinct-author enumeration feature: the seek-skip engine
(`distinct_authors` + `increment_be`), the `AuthorsPage` GraphQL type, the
`authors` resolver (clamp / two-step cursor decode / `spawn_blocking`), and the
integration tests. I cross-referenced the `StringUint64Cmp` comparator
(`reference/golpe_comparators.cpp`), the index-open helper
(`src/lmdb/indexes.rs`), and `decode_hex` (`src/query/router.rs`) to verify the
load-bearing correctness and security claims rather than trusting the summaries.

**The core algorithm is correct and the security posture is sound.** I verified
the four highest-risk claims directly and found them all valid:

1. **40-byte seek key reasoning is correct.** The C++ comparator strips the
   *last* 8 bytes (`a2.mv_size -= 8`) and compares the remaining prefix with
   shorter-sorts-first-then-memcmp semantics. `Event__pubkey` keys are 40 bytes
   (pubkey 32 ‖ created_at 8 LE), so their string prefix is exactly 32 bytes. A
   40-byte seek key `increment_be(pk) ‖ 0x00*8` yields a 32-byte string prefix
   that compares apples-to-apples against stored keys. A 32-byte seek key would
   present a 24-byte string prefix and mis-position — the implementation gets
   this right (and a fixture test proves PK1's 9 entries collapse to one visit).
2. **`increment_be` carry/overflow is correct.** LSB-to-MSB carry propagation;
   all-`0xFF` → `None` (clean end-of-stream). Unit tests cover zero→one, carry,
   and overflow.
3. **Cursor decode is genuinely fail-closed with no byte echo.** Two gates
   (`decode_hex` rejects non-hex/odd-length; `try_into::<[u8;32]>` rejects any
   length ≠ 32). The `decode_hex` error (which carries a byte *position*, never
   the input bytes) is discarded via `map_err(|_| …)`; the returned message is
   generic. The `"79be66"` test proves the second gate fires.
4. **Read-only invariants hold.** Only `env.read_txn()`; sub-DB opened via
   `open_index_string_uint64` (which calls `.open()`, never `.create()`); the
   `RoTxn` is local to `distinct_authors` and dropped before return; `env` is
   cloned before the `spawn_blocking` closure (`RoTxn` is `!Send`). No
   unchecked slicing/indexing on attacker-influenced lengths — `key[0..32]` is
   guarded by an explicit `key.len() < 32` check, and `copy_from_slice` is
   length-safe.

No Critical findings. The two Warnings are robustness/consistency gaps, not
correctness defects. The Info items are documentation and minor-hygiene notes.

## Warnings

### WR-01: Short-key structural anomaly silently truncates the page with a clean `None` cursor

**File:** `src/query/authors.rs:166-174` (and interaction with `:195-199`)
**Issue:** When a `Event__pubkey` entry shorter than 32 bytes is encountered,
the loop logs a warning and `break`s. Whatever authors were already collected
are returned, and because `result.len() < limit` after an early break, the
`next_cursor` computation yields `None` — signalling "true end of stream" to the
caller. A structural anomaly mid-index is therefore indistinguishable, to the
GraphQL consumer, from a clean exhaustion of authors: the consumer stops
paginating and silently sees a *truncated* author set with `hasMore=false`.

This mirrors a defect class the team already chose to treat as an error
elsewhere: in `read_stats` (`resolvers.rs:444-466`), a non-8-byte last key is
deliberately surfaced as `MalformedKey` rather than masked as `max_lev_id = 0`,
with the explicit rationale "indistinguishable from an empty DB and masking
corruption." The same reasoning applies here — a malformed key should arguably
return an internal error (or at least propagate a signal) rather than masquerade
as end-of-stream.

The mitigating factor: strfry should never produce such a key, and a
`tracing::warn!` is emitted (so operators *can* detect it in logs). That is why
this is a Warning, not a Critical. But the silent-truncation-as-success behavior
is inconsistent with the WR-07 decision made on the adjacent stats path.

**Fix:** Either propagate the anomaly as an error (consistent with `read_stats`):
```rust
if key.len() < 32 {
    tracing::error!(key_len = key.len(),
        "distinct_authors: Event__pubkey key < 32 bytes — structural anomaly");
    return Err(QueryError::Lmdb(crate::lmdb::indexes::IndexError::MalformedKey {
        name: "Event__pubkey".to_string(),
        expected: 32,
        actual: key.len(),
    }));
}
```
or, if silent degradation is intentionally preferred, document explicitly in the
function's doc comment that a mid-stream short key truncates the result with
`hasMore=false` and is NOT distinguishable from genuine end-of-stream by the
caller — so the divergence from the WR-07 policy is a recorded decision.

### WR-02: Resolver doc comment claims `distinct_authors` interprets `limit=0` as `DEFAULT_WINDOW_SIZE` — it does not

**File:** `src/graphql/resolvers.rs:210-214`
**Issue:** The `authors` resolver comment states: *"NEVER pass 0 — engine treats
0 as 'use DEFAULT_WINDOW_SIZE', not 100."* This is copied from the `events()`
resolver, where it is true (`execute_query` → `merge.rs:411`:
`if limit == 0 { DEFAULT_WINDOW_SIZE }`). It is **false** for `distinct_authors`,
which has no such special-casing: `for _ in 0..0` simply returns an empty page.

This is a Warning rather than Info because the comment justifies a real
invariant (the `.max(1)` clamp) with an incorrect rationale. A future maintainer
who reads this comment, checks `distinct_authors`, finds no `DEFAULT_WINDOW_SIZE`
handling, and concludes "the comment is stale, the clamp is unnecessary" could
remove the `.max(1)` — at which point `limit=0` would still be harmless here
(empty page), but the divergence between the two resolvers' actual semantics is
now undocumented and the copy-pasted comment is actively misleading. The clamp
*is* still worth keeping for consistency and to reject negative `i32` limits, so
the fix is to correct the justification, not the code.

**Fix:** Replace the misleading clause:
```rust
// D-04/D-05: clamp at API layer. Default 100, ceiling 500. `.max(1)` also
// rejects negative i32 limits. NOTE: unlike events(), distinct_authors has no
// limit==0 special case — 0 would yield an empty page, not DEFAULT_WINDOW_SIZE.
let effective_limit = limit.map(|l| (l.max(1) as usize).min(500)).unwrap_or(100);
```

## Info

### IN-01: Full-page-on-exact-boundary returns a cursor that resolves to one wasted empty page

**File:** `src/query/authors.rs:195-199`
**Issue:** When the final author exactly fills the page (`result.len() == limit`
and the index is then exhausted), `next_cursor = Some(last)` and `hasMore=true`.
The consumer fetches one more page, which seeks past `last`, finds nothing, and
returns `([], None)`. This is correct (no missed/duplicated authors) and matches
the documented `events()` behavior, so it is intentional. Flagged only so it is
not mistaken for a bug during future review — the off-by-one "phantom page" is
the standard cursor-pagination trade-off, not a defect.
**Fix:** None required. Optionally note in the doc comment that a boundary-exact
final page costs one extra empty round-trip.

### IN-02: `iter` is fully recreated (re-seek from root) on every loop iteration

**File:** `src/query/authors.rs:152-189`
**Issue:** Each iteration constructs a fresh `db.range(&rtxn, &range)` and takes
only `iter.next()`. This is the intended seek-skip design (one `MDB_SET_RANGE`
per distinct author) and is O(distinct authors) as specified. Performance is
explicitly out of v1 review scope; noting only that the per-iteration
`RoRange` construction is deliberate, not accidental dead work.
**Fix:** None. Correct as designed.

### IN-03: `decode_hex` error carries a byte position; resolver correctly discards it

**File:** `src/graphql/resolvers.rs:232-235` (re: `src/query/router.rs` `decode_hex`)
**Issue:** `decode_hex` returns errors like `"byte 3: invalid hex nibble"`. This
includes an *index* but never the offending byte value, so even if it were
surfaced it would not echo attacker input. The resolver discards it via
`map_err(|_| …)` and returns a generic `"invalid cursor: malformed hex"`. The
no-echo invariant (T-07-CUR) holds. Recorded as a positive confirmation, not a
defect.
**Fix:** None.

### IN-04: `hex_encode_lowercase` is hard-coded to 32 bytes but typed as `&[u8; 32]` — fine, but duplicates an encoder concept

**File:** `src/graphql/resolvers.rs:492-499`
**Issue:** The inline hex encoder is correct (`{:02x}` always emits exactly two
lowercase digits; `String::with_capacity(64)` is right for 32 bytes; the
`.expect("infallible")` on `write!` to a `String` cannot fire). The codebase now
has a `decode_hex` (router.rs) and a separate `hex_encode_lowercase`
(resolvers.rs) living in different modules. Not a bug; a mild cohesion smell if a
third hex site appears. No new dependency was added, which matches project
policy.
**Fix:** None required. If a third hex consumer emerges, consider a small
`src/util/hex.rs` housing both directions.

### IN-05: Tests assert the fixture has exactly two distinct pubkeys but do not pin that assumption against the fixture's own metadata

**File:** `tests/authors_test.rs:90-105`, `src/query/authors.rs:343-355`
**Issue:** The tests hard-code "fixture has exactly 2 distinct pubkeys (PK1,
PK2)". This is fine and is the correct way to test the algorithm. The only risk
is that a future fixture regeneration that adds a third pubkey would break these
tests in a way whose cause (fixture changed) is not self-evident from the
assertion message. The CONTEXT file (line 79) explicitly asked to "confirm the
full set," which these tests do. Low-severity hygiene note only.
**Fix:** None required. Optionally cross-check against
`scan_lev_ids_for_index("Event__pubkey")` distinct-prefix count so a fixture
change produces a clearer diagnostic.

---

_Reviewed: 2026-06-24_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
