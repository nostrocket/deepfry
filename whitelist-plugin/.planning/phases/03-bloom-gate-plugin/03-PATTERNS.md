# Phase 3: Bloom Gate Plugin - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 4 (3 new, 1 modified)
**Analogs found:** 4 / 4

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `cmd/bloom/main.go` | entrypoint | request-response (JSONL) | `cmd/whitelist/main.go` | exact |
| `pkg/bloom/checker.go` (or inline in `cmd/bloom`) | checker / service | request-response | `pkg/whitelist/whitelist.go` (`IsWhitelisted`) | exact role |
| `pkg/bloom/fetcher.go` (or `pkg/bloomfetcher/`) | service | periodic-refresh + file-I/O | `pkg/whitelist/whitelist_refresher.go` + `pkg/client/client.go` | exact role, composite |
| `pkg/config/config.go` (MODIFY — add `BloomConfig`/`LoadBloomConfig`) | config | — | `pkg/config/config.go` `LoadClientConfig` block | exact |

---

## Pattern Assignments

### `cmd/bloom/main.go` (entrypoint, request-response)

**Analog:** `cmd/whitelist/main.go` (all 121 lines — copy scaffolding verbatim, swap checker construction)

**Imports pattern** (lines 1-13):
```go
package main

import (
    "bufio"
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "whitelist-plugin/pkg/config"
    "whitelist-plugin/pkg/handler"
    // replace pkg/client with the new bloom fetcher / checker package
)
```

**Logger + signal context pattern** (lines 16-20):
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

logger := log.New(os.Stderr, "[bloom-plugin] ", log.LstdFlags)
```
Note: prefix changes from `[whitelist-plugin]` to `[bloom-plugin]`.

**Config load + checker construction pattern** (lines 21-34):
```go
cfg, err := config.LoadClientConfig()       // → config.LoadBloomConfig()
if err != nil {
    logger.Fatalf("Failed to load config: %v", err)
}

checker := client.NewWhitelistClient(...)   // → NewBloomChecker(cfg, logger) or fetcher wired here
```
The bloom variant constructs the checker with cold-start blocking: attempt disk load first (D-04), then fall back to a server fetch; only block (hold) if neither source has a filter (D-06).

**Handler + IOAdapter wiring pattern** (lines 35-41) — reused verbatim:
```go
h := handler.NewWhitelistHandler(checker, logger)
ioAdapter := handler.NewJSONLIOAdapter(os.Stdout)

if err := runEventLoop(ctx, h, ioAdapter, logger); err != nil {
    logger.Printf("Error in event loop: %v", err)
    os.Exit(1)
}
```

**Scanner buffer sizing pattern** (lines 45-52) — copy exactly:
```go
const (
    initBuf = 64 * 1024
    maxBuf  = 10 * 1024 * 1024
)

scanner := bufio.NewScanner(os.Stdin)
buf := make([]byte, 0, initBuf)
scanner.Buffer(buf, maxBuf)
```

**Goroutine-based line-feeding + select loop** (lines 54-88) — copy verbatim; owns the ctx-cancellation path and the lines channel.

**`processLine` + `safeOutput`** (lines 91-120) — copy verbatim; no changes needed.

---

### `pkg/bloom/checker.go` — `BloomChecker` (checker, request-response hot path)

**Primary analog:** `pkg/whitelist/whitelist.go` — `atomic.Pointer` lock-free swap model.
**Secondary analog:** `pkg/handler/handler.go` — `Checker` interface that `IsWhitelisted` must satisfy.

**`Checker` interface to implement** (`pkg/handler/handler.go` lines 3-5):
```go
type Checker interface {
    IsWhitelisted(pubkey string) (bool, error)
}
```

**`atomic.Pointer` lock-free swap model** (`pkg/whitelist/whitelist.go` lines 9-12, 46-52):
```go
type Whitelist struct {
    list atomic.Pointer[map[[32]byte]struct{}]
}

// swap on update (single writer):
wl.list.Store(&nm)

