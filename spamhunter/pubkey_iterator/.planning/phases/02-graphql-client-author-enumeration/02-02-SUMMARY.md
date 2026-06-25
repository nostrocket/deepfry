---
phase: 02-graphql-client-author-enumeration
plan: 02
subsystem: api
tags: [graphql, reqwest, tokio, thiserror, serde, http-client, error-handling]

# Dependency graph
requires:
  - phase: 01-foundation
    provides: serde/serde_json derive idiom + the lib.rs/store mod-registration pattern mirrored here
provides:
  - "GraphQlClient: reusable async GraphQL-over-HTTP transport with an injectable endpoint field and a generic query<T>"
  - "Two-layer ClientError taxonomy (Unavailable/PayloadTooLarge/Transport/Graphql) mapping contract §7 to matchable typed errors"
  - "GraphQlResponse<T> envelope that surfaces in-body errors[] before trusting data (D-08)"
  - "AUTHORS_QUERY/STATS_QUERY consts + AuthorsPage/AuthorsData/StatsResult/StatsData serde structs"
  - "authors()/stats() thin typed wrappers (D-11 additive seam for Phase 3's latestPerAuthor)"
affects: [enumerate, plan-02-03, phase-03-fetch-pipeline]

# Tech tracking
tech-stack:
  added: ["reqwest 0.13 (json/charset/http2, no TLS)", "tokio 1 (rt-multi-thread/macros/time)", "thiserror 2"]
  patterns:
    - "Two-layer error dispatch: HTTP transport status (503/413) checked before body errors[]; errors[] checked before data"
    - "Injectable endpoint as a struct field (no hardcoded URL) so tests point the client at a loopback stub"
    - "Hand-written query consts + serde structs (D-10, no introspection codegen)"
    - "Generic query<T> with thin typed wrappers as the D-11 additive transport seam"

key-files:
  created:
    - src/graphql/mod.rs
    - src/graphql/envelope.rs
    - src/graphql/queries.rs
    - src/graphql/client.rs
  modified:
    - Cargo.toml
    - Cargo.lock
    - src/lib.rs

key-decisions:
  - "Hand-rolled std::net loopback stub server for client tests instead of adding a wiremock dev-dep (fail-fast / owning-phase dep discipline; a single canned response per test suffices)"
  - "thiserror adopted for the ClientError enum (RESEARCH-recommended) — keeps the two-layer mapping explicit and gives Display impls that omit the response body (T-02-04)"
  - "No TLS feature on reqwest — adapter default-binds loopback plain HTTP (contract §10); rustls-tls is a Phase-4 concern"

patterns-established:
  - "GraphQL transport seam: query<T> generic + authors()/stats() wrappers; Phase 3 adds latest_per_author additively (new const + struct + wrapper, no transport change)"
  - "Envelope-first decode: GraphQlResponse<T> with #[serde(default)] errors so {data}, {data:null,errors}, and {data,errors} all parse"

requirements-completed: [INGEST-01, INGEST-04]

# Metrics
duration: 14min
completed: 2026-06-25
status: complete
---

# Phase 02 Plan 02: GraphQL Client Module Summary

**Hand-written async `GraphQlClient` (reqwest/tokio) with an injectable endpoint, a generic `query<T>` that surfaces in-body `errors[]` before trusting `data`, 503/413 transport mapping, and `authors`/`stats` typed wrappers — all unit-tested against a hand-rolled loopback stub and verified end-to-end against the live v1.2 adapter.**

## Performance

- **Duration:** 14 min
- **Started:** 2026-06-25T22:20Z (approx)
- **Completed:** 2026-06-25T14:34:32Z (UTC)
- **Tasks:** 2
- **Files modified:** 7 (4 created, 3 modified)

