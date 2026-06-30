# Phase 2: Server Bloom Endpoint - Pattern Map

**Mapped:** 2026-06-30
**Files analyzed:** 5 (modified) + 1 (new handler)
**Analogs found:** 5 / 5

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/server/server.go` | server + handler | request-response | `pkg/server/server.go` (self) | exact — extend in-place |
| `pkg/whitelist/whitelist_refresher.go` | service | event-driven (periodic) | `pkg/whitelist/whitelist_refresher.go` (self) | exact — extend in-place |
| `pkg/whitelist/whitelist.go` | model | CRUD (atomic swap) | `pkg/whitelist/whitelist.go` (self) | exact — read-only ref, DO NOT MODIFY |
| `cmd/server/main.go` | entrypoint / wiring | request-response | `cmd/server/main.go` (self) | exact — extend in-place |
| `pkg/config/config.go` | config | N/A | `pkg/config/config.go` (self) | exact — extend in-place |

---

## Pattern Assignments

### `pkg/server/server.go` — new fields + `SetStats`, `SwapFilter`, `handleBloom`

**Analog:** `pkg/server/server.go` (self, current state)

**Existing struct fields to mirror** (lines 16–24):
```go
type WhitelistServer struct {
	whitelist   *whitelist.Whitelist
	addr        string
	logger      *log.Logger
	debug       bool
	ready       atomic.Bool
	entries     atomic.Int64
	lastRefresh atomic.Pointer[time.Time]
}
```
Add two new fields following the same `atomic.*` style (D-03, D-05):
```go
// bloomSnapshot holds the current serialized filter and its ETag, swapped
// atomically on each successful refresh. nil until the first rebuild.
bloomSnapshot atomic.Pointer[bloomEntry]
```
where `bloomEntry` is a small unexported struct (discretion: single atomic pointer covers D-03 + D-05):
```go
type bloomEntry struct {
	etag  string // Filter.ETag() — pre-computed quoted hex string
	bytes []byte // Filter.MarshalBinary() output — cached once per generation
}
```

**`SetReady` pattern — mirror for `SetStats`** (lines 36–41):
```go
// SetReady marks the server as ready to serve traffic (whitelist loaded).
func (s *WhitelistServer) SetReady(entries int) {
	s.entries.Store(int64(entries))
	now := time.Now()
	s.lastRefresh.Store(&now)
	s.ready.Store(true)
}
```
`SetStats(n int, t time.Time)` follows the identical shape but does NOT flip `ready` (readiness is set once at startup by `SetReady`; subsequent per-refresh updates just keep the values live per D-10):
```go
func (s *WhitelistServer) SetStats(n int, t time.Time) {
	s.entries.Store(int64(n))
	s.lastRefresh.Store(&t)
}
```

**`SwapFilter` — atomic pointer store** (mirrors `SetReady` Store pattern, lines 37–40):
```go
func (s *WhitelistServer) SwapFilter(f *bloom.Filter) error {
	b, err := f.MarshalBinary()
	if err != nil {
		return err
	}
	s.bloomSnapshot.Store(&bloomEntry{etag: f.ETag(), bytes: b})
	return nil
}
```

**Mux registration — `Handler()`** (lines 44–52, add one line):
```go
func (s *WhitelistServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /check/{pubkey}", s.handleCheck)
	mux.HandleFunc("POST /check", s.handleBulkCheck)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /bloom", s.handleBloom) // add here
	return mux
}
```

**`/health` 503-while-loading — template for `/bloom` not-ready** (lines 166–178):
```go
func (s *WhitelistServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "loading",
			"detail": "populating whitelist from dgraph",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
```
`handleBloom` uses the same guard — replace `!s.ready.Load()` with `snap == nil` (nil pointer from `bloomSnapshot.Load()`) and use `"detail": "bloom filter not yet built"`. Then for the happy path (D-06, D-07):
```go
func (s *WhitelistServer) handleBloom(w http.ResponseWriter, r *http.Request) {
	snap := s.bloomSnapshot.Load()
	if snap == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "loading",
			"detail": "bloom filter not yet built",
		})
		return
	}
	// Conditional GET (D-07)
	if r.Header.Get("If-None-Match") == snap.etag {
		w.Header().Set("ETag", snap.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", snap.etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(snap.bytes)))
	w.Write(snap.bytes)
}
```
Note: `strconv` is already imported (line 9 of current server.go).

---

### `pkg/whitelist/whitelist_refresher.go` — add `onRefresh` callback

**Analog:** `pkg/whitelist/whitelist_refresher.go` (self)

**Existing struct** (lines 11–20) — add one field:
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
	onRefresh  func(keys [][32]byte) // D-01: registered before Start(), called after UpdateKeys
}
```

**Setter (discretion: `SetOnRefresh`):**
```go
// SetOnRefresh registers a callback that fires after every successful whitelist
// refresh (after UpdateKeys). Must be called before Start(). Not concurrency-safe
// with Start() — wiring happens in main before the goroutine launches.
func (r *WhitelistRefresher) SetOnRefresh(fn func(keys [][32]byte)) {
	r.onRefresh = fn
}
```

**`refresh()` callback site** (lines 62–87) — fire after `UpdateKeys` on line 82, before the `return`:
```go
r.whitelist.UpdateKeys(keys)
r.logger.Printf("whitelist refreshed with %d keys", len(keys))
if r.onRefresh != nil {
	r.onRefresh(keys)
}
return
```
The `onRefresh` call is inside the success branch only (D-02: failed refresh → no callback, prior filter preserved).

---

### `pkg/whitelist/whitelist.go` — READ-ONLY REFERENCE (D-03)

**DO NOT MODIFY.** The `atomic.Pointer[map[[32]byte]struct{}]` on lines 10–11 must remain exactly as-is. The `/check` hot path depends on it.

Key seam for callback: `UpdateKeys` (lines 46–52) accepts `[][32]byte` — the same type the callback receives from `refresh()`. `Len()` (lines 38–44) returns the count the callback passes to `SetStats`.

```go
// These signatures are the contract — do not change them:
func (wl *Whitelist) UpdateKeys(keys [][32]byte)
func (wl *Whitelist) Len() int
```

---

### `cmd/server/main.go` — register callback before `refresher.Start()`

**Analog:** `cmd/server/main.go` (self)

**Current wiring** (lines 34–51):
```go
refresher := whitelist.NewWhitelistRefresher(ctx, keyRepo, cfg.RefreshInterval, cfg.RefreshRetryCount, logger)

// Start HTTP server immediately so /health can respond during loading
srv := server.NewWhitelistServer(refresher.Whitelist(), cfg.ServerListenAddr, cfg.Debug, logger)

go func() {
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Fatalf("Server error: %v", err)
	}
}()

// Block until initial whitelist is loaded
logger.Printf("Loading whitelist from %s ...", cfg.DgraphGraphQLURL)
refresher.Start()
defer refresher.Stop()

srv.SetReady(refresher.Whitelist().Len())
```

Insert `SetOnRefresh` between `refresher` construction and `refresher.Start()`:
```go
refresher.SetOnRefresh(func(keys [][32]byte) {
	// Rebuild bloom filter from the refreshed key set (D-01, D-09)
	b := bloom.NewBuilder(uint(len(keys)), cfg.BloomFPRate)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		logger.Printf("bloom build failed: %v", err)
		return
	}
	if err := srv.SwapFilter(f); err != nil {
		logger.Printf("bloom serialize failed: %v", err)
		return
	}
	srv.SetStats(len(keys), time.Now())
})
```

`SetReady` call on line 50 stays (marks readiness for `/health`). The initial `refresher.Start()` synchronously calls `refresh()` which fires the callback, so `/bloom` becomes ready in lockstep (code_context §Integration Points).

Add `"whitelist-plugin/pkg/bloom"` and `"time"` to imports (time is already imported transitively — verify; bloom is new).

---

### `pkg/config/config.go` — add `bloom_fp_rate` (SRV-04)

**Analog:** `pkg/config/config.go` (self)

**Existing `ServerConfig` struct** (lines 14–23) — add one field with matching `mapstructure` tag style:
```go
type ServerConfig struct {
	DgraphGraphQLURL  string        `mapstructure:"dgraph_graphql_url"`
	RefreshInterval   time.Duration `mapstructure:"refresh_interval"`
	RefreshRetryCount int           `mapstructure:"refresh_retry_count"`
	IdleConnTimeout   time.Duration `mapstructure:"idle_conn_timeout"`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout"`
	QueryTimeout      time.Duration `mapstructure:"query_timeout"`
	ServerListenAddr  string        `mapstructure:"server_listen_addr"`
	Debug             bool          `mapstructure:"debug"`
	BloomFPRate       float64       `mapstructure:"bloom_fp_rate"` // add this
}
```

**Existing viper `SetDefault` block** (lines 43–50) — add one line following the same pattern:
```go
v.SetDefault("bloom_fp_rate", 0.000001) // 1e-6 per D-09
```
Insert after the existing `v.SetDefault("debug", true)` line.

---

## Shared Patterns

### Atomic Pointer Store (single-writer / many-reader)
**Source:** `pkg/server/server.go` lines 23, 37–40 (`lastRefresh atomic.Pointer[time.Time]` + `s.lastRefresh.Store(&now)`)
**Apply to:** `bloomSnapshot atomic.Pointer[bloomEntry]` in `WhitelistServer` and `SwapFilter`
The pattern: allocate a new value, take its address, call `.Store(&val)`. Readers call `.Load()` and check for nil before dereferencing. No locks needed.

### 503-while-loading JSON response
**Source:** `pkg/server/server.go` lines 166–175
**Apply to:** `handleBloom` not-ready guard (D-08)
Pattern: `w.Header().Set("Content-Type", "application/json")` → `w.WriteHeader(http.StatusServiceUnavailable)` → `json.NewEncoder(w).Encode(map[string]string{...})` → `return`.

### Viper SetDefault + mapstructure tag
**Source:** `pkg/config/config.go` lines 43–50 (SetDefault block) and lines 14–23 (struct tags)
**Apply to:** `bloom_fp_rate` field + default in `LoadServerConfig`
Pattern: snake_case key string in both the `mapstructure:""` tag and the `v.SetDefault("key", value)` call — they must match exactly.

### Callback-at-success-only
**Source:** `pkg/whitelist/whitelist_refresher.go` lines 62–87 — `refresh()` returns on any error; `UpdateKeys` is only reached after all retries succeed (line 82).
**Apply to:** `onRefresh` call site — place after `UpdateKeys` inside the success branch, before the `return` on line 84. Never call on the error paths.

---

## No Analog Found

None. All files have direct self-analogs (extend-in-place). The `bloomEntry` struct and `handleBloom` handler are new within `pkg/server/server.go`, but they directly copy from `SetReady`/`handleHealth` patterns already present.

---

## Metadata

**Analog search scope:** `pkg/server/`, `pkg/whitelist/`, `pkg/bloom/`, `pkg/config/`, `cmd/server/`
**Files scanned:** 6
**Pattern extraction date:** 2026-06-30