// read on every event (many readers, zero locks):
mp := wl.list.Load()
```
The bloom analog holds `atomic.Pointer[bloom.Filter]` instead of `atomic.Pointer[map[...]]`:
```go
type BloomChecker struct {
    filter atomic.Pointer[bloom.Filter]  // nil = no filter yet (GATE-06)
    ready  chan struct{}                  // closed once first filter is stored
}
```

**`IsWhitelisted` boundary pattern** (`pkg/whitelist/whitelist.go` lines 22-36):
```go
func (wl *Whitelist) IsWhitelisted(key string) (bool, error) {
    if len(key) != 64 {
        return false, nil  // bad length → not-present, no error
    }
    var k [32]byte
    if _, err := hex.Decode(k[:], []byte(strings.ToLower(key))); err != nil {
        return false, nil  // bad hex → not-present, no error
    }
    mp := wl.list.Load()
    if mp == nil {
        return false, nil
    }
    _, ok := (*mp)[k]
    return ok, nil
}
```
The bloom analog replaces the map lookup with `filter.ContainsHex(pubkey)`. The nil-filter case (no filter yet, GATE-06) must block — do NOT return `(false, nil)` — wait on the `ready` channel instead. The `handler.WhitelistHandler` interprets `(false, err)` as check-failed reject and `(false, nil)` as not-whitelisted reject; blocking avoids both by not returning at all until ready.

**`ContainsHex` call pattern** (`pkg/bloom/bloom.go` lines 134-143):
```go
func (f *Filter) ContainsHex(s string) (bool, error) {
    if len(s) != 64 {
        return false, nil   // lenient: bad hex = not-present
    }
    var k [32]byte
    if _, err := hex.Decode(k[:], []byte(strings.ToLower(s))); err != nil {
        return false, nil
    }
    return f.Contains(k), nil
}
```
This is the hot-path call; it is alloc-free on the Contains side (D-08/D-10). Call it as `filter.ContainsHex(pubkey)` directly — no extra hex decode layer needed in the checker.

**`ETag()` call pattern** (`pkg/bloom/bloom.go` lines 153-155):
```go
func (f *Filter) ETag() string {
    return `"` + hex.EncodeToString(f.gen[:]) + `"`
}
```
The fetcher reads the current in-memory filter's `ETag()` to populate `If-None-Match` on each conditional GET.

---

### `pkg/bloom/fetcher.go` — periodic conditional-GET fetcher (service, periodic-refresh + file-I/O)

**Primary analog:** `pkg/whitelist/whitelist_refresher.go` — `Start`/`Stop`/retry-loop pattern.
**Secondary analog:** `pkg/client/client.go` — `http.Client` transport construction.

**Refresher struct + constructor pattern** (`pkg/whitelist/whitelist_refresher.go` lines 11-35):
```go
type WhitelistRefresher struct {
    whitelist  *Whitelist
    keyRepo    repository.KeyRepository
    interval   time.Duration
    ctx        context.Context
    cancel     context.CancelFunc
    waitGroup  sync.WaitGroup
    retryCount int
    logger     *log.Logger
    onRefresh  func(keys [][32]byte)
}

func NewWhitelistRefresher(ctx context.Context, keyRepo repository.KeyRepository,
    interval time.Duration, retryCount int, logger *log.Logger) *WhitelistRefresher {
    ctx, cancel := context.WithCancel(ctx)
    r := &WhitelistRefresher{ ... }
    return r
}
```
The bloom fetcher analog:
```go
type BloomFetcher struct {
    checker    *BloomChecker      // writes to checker.filter; closes checker.ready
    serverURL  string
    bloomPath  string             // ~/deepfry/bloom.dfbf
    interval   time.Duration
    retryCount int
    httpClient *http.Client
    ctx        context.Context
    cancel     context.CancelFunc
    wg         sync.WaitGroup
    logger     *log.Logger
}
```

**`Start()` / `Stop()` lifecycle** (`pkg/whitelist/whitelist_refresher.go` lines 44-68):
```go
func (r *WhitelistRefresher) Start() {
    r.refresh()       // synchronous initial attempt
    r.waitGroup.Add(1)
    go func() {
        defer r.waitGroup.Done()
        ticker := time.NewTicker(r.interval)
        defer ticker.Stop()
        for {
            select {
            case <-r.ctx.Done():
                return
            case <-ticker.C:
                r.refresh()
            }
        }
    }()
}

