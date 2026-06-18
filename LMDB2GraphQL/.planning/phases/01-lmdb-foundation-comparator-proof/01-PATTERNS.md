# Phase 1: LMDB Foundation & Comparator Proof - Pattern Map

**Mapped:** 2026-06-10
**Files analyzed:** 14 new files (greenfield Rust crate)
**Analogs found:** 6 / 14 — all from parent DeepFry Go stack infra; zero Rust analogs exist in-repo

---

## Context: Greenfield Crate

This is a brand-new Rust crate. There is no existing Rust code anywhere in this repository. All analog
patterns come from the **parent DeepFry Go stack** one directory up (`../`), which establishes conventions
for:
- Makefile target layout and Alpine static-binary builds
- Docker multi-stage build and scratch/alpine runtime images
- docker-compose service definition with `:ro` volume mounts and external network
- `~/deepfry/<component>.yaml` config file convention with `os.UserHomeDir()` + `viper` pattern
- Startup gate structure: parse flags / load config / health-check / run
- Structured JSON logging to stderr

For files where **no in-repo analog exists** (build.rs, heed Comparator trait, FFI declarations,
golden vector test fixtures), the Research doc patterns are the authoritative reference.

---

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `Cargo.toml` | config | — | `../event-forwarder/Makefile` (dependency declarations) | partial |
| `rust-toolchain.toml` | config | — | None in-repo | no analog |
| `build.rs` | build/utility | transform (C++ compile) | None in-repo | no analog |
| `src/main.rs` | binary entrypoint | request-response (startup gate) | `../quarantine-rescuer/cmd/quarantine-rescue/main.go` | role-match |
| `src/config.rs` | config/utility | request-response | `../whitelist-plugin/pkg/config/config.go` | role-match |
| `src/lmdb/mod.rs` | module index | — | None in-repo | no analog |
| `src/lmdb/env.rs` | service | CRUD (read-only LMDB env open) | None in-repo — heed patterns from RESEARCH.md | no analog |
| `src/lmdb/meta.rs` | service | CRUD (read single record) | None in-repo | no analog |
| `src/lmdb/comparators.rs` | utility | transform (FFI bridge) | None in-repo | no analog |
| `src/lmdb/indexes.rs` | service | CRUD (open named sub-DBs) | None in-repo | no analog |
| `src/lmdb/self_check.rs` | utility | batch (scan + assert) | None in-repo | no analog |
| `src/lmdb/types.rs` | model | — | None in-repo | no analog |
| `reference/golpe_comparators.cpp` | utility | transform (C++ FFI) | None in-repo (vendored upstream) | no analog |
| `reference/lmdbxx/lmdb++.h` | config/header | — | None in-repo (vendored upstream) | no analog |
| `reference/golpe.yaml` | config | — | `../config/strfry/strfry.conf` (pinned infra config) | partial |
| `tests/fixture/` (data.mdb, golden_vectors/) | test | batch | None in-repo | no analog |
| `tests/self_check_test.rs` | test | batch | None in-repo | no analog |
| `Makefile` | config/build | — | `../whitelist-plugin/Makefile` | exact |
| `Dockerfile` | config/build | — | `../event-forwarder/Dockerfile` | role-match |
| `docker-compose.lmdb2graphql.yml` (Phase 5, sketch) | config | — | `../docker-compose.strfry.yml` | role-match |
| `~/deepfry/lmdb2graphql.yaml` (runtime, not in repo) | config | — | `../whitelist-plugin/pkg/config/config.go` convention | role-match |

---

## Pattern Assignments

### `Makefile` (config/build)

**Analog:** `../whitelist-plugin/Makefile` (lines 1–177)

This is the only file where a near-exact analog exists. The whitelist-plugin Makefile establishes
the DeepFry Makefile convention: target names, Alpine static build targets, Docker targets,
lint/fmt/test targets, and the `help` target format.

**Standard targets pattern** (from `../whitelist-plugin/Makefile` lines 26–65 and `../event-forwarder/Makefile` lines 26–84):
```makefile
APP=lmdb2graphql
VERSION ?= dev
GIT_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: all build test fmt lint lint-fix clean help build-alpine

all: build

build:
    cargo build --release

test:
    cargo test

fmt:
    cargo fmt

lint:
    cargo clippy -- -D warnings

lint-fix:
    cargo clippy --fix

clean:
    rm -rf target/

build-alpine:
    # Static musl binary — Rust equivalent of CGO_ENABLED=0 + -extldflags '-static'
    RUSTFLAGS="-C target-feature=+crt-static" \
    cargo build --release --target x86_64-unknown-linux-musl
```

