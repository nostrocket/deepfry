# Technology Stack

**Analysis Date:** 2026-06-15

## Languages & Runtimes

**Primary:**
- Go 1.24.1+ — all production subsystems (`event-forwarder`, `whitelist-plugin`, `web-of-trust`, `quarantine-rescuer`)
  - `event-forwarder/go.mod`: `go 1.24.1`, toolchain `go1.24.2`
  - `whitelist-plugin/go.mod`: `go 1.24.2`
  - `web-of-trust/go.mod`: `go 1.24.1`
  - `quarantine-rescuer/go.mod`: `go 1.24.2`
- Rust 2021 edition — lmdb2graphql subsystem (`spam/`)

**Build target:**
- Static Linux/amd64 binaries (`CGO_ENABLED=0`, `-tags netgo`, `-extldflags '-static'`)
- Built for Alpine containers via multi-stage Docker builds
- Rust: `--target x86_64-unknown-linux-musl` with `+crt-static` for static libc

## Frameworks & Libraries

**Nostr Protocol:**
- `github.com/nbd-wtf/go-nostr v0.52.x` — NIP-01 WebSocket relay client, event signing, filter subscriptions; used in all four Go modules
- `github.com/nbd-wtf/go-nostr/nip11` — NIP-11 relay discovery (discover-relays tool)

**Graph Database Client:**
- `github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d` — Dgraph gRPC client; `web-of-trust` only
- `google.golang.org/grpc v1.75.1` — gRPC transport for Dgraph
- `google.golang.org/protobuf v1.36.9` — Protocol Buffers serialization

**Configuration:**
- `github.com/spf13/viper v1.21.0` — YAML config loading; `whitelist-plugin`, `quarantine-rescuer`
- `github.com/spf13/viper v1.18.2` — same library, older pin; `web-of-trust`
- `gopkg.in/yaml.v3 v3.0.1+` — YAML parsing

**GraphQL & Web Framework (Rust):**
- `async-graphql 7.2.1` — GraphQL query execution engine (lmdb2graphql)
- `async-graphql-axum 7.2.1` — axum integration (must stay same minor version as async-graphql)
- `axum 0.8.9` — async HTTP framework (lmdb2graphql)
- `tokio 1.52.3` (full features) — async runtime (lmdb2graphql)
- `tower-http 0.6` with limit feature — request body limit enforcement

**Database (Rust):**
- `heed 0.22.1` — LMDB typed wrapper with Comparator trait, read-only env open
- `lmdb-sys` — Low-level LMDB bindings (vendored, statically linked)

**JSON / Serialization:**
- `github.com/tidwall/gjson v1.18.0` — fast JSON path reads; transitive via go-nostr
- `github.com/json-iterator/go v1.1.12` — faster `encoding/json` drop-in; transitive
- `github.com/mailru/easyjson v0.9.0` — code-gen JSON; transitive
- `serde v1` — Rust serialization framework
- `serde_json v1` — Rust JSON support
- `serde_yaml_ng v0.10` — YAML parsing for lmdb2graphql (maintained fork of dtolnay)

**WebSocket:**
- `github.com/coder/websocket v1.8.x` — WebSocket transport; transitive via go-nostr

**Cryptography:**
- `github.com/btcsuite/btcd/btcec/v2` — secp256k1 elliptic curve; transitive (Nostr key signing)
- `github.com/decred/dcrd/dcrec/secp256k1/v4` — secp256k1 primitives; transitive

**TUI (event-forwarder only):**
- `github.com/rivo/tview v0.42.0` — terminal UI
- `github.com/gdamore/tcell/v2 v2.8.1` — terminal cell library

**Concurrency:**
- `github.com/puzpuzpuz/xsync/v3` — sharded concurrent maps; transitive via go-nostr
- `github.com/sourcegraph/conc` — structured concurrency helpers; transitive via viper
- `go.uber.org/atomic v1.9.0` — atomic counters; web-of-trust
- `tokio::sync::OnceCell` — Rust async-safe one-time initialization

**Tracing & Logging (Rust):**
- `tracing v0.1.44+` — structured tracing (pinned per plan 01-01)
- `tracing-subscriber v0.3.23+` — JSON/pretty output formatting

