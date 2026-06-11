# Phase 6: Filter Size & Per-Relay Cap Detection - Pattern Map

**Mapped:** 2026-06-11
**Files analyzed:** 3 (2 modified source files + 1 new test file implied)
**Analogs found:** 3 / 3

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/config/config.go` | config | — | `pkg/config/config.go` (self) | exact — field addition |
| `cmd/crawler/main.go` | entry-point | request-response | `cmd/crawler/main.go` (self) | exact — constant swap |
| `pkg/crawler/crawler.go` | service | event-driven | `pkg/crawler/crawler.go` (self) | exact — struct field + method edits |
| `pkg/crawler/crawler_filter_test.go` (new) | test | — | `pkg/dgraph/dgraph_stale_test.go` | role-match |

---

## Pattern Assignments

### `pkg/config/config.go` — add `RelayFilterBatchSize` field

**Analog:** `pkg/config/config.go` (self, existing `StalePubkeyThreshold` field)

**Existing field pattern to copy** (lines 21, 62):
```go
// In Config struct:
StalePubkeyThreshold int64 `mapstructure:"stale_pubkey_threshold"`

// In LoadConfig, Viper default:
viper.SetDefault("stale_pubkey_threshold", 24*60*60) // 24 hours in seconds
```

**Apply the same pattern for the new field:**
- Add to `Config` struct after `StalePubkeyThreshold`:
  ```go
  RelayFilterBatchSize int `mapstructure:"relay_filter_batch_size"`
  ```
- Add Viper default in `LoadConfig` after the existing threshold default:
  ```go
  viper.SetDefault("relay_filter_batch_size", 100)
  ```

No other changes to config.go — `viper.Unmarshal(&cfg)` at line 102 picks up the new field automatically.

---

### `cmd/crawler/main.go` — replace `batchSize` constant with `cfg.RelayFilterBatchSize`

**Analog:** `cmd/crawler/main.go` (self, lines 109–110)

**Existing pattern** (lines 109–110):
```go
const batchSize = 500
pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, batchSize)
```

**New pattern** — delete the const, pass the config field directly:
```go
pubkeys, err := dgraphClient.GetStalePubkeys(ctx, time.Now().Unix()-cfg.StalePubkeyThreshold, cfg.RelayFilterBatchSize)
```

`cfg` is already in scope (loaded at line 42). No import changes needed.

---

### `pkg/crawler/crawler.go` — four edit sites

#### Edit 1: `relayState` struct — add `filterCap int` (line 39)

**Analog:** `relayState.failures atomic.Int32` field (lines 39–46) — same pattern: in-memory field on relay state, no locking needed when accessed from the single goroutine per relay.

**Existing struct** (lines 39–46):
```go
type relayState struct {
	url      string
	conn     *nostr.Relay
	alive    bool
	backoff  time.Duration
	retryAt  time.Time
	failures atomic.Int32
}
```

**New field to add** (after `failures`):
```go
filterCap int
```

Plain `int` (not atomic) — correct because each relay's goroutine is the sole writer; no cross-goroutine mutation occurs.

#### Edit 2: `New()` — `WithNoticeHandler` wiring and `filterCap` init (lines 85–100)

**Analog:** Existing relay connect loop in `New()` (lines 85–100):
```go
for _, url := range cfg.RelayURLs {
    relay, err := nostr.RelayConnect(context.Background(), url)
    if err != nil {
        log.Printf("WARN: Failed to connect to relay %s, removing from config: %v", url, err)
        if cfg.OnConnectFail != nil {
            cfg.OnConnectFail(url)
        }
        continue
    }
    rs := &relayState{url: url, backoff: initialBackoff, conn: relay, alive: true}
    relays = append(relays, rs)
    ...
}
```

**New pattern** — create `rs` before connecting so the NOTICE handler can close over it; pass `WithNoticeHandler` as a relay option:
```go
for _, url := range cfg.RelayURLs {
    rs := &relayState{url: url, backoff: initialBackoff, filterCap: cfg.FilterBatchSize}
    noticeHandler := nostr.WithNoticeHandler(func(notice string) {
        handleFilterNotice(rs, notice, cfg.FilterBatchSize)
    })
    relay, err := nostr.RelayConnect(context.Background(), url, noticeHandler)
    if err != nil {
        ...
        continue
    }
    rs.conn = relay
    rs.alive = true
    relays = append(relays, rs)
    ...
}
```

Key idiom: `rs` must be created before the loop body assigns to it, so the closure captures the pointer not the loop variable. The existing `go func(rs *relayState)` idiom throughout the file already demonstrates this pointer-capture discipline.

`Config` struct in `pkg/crawler/crawler.go` also needs `FilterBatchSize int` to carry the value from `cmd/crawler/main.go`.

#### Edit 3: `ReconnectRelays()` — same `WithNoticeHandler` wiring (lines 214–228)

**Analog:** Existing reconnect call (lines 215–228):
```go
relay, err := nostr.RelayConnect(ctx, rs.url)
if err != nil {
    ...
    continue
}
rs.conn = relay
rs.alive = true
rs.backoff = initialBackoff
rs.failures.Store(0)
```

**New pattern** — add `WithNoticeHandler` option, same closure as `New()`. `rs` already exists as the loop variable pointer so the closure captures it correctly:
```go
noticeHandler := nostr.WithNoticeHandler(func(notice string) {
    handleFilterNotice(rs, notice, c.filterBatchSize)
})
relay, err := nostr.RelayConnect(ctx, rs.url, noticeHandler)
```

`c.filterBatchSize` is a new field on `Crawler` to hold the configured batch size for use at reconnect time.

#### Edit 4: `queryRelay()` — chunked sub-REQ loop and connection-drop attribution (line 434)

**Analog:** Existing `queryRelay` (lines 434–495) — single `Subscribe` call, EOSE-driven return.

**Existing core pattern** (lines 436–451):
```go
sub, err := relay.Subscribe(ctx, []nostr.Filter{filter})
if err != nil {
    if ctx.Err() != nil {
        return ctx.Err()
    }
    cleanErr := cleanSubscribeError(err)
    if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write") {
        c.logRelayError("connection_lost", ...)
        return &transportError{...}
    }
    c.logRelayError("subscription_failed", ...)
    return &subscriptionError{...}
}
defer sub.Unsub()
```

**New pattern** — wrap in a chunk loop. The existing subscribe-and-drain body moves into a helper or inline loop; the outer loop iterates over `filterCap`-sized chunks of `filter.Authors`:

```go
func (c *Crawler) queryRelay(ctx context.Context, relay *nostr.Relay, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
    authors := filter.Authors
    for len(authors) > 0 {
        cap := rs.filterCap
        chunk := authors
        if len(authors) > cap {
            chunk = authors[:cap]
        }
        authors = authors[len(chunk):]

        chunkFilter := filter
        chunkFilter.Authors = chunk

        subscribeStart := time.Now()
        sub, err := relay.Subscribe(ctx, []nostr.Filter{chunkFilter})
        if err != nil {
            if ctx.Err() != nil {
                return ctx.Err()
            }
            // Connection-drop attribution: if drop happened within 500ms of
            // Subscribe, treat as filter-rejection (D-09).
            if time.Since(subscribeStart) < 500*time.Millisecond &&
                (strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write")) {
                if rs.filterCap > 10 {
                    rs.filterCap = max(rs.filterCap/2, 10)
                    log.Printf("Relay %s: filter rejection drop, halved cap to %d; retrying chunk", rs.url, rs.filterCap)
                    authors = append(chunk, authors...) // re-queue this chunk
                    continue
                }
                // Floor reached; mark dead
                return &transportError{err: fmt.Errorf("relay %s: filter cap floor reached, marking dead", rs.url)}
            }
            cleanErr := cleanSubscribeError(err)
            if strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write") {
                c.logRelayError("connection_lost", fmt.Errorf("relay %s: %s", rs.url, cleanErr))
                return &transportError{err: fmt.Errorf("relay %s: %s", rs.url, cleanErr)}
            }
            c.logRelayError("subscription_failed", fmt.Errorf("relay %s: %s", rs.url, cleanErr))
            return &subscriptionError{err: fmt.Errorf("relay %s: %s", rs.url, cleanErr)}
        }

        if err := c.drainSubscription(ctx, sub, rs.url, eventsChan); err != nil {
            sub.Unsub()
            return err
        }
        sub.Unsub()
    }
    return nil
}
```

The existing event-drain `for { select { case event... case EndOfStoredEvents... } }` loop (lines 460–494) becomes `drainSubscription` — extracted to avoid repeating it per chunk.

Note: `queryRelay` signature gains `rs *relayState` parameter so the chunk loop can read and update `rs.filterCap`. Update the single call site in `FetchAndUpdateFollows` (line 304) accordingly.

**`handleFilterNotice` helper** (new, small, in crawler.go):
```go
func handleFilterNotice(rs *relayState, notice string, minCap int) {
    lower := strings.ToLower(notice)
    if strings.Contains(lower, "filter") && strings.Contains(lower, "too large") {
        if rs.filterCap > minCap {
            rs.filterCap = max(rs.filterCap/2, minCap)
        }
        log.Printf("Relay %s NOTICE filter-too-large: halved cap to %d", rs.url, rs.filterCap)
    }
}
```

`minCap` floor is 10 (D-05); `max(a, b)` is available as a built-in since Go 1.21.

---

### `pkg/crawler/crawler_filter_test.go` (new) — unit tests

**Analog:** `pkg/dgraph/dgraph_stale_test.go` (integration test template) and existing crawler package conventions.

**Build tag pattern** (line 1 of dgraph_stale_test.go):
```go
//go:build integration
```

Phase 6 tests that do not require a live relay can use `//go:build !integration` (plain `go test`) or no tag. Tests for NOTICE handling and cap halving are pure unit tests — no build tag needed.

