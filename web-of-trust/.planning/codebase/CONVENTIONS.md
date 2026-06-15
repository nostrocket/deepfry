# Coding Conventions

**Analysis Date:** 2026-06-15

This document covers conventions across the multi-subsystem DeepFry repository: Go modules (web-of-trust, event-forwarder, whitelist-plugin, quarantine-rescuer) and Rust (spam/lmdb2graphql).

## Naming Patterns

### Go — Files

- Lowercase with underscores: `main.go`, `crawler.go`, `chunks.go`, `backoff_test.go`
- Package follows directory name: `package dgraph` in `pkg/dgraph/`
- Test files: `<name>_test.go` (standard Go pattern)
- Integration tests gate with `//go:build integration` directive (see `pkg/dgraph/dgraph_writepath_test.go:1`)

### Go — Functions and Methods

**Exported (public):**
- PascalCase: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`, `Handle()`
- Constructor pattern: `func New(cfg Config) (*Type, error)` (e.g., `NewClient` in `pkg/dgraph/dgraph.go:34`)

**Unexported (private):**
- camelCase: `normalizeSeedPubkeys()`, `cleanSubscribeError()`, `markDead()`, `quorumReached()`

**Methods:**
- Receiver as single letter lowercase: `(c *Client)`, `(e *subscriptionError)`, `(f *flags)`
- Grouped with their type in same file

### Go — Variables and Constants

**Variables (exported):**
- PascalCase: `RelayURLs`, `DgraphAddr`, `Timeout`

**Variables (unexported):**
- camelCase: `signerPubkey`, `followeeList`, `dgClient`, `relayState`, `dryRun`

**Constants:**
- UPPERCASE_WITH_UNDERSCORES for package-level: `initialBackoff`, `maxBackoff`, `baseTimeout` (see `pkg/crawler/crawler.go:47-49`)
- Named enums use iota: `classTransport`, `classFilterRej`, `classSubFlap` (see `pkg/crawler/crawler.go:54-59`)

**Types:**
- PascalCase: `Client`, `Crawler`, `Config`, `EjectionThresholds`, `relayState`

**Struct Tags:**
- Config mapping: `mapstructure:"relay_urls"` (snake_case in YAML)
- JSON marshalling: `json:"pubkey"`, `json:"kind3CreatedAt"`
- Serde (Rust): `#[serde(default = "...")]` or `#[serde(derive)]`

**Booleans:**
- Prefix with state: `alive`, `deleted`, `valid`, `dryRun`, `debug`

**Timestamps:**
- Suffix with unit or context: `kind3CreatedAt`, `lastUpdate`, `olderThanUnix`, `lastAttempt`, `nextAttempt`

### Rust — Files and Modules

- Lowercase with underscores: `main.rs`, `config.rs`, `types.rs`, `payload.rs`
- Module hierarchy mirrors `src/` directory structure: `src/lmdb/env.rs` exposed as `lmdb::env`
- Test functions colocated: `#[cfg(test)]` block at end of file (see `spam/src/config.rs:89`)
- Integration tests: separate `tests/` directory (see `spam/tests/scan_test.rs`)

### Rust — Functions and Types

**Functions (exported):**
- snake_case: `load()`, `open_fixture_env()`, `scan_index_bounded()`, `build_schema()`
- Async functions: `async fn` prefix (e.g., `#[tokio::main] async fn main()` in `spam/src/main.rs:39`)

**Types and Structs:**
- PascalCase: `Config`, `AppState`, `AppRouter`, `NostrEvent`
- Derive macros: `#[derive(Debug, Deserialize, Serialize)]`

**Error Types:**
- PascalCase with suffix `Error`: `PayloadError`, `ConfigError`
- Use `thiserror` or `anyhow` (see `spam/Cargo.toml:20-21`)

## Code Style

### Go

**Formatting:**
- Tool: `go fmt` (enforced via Makefile `make fmt`)
- Standard Go formatting rules (tabs = 8 spaces)
- Line length: no strict limit; functions kept readable
- Indentation: tab characters (Go standard)

**Linting:**
- Primary: `golangci-lint` with `.golangci.yml` in event-forwarder
- Linters enabled: errcheck, gosimple, govet, ineffassign, staticcheck, typecheck, unused, gocyclo, revive, gofumpt
- Makefile targets: `make lint`, `make lint-fix` (gracefully handle absence)
- Non-failing in CI; advisory only

**Line Breaks:**
- Imports grouped: stdlib, third-party, local (see `pkg/crawler/crawler.go:3-19`)
- Function bodies: early returns preferred

### Rust

**Formatting:**
- Tool: `rustfmt` (automatically via Rust toolchain)
- Edition: 2021 (see `spam/Cargo.toml:4`)
- Conventions: 4-space indentation, line length ~100 chars (Rust standard)

