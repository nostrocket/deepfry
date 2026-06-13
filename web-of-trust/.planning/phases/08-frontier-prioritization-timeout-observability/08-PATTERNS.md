# Phase 8: Frontier Prioritization, Timeout & Observability - Pattern Map

**Mapped:** 2026-06-13
**Files analyzed:** 5 (4 modified, 1 test extension)
**Analogs found:** 5 / 5

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/dgraph/dgraph.go` (schema + EnsureSchema) | model/schema | CRUD | `pkg/dgraph/dgraph.go:60-74` (existing `last_attempt` predicate addition, Phase 5) | exact |
| `pkg/dgraph/dgraph.go` (GetStalePubkeys + collectStale) | service/query | CRUD | `pkg/dgraph/dgraph.go:500-563` (current two-phase implementation) | exact |
| `pkg/dgraph/dgraph.go` (MarkAttempted extension) | service/mutation | CRUD | `pkg/dgraph/dgraph.go:565-676` (existing nquad stamping + VALID-03 gate) | exact |
| `pkg/dgraph/dgraph.go` (new CountStalePubkeys) | service/query | CRUD | `pkg/dgraph/dgraph.go:679-711` (`CountPubkeys` count() query pattern) | role-match |
| `pkg/crawler/crawler.go` (FetchAndUpdateFollows + drainSubscription) | service | request-response | `pkg/crawler/crawler.go:418-608` (current select loop + drainSubscription) | exact |
| `pkg/config/config.go` (Config struct + SetDefault) | config | - | `pkg/config/config.go:14-101` (EjectionThresholds nested struct + SetDefault map pattern) | exact |
| `cmd/crawler/main.go` (main loop wiring) | controller | request-response | `cmd/crawler/main.go:100-165` (current GetStalePubkeys → FetchAndUpdateFollows → MarkAttempted loop) | exact |
| `pkg/dgraph/dgraph_stale_test.go` (new test cases) | test | CRUD | `pkg/dgraph/dgraph_stale_test.go:1-94` (mustMutate + countFrontier + timestamp-fixture conventions) | exact |

---

## Pattern Assignments

### `pkg/dgraph/dgraph.go` — Schema predicates (lines 60-74)

**Analog:** `pkg/dgraph/dgraph.go:60-74` — existing EnsureSchema schema string.

**Schema string pattern** (lines 61-73):
```go
schema := `pubkey: string @index(exact) @upsert @unique .
kind3CreatedAt: int @index(int) .
last_db_update: int @index(int) .
last_attempt: int @index(int) .
follows: [uid] @reverse .

type Profile {
  pubkey
  follows
  kind3CreatedAt
  last_db_update
  last_attempt
}`
return c.dg.Alter(ctx, &api.Operation{Schema: schema})
```

**Add-predicate convention:** Phase 5 added `last_attempt: int @index(int) .` the same way — append to the raw string, add the name to the `type Profile {}` block, and pass to `c.dg.Alter`. New predicates for Phase 8:
- `next_attempt: int @index(int) .` — must be indexed for `lt(next_attempt, now)` filter in aged query
- `miss_count: int .` — no index needed (never filtered on, only read per-node)

Both go in the schema string and in the `type Profile {}` block.

---

### `pkg/dgraph/dgraph.go` — GetStalePubkeys + collectStale (lines 490-563)

**Analog:** `pkg/dgraph/dgraph.go:490-563` — current two-phase implementation.

**Frontier query pattern** (lines 508-515) — template for PERF-01 ordered frontier:
```go
frontierQuery := fmt.Sprintf(`
{
    frontier(func: has(pubkey), first: %d) @filter(NOT has(last_attempt)) {
        pubkey
        kind3CreatedAt
    }
}`, limit)
if err := c.collectStale(ctx, frontierQuery, "frontier", out); err != nil {
    return nil, err
}
```

Phase 8 change: add `orderdesc: count(~follows)` to both frontier and aged func lines. Aged query also replaces `lt(last_attempt, olderThanUnix)` filter with `lt(next_attempt, now)` and switches the root func from `has(last_attempt)` to `has(next_attempt)`:
```go
// Phase 1 (PERF-01): frontier ordered by follower count DESC
frontierQuery := fmt.Sprintf(`
{
    frontier(func: has(pubkey), first: %d, orderdesc: count(~follows)) @filter(NOT has(last_attempt)) {
        pubkey
        kind3CreatedAt
    }
}`, limit)

