# Coding Conventions

**Analysis Date:** 2026-06-16

## Naming Patterns

**Files:**
- Lowercase with underscores: `crawler.go`, `dgraph.go`, `backoff.go`, `config.go`
- Package name matches directory: `package crawler` in `pkg/crawler/`, `package dgraph` in `pkg/dgraph/`
- Test files follow pattern `*_test.go`: `crawler_quorum_test.go`, `backoff_test.go`, `config_test.go`
- Integration tests use same pattern with `//go:build integration` at top: `dgraph_writepath_test.go`, `dgraph_stale_test.go`

**Functions:**
- Exported functions use PascalCase: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`, `LoadConfig()`, `EjectRelayURL()`
- Unexported functions use camelCase: `quorumReached()`, `normalizeSeedPubkeys()`, `handleFilterNotice()`, `retryDgraph()`, `resolveUID()`, `selectLargestFixture()`
- Constructors follow pattern `New[Type]()`: `NewClient()`, `newCallMetrics()`
- Helper functions in tests: `fakeSleep()`, `neverSleep()`, `makeStrings()`

**Variables and Parameters:**
- camelCase for variables: `filterCap`, `signerPubkey`, `relayState`, `dgClient`, `slept`, `ctx`
- Boolean variables prefixed with state: `alive`, `deleted`, `valid`, `found`, `foundB`, `diskFound`
- Counters/metrics suffixed: `callCount`, `total`, `count`
- Duration variables suffixed: `baseTimeout`, `dgraphRetryMax`, `initialBackoff`, `maxBackoff`
- Context always first parameter: `ctx context.Context`
- Receiver methods use single lowercase letter: `(c *Client)`, `(e *subscriptionError)`, `(m *callMetrics)`, `(rs *relayState)`

**Types:**
- Exported structs use PascalCase: `Client`, `Crawler`, `Config`, `EjectionThresholds`, `MissBackoffParams`, `BackoffParams`, `relayState`, `subscriptionError`, `transportError`, `filterRejectionError`
- Struct tags for config mapping use snake_case: `mapstructure:"relay_urls"`, `mapstructure:"dgraph_addr"`, `mapstructure:"timeout"`
- Struct tags for JSON use camelCase: `json:"pubkey"`, `json:"kind3CreatedAt"`, `json:"created_at"`
- Error types use pattern `[name]Error` with lowercase receiver: `subscriptionError`, `transportError`, `filterRejectionError`

**Constants:**
- UPPERCASE_WITH_UNDERSCORES for package-level constants: `initialBackoff`, `maxBackoff`, `maxRecvMsgSize`, `baseTimeout`, `perBatchTimeout`, `batchSize`
- Magic numbers extracted to named constants: `const (maxRecvMsgSize = 256 << 20)` (256 MiB)
- Failure class constants follow pattern `class[Type]`: `classTransport`, `classFilterRej`, `classSubFlap`

## Code Style

**Formatting:**
- Standard Go formatting: `go fmt` enforced via Makefile (`make fmt`)
- 8-space indentation using tabs (Go default)
- Line length: no strict limit, but functions kept readable
- Imports grouped: standard library, then external packages, then local packages (Go idiom)
- Example from `pkg/crawler/crawler.go` (lines 3-19):
  ```go
  import (
      "bufio"
      "context"
      "encoding/json"
      "errors"
      "fmt"
      "log"
      ...
      "web-of-trust/pkg/config"
      "web-of-trust/pkg/crawler"
      "web-of-trust/pkg/dgraph"
      "github.com/nbd-wtf/go-nostr"
  )
  ```

**Linting:**
- `golangci-lint` optional (non-failing via Makefile)
- Targets: `make lint` (warns), `make lint-fix` (auto-fix)
- Tool gracefully handles absence (doesn't block build)
- `go vet` enforced via `make vet`

**Whitespace:**
- Blank lines between logical sections
- Consecutive struct fields on separate lines
- Comments above definitions with blank line separator

## Import Organization

**Order:**
1. Standard library packages (bufio, context, encoding/json, errors, fmt, log, etc.)
2. External packages (github.com/*, google.golang.org/*)
3. Local packages (web-of-trust/*)

**Path Aliases:**
- None used; all imports explicit with full module path: `web-of-trust/pkg/config`, `web-of-trust/pkg/dgraph`, `web-of-trust/pkg/crawler`

**No Dot Imports:**
- All external packages imported with full name: `grpc`, `status` (from `google.golang.org/grpc/status`), `codes` (from `google.golang.org/grpc/codes`)

## Error Handling

**Error Wrapping:**
- All errors wrapped with context using `%w` verb for error chain: `fmt.Errorf("query failed: %w", err)`
- Error message explains the operation that failed, not just the underlying cause
- Example from `pkg/dgraph/dgraph.go`: `fmt.Errorf("could not determine home directory: %w", err)`
- Example from `cmd/crawler/main.go`: `fmt.Errorf("Failed to create crawler: %v", err)` (fatal)

**Error Types:**
- Named error types for specific conditions: `subscriptionError`, `transportError`, `filterRejectionError` (all in `pkg/crawler/crawler.go`)
- Each custom error type implements `Error() string` and `Unwrap() error` methods
- Example from lines 21-26, 28-34, 40-45:
  ```go
  type subscriptionError struct { err error }
  func (e *subscriptionError) Error() string { return e.err.Error() }
  func (e *subscriptionError) Unwrap() error { return e.err }
  ```

**Error Classification:**
- Errors classified into groups by failure class (D-05, D-06): `classTransport`, `classFilterRej`, `classSubFlap`
- Transient errors identified by gRPC status code: `codes.Unavailable`, `codes.DeadlineExceeded` (vs. fatal `codes.ResourceExhausted`)
- Fatal errors returned immediately; transient errors trigger retry logic with exponential backoff

**Immediate Return Pattern:**
- Errors returned immediately to caller with context added
- No silent error suppression except explicit cases (e.g., config file not found is non-fatal)
- Named return values used in some contexts: `([]string, error)`, `(bool, error)`

## Logging

**Framework:** Built-in `log` package (no external logging library)

**Patterns:**
- `log.Printf("...")` for informational messages
- `log.Printf("WARN: ...")` for warnings (prefixed with WARN)
- `log.Printf("DEBUG: ...")` guarded by `if c.debug { ... }` for debug output (enabled by config)
- `log.Fatalf("...")` for fatal errors in main()
- Connection lifecycle events logged: "Connected to relay: %s", relay down events, reconnect attempts
- Major state changes logged: relay marked dead, retry scheduled, batch completed, backoff applied
- Configuration loaded/saved logged: file location, default values applied
- Processing milestones logged: pubkey count processed, batch completion, query results, statistics
- **No logging of raw secrets** — env vars and private keys never logged

**Debug Flag:**
- Controlled via config: `cfg.Debug` or command-line flag
- Debug output guarded: `if c.debug { log.Printf(...) }`
- Example from `cmd/crawler/main.go` and `pkg/crawler/crawler.go`: debug mode enables detailed per-relay and per-pubkey tracing

**Timing Instrumentation:**
- Call duration recorded for successful operations: `start := time.Now()` then `time.Since(start)`
- Metrics accumulated in `callMetrics` struct: `m.record(callName, duration)` — success-only (RETRY-01/D-07)
- Average computed lazily: `m.avg(callName)`

## Comments

**File-Level Comments:**
- Explain purpose above package declaration: "Command clusterscan finds suspected spam clusters..." (lines 1-6 in `cmd/clusterscan/main.go`)
- Describe module scope and responsibilities for complex packages

**Exported Function Comments:**
- All exported functions have comment starting with function name (Go convention)
- Describe parameters (if not obvious), return values, side effects, and error conditions
- Example from `pkg/dgraph/dgraph.go` (line 32-33): "NewClient creates a new Client connected to the given dgraph gRPC address (eg "localhost:9080")."
- Example from `pkg/config/config.go` (line 70): "LoadConfig loads the application configuration from various sources"

**Inline Comments:**
- Explain non-obvious logic: workarounds for external limitations, invariants, design decisions
- Prefix complex comments with reference tags: "WR-01", "D-05", "PERF-02" (test/issue tracking)
- Example from `pkg/dgraph/dgraph.go` (line 87): "// viper.SafeWriteConfigAs does not write SetDefault values..."
- Example from `pkg/crawler/crawler.go` (line 95-100): detailed comment on failure classification purpose

**Minimal Inline Comments:**
- Code is self-documenting via clear naming; comments explain "why" not "what"
- Used only when logic is non-obvious or non-standard
- Avoid obvious comments like `i := 0 // initialize i`

