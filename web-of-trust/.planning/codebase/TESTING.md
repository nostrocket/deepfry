# Testing Patterns

**Analysis Date:** 2026-06-09

## Test Status

**Current State:** No test files present in this module (`pkg/`, `cmd/`)

The codebase contains zero `*_test.go` files. The module is untested.

## Test Framework & Configuration

**Framework:** Would use standard `testing` package (Go built-in)

**Makefile Targets (defined but no tests exist):**
```bash
make test                # go test ./... -short -cover
make test-integration    # go test -tags=integration ./...
```

**Run Commands (available for future tests):**
```bash
# Unit tests (fast, short timeout)
go test ./... -short -cover

# Integration tests (requires live Dgraph at localhost:9080)
go test -tags=integration ./...

# Verbose output
go test -v ./...

# Cover mode with HTML report
go test -cover ./...
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

**Coverage:** No current coverage requirements enforced

## Integration Test Gating

**Build tag convention:**
- Tests requiring Dgraph should use `//go:build integration` directive
- Example pattern (not yet implemented):
  ```go
  //go:build integration
  // +build integration

  package dgraph

  func TestAddFollowersIntegration(t *testing.T) {
      // Requires: Dgraph at localhost:9080
      // Setup: EnsureSchema() called first
  }
  ```

**Dgraph Dependency:**
- Integration tests connect to gRPC endpoint: `localhost:9080` (default in `pkg/dgraph/dgraph.go`)
- Schema initialization required: `EnsureSchema(ctx)` must run before data operations
- Tests must clean up state (delete nodes) or use isolated graph

**Makefile Integration Test Target:**
```makefile
test-integration:
    go test -tags=integration ./...
```

This flag filters tests to only run those with `//go:build integration` tag, excluding unit tests.

## Recommended Test Structure

### Unit Tests (when added)

**File location:** Co-located with source
- `pkg/config/config_test.go` - test configuration loading/saving
- `pkg/dgraph/dgraph_test.go` - test Dgraph client query building
- `pkg/crawler/crawler_test.go` - test event processing logic (mocked relay)

**Test naming:**
```go
func TestFunctionName(t *testing.T) { ... }
func TestFunctionName_Scenario(t *testing.T) { ... }
```

**Table-driven tests pattern (Go convention):**
```go
func TestNormalizeSeedPubkeys(t *testing.T) {
    tests := []struct {
        name     string
        input    []string
        expected []string
    }{
        {
            name:     "deduplicates",
            input:    []string{"abc", "abc"},
            expected: []string{"abc"},
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := normalizeSeedPubkeys(tt.input)
            if !reflect.DeepEqual(result, tt.expected) {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

### Integration Tests

**File location:** Same as unit tests, gated with build tag
- `pkg/dgraph/dgraph_integration_test.go` or same file with `//go:build integration`

**Test pattern:**
```go
//go:build integration
// +build integration

package dgraph

import (
    "context"
    "testing"
)

func TestAddFollowersIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    // Setup
    client, err := NewClient("localhost:9080")
    if err != nil {
        t.Fatalf("failed to connect: %v", err)
    }
    defer client.Close()

    ctx := context.Background()
    if err := client.EnsureSchema(ctx); err != nil {
        t.Fatalf("failed to ensure schema: %v", err)
    }

    // Test
    pubkey := "test_pubkey_001"
    follows := map[string]struct{}{"followee1": {}, "followee2": {}}
    err = client.AddFollowers(ctx, pubkey, 1234567890, follows, false)
    if err != nil {
        t.Fatalf("AddFollowers failed: %v", err)
    }

    // Verify
    count, err := client.CountPubkeys(ctx)
    if err != nil {
        t.Fatalf("CountPubkeys failed: %v", err)
    }
    if count < 3 { // test pubkey + 2 followees
        t.Errorf("expected at least 3 pubkeys, got %d", count)
    }

    // Cleanup (delete test nodes)
    // Implementation needed
}
```

## Setup & Teardown Patterns (for integration tests)

**For tests requiring Dgraph:**

