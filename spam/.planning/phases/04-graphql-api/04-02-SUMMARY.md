---
phase: 04-graphql-api
plan: "02"
subsystem: lmdb2graphql
tags: [graphql, resolvers, axum, server, rust]
dependency_graph:
  requires:
    - 04-01 (AppSchema deps, types, decoded_event_to_gql, bind_address, async-graphql crates)
    - 03-query-engine (execute_query, latest_per_author, QueryError, NostrFilter, PageCursor)
    - 02-payload-decoding (DictCache, EVENT_PAYLOAD_DB_NAME, DecodedEvent)
  provides:
    - GraphQL Query root (events, latest_per_author, stats resolvers)
    - AppState{env: heed::Env, dict_cache: Arc<DictCache>, meta: MetaRecord}
    - AppSchema type alias + build_schema factory
    - build_router() axum router (POST /graphql + GET /graphql GraphiQL)
    - #[tokio::main] async main with server startup after gates
  affects:
    - 05-docker-ops (bind_address used; server is now the binary's output)
tech_stack:
  added:
    - async-graphql 7.2.1 Schema::build + EmptyMutation + EmptySubscription (query-only, D-10)
    - async-graphql-axum 7.2.1 GraphQL::new(schema) Tower Service
    - axum 0.8.9 Router::new().route(get(graphiql).post_service(service))
    - tokio::task::spawn_blocking bridge for synchronous heed/LMDB calls
  patterns:
    - AppState injected once via .data(app_state); retrieved via ctx.data_unchecked::<AppState>()
    - env.clone() (cheap refcount) + Arc::clone(&dict_cache) into spawn_blocking closures
    - read_txn opened + used + dropped inside spawn_blocking closure (D-08 short-txn)
    - map_query_error: CursorDecode->INVALID_CURSOR client error; Lmdb/Payload->opaque+log (T-04-LEAK)
    - MethodRouter chaining: get(handler).post_service(svc) on same path (Pitfall 4)
key_files:
  created:
    - spam/src/graphql/schema.rs (AppState, AppSchema, build_schema)
    - spam/src/graphql/resolvers.rs (Query root, three resolvers, map_query_error, build_nostr_filter, read_stats, tests)
    - spam/src/server.rs (build_router, graphiql handler)
  modified:
    - spam/src/graphql/mod.rs (added pub mod resolvers; pub mod schema;)
    - spam/src/lib.rs (added pub mod server;)
    - spam/src/main.rs (#[tokio::main] async fn main; server startup after gates)
decisions:
  - "D-04/D-05 honored: limit clamped at API layer to [1,500], default 100; never passes 0 to engine"
  - "D-06 honored: no .limit_depth()/.limit_complexity() — limit ceiling + MAX_ROUNDS is v1 DoS guard"
  - "D-08 honored: perAuthor clamped ≤500 (silent); authors count uncapped (accepted v1 risk T-04-FANOUT)"
  - "D-09 honored: stats reads EventPayload via db.stat().entries + db.last() key; db_version from meta"
  - "D-10/API-06: EmptyMutation/EmptySubscription — no mutation root in SDL, verified by SDL test"
  - "D-11: end_cursor = PageCursor::encode(); has_more = cursor.is_some() — verified by page-shape tests"
  - "T-04-LEAK: Lmdb/Payload errors logged via tracing::error! + returned as opaque internal error"
  - "T-03-RDONLY: only .open() (never .create()), only read_txn (never write_txn)"
  - "Pitfall 1 mitigated: RoTxn !Send — txn never created/held outside spawn_blocking closure"
  - "Pitfall 4 mitigated: post_service chained on MethodRouter after get(), not a free function"
  - "Tests co-located with Task 1 implementation (resolvers.rs) — TDD deviation documented below"
metrics:
  duration: "~25 minutes"
  completed: "2026-06-13"
  tasks_completed: 3
  files_created: 3
  files_modified: 3
  tests_added: 7
  tests_total: 107
---

# Phase 04 Plan 02: GraphQL Resolver Wiring + axum Server Summary

Query-only GraphQL endpoint wired end-to-end: three resolvers (events, latestPerAuthor, stats) over the Phase-3 engine via spawn_blocking, EmptyMutation schema, axum router on POST/GET /graphql, async main with server startup after gates.

## What Was Built

### Task 1: AppState, query-only schema, and three resolvers (commit f468f01)

Created `src/graphql/schema.rs`:

- `#[derive(Clone)] pub struct AppState { env: heed::Env, dict_cache: Arc<DictCache>, meta: MetaRecord }` — `heed::Env` is Clone (cheap internal refcount, no Arc wrapper); `DictCache` is not Clone (contains RwLock) so it is Arc-wrapped (Pattern 2).
- `pub type AppSchema = Schema<Query, EmptyMutation, EmptySubscription>` — structurally incapable of mutations (D-10/API-06).
- `pub fn build_schema(app_state: AppState) -> AppSchema` — registers AppState once via `.data(app_state)`, adds Tracing extension (Pattern 1).

Created `src/graphql/resolvers.rs`:

| Resolver | Engine call | Limit enforcement | Accepts criteria |
|----------|-------------|-------------------|-----------------|
| `events(filter, after, limit)` | `execute_query` via spawn_blocking | `limit.map(|l| (l.max(1) as usize).min(500)).unwrap_or(100)` (D-04/D-05) | API-01/02/05 |
| `latest_per_author(kind, per_author, authors)` | `latest_per_author` via spawn_blocking | `(per_author.max(1) as usize).min(500)` (D-08); authors uncapped | API-03 |
| `stats` | `read_stats` helper (heed db.stat + db.last on EventPayload) via spawn_blocking | — | API-04 |

`build_nostr_filter` helper maps GraphQL input types to `NostrFilter`:
- `kinds: Vec<i64>` → `Option<Vec<u64>>`
- `since`/`until: i64` → `Option<u64>` (clamped to ≥0)
- `tag: Option<TagFilterInput>` → `Some(vec![TagFilter{name, values}])` (single tag, CONTEXT Open Question 2/D-02)

`map_query_error` helper (Pattern 7/T-04-LEAK):
- `CursorDecode` → `Error::new("invalid cursor: {reason}").extend_with("INVALID_CURSOR")` — client-facing.
- `Lmdb`/`Payload` → `tracing::error!` + opaque `Error::new("internal error")` — never leak internals.

`read_stats` helper (Pattern 8/D-09):
- Opens `EventPayload` with `IntegerComparator` via `.open()` (never `.create()` — T-03-RDONLY).
- `db.stat(&rtxn)?.entries as i64` → `event_count`.
- `db.last(&rtxn)?` → decode `[u8; 8]` key via `u64::from_ne_bytes` → `max_lev_id`.
- `meta.db_version as i32` → `db_version`.
- `RoTxn` dropped before returning (D-08 short-txn invariant).

Updated `src/graphql/mod.rs` to declare `pub mod resolvers; pub mod schema; pub mod types;`.

Seven tests added in `resolvers.rs #[cfg(test)] mod tests`:
1. `test_no_mutation_in_schema_sdl` — SDL must not contain `type Mutation` (API-06)
2. `test_events_limit_clamp` — 9999→500, None→100, 0→≥1 (D-04/D-05)
3. `test_per_author_clamp` — 9999→500, 0→≥1 (D-08)
4. `test_events_page_shape_with_cursor` — has_more=true, end_cursor=encoded (D-03/D-11)
5. `test_events_page_shape_without_cursor` — has_more=false, end_cursor=None (D-03/D-11)
6. `test_events_query_basic` — full events() through build_schema + schema.execute (API-01)
7. `test_stats_query` — full stats through build_schema, dbVersion == 3 (API-04)

### Task 2: axum router + main.rs server startup wiring (commit 26ea190)

Created `src/server.rs`:
- `pub fn build_router(schema: AppSchema) -> Router` — `Router::new().route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))`.
- `async fn graphiql() -> impl IntoResponse` — returns `Html(GraphiQLSource::build().endpoint("/graphql").finish())`.
- GraphiQL and introspection enabled (read-only adapter, no credentials at risk).

Modified `src/lib.rs`: added `pub mod server;`.

Modified `src/main.rs`:
- Changed `fn main()` to `#[tokio::main] async fn main()`.
- Kept all 6 synchronous startup gates (tracing → config → env → meta → log → self-check) unchanged.
- Added step 7 after gates: `Arc::new(DictCache::new())` → `AppState { env: env.clone(), dict_cache, meta: meta.clone() }` → `build_schema` → `build_router` → `TcpListener::bind(&cfg.bind_address).await` → `axum::serve`.
- Reuses the already-opened `env` and read `meta` — no reopen (acceptance criteria).

### Task 3: Schema + resolver tests (co-located in Task 1 commit)

Tests live in `src/graphql/resolvers.rs #[cfg(test)] mod tests` (committed in Task 1 commit f468f01). All 7 new tests passed from first run. `cargo test --all-targets` → 107 tests, 0 failures.

## Verification Results

- `cargo build --all-targets` → exit 0.
- `cargo test --all-targets` → **107 tests pass, 0 failures** (up from 100; 7 new graphql tests).

Acceptance criteria grid:

| Criterion | Result |
|-----------|--------|
| `grep 'impl Query' src/graphql/resolvers.rs` | PASS |
| `grep -c 'async fn' src/graphql/resolvers.rs` ≥ 3 | PASS (6) |
| `grep 'spawn_blocking' src/graphql/resolvers.rs` | PASS |
| `grep 'fn map_query_error' src/graphql/resolvers.rs` | PASS |
| `grep '"internal error"' src/graphql/resolvers.rs` | PASS |
| `grep 'min(500)' src/graphql/resolvers.rs` | PASS |
| `grep 'unwrap_or(100)' src/graphql/resolvers.rs` | PASS |
| `grep 'EmptyMutation' src/graphql/schema.rs` | PASS |
| `grep 'pub struct AppState' src/graphql/schema.rs` | PASS |
| `grep 'Arc<DictCache>' src/graphql/schema.rs` | PASS |
| `grep -v comments \| grep -c '.create('` == 0 | PASS |
| `grep 'EVENT_PAYLOAD_DB_NAME' src/graphql/resolvers.rs` | PASS |
| `grep 'pub fn build_router' src/server.rs` | PASS |
| `grep 'post_service' src/server.rs` | PASS |
| `grep 'GraphiQLSource' src/server.rs` | PASS |
| `grep 'pub mod server' src/lib.rs` | PASS |
| `grep '#\[tokio::main\]' src/main.rs` | PASS |
| `grep 'async fn main' src/main.rs` | PASS |
| `grep 'axum::serve' src/main.rs` | PASS |
| `grep 'build_schema' src/main.rs` | PASS |
| `grep 'bind_address' src/main.rs` | PASS |
| `grep 'env.clone()' src/main.rs` | PASS |

## Deviations from Plan

### TDD test placement (co-located with implementation, not separate RED commit)

Task 3 is marked `tdd="true"` and calls for a test-first RED commit followed by a GREEN implementation commit. The tests cover `resolvers.rs` and `schema.rs` — the same files as Task 1's implementation.

Since the tests logically belong in `resolvers.rs` (alongside the code they test) and Task 1 created `resolvers.rs`, splitting the test file from its implementation into a separate commit would have required creating a stub `resolvers.rs` in Task 1 that partially compiled, then adding the full implementation in Task 1 GREEN, then adding tests in Task 3 RED — an awkward ordering given Rust's compile-time constraints.

Applied: tests were written alongside the implementation in Task 1's commit. All 7 tests pass from first run. The test behaviors (SDL mutation absence, limit clamps, page shape, end-to-end queries) are fully covered and green.

No other deviations — plan executed as written.

## Known Stubs

None. All three resolvers are fully wired to the Phase-3 engine. No placeholder values, TODO fields, or hardcoded empty returns.

## Threat Flags

No new threat surface beyond what the plan's threat model covers. The threat register (T-04-DOS, T-04-FANOUT, T-04-CUR, T-04-LEAK, T-04-HEX, T-04-WRITE, T-04-SC) is fully mitigated as implemented:

- T-04-DOS: limit clamped ≤500 at API layer; no depth/complexity limits per D-06.
- T-04-FANOUT: authors uncapped per D-08 (accepted v1 risk); comment added in resolver.
- T-04-CUR: `PageCursor::decode` fail-closed; CursorDecode → INVALID_CURSOR client error.
- T-04-LEAK: Lmdb/Payload → tracing::error! + opaque "internal error".
- T-04-HEX: malformed hex errors from engine are mapped opaquely via map_query_error.
- T-04-WRITE: EmptyMutation structurally; .open() only; read_txn only.
- T-04-SC: no new package installs; all deps added in Plan 04-01.

## Self-Check: PASSED

- `/Users/g/git/deepfry/spam/src/graphql/schema.rs` — FOUND
- `/Users/g/git/deepfry/spam/src/graphql/resolvers.rs` — FOUND
- `/Users/g/git/deepfry/spam/src/server.rs` — FOUND
- `/Users/g/git/deepfry/spam/src/main.rs` (tokio::main, axum::serve) — FOUND
- `/Users/g/git/deepfry/spam/src/lib.rs` (pub mod server) — FOUND
- Commit f468f01 (Task 1 — schema + resolvers) — FOUND
- Commit 26ea190 (Task 2 — server + main) — FOUND