**Key deviation from Go sibling Makefiles:** Rust uses `cargo` instead of `go build`; Alpine static
binary uses `--target x86_64-unknown-linux-musl` with `LMDB_STATIC=1` env rather than
`CGO_ENABLED=0`. The `-a -installsuffix cgo -ldflags "-extldflags '-static'"` Go pattern maps to
`RUSTFLAGS="-C target-feature=+crt-static"` in Rust.

**Docker targets pattern** (from `../event-forwarder/Makefile` lines 96–108):
```makefile
DOCKER_IMAGE ?= deepfry/lmdb2graphql
DOCKER_TAG ?= $(VERSION)

docker-build:
    docker build \
        --build-arg VERSION=$(VERSION) \
        --build-arg GIT_COMMIT=$(GIT_COMMIT) \
        --build-arg BUILD_TIME=$(BUILD_TIME) \
        -t $(DOCKER_IMAGE):$(DOCKER_TAG) \
        -t $(DOCKER_IMAGE):latest \
        .
```

---

### `Dockerfile` (build/config)

**Analog:** `../event-forwarder/Dockerfile` (all 83 lines)

**Multi-stage build pattern** (lines 1–18 of event-forwarder/Dockerfile):
```dockerfile
# Stage 1: Build stage
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -installsuffix cgo -tags netgo -trimpath \
    -ldflags="-w -s -extldflags '-static' ..." \
    -o fwd ./cmd/fwd
```

**Rust equivalent for Stage 1:**
```dockerfile
FROM rust:alpine AS builder
# C++ toolchain needed for build.rs compiling golpe_comparators.cpp
RUN apk add --no-cache musl-dev g++ lmdb-dev
WORKDIR /build
COPY Cargo.toml Cargo.lock ./
COPY reference/ ./reference/
COPY src/ ./src/
COPY build.rs ./
ARG VERSION=docker
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN LMDB_STATIC=1 RUSTFLAGS="-C target-feature=+crt-static" \
    cargo build --release --target x86_64-unknown-linux-musl
# Verify static (ldd pattern from event-forwarder/Dockerfile line 40-42):
RUN (ldd target/x86_64-unknown-linux-musl/release/lmdb2graphql 2>&1 | \
     grep -q "not a dynamic executable") || \
    echo "Warning: binary may have dynamic dependencies"
```

**Runtime stage pattern** (event-forwarder/Dockerfile lines 44–83):
```dockerfile
FROM scratch
ARG VERSION=docker
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/target/x86_64-unknown-linux-musl/release/lmdb2graphql /lmdb2graphql
USER 65534:65534
LABEL maintainer="DeepFry Team" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT}" \
    org.opencontainers.image.created="${BUILD_TIME}"
ENTRYPOINT ["/lmdb2graphql"]
```

**Key deviation:** The Rust binary requires a C++ runtime for `golpe_comparators.cpp`. With
`-fno-exceptions -fno-rtti` and static libstdc++, the scratch stage can still be used — verify
via `ldd`. If dynamic libstdc++ cannot be avoided, use `FROM alpine` as runtime instead of
`FROM scratch` and add `libstdc++`.

---

### `src/main.rs` (binary entrypoint, startup gate)

**Analog:** `../quarantine-rescuer/cmd/quarantine-rescue/main.go` (lines 82–99)

The startup gate pattern in DeepFry binaries is: parse args → load config → validate/health check
→ run → exit loudly on any error.

**Startup gate pattern** (quarantine-rescue/main.go lines 82–99):
```go
func main() {
    f := parseFlags()
    if f.showVersion {
        fmt.Printf("quarantine-rescue version=%s commit=%s built=%s\n", Version, Commit, Built)
        return
    }
    logger := newLogger(f.logLevel)
    slog.SetDefault(logger)
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()
    if err := run(ctx, f, logger); err != nil {
        logger.Error("rescue failed", "err", err)
        os.Exit(1)
    }
}
```

**Rust equivalent pattern for `src/main.rs`:**
```rust
fn main() -> anyhow::Result<()> {
    // 1. Initialize tracing (JSON for Docker, pretty for dev)
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();

    // 2. Load config from ~/deepfry/lmdb2graphql.yaml
    let cfg = config::load().context("load config")?;
    tracing::info!(
        strfry_db = %cfg.strfry_db_path.display(),
        pinned_version = %cfg.pinned_strfry_version,
        "config loaded"
    );

    // 3. Open LMDB env (read-only)
    let env = lmdb::env::open_read_only_env(&cfg.strfry_db_path, cfg.map_size)
        .context("open strfry LMDB env")?;

    // 4. Verify Meta (dbVersion gate + endianness gate) — exit loudly on failure
    let meta = lmdb::meta::read_meta(&env).context("read Meta")?;
    lmdb::meta::assert_db_version(&meta)?;
    lmdb::meta::assert_endianness(&meta)?;
    tracing::info!(db_version = meta.db_version, "Meta gates passed");

    // 5. Run comparator self-check — fail closed on mismatch
    lmdb::self_check::run_comparator_self_check(&env, &cfg.fixture_path)
        .context("comparator self-check")?;
    tracing::info!("comparator self-check passed — all 6 indexes verified");

    Ok(())
}
```

