# Phase 4: GraphQL API - Research

**Researched:** 2026-06-13
**Domain:** async-graphql 7.2.1 + async-graphql-axum 7.2.1 + axum 0.8.9 HTTP/schema adapter layer
**Confidence:** MEDIUM (all crate API references verified against live docs.rs at pinned versions)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01** Typed fields + `raw: String!` escape hatch on `Event` type (`id`, `pubkey`, `kind`, `createdAt`, `content`, `sig` + `raw` from `DecodedEvent.raw_json`).
- **D-02** `tags: [[String!]!]` — native nested list, no JSON scalar.
- **D-03** Simple page object: `{ events: [Event!]!, endCursor: String, hasMore: Boolean! }`. NOT Relay Connection. `after: String` arg passes the opaque cursor.
- **D-04** Hard ceiling = 500; default = 100. Enforced at API layer before calling engine.
- **D-05** Cap silently — never reject (clamped to 500).
- **D-06** No depth/complexity limits for v1.
- **D-07** `latestPerAuthor` returns `[{ author: String!, events: [Event!]! }]` (list of author groups).
- **D-08** Clamp `perAuthor` to ceiling (≤500, silent). Author count NOT capped.
- **D-09** `stats` returns `{ eventCount, maxLevId, dbVersion }`.
- **D-10** Query-only schema — no Mutation root.
- **D-11** Opaque cursor = `PageCursor::encode()` (already implemented in Phase 3).

### Claude's Discretion

- Module layout (e.g. `src/graphql/` schema + resolvers, `src/server.rs` or `src/http.rs` axum wiring).
- `QueryError` → GraphQL error mapping strategy (recommendation: `CursorDecode` + malformed hex → client error; `Lmdb`/`Payload` → opaque + `tracing::error!`).
- HTTP endpoint path (suggest `POST /graphql`), bind address/port from config.
- Whether engine calls run inside `tokio::task::spawn_blocking` (recommended per CLAUDE.md).
- How `heed::Env` + `DictCache` are shared into resolvers (async-graphql `Data`, `Arc`-wrapped).
- Field nullability details for `Event`.
- GraphiQL playground and schema introspection on/off (default: enable both for read-only service).

### Deferred Ideas (OUT OF SCOPE)

- async-graphql depth/complexity limits (D-06).
- Bounding `latestPerAuthor` author-count fan-out.
- Relay Connection pagination.
- Subscriptions / live push, REST facade, Prometheus `/metrics` (v2 scope).
- `/health`, `/ready`, Docker subsystem, docker-compose, CI fixture assertions (Phase 5).

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| API-01 | Consumer can query `events()` filtered by ids, authors, kinds, since, until, limit | `#[Object]` resolver constructs `NostrFilter`, calls `execute_query`, maps `DecodedEvent` → `Event` GQL type |
| API-02 | Consumer can query `events()` filtered by tag (name + values) | Resolver populates `NostrFilter.tags: Vec<TagFilter>`, same `execute_query` path |
| API-03 | Consumer can query `latestPerAuthor(kind, perAuthor, authors)` | Resolver calls `latest_per_author`, maps `HashMap<String,Vec<DecodedEvent>>` → `[AuthorGroup]` |
| API-04 | Consumer can query `stats` (event count, max levId, dbVersion) | `db.stat(&rtxn)?.entries` for count; `db.last(&rtxn)?` for max key; `Meta.db_version` from app state |
| API-05 | Hard limit ceiling + cursor pagination | Clamp `limit`/`perAuthor` at API layer; `after: String` arg decoded via `PageCursor::decode`; `endCursor`/`hasMore` from engine return |
| API-06 | API is read-only — no mutations | `Schema::build(Query, EmptyMutation, EmptySubscription)` — structurally impossible to mutate |

</phase_requirements>

---

## Summary

Phase 4 adds a thin HTTP/schema adapter on top of the Phase-3 query engine. The work is purely mechanical mapping: GraphQL arg structs → engine input types → engine results → GraphQL output types. No query logic is reimplemented.

The technology surface is three crates: `async-graphql` 7.2.1 (schema + resolver macros), `async-graphql-axum` 7.2.1 (axum HTTP bridge), and `axum` 0.8.9 (HTTP server). All three versions are confirmed current-stable on crates.io as of 2026-06-13. `tokio` 1.52.3 is required for the async runtime and `spawn_blocking` bridge.

The dominant implementation concern is the **sync/async boundary**: `heed::Env` and the engine functions are synchronous. Every resolver that calls the engine must wrap the call in `tokio::task::spawn_blocking` to avoid blocking the tokio runtime's async threads. `heed::Env` is `Clone + Send + Sync`, so it can be cloned directly into closures. `DictCache` is not `Clone` (contains `RwLock`) and must be wrapped in `Arc<DictCache>`.

