---
phase: 07-distinct-author-enumeration
verified: 2026-06-24T03:42:14Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
re_verification: false
---

# Phase 07: Distinct Author Enumeration Verification Report

**Phase Goal:** A consumer can paginate the complete set of distinct pubkeys that have authored at least one event, served directly from strfry's live `Event__pubkey` index in O(distinct authors) — without scanning every event and without a caller-supplied author list.

**Verified:** 2026-06-24T03:42:14Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A GraphQL `authors(after, limit)` query returns a page of distinct hex pubkeys present in the DB, with `hasMore` and an opaque `endCursor` | VERIFIED | `AuthorsPage` struct in `src/graphql/types.rs` (line 117); `async fn authors` resolver in `src/graphql/resolvers.rs` (line 200); `test_authors_full_page` passes returning `[PK1, PK2]`, hasMore=false, endCursor=null |
| 2 | Enumeration is O(distinct authors): each distinct pubkey costs one B-tree seek (seek-skip via `increment_be` of the 40-byte key prefix) | VERIFIED | `distinct_authors` in `src/query/authors.rs` loop structure: one `db.range(...).next()` per distinct pubkey, followed by `increment_be` to build next 40-byte seek key; `test_distinct_authors_pk1_returned_exactly_once` proves PK1 (9 index entries across 5 created_at keys) is returned exactly once — if adjacent-dedup were used, it would appear multiple times |
| 3 | Pagination is correct and exhaustive — successive pages using `endCursor` cover every distinct pubkey exactly once within a snapshot, terminating cleanly | VERIFIED | `test_authors_multi_page_walk` (both lib and integration): limit=1 pages, feeding endCursor back as `after`, collects exactly `[PK1, PK2]` with no repetitions; `test_distinct_authors_resume_after_pk1_yields_pk2` verifies the after-cursor exclude semantics (`increment_be` so cursor pubkey is not re-emitted); `after=all-0xFF` short-circuits to `(vec![], None)` |
| 4 | `limit` is clamped to the same hard ceiling as `events()`; a malformed `after` cursor fails closed with a client error that does not echo the offending bytes | VERIFIED | Limit clamp `(l.max(1) as usize).min(500).unwrap_or(100)` appears 5 times in resolvers.rs (includes events + authors + tests); Two-step cursor decode: `decode_hex` + `try_into::<[u8;32]>()` — the critical "79be66" test (3 bytes, valid even-length hex) passes through `decode_hex` but fails the `try_into` gate; `test_authors_bad_cursor_nonhex`, `test_authors_bad_cursor_short`, `test_authors_bad_cursor_even_but_not_32_bytes` all return non-empty errors with no echo of the offending input |
| 5 | Read-only invariants hold: short per-call read txns, no write txn, no `.create()`; the resolver runs via `spawn_blocking` like the existing resolvers | VERIFIED | `grep write_txn\|\.create(` on authors.rs returns 0 actual calls (only comments); `env.read_txn()` opened and `drop(rtxn)` explicit at end of `distinct_authors`; resolver clones `env` before closure and uses `tokio::task::spawn_blocking`; no write_txn or `.create(` in resolvers.rs beyond comments |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/authors.rs` | `distinct_authors` engine + `increment_be` helper + unit tests (min 80 lines) | VERIFIED | File exists; `pub fn distinct_authors` at line 111; `pub(crate) fn increment_be` at line 51; 9 test functions across `#[cfg(test)] mod tests`; 439 lines total |
| `src/query/mod.rs` | `pub mod authors` declaration | VERIFIED | `pub mod authors;` at line 4 (alphabetical order, before `engine`) |
| `src/graphql/types.rs` | `AuthorsPage` SimpleObject | VERIFIED | `#[derive(SimpleObject)] pub struct AuthorsPage` at line 116-127; fields `authors: Vec<String>`, `end_cursor: Option<String>`, `has_more: bool` |
| `src/graphql/resolvers.rs` | `authors` resolver on Query root | VERIFIED | `async fn authors` at line 200; imports `AuthorsPage` and `distinct_authors` at lines 29 and 32 |
| `tests/authors_test.rs` | Fixture integration tests (min 60 lines, 5+ test functions) | VERIFIED | 364 lines; 7 `#[tokio::test]` functions covering all 5 required behaviors |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `src/query/authors.rs` | `src/lmdb/indexes.rs` | `open_index_string_uint64(env, &rtxn, "Event__pubkey")` — never `.create()` | VERIFIED | Called at line 123; `open_index_string_uint64` imported via `use crate::lmdb::indexes::open_index_string_uint64` |
| `src/query/authors.rs` | `src/query/filter.rs` | Returns `Result<_, QueryError>` — reuses existing engine error type | VERIFIED | `use crate::query::filter::QueryError` at line 27; return type is `Result<AuthorsPage, QueryError>` |
| `src/graphql/resolvers.rs` | `src/query/authors.rs` | `spawn_blocking(move \|\| distinct_authors(&env, after.as_ref(), limit))` | VERIFIED | `distinct_authors` called at line 250 inside `tokio::task::spawn_blocking`; env cloned before closure at line 246 (Pitfall 1) |
| `src/graphql/resolvers.rs` | `src/graphql/types.rs` | Returns `AuthorsPage { authors, has_more, end_cursor }` | VERIFIED | `AuthorsPage` imported at line 29; constructed at lines 258-262 with `authors`, `has_more`, `end_cursor` fields |