// Phase 2 (PERF-01 + PERF-02): aged eligible under next_attempt, also ordered by follower count
if remaining := limit - len(out); remaining > 0 {
    nowUnix := time.Now().Unix()
    agedQuery := fmt.Sprintf(`
    {
        aged(func: has(next_attempt), first: %d, orderdesc: count(~follows))
        @filter(lt(next_attempt, %d)) {
            pubkey
            kind3CreatedAt
        }
    }`, remaining, nowUnix)
    if err := c.collectStale(ctx, agedQuery, "aged", out); err != nil {
        return nil, err
    }
}
```

**collectStale pattern** (lines 537-563) — unchanged; reused as-is by both query phases:
```go
func (c *Client) collectStale(
    ctx context.Context,
    query, block string,
    out map[string]int64,
) error {
    txn := c.dg.NewReadOnlyTxn()
    defer txn.Discard(ctx)
    resp, err := txn.Query(ctx, query)
    if err != nil {
        return fmt.Errorf("query stale pubkeys (%s) failed: %w", block, err)
    }
    var parsed map[string][]struct {
        Pubkey         string `json:"pubkey"`
        Kind3CreatedAt int64  `json:"kind3CreatedAt"`
    }
    if err := json.Unmarshal(resp.Json, &parsed); err != nil {
        return fmt.Errorf("unmarshal stale pubkeys (%s) failed: %w", block, err)
    }
    for _, n := range parsed[block] {
        out[n.Pubkey] = n.Kind3CreatedAt
    }
    return nil
}
```

---

### `pkg/dgraph/dgraph.go` — MarkAttempted extension (lines 565-676)

**Analog:** `pkg/dgraph/dgraph.go:565-676` — existing MarkAttempted.

**Signature change (D-05):** Add a `hits map[string]struct{}` parameter so callers can pass the pubkeys-with-events set. The function applies hit vs miss stamping per pubkey instead of a flat `last_attempt` stamp for all.

**Nquad stamping pattern** (lines 661-676) — template for new predicates:
```go
var nquads strings.Builder
for _, uid := range uids {
    nquads.WriteString(fmt.Sprintf("<%s> <last_attempt> \"%d\" .\n", uid, ts))
}
// ...
txn := c.dg.NewTxn()
defer txn.Discard(ctx)
mu := &api.Mutation{SetNquads: []byte(nquads.String()), CommitNow: true}
if _, err := txn.Mutate(ctx, mu); err != nil {
    return fmt.Errorf("mark attempted failed: %w", err)
}
```

Phase 8 adds `next_attempt` and `miss_count` to the nquad writer, branching on `hits`:
```go
// HIT path (D-03): reset backoff
nquads.WriteString(fmt.Sprintf("<%s> <next_attempt> \"%d\" .\n", uid, ts+cfg.HitRefreshInterval))
nquads.WriteString(fmt.Sprintf("<%s> <miss_count> \"0\" .\n", uid))
// MISS path (D-04): exponential backoff, capped at 7d
interval := min(cfg.BackoffBase * (1 << missCount), cfg.BackoffCap)
nquads.WriteString(fmt.Sprintf("<%s> <next_attempt> \"%d\" .\n", uid, ts+int64(interval.Seconds())))
nquads.WriteString(fmt.Sprintf("<%s> <miss_count> \"%d\" .\n", uid, missCount+1))
```

**VALID-03 gate must be preserved** — the recover-or-purge block (lines 577-652) is unchanged; new stamping logic runs only in the final nquad builder after the valid slice is collected.

---

### `pkg/dgraph/dgraph.go` — New CountStalePubkeys (analog: CountPubkeys, lines 679-711)

**Analog:** `pkg/dgraph/dgraph.go:679-711` — CountPubkeys.

**Count query pattern** (lines 681-710):
```go
func (c *Client) CountPubkeys(ctx context.Context) (int, error) {
    query := `
    {
        count(func: has(pubkey)) {
            count(uid)
        }
    }`
    txn := c.dg.NewTxn()
    defer txn.Discard(ctx)
    resp, err := txn.Query(ctx, query)
    if err != nil {
        return 0, fmt.Errorf("query pubkey count failed: %w", err)
    }
    var result struct {
        Count []struct {
            Count int `json:"count"`
        } `json:"count"`
    }
    if err := json.Unmarshal(resp.Json, &result); err != nil {
        return 0, fmt.Errorf("unmarshal pubkey count failed: %w", err)
    }
    if len(result.Count) == 0 {
        return 0, nil
    }
    return result.Count[0].Count, nil
}
```

**CountStalePubkeys shape (D-16):** Two separate count queries (frontier + aged), summed:
```go
func (c *Client) CountStalePubkeys(ctx context.Context) (int, error) {
    nowUnix := time.Now().Unix()
    query := fmt.Sprintf(`
    {
        frontier(func: has(pubkey)) @filter(NOT has(last_attempt)) {
            count(uid)
        }
        aged(func: has(next_attempt)) @filter(lt(next_attempt, %d)) {
            count(uid)
        }
    }`, nowUnix)
    // unmarshal pattern identical to CountPubkeys but two named blocks
    // return frontierCount + agedCount, nil
}
```

Use `c.dg.NewReadOnlyTxn()` (read-only, like collectStale) since this is a count-only query.

---

### `pkg/crawler/crawler.go` — FetchAndUpdateFollows + drainSubscription

**Analog:** `pkg/crawler/crawler.go:418-608` (current implementation).

**Timeout context pattern** (line 424) — already present, TIMEOUT-01 is a default change only:
```go
relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
defer cancel()
```

**Relay fan-out goroutine pattern** (lines 428-445) — template for quorum counter increment:
```go
for _, rs := range c.relays {
    if !rs.alive {
        continue
    }
    wg.Add(1)
    go func(rs *relayState) {
        defer wg.Done()
        err := c.queryRelay(relayQueryContext, rs, filter, eventsChan)
        if err != nil {
            errorsChan <- relayError{url: rs.url, err: err}
            return  // ← quorum counter must also increment here (D-14: errors count toward done)
        }
        // success counters reset here...
    }(rs)
}
```

**Atomic counter pattern** from `relayState` (lines 73-91) — the established lock-free pattern to copy for the quorum `done` counter:
```go
type relayState struct {
    // ...
    failTransport atomic.Int32
    failFilterRej atomic.Int32
    failSubFlap   atomic.Int32
    filterCap     atomic.Int32
    successStreak atomic.Int32
    probing       atomic.Bool
}
```

Phase 8 declares the quorum counter as a **function-local** `atomic.Int32` (not on relayState — it is per-batch, not per-relay lifetime):
```go
var done atomic.Int32
queriedRelays := int32(aliveCount) // count of goroutines launched