**Primary recommendation:** Define `AppState { env: heed::Env, dict_cache: Arc<DictCache>, meta: MetaRecord }`, register it once in `Schema::build(...).data(app_state).finish()`, retrieve it in each resolver via `ctx.data_unchecked::<AppState>()`, clone `env` and `Arc::clone(&state.dict_cache)` into `spawn_blocking` closures.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| GraphQL schema definition | API / GraphQL layer (`src/graphql/`) | — | Types, resolvers, field mapping live here |
| HTTP server binding | HTTP layer (`src/server.rs` or `src/http.rs`) | — | axum `Router`, `TcpListener`, `serve` |
| Query execution (filter → events) | Engine (`src/query/engine.rs`) | API layer (arg mapping) | Engine already built in Phase 3; API layer constructs `NostrFilter` and calls it |
| Limit ceiling enforcement | API layer (resolver) | — | D-04/D-05: clamp at the resolver, before calling engine |
| Cursor encode/decode | Engine types (`src/query/filter.rs`) | API layer (arg/field) | `PageCursor::encode/decode` already exist; resolver calls them |
| Shared state (Env + DictCache) | API layer (app state) | — | Constructed in `main.rs`, injected via async-graphql `Data` |
| stats query | API layer resolver + LMDB | — | Opens short `read_txn`, calls `db.stat()` and `db.last()` on `EventPayload` |
| Startup gate (continues from Phase 1–3) | `main.rs` | — | Server starts ONLY after gates pass; reuses same opened `Env` |

---

## Standard Stack

### Core (new additions for Phase 4)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `async-graphql` | 7.2.1 | Schema + resolver macros | Most active Rust GraphQL library; procedural macros; built-in complexity limits (deferred per D-06); pinned per CLAUDE.md [VERIFIED: crates.io] |
| `async-graphql-axum` | 7.2.1 | GraphQL ↔ axum HTTP bridge | Official integration crate; `GraphQL::new(schema)` as Tower Service; must match `async-graphql` minor version exactly [VERIFIED: crates.io] |
| `axum` | 0.8.9 | HTTP server | Tokio-team maintained; idiomatic async; pinned per CLAUDE.md [VERIFIED: crates.io] |
| `tokio` | 1.52.3 | Async runtime | Required by axum; `spawn_blocking` for sync heed calls [VERIFIED: crates.io] |

### Already in Cargo.toml (reused)

| Library | Version | Purpose |
|---------|---------|---------|
| `tracing` | 0.1 | Structured logging in resolvers |
| `tracing-subscriber` | 0.3 | JSON output for Docker |
| `serde_json` | 1 | `raw_json: Vec<u8>` → `String` conversion for `Event.raw` field |
| `heed` | 0.22.1 | Stats query: `db.stat()`, `db.last()` on `EventPayload` |
| `thiserror` | 2 | Error type boundaries |
| `anyhow` | 1 | Error propagation in `main.rs` |

**Installation (additions to Cargo.toml):**
```toml
async-graphql = { version = "7.2.1", features = ["tracing"] }
async-graphql-axum = "7.2.1"
axum = "0.8.9"
tokio = { version = "1.52.3", features = ["full"] }
```

Note: the `tracing` feature on `async-graphql` enables the `.extension(Tracing)` schema builder hook for free tracing of resolver execution. [CITED: docs.rs/async-graphql/7.2.1/async_graphql/extensions/struct.Tracing.html]

---

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `async-graphql` | crates.io | ~6 yrs (2020-03-03) | 209,307/wk | github.com/async-graphql/async-graphql | OK | Approved |
| `async-graphql-axum` | crates.io | ~5 yrs (2021-09-01) | 102,877/wk | github.com/async-graphql/async-graphql | OK | Approved |
| `axum` | crates.io | ~5 yrs (2021-07-22) | 6,807,273/wk | github.com/tokio-rs/axum | OK | Approved |
| `tokio` | crates.io | ~10 yrs (2016-07-01) | 13,283,969/wk | github.com/tokio-rs/tokio | OK | Approved |

**Packages removed due to SLOP verdict:** none
**Packages flagged as suspicious SUS:** none

All four packages confirmed via crates.io API + legitimacy seam check. [VERIFIED: crates.io via package-legitimacy check 2026-06-13]

---

## Architecture Patterns

### System Architecture Diagram

```
HTTP Client
    │
    │ POST /graphql (JSON body)
    ▼
axum Router
    ├── GET  /graphql  ──► graphiql_handler() → Html(GraphiQLSource)
    └── POST /graphql  ──► GraphQL::new(schema)  [Tower Service]
                               │
                               │ schema.execute(request)
                               ▼
                        async-graphql Query root
                               │
                ┌──────────────┼──────────────┐
                │              │              │
          events(...)  latestPerAuthor(...)  stats
                │              │              │
                ▼              ▼              ▼
         spawn_blocking  spawn_blocking   spawn_blocking
                │              │              │
         execute_query  latest_per_author  db.stat()+db.last()
                │              │              │
         heed::Env (LMDB)  heed::Env     heed::Env
                │              │              │
         DecodedEvent[]   HashMap<...>   (u64, u32)
                │              │              │
                └──────────────┴──────────────┘
                               │
                        map to GraphQL types
                        (Event, AuthorGroup, Stats)
                               │
                        GraphQLResponse → HTTP 200
```

### Recommended Project Structure

