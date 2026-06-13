# Phase 5: Hardening & Docker Packaging — Pattern Map

**Mapped:** 2026-06-13
**Files analyzed:** 8 new/modified files
**Analogs found:** 8 / 8

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `spam/src/server.rs` (modify) | middleware/router | request-response | `spam/src/server.rs` (self) | exact |
| `spam/src/main.rs` (modify) | config/startup | request-response | `spam/src/main.rs` (self) | exact |
| `spam/src/graphql/schema.rs` (modify) | config/state | request-response | `spam/src/graphql/schema.rs` (self) | exact |
| `spam/src/graphql/types.rs` (modify) | model | request-response | `spam/src/graphql/types.rs` (self) | exact |
| `spam/src/graphql/resolvers.rs` (modify) | controller | request-response | `spam/src/graphql/resolvers.rs` (self) | exact |
| `spam/Dockerfile` (new) | config/build | batch | `Dockerfile.strfry` | role-match |
| `docker-compose.lmdb2graphql.yml` (new) | config/deploy | request-response | `docker-compose.strfry.yml` | exact |
| `.github/workflows/lmdb2graphql.yml` (new) | config/CI | batch | none in repo | no analog |
| `spam/tests/generate_fixture.sh` (new) | utility/CI | batch | none in repo | no analog |

---

## Pattern Assignments

### `spam/src/server.rs` — Add `/health` and `/ready` routes (OPS-01)

**Analog:** `spam/src/server.rs` (self — extend existing `build_router`)

**Existing imports pattern** (`src/server.rs` lines 24-31):
```rust
use async_graphql::http::GraphiQLSource;
use async_graphql_axum::GraphQL;
use axum::{
    response::{Html, IntoResponse},
    routing::get,
    Router,
};
use tower_http::limit::RequestBodyLimitLayer;
```

**Add to imports** — new imports required for health/readiness:
```rust
use axum::{extract::State, http::StatusCode};
use std::sync::{Arc, atomic::{AtomicBool, Ordering}};
```

**Existing router pattern** (`src/server.rs` lines 49-62):
```rust
pub fn build_router(schema: AppSchema) -> Router {
    Router::new()
        .route(
            "/graphql",
            get(graphiql).post_service(GraphQL::new(schema)),
        )
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}
```

**Modified router pattern — extend signature and add routes:**
```rust
pub fn build_router(schema: AppSchema, ready: Arc<AtomicBool>) -> Router {
    Router::new()
        .route("/graphql", get(graphiql).post_service(GraphQL::new(schema)))
        .route("/health", get(health_handler))
        .route("/ready", get(ready_handler))
        .with_state(ready)
        .layer(RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES))
}
```

CRITICAL: `.layer(RequestBodyLimitLayer::new(...))` must remain the outermost layer — after `.with_state()`. `.with_state()` must precede `.layer()`. This matches the existing axum 0.8 layering pattern in server.rs.

**New handlers to append (no analog — new code):**
```rust
/// /health — liveness probe. Always 200 when the process is alive. (OPS-01)
/// No LMDB access; no state check.
async fn health_handler() -> StatusCode {
    StatusCode::OK
}

/// /ready — readiness probe. 200 after LMDB env open + comparator self-check pass. (OPS-01)
/// 503 before gates pass.
async fn ready_handler(State(ready): State<Arc<AtomicBool>>) -> StatusCode {
    if ready.load(Ordering::Acquire) {
        StatusCode::OK
    } else {
        StatusCode::SERVICE_UNAVAILABLE
    }
}
```

---

### `spam/src/main.rs` — Wire `Arc<AtomicBool>` readiness flag (OPS-01)

**Analog:** `spam/src/main.rs` (self — extend existing startup gate chain)

**Existing startup gate chain** (`src/main.rs` lines 20-116) — the sequence that defines "ready":
```rust
// 3. Open LMDB env read-only
let env = lmdb::env::open_read_only_env(&cfg.strfry_db_path, cfg.map_size)
    .context("open strfry LMDB env")?;

// 4. Read Meta and run the version + endianness gates (fail-closed).
let meta = lmdb::meta::read_meta(&env).context("read Meta from strfry LMDB")?;
lmdb::meta::assert_db_version(&meta).with_context(|| { ... })?;
lmdb::meta::assert_endianness(&meta).context("endianness gate failed")?;

// 6. Run comparator self-check (fail-closed)
let golden = lmdb::self_check::GoldenVectors::load_committed()
    .context("load committed golden vectors for self-check")?;
lmdb::self_check::run_comparator_self_check(&env, &golden)
    .context("comparator self-check failed ...")?;
```

