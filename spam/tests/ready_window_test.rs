//! Regression tests for the OPS-01 readiness window gap (Plan 05-03).
//!
//! ## Why this file exists
//!
//! Phase 05 verification (05-VERIFICATION.md) flagged a BLOCKER gap: the existing probe
//! tests in `health_ready_test.rs` construct `build_router` with a directly-controlled
//! `Arc<AtomicBool>` that is fixed at construction time — they NEVER exercise the flag
//! transitioning from `false` to `true` while the router is already serving. As a result,
//! the production defect (main.rs calling `store(true)` BEFORE `TcpListener::bind`) went
//! undetected: the 503 branch of `ready_handler` was dead code in production, but all
//! unit tests passed.
//!
//! This file closes that blind spot by testing `build_probe_router` — the startup-window
//! surface introduced in Plan 05-03 — and asserting the `false→true` transition through
//! a router-served surface on a SHARED `Arc<AtomicBool>`. It also asserts that the probe
//! surface exposes NO data routes (GET /graphql → 404) while `ready=false`.
//!
//! ## Key difference from health_ready_test.rs
//!
//! `health_ready_test.rs` builds the FULL `build_router` with a fixed flag — correct for
//! testing handler logic, but does not catch ordering defects in `main.rs`. This file
//! targets `build_probe_router` and drives the transition, proving the 503→200 window
//! is observable through a router-served surface rather than via a bare handler call.
//!
//! ## Acceptance criteria (T-05-06)
//!
//! 1. GET /ready on the probe router returns 503 when the shared `Arc<AtomicBool>` is `false`.
//! 2. GET /health on the probe router returns 200 regardless of the flag state.
//! 3. After `store(true, Ordering::Release)` on the SAME `Arc`, GET /ready returns 200.
//!    This is the transition assertion — the flag flip is observable through the router surface.
//! 4. GET /graphql on the probe router returns 404 — the data surface is NOT mounted during
//!    the gate window (T-05-05-SC: no corpus reachable while `ready=false`).

use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use axum::http::Request;
use lmdb2graphql::server::build_probe_router;
use tower::ServiceExt; // for `oneshot`

// ---------------------------------------------------------------------------
// Test 1: GET /ready returns 503 when flag is false (startup gate window)
// ---------------------------------------------------------------------------

/// GET /ready on the probe router must return 503 while `ready=false`.
///
/// This models the startup gate window: the TCP listener is bound and the probe
/// server is serving, but the LMDB env open + comparator self-check have not yet
/// completed. An orchestrator polling /ready during this window should see 503,
/// not 200 (OPS-01 gap-closure — the production fix Plan 05-03 makes this reachable).
#[tokio::test]
async fn test_probe_router_ready_returns_503_when_flag_false() {
    let ready = Arc::new(AtomicBool::new(false));
    let router = build_probe_router(Arc::clone(&ready));

    let req = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        503,
        "GET /ready must return 503 on the probe router when ready=false (OPS-01)"
    );
}

// ---------------------------------------------------------------------------
// Test 2: GET /health returns 200 even during the gate window (flag=false)
// ---------------------------------------------------------------------------

/// GET /health on the probe router must return 200 regardless of readiness state.
///
/// Liveness is always 200 — the /health endpoint performs zero work and is independent
/// of the readiness flag (OPS-01, T-05-03). This must hold during the startup gate
/// window (flag=false) as well as after gates pass (flag=true).
#[tokio::test]
async fn test_probe_router_health_returns_200_during_gate_window() {
    let ready = Arc::new(AtomicBool::new(false));
    let router = build_probe_router(Arc::clone(&ready));

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
// Test 3: GET /ready transitions 503 → 200 after store(true) on the shared Arc
// ---------------------------------------------------------------------------

/// The 503→200 transition on /ready is observable through the router-served surface.
///
/// This is the gap-closure regression test (T-05-06): it proves that flipping the
/// SAME `Arc<AtomicBool>` that was passed to `build_probe_router` causes a subsequent
/// GET /ready to return 200. This is precisely what the production fix enables —
/// `main.rs` now binds the socket and serves the probe router BEFORE calling
/// `store(true)`, so this 503→200 transition is reachable by a real orchestrator.
///
/// Unlike `health_ready_test.rs` which constructs the router with a fixed flag,
/// this test drives the flag transition while the router is already built and
/// asserts both sides of the transition through the router surface.
#[tokio::test]
async fn test_probe_router_ready_transitions_503_to_200() {
    let ready = Arc::new(AtomicBool::new(false));

    // Build the router once — it holds an Arc<AtomicBool> pointing to `ready`.
    // Router is Clone, so we clone it for each oneshot call while sharing the same Arc.
    let router = build_probe_router(Arc::clone(&ready));

    // Step 1: Assert 503 while flag is false (gate window).
    let req_before = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request (before)");
    let resp_before = router.clone().oneshot(req_before).await.expect("router responds (before)");
    assert_eq!(
        resp_before.status().as_u16(),
        503,
        "GET /ready must return 503 before store(true) — gate window (OPS-01)"
    );

    // Step 2: Flip the flag (simulates run_comparator_self_check returning Ok in main.rs).
    ready.store(true, Ordering::Release);

    // Step 3: Assert 200 on the SAME router with the SAME Arc — the transition is observable.
    let req_after = Request::builder()
        .method("GET")
        .uri("/ready")
        .body(axum::body::Body::empty())
        .expect("build /ready request (after)");
    let resp_after = router.oneshot(req_after).await.expect("router responds (after)");
    assert_eq!(
        resp_after.status().as_u16(),
        200,
        "GET /ready must return 200 after store(true) on the shared Arc (OPS-01 gap-closure)"
    );
}

// ---------------------------------------------------------------------------
// Test 4: GET /graphql returns 404 on the probe router (no data surface during gate window)
// ---------------------------------------------------------------------------

/// GET /graphql on the probe router must return 404 — the data surface is NOT mounted.
///
/// The probe router (`build_probe_router`) intentionally omits the /graphql route
/// (T-05-05-SC: no corpus reachable while `ready=false`). Only after gates pass and
/// the full `build_router` is serving does the GraphQL endpoint become reachable.
/// This test asserts that information-disclosure via an early /graphql route is
/// impossible during the startup gate window.
#[tokio::test]
async fn test_probe_router_graphql_returns_404() {
    let ready = Arc::new(AtomicBool::new(false));
    let router = build_probe_router(Arc::clone(&ready));

    let req = Request::builder()
        .method("GET")
        .uri("/graphql")
        .body(axum::body::Body::empty())
        .expect("build /graphql request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        404,
        "GET /graphql on the probe router must return 404 — no data route during gate window (T-05-05-SC)"
    );
}