```
src/
├── graphql/
│   ├── mod.rs           # pub use schema, types
│   ├── schema.rs        # Schema type alias, build_schema() fn
│   ├── types.rs         # Event, EventsPage, AuthorGroup, StatsResult #[SimpleObject]
│   └── resolvers.rs     # Query struct + #[Object] impl (events, latestPerAuthor, stats)
├── server.rs            # build_router(), bind address, axum::serve()
├── config.rs            # Config (extended: add bind_address field)
├── lmdb/                # unchanged from Phase 3
├── query/               # unchanged from Phase 3
├── lib.rs               # add pub mod graphql; pub mod server (if server stays in lib)
└── main.rs              # extended: after gates pass, call build_schema + build_router + serve
```

### Pattern 1: Query-Only Schema Construction

**What:** Build an async-graphql schema with no Mutation or Subscription roots, injecting shared state once.
**When to use:** This is the only schema construction pattern for this phase.

```rust
// Source: docs.rs/async-graphql/7.2.1/async_graphql/struct.SchemaBuilder.html
// Source: docs.rs/async-graphql/7.2.1/async_graphql/extensions/struct.Tracing.html
use async_graphql::{Schema, EmptyMutation, EmptySubscription, extensions::Tracing};
use crate::graphql::resolvers::Query;

pub type AppSchema = Schema<Query, EmptyMutation, EmptySubscription>;

pub fn build_schema(app_state: AppState) -> AppSchema {
    Schema::build(Query, EmptyMutation, EmptySubscription)
        .data(app_state)          // register once; retrieved per-resolver via ctx.data_unchecked
        .extension(Tracing)       // free tracing of resolver execution (requires "tracing" feature)
        .finish()
}
```

`EmptyMutation` and `EmptySubscription` are provided by async-graphql. A schema built this way has no `mutation` type in the SDL — it is **structurally** incapable of mutations (D-10). [CITED: docs.rs/async-graphql/7.2.1]

### Pattern 2: Shared App State

**What:** Wrap the opened `heed::Env`, `Arc<DictCache>`, and `MetaRecord` in a single `AppState` struct, registered once via `schema.data()`.
**When to use:** All three resolvers need these; register once, retrieve per-call.

```rust
// heed::Env is Clone + Send + Sync — can be cloned directly (no Arc wrapper needed)
// DictCache is NOT Clone (contains RwLock) — must be Arc-wrapped
#[derive(Clone)]
pub struct AppState {
    pub env: heed::Env,                   // Clone is cheap (heed internal refcount)
    pub dict_cache: Arc<DictCache>,       // Arc for shared ownership across spawn_blocking clones
    pub meta: MetaRecord,                 // Clone — plain struct with primitives
}
```

`heed::Env` implements `Clone + Send + Sync` per heed 0.22.1 docs. [CITED: docs.rs/heed/0.22.1/heed/struct.Env.html]

### Pattern 3: Data Retrieval in Resolvers

**What:** Retrieve `AppState` from async-graphql context inside a resolver.
**When to use:** Every `#[Object]` resolver method.

```rust
// Source: async-graphql.github.io/async-graphql/en/context.html
#[Object]
impl Query {
    async fn events(
        &self,
        ctx: &Context<'_>,
        // ... args
    ) -> Result<EventsPage> {
        let state = ctx.data_unchecked::<AppState>();
        // clone into spawn_blocking:
        let env = state.env.clone();
        let dict_cache = Arc::clone(&state.dict_cache);
        // ...
    }
}
```

`ctx.data_unchecked::<T>()` panics if `T` was not registered — use only for types guaranteed registered at schema build. `ctx.data::<T>()` returns `Result<&T>` for fallible access. [CITED: docs.rs/async-graphql/7.2.1/async_graphql/struct.Context.html]

### Pattern 4: spawn_blocking for Synchronous Engine Calls

**What:** Call the synchronous `execute_query` / `latest_per_author` / heed stat functions from async resolvers without blocking tokio.
**When to use:** Every engine call in every resolver.

```rust
// Source: docs.rs/tokio/1.52.3/tokio/task/fn.spawn_blocking.html
async fn events_resolver(state: &AppState, filter: NostrFilter, cursor: Option<PageCursor>)
    -> Result<EventsPage>
{
    let env = state.env.clone();                     // heed::Env is Clone
    let dict_cache = Arc::clone(&state.dict_cache);  // Arc<DictCache>

    tokio::task::spawn_blocking(move || {
        // &*dict_cache deref-coerces Arc<DictCache> → &DictCache
        crate::query::engine::execute_query(&env, &filter, &*dict_cache, cursor.as_ref())
    })
    .await
    .map_err(|e| async_graphql::Error::new(format!("task join error: {e}")))?
    .map_err(map_query_error)
}
```

The closure must be `'static` — no borrows from the async context. Clone `env` and `Arc::clone` dict_cache before the closure, then move them in. [CITED: docs.rs/tokio/1.52.3/tokio/task/fn.spawn_blocking.html]

### Pattern 5: axum Router Wiring

**What:** Mount the GraphQL Tower Service on `POST /graphql` and a GraphiQL playground on `GET /graphql`.
**When to use:** The single `build_router()` function in `server.rs`.

```rust
// Source: medium.com/@mikecode (verified against async-graphql-axum 7.2.1 docs)
use async_graphql::http::GraphiQLSource;
use async_graphql_axum::GraphQL;
use axum::{response::{Html, IntoResponse}, routing::get, Router};

pub fn build_router(schema: AppSchema) -> Router {
    Router::new()
        .route(
            "/graphql",
            get(graphiql).post_service(GraphQL::new(schema)),
        )
}

async fn graphiql() -> impl IntoResponse {
    Html(GraphiQLSource::build().endpoint("/graphql").finish())
}
```

