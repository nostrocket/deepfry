# Phase 6: CORS Support - Pattern Map

**Mapped:** 2026-06-23
**Files analyzed:** 3 (2 modified, 1 created)
**Analogs found:** 3 / 3

## File Classification

| New/Modified File | New/Modified | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|--------------|------|-----------|----------------|---------------|
| `src/server.rs` | modified | config/router (HTTP middleware composition) | request-response | itself — existing `RequestBodyLimitLayer` `.layer(...)` line | exact (in-file precedent) |
| `Cargo.toml` | modified | config (dependency feature flag) | n/a | itself — existing `tower-http = { ..., features = ["limit"] }` line | exact (in-file precedent) |
| `tests/cors_test.rs` | created | test (integration) | request-response | `tests/body_limit_test.rs` + `tests/health_ready_test.rs` | exact |

All three changes have an exact in-repo precedent. There is no "no analog" case in this phase.

## Pattern Assignments

### `src/server.rs` (router middleware composition, request-response) — MODIFY

**Analog:** the existing layer-composition pattern inside the same file's `build_router`.

**Current imports block** (lines 43-60) — the new CORS imports go alongside these. Note the file already imports from `axum::http` (`StatusCode`), confirming Assumption A1 in RESEARCH.md (axum re-exports `http`, so `axum::http::Method` and `axum::http::header::CONTENT_TYPE` need no new crate):
```rust
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use async_graphql::http::GraphiQLSource;
use async_graphql_axum::{GraphQLRequest, GraphQLResponse};
use axum::{
    extract::State,
    http::StatusCode,
    response::{Html, IntoResponse, Response},
    routing::get,
    Router,
};
use tokio::sync::OnceCell;
use tower_http::limit::RequestBodyLimitLayer;
```
Planner: add to the `axum::http` group `Method` and `header::CONTENT_TYPE`, and add `use tower_http::cors::{Any, CorsLayer};` next to the existing `use tower_http::limit::RequestBodyLimitLayer;`.

**Core pattern — current `build_router` layer chain** (lines 108-124). This is the exact insertion site. The existing single `.layer(...)` is the LAST call after `.with_state(state)`, making it outermost:
```rust
pub fn build_router(state: AppRouterState) -> Router {
    Router::new()
        .route(
            "/graphql",
            get(graphiql).post(graphql_handler),
        )
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(state)
        // WR-02-LAYER: ... (comment block, lines 117-122) ...
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}
```
**Where the new line goes (CORS-04 ordering):** Per RESEARCH.md Pattern 2, `CorsLayer` must be the *outermost* layer = the *last* `.layer(...)` call, so it wraps the 413 produced by `RequestBodyLimitLayer`. Append `.layer(cors)` AFTER the existing `.layer(RequestBodyLimitLayer::new(...))` line. The existing body-limit line is unchanged and stays before the CORS line.

**Existing layer-ordering doc precedent** (lines 38-42 and 102-107): the file already documents layer ordering for `RequestBodyLimitLayer` ("must be the OUTERMOST layer (applied after `.with_state()`)... layers wrap the entire service stack in definition order"). The planner should extend this rustdoc to note CORS is now the new outermost layer and why (headers on 413/503).

**Constant pattern** (line 66) — precedent for any module-level config constant, should the planner choose to factor the CORS builder into a `fn cors_layer()` helper or keep config inline:
```rust
const MAX_REQUEST_BODY_BYTES: usize = 256 * 1024;
```

**Recommended CORS layer construction** (from RESEARCH.md Pattern 1, grounded against the real import precedent above):
```rust
let cors = CorsLayer::new()
    .allow_origin(Any)
    .allow_methods([Method::GET, Method::POST, Method::OPTIONS])
    .allow_headers([CONTENT_TYPE]);
    // do NOT call .allow_credentials(true) — omission => no ACAC header (CORS-03)
```

