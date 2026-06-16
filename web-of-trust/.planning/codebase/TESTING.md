# Testing Patterns

**Analysis Date:** 2026-06-15

## Test Framework

### Go

**Runner:** Go standard `testing` package — no third-party test runner.

**Assertion style:** Manual `if`/`t.Fatalf`/`t.Errorf` — no testify or gomock. All assertions are hand-rolled.

**Run Commands:**
```bash
# From within a subsystem directory:
make test                    # go test ./... -short -cover
make test-integration        # go test -tags=integration ./...
make bench                   # whitelist-plugin only; configurable via BENCHPKG/BENCHFLAGS
go test -bench=. ./...       # run benchmarks manually
```

### Rust

**Runner:** `cargo test` — standard Rust test harness.

**Assertion style:** Rust's built-in `assert_eq!`, `assert!` macros; no external assertion libraries.

**Run Commands:**
```bash
# From spam/ directory:
cargo test                   # all unit + integration tests
cargo test --lib            # unit tests only
cargo test --test '*'        # integration tests only
cargo bench                  # run benchmarks (unstable; use `--nightly` flag if needed)
```

**Build Commands:**
```bash
cargo build                  # debug binary with debug symbols
cargo build --release        # optimized binary
cargo fmt --check            # verify formatting (pre-commit check)
```

## Test Coverage

| Subsystem | Test Files | Types Present | Notes |
|-----------|-----------|---------------|-------|
| `event-forwarder` | 15+ `_test.go` | Unit, integration, benchmarks | Heaviest coverage in the project |
| `whitelist-plugin` | 10 `_test.go` | Unit, benchmarks | Covers handler, heuristics, quarantine, repository, whitelist, server, client |
| `quarantine-rescuer` | 6 `_test.go` | Unit | Covers all `internal/` packages |
| `web-of-trust` | 3 `_test.go` + integration | Unit, integration | `dgraph_chunks_test.go` (unit); stale/writepath tests are integration-tagged |
| `spam/lmdb2graphql` | 10+ `.rs` tests + 7 integration tests | Unit, integration | Config, LMDB types, payload; integration tests in `tests/` |
| `search-plugin` | None | — | Placeholder only — `README.md` present, no Go source |
| `embeddings-generator` | None | — | Placeholder only — `README.md` present, no Go source |
| `profile-builder` | None | — | Not a Go module |
| `thread-inference` | None | — | Not a Go module |

## Test Types

### Go Unit Tests (default `make test`)

The majority of tests. Run with `-short` flag; any test requiring external services must check `testing.Short()` and skip, or use the `integration` build tag.

Located co-located with source: `pkg/<concern>/<file>_test.go` or `internal/<concern>/<file>_test.go`.

### Go Integration Tests (`//go:build integration`)

Require live external services (real relay WebSocket, live Dgraph). Gated by build tag so they never run in `make test`.

Files:
- `event-forwarder/pkg/forwarder/forwarder_integration_test.go`
- `event-forwarder/pkg/nsync/nsync_integration_test.go`
- `web-of-trust/pkg/dgraph/dgraph_stale_test.go` (see line 1: `//go:build integration`)
- `web-of-trust/pkg/dgraph/dgraph_writepath_test.go` (see line 1: `//go:build integration`)

Some integration tests also double-check with `testing.Short()` for extra safety.

### Rust Unit Tests

Colocated in the same module file within `#[cfg(test)]` blocks (see `spam/src/config.rs:89-100`).

Tests verify:
- Default values: `test_map_size_default()`, `test_bind_address_default()`
- Config loading: `test_load_from_tempdir()` (uses `tempfile::tempdir()`)
- Type deserialization: `test_nostr_event_ignores_unknown_fields()`
- Error cases: `test_nostr_event_missing_required_field_errors()`

Run via `cargo test --lib` (library code only).

### Rust Integration Tests

Separate `tests/` directory with full integration tests (see `spam/tests/`):

- `tests/scan_test.rs` — LMDB index scanning, resume-cursor validation, DUPSORT coverage
- `tests/body_limit_test.rs` — HTTP request body size limiting (WR-02-LAYER)
- `tests/payload_test.rs` — Payload decompression and chunking
- `tests/ready_window_test.rs` — Server readiness gate sequencing
- `tests/health_ready_test.rs` — Health and readiness endpoint states
- `tests/comparator_hook_smoke.rs` — Comparator hook integration (LMDB-06)
- `tests/self_check_test.rs` — Self-check gate (LMDB version, endianness)
- `tests/dupsort_resume_test.rs` — DUPSORT duplicate key handling
- `tests/fixture/` — Committed test fixture (`data.mdb`, `lock.mdb`)

