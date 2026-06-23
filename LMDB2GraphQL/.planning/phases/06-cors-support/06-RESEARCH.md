# Phase 6: CORS Support - Research

**Researched:** 2026-06-23
**Domain:** HTTP CORS middleware on axum 0.8 / tower-http (Rust)
**Confidence:** HIGH

## Summary

Phase 6 adds CORS support to the existing axum GraphQL server so a browser frontend on any origin can query `POST /graphql` (and `GET /graphql` for GraphiQL) cross-origin. The entire change is **one tower layer** added in `build_router(...)` in `src/server.rs`, plus enabling the `cors` feature on the **already-present** `tower-http 0.6.11` crate. No new crate is added; no new config knob is introduced; no existing protection is touched.

The mechanism is `tower_http::cors::CorsLayer`. It self-handles preflight: when a request arrives with `Method::OPTIONS`, the layer **short-circuits before the inner service runs** and returns a `200 OK` response carrying the CORS headers — meaning preflight is answered correctly even while the `POST /graphql` schema gate would return 503, because preflight never reaches the handler. This is the structural reason CORS-04 is satisfiable without re-ordering the readiness gate. [CITED: github.com/tower-rs/tower-http/blob/main/tower-http/src/cors/mod.rs]

The wildcard-without-credentials policy required by CORS-01/CORS-03 is the **library default**: `allow_origin(Any)` emits `Access-Control-Allow-Origin: *`, and because `allow_credentials` defaults to `false`, the `Access-Control-Allow-Credentials` header is **omitted entirely** (the value is only written when explicitly set true). tower-http additionally `assert!`-panics at construction if you ever combine `allow_credentials(true)` with a wildcard — so the "wildcard + credentials is incompatible" invariant is enforced by the library, not just by us. [CITED: tower-rs/tower-http cors/mod.rs]

**Primary recommendation:** Add a single explicit `CorsLayer` to `build_router` as a layer **inside** (applied before, i.e. listed after) `RequestBodyLimitLayer`, configured `.allow_origin(Any).allow_methods([GET, POST, OPTIONS]).allow_headers([CONTENT_TYPE])` and never calling `allow_credentials`. Enable `tower-http`'s `cors` feature. Add one integration test file following the established `tower::ServiceExt::oneshot` + fixture pattern, asserting: (a) `POST /graphql` carries `Access-Control-Allow-Origin: *`; (b) `OPTIONS /graphql` returns 200 with `Access-Control-Allow-Methods` and `Access-Control-Allow-Headers`; (c) no `Access-Control-Allow-Credentials` header on any response; (d) the body-limit 413 and the 503-until-ready gate still fire and still carry CORS headers.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| CORS header emission | API / Backend (HTTP middleware) | — | CORS is an HTTP-response-header concern; it belongs in the tower layer stack wrapping the axum Router, not in resolvers or the LMDB layer |
| Preflight (OPTIONS) answering | API / Backend (tower middleware) | — | `CorsLayer` intercepts OPTIONS before routing; no axum route or handler is involved |
| Body-size protection | API / Backend | — | Existing `RequestBodyLimitLayer` — unchanged by Phase 6 |
| Readiness/503 gate | API / Backend | — | Existing `OnceCell<AppSchema>` gate in `graphql_handler` — unchanged by Phase 6 |
| Network exposure (bind) | API / Backend (process bind) | — | `bind_address` loopback default in `config.rs`/`main.rs` — CORS does NOT affect this; CORS relaxes browser same-origin policy only, not who can route to the socket |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `tower-http` | 0.6.11 (resolved) | `CorsLayer` middleware | Already a dependency (currently `features = ["limit"]`); the canonical CORS layer for axum/tower; 7.1M weekly downloads, maintained by tower-rs [VERIFIED: crates registry] |
| `axum` | 0.8.9 (resolved) | HTTP router + `axum::http` re-exports (`Method`, `header::CONTENT_TYPE`, `StatusCode`) | Already in use; tower-http 0.6 is the matching CORS layer for axum 0.8 [CITED: docs.rs/tower-http/0.6.11] |

