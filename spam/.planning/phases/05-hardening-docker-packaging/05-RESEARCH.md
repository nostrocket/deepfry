# Phase 5: Hardening & Docker Packaging — Research

**Researched:** 2026-06-13
**Domain:** Axum health/readiness endpoints · Alpine multi-stage Docker (Rust + C++ + static liblmdb) · docker-compose co-location · CI fixture generation
**Confidence:** HIGH

---

## Summary

Phase 5 makes LMDB2GraphQL operationally safe for DeepFry deployment. The four success criteria (OPS-01..OPS-04) are self-contained infrastructure additions to an already-working service: health/readiness HTTP endpoints, a Docker image and compose service, a CI correctness gate, and version-drift surfacing in the `stats` query.

The codebase is in excellent shape for this phase. `run_comparator_self_check` already carries a doc comment explicitly noting it is "a standalone public function; Phase 5's `/ready` endpoint calls it directly without code duplication" (self_check.rs:303). The startup gate chain in main.rs is the canonical authority for what "ready" means. The key implementation decisions are (a) how to share readiness state between the synchronous startup gate and the axum router, and (b) how to produce a working Alpine static binary when build.rs links a C++ golpe comparator shim alongside liblmdb.

The main landmines are: (1) `rust-toolchain.toml` currently pins `stable-x86_64-apple-darwin` — the Docker builder must ignore or override this with an explicit Linux target; (2) the golpe C++ shim in build.rs requires both a C++ toolchain (g++) and lmdb.h in the Alpine builder — the build.rs already handles the `/usr/include/lmdb.h` system path for Linux; (3) producing a genuine 0x01 (zstd-dictionary) fixture without running strfry's offline compaction is non-trivial and may not be achievable purely synthetically — the CI gate for 0x01 can reuse the round-trip test already present in `payload_test.rs` rather than requiring a compaction-produced LMDB file.

**Primary recommendation:** Wire readiness via `Arc<AtomicBool>` set after the self-check passes and passed into the router — the cleanest pattern that lets `/ready` return 503 during the async gap between process start and first successful gate. Use a two-stage Dockerfile (builder on `rust:1.89-alpine3.21`, runtime on `alpine:3.21`) with `musl-dev`, `lmdb-dev`, `g++`, and `RUSTFLAGS=-C target-feature=+crt-static`. Override `rust-toolchain.toml` in the Docker builder via `RUSTUP_TOOLCHAIN=stable-x86_64-unknown-linux-musl`.

---

## Project Constraints (from CLAUDE.md)

- Read-only LMDB access only (`MDB_RDONLY`); never open a write txn.
- Hard version gate: refuse to run if `Meta.dbVersion != 3`; assert endianness matches host.
- No event payloads outside StrFry — LMDB2GraphQL is a read-only query lens.
- Co-located deployment: mount strfry's `strfry-db` read-only (`:ro`).
- Packaged as its own Docker subsystem with a docker-compose service.
- Config files live in `~/deepfry/`; tests must use `tempfile::tempdir()`.
- Tech stack is Rust — `heed` 0.22.1, `async-graphql` 7.2.1, `axum` 0.8.9, `tokio` 1.52.3.

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| OPS-01 | Expose `/health` (200 when alive) and `/ready` (200 after env open + comparator self-check pass; 503 otherwise) | Arc<AtomicBool> readiness pattern; axum handler returning StatusCode |
| OPS-02 | Docker subsystem with docker-compose service, co-located with strfry, mounting `strfry-db` read-only | Multi-stage Dockerfile; docker-compose volumes `:ro`; external network `deepfry-net` |
| OPS-03 | CI pins strfry digest, generates fixture, asserts 0x00 and 0x01 payload decode + comparator scan order | GitHub Actions workflow; fixture from `docker cp`; 0x01 via existing round-trip test |
| OPS-04 | `stats` / startup output surfaces pinned strfry version + on-disk `dbVersion` | `pinned_strfry_version` already in AppState.meta / config; extend StatsResult |
</phase_requirements>

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| `/health` liveness probe | API / Backend (axum router) | — | Pure HTTP — process alive = respond; no LMDB state needed |
| `/ready` readiness probe | API / Backend (axum router) | Startup gate (main.rs) | Router reads `Arc<AtomicBool>` set by main.rs after gates pass |
| Startup gate chain (env open, Meta, self-check) | Startup / main.rs | — | Already lives in main.rs; sets the shared readiness flag |
| Docker image build | Build / CI | — | Multi-stage Dockerfile; not runtime code |
| docker-compose service definition | Deployment / Ops | — | Separate compose file; uses parent stack's named volume |
| CI fixture generation + assertions | CI / GitHub Actions | — | Workflow job; generates fixture from pinned image |
| Version drift surfacing (stats) | API / Backend (resolvers.rs) | Config / AppState | `pinned_strfry_version` from config added to AppState, read in `stats` resolver |

