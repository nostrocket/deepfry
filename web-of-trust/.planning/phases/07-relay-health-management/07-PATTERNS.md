# Phase 7: Relay Health Management - Pattern Map

**Mapped:** 2026-06-12
**Files analyzed:** 3 (2 modified, 1 test extended)
**Analogs found:** 3 / 3

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `pkg/crawler/crawler.go` | service (state machine) | event-driven + request-response | self (surgical edit) | exact |
| `pkg/config/config.go` | config | CRUD | self (surgical edit) | exact |
| `pkg/crawler/crawler_filter_test.go` | test | — | self (extend) | exact |
| `cmd/crawler/main.go` | entry point (wiring) | — | self (one-line callback change) | exact |

---

## Pattern Assignments

### `pkg/crawler/crawler.go` — relayState struct surgery

**Primary changes:** `relayState` struct, `markRelayDead`, `ReconnectRelays`, `queryRelay`, `handleFilterNotice`, `FetchAndUpdateFollows` error dispatch, `logRelayError`.

---

#### Current `relayState` struct (lines 39–47) — replace this

```go
type relayState struct {
	url       string
	conn      *nostr.Relay
	alive     bool
	backoff   time.Duration
	retryAt   time.Time
	failures  atomic.Int32
	filterCap atomic.Int32
}
```

**New struct — copy this pattern:**

```go
// failureClass identifies which per-relay counter to increment.
type failureClass int

const (
	classTransport    failureClass = iota // transport errors, connection drops
	classFilterRej                        // filter rejections at or below learned cap
	classSubFlap                          // Subscribe refused (not filter-size related)
)

func (fc failureClass) String() string {
	switch fc {
	case classTransport:
		return "transport"
	case classFilterRej:
		return "filter_rejection"
	case classSubFlap:
		return "subscription_flap"
	default:
		return "unknown"
	}
}

type relayState struct {
	url     string
	conn    *nostr.Relay
	alive   bool
	backoff time.Duration
	retryAt time.Time

	// Per-class failure counters (D-05). Halved on reconnect (D-01),
	// reset to 0 on completed query (D-02). In-memory only (D-04).
	failTransport atomic.Int32
	failFilterRej atomic.Int32
	failSubFlap   atomic.Int32

	// Phase 6: filterCap NOT reset on reconnect (D-09).
	filterCap atomic.Int32

	// Probe-up state (D-10/D-11).
	successStreak atomic.Int32 // incremented once per successful queryRelay call
	probing       atomic.Bool  // true while a probe chunk is in flight; exempt from filter_rejection counting
}
```

**Atomicity model** — copy from Phase 6 CAS pattern (lines 683–688):

```go
// atomic.Int32 Add — for increment:
count = rs.failTransport.Add(1)

// atomic.Int32 Store — for halve on reconnect:
rs.failTransport.Store(rs.failTransport.Load() / 2)

// atomic.Bool Store — for probe flag:
rs.probing.Store(true)
defer rs.probing.Store(false) // clear in all exit paths

// CompareAndSwap — for concurrent NOTICE handler (existing pattern, lines 683-688):
if rs.filterCap.CompareAndSwap(old, newVal) { ... }
```

---

#### `Crawler` struct — add ejection threshold fields (lines 49–58)

The `Crawler` struct needs to carry the per-class thresholds loaded from config. Follow the existing field pattern:

```go
type Crawler struct {
	relays          []*relayState
	forwardRelay    *relayState
	dgClient        *dgraph.Client
	timeout         time.Duration
	debug           bool
	dbUpdateMutex   sync.Mutex
	onConnectFail   func(url string)
	filterBatchSize int
	// Phase 7: per-class ejection thresholds from config (D-06)
	ejectionThresholds map[failureClass]int32
}
```

Wire thresholds in `New()` after config unmarshal, e.g.:

```go
c.ejectionThresholds = map[failureClass]int32{
	classTransport: int32(cfg.EjectionThresholds.Transport),
	classFilterRej: int32(cfg.EjectionThresholds.FilterRej),
	classSubFlap:   int32(cfg.EjectionThresholds.SubFlap),
}
```

---

#### `markRelayDead` (lines 180–209) — replace entirely