// In each goroutine, after queryRelay returns (success OR error):
done.Add(1)
if float64(done.Load()) >= math.Ceil(float64(queriedRelays)*c.quorum) {
    cancel() // early exit — quorum reached
}
```

**drainSubscription EOSE return** (lines 589-593) — the per-relay "done" signal; the goroutine calling drainSubscription invokes the quorum increment after drainSubscription returns (either EOSE or error):
```go
case <-sub.EndOfStoredEvents:
    if c.debug {
        log.Printf("EOSE received from relay %s", relayURL)
    }
    return nil  // ← caller increments quorum counter after this return
```

**Return signature:** `FetchAndUpdateFollows` currently returns `(int, error)` (count of pubkeys-with-events). Phase 8 needs the actual hit-set (not just count) to pass to `MarkAttempted`. Change return type to `(map[string]struct{}, error)` — `pubkeysWithEvents` is already built at line 458.

---

### `pkg/config/config.go` — Config struct + SetDefault

**Analog:** `pkg/config/config.go:14-101` — EjectionThresholds nested struct and its SetDefault map.

**Nested struct pattern** (lines 14-43):
```go
type EjectionThresholds struct {
    Transport int `mapstructure:"transport"`
    FilterRej int `mapstructure:"filter_rejection"`
    SubFlap   int `mapstructure:"subscription_flap"`
}