---

## Standard Stack

No new external crates are required for this phase. All needed functionality is available from the existing locked stack:

| Component | What It Provides | Already in Cargo.toml |
|-----------|------------------|----------------------|
| `axum` 0.8.9 | `Router`, `StatusCode`, handler functions | Yes |
| `tokio` 1.52.3 | `Arc`, async runtime | Yes |
| `std::sync::atomic::AtomicBool` | Shared readiness flag | std — no dep |
| `tracing` | Startup log lines | Yes |

For Docker:
- Builder image: `rust:1.89-alpine3.21` (or `rust:alpine` pinned to the same) [ASSUMED — verify current stable tag]
- Runtime image: `alpine:3.21` [ASSUMED — verify current]
- Alpine packages needed in builder: `musl-dev` `lmdb-dev` `g++` `pkgconfig`

**No new Cargo dependencies required.**

---

## Architecture Patterns

### System Architecture Diagram

```
[Process start]
      │
      ▼
main.rs startup gate chain:
  load config → open LMDB env (READ_ONLY)
              → read Meta → assert_db_version → assert_endianness
              → run_comparator_self_check
              → set Arc<AtomicBool> "ready = true"
              │
              ▼
        build_router(schema, ready_flag)
              │
    ┌─────────┴──────────┐
    │                    │
    ▼                    ▼
GET /health          GET /ready
(always 200)     (200 if ready_flag.load(Acquire) == true,
                  else 503)
```

### Pattern 1: Health and Readiness in Axum 0.8

**What:** Two simple axum handlers added to `build_router`. Health always returns 200. Ready reads a shared `Arc<AtomicBool>` and returns 200 or 503.

**When to use:** This is the definitive pattern for this codebase — the startup gates are synchronous in main.rs, and the readiness flag bridges the synchronous gate result into the async router.

**Concrete implementation:**

```rust
// In server.rs — extend build_router signature
use std::sync::{Arc, atomic::{AtomicBool, Ordering}};
use axum::{Router, routing::get, http::StatusCode, extract::State};

pub fn build_router(schema: AppSchema, ready: Arc<AtomicBool>) -> Router {
    Router::new()
        .route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(ready)
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}

async fn health_handler() -> StatusCode {
    StatusCode::OK
}

async fn ready_handler(State(ready): State<Arc<AtomicBool>>) -> StatusCode {
    if ready.load(Ordering::Acquire) {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    }
}
```

```rust
// In main.rs — after all startup gates pass, before build_router
let ready = Arc::new(AtomicBool::new(false));

// ... all existing gate code (env open, meta, self-check) ...

// Set readiness AFTER all gates pass.
ready.store(true, Ordering::Release);

let router = build_router(schema, Arc::clone(&ready));
```

**Source:** axum 0.8 `State` extractor pattern — `[ASSUMED]` from training data; pattern is stable and idiomatic.

**Key design decision — pre-gate vs. post-gate readiness:**

The current startup is synchronous and short (< 1 second on any real hardware). The readiness flag approach described above starts the `AtomicBool` as `false` and the router mounts before `axum::serve` is called but after all gates pass (because `ready.store(true)` fires before `build_router`). This means `/ready` returns 200 immediately on first request — there is no async startup window where 503 could actually fire in the single-threaded startup.

However, a true async-startup scenario (where the env is opened in a background task while the HTTP server is already up) would need the `false` start. The current architecture is synchronous so either approach works. The `Arc<AtomicBool>` initialized to `true` after gates pass is the cleanest pattern that also handles the hypothetical future async case and makes the intent self-documenting.

**Alternative considered — router mounts only after gates pass (simpler):**

Because the current startup is synchronous, we could simply not add the `Arc<AtomicBool>` and just wire `/health` + `/ready` both to return 200 (since by the time the server binds, gates have already passed). This is simpler but loses the ability to distinguish "alive but not ready" from "alive and ready" — which matters if the startup is ever made async or if an operator probes before startup completes. The `Arc<AtomicBool>` pattern is marginally more complex but gives the correct semantics with no meaningful overhead.

**axum 0.8 router pattern note:** The existing `build_router` takes only `schema`. Adding `ready: Arc<AtomicBool>` as a second parameter is a clean extension. Use `.with_state(ready)` to inject it; `.layer(RequestBodyLimitLayer::new(...))` must remain outermost (after all routes). The `State` extractor for `Arc<AtomicBool>` requires that the type implements `Clone` — `Arc<T>` does. [ASSUMED]

**Do NOT use a `Mutex<bool>` or `RwLock<bool>` for readiness state.** `AtomicBool` is sufficient for a single boolean and avoids lock contention on every health probe. [ASSUMED]

### Pattern 2: Stats Resolver — Add `pinnedStrfryVersion`

**What:** Extend `StatsResult` in `types.rs` with a `pinned_strfry_version: String` field, and pass `cfg.pinned_strfry_version` into `AppState` so the `stats` resolver can read it without opening config a second time.