**The only dependency change** is adding `"cors"` to tower-http's feature list. No new crate.

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `tower` | 0.5.3 (dev-dep, resolved) | `ServiceExt::oneshot` | Already a dev-dependency — reuse for the CORS integration test |
| `http-body-util` | 0.1 (dev-dep) | Drain/build bodies in tests | Already a dev-dependency — reuse |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| explicit `CorsLayer::new().allow_origin(Any)...` | `CorsLayer::permissive()` | `permissive()` sets `allow_headers(Any)`, `allow_methods(Any)`, `allow_origin(Any)`, `expose_headers(Any)` — wildcard everything, **no credentials** (so still CORS-03-safe). It is functionally adequate for CORS-01/02/03 but is less explicit/auditable: it allows ALL methods (incl. PUT/DELETE) and ALL headers. The explicit builder documents exactly what GET/POST/OPTIONS + Content-Type is intended. **Recommend the explicit form** for an auditable security surface; `permissive()` is an acceptable fallback. [CITED: tower-rs/tower-http cors/mod.rs] |
| `allow_headers([CONTENT_TYPE])` | `allow_headers(Any)` (emits literal `*`) | `Any` emits `Access-Control-Allow-Headers: *`. With no credentials, `*` is a valid wildcard that covers `Content-Type`. But an explicit `[CONTENT_TYPE]` list is unambiguous, future-proof against the `Authorization`-not-covered-by-`*` rule, and matches CORS-02's "headers a GraphQL-over-HTTP client sends." **Recommend explicit list.** [CITED: tower-rs/tower-http cors/mod.rs] |
| separate `OPTIONS` route | rely on CorsLayer auto-preflight | Registering an OPTIONS route is **unnecessary and would be wrong** — CorsLayer short-circuits OPTIONS before routing; a manual route would never be hit for valid preflight and would risk the handler requiring a body/auth. Do not add one. [CITED: tower-rs/tower-http issue #244, cors/mod.rs] |

**Installation (Cargo.toml change — feature flag only):**
```toml
# BEFORE
tower-http = { version = "0.6", features = ["limit"] }
# AFTER
tower-http = { version = "0.6", features = ["limit", "cors"] }
```

**Version verification:** `cargo tree -i tower-http` → `tower-http v0.6.11`; `cargo metadata` confirms the `cors` feature exists on the resolved crate. No version bump required — `^0.6` already resolves to 0.6.11, which has `CorsLayer`. [VERIFIED: cargo tree / cargo metadata, 2026-06-23]

## Package Legitimacy Audit

> Only one crate is touched, and it is already vendored. Enabling a feature flag adds no new package to the dependency graph beyond tower-http's own `cors`-gated internals (all within tower-http).

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| tower-http | crates.io | first published 2017 | ~7.19M/week | github.com/tower-rs/tower-http | OK | Approved (feature flag only) |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

*No package discovered via WebSearch — tower-http was already in `Cargo.toml`/`Cargo.lock` and is the canonical CORS layer. Verdict OK via `gsd-tools query package-legitimacy check --ecosystem crates tower-http`.*

## Architecture Patterns

### System Architecture Diagram

```text
Browser (origin https://app.example) ──HTTP──> :8080 (bind_address, loopback default)
   │                                              │
   │  (1) preflight: OPTIONS /graphql             ▼
   │      Access-Control-Request-Method: POST   ┌──────────────────────────────────────┐
   │      Access-Control-Request-Headers: ...   │ tower layer stack (outer → inner)     │
   │                                            │                                        │
   │                                            │  RequestBodyLimitLayer (256 KiB)  ◄─ outermost
   │                                            │        │                               │
   │                                            │        ▼                               │
   │                                            │  CorsLayer  ──┐                         │
   │                                            │        │      │ if Method==OPTIONS:     │
   │  (1') ◄── 200 OK + ACAO:* + ACAM + ACAH ───┼────────┘      │  SHORT-CIRCUIT here     │
   │      (preflight answered; inner NOT run)   │               │  (inner never called)   │
   │                                            │        │ else (GET/POST): add ACAO:*    │
   │                                            │        ▼  to response, then call inner   │
   │  (2) actual: POST /graphql {query}         │  axum Router (.with_state)             │
   │      ────────────────────────────────────►│   /graphql  GET→graphiql POST→handler  │
   │                                            │   /health   /ready                     │
   │  (2') ◄── 200 query result + ACAO:*  ───────┤        │                               │
   │      OR 503 (schema cell empty) + ACAO:*   │        ▼ graphql_handler:               │
   │      OR 413 (body cap) + ACAO:*            │          schema.get() None→503 / Some→exec
   │                                            └──────────────────────────────────────┘
   ACAO = Access-Control-Allow-Origin   ACAM = Access-Control-Allow-Methods   ACAH = Access-Control-Allow-Headers
```

Key flows traced: preflight (1→1') is answered by CorsLayer alone; the actual request (2→2') passes the body-limit and CORS layers, then hits the gated handler. CORS headers are attached to the response **on the way back out** through CorsLayer, so they appear on 200, 503, and 413 alike — provided CorsLayer wraps the responses that produce those statuses (see Layer Ordering pattern below).

### Component Responsibilities

| Component | File | Phase 6 change |
|-----------|------|----------------|
| `build_router(state)` | `src/server.rs` | Add `.layer(cors_layer())` (or inline) to the layer chain; add a `cors_layer()` helper or inline builder |
| Cargo features | `Cargo.toml` | `features = ["limit", "cors"]` on tower-http |
| CORS integration test | `tests/cors_test.rs` (new) | New file mirroring `body_limit_test.rs` / `health_ready_test.rs` patterns |
| `main.rs` | `src/main.rs` | **No change** — it calls `build_router` and serves; CORS is internal to the router |
| `config.rs` | `src/config.rs` | **No change** — no new config knob (per STATE.md v1.1 roadmap decision) |

### Pattern 1: Add CorsLayer in build_router (the single integration point)
**What:** Construct the CORS layer and add it to the existing layer chain in `build_router`.
**When to use:** This is the entire implementation.
**Example:**
```rust
// Source: docs.rs/tower-http/0.6.11/tower_http/cors + existing src/server.rs
use axum::http::{header::CONTENT_TYPE, Method};   // axum re-exports the `http` crate; no new dep
use tower_http::cors::{Any, CorsLayer};
use tower_http::limit::RequestBodyLimitLayer;      // existing

pub fn build_router(state: AppRouterState) -> Router {
    let cors = CorsLayer::new()
        .allow_origin(Any)                              // -> Access-Control-Allow-Origin: *  (CORS-01)
        .allow_methods([Method::GET, Method::POST, Method::OPTIONS]) // CORS-02
        .allow_headers([CONTENT_TYPE]);                 // CORS-02 (GraphQL-over-HTTP sends Content-Type)
        // NOTE: do NOT call .allow_credentials(true) — omission means the
        //       Access-Control-Allow-Credentials header is never emitted (CORS-03).

    Router::new()
        .route("/graphql", get(graphiql).post(graphql_handler))
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(state)
        // existing body cap stays OUTERMOST relative to the router/state...
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
        // ...and CORS is applied LAST in source so it is the true outermost layer:
        .layer(cors);
}
```
**Why CORS is the last `.layer(...)` call (outermost):** see the Layer Ordering pattern — this guarantees CORS headers are attached to *every* response, including the 413 produced by the body-limit layer and the 503 produced by the gate.

### Pattern 2: Layer ordering — CORS outermost (the crux of CORS-04)
**What:** In axum 0.8, `.layer(X)` wraps the entire service built *so far*. Chained `.layer(A).layer(B)` makes **B the outer wrapper of A** (B is applied last, so requests hit B first and responses leave through B last). To make CORS headers appear on responses generated by inner layers/handlers (413, 503), CORS must be the **outermost** layer = the **last** `.layer(...)` call.
**When to use:** Always, for CORS over a stack that can reject early.
**Reasoning (decision matrix):**

| Concern | If CorsLayer is OUTERMOST (last `.layer`) | If CorsLayer is INNER (before body-limit) |
|---------|-------------------------------------------|-------------------------------------------|
| CORS headers on 200 query result | ✅ yes | ✅ yes |
| CORS headers on 503 (schema gate) | ✅ yes — response passes back out through CORS | ✅ yes (gate is in the handler, inside both) |
| CORS headers on 413 (body cap) | ✅ yes — 413 produced by RequestBodyLimitLayer is wrapped by CORS | ❌ **no** — body-limit rejects *outside* CORS; the 413 never passes through CorsLayer, so a browser sees a CORS error instead of a clean 413 |
| Preflight answered while not ready | ✅ yes — OPTIONS short-circuits in CorsLayer before reaching body-limit or handler | ✅ yes (OPTIONS still short-circuits) but a malformed-length OPTIONS could be rejected by body-limit first |
| Body cap still enforced | ✅ yes — RequestBodyLimitLayer unchanged, still fires on POST | ✅ yes |
| 503 gate still enforced | ✅ yes — handler logic unchanged | ✅ yes |

**Conclusion:** Put CorsLayer **outermost** (last `.layer(...)`). This is the only ordering that satisfies CORS-04 fully: preflight answered during 503, AND CORS headers present on body-limit-rejected (413) and 503 responses. [CITED: docs.rs/axum/0.8 Router::layer ordering semantics; tower-rs/tower-http cors/mod.rs preflight short-circuit]

> **Note on preflight vs. body-limit:** Even with CORS outermost, OPTIONS short-circuits inside CorsLayer and never descends to the body-limit layer — so a preflight is never at risk of a 413. The body-limit only matters for the actual POST, which is exactly where you want it.

### Anti-Patterns to Avoid
- **Registering a manual `OPTIONS /graphql` route.** CorsLayer answers preflight by short-circuit; a manual route is dead code for valid preflight and can break it if the handler needs a body/auth. Don't.
- **Calling `.allow_credentials(true)` with `Any`.** tower-http `assert!`-panics at layer construction ("Cannot combine `Access-Control-Allow-Credentials: true` with `Access-Control-Allow-Origin: *`"). This would crash the server at startup. Never set credentials here. [CITED: tower-rs/tower-http cors/mod.rs]
- **Putting CorsLayer *inside* the body-limit layer** (before it in the chain). Loses CORS headers on 413 responses (see decision matrix). 
- **Adding a `bind_address`-style "allow widening" config knob for origins.** STATE.md v1.1 decision: no new config knob; static wildcard policy. Don't introduce one.
- **Treating CORS as a network-exposure control.** CORS only relaxes the browser same-origin policy; it does NOT change who can reach the socket. The `bind_address` loopback default and the non-loopback warning in `main.rs` are the network controls and remain unchanged (CORS-04).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Emitting `Access-Control-*` headers | Manual header-insertion middleware | `tower_http::cors::CorsLayer` | Correct preflight short-circuit, header mirroring/wildcard rules, and the credentials/wildcard safety assertion are subtle and spec-bound |
| Answering OPTIONS preflight | A hand-written `OPTIONS` handler | CorsLayer auto-preflight | The layer reads `Access-Control-Request-Method/Headers` and responds 200 with the right headers before routing |
| Validating origins | Custom allow-list logic | (not needed) `allow_origin(Any)` | Corpus is public Nostr data; wildcard is the chosen policy (CORS-01) |

**Key insight:** CORS is almost entirely a "configure a known-correct middleware" task. The only real engineering decisions are (1) explicit vs. permissive policy and (2) layer ordering — both resolved above.

## Common Pitfalls

### Pitfall 1: CORS headers missing on 413 / 503 because of layer order
**What goes wrong:** A browser hitting an oversized POST or a not-yet-ready server sees a generic "CORS policy" failure in devtools instead of a clean 413/503, because the rejecting layer sat outside CorsLayer and the error response carried no `Access-Control-Allow-Origin`.
**Why it happens:** `.layer()` ordering — the last `.layer` is outermost; an error produced by an outer layer never traverses an inner CorsLayer.
**How to avoid:** Make CorsLayer the **last** `.layer(...)` call (outermost). Add a test asserting `Access-Control-Allow-Origin: *` is present on both the 413 and 503 responses.
**Warning signs:** Browser console "No 'Access-Control-Allow-Origin' header" on error responses while success responses work.

### Pitfall 2: Startup panic from credentials + wildcard
**What goes wrong:** Server panics at `build_router` time with "Cannot combine `Access-Control-Allow-Credentials: true` with `Access-Control-Allow-Origin: *`".
**Why it happens:** Someone adds `.allow_credentials(true)` (e.g., copied from a tutorial) alongside `allow_origin(Any)`. tower-http asserts this is invalid.
**How to avoid:** Never call `allow_credentials` in this phase. CORS-03 explicitly forbids the credentials header.
**Warning signs:** Process exits during startup before listening; panic message mentions CORS configuration.

### Pitfall 3: `Access-Control-Allow-Headers: *` does not cover `Authorization` (and credentialed `*` is invalid)
**What goes wrong:** Using `allow_headers(Any)` emits a literal `*`. Per the Fetch spec, `*` in Allow-Headers does NOT match `Authorization`, and a `*` wildcard is ignored entirely if the request is credentialed.
**Why it happens:** `Any` is convenient but its wildcard semantics have carve-outs.
**How to avoid:** This API is unauthenticated (no `Authorization`), so `*` would actually work — but use an explicit `[CONTENT_TYPE]` list anyway for clarity and to match exactly what a GraphQL-over-HTTP client sends. [CITED: tower-rs/tower-http cors/mod.rs; MDN Fetch CORS]
**Warning signs:** Future auth additions silently break preflight.

### Pitfall 4: Asserting the wrong preflight status (204 vs 200)
**What goes wrong:** A test asserts `204` for the OPTIONS response and fails.
**Why it happens:** Many CORS implementations use 204; tower-http's `CorsLayer` builds the preflight response with `Response::new(B::default())`, which is **200 OK**.
**How to avoid:** Assert the preflight status is in 2xx (CORS-02 says "200 or 204"); for tower-http specifically it is **200**. [CITED: tower-rs/tower-http cors/mod.rs]
**Warning signs:** Flaky/failing preflight test pinned to an exact non-200 code.

## Runtime State Inventory

> Not a rename/refactor/migration phase — this is an additive HTTP-middleware change. No stored data, live-service config, OS-registered state, secrets/env vars, or build artifacts encode any value affected by adding a CORS layer.
> **None — verified:** no string-replacement, no datastore keys, no external service config, no env/secret names, and no build artifacts are touched. The only changes are `src/server.rs`, `Cargo.toml` (feature flag), and a new test file.

## Code Examples

### CORS integration test (mirrors existing `body_limit_test.rs` / `health_ready_test.rs`)
```rust
// Source: existing tests/body_limit_test.rs + tests/health_ready_test.rs patterns
use axum::http::Request;
use http_body_util::BodyExt;
use lmdb2graphql::graphql::schema::{build_schema, AppState};
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::meta::read_meta;
use lmdb2graphql::lmdb::payload::DictCache;
use lmdb2graphql::server::{build_router, AppRouterState};
use std::sync::{atomic::AtomicBool, Arc};
use tokio::sync::OnceCell;
use tower::ServiceExt; // oneshot

// (reuse the open_temp_fixture_env + make_router helpers verbatim from body_limit_test.rs)

#[tokio::test]
async fn test_post_carries_wildcard_origin() {
    let router = make_router(); // schema cell populated, ready=true
    let req = Request::builder()
        .method("POST").uri("/graphql")
        .header("content-type", "application/json")
        .header("origin", "https://app.example")
        .body(axum::body::Body::from(r#"{"query":"{ stats { dbVersion } }"}"#))
        .unwrap();
    let resp = router.oneshot(req).await.unwrap();
    assert_eq!(resp.headers().get("access-control-allow-origin").unwrap(), "*"); // CORS-01
    assert!(resp.headers().get("access-control-allow-credentials").is_none());   // CORS-03
}

#[tokio::test]
async fn test_preflight_options_answered() {
    let router = make_router();
    let req = Request::builder()
        .method("OPTIONS").uri("/graphql")
        .header("origin", "https://app.example")
        .header("access-control-request-method", "POST")
        .header("access-control-request-headers", "content-type")
        .body(axum::body::Body::empty())
        .unwrap();
    let resp = router.oneshot(req).await.unwrap();
    assert!(resp.status().is_success(), "preflight must be 2xx (tower-http: 200)"); // CORS-02
    assert!(resp.headers().contains_key("access-control-allow-methods"));            // CORS-02
    assert!(resp.headers().contains_key("access-control-allow-headers"));            // CORS-02
    assert!(resp.headers().get("access-control-allow-credentials").is_none());       // CORS-03
}

#[tokio::test]
async fn test_cors_headers_on_413() {
    // Reuse the oversized-body construction from body_limit_test.rs; assert BOTH
    // status == 413 (body cap still bites, CORS-04) AND ACAO:* present (CORS-04 ordering).
    // ...
}

#[tokio::test]
async fn test_cors_headers_on_503_when_not_ready() {
    // Build state with an EMPTY schema OnceCell (do not .set()) and ready=false.
    // POST /graphql must return 503 (gate still fires, CORS-04) AND carry ACAO:*.
    let ready = Arc::new(AtomicBool::new(false));
    let schema_cell: Arc<OnceCell<_>> = Arc::new(OnceCell::new()); // intentionally empty
    let state = AppRouterState { ready, schema: schema_cell };
    let router = build_router(state);
    let req = Request::builder()
        .method("POST").uri("/graphql")
        .header("content-type", "application/json")
        .header("origin", "https://app.example")
        .body(axum::body::Body::from(r#"{"query":"{ stats { dbVersion } }"}"#))
        .unwrap();
    let resp = router.oneshot(req).await.unwrap();
    assert_eq!(resp.status().as_u16(), 503);                                        // CORS-04 (gate intact)
    assert_eq!(resp.headers().get("access-control-allow-origin").unwrap(), "*");    // CORS-04 (headers present)
    let _ = resp.into_body().collect().await;
}
```
**Note:** the existing `make_router()` helper in `body_limit_test.rs` populates the schema cell and sets `ready=true`; reuse it for the success/413/preflight tests. For the 503 test, build `AppRouterState` directly with an empty `OnceCell` (as shown) — do NOT call `make_router()`.

### Cargo.toml feature flag
```toml
# Source: existing Cargo.toml line 47
tower-http = { version = "0.6", features = ["limit", "cors"] }
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand-rolled CORS header middleware / manual OPTIONS handler | `tower_http::cors::CorsLayer` with auto-preflight | tower-http stable | Less code, spec-correct preflight, credentials/wildcard safety assertion |
| `allow_credentials(true)` with `*` (silently broken in browsers) | Library `assert!` panics on this combo | tower-http enforces | Fail-fast at startup instead of silent runtime CORS failures |

**Deprecated/outdated:**
- Nothing to deprecate; this is a net-new additive layer.

## Assumptions Log

> Every load-bearing claim was confirmed against the official tower-http source on GitHub, the resolved Cargo.lock, or the local codebase. The few items below are the only non-fully-verified points.

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `axum::http::header::CONTENT_TYPE` and `axum::http::Method` are importable without adding the `http` crate directly | Standard Stack / Pattern 1 | LOW — axum re-exports `http` (server.rs already imports `axum::http::StatusCode`); if a path differs, use `tower_http::cors::...` re-exports or `axum::http::Method`. Trivially confirmed at compile time. |
| A2 | tower-http preflight response status is exactly `200` (not 204) | Pitfall 4 | LOW — CORS-02 accepts "200 or 204"; tests should assert `is_success()` not an exact code, so even if the library changes to 204 the requirement still holds. Source shows `Response::new(...)` = 200. |

**Most-impactful note:** CORS-04's layer-ordering requirement (CORS outermost) is the single decision a planner must lock. It is CITED from axum/tower-http semantics, not assumed.

## Open Questions (RESOLVED)

1. **Should GraphiQL (`GET /graphql`) need CORS at all?**
   - What we know: GraphiQL is served same-origin (the browser loads the HTML from this server, then issues same-origin fetches). Cross-origin browser clients are separate SPAs.
   - What's unclear: nothing blocking — `allow_methods` includes GET so GraphiQL fetches from a *different* origin also work. CORS-01 explicitly mentions `GET /graphql` for GraphiQL.
   - **RESOLVED:** include GET in `allow_methods` (already in the recommended config). No further action.

2. **Explicit `[CONTENT_TYPE]` vs `permissive()`?**
   - What we know: both satisfy CORS-01/02/03. Explicit is more auditable; `permissive()` is one line.
   - **RESOLVED:** use the explicit `[CONTENT_TYPE]` builder (recommended above; adopted by the plan in Task 1). This was a "Claude's discretion"-style call since no CONTEXT.md exists; the requirements are met either way and explicit was chosen for auditability.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `tower-http` crate (`cors` feature) | CorsLayer | ✓ | 0.6.11 resolved; `cors` feature present | — |
| `cargo` toolchain | build/test | ✓ | (see STATE.md toolchain caveat below) | use `cargo test --all-targets` |

**Missing dependencies with no fallback:** none — everything is already vendored.

**Toolchain caveat (from STATE.md Pending Todos):** `rust-toolchain.toml` pins `stable-x86_64-apple-darwin` on an arm64 host; stale system `rustdoc`/`clippy-driver` shadow the rustup toolchain so bare `cargo test`/`cargo clippy` can fail on the doctest/build-script step. **Workaround:** run `cargo test --all-targets` (skips doctests). The plan's verification steps should use `cargo test --all-targets` to run the new CORS integration tests, consistent with prior phases.

## Security Domain

> `security_enforcement: true`, ASVS level 1 (from `.planning/config.json`).

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | API is intentionally unauthenticated (public read-only Nostr corpus); no credentials surface — this is *why* wildcard CORS is acceptable |
| V3 Session Management | no | No sessions/cookies; `Access-Control-Allow-Credentials` deliberately absent (CORS-03) |
| V4 Access Control | partial | Network exposure is governed by `bind_address` loopback default (unchanged); CORS does NOT grant network access, only relaxes browser same-origin policy |
| V5 Input Validation | yes (unchanged) | `RequestBodyLimitLayer` (256 KiB) and async-graphql complexity/depth limits remain in force; CORS adds no new input path |
| V6 Cryptography | no | No crypto introduced |
| V13 API & Web Service | yes | CORS is configured via the vetted `CorsLayer`; preflight handled correctly; no wildcard-with-credentials (library-asserted) |

### Known Threat Patterns for axum/tower CORS

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Wildcard origin + credentials (cookie/token theft across origins) | Information Disclosure | Never set `allow_credentials`; tower-http `assert!`-panics on the unsafe combo (defense-in-depth). CORS-03. |
| CORS misperceived as a network-access control → operator binds `0.0.0.0` thinking CORS limits exposure | Elevation of Privilege / Info Disclosure | Keep `bind_address` loopback default + non-loopback `tracing::warn!` in `main.rs` (CR-01, unchanged). Document that CORS ≠ network ACL (CORS-04). |
| Preflight reaching a body/auth-requiring handler and failing | Denial of Service (functional) | CorsLayer short-circuits OPTIONS before routing — no manual OPTIONS handler added. |
| Oversized cross-origin POST | Denial of Service | `RequestBodyLimitLayer` 413 unchanged; CORS layered outside so the 413 still carries CORS headers (clean rejection). CORS-04. |

**Net security delta of Phase 6:** CORS relaxes the *browser's* same-origin policy for an already-public, unauthenticated, read-only endpoint. It does not widen the network attack surface (the socket was already reachable by anything that could route to `bind_address`). No new secrets, auth, or write paths. The existing body-cap, 503 gate, and loopback-bind controls are preserved verbatim (CORS-04).

## Sources

### Primary (HIGH confidence)
- tower-rs/tower-http `cors/mod.rs` (official source, `main`) — preset definitions (`permissive`/`very_permissive`), preflight short-circuit on `Method::OPTIONS`, `Response::new` (200), credentials/wildcard `assert!`, default-false credentials header omission. https://github.com/tower-rs/tower-http/blob/main/tower-http/src/cors/mod.rs
- docs.rs/tower-http/0.6.11/tower_http/cors — `CorsLayer` builder API (`allow_origin`, `allow_methods`, `allow_headers`, `allow_credentials`; `Any`).
- Local codebase: `src/server.rs` (build_router, existing layer chain), `src/main.rs` (bind-once serve, loopback warn), `src/config.rs` (bind_address loopback default), `tests/body_limit_test.rs` + `tests/health_ready_test.rs` (oneshot test pattern), `Cargo.toml` (tower-http 0.6 `limit`), `Cargo.lock` (tower-http 0.6.11, http 1.4.2, tower 0.5.3) — all read this session.
- `cargo tree -i tower-http` → 0.6.11; `cargo metadata` confirms `cors` feature; `gsd-tools query package-legitimacy check` → OK.

### Secondary (MEDIUM confidence)
- tower-rs/tower-http issue #244 (preflight origin behavior with `Any`) — confirms auto-preflight semantics.
- MDN Fetch / CORS — `Access-Control-Allow-Headers: *` carve-outs (Authorization; credentialed wildcard ignored).

### Tertiary (LOW confidence)
- None relied upon for load-bearing claims.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — crate already resolved (Cargo.lock), feature confirmed via cargo metadata, legitimacy OK.
- Architecture / layer ordering: HIGH — confirmed against axum layer semantics and tower-http source; the ordering decision is the documented crux for CORS-04.
- Pitfalls: HIGH — credentials/wildcard panic, preflight 200, and `*`-header carve-outs are quoted from official source/spec.

**Research date:** 2026-06-23
**Valid until:** 2026-07-23 (stable crate, pinned major; revisit if tower-http 0.7 is adopted)