**Version logging pattern** (quarantine-rescue/main.go lines 85–87):
```go
// Build metadata injected via ldflags — Rust equivalent: use env!("CARGO_PKG_VERSION")
// or inject via build.rs with:
//   println!("cargo:rustc-env=GIT_COMMIT={}", git_commit);
fmt.Printf("version=%s commit=%s built=%s\n", Version, Commit, Built)
```

**Structured JSON logging to stderr** (quarantine-rescue/main.go lines 67–79):
```go
// Go uses slog.NewJSONHandler(os.Stderr, ...) — Rust equivalent:
// tracing_subscriber::fmt().json().with_writer(std::io::stderr).init()
```

---

### `src/config.rs` (config/utility)

**Analog:** `../whitelist-plugin/pkg/config/config.go` (all 129 lines) — closest match for
the `~/deepfry/<component>.yaml` loading convention.

**Config location convention** (whitelist-plugin/pkg/config/config.go lines 91–104):
```go
// DeepFry convention: config always lives in ~/deepfry/<component>.yaml
func ensureConfigDir() (string, error) {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        return "", fmt.Errorf("could not determine home directory: %w", err)
    }
    configDir := filepath.Join(homeDir, "deepfry")
    if _, err := os.Stat(configDir); os.IsNotExist(err) {
        if err := os.MkdirAll(configDir, 0755); err != nil {
            return "", fmt.Errorf("failed to create config directory %s: %w", configDir, err)
        }
    }
    return configDir, nil
}
```

**Config struct + defaults pattern** (whitelist-plugin/pkg/config/config.go lines 14–62):
```go
type ClientConfig struct {
    ServerURL    string        `mapstructure:"server_url"`
    CheckTimeout time.Duration `mapstructure:"check_timeout"`
}
// Defaults then viper.Unmarshal(&cfg)
```

**Rust equivalent for `src/config.rs`** (pattern to copy, no in-repo Rust analog):
```rust
// ~/deepfry/lmdb2graphql.yaml — Phase 1 minimal config:
// strfry_db_path: /app/strfry-db
// map_size: 10995116277760        # must match strfry.conf mapsize
// pinned_strfry_version: "dockurr/strfry@sha256:<full-digest>"
// pinned_strfry_commit: "<git-sha>"

use serde::Deserialize;
use std::path::PathBuf;

#[derive(Debug, Deserialize)]
pub struct Config {
    pub strfry_db_path: PathBuf,
    #[serde(default = "default_map_size")]
    pub map_size: usize,
    pub pinned_strfry_version: String,
    pub pinned_strfry_commit: String,
}

fn default_map_size() -> usize { 10_995_116_277_760 }

pub fn load() -> anyhow::Result<Config> {
    // NEVER delete or overwrite ~/deepfry/lmdb2graphql.yaml in tests
    // Use tempdir for test config loading (per CLAUDE.md)
    let home = dirs::home_dir().context("cannot determine home dir")?;
    let path = home.join("deepfry").join("lmdb2graphql.yaml");
    let text = std::fs::read_to_string(&path)
        .with_context(|| format!("read config {}", path.display()))?;
    let cfg: Config = serde_yaml::from_str(&text)?;
    Ok(cfg)
}
```

**CLAUDE.md safety rule:** `~/deepfry/` files must never be deleted or overwritten.
Tests must use `tempfile::tempdir()` and write a test config there.

---

### `src/lmdb/env.rs` (service, read-only LMDB open)

**Analog:** None in-repo. Use RESEARCH.md Pattern 1 (verified from heed 0.22.1 source).

**Pattern to implement** (from RESEARCH.md "Opening the strfry env read-only", lines 676–702):
```rust
// Source: [VERIFIED: docs.rs/heed/0.22.1/heed/struct.EnvOpenOptions.html]
// map_size MUST be >= strfry.conf mapsize (10995116277760 = 10 TiB)
// ../config/strfry/strfry.conf line 12: mapsize = 10995116277760
use heed::{EnvOpenOptions, EnvFlags};

pub fn open_read_only_env(db_path: &Path, map_size: usize) -> heed::Result<heed::Env> {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(20)
            .map_size(map_size)
            .flags(EnvFlags::READ_ONLY)
            .open(db_path)
    }
}

// CI/test fixture only (no live strfry):
pub fn open_fixture_env(fixture_path: &Path) -> heed::Result<heed::Env> {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(20)
            .map_size(10_995_116_277_760)
            .flags(EnvFlags::READ_ONLY | EnvFlags::NO_LOCK)
            .open(fixture_path)
    }
}
```