**Linting:**
- Tool: `cargo clippy` (recommended, not enforced)
- Warnings treated as guidance; no strict CI gate observed

**Comments:**
- Doc comments: `///` for public items (see `spam/src/main.rs:1`)
- Line comments: `//` (see `spam/src/main.rs:41`)
- Doc tests: supported but not heavily used in this codebase

## Import Organization

### Go

**Order:**
1. Standard library (`context`, `encoding/json`, `fmt`, `log`)
2. Third-party packages (`github.com/nbd-wtf/go-nostr`, `google.golang.org/grpc`)
3. Local packages (`web-of-trust/pkg/config`, `web-of-trust/pkg/dgraph`)

Example from `pkg/crawler/crawler.go`:
```go
import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "math"
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "web-of-trust/pkg/config"
    "web-of-trust/pkg/dgraph"

    "github.com/nbd-wtf/go-nostr"
)
```

**Path Aliases:** None detected; direct module imports used.

### Rust

**Order:**
1. Standard library (`std::...`)
2. External crates (`anyhow`, `serde`, `tokio`)
3. Internal crates (`lmdb2graphql::...`)

Example from `spam/src/main.rs:28-37`:
```rust
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use anyhow::Context;
use lmdb2graphql::graphql::schema::{AppState, build_schema};
use lmdb2graphql::lmdb;
use lmdb2graphql::server::{AppRouterState, build_router};
use tokio::sync::OnceCell;
```

## Error Handling

### Go

**Pattern:** Error wrapping with context using `%w` verb

All errors wrapped with context explaining the operation that failed:
```go
if err != nil {
    return fmt.Errorf("query follower failed: %w", err)
}
```

**Error Types:**
- `subscriptionError` — subscription-related failures on relay (see `pkg/crawler/crawler.go:21-26`)
- `transportError` — connection/transport failures (see `pkg/crawler/crawler.go:28-33`)
- `filterRejectionError` — filter-cap rejection (at-cap or floor-reached) (see `pkg/crawler/crawler.go:40-45`)

All implement `Error()` and `Unwrap()` methods for error chain introspection.

**Return Style:**
- Errors always last return value: `(result Type, error)`
- Named return values used where helpful: `(deleted bool, error)`, `([]string, error)`
- Boolean success flag paired with error: `(deleted bool, err error)` (e.g., `GetKind3CreatedAt`)

**No Silent Suppression:**
- Errors returned immediately with context added
- Exception: explicit cases (e.g., config file not found → create with defaults)

### Rust

**Pattern:** `anyhow::Result<T>` for errors with context chain

All errors propagated with context:
```rust
let cfg = load()
    .context("load config from ~/deepfry/lmdb2graphql.yaml")?;
```

**Error Types:**
- `anyhow::Error` for application errors (see `spam/src/main.rs:33`)
- `thiserror` for custom error types with structured messages (see `spam/Cargo.toml:20`)
- `? operator` for early exit with error context propagation

**Fail-Closed Pattern:**
- Startup gates propagate errors via `? operator` → process exits non-zero
- Example: `spam/src/main.rs:19` — LMDB checks fail before server readiness set

## Logging

### Go

**Framework:** `log` standard library (no external logger)

**Levels (implicit via prefix):**
- Info (default): `log.Printf("Connected to relay: %s", url)`
- Warn: `log.Printf("WARN: Failed to connect: %v", err)` (see `pkg/crawler/crawler.go:164`)
- Debug: `log.Printf("DEBUG: Starting AddFollowers for pubkey %s...", pubkey)` (guarded by `if c.debug`)
- Fatal: `log.Fatalf("Failed to create crawler: %v", err)`

**When to Log:**
- Connection lifecycle: connect, disconnect, reconnect, dead, ejected
- Major state changes: relay status transitions, pubkey processing milestones
- Configuration loaded/saved
- Batch processing: pubkeys processed, chunks completed
- Errors with context (always include what failed and why)

**Debug Output:**
- Guarded by `if c.debug { log.Printf(...) }` flag (see `pkg/dgraph/dgraph.go:145`)

**Security:**
- No logging of raw secrets (env vars, private keys, pubkey details in trace mode)
- Pubkeys logged by truncation (first/last N chars) when identifying them

Examples from `pkg/crawler/crawler.go`:
```go
log.Printf("Relay %s ejected (%s %d/%d)", url, class, count, threshold)
log.Printf("Reconnected %d/%d relays, %d removed, %d still dead", ...)
```

### Rust

**Framework:** `tracing` crate for structured logging

**Configuration:**
- Controlled by `RUST_LOG` environment variable (see `spam/src/main.rs:42`)
- JSON output for Docker, pretty-print for dev (see `spam/src/main.rs:43-47`)
- `tracing_subscriber::EnvFilter` for dynamic filtering

