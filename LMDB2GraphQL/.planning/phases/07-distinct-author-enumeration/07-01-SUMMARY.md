---
phase: 07-distinct-author-enumeration
plan: "01"
subsystem: query-engine
status: complete
tags: [lmdb, seek-skip, enumeration, authors, pagination]
dependency_graph:
  requires:
    - src/lmdb/indexes.rs (open_index_string_uint64, IndexError)
    - src/query/filter.rs (QueryError)
    - src/lmdb/comparators.rs (StringUint64Cmp — comparator semantics)
  provides:
    - src/query/authors.rs (distinct_authors, increment_be, AuthorsPage)
  affects:
    - src/query/mod.rs (pub mod authors declaration added)
tech_stack:
  added: []
  patterns:
    - seek-skip enumeration over LMDB DUPSORT index
    - 40-byte composite seek key (pubkey||created_at) for StringUint64Cmp
    - big-endian integer increment for next-author lower bound
    - short per-call RoTxn (LMDB-09)
key_files:
  created:
    - src/query/authors.rs
  modified:
    - src/query/mod.rs
decisions:
  - "Seek keys must be 40 bytes (pubkey(32)||created_at(8)) not 32: StringUint64Cmp strips the last 8 bytes as the uint64 suffix before comparing the string prefix — a 32-byte key would have only 24-byte string portion, producing incorrect range positioning"
  - "AuthorsPage type alias introduced to satisfy clippy::type_complexity for the (Vec<[u8;32]>, Option<[u8;32]>) return type"
  - "increment_be encoded inline with no external hex crate: test helpers use decode_hex32/nibble functions (project has no hex dep)"
metrics:
  duration_minutes: 18
  completed_date: "2026-06-24"
  tasks_completed: 2
  files_changed: 2
---

# Phase 07 Plan 01: Distinct Author Enumeration Engine Summary

**One-liner:** Seek-skip `distinct_authors` over `Event__pubkey` in O(distinct authors) using 40-byte composite seek keys + `increment_be` big-endian successor.

## Tasks Completed

| # | Name | Status | Commit |
|---|------|--------|--------|
| 1 | increment_be big-endian successor helper + unit tests | DONE | fc07188 |
| 2 | distinct_authors seek-skip enumeration + fixture unit tests | DONE | fc07188 |

Both tasks were implemented together in `src/query/authors.rs` and committed atomically in `fc07188`.

## What Was Built

### `increment_be(pk: &[u8; 32]) -> Option<[u8; 32]>`

Big-endian +1 on a 32-byte value. Iterates from the last (least-significant) byte toward the first; adds 1 with carry propagation. Returns `None` when every byte carries (all-`0xFF` input — clean end-of-stream signal).

Used to compute the seek-skip lower bound: `increment_be(current_pubkey)` gives the smallest possible pubkey value that is strictly greater than `current_pubkey`, allowing a single `MDB_SET_RANGE` seek to jump past all of the current author's index entries.

### `distinct_authors(env, after, limit) -> Result<AuthorsPage, QueryError>`

Enumerates distinct pubkeys from `Event__pubkey` in byte-ascending order, O(distinct authors). Each call:

1. Opens one short `RoTxn` (dropped before return — T-07-TXN / LMDB-09).
2. Opens `Event__pubkey` via `open_index_string_uint64` (never `.create()`).
3. Loops up to `limit` times: builds a 40-byte seek key `[next_pk || 0x00*8]`, does `MDB_SET_RANGE`, extracts the 32-byte pubkey prefix from the first matching entry, pushes it to results, then advances the seek key via `increment_be`.
4. Returns `(pubkeys, next_cursor)` where `next_cursor = Some(last_pubkey)` iff a full page was returned.

### Key Implementation Decision: 40-Byte Seek Keys

The critical correctness insight: `StringUint64Cmp` strips the **last 8 bytes** from each key as the `uint64` suffix before comparing the remaining bytes as the "string" prefix. A 32-byte seek key would have only a 24-byte string prefix — incorrect for jumping past a 32-byte pubkey boundary. The seek key must always be the full 40 bytes: `pubkey(32) || created_at(8)`. With `created_at = 0x00*8`, `MDB_SET_RANGE` lands on the very first entry of the target pubkey (or the next pubkey if it has no entries).

### `pub mod authors` in `src/query/mod.rs`

Added in alphabetical order (before `engine`).

