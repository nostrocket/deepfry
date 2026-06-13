# Phase 9: Phase 8 Hardening & Resilience Follow-ups — Pattern Map

**Mapped:** 2026-06-13
**Files analyzed:** 3 modified files, 5 change sites
**Analogs found:** 5 / 5

---

## File Classification

| Modified File | Change Site | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|---|
| `pkg/dgraph/dgraph.go` | `BackfillNextAttempt` | service, pagination | batch | `GetPubkeysWithMinFollowersPaginated` + `AddFollowers` chunking | exact |
| `pkg/dgraph/dgraph.go` | `MarkAttempted` recover txn | service, mutation | CRUD | `RemovePubKeyIfNoFollowers` (defer txn.Discard) | role-match |
| `pkg/dgraph/dgraph.go` | `GetStalePubkeys` + `TestGetStalePubkeysOrder` | service + integration test | CRUD | `TestGetStalePubkeysIncludesFrontier` (same harness) | exact |
| `pkg/crawler/crawler.go` | `forwardEvent` | service, publish | request-response | `queryRelay` / `drainSubscription` bounded-ctx pattern | role-match |
| `cmd/crawler/main.go` | main loop break-on-error | entrypoint, loop | request-response | relay backoff constants + existing loop structure | role-match |

---

## Pattern Assignments

---

### HARD-01 — `BackfillNextAttempt` pagination (`pkg/dgraph/dgraph.go` lines 814–861)

**Analog 1 — `GetPubkeysWithMinFollowersPaginated`** (`pkg/dgraph/dgraph.go` lines 1031–1091):
The canonical `first:`/`offset:` pagination loop already in the file.

```go
// GetPubkeysWithMinFollowersPaginated — lines 1036–1090
offset := 0
for {
    query := fmt.Sprintf(`
    {
        popular(func: has(pubkey), first: %d, offset: %d) 
        @filter(ge(count(~follows), %d)) {
            pubkey
        }
    }`, batchSize, offset, minFollowers)

    txn := c.dg.NewTxn()
    resp, err := txn.Query(ctx, query)
    txn.Discard(ctx)        // ← inline Discard (not deferred), per-iteration

    if err != nil {
        return fmt.Errorf("query popular pubkeys failed: %w", err)
    }
    // ... unmarshal ...
    if len(result.Popular) == 0 {
        break
    }
    if err := callback(batch); err != nil {
        return fmt.Errorf("callback error: %w", err)
    }
    if len(result.Popular) < batchSize {
        break
    }
    offset += batchSize
}
```

**Analog 2 — `AddFollowers` chunked mutation loop** (`pkg/dgraph/dgraph.go` lines 278–367):
Shows how to commit per-window mutations inside a loop.

```go
// AddFollowers — lines 278–351 (resolution query per window)
for _, window := range chunkSlice(followeeList, batchSize) {
    // ... build bulkQuery for window ...
    bulkResp, err := txn.Query(queryCtx, bulkQuery)
    if err != nil {
        return fmt.Errorf("bulk query followees failed: %w", err)
    }
    // ... accumulate nquads ...
}
// Then a second pass batches edge mutations:
for _, edgeWindow := range chunkSlice(edgeLines, batchSize) {
    mu := &api.Mutation{SetNquads: []byte(edgeNQuads), CommitNow: false}
    if _, err := txn.Mutate(queryCtx, mu); err != nil {
        return fmt.Errorf("create follow edges failed: %w", err)
    }
}
```

**Current `BackfillNextAttempt` body to be replaced** (`pkg/dgraph/dgraph.go` lines 814–861):

```go
// CURRENT — loads everything in one query, writes one unbounded mutation.
query := `
{
    nodes(func: has(last_attempt)) @filter(NOT has(next_attempt)) {
        uid
        last_attempt
    }
}`
txn := c.dg.NewReadOnlyTxn()
resp, err := txn.Query(ctx, query)
txn.Discard(ctx)
// ... unmarshal all nodes ...

const hitRefreshSec = 86400 // magic number — IN-03 scope
var nquads strings.Builder
for _, n := range result.Nodes {
    nquads.WriteString(...)
}
muTxn := c.dg.NewTxn()
defer muTxn.Discard(ctx)
mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
if _, err := muTxn.Mutate(ctx, mu); err != nil {
    return 0, fmt.Errorf("backfill mutation failed: %w", err)
}
return len(result.Nodes), nil
```