func (r *WhitelistRefresher) Stop() {
    r.cancel()
    r.waitGroup.Wait()
}
```
The bloom fetcher's `Start()` differs: cold-start is disk-first (D-04). Load from `bloom_path` first; if that succeeds, store into `checker.filter` and close `checker.ready` immediately so the event loop unblocks before the first network fetch. Then launch the ticker goroutine as above.

**`refresh()` retry loop with linear backoff** (`pkg/whitelist/whitelist_refresher.go` lines 70-98):
```go
func (r *WhitelistRefresher) refresh() {
    for attempt := 0; attempt <= r.retryCount; attempt++ {
        keys, err := r.keyRepo.GetAll(r.ctx)
        if err != nil {
            if r.ctx.Err() != nil {
                r.logger.Printf("Refresh cancelled")
                return
            }
            r.logger.Printf("Failed to fetch keys (attempt %d/%d): %v",
                attempt+1, r.retryCount+1, err)
            if attempt < r.retryCount {
                select {
                case <-r.ctx.Done():
                    r.logger.Printf("Refresh cancelled during retry backoff")
                    return
                case <-time.After(time.Second * time.Duration(attempt+1)):
                }
            }
            continue
        }
        r.whitelist.UpdateKeys(keys)
        r.logger.Printf("whitelist refreshed with %d keys", len(keys))
        return
    }
    r.logger.Printf("Refresh failed after %d attempts", r.retryCount+1)
}
```
The bloom fetcher's `fetch()` replaces the `keyRepo.GetAll` call with the HTTP conditional GET sequence:
1. Build `GET {serverURL}/bloom` request; set `If-None-Match: {checker.filter.Load().ETag()}` if a filter is already held.
2. On `304`: no-op (nothing changed).
3. On `503`: treat as transient (same as a network error — fall back to disk; retry per D-11).
4. On `200`: read full body into `[]byte`, call `bloom.ReadFilter(bytes.NewReader(body))` (D-07 parse-before-persist), then atomically store the new `*Filter` into `checker.filter`, persist via temp+rename (D-08).
5. On any other status or transport error: log and retry.

**HTTP client construction** (`pkg/client/client.go` lines 27-36):
```go
func NewWhitelistClient(serverURL string, timeout time.Duration, logger *log.Logger) *WhitelistClient {
    return &WhitelistClient{
        serverURL: serverURL,
        httpClient: &http.Client{
            Timeout: timeout,
        },
        logger: logger,
    }
}
```
The bloom fetcher uses the same `http.Client{Timeout: cfg.BloomFetchTimeout}` construction — one shared client for all fetches in the fetcher struct.

**Conditional GET + ETag header pattern** (`pkg/client/client.go` lines 43-54 for shape, applied to `/bloom`):
```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/bloom", nil)
if cur := checker.filter.Load(); cur != nil {
    req.Header.Set("If-None-Match", (*cur).ETag())
}
resp, err := httpClient.Do(req)
```

**Atomic store (writer side)** (`pkg/whitelist/whitelist.go` lines 46-52 — analogous):
```go
// In whitelist: wl.list.Store(&nm)
// In bloom fetcher, after successful ReadFilter:
checker.filter.Store(newFilter)   // *bloom.Filter is already a pointer; store the *Filter directly
```
Note: `atomic.Pointer[bloom.Filter]` stores `*bloom.Filter`. `Store` takes `*bloom.Filter`.

**Temp+rename atomic disk write** (D-08 — no existing analog; stdlib pattern):
```go
tmpPath := bloomPath + ".tmp"
f, err := os.Create(tmpPath)
// ... write via filter.WriteTo(f) ...
f.Close()
os.Rename(tmpPath, bloomPath)  // atomic on same filesystem
```

**Disk load at cold start** (D-04 — uses `bloom.ReadFilter`):
```go
f, err := os.Open(bloomPath)
if err == nil {
    filter, err := bloom.ReadFilter(f)
    f.Close()
    if err == nil {
        checker.filter.Store(filter)
        close(checker.ready)  // unblock event loop immediately
    }
}
```

---

### `pkg/config/config.go` (MODIFY — add `BloomConfig` + `LoadBloomConfig`)

**Analog:** `LoadClientConfig` block in `pkg/config/config.go` (lines 66-91) — slot `BloomConfig` in as a third config type using the identical viper pattern.

**Struct with `mapstructure` tags** (lines 27-30 — `ClientConfig` shape):
```go
type ClientConfig struct {
    ServerURL    string        `mapstructure:"server_url"`
    CheckTimeout time.Duration `mapstructure:"check_timeout"`
}
```
`BloomConfig` follows the same shape:
```go
type BloomConfig struct {
    ServerURL           string        `mapstructure:"server_url"`
    BloomRefreshInterval time.Duration `mapstructure:"bloom_refresh_interval"`
    BloomPath           string        `mapstructure:"bloom_path"`
    BloomFetchTimeout   time.Duration `mapstructure:"bloom_fetch_timeout"`
    RefreshRetryCount   int           `mapstructure:"refresh_retry_count"`
}
```
`server_url` is shared with `ClientConfig` (D-02). `bloom_`-prefixed keys are additive and unambiguous (D-03).

**`LoadBloomConfig` viper block** — copy `LoadClientConfig` (lines 66-91), replace `SetDefault` calls:
```go
func LoadBloomConfig() (*BloomConfig, error) {
    v := viper.New()

    configDir, err := ensureConfigDir()
    if err != nil {
        return nil, err
    }

    v.SetConfigName("whitelist")
    v.SetConfigType("yaml")
    v.AddConfigPath(configDir)

    v.SetDefault("server_url", "http://localhost:8081")
    v.SetDefault("bloom_refresh_interval", "6h")
    v.SetDefault("bloom_path", filepath.Join(configDir, "bloom.dfbf"))  // ensureConfigDir result reused
    v.SetDefault("bloom_fetch_timeout", "30s")
    v.SetDefault("refresh_retry_count", 3)

    if err := readConfig(v, configDir, "whitelist.yaml"); err != nil {
        return nil, err
    }

    var cfg BloomConfig
    if err := v.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("unable to decode config: %w", err)
    }

    return &cfg, nil
}
```
`ensureConfigDir()` and `readConfig()` are reused without modification (lines 93-130). `bloom_path` default is computed from `configDir` so it resolves to `~/deepfry/bloom.dfbf` (D-03).

---

## Shared Patterns

### Handler / IOAdapter (unchanged — reused verbatim)
**Source:** `pkg/handler/handler.go` (all 3 interfaces), `pkg/handler/whitelist_handler.go` (all 35 lines), `pkg/handler/jsonl_io_adapter.go`
**Apply to:** `cmd/bloom/main.go`
```go
h := handler.NewWhitelistHandler(checker, logger)
ioAdapter := handler.NewJSONLIOAdapter(os.Stdout)
```
No modifications to these packages. `BloomChecker` satisfies `handler.Checker` directly.

### `atomic.Pointer` single-writer / many-reader swap
**Source:** `pkg/whitelist/whitelist.go` lines 10-11, 46-52
**Apply to:** `BloomChecker` struct, `BloomFetcher.fetch()`
```go
// struct field:
filter atomic.Pointer[bloom.Filter]  // T = bloom.Filter, stored as *bloom.Filter

