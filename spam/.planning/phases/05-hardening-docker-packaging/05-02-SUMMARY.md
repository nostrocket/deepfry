---
phase: 05-hardening-docker-packaging
plan: "02"
subsystem: lmdb2graphql
tags: [docker, alpine, ci, fixture, ops]
dependency_graph:
  requires: ["05-01"]
  provides: ["spam/Dockerfile", "docker-compose.lmdb2graphql.yml", "spam/tests/generate_fixture.sh", ".github/workflows/lmdb2graphql.yml"]
  affects: ["deployment", "ci"]
tech_stack:
  added: []
  patterns: ["multi-stage Alpine Dockerfile", "docker-compose co-location with :ro mount", "GitHub Actions CI with pinned strfry digest", "deterministic fixture regeneration via strfry import"]
key_files:
  created:
    - spam/Dockerfile
    - docker-compose.lmdb2graphql.yml
    - spam/tests/generate_fixture.sh
    - .github/workflows/lmdb2graphql.yml
  modified:
    - spam/config/lmdb2graphql.yaml.example
decisions:
  - "Healthcheck targets /health (not /ready) to avoid Docker restart loops during LMDB self-check startup window (T-05-08)"
  - "docker-compose publishes 127.0.0.1:8080:8080 (loopback only) while in-container bind uses 0.0.0.0:8080 (CR-01 / ASVS V4)"
  - "generate_fixture.sh injects seed events via stdin to strfry import (not docker cp of seed file) to preserve insertion order for levId determinism"
  - "Dockerfile removes rust-toolchain.toml before cargo build to avoid stable-x86_64-apple-darwin channel error on Linux (RESEARCH.md Pitfall 1)"
  - "CI installs Rust via official rustup.rs over TLS rather than a third-party action (RESEARCH.md Open Question 2 / Assumption A5)"
  - "0x01 payload decode gate satisfied by existing payload_test.rs synthetic round-trip — no compaction-produced LMDB fixture required (RESEARCH.md Open Question 3)"
metrics:
  duration: "~8 minutes"
  completed: "2026-06-15T03:39:08Z"
  tasks_completed: 2
  files_changed: 5
---

# Phase 05 Plan 02: Docker Packaging + CI Correctness Gate Summary

**One-liner:** Alpine multi-stage Dockerfile with musl/crt-static/golpe-deps, docker-compose :ro co-location, loopback-only port publish, /health healthcheck, and GitHub Actions CI gating comparator + payload decode correctness against the pinned strfry digest.

## Tasks Completed

| Task | Description | Commit | Files |
|------|-------------|--------|-------|
| 1 | Alpine Dockerfile + docker-compose service + example config (OPS-02) | 5690436 | spam/Dockerfile, docker-compose.lmdb2graphql.yml, spam/config/lmdb2graphql.yaml.example |
| 2 | CI workflow + fixture generation script (OPS-03) | 4435265 | .github/workflows/lmdb2graphql.yml, spam/tests/generate_fixture.sh |

## What Was Built

### Task 1: OPS-02 — Docker subsystem

**spam/Dockerfile** — Two-stage build:
- Builder: `rust:alpine`; installs `musl-dev g++ lmdb-dev pkgconfig`; removes `rust-toolchain.toml` (RESEARCH.md Pitfall 1 — it pins `stable-x86_64-apple-darwin` which fails on Linux); sets `RUSTFLAGS="-C target-feature=+crt-static"`; builds `--target x86_64-unknown-linux-musl`.
- Runtime: `alpine:3.21`; installs only `ca-certificates`; copies the binary. No lmdb runtime dependency (lmdb-sys builds from vendored source by default).

**docker-compose.lmdb2graphql.yml** — Co-located service:
- Mounts `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db:ro` — the `:ro` suffix enforces kernel-level read-only even if a code bug attempted a write txn (T-05-05).
- Publishes `127.0.0.1:8080:8080` — loopback only (CR-01 / ASVS V4); the in-container `0.0.0.0:8080` bind is invisible to the host network (T-05-06).
- Healthcheck: `wget -qO- http://localhost:8080/health` with `start_period: 10s` — uses `/health` not `/ready` to prevent restart loops during LMDB self-check (T-05-08).
- Declares `deepfry-net` as `external: true; name: deepfry-net` — follows existing strfry compose pattern exactly.

**spam/config/lmdb2graphql.yaml.example** — Added `bind_address` key with explicit documentation: the default is `127.0.0.1:8080` (loopback for bare-metal/dev); operators running in Docker must set `0.0.0.0:8080` (RESEARCH.md Pitfall 2). The binary's config.rs default is unchanged.

