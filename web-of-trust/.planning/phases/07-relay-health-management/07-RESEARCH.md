# Phase 7: Relay Health Management - Research

**Researched:** 2026-06-12
**Domain:** Go relay state machine — failure classification, decay counters, filter-cap persistence, log consolidation
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**RELAY-01: Failure counting & decay**
- D-01: On successful reconnect, the relay's failure counter is halved (`failures = failures / 2`), not reset to zero. Replaces `rs.failures.Store(0)` at `pkg/crawler/crawler.go:239`. A flapping relay (+1, +1, /2, +1, …) trends upward past its threshold.
- D-02: A successful query (subscribed, drained events, returned without error) resets the counter to 0 — keep the semantics of `crawler.go:323` but only for genuine completed work.
- D-03: A failed reconnect attempt no longer removes the relay from config immediately. Instead it counts as a transport-class failure, increments the counter, and re-schedules with backoff. Ejection happens only via the per-class threshold.
- D-04: Failure counters are in-memory only. Lost on process restart.

**RELAY-02: Classification & ejection policy**
- D-05: Each `relayState` tracks one counter per failure class (transport, filter_rejection, subscription_flap). Decay rules (D-01 halve on reconnect, D-02 reset on good query) apply to all class counters.
- D-06: Per-class thresholds in nested YAML map with defaults: transport=10, filter_rejection=3, subscription_flap=5.
- D-07: Classification maps existing error paths — NOTICE-driven cap halvings and floor-reached deaths → filter_rejection; `subscriptionError` (Subscribe refused) → subscription_flap; `transportError` mid-drain / connection lost / timeout → transport.
- D-08: On ejection, relay is removed from `relay_urls` AND appended to `ejected_relays` list in web-of-trust.yaml (URL only; reason/count/timestamp go in the log line).

**RELAY-03: Filter-cap persistence & recovery**
- D-09: Learned `filterCap` values survive reconnects — delete `rs.filterCap.Store(int32(c.filterBatchSize))` at `crawler.go:240`.
- D-10: Recovery via probe-up by doubling: after 10 consecutive successful batches at cap C, next real batch uses `min(C*2, relay_filter_batch_size)`. Failed probe → halving logic puts cap back, streak resets.
- D-11: Probe-induced rejections are exempt from ejection counting. Only rejections at or below the learned cap count toward filter_rejection threshold.
- D-12: Caps are in-memory only (consistent with D-04).

**LOG-01/02/03: Log summary format**
- D-13 (LOG-01): Reconnect sweep emits summary line only when something changed. Per-relay reconnect detail only under `c.debug`.
- D-14 (LOG-02): Cap negotiation silent when stable: one line only when cap changed this batch. Individual halving steps are debug-only.
- D-15 (LOG-03): State-change lines are plain text (e.g., `Relay X dead (transport 3/10), retry in 1m`). Duplicate WARN+dead pair collapses into this single line. `RELAY_ERROR:` JSON blobs demoted to debug-only.

### Claude's Discretion
- Exact wording/format of each log line (required fields: class, count, threshold, next retry must be present).
- Where the per-relay success-streak counter for probe-up lives (field on `relayState` vs derived).
- Whether per-class counters are an array indexed by class constant or named fields; atomicity approach.
- How "probe in progress" is flagged so D-11's exemption can tell probe rejections from at-cap rejections.
- Whether `ejected_relays` handling lives in `RemoveRelayURL` or a new `EjectRelayURL` function.
- Forward-relay handling: forward relay is config-critical and should presumably be exempt from ejection.
- Test layout for the state-machine logic (unit-testable decay/classification helpers vs integration).

### Deferred Ideas (OUT OF SCOPE)
- Relay state persistence across restarts (counters and caps).
- `ejected_relays` metadata in YAML (reason/timestamp/count per entry) — URL-only list chosen.
- Structured JSON relay-event logging for metrics scraping — belongs with OBS-01.
- discover-relays skipping `ejected_relays` — lands with DISC-01.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| RELAY-01 | Relay failure counter decayed (halved) on reconnect, not reset to zero; flapping relay trends past threshold | D-01/D-02/D-03 analysis; `relayState` struct extension pattern documented |
| RELAY-02 | Failure reasons classified into transport/filter_rejection/subscription_flap; per-class thresholds configurable; ejected relay removed from config + appended to ejected_relays | D-05–D-08 analysis; existing error type mapping documented; config mutation pattern documented |
| RELAY-03 | Learned per-relay filter caps persist across reconnects; probe-up recovery doubles cap after 10 successes; probe rejections exempt from ejection counting | D-09–D-12 analysis; Phase 6 filterCap atomic.Int32 pattern documented; probe flag design documented |
| LOG-01 | Reconnect sweep emits one summary line when changed; per-relay detail under debug flag | D-13 analysis; sweep counter accumulation pattern documented |
| LOG-02 | Filter-cap negotiation logs at most one line per relay per batch stating final outcome | D-14 analysis; debug guard pattern documented |
| LOG-03 | Relay entering dead state produces exactly one log line with failure class/count/retry; duplicate WARN+dead pair collapsed; RELAY_ERROR JSON demoted to debug | D-15 analysis; existing duplicate log sites identified |
</phase_requirements>