**Compression & Encoding:**
- `base64 v0.22` — cursor pagination encoding (Rust)
- `zstd v0.13.3` — Zstandard compression (Rust)

**Error Handling (Rust):**
- `thiserror v2` — error types
- `anyhow v1` — error context propagation

**Utilities:**
- `dirs v5.0.1` — home directory resolution (~/deepfry/)

## Build & Tooling

**Build system:**
- `make` — each subsystem has its own `Makefile` with targets: `build`, `test`, `lint`, `lint-fix`, `fmt`, `vet`, `tidy`, `clean`
- `event-forwarder` adds: `build-alpine`, `docker-build`, `test-integration`
- `whitelist-plugin` adds: `bench`, `build-alpine`
- `web-of-trust` adds: `build-crawler`, `build-pubkeys`, `build-discover-relays`, `build-healthcheck`, `build-clusterscan`
- `spam/`: `cargo build --release --target x86_64-unknown-linux-musl`

**Version injection:**
- Go ldflags: `Version`, `Commit`, `Built` injected into `pkg/version` package
- Whitelist server: `-buildvcs=true` stamps git metadata via `runtime/debug.ReadBuildInfo`

**Linting:**
- `golangci-lint` — optional, warns but does not fail

**Testing:**
- Go: `go test` with `-short` flag for unit tests; `-tags=integration` for integration tests
- Rust: `cargo test` for unit tests; integration tests in `spam/tests/`

## Infrastructure

**Containerization:**
- Docker with multi-stage builds
  - Go binaries: builder `golang:1.24-alpine`, runtime `alpine:latest` or `dockurr/strfry:latest`
  - Rust binaries: builder `rust:alpine`, runtime `alpine:3.21`
- `Dockerfile.strfry` — builds whitelist and router plugins, copies into `dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` (pinned)
- `Dockerfile.whitelist-server` — standalone HTTP server binary on `alpine:latest`
- `event-forwarder/Dockerfile` — standalone forwarder binary on `scratch`
- `spam/Dockerfile` — lmdb2graphql binary on `alpine:3.21`

**Orchestration:**
- Docker Compose (three compose files, split by concern):
  - `docker-compose.dgraph.yml` — Dgraph standalone v25.3.0 + Ratel UI + whitelist-server
  - `docker-compose.strfry.yml` — mainline StrFry relay + quarantine StrFry relay
  - `docker-compose.evtfwd.yml` — event-forwarder instances
  - `docker-compose.lmdb2graphql.yml` — lmdb2graphql service (reads strfry LMDB read-only)
- Shared bridge network: `deepfry-net`

**Resource limits (per forwarder container):**
- CPU: 0.5 cores max, 0.1 reserved
- Memory: 128 MB max, 32 MB reserved

**Logging:**
- Docker `json-file` driver, 10 MB max per file, 3 files retained
- Rust: JSON structured logging via `tracing-subscriber`, filter via `RUST_LOG` env var

**Secrets:**
- Environment variables via `.env` file (`.env.example` at repo root)
- Key env vars: `STRFRY_PRIVATE_KEY`, `NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`
- Env vars for event-forwarder: `SOURCE_RELAY_URL`, `DEEPFRY_RELAY_URL`, `NOSTR_SYNC_SECKEY`
- Rust env: `RUST_LOG` for tracing filter

## Configuration Files

**Subsystem configs (in ~/deepfry/):**
- `web-of-trust.yaml` — relay URLs, cluster scan settings
- `whitelist.yaml` — whitelist server config
- `router.yaml` — router plugin config (quarantine forwarding rules)
- `lmdb2graphql.yaml` — strfry DB path, HTTP bind address, map size, pinned StrFry version

**Repo-level configs:**
- `config/strfry/strfry.conf` — StrFry relay settings (plugins, logging, thread counts)
- `config/strfry/strfry-quarantine.conf` — Quarantine relay settings
- `config/dgraph/schema.graphql` — Dgraph Profile schema
- `config/dgraph/seed_data.graphql` — Initial data
- `config/whitelist/whitelist-server.yaml` — Whitelist server mounted config

---

*Stack analysis: 2026-06-15*