**Critical:** `map_size` value comes from `../config/strfry/strfry.conf` line 12. Never open a
write transaction. `NO_LOCK` is only for CI fixture tests; production uses `READ_ONLY` only.

---

### `src/lmdb/comparators.rs` (utility, FFI bridge)

**Analog:** None in-repo. Net-new pattern — copy from RESEARCH.md Pattern 1 and Pattern 2.

**heed Comparator trait + FFI pattern** (RESEARCH.md lines 710–742):
```rust
// In src/lmdb/comparators.rs — bridge between heed's Comparator trait and C++ FFI

extern "C" {
    // Exported from reference/golpe_comparators.cpp (compiled by build.rs)
    // Wrapper that takes raw ptr+len instead of MDB_val* — avoids exposing lmdb-sys types
    fn lmdb_comparator__StringUint64_safe(
        a_ptr: *const u8, a_len: usize,
        b_ptr: *const u8, b_len: usize,
    ) -> i32;
    fn lmdb_comparator__Uint64Uint64_safe(
        a_ptr: *const u8, a_len: usize,
        b_ptr: *const u8, b_len: usize,
    ) -> i32;
    fn lmdb_comparator__StringUint64Uint64_safe(
        a_ptr: *const u8, a_len: usize,
        b_ptr: *const u8, b_len: usize,
    ) -> i32;
}

// Zero-sized enum is the heed convention for Comparator implementors
pub enum StringUint64Cmp {}
impl heed::Comparator for StringUint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
        // Safety: golpe_comparators.cpp compiled with -fno-exceptions;
        //         abort() on too-short keys (malformed = programming error)
        let r = unsafe {
            lmdb_comparator__StringUint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}

pub enum Uint64Uint64Cmp {}
impl heed::Comparator for Uint64Uint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
        let r = unsafe {
            lmdb_comparator__Uint64Uint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}

pub enum StringUint64Uint64Cmp {}
impl heed::Comparator for StringUint64Uint64Cmp {
    fn compare(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
        let r = unsafe {
            lmdb_comparator__StringUint64Uint64_safe(a.as_ptr(), a.len(), b.as_ptr(), b.len())
        };
        r.cmp(&0)
    }
}
```

**CRITICAL — never panic in Comparator::compare:** A panic across extern "C" is also UB. The
comparator implementations must not call anything that can panic. The FFI call site only panics
if the C++ throws (prevented by `-fno-exceptions`) or if `r.cmp(&0)` panics (it cannot).

---

### `build.rs` (build/utility, C++ compilation)

**Analog:** None in-repo. Net-new. Use RESEARCH.md Pattern 2.

**cc crate build pattern** (RESEARCH.md lines 519–534):
```rust
// build.rs
fn main() {
    // Rerun only when these change
    println!("cargo:rerun-if-changed=reference/golpe_comparators.cpp");
    println!("cargo:rerun-if-changed=reference/lmdbxx/lmdb++.h");

    cc::Build::new()
        .cpp(true)
        .file("reference/golpe_comparators.cpp")
        .include("reference/lmdbxx")          // for lmdb++.h (hoytech/lmdbxx header-only)
        // lmdb.h: try lmdb-sys's OUT_DIR first; fall back to system
        .include(std::env::var("DEP_LMDB_INCLUDE").unwrap_or_default())
        .flag("-std=c++17")                   // lmdbxx requires C++17
        .flag("-fno-exceptions")              // prevent UB from throw across FFI boundary
        .flag("-fno-rtti")                    // reduce binary size
        .compile("golpe_comparators");        // → libgolpe_comparators.a

    // Alpine musl static libstdc++ (LOW confidence — spike required):
    // println!("cargo:rustc-link-lib=static=stdc++");
}
```

**Spike risk (A2/A4):** The lmdb.h include path and musl static libstdc++ linking are not verified
on Alpine. The planner must include a smoke-test build task on the target Alpine image.

---

### `src/lmdb/meta.rs` (service, single-record read)

**Analog:** None in-repo. Net-new. Use RESEARCH.md Pattern 3.