## Accomplishments
- Added the Phase-2-owned transport deps (reqwest 0.13 no-TLS, tokio 1 with `time` for plan-03's backoff, thiserror 2) in their owning phase; `rayon`/fetch-pipeline deps deliberately stay out for Phase 3.
- Built the `src/graphql/` module: the generic `{data, errors}` envelope, the hand-written `AUTHORS_QUERY`/`STATS_QUERY` documents + their camelCase serde response structs, and the async `GraphQlClient` with the two-layer error dispatch.
- Two-layer error handling (criterion 3 / INGEST-04 / D-08): HTTP `503 → Unavailable` (retryable), `413 → PayloadTooLarge` (non-retryable), in-body `errors[]`/`extensions.code` parsed and surfaced as `ClientError::Graphql` **before** `data` is ever read.
- Endpoint injectability: `GraphQlClient { http, endpoint }` takes the URL as a field — no hardcoded URL in client code; tests construct the client from an ephemeral loopback stub's URL.
- Connectivity proof: the hand-written query consts + structs deserialize the **real** v1.2 adapter response (`stats.maxLevId == 47928105`; `authors` returns 64-char hex pubkeys + `hasMore`/`endCursor`) against `http://192.168.149.21:8080/graphql`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add reqwest/tokio deps and scaffold the graphql module (envelope + queries)** - `6a0518a` (feat)
2. **Task 2: Implement the async GraphQlClient with two-layer error dispatch and injectable endpoint** - `505b4ed` (feat)

_Plan is `type: execute` (not TDD) — each task is one atomic feat commit with its tests included._

## Files Created/Modified
- `src/graphql/mod.rs` - Module root: `mod client/envelope/queries` + selective `pub use` of `GraphQlClient`, `ClientError`, `AuthorsPage`, `StatsResult`, envelope types, query consts.
- `src/graphql/envelope.rs` - Generic `GraphQlResponse<T>` + `GraphQlError` + `Extensions` (`#[serde(default)]` on `errors`).
- `src/graphql/queries.rs` - `AUTHORS_QUERY`/`STATS_QUERY` consts + `AuthorsPage`/`AuthorsData`/`StatsResult`/`StatsData` camelCase structs + 3 fixture-parse tests.
- `src/graphql/client.rs` - `GraphQlClient` + `ClientError` + generic `query<T>` + `authors`/`stats` wrappers + 6 tests over a hand-rolled std::net loopback stub.
- `Cargo.toml` / `Cargo.lock` - reqwest/tokio/thiserror added; stack-discipline comment updated to reflect Phase-2 ownership.
- `src/lib.rs` - `pub mod graphql;` registration with the existing doc-comment idiom.

## Decisions Made
- **Hand-rolled loopback stub over wiremock:** a `std::net::TcpListener` on an ephemeral port serving one canned response per test, driven by a current-thread tokio runtime. Avoids a dev-dep for what a few lines of `std::net` cover; ran 5× to confirm stability.
- **thiserror for `ClientError`:** explicit two-layer mapping; the `Display` impl carries only HTTP status + first error `message`, never the full body (T-02-04 mitigation).
- **`first.extensions.and_then(|x| x.code)`** consumes the error by value (`into_iter().next()`) rather than cloning — clippy-clean and avoids an extra allocation.

## Deviations from Plan

None - plan executed exactly as written.

The plan offered discretion between a hand-rolled stub and `wiremock`; the hand-rolled stub was chosen per the documented preference ("prefer hand-rolling to avoid the dep"). This is the planned discretion path, not a deviation.

## Issues Encountered
None. Baseline build/tests were green before starting; all 21 tests (12 store + 9 graphql) pass after both tasks; `cargo clippy --all-targets -- -D warnings` is clean.

## Threat Surface Scan
No new security surface beyond the plan's `<threat_model>`. The single network endpoint is operator-supplied and injectable (T-02-05 accepted; forward constraint for OPS-03/Phase 4 noted in the plan). In-body errors are surfaced not dropped (T-02-06 mitigated). `ClientError` carries only status + first message, not the body (T-02-04 mitigated). Page `limit ≤ 500` bounds responses (T-02-07 accepted). No new auth/file/schema surface introduced.

## Verification
- `cargo build` — succeeds with reqwest/tokio added; no rayon.
- `cargo test` — 21 passed (3 envelope/query parse, 6 client: in-body-errors-surface, 503/413 mapping, authors/stats happy path, null-data-is-error; 12 pre-existing store tests still green).
- `cargo clippy --all-targets -- -D warnings` — clean.
- `grep` acceptance criteria: `reqwest`/`tokio` present, no `rayon` dependency, `pub mod graphql` registered, `struct GraphQlClient` with `endpoint: String` field + `pub fn new`, `127.0.0.1` appears only inside the `#[cfg(test)]` module.
- Live connectivity proof: `STATS_QUERY` and `AUTHORS_QUERY` round-trip against the live v1.2 adapter (`192.168.149.21:8080`) — real response shapes match the structs.

## Next Phase Readiness
- The reusable transport seam is in place for plan 02-03 (the `enumerate` walk) to fetch `authors`/`stats` through and branch on `ClientError` (503→retry/no-advance, INVALID_CURSOR→restart, exhaustion→abort).
- Phase 3 inherits the async transport and adds `latest_per_author` additively (new const + struct + wrapper) with no transport change (D-11).
- No blockers. `tokio`'s `time` feature is already present for plan-03's `tokio::time::sleep` backoff.

## Self-Check: PASSED

- All 4 created files exist (`src/graphql/{mod,envelope,queries,client}.rs`) + SUMMARY.
- Both task commits exist: `6a0518a`, `505b4ed`.

---
*Phase: 02-graphql-client-author-enumeration*
*Completed: 2026-06-25*