**Existing `build_router` call** (`src/main.rs` line 87):
```rust
let router = build_router(schema);
```

**Pattern to insert — add `Arc<AtomicBool>` after gates, before router:**
```rust
use std::sync::{Arc, atomic::{AtomicBool, Ordering}};

// ... (after all gates pass at line ~77) ...

// Gates passed — mark service as ready (OPS-01).
let ready = Arc::new(AtomicBool::new(false));
ready.store(true, Ordering::Release);
tracing::info!("startup gates passed — service is ready");

// (line ~87 — update build_router call)
let router = build_router(schema, Arc::clone(&ready));
```

**Existing tracing pattern** (`src/main.rs` lines 29-32, 59-64):
```rust
tracing::info!(
    version = env!("CARGO_PKG_VERSION"),
    "lmdb2graphql starting"
);
// structured key=value pairs, not string formatting:
tracing::info!(
    db_version = meta.db_version,
    pinned_strfry_version = %cfg.pinned_strfry_version,
    "Meta gates passed — dbVersion verified"
);
```

**Existing AppState construction** (`src/main.rs` lines 81-86):
```rust
let app_state = AppState {
    env: env.clone(),
    dict_cache,
    meta: meta.clone(),
};
```
Extend to add `pinned_strfry_version: cfg.pinned_strfry_version.clone()` (OPS-04).

---

### `spam/src/graphql/schema.rs` — Add `pinned_strfry_version` to `AppState` (OPS-04)

**Analog:** `spam/src/graphql/schema.rs` (self)

**Existing `AppState` struct** (`src/graphql/schema.rs` lines 31-42):
```rust
#[derive(Clone)]
pub struct AppState {
    /// The opened strfry LMDB environment (read-only). Cheap to clone (internal refcount).
    pub env: heed::Env,

    /// Cached zstd dictionaries shared across all resolvers and queries.
    pub dict_cache: Arc<DictCache>,

    /// Parsed Meta record from LMDB — carries `db_version` for the `stats` resolver (D-09).
    pub meta: MetaRecord,
}
```

**Pattern — add one field (mirrors the existing meta pattern):**
```rust
#[derive(Clone)]
pub struct AppState {
    pub env: heed::Env,
    pub dict_cache: Arc<DictCache>,
    pub meta: MetaRecord,
    /// Pinned strfry Docker image reference from config (e.g. "dockurr/strfry@sha256:545555da...").
    /// Surfaced by the `stats` resolver alongside `db_version` for operator drift detection (OPS-04).
    pub pinned_strfry_version: String,
}
```

`AppState` already derives `Clone` — `String` is `Clone`, no additional derives needed.

---

### `spam/src/graphql/types.rs` — Extend `StatsResult` with `pinned_strfry_version` (OPS-04)

**Analog:** `spam/src/graphql/types.rs` (self)

**Existing `StatsResult`** (`src/graphql/types.rs` lines 118-130):
```rust
#[derive(SimpleObject)]
pub struct StatsResult {
    /// Total event count from `EventPayload` LMDB stat (renamed to `eventCount`).
    pub event_count: i64,

    /// Largest levId in `EventPayload` — 0 when the DB is empty (renamed to `maxLevId`).
    pub max_lev_id: i64,

    /// strfry `Meta.dbVersion`. Must be 3; verified by startup gate (renamed to `dbVersion`).
    pub db_version: i32,
}
```

**Pattern — add one field (pure additive; no breaking change):**
```rust
#[derive(SimpleObject)]
pub struct StatsResult {
    pub event_count: i64,
    pub max_lev_id: i64,
    pub db_version: i32,
    /// Pinned strfry image from config (e.g. "dockurr/strfry@sha256:545555da...").
    /// async-graphql renames this to `pinnedStrfryVersion` in the SDL (camelCase convention).
    /// Surfaces the expected image alongside `dbVersion` so operators spot version drift (OPS-04).
    pub pinned_strfry_version: String,
}
```