## Verification

- `cargo test --all-targets increment_be` — 4 tests pass (zero→one, carry propagation, all-FF→None, PK1 spot check)
- `cargo test --all-targets distinct_authors` — 5 tests pass:
  - `test_distinct_authors_all_limit_10` — returns `[PK1, PK2]`, cursor=None
  - `test_distinct_authors_limit_1_returns_pk1_with_cursor` — returns `[PK1]`, cursor=Some(PK1)
  - `test_distinct_authors_resume_after_pk1_yields_pk2` — after=PK1 → `[PK2]`, cursor=None
  - `test_distinct_authors_multi_page_walk` — limit=1 pages collect exactly `[PK1, PK2]`, no repeats
  - `test_distinct_authors_pk1_returned_exactly_once` — PK1 (9 index entries) returned exactly once
- `cargo test --all-targets` — 121 lib tests + 27 integration tests = 148 total, all pass (no regression in prior 125 tests)
- Read-only invariant: no `write_txn` or `.create(` in `src/query/authors.rs`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] 32-byte seek key → 40-byte seek key (comparator semantics)**
- **Found during:** Task 2 implementation (5 distinct_authors tests all failed: returned 10 results instead of 2)
- **Issue:** The plan described `increment_be(pk)` returning a 32-byte lower bound for `db.range()`. However, `StringUint64Cmp` treats the LAST 8 bytes of any key as the `uint64` suffix and compares only the remaining prefix as the "string" part. A 32-byte seek key produces only a 24-byte string prefix — incorrect for jumping past a 32-byte pubkey. The range iterator walked individual DUPSORT entries instead of jumping to the next distinct pubkey.
- **Fix:** Build 40-byte seek keys: `[increment_be(pk) || 0x00*8]`. The all-zero `created_at` suffix ensures `MDB_SET_RANGE` lands on the first entry of the next pubkey.
- **Files modified:** `src/query/authors.rs`
- **Commit:** fc07188 (implemented correctly from the start after root-cause analysis)

**2. [Rule 2 - Missing critical functionality] `AuthorsPage` type alias for clippy compliance**
- **Found during:** Running `cargo clippy --all-targets -- -D warnings`
- **Issue:** `clippy::type_complexity` triggered on `Result<(Vec<[u8;32]>, Option<[u8;32]>), QueryError>` return type.
- **Fix:** Added `pub type AuthorsPage = (Vec<[u8; 32]>, Option<[u8; 32]>)` type alias.
- **Files modified:** `src/query/authors.rs`
- **Commit:** fc07188

**3. [Rule 2 - Missing] Inline hex decode (no `hex` crate)**
- **Found during:** Task 2 test implementation — `hex::decode()` referenced in test helpers but the `hex` crate is not in `Cargo.toml`.
- **Fix:** Implemented `decode_hex32` + `nibble` helper functions inline in the test module (not a new dependency — avoids package-legitimacy checkpoint).
- **Files modified:** `src/query/authors.rs`
- **Commit:** fc07188

### Pre-existing Clippy Issues (Out of Scope)

`cargo clippy -- -D warnings` surfaces 26 errors in pre-existing code (`comparators.rs`, `engine.rs`, `merge.rs`, `resolvers.rs`, `router.rs`). These are pre-existing; they were present before this plan. Logged to deferred-items per deviation rule scope boundary.

## Known Stubs

None — `distinct_authors` is a complete, tested implementation wired to the live index.

## Threat Flags

No new network endpoints, auth paths, or trust boundaries introduced. The `distinct_authors` function is a read-only LMDB query with no external surface. Threat model from PLAN.md is fully mitigated:
- T-07-RDONLY: no `write_txn`, no `.create()` — verified by grep gate (0 matches)
- T-07-TXN: single short RoTxn dropped at end of call
- T-07-DOS: O(distinct authors) — each call does at most `limit` seeks
- T-07-SHORTKEY: `key.len() < 32` guard with `tracing::warn!` + break

## Self-Check: PASSED

| Check | Result |
|-------|--------|
| `src/query/authors.rs` exists | FOUND |
| `src/query/mod.rs` exists | FOUND |
| Commit `fc07188` exists | FOUND |
| `cargo test --all-targets` passes | 148 tests, 0 failures |
| `grep write_txn\|\.create(` in authors.rs | 0 matches (read-only invariant) |