**Current signature:** `func (c *Crawler) markRelayDead(url string)`
**New signature:** `func (c *Crawler) markRelayDead(url string, class failureClass)`

**Pattern to copy (slice-reuse invariant from lines 181–208):**

```go
func (c *Crawler) markRelayDead(url string, class failureClass) {
	kept := c.relays[:0]  // MUST preserve this slice-reuse trick
	for _, rs := range c.relays {
		if rs.url != url {
			kept = append(kept, rs)
			continue
		}
		if rs.conn != nil {
			rs.conn.Close()
		}
		rs.conn = nil
		rs.alive = false

		var count int32
		switch class {
		case classTransport:
			count = rs.failTransport.Add(1)
		case classFilterRej:
			count = rs.failFilterRej.Add(1)
		case classSubFlap:
			count = rs.failSubFlap.Add(1)
		}

		threshold := c.ejectionThresholds[class]
		if threshold <= 0 {
			threshold = 10 // safety: never eject immediately on misconfigured threshold
		}
		if count >= threshold {
			log.Printf("Relay %s ejected (%s %d/%d)", url, class, count, threshold)
			if c.onConnectFail != nil {
				c.onConnectFail(url)
			}
			continue // do NOT re-add to kept
		}

		rs.retryAt = time.Now().Add(rs.backoff)
		log.Printf("Relay %s dead (%s %d/%d), retry in %v", url, class, count, threshold, rs.backoff)
		rs.backoff *= 2
		if rs.backoff > maxBackoff {
			rs.backoff = maxBackoff
		}
		kept = append(kept, rs)
	}
	c.relays = kept
}
```

---

#### `ReconnectRelays` (lines 211–244) — three targeted changes

**Change 1 — replace lines 239–240 (reset → halve; delete filterCap reset):**

```go
// OLD (lines 239-240):
rs.failures.Store(0)
rs.filterCap.Store(int32(c.filterBatchSize))

// NEW (D-01 halve; D-09 delete filterCap reset):
rs.failTransport.Store(rs.failTransport.Load() / 2)
rs.failFilterRej.Store(rs.failFilterRej.Load() / 2)
rs.failSubFlap.Store(rs.failSubFlap.Load() / 2)
// filterCap line deleted
```

**Change 2 — replace lines 229–234 (eject-on-first-fail → D-03 counter path):**

```go
// OLD (lines 229-234):
if err != nil {
    log.Printf("WARN: Reconnect to %s failed, removing from config: %v", rs.url, err)
    if c.onConnectFail != nil {
        c.onConnectFail(rs.url)
    }
    continue
}

// NEW (D-03: failed reconnect counts as transport failure, threshold governs ejection):
if err != nil {
    if c.debug {
        log.Printf("Reconnect to %s failed: %v", rs.url, err)
    }
    // Increment transport counter; markRelayDead handles ejection check + log.
    // Re-schedule backoff for next retry.
    rs.failTransport.Add(1)
    threshold := c.ejectionThresholds[classTransport]
    if threshold <= 0 {
        threshold = 10
    }
    if rs.failTransport.Load() >= threshold {
        log.Printf("Relay %s ejected (%s %d/%d) after repeated reconnect failures",
            rs.url, classTransport, rs.failTransport.Load(), threshold)
        if c.onConnectFail != nil {
            c.onConnectFail(rs.url)
        }
        removed++
        continue
    }
    rs.retryAt = time.Now().Add(rs.backoff)
    rs.backoff *= 2
    if rs.backoff > maxBackoff {
        rs.backoff = maxBackoff
    }
    stillDead++
    kept = append(kept, rs)
    continue
}
reconnected++
```

**Change 3 — add sweep summary counters (D-13):**

```go
// Declare at top of ReconnectRelays, before the loop:
var reconnected, removed, stillDead int

// At end of ReconnectRelays, after c.relays = kept:
if reconnected > 0 || removed > 0 || stillDead > 0 {
    total := len(c.relays) + removed
    log.Printf("Reconnected %d/%d relays, %d removed, %d still dead",
        reconnected, total, removed, stillDead)
}
```

**Forward relay block (lines 246–270) is UNCHANGED.** It does not route through `markRelayDead` and must not be touched by this refactor.

---

#### `FetchAndUpdateFollows` error dispatch (lines 433–443) — classification

**Current (lines 433–443):**