**Meta read pattern** (RESEARCH.md lines 328–344):
```rust
// Meta dbi uses MDB_INTEGERKEY (IntegerComparator) — NOT a golpe custom comparator
use heed::types::{U64, Bytes};
use heed::byteorder::NativeEndian;

pub fn read_meta(env: &heed::Env) -> Result<MetaRecord, LmdbError> {
    let rtxn = env.read_txn()?;
    let meta_db: heed::Database<U64<NativeEndian>, Bytes> = env
        .database_options()
        .types::<U64<NativeEndian>, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name("Meta")
        .open(&rtxn)?
        .expect("Meta sub-DB must exist in strfry env");

    let meta_bytes = meta_db.get(&rtxn, &1u64)?
        .expect("Meta record id=1 must exist");
    // Parse bytes — exact offsets are a spike item (A3):
    // Must read build/golpe.h from pinned strfry to verify field layout
    let db_version = u32::from_ne_bytes(meta_bytes[0..4].try_into()?);
    let endianness = u32::from_ne_bytes(meta_bytes[4..8].try_into()?);
    Ok(MetaRecord { db_version, endianness })
}

pub fn assert_db_version(meta: &MetaRecord) -> Result<(), LmdbError> {
    if meta.db_version != 3 {
        // Exit loudly — fail closed
        return Err(LmdbError::DbVersionMismatch {
            expected: 3,
            actual: meta.db_version,
        });
    }
    Ok(())
}

pub fn assert_endianness(meta: &MetaRecord) -> Result<(), LmdbError> {
    let host_is_little = cfg!(target_endian = "little");
    let db_is_little = meta.endianness == 0;
    if host_is_little != db_is_little {
        return Err(LmdbError::EndiannessMismatch {
            host_little: host_is_little,
            db_little: db_is_little,
        });
    }
    Ok(())
}
```

**WARNING (A3):** Byte offsets `[0..4]` for dbVersion and `[4..8]` for endianness are inferred
from golpe conventions, not verified from the generated struct. The planner must include a task to
read `build/golpe.h` from the pinned strfry commit to confirm field layout before implementing.

---

### `src/lmdb/indexes.rs` (service, open named sub-DBs)

**Analog:** None in-repo. Net-new. Derived from RESEARCH.md Pattern 1 + the index-to-comparator
mapping from spec.md §3 / CONTEXT.md specifics section.

**Index-to-comparator mapping** (CONTEXT.md "Specific Ideas" + RESEARCH.md Pattern 4):
```rust
// In src/lmdb/indexes.rs
// Index → Comparator mapping (authoritative from spec.md §3 / golpe.yaml):
//
//   Event__id          → StringUint64Cmp     (id:32 ‖ created_at:8)
//   Event__pubkey      → StringUint64Cmp     (pubkey:32 ‖ created_at:8)
//   Event__tag         → StringUint64Cmp     (tagName:1 ‖ tagValue:var ‖ created_at:8)
//   Event__kind        → Uint64Uint64Cmp     (kind:8 ‖ created_at:8)
//   Event__pubkeyKind  → StringUint64Uint64Cmp (pubkey:32 ‖ kind:8 ‖ created_at:8)
//   Event__created_at  → IntegerComparator   (MDB_INTEGERKEY, no golpe custom cmp)

use heed::types::Bytes;

pub fn open_event_index_string_uint64(
    env: &heed::Env,
    rtxn: &heed::RoTxn,
    name: &str,
) -> Result<heed::Database<Bytes, Bytes>, LmdbError> {
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<StringUint64Cmp>()
        .name(name)
        .open(rtxn)?
        .ok_or_else(|| LmdbError::SubDbNotFound(name.to_string()))
}
// (similarly for Uint64Uint64Cmp and StringUint64Uint64Cmp variants)
```

**CRITICAL (from RESEARCH.md anti-patterns):**
- Use `.open()` NOT `.create()` — `create()` would use `MDB_CREATE` and create new sub-DBs.
- Every `Event__*` open MUST include the `.key_comparator::<T>()` call or scans are silently wrong.
- `Event__created_at` uses `IntegerComparator`, not a golpe comparator.

---

### `src/lmdb/self_check.rs` (utility, startup gate)

**Analog:** None in-repo. Net-new. This is a reusable function that Phase 5's `/ready` will call.

**Self-check structure** (from RESEARCH.md architecture diagram + D-04/D-05/D-06/D-07):
```rust
// In src/lmdb/self_check.rs
// Run once at startup before serving. Phase 5's /ready endpoint calls this fn directly.
pub fn run_comparator_self_check(env: &heed::Env) -> Result<(), SelfCheckError> {
    // Load golden vectors from tests/fixture/golden_vectors/*.json
    // (embedded at compile time via include_str! or loaded from a path in config)
    for index_name in &ALL_EVENT_INDEXES {
        let golden = load_golden_vector(index_name)?;
        let actual = scan_lev_ids(env, index_name)?;
        if actual != golden.ordered_lev_ids {
            return Err(SelfCheckError::OrderMismatch {
                index: index_name.to_string(),
                expected: golden.ordered_lev_ids,
                actual,
            });
        }
    }
    Ok(())
}

// ALL_EVENT_INDEXES covers all 6 (D-07):
// ["Event__id", "Event__pubkey", "Event__created_at",
//  "Event__kind", "Event__pubkeyKind", "Event__tag"]
```

