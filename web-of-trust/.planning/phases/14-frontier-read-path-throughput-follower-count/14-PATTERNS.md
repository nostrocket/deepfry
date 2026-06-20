# Phase 14: Frontier Read-Path Throughput (`follower_count`) - Pattern Map

**Mapped:** 2026-06-20
**Files analyzed:** 5 (3 modified, 1 new cmd, 1 new/extended test)
**Analogs found:** 6 / 6

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/dgraph/dgraph.go` — `EnsureSchema` (schema add) | model/schema | transform (DDL) | existing `@index(int)` predicates in `EnsureSchema` (L64-81) | exact |
| `pkg/dgraph/dgraph.go` — `GetStalePubkeys` + `collectStale` (read rewrite) | service | request-response (read) | current frontier/aged blocks (L737-768) + `collectStale` (L775) | exact |
| `pkg/dgraph/dgraph.go` — `AddFollowers` (delta maintenance) | service | CRUD (write) | existing `existingFollows` resolution (L350,406-408) + chunked edge nquads (L457-563) | exact |
| `pkg/dgraph/dgraph.go` — `BackfillFollowerCount` (new method) | service | batch (paginated write) | `BackfillNextAttempt` (L1042-1102) | exact |
| `cmd/backfill-follower-count/main.go` (new) | cmd/CLI | batch | `cmd/healthcheck/main.go` + `cmd/pubkeys/main.go` | role-match |
| `pkg/dgraph/dgraph_follower_count_test.go` (new) | test | request-response + transform | `dgraph_stale_test.go` (integration) + `dgraph_chunks_test.go` (unit table-driven) | exact |
| `Makefile` (add build target) | config | transform | existing `build-healthcheck` target (L51-52, L36) | exact |

---

## Pattern Assignments

### Work item 1 — Schema predicate addition (`EnsureSchema`, `pkg/dgraph/dgraph.go:63`)

**Analog:** existing int-indexed predicates already declared in the same `Alter` schema string.

**Pattern to copy** (L64-81): add one predicate line and one type field. The `@index(int)` mirrors `kind3CreatedAt`/`last_attempt`/`next_attempt` exactly — required for an efficient `orderdesc`.

```go
schema := `pubkey: string @index(exact) @upsert @unique .
kind3CreatedAt: int @index(int) .
last_db_update: int @index(int) .
last_attempt: int @index(int) .
next_attempt: int @index(int) .
miss_count: int .
follower_count: int @index(int) .   // <-- ADD: int index required for orderdesc
follows: [uid] @reverse .

type Profile {
  pubkey
  follows
  kind3CreatedAt
  last_db_update
  last_attempt
  next_attempt
  miss_count
  follower_count                     // <-- ADD to type
}`
return c.dg.Alter(ctx, &api.Operation{Schema: schema})
```

**Convention:** Dgraph schema is additive — a single `c.dg.Alter` call is the only place predicates are declared (CONTEXT §"Established Patterns"). No migration step. Update the doc comment above `EnsureSchema` to note Phase 14 adds `follower_count` (additive only), matching the existing "Phase 8 adds next_attempt and miss_count" comment style at L62.

---

### Work item 2 — Read-path rewrite (`GetStalePubkeys`, `pkg/dgraph/dgraph.go:728`)

**Analog:** the current frontier (L737-746) and aged (L755-764) query construction, plus `collectStale` (L775-799) which is unchanged.

**Current frontier block to REPLACE** (L737-746):

```go
frontierQuery := fmt.Sprintf(`
{
    var(func: has(pubkey)) @filter(NOT has(last_attempt)) {
        fc as count(~follows)
    }
    frontier(func: uid(fc), first: %d, orderdesc: val(fc)) {
        pubkey
        kind3CreatedAt
    }
}`, limit)
```

**Rewrite to** (drop the `var{}` block; root the selection on `has(pubkey)` + the existing `NOT has(last_attempt)` filter, order by the stored predicate, keep explicit `first: N`):

```go
frontierQuery := fmt.Sprintf(`
{
    frontier(func: has(pubkey), first: %d, orderdesc: follower_count) @filter(NOT has(last_attempt)) {
        pubkey
        kind3CreatedAt
    }
}`, limit)
```

**Current aged block to REPLACE** (L755-764):

```go
agedQuery := fmt.Sprintf(`
{
    var(func: has(next_attempt)) @filter(lt(next_attempt, %d)) {
        ac as count(~follows)
    }
    aged(func: uid(ac), first: %d, orderdesc: val(ac)) {
        pubkey
        kind3CreatedAt
    }
}`, nowUnix, remaining)
```

**Rewrite to** (same shape, ordered by stored predicate; preserve `lt(next_attempt, now)` and `first: remaining`):

```go
agedQuery := fmt.Sprintf(`
{
    aged(func: has(next_attempt), first: %d, orderdesc: follower_count) @filter(lt(next_attempt, %d)) {
        pubkey
        kind3CreatedAt
    }
}`, remaining, nowUnix)
```

**Conventions / required edits:**
- `collectStale` (L775) is reused **verbatim** — block names stay `"frontier"`/`"aged"`, read-only txn via `c.dg.NewReadOnlyTxn()`, same `{pubkey, kind3CreatedAt}` unmarshal struct.
- Update the large doc comment at L693-727: the `D-09 val(fc)` rationale (L699-705) and the `D-10` "stored follower_count predicate is NOT needed" note (L704) are now **superseded** — rewrite to state the perf rationale (CONTEXT §specifics). Keep the WR-05 sort-cap note but reframe: after backfill every frontier node has `follower_count`, so `orderdesc: follower_count` + explicit `first: N` is the intended pattern (re-verified in TEST-03).
- Caller signature unchanged (`olderThanUnix int64, limit int`) — `cmd/crawler` is untouched (CONTEXT §Integration Points).

---

### Work item 3 — Delta maintenance in `AddFollowers` (`pkg/dgraph/dgraph.go:252`)

**Analog:** `AddFollowers` already (a) loads `existingFollows` as a `pubkey→uid` map, and (b) resolves/creates followee UIDs in `batchSize` windows and accumulates edge nquads. The delta sets are computable from data already in hand — no extra query.

**Existing data sources to reuse:**

`existingFollows` populated at L406-408 (the signer's prior follow set, before replacement):

```go
existingFollows := make(map[string]string) // pubkey -> uid   (L350)
...
for _, f := range result.Follower[0].Follows {
    existingFollows[f.Pubkey] = f.UID            // L406-408
}
```

`new` set is the incoming `follows map[string]struct{}` param; valid followees are collected into `followeeList` (L352-360) and resolved to `followeeUIDs` per window (L489-505), where missing nodes are created as `_:new_followee_i` stubs.

**Delta math (table-driven-testable pure helper, CONTEXT Area 2):**
- `added  = new − existingFollows.keys` → each added followee `<uid> <follower_count> +1`
- `removed = existingFollows.keys − new` → each removed followee `<uid> <follower_count> -1`
- unchanged (intersection) → untouched

**Edge-nquad chunking pattern to mirror for the ±1 count nquads** (L545-563): split accumulated lines into `chunkSlice(lines, batchSize)` windows, one `txn.Mutate` per window inside the same txn, each wrapped in `withWindowTimeout`:

```go
for _, edgeWindow := range chunkSlice(edgeLines, batchSize) {
    edgeNQuads := strings.Join(edgeWindow, "\n") + "\n"
    mu := &api.Mutation{SetNquads: []byte(edgeNQuads), CommitNow: false}
    progress.beginChunk("create_follow_edges", progress.completedChunks+1)
    windowCtx, windowCancel = withWindowTimeout(queryCtx)
    _, err = txn.Mutate(windowCtx, mu)
    windowCancel()
    if err != nil { return fail("create follow edges failed: %w", err) }
    progress.completeChunk()
}
```

**Conventions:**
- All count-update nquads go in the **same `txn`** (`CommitNow:false`, single `txn.Commit` at L569) — preserves kind-3 all-or-nothing semantics (DWRITE-02 spirit, CONTEXT Area 2).
- Dgraph has no native increment in a `SetNquads` upsert here; the delta is computed in Go from `existingFollows` vs `new`, then the resulting target value (or signed adjustment via an upsert `var`+`val` block) is written. Simplest path consistent with the "ordering hint, not authoritative" accuracy contract: read each affected followee's current `follower_count` from the data already resolved, or use a DQL `upsert{ query ... mutation }` block — at Claude's discretion, but stay inside the existing single txn.
- **New followee stubs** created at L488-505 must initialize `follower_count = 1` (signer is their first observed follower, CONTEXT Area 2). Add the predicate to the stub creation nquads alongside `<pubkey>` and `<dgraph.type>`:

  ```go
  createNQuads += fmt.Sprintf("_:%s <pubkey> %q .\n", blankNodeID, followee)
  createNQuads += fmt.Sprintf("_:%s <dgraph.type> \"Profile\" .\n", blankNodeID)
  createNQuads += fmt.Sprintf("_:%s <follower_count> \"1\" .\n", blankNodeID)  // <-- ADD
  ```

- Bump `progress.totalChunks` for any added mutation windows, mirroring L432 (`progress.totalChunks++`) so the progress/chunk accounting stays honest.
- Error wrapping uses the local `fail("... : %w", err)` helper (L272-281) — every new mutation follows that exact form.

---

### Work item 4 — Paginated idempotent backfill (`BackfillFollowerCount`, new method in `pkg/dgraph/dgraph.go`)

**Analog:** `BackfillNextAttempt` (`pkg/dgraph/dgraph.go:1042`) — the canonical paginated, idempotent, per-window-committed backfill.

**Pattern to copy** (loop with `first: batchSize, offset: 0`; read-only query per page; inline `Discard` (not deferred) so it fires every iteration; per-page `CommitNow:true` mutation; break when a page returns 0 rows):

```go
func (c *Client) BackfillFollowerCount(ctx context.Context) (int, error) {
    total := 0
    for {
        // Select a page of nodes whose follower_count is not yet set.
        // Always offset:0 — each committed window removes its rows from the
        // filtered set, so the next iteration starts from a fresh offset:0 page.
        query := fmt.Sprintf(`
        {
            nodes(func: has(pubkey), first: %d, offset: 0) @filter(NOT has(follower_count)) {
                uid
                fc: count(~follows)
            }
        }`, batchSize)

        txn := c.dg.NewReadOnlyTxn()
        resp, err := txn.Query(ctx, query)
        txn.Discard(ctx) // inline discard — fires every iteration (HARD-01)
        if err != nil { return total, fmt.Errorf("backfill follower_count query failed: %w", err) }

        var result struct {
            Nodes []struct {
                UID string `json:"uid"`
                FC  int    `json:"fc"`
            } `json:"nodes"`
        }
        if err := json.Unmarshal(resp.Json, &result); err != nil {
            return total, fmt.Errorf("backfill follower_count unmarshal failed: %w", err)
        }
        if len(result.Nodes) == 0 { break }

        var nquads strings.Builder
        for _, n := range result.Nodes {
            nquads.WriteString(fmt.Sprintf("<%s> <follower_count> \"%d\" .\n", n.UID, n.FC))
        }
        muTxn := c.dg.NewTxn()
        mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
        _, mutErr := muTxn.Mutate(ctx, mu)
        muTxn.Discard(ctx) // inline discard — fires every iteration
        if mutErr != nil { return total, fmt.Errorf("backfill follower_count mutation failed: %w", mutErr) }

        total += len(result.Nodes)
    }
    return total, nil
}
```

**Conventions / discretion (CONTEXT Area 3 + Claude's Discretion):**
- Page size: reuse `batchSize` (200) as `BackfillNextAttempt` does, OR a larger page (`cmd/pubkeys` uses 1000, `cmd/healthcheck` uses 5000 for read-only scans) — at discretion. Note `count(~follows)` per node makes the page heavier than the `next_attempt` backfill, so the smaller `batchSize` is the safer default.
- **Idempotent re-run:** the `@filter(NOT has(follower_count))` makes a first run a one-time fill, but the CONTEXT says safe to **re-run with idempotent overwrite**. For an overwrite-on-rerun variant, drop the `NOT has(follower_count)` filter and page by `uid` cursor (`func: has(pubkey), first: N` with `gt(uid, lastUID)` paging) since `offset:0` no longer self-empties. Choose one and document it in the method comment — mirror the `BackfillNextAttempt` comment style (L1050-1052 explains the offset:0 invariant).
- `count(~follows)` is the recompute used everywhere else (it's exactly the aggregate `GetStalePubkeys` previously computed inline at L740/L758).

---

### Work item 5 — Backfill CLI (`cmd/backfill-follower-count/main.go`, new) + Makefile

**Analog:** `cmd/healthcheck/main.go` (flag parsing + `dgraph.NewClient` + progress prints + `log.Fatalf`) and `cmd/pubkeys/main.go` (minimal one-shot, fixed connect).

**Pattern to copy** (from `cmd/healthcheck/main.go:16-37`):

```go
func main() {
    dgraphAddr := flag.String("dgraph-addr", "localhost:9080", "Dgraph gRPC address")
    flag.Parse()

    ctx := context.Background()
    client, err := dgraph.NewClient(*dgraphAddr)
    if err != nil {
        log.Fatalf("Failed to create Dgraph client: %v", err)
    }
    defer client.Close()

    // Ensure follower_count predicate exists before backfilling.
    if err := client.EnsureSchema(ctx); err != nil {
        log.Fatalf("Failed to ensure schema: %v", err)
    }

    updated, err := client.BackfillFollowerCount(ctx)
    if err != nil {
        log.Fatalf("Backfill failed: %v", err)
    }
    fmt.Printf("Backfilled follower_count on %d nodes.\n", updated)
}
```

**Conventions:**
- Import block: `context`, `flag`, `fmt`, `log`, `"web-of-trust/pkg/dgraph"` (exactly `cmd/healthcheck`'s shape).
- `dgraph.NewClient(addr)` + `defer client.Close()` + `context.Background()` is the universal cmd/ entry pattern.
- Optional `--dry-run`/`-v` flags at discretion, modeling `cmd/healthcheck`'s `-purge`/`-v` bool flags (L18-19). A dry-run that counts-without-writing is a good fit for an operator backfill.
- **Makefile** (mirror L51-52 + register in `build` at L36 and `.PHONY` at L30, add a `run-` target like L71-72, and a help line L119):

  ```makefile
  APP_BACKFILL_FC=backfill-follower-count        # near L1-5 with the other APP_ vars

  build-backfill-follower-count:
  	go build $(BUILD_FLAGS) -o bin/$(APP_BACKFILL_FC)$(BINARY_EXT) ./cmd/$(APP_BACKFILL_FC)
  ```
  Add `build-backfill-follower-count` to the `build:` aggregate (L36) and the `.PHONY` list (L30).

---

### Work item 6 — Tests (`pkg/dgraph/dgraph_follower_count_test.go`, new)

Two test surfaces, two analog files:

**(a) Unit / table-driven delta math** — analog `pkg/dgraph/dgraph_chunks_test.go` (no build tag = runs under `make test -short`). The delta computation (`added`/`removed`/`unchanged`) must be a **pure helper** so it is testable without Dgraph (CONTEXT Area 3: "table-driven delta math"). Mirror the `chunkSlice` testing rationale (L223-226: "pure helper (no Dgraph dependency) so it can be unit-tested as the seam"):

```go
func TestFollowerCountDelta(t *testing.T) {
    cases := []struct {
        name              string
        existing, updated []string
        wantAdded         []string
        wantRemoved       []string
    }{
        {"all new",        nil,                []string{"a","b"}, []string{"a","b"}, nil},
        {"all removed",    []string{"a","b"},  nil,               nil,               []string{"a","b"}},
        {"disjoint",       []string{"a"},      []string{"b"},     []string{"b"},     []string{"a"}},
        {"unchanged",      []string{"a","b"},  []string{"a","b"}, nil,               nil},
        {"mixed",          []string{"a","b"},  []string{"b","c"}, []string{"c"},     []string{"a"}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { /* compare sorted sets */ })
    }
}
```

This requires extracting the delta into an exported-or-package-level pure function (e.g. `followerCountDelta(existing map[string]string, updated map[string]struct{}) (added, removed []string)`) — the same seam discipline `chunkSlice` uses.

**(b) Integration / live behavior** — analog `pkg/dgraph/dgraph_stale_test.go` (`//go:build integration` tag, L1). Reuse its helpers verbatim: `mustMutate` (L86-94), `countFrontier` (L62-83), `ResolvePubkeysToUIDs`+`DeleteNodes` cleanup pattern (L160-176), unique fake pubkeys via `fmt.Sprintf("%064x", time.Now().UnixNano()+n)`.