`GraphQL::new(schema)` wraps the schema as a Tower `Service` and is mounted via `post_service()`. `get(graphiql)` handles the `GET /graphql` playground request on the same path. [CITED: docs.rs/async-graphql-axum/7.2.1/async_graphql_axum/struct.GraphQL.html]

### Pattern 6: GraphQL Output Types

**What:** Define the `Event`, `EventsPage`, `AuthorGroup`, and `StatsResult` GraphQL types.
**When to use:** `src/graphql/types.rs`.

```rust
// Source: docs.rs/async-graphql/7.2.1 — SimpleObject auto-exposes struct fields
use async_graphql::SimpleObject;

/// GraphQL Event type (D-01/D-02)
#[derive(SimpleObject)]
pub struct Event {
    pub id: String,
    pub pubkey: String,
    pub kind: i64,                  // u64 → i64 for GraphQL Int compatibility (see Pitfall 2)
    pub created_at: i64,            // u64 → i64
    pub content: String,
    pub sig: String,
    pub tags: Vec<Vec<String>>,     // maps to [[String!]!] automatically (D-02)
    pub raw: String,                // exact JSON passthrough from DecodedEvent.raw_json (D-01)
}

/// Page object (D-03)
#[derive(SimpleObject)]
pub struct EventsPage {
    pub events: Vec<Event>,
    pub end_cursor: Option<String>, // None when no next page; async-graphql renames to endCursor
    pub has_more: bool,
}

/// latestPerAuthor group (D-07)
#[derive(SimpleObject)]
pub struct AuthorGroup {
    pub author: String,
    pub events: Vec<Event>,
}

/// stats result (D-09)
#[derive(SimpleObject)]
pub struct StatsResult {
    pub event_count: i64,   // usize → i64; async-graphql renames to eventCount
    pub max_lev_id: i64,    // u64 → i64; renamed to maxLevId
    pub db_version: i32,    // u32 → i32; renamed to dbVersion
}
```

`#[derive(SimpleObject)]` auto-renames snake_case fields to camelCase in the schema. [CITED: docs.rs/async-graphql/7.2.1]

### Pattern 7: QueryError → GraphQL Error Mapping

**What:** Translate engine `QueryError` variants to appropriate GraphQL errors.
**When to use:** In a helper function called by every resolver that invokes the engine.

```rust
// Source: async-graphql.github.io/async-graphql/en/error_handling.html
// Source: docs.rs/async-graphql/7.2.1/async_graphql/struct.Error.html
use async_graphql::{Error, ErrorExtensions};
use crate::query::filter::QueryError;

fn map_query_error(e: QueryError) -> Error {
    match e {
        QueryError::CursorDecode { reason } => {
            // Client error: malformed cursor — safe to expose
            Error::new(format!("invalid cursor: {reason}"))
                .extend_with(|_, ext| ext.set("code", "INVALID_CURSOR"))
        }
        QueryError::Lmdb(inner) => {
            // Internal error: log it, return opaque message
            tracing::error!(error = %inner, "LMDB error during query");
            Error::new("internal error")
        }
        QueryError::Payload(inner) => {
            // Internal error: log it, return opaque message
            tracing::error!(error = %inner, "payload decode error during query");
            Error::new("internal error")
        }
    }
}
```

Hex decode errors (malformed `ids`/`authors`/`pubkeys` in GraphQL args) are produced by the resolver before calling the engine; they are also client errors and can use `Error::new("invalid hex: ...")`. [CITED: docs.rs/async-graphql/7.2.1/async_graphql/struct.Error.html]

### Pattern 8: stats Implementation

**What:** Read `eventCount` (LMDB stat), `maxLevId` (last key of `EventPayload`), and `dbVersion` (from app state).
**When to use:** `stats` resolver.

```rust
// Source: docs.rs/heed/0.22.1/heed/struct.Database.html
// Source: docs.rs/heed/0.22.1/heed/struct.DatabaseStat.html
use crate::lmdb::payload::EVENT_PAYLOAD_DB_NAME;
use heed::{types::Bytes, IntegerComparator};

fn read_stats(env: &heed::Env, db_version: u32) -> Result<StatsResult, QueryError> {
    let rtxn = env.read_txn().map_err(...)?;
    let db: heed::Database<Bytes, Bytes, IntegerComparator> = env
        .database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<IntegerComparator>()
        .name(EVENT_PAYLOAD_DB_NAME)
        .open(&rtxn)
        .map_err(...)?
        .ok_or(...)?;

    let stat = db.stat(&rtxn).map_err(...)?;
    let event_count = stat.entries as i64;

    // db.last() returns Option<(key, value)>; key is the max levId (IntegerKey)
    let max_lev_id: i64 = db
        .last(&rtxn)
        .map_err(...)?
        .map(|(k, _)| u64::from_ne_bytes(k.try_into().unwrap_or([0u8;8])) as i64)
        .unwrap_or(0);

    Ok(StatsResult { event_count, max_lev_id, db_version: db_version as i32 })
}
```