---

## Summary

Phase 7 rewrites the relay state machine in `pkg/crawler/crawler.go`. The current state machine has a single `failures atomic.Int32` counter that resets to zero on reconnect (meaning flapping relays never accumulate past the ejection threshold), a hardcoded `maxConsecutiveFailures = 5` constant (no per-class differentiation), and ejects relays on the first failed reconnect attempt (too aggressive). Phase 6 added a `filterCap atomic.Int32` that is also reset on reconnect — meaning the learned 50→25→12→10 cascade re-runs every batch after a reconnect.

This phase replaces the single-counter `failures` field with three per-class counters (`transport`, `filter_rejection`, `subscription_flap`), each using the same `atomic.Int32` approach Phase 6 established for `filterCap`. The `relayState` struct grows a success-streak counter (for probe-up, D-10) and a probe-in-progress flag (for exemption logic, D-11). Decay on reconnect is halving (not zero-reset); full reset only on genuine completed query. The `filterCap` reset on reconnect is deleted. Ejection moves through a unified path — threshold exceeded — instead of first-failed-reconnect removal.

The logging changes touch `markRelayDead`, `ReconnectRelays`, and `queryRelay`/`handleFilterNotice`. Duplicate log pairs collapse to one structured line. Per-relay reconnect detail and per-step halving logs are demoted to `c.debug` guards. Config changes extend `Config` struct with a nested ejection-threshold map and add `ejected_relays` persistence.

**Primary recommendation:** Treat this as a struct surgery + dead-code pruning task. The existing `subscriptionError`/`transportError` types already provide the classification backbone — the new per-class counters are wired into paths that already emit these error types. No new error taxonomy; no new goroutines; no Dgraph changes.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Failure counter decay/reset | pkg/crawler (relayState) | — | In-memory relay state; lost on restart by design (D-04) |
| Failure classification | pkg/crawler (markRelayDead, FetchAndUpdateFollows error handler) | — | Maps existing subscriptionError/transportError types to class enums |
| Per-class ejection threshold | pkg/config (Config struct + Viper) | pkg/crawler (reads thresholds) | Threshold values are config; ejection logic is crawler |
| Relay ejection + YAML write | pkg/config (RemoveRelayURL / EjectRelayURL) | cmd/crawler (callback wiring) | Config mutation must go through package-global Viper singleton |
| Filter-cap persistence | pkg/crawler (delete reset on reconnect) | — | Purely in-memory; reversal of WR-03 from Phase 6 |
| Probe-up sizing | pkg/crawler (queryRelay chunk sizing path) | — | Probe is just a larger chunk on a normal batch |
| Probe-exemption from ejection | pkg/crawler (handleFilterNotice + queryRelay) | — | D-11 must gate counting at the point the rejection is classified |
| Log consolidation | pkg/crawler (markRelayDead, ReconnectRelays, queryRelay, handleFilterNotice, logRelayError) | — | All log noise comes from these five sites |
| Reconnect sweep summary | pkg/crawler (ReconnectRelays) | — | Natural accumulation point; called once per main loop iteration |

---

## Standard Stack

### Core (all already in go.mod — no new dependencies)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `sync/atomic` (stdlib) | go1.26.1 | Lock-free per-class counters on relayState | Phase 6 precedent: filterCap uses atomic.Int32; same pattern for 3 class counters + streak + probe flag |
| `github.com/spf13/viper` | v1.18.2 (in go.mod) | Nested YAML config for ejection thresholds + ejected_relays list | Already used for all config; SetDefault + mapstructure tag is established pattern |

[VERIFIED: codebase grep] No new external dependencies are required for this phase. All needed primitives — `sync/atomic`, `sync.Mutex`, `log`, `time` — are stdlib, and Viper is already loaded.

### No New Packages
This phase has no package installation step. The `## Package Legitimacy Audit` section is omitted.

---

## Architecture Patterns

### System Architecture Diagram