**Levels:**
- `tracing::info!()` — startup, config loaded, major milestones
- `tracing::warn!()` — non-loopback bind address (CR-01 warning)
- `tracing::debug!()` — detailed LMDB operations
- `tracing::error!()` — errors before exit

**Structured Fields:**
```rust
tracing::info!(
    version = env!("CARGO_PKG_VERSION"),
    "lmdb2graphql starting"
);
```

**Security:**
- No logging of raw event payloads
- LMDB file paths and config paths logged; not event contents

## Comments

### Go

**File-Level Comments:**
- Explain purpose above `package` declaration
- Example from `pkg/dgraph/dgraph.go:17-24`:
```go
// Abstraction layer over Dgraph to store a Nostr Web-of-Trust (kind 3) graph.
// Guarantees uniqueness of pubkeys using @upsert schema and upsert blocks.
//
// Schema used:
//   pubkey: string @index(exact) @upsert .
//   kind3CreatedAt: int .
//   last_db_update: int .
//   follows: uid @reverse .
```

**Function Comments:**
- All exported functions have comment starting with function name
- Describe parameters, return values, and side effects
- Example: `NewClient` in `pkg/dgraph/dgraph.go:32-33`

**Inline Comments:**
- Minimal; code is self-documenting with clear names
- Explain non-obvious logic: workarounds, external limitations
- Example from `pkg/dgraph/dgraph.go:35-38`: comment on gRPC message size tuning

**Phase/Implementation Comments:**
- Reference design phase tickets (e.g., "D-01", "T-08-EARLY")
- Example from `pkg/crawler/crawler.go:82-85`: failure class documentation with phase refs

### Rust

**Module-Level Docs:**
- Doc comments `///` with markdown (see `spam/src/main.rs:1-27`)
- Detailed startup sequence and fail-closed guarantees documented

**Function Docs:**
- Doc comments `///` for public functions
- Describe panics, errors, safety (if applicable)
- Example from `spam/src/config.rs:62-66`:
```rust
/// Load config from `~/deepfry/lmdb2graphql.yaml`.
///
/// # Errors
/// Returns an error if the home directory cannot be determined, the config file
/// cannot be read, or the YAML cannot be deserialized into [`Config`].
```

**Inline Comments:**
- `//` for step-by-step narrative in complex sequences
- Example from `spam/src/main.rs:41-51`: each startup step numbered and explained

## Function Design

### Go

**Signature Conventions:**
- Context always first parameter: `func (c *Client) Query(ctx context.Context, ...)`
- Configuration struct as value: `cfg Config` (not pointer)
- Callbacks for iteration/pagination: `func([]string) error` (see `pkg/dgraph/dgraph.go:124`)
- Debug flag passed explicitly: `debug bool` (not hidden in context)
- Error always last return value

**Example from `pkg/dgraph/dgraph.go:124-130`:**
```go
func (c *Client) AddFollowers(
    ctx context.Context,
    signerPubkey string,
    kind3createdAt int64,
    follows map[string]struct{},
    debug bool,
) error
```

**Size Guidelines:**
- Functions kept to 30-50 lines where feasible
- Large operations (like `AddFollowers` chunking) split into named helpers
- Test files check specific behaviors, not entire workflows

### Rust

**Signature Conventions:**
- Async functions for I/O: `async fn` with `#[tokio::main]` or task spawn
- Result type for errors: `anyhow::Result<T>` or custom `Result<T, E>`
- No implicit `None`; use `Option<T>` explicitly when optional
- Context via `Arc` for shared state across tasks

**Example from `spam/src/config.rs:71-75`:**
```rust
pub fn load() -> anyhow::Result<Config> {
    let home = dirs::home_dir().context("cannot determine home directory")?;
    let path = home.join("deepfry").join("lmdb2graphql.yaml");
    load_from(&path)
}
```

**Early Exit:**
- `?` operator for error propagation with context (preferred)
- `match` for explicit control flow when needed

## Module Design

### Go

**Public APIs (via exported types/functions):**

- `pkg/config` — Load/save YAML config from `~/deepfry/`
  - `LoadConfig() (*Config, error)`
  - `SaveForwardRelayURL(url string) error`
  - `RemoveRelayURL(url string) error`

- `pkg/crawler` — Relay connection pool and event processing
  - `New(cfg *config.Config) (*Crawler, error)`
  - `FetchAndUpdateFollows(ctx context.Context) error`
  - Handles relay subscriptions, event validation, chunked writes

- `pkg/dgraph` — Dgraph mutations and queries
  - `NewClient(addr string) (*Client, error)`
  - `AddFollowers(ctx, signerPubkey, kind3createdAt, follows, debug)`
  - `GetStalePubkeys(ctx, limit) ([]string, error)`
  - `DeleteNodes(ctx, uids []string) error`
  - `ExpandTrustedSet()`, `GetWeakBridges()`, `ResolvePubkeysToUIDs()`

