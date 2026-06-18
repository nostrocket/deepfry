# Phase 4: GraphQL API - Pattern Map

**Mapped:** 2026-06-13
**Files analyzed:** 7 new/modified files
**Analogs found:** 7 / 7

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/graphql/mod.rs` | module | — | `src/query/mod.rs` | role-match |
| `src/graphql/types.rs` | model | request-response | `src/lmdb/types.rs` | role-match |
| `src/graphql/schema.rs` | config/factory | request-response | `src/config.rs` | role-match |
| `src/graphql/resolvers.rs` | controller | request-response | `src/query/engine.rs` | role-match |
| `src/server.rs` | middleware/config | request-response | `src/main.rs` | role-match |
| `src/config.rs` | config | — | `src/config.rs` (modify) | exact |
| `src/main.rs` | utility/entrypoint | request-response | `src/main.rs` (modify) | exact |

---

## Pattern Assignments

### `src/graphql/mod.rs` (module)

**Analog:** `src/query/mod.rs`

**Imports/module pattern** (`src/query/mod.rs` lines 1–5):
```rust
pub mod engine;
pub mod filter;
pub mod hydrate;
pub mod merge;
pub mod router;
```

Copy pattern: declare submodules with `pub mod`. `src/graphql/mod.rs` should declare:
```rust
pub mod schema;
pub mod types;
pub mod resolvers;
```

---

### `src/graphql/types.rs` (model, request-response)

**Analog:** `src/lmdb/types.rs`

**Struct definition pattern** (`src/lmdb/types.rs` lines 87–112):
```rust
#[derive(Debug, Clone, serde::Deserialize)]
pub struct NostrEvent {
    pub id: String,
    pub pubkey: String,
    pub created_at: u64,
    pub kind: u64,
    pub tags: Vec<Vec<String>>,
    pub content: String,
    pub sig: String,
}
```

**Output type pattern** (`src/lmdb/types.rs` lines 124–133):
```rust
#[derive(Debug, Clone)]
pub struct DecodedEvent {
    pub event: NostrEvent,
    pub raw_json: Vec<u8>,
}
```

**GraphQL output types** — replace `#[derive(Debug, Clone)]` with `#[derive(SimpleObject)]` from `async_graphql`. Use `i64` for all `u64` fields (Pitfall 2 in RESEARCH.md). Follow the field naming convention from `NostrEvent` but add `raw: String` as the passthrough field (D-01). `tags: Vec<Vec<String>>` maps to `[[String!]!]` automatically (D-02).

**Doc-comment pattern** (`src/lmdb/types.rs` lines 68–86): All public types carry doc comments citing the decision reference (D-XX) they implement.

---

### `src/graphql/schema.rs` (config/factory, request-response)

**Analog:** `src/config.rs`

**Factory function pattern** (`src/config.rs` lines 46–50):
```rust
pub fn load() -> anyhow::Result<Config> {
    let home = dirs::home_dir().context("cannot determine home directory")?;
    let path = home.join("deepfry").join("lmdb2graphql.yaml");
    load_from(&path)
}
```

Apply: `build_schema(app_state: AppState) -> AppSchema` is the factory function for this module. Returns a type alias `AppSchema = Schema<Query, EmptyMutation, EmptySubscription>`.

**Type alias pattern**: Config uses a plain struct. Schema uses `pub type AppSchema = Schema<Query, EmptyMutation, EmptySubscription>;` declared at the top of `schema.rs`.

**AppState struct**: follows `Config` struct pattern — plain `#[derive(Clone)]` struct with `pub` fields. `heed::Env` is `Clone` directly (cheap internal refcount); `DictCache` requires `Arc<DictCache>` because it contains `RwLock` and is not `Clone`. `MetaRecord` is `#[derive(Clone)]` (src/lmdb/types.rs lines 51–66).

---

### `src/graphql/resolvers.rs` (controller, request-response)

**Analog:** `src/query/engine.rs`

**Imports pattern** (`src/query/engine.rs` lines 28–36):
```rust
use crate::lmdb::payload::DictCache;
use crate::lmdb::scan::{scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE};
use crate::lmdb::types::{DecodedEvent, LevId, NostrEvent};
use crate::query::filter::{NostrFilter, PageCursor, QueryError};
use crate::query::hydrate::hydrate_lev_ids;
use crate::query::merge::merge_windowed;
use crate::query::router::{build_start_keys, decode_hex, select_index, SelectedIndex};
use std::collections::HashMap;
use std::time::{SystemTime, UNIX_EPOCH};
```

GraphQL resolver imports add: `async_graphql::{Context, Object, Result, Error, InputObject, SimpleObject}`, `tokio::task`, `std::sync::Arc`.