**How to replace:** Convert the single query into a `first:`/`offset:` loop (mirror `GetPubkeysWithMinFollowersPaginated`). Within each page, build nquads for just that page's nodes and commit a separate `CommitNow: true` mutation — mirroring the per-window commit discipline. Accumulate the total count. For IN-03: accept a `hitRefreshCadence int64` parameter (seconds) instead of the hard-coded `86400`; the caller in `crawler.go:140` passes `int64(cfg.MissBackoff.HitRefreshCadence.Seconds())`.

**Signature change for IN-03:**
```go
// Before:
func (c *Client) BackfillNextAttempt(ctx context.Context) (int, error)

// After (IN-03):
func (c *Client) BackfillNextAttempt(ctx context.Context, hitRefreshCadence int64) (int, error)
```

**Caller update** (`pkg/crawler/crawler.go` line 140):
```go
// Before:
if count, err := dgClient.BackfillNextAttempt(ctx); err != nil {
// After:
cadenceSec := int64(cfg.MissBackoff.HitRefreshCadence.Seconds())
if count, err := dgClient.BackfillNextAttempt(ctx, cadenceSec); err != nil {
```

---

### HARD-02 — `MarkAttempted` recovery txn safety (`pkg/dgraph/dgraph.go` lines 666–696)

**Problem site — current in-place recovery txn** (`pkg/dgraph/dgraph.go` lines 666–675):

```go
// CURRENT — success branch leaves txn open, no defer txn.Discard in loop body.
txn := c.dg.NewTxn()
mu := &api.Mutation{
    SetNquads: []byte(fmt.Sprintf("<%s> <pubkey> %q .\n", garbageUID, lower)),
    CommitNow: true,
}
if _, err := txn.Mutate(ctx, mu); err != nil {
    txn.Discard(ctx)   // ← only on error branch
    log.Printf("WARN: ...")
} else {
    log.Printf("INFO: ...")
    // ← no Discard on success branch — relying on GC
}
```

**Analog — correct per-iteration discard pattern** (`pkg/dgraph/dgraph.go` lines 448–492, `RemovePubKeyIfNoFollowers`):

```go
// RemovePubKeyIfNoFollowers — lines 448–492
txn := c.dg.NewTxn()
defer txn.Discard(ctx)      // ← always discarded, even after CommitNow
// ...
_, err = txn.Mutate(ctx, mu)
if err != nil {
    return false, err
}
err = txn.Commit(ctx)
```

NOTE: `defer` inside the `for _, pk := range pubkeys` loop would defer until function return (accumulating undiscarded txns). The correct pattern is **explicit inline discard** after the Mutate branch, not `defer`. Mirror how `GetPubkeysWithMinFollowersPaginated` does it:

```go
txn := c.dg.NewTxn()
resp, err := txn.Query(ctx, query)
txn.Discard(ctx)   // ← inline, not deferred, so it fires every iteration
```

**Fix pattern for the recovery txn** (replaces lines 666–675):

```go
txn := c.dg.NewTxn()
mu := &api.Mutation{
    SetNquads: []byte(fmt.Sprintf("<%s> <pubkey> %q .\n", garbageUID, lower)),
    CommitNow: true,
}
_, mutErr := txn.Mutate(ctx, mu)
txn.Discard(ctx)  // ← always, inline, closes both success and error paths
if mutErr != nil {
    log.Printf("WARN: recover uppercase pubkey %q (uid %s) to %q failed: %v", pk, garbageUID, lower, mutErr)
} else {
    log.Printf("INFO: recovered uppercase pubkey %q (uid %s) → %q", pk, garbageUID, lower)
}
```

**CAUTION:** The VALID-03 recover-or-purge *semantics* (the if/else decision tree at lines 630–696) must be preserved verbatim. HARD-02 only adds txn hygiene (inline Discard) and code comments documenting that recovery and stamp are independent operations.

---

### HARD-03 — `forwardEvent` bounded context (`pkg/crawler/crawler.go` lines 235–255)

**Current `forwardEvent` body** (lines 235–255):

```go
func (c *Crawler) forwardEvent(ctx context.Context, event *nostr.Event) {
    if c.forwardRelay == nil || !c.forwardRelay.alive {
        return
    }
    err := c.forwardRelay.conn.Publish(ctx, *event)  // ← no per-publish timeout
    if err != nil {
        // ... backoff bookkeeping ...
    }
}
```

**Analog — bounded context for relay operations** (`pkg/crawler/crawler.go` lines 456–459, `FetchAndUpdateFollows`):

```go
// FetchAndUpdateFollows — lines 456-459
// Set timeout context for relay operations only
relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
defer cancel()
```

And the `drainSubscription` inner bounded-send pattern (lines 651–656):

```go
// drainSubscription — lines 651-656
select {
case eventsChan <- event:
    // sent successfully
case <-ctx.Done():
    return ctx.Err()
}
```