```go
var subErr *subscriptionError
var transErr *transportError
switch {
case errors.As(re.err, &subErr):
    log.Printf("WARN: Subscription failed: %v", re.err)
case errors.As(re.err, &transErr):
    log.Printf("WARN: Connection timed out: %v", re.err)
    c.markRelayDead(re.url)
default:
    log.Printf("WARN: Relay error: %v", re.err)
}
```

**New — copy `errors.As` dispatch, add class, collapse log into markRelayDead:**

```go
var subErr *subscriptionError
var transErr *transportError
switch {
case errors.As(re.err, &subErr):
    c.markRelayDead(re.url, classSubFlap)
case errors.As(re.err, &transErr):
    c.markRelayDead(re.url, classTransport)
default:
    if c.debug {
        log.Printf("Relay %s: unclassified error: %v", re.url, re.err)
    }
    c.markRelayDead(re.url, classTransport) // treat unknown as transport
}
```

**Also on line 323 — per-class reset on good query (D-02):**

```go
// OLD:
rs.failures.Store(0)

// NEW:
rs.failTransport.Store(0)
rs.failFilterRej.Store(0)
rs.failSubFlap.Store(0)
rs.successStreak.Add(1) // increment streak for probe-up
```

---

#### `queryRelay` (lines 490–559) — probe-up and probe-exemption

**Insert probe-up sizing at line 501 (after `batchCap := int(rs.filterCap.Load())`):**

```go
batchCap := int(rs.filterCap.Load())
if batchCap <= 0 {
    batchCap = 10
}

// Probe-up (D-10): after 10 successful batches, try doubling the cap.
isProbing := false
if rs.successStreak.Load() >= 10 {
    probe := batchCap * 2
    if probe > c.filterBatchSize {
        probe = c.filterBatchSize
    }
    if probe > batchCap {
        isProbing = true
        batchCap = probe
        rs.probing.Store(true)
    }
}
// Ensure probing flag is always cleared on return.
defer rs.probing.Store(false)
```

**Connection-drop-on-REQ path (lines 523–538) — add probe-exemption (D-11):**

```go
// OLD (line 532-533):
rs.filterCap.Store(newVal)
log.Printf("Relay %s: filter rejection drop, halved cap to %d; retrying chunk", relayURL, newVal)

// NEW — check probe flag before counting toward ejection:
old := rs.filterCap.Load()
if old > 10 {
    newVal := old / 2
    if newVal < 10 {
        newVal = 10
    }
    rs.filterCap.Store(newVal)
    rs.successStreak.Store(0)
    rs.probing.Store(false)
    if isProbing {
        // Probe rejection: cap reverted, streak reset, no ejection count (D-11)
        log.Printf("Relay %s: probe-up to %d rejected, reverting to %d", relayURL, batchCap, newVal)
    } else {
        // At-cap rejection: count toward filter_rejection ejection
        log.Printf("Relay %s: filter rejection at cap %d, halved to %d", relayURL, old, newVal)
        c.markRelayDead(relayURL, classFilterRej)
    }
    authors = append(chunk, authors...)
    continue
}
// Floor reached (D-07: filter_rejection, not transport wording)
log.Printf("Relay %s: filter cap at floor, marking dead (filter_rejection)", relayURL)
return &transportError{err: fmt.Errorf("relay %s: filter cap floor reached", relayURL)}
```

**After successful `drainSubscription` (line 552), increment streak:**

```go
if err := c.drainSubscription(ctx, sub, relayURL, eventsChan); err != nil {
    sub.Unsub()
    return err
}
sub.Unsub()
// Probe succeeded: update cap and log (D-10, D-14)
if isProbing && batchCap > int(rs.filterCap.Load()) {
    rs.filterCap.Store(int32(batchCap))
    rs.successStreak.Store(0)
    log.Printf("Relay %s: probe-up to %d succeeded, new cap", relayURL, batchCap)
    isProbing = false
}
```

Note: `successStreak` per-queryRelay-call increment happens at line 323 in `FetchAndUpdateFollows` (the goroutine success path), not per-chunk here.

---

#### `handleFilterNotice` (lines 667–689) — probe-exemption hook (D-11)

The function signature must change to accept the `relayState` pointer (already does). Add probe-exemption after the CAS succeeds:

```go
// After the CAS at line 683:
if rs.filterCap.CompareAndSwap(old, newVal) {
    rs.successStreak.Store(0)
    rs.probing.Store(false)
    if c.debug { // D-14: per-step halving is debug-only
        log.Printf("Relay %s NOTICE filter-too-large: halved cap to %d", rs.url, newVal)
    } else {
        log.Printf("Relay %s: cap learned at %d (NOTICE)", rs.url, newVal)
    }
    // D-11: probe rejections do not increment filter_rejection counter
    // The probing flag is read at the call site (queryRelay) which handles ejection.
    return
}
```

Note: `handleFilterNotice` is a package-level function (no `*Crawler` receiver). To check `rs.probing` for D-11 exemption, the function can return a bool indicating whether it halved, and the caller (`queryRelay`) decides whether to call `markRelayDead`. Alternatively pass `rs` and let the function check `rs.probing.Load()` — but since `markRelayDead` needs `c`, the cleanest approach is: `handleFilterNotice` halves the cap and returns, then the caller checks `rs.probing.Load()` to decide whether to call `markRelayDead(classFilterRej)`.

---

#### `logRelayError` (lines 691–701) — demote to debug-only (D-15)

```go
func (c *Crawler) logRelayError(errorType string, err error) {
    if !c.debug {
        return // D-15: RELAY_ERROR JSON blobs are debug-only
    }
    metrics := map[string]interface{}{
        "error_type":  errorType,
        "error":       err.Error(),
        "occurred_at": time.Now().Format(time.RFC3339),
        "component":   "web-of-trust-crawler",
    }
    metricsJSON, _ := json.Marshal(metrics)
    log.Printf("RELAY_ERROR: %s", string(metricsJSON))
}
```

---

#### Constants block (lines 33–37) — remove `maxConsecutiveFailures`

```go
const (
    initialBackoff = 30 * time.Second
    maxBackoff     = 5 * time.Minute
    // maxConsecutiveFailures = 5  ← DELETE; replaced by per-class config thresholds
)
```

---

### `pkg/config/config.go` — two targeted changes

#### Change 1: Add `EjectionThresholds` struct + fields to `Config` (after line 29)

**Pattern to copy:** Nested struct with `mapstructure` tags, following clusterscan settings (lines 25–30):

```go
// RelayEjectionThresholds holds per-failure-class ejection thresholds (D-06).
type EjectionThresholds struct {
    Transport int `mapstructure:"transport"`
    FilterRej int `mapstructure:"filter_rejection"`
    SubFlap   int `mapstructure:"subscription_flap"`
}

// Add to Config struct after MinClusterSize:
RelayEjectionThresholds EjectionThresholds `mapstructure:"relay_ejection_thresholds"`
EjectedRelays           []string           `mapstructure:"ejected_relays"`
```

**Add `SetDefault` calls in `LoadConfig` after the existing clusterscan defaults (line 79):**

```go
viper.SetDefault("relay_ejection_thresholds", map[string]interface{}{
    "transport":         10,
    "filter_rejection":  3,
    "subscription_flap": 5,
})
viper.SetDefault("ejected_relays", []string{})
```

**Post-unmarshal guard (after `viper.Unmarshal`, before relay URL check):**

```go
// Guard: zero thresholds would eject all relays immediately.
if cfg.RelayEjectionThresholds.Transport <= 0 {
    cfg.RelayEjectionThresholds.Transport = 10
}
if cfg.RelayEjectionThresholds.FilterRej <= 0 {
    cfg.RelayEjectionThresholds.FilterRej = 3
}
if cfg.RelayEjectionThresholds.SubFlap <= 0 {
    cfg.RelayEjectionThresholds.SubFlap = 5
}
```

---

#### Change 2: Add `EjectRelayURL` function (after `RemoveRelayURL`, line 168)

**Pattern to copy:** `RemoveRelayURL` (lines 155–168) — same Viper singleton access + `viper.WriteConfig()`:

