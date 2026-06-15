---
phase: 05-hardening-docker-packaging
verified: 2026-06-15T00:00:00Z
status: gaps_found
score: 6/7 must-haves verified
overrides_applied: 0
gaps:
  - truth: "GET /ready returns 503 only before LMDB env open AND comparator self-check pass; 200 after — the 503 branch is reachable by a real orchestrator client"
    status: failed
    reason: "The readiness flag is initialized false and stored true on adjacent lines (main.rs:84-85) before the TcpListener binds (main.rs:101) and before axum::serve starts (main.rs:122). The socket does not exist until after the flag is already true, so no external client can ever receive 503. The 503 branch in ready_handler is dead code in production. Unit tests pass only because they construct the router directly with AtomicBool::new(false) — bypassing the startup ordering. The phase goal says 'readiness gates are live', which this implementation does not satisfy for any real orchestrator."
    artifacts:
      - path: "spam/src/main.rs"
        issue: "lines 84-85: Arc::new(AtomicBool::new(false)) then immediately store(true, Release) — before TcpListener::bind at line 101 and axum::serve at line 122. The flag transitions false→true before the socket is bound."
    missing:
      - "Bind the TcpListener (or at minimum start a minimal /health+/ready router) BEFORE the gate chain completes, so the socket is live while the comparator self-check runs. Then store(true) only after run_comparator_self_check returns Ok. This makes the 503→200 transition observable to a real orchestrator polling /ready during startup."
---

# Phase 05: Hardening + Docker Packaging Verification Report

**Phase Goal:** The service is operationally safe for DeepFry deployment: health/readiness gates are live, CI asserts correctness against the pinned strfry fixture, and docker-compose brings it up co-located with strfry mounting strfry-db read-only.
**Verified:** 2026-06-15
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | GET /health returns 200 whenever the process is running | ✓ VERIFIED | `health_handler` returns `StatusCode::OK` unconditionally; `test_health_returns_200` passes |
| 2 | GET /ready 503 branch is reachable by a real orchestrator during startup | ✗ FAILED | `store(true)` at main.rs:85 precedes `TcpListener::bind` at line 101 and `axum::serve` at line 122 — socket not bound until flag is already true; 503 branch is dead in production |
| 3 | A stats GraphQL query returns pinnedStrfryVersion alongside dbVersion | ✓ VERIFIED | `StatsResult.pinned_strfry_version` populated via `read_stats`; `test_stats_pinned_strfry_version` passes |
| 4 | The readiness flag is set true only after run_comparator_self_check succeeds (source order) | ✓ VERIFIED | main.rs:73 `run_comparator_self_check`, then main.rs:84-85 `AtomicBool::new(false)` + `store(true)` — gate-before-flag in source order is correct; the defect is that the socket does not bind until after |
| 5 | A multi-stage Dockerfile builds a static Alpine binary | ✓ VERIFIED | Two-stage Dockerfile; `rm -f rust-toolchain.toml`; `RUSTFLAGS="-C target-feature=+crt-static"`; `--target x86_64-unknown-linux-musl`; `docker compose config` validates |
| 6 | A docker-compose service mounts strfry-db read-only (:ro) co-located with strfry, healthchecks /health | ✓ VERIFIED | `:/app/strfry-db:ro` confirmed; healthcheck: `wget -qO- http://localhost:8080/health`; `external: true` deepfry-net; `docker compose -f docker-compose.lmdb2graphql.yml config` exits 0 |
| 7 | CI builds, runs the test suite against the pinned strfry fixture, asserts comparator scan order + payload decode | ✓ VERIFIED | `.github/workflows/lmdb2graphql.yml` pins digest `545555da...`; removes `spam/rust-toolchain.toml`; runs `bash tests/generate_fixture.sh`; `cargo test --all-targets`; `clippy -- -D warnings`; `generate_fixture.sh` is syntactically valid and pins same digest |