**State struct (no change, but tests construct it directly)** — `AppRouterState` (lines 78-82). The 503 test path in `tests/cors_test.rs` builds this struct by hand with an empty `OnceCell`:
```rust
#[derive(Clone)]
pub struct AppRouterState {
    pub ready: Arc<AtomicBool>,
    pub schema: Arc<OnceCell<AppSchema>>,
}
```

---

### `Cargo.toml` (dependency feature flag) — MODIFY

**Analog:** the existing `tower-http` dependency line itself.

**Current line** (in `[dependencies]`, preceded by a WR-02-LAYER comment block):
```toml
tower-http = { version = "0.6", features = ["limit"] }
```
**Change:** add `"cors"` to the feature array — no version bump (resolves to 0.6.11, which has `CorsLayer`):
```toml
tower-http = { version = "0.6", features = ["limit", "cors"] }
```
No `[dev-dependencies]` change: `tower = { version = "0.5", features = ["util"] }` (for `oneshot`) and `http-body-util = "0.1"` are already present and used by the analog tests.

---

### `tests/cors_test.rs` (integration test, request-response) — CREATE

**Analogs:** `tests/body_limit_test.rs` (fixture + `make_router()` helper + oneshot + status/413 assertions) and `tests/health_ready_test.rs` (direct `AppRouterState` construction with controllable `ready`/empty `OnceCell` for the 503 path).

**Imports pattern** (copy from `body_limit_test.rs` lines 9-17; add `axum::http::Request` from `health_ready_test.rs` line 16 and `AtomicBool` from `health_ready_test.rs` lines 11-14):
```rust
use http_body_util::BodyExt;
use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use lmdb2graphql::server::{AppRouterState, build_router};
use std::sync::{atomic::AtomicBool, Arc};
use tokio::sync::OnceCell;
use tower::ServiceExt; // for `oneshot`
```

**Fixture helper — copy VERBATIM** from `body_limit_test.rs` lines 20-27. This is the canonical fixture-open used across the test suite:
```rust
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}
```

**Ready-router helper — copy VERBATIM** from `body_limit_test.rs` lines 29-52. Populates the schema cell and sets `ready=true`; use for the POST-success, preflight, and 413 tests:
```rust
fn make_router() -> axum::Router {
    let (env, tmp) = open_temp_fixture_env();
    std::mem::forget(tmp); // keep tempdir alive; short-lived test process
    let meta = read_meta(&env).expect("read_meta from fixture");
    let app_state = AppState {
        env,
        dict_cache: Arc::new(DictCache::new()),
        meta,
        pinned_strfry_version: "test-pinned".to_string(),
    };
    let schema = build_schema(app_state);
    let schema_cell = Arc::new(OnceCell::new());
    let _ = schema_cell.set(schema);
    let ready = Arc::new(std::sync::atomic::AtomicBool::new(true));
    let state = AppRouterState { ready, schema: schema_cell };
    build_router(state)
}
```

**Oneshot request + status-assert pattern** (from `body_limit_test.rs` lines 56-72; `Request::builder().method(...).uri(...).header(...).body(...)` then `router.oneshot(req).await`):
```rust
let req = axum::http::Request::builder()
    .method("POST")
    .uri("/graphql")
    .header("content-type", "application/json")
    .body(axum::body::Body::from(body))
    .expect("build request");
let resp = router.oneshot(req).await.expect("router responds");
assert!(resp.status().is_success(), "...; got {}", resp.status());
```

**Header-assertion pattern** — the analog tests only assert on status; CORS tests additionally read response headers. Use `resp.headers().get(...)` / `resp.headers().contains_key(...)` (header names are lowercase ASCII):
```rust
assert_eq!(resp.headers().get("access-control-allow-origin").unwrap(), "*");      // CORS-01
assert!(resp.headers().get("access-control-allow-credentials").is_none());        // CORS-03
assert!(resp.headers().contains_key("access-control-allow-methods"));             // CORS-02
```

