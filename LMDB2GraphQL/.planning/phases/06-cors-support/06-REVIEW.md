---
phase: 06-cors-support
reviewed: 2026-06-24T00:00:00Z
depth: standard
files_reviewed: 3
files_reviewed_list:
  - src/server.rs
  - Cargo.toml
  - tests/cors_test.rs
findings:
  critical: 0
  warning: 2
  info: 3
  total: 5
status: issues_found
---

# Phase 6: Code Review Report

**Reviewed:** 2026-06-24T00:00:00Z
**Depth:** standard
**Files Reviewed:** 3
**Status:** issues_found

## Summary

Reviewed the Phase 6 wildcard CORS layer added to the axum GraphQL server: the new
`CorsLayer` in `build_router` (`src/server.rs`), the `tower-http` `cors` feature
addition (`Cargo.toml`), and the four-test integration suite (`tests/cors_test.rs`).

The implementation is correct and faithfully matches the phase plan. I ran
`cargo test --test cors_test` — all four tests pass, confirming the live response
behavior (wildcard ACAO on 200/413/503, preflight 2xx with ACAM/ACAH, no ACAC). Layer
ordering is right (CorsLayer outermost, RequestBodyLimitLayer second-outermost, schema
gate innermost). The credentials header is correctly never emitted, and the network
controls (loopback bind default, non-loopback warn) in `main.rs` are untouched as
intended.

No BLOCKER-class defects found. The findings below are robustness and
maintainability concerns. The most substantive is WR-01: the `allow_headers([CONTENT_TYPE])`
allowlist is narrower than the de-facto header set real-world GraphQL clients send,
which will cause silent cross-origin preflight failures for some legitimate clients —
a correctness-adjacent gap worth a deliberate decision rather than an accident.

## Warnings

### WR-01: `allow_headers([CONTENT_TYPE])` will reject legitimate cross-origin GraphQL clients that send extra request headers

**File:** `src/server.rs:140`
**Issue:** The preflight allowlist permits only `Content-Type`. Real-world GraphQL
clients and dev tooling routinely send additional request headers cross-origin that the
browser will list in `Access-Control-Request-Headers` during preflight — e.g. Apollo
Client's `apollo-require-preflight`, `x-apollo-operation-name`, `graphql-require-preflight`,
or a generic `Accept` override. When a client includes any header outside `{Content-Type}`,
tower-http's `CorsLayer` will NOT echo it back in `Access-Control-Allow-Headers`, the
browser fails the preflight, and the actual request is silently blocked by the browser
with an opaque CORS error — exactly the frontend-cross-origin scenario this phase exists
to enable. The four tests only exercise the `content-type` case, so this gap is invisible
to the suite. This is a deliberate-decision gap, not a coding bug: if the narrow allowlist
is intended (defense-in-depth on a public read-only endpoint), document the trade-off and
the known-incompatible clients; otherwise widen it.
**Fix:** Either consciously document the restriction, or relax the allowlist. Given the
endpoint is already wildcard-origin, unauthenticated, and read-only, `Any` for headers is
consistent with the existing posture and removes the footgun:
```rust
use tower_http::cors::Any;
let cors = CorsLayer::new()
    .allow_origin(Any)
    .allow_methods([Method::GET, Method::POST, Method::OPTIONS])
    .allow_headers(Any); // public read-only endpoint; no credentials → wildcard headers are consistent
```
If the narrow allowlist is intentional, at minimum add the headers common GraphQL clients
send (e.g. `ACCEPT`, plus the Apollo preflight headers) and add a test asserting a preflight
with a multi-header `access-control-request-headers` is still answered.

### WR-02: No test asserts the `allow_methods` allowlist actually constrains preflight (negative-method coverage gap)

**File:** `tests/cors_test.rs:98-133`
**Issue:** `test_preflight_options_answered` asserts only that `Access-Control-Allow-Methods`
and `Access-Control-Allow-Headers` are *present* (`contains_key`). It never asserts their
*values*. A future refactor that accidentally narrows the method set (e.g. drops `POST`) or
mis-configures the header allowlist would still pass this test, because presence-only
assertions can't detect a wrong-but-present value. Given the whole point of the layer is to
advertise GET/POST/OPTIONS and `Content-Type`, the test should pin those values so the
allowlist is regression-guarded.
**Fix:** Assert the header contents, not just presence:
```rust
let acam = resp.headers().get("access-control-allow-methods").unwrap()
    .to_str().unwrap().to_ascii_uppercase();
assert!(acam.contains("POST") && acam.contains("GET") && acam.contains("OPTIONS"),
    "preflight must advertise GET/POST/OPTIONS; got {acam}");
let acah = resp.headers().get("access-control-allow-headers").unwrap()
    .to_str().unwrap().to_ascii_lowercase();
assert!(acah.contains("content-type"),
    "preflight must advertise content-type; got {acah}");
```

## Info

### IN-01: Tests leak the fixture tempdir on every run (`std::mem::forget`)

**File:** `tests/cors_test.rs:39-41`
**Issue:** `make_router` calls `std::mem::forget(tmp)` to keep the fixture's backing files
alive for the env's lifetime, intentionally leaking the `TempDir` (the comment acknowledges
this). Three of the four tests call `make_router`, so each test run leaves three temp
directories on disk that are never cleaned up. For a short-lived test process this is benign,
but it accumulates under `$TMPDIR` across repeated local runs. This pattern was copied
verbatim from `body_limit_test.rs` per the plan, so it is a pre-existing convention rather
than a new defect — noting it for awareness, not as a Phase 6 regression.
**Fix:** Return the `TempDir` guard from `make_router` and bind it in each test (`let (_router, _tmp) = ...`) so it drops at test end, or store the env+tempdir in a struct whose `Drop` cleans up. Lowest-effort: leave as-is given it matches the existing test convention, but consider fixing the shared helper once.

### IN-02: `503` test relies on `OnceCell` default type inference that could drift

**File:** `tests/cors_test.rs:181`
**Issue:** `let schema_cell: Arc<OnceCell<_>> = Arc::new(OnceCell::new());` uses an inferred
inner type (`_`), resolved only by the later `AppRouterState { schema: schema_cell }` field
assignment. This compiles today, but if `AppRouterState::schema`'s type ever changes the
inference site moves and the failure message becomes obscure. Minor readability/robustness
nit; spelling the type explicitly (`Arc<OnceCell<AppSchema>>`) documents intent at the
construction site.
**Fix:** `let schema_cell: Arc<OnceCell<lmdb2graphql::graphql::schema::AppSchema>> = Arc::new(OnceCell::new());`

### IN-03: Rustdoc duplicates the layer-ordering explanation in two places

**File:** `src/server.rs:38-60` and `src/server.rs:121-135`
**Issue:** The module-level doc block (lines 38-60) and the `build_router` doc block
(lines 121-135) both explain the CorsLayer-outermost / RequestBodyLimitLayer-second
ordering and the 413/503/preflight rationale at length, with substantial overlap. This is
accurate but duplicated — future edits must be kept in sync in two locations or they drift.
Not a defect; a maintainability note.
**Fix:** Keep the detailed explanation on `build_router` (closest to the code) and trim the
module-level block to a one-line pointer (`see build_router for layer ordering`), or vice
versa.

---

_Reviewed: 2026-06-24T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
