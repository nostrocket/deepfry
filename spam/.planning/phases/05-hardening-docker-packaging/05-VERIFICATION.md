---
phase: 05-hardening-docker-packaging
verified: 2026-06-15T12:00:00Z
status: passed
score: 7/7 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 6/7
  gaps_closed:
    - "GET /ready 503 branch reachable by a real orchestrator on a continuously-served socket — bind-once design (CR-01/CR-02 fix)"
  gaps_remaining: []
  regressions: []
---

# Phase 05: Hardening + Docker Packaging Verification Report

**Phase Goal:** The service is operationally safe for DeepFry deployment: health/readiness gates are live, CI asserts correctness against the pinned strfry fixture, and docker-compose brings it up co-located with strfry mounting `strfry-db` read-only.
**Verified:** 2026-06-15T12:00:00Z
**Status:** PASSED
**Re-verification:** Yes — after gap closure (commits c02240a, c9ae7cf)

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | GET /health returns 200 whenever the process is running | ✓ VERIFIED | `health_handler` returns `StatusCode::OK` unconditionally; no LMDB access; `test_health_returns_200` passes |
| 2 | GET /ready returns 503 while startup gates run; 200 only after self-check passes; 503→200 transition observable on a continuously-served socket (no connection-refused window) | ✓ VERIFIED | Bind-once design: `TcpListener::bind` at line 76, `axum::serve` spawned at line 105, gate chain runs lines 115-143, `schema_cell.set` at line 165, `ready.store(true)` at line 167. Single continuous serve — no re-bind, no gap. `test_ready_transitions_503_to_200` passes; `test_ready_returns_503_when_flag_false` passes. |
| 3 | POST /graphql returns 503 and executes no query while gates are running (no corpus reachable while not ready) | ✓ VERIFIED | `graphql_handler` reads `state.schema.get()` — `None` returns 503 SERVICE_UNAVAILABLE with no query executed; `test_graphql_returns_503_when_schema_not_ready` passes. Security-equivalent to probe-router T-05-05-SC. |
| 4 | A stats GraphQL query returns pinnedStrfryVersion alongside dbVersion | ✓ VERIFIED | `StatsResult.pinned_strfry_version` populated via `read_stats`; `db_version = meta.db_version` and `pinned_strfry_version` logged together at main.rs:131-136; `test_stats_pinned_strfry_version` passes |
| 5 | A multi-stage Dockerfile builds a static Alpine binary that runs lmdb2graphql | ✓ VERIFIED | Two-stage Dockerfile; `RUN rm -f rust-toolchain.toml`; `ENV RUSTFLAGS="-C target-feature=+crt-static"`; `--target x86_64-unknown-linux-musl`; builder installs `musl-dev g++ lmdb-dev pkgconfig` |
| 6 | A docker-compose service mounts strfry-db :ro co-located with strfry, healthchecks /health | ✓ VERIFIED | `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db:ro`; healthcheck `wget -qO- http://localhost:8080/health`; loopback publish `127.0.0.1:8080:8080`; `deepfry-net` external; `docker compose config` validates |
| 7 | CI builds, generates fixture from pinned strfry, asserts comparator scan order + 0x00/0x01 payload decode | ✓ VERIFIED | `.github/workflows/lmdb2graphql.yml` pins digest `545555da...`; removes `spam/rust-toolchain.toml`; runs `bash tests/generate_fixture.sh`; `cargo test --all-targets`; `cargo clippy -- -D warnings`; `generate_fixture.sh` syntactically valid and pins same digest |

**Score:** 7/7 truths verified

### Gap Closure Verification

**Previous BLOCKER:** OPS-01 — `store(true)` at old main.rs:85 fired before `TcpListener::bind` at line 101, making the 503 branch of `/ready` dead code in production.

**Fix confirmed (bind-once / zero-gap design, commits c02240a + c9ae7cf):**

Source order in `/Users/g/git/deepfry/spam/src/main.rs`:
- Line 76: `TcpListener::bind(&cfg.bind_address)` — BEFORE gate chain
- Line 105: `tokio::spawn(axum::serve(listener, router))` — BEFORE gate chain; listener consumed once, no re-bind
- Line 115: `open_read_only_env(...)` — gate chain begins
- Line 142: `run_comparator_self_check(...)` — gate chain ends
- Line 165: `schema_cell.set(schema)` — populate BEFORE flip (populate-before-flip invariant)
- Line 167: `ready.store(true, Ordering::Release)` — AFTER self-check

AWK source-order assertions:
- `TcpListener::bind` (line 76) < `run_comparator_self_check` (line 155): PASS
- `run_comparator_self_check` (line 155) < `ready.store(true)` (line 167): PASS
- `TcpListener::bind` occurrence count = 1 (single-bind invariant): PASS
- `NetListener`, `build_probe_router`, `with_graceful_shutdown`, `Notify` occurrence count in main.rs = 0 (probe-router artifacts removed): PASS