**413-with-CORS test** — copy the oversized-body construction VERBATIM from `body_limit_test.rs` lines 77-104 (the `"a".repeat(300 * 1024)` + explicit `content-length` header), then add the ACAO assertion. Keep the existing `let _ = resp.into_body().collect().await;` drain (line 107):
```rust
let huge = "a".repeat(300 * 1024);
let body = format!(r#"{{"query":"{{ stats {{ dbVersion }} }}","padding":"{huge}"}}"#);
let content_length = body.len();
let req = axum::http::Request::builder()
    .method("POST").uri("/graphql")
    .header("content-type", "application/json")
    .header("content-length", content_length)
    .header("origin", "https://app.example")
    .body(axum::body::Body::from(body)).expect("build request");
let resp = router.oneshot(req).await.expect("router responds");
assert_eq!(resp.status().as_u16(), 413, "...");                                   // CORS-04 (gate intact)
assert_eq!(resp.headers().get("access-control-allow-origin").unwrap(), "*");      // CORS-04 (headers present)
let _ = resp.into_body().collect().await;
```

**503-with-CORS test** — follow `health_ready_test.rs` lines 92-99 for direct `AppRouterState` construction with an EMPTY `OnceCell` (do NOT call `make_router()`; do NOT `.set()` the cell). RESEARCH.md note line 288 is explicit on this:
```rust
let ready = Arc::new(AtomicBool::new(false));
let schema_cell: Arc<OnceCell<_>> = Arc::new(OnceCell::new()); // intentionally empty
let state = AppRouterState { ready, schema: schema_cell };
let router = build_router(state);
// POST /graphql -> assert status 503 AND access-control-allow-origin == "*"
```

**Preflight assertion** — assert 2xx via `resp.status().is_success()`, NOT an exact code (tower-http returns 200, not 204; RESEARCH.md Pitfall 4 / Assumption A2).

**Test attribute:** `#[tokio::test]` async fns, matching both analogs.

## Shared Patterns

### Fixture setup (copy strfry LMDB fixture to tempdir, open read-only)
**Source:** `tests/body_limit_test.rs` lines 20-27 (`open_temp_fixture_env`) and the inline copy in `tests/health_ready_test.rs` lines 32-46.
**Apply to:** any test that needs a populated schema (POST-success, preflight, 413).
```rust
let src = std::path::Path::new("tests/fixture");
let tmp = tempfile::tempdir().expect("create tempdir");
std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
std::mem::forget(tmp); // keep tempdir backing files alive for env lifetime
```

### Drive the Router as a tower Service (single request)
**Source:** `tests/body_limit_test.rs` line 17 + 66; `tests/health_ready_test.rs` line 23 + 74.
**Apply to:** every CORS test.
```rust
use tower::ServiceExt; // for `oneshot`
let resp = router.oneshot(req).await.expect("router responds");
```

### Direct AppRouterState construction for gate-state control
**Source:** `tests/health_ready_test.rs` lines 59-66, 93-99, 127-135.
**Apply to:** the 503-not-ready CORS test (empty `OnceCell`, `ready=false`).
```rust
let ready = Arc::new(AtomicBool::new(false));
let schema_cell = Arc::new(OnceCell::new()); // leave empty -> POST /graphql 503s
let state = AppRouterState { ready, schema: schema_cell };
let router = build_router(state);
```

### Layer composition = last `.layer(...)` is outermost
**Source:** `src/server.rs` lines 108-124 (the `RequestBodyLimitLayer` precedent) + rustdoc lines 38-42, 102-107.
**Apply to:** `src/server.rs` — append `.layer(cors)` after the existing body-limit layer so CORS is outermost (CORS-04).

## No Analog Found

None. Every file in this phase has an exact in-repo precedent.

## Metadata

**Analog search scope:** `src/` (server.rs), `tests/` (body_limit_test.rs, health_ready_test.rs), `Cargo.toml`.
**Files scanned:** 4 read in full (server.rs, body_limit_test.rs, health_ready_test.rs, Cargo.toml); tests/ directory listing reviewed (8 test files; the two HTTP-router integration tests are the only relevant analogs — the rest are LMDB/comparator/scan/payload tests with no HTTP surface).
**Pattern extraction date:** 2026-06-23