Two integration tests to add, modeled on existing ones:
- **Query-orders-by-predicate** (rework of `TestGetStalePubkeysOrder`, L101-196): insert frontier nodes, set `follower_count` directly via `mustMutate` (no longer need to wire actual follow edges since the read path reads the stored predicate), assert `GetStalePubkeys` returns them ordered by `follower_count` DESC. Same fixture shape; same `EnsureSchema` + `NewClient("localhost:9080")` preamble (L102-110).
- **Backfill correctness/idempotency** (clone of `TestBackfillNextAttempt`, L515-617): node with follows but no `follower_count` gets `follower_count = count(~follows)`; node that already has it and re-run leaves it consistent; assert idempotent on second run (value unchanged). Reuse `queryNodeBackoff`-style helper (L380-408), extended to read `follower_count`, or a small dedicated `queryFollowerCount` helper following the same read-only-txn + unmarshal shape.

**Conventions:**
- Integration tests gate behind `//go:build integration` (run via `make test-integration`, Makefile L83-84). Live Dgraph at `localhost:9080`.
- Every integration test calls `c.EnsureSchema(ctx)` first (every existing test does — L22, L108, L207).
- Always `defer` cleanup that resolves UIDs and `DeleteNodes` (L160-176) — never leave fixtures in the live graph.