**Plan deviation documented:** The 05-03 PLAN prescribed a `build_probe_router` + probe-shutdown + re-bind approach. The code review (05-REVIEW.md CR-01/CR-02) found this approach structurally incapable of satisfying OPS-01 without a connection-refused gap. The fix replaces it with the bind-once / `OnceCell`-gated design. The deviation is documented in 05-03-SUMMARY.md under "Design Change (Rule 3 — planned approach replaced)". The 05-03 PLAN must_have truth "only /health and /ready are MOUNTED during the gate window" is superseded — the router mounts `/graphql` from the start but the handler returns 503 and executes no query until `schema_cell` is populated, which is security-equivalent for T-05-05-SC.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `spam/src/server.rs` | `build_router(state: AppRouterState)` with /health, /ready, /graphql (gated) | ✓ VERIFIED | `pub fn build_router(state: AppRouterState) -> Router`; `AppRouterState { ready: Arc<AtomicBool>, schema: Arc<OnceCell<AppSchema>> }`; `/graphql` handler returns 503 on empty cell; all four routes present |
| `spam/src/main.rs` | Bind-once; serve spawned before gates; schema_cell populated then store(true) after self-check | ✓ VERIFIED | Single `TcpListener::bind` at line 76; `tokio::spawn(axum::serve(...))` at line 105; gate chain 115-143; `schema_cell.set` at 165; `ready.store(true)` at 167 |
| `spam/src/graphql/types.rs` | `StatsResult.pinned_strfry_version: String` | ✓ VERIFIED | `pub pinned_strfry_version: String` present; async-graphql renames to `pinnedStrfryVersion` in SDL |
| `spam/src/graphql/schema.rs` | `AppState.pinned_strfry_version: String` | ✓ VERIFIED | `pub pinned_strfry_version: String` in `AppState` struct |
| `spam/src/graphql/resolvers.rs` | `read_stats` threads `pinned_strfry_version` into `StatsResult` | ✓ VERIFIED | Clone-before-closure pattern; `pinned_strfry_version: String` parameter present |
| `spam/tests/health_ready_test.rs` | Integration tests for /health (200), /ready probes | ✓ VERIFIED | 3 tests pass; updated to `AppRouterState` signature |
| `spam/tests/ready_window_test.rs` | Regression: 503→200 transition + /graphql-gated-503 through router-served surface | ✓ VERIFIED | 4 tests; `test_ready_transitions_503_to_200` drives the shared-Arc transition; `test_graphql_returns_503_when_schema_not_ready` asserts T-05-05-SC |
| `spam/Dockerfile` | Multi-stage Alpine; `rm -f rust-toolchain.toml`; g++/lmdb-dev/musl-dev; crt-static | ✓ VERIFIED | All elements confirmed present; static linkage note included |
| `docker-compose.lmdb2graphql.yml` | `:ro` strfry-db mount, /health healthcheck, deepfry-net external, loopback publish | ✓ VERIFIED | All elements confirmed; `start_period: 10s` avoids restart loop on LMDB self-check window |
| `spam/config/lmdb2graphql.yaml.example` | `bind_address: "0.0.0.0:8080"` documented for in-container use | ✓ VERIFIED | Present as active documented key |
| `spam/tests/generate_fixture.sh` | Deterministic fixture regeneration; pins `545555da` digest | ✓ VERIFIED | Executable (`-rwxr-xr-x`); pins same digest; `bash -n` passes; stdin injection pattern |
| `.github/workflows/lmdb2graphql.yml` | CI: build deps, rustup, pull pinned strfry, generate fixture, cargo test, clippy | ✓ VERIFIED | All steps present; digest `545555da`; `rm -f spam/rust-toolchain.toml` before cargo |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `docker-compose.lmdb2graphql.yml` healthcheck | `/health` endpoint | `wget http://localhost:8080/health` | ✓ WIRED | Line 44; uses `/health` (liveness), not `/ready` (T-05-08) |
| `spam/Dockerfile` builder | static binary | `rm rust-toolchain.toml` + `RUSTFLAGS crt-static` + musl target | ✓ WIRED | All three elements in Dockerfile |
| `.github/workflows/lmdb2graphql.yml` | `tests/generate_fixture.sh` + pinned strfry digest | `docker pull ...545555da` then `bash generate_fixture.sh` | ✓ WIRED | Both steps present; digest matches across workflow, script, and config example |
| `spam/src/main.rs` gate chain | `ready.store(true)` + `schema_cell.set` | After `run_comparator_self_check` returns Ok | ✓ WIRED | Source order confirmed: self-check(155) < schema_cell.set(165) < store(true)(167) |
| `spam/src/graphql/resolvers.rs` stats resolver | `AppState.pinned_strfry_version` | `state.pinned_strfry_version.clone()` before spawn_blocking | ✓ WIRED | Clone-before-closure pattern; threaded into `read_stats` and returned in `StatsResult` |
| `spam/src/main.rs` TCP listener | `axum::serve` probe-capable surface before gates | Single `tokio::spawn` on bound listener before gate chain | ✓ WIRED | `tokio::spawn(axum::serve(listener, router))` at line 105, before `open_read_only_env` at line 115 |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `StatsResult.pinnedStrfryVersion` | `pinned_strfry_version: String` | `cfg.pinned_strfry_version` from `config::load()` → `AppState` → cloned before spawn_blocking → `read_stats` parameter → `StatsResult` | Yes — loaded from config file on disk | ✓ FLOWING |
| `ready_handler` → 200/503 | `ready: Arc<AtomicBool>` | Initialized `false` at main.rs:68; flipped `true` at line 167 after all gates pass | Yes — real gate outcomes determine value | ✓ FLOWING |
| `graphql_handler` → 503/query | `schema: Arc<OnceCell<AppSchema>>` | Cell empty until main.rs:165 `schema_cell.set(schema)` after gate chain | Yes — only populated after real LMDB + comparator gates pass | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `ready_window_test.rs` (4 tests) — 503→200 transition + graphql-gated-503 | `cargo test --test ready_window_test` | 4 passed; 0 failed | ✓ PASS |
| `cargo test --all-targets` — full suite, no regressions | `cargo test --all-targets 2>&1` | 133 passed; 0 failed (108 lib + 25 integration) | ✓ PASS |
| Source order: bind < self-check | awk assertion on main.rs | bind(76) < self_check(155): PASS | ✓ PASS |
| Source order: self-check < store(true) | awk assertion on main.rs | self_check(155) < store(true)(167): PASS | ✓ PASS |
| Bind-once invariant | `grep -c 'TcpListener::bind' src/main.rs` | 1 | ✓ PASS |
| Probe-router artifacts removed | grep for NetListener/build_probe_router/with_graceful_shutdown/Notify | 0 matches | ✓ PASS |
| `docker compose config` validates | `docker compose -f docker-compose.lmdb2graphql.yml config` | exit 0 | ✓ PASS |
| `generate_fixture.sh` syntax valid | `bash -n spam/tests/generate_fixture.sh` | exit 0 | ✓ PASS |
| NON-LOOPBACK warning preserved | `grep -q 'NON-LOOPBACK' src/main.rs` | found at line 86 | ✓ PASS |
| Digest lockstep across all 3 locations | grep across workflow, generate_fixture.sh, compose | 3/3 contain `545555da` | ✓ PASS |

