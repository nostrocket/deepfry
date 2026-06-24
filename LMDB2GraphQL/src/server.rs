/// server.rs — axum HTTP router for the lmdb2graphql GraphQL API.
///
/// Provides a single `build_router(state: AppRouterState) -> Router` that serves the
/// entire process lifetime — from the initial bind through startup gates to full readiness.
/// The router gates the data surface behind an `Arc<OnceCell<AppSchema>>`:
///
/// ```text
///   POST /graphql  →  503 (schema cell empty / not ready) OR execute query (schema populated)
///   GET  /graphql  →  graphiql handler → Html(GraphiQLSource)  [always available]
///   GET  /health   →  health_handler → 200 always (liveness — OPS-01)
///   GET  /ready    →  ready_handler  → 200 if ready flag is true, 503 otherwise (OPS-01)
/// ```
///
/// ## Bind-once / zero-gap design (CR-01 / CR-02 fix)
///
/// The listener is bound ONCE before the startup gate chain. A single `axum::serve` call
/// serves this router for the entire process lifetime — no probe server, no graceful-shutdown
/// handshake, no re-bind. The `POST /graphql` handler returns 503 and executes NO query while
/// `AppRouterState::schema` is empty (`OnceCell` not yet populated). After the gate chain
/// completes, `main.rs` populates the cell and flips `ready` to `true`; subsequent POST
/// requests are routed to the schema. This eliminates the connection-refused gap (CR-01)
/// and the ephemeral-port re-bind bug (CR-02) present in the previous probe-shutdown design.
///
/// Security (T-05-05-SC): no Nostr corpus is reachable while the schema cell is empty.
/// A 503 response to POST /graphql executes no query and reads no LMDB data.
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
/// ## Layer ordering note (WR-02-LAYER / CORS-04)
///
/// Layer ordering (outermost → innermost), last `.layer(...)` call = outermost:
///   1. `CorsLayer` — OUTERMOST. Attaches `Access-Control-Allow-Origin: *` (and related
///      CORS headers) to every response on the way out, including 413 and 503, and
///      short-circuits OPTIONS preflight before any inner layer or handler runs.
///      This is why 413/503 responses carry CORS headers (CORS-04) and why preflight
///      is answered correctly even when the schema gate would return 503.
///   2. `RequestBodyLimitLayer` — second-outermost. Enforces the 256 KiB body cap;
///      returns 413 before the body is buffered. Still fires on every non-OPTIONS POST
///      because CorsLayer only short-circuits OPTIONS; other methods pass through.
///
/// In axum 0.8, `.layer(X)` wraps the service built so far. Chaining `.layer(A).layer(B)`
/// makes B the outer wrapper of A. CorsLayer is the last `.layer(...)` call, so it is
/// the outermost — every response (200, 413, 503) travels out through CorsLayer last.
///
/// NOTE: the CORS policy for this endpoint is wildcard-without-credentials by design.
/// The adapter serves public, unauthenticated, read-only Nostr corpus data. No
/// session cookies or tokens are ever set. The CORS layer deliberately never makes the
/// credentials-permitting builder call — omitting it means the browser-restricted
/// credentials header is never emitted (CORS-03), and tower-http additionally panics at
/// construction if that header were combined with a wildcard origin, providing
/// defense-in-depth.
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use async_graphql::http::GraphiQLSource;
use async_graphql_axum::{GraphQLRequest, GraphQLResponse};
use axum::{
    extract::State,
    http::{header::CONTENT_TYPE, Method, StatusCode},
    response::{Html, IntoResponse, Response},
    routing::get,
    Router,
};
use tokio::sync::OnceCell;
use tower_http::cors::{Any, CorsLayer};
use tower_http::limit::RequestBodyLimitLayer;

use crate::graphql::schema::AppSchema;

/// WR-02: cap the request body at 256 KiB. Without this, a client can POST an arbitrarily
/// large query/variables document (e.g. a multi-MB `authors` array or a giant query string),
/// which the server buffers — a trivial memory/CPU amplification vector. A query document
/// large enough to be legitimate does not approach this ceiling.
const MAX_REQUEST_BODY_BYTES: usize = 256 * 1024;

/// Shared state for the single gated router.
///
/// Both fields are `Arc`-wrapped so `AppRouterState` is `Clone` (required by axum's
/// `with_state` — the state is cloned into every handler invocation).
///
/// - `ready`: `AtomicBool` initialized `false` in `main.rs`; flipped `true` ONLY after all
///   startup gates pass (`run_comparator_self_check` succeeds). Readable via `GET /ready`.
/// - `schema`: `OnceCell<AppSchema>` populated ONLY after the gate chain returns `Ok` and
///   BEFORE `ready.store(true)`. `POST /graphql` returns 503 while the cell is empty —
///   no event data is reachable until the schema is present (T-05-05-SC).
#[derive(Clone)]
pub struct AppRouterState {
    pub ready: Arc<AtomicBool>,
    pub schema: Arc<OnceCell<AppSchema>>,
}

