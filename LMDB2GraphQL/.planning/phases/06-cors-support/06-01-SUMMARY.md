---
phase: 06-cors-support
plan: 01
subsystem: LMDB2GraphQL
tags: [cors, http, axum, tower-http, security]
requires:
  - "src/server.rs build_router (existing axum router + RequestBodyLimitLayer)"
  - "tower-http 0.6.11 (already vendored)"
provides:
  - "Wildcard-without-credentials CORS on POST/GET /graphql"
  - "Auto-answered OPTIONS preflight"
  - "CORS headers on 413 (body cap) and 503 (schema gate) responses"
affects:
  - "src/server.rs"
  - "Cargo.toml"
  - "tests/cors_test.rs"
tech-stack:
  added: []
  patterns:
    - "tower_http::cors::CorsLayer as outermost tower layer"
    - "explicit allow_origin(Any) + allow_methods + allow_headers builder (auditable over permissive())"
key-files:
  created:
    - "tests/cors_test.rs"
  modified:
    - "src/server.rs"
    - "Cargo.toml"
decisions:
  - "CorsLayer applied as the OUTERMOST layer (last .layer call) so CORS headers attach to 413/503 and OPTIONS preflight short-circuits before body-limit/handler (CORS-04 crux)"
  - "Explicit allow_origin(Any).allow_methods([GET,POST,OPTIONS]).allow_headers([CONTENT_TYPE]) chosen over CorsLayer::permissive() for an auditable security surface"
  - "Credentials-permitting builder call never made — keeps Access-Control-Allow-Credentials absent (CORS-03); literal token kept out of src/server.rs to satisfy the Task 1 negative grep gate"
metrics:
  duration: "~6m active (wall-clock spans a transient-overload pause and a day boundary)"
  completed: "2026-06-24"
  tasks: 2
  files: 3
status: complete
---

# Phase 6 Plan 01: CORS Support Summary

Permissive wildcard CORS (no credentials) added to the axum GraphQL server via a single
`tower_http::cors::CorsLayer` placed as the outermost layer in `build_router`, so a
browser frontend on any origin can query `POST /graphql` (and `GET /graphql` for GraphiQL)
cross-origin — with auto-answered OPTIONS preflight and CORS headers preserved on the
existing 413 body-cap and 503 readiness-gate responses.

## What Was Built

### Task 1 — Enable tower-http `cors` feature and add the outermost CorsLayer (commit `4d19965`)
- `Cargo.toml`: `tower-http` feature list changed from `["limit"]` to `["limit", "cors"]`.
  No version bump — `"0.6"` resolves to 0.6.11, which ships `CorsLayer`. No new crate.
- `src/server.rs` imports: added `Method` and `header::CONTENT_TYPE` to the existing
  `axum::http` group, and `use tower_http::cors::{Any, CorsLayer};` next to the existing
  `RequestBodyLimitLayer` import.
- `build_router`: constructs
  `CorsLayer::new().allow_origin(Any).allow_methods([Method::GET, Method::POST, Method::OPTIONS]).allow_headers([CONTENT_TYPE])`
  bound to a `cors` local, and applies it as the last `.layer(cors)` — AFTER the unchanged
  `.layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))`, making CorsLayer the
  outermost layer.
- The credentials-permitting builder call is never made (CORS-03); no manual OPTIONS route
  is added (CorsLayer auto-answers preflight).
- Module-level and `build_router` rustdoc updated to name CorsLayer the new outermost layer
  and explain the 413/503/preflight rationale, without using the literal credentials-header
  token in source.

### Task 2 — `tests/cors_test.rs` integration suite (commit `2924c1b`)
- New file with `open_temp_fixture_env()` and `make_router()` helpers copied verbatim from
  `tests/body_limit_test.rs`, plus four `#[tokio::test]` functions:
  - `test_post_carries_wildcard_origin` — asserts `access-control-allow-origin == *`
    (CORS-01) and `access-control-allow-credentials` absent (CORS-03).
  - `test_preflight_options_answered` — asserts OPTIONS preflight is 2xx
    (`status().is_success()`, not a pinned code), `access-control-allow-methods` +
    `access-control-allow-headers` present (CORS-02), credentials header absent (CORS-03).
  - `test_cors_headers_on_413` — reuses the verbatim oversized-body construction; asserts
    status is exactly 413 (body cap intact) AND `access-control-allow-origin == *` (CORS-04).
  - `test_cors_headers_on_503_when_not_ready` — builds `AppRouterState` by hand with an
    empty `OnceCell` and `ready=false`; asserts status 503 (gate intact) AND
    `access-control-allow-origin == *` (CORS-04).

## Verification

| Step | Command | Result |
|------|---------|--------|
| Build | `cargo build --all-targets` | Compiles; GATE_OK |
| Task 1 gate | feature/layer/CorsLayer greps + `! grep allow_credentials src/server.rs` | GATE_OK |
| CORS tests | `cargo test --all-targets --test cors_test` | 4 passed, 0 failed; GATE_OK |
| Regression guard | `cargo test --all-targets --test body_limit_test --test health_ready_test` | body_limit 2 passed, health_ready 3 passed — 413/503/ready behavior unchanged (CORS-04) |

Note: the build emits one pre-existing `dead_code` warning in `tests/scan_test.rs`
(`kind_reverse_high_key` never used) — out of scope for this plan, not introduced here,
left untouched.

## Requirements Satisfied

- **CORS-01**: cross-origin POST /graphql carries `Access-Control-Allow-Origin: *`
  (`test_post_carries_wildcard_origin`).
- **CORS-02**: OPTIONS /graphql preflight returns 2xx with Allow-Methods + Allow-Headers
  (`test_preflight_options_answered`).
- **CORS-03**: no `Access-Control-Allow-Credentials` header on any response — enforced by
  never making the credentials-permitting call (asserted in POST and preflight tests).
- **CORS-04**: 413 body-cap, 503-until-ready schema gate, and the loopback bind default
  behave as in Phase 5; 413/503 responses carry CORS headers
  (`test_cors_headers_on_413`, `test_cors_headers_on_503_when_not_ready`, and the unchanged
  body_limit/health_ready suites). The loopback bind default in main.rs/config.rs was not
  touched.

## Deviations from Plan

None — plan executed exactly as written. One coordinator-relayed adjustment was applied and
independently verified: the Task 1 negative-grep gate (`! grep -q 'allow_credentials'
src/server.rs`) required the literal token to be absent from the entire file, so the rustdoc
and inline comment were worded to describe the concept ("credentials-permitting builder call")
without the exact substring. This matches the plan's explicit instruction to keep that literal
out of `src/server.rs` (Task 1 action paragraph), so it is not a true deviation.

## Authentication Gates

None — no auth, no package installs (tower-http already vendored; only a feature flag toggled,
so no package-legitimacy checkpoint required).

## Known Stubs

None.

## Threat Flags

None — this plan adds only an HTTP response-header layer. No new network endpoint, auth path,
file-access pattern, or schema change at a trust boundary. The endpoint was already publicly
reachable; CORS relaxes only the browser same-origin policy on already-public, unauthenticated,
read-only data, and never enables credentials. The body-cap (413), the 503 readiness gate, and
the loopback bind default are preserved verbatim.

## Self-Check: PASSED
- FOUND: Cargo.toml (`features = ["limit", "cors"]`)
- FOUND: src/server.rs (CorsLayer outermost; no `allow_credentials` token)
- FOUND: tests/cors_test.rs (4 tests, all pass)
- FOUND commit: 4d19965 (feat — Task 1)
- FOUND commit: 2924c1b (test — Task 2)