```
cmd/crawler/main.go
  │  OnConnectFail callback → config.EjectRelayURL(url)
  │  ReconnectRelays() called once per batch loop
  │
  └─ pkg/crawler/crawler.go
       │
       ├── relayState (per relay)
       │     failures.transport   atomic.Int32  ← halved on reconnect, reset on good query
       │     failures.filterRej   atomic.Int32  ← same decay; probe rejections EXEMPT
       │     failures.subFlap     atomic.Int32  ← same decay
       │     filterCap            atomic.Int32  ← NOT reset on reconnect (D-09 reversal)
       │     successStreak        atomic.Int32  ← probe-up: reset when cap changes
       │     probing              atomic.Bool   ← D-11 exemption flag
       │     [existing: url, conn, alive, backoff, retryAt]
       │
       ├── markRelayDead(url, class failureClass)
       │     increment class counter → check vs threshold
       │     if threshold exceeded → onConnectFail(url) → EJECTED (log class/count/threshold/retry)
       │     else → schedule backoff (log single dead line)
       │
       ├── ReconnectRelays(ctx)
       │     for each dead relay past retryAt:
       │       attempt nostr.RelayConnect
       │       SUCCESS → halve all class counters (D-01); keep filterCap (D-09)
       │       FAIL    → increment transport counter (D-03); check threshold
       │     accumulate: reconnected / removed / still-dead counts
       │     emit summary line iff something changed (D-13)
       │
       ├── queryRelay(ctx, rs, filter, eventsChan)
       │     chunk sizing: if successStreak >= 10 → probe = min(cap*2, maxBatchSize)
       │     if probe && rejection → halve cap; reset streak; DO NOT count filter_rejection
       │     if !probe && rejection → halve cap; increment filter_rejection counter
       │     NOTICE → handleFilterNotice (same probe-exemption check)
       │     on EOSE success → increment successStreak
       │
       └── FetchAndUpdateFollows goroutine fan-out
             success (no error) → reset all class counters to 0 (D-02)
             subscriptionError  → increment subFlap; markRelayDead(subscription_flap)
             transportError     → increment transport; markRelayDead(transport)

  pkg/config/config.go
       RemoveRelayURL → filter relay_urls (unchanged)
       EjectRelayURL  → filter relay_urls AND append to ejected_relays; viper.WriteConfig()
       Config struct  → add RelayEjectionThresholds map[string]int + EjectedRelays []string
```

### Recommended Project Structure (no changes to directory layout)
```
pkg/crawler/
├── crawler.go          # all relay state machine changes live here
└── crawler_filter_test.go   # existing; add new unit tests here

pkg/config/
└── config.go           # Config struct + EjectRelayURL addition
```

### Pattern 1: Per-Class Failure Counters on relayState

**What:** Replace the single `failures atomic.Int32` with three named atomic fields, one per failure class.

**When to use:** When failure class determines ejection threshold. Named fields (not an array) are preferred per project conventions (named fields over magic index).

**Example:**
```go
// Source: existing relayState pattern in pkg/crawler/crawler.go:39-47
type relayState struct {
    url     string
    conn    *nostr.Relay
    alive   bool
    backoff time.Duration
    retryAt time.Time

    // Per-class failure counters (D-05). All three decay on reconnect (D-01),
    // reset on completed query (D-02). In-memory only (D-04).
    failTransport   atomic.Int32
    failFilterRej   atomic.Int32
    failSubFlap     atomic.Int32

    filterCap    atomic.Int32 // Phase 6; NOT reset on reconnect (D-09)
    successStreak atomic.Int32 // probe-up streak (D-10); reset when cap changes
    probing       atomic.Bool  // true during a probe chunk (D-11 exemption)
}
```

**Why named fields over array:** The project conventions document names like `failTransport` over `failures[0]`. The three classes are stable and known at compile time; array indexing adds a constant definition indirection with no benefit.

**Atomicity approach:** All five fields use `atomic.Int32` / `atomic.Bool` from `sync/atomic`. This matches Phase 6 precedent (filterCap as atomic.Int32, CAS loop in handleFilterNotice). No mutex needed for these per-relay fields since writes from the NOTICE handler goroutine must be atomic, and reads from the main reconnect/query paths must see the latest value.

### Pattern 2: Decay on Reconnect (D-01)

**What:** On successful `nostr.RelayConnect`, halve all three class counters instead of resetting to zero.

**When to use:** Every successful reconnect.

**Example:**
```go
// In ReconnectRelays, replace rs.failures.Store(0) at crawler.go:239
rs.failTransport.Store(rs.failTransport.Load() / 2)
rs.failFilterRej.Store(rs.failFilterRej.Load() / 2)
rs.failSubFlap.Store(rs.failSubFlap.Load() / 2)
// filterCap: NOT touched (D-09)
```

**Why halve, not zero:** A relay that connects-then-drops immediately accumulates: +1, +1, /2=1, +1=2, +1=3, /2=1, … The counter stays elevated through cycles of flapping. Zero-reset meant a relay could flap indefinitely without ejection — the current bug.

### Pattern 3: Unified Ejection via markRelayDead

**What:** `markRelayDead` accepts a failure class, increments the appropriate counter, checks against threshold, and either schedules retry or ejects.

**When to use:** All failure paths converge here — transport errors, filter rejection floor, subscription failures, and failed reconnects (D-03).