/// Build the single axum router for the entire process lifetime (OPS-01 / CR-01 fix).
///
/// Mounts four routes:
/// - `GET  /graphql` → GraphiQL playground (HTML; always available; exposes no data)
/// - `POST /graphql` → gated handler: 503 while schema cell empty; execute query when populated
/// - `GET  /health`  → liveness probe: always 200 (OPS-01, T-05-03)
/// - `GET  /ready`   → readiness probe: 200 if `ready` flag is true, 503 otherwise (OPS-01)
///
/// ## Readiness gate (OPS-01 / T-05-05-SC)
///
/// `state.schema` is a `OnceCell<AppSchema>` that starts empty. The `POST /graphql` handler
/// reads `state.schema.get()`:
/// - `None`  → 503 SERVICE_UNAVAILABLE; no query executed; no LMDB data accessed.
/// - `Some(schema)` → execute the GraphQL request and return the result.
///
/// `main.rs` populates the cell and THEN flips `state.ready` to `true`, so a 200 on `/ready`
/// always implies the schema is present and `/graphql` is queryable.
///
/// ## Layer ordering (WR-02-LAYER / CORS-04)
///
/// `.with_state(state)` injects `AppRouterState` into the router state.
///
/// Layer chain (innermost first in source order, outermost last):
///
/// 1. `.layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))` — enforces the 256 KiB
///    body cap at the Content-Length/body-stream level regardless of extractor, returning
///    413 Payload Too Large before the body is buffered. This is the second-outermost layer.
///
/// 2. `.layer(cors)` — OUTERMOST layer (applied last). The `CorsLayer` attaches CORS headers
///    to every outgoing response, including the 413 produced by RequestBodyLimitLayer and the
///    503 produced by the schema gate, because it wraps the entire stack below it. OPTIONS
///    preflight is short-circuited here before descending to any inner layer or handler,
///    so preflight is answered correctly even when the schema cell is empty (503 state).
pub fn build_router(state: AppRouterState) -> Router {
    let cors = CorsLayer::new()
        .allow_origin(Any)                                            // CORS-01: Access-Control-Allow-Origin: *
        .allow_methods([Method::GET, Method::POST, Method::OPTIONS])  // CORS-02: allowed methods
        .allow_headers([CONTENT_TYPE]);                               // CORS-02: Content-Type (GraphQL-over-HTTP)
        // NOTE: the credentials-permitting builder call is never made — its omission means
        // the browser-restricted credentials header is never emitted (CORS-03). tower-http
        // additionally panics at construction if that header were combined with Any,
        // providing defense-in-depth (T-06-01).

    Router::new()
        .route(
            "/graphql",
            get(graphiql).post(graphql_handler),
        )
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(state)
        // WR-02-LAYER: enforce the body cap with tower-http's RequestBodyLimitLayer rather
        // than axum's DefaultBodyLimit. DefaultBodyLimit relies on the Bytes/String extractors
        // and does NOT bite on the async-graphql `.post_service(...)` Tower-service path — an
        // oversized POST returned 200 OK in tests/body_limit_test.rs. RequestBodyLimitLayer
        // enforces at the Content-Length / body-stream level regardless of extractor, returning
        // 413 Payload Too Large before the body is buffered.
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
        // CORS-04: CorsLayer is the outermost layer so CORS headers appear on 413 (body-cap)
        // and 503 (schema-gate not-ready) responses, and OPTIONS preflight short-circuits
        // before reaching body-limit or the handler.
        .layer(cors)
}

/// GraphiQL playground handler — returns the GraphiQL v2 HTML page (GET /graphql).
///
/// GraphiQL and schema introspection are enabled for this read-only adapter.
/// No credentials or sensitive state are exposed (the adapter only reads strfry LMDB).
/// This handler is NOT gated — the playground HTML contains no corpus data.
async fn graphiql() -> impl IntoResponse {
    Html(GraphiQLSource::build().endpoint("/graphql").finish())
}

/// GraphQL POST handler — gated on schema readiness (POST /graphql, OPS-01 / T-05-05-SC).
///
/// Reads `state.schema.get()`:
/// - `None`  (schema cell empty, gates not yet passed) → 503 SERVICE_UNAVAILABLE.
///   No query is executed and no LMDB data is accessed. This preserves T-05-05-SC:
///   no corpus is reachable while the service is not ready.
/// - `Some(schema)` (gates passed, cell populated) → execute the GraphQL request
///   via async-graphql and return the response.
///
/// The `RequestBodyLimitLayer` (outermost layer) still fires before this handler,
/// so an oversized POST body is rejected with 413 before reaching this logic.
async fn graphql_handler(
    State(state): State<AppRouterState>,
    req: GraphQLRequest,
) -> Response {
    match state.schema.get() {
        None => StatusCode::SERVICE_UNAVAILABLE.into_response(),
        Some(schema) => {
            GraphQLResponse::from(schema.execute(req.into_inner()).await).into_response()
        }
    }
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
/// Reads the `Arc<AtomicBool>` from `AppRouterState` injected via `.with_state(state)`. Returns:
/// - `200 OK` if `state.ready.load(Ordering::Acquire)` is `true` (all startup gates passed).
/// - `503 SERVICE_UNAVAILABLE` if the flag is still `false` (gates not yet completed).
///
/// `Ordering::Acquire` pairs with the `Ordering::Release` in `main.rs` where the flag is
/// set — ensures the flag-set happens-before any subsequent load in another thread.
///
/// Security (T-05-01): exposes only a boolean as an HTTP status code. No internal state,
/// paths, or error text in the response body. ASVS L1 V4 partial.
async fn ready_handler(State(state): State<AppRouterState>) -> StatusCode {
    if state.ready.load(Ordering::Acquire) {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    }
}