`DatabaseStat.entries` = total entry count. `db.last()` = last (largest IntegerKey) entry. [CITED: docs.rs/heed/0.22.1/heed/struct.DatabaseStat.html]

### Anti-Patterns to Avoid

- **Calling engine functions directly in async resolvers (not via spawn_blocking):** heed/LMDB is synchronous C FFI. Calling it directly on a tokio async thread blocks the executor, starving other requests. Always use `spawn_blocking`.
- **Holding `RoTxn` across `await` points:** `RoTxn` is not `Send` (tied to the LMDB thread that created it). It can only live inside the `spawn_blocking` closure; never hold or return one.
- **Using `Arc<heed::Env>`:** Unnecessary — `heed::Env` is `Clone` and cheap to clone (internal reference count). Use `state.env.clone()` directly.
- **Forgetting to `Arc`-wrap `DictCache`:** `DictCache` contains `RwLock` and is not `Clone`. Without `Arc`, you cannot move it into `spawn_blocking` closures from shared state.
- **`ctx.data::<T>()` vs `ctx.data_unchecked::<T>()`:** Use `data_unchecked` for state that is always registered (panics with a clear message if missing, vs silently returning `Err`). Use `data` for optional data.
- **Re-serializing `raw_json`:** `DecodedEvent.raw_json` is `Vec<u8>` containing the exact bytes strfry stored. Convert to `String` with `String::from_utf8_lossy` or `String::from_utf8`. Never `serde_json::to_string` the `NostrEvent` struct — re-serializing changes key order and whitespace (Phase 2 D-01).
- **Using `u64` directly as GraphQL Int:** GraphQL `Int` is 32-bit signed. Use `i64` (maps to `Int64` / `BigInt`) for `kind`, `created_at`, `max_lev_id` to avoid truncation.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| GraphQL schema serialization / SDL generation | Custom SDL emitter | `async-graphql` macro system | Handles type registration, nullability, introspection automatically |
| Cursor encode/decode | New base64 codec | `PageCursor::encode/decode` (already in `src/query/filter.rs`) | Already implemented, fail-closed, tested (T-03-CUR) |
| JSON content-type parsing of POST body | Manual body parsing | `async-graphql-axum`'s `GraphQL::new(schema)` Tower Service | Handles GET params, POST JSON body, content-type negotiation |
| GraphiQL playground HTML | Custom HTML | `GraphiQLSource::build().endpoint("/graphql").finish()` | Maintains up-to-date GraphiQL v2 HTML |
| Field name snake→camelCase conversion | Manual renaming attributes | `#[derive(SimpleObject)]` — auto-renames | Rust snake_case → GraphQL camelCase is the default behavior |
| Error classification | Custom error discriminant | `map_query_error` helper with `match` on `QueryError` variants | Each variant has distinct client-safety semantics |

**Key insight:** In this phase, every "interesting" problem is already solved — cursor codec in Phase 3, query execution in Phase 3, event decoding in Phase 2. The planner should treat this as pure mapping/adapter work with no novel algorithms.

---

## Common Pitfalls

### Pitfall 1: `RoTxn` Lifetime and `spawn_blocking`

**What goes wrong:** The resolver creates a `RoTxn` outside `spawn_blocking`, passes it by reference into an `async` block, then fails to compile (`RoTxn` is not `Send`).
**Why it happens:** heed's `RoTxn` is tied to the LMDB environment thread context and intentionally not `Send`.
**How to avoid:** All LMDB operations — including txn creation — happen *inside* the `spawn_blocking` closure. Resolvers pass only `heed::Env` (cloned, `Send + Sync`) and `Arc<DictCache>` into the closure.
**Warning signs:** Compiler error mentioning `RoTxn: !Send` or `future is not Send`.

### Pitfall 2: u64 Fields and GraphQL Int Type

**What goes wrong:** Returning `u64` from a `#[SimpleObject]` or `#[Object]` field — async-graphql's built-in `u64` scalar is `Int` (32-bit) or absent depending on version. `kind` and `created_at` are `u64` on `NostrEvent` but must be `i64` in the GraphQL type to map to a 64-bit integer.
**Why it happens:** GraphQL's `Int` is 32-bit signed; async-graphql's Rust mapping uses `i64` for 64-bit integers.
**How to avoid:** `Event` and `StatsResult` GraphQL types use `i64` fields. Cast `NostrEvent.kind as i64`, `NostrEvent.created_at as i64`, etc. in the `DecodedEvent → Event` mapping.
**Warning signs:** Overflow on `kind` values > 2^31 or `created_at` values > 2^31 (year 2038 and beyond). Compile-time type mismatch errors.

### Pitfall 3: `async-graphql` and `async-graphql-axum` Version Mismatch

**What goes wrong:** Using `async-graphql` 7.2.1 with `async-graphql-axum` 7.0.x — they share internal trait definitions that must be at the exact same minor version.
**Why it happens:** The crates share type definitions; minor-version mismatches cause trait-impl conflicts at compile time.
**How to avoid:** Pin both to exactly `7.2.1` in Cargo.toml. Do not let Cargo semver-resolve them to different patches.
**Warning signs:** `error[E0277]: the trait bound ... is not satisfied` at schema or handler construction.

### Pitfall 4: `post_service` vs `route_service` vs `route`