**Fail-closed contract (D-04):** Any `Err` from `run_comparator_self_check` must cause `main` to
call `std::process::exit(1)` with a clear error message. Never continue past a failed self-check.

---

### `src/lmdb/types.rs` (model)

**Analog:** None in-repo. Simple struct definitions — no complex pattern.

```rust
// In src/lmdb/types.rs
#[derive(Debug, Clone)]
pub struct MetaRecord {
    pub db_version: u32,
    pub endianness: u32,          // 0 = little, 1 = big
    pub negentropy_mod_counter: u64,
}

// LevId is strfry's internal monotonic event identifier
pub type LevId = u64;
```

---

### `reference/golpe_comparators.cpp` (vendored C++ with safety modifications)

**Analog:** None in-repo. Vendored from rasgueadb with required safety modifications.

**Source:** `raw.githubusercontent.com/hoytech/rasgueadb/master/utils.h.tt`

**Required modifications from upstream** (RESEARCH.md lines 508–515 — CRITICAL):
- Replace all `throw hoytech::error(...)` with `std::abort()` — throwing across the Rust
  `extern "C"` FFI boundary is undefined behavior.
- Compile with `-fno-exceptions -fno-rtti` (enforced in build.rs) as defense-in-depth.
- Expose the three functions as `extern "C"` wrapper functions that take `(ptr, len)` pairs
  instead of `MDB_val*` to avoid exposing lmdb-sys types to the Rust linker directly.

**Template from RESEARCH.md lines 426–493** (exact C++ to vendor and modify):
```cpp
// reference/golpe_comparators.cpp
// Vendored from hoytech/rasgueadb @ <pinned commit>
// MODIFICATIONS: throw → std::abort(); added extern "C" safe wrappers
#include "lmdbxx/lmdb++.h"
#include <cstdlib>
#include <cstring>
#include <cstdint>

// [three inline comparator functions from rasgueadb utils.h.tt]
// with throw replaced by: if (a->mv_size < N || b->mv_size < N) std::abort();

extern "C" int lmdb_comparator__StringUint64_safe(
    const uint8_t* a_ptr, size_t a_len,
    const uint8_t* b_ptr, size_t b_len
) {
    MDB_val a = {a_len, (void*)a_ptr};
    MDB_val b = {b_len, (void*)b_ptr};
    return lmdb_comparator__StringUint64(&a, &b);
}
// (similarly for Uint64Uint64_safe, StringUint64Uint64_safe)
```

**Spike item (A7):** `mdb_cmp_memn` used in the original source may not be in the public `lmdb.h`.
If absent, inline the equivalent: `if (a->mv_size != b->mv_size) return a->mv_size < b->mv_size ? -1 : 1; return memcmp(a->mv_data, b->mv_data, a->mv_size);`

---

### `tests/fixture/` (data.mdb, golden_vectors/, PROVENANCE.md)

**Analog:** None in-repo. Net-new fixture infrastructure.

**Golden vector format** (RESEARCH.md lines 745–756):
```json
// tests/fixture/golden_vectors/Event__pubkey.json
{
  "comparator": "StringUint64",
  "index": "Event__pubkey",
  "seed_commit": "sha256:<data.mdb-sha256sum>",
  "ordered_lev_ids": [3, 1, 5, 2, 4, 6]
}
```

**PROVENANCE.md must record** (D-08/D-10/D-12):
- `dockurr/strfry` image tag + full sha256 digest
- strfry git commit SHA
- adversarial seed events design rationale
- sha256sum of committed `data.mdb`
- Instructions to regenerate: `docker run ... strfry import < tests/fixture/seed_events.jsonl`

**Fixture generation CI pattern** (RESEARCH.md lines 604–610):
```bash
# CI: regenerate from pinned image, compare sha256
docker run --rm -v ./ci-fixture:/app/strfry-db \
    dockurr/strfry@sha256:<digest> \
    strfry import < tests/fixture/seed_events.jsonl
sha256sum tests/fixture/data.mdb ci-fixture/data.mdb
# If mismatch: fail CI — strfry version diverged or import is non-deterministic
```

---

### `~/deepfry/lmdb2graphql.yaml` (runtime config, not in repo)

**Analog:** `../config/whitelist/whitelist.yaml` (lines 1–2) — simplest example of the convention.

