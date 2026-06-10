# Phase 3: Write-Path Correctness + Regression Coverage - Pattern Map

**Mapped:** 2026-06-09
**Files analyzed:** 6 new/modified files
**Analogs found:** 6 / 6

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `pkg/dgraph/dgraph.go` (modify: `AddFollowers`, validation gate) | service | CRUD | `pkg/dgraph/dgraph.go` itself | self |
| `pkg/crawler/chunks.go` (delete entirely) | utility | batch | `pkg/crawler/chunks.go` | self (delete) |
| `pkg/crawler/crawler.go` (remove `>10000` branch at :533-535) | service | event-driven | `pkg/crawler/crawler.go` | self |
| `pkg/dgraph/validate.go` (new: shared pubkey validator) | utility | transform | `cmd/healthcheck/main.go:17` | role-match |
| `pkg/dgraph/dgraph_chunks_test.go` (new: unit test, TEST-04) | test | transform | `pkg/dgraph/dgraph_stale_test.go` | role-match |
| `pkg/dgraph/dgraph_writepath_test.go` (new: integration test, TEST-03) | test | CRUD | `pkg/dgraph/dgraph_stale_test.go` | exact |

---

## Pattern Assignments

### `pkg/dgraph/dgraph.go` — modify `AddFollowers` + add internal batching (D-01, D-05, D-06, D-07, D-08, D-09)

**Analog:** `pkg/dgraph/dgraph.go` (self — restructure existing function)

**Existing imports block** (lines 1-15) — keep as-is, no new imports needed:
```go
package dgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)
```

**Version guard / dedup pattern** (lines 163-168) — runs ONCE at the top of the unified write; unchanged logic:
```go
existingKind3CreatedAt := result.Follower[0].Kind3CreatedAt
if kind3createdAt <= existingKind3CreatedAt {
    // Skip update - existing event is newer or same age
    return nil
}
```

**Transaction lifecycle pattern** (lines 99-101, 309-311) — one `txn` spans the whole operation:
```go
txn := c.dg.NewTxn()
defer txn.Discard(queryCtx)
// ... all Mutate / Query calls on same txn ...
if err := txn.Commit(queryCtx); err != nil {
    return fmt.Errorf("commit transaction failed: %w", err)
}
```

**Internal batch loop pattern for followee resolution** (lines 216-232) — the bulk query string itself must be batched in 200-item windows. The existing single-pass pattern to extend:
```go
// Build single query for all followees (EXISTING — must be wrapped in loop of 200-item windows)
var queryParts []string
for i, followee := range followeeList {
    part := fmt.Sprintf(
        `followee_%d(func: eq(pubkey, %q)) { uid }`,
        i,
        followee,
    )
    queryParts = append(queryParts, part)
}
bulkQuery := fmt.Sprintf("{ %s }", strings.Join(queryParts, "\n"))
```

**Size-scaled timeout pattern** (D-07) — derive from follow count, not fixed:
```go
// Base 30 s + 5 s per 200-item batch; formula lives here so it is easy to tune.
const (
    baseTimeout     = 30 * time.Second
    perBatchTimeout = 5 * time.Second
    batchSize       = 200
)
batches := (len(follows) + batchSize - 1) / batchSize
deadline := baseTimeout + time.Duration(batches)*perBatchTimeout
queryCtx, cancel := context.WithTimeout(ctx, deadline)
defer cancel()
```

**Skip-and-log on invalid signer / followee** (D-09) — matches crawler convention at `crawler.go:266,507`:
```go
// Invalid signer → return error, nothing written
if !isValidHexPubkey(signerPubkey) {
    return fmt.Errorf("invalid signer pubkey %q: must be 64 hex chars", signerPubkey)
}
// Invalid followee → skip entry, log, continue
if !isValidHexPubkey(followee) {
    log.Printf("WARN: skipping invalid followee pubkey %q for signer %s", followee, signerPubkey)
    continue
}
```

**Error wrapping pattern** (lines 117-118, 157-158, etc.) — always `%w`:
```go
return fmt.Errorf("query follower failed: %w", err)
return fmt.Errorf("create follower failed: %w", err)
return fmt.Errorf("bulk query followees failed: %w", err)
```

---

### `pkg/dgraph/validate.go` (new file — D-08)

