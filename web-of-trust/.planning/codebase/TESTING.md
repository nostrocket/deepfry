# Testing Patterns

**Analysis Date:** 2026-06-16

## Test Framework

**Runner:**
- Go built-in testing framework (`testing` package)
- No external test runner or assertion library
- Config: Makefile targets `make test` and `make test-integration`

**Assertion Library:**
- Go built-in `testing.T`: `t.Fatalf()`, `t.Errorf()`, `t.Fatal()`, `t.Error()`, `t.Skip()`
- No external assertion library (testify, etc.)
- Manual equality checks with conditional `t.Fatalf()`: `if got != want { t.Fatalf("expected %d, got %d", want, got) }`

**Run Commands:**
```bash
make test                    # Run all unit tests with -short flag; coverage enabled
make test-integration        # Run integration tests (-tags=integration)
```

**Specific Commands:**
```bash
go test ./... -short -cover           # Unit tests only (skips //go:build integration)
go test -tags=integration ./...       # Integration tests only
go test -v ./pkg/crawler              # Verbose output for one package
```

## Test File Organization

**Location:**
- Co-located with source code: `pkg/crawler/crawler.go` → `pkg/crawler/crawler_quorum_test.go`, `crawler_filter_test.go`, `crawler_hang_test.go`
- Integration tests in same directory: `pkg/dgraph/dgraph_writepath_test.go`, `dgraph_stale_test.go`, `dgraph_validation_test.go`
- Entry point tests: `cmd/crawler/main_test.go` (tests for retryDgraph logic)

**Naming:**
- Pattern: `[module]_[scenario]_test.go` or `[module]_test.go`
- Examples: `crawler_quorum_test.go`, `backoff_test.go`, `config_test.go`, `dgraph_chunks_test.go`
- Test functions: `Test[FunctionName]_[Scenario]`

**Tagging:**
- Integration tests use `//go:build integration` directive at top of file (line 1)
- Example from `pkg/dgraph/dgraph_writepath_test.go`, `dgraph_stale_test.go`, `dgraph_validation_test.go`
- Allows `go test ./... -short` to skip integration tests (no Dgraph dependency)
- Full integration tests run with `go test -tags=integration ./...`

## Test Structure

**Suite Organization:**
- No test suite/describe blocks (Go convention is per-function tests)
- Tests are flat functions; no nesting
- Test coverage organized by module: all `crawler_*_test.go` files in `pkg/crawler/` test `crawler.go` functions
- Example test from `pkg/crawler/crawler_quorum_test.go` (lines 8-24):
  ```go
  func TestQuorumReached_BelowThreshold(t *testing.T) {
      if quorumReached(6, 10, 0.70) {
          t.Fatal("expected false when done=6 < ceil(0.70*10)=7")
      }
  }
  ```

