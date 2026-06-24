//! CORS-01..CORS-04: integration tests for the wildcard CORS layer on the axum router.
//!
//! The `CorsLayer` is the OUTERMOST layer in `build_router` (applied after
//! `RequestBodyLimitLayer`). These tests drive the router as a tower `Service` via
//! `oneshot` and assert on response headers:
//!   1. POST /graphql carries `Access-Control-Allow-Origin: *` and NO
//!      `Access-Control-Allow-Credentials` header (CORS-01, CORS-03).
//!   2. OPTIONS /graphql preflight returns 2xx with `Access-Control-Allow-Methods`
//!      and `Access-Control-Allow-Headers`, and NO credentials header (CORS-02, CORS-03).
//!   3. An oversized POST still returns 413 (body cap intact) AND carries CORS headers,
//!      proving CorsLayer is outermost (CORS-04).
//!   4. A POST against an empty schema cell still returns 503 (gate intact) AND carries
//!      CORS headers (CORS-04).

use http_body_util::BodyExt;
use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use lmdb2graphql::server::{build_router, AppRouterState};
use std::sync::{atomic::AtomicBool, Arc};
use tokio::sync::OnceCell;
use tower::ServiceExt; // for `oneshot`

// Copied VERBATIM from tests/body_limit_test.rs (the canonical fixture-open helper).
/// Copy the committed fixture to a temp dir and open a read-only env there.
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}

// Copied VERBATIM from tests/body_limit_test.rs (populates schema cell, sets ready=true).
fn make_router() -> axum::Router {
    let (env, tmp) = open_temp_fixture_env();
    // Keep the tempdir alive for the lifetime of the env by leaking it — the test
    // process is short-lived and this avoids the env's backing files vanishing.
    std::mem::forget(tmp);
    let meta = read_meta(&env).expect("read_meta from fixture");
    let app_state = AppState {
        env,
        dict_cache: Arc::new(DictCache::new()),
        meta,
        pinned_strfry_version: "test-pinned".to_string(),
    };
    let schema = build_schema(app_state);
    // Populate the schema cell so POST /graphql routes to the handler (not early 503).
    // The body-limit layer fires BEFORE the handler regardless, so 413 still fires on oversized.
    let schema_cell = Arc::new(OnceCell::new());
    let _ = schema_cell.set(schema);
    let ready = Arc::new(std::sync::atomic::AtomicBool::new(true));
    let state = AppRouterState {
        ready,
        schema: schema_cell,
    };
    build_router(state)
}

/// CORS-01 / CORS-03: a cross-origin POST /graphql carries `Access-Control-Allow-Origin: *`
/// and carries NO `Access-Control-Allow-Credentials` header.
#[tokio::test]
async fn test_post_carries_wildcard_origin() {
    let router = make_router();
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .header("origin", "https://app.example")
        .body(axum::body::Body::from(
            r#"{"query":"{ stats { dbVersion } }"}"#,
        ))
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.headers()
            .get("access-control-allow-origin")
            .expect("ACAO header present"),
        "*",
        "POST response must carry Access-Control-Allow-Origin: * (CORS-01)"
    );
    assert!(
        resp.headers()
            .get("access-control-allow-credentials")
            .is_none(),
        "no Access-Control-Allow-Credentials header on the POST response (CORS-03)"
    );

    // Drain the body to be a well-behaved client.
    let _ = resp.into_body().collect().await;
}

/// CORS-02 / CORS-03: an OPTIONS /graphql preflight is answered 2xx with
/// `Access-Control-Allow-Methods` and `Access-Control-Allow-Headers`, and no credentials header.
#[tokio::test]
async fn test_preflight_options_answered() {
    let router = make_router();
    let req = axum::http::Request::builder()
        .method("OPTIONS")
        .uri("/graphql")
        .header("origin", "https://app.example")
        .header("access-control-request-method", "POST")
        .header("access-control-request-headers", "content-type")
        .body(axum::body::Body::empty())
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    // Do NOT pin an exact code: tower-http returns 200; CORS-02 accepts 200 or 204.
    assert!(
        resp.status().is_success(),
        "preflight must be 2xx (tower-http: 200); got {}",
        resp.status()
    );
    assert!(
        resp.headers().contains_key("access-control-allow-methods"),
        "preflight must carry Access-Control-Allow-Methods (CORS-02)"
    );
    assert!(
        resp.headers().contains_key("access-control-allow-headers"),
        "preflight must carry Access-Control-Allow-Headers (CORS-02)"
    );
    assert!(
        resp.headers()
            .get("access-control-allow-credentials")
            .is_none(),
        "no Access-Control-Allow-Credentials header on the preflight response (CORS-03)"
    );

    let _ = resp.into_body().collect().await;
}

/// CORS-04: an oversized cross-origin POST still returns 413 (body cap intact) AND carries
/// `Access-Control-Allow-Origin: *`, proving CorsLayer is outermost and wraps the 413.
#[tokio::test]
async fn test_cors_headers_on_413() {
    let router = make_router();

    // Oversized-body construction copied VERBATIM from tests/body_limit_test.rs.
    let huge = "a".repeat(300 * 1024);
    let body = format!(r#"{{"query":"{{ stats {{ dbVersion }} }}","padding":"{huge}"}}"#);
    assert!(body.len() > 256 * 1024, "test body must exceed the cap");

    let content_length = body.len();
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .header("content-length", content_length)
        .header("origin", "https://app.example")
        .body(axum::body::Body::from(body))
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        413,
        "oversized body must still be rejected with 413 (body cap intact, CORS-04); got {}",
        resp.status()
    );
    assert_eq!(
        resp.headers()
            .get("access-control-allow-origin")
            .expect("ACAO header present on 413"),
        "*",
        "413 response must carry Access-Control-Allow-Origin: * (CORS-04 — CORS outermost)"
    );

    // Drain the body to be a well-behaved client.
    let _ = resp.into_body().collect().await;
}

/// CORS-04: a POST against an empty schema cell still returns 503 (readiness gate intact)
/// AND carries `Access-Control-Allow-Origin: *`. The router is built by hand here (NOT via
/// make_router()) with an intentionally-empty OnceCell and ready=false.
#[tokio::test]
async fn test_cors_headers_on_503_when_not_ready() {
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell: Arc<OnceCell<_>> = Arc::new(OnceCell::new()); // intentionally empty — do NOT .set()
    let state = AppRouterState {
        ready,
        schema: schema_cell,
    };
    let router = build_router(state);

    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .header("origin", "https://app.example")
        .body(axum::body::Body::from(
            r#"{"query":"{ stats { dbVersion } }"}"#,
        ))
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        503,
        "POST against empty schema cell must return 503 (gate intact, CORS-04); got {}",
        resp.status()
    );
    assert_eq!(
        resp.headers()
            .get("access-control-allow-origin")
            .expect("ACAO header present on 503"),
        "*",
        "503 response must carry Access-Control-Allow-Origin: * (CORS-04 — CORS outermost)"
    );

    let _ = resp.into_body().collect().await;
}