**Public function signature pattern** (`src/query/engine.rs` lines 122–129):
```rust
pub fn execute_query(
    env: &heed::Env,
    filter: &NostrFilter,
    dict_cache: &DictCache,
    cursor: Option<&PageCursor>,
) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError> {
    execute_query_internal(env, filter, dict_cache, cursor, DEFAULT_WINDOW_SIZE)
}
```

Apply: resolvers are `async fn` methods on `impl Query` under `#[Object]`. Each resolver clones `env` and `Arc::clone(&dict_cache)` from `AppState` retrieved via `ctx.data_unchecked::<AppState>()`, then wraps the engine call in `tokio::task::spawn_blocking(move || { ... })`.

**Error handling pattern** (`src/query/filter.rs` lines 155–171):
```rust
#[derive(Debug, thiserror::Error)]
pub enum QueryError {
    #[error("LMDB error: {0}")]
    Lmdb(#[from] crate::lmdb::indexes::IndexError),

    #[error("Payload error: {0}")]
    Payload(#[from] crate::lmdb::payload::PayloadError),

    #[error("Cursor decode error: {reason}")]
    CursorDecode { reason: String },
}
```

Apply: a `map_query_error(e: QueryError) -> async_graphql::Error` helper function translates variants. `CursorDecode` → client-facing error with `"INVALID_CURSOR"` extension code. `Lmdb` / `Payload` → `tracing::error!` + opaque `"internal error"` (never leak internals).

**Cursor encode/decode pattern** (`src/query/filter.rs` lines 109–139):
```rust
pub fn encode(&self) -> String { /* base64 */ }
pub fn decode(s: &str) -> Result<Self, QueryError> { /* fail-closed */ }
```

Apply directly: `after: Option<String>` resolver arg is decoded via `PageCursor::decode(&s).map_err(map_query_error)?`. `endCursor` is produced via `cursor.map(|c| c.encode())`.

**Limit ceiling enforcement** — resolvers clamp before calling the engine:
```rust
let effective_limit = limit.map(|l| (l.max(1) as usize).min(500)).unwrap_or(100);
```
This is the D-04/D-05 enforcement point. Never pass `0` to `NostrFilter.limit` (triggers engine default 256, not API default 100).

---

### `src/server.rs` (middleware/config, request-response)

**Analog:** `src/main.rs`

**Startup/initialization pattern** (`src/main.rs` lines 14–72): synchronous sequential setup using `anyhow::Context` for error chaining. Each step is `.context("description")?`.

`server.rs` contains `build_router(schema: AppSchema) -> axum::Router` and optionally a `serve(router, bind_addr)` async function. It is not a binary entrypoint; `main.rs` calls it after the gates pass.

**Tracing pattern** (`src/main.rs` lines 17–21):
```rust
tracing_subscriber::fmt()
    .json()
    .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
    .with_writer(std::io::stderr)
    .init();
```

Apply: `tracing::info!` before and after server bind (line 23–26 pattern). Use structured fields: `addr = %listener.local_addr()?`.

---

### `src/config.rs` (config, modify)

**Analog:** `src/config.rs` itself (modify in-place)

**Existing field pattern** (`src/config.rs` lines 13–29):
```rust
#[derive(Debug, Deserialize)]
pub struct Config {
    pub strfry_db_path: PathBuf,
    #[serde(default = "default_map_size")]
    pub map_size: usize,
    pub pinned_strfry_version: String,
    pub pinned_strfry_commit: String,
}

fn default_map_size() -> usize {
    10_995_116_277_760
}
```

**Add `bind_address` field** following the `default_map_size` pattern: add a `default_bind_address` fn returning `"0.0.0.0:8080".to_string()` and add:
```rust
#[serde(default = "default_bind_address")]
pub bind_address: String,
```

The doc comment should note: `/// HTTP bind address for the GraphQL server. Default: 0.0.0.0:8080.`

**Test pattern** (`src/config.rs` lines 64–131): add a test that verifies `bind_address` defaults to `"0.0.0.0:8080"` when omitted from YAML, following the existing `test_map_size_default` and `test_load_from_tempdir` pattern. Tests use `tempfile::tempdir()` — never `~/deepfry/`.

---

### `src/main.rs` (entrypoint, request-response, modify)

**Analog:** `src/main.rs` itself (modify in-place)

**Existing startup gate** (`src/main.rs` lines 11–73): sequential `fn main() -> anyhow::Result<()>` with 6 numbered steps using `.context(...)`. The server phase runs ONLY after all gates pass — append after step 6 (comparator self-check).

**Main function signature stays `fn main()`**: axum requires an async entrypoint. Change to `#[tokio::main] async fn main() -> anyhow::Result<()>`. The existing synchronous calls (`lmdb::env::open_read_only_env`, `lmdb::meta::read_meta`, etc.) are still synchronous and called before the server; they do not need `spawn_blocking` here since main is not yet serving requests.