**Concrete change required:**

1. `AppState` (schema.rs): add `pinned_strfry_version: String`.
2. `main.rs`: populate `AppState.pinned_strfry_version = cfg.pinned_strfry_version.clone()`.
3. `types.rs`: add `pub pinned_strfry_version: String` to `StatsResult`.
4. `resolvers.rs` `read_stats`: add `pinned_strfry_version: state.pinned_strfry_version.clone()` to the returned struct.

The `stats` query already exposes `dbVersion`; adding `pinnedStrfryVersion: String!` is a pure additive schema change with no breaking impact. [ASSUMED]

### Pattern 3: Alpine Multi-Stage Dockerfile

**What:** A two-stage Dockerfile that builds a statically-linked binary in an Alpine builder, then copies only the binary into a minimal Alpine runtime image.

**Critical requirements for this specific build:**

1. **C++ toolchain in builder:** build.rs calls `cc::Build::new().cpp(true)` to compile `reference/golpe_comparators.cpp`. The Alpine builder needs `g++` (or `c++`). `musl-dev` is required for the musl C library headers. [ASSUMED — standard Alpine Alpine packages]
2. **lmdb.h in builder:** build.rs locates `lmdb.h` via pkg-config or `/usr/include/lmdb.h`. On Alpine, installing `lmdb-dev` places `lmdb.h` at `/usr/include/lmdb.h` — build.rs probe path 4 picks it up. [ASSUMED]
3. **Static liblmdb:** heed/lmdb-sys can build liblmdb from source (its default Cargo feature). If `lmdb-sys` builds from vendored source, no system `liblmdb.so` is needed at runtime. Confirm by checking if `lmdb-sys` in Cargo.lock uses vendored source or system library. If using system library, set `LMDB_STATIC=1`. [ASSUMED]
4. **rust-toolchain.toml override:** The current `rust-toolchain.toml` pins `channel = "stable-x86_64-apple-darwin"`. In the Docker builder, this file causes `rustup` to try to install the macOS toolchain inside a Linux container — which will fail or install the wrong target. **The Dockerfile must either delete this file before building or set `RUSTUP_TOOLCHAIN=stable` to override it.** The safest approach is to remove or ignore it in Docker.

**Concrete Dockerfile:**

```dockerfile
# syntax=docker/dockerfile:1

# Stage 1 — builder
FROM rust:alpine AS builder

RUN apk add --no-cache \
    musl-dev \
    g++ \
    lmdb-dev \
    pkgconfig

WORKDIR /src
COPY . .

# Override the macOS-pinned rust-toolchain.toml — the pinned channel
# "stable-x86_64-apple-darwin" is invalid on Linux. Remove it so rustup
# uses the stable toolchain that ships with this image.
RUN rm -f rust-toolchain.toml

ENV RUSTFLAGS="-C target-feature=+crt-static"
RUN cargo build --release --target x86_64-unknown-linux-musl

# Stage 2 — minimal runtime
FROM alpine:3.21

# lmdb-dev NOT needed at runtime — binary is statically linked.
# ca-certificates for any future TLS; not strictly needed now.
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /src/target/x86_64-unknown-linux-musl/release/lmdb2graphql /app/lmdb2graphql

EXPOSE 8080
ENTRYPOINT ["/app/lmdb2graphql"]
```

**Source:** CLAUDE.md Alpine static-binary pattern + build.rs lmdb.h probe logic [CITED: /Users/g/git/deepfry/spam/CLAUDE.md, build.rs]

**Landmine 1 — musl target vs. default Alpine target:** The `rust:alpine` image uses `x86_64-unknown-linux-musl` as its default target. `RUSTFLAGS=-C target-feature=+crt-static` is needed to statically link the C runtime. Without it, the binary may still dynamically link `libc.so` even on musl, which would break on the minimal runtime image.

**Landmine 2 — lmdb-sys build mode:** If `lmdb-sys` (transitively via heed) links dynamically to the system `liblmdb.so`, the runtime Alpine image also needs `lmdb` installed. Check whether heed's default features compile lmdb from source or use the system library. In practice, `lmdb-sys` (used by heed 0.22.x) compiles from its vendored source by default — meaning no system `liblmdb.so` is needed. If this assumption is wrong, add `lmdb` to the runtime `apk add`. [ASSUMED — verify by inspecting `lmdb-sys` in Cargo.lock or heed's Cargo.toml features]

**Landmine 3 — golpe_comparators.cpp and the musl toolchain:** `g++` on Alpine is the musl cross-toolchain. The `.cpp(true)` + `-std=c++17` flags should work. If `lmdb++.h` (reference/lmdbxx/lmdb++.h) includes standard C++ headers that are musl-incompatible, there could be issues. Given that the existing CI tests pass on Linux (build.rs has Linux paths for system lmdb.h), this should be fine. [ASSUMED]