`async-graphql` auto-renames `pinned_strfry_version` → `pinnedStrfryVersion` in the GraphQL SDL (same camelCase convention as `event_count` → `eventCount`, confirmed by existing fields lines 34-35 doc comment).

---

### `spam/src/graphql/resolvers.rs` — Extend `stats` resolver for OPS-04

**Analog:** `spam/src/graphql/resolvers.rs` (self)

**Existing `stats` resolver** (`src/graphql/resolvers.rs` lines 178-196):
```rust
async fn stats(&self, ctx: &Context<'_>) -> GqlResult<StatsResult> {
    let state = ctx.data_unchecked::<AppState>();
    let env = state.env.clone();
    let db_version = state.meta.db_version;

    tokio::task::spawn_blocking(move || read_stats(&env, db_version))
        .await
        .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
        .map_err(map_query_error)
}
```

**Existing `spawn_blocking` clone-before-closure pattern** (`src/graphql/resolvers.rs` lines 87-88):
```rust
let env = state.env.clone();
let dict_cache = Arc::clone(&state.dict_cache);
```

**Pattern — add `pinned_strfry_version` clone before closure:**
```rust
async fn stats(&self, ctx: &Context<'_>) -> GqlResult<StatsResult> {
    let state = ctx.data_unchecked::<AppState>();
    let env = state.env.clone();
    let db_version = state.meta.db_version;
    let pinned = state.pinned_strfry_version.clone();  // clone before closure (Pitfall 1)

    tokio::task::spawn_blocking(move || read_stats(&env, db_version, pinned))
        .await
        .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
        .map_err(map_query_error)
}
```

The private `read_stats` function (not shown in excerpts above) must also be updated to accept `pinned_strfry_version: String` and include it in the returned `StatsResult`. Copy the signature extension pattern from any existing helper (e.g. `build_nostr_filter` at line 237).

---

### `spam/Dockerfile` — Alpine multi-stage static binary (OPS-02)

**Analog:** `/Users/g/git/deepfry/Dockerfile.strfry` (role-match: multi-stage Docker build)

**Existing Go multi-stage pattern** (`Dockerfile.strfry` lines 1-17):
```dockerfile
FROM golang:1.24-alpine AS plugin-builder
WORKDIR /build
COPY whitelist-plugin/ .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo \
    -ldflags "-w -s -extldflags '-static'" \
    -tags netgo \
    -o /whitelist ./cmd/whitelist

FROM dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5
COPY --from=plugin-builder /whitelist /app/plugins/whitelist
```

**Rust equivalent — critical differences from Go pattern:**

1. Rust uses `RUSTFLAGS=-C target-feature=+crt-static` instead of CGO_ENABLED=0.
2. The C++ shim (`reference/golpe_comparators.cpp`) requires `g++` and `lmdb-dev` in builder.
3. `rust-toolchain.toml` pins `stable-x86_64-apple-darwin` — MUST be deleted in Docker builder before `cargo build` (Pitfall 1 from RESEARCH.md).
4. Target is `x86_64-unknown-linux-musl` (Alpine default for `rust:alpine`).

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

# CRITICAL: rust-toolchain.toml pins "stable-x86_64-apple-darwin" which is
# invalid on Linux. Remove it so rustup uses the stable Linux toolchain that
# ships with this image. (RESEARCH.md Pitfall 1)
RUN rm -f rust-toolchain.toml

ENV RUSTFLAGS="-C target-feature=+crt-static"
RUN cargo build --release --target x86_64-unknown-linux-musl

# Stage 2 — minimal runtime
FROM alpine:3.21

# ca-certificates for future TLS; lmdb NOT needed (binary is statically linked).
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /src/target/x86_64-unknown-linux-musl/release/lmdb2graphql /app/lmdb2graphql

EXPOSE 8080
ENTRYPOINT ["/app/lmdb2graphql"]
```

**Verification step (post-build, not in Dockerfile):** Run `ldd target/x86_64-unknown-linux-musl/release/lmdb2graphql` in the builder — if `liblmdb.so.0` appears, add `ENV LMDB_STATIC=1` to the builder stage (RESEARCH.md Pitfall 3 / Open Question 1).

---

### `docker-compose.lmdb2graphql.yml` — Co-located service (OPS-02)

**Analog:** `/Users/g/git/deepfry/docker-compose.strfry.yml` (exact match: same stack, same volume pattern, same network)

**Existing strfry volume pattern** (`docker-compose.strfry.yml` lines 11-14):
```yaml
volumes:
  - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db
  - ./config/strfry/strfry.conf:/etc/strfry.conf:ro
