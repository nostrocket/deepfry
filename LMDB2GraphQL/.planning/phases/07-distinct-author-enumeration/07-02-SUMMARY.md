---
phase: 07-distinct-author-enumeration
plan: "02"
subsystem: graphql-api
status: complete
tags: [graphql, authors, pagination, cursor, types, resolver]
dependency_graph:
  requires:
    - src/query/authors.rs (distinct_authors, AuthorsPage — Plan 07-01)
    - src/graphql/types.rs (EventsPage shape to mirror)
    - src/graphql/resolvers.rs (events() clamp/spawn_blocking/map_query_error patterns)
    - src/query/router.rs (decode_hex pub(crate) hex decoder)
  provides:
    - src/graphql/types.rs (AuthorsPage SimpleObject)
    - src/graphql/resolvers.rs (authors resolver, hex_encode_lowercase)
    - tests/authors_test.rs (7 fixture integration tests)
  affects:
    - CLAUDE.md (stale Event__* What-NOT-to-Use row corrected)
tech_stack:
  added: []
  patterns:
    - SimpleObject mirroring EventsPage for AuthorsPage
    - Two-step cursor validation: decode_hex + try_into:[u8;32]
    - hex_encode_lowercase private fn (no hex crate dep)
    - spawn_blocking + env clone before closure (Pitfall 1)
    - map_query_error reuse (T-07-LEAK)
key_files:
  created:
    - tests/authors_test.rs
  modified:
    - src/graphql/types.rs
    - src/graphql/resolvers.rs
    - CLAUDE.md
decisions:
  - "Two-step cursor decode: decode_hex (odd-length/non-hex gate) then try_into:<[u8;32]> (exact 32-byte gate) — decode_hex alone accepts even-length sub-64-char strings, so try_into is mandatory for fail-closed T-07-CUR"
  - "hex_encode_lowercase added inline (no hex crate dep — consistent with the project's decode_hex inline policy from Plan 03)"
  - "decode_hex accessed via crate::query::router::decode_hex (pub(crate)) — no duplication, no new dep"
  - "CLAUDE.md corrected: blanket ban on Event__* opens replaced with accurate guidance (comparator-less opens are the footgun; opens via open_index_* helpers are supported and correct)"
metrics:
  duration_minutes: 5
  completed_date: "2026-06-24"
  tasks_completed: 3
  files_changed: 4
requirements: [API-07]
---

# Phase 07 Plan 02: Authors Resolver + CLAUDE.md Correction Summary

**One-liner:** GraphQL `authors(after, limit)` resolver over `distinct_authors` engine with two-step fail-closed cursor decode and 7 fixture integration tests.

## Tasks Completed

| # | Name | Status | Commit |
|---|------|--------|--------|
| 1 | AuthorsPage GraphQL type + authors resolver | DONE | 94e0442 |
| 2 | Fixture integration tests for the authors query | DONE | 0396680 |
| 3 | Correct stale CLAUDE.md Event__* entry | DONE | 6e1ad83 |

## What Was Built

### `AuthorsPage` in `src/graphql/types.rs`

Added alongside `EventsPage`:

```rust
#[derive(SimpleObject)]
pub struct AuthorsPage {
    pub authors: Vec<String>,       // 64-char lowercase hex pubkeys, byte-ascending
    pub end_cursor: Option<String>, // → endCursor (last pubkey hex), None at end-of-stream
    pub has_more: bool,             // → hasMore
}
```

Mirrors `EventsPage` shape. Per-author counts are explicitly excluded (CONTEXT Output-shape decision — would reintroduce O(total events) fan-out).

### `authors` resolver in `src/graphql/resolvers.rs`

Implements the full GraphQL→engine→LMDB pipeline for API-07:

**Limit clamp** (D-04/D-05/T-07-DOS): identical to `events()`:
```rust
let effective_limit = limit.map(|l| (l.max(1) as usize).min(500)).unwrap_or(100);
```

**Two-step fail-closed cursor decode** (T-07-CUR):
1. `crate::query::router::decode_hex(s)` — rejects non-hex chars and odd-length strings.
2. `.try_into::<[u8; 32]>()` — rejects any byte length != 32 (the critical gate: `decode_hex` alone accepts an even-length string of any length).

Error messages use generic text; the offending bytes are NEVER echoed.

**`spawn_blocking` with env cloned before closure** (Pitfall 1):
```rust
let env = state.env.clone();
tokio::task::spawn_blocking(move || distinct_authors(&env, after_bytes.as_ref(), effective_limit))
```

**Result mapping**: `pubkeys.iter().map(hex_encode_lowercase).collect()` for both `authors` list and `end_cursor`.