**Test Comments:**
- Comprehensive narrative comments at top of test: what it tests, why it matters, error scenario
- Example from `pkg/crawler/crawler_quorum_test.go` (lines 8-9): "TestQuorumReached_BelowThreshold verifies that quorumReached returns false when done < ceil(q * queried)."
- Comments include threshold math for clarity

**Regression/Bug Fix Comments:**
- Reference bug/issue numbers and phases: "Phase 8", "CR-01", "WR-01", "TEST-03"
- Describe red/green proof: test fails against pre-fix code, passes post-fix
- Example from `pkg/dgraph/dgraph_writepath_test.go` (lines 18-25): "Red/green proof: this test FAILS against pre-fix code... and PASSES post-fix."

## Function Design

**Signature Patterns:**
- `ctx context.Context` always first parameter (Go convention)
- Configuration struct passed as value, not pointer: `cfg Config`
- Receiver as first named parameter after method receiver: `(c *Client) AddFollowers(ctx context.Context, ...)`
- Callback functions used for pagination: `callback func([]string) error`
- Debug flag passed explicitly: `debug bool` (no global flag)
- Error always last return value: `(..., error)`
- Boolean for optional success condition: `(deleted bool, error)` — `deleted=true` means operation succeeded and deleted
- Map for bulk results: `map[string]int64` for UID resolution results