**Example:**
```go
type failureClass int

const (
    classTransport    failureClass = iota
    classFilterRej
    classSubFlap
)

func (c *Crawler) markRelayDead(url string, class failureClass) {
    kept := c.relays[:0]
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

        // Increment the class counter and read all for threshold check
        var count int32
        switch class {
        case classTransport:
            count = rs.failTransport.Add(1)
        case classFilterRej:
            count = rs.failFilterRej.Add(1)
        case classSubFlap:
            count = rs.failSubFlap.Add(1)
        }

        threshold := int32(c.ejectionThresholds[class]) // from config
        if count >= threshold {
            log.Printf("Relay %s ejected (%s %d/%d)", url, class, count, threshold)
            if c.onConnectFail != nil {
                c.onConnectFail(url)
            }
            continue // don't re-add to kept
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

**Slice-reuse invariant:** The `kept := c.relays[:0]` trick must be preserved — CONCERNS.md § "In-memory relay state lost on restart" documents this explicitly. Do not hold references to `c.relays` across these calls.

### Pattern 4: Ejection Threshold Config (D-06)

**What:** Nested YAML map with per-class thresholds, loaded via mapstructure.

**When to use:** Config struct extension; follows clusterscan settings precedent.

**Example:**
```go
// pkg/config/config.go — add to Config struct
RelayEjectionThresholds EjectionThresholds `mapstructure:"relay_ejection_thresholds"`

type EjectionThresholds struct {
    Transport    int `mapstructure:"transport"`
    FilterRej    int `mapstructure:"filter_rejection"`
    SubFlap      int `mapstructure:"subscription_flap"`
}

// In LoadConfig, after existing SetDefaults:
viper.SetDefault("relay_ejection_thresholds", map[string]interface{}{
    "transport":         10,
    "filter_rejection":  3,
    "subscription_flap": 5,
})
```

**YAML shape (D-06):**
```yaml
relay_ejection_thresholds:
  transport: 10
  filter_rejection: 3
  subscription_flap: 5
```

**Viper nested map caveat:** Viper `SetDefault` with a `map[string]interface{}` for a nested struct works when `mapstructure` tags on the nested struct match the YAML keys exactly. This is the established pattern for `clusterscan` settings (e.g., `SeedPubkeys`, `TrustK`). Unmarshal via `viper.Unmarshal(&cfg)` handles nested structs automatically. [VERIFIED: codebase grep, config.go:26-30]

### Pattern 5: EjectRelayURL Config Function (D-08)

**What:** New function (or extension of RemoveRelayURL) that removes from `relay_urls` AND appends to `ejected_relays`.

**When to use:** Called from `onConnectFail` callback only when ejection threshold is exceeded.

**Example:**
```go
// pkg/config/config.go
func EjectRelayURL(url string) error {
    // Remove from relay_urls
    current := viper.GetStringSlice("relay_urls")
    filtered := make([]string, 0, len(current))
    for _, u := range current {
        if u != url {
            filtered = append(filtered, u)
        }
    }
    viper.Set("relay_urls", filtered)

    // Append to ejected_relays (URL only; reason goes in log)
    ejected := viper.GetStringSlice("ejected_relays")
    ejected = append(ejected, url)
    viper.Set("ejected_relays", ejected)

    return viper.WriteConfig()
}
```

**Config Viper singleton constraint:** CONCERNS.md § "Config mutation via global Viper singleton" — must call `EjectRelayURL` from a context where `LoadConfig()` has already run (i.e., the callback wired in `cmd/crawler/main.go`). Never call from tests against the live `~/deepfry/web-of-trust.yaml`; always use a temp `HOME`.

### Pattern 6: Probe-Up Sizing and Exemption (D-10/D-11)

**What:** After 10 consecutive successful batches at cap C, the next chunk uses `min(C*2, maxBatchSize)`. Set `rs.probing = true` during a probe chunk. If a rejection arrives while `rs.probing` is true, halve cap and reset streak but do NOT call `markRelayDead` with `classFilterRej`.

**When to use:** Inside `queryRelay`, at the start of each chunk iteration.

**Example:**
```go
// In queryRelay chunk loop, before Subscribe call:
batchCap := int(rs.filterCap.Load())
if rs.successStreak.Load() >= 10 {
    probe := batchCap * 2
    if probe > c.filterBatchSize {
        probe = c.filterBatchSize
    }
    if probe > batchCap {
        rs.probing.Store(true)
        batchCap = probe
    }
}

// On rejection (NOTICE or connection-drop-on-REQ):
if rs.probing.Load() {
    // Probe rejected: halve cap, reset streak, clear probe flag — no ejection count
    rs.filterCap.Store(max(old/2, 10))
    rs.successStreak.Store(0)
    rs.probing.Store(false)
    // log: "probe-up to N rejected, reverting to M" (D-14: only when cap changes)
} else {
    // At-cap rejection: count toward ejection
    // (existing halving logic + markRelayDead classFilterRej)
}