```

**Existing network pattern** (`docker-compose.strfry.yml` lines 48-51):
```yaml
networks:
  deepfry-net:
    external: true
    name: deepfry-net
```

**Pattern — new file following strfry conventions exactly:**
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
      # Operator-supplied config. bind_address MUST be "0.0.0.0:8080" inside Docker (RESEARCH.md Pitfall 2).
      - ${LMDB2GRAPHQL_CONFIG:-./config/lmdb2graphql.yaml}:/root/deepfry/lmdb2graphql.yaml:ro
    ports:
      - "127.0.0.1:8080:8080"   # Loopback-only publish (CR-01 / ASVS V4)
    healthcheck:
      # Use /health (liveness), NOT /ready — /ready returning 503 during startup
      # triggers Docker restart loop (RESEARCH.md Pitfall 5 / Anti-Pattern).
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

**Key differences from strfry pattern:**
- Volume mount appends `:ro` (read-only — core safety invariant).
- `ports:` publishes on `127.0.0.1` only (CR-01).
- `healthcheck:` added (strfry omits this; lmdb2graphql needs it for orchestrator integration).
- No `command:` or `working_dir:` overrides (unlike strfry-quarantine).

**Operator note:** The config file at `./config/lmdb2graphql.yaml` must set `bind_address: "0.0.0.0:8080"` (not the loopback default) for the container port to be reachable. Document this prominently in `config/lmdb2graphql.yaml.example`.

---

### `.github/workflows/lmdb2graphql.yml` — CI correctness gate (OPS-03)

**No analog** — no existing GitHub Actions workflows in the repo. Create `.github/workflows/` directory; this is the first workflow.

**Pattern source:** RESEARCH.md Pattern 5 + existing `cargo test` conventions from CLAUDE.md.

**Existing test command from CLAUDE.md:**
```bash
make test   # → cargo test (-short)
# Integration/all targets:
cargo test --all-targets
```

**CI workflow — modeled on RESEARCH.md Pattern 5 with corrections:**

```yaml
name: lmdb2graphql CI

on:
  push:
    paths: ['spam/**', '.github/workflows/lmdb2graphql.yml']
  pull_request:
    paths: ['spam/**', '.github/workflows/lmdb2graphql.yml']

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install build dependencies
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y --no-install-recommends liblmdb-dev g++ pkg-config

      - name: Install Rust stable
        run: |
          curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable
          echo "$HOME/.cargo/bin" >> $GITHUB_PATH

      - name: Pull pinned strfry image
        run: |
          docker pull dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5

      - name: Generate fixture from pinned strfry
        working-directory: spam
        run: bash tests/generate_fixture.sh

      - name: Run tests (comparator + payload decode gate — OPS-03)
        working-directory: spam
        env:
          RUST_LOG: "error"
        run: cargo test --all-targets -- --nocapture

      - name: Clippy
        working-directory: spam
        run: cargo clippy -- -D warnings
```

NOTE on rust-toolchain.toml: On the ubuntu-latest runner, `rust-toolchain.toml` specifies `stable-x86_64-apple-darwin` — this will fail. The CI workflow must either delete the file before running cargo commands, or `rustup override set stable` after install. The safest approach: add a step `rm -f spam/rust-toolchain.toml` before the test step.

---

### `spam/tests/generate_fixture.sh` — Fixture generation script (OPS-03)

**No close analog** — no existing fixture-generation scripts. The existing committed fixture at `tests/fixture/data.mdb` was produced manually (per RESEARCH.md). This script automates that process.

**Related existing files to understand:**
- `spam/tests/fixture/seed_events.jsonl` — 11 seed events (already exists; use as-is)
- `spam/tests/fixture/data.mdb` + `lock.mdb` — committed fixture (regenerated by this script)
- `spam/tests/fixture/golden_vectors/` — 6 JSON files committed alongside fixture

**Script structure (no analog — new pattern):**
```bash
#!/usr/bin/env bash
# generate_fixture.sh — regenerate tests/fixture/data.mdb from the pinned strfry image.
# Pinned digest must match Dockerfile.strfry and config.rs pinned_strfry_version.
set -euo pipefail

