/// server.rs — axum HTTP router for the lmdb2graphql GraphQL API.
///
/// Provides:
///   - `build_router(schema, ready)` — mounts `POST /graphql` (GraphQL service),
///     `GET /graphql` (GraphiQL playground), `GET /health` (liveness), and
///     `GET /ready` (readiness) on the same router (Pattern 5 / Pitfall 4 / OPS-01).
///
/// ## Routing
///
/// ```text
///   POST /graphql  →  async-graphql Tower Service (GraphQL::new(schema))
///   GET  /graphql  →  graphiql handler → Html(GraphiQLSource)
///   GET  /health   →  health_handler → 200 always (liveness — OPS-01)
///   GET  /ready    →  ready_handler  → 200 if ready flag is true, 503 otherwise (OPS-01)
/// ```
///
/// ## Pitfall 4 (RESEARCH.md)
///
/// `post_service` is a method on `MethodRouter`, NOT a free function in `axum::routing`.
/// Pattern: `get(handler).post_service(svc)` — chains on `MethodRouter` after `get(...)`.
/// Using `Router::new().route("/graphql", post_service(...))` would fail to compile in axum 0.8.
///
/// ## Read-only surface
///
/// The router has no write routes. No Mutation type in the schema (T-04-WRITE/API-06).
/// GraphiQL and introspection are enabled — this is a read-only adapter; no credentials are at risk.
///
/// ## Layer ordering note (WR-02-LAYER)
///
/// `.layer(RequestBodyLimitLayer::new(...))` must be the OUTERMOST layer (applied after
/// `.with_state()`). In axum 0.8, layers wrap the entire service stack in definition order;
/// placing the body-limit layer last (outermost) ensures it fires before any handler sees the body.
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use async_graphql::http::GraphiQLSource;
use async_graphql_axum::GraphQL;
use axum::{
    extract::State,
    http::StatusCode,
    response::{Html, IntoResponse},
    routing::get,
    Router,
};
use tower_http::limit::RequestBodyLimitLayer;

use crate::graphql::schema::AppSchema;

/// WR-02: cap the request body at 256 KiB. Without this, a client can POST an arbitrarily
/// large query/variables document (e.g. a multi-MB `authors` array or a giant query string),
/// which the server buffers — a trivial memory/CPU amplification vector. A query document
/// large enough to be legitimate does not approach this ceiling.
const MAX_REQUEST_BODY_BYTES: usize = 256 * 1024;

/// Build the axum router for the GraphQL API (Pattern 5 / RESEARCH.md / OPS-01).
///
/// Mounts four routes:
/// - `GET  /graphql` → GraphiQL playground (HTML, for browser use)
/// - `POST /graphql` → GraphQL Tower Service (for programmatic clients)
/// - `GET  /health`  → liveness probe: always 200 (OPS-01, T-05-03)
/// - `GET  /ready`   → readiness probe: 200 if `ready` flag is true, 503 otherwise (OPS-01)
///
/// `post_service` is called on the `MethodRouter` returned by `get(graphiql)` (Pitfall 4 —
/// NOT a free `axum::routing::post_service` function).
///
/// ## Readiness flag (OPS-01 / T-05-04)
///
/// `ready` is an `Arc<AtomicBool>` initialized `false` in `main.rs` and set `true` only
/// after all startup gates pass (`run_comparator_self_check` succeeds). Using `AtomicBool`
/// (not `Mutex<bool>` or `RwLock<bool>`) avoids lock contention on every probe — the readiness
/// state is a single bit that only ever transitions false → true (per RESEARCH anti-patterns).
///
/// ## Layer ordering (WR-02-LAYER)
///
/// `.with_state(ready)` injects the `Arc<AtomicBool>` into the router state.
/// `.layer(RequestBodyLimitLayer::new(...))` is applied AFTER `.with_state()` so it is the
/// outermost layer — enforces the body cap at the Content-Length/body-stream level regardless
/// of extractor, returning 413 Payload Too Large before the body is buffered.
pub fn build_router(schema: AppSchema, ready: Arc<AtomicBool>) -> Router {
    Router::new()
        .route(
            "/graphql",
            get(graphiql).post_service(GraphQL::new(schema)),
        )
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(ready)
        // WR-02-LAYER: enforce the body cap with tower-http's RequestBodyLimitLayer rather
        // than axum's DefaultBodyLimit. DefaultBodyLimit relies on the Bytes/String extractors
        // and does NOT bite on the async-graphql `.post_service(...)` Tower-service path — an
        // oversized POST returned 200 OK in tests/body_limit_test.rs. RequestBodyLimitLayer
        // enforces at the Content-Length / body-stream level regardless of extractor, returning
        // 413 Payload Too Large before the body is buffered.
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}

/// GraphiQL playground handler — returns the GraphiQL v2 HTML page (GET /graphql).
///
/// GraphiQL and schema introspection are enabled for this read-only adapter.
/// No credentials or sensitive state are exposed (the adapter only reads strfry LMDB).
async fn graphiql() -> impl IntoResponse {
    Html(GraphiQLSource::build().endpoint("/graphql").finish())
}

/// Liveness probe handler — always returns 200 OK (GET /health, OPS-01, T-05-03).
///
/// Performs zero work: no LMDB access, no shared state, no allocation. The cheapest
/// possible handler — any process-level hang or crash will prevent a response, which
/// is the correct semantics for a liveness probe (the orchestrator restarts on no-response).
///
/// Security (T-05-03): zero I/O surface — a flood of GET /health requests does almost
/// no work. The existing `RequestBodyLimitLayer` still bounds POST body sizes.
async fn health_handler() -> StatusCode {
    StatusCode::OK
}

/// Readiness probe handler — returns 200 if ready, 503 otherwise (GET /ready, OPS-01, T-05-04).
///
/// Reads the `Arc<AtomicBool>` injected via `.with_state(ready)`. Returns:
/// - `200 OK` if `ready.load(Ordering::Acquire)` is `true` (all startup gates passed).
/// - `503 SERVICE_UNAVAILABLE` if the flag is still `false` (gates not yet completed).
///
/// `Ordering::Acquire` pairs with the `Ordering::Release` in `main.rs` where the flag is
/// set — ensures the flag-set happens-before any subsequent load in another thread.
///
/// Security (T-05-01): exposes only a boolean as an HTTP status code. No internal state,
/// paths, or error text in the response body. ASVS L1 V4 partial.
async fn ready_handler(State(ready): State<Arc<AtomicBool>>) -> StatusCode {
    if ready.load(Ordering::Acquire) {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    }
}