**Common Setup Pattern:**
- Each test function self-contained (minimal shared state)
- Setup happens inline: create temp directory, write config file
- Example from `pkg/config/config_test.go` (lines 35-53):
  ```go
  tmpHome := t.TempDir()
  t.Setenv("HOME", tmpHome)
  configDir := tmpHome + "/deepfry"
  if err := os.MkdirAll(configDir, 0755); err != nil {
      t.Fatal(err)
  }
  configContent := `relay_urls:\n  - wss://relay.damus.io\n`
  if err := os.WriteFile(configDir+"/web-of-trust.yaml", []byte(configContent), 0644); err != nil {
      t.Fatal(err)
  }
  ```

**Cleanup Pattern:**
- Cleanup via `t.Cleanup()` callback (Go 1.14+)
- Example from `pkg/dgraph/dgraph_writepath_test.go` (lines 86-93):
  ```go
  t.Cleanup(func() {
      uid := resolveUID(t, c, signer)
      if uid != "" {
          if err := c.DeleteNodes(ctx, []string{uid}); err != nil {
              t.Logf("cleanup: delete signer %s (uid %s) failed: %v", signer, uid, err)
          }
      }
  })
  ```

**Viper State Reset:**
- Viper (config parser) holds global state across tests
- Tests reset it: `viper.Reset()` at start (see `pkg/config/config_test.go` lines 15, 55, 109)
- Per-test HOME environment: `t.Setenv("HOME", tmpHome)` to avoid collision with real config

## Test Structure Examples

### Unit Test Example: Table-Driven

```go
// pkg/dgraph/backoff_test.go, lines 13-44
func TestBackoffInterval(t *testing.T) {
    const (
        base  = 2 * time.Hour
        cap_  = 168 * time.Hour
        ratio = 2
    )

    cases := []struct {
        missCount int
        want      time.Duration
    }{
        {0, 2 * time.Hour},
        {1, 4 * time.Hour},
        {7, 168 * time.Hour},   // capped at 7d
        {100, 168 * time.Hour}, // overflow guard
    }

    for _, tc := range cases {
        got := BackoffInterval(tc.missCount, base, ratio, cap_)
        if got != tc.want {
            t.Errorf("BackoffInterval(missCount=%d) = %v; want %v", tc.missCount, got, tc.want)
        }
    }
}
```

### Unit Test Example: Mocking/Injection

```go
// cmd/crawler/main_test.go, lines 36-72
func TestRetryDgraph_BackoffSequence(t *testing.T) {
    var slept []time.Duration
    calls := 0
    fn := func() (int, error) {
        calls++
        if calls <= 5 {
            return 0, status.Error(codes.Unavailable, "transient")
        }
        return 42, nil
    }

    got, err := retryDgraph(context.Background(), "BackoffTest", fn, newCallMetrics(), fakeSleep(&slept))
    if err != nil {
        t.Fatalf("expected nil error after transient retries, got: %v", err)
    }
    if got != 42 {
        t.Fatalf("expected value 42, got %d", got)
    }

    want := []time.Duration{
        1 * time.Minute,
        2 * time.Minute,
        4 * time.Minute,
        5 * time.Minute,
        5 * time.Minute,
    }
    if len(slept) != len(want) {
        t.Fatalf("expected %d recorded delays, got %d: %v", len(want), len(slept), slept)
    }
    for i, w := range want {
        if slept[i] != w {
            t.Errorf("delay[%d] = %v; want %v", i, slept[i], w)
        }
    }
}
```

### Integration Test Example

```go
// pkg/dgraph/dgraph_writepath_test.go, lines 30-100 (partial)
//go:build integration

func TestAddFollowersLargeKind3(t *testing.T) {
    fixture := selectLargestFixture(t)
    if fixture == "" {
        t.Skip("no large kind-3 fixture found...")
    }

    raw, err := os.ReadFile(fixture)
    if err != nil {
        t.Fatalf("read fixture %s failed: %v", fixture, err)
    }

    // Unmarshal and parse event...

    ctx := context.Background()
    c, err := NewClient("localhost:9080")
    if err != nil {
        t.Fatal(err)
    }
    defer c.Close()
    if err := c.EnsureSchema(ctx); err != nil {
        t.Fatal(err)
    }

    // Cleanup registered via t.Cleanup()
    t.Cleanup(func() {
        uid := resolveUID(t, c, signer)
        if uid != "" {
            if err := c.DeleteNodes(ctx, []string{uid}); err != nil {
                t.Logf("cleanup: delete signer %s (uid %s) failed: %v", signer, uid, err)
            }
        }
    })

    if err := c.AddFollowers(ctx, signer, createdAt, follows, true); err != nil {
        t.Fatalf("AddFollowers failed: %v", err)
    }

    // Assert the full follow set persisted...
}
```

## Mocking

**Framework:** 
- No external mocking library (gomock, etc.)
- Dependency injection via function parameters
- Callback interfaces for pagination

**Patterns:**

### Injected Functions
```go
// cmd/crawler/main_test.go, lines 21-34
// Fake sleeper returns buffered channel that fires immediately
func fakeSleep(slept *[]time.Duration) func(time.Duration) <-chan time.Time {
    return func(d time.Duration) <-chan time.Time {
        *slept = append(*slept, d)
        ch := make(chan time.Time, 1)
        ch <- time.Now()
        return ch
    }
}

