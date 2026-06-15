---
phase: 05-hardening-docker-packaging
plan: 01
subsystem: lmdb2graphql
tags: [health-probes, readiness, graphql-stats, ops, atomicbool, axum]
dependency_graph:
  requires: [04-01]
  provides: [OPS-01, OPS-04]
  affects: [spam/src/server.rs, spam/src/main.rs, spam/src/graphql/schema.rs, spam/src/graphql/types.rs, spam/src/graphql/resolvers.rs]
tech_stack:
  added: []
  patterns:
    - "Arc<AtomicBool> readiness flag initialized false, set true only after run_comparator_self_check"
    - "axum .with_state(ready) + .layer(RequestBodyLimitLayer) layer ordering (state before outermost layer)"
    - "clone-before-spawn_blocking for String (OPS-04 follows existing Pitfall-1 pattern)"
key_files:
  created:
    - spam/tests/health_ready_test.rs
  modified:
    - spam/src/server.rs
    - spam/src/main.rs
    - spam/src/graphql/schema.rs
    - spam/src/graphql/types.rs
    - spam/src/graphql/resolvers.rs
    - spam/tests/body_limit_test.rs
decisions:
  - "AtomicBool (not Mutex/RwLock) for readiness state — single bit, no lock contention on every probe"
  - "readiness flag initialized false; store(true) strictly after run_comparator_self_check in source order"
  - "pinnedStrfryVersion is a pure additive String field on StatsResult — no breaking schema change"
  - "read_stats receives pinned_strfry_version as a String parameter (clone-before-closure), not via env re-open"
metrics:
  duration_seconds: 316
  completed_date: "2026-06-13T15:55:24Z"
  tasks: 2
  files_modified: 7
  commits: 4
---

# Phase 05 Plan 01: Health + Readiness Probes and Stats Version Surface Summary

**One-liner:** `/health` and `/ready` HTTP probes via `Arc<AtomicBool>` flag set after LMDB self-check; `pinnedStrfryVersion` threaded from config through `AppState` into the `stats` GraphQL resolver.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 RED | Failing tests for /health and /ready | 44f54b7 | tests/health_ready_test.rs |
| 1 GREEN | Wire /health + /ready probes (OPS-01) | ba932f9 | server.rs, main.rs, schema.rs, body_limit_test.rs, resolvers.rs |
| 2 RED | Failing test for pinnedStrfryVersion | 5a351ba | resolvers.rs |
| 2 GREEN | Surface pinnedStrfryVersion in stats (OPS-04) | f8175ce | types.rs, resolvers.rs |

## What Was Built

### Task 1 — /health and /ready probes (OPS-01)

`build_router` signature changed from `build_router(schema: AppSchema)` to `build_router(schema: AppSchema, ready: Arc<AtomicBool>)`. Two new routes added:

- `GET /health` → `health_handler`: returns `StatusCode::OK` unconditionally; zero LMDB access, zero allocation. Cheapest possible liveness handler.
- `GET /ready` → `ready_handler`: returns `200 OK` if `ready.load(Ordering::Acquire)` is true, `503 SERVICE_UNAVAILABLE` otherwise.

Router uses `.with_state(ready)` to inject the flag; `RequestBodyLimitLayer` remains the outermost layer (after `.with_state`), preserving the WR-02 body-cap invariant.

In `main.rs`: `Arc::new(AtomicBool::new(false))` created after tracing/config/LMDB/Meta/self-check gates; `store(true, Ordering::Release)` placed strictly after `run_comparator_self_check` returns `Ok` (T-05-04: false-ready tamper mitigation). `Arc::clone(&ready)` passed to `build_router`.

`AppState` gains `pinned_strfry_version: String` field (used by Task 2; both tasks share one `AppState` struct change to avoid struct split commits).

### Task 2 — pinnedStrfryVersion in stats query (OPS-04)

Three additive changes:

1. `StatsResult` in `types.rs` gains `pub pinned_strfry_version: String`; async-graphql auto-renames it to `pinnedStrfryVersion` in the SDL (same camelCase convention as existing `event_count` → `eventCount`).
2. `read_stats` signature extended to `read_stats(env, db_version, pinned_strfry_version: String)` and threads the value into `StatsResult`.
3. `stats` resolver clones `state.pinned_strfry_version` before the `spawn_blocking` closure (follows the existing clone-before-closure pattern for Pitfall 1 / `!Send` boundary).

`main.rs` AppState construction populates `pinned_strfry_version: cfg.pinned_strfry_version.clone()`.

## Verification

- `cargo test --all-targets` passes: 108 lib + 17 integration = 125 tests, 0 failures.
- `cargo build` succeeds.
- `/health` returns 200 (test_health_returns_200).
- `/ready` returns 503 before flag set (test_ready_returns_503_before_flag_set).
- `/ready` returns 200 after flag set (test_ready_returns_200_after_flag_set).
- `stats { pinnedStrfryVersion }` returns the configured value (test_stats_pinned_strfry_version).
- `dbVersion` unchanged — additive regression test passes.

## Acceptance Criteria

- `grep -q 'fn build_router(schema: AppSchema, ready' spam/src/server.rs` — PASS
- `grep -q 'SERVICE_UNAVAILABLE' spam/src/server.rs` — PASS
- `grep -q 'build_router(schema, Arc::clone(&ready))' spam/src/main.rs` — PASS
- `store(true, ...)` appears after `run_comparator_self_check` in source order — PASS
- `grep -q 'pinned_strfry_version: String' spam/src/graphql/types.rs` — PASS
- `grep -q 'pinned_strfry_version: String' spam/src/graphql/schema.rs` — PASS
- `grep -q 'pinned_strfry_version: cfg.pinned_strfry_version.clone()' spam/src/main.rs` — PASS
- `read_stats` signature includes `pinned_strfry_version: String` — PASS
- `cargo test --all-targets` passes — PASS

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing field] `body_limit_test.rs` and `resolvers.rs` unit tests updated for new signatures**

- **Found during:** Task 1 implementation
- **Issue:** `body_limit_test.rs` called `build_router(build_schema(app_state))` with one arg (the old signature). `resolvers.rs` unit test `make_app_state` constructed `AppState` without the new `pinned_strfry_version` field (struct exhaustiveness). Both broke compilation.
- **Fix:** `body_limit_test.rs` updated to pass `Arc::new(AtomicBool::new(true))` as second arg and add `pinned_strfry_version: "test-pinned".to_string()` to AppState. `resolvers.rs` `make_app_state` updated identically.
- **Files modified:** `spam/tests/body_limit_test.rs`, `spam/src/graphql/resolvers.rs`
- **Commit:** ba932f9

None of the other existing tests required modification — all other callers of `AppState` were confined to the two locations above.

## Known Stubs

None — all fields are wired to real data sources (`cfg.pinned_strfry_version` from config, `AtomicBool` from startup gate). No placeholder text or empty values flow to outputs.

## Threat Flags

None — no new network endpoints, auth paths, or file access patterns beyond those documented in the plan's threat model (T-05-01 through T-05-SC accepted/mitigated as specified).

## Self-Check: PASSED

Files verified present:
- spam/src/server.rs — FOUND
- spam/src/main.rs — FOUND
- spam/src/graphql/schema.rs — FOUND
- spam/src/graphql/types.rs — FOUND
- spam/src/graphql/resolvers.rs — FOUND
- spam/tests/health_ready_test.rs — FOUND

Commits verified:
- 44f54b7 — FOUND (test(05-01): add failing tests for /health and /ready probes)
- ba932f9 — FOUND (feat(05-01): wire /health + /ready probes)
- 5a351ba — FOUND (test(05-01): add failing test for pinnedStrfryVersion)
- f8175ce — FOUND (feat(05-01): surface pinnedStrfryVersion through stats query)