**Struct Visibility:**
- Config struct exported with public fields for `mapstructure` unmarshalling
- Helper types exported when needed: `PubkeyNode`, `WeakBridge`
- relayState unexported (internal relay management)

**Entry Points:**
- `cmd/crawler/main.go` — Spawn crawler loop, handle signals, report stats
- `cmd/clusterscan/main.go` — Trust propagation and cluster analysis
- `cmd/pubkeys/main.go` — Export all pubkeys to CSV
- `cmd/healthcheck/main.go` — Scan and optionally purge invalid pubkeys
- `cmd/discover-relays/main.go` — NIP-65 relay discovery

### Rust

**Public APIs:**
- `lmdb2graphql::config` — Load YAML from `~/deepfry/lmdb2graphql.yaml`
  - `load() -> anyhow::Result<Config>`
  - `load_from(path) -> anyhow::Result<Config>`

- `lmdb2graphql::lmdb` — LMDB environment and index operations
  - `env::open_fixture_env(path) -> Result<Env>`
  - `scan::scan_index_bounded(env, index, direction, key, limit)`
  - `scan::scan_index_windowed(env, index, direction, key, window)`

- `lmdb2graphql::graphql::schema` — GraphQL schema building
  - `build_schema(env, config) -> Result<AppSchema>`

**Crate Structure:**
- `src/config.rs` — Configuration loading
- `src/main.rs` — Startup gate and axum HTTP server
- `src/lmdb/` — LMDB environment, indexes, scan operations
- `src/graphql/` — GraphQL schema, resolvers, types

**Module Exports:**
- Re-exported at crate root: `pub use config::load`
- Used by main via fully-qualified imports: `lmdb2graphql::config::load()`

## Concurrency

### Go

**Synchronization Primitives:**
- `sync.Mutex` for protecting shared state: `dbUpdateMutex` in `pkg/crawler/crawler.go`
- `sync.WaitGroup` for waiting on goroutines: signal handling in crawler main
- `sync.atomic.Int32` for concurrent counter access: `failures` counter in `relayState`

**Context-Based Cancellation:**
- `context.WithCancel()` for graceful shutdown
- `context.WithTimeout()` for operation deadlines
- All operations accept `context.Context` as first parameter

**Goroutine Model:**
- Single-threaded event loop in main for orchestration
- Concurrent relay subscriptions in `FetchAndUpdateFollows`
- Sequential Dgraph writes (mutex-protected)
- No shared memory except through explicit Dgraph graph

### Rust

**Async Runtime:**
- `tokio` for async task spawning and runtime
- `#[tokio::main]` for single-threaded async executor

**Synchronization:**
- `tokio::sync::OnceCell` for one-time initialization (see `spam/src/main.rs:69`)
- `Arc<AtomicBool>` for atomic flags (see `spam/src/main.rs:68`)
- `Arc<Mutex<T>>` for protected state (not heavily used; prefer immutable-first design)

**Concurrency Model:**
- Startup gate runs serially; LMDB gates fail-closed before server readiness
- HTTP server (axum) handles requests concurrently per connection
- No background tasks during startup (all gates run before server binds)

## Dependencies & Integrations

### Go Modules

**Critical (Nostr + Graph Storage):**
- `github.com/nbd-wtf/go-nostr` v0.52.0 — NIP-01 WebSocket relay protocol
- `github.com/dgraph-io/dgo/v210` v210.0.0 — Dgraph gRPC client (pubkey graph)

**Configuration & Runtime:**
- `github.com/spf13/viper` v1.18.2 — YAML config loading
- `google.golang.org/grpc` v1.75.1 — gRPC transport

**Utilities:**
- `gopkg.in/yaml.v3` — YAML parsing
- Cryptography: `github.com/btcsuite/btcd/btcec/v2`, `github.com/decred/dcrd/dcrec/secp256k1/v4`

### Rust Crates

**Critical (LMDB + GraphQL):**
- `heed` 0.22.1 — LMDB typed wrapper with comparator support (pinned per spec)
- `async-graphql` 7.2.1 — GraphQL schema and execution
- `async-graphql-axum` 7.2.1 — GraphQL layer for axum (MUST match minor version)
- `axum` 0.8.9 — HTTP web framework

**Observability:**
- `tracing` 0.1 — Structured logging
- `tracing-subscriber` 0.3 — Log output formatters (JSON + env-filter)

**Configuration & Utilities:**
- `serde_yaml_ng` 0.10 — YAML parsing (maintained fork)
- `dirs` 5.0.1 — Home directory resolution
- `anyhow` 1 — Error handling with context
- `thiserror` 2 — Custom error types

---

*Convention analysis: 2026-06-15*