// Never-firing sleep for cancellation testing
func neverSleep(time.Duration) <-chan time.Time {
    return make(chan time.Time) // unbuffered, never written
}
```

### Dependency Injection Usage
```go
// Inject fakeSleep into retryDgraph
got, err := retryDgraph(context.Background(), "BackoffTest", fn, newCallMetrics(), fakeSleep(&slept))

// Alternatively, inject neverSleep for cancellation paths
_, err := retryDgraph(ctx, "CancelTest", fn, newCallMetrics(), neverSleep)
```

### Callback Interface Pattern
```go
// GetPubkeysWithMinFollowersPaginated uses a callback to process batches
// Mocking done by passing different callbacks in tests
dgraph.GetPubkeysWithMinFollowersPaginated(ctx, minFollowers, func(batch []string) error {
    // Test callback behavior here
    return nil
})
```

**What to Mock:**
- Time-dependent operations: inject `sleepFn` instead of real `time.Sleep()`
- I/O with side effects: Dgraph calls use real gRPC (integration tests; mocking not done)
- External services: relay connections tested via real WebSocket (to live relays)

**What NOT to Mock:**
- Pure functions: `chunkSlice()`, `BackoffInterval()` tested directly without mocking
- Go standard library: context, error types, channels (used as-is)
- Business logic: retry logic, backoff math tested with real invocations (not mocked)

## Fixtures and Factories

**Test Data Factories:**
```go
// pkg/dgraph/dgraph_chunks_test.go, lines 8-15
func makeStrings(n int) []string {
    out := make([]string, n)
    for i := 0; i < n; i++ {
        out[i] = fmt.Sprintf("%d", i)
    }
    return out
}
```

**Fixture Files:**
- Large kind-3 event fixtures stored under `testdata/largest-kind3-*.json`
- Tests select largest fixture via helper: `selectLargestFixture(t)` (from `pkg/dgraph/dgraph_writepath_test.go`)
- Fixtures harvested manually from live crawler runs (optional; test skips if none present)
- JSON format: Nostr kind-3 event with pubkey, created_at, tags array

**Location:**
- Fixtures: `pkg/dgraph/testdata/` (implied by test code)
- Factories: inline in test files (no separate factory packages)
- Config test data: temporary files created with `t.TempDir()` and `os.WriteFile()`

## Coverage

**Requirements:** 
- No explicit coverage target enforced
- Coverage flag enabled in Makefile: `go test ./... -short -cover` (line 80)
- Coverage reported to stdout but not gated on minimum percentage

**View Coverage:**
```bash
make test                                    # Shows coverage summary
go test ./... -short -coverprofile=cov.txt  # Write profile to file
go tool cover -html=cov.txt                 # View HTML report
```

**Observed Coverage:**
- Unit test packages well-covered: `pkg/dgraph/backoff_test.go`, `pkg/crawler/crawler_quorum_test.go`, `pkg/config/config_test.go`
- Integration tests gate on live Dgraph: `pkg/dgraph/dgraph_writepath_test.go`, `dgraph_stale_test.go`
- Main logic (`cmd/crawler/main.go`) unit-tested for critical paths: `cmd/crawler/main_test.go` covers `retryDgraph()`, backoff, metrics

## Test Types

**Unit Tests (make test -short):**
- Scope: Single function or small component
- No external dependencies: `chunkSlice()`, `BackoffInterval()`, `quorumReached()`, `LoadConfig()`
- Fast execution (microseconds to milliseconds)
- Location: `pkg/*/` (most files)
- Examples: `pkg/dgraph/backoff_test.go` (pure math), `pkg/crawler/crawler_quorum_test.go` (formula), `pkg/config/config_test.go` (config loading)
- Deterministic: injected dependencies (`fakeSleep`), temp filesystem (`t.TempDir()`), known inputs

**Integration Tests (make test-integration, tagged //go:build integration):**
- Scope: Multiple components interacting with live Dgraph
- External dependency: Dgraph gRPC on `localhost:9080`
- Slow execution (seconds to minutes)
- Location: `pkg/dgraph/dgraph_*_test.go` (3 files with integration tag)
- Examples: `dgraph_writepath_test.go` (large kind-3 event persistence), `dgraph_stale_test.go` (staleness detection), `dgraph_validation_test.go` (pubkey validation)
- Conditionally skipped: `t.Skip()` if Dgraph unavailable or fixture missing
- Cleanup via `t.Cleanup()`: deletes test data after test runs

**E2E Tests:**
- Not automated (manual verification required per `CLAUDE.md` §6)
- Manual step: run crawler against live StrFry + relays on strfry host
- Verifies: crawling throughput, relay subscriptions, Dgraph writes, graceful shutdown

## Common Patterns

**Async Testing (Context + Done Channel):**
```go
// cmd/crawler/main_test.go, lines 96-131
func TestRetryDgraph_TransientOnCancelledCtx(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel() // pre-cancel: ctx.Err() is non-nil from the first iteration

    var slept []time.Duration
    calls := 0
    fn := func() (int, error) {
        calls++
        return 0, status.Error(codes.Unavailable, "transient during shutdown")
    }

    done := make(chan error, 1)
    go func() {
        _, err := retryDgraph(ctx, "CancelDuringCall", fn, newCallMetrics(), fakeSleep(&slept))
        done <- err
    }()

    select {
    case err := <-done:
        if err == nil {
            t.Fatal("expected non-nil error when ctx cancelled, got nil")
        }
    case <-time.After(2 * time.Second):
        t.Fatalf("retryDgraph did not return within bounded time; looped %d times", calls)
    }
}
```

**Error Type Assertion Testing:**
```go
// Implicit through error classification: isDgraphTransient() function tested
// by passing gRPC status codes
fn := func() (int, error) {
    return 0, status.Error(codes.Unavailable, "transient")
}
got, err := retryDgraph(context.Background(), "Test", fn, ...)
// retryDgraph internally calls isDgraphTransient(err) and retries
```

**Boolean Success Conditions:**
```go
// pkg/dgraph/dgraph_chunks_test.go, lines 20-61
func TestChunkSlice(t *testing.T) {
    cases := []struct {
        name       string
        input      int
        wantChunks int
    }{
        {"empty", 0, 0},
        {"exactly one chunk", 200, 1},
        {"one over boundary", 201, 2},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            in := makeStrings(tc.input)
            chunks := chunkSlice(in, size)

            if len(chunks) != tc.wantChunks {
                t.Fatalf("want %d chunks, got %d", tc.wantChunks, len(chunks))
            }

            // Verify membership union equals input length
            total := 0
            for i, ch := range chunks {
                if len(ch) == 0 {
                    t.Fatalf("chunk %d is empty", i)
                }
                if len(ch) > size {
                    t.Fatalf("chunk %d exceeds size", i)
                }
                total += len(ch)
            }
            if total != tc.input {
                t.Fatalf("union mismatch: want %d items, got %d", tc.input, total)
            }
        })
    }
}
```

**Timing Invariant Testing:**
```go
// cmd/crawler/main_test.go, lines 162-187
// Test that success-only timing is recorded (no timing on failures)
func TestRetryDgraph_TransientThenSuccess(t *testing.T) {
    m := newCallMetrics()
    got, err := retryDgraph(context.Background(), "X", fn, m, fakeSleep(&slept))
    // ...
    if m.count["X"] != 1 {
        t.Errorf("expected exactly 1 recorded success for \"X\", got %d", m.count["X"])
    }
}
```

## Test Organization by Module

| Package | Files | Count | Type | Dep |
|---------|-------|-------|------|-----|
| `pkg/config` | `config_test.go` | 7 | Unit | Viper, YAML |
| `pkg/crawler` | `crawler_quorum_test.go`, `crawler_filter_test.go`, `crawler_hang_test.go` | 20+ | Unit | None |
| `pkg/dgraph` | `backoff_test.go`, `validate_test.go`, `dgraph_chunks_test.go`, `dgraph_writepath_test.go`, `dgraph_stale_test.go`, `dgraph_validation_test.go` | 30+ | Unit + Integration | Dgraph (integration only) |
| `cmd/crawler` | `main_test.go` | 7 | Unit | gRPC status codes |

---

*Testing analysis: 2026-06-16*