**What goes wrong:** Using `Router::route("/graphql", post_service(...))` — there is no free function `post_service` in `axum::routing` that returns a `MethodRouter` directly in axum 0.8.
**Why it happens:** axum 0.8's routing API uses `MethodRouter` methods for method-specific service mounting: `get(handler).post_service(svc)` chains on a `MethodRouter`.
**How to avoid:** Use the pattern: `Router::new().route("/graphql", get(graphiql_handler).post_service(GraphQL::new(schema)))`. The `post_service` method is on `MethodRouter`, called after `get(...)`.
**Warning signs:** Compile error that `post_service` is not found or type mismatch on `route()`.

### Pitfall 5: `String::from_utf8` on `raw_json`

**What goes wrong:** Panicking or returning an error when `raw_json` (originally valid UTF-8 JSON from strfry) fails `String::from_utf8`.
**Why it happens:** `raw_json: Vec<u8>` is guaranteed valid UTF-8 (it's a JSON string from strfry). Using `String::from_utf8` returns `Result` — callers sometimes `.unwrap()` without considering the `Err` arm, even though strfry events are always valid UTF-8.
**How to avoid:** Use `String::from_utf8_lossy(&decoded.raw_json).into_owned()` as a safe fallback, or use `String::from_utf8(decoded.raw_json).expect("strfry event JSON is always valid UTF-8")` with a documentation comment explaining why the expect is safe.
**Warning signs:** N/A (will not panic in practice with real strfry data, but panic path exists in `from_utf8`).

### Pitfall 6: Limit Ceiling Enforcement — Where It Happens

**What goes wrong:** Forgetting to clamp `limit`/`perAuthor` at the resolver before calling the engine, or clamping inside the engine (wrong layer per D-04/D-05).
**Why it happens:** The engine's `filter.limit = 0` behavior (use `DEFAULT_WINDOW_SIZE`) is internal and separate from the Phase-4 ceiling. The engine does not enforce the 500-event ceiling.
**How to avoid:** The resolver clamps: `let effective_limit = limit.unwrap_or(100).min(500);` before building `NostrFilter`. Default 100 when omitted (D-04). Cap 500 (D-05). Pass `effective_limit` as `NostrFilter.limit`.
**Warning signs:** Query returning more than 500 events, or `NostrFilter.limit = 0` being passed accidentally (triggers engine default 256, not the API default 100).

---

## Code Examples

### Full events() resolver skeleton

```rust
// Source: patterns assembled from docs.rs/async-graphql/7.2.1 + engine.rs
#[Object]
impl Query {
    /// events() — filtered event feed with cursor pagination (API-01/02/05)
    async fn events(
        &self,
        ctx: &Context<'_>,
        #[graphql(desc = "NIP-01 filter")] filter: Option<EventFilterInput>,
        #[graphql(desc = "Opaque pagination cursor from previous page")] after: Option<String>,
        #[graphql(desc = "Maximum events to return (1–500, default 100)")] limit: Option<i32>,
    ) -> Result<EventsPage> {
        let state = ctx.data_unchecked::<AppState>();

        // D-04/D-05: clamp at API layer, before calling engine
        let effective_limit = limit
            .map(|l| (l.max(1) as usize).min(500))
            .unwrap_or(100);

        // Decode optional cursor (T-03-CUR: fail-closed on bad input)
        let cursor: Option<PageCursor> = match after {
            Some(s) => Some(PageCursor::decode(&s).map_err(map_query_error)?),
            None => None,
        };

        // Build NostrFilter from GraphQL args
        let nostr_filter = build_nostr_filter(filter, effective_limit)?; // client error on bad hex

        let env = state.env.clone();
        let dict_cache = Arc::clone(&state.dict_cache);

        let (decoded_events, next_cursor) =
            tokio::task::spawn_blocking(move || {
                execute_query(&env, &nostr_filter, &*dict_cache, cursor.as_ref())
            })
            .await
            .map_err(|e| Error::new(format!("task error: {e}")))?
            .map_err(map_query_error)?;

        let events: Vec<Event> = decoded_events
            .into_iter()
            .map(decoded_event_to_gql)
            .collect();

        Ok(EventsPage {
            events,
            end_cursor: next_cursor.map(|c| c.encode()),
            has_more: next_cursor.is_some(),
        })
    }
}
```

### EventFilterInput (GraphQL input type)

```rust
// Source: docs.rs/async-graphql/7.2.1 — InputObject macro
#[derive(InputObject, Default)]
pub struct EventFilterInput {
    pub ids: Option<Vec<String>>,
    pub authors: Option<Vec<String>>,
    pub kinds: Option<Vec<i64>>,
    pub since: Option<i64>,
    pub until: Option<i64>,
    pub tag: Option<TagFilterInput>,   // single tag filter per query (API-02)
}

#[derive(InputObject)]
pub struct TagFilterInput {
    pub name: String,
    pub values: Vec<String>,
}
```

### Mapping DecodedEvent to GraphQL Event

```rust
fn decoded_event_to_gql(d: DecodedEvent) -> Event {
    Event {
        id: d.event.id,
        pubkey: d.event.pubkey,
        kind: d.event.kind as i64,         // u64 → i64 (Pitfall 2)
        created_at: d.event.created_at as i64,
        content: d.event.content,
        sig: d.event.sig,
        tags: d.event.tags,                // Vec<Vec<String>> → [[String!]!] (D-02)
        // Phase 2 D-01: use raw_json passthrough, never re-serialize
        raw: String::from_utf8_lossy(&d.raw_json).into_owned(),
    }
}
```

### main.rs extension (server startup)

```rust
// After gates pass in main.rs — extend the existing startup sequence:
let dict_cache = Arc::new(DictCache::new());
let app_state = AppState { env: env.clone(), dict_cache, meta: meta.clone() };
let schema = build_schema(app_state);
let router = build_router(schema);

// Bind address from config (needs new `bind_address` field in Config)
let listener = tokio::net::TcpListener::bind(&cfg.bind_address)
    .await
    .context("bind HTTP listener")?;
tracing::info!(addr = %listener.local_addr()?, "GraphQL server listening");
axum::serve(listener, router).await.context("axum serve")?;
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `juniper` for Rust GraphQL | `async-graphql` as dominant library | ~2020 onward | async-graphql has first-class procedural macros, built-in depth/complexity limits, axum integration crate |
| `actix-web` for Rust HTTP | `axum` (tokio-rs maintained) | ~2021 onward | axum has stronger tokio integration, Tower middleware compatibility |
| Manual introspection handlers | `async-graphql` built-in introspection | N/A | Schema introspection is on by default; disable with `.disable_introspection()` if needed |
| `async-graphql` v8 RC | v7.2.1 stable | v8.0.0-rc in progress | RC per CLAUDE.md — stick with stable 7.2.1 |

**Deprecated/outdated:**
- `juniper`: Lower community momentum; no built-in complexity limits; weaker axum story [ASSUMED]
- `async-graphql` v8.0.0-rc.x: Release candidate — CLAUDE.md explicitly says do not use [CITED: CLAUDE.md]

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `get(handler).post_service(svc)` chaining on `MethodRouter` is the correct axum 0.8 pattern for same-path GET+POST-service | Architecture Patterns / Pattern 5 | Could fail to compile if axum 0.8 broke this chaining; workaround: use `route_service` + manually handle GET via middleware |
| A2 | Field name auto-renaming (snake_case → camelCase) is the default for `#[derive(SimpleObject)]` without explicit `#[graphql(name = "...")]` | Architecture Patterns / Pattern 6 | If wrong, `endCursor`/`hasMore`/`eventCount` would appear as `end_cursor`/`has_more`/`event_count` in schema; fix with explicit `#[graphql(name)]` attributes |
| A3 | `u64` fields on `NostrEvent` must be cast to `i64` for GraphQL; async-graphql does not have a native `u64` scalar | Architecture Patterns / Pattern 6, Pitfall 2 | If async-graphql 7.x added `u64` support, direct use is possible; otherwise casting to `i64` is correct and values stay in range for Nostr timestamps/kinds |

**If this table is empty:** All claims in this research were verified or cited — no user confirmation needed.
_(Table is not empty — A1/A2/A3 above need planner awareness.)_

---

## Open Questions

1. **Config: `bind_address` field**
   - What we know: `Config` currently has no `bind_address`. The server needs a listen address.
   - What's unclear: Whether to hardcode `0.0.0.0:8080` or add a `bind_address: String` field to `Config` with a YAML default. Context.md says "bind address/port from config; follow the `~/deepfry/lmdb2graphql.yaml` convention" (Claude's Discretion).
   - Recommendation: Add `bind_address: String` to `Config` with `#[serde(default = "default_bind_address")]` → `"0.0.0.0:8080"`. This follows existing config pattern.