**Return Value Patterns:**
- Single return value for simple operations: `(error)`
- Tuple return for value + error: `(int64, error)`, `(map[string]int64, error)`, `([]string, error)`
- Explicit nil for "not found" (not error): `func GetKind3CreatedAt(...) (int64, error)` returns `(0, nil)` if not exists
- Boolean indicates success condition: `(bool, error)` where `true` means operation had effect

**Context Handling:**
- All I/O operations accept `ctx context.Context` as first parameter
- Explicit context cancellation checks in retry loops: `if err := ctx.Err(); err != nil { return ... }`
- Deadline-based retry: `time.After()` used with `select { case <-ctx.Done(): ... }`

**Function Size:**
- Functions kept concise and single-purpose
- Bulk operations chunked to prevent memory/gRPC limits: `chunkSlice()` splits items into batches
- Helper functions extracted for repeated logic: `quorumReached()`, `handleFilterNotice()`

## Module Design

**Public APIs per Package:**

**`pkg/config`:**
- Constructor: `LoadConfig() (*Config, error)`
- Helpers: `EjectRelayURL(url string) error`, `SaveForwardRelayURL(url string) error`, `RemoveRelayURL(url string) error`
- Config struct exported with public fields for mapstructure unmarshalling: `type Config struct { RelayURLs []string, DgraphAddr string, ... }`
- Sub-structs exported: `EjectionThresholds`, `MissBackoffParams`

**`pkg/dgraph`:**
- Constructor: `NewClient(addr string) (*Client, error)`
- Lifecycle: `Close() error`
- Schema setup: `EnsureSchema(ctx context.Context) error`
- Write operations: `AddFollowers()`, `RemoveFollower()`, `DeleteNodes()`, `MarkAttempted()`, `SetQueryTimestamp()`
- Read operations: `GetStalePubkeys()`, `CountPubkeys()`, `GetKind3CreatedAt()`, `ResolvePubkeysToUIDs()`
- Paginated reads: `GetPubkeysWithMinFollowersPaginated()`, `GetAllPubkeysPaginated()`
- Graph analysis: `ResolvePubkeysToUIDs()`, `ExpandTrustedSet()`, `GetWeakBridges()`, `ClusterBeneath()`
- Helper types exported: `BackoffParams` (in backoff.go)

**`pkg/crawler`:**
- Constructor: `NewCrawler(cfg *config.Config) (*Crawler, error)`
- Main loop: `FetchAndUpdateFollows(ctx context.Context, relays []string) error`
- Helper: `Run(ctx context.Context) error` (wrapper for main loop)

**Barrel Files:**
- No barrel files (no `__init__.go` or `index.go`); each package is imported directly: `import "web-of-trust/pkg/crawler"`

**Exported vs. Unexported:**
- Struct fields are exported (PascalCase) when needed for mapstructure unmarshalling: `Config.RelayURLs`, `Config.DgraphAddr`, `EjectionThresholds.Transport`
- Internal state kept unexported (camelCase): `Client.dg`, `Client.conn`, `relayState.alive`, `relayState.backoff`
- Type itself exported if it's a return value or struct field: `Client`, `Config`, `BackoffParams`
- Helper functions unexported if internal: `chunkSlice()`, `quorumReached()`, `newCallMetrics()`

**Configuration:**
- YAML file at `~/deepfry/web-of-trust.yaml` (via Viper library)
- Defaults provided if file missing or keys absent
- Runtime mutation functions: `EjectRelayURL()`, `RemoveRelayURL()`, `SaveForwardRelayURL()` — persist to disk
- Field mapping: struct tags use snake_case: `mapstructure:"relay_urls"`

## Testing Patterns in Conventions

**Test Naming:**
- Test functions follow Go convention: `func Test[FunctionName]_[Scenario](t *testing.T)`
- Descriptive scenario suffix: `TestQuorumReached_BelowThreshold`, `TestChunkSlice`, `TestBackoffInterval_NonAlignedCap`

**Deterministic Timing:**
- Injected `sleepFn func(time.Duration) <-chan time.Time` in `retryDgraph()` for deterministic testing (no real `time.Sleep()`)
- Fake sleepers return buffered channels that fire immediately: `fakeSleep()`, `neverSleep()`
- Full backoff sequence verified in microseconds (not wall-clock time)

**Helpers in Tests:**
- Factories: `makeStrings(n int) []string` — generates test data
- Fakes: `fakeSleep()`, `neverSleep()` — injectable dependencies for deterministic behavior
- Assertions: custom `t.Fatalf()` for clear error messages

---

*Convention analysis: 2026-06-16*