### `hex_encode_lowercase` private helper in `src/graphql/resolvers.rs`

The codebase has `decode_hex` (a decoder) but no hex encoder. Added inline:
```rust
fn hex_encode_lowercase(pk: &[u8; 32]) -> String {
    use std::fmt::Write as _;
    let mut s = String::with_capacity(64);
    for b in pk { write!(s, "{:02x}", b).expect("infallible"); }
    s
}
```

No new crate dependency — consistent with the project's decode_hex inline policy.

### `tests/authors_test.rs` — 7 fixture integration tests

| Test | Behavior Verified |
|------|-------------------|
| `test_authors_full_page` | Returns [PK1, PK2] byte-ascending, hasMore=false, endCursor=null |
| `test_authors_limit_1_returns_pk1_with_cursor` | Returns [PK1], hasMore=true, endCursor=PK1 |
| `test_authors_multi_page_walk` | Feeds endCursor back as after; collects [PK1,PK2] exactly once; terminates |
| `test_authors_bad_cursor_nonhex` | Non-hex `"zzzz"` → errors non-empty, no echo |
| `test_authors_bad_cursor_short` | Short `"79be"` (2 bytes) → errors non-empty, no echo |
| `test_authors_bad_cursor_even_but_not_32_bytes` | `"79be66"` (3 bytes, even-length valid hex) → errors non-empty, no echo — proves try_into gate |
| `test_authors_no_mutation_in_schema_sdl` | SDL has no `type Mutation`, does contain `authors` |

The `"79be66"` test case is the critical proof: it is a valid even-length hex string that `decode_hex` alone would accept. Only the `try_into::<[u8; 32]>()` step rejects it, confirming the resolver enforces the full 32-byte contract.

### CLAUDE.md correction

**Before (stale):**
```
| Opening any `Event__*` sub-DB | Custom golpe comparators not present in deepfry process; range scans silently wrong | Only open `EventPayload`, `Meta`, `CompressionDictionary`, `Event` |
```

**After (corrected):**
```
| Opening an `Event__*` sub-DB WITHOUT registering its comparator | Without the matching golpe comparator, heed/LMDB falls back to memcmp ordering and range scans are silently wrong; opening IS supported when the comparator is registered | Use the `open_index_*` helpers in `src/lmdb/indexes.rs` — they register the correct comparator and call `open_database` (never `.create()`); the startup self-check gate validates correctness at runtime |
```

The blanket ban was stale since Phase 1, which opened all six `Event__*` indexes with reimplemented comparators behind a startup self-check gate.

## Verification

- `cargo build` — success
- `cargo test --lib` — 121 lib tests pass (no regression)
- `cargo test --test authors_test` — 7 integration tests pass
- `cargo test --all-targets` — 155 total tests, 0 failures

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — `authors(after, limit)` is a complete, tested implementation wired to the live index via `distinct_authors`.

## Threat Flags

No new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries beyond what the plan's threat model covers. All T-07-* mitigations applied:

| Threat | Mitigation Applied |
|--------|--------------------|
| T-07-CUR | Two-step fail-closed decode; generic error messages; no byte echo |
| T-07-DOS | Limit clamped [1,500] default 100 at resolver layer |
| T-07-LEAK | `map_query_error` reused; Lmdb/Payload → opaque "internal error" |
| T-07-SEND | `env` cloned before `spawn_blocking` closure |
| T-07-MUT | No mutation added; SDL regression test confirms no `type Mutation` |
| T-07-SC | No new crates added |

## Self-Check: PASSED

| Check | Result |
|-------|--------|
| `src/graphql/types.rs` has `struct AuthorsPage` | FOUND (1 match) |
| `src/graphql/resolvers.rs` has `async fn authors` | FOUND (1 match) |
| `src/graphql/resolvers.rs` has `distinct_authors` | FOUND (3 matches) |
| Limit clamp count >= 2 (events + authors) | FOUND (5 matches) |
| `fn hex_encode_lowercase` exists | FOUND (1 match) |
| `try_into` in resolvers.rs | FOUND (3 matches) |
| `tests/authors_test.rs` exists | FOUND |
| `tests/authors_test.rs` has >= 5 test fns | FOUND (7) |
| `"79be66"` in authors_test.rs | FOUND (4 matches) |
| Old blanket ban gone from CLAUDE.md | CONFIRMED (0 matches for old text) |
| `open_index` in CLAUDE.md | FOUND |
| `| Avoid | Why | Use Instead |` table header | FOUND (1 match) |
| Commits 94e0442, 0396680, 6e1ad83 exist | VERIFIED |
| `cargo test --all-targets` passes | 155 tests, 0 failures |
| No Cargo.toml changes | CONFIRMED |
