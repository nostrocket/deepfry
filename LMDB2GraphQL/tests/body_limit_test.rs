//! WR-02-LAYER: integration test for the 256 KiB request body cap on POST /graphql.
//!
//! The body limit (`MAX_REQUEST_BODY_BYTES`) is applied to the axum `Router` via
//! `RequestBodyLimitLayer` (outermost layer). The GraphQL handler is mounted via a custom
//! async handler that checks the `OnceCell<AppSchema>`, so this test confirms the layer
//! actually bites on that path: a POST body over the cap must be rejected (HTTP 413)
//! before it is buffered/parsed, while a small body must be accepted.

use http_body_util::BodyExt;
use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use lmdb2graphql::server::{AppRouterState, build_router};
use std::sync::Arc;
use tokio::sync::OnceCell;
use tower::ServiceExt; // for `oneshot`

/// Copy the committed fixture to a temp dir and open a read-only env there.
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}

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

/// A small, valid GraphQL POST body must be accepted (status < 400).
#[tokio::test]
async fn test_small_body_accepted() {
    let router = make_router();
    let body = r#"{"query":"{ stats { dbVersion } }"}"#;
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .body(axum::body::Body::from(body))
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert!(
        resp.status().is_success() || resp.status().as_u16() == 200,
        "small body must be accepted; got status {}",
        resp.status()
    );
}

/// A POST body larger than the 256 KiB cap must be rejected with 413 Payload Too Large,
/// proving RequestBodyLimitLayer is enforced on the `POST /graphql` path (WR-02-LAYER).
#[tokio::test]
async fn test_oversized_body_rejected() {
    let router = make_router();

    // Build a > 256 KiB JSON document. The exact contents do not matter — the body
    // length must exceed MAX_REQUEST_BODY_BYTES (256 * 1024) before parsing.
    let huge = "a".repeat(300 * 1024);
    let body = format!(r#"{{"query":"{{ stats {{ dbVersion }} }}","padding":"{huge}"}}"#);
    assert!(body.len() > 256 * 1024, "test body must exceed the cap");

    // Set Content-Length so RequestBodyLimitLayer can reject up-front (the realistic client
    // case): a declared length over the cap yields a clean 413 before the body is read.
    let content_length = body.len();
    let req = axum::http::Request::builder()
        .method("POST")
        .uri("/graphql")
        .header("content-type", "application/json")
        .header("content-length", content_length)
        .body(axum::body::Body::from(body))
        .expect("build request");

    let resp = router.oneshot(req).await.expect("router responds");
    assert_eq!(
        resp.status().as_u16(),
        413,
        "oversized body with Content-Length over the cap must be rejected with 413 \
         Payload Too Large (WR-02-LAYER); got {}",
        resp.status()
    );

    // Drain the body to be a well-behaved client (and to surface any panic in collection).
    let _ = resp.into_body().collect().await;
}
