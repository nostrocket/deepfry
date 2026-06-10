# Testing Patterns

**Analysis Date:** 2026-06-10

## Test Framework

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

## Test Coverage

| Subsystem | Test Files | Types Present | Notes |
|-----------|-----------|---------------|-------|
| `event-forwarder` | 15+ `_test.go` | Unit, integration, benchmarks | Heaviest coverage in the project |
| `whitelist-plugin` | 10 `_test.go` | Unit, benchmarks | Covers handler, heuristics, quarantine, repository, whitelist, server, client |
| `quarantine-rescuer` | 6 `_test.go` | Unit | Covers all `internal/` packages |
| `web-of-trust` | 3 `_test.go` | Unit, integration | `dgraph_chunks_test.go` (unit); stale/writepath tests are integration-tagged |
| `search-plugin` | None | — | Placeholder only — `README.md` present, no Go source |
| `embeddings-generator` | None | — | Placeholder only — `README.md` present, no Go source |
| `profile-builder` | None | — | Not a Go module |
| `thread-inference` | None | — | Not a Go module |

## Test Types

### Unit Tests (default `make test`)

The majority of tests. Run with `-short` flag; any test requiring external services must check `testing.Short()` and skip, or use the `integration` build tag.

Located co-located with source: `pkg/<concern>/<file>_test.go` or `internal/<concern>/<file>_test.go`.

### Integration Tests (`//go:build integration`)

Require live external services (real relay WebSocket, live Dgraph). Gated by build tag so they never run in `make test`.

Files:
- `event-forwarder/pkg/forwarder/forwarder_integration_test.go`
- `event-forwarder/pkg/nsync/nsync_integration_test.go`
- `web-of-trust/pkg/dgraph/dgraph_stale_test.go`
- `web-of-trust/pkg/dgraph/dgraph_writepath_test.go`

Some integration tests also double-check with `testing.Short()` for extra safety.

### Benchmarks

- `whitelist-plugin/pkg/whitelist/whitelist_bench_test.go` — `BenchmarkNewWhiteList` and `BenchmarkIsWhitelisted` at sizes 1k, 10k, 100k, 500k. Uses `b.ReportAllocs()`.
- `whitelist-plugin/pkg/repository/dgraph_repository_bench_test.go` — `BenchmarkGetAll`, `BenchmarkFetchPubkeysPage`, `BenchmarkMergePubkeys` using `httptest.NewServer` for realistic pagination.

## Test Patterns

### Subtests with `t.Run`

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

### Mock/Stub Construction

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

### Shared Test Utilities

`event-forwarder/pkg/testutil/testutil.go` — fixed Nostr key constants (`TestSKHex`, `TestPKHex`, `TestSK`, `TestPK`) used across packages to avoid generating new keys per test.

`event-forwarder/pkg/testutil/telemetry_capture.go` — `CapturingPublisher` that records emitted telemetry events for assertion.

### Logger in Tests

Tests construct a real `*log.Logger` pointing to `os.Stdout` or `io.Discard`:

```go
func createTestLogger() *log.Logger { return log.New(os.Stdout, "[TEST] ", 0) }
func newSilentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

`io.Discard` loggers are used wherever log output is not under assertion.

### Error Path Testing

Tests explicitly inject errors via mock fields and assert the returned error is non-nil:

```go
dst.PublishError = errors.New("boom")
if err := f.processRealtimeEvent(ctx, evt); err == nil {
    t.Fatalf("expected error when publish fails")
}
```

### Immutability and Edge Case Tests

Whitelist tests verify input-slice mutation does not affect the constructed whitelist (`TestNewWhiteList_immutability_of_input_slice`). LMDB tests cover bad-entry skip paths, context cancellation mid-iteration, and empty DBs.

## Testing Infrastructure

**No test containers / Docker in unit tests.** Integration tests connect to real services expected to be running locally (Dgraph on `localhost:9080`, relay on `localhost:7777`).

**No CI pipeline detected** (no `.github/workflows/`, no `Jenkinsfile`, no `.circleci/`). Tests are run manually via `make test`.

**LMDB tests** build real in-process LMDB environments using `t.TempDir()` — no external dependency required.

## Gaps

- **`web-of-trust/pkg/crawler/`** — `crawler.go` has no `_test.go`. The crawler's subscription and write logic is untested at unit level.
- **`web-of-trust/pkg/config/`** — `config.go` (Viper YAML loading) has no test.
- **`web-of-trust` cmd packages** — `cmd/crawler/`, `cmd/pubkeys/`, `cmd/discover-relays/`, `cmd/clusterscan/`, `cmd/healthcheck/` have no tests.
- **`whitelist-plugin` cmd packages** — `cmd/whitelist/main.go`, `cmd/server/main.go`, `cmd/router/main.go` have no tests; only their subordinate `pkg/` code is tested.
- **`quarantine-rescuer/internal/runner/`** — `runner.go` has no `_test.go`.
- **`quarantine-rescuer/internal/event/`** — `event.go` has no `_test.go`.
- **No E2E tests** — the full pipeline (upstream relay → event-forwarder → StrFry → whitelist-plugin → Dgraph) is not tested end-to-end.
- **No CI enforcement** — coverage is not tracked or gated automatically.