STRFRY_IMAGE="dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5"
FIXTURE_DIR="$(cd "$(dirname "$0")/fixture" && pwd)"
TMPDB="$(mktemp -d)"

# 1. Start strfry with empty DB
docker run -d --name strfry-fixture-gen \
    -v "$TMPDB:/app/strfry-db" \
    "$STRFRY_IMAGE" relay &
STRFRY_PID=$!
sleep 2  # wait for relay to be ready

# 2. Inject seed events
# (strfry import or nostr publish via wscat/websocat — depends on available tools)
# ...

# 3. Stop strfry and copy LMDB files
docker stop strfry-fixture-gen
docker cp strfry-fixture-gen:/app/strfry-db/data.mdb "$FIXTURE_DIR/data.mdb"
docker cp strfry-fixture-gen:/app/strfry-db/lock.mdb "$FIXTURE_DIR/lock.mdb"
docker rm strfry-fixture-gen

rm -rf "$TMPDB"
echo "Fixture regenerated from pinned strfry image."
```

CRITICAL: inject seed events in deterministic order (RESEARCH.md Pitfall 4 — levId is monotonic; different insertion order → different golden vectors). Regenerate to a temp location first; copy into place only after `cargo test` passes.

---

## Shared Patterns

### Tracing (structured logging)
**Source:** `spam/src/main.rs` lines 23-27, 59-64
**Apply to:** Any new startup log lines in main.rs

```rust
tracing::info!(
    key = value,                    // typed structured fields
    string_field = %some_string,    // Display-format for strings
    "human readable message"
);
```

Pattern: structured key=value pairs, not `format!()` interpolation in the message. JSON output (`tracing_subscriber::fmt().json()`) for Docker.

### `anyhow` fail-closed startup
**Source:** `spam/src/main.rs` lines 35, 44, 49-54, 70-71
**Apply to:** Any new startup gate steps

```rust
let thing = do_something().context("human description of what failed")?;
// or with dynamic context:
something().with_context(|| format!("gate failed (pinned strfry: {})", cfg.pinned_strfry_version))?;
```

Pattern: every fallible startup step uses `.context(...)` so the error chain is informative. `?` propagates to `main` which returns `anyhow::Result<()>` — non-zero exit on any error.

### `spawn_blocking` + clone-before-closure
**Source:** `spam/src/graphql/resolvers.rs` lines 87-96
**Apply to:** Any resolver calling synchronous LMDB or blocking I/O

```rust
let env = state.env.clone();
// Arc::clone for non-Clone shared state
let dict_cache = Arc::clone(&state.dict_cache);
// plain Clone for simple values
let pinned = state.pinned_strfry_version.clone();

tokio::task::spawn_blocking(move || work(&env, &*dict_cache, pinned))
    .await
    .map_err(|e| async_graphql::Error::new(format!("task error: {e}")))?
    .map_err(map_query_error)
```

### `deepfry-net` external network declaration
**Source:** `docker-compose.strfry.yml` lines 48-51; `docker-compose.evtfwd.yml` lines 238-241
**Apply to:** `docker-compose.lmdb2graphql.yml`

```yaml
networks:
  deepfry-net:
    external: true
    name: deepfry-net
```

Every DeepFry compose file declares `deepfry-net` as external with the same `name: deepfry-net`. Do not use `driver: bridge` or omit `name:`.

### Bind-mount volume pattern (not named Docker volumes)
**Source:** `docker-compose.strfry.yml` line 11
**Apply to:** `docker-compose.lmdb2graphql.yml`

```yaml
volumes:
  - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db
```

DeepFry uses host bind-mounts with env-var overrides and `:-` defaults. Not named Docker volumes. The lmdb2graphql mount appends `:ro`.

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `.github/workflows/lmdb2graphql.yml` | config/CI | batch | No existing GitHub Actions workflows in the repo (`.github/` contains only `copilot-instructions.md`) |
| `spam/tests/generate_fixture.sh` | utility | batch | No existing fixture-generation scripts; the committed fixture was produced manually |

For these files, use RESEARCH.md Pattern 5 (CI workflow) and the strfry Docker image conventions directly.

---

## Metadata

**Analog search scope:** `spam/src/`, `spam/tests/`, `/Users/g/git/deepfry/` (root monorepo docker-compose and Dockerfiles)
**Files read:** 10 source files
**Pattern extraction date:** 2026-06-13