**Convention:** Two-field minimal config (similar to whitelist client config):
```yaml
# ~/deepfry/whitelist.yaml (2 lines):
server_url: "http://whitelist-server:8081"
check_timeout: 2s
```

**Phase 1 minimal lmdb2graphql.yaml:**
```yaml
# ~/deepfry/lmdb2graphql.yaml
# Introduced in Phase 1 (D-14). Grows in later phases.
strfry_db_path: /app/strfry-db
map_size: 10995116277760        # must match strfry.conf dbParams.mapsize
pinned_strfry_version: "dockurr/strfry@sha256:<full-digest-to-be-filled>"
pinned_strfry_commit: "<strfry-git-sha-to-be-filled>"
```

**CRITICAL:** Never delete or overwrite this file. Tests must use `tempfile::tempdir()` and
write a temporary config there instead of touching `~/deepfry/`. (per CLAUDE.md)

---

### `docker-compose.lmdb2graphql.yml` (config/compose — Phase 5 sketch, D-13 note)

**Analog:** `../docker-compose.strfry.yml` (all 52 lines) — specifically lines 1–18 for the
`:ro` volume mount pattern on the strfry-db volume.

**`:ro` volume mount pattern** (docker-compose.strfry.yml lines 11–12):
```yaml
volumes:
  - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db   # NOTE: no :ro here for strfry itself
```

**For LMDB2GraphQL, the mount MUST be read-only** (per spec + CLAUDE.md):
```yaml
services:
  lmdb2graphql:
    build:
      context: ./spam
      dockerfile: Dockerfile
    container_name: lmdb2graphql
    restart: unless-stopped
    volumes:
      - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db:ro   # :ro — NEVER remove
      - ./spam/config/lmdb2graphql.yaml:/root/deepfry/lmdb2graphql.yaml:ro
    networks:
      - deepfry-net
```

**External network pattern** (docker-compose.strfry.yml lines 48–52):
```yaml
networks:
  deepfry-net:
    external: true
    name: deepfry-net
```

---

### `Cargo.toml` (config)

**Analog:** None in-repo (no existing Rust crates). Use RESEARCH.md "Installation (Phase 1 Cargo.toml)".

**Phase 1 Cargo.toml pattern** (RESEARCH.md lines 116–128):
```toml
[package]
name = "lmdb2graphql"
version = "0.1.0"
edition = "2021"

[dependencies]
heed = "0.22.1"
tracing = "0.1.44"
tracing-subscriber = { version = "0.3.23", features = ["env-filter", "json"] }
thiserror = "2"
anyhow = "1"
serde = { version = "1", features = ["derive"] }
serde_yaml = "0.9"   # or use the `config` crate
dirs = "5"           # for home_dir() — idiomatic Rust replacement for os.UserHomeDir()

[dev-dependencies]
tempfile = "3"

[build-dependencies]
cc = "1"
```

---

### `rust-toolchain.toml` (config)

**Analog:** None in-repo. Standard Rust ecosystem pattern.

```toml
[toolchain]
channel = "stable"
# Minimum version confirmed available: rustc 1.89.0 (2025-08-04)
# from RESEARCH.md Environment Availability table
```

---

## Shared Patterns

### Startup Fail-Closed Gate
**Source:** `../quarantine-rescuer/cmd/quarantine-rescue/main.go` lines 82–99
**Apply to:** `src/main.rs`

The DeepFry binary convention is: any error in startup (config load, version gate, self-check)
calls `os.Exit(1)` with a structured log error. In Rust: propagate via `?` to `main()` returning
`anyhow::Result<()>`; `anyhow` prints the error chain and exits with code 1.
```go
if err := run(ctx, f, logger); err != nil {
    logger.Error("rescue failed", "err", err)
    os.Exit(1)
}
```

### Config File Convention (`~/deepfry/<component>.yaml`)
**Source:** `../whitelist-plugin/pkg/config/config.go` lines 91–104 (ensureConfigDir) + lines 31–62 (LoadClientConfig)
**Apply to:** `src/config.rs`

Pattern: `os.UserHomeDir()` → join `"deepfry"` → join `"<component>.yaml"`. Create dir if absent.
Never delete or overwrite the file (per CLAUDE.md). In tests: use `tempfile::tempdir()`.

### Structured JSON Logging
**Source:** `../quarantine-rescuer/cmd/quarantine-rescue/main.go` lines 67–79 (`newLogger`)
**Apply to:** `src/main.rs`

Go uses `slog.NewJSONHandler(os.Stderr, ...)`. Rust equivalent:
```rust
tracing_subscriber::fmt()
    .json()
    .with_writer(std::io::stderr)
    .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
    .init();
```
For dev pretty output, detect `LOG_FORMAT=pretty` env var.

