//! Regression tests for the OPS-01 readiness window gap (Plan 05-03, CR-01/CR-02 fix).
//!
//! ## What this file tests
//!
//! The corrected design binds the TCP listener ONCE before the startup gate chain and serves
//! a SINGLE router for the entire process lifetime. The data surface (`POST /graphql`) is gated
//! behind an `Arc<OnceCell<AppSchema>>`: while the cell is empty (gates still running), the
//! handler returns 503 and executes NO query. After the gate chain completes, `main.rs`
//! populates the cell and flips the `AtomicBool` ready flag to `true`.
//!
//! This eliminates the connection-refused gap introduced by the previous "probe-shutdown →
//! re-bind" approach (CR-01): a client polling `/ready` during the startup gate window now
//! receives a continuous stream of 503 responses (not ECONNREFUSED), transitioning to 200
//! exactly once on the same continuously-served socket.
//!
//! ## Acceptance criteria (T-05-06, updated for bind-once design)
//!
//! 1. GET /ready returns 503 while the shared `Arc<AtomicBool>` is `false` (gate window).
//! 2. GET /health returns 200 regardless of the flag state (liveness, OPS-01).
//! 3. POST /graphql returns 503 while the schema cell is empty — no data reachable while
//!    not ready (T-05-05-SC: no corpus reachable while `ready=false`).
//! 4. After `schema_cell.set(schema)` + `ready.store(true, Ordering::Release)` on the SAME
//!    shared Arcs: GET /ready returns 200 (the observable transition the production fix
//!    guarantees on a continuously-served socket).
//!
//! ## Key difference from health_ready_test.rs
//!
//! `health_ready_test.rs` builds `build_router` with a fixed-state `AppRouterState` and
//! verifies handler logic at a given flag value. This file drives state TRANSITIONS through
//! the router surface — asserting both the gate-window (503) and the post-gate (200) states
//! and the /graphql-gated-503 behaviour — using shared `Arc`s that are mutated mid-test.

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Build a minimal AppState + AppSchema from the committed fixture.
/// The fixture env is copied to a tempdir to avoid interfering with other tests.
fn make_schema() -> lmdb2graphql::graphql::schema::AppSchema {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env");
    // Leak the tempdir — the test process is short-lived; leaking avoids the env's
    // backing files vanishing before the env is dropped.
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
// Test 1: GET /ready returns 503 when flag is false (startup gate window)
// ---------------------------------------------------------------------------

/// GET /ready on the gated router must return 503 while `ready=false`.
///
/// This models the startup gate window: the TCP listener is bound and the single
/// router is serving, but the LMDB env open + comparator self-check have not yet
/// completed. An orchestrator polling /ready during this window should see 503
/// with NO connection-refused gap (CR-01 fix — the listener is never torn down).
#[tokio::test]
async fn test_ready_returns_503_when_flag_false() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell = Arc::new(OnceCell::new());
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
        "GET /ready must return 503 on the gated router when ready=false (OPS-01)"
    );
}

// ---------------------------------------------------------------------------
// Test 2: GET /health returns 200 even during the gate window (flag=false)
// ---------------------------------------------------------------------------

/// GET /health on the gated router must return 200 regardless of readiness state.
///
/// Liveness is always 200 — the /health endpoint performs zero work and is independent
/// of the readiness flag (OPS-01, T-05-03).
#[tokio::test]
async fn test_health_returns_200_during_gate_window() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell = Arc::new(OnceCell::new());
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
        "GET /health must return 200 even when ready=false (OPS-01)"
    );
}

// ---------------------------------------------------------------------------
// Test 3: POST /graphql returns 503 while schema cell is empty (no data reachable)
// ---------------------------------------------------------------------------

/// POST /graphql on the gated router must return 503 while the schema cell is empty.
///
/// T-05-05-SC: no Nostr corpus is reachable while the service is not ready. The
/// `POST /graphql` handler checks `state.schema.get()`; an empty cell yields 503
/// and executes NO query. This is security-equivalent to the probe-router design
/// (which returned 404) while eliminating the connection-refused gap (CR-01).
#[tokio::test]
async fn test_graphql_returns_503_when_schema_not_ready() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell: Arc<OnceCell<lmdb2graphql::graphql::schema::AppSchema>> =
        Arc::new(OnceCell::new());
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);

    let req = Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .body(axum::body::Body::from(r#"{"query":"{ stats { dbVersion } }"}"#))
        .expect("build /graphql request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        503,
        "POST /graphql must return 503 when schema cell is empty (T-05-05-SC: no corpus reachable while not ready)"
    );
}

// ---------------------------------------------------------------------------
// Test 4: /ready transitions 503 → 200 after schema set + store(true) on shared Arcs
// ---------------------------------------------------------------------------

/// The 503→200 transition on /ready is observable through the router-served surface.
///
/// This is the gap-closure regression test (T-05-06): it proves that populating the
/// schema cell and flipping the SAME `Arc<AtomicBool>` that was passed to `build_router`
/// causes a subsequent GET /ready to return 200. This is precisely what the production
/// fix enables — `main.rs` binds once and serves one router throughout, so this
/// 503→200 transition is reachable by a real orchestrator with NO connection-refused gap.
#[tokio::test]
async fn test_ready_transitions_503_to_200() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell: Arc<OnceCell<lmdb2graphql::graphql::schema::AppSchema>> =
        Arc::new(OnceCell::new());

    // Build the router once — it holds Arcs pointing to `ready` and `schema_cell`.
    // Router is Clone, so we clone it for each oneshot call while sharing the same Arcs.
    let state = AppRouterState {
        ready: Arc::clone(&ready),
        schema: Arc::clone(&schema_cell),
    };
    let router = build_router(state);

    // Step 1: Assert 503 while flag is false and cell is empty (gate window).
    let req_before = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request (before)");
    let resp_before = router
        .clone()
        .oneshot(req_before)
        .await
        .expect("router responds (before)");
    assert_eq!(
        resp_before.status().as_u16(),
        503,
        "GET /ready must return 503 before schema set + store(true) — gate window (OPS-01)"
    );

    // Step 2: Simulate gate chain completion — populate schema, then flip ready.
    //         populate-before-flip: a 200 /ready always implies /graphql is queryable.
    let schema = make_schema();
    let _ = schema_cell.set(schema);
    ready.store(true, Ordering::Release);

    // Step 3: Assert 200 on the SAME router with the SAME Arcs — the transition is observable.
    let req_after = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request (after)");
    let resp_after = router
        .oneshot(req_after)
        .await
        .expect("router responds (after)");
    assert_eq!(
        resp_after.status().as_u16(),
        200,
        "GET /ready must return 200 after schema set + store(true) on the shared Arcs (OPS-01 gap-closure)"
    );
}