**Extension pattern** (after line 72 `Ok(())`): replace the `Ok(())` with:
```rust
// 7. Build GraphQL schema and start HTTP server.
let dict_cache = Arc::new(lmdb::payload::DictCache::new());
let app_state = AppState { env: env.clone(), dict_cache, meta: meta.clone() };
let schema = graphql::schema::build_schema(app_state);
let router = server::build_router(schema);
let listener = tokio::net::TcpListener::bind(&cfg.bind_address)
    .await
    .context("bind HTTP listener")?;
tracing::info!(addr = %listener.local_addr()?, "GraphQL server listening");
axum::serve(listener, router).await.context("axum serve")?;
Ok(())
```

**lib.rs extension**: add `pub mod graphql;` and `pub mod server;` following the existing pattern (`src/lib.rs` lines 1–12):
```rust
pub mod lmdb;
pub mod config;
pub mod query;
// add:
pub mod graphql;
pub mod server;
```

---

## Shared Patterns

### Error Handling (`thiserror` in libs / `anyhow` in binaries)

**Source:** `src/query/filter.rs` lines 155–171 and `src/main.rs` line 11
**Apply to:** `src/graphql/resolvers.rs` (uses `async_graphql::Error`), `src/main.rs` (uses `anyhow`)

```rust
// In lib code (resolvers.rs) — translate to async_graphql::Error
fn map_query_error(e: QueryError) -> async_graphql::Error {
    match e {
        QueryError::CursorDecode { reason } => {
            async_graphql::Error::new(format!("invalid cursor: {reason}"))
                .extend_with(|_, ext| ext.set("code", "INVALID_CURSOR"))
        }
        QueryError::Lmdb(inner) => {
            tracing::error!(error = %inner, "LMDB error during query");
            async_graphql::Error::new("internal error")
        }
        QueryError::Payload(inner) => {
            tracing::error!(error = %inner, "payload decode error during query");
            async_graphql::Error::new("internal error")
        }
    }
}
```

### spawn_blocking for Synchronous Engine Calls

**Source:** CLAUDE.md + RESEARCH.md Pattern 4
**Apply to:** Every resolver method in `src/graphql/resolvers.rs` that calls `execute_query` or `latest_per_author`

```rust
let env = state.env.clone();                     // heed::Env is Clone — cheap
let dict_cache = Arc::clone(&state.dict_cache);  // Arc<DictCache>

tokio::task::spawn_blocking(move || {
    crate::query::engine::execute_query(&env, &filter, &*dict_cache, cursor.as_ref())
})
.await
.map_err(|e| async_graphql::Error::new(format!("task join error: {e}")))?
.map_err(map_query_error)
```

Closures must be `'static` — no borrows from the async context. Clone `env` and `Arc::clone` dict_cache before the closure.

### Tracing Pattern

**Source:** `src/main.rs` lines 17–21
**Apply to:** `src/server.rs`, `src/graphql/resolvers.rs`

```rust
tracing::info!(addr = %listener.local_addr()?, "GraphQL server listening");
tracing::error!(error = %inner, "LMDB error during query");
```

Use structured fields with `key = %value` format. Resolvers log opaque internal errors at `error!` level; never surface LMDB error messages to GraphQL response.

### Doc-Comment Convention

**Source:** `src/lmdb/types.rs`, `src/query/filter.rs`, `src/main.rs`
**Apply to:** All new files

All public types and functions carry doc comments citing the decision reference (D-XX), requirement (QRY-XX / API-XX), or security invariant (T-03-XX) they implement. Example pattern (`src/query/filter.rs` lines 1–11):
```rust
/// filter.rs — Engine-facing input/contract types for the Phase-3 query engine.
///
/// - `NostrFilter` / `TagFilter`: structured input from the Phase-4 GraphQL resolvers (D-01..D-04).
/// - `PageCursor`: opaque pagination cursor encoding `(created_at, lev_id)` (D-10, D-11).
/// - `QueryError`: unified error boundary for the engine (T-03-CUR, thiserror house style).
```

### Read-Only LMDB Invariant

**Source:** `src/query/engine.rs` lines 17–20, `src/lmdb/payload.rs` lines 22–29
**Apply to:** `src/graphql/resolvers.rs` (stats resolver), `src/server.rs`

- Never open a write txn. `read_txn()` only, inside `spawn_blocking`.
- `RoTxn` is NOT `Send` — never hold or return one from `spawn_blocking`. Keep it local to the closure.
- Short txns: open → use → drop within the same `spawn_blocking` closure (D-08).

---

## No Analog Found

All Phase-4 files have analogs in the existing codebase. No entries.

---

## Metadata

**Analog search scope:** `src/` (all modules)
**Files scanned:** 18 source files
**Pattern extraction date:** 2026-06-13