type Config struct {
    // ...
    RelayEjectionThresholds EjectionThresholds `mapstructure:"relay_ejection_thresholds"`
}
```

**SetDefault map pattern for nested struct** (lines 96-101):
```go
viper.SetDefault("relay_ejection_thresholds", map[string]interface{}{
    "transport":         10,
    "filter_rejection":  3,
    "subscription_flap": 5,
})
```

**Phase 8 additions** follow the exact same pattern:

Config struct additions:
```go
// Relay EOSE quorum (Phase 8 TIMEOUT-02): fraction of queried relays that must
// reach EOSE or error before the batch cancels early.
RelayEOSEQuorum float64 `mapstructure:"relay_eose_quorum"`

// PERF-02 miss-backoff parameters.
BackoffBase        time.Duration `mapstructure:"backoff_base"`
BackoffRatio       int           `mapstructure:"backoff_ratio"`
BackoffCap         time.Duration `mapstructure:"backoff_cap"`
HitRefreshInterval time.Duration `mapstructure:"hit_refresh_interval"`
```

Or group as a nested struct following the EjectionThresholds precedent:
```go
type MissBackoffParams struct {
    Base            time.Duration `mapstructure:"base"`
    Ratio           int           `mapstructure:"ratio"`
    Cap             time.Duration `mapstructure:"cap"`
    HitRefreshCadence time.Duration `mapstructure:"hit_refresh_cadence"`
}
// in Config:
MissBackoff MissBackoffParams `mapstructure:"miss_backoff"`
```

SetDefault calls:
```go
viper.SetDefault("timeout", "15s")         // TIMEOUT-01: was "30s"
viper.SetDefault("relay_eose_quorum", 0.70) // TIMEOUT-02
viper.SetDefault("miss_backoff", map[string]interface{}{
    "base":               "2h",
    "ratio":              2,
    "cap":                "168h",  // 7 days
    "hit_refresh_cadence": "24h",  // StalePubkeyThreshold repurposed
})
```

---

### `cmd/crawler/main.go` — Main loop wiring

**Analog:** `cmd/crawler/main.go:100-165` — current main loop.

**GetStalePubkeys call site** (line 111):
```go
pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
```
Phase 8: the `olderThanUnix` parameter is no longer needed by the aged phase (it now uses `lt(next_attempt, now)` internally), but the argument can remain or be removed — planner's call.

**FetchAndUpdateFollows + MarkAttempted wiring** (lines 142-160):
```go
hadEvents, err := crawler.FetchAndUpdateFollows(ctx, pubkeys)
// ...
batchKeys := make([]string, 0, len(pubkeys))
for pk := range pubkeys {
    batchKeys = append(batchKeys, pk)
}
if err := dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix()); err != nil {
    log.Printf("Warning: failed to mark batch attempted: %v", err)
}
```

Phase 8 change: `FetchAndUpdateFollows` returns `(map[string]struct{}, error)` instead of `(int, error)`. The hit-set flows directly to `MarkAttempted`:
```go
hitSet, err := crawler.FetchAndUpdateFollows(ctx, pubkeys)
// ...
if err := dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix(), hitSet); err != nil {
    log.Printf("Warning: failed to mark batch attempted: %v", err)
}
```

**staleRemaining bug fix** (lines 136, 162-163) — current broken code:
```go
totalStale := len(pubkeys)       // line 136: always equals batch size
// ...
staleRemaining := totalStale - len(pubkeys)  // line 162: always 0
```

Phase 8 replacement (D-15/D-16) — add `CountStalePubkeys` call before the batch, follow the same error-check pattern as `CountPubkeys` at line 117:
```go
totalStale, err := dgraphClient.CountStalePubkeys(ctx)
if err != nil {
    log.Printf("Error counting stale pubkeys: %v", err)
    break
}
// ...
staleRemaining := totalStale - len(pubkeys)
log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d stale remaining | %d total in DB",
    len(pubkeys), len(hitSet), staleRemaining, totalPubkeys)