1. **Setup Phase:**
   ```go
   func setupTestClient(t *testing.T) *Client {
       client, err := NewClient("localhost:9080")
       if err != nil {
           t.Fatalf("failed to connect to Dgraph: %v", err)
       }
       ctx := context.Background()
       if err := client.EnsureSchema(ctx); err != nil {
           t.Fatalf("failed to ensure schema: %v", err)
       }
       return client
   }
   ```

2. **Cleanup Phase:**
   ```go
   defer func() {
       // Delete test data
       // Call client.DeleteNodes(ctx, testUIDs)
       client.Close()
   }()
   ```

3. **Test Isolation:**
   - Each test should use unique pubkeys (e.g., `test_<testname>_<timestamp>`)
   - Delete created nodes at end of test
   - Do NOT assume clean Dgraph state between tests

## Mocking Patterns (for unit tests)

**What to mock:**
- `*nostr.Relay` - external relay connections (use interface mocking)
- `*dgo.Dgraph` - internal client can be tested against mock data
- File I/O in config tests - use temp directories

**What NOT to mock:**
- Dgraph queries - integration tests better for complex query logic
- Date/time calculations - use fake time.Time values
- Error wrapping - test actual fmt.Errorf behavior

**Example mocking approach (not yet in codebase):**
```go
// Mock relay for crawler tests
type mockRelay struct {
    events chan *nostr.Event
}

func (m *mockRelay) QuerySync(ctx context.Context, f nostr.Filter) ([]*nostr.Event, error) {
    // Return test events
    return []*nostr.Event{...}, nil
}
```

## Test Data & Fixtures

**Config fixtures:** None yet, would use temp directories

**Example pattern (if added):**
```go
func TestLoadConfig_WithYAML(t *testing.T) {
    // Create temp config directory
    tmpDir := t.TempDir()
    
    // Write test YAML
    configPath := filepath.Join(tmpDir, "web-of-trust.yaml")
    testConfig := `relay_urls: ["wss://test.relay"]`
    if err := os.WriteFile(configPath, []byte(testConfig), 0644); err != nil {
        t.Fatalf("failed to write test config: %v", err)
    }
    
    // Test loading (would need to inject path)
    // ...
}
```

**Pubkey fixtures:** Use deterministic test keys (hex format, lowercase)
```go
const (
    testPubkey1 = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
    testPubkey2 = "f1f2f3f4f5f6f1f2f3f4f5f6f1f2f3f4f5f6f1f2f3f4f5f6f1f2f3f4f5f6f1f2"
)
```

## Coverage Goals

**Recommended targets (not enforced):**
- `pkg/config` - 80%+ (config parsing logic critical)
- `pkg/dgraph` - query building tested, integration coverage for mutations
- `pkg/crawler` - event processing logic, error handling

**Commands (`cmd/*/`) coverage:**
- Main functions typically not unit tested (integration test at binary level)
- Focus on library code (`pkg/`)

## Testing Checklist for Future Implementation

- [ ] Create `*_test.go` files in each package
- [ ] Implement table-driven tests for config normalization
- [ ] Add unit tests for Dgraph query building (no DB calls)
- [ ] Add integration tests with `//go:build integration` tag
- [ ] Create test helper functions in `_test.go` files (not exported)
- [ ] Run `make test` in CI/CD (short mode, no integration tests)
- [ ] Run `make test-integration` separately when Dgraph available
- [ ] Generate coverage reports: `go test -coverprofile=coverage.out ./...`
- [ ] Test error wrapping with `errors.Is()` and `errors.As()`
- [ ] Test context cancellation paths (graceful shutdown)

## Running Tests

**Local development:**
```bash
cd /Users/g/git/deepfry/web-of-trust

# Run unit tests only (short mode, no integration)
make test

# Run with coverage
go test ./... -short -cover

# Run integration tests (requires Dgraph at localhost:9080)
docker-compose -f docker-compose.dgraph.yml up -d
make test-integration

# Run specific package
go test -v ./pkg/dgraph -tags=integration

# Run specific test function
go test -run TestAddFollowers -v ./pkg/dgraph -tags=integration
```

**CI/CD considerations:**
- Unit tests must run in all environments: `make test`
- Integration tests optional, require Docker: `make test-integration`
- Coverage reports saved: `go test -coverprofile=coverage.out ./...`

---

*Testing analysis: 2026-06-09*