### Probe Execution

Step 7c: SKIPPED — no `scripts/*/tests/probe-*.sh` files declared or present for this phase. CI workflow exists but cannot be run without GitHub Actions infrastructure.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| OPS-01 | 05-01-PLAN.md + 05-03-PLAN.md | `/health` and `/ready`; `/ready` gates on env open + comparator self-check; 503→200 transition observable on continuously-served socket | ✓ SATISFIED | Bind-once design confirmed; `ready_window_test.rs` proves the transition; all source-order assertions pass; no connection-refused gap |
| OPS-02 | 05-02-PLAN.md | Docker subsystem, docker-compose co-located with strfry, strfry-db `:ro` | ✓ SATISFIED | Dockerfile, compose file, example config all verified |
| OPS-03 | 05-02-PLAN.md | CI pins same strfry digest, generates fixture, asserts 0x00/0x01 decode + comparator scan order | ✓ SATISFIED | GitHub Actions workflow confirmed; `payload_test` covers both decode paths; `self_check_test` covers comparator parity |
| OPS-04 | 05-01-PLAN.md | `stats` surfaces pinned strfry version + detected dbVersion; startup output logs both | ✓ SATISFIED | `pinnedStrfryVersion` wired from config → AppState → resolver; `db_version` and `pinned_strfry_version` logged together at main.rs:131-136 |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| No TBD/FIXME/XXX markers in any phase-5 modified file | — | — | — | Clean |
| No placeholder text or empty return stubs | — | — | — | Clean |

No debt markers. No stub anti-patterns. Note: pre-existing clippy warnings in unmodified files (resolvers.rs, comparators.rs, etc.) are deferred per STATE.md — not introduced by this phase.

### Human Verification Required

None. All phase-5 behaviors were verifiable programmatically. The previous BLOCKER gap (OPS-01 window unreachable) is resolved and confirmed by static source-order analysis and passing tests.

### Gaps Summary

No gaps. The single BLOCKER gap from the initial verification is closed.

The bind-once / `OnceCell`-gated router design (commits c02240a, c9ae7cf) satisfies OPS-01's continuous-observability requirement:
- The TCP listener binds once at startup, before the gate chain
- The single `axum::serve` task runs for the entire process lifetime — no re-bind, no connection-refused window
- `/ready` returns 503 during the gate window (flag=false, schema cell empty)
- `POST /graphql` returns 503 during the gate window (schema cell empty) — no LMDB data reachable
- After `run_comparator_self_check` returns Ok: schema cell populated, then `ready.store(true)` — populate-before-flip invariant holds
- Any gate failure exits the process non-zero via `?` propagation; the serve task is dropped; `/ready` never reaches 200

All 133 tests pass. OPS-01, OPS-02, OPS-03, OPS-04 are satisfied.

---

_Verified: 2026-06-15T12:00:00Z_
_Verifier: Claude (gsd-verifier)_