**Score:** 6/7 truths verified

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `spam/src/server.rs` | build_router(schema, ready) with /health and /ready routes | ✓ VERIFIED | `fn build_router(schema: AppSchema, ready: Arc<AtomicBool>)` confirmed; both handlers present and substantive |
| `spam/src/main.rs` | Arc<AtomicBool> readiness flag set after gates, passed to build_router | ⚠ PARTIAL | Flag exists and is set after `run_comparator_self_check` in source order, but `store(true)` happens before socket bind — probe window never exists |
| `spam/src/graphql/types.rs` | StatsResult.pinned_strfry_version field | ✓ VERIFIED | `pub pinned_strfry_version: String` at line 139 |
| `spam/src/graphql/schema.rs` | AppState.pinned_strfry_version field | ✓ VERIFIED | `pub pinned_strfry_version: String` at line 49 |
| `spam/src/graphql/resolvers.rs` | read_stats threads pinned_strfry_version into StatsResult | ✓ VERIFIED | `fn read_stats(env, db_version, pinned_strfry_version: String)`; returned in `StatsResult { ..., pinned_strfry_version }` at line 387 |
| `spam/tests/health_ready_test.rs` | Integration tests for /health (200), /ready (200 after flag, 503 before) | ✓ VERIFIED | 3 tests all pass; tests correctly exercise handler logic with directly controlled AtomicBool |
| `spam/Dockerfile` | Multi-stage Alpine; rm -f rust-toolchain.toml; g++/lmdb-dev/musl-dev; crt-static | ✓ VERIFIED | All four elements confirmed present |
| `docker-compose.lmdb2graphql.yml` | lmdb2graphql service with :ro strfry-db mount, /health healthcheck, deepfry-net external | ✓ VERIFIED | All elements confirmed; `docker compose config` validates |
| `spam/config/lmdb2graphql.yaml.example` | bind_address 0.0.0.0:8080 documented | ✓ VERIFIED | `bind_address: "0.0.0.0:8080"` present as active line with loopback/Docker instructions |
| `spam/tests/generate_fixture.sh` | Deterministic fixture regeneration; pins 545555da digest | ✓ VERIFIED | Pins same digest; `set -euo pipefail`; stdin injection; temp-dir-before-commit pattern; `bash -n` passes |
| `.github/workflows/lmdb2graphql.yml` | CI: build deps, rustup, pull pinned strfry, generate fixture, cargo test, clippy | ✓ VERIFIED | All steps confirmed; digest `545555da` present; `rm -f spam/rust-toolchain.toml` before cargo |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `docker-compose.lmdb2graphql.yml` healthcheck | `/health` endpoint | `wget http://localhost:8080/health` | ✓ WIRED | Confirmed in compose file line 44 |
| `spam/Dockerfile` builder | static binary | `rm rust-toolchain.toml` + `RUSTFLAGS crt-static` + musl target | ✓ WIRED | All three elements confirmed in Dockerfile |
| `.github/workflows/lmdb2graphql.yml` | `tests/generate_fixture.sh` + pinned strfry digest | `docker pull ... 545555da` then `bash generate_fixture.sh` | ✓ WIRED | Both steps present; digest matches across all files |
| `spam/src/main.rs` | `spam/src/server.rs build_router` | `Arc<AtomicBool>` passed as second arg after self-check | ✓ PARTIAL | Arg passed correctly; but flag is already `true` when `build_router` is called — the intent of "after self-check" is source-order correct, but the socket hasn't bound yet, so no window exists |
| `spam/src/graphql/resolvers.rs` stats resolver | `AppState.pinned_strfry_version` | clone-before-closure into `read_stats` | ✓ WIRED | `let pinned = state.pinned_strfry_version.clone()` at line 194; passed to `read_stats` |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `StatsResult.pinned_strfry_version` | `pinned_strfry_version: String` | `cfg.pinned_strfry_version` from `config::load()` → `AppState` → cloned before spawn_blocking → `read_stats` parameter → `StatsResult` field | Yes — loaded from config file on disk, not hardcoded | ✓ FLOWING |
| `ready_handler` | `ready: Arc<AtomicBool>` | `Arc::new(AtomicBool::new(false))` in main.rs then immediately `store(true)` before socket bind | The flag flows correctly into the handler; the defect is the startup window, not the data flow itself | ✓ FLOWING (but window unreachable) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `cargo test --all-targets` passes (125 total) | `cargo test --all-targets 2>&1 \| grep "test result"` | 9 result lines, all `ok. N passed; 0 failed` | ✓ PASS |
| health_ready_test: 3 probe tests pass | `cargo test --test health_ready_test` | `3 passed; 0 failed` | ✓ PASS |
| docker compose config validates | `docker compose -f docker-compose.lmdb2graphql.yml config` | exit 0 | ✓ PASS |
| generate_fixture.sh syntax valid | `bash -n spam/tests/generate_fixture.sh` | exit 0 | ✓ PASS |
| Digest lockstep: 545555da in all 3 locations | grep across workflow, generate_fixture.sh, config example | 3/3 | ✓ PASS |