// On successful drainSubscription (EOSE returned nil):
rs.probing.Store(false) // probe succeeded
rs.successStreak.Add(1)
if batchCap > int(rs.filterCap.Load()) {
    // probe succeeded at higher cap: update filterCap
    rs.filterCap.Store(int32(batchCap))
    rs.successStreak.Store(0) // reset so next probe waits another 10 successful batches
    log.Printf("Relay %s: probe-up to %d succeeded", relayURL, batchCap) // D-14
}
```

### Pattern 7: Reconnect Sweep Summary (D-13)

**What:** Accumulate counters during `ReconnectRelays`; emit one summary log line only if something changed.

**When to use:** At the end of `ReconnectRelays`.

**Example:**
```go
// Inside ReconnectRelays, declare counters at top:
var reconnected, removed, stillDead int

// On successful reconnect: reconnected++
// On ejection during reconnect: removed++
// On still-dead (retryAt not yet reached): stillDead++
// On failed-reconnect that doesn't eject: stillDead++ (it gets re-queued)

// At end:
if reconnected > 0 || removed > 0 || stillDead > 0 {
    log.Printf("Reconnect sweep: %d reconnected, %d removed, %d still dead",
        reconnected, removed, stillDead)
}
// Target shape (from REQUIREMENTS.md LOG-01): "Reconnected 96/103 relays, 1 removed, 6 still dead"
```

### Anti-Patterns to Avoid

- **Resetting filterCap on reconnect:** Deleted at `crawler.go:240` per D-09. Do not re-add under any refactor.
- **Ejecting on first failed reconnect:** The current `ReconnectRelays:231` `if err != nil { c.onConnectFail(rs.url); continue }` is replaced by D-03: increment transport counter, check threshold, then maybe eject.
- **Duplicate dead log lines:** `markRelayDead` emits `WARN: Connection timed out` (line 439) AND `log.Printf("Relay %s marked dead...")` (line 201). Per D-15, collapse to one structured line. Do not emit the WARN at the `FetchAndUpdateFollows` error-switch site (lines 436-440) — that site hands off to `markRelayDead` which now owns the single log line.
- **Using "timed out" wording for filter-cap failures:** Filter-cap floor reached is a `transportError` today (`crawler.go:538`); LOG-03 says this must not be described as a timeout. The single dead line for this path must say "filter cap floor" or "filter_rejection", not "Connection timed out".
- **`logRelayError` JSON blobs at INFO level:** The `RELAY_ERROR:` JSON at `crawler.go:700` must be demoted to `if c.debug { ... }`. Do not remove it — the planner uses debug mode in production to diagnose issues.
- **Probing with a separate goroutine or extra Subscribe call:** D-10 is zero-cost: the probe is just a larger chunk size on the next normal batch. No extra traffic, no parallel goroutines.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Lock-free counter increment/read | Custom mutex-guarded int | `atomic.Int32` (stdlib) | Phase 6 precedent; NOTICE handler fires from a goroutine concurrent with queryRelay; mutex would add contention |
| YAML nested config struct | Custom YAML parser | `viper.SetDefault` + `mapstructure` tag | Established project pattern; clusterscan config is the template |
| Config file write | Direct file I/O | `viper.WriteConfig()` | CONCERNS.md: must go through the same Viper singleton that loaded the config |
| Error type dispatch | String matching | `errors.As` with typed errors | The `subscriptionError`/`transportError` types already exist; use `errors.As` at the classification sites |

**Key insight:** This phase's logic (decay math, threshold comparison, probe sizing) is trivially testable as pure functions on `relayState` without touching Dgraph or live relays. Extract the pure parts for unit test coverage.

---

## Common Pitfalls

### Pitfall 1: Slice-Reuse Invariant Broken During Relay Pruning

**What goes wrong:** Editing `markRelayDead` or `ReconnectRelays` pruning loops without preserving the `kept := c.relays[:0]` trick causes relay states to be double-freed or missed.

**Why it happens:** `c.relays[:0]` reuses the backing array without allocating. If any other goroutine holds a pointer into `c.relays` at the time of the slice replacement (`c.relays = kept`), it reads stale data.

**How to avoid:** Never hold references to `c.relays` elements across these calls. The only safe access pattern is within the loop (via the `rs` loop variable which is pointer-stable) or after `c.relays = kept` completes. The `dbUpdateMutex` in `FetchAndUpdateFollows` serializes Dgraph writes but does NOT protect relay slice mutations — relay mutations happen only in the main loop (single-goroutine domain).

**Warning signs:** Relay reappearing in the list after removal; nil pointer dereference on `rs.conn` after a pruning run.

### Pitfall 2: Viper Nested Map Default Not Unmarshalled

**What goes wrong:** `viper.SetDefault("relay_ejection_thresholds", map[string]interface{}{...})` sets a map but `viper.Unmarshal(&cfg)` with a nested struct may produce zero values if the `mapstructure` tags don't match the YAML key names exactly.

**Why it happens:** Viper merges the default map with any YAML-provided map; mapstructure requires the `mapstructure:"..."` tag to match the snake_case YAML key. A mismatch produces zero-value thresholds, causing all relays to be ejected immediately (threshold 0 is always exceeded).

**How to avoid:** Add a post-unmarshal guard: if `cfg.RelayEjectionThresholds.Transport == 0`, apply hardcoded fallback defaults. Write a unit test for `LoadConfig` using a temp `HOME` that verifies defaults are non-zero when the config file is absent.

**Warning signs:** All relays ejected on first failure; `EjectRelayURL` called immediately after startup.

### Pitfall 3: Probe Flag Not Cleared on Context Cancel

**What goes wrong:** `queryRelay` is cancelled mid-probe (context deadline or relay shutdown). `rs.probing` stays `true`. On the next batch, the first real chunk is misidentified as a probe and its rejection is exempt from ejection counting.

**Why it happens:** The probe flag is set before `relay.Subscribe(ctx, ...)` and cleared only on rejection or EOSE success. A context cancellation exits the chunk loop early without clearing the flag.

**How to avoid:** Clear `rs.probing.Store(false)` before returning from `queryRelay` in all paths — use `defer rs.probing.Store(false)` at the top of the chunk loop or at function entry.

**Warning signs:** Filter rejections not incrementing `failFilterRej` despite the relay being at or below its known cap.

### Pitfall 4: Failed Reconnect Counting Transport vs Ejection Race

**What goes wrong:** A failed reconnect increments `failTransport` and the count immediately exceeds the threshold, so `onConnectFail` fires during `ReconnectRelays`. But the `removed` counter for the sweep summary is only incremented if the relay is dropped in the `kept` pruning pass, not when the ejection happens inside the per-relay block.

**Why it happens:** The sweep summary counters and the relay ejection happen in the same loop but the summary logic has to correctly attribute removals regardless of whether they happen via the reconnect path or a pre-existing dead-marking path.

**How to avoid:** Set `removed++` wherever a relay is dropped from `kept` (i.e., `continue` without re-appending). Track `reconnected++` only when `nostr.RelayConnect` succeeds and `rs.alive = true`. Do not count a failed reconnect that stays dead as `reconnected`.

### Pitfall 5: onConnectFail Callback Signature Change

**What goes wrong:** D-08 changes what the `onConnectFail` callback does (move to ejected, not just remove). If the callback signature is changed to pass the failure class, `cmd/crawler/main.go` must be updated at the wiring site. Forgetting to update the wiring leaves the callback calling the old `RemoveRelayURL` instead of the new `EjectRelayURL`.

**Why it happens:** The callback is wired in `cmd/crawler/main.go:78-84` via a closure that calls `config.RemoveRelayURL(url)`. If `EjectRelayURL` is a new function in `pkg/config`, the closure in main must be updated.

**How to avoid:** Update `cmd/crawler/main.go` callback wiring as part of the same task that adds `EjectRelayURL`. The context documents this integration point explicitly.

### Pitfall 6: Forward Relay Ejection

**What goes wrong:** The forward relay has its own reconnect path at `crawler.go:247-270` that does NOT route through `markRelayDead`. If ejection logic is naively applied to every `onConnectFail` call, the forward relay could be ejected.

**Why it happens:** The forward relay is config-critical (events won't be forwarded to StrFry if it's removed). Its failure path (`forwardRelay.alive = false`, backoff scheduling) at `crawler.go:160-178` does not call `c.onConnectFail`.

**How to avoid:** Confirm the forward relay's failure paths do NOT call `onConnectFail` and do NOT call `markRelayDead`. The forward relay already has its own independent backoff. No change needed for the forward relay beyond confirming it is exempt.

---

## Code Examples

### Existing per-relay atomic pattern (Phase 6 template)
```go
// Source: pkg/crawler/crawler.go:44-47
type relayState struct {
    // ...
    failures  atomic.Int32  // ← becomes failTransport, failFilterRej, failSubFlap
    filterCap atomic.Int32  // ← kept, NOT reset on reconnect (D-09)
}
```

### Existing markRelayDead (lines 180-209) — before change
```go
func (c *Crawler) markRelayDead(url string) {
    kept := c.relays[:0]
    for _, rs := range c.relays {
        if rs.url != url { kept = append(kept, rs); continue }
        // close conn, set alive=false
        failures := int(rs.failures.Add(1))
        if failures >= maxConsecutiveFailures {
            // eject: onConnectFail + log
            continue
        }
        // schedule backoff + log
        kept = append(kept, rs)
    }
    c.relays = kept
}
```

After Phase 7: `markRelayDead(url string, class failureClass)` — accepts class, increments per-class counter, checks per-class threshold from config.

### Existing ReconnectRelays reset lines (lines 238-242) — change points
```go
rs.conn = relay
rs.alive = true
rs.backoff = initialBackoff
rs.failures.Store(0)          // D-01: replace with halving all class counters
rs.filterCap.Store(int32(c.filterBatchSize))  // D-09: DELETE this line
```

### Existing FetchAndUpdateFollows error handler (lines 433-443) — classification point
```go
case errors.As(re.err, &subErr):
    log.Printf("WARN: Subscription failed: %v", re.err)
    // Phase 7: markRelayDead(re.url, classSubFlap)
