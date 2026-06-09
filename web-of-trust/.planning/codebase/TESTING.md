# Testing Patterns

**Analysis Date:** 2026-06-09

## Test Framework

**Runner:**
- Go's built-in `testing` package (`go test`) — no third-party test runner
- Config: none (no test config file; behaviour controlled via build tags and `go test` flags)

**Assertion Library:**
- None. Tests use plain `if`-condition checks with `t.Fatal`, `t.Fatalf`, and `t.Errorf`. No testify or gomega.

**Run Commands:**
```bash
make test              # go test ./... -short -cover  (unit/short tests)
make test-integration  # NOT defined in this module's Makefile (see note below)
go test ./... -short   # run only short tests, skipping integration-tagged files
go test -tags=integration ./pkg/dgraph/   # build & run integration tests
```

> **Note:** Project CLAUDE.md references `make test-integration`, but this `web-of-trust` Makefile only defines `test` (`go test ./... -short -cover`). Integration tests are gated by the `//go:build integration` tag and must be run by passing `-tags=integration` to `go test` directly, or via a sibling subsystem's target. There is no `-short` skip guard inside the test itself — the build tag is the gate.

## Test File Organization

**Location:**
- Co-located with the code under test, in the same package and directory
- Example: `pkg/dgraph/dgraph_stale_test.go` sits beside `pkg/dgraph/dgraph.go`

**Naming:**
- `<subject>_test.go`, with an optional descriptive segment: `dgraph_stale_test.go` (tests the stale-pubkey selection path)
- Test functions: `Test<Behaviour>` — `TestGetStalePubkeysIncludesFrontier`

**Package placement:**
- Tests live in the same package as the code (white-box): `package dgraph` (not `dgraph_test`), giving access to unexported fields such as `c.dg` (`pkg/dgraph/dgraph_stale_test.go:3`, `:65`)

**Current coverage:**
- Exactly one test file exists: `pkg/dgraph/dgraph_stale_test.go`. No unit-test suite exists for `pkg/crawler`, `pkg/config`, or the `cmd/*` entry points yet.

## Test Structure

**Suite Organization:**
```go
//go:build integration

package dgraph

func TestGetStalePubkeysIncludesFrontier(t *testing.T) {
    ctx := context.Background()
    c, err := NewClient("localhost:9080")
    if err != nil {
        t.Fatal(err)
    }
    defer c.Close()
    if err := c.EnsureSchema(ctx); err != nil {
        t.Fatal(err)
    }
    // ... arrange (mutate), act (query), assert ...
}
```

**Patterns:**
- **Setup:** Each test opens a live `*Client` via `NewClient("localhost:9080")`, calls `EnsureSchema(ctx)`, and `defer c.Close()`
- **Arrange:** Insert fixture nodes with raw RDF n-quads through the `mustMutate` helper
- **Act/Assert:** Call the function under test (`GetStalePubkeys`), then assert on the returned map with `if _, ok := got[stub]; !ok { t.Fatalf(...) }`
- **Assertion idiom:** `t.Fatalf` for conditions that must hold (test cannot continue), `t.Errorf` for soft checks that allow the test to keep running (`pkg/dgraph/dgraph_stale_test.go:51-57`)
- **Build-tag gating:** The `//go:build integration` tag at the top of the file excludes it from default `go test` runs, so live-Dgraph tests never run during `make test`

## Mocking

**Framework:** None. There are no mock objects, fakes, or interface-based test doubles in the codebase.

**Approach:**
- Integration tests run against a **real, live Dgraph instance** at `localhost:9080` rather than mocking the client
- The Dgraph `Client` is a concrete struct (no interface), so substitution-based mocking is not currently possible without refactoring

**What is exercised live:**
- gRPC connection, schema alteration, mutations, and read-only queries all hit the real database

**What is NOT mocked:**
- Dgraph (used live)
- Nostr relays (not covered by any automated test)

## Fixtures and Factories