```

---

### `pkg/dgraph/dgraph_stale_test.go` — New test cases

**Analog:** `pkg/dgraph/dgraph_stale_test.go:1-94` — existing integration test conventions.

**Build tag + package** (lines 1-2):
```go
//go:build integration

package dgraph
```

**mustMutate helper** (lines 85-94) — reuse as-is for fixture setup:
```go
func mustMutate(t *testing.T, c *Client, rdf string) {
    t.Helper()
    ctx := context.Background()
    txn := c.dg.NewTxn()
    defer txn.Discard(ctx)
    if _, err := txn.Mutate(ctx, &api.Mutation{SetNquads: []byte(rdf), CommitNow: true}); err != nil {
        t.Fatalf("mustMutate failed: %v", err)
    }
}
```

**Timestamp-fixture pubkey convention** (lines 27-28):
```go
stub    := fmt.Sprintf("%064x", time.Now().UnixNano())
crawled := fmt.Sprintf("%064x", time.Now().UnixNano()+1)
```

**Test structure pattern** (lines 15-57) — each test: NewClient → EnsureSchema → mustMutate fixtures → call function under test → assert. New tests follow this structure for:
- PERF-01: assert frontier and aged results come back ordered by descending `count(~follows)` (insert nodes with different follower counts, verify order)
- PERF-02 backoff: assert miss increments `miss_count` and sets `next_attempt` per schedule; assert hit resets both; assert D-06 backfill sets correct values
- METRIC-01: assert `CountStalePubkeys` returns frontier + aged total matching expected fixture count

**Backoff math unit tests** (no Dgraph needed): follow standard `go test` (no build tag), test the interval calculation function directly with table-driven cases for miss_count 0..8 and cap behavior.

---

## Shared Patterns

### Context + cancel pattern
**Source:** `pkg/crawler/crawler.go:424-425`
**Apply to:** FetchAndUpdateFollows quorum wiring
```go
relayQueryContext, cancel := context.WithTimeout(relayContext, c.timeout)
defer cancel()
// cancel() is also called early by the quorum goroutine — safe to call multiple times
```

### Error wrapping
**Source:** `pkg/dgraph/dgraph.go:549` (collectStale)
**Apply to:** All new Dgraph functions
```go
return fmt.Errorf("query stale pubkeys (%s) failed: %w", block, err)
```

### Read-only txn for queries
**Source:** `pkg/dgraph/dgraph.go:544` (collectStale), `pkg/dgraph/dgraph.go:725` (GetKind3CreatedAt)
**Apply to:** `CountStalePubkeys` and any other new read-only Dgraph queries
```go
txn := c.dg.NewReadOnlyTxn()
defer txn.Discard(ctx)
```

### Debug guard
**Source:** `pkg/crawler/crawler.go:464-466`
**Apply to:** Quorum counter log lines, per-relay EOSE log
```go
if c.debug {
    log.Printf("...")
}
```

### Config SetDefault for nested map
**Source:** `pkg/config/config.go:96-101`
**Apply to:** `miss_backoff` param group (D-07)
```go
viper.SetDefault("miss_backoff", map[string]interface{}{
    "base":  "2h",
    "ratio": 2,
    "cap":   "168h",
    "hit_refresh_cadence": "24h",
})
```

---

## No Analog Found

All files have close analogs. No new directories or novel patterns are introduced.

---

## Metadata

**Analog search scope:** `pkg/dgraph/`, `pkg/crawler/`, `pkg/config/`, `cmd/crawler/`
**Files scanned:** 5 source files, 1 test file
**Pattern extraction date:** 2026-06-13