### Alpine Static Binary Build
**Source:** `../whitelist-plugin/Makefile` lines 44–51 (`build-alpine` target)
**Apply to:** `Makefile` (`build-alpine` target), `Dockerfile` (Stage 1 build command)

Go convention: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -ldflags "... -extldflags '-static'" -tags netgo`.
Rust equivalent: `RUSTFLAGS="-C target-feature=+crt-static" cargo build --release --target x86_64-unknown-linux-musl`.
Note: because Phase 1 uses a C++ build (`build.rs`), `CGO_ENABLED=0` has no Rust equivalent —
instead `LMDB_STATIC=1` and static libstdc++ linking must be verified on the Alpine image.

### Read-Only Docker Volume Mount
**Source:** `../docker-compose.strfry.yml` lines 11–14 (volume mount pattern)
**Apply to:** `docker-compose.lmdb2graphql.yml`

The strfry-db volume MUST be mounted `:ro` for LMDB2GraphQL. The strfry service itself does not
use `:ro` on its own db mount (it writes). Config files use `:ro` in both services.

---

## No Analog Found

Files with no close match in the codebase (planner must use RESEARCH.md patterns instead):

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `rust-toolchain.toml` | config | — | No Rust crates exist in-repo |
| `build.rs` | build/utility | transform | No C/C++ build scripts exist in-repo |
| `src/lmdb/env.rs` | service | CRUD | No heed/LMDB code in-repo; use RESEARCH.md Pattern 1 |
| `src/lmdb/meta.rs` | service | CRUD | No heed code in-repo; use RESEARCH.md Pattern 3 |
| `src/lmdb/comparators.rs` | utility | transform | Novel FFI bridge; use RESEARCH.md Pattern 1+2 |
| `src/lmdb/indexes.rs` | service | CRUD | Novel heed DB open; use RESEARCH.md Pattern 1 |
| `src/lmdb/self_check.rs` | utility | batch | Novel; use RESEARCH.md architecture diagram + D-04..D-07 |
| `src/lmdb/types.rs` | model | — | Simple structs; no analog needed |
| `src/lmdb/mod.rs` | module index | — | Trivial pub-use barrel; no analog needed |
| `reference/golpe_comparators.cpp` | vendor/utility | transform | Upstream source with safety mods; no in-repo precedent |
| `reference/lmdbxx/lmdb++.h` | vendor/header | — | Header-only; just vendor from hoytech/lmdbxx |
| `reference/golpe.yaml` | config | — | Vendor from hoytech/strfry at pinned commit |
| `tests/fixture/` | test | batch | Novel fixture strategy; see RESEARCH.md R4 |
| `tests/self_check_test.rs` | test | batch | Novel; use RESEARCH.md validation architecture |

---

## Spike Items Requiring Human Verification Before Implementation

These are not pattern gaps — they are factual unknowns that must be resolved before writing code.
The planner must create explicit tasks for each:

| Item | Risk | What to Verify |
|------|------|----------------|
| **A3** — Meta struct field offsets | HIGH | Read `build/golpe.h` from pinned strfry to confirm dbVersion at bytes 0..4, endianness at 4..8 |
| **A4** — lmdb.h include path | MEDIUM | In build.rs, check if `DEP_LMDB_INCLUDE` env var is set by lmdb-sys; if not, find lmdb.h via pkg-config or system package |
| **A7** — `mdb_cmp_memn` availability | MEDIUM | Check if `mdb_cmp_memn` appears in lmdb-sys vendored lmdb.h; if not, inline the equivalent `memcmp`-with-size-priority |
| **A2** — musl static libstdc++ | LOW | Build the comparator archive on the target `rust:alpine` image and verify `ldd` shows no dynamic libstdc++ |
| **A5** — `strfry import` byte-determinism | LOW | Run `strfry import < seed_events.jsonl` twice with the pinned image; compare sha256sum of resulting data.mdb |
| **R3** — full strfry image digest | BLOCKING (D-08/D-09) | `docker pull dockurr/strfry:1.1.0` + `docker inspect` for full sha256; update `Dockerfile.strfry` FROM line |

---

## Metadata

**Analog search scope:** `/Users/gareth/git/nostr/deepfry/` (parent stack + all Go sibling subsystems)
**Files scanned:** 10 (Dockerfile.strfry, docker-compose.strfry.yml, docker-compose.evtfwd.yml,
  config/strfry/strfry.conf, event-forwarder/Makefile, event-forwarder/Dockerfile,
  whitelist-plugin/Makefile, whitelist-plugin/pkg/config/config.go,
  web-of-trust/pkg/config/config.go, quarantine-rescuer/cmd/quarantine-rescue/main.go)
**Pattern extraction date:** 2026-06-10