**Analog:** `cmd/healthcheck/main.go` lines 17

**Source regex to promote:**
```go
// cmd/healthcheck/main.go:17
var validPubkey = regexp.MustCompile(`^[0-9a-f]{64}$`)
```

**New file pattern** — exported helper + unexported alias for package-internal use:
```go
// pkg/dgraph/validate.go
package dgraph

import "regexp"

// validHexPubkeyRe matches a valid Nostr pubkey: exactly 64 lowercase hex characters.
var validHexPubkeyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidatePubkey returns an error if pubkey is not a valid 64-char lowercase hex string.
// Phase 4 SEC-02 MUST reuse this function rather than rolling its own validator.
func ValidatePubkey(pubkey string) error {
    if !validHexPubkeyRe.MatchString(pubkey) {
        return fmt.Errorf("pubkey %q is not a valid 64-char hex Nostr pubkey", pubkey)
    }
    return nil
}

// isValidHexPubkey is the package-internal fast-path used in hot loops.
func isValidHexPubkey(pubkey string) bool {
    return validHexPubkeyRe.MatchString(pubkey)
}
```

`cmd/healthcheck/main.go:17` must then become:
```go
var validPubkey = dgraph.ValidatePubkey  // or inline call; remove local regexp
```

---

### `pkg/crawler/chunks.go` (delete entirely — D-02, D-03)

No new pattern needed. Delete the file. Remove the import of `"math"` from `crawler.go` if it is only used here (verify with `grep -n '"math"' pkg/crawler/crawler.go`).

---

### `pkg/crawler/crawler.go` — remove `>10000` branch (D-02)

**Analog:** `pkg/crawler/crawler.go` lines 533-538 (the branch to remove):
```go
// BEFORE (lines 533-538) — DELETE the if/else, keep only the direct call:
if uniqueFollowsCount > 10000 {
    err = c.processFollowsInChunks(ctx, event.PubKey, int64(event.CreatedAt), followsMap)
} else {
    err = c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, c.debug)
}

// AFTER — single write path regardless of size:
err = c.dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, c.debug)
```

---

### `pkg/dgraph/dgraph_chunks_test.go` (new — TEST-04 unit test, D-10)

**Analog:** `pkg/dgraph/dgraph_stale_test.go` — white-box `package dgraph`, plain `if`/`t.Fatalf`, no build tag, no Dgraph dependency.

**Build tag / package line** — NO `//go:build` tag (runs under `make test` / `-short`):
```go
package dgraph
```

**Test structure pattern** — plain table-driven, no testify, no external deps:
```go
func TestChunkSlice(t *testing.T) {
    cases := []struct {
        name      string
        input     int    // generate a slice of this length
        size      int
        wantChunks int
        wantTotal  int   // union of all chunks must equal input length
    }{
        {"empty", 0, 200, 0, 0},
        {"exactly one chunk", 200, 200, 1, 200},
        {"one over boundary", 201, 200, 2, 201},
        {"500", 500, 200, 3, 500},   // ceil(500/200)=3: [200,200,100]
        {"501", 501, 200, 3, 501},   // ceil(501/200)=3: [200,200,101]
        {"10000", 10000, 200, 50, 10000},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            in := makeStrings(tc.input)
            chunks := chunkSlice(in, tc.size)
            if len(chunks) != tc.wantChunks {
                t.Fatalf("want %d chunks, got %d", tc.wantChunks, len(chunks))
            }
            got := 0
            for _, ch := range chunks {
                got += len(ch)
            }
            if got != tc.wantTotal {
                t.Fatalf("want total %d items, got %d", tc.wantTotal, got)
            }
        })
    }
}
```

Note: `chunkSlice` is the pure helper extracted from `AddFollowers`'s internal batching loop (no Dgraph dependency). Exact signature at Claude's discretion per CONTEXT.md.

---

### `pkg/dgraph/dgraph_writepath_test.go` (new — TEST-03 integration test, D-11)

**Analog:** `pkg/dgraph/dgraph_stale_test.go` — exact match on build tag, package, helpers, and Dgraph lifecycle.

**Build tag** (line 1 — mandatory):
```go
//go:build integration

package dgraph
```

**Client setup pattern** (lines 16-24 of analog — copy verbatim):
```go
ctx := context.Background()
c, err := NewClient("localhost:9080")
if err != nil {
    t.Fatal(err)
}
defer c.Close()
if err := c.EnsureSchema(ctx); err != nil {
    t.Fatal(err)
}
```