```go
// EjectRelayURL removes a relay URL from relay_urls and appends it to
// ejected_relays in the config file (D-08). The reason/count/timestamp
// are logged by the caller; this function persists the URL-only list.
func EjectRelayURL(url string) error {
    // Remove from relay_urls (same as RemoveRelayURL)
    current := viper.GetStringSlice("relay_urls")
    filtered := make([]string, 0, len(current))
    for _, u := range current {
        if u != url {
            filtered = append(filtered, u)
        }
    }
    viper.Set("relay_urls", filtered)

    // Append to ejected_relays (URL only)
    ejected := viper.GetStringSlice("ejected_relays")
    ejected = append(ejected, url)
    viper.Set("ejected_relays", ejected)

    return viper.WriteConfig()
}
```

---

### `cmd/crawler/main.go` — callback wiring (lines 78–84)

Replace `config.RemoveRelayURL` with `config.EjectRelayURL` in the `OnConnectFail` closure:

```go
// OLD (lines 78-84):
OnConnectFail: func(url string) {
    if err := config.RemoveRelayURL(url); err != nil {
        log.Printf("Warning: could not remove relay %s from config: %v", url, err)
    } else {
        log.Printf("Removed relay %s from config", url)
    }
},

// NEW:
OnConnectFail: func(url string) {
    if err := config.EjectRelayURL(url); err != nil {
        log.Printf("Warning: could not eject relay %s from config: %v", url, err)
    }
    // markRelayDead already logged the ejection line with class/count/threshold.
},
```

Also pass `EjectionThresholds` from config into `crawler.Config`:

```go
crawlerCfg := crawler.Config{
    // ... existing fields ...
    EjectionThresholds: cfg.RelayEjectionThresholds, // Phase 7
}
```

And add the field to `crawler.Config` struct (lines 60–68):

```go
type Config struct {
    // ... existing fields ...
    EjectionThresholds config.EjectionThresholds // or copy the struct inline
}
```

---

### `pkg/crawler/crawler_filter_test.go` — new unit tests

**Pattern to copy:** Existing test structure (lines 1–98) — direct `relayState` construction, no Dgraph, no relay connection, `package crawler` (white-box):

```go
// New tests follow the same form as TestHandleFilterNotice_Halves:
func TestDecayCounters_HalveOnReconnect(t *testing.T) {
    rs := &relayState{url: "wss://example.com"}
    rs.failTransport.Store(8)
    rs.failFilterRej.Store(4)
    rs.failSubFlap.Store(6)
    // simulate halve (the logic extracted from ReconnectRelays):
    rs.failTransport.Store(rs.failTransport.Load() / 2)
    rs.failFilterRej.Store(rs.failFilterRej.Load() / 2)
    rs.failSubFlap.Store(rs.failSubFlap.Load() / 2)
    if rs.failTransport.Load() != 4 { t.Fatalf("want 4, got %d", rs.failTransport.Load()) }
    if rs.failFilterRej.Load() != 2 { t.Fatalf("want 2, got %d", rs.failFilterRej.Load()) }
    if rs.failSubFlap.Load() != 3 { t.Fatalf("want 3, got %d", rs.failSubFlap.Load()) }
}

func TestProbeUp_StreakThreshold(t *testing.T) {
    rs := &relayState{url: "wss://example.com"}
    rs.filterCap.Store(50)
    rs.successStreak.Store(10)
    // batchCap sizing logic (copy from queryRelay):
    batchCap := int(rs.filterCap.Load())
    isProbing := false
    if rs.successStreak.Load() >= 10 {
        probe := batchCap * 2
        if probe > 100 { probe = 100 } // maxBatchSize
        if probe > batchCap { isProbing = true; batchCap = probe }
    }
    if !isProbing { t.Fatal("expected probing=true at streak 10") }
    if batchCap != 100 { t.Fatalf("want batchCap 100, got %d", batchCap) }
}

func TestProbeUp_NoProbeBeforeStreak(t *testing.T) {
    rs := &relayState{}
    rs.filterCap.Store(50)
    rs.successStreak.Store(9)
    batchCap := int(rs.filterCap.Load())
    isProbing := false
    if rs.successStreak.Load() >= 10 {
        isProbing = true
    }
    if isProbing { t.Fatal("should not probe at streak 9") }
    if batchCap != 50 { t.Fatalf("batchCap should be 50, got %d", batchCap) }
}
```

Config loading test (in a new file `pkg/config/config_test.go` or inline):