case errors.As(re.err, &transErr):
    log.Printf("WARN: Connection timed out: %v", re.err)  // Phase 7: collapse into markRelayDead log
    c.markRelayDead(re.url)
    // Phase 7: markRelayDead(re.url, classTransport)
```

### handleFilterNotice CAS loop (lines 671-688) — probe-exemption hook point
```go
func handleFilterNotice(rs *relayState, notice string, minCap int) {
    lower := strings.ToLower(notice)
    if strings.Contains(lower, "filter") && strings.Contains(lower, "too large") {
        for {
            old := rs.filterCap.Load()
            if old <= int32(minCap) {
                log.Printf(...)  // D-14: demote to debug, or emit "floor reached" once
                return
            }
            newVal := old / 2
            // ...CAS...
            if rs.filterCap.CompareAndSwap(old, newVal) {
                log.Printf(...)  // D-14: demote to debug; log "cap learned at N" only on change
                // Phase 7: check rs.probing.Load() here for D-11 exemption
                return
            }
        }
    }
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `failures atomic.Int32` single counter | Three per-class counters (transport/filterRej/subFlap) | Phase 7 | Enables per-class thresholds; transport gets slack, filter_rejection ejects fast |
| Zero-reset on reconnect | Halve on reconnect, zero only on completed query | Phase 7 | Flapping relays now trend past threshold |
| Eject on first failed reconnect | Eject only when class counter exceeds threshold | Phase 7 | Eliminates over-aggressive removal on transient network blips |
| filterCap reset to default on reconnect (WR-03) | filterCap preserved on reconnect | Phase 7 (reverts Phase 6 WR-03) | Eliminates 50→25→12→10 cascade re-running every reconnect |
| No cap recovery | Probe-up after 10 successful batches | Phase 7 | Cap can grow back if relay's limit was transient |
| ~100 per-relay log lines per sweep | One summary line when changed | Phase 7 | Log noise reduction: key production motivation |

**Deprecated in this phase:**
- `maxConsecutiveFailures = 5` constant: replaced by config-driven per-class thresholds.
- `rs.failures atomic.Int32` field: replaced by `failTransport`, `failFilterRej`, `failSubFlap`.
- `logRelayError` at INFO level: demoted to debug-only (LOG-03/D-15).
- Inline eject on failed reconnect (`crawler.go:230-234`): replaced by D-03 counter path.

---

## Project Constraints (from CLAUDE.md)

- **Never fork StrFry**: StrFry stays unmodified. Phase 7 touches only `pkg/crawler/` and `pkg/config/`. No StrFry changes.
- **Data separation**: ID-only graph in Dgraph; no event payloads. Phase 7 adds no Dgraph writes.
- **Live config**: Never edit `~/deepfry/web-of-trust.yaml` in tests. Config tests must use `t.Setenv("HOME", t.TempDir())`.
- **go-nostr error string coupling**: Classification must use `errors.As(re.err, &subErr)`/`errors.As(re.err, &transErr)` (existing typed errors), not new string matching. CONCERNS.md § "go-nostr error-string coupling".
- **Viper singleton**: `EjectRelayURL` must operate on the package-global Viper instance loaded by `LoadConfig`. No second Viper initialization. CONCERNS.md § "Config mutation via global Viper singleton".
- **Slice-reuse invariant**: `kept := c.relays[:0]` in `markRelayDead` / `ReconnectRelays` must be preserved. CONCERNS.md § "In-memory relay state lost on restart".
- **go fmt + golangci-lint**: `make fmt` and `make lint` must pass. Tabs for indentation.
- **`go test -race`**: Phase 6 CR-01 established the precedent; all new concurrent field accesses must be race-clean. Use `atomic.Int32` / `atomic.Bool` for fields written from NOTICE handler goroutines.

---

## Open Questions

1. **Forward relay exemption confirmation**
   - What we know: `forwardRelay` failure paths (`crawler.go:160-178`) do not call `c.onConnectFail` today.
   - What's unclear: If Phase 7 adds ejection logic to the reconnect path, does the forward relay reconnect at `crawler.go:247-270` inadvertently route through any shared path?
   - Recommendation: During planning, verify that `ReconnectRelays` forward relay block (lines 247-270) is kept separate and does NOT call `markRelayDead`. The forward relay failure path is self-contained; confirm no refactor accidentally introduces ejection.

2. **successStreak scope: per-chunk or per-relay-call?**
   - What we know: D-10 says "10 consecutive successful batches at cap C". A batch is one `queryRelay` call. But `queryRelay` may process multiple chunks for a single call.
   - What's unclear: Does a successful multi-chunk `queryRelay` call increment the streak by 1 (one batch = one increment), or by N chunks?
   - Recommendation: Increment `successStreak` once per successful `queryRelay` call (after all chunks drain without error), not per chunk. This aligns with "10 consecutive successful batches" meaning 10 main-loop iterations, not 10 Subscribe calls.

3. **ejected_relays YAML default**
   - What we know: `ejected_relays` will be appended to. Viper SetDefault for an empty slice is straightforward.
   - What's unclear: Does a fresh config (no YAML file) need `ejected_relays: []` in the written defaults, or is absent equivalent to empty for `viper.GetStringSlice`?
   - Recommendation: `viper.SetDefault("ejected_relays", []string{})` in `LoadConfig`; `viper.GetStringSlice("ejected_relays")` returns an empty slice when absent. No explicit YAML entry needed. Verify with a unit test.

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | All compilation | Yes | go1.26.1 | — |
| go-nostr v0.52.0 | Relay connections | Yes (in go.sum) | v0.52.0 | — |
| Viper v1.18.2 | Config load/write | Yes (in go.sum) | v1.18.2 | — |
| Dgraph gRPC | Integration tests only | Assumed (not checked) | — | Unit tests cover pure logic |

No missing dependencies for this phase.

---

## Security Domain

> `security_enforcement: true` — section required.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | — |
| V3 Session Management | No | — |
| V4 Access Control | No | — |
| V5 Input Validation | Partial | Relay URL format: existing `RemoveRelayURL`/`EjectRelayURL` takes a URL string; no new user input surfaces |
| V6 Cryptography | No | — |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| YAML injection via ejected_relays | Tampering | URLs appended via `viper.Set` (not string concatenation into YAML); Viper handles marshalling |
| Config write race (concurrent EjectRelayURL calls) | Tampering | `viper.WriteConfig()` is not goroutine-safe. Ejection happens in the single-threaded main loop via `onConnectFail`; `FetchAndUpdateFollows` goroutines do not call it. Current design is safe. Document if adding parallelism later. |
| Ejection threshold of 0 from config parse failure | Denial of Service | All relays ejected immediately. Guard: post-unmarshal validation that all thresholds >= 1 |

**Security note:** No new input surfaces are added. The `ejected_relays` list is written as a slice value via Viper (no raw string concatenation into YAML). The only new trust boundary is the ejection-threshold values from config — validate post-unmarshal that all thresholds are positive integers.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Forward relay reconnect path (crawler.go:247-270) does not route through markRelayDead and is not affected by per-class ejection logic | Open Questions | Forward relay could be ejected, breaking event forwarding to StrFry |
| A2 | successStreak increments once per queryRelay call (not per chunk) | Architecture Patterns | Probe fires too early (per-chunk) or too late (per-main-batch) depending on interpretation |
| A3 | `viper.GetStringSlice("ejected_relays")` returns empty slice (not error) when the key is absent | Code Examples | EjectRelayURL appends to nil, panics or writes malformed YAML |

---

## Sources

### Primary (HIGH confidence)
- `pkg/crawler/crawler.go` (full read) — all current relay state machine code; counter reset locations, error dispatch, log sites, markRelayDead/ReconnectRelays/queryRelay/handleFilterNotice/logRelayError
- `pkg/config/config.go` (full read) — Config struct, Viper patterns, RemoveRelayURL, global singleton constraint
- `pkg/crawler/crawler_filter_test.go` (full read) — existing test patterns for relayState unit tests
- `.planning/phases/07-relay-health-management/07-CONTEXT.md` (full read) — locked decisions D-01 through D-15
- `.planning/codebase/CONCERNS.md` (full read) — slice-reuse invariant, Viper singleton, go-nostr error coupling
- `.planning/codebase/TESTING.md` (full read) — test patterns, build-tag conventions, temp-HOME requirement
- `.planning/REQUIREMENTS.md` (full read) — RELAY-01/02/03, LOG-01/02/03 formal text
- `.planning/STATE.md` (full read) — Phase 6 CR-01 decisions, atomic.Int32 precedent
- `cmd/crawler/main.go` (full read) — callback wiring site, onConnectFail closure

### Secondary (MEDIUM confidence)
- `.planning/phases/06-filter-size-per-relay-cap-detection/06-CONTEXT.md` — Phase 6 decisions; WR-03 (filterCap reset on reconnect) that D-09 reverts

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all dependencies verified in go.mod; no new packages
- Architecture: HIGH — all decisions locked in CONTEXT.md; code structure verified by direct file reads
- Pitfalls: HIGH — derived from CONCERNS.md audit + direct code analysis of the six sites under change
- Security: HIGH — no new input surfaces; threat model is narrow

**Research date:** 2026-06-12
**Valid until:** 2026-07-12 (stable codebase; decisions locked)