// writer (fetcher goroutine only):
checker.filter.Store(newFilter)

// reader (hot path, many goroutines):
cur := checker.filter.Load()   // *bloom.Filter, nil if not yet loaded
```

### Retry loop with linear backoff + ctx-cancellation check
**Source:** `pkg/whitelist/whitelist_refresher.go` lines 70-98
**Apply to:** `BloomFetcher.fetch()`
```go
for attempt := 0; attempt <= r.retryCount; attempt++ {
    // ... attempt fetch ...
    if err != nil {
        if r.ctx.Err() != nil { return }   // cancelled
        r.logger.Printf("... attempt %d/%d ...", attempt+1, r.retryCount+1, err)
        if attempt < r.retryCount {
            select {
            case <-r.ctx.Done(): return
            case <-time.After(time.Second * time.Duration(attempt+1)):
            }
        }
        continue
    }
    // success path
    return
}
r.logger.Printf("Refresh failed after %d attempts", r.retryCount+1)
```

### Stderr logger prefix convention
**Source:** `cmd/whitelist/main.go` line 19
**Apply to:** `cmd/bloom/main.go`
```go
logger := log.New(os.Stderr, "[bloom-plugin] ", log.LstdFlags)
```

### Config: `ensureConfigDir` + `readConfig` + viper wiring
**Source:** `pkg/config/config.go` lines 93-130
**Apply to:** `LoadBloomConfig` — call both helpers unchanged; only the `SetDefault` calls differ.

---

## No Analog Found

None. All four files have strong analogs in the existing codebase. The only genuinely new pattern is the temp+rename atomic disk write (D-08), which is a stdlib `os.Create` / `os.Rename` idiom with no existing project instance — implement directly per D-08 spec.

---

## Metadata

**Analog search scope:** `cmd/whitelist/`, `pkg/handler/`, `pkg/whitelist/`, `pkg/client/`, `pkg/config/`, `pkg/bloom/`
**Files read:** 8 source files (all canonical refs from CONTEXT.md)
**Pattern extraction date:** 2026-06-30