### Task 2: OPS-03 — CI correctness gate

**.github/workflows/lmdb2graphql.yml** — First GitHub Actions workflow in the DeepFry repo:
- Triggers on `push` and `pull_request` filtered to `spam/**` and the workflow file itself.
- Installs build deps via `apt-get`: `liblmdb-dev g++ pkg-config` (needed by build.rs on Ubuntu).
- Installs Rust stable via official `rustup.rs` over TLS (no third-party action) then adds `$HOME/.cargo/bin` to `$GITHUB_PATH`.
- Removes `spam/rust-toolchain.toml` before any cargo command (same landmine as Dockerfile).
- Pulls pinned strfry image by immutable digest `545555da...` — prevents comparator/fixture drift (T-05-07).
- Runs `bash tests/generate_fixture.sh` to regenerate `tests/fixture/data.mdb` from the pinned image.
- Runs `cargo test --all-targets -- --nocapture` — this is the OPS-03 correctness gate: self_check tests assert comparator scan-order parity (golden vectors), payload_test asserts 0x00 decode (fixture) and 0x01 decode (synthetic round-trip in payload_test.rs).
- Runs `cargo clippy -- -D warnings`.

**spam/tests/generate_fixture.sh** — Deterministic fixture regeneration:
- Pins the same `STRFRY_IMAGE` digest as the Dockerfile and CI.
- Runs `strfry import` via `docker run --rm -i` feeding `seed_events.jsonl` via stdin in file order — preserves levId monotonicity (RESEARCH.md Pitfall 4; golden vector determinism).
- Writes to a `mktemp -d` temp directory; copies `data.mdb` and `lock.mdb` to `tests/fixture/` only after successful import.
- Prints sha256 of the regenerated `data.mdb` alongside the PROVENANCE.md reference sha256 for operator verification.
- Script is executable and syntactically valid (`bash -n` passes).

## Decisions Made

1. **Healthcheck `/health` not `/ready`** — Docker healthcheck failures can trigger container restarts. `/ready` returns 503 during the LMDB self-check window; using it would cause a restart loop. `/health` is always 200 when the process is alive. `start_period: 10s` gives the self-check time to complete before the healthcheck begins counting failures.

2. **Loopback publish + in-container 0.0.0.0** — The docker-compose file publishes `127.0.0.1:8080:8080` (host-side exposure control). The example config documents `bind_address: "0.0.0.0:8080"` (required inside the container to be reachable via the bridge). These two work together: the container binds widely within its namespace; the host only forwards to loopback.

3. **rustup.rs direct install in CI** — RESEARCH.md Open Question 2 noted that the third-party action name `dtolsma/rust-toolchain@v1` is `[ASSUMED]`. Using `curl https://sh.rustup.rs` over TLS is the official install path with no third-party action supply-chain dependency.

4. **0x01 gate via synthetic round-trip** — Running `strfry compact` in CI requires orchestrating a multi-step strfry lifecycle. The existing `payload_test.rs` already has a synthetic 0x01 round-trip test that proves the decode path works. RESEARCH.md (Open Question 3) explicitly endorses this as sufficient for OPS-03.

5. **Fixture via stdin `strfry import`** — Feeding `seed_events.jsonl` via stdin (not `docker cp`) lets the container see events in file order without mounting the host path, keeping the script portable. File order preserves levId monotonicity per PROVENANCE.md §A5.

## Deviations from Plan

None — plan executed exactly as written.

## Threat Flags

All threats mitigated as designed (T-05-05 through T-05-09). No new security-relevant surface introduced beyond what the plan's threat model covers:

| Threat ID | Mitigation Applied |
|-----------|-------------------|
| T-05-05 | `:ro` on strfry-db mount |
| T-05-06 | `127.0.0.1:8080:8080` loopback-only publish |
| T-05-07 | Pinned `@sha256:545555da...` digest in all three locations |
| T-05-08 | `/health` (not `/ready`) as healthcheck target; `start_period: 10s` |
| T-05-09 | No new Cargo deps; Rust toolchain via official rustup.rs TLS |

## Self-Check: PASSED

Files created/exist:
- spam/Dockerfile: FOUND
- docker-compose.lmdb2graphql.yml: FOUND
- spam/config/lmdb2graphql.yaml.example: FOUND (modified)
- .github/workflows/lmdb2graphql.yml: FOUND
- spam/tests/generate_fixture.sh: FOUND

Commits:
- 5690436: feat(05-02): Alpine multi-stage Dockerfile + docker-compose service + example config (OPS-02)
- 4435265: feat(05-02): CI workflow + fixture generation script (OPS-03)