**Fix pattern for `forwardEvent`:**

```go
func (c *Crawler) forwardEvent(ctx context.Context, event *nostr.Event) {
    if c.forwardRelay == nil || !c.forwardRelay.alive {
        return
    }
    // Wrap publish in a short bounded context (c.timeout) so a hung forward
    // relay cannot stall the single-threaded drain loop (WR-04).
    pubCtx, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()
    err := c.forwardRelay.conn.Publish(pubCtx, *event)
    if err != nil {
        // ... existing backoff bookkeeping unchanged ...
    }
}
```

`c.timeout` is already a field on `Crawler` (line 99, set from `cfg.Timeout` at line 192). The caller passes `relayContext` (the long-lived main ctx) at line 587, so the bounded child context guards only the publish call, not the outer drain loop.

---

### HARD-04 — Large-frontier sort-cap integration test (`pkg/dgraph/dgraph_stale_test.go`)

**Current `TestGetStalePubkeysOrder` structure** (lines 101–196):

```go
//go:build integration   // ← file-level tag, line 1

func TestGetStalePubkeysOrder(t *testing.T) {
    ctx := context.Background()
    c, err := NewClient("localhost:9080")   // ← live Dgraph, no mock
    // ...

    // Fixture helpers
    now := time.Now().UnixNano()
    high := fmt.Sprintf("%064x", now)       // unique fake pubkey via UnixNano
    // ...

    // Insert nodes via mustMutate (shared helper, lines 86–93)
    mustMutate(t, c, fmt.Sprintf(`...`))

    // Resolve UIDs via ResolvePubkeysToUIDs (shared with real code)
    frontierUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{high, mid, low})

    defer func() {
        // Cleanup: resolve then DeleteNodes
        allUIDs, err := c.ResolvePubkeysToUIDs(ctx, []string{...})
        if err := c.DeleteNodes(ctx, toDelete); err != nil { ... }
    }()

    // Limit sized to current frontier + small headroom
    got, err := c.GetStalePubkeys(ctx, 0, countFrontier(t, c)+100)  // ← never exceeds 1000-cap regime
    // ...
    // Assertions: check presence, NOT order (map[string]int64 return)
    if _, ok := got[high]; !ok { t.Errorf(...) }
}
```

**Harness conventions to follow for any new test:**
- File tag: `//go:build integration` on line 1 of file
- Package: `package dgraph` (same package as production code — access to unexported fields)
- Unique pubkeys: `fmt.Sprintf("%064x", time.Now().UnixNano()+N)` where N is a distinct offset per fixture
- Mutations: `mustMutate(t, c, rdfString)` (lines 86–93, available to all tests in the file)
- Cleanup: always `defer` a closure that calls `c.ResolvePubkeysToUIDs` then `c.DeleteNodes`
- Frontier count helper: `countFrontier(t, c)` (lines 62–83) returns the live frontier size
- No temp HOME needed for this package — no config loading in `pkg/dgraph` tests

**Gap the new test must fill (WR-05):** `TestGetStalePubkeysOrder` uses `countFrontier()+100` as the limit, which is always < 1000 rows larger than the actual frontier, so the `orderdesc: val(fc)` sort is never exercised at >1000-row scale. The new test or documentation must address this.

**Documentation escape hatch (from CONTEXT.md):** The live-verified D-09 human checkpoint already confirmed that `first:` is honored together with `orderdesc: val(fc)` on the production graph. If a >1000-row integration test is infeasible against the test Dgraph (inserting 1001+ frontier nodes would be slow and pollute the shared DB), a doc comment on `GetStalePubkeys` citing D-09 evidence is an acceptable alternative. The doc comment should cite `08-REVIEW.md WR-05` and the checkpoint finding.

---

### RESIL-01 — Main loop retry for transient Dgraph errors (`cmd/crawler/main.go`)

**Current break-on-error sites** (`cmd/crawler/main.go` lines 113–144):

```go
// Line 113–117: GetStalePubkeys
pubkeys, err := dgraphClient.GetStalePubkeys(ctx, ...)
if err != nil {
    log.Printf("Error getting stale pubkeys: %v", err)
    break    // ← exits main loop; supervisor must restart
}

// Line 119–123: CountPubkeys
totalPubkeys, err := dgraphClient.CountPubkeys(ctx)
if err != nil {
    log.Printf("Error counting pubkeys: %v", err)
    break    // ← same
}

// Line 140–144: CountStalePubkeys
totalStale, err := dgraphClient.CountStalePubkeys(ctx)
if err != nil {
    log.Printf("Error counting stale pubkeys: %v", err)
    break    // ← same
}
```