Run via `cargo test --test '*'` or individual test via `cargo test --test scan_test`.

### Benchmarks

**Go:**
- `whitelist-plugin/pkg/whitelist/whitelist_bench_test.go` — `BenchmarkNewWhiteList` and `BenchmarkIsWhitelisted` at sizes 1k, 10k, 100k, 500k. Uses `b.ReportAllocs()`.
- `whitelist-plugin/pkg/repository/dgraph_repository_bench_test.go` — `BenchmarkGetAll`, `BenchmarkFetchPubkeysPage`, `BenchmarkMergePubkeys` using `httptest.NewServer` for realistic pagination.

**Rust:**
- None observed in current codebase (no `#[bench]` or similar marked in the spam module).

## Test Patterns

### Go Subtests with `t.Run`

The dominant pattern. Used in both flat and table-driven forms:

```go
// Flat subtests (config validation tests)
func TestValidate(t *testing.T) {
    t.Run("empty config", func(t *testing.T) { ... })
    t.Run("missing source relay", func(t *testing.T) { ... })
}

// Table-driven (whitelist handler, message deserialization)
var validCases = []struct{ name, msg string }{ ... }
func TestValidInputMessageDeserialization(t *testing.T) {
    for _, test := range validCases {
        t.Run(fmt.Sprintf("%s is serialised", test.name), func(t *testing.T) { ... })
    }
}
```

### Go Mock/Stub Construction

**`testutil.MockRelay`** (`event-forwarder/pkg/testutil/mock_relay.go`) — implements `relay.Relay`. Captures calls in `QuerySyncCalls`, `PublishCalls`, etc. Errors injectable via `PublishError`, `SubscribeError`.

**In-test stubs** — small structs defined at the bottom of `_test.go` files implement the interface under test. Pattern: `type stubWindowMgr struct { ... }` with function fields for overrideable behaviour:

```go
type stubWindowMgr struct {
    window   *nsync.Window
    updateFn func(ctx context.Context, w nsync.Window) error
}
func (s *stubWindowMgr) Update(ctx context.Context, w nsync.Window) error {
    if s.updateFn != nil { return s.updateFn(ctx, w) }
    return nil
}
```

**`httptest.NewServer`** — used in repository and benchmark tests to mock HTTP/GraphQL endpoints without a live Dgraph (`whitelist-plugin/pkg/repository/dgraph_repository_bench_test.go`).

**`t.TempDir()`** — used in `quarantine-rescuer/internal/lmdbreader/reader_test.go` to create real LMDB environments for I/O tests. Cleans up automatically.

### Go Logger in Tests

Tests construct a real `*log.Logger` pointing to `os.Stdout` or `io.Discard`:

```go
func createTestLogger() *log.Logger { return log.New(os.Stdout, "[TEST] ", 0) }
func newSilentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

`io.Discard` loggers are used wherever log output is not under assertion.

### Go Error Path Testing

Tests explicitly inject errors via mock fields and assert the returned error is non-nil:

```go
dst.PublishError = errors.New("boom")
if err := f.processRealtimeEvent(ctx, evt); err == nil {
    t.Fatalf("expected error when publish fails")
}
```

### Go Immutability and Edge Case Tests

Whitelist tests verify input-slice mutation does not affect the constructed whitelist (`TestNewWhiteList_immutability_of_input_slice`). LMDB tests cover bad-entry skip paths, context cancellation mid-iteration, and empty DBs.

### Rust Test Organization

Tests are named with `test_` prefix and grouped by concern:

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_map_size_default() {
        assert_eq!(
            default_map_size(),
            10_995_116_277_760,
            "default map_size must match strfry.conf mapsize (10 TiB)"
        );
    }

    #[test]
    fn test_load_from_tempdir() {
        let tmp = tempfile::tempdir().expect("create tempdir");
        // ... test code
    }
}
```

**Pattern:** Tests use `expect()` for setup failures (panic is acceptable for test harness) and `assert_eq!` for behavioral assertions.

### Rust Fixture Pattern

Integration tests use a committed fixture in `tests/fixture/` with real LMDB files (`data.mdb`, `lock.mdb`):

```rust
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}
```

This pattern (seen in `tests/scan_test.rs:23-30` and `tests/self_check_test.rs`) avoids I/O to `~/deepfry/` and keeps tests hermetic.