---

## Shared Patterns

### Single transaction, per-window mutations (write path)
**Source:** `AddFollowers` (`pkg/dgraph/dgraph.go:304-574`)
**Apply to:** Work item 3 delta maintenance.
`txn := c.dg.NewTxn()` once, `defer txn.Discard`, every `txn.Mutate` is `CommitNow:false` and wrapped in `withWindowTimeout(queryCtx)` (L219-221), single `txn.Commit` at the end (L569). `chunkSlice(items, batchSize)` (L227-241) bounds each mutation under the ~4MB gRPC cap.

### Paginated read-only backfill with inline Discard
**Source:** `BackfillNextAttempt` (`pkg/dgraph/dgraph.go:1042-1102`)
**Apply to:** Work item 4.
`NewReadOnlyTxn()` per page, **inline** `txn.Discard(ctx)` (NOT deferred — HARD-01, fires every loop iteration), per-page `NewTxn()` + `CommitNow:true` mutation, loop until a page yields 0 rows.

### Read-only stale-selection query → collectStale
**Source:** `collectStale` (`pkg/dgraph/dgraph.go:775-799`)
**Apply to:** Work item 2 — unchanged. Named block + `{pubkey, kind3CreatedAt}` unmarshal, `NewReadOnlyTxn()`.

### Error wrapping with `%w`
**Source:** throughout `pkg/dgraph` — `fmt.Errorf("operation failed: %w", err)`; in `AddFollowers` via the `fail(...)` closure (L272-281).
**Apply to:** all new methods/CLI. CLI uses `log.Fatalf` for fatal entry-point errors (`cmd/healthcheck` L26).

### cmd/ entry shape
**Source:** `cmd/healthcheck/main.go:16-28`
**Apply to:** Work item 5. `flag.String("dgraph-addr", "localhost:9080", ...)` → `dgraph.NewClient` → `defer Close()` → `context.Background()`.

---

## No Analog Found

None. Every Phase 14 work item maps to an established pattern in `pkg/dgraph`, `cmd/`, or the `Makefile`.

## Metadata

**Analog search scope:** `pkg/dgraph/` (dgraph.go, *_test.go), `cmd/` (healthcheck, pubkeys), `Makefile`
**Files scanned:** dgraph.go (targeted ranges: 55-175, 210-600, 690-799, 1042-1185), dgraph_stale_test.go, dgraph_chunks_test.go, cmd/healthcheck/main.go, cmd/pubkeys/main.go, Makefile
**Pattern extraction date:** 2026-06-20