---

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `src/graphql/resolvers.rs::authors` | `pubkeys: Vec<[u8;32]>` | `distinct_authors(&env, ...)` → `Event__pubkey` LMDB index via `open_index_string_uint64` → real B-tree range scan | Yes — `db.range(...).next()` against live LMDB index; fixture integration test `test_authors_full_page` returns `[PK1, PK2]` non-empty | FLOWING |

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `increment_be`: 4 behaviors (zero→one, carry, all-FF→None, PK1 spot) | `cargo test --lib increment_be` | 4 passed; 0 failed | PASS |
| `distinct_authors`: 5 fixture behaviors (full, limit=1 cursor, resume-after, multi-page walk, PK1-exact-once) | `cargo test --lib distinct_authors` | 5 passed; 0 failed | PASS |
| `authors` GraphQL query: 7 integration behaviors (full page, limit=1, multi-page walk, 3x bad cursor, no-mutation SDL) | `cargo test --test authors_test` | 7 passed; 0 failed | PASS |
| Full regression suite | `cargo test --all-targets` | 121 lib + 34 integration = 155 total, 0 failed | PASS |

---

### Probe Execution

Step 7c: SKIPPED — no `scripts/*/tests/probe-*.sh` declared or found for this phase.

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| QRY-06 | 07-01-PLAN.md | Enumerates distinct pubkeys from `Event__pubkey` in O(distinct authors) via seek-skip, short per-call read txns, no write txn | SATISFIED | `distinct_authors` in `src/query/authors.rs` implements seek-skip with 40-byte composite keys; 5 lib tests pass including exact-once proof for PK1 (9 index entries) |
| API-07 | 07-02-PLAN.md | Consumer can query `authors(after, limit)` → `AuthorsPage { authors: [String!]!, hasMore, endCursor }`, limit clamped, opaque after cursor decoded fail-closed | SATISFIED | `async fn authors` resolver in `src/graphql/resolvers.rs`; `AuthorsPage` in `src/graphql/types.rs`; 7 integration tests pass including bad-cursor no-echo and multi-page walk |

Both requirements are fully satisfied. No orphaned requirements — QRY-06 and API-07 are the only Phase 7 requirements in REQUIREMENTS.md traceability table.

---

### Anti-Patterns Found

No blockers or warnings. Scan of phase-modified files (`src/query/authors.rs`, `src/query/mod.rs`, `src/graphql/types.rs`, `src/graphql/resolvers.rs`, `tests/authors_test.rs`, `CLAUDE.md`):

- No `TBD`, `FIXME`, or `XXX` markers.
- No `TODO` or `HACK` markers.
- No `return null`, `return {}`, or `return []` stubs.
- No hardcoded empty props at call sites.
- No `write_txn` or `.create(` calls (only comments noting their absence).
- Pre-existing clippy issues noted in SUMMARY.md (26 errors in `comparators.rs`, `engine.rs`, `merge.rs`, `resolvers.rs`, `router.rs`) are out of scope for this phase; they predate Phase 7.

---

### Human Verification Required

None. All truths are mechanically verified by passing tests. No behavior-dependent truths remain unexercised.

---

### Gaps Summary

No gaps. All 5 success criteria verified against the actual codebase with passing tests.

---

## Additional Verifier Notes

**40-byte seek key correctness (critical implementation insight confirmed):** The PLAN described 32-byte seek keys but the implementation correctly uses 40-byte keys (`pubkey(32) || created_at(8 LE)`). This is required because `StringUint64Cmp` strips the last 8 bytes as the uint64 suffix — a 32-byte key would have only a 24-byte string prefix, causing incorrect range positioning. The SUMMARY documents this as a deviation-auto-fixed during implementation. The correctness is proven by `test_distinct_authors_pk1_returned_exactly_once` (which would return 9 results instead of 1 if seek keys were wrong).

**Two-step cursor decode confirmed:** The critical "79be66" test case (6 chars → 3 bytes — even length valid hex) appears 4 times in `tests/authors_test.rs` and the test passes, proving the `try_into::<[u8;32]>()` gate is wired and not dead code.

**CLAUDE.md correction verified:** Old row `"Opening any Event__* sub-DB | Custom golpe comparators not present..."` is absent (0 matches). New row references `open_index_*` helpers. Table header `| Avoid | Why | Use Instead |` is present (1 match).

**No new Cargo.toml dependencies:** Phase 7 reuses all existing crates (heed, async-graphql, tokio, tracing, serde_json) with no new additions.

---

_Verified: 2026-06-24T03:42:14Z_
_Verifier: Claude (gsd-verifier)_
