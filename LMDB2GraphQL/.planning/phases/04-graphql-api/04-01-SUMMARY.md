---
phase: 04-graphql-api
plan: "01"
subsystem: lmdb2graphql
tags: [graphql, types, config, dependencies, rust]
dependency_graph:
  requires:
    - 03-query-engine (DecodedEvent, NostrEvent, LevId from lmdb/types.rs)
    - 03-query-engine (QueryError, NostrFilter, TagFilter from query/filter.rs)
  provides:
    - GraphQL type contracts (Event, EventsPage, AuthorGroup, StatsResult)
    - GraphQL input types (EventFilterInput, TagFilterInput)
    - decoded_event_to_gql mapping function
    - bind_address config field (default 0.0.0.0:8080)
    - async-graphql 7.2.1 + axum 0.8.9 + tokio 1.52.3 in Cargo.toml
  affects:
    - 04-02 (consumes type contracts, config field, crate deps established here)
    - 05-docker-ops (bind_address config field used for server bind)
tech_stack:
  added:
    - async-graphql 7.2.1 (GraphQL schema + #[SimpleObject]/#[InputObject] macros, tracing feature)
    - async-graphql-axum 7.2.1 (axum HTTP bridge, matched exactly to async-graphql minor — Pitfall 3)
    - axum 0.8.9 (HTTP server)
    - tokio 1.52.3 (async runtime + spawn_blocking bridge for sync heed calls)
  patterns:
    - SimpleObject auto-renames snake_case → camelCase in GraphQL SDL
    - InputObject + Default for optional filter args
    - u64 → i64 cast for kind/created_at (GraphQL Int is 32-bit; i64 = 64-bit scalar)
    - String::from_utf8_lossy for raw_json passthrough (never re-serialize NostrEvent)
key_files:
  created:
    - spam/src/graphql/mod.rs (graphql module root: pub mod types)
    - spam/src/graphql/types.rs (all GraphQL types + decoded_event_to_gql)
  modified:
    - spam/Cargo.toml (4 new crate deps)
    - spam/src/config.rs (bind_address field + 2 tests)
    - spam/src/lib.rs (pub mod graphql added)
decisions:
  - "D-01 honored: raw field uses from_utf8_lossy(raw_json) passthrough, never re-serializes NostrEvent"
  - "D-02 honored: tags is Vec<Vec<String>> mapping to [[String!]!], no JSON scalar"
  - "D-03 honored: EventsPage is simple page object (end_cursor + has_more), not Relay Connection"
  - "D-07 honored: AuthorGroup preserves per-author grouping from engine HashMap"
  - "D-09 honored: StatsResult carries eventCount/maxLevId/dbVersion"
  - "Pitfall 2 mitigated: u64 kind/created_at cast to i64 in decoded_event_to_gql and GraphQL types"
  - "Pitfall 3 mitigated: async-graphql and async-graphql-axum pinned to exact same version 7.2.1"
  - "T-04-SC: all 4 crate deps pre-audited OK/Approved per RESEARCH Package Legitimacy Audit"
  - "schema.rs and resolvers.rs deferred to Plan 04-02 (only pub mod types declared in mod.rs)"
metrics:
  duration: "~9 minutes"
  completed: "2026-06-13"
  tasks_completed: 3
  files_created: 2
  files_modified: 3
  tests_added: 6
  tests_total: 116
---

# Phase 04 Plan 01: GraphQL Type Foundation Summary

GraphQL schema type contracts (SimpleObject + InputObject) + async-graphql/axum/tokio crate deps + bind_address config field — compile-verified foundation for Plan 04-02 resolver wiring.

## What Was Built

### Task 1: Add GraphQL crate dependencies (commit 4a0c959)

Added four crates to `Cargo.toml` at exactly-pinned versions (pre-audited in RESEARCH.md Package Legitimacy Audit — all OK/Approved):

```toml
async-graphql = { version = "7.2.1", features = ["tracing"] }
async-graphql-axum = "7.2.1"
axum = "0.8.9"
tokio = { version = "1.52.3", features = ["full"] }
```

Both `async-graphql` and `async-graphql-axum` pinned to `7.2.1` (exact minor version match required per Pitfall 3 — they share trait definitions that must align). First `cargo build --all-targets` downloaded and compiled all four crates plus transitive deps. Exit 0 — existing codebase unaffected.

### Task 2: bind_address config field (commit 9bba7f9)

Added `pub bind_address: String` to `Config` in `src/config.rs`:

- `#[serde(default = "default_bind_address")]` follows the existing `map_size`/`default_map_size` pattern exactly.
- `fn default_bind_address() -> String` returns `"0.0.0.0:8080".to_string()`.
- Two new tests (both using `tempfile::tempdir()`, never `~/deepfry/`):
  - `test_bind_address_default` — asserts default applied when YAML omits the field.
  - `test_explicit_bind_address` — asserts YAML override is honored.

### Task 3: GraphQL types module + DecodedEvent mapping (commit b709446)

Created `src/graphql/mod.rs` declaring `pub mod types;`. Schema and resolvers submodules are commented forward-declarations only (Plan 04-02 wires them — declaring stub files now would fail to compile without a Query root type, so only `types` is declared).

Created `src/graphql/types.rs` with:

| Type | Kind | Decision |
|------|------|----------|
| `Event` | `#[derive(SimpleObject)]` | D-01 (raw passthrough), D-02 (tags nested list), Pitfall 2 (i64) |
| `EventsPage` | `#[derive(SimpleObject)]` | D-03 (simple page, not Relay) |
| `AuthorGroup` | `#[derive(SimpleObject)]` | D-07 (per-author grouping) |
| `StatsResult` | `#[derive(SimpleObject)]` | D-09 (eventCount/maxLevId/dbVersion) |
| `EventFilterInput` | `#[derive(InputObject, Default)]` | API-01/02 (ids/authors/kinds/since/until/tag) |
| `TagFilterInput` | `#[derive(InputObject)]` | API-02 (single tag filter for v1) |

`decoded_event_to_gql(DecodedEvent) -> Event` mapping function:
- Casts `kind` and `created_at` from `u64` to `i64` (Pitfall 2 — GraphQL Int is 32-bit).
- Sets `raw` via `String::from_utf8_lossy(&d.raw_json).into_owned()` (D-01, Pitfall 5 — never re-serializes `NostrEvent`; re-serializing changes key order and whitespace vs what strfry stored).
- Moves `tags` directly (`Vec<Vec<String>>` → `[[String!]!]`, D-02).

Added `pub mod graphql` to `src/lib.rs`.

Added 4 unit tests in `src/graphql/types.rs`:
- `test_decoded_event_to_gql_i64_cast` — kind and created_at come through as i64.
- `test_decoded_event_to_gql_raw_passthrough` — raw equals input bytes exactly (D-01).
- `test_decoded_event_to_gql_tags` — tags are moved correctly.
- `test_decoded_event_to_gql_string_fields` — id/pubkey/content/sig are mapped.

## Verification Results

- `cargo build --all-targets` → exit 0 (deps resolve, types compile, mapping fn type-checks).
- `cargo test --all-targets` → **116 tests pass, 0 failures** (100 lib + 16 integration).
- `cargo test --all-targets config::` → 5 config tests pass (includes new bind_address tests).

## Deviations from Plan

### Schema/resolver forward declarations adjusted

**Task 3 action** specified declaring `pub mod schema;` and `pub mod resolvers;` in `mod.rs` if they compile. They do not compile without stub files (Rust requires each `pub mod X` to have a corresponding `X.rs` or `X/mod.rs`). Creating stub `schema.rs` / `resolvers.rs` would fail to compile because they need a `Query` type — which is Plan 04-02 content.

**Applied:** `pub mod types;` only in `mod.rs`. The schema/resolvers submodule declarations were forward-documented as comments pointing to Plan 04-02. This is correct per the plan's own escape hatch ("otherwise declare just `pub mod types;` and let 04-02 add the rest").

No other deviations — plan executed exactly as written.

## Known Stubs

None. This plan defines type contracts only — no resolvers, no data wiring. The types are compile-verified contracts consumed by Plan 04-02. All fields are properly typed with no placeholder values.

## Threat Flags

No new threat surface introduced. This plan adds types and crate deps only — no HTTP endpoints, no auth paths, no LMDB operations, no network surface beyond build-time crates.io resolution (covered by T-04-SC in plan threat model, all deps OK/Approved).

## Self-Check: PASSED

- `/Users/g/git/deepfry/spam/src/graphql/mod.rs` — FOUND
- `/Users/g/git/deepfry/spam/src/graphql/types.rs` — FOUND
- `/Users/g/git/deepfry/spam/src/config.rs` (bind_address) — FOUND
- `/Users/g/git/deepfry/spam/Cargo.toml` (async-graphql) — FOUND
- Commit 4a0c959 (Task 1 deps) — FOUND
- Commit 9bba7f9 (Task 2 config) — FOUND
- Commit b709446 (Task 3 types) — FOUND