### Pattern 4: docker-compose Co-Location

**What:** A `docker-compose.lmdb2graphql.yml` (or add a service to an existing file) that adds `lmdb2graphql` to the DeepFry stack, mounting the same `strfry-db` volume read-only and connecting to `deepfry-net`.

**Key observation from existing docker-compose.strfry.yml:**
- The strfry service mounts `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db` as a named bind-mount.
- `deepfry-net` is an **external** named network (`external: true; name: deepfry-net`). The lmdb2graphql service must also declare it as external.
- There is no named Docker volume (no `volumes:` top-level block with a named volume like `strfry-db:`); the strfry DB is a bind-mount at a host path controlled by `STRFRY_DB_PATH`.

**Concrete service stanza:**

```yaml
services:
  lmdb2graphql:
    build:
      context: spam/
      dockerfile: Dockerfile
    container_name: lmdb2graphql
    restart: unless-stopped
    environment:
      RUST_LOG: "info,lmdb2graphql=info"
    volumes:
      # Mount strfry's LMDB read-only. Must match STRFRY_DB_PATH in docker-compose.strfry.yml.
      - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db:ro
      # Operator-supplied config.
      - ${LMDB2GRAPHQL_CONFIG:-./config/lmdb2graphql.yaml}:/root/deepfry/lmdb2graphql.yaml:ro
    ports:
      - "127.0.0.1:8080:8080"   # Expose only on loopback; default bind_address in config
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 10s
    networks:
      - deepfry-net

networks:
  deepfry-net:
    external: true
    name: deepfry-net
```

**Notes:**
- `wget` is available on Alpine minimal images; `curl` is not always present. [ASSUMED — standard Alpine]
- `start_period: 10s` gives the LMDB self-check time to complete before failing health.
- The `/health` endpoint (not `/ready`) is the right choice for docker healthcheck — `/ready` returning 503 during the startup race should not cause Docker to restart the container; it just means "not yet serving requests".
- The config file is bind-mounted from host — operator must supply `./config/lmdb2graphql.yaml` or set `LMDB2GRAPHQL_CONFIG`. A `config/lmdb2graphql.yaml.example` already exists at `spam/config/lmdb2graphql.yaml.example`.
- The bind_address in config must be `0.0.0.0:8080` (not `127.0.0.1:8080`) when running inside Docker so the container's port is reachable from outside. Document this prominently; the default is loopback.

**Where to put this file:** Create `docker-compose.lmdb2graphql.yml` in the DeepFry root (alongside `docker-compose.strfry.yml`). This follows the existing pattern for each subsystem having its own compose file.

### Pattern 5: CI Fixture Generation and Correctness Gate

**What:** A GitHub Actions workflow (`.github/workflows/lmdb2graphql.yml`) that:
1. Pulls the pinned `dockurr/strfry@sha256:545555da...` image.
2. Generates a `strfry-db` by running strfry with a seed, then copies out the LMDB files.
3. Runs `cargo test` against that fixture, asserting comparator scan order and payload decoding.

**Current fixture situation:**
- A committed fixture already exists at `tests/fixture/data.mdb` and `tests/fixture/lock.mdb`, generated in Phase 1 against the pinned strfry 1.1.0 image.
- Golden vectors (6 JSON files in `tests/fixture/golden_vectors/`) are embedded into the binary via `include_str!` in `self_check.rs`.
- The existing `cargo test` suite (108 tests) already asserts comparator scan order (via `self_check_test.rs`) and payload decoding (via `payload_test.rs`).

**CI strategy:** The CI job should regenerate the fixture from the pinned strfry digest and run `cargo test --all-targets`. This proves the fixture is reproducible from the pinned image and that tests pass against it. The committed fixture is the reference; CI regenerates and asserts they match (or simply overwrites and runs tests).

**0x01 (zstd-dictionary) fixture production:**

Strfry generates `0x01` payloads only via its offline compaction command (`strfry compact`). Running compaction in CI requires:
1. Starting strfry against a populated DB.
2. Running `strfry compact`.
3. Copying out the compacted DB.

This is non-trivial in CI. **The practical approach:** the `0x01` decode test in `payload_test.rs` uses a synthetic round-trip (not an LMDB fixture from a compaction run). OPS-03 requires asserting 0x01 decode succeeds — this is already covered by the existing round-trip test (`test_0x01_round_trip` or equivalent in `payload_test.rs`). The CI gate for 0x01 should point to this existing test, not attempt to produce a compaction-produced LMDB file.

**Concrete GitHub Actions workflow:**