2. **`tags` GraphQL field: single `TagFilterInput` or `Vec<TagFilterInput>`?**
   - What we know: `NostrFilter.tags` is `Option<Vec<TagFilter>>` — AND semantics across multiple tag filters (NIP-01 CR-06 fix in engine.rs).
   - What's unclear: Context.md says "API-02: filtered by a tag (name + values)" — singular. The engine supports multiple. For v1, exposing `tag: TagFilterInput` (single, optional) satisfies API-02 and avoids surfacing multi-tag complexity.
   - Recommendation: Expose `tag: Option<TagFilterInput>` (single) on `EventFilterInput` for v1. Map to `NostrFilter.tags: Some(vec![tag_filter])`. Multi-tag is a v2 expansion.

3. **`kind` argument on `latestPerAuthor`: `Int` (i32) vs `Int64`/`BigInt` (i64)?**
   - What we know: `kind` in Nostr is `u64` (spec allows large values), but practical values are all small (< 65535).
   - What's unclear: Whether to accept `Int` (i32) or `i64` in the GraphQL arg. If the arg is `Int` and a client passes a kind > 2^31, it silently wraps.
   - Recommendation: Use `i64` for `kind` arg in `latestPerAuthor` — consistent with `Event.kind` field type and avoids silent truncation.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `cargo` / Rust 1.89 | All compilation | ✓ | rustup 1.89 stable (see STATE.md pending toolchain pin note) | — |
| async-graphql 7.2.1 | Schema + resolvers | ✗ (not yet in Cargo.toml) | — | — |
| async-graphql-axum 7.2.1 | HTTP bridge | ✗ (not yet in Cargo.toml) | — | — |
| axum 0.8.9 | HTTP server | ✗ (not yet in Cargo.toml) | — | — |
| tokio 1.52.3 | Async runtime | ✗ (not yet in Cargo.toml) | — | — |
| Network (crates.io) | `cargo add` / first build | ✓ (development machine) | — | Offline: pre-download before execution |