**Test Data:**
```go
// Unique fake pubkeys generated from the current nanosecond timestamp,
// formatted as 64-hex-char strings to satisfy the pubkey index/format.
stub    := fmt.Sprintf("%064x", time.Now().UnixNano())
crawled := fmt.Sprintf("%064x", time.Now().UnixNano()+1)

// Inserted via raw RDF n-quads in a single committed transaction.
mustMutate(t, c, fmt.Sprintf(`_:s <pubkey> %q .
_:s <dgraph.type> "Profile" .
_:c <pubkey> %q .
_:c <dgraph.type> "Profile" .
_:c <kind3CreatedAt> "%d" .
_:c <last_db_update> "%d" .
_:c <last_attempt> "%d" .
`, stub, crawled, now, now, now))
```

**Helper functions (test-local, `t.Helper()`-marked):**
- `mustMutate(t, c, rdf)` — runs RDF n-quads in one committed transaction; fails the test on error (`pkg/dgraph/dgraph_stale_test.go:86-94`)
- `countFrontier(t, c)` — counts never-attempted (`NOT has(last_attempt)`) nodes via a read-only DQL count query, used to size the query limit dynamically (`pkg/dgraph/dgraph_stale_test.go:62-83`)

**Fixture strategy:**
- Unique pubkeys are derived from `time.Now().UnixNano()` so each run inserts fresh nodes and does not collide with existing live-graph data
- Query limits are sized **relative to the live graph** (`countFrontier(t, c) + 1000`) so assertions stay deterministic regardless of how many real stub nodes already exist (`pkg/dgraph/dgraph_stale_test.go:39-44`)

**Location:** Fixtures are inline within the test file; there is no separate `testdata/` directory or factory package.

## Coverage

**Requirements:** None enforced. No coverage threshold or CI gate.

**View Coverage:**
```bash
make test          # runs with -cover, prints per-package coverage summary
go test ./... -cover
go test -tags=integration -coverprofile=cover.out ./pkg/dgraph/
go tool cover -html=cover.out   # HTML report
```

> The `-cover` flag is included in `make test`, but because the only test is integration-tagged, the default `make test` run reports 0% / no statements covered for most packages.

## Test Types

**Unit Tests:**
- None present. No pure in-memory unit tests exist for any package.

**Integration Tests:**
- Scope: end-to-end behaviour of `pkg/dgraph` query/mutation logic against a real Dgraph
- Gated by the `//go:build integration` build tag
- Require a live Dgraph reachable at `localhost:9080` (start via `docker-compose -f docker-compose.dgraph.yml up -d` from the repo root)

**E2E Tests:**
- Not automated. Full crawler verification (running against live Dgraph + Nostr relays) is a **manual step** performed on the strfry host, per the project spec and `8pc_crawled.md` §6.

## Common Patterns

**Live-resource setup/teardown:**
```go
c, err := NewClient("localhost:9080")
if err != nil {
    t.Fatal(err)
}
defer c.Close()
```

**Read-only query inside a test helper:**
```go
txn := c.dg.NewReadOnlyTxn()
defer txn.Discard(ctx)
resp, err := txn.Query(ctx, `{ f(func: has(pubkey)) @filter(NOT has(last_attempt)) { c: count(uid) } }`)
if err != nil {
    t.Fatalf("count frontier failed: %v", err)
}
```

**Regression-guard assertions:**
```go
// Asserts a specific previously-broken behaviour stays fixed.
if _, ok := got[stub]; !ok {
    t.Fatalf("frontier stub %s was NOT selected — regression of the orderasc/1000-cap bug", stub)
}
```
The test exists specifically to lock in the fix for the `GetStalePubkeys` frontier-selection bug documented in `pkg/dgraph/dgraph.go:438-442`.

## Recommendations / Gaps

- **No unit tests** for `pkg/config` (viper loading, defaults, relay add/remove), `pkg/crawler` (event validation, chunking, backoff), or `cmd/*`. These would not require a live Dgraph and could run under default `make test`.
- **Config tests must use a temp `HOME`** — never the live `~/deepfry/web-of-trust.yaml` (per CLAUDE.md and `8pc_crawled.md` §6). Set a temporary directory via `t.Setenv("HOME", t.TempDir())` before exercising `config.LoadConfig`.
- **`make test-integration` is referenced but not defined** in this Makefile; add the target or run `go test -tags=integration ./pkg/dgraph/` directly.
- Consider introducing an interface over the Dgraph `Client` if mockable unit tests for `pkg/crawler` become desirable.

---

*Testing analysis: 2026-06-09*