```yaml
name: lmdb2graphql CI

on:
  push:
    paths: ['spam/**', '.github/workflows/lmdb2graphql.yml']
  pull_request:
    paths: ['spam/**']

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install Rust stable
        uses: dtolsma/rust-toolchain@v1  # [ASSUMED crate name — verify]
        with:
          toolchain: stable

      - name: Install build dependencies
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y --no-install-recommends liblmdb-dev g++ pkg-config

      - name: Pull pinned strfry image
        run: |
          docker pull dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5

      - name: Generate fixture from pinned strfry
        run: |
          cd spam
          bash tests/generate_fixture.sh

      - name: Run tests
        working-directory: spam
        env:
          RUST_LOG: "error"
        run: |
          cargo test --all-targets -- --nocapture

      - name: Run cargo clippy
        working-directory: spam
        run: |
          cargo clippy -- -D warnings
```

**Fixture generation script (`tests/generate_fixture.sh`):** This script should:
1. Start a strfry container with an empty DB dir.
2. Inject seed events from `tests/fixture/seed_events.jsonl` (already exists).
3. Copy `data.mdb` and `lock.mdb` out via `docker cp`.
4. Regenerate golden vectors by scanning the indexes (or verify the committed ones match).

The script can be a new file to write in this phase. The committed `seed_events.jsonl` (11 events) already serves as the seed. [CITED: tests/fixture/seed_events.jsonl — confirmed file exists]

**GitHub Actions root location:** The `.github/` directory does not currently exist in the DeepFry repo root (`ls .github/` returned only `copilot-instructions.md`). The CI workflow file will be the first GitHub Actions workflow. Create `.github/workflows/lmdb2graphql.yml`. [CITED: Bash ls output confirming no existing .github/workflows/]

### Anti-Patterns to Avoid

- **Using `/ready` as the docker healthcheck test:** `/ready` returning 503 during the startup gap can cause Docker to mark the container unhealthy and restart it before the LMDB env has finished opening. Use `/health` for the docker healthcheck; `/ready` is for orchestrators (Kubernetes, etc.) or the operator.
- **Binding `0.0.0.0` in config without explicit documentation:** The config default is loopback; the Docker deployment needs `0.0.0.0` inside the container. This is a footgun if not clearly documented in the example config.
- **Deleting `tests/fixture/data.mdb` in the CI fixture-regeneration step before confirming the regenerated one is valid:** Regenerate to a temp location, run tests, then copy into place.
- **Long-lived readiness structures:** Do not use a `Mutex<ReadinessState>` with multiple fields when `AtomicBool` suffices. The `/ready` gate has exactly one bit of state.
- **Running `docker compact` in CI without a controlled environment:** See 0x01 discussion above — use the synthetic round-trip test instead.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Health/readiness HTTP response | Custom middleware | `axum::http::StatusCode` returned from a plain handler | Axum handlers returning `StatusCode` compile directly to the correct HTTP response |
| Shared state in axum | `lazy_static` / global | `Arc<AtomicBool>` + `.with_state()` | Axum 0.8's `State` extractor is the idiomatic solution |
| Static binary on Alpine | Custom link flags | `RUSTFLAGS=-C target-feature=+crt-static` | Proven pattern; cc crate respects this for the C++ shim as well |
| 0x01 fixture production | Running strfry compact in CI | Reuse existing synthetic round-trip test | Compaction requires orchestrating a multi-step strfry lifecycle; synthetic test already proves the decode path |
| Healthcheck in docker-compose | Custom TCP probe script | `wget -qO- http://localhost:8080/health` | wget is present on Alpine; no extra dep |

---

## Common Pitfalls

### Pitfall 1: rust-toolchain.toml breaks Linux Docker build
**What goes wrong:** `rust-toolchain.toml` pins `channel = "stable-x86_64-apple-darwin"`. Inside a Linux Docker container, `rustup` attempts to install the macOS toolchain and fails (or falls back incorrectly). The build fails at the first `cargo build` invocation.
**Why it happens:** `rust-toolchain.toml` is a per-directory override that rustup unconditionally respects, including inside Docker where the target OS is different.
**How to avoid:** In the Dockerfile, `RUN rm -f rust-toolchain.toml` before `cargo build`. Alternatively, add `RUSTUP_TOOLCHAIN=stable` as a Docker ENV.
**Warning signs:** `error: override file '/src/rust-toolchain.toml' specifies an unavailable channel 'stable-x86_64-apple-darwin'` in docker build output. [CITED: STATE.md — pending todo about rust-toolchain.toml pin]

### Pitfall 2: `bind_address` loopback default blocks Docker port reachability
**What goes wrong:** Config default is `127.0.0.1:8080`. Inside a Docker container this binds to the container's loopback — the port is invisible to the host and to other containers on `deepfry-net`. Health checks and all GraphQL requests timeout.
**Why it happens:** The loopback default is correct for bare-metal/dev (CR-01) but wrong inside Docker where the container network uses a bridge, not loopback.
**How to avoid:** The docker-compose YAML or operator config must set `bind_address: "0.0.0.0:8080"`. Document this in the example config. Do NOT change the binary default.
**Warning signs:** `lmdb2graphql` container starts, logs "GraphQL server listening" on `127.0.0.1:8080`, but `docker compose ps` shows unhealthy and all requests timeout.