**Analog — relay backoff constants** (`pkg/crawler/crawler.go` lines 47–50):

```go
const (
    initialBackoff = 30 * time.Second
    maxBackoff     = 5 * time.Minute
)
```

**Analog — relay reconnect backoff loop** (`pkg/crawler/crawler.go` lines 344–353):

```go
rs.retryAt = time.Now().Add(rs.backoff)
rs.backoff *= 2
if rs.backoff > maxBackoff {
    rs.backoff = maxBackoff
}
```

**grpc/status import for error classification:**

```go
// import needed (not yet in cmd/crawler/main.go):
import (
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

// Classification pattern:
func isDgraphTransient(err error) bool {
    if err == nil {
        return false
    }
    st, ok := status.FromError(err)
    if !ok {
        return false
    }
    switch st.Code() {
    case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
        return true
    default:
        return false
    }
}
```

**Fix pattern — retry wrapper for each call site:**

```go
// Sensible defaults (consistent with relay initialBackoff/maxBackoff):
const (
    dgraphRetryInitial  = 5 * time.Second
    dgraphRetryMax      = 2 * time.Minute
    dgraphRetryAttempts = 5
)

// Inline at each call site (example for CountStalePubkeys):
var (
    totalStale int
    retryDelay = dgraphRetryInitial
)
for attempt := 0; attempt < dgraphRetryAttempts; attempt++ {
    totalStale, err = dgraphClient.CountStalePubkeys(ctx)
    if err == nil {
        break
    }
    if !isDgraphTransient(err) {
        log.Printf("Fatal Dgraph error counting stale pubkeys: %v", err)
        goto exitLoop  // or: break mainLoop
    }
    log.Printf("Transient Dgraph error counting stale pubkeys (attempt %d/%d): %v; retrying in %v",
        attempt+1, dgraphRetryAttempts, err, retryDelay)
    select {
    case <-time.After(retryDelay):
    case <-ctx.Done():
        break mainLoop
    }
    retryDelay *= 2
    if retryDelay > dgraphRetryMax {
        retryDelay = dgraphRetryMax
    }
}
if err != nil {
    log.Printf("Dgraph unavailable after %d attempts, exiting: %v", dgraphRetryAttempts, err)
    break mainLoop
}
```

**Apply the same pattern to:** `GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`. The planner should also consider wrapping `MarkAttempted` (currently only `log.Printf` on error — a transient failure there defeats PERF-02 for the batch).

**Fatal vs transient distinction:**
- Transient: `codes.Unavailable` ("error reading from server: EOF", network blip), `codes.DeadlineExceeded`, `codes.ResourceExhausted`
- Fatal: `codes.InvalidArgument`, `codes.NotFound`, `codes.PermissionDenied`, `codes.Internal`, `codes.Unimplemented`, or non-gRPC errors (misconfigured endpoint, DNS failure at startup)

---

## Shared Patterns

### `defer txn.Discard(ctx)` — function-scoped only
**Source:** `pkg/dgraph/dgraph.go` lines 153–154, 449–450, 570–571, 773–774
**Apply to:** Any new read-only or read-write txn in `pkg/dgraph/dgraph.go` that lives for the duration of a function call.
```go
txn := c.dg.NewReadOnlyTxn()
defer txn.Discard(ctx)
```
**Exception (loop body):** When a txn is opened inside a `for` loop, use inline `txn.Discard(ctx)` (not `defer`) so it fires each iteration, not at function return. See `GetPubkeysWithMinFollowersPaginated` lines 1048–1050.

### Error wrapping
**Source:** throughout `pkg/dgraph/dgraph.go`
**Pattern:** `fmt.Errorf("operation description failed: %w", err)` — always wrap with `%w`, always add operation context.

### Bounded context for relay Publish/Subscribe
**Source:** `pkg/crawler/crawler.go` lines 456–459
**Apply to:** any `relay.Publish(...)` or `relay.Subscribe(...)` call — always wrap in `context.WithTimeout(parentCtx, c.timeout)`.

### Integration test cleanup
**Source:** `pkg/dgraph/dgraph_stale_test.go` lines 160–176
**Apply to:** all integration tests that insert fixture nodes — always `defer` a cleanup closure that calls `ResolvePubkeysToUIDs` then `DeleteNodes`.

---

## No Analog Found

None. All five change sites have direct analogs in the existing codebase.

---

## Metadata

**Analog search scope:** `pkg/dgraph/`, `pkg/crawler/`, `cmd/crawler/`
**Files scanned:** `dgraph.go` (1250 lines), `crawler.go` (985 lines), `main.go` (207 lines), `dgraph_stale_test.go` (618 lines)
**Pattern extraction date:** 2026-06-13