**mustMutate helper** (lines 86-94 of analog — available in same package, reuse without redeclaring):
```go
// already declared in dgraph_stale_test.go — do NOT redeclare
// mustMutate(t, c, rdfString)
```

**Fixture glob + t.Skip pattern** (D-11):
```go
import (
    "encoding/json"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
    "testing"
)

func TestAddFollowersLargeKind3(t *testing.T) {
    // Locate fixture: pkg/dgraph/testdata/largest-kind3-<count>.json
    // Select the file with the highest <count> in its name.
    matches, _ := filepath.Glob("testdata/largest-kind3-*.json")
    if len(matches) == 0 {
        t.Skip("no large kind-3 fixture found; run the crawler with HARVEST_LARGEST_KIND3=1 to generate one")
    }
    // ... select highest count, load JSON, run AddFollowers, assert full membership
}
```

**Fake-pubkey generation pattern** (lines 27-28 of analog):
```go
// Use time-seeded hex strings so test nodes are unique across runs
stub := fmt.Sprintf("%064x", time.Now().UnixNano())
```

**Cleanup/teardown pattern** — use `t.Cleanup` + `DeleteNodes` to remove test-inserted nodes:
```go
t.Cleanup(func() {
    // resolve UIDs and call c.DeleteNodes(ctx, uids)
})
```

**Assertion pattern** (lines 51-57 of analog — plain `if` + `t.Fatalf`):
```go
if count != wantCount {
    t.Fatalf("expected %d follow edges, got %d — chunked write dropped %d entries",
        wantCount, count, wantCount-count)
}
```

---

### `pkg/dgraph/testdata/` directory (new fixture location — D-11)

No Go code. Fixture file committed as `pkg/dgraph/testdata/largest-kind3-<count>.json` where `<count>` is the number of p-tag entries in the event. If absent the integration test skips. Add `pkg/dgraph/testdata/` to `.gitignore` only if payload storage becomes a concern — by default the fixture IS committed (it is a real Nostr event from the wild and harmless).

---

## Shared Patterns

### Transaction lifecycle (all Dgraph write methods)
**Source:** `pkg/dgraph/dgraph.go` lines 99-101, 309-311
**Apply to:** the rewritten `AddFollowers` internals
```go
txn := c.dg.NewTxn()
defer txn.Discard(queryCtx)
// ... staged Mutate / Query calls, CommitNow: false ...
if err := txn.Commit(queryCtx); err != nil {
    return fmt.Errorf("commit transaction failed: %w", err)
}
```

### Error wrapping (all methods)
**Source:** `pkg/dgraph/dgraph.go` (pervasive `fmt.Errorf("... failed: %w", err)`)
**Apply to:** all new code in `validate.go`, test helpers, and modified `AddFollowers`
```go
return fmt.Errorf("<operation> failed: %w", err)
```

### Debug-guarded logging
**Source:** `pkg/dgraph/dgraph.go` lines 91-93, 122-124
**Apply to:** new batching loops inside `AddFollowers`
```go
if debug {
    log.Printf("DEBUG: ...", ...)
}
```

### Pubkey validation gate
**Source:** `pkg/dgraph/validate.go` (new, promoted from `cmd/healthcheck/main.go:17`)
**Apply to:** `AddFollowers` signer check (return error), followee loop (skip + log), `MarkAttempted` input loop (skip + log)
```go
if !isValidHexPubkey(pubkey) {
    log.Printf("WARN: skipping invalid pubkey %q", pubkey)
    continue
}
```

### Integration test lifecycle
**Source:** `pkg/dgraph/dgraph_stale_test.go` lines 16-24, 86-94
**Apply to:** `dgraph_writepath_test.go`
- `//go:build integration` tag, `package dgraph`, `NewClient("localhost:9080")`, `EnsureSchema`, `mustMutate` reuse, `t.Cleanup` for teardown

---

## No Analog Found

All files have analogs. No entries here.

---

## Metadata

**Analog search scope:** `pkg/dgraph/`, `pkg/crawler/`, `cmd/healthcheck/`
**Files scanned:** 4 source files read in full
**Pattern extraction date:** 2026-06-09