### Pitfall 3: lmdb-sys dynamic link on Alpine breaks runtime image
**What goes wrong:** If `lmdb-sys` links dynamically to the system `liblmdb.so.0`, the binary is not truly static despite `RUSTFLAGS=-C target-feature=+crt-static`. The minimal Alpine runtime image does not have liblmdb, so the binary fails at startup with a missing shared library error.
**Why it happens:** `target-feature=+crt-static` statically links the C runtime but not necessarily all third-party `.so` files that the C build system found via pkg-config.
**How to avoid:** Either (a) confirm heed/lmdb-sys uses vendored liblmdb (built from source, statically linked), or (b) set `LMDB_STATIC=1` in the builder ENV, or (c) install `lmdb` in the runtime Alpine image as a fallback. Check at build time: `ldd target/x86_64-unknown-linux-musl/release/lmdb2graphql` should list no `liblmdb`.
**Warning signs:** Container exits immediately with `error while loading shared libraries: liblmdb.so.0: cannot open shared object file`. [ASSUMED — standard Alpine static-linking concern]

### Pitfall 4: Golden vector mismatch in CI when fixture is regenerated
**What goes wrong:** The CI regenerates the fixture by running strfry with the pinned image and seed events. If the resulting `data.mdb` has different levIds or index order than the committed golden vectors, `self_check_test.rs` fails even though the implementation is correct — the golden vectors are stale.
**Why it happens:** levId is assigned by strfry at insert time and is monotonic per-run. Injecting the same seed events in a different order, or with different timing, can produce different levIds.
**How to avoid:** The fixture generation script must inject events in a deterministic order (sorted by the seed_events.jsonl file order). If the golden vectors change, they must be regenerated using the same fixture-generation procedure documented in `spec.md` or `PROVENANCE.md` (Plan 01-02 established this procedure). Verify golden vectors match before committing a CI-regenerated fixture. [CITED: spec.md — Phase 1 fixture generation procedure]

### Pitfall 5: `/ready` vs `/health` confusion in docker-compose healthcheck
**What goes wrong:** Using `/ready` as the docker healthcheck causes a restart loop: the service starts, Docker immediately probes `/ready`, gets 503 during the startup gate window, marks the container unhealthy, and may restart it — interrupting the gate sequence.
**Why it happens:** `/ready` is designed to return 503 before gates pass; docker healthcheck failures below a threshold trigger restarts.
**How to avoid:** Use `/health` for the docker healthcheck (`test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]`). Set `start_period: 10s` to give the container time before health checks begin counting. [ASSUMED — Docker compose healthcheck behavior]

---

## Code Examples

### Health and Readiness Handlers (OPS-01)

```rust
// server.rs additions
use std::sync::{Arc, atomic::{AtomicBool, Ordering}};
use axum::{extract::State, http::StatusCode};

pub fn build_router(schema: AppSchema, ready: Arc<AtomicBool>) -> Router {
    Router::new()
        .route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(ready)
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}

/// /health — liveness probe. Returns 200 when the process is alive.
/// No LMDB access; no state check. (OPS-01)
async fn health_handler() -> StatusCode {
    StatusCode::OK
}

/// /ready — readiness probe. Returns 200 when LMDB env is open and
/// the comparator self-check has passed; 503 otherwise. (OPS-01)
async fn ready_handler(State(ready): State<Arc<AtomicBool>>) -> StatusCode {
    if ready.load(Ordering::Acquire) {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    }
}
```

```rust
// main.rs additions — set ready AFTER all gates pass, BEFORE build_router
let ready = Arc::new(AtomicBool::new(false));

// ... existing gate code (env open, meta, self-check) ...

// Gates passed — mark service as ready (OPS-01).
ready.store(true, Ordering::Release);
tracing::info!("startup gates passed — service is ready");

let router = build_router(schema, Arc::clone(&ready));
```

**Source:** [ASSUMED — axum 0.8 State extractor pattern; stable API]

### StatsResult Extension for OPS-04

```rust
// types.rs: extend StatsResult
#[derive(SimpleObject)]
pub struct StatsResult {
    pub event_count: i64,
    pub max_lev_id: i64,
    pub db_version: i32,
    /// Pinned strfry version string from config (e.g. "dockurr/strfry@sha256:545555da...").
    /// Surfaces the expected image alongside the detected dbVersion so operators spot drift.
    pub pinned_strfry_version: String,
}

// schema.rs / AppState: add the field
pub struct AppState {
    pub env: heed::Env,
    pub dict_cache: Arc<DictCache>,
    pub meta: MetaRecord,
    pub pinned_strfry_version: String,  // new — from config
}

// main.rs: populate
let app_state = AppState {
    env: env.clone(),
    dict_cache,
    meta: meta.clone(),
    pinned_strfry_version: cfg.pinned_strfry_version.clone(),
};

// resolvers.rs: read_stats signature change + usage
fn read_stats(env: &heed::Env, db_version: u32, pinned_strfry_version: String)
    -> Result<StatsResult, QueryError>
{
    // ... existing code ...
    Ok(StatsResult {
        event_count,
        max_lev_id,
        db_version: db_version as i32,
        pinned_strfry_version,
    })
}

// stats resolver: update spawn_blocking call
let pinned = state.pinned_strfry_version.clone();
tokio::task::spawn_blocking(move || read_stats(&env, db_version, pinned))
```