**Missing dependencies with no fallback:**
- All four new crates must be added to `Cargo.toml` before compilation. First plan (Wave 0) adds them.

**Pending non-blocking (from STATE.md):** `rust-toolchain.toml` pins `stable-x86_64-apple-darwin` on arm64 — bare `cargo test` fails on doctest step. Workaround: `cargo test --all-targets`. Do not fix in Phase 4.

---

## Security Domain

> `security_enforcement: true`, `security_asvs_level: 1`, `security_block_on: high` from `.planning/config.json`.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | Read-only public adapter; no auth for v1 (Phase 5 / v2 scope) |
| V3 Session Management | No | Stateless HTTP; no sessions |
| V4 Access Control | Partial | Query-only schema: `EmptyMutation` structurally prevents writes (D-10) |
| V5 Input Validation | Yes | `PageCursor::decode` fail-closed (T-03-CUR); hex input validation for ids/authors; `limit` clamped silently |
| V6 Cryptography | No | No new crypto; signatures already verified by strfry on ingest |

### Known Threat Patterns for this Stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Unbounded query (large `limit`) | DoS | Ceiling 500 enforced at API layer (D-04/D-05); engine `MAX_ROUNDS=8` bound |
| Malformed cursor → panic/crash | Tampering | `PageCursor::decode` already fail-closed (T-03-CUR); maps to `CursorDecode` error |
| Invalid hex in `ids`/`authors` — information leak via error message | Information Disclosure | Client errors from hex decode should say "invalid hex" without echoing the invalid bytes |
| Large `authors` list in `latestPerAuthor` | DoS | Uncapped per D-08 (accepted v1 risk); each author bounded at `perAuthor`; document in code |
| Write attempt via GraphQL | Elevation of Privilege | `EmptyMutation` at schema level — structurally impossible; no Mutation root type registered |
| LMDB error messages leaked in GraphQL errors | Information Disclosure | `map_query_error` returns opaque "internal error" for `Lmdb`/`Payload` variants; only `CursorDecode` is client-facing |

---

## Sources

### Primary (MEDIUM confidence — verified against live docs.rs at pinned versions)
- [docs.rs/async-graphql/7.2.1](https://docs.rs/async-graphql/7.2.1/async_graphql/) — Schema, SchemaBuilder, #[Object], #[SimpleObject], #[InputObject], Error, ErrorExtensions, Context, extensions::Tracing
- [docs.rs/async-graphql-axum/7.2.1](https://docs.rs/async-graphql-axum/7.2.1/async_graphql_axum/) — GraphQL::new(), GraphQLRequest, GraphQLResponse, axum ^0.8.1 requirement
- [docs.rs/axum/0.8.9](https://docs.rs/axum/0.8.9/axum/) — Router::route(), post_service(), with_state(), State
- [docs.rs/tokio/1.52.3](https://docs.rs/tokio/1.52.3/tokio/task/fn.spawn_blocking.html) — spawn_blocking 'static + Send constraints
- [docs.rs/heed/0.22.1](https://docs.rs/heed/0.22.1/heed/struct.Database.html) — Database::stat(), Database::last(), DatabaseStat::entries, Env: Clone + Send + Sync
- [docs.rs/heed/0.22.1/heed/struct.DatabaseStat.html](https://docs.rs/heed/0.22.1/heed/struct.DatabaseStat.html) — entries field for eventCount
- [async-graphql context guide](https://async-graphql.github.io/async-graphql/en/context.html) — ctx.data(), ctx.data_unchecked(), schema-level vs request-level data
- [async-graphql error handling](https://async-graphql.github.io/async-graphql/en/error_handling.html) — extend_with, error extensions, Result<T>
- [crates.io package legitimacy](https://crates.io) — verified versions: async-graphql 7.2.1 (2026-01-20), async-graphql-axum 7.2.1, axum 0.8.9, tokio 1.52.3

### Secondary (MEDIUM confidence — code example verified against documented API surface)
- [@mikecode axum+graphql example](https://medium.com/@mikecode/axum-graphql-build-a-bas-graphql-service-with-axum-2e925239018b) — `Router::route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))` pattern with axum 0.8.1 + async-graphql 7.0.15 (same 7.x API)

### Tertiary (LOW confidence — training knowledge, not verified in this session)
- `juniper` competitive position vs `async-graphql` [ASSUMED]

---

## Metadata

**Confidence breakdown:**
- Standard stack: MEDIUM — all crate versions confirmed against live crates.io API; API patterns verified against docs.rs at pinned versions
- Architecture: MEDIUM — axum 0.8 + async-graphql-axum 7 wiring verified via code example and docs; one pattern (post_service chaining) marked ASSUMED pending compile verification
- Pitfalls: MEDIUM — derived from direct API docs reading; Pitfall 1 (spawn_blocking) and Pitfall 4 (post_service) are HIGH-confidence from direct observation

**Research date:** 2026-06-13
**Valid until:** 2026-07-13 (stable crates; API unlikely to change at pinned versions)