```go
func TestLoadConfig_EjectionThresholdDefaults(t *testing.T) {
    t.Setenv("HOME", t.TempDir()) // never touch ~/deepfry/
    cfg, err := config.LoadConfig()
    if err != nil { t.Fatal(err) }
    if cfg.RelayEjectionThresholds.Transport != 10 {
        t.Fatalf("transport threshold default: want 10, got %d", cfg.RelayEjectionThresholds.Transport)
    }
    if cfg.RelayEjectionThresholds.FilterRej != 3 {
        t.Fatalf("filter_rejection threshold default: want 3, got %d", cfg.RelayEjectionThresholds.FilterRej)
    }
    if cfg.RelayEjectionThresholds.SubFlap != 5 {
        t.Fatalf("subscription_flap threshold default: want 5, got %d", cfg.RelayEjectionThresholds.SubFlap)
    }
}
```

---

## Shared Patterns

### Atomic field access
**Source:** `pkg/crawler/crawler.go` lines 683–688 (handleFilterNotice CAS loop) and line 45 (`filterCap atomic.Int32`)
**Apply to:** All new `relayState` fields (`failTransport`, `failFilterRej`, `failSubFlap`, `successStreak`, `probing`)

```go
// Load-then-store (halve on reconnect):
rs.failTransport.Store(rs.failTransport.Load() / 2)

// Add-and-read (increment):
count := rs.failTransport.Add(1)

// CAS (concurrent NOTICE handler — existing pattern):
if rs.filterCap.CompareAndSwap(old, newVal) { ... }

// Bool set/clear:
rs.probing.Store(true)
defer rs.probing.Store(false)
```

### Debug guard
**Source:** `pkg/crawler/crawler.go` lines 106–108, 219–222, 458–461
**Apply to:** All per-relay reconnect detail logs (D-13), per-step halving logs (D-14)

```go
if c.debug {
    log.Printf("...")
}
```

### Slice-reuse pruning
**Source:** `pkg/crawler/crawler.go` lines 181, 208, 212, 244
**Apply to:** Any edit to `markRelayDead` or `ReconnectRelays` that touches `c.relays`

```go
kept := c.relays[:0] // reuse backing array; do not hold external references
for _, rs := range c.relays {
    // ...
    kept = append(kept, rs)
}
c.relays = kept
```

### Viper singleton config mutation
**Source:** `pkg/config/config.go` lines 155–168 (`RemoveRelayURL`)
**Apply to:** `EjectRelayURL`

```go
viper.Set("key", value)        // mutate
return viper.WriteConfig()     // persist through the same instance that LoadConfig used
```

### errors.As dispatch
**Source:** `pkg/crawler/crawler.go` lines 433–443
**Apply to:** Any new error classification site

```go
var subErr *subscriptionError
var transErr *transportError
switch {
case errors.As(re.err, &subErr):
    // subscription_flap class
case errors.As(re.err, &transErr):
    // transport class
}
```

---

## No Analog Found

All files have direct analogs (they are surgical edits to existing files). No file requires pattern sourcing from RESEARCH.md alone.

---

## Key Constraints (copy into every plan)

1. **Slice-reuse invariant:** `kept := c.relays[:0]` in `markRelayDead`/`ReconnectRelays` must be preserved verbatim.
2. **Forward relay exempt:** Lines 246–270 (`ReconnectRelays` forward relay block) must not be routed through `markRelayDead`. Confirm no refactor touches it.
3. **Viper singleton:** `EjectRelayURL` operates on the same `viper` instance that `LoadConfig` used; never initialize a second Viper.
4. **Temp HOME in tests:** `t.Setenv("HOME", t.TempDir())` for any test that calls `config.LoadConfig` or `config.EjectRelayURL`.
5. **`go test -race`:** All new `atomic.Int32`/`atomic.Bool` fields must be accessed only via atomic methods; no bare reads/writes.
6. **`maxConsecutiveFailures` constant deleted:** Any reference to it after this phase is a bug.
7. **filterCap reset on reconnect deleted:** Line `rs.filterCap.Store(int32(c.filterBatchSize))` at crawler.go:240 must not survive the refactor.

---

## Metadata

**Analog search scope:** `pkg/crawler/`, `pkg/config/`, `cmd/crawler/`
**Files scanned:** 4
**Pattern extraction date:** 2026-06-12