**Source:** [ASSUMED — async-graphql SimpleObject field addition; stable pattern]

---

## OBS-01 and PORT-01 Traceability Reconciliation

The CONTEXT.md notes: "OBS-01, PORT-01 appear in REQUIREMENTS.md body but are missing from the traceability table — determine whether they belong to the hardening scope."

**Finding:** These belong to **v2 requirements**, NOT Phase 5 scope:
- **OBS-01** (`Prometheus /metrics endpoint`) — listed under `## v2 Requirements / Observability`. It does not belong in Phase 5. The traceability table in REQUIREMENTS.md only covers v1 requirements; the fact that OBS-01 is missing from the table is correct because v2 requirements are not yet planned. The traceability coverage report ("v1 requirements: 25 total, mapped: 25, unmapped: 0") confirms all v1 requirements are accounted for.
- **PORT-01** (`Cross-architecture / big-endian support`) — listed under `## v2 Requirements / Portability`. Same conclusion: not a Phase 5 concern.

**Action for the planner:** No traceability fix is needed for these IDs. They are correctly positioned as deferred v2 requirements. The traceability table only covers v1 requirements by design. No code changes needed; just a note in the plan that these are out of scope.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Docker | Dockerfile build, CI fixture gen | ✓ | Confirmed (STATE.md: dockurr/strfry:1.1.0 pre-pulled) | — |
| GitHub Actions | CI workflow | Unknown — no .github/workflows/ exists yet | — | Local `cargo test` manually |
| Alpine `lmdb-dev` | Docker builder (lmdb.h for build.rs) | ✓ (standard Alpine package) | In builder only | Use `apt-get install liblmdb-dev` on ubuntu-latest runner |
| Alpine `g++` | Docker builder (golpe_comparators.cpp) | ✓ (standard Alpine package) | In builder only | Use `apt-get install g++` on ubuntu-latest runner |
| `wget` | docker-compose healthcheck | ✓ on Alpine | Built-in Alpine | Use `curl` if Alpine wget unavailable (unlikely) |

**Missing dependencies with no fallback:** none.

**Missing dependencies with fallback:**
- GitHub Actions: no existing workflow. The CI workflow is a new file to create. Fallback: run `cargo test --all-targets` locally.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hardcoded readiness (server only starts after gates pass — no explicit `/ready`) | `Arc<AtomicBool>` + dedicated `/ready` endpoint | This phase | Enables orchestrators to probe readiness independently of liveness |
| No Docker packaging | Multi-stage Alpine Dockerfile | This phase | Production deployment becomes reproducible |
| Committed fixture only | CI-regenerated fixture from pinned strfry digest | This phase | Proves fixture is reproducible; guards against strfry schema drift |

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `rust:1.89-alpine3.21` is the current stable tag for Alpine-based Rust builder | Pattern 3 | Wrong tag fails docker build; fix: use `rust:alpine` and pin to digest |
| A2 | axum 0.8 `State` extractor for `Arc<AtomicBool>` requires no additional feature flags | Pattern 1 | Compile error; fix: check axum docs |
| A3 | `lmdb-sys` (transitively via heed 0.22.1) builds liblmdb from vendored source, not the system library | Pattern 3 / Pitfall 3 | Runtime binary depends on `liblmdb.so.0`; fix: add `lmdb` to runtime apk add |
| A4 | `wget` is present in the minimal `alpine:3.21` runtime image | Pattern 4 | Docker healthcheck test fails; fix: use `curl` or add `wget` to runtime apk |
| A5 | `dtolsma/rust-toolchain@v1` is the correct GitHub Actions action name for Rust toolchain setup | Pattern 5 | CI workflow fails to install Rust; fix: use `actions-rs/toolchain@v1` or equivalent |
| A6 | async-graphql auto-renames `pinned_strfry_version` (snake_case) to `pinnedStrfryVersion` (camelCase) in the SDL | Code example / OPS-04 | Field name mismatch in schema; fix: confirm with async-graphql camelCase convention |

---

## Open Questions