### Probe Execution

Step 7c: SKIPPED — no `scripts/*/tests/probe-*.sh` files declared in PLAN or present conventionally for this phase. CI workflow exists but cannot be run locally without GitHub Actions infrastructure.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| OPS-01 | 05-01-PLAN.md | `/health` and `/ready` endpoints; `/ready` gates on env open + comparator self-check | ✗ PARTIAL | `/health` fully works. `/ready` handler logic is correct but the 503 branch is unreachable in production — the socket does not bind until after the flag is already true (CR-01). |
| OPS-02 | 05-02-PLAN.md | Docker subsystem, docker-compose co-located with strfry, strfry-db `:ro` | ✓ SATISFIED | Dockerfile, compose file, example config all verified |
| OPS-03 | 05-02-PLAN.md | CI pins same strfry digest, generates fixture, asserts 0x00/0x01 decode + comparator scan order | ✓ SATISFIED | GitHub Actions workflow confirmed; payload_test covers both decode paths; self_check_test covers comparator parity |
| OPS-04 | 05-01-PLAN.md | `stats` surfaces pinned strfry version + detected dbVersion | ✓ SATISFIED | `pinnedStrfryVersion` field in `StatsResult` wired from config through AppState to resolver |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| No TBD/FIXME/XXX markers found in any phase-5 modified file | — | — | — | Clean |
| No placeholder text or empty return stubs found | — | — | — | Clean |

No debt markers found. No stub anti-patterns found.

### Human Verification Required

None. All phase-5 behaviors were verifiable programmatically. The OPS-01 readiness defect was confirmed by static analysis of source order in `main.rs` and is a BLOCKER gap, not a human-verify item.

### Gaps Summary

**One gap blocks goal achievement.**

**OPS-01 readiness probe window is unreachable (BLOCKER):**

The phase's stated goal requires that "health/readiness gates are live." The `/ready` handler logic is correct — it returns 503 when the `AtomicBool` is false. However, in `main.rs` the sequence is:

```
line 73:  run_comparator_self_check(...)   ← gate runs
line 84:  let ready = Arc::new(AtomicBool::new(false));
line 85:  ready.store(true, Ordering::Release);  ← immediately flipped
line 98:  build_router(schema, Arc::clone(&ready))
line 101: TcpListener::bind(...)           ← socket opens AFTER flag is true
line 122: axum::serve(...)                 ← serving starts AFTER flag is true
```

Every request that can ever reach `/ready` will see `true`. The 503 branch in `ready_handler` (server.rs:133) is dead code in production. An orchestrator polling `/ready` during startup gains nothing over polling `/health`.

The unit tests in `health_ready_test.rs` exercise the 503 path correctly for the handler logic, but they construct the router with a directly controlled `AtomicBool::new(false)` — they do not test the startup sequence in `main.rs`.

**Fix:** Restructure startup so the HTTP listener is bound (or at minimum a minimal probe-only router is served) before the gate chain runs, then `store(true)` only after `run_comparator_self_check` returns `Ok`. The flag transitions false→true while the socket is live, making the 503→200 transition observable to a real orchestrator.

**All other must-haves are fully met:** OPS-02, OPS-03, OPS-04 are clean. 125 tests pass with zero failures. All acceptance criteria greps pass. No debt markers.

---

_Verified: 2026-06-15_
_Verifier: Claude (gsd-verifier)_
