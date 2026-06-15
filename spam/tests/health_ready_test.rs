//! Integration tests for the /health and /ready HTTP probes (OPS-01).
//!
//! Verifies the three probe semantics without any LMDB access beyond the schema-build fixture:
//!   1. GET /health always returns 200 (liveness — no LMDB, no state).
//!   2. GET /ready returns 503 when the readiness flag is false.
//!   3. GET /ready returns 200 when the readiness flag is set true.
//!
//! The router is constructed via `build_router(AppRouterState {...})` with a directly-controlled
//! `Arc<AtomicBool>` — health/ready handlers do not touch LMDB state.

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use axum::http::Request;
use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use lmdb2graphql::server::{AppRouterState, build_router};
use tokio::sync::OnceCell;
use tower::ServiceExt; // for `oneshot`

/// Build a minimal AppState from the committed fixture for schema construction.
///
/// Health and ready handlers do not access `AppState` — but `build_router`
/// requires a schema in the `OnceCell` for /graphql access (not needed here,
/// but the fixture env satisfies the type system. The OnceCell can be left
/// empty for health/ready-only tests since those handlers never touch it.
fn make_schema() -> lmdb2graphql::graphql::schema::AppSchema {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env");
    // Keep tempdir alive (test process is short-lived, so leaking is safe).
    std::mem::forget(tmp);
    let meta = read_meta(&env).expect("read_meta from fixture");
    let app_state = AppState {
        env,
        dict_cache: Arc::new(DictCache::new()),
        meta,
        pinned_strfry_version: "test-pinned".to_string(),
    };
    build_schema(app_state)
}

// ---------------------------------------------------------------------------
// Test 1: GET /health always returns 200 (liveness probe, no LMDB access)
// ---------------------------------------------------------------------------

/// GET /health must return 200 regardless of readiness state.
///
/// The health handler performs zero work — it returns `StatusCode::OK` as a
/// constant with no LMDB access and no shared state (OPS-01).
#[tokio::test]
async fn test_health_returns_200() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell = Arc::new(OnceCell::new());
    let _ = schema_cell.set(make_schema());
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);

    let req = Request::builder()
        .method("GET")
        .uri("/health")
        .body(axum::body::Body::empty())
        .expect("build /health request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        200,
        "GET /health must return 200 even when readiness flag is false (OPS-01)"
    );
}

// ---------------------------------------------------------------------------
// Test 2: GET /ready returns 503 when readiness flag is false
// ---------------------------------------------------------------------------

/// GET /ready must return 503 SERVICE_UNAVAILABLE when the readiness flag is false.
///
/// This models the window between process start and startup gate completion
/// (env open + comparator self-check). The flag starts `false` in main.rs and is
/// set `true` only after `run_comparator_self_check` passes (T-05-04 / OPS-01).
#[tokio::test]
async fn test_ready_returns_503_before_flag_set() {
    let ready = Arc::new(AtomicBool::new(false)); // flag NOT set
    let schema_cell = Arc::new(OnceCell::new());
    let _ = schema_cell.set(make_schema());
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);

    let req = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        503,
        "GET /ready must return 503 when readiness flag is false (OPS-01)"
    );
}

// ---------------------------------------------------------------------------
// Test 3: GET /ready returns 200 after readiness flag is set true
// ---------------------------------------------------------------------------

/// GET /ready must return 200 after the readiness flag is set true.
///
/// This models post-startup-gate state: all gates have passed, the `AtomicBool`
/// is `store(true, Ordering::Release)`, and the orchestrator/healthcheck can
/// confirm the service is ready to serve queries (OPS-01).
#[tokio::test]
async fn test_ready_returns_200_after_flag_set() {
    let ready = Arc::new(AtomicBool::new(false));
    // Simulate startup gate completion — set the flag true before serving.
    ready.store(true, Ordering::Release);
    let schema_cell = Arc::new(OnceCell::new());
    let _ = schema_cell.set(make_schema());
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);

    let req = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        200,
        "GET /ready must return 200 after readiness flag is set true (OPS-01)"
    );
}