1. **lmdb-sys link mode (static vs dynamic)**
   - What we know: heed/lmdb-sys builds; CLAUDE.md says static link is achievable with `LMDB_STATIC=1` or heed's default Cargo feature.
   - What's unclear: Whether the current Cargo.lock resolves lmdb-sys to its vendored-source build or a system library link.
   - Recommendation: Run `ldd target/x86_64-unknown-linux-musl/release/lmdb2graphql` in the Docker builder before final Dockerfile commit. If liblmdb appears, add `ENV LMDB_STATIC=1` to the builder stage.

2. **CI action name for Rust toolchain**
   - What we know: The standard GitHub Actions Rust setup action has existed for years; `actions-rs` is widely used.
   - What's unclear: The exact current canonical action — `dtolsma/rust-toolchain@v1` vs `actions-rs/toolchain@v1` vs the official `rust-lang/rust` action.
   - Recommendation: Use `rustup` directly in the `run` step as a fallback: `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable`.

3. **0x01 fixture file vs. synthetic round-trip for OPS-03**
   - What we know: OPS-03 says "asserts LMDB2GraphQL (a) decodes both 0x00 and 0x01 payloads." The existing `payload_test.rs` synthetic round-trip satisfies this.
   - What's unclear: Whether the acceptance test requires a genuine LMDB fixture with 0x01 records (produced by strfry compact), or whether the synthetic round-trip suffices.
   - Recommendation: Treat the existing `payload_test.rs` round-trip test as the OPS-03 0x01 gate. Document this explicitly in the PLAN so the verifier knows what "0x01 asserted" means. If a full compaction-produced fixture is later required, defer it to v2.

---

## Security Domain

`security_enforcement: true`, `security_asvs_level: 1` in config.json. Phase 5 adds HTTP endpoints and a Docker deployment surface.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | `/health` and `/ready` are unauthenticated intentionally — internal probes only |
| V3 Session Management | No | No sessions |
| V4 Access Control | Partial | Port binding: publish only on loopback (`127.0.0.1:8080:8080`) in compose; document explicitly |
| V5 Input Validation | No | `/health` and `/ready` take no input |
| V6 Cryptography | No | No new crypto paths |

### Known Threat Patterns for This Phase

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Accidental wide binding of unauthenticated GraphQL endpoint | Information disclosure | Loopback default in config; docker-compose publish `127.0.0.1:8080:8080` not `0.0.0.0:8080:8080`; warn in logs on non-loopback bind (already in main.rs) |
| `/ready` returning 503 triggers unwanted Docker container restart | Availability | Use `/health` (not `/ready`) for docker healthcheck; set `start_period` |
| Leaked strfry digest/version in stats | Information disclosure | Acceptable — `pinned_strfry_version` exposes which strfry version is in use; this is internal operational data, not a secret. The existing CR-01 warning in main.rs already addresses public exposure of the endpoint itself. |

---

## Sources

### Primary (CITED — codebase reading)
- `src/main.rs` — startup gate chain, logging pattern, AppState construction
- `src/server.rs` — existing `build_router(schema)` signature and axum 0.8 routing pattern
- `src/config.rs` — `Config` struct, `pinned_strfry_version`, `bind_address` default
- `src/lmdb/self_check.rs` — `run_comparator_self_check` public API, doc comment referencing Phase 5 `/ready`
- `src/graphql/resolvers.rs` — `read_stats`, `StatsResult`, existing `stats` resolver pattern
- `src/graphql/types.rs` — `StatsResult`, `AppState` structure
- `build.rs` — C++ golpe shim build, lmdb.h probe paths (pkg-config, Homebrew, `/usr/include`)
- `docker-compose.strfry.yml` — volume pattern, `deepfry-net` external network pattern
- `CLAUDE.md` — Alpine static-binary RUSTFLAGS pattern, locked stack
- `Cargo.toml` — confirmed axum 0.8.9, tokio 1.52.3, async-graphql 7.2.1 in use
- `STATE.md` — rust-toolchain.toml pending fix, strfry pinned digest
- `REQUIREMENTS.md` — OPS-01..OPS-04 definitions; OBS-01/PORT-01 in v2 requirements

### Secondary (ASSUMED — training knowledge, not verified this session)
- axum 0.8 `State` extractor API for `Arc<AtomicBool>`
- Alpine package names: `musl-dev`, `lmdb-dev`, `g++`, `pkgconfig`
- docker-compose healthcheck `start_period` behavior
- GitHub Actions Rust toolchain action name

---

## Metadata

**Confidence breakdown:**
- Health/readiness axum pattern: HIGH — directly derivable from existing server.rs code + axum 0.8 documented patterns
- Docker Alpine static build: MEDIUM — CLAUDE.md documents the approach; lmdb-sys link mode is an open question
- docker-compose co-location: HIGH — directly modeled on existing docker-compose.strfry.yml
- CI fixture generation: MEDIUM — GitHub Actions is new for this repo; 0x01 fixture producibility is a known complexity

**Research date:** 2026-06-13
**Valid until:** 2026-07-13 (stable libraries; no fast-moving dependencies)