### Rust Assert Patterns

**Structural assertions:**
```rust
assert_eq!(batch1_lev_ids, vec![4u64, 5, 6], "First batch must be [4,5,6], got {:?}", batch1_lev_ids);
```

**Helper functions in tests:**
```rust
fn kind_forward_low_key() -> Vec<u8> {
    let mut k = Vec::with_capacity(16);
    k.extend_from_slice(&0u64.to_le_bytes());
    k.extend_from_slice(&0u64.to_le_bytes());
    k
}
```

### Rust Context and Cleanup

Integration tests use `t.Cleanup` pattern in Go; Rust uses `defer!` or explicit cleanup:

```rust
#[test]
fn test_add_followers_large() {
    // ... setup and test
    
    // Cleanup on test exit (implicit drop of temp dir)
    let (env, _tmp) = open_temp_fixture_env();
    // _tmp is dropped at end of scope
}
```

LMDB tests ensure `env.close()` is called (if applicable) before assertion to flush writes.

## Testing Infrastructure

**Go:**
- No test containers / Docker in unit tests
- Integration tests connect to real services expected to be running locally (Dgraph on `localhost:9080`, relay on `localhost:7777`)
- No CI pipeline detected (no `.github/workflows/`, no `Jenkinsfile`, no `.circleci/`)
- Tests are run manually via `make test`

**Rust:**
- Fixtures are committed in `tests/fixture/`
- No external service dependencies for tests (fixtures are standalone LMDB files)
- Real LMDB environments created in-process via `heed::Env` with `tempfile::TempDir`
- No Docker or CI pipeline integration observed

## Shared Test Utilities

### Go

`event-forwarder/pkg/testutil/testutil.go` — fixed Nostr key constants (`TestSKHex`, `TestPKHex`, `TestSK`, `TestPK`) used across packages to avoid generating new keys per test.

`event-forwarder/pkg/testutil/telemetry_capture.go` — `CapturingPublisher` that records emitted telemetry events for assertion.

### Rust

`tests/` directory contains helper modules used by integration tests:
- `open_fixture_env()` — shared fixture loader (defined in multiple test files as a reusable pattern)
- Golden vectors (e.g., `tests/fixture/golden_vectors/Event__kind.json`) for index ordering validation

## Test Execution

### Go

From within a Go subsystem directory (e.g., `web-of-trust/`, `event-forwarder/`):

```bash
make test                    # -short + coverage
make test-integration        # run only //go:build integration tests
make clean                   # remove bin/
make lint                    # run golangci-lint (advisory)
```

### Rust

From the `spam/` directory:

```bash
cargo test                   # unit + integration tests (debug build)
cargo test --release         # optimized build
cargo test --lib             # unit tests only
cargo test --test 'scan_test'  # single integration test
cargo fmt                    # auto-format (runs before compilation)
```

## Coverage Gaps

### Go

- **`web-of-trust/pkg/crawler/`** — `crawler.go` has no `_test.go`. The crawler's subscription and write logic is untested at unit level.
- **`web-of-trust/pkg/config/`** — `config.go` (Viper YAML loading) has no test.
- **`web-of-trust` cmd packages** — `cmd/crawler/`, `cmd/pubkeys/`, `cmd/discover-relays/`, `cmd/clusterscan/`, `cmd/healthcheck/` have no tests.
- **`whitelist-plugin` cmd packages** — `cmd/whitelist/main.go`, `cmd/server/main.go`, `cmd/router/main.go` have no tests; only their subordinate `pkg/` code is tested.
- **`quarantine-rescuer/internal/runner/`** — `runner.go` has no `_test.go`.
- **`quarantine-rescuer/internal/event/`** — `event.go` has no `_test.go`.
- **No E2E tests** — the full pipeline (upstream relay → event-forwarder → StrFry → whitelist-plugin → Dgraph) is not tested end-to-end.
- **No CI enforcement** — coverage is not tracked or gated automatically.

### Rust

- **`spam/src/server.rs`** — HTTP server routing logic has no test coverage.
- **`spam/src/graphql/`** — GraphQL resolvers untested directly (covered by integration tests).
- **`spam/src/lmdb/scan.rs`** — Full scan logic tested via integration; unit tests exist for key builders but not all edge cases.
- **No E2E tests** — the full GraphQL query execution against a live populated LMDB is not formally tested.
- **No CI enforcement** — coverage is not tracked or gated automatically.

---

*Testing analysis: 2026-06-15*