**Test structure pattern** (from dgraph_stale_test.go lines 15–57):
```go
func TestXxx(t *testing.T) {
    // Arrange: construct state directly
    rs := &relayState{url: "wss://example.com", filterCap: 100}

    // Act
    handleFilterNotice(rs, "Error: filter item too large", 10)

    // Assert
    if rs.filterCap != 50 {
        t.Fatalf("expected filterCap 50, got %d", rs.filterCap)
    }
}
```

Keep tests in `package crawler` (same package, not `_test`) to access unexported `relayState` and `handleFilterNotice` directly — consistent with the `dgraph` package test approach.

---

## Shared Patterns

### Error type selection: transport vs. subscription
**Source:** `pkg/crawler/crawler.go` lines 19–31, 446–451
**Apply to:** All new error return paths in `queryRelay`
```go
// Transport error (connection lost, relay to be marked dead):
return &transportError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}

// Subscription error (bad filter params, not a connection issue):
return &subscriptionError{err: fmt.Errorf("relay %s: %s", relayURL, cleanErr)}
```

### Error string detection pattern
**Source:** `pkg/crawler/crawler.go` lines 446, 595–600
**Apply to:** NOTICE handler and connection-drop attribution in `queryRelay`
```go
// Existing transport detection:
strings.Contains(err.Error(), "not connected") || strings.Contains(err.Error(), "failed to write")

// Existing cleanSubscribeError helper (lines 593–601):
func cleanSubscribeError(err error) string {
    msg := err.Error()
    if idx := strings.Index(msg, "couldn't subscribe to"); idx != -1 {
        if atIdx := strings.Index(msg[idx:], "]: "); atIdx != -1 {
            return strings.TrimSpace(msg[idx+atIdx+3:])
        }
    }
    return msg
}
```
Use `strings.ToLower` + `strings.Contains` for NOTICE text matching (NOTICE formats vary by relay and are not canonical).

### Viper default + struct field
**Source:** `pkg/config/config.go` lines 21, 62
**Apply to:** `RelayFilterBatchSize` addition
```go
// Struct field (snake_case mapstructure tag):
StalePubkeyThreshold int64 `mapstructure:"stale_pubkey_threshold"`

// Viper default (must precede ReadInConfig):
viper.SetDefault("stale_pubkey_threshold", 24*60*60)
```

### Loop-variable capture for closure over relay state
**Source:** `pkg/crawler/crawler.go` line 302
**Apply to:** `WithNoticeHandler` closure in `New()` and `ReconnectRelays()`
```go
// Existing safe capture pattern:
go func(rs *relayState) {
    defer wg.Done()
    err := c.queryRelay(relayQueryContext, rs.conn, rs.url, filter, eventsChan)
    ...
}(rs)
```
For the NOTICE handler closure, create `rs` before the `nostr.RelayConnect` call (not after) so the pointer is stable when the closure is formed.

---

## No Analog Found

None. All files have direct analogs or are self-modifications of existing files.

---

## Metadata

**Analog search scope:** `pkg/config/`, `pkg/crawler/`, `pkg/dgraph/`, `cmd/crawler/`
**Files scanned:** 5
**Pattern extraction date:** 2026-06-11
