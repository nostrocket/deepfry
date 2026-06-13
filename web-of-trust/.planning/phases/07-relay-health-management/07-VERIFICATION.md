---
phase: 07-relay-health-management
verified: 2026-06-13T14:40:00Z
status: passed
score: 10/10 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 4/10
  gaps_closed:
    - "CR-01: filter-rejection cascade (markRelayDead + continue on closed conn) — closed; queryRelay never calls markRelayDead"
    - "CR-02: c.relays data race from per-relay goroutines — closed; markRelayDead is single-threaded (dispatcher only)"
    - "CR-03: startup eject bypasses thresholds — closed; New() keeps relay in pool with alive=false, increments failTransport, never calls OnConnectFail"
    - "WR-01: floor-reached path returned transportError — closed; returns filterRejectionError routed to classFilterRej"
    - "WR-03: defer rs.probing.Store(false) inside loop stacked N defers — closed; defer hoisted before loop"
    - "IN-04: at-cap rejection emitted two log lines — closed; cap-halving detail demoted to debug, markRelayDead emits the single line"
    - "WR-05: tests re-implemented logic inline, couldn't catch real-path regressions — closed; Tests A-D drive handleCapRejection and classifyRelayError directly"
  gaps_remaining: []
  regressions: []
deferred: []
human_verification: []
---

# Phase 7: Relay Health Management — Re-Verification Report (after Plan 03 gap-closure)

**Phase Goal:** Relays that repeatedly fail are automatically removed from the config without manual intervention, failure tracking and learned filter caps survive reconnects, and relay lifecycle logging is one line per state change instead of per-event spam.
**Verified:** 2026-06-13T14:40:00Z
**Status:** PASSED
**Re-verification:** Yes — after gap-closure plan 07-03 (see previous VERIFICATION at 2026-06-12T13:33:08Z, which found CR-01/CR-02/CR-03/WR-01/WR-03/IN-04/WR-05)

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Per-class ejection thresholds (transport/filter_rejection/subscription_flap) readable from config with defaults 10/3/5 | VERIFIED | EjectionThresholds struct at config.go:18-22; SetDefault map at lines 96-100; positivity guard at lines 132-140; 5 config tests PASS |
| 2 | A config parse with zero/negative threshold is corrected to its default (never ejects immediately) | VERIFIED | Post-unmarshal guard at config.go:132-140; TestLoadConfig_EjectionThresholdGuard PASS |
| 3 | Ejecting a relay removes it from relay_urls and appends it to ejected_relays in YAML | VERIFIED | EjectRelayURL at config.go:216-233; TestEjectRelayURL_MovesToEjected and TestEjectRelayURL_AppendsNotReplaces PASS |
| 4 | D-01: Per-class failure counters halve on reconnect (not reset to 0) | VERIFIED | ReconnectRelays lines 345-347 halve all three counters; TestDecayCounters_HalveOnReconnect PASS. CR-03 closed: startup failures no longer bypass this path via immediate ejection |
| 5 | When a class counter exceeds its threshold the relay is removed via EjectRelayURL with one log line (class/count/threshold) | VERIFIED | CR-01 closed: queryRelay returns *filterRejectionError and never continues on a closed conn; dispatcher calls markRelayDead once per cycle; failFilterRej rises by 1 per rejection. TestDispatch_FilterRejectionRoutesToFilterRejClass: failFilterRej 2→3 at threshold=3 fires ejection exactly once. markRelayDead log line at crawler.go:275/283: "Relay %s ejected (%s %d/%d)" |
| 6 | D-05/D-07: Three-class classification via errors.As; per-class counters; per-class configurable thresholds | VERIFIED | CR-02 closed: markRelayDead called only from single-threaded dispatcher (crawler.go:563) and ReconnectRelays (main-loop); never from per-relay goroutines. classifyRelayError (crawler.go:848) routes filterRejectionError→classFilterRej, subscriptionError→classSubFlap, transportError→classTransport. `go test -race ./pkg/crawler/` exits 0 |
| 7 | D-01/D-03/T-07-DOS: A startup RelayConnect failure keeps the relay in the pool with alive=false; transient boot/DNS outage cannot permanently eject all relays | VERIFIED | CR-03 closed: New() startup block (crawler.go:143-157) sets alive=false, conn=nil, failTransport.Add(1), retryAt, never calls cfg.OnConnectFail. The two OnConnectFail references in New() are a comment (line 145) and a struct assignment (line 180) — neither invokes the callback |
| 8 | A probe rejection reverts cap and resets streak without counting toward ejection (D-11) | VERIFIED | WR-03 closed: defer rs.probing.Store(false) hoisted before the for loop (crawler.go:625, count=1 in queryRelay). handleCapRejection (crawler.go:816-837): isProbing=true branch returns nil (no error, no ejection), logs "probe-up rejected, reverting". TestProbeRejection_ExemptFromEjection PASS |
| 9 | Learned filterCap survives reconnect; 50→25→12→10 cascade not re-run per batch | VERIFIED | filterCap reset-on-reconnect absent (grep 'rs.filterCap.Store(int32(c.filterBatchSize))' in ReconnectRelays: no match). WR-01 closed: handleCapRejection floor-reached returns *filterRejectionError (not *transportError); TestDispatch_FloorReachedIsFilterRejNotTransport PASS |
| 10 | One dead-state log line per state change; no duplicate WARN+dead; no 'timed out' wording for filter-cap failures | VERIFIED | IN-04 closed: cap-halving detail demoted to `if c.debug` (crawler.go:830-831), markRelayDead emits the single production line (lines 275/283). `grep -i 'timed out' crawler.go` → no match. logRelayError is debug-only (crawler.go:902). LOG-01: sweep summary at line 359, conditional on changed relays. LOG-02: handleFilterNotice CAS-success log path present |

**Score:** 10/10 truths verified

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/config/config.go` | EjectionThresholds struct, Config fields, EjectRelayURL, defaults, guard | VERIFIED | All symbols present and correct; 5 config tests PASS |
| `pkg/config/config_test.go` | 5 unit tests using temp HOME | VERIFIED | All 5 tests PASS; no references to live config path |
| `pkg/crawler/crawler.go` | filterRejectionError type; queryRelay returns typed errors; classifyRelayError; handleCapRejection; isUnclassified; New() keeps pool relay with alive=false; defer hoisted before loop | VERIFIED | filterRejectionError at line 39; handleCapRejection at line 816; classifyRelayError at line 848; isUnclassified at line 862; 11 filterRejectionError references; all per-plan corrections confirmed |
| `pkg/crawler/crawler_filter_test.go` | 4 new real-seam tests (Tests A-D) driving handleCapRejection and classifyRelayError | VERIFIED | TestDispatch_FilterRejectionRoutesToFilterRejClass, TestDispatch_FloorReachedIsFilterRejNotTransport, TestMarkRelayDead_ConcurrentDispatchRaceClean, TestQueryRelay_AtCapRejectionReturnsFilterRejErrorNoEject — all PASS; 12 filterRejectionError references in test file |
| `cmd/crawler/main.go` | OnConnectFail→EjectRelayURL; EjectionThresholds from cfg | VERIFIED | Lines 80-85 confirm wiring; OnConnectFail closure calls config.EjectRelayURL |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `queryRelay` filter-rejection path | FetchAndUpdateFollows errorsChan dispatcher | `return &filterRejectionError{}` → errorsChan → `classifyRelayError` → `markRelayDead(classFilterRej)` | VERIFIED | queryRelay has 0 markRelayDead calls; dispatcher at line 559-563 calls classifyRelayError then markRelayDead |
| FetchAndUpdateFollows dispatcher | `markRelayDead` | Single-threaded: only call site from query path is crawler.go:563 | VERIFIED | `grep -n 'c.markRelayDead' crawler.go` → line 563 only (one call site outside the definition/comments) |
| `New()` startup connect failure | relayState pool (alive=false) | `rs.failTransport.Add(1)` + `relays = append(relays, rs)` (no OnConnectFail) | VERIFIED | crawler.go:143-157; `awk New...` \| grep `rs.failTransport.Add` → 1; `awk New...` \| grep `cfg.OnConnectFail` → comment + struct assignment only |
| `cmd/crawler/main.go OnConnectFail` | `config.EjectRelayURL` | closure at lines 80-85 | VERIFIED | Correct and unchanged from 07-02 |
| `crawler.go New()` | `config.EjectionThresholds` | ejectionThresholds map at lines 182-186 | VERIFIED | Thresholds flow from config into Crawler |

### Data-Flow Trace (Level 4)

Not applicable — this phase delivers a behavior/state machine, not data-rendering components.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build succeeds across all packages | `go build ./...` | exit 0, no output | PASS |
| All unit tests pass race-clean | `go test -race -count=1 ./pkg/crawler/ ./pkg/config/` | 23+5 tests PASS | PASS |
| Full test suite race-clean | `go test -race -count=1 ./...` | pkg/config, pkg/crawler, pkg/dgraph all PASS | PASS |
| go vet clean | `go vet ./...` | exit 0, no output | PASS |
| No markRelayDead inside queryRelay (CR-01/CR-02) | `awk '/^func .* queryRelay/{f=1;next} /^func /{f=0} f' crawler.go \| grep -c markRelayDead` | 0 | PASS |
| filterRejectionError type present (WR-01) | `grep -c 'filterRejectionError' crawler.go` | 11 | PASS |
| defer rs.probing.Store(false) hoisted (WR-03, count=1 in queryRelay) | `awk '/^func .* queryRelay/{f=1;next} /^func /{f=0} f' crawler.go \| grep -c 'defer rs.probing.Store(false)'` | 1 | PASS |
| No append(chunk, authors) requeue-continue (CR-01) | `awk '/^func .* queryRelay/{f=1;next} /^func /{f=0} f' crawler.go \| grep -c 'append(chunk, authors'` | 0 | PASS |
| New() keeps failed relay in pool with failTransport++ (CR-03) | `awk '/^func New\(cfg Config\)/{f=1;next} /^func /{f=0} f' crawler.go \| grep -c 'rs.failTransport.Add'` | 1 | PASS |
| New() never calls OnConnectFail as a function (CR-03) | comment + struct-assignment only in New() | both inert (no invocation) | PASS |
| Slice invariant preserved (2x kept := c.relays[:0]) | `grep -c 'kept := c.relays\[:0\]' crawler.go` | 2 | PASS |
| No 'timed out' wording on filter path (LOG-03/D-15) | `grep -i 'timed out' crawler.go` | no match | PASS |
| Test A drives real classifyRelayError (WR-05) | TestDispatch_FilterRejectionRoutesToFilterRejClass | PASS | PASS |
| Test B drives real handleCapRejection floor path (WR-01) | TestDispatch_FloorReachedIsFilterRejNotTransport | PASS | PASS |
| Test C documents single-threaded markRelayDead contract (CR-02) | TestMarkRelayDead_ConcurrentDispatchRaceClean | PASS (race-clean) | PASS |
| Test D drives handleCapRejection at-cap, failFilterRej stays 0 (CR-01) | TestQueryRelay_AtCapRejectionReturnsFilterRejErrorNoEject | PASS | PASS |
| filterRejectionError references in test file (>= 2, WR-05) | `grep -c 'filterRejectionError' crawler_filter_test.go` | 12 | PASS |

### Probe Execution

No probes defined for this phase.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| RELAY-01 | 07-02 | Failure counter halved on reconnect, not reset | VERIFIED | ReconnectRelays lines 345-347 halve all three counters; CR-03 closed so startup failures accrue to failTransport via New() and are governed by ReconnectRelays threshold — they no longer bypass the halving path via premature ejection |
| RELAY-02 | 07-01, 07-02, 07-03 | Three-class classification; per-class thresholds; auto-remove from config | VERIFIED | Config layer VERIFIED (07-01). State machine: CR-01 closed (no cascade), CR-02 closed (single-threaded markRelayDead), WR-01 closed (filterRejectionError routes to classFilterRej not classTransport). TestDispatch tests lock in the classification contract |
| RELAY-03 | 07-02, 07-03 | Learned filterCap persists across reconnect; probe-up recovery | VERIFIED | filterCap reset-on-reconnect absent. WR-03 closed (defer hoisted). WR-01 closed (floor-reached classified correctly). TestProbeRejection_ExemptFromEjection and TestProbeUp_* PASS |
| LOG-01 | 07-02 | Single sweep summary line; per-relay detail debug-only | VERIFIED | crawler.go:357-361: single summary line conditional on changed relays; per-relay detail behind c.debug |
| LOG-02 | 07-02 | One cap-change line per relay per batch; halving steps debug-only | VERIFIED | handleCapRejection: production halving detail at line 830-831 is inside `if c.debug`; probe-up logged once at line 827 |
| LOG-03 | 07-02, 07-03 | Single dead-state line; no WARN+dead duplicate; no 'timed out' for filter-cap failures | VERIFIED | IN-04 closed: cap-halving detail demoted to debug; markRelayDead is the single log owner for dead-state lines; no "timed out" in file; logRelayError is debug-only |

### Anti-Patterns Found

No blockers. All six previously-found blockers (CR-01, CR-02, CR-03, WR-01, WR-03, IN-04) have been closed and regression-tested. No new anti-patterns introduced.

### Human Verification Required

None — all correctness properties are statically verifiable from the code and confirmed by the race-clean unit test suite.

The only out-of-band step is manual validation on the live strfry host (per spec §6): running the crawler against live Dgraph + relays and confirming a single real filter rejection bumps the counter by 1 (not instant ejection), a transient startup outage keeps relays in config, and one-line-per-state-change logging holds. This is an operational gate, not a code correctness issue, and was acknowledged as out-of-band in the original plan.

### Gaps Summary

No gaps. All ten observable truths verified.

**Previous blockers closed by plan 07-03:**

- **CR-01** (cascade): queryRelay now returns *filterRejectionError and exits; it never calls markRelayDead or continues on a closed connection. A single real rejection increments failFilterRej by 1; the threshold of 3 governs ejection over multiple cycles.
- **CR-02** (data race): markRelayDead is called only from the single-threaded FetchAndUpdateFollows dispatcher (line 563) and ReconnectRelays (main loop). Per-relay goroutines never touch c.relays. go test -race clean.
- **CR-03** (startup ejection): New() keeps every failed-startup relay in the pool with alive=false, increments failTransport, sets retryAt. OnConnectFail is never invoked from New(). A transient DNS/boot outage cannot produce an empty relay_urls in YAML.
- **WR-01** (mis-classification): handleCapRejection returns *filterRejectionError for both at-cap and floor-reached paths. classifyRelayError routes it to classFilterRej (threshold 3). TestDispatch_FloorReachedIsFilterRejNotTransport asserts errors.As(&transportError) is false.
- **WR-03** (stacked defers): defer rs.probing.Store(false) is registered exactly once before the for loop in queryRelay (count 1 confirmed by awk grep).
- **IN-04** (two log lines): cap-halving detail is inside `if c.debug`; markRelayDead emits the single production dead-state line.
- **WR-05** (inline test re-implementation): Tests A-D call handleCapRejection and classifyRelayError directly — real production code, not copies. 12 filterRejectionError references in the test file.

---

_Verified: 2026-06-13T14:40:00Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification after plan 07-03 gap-closure (previous verification: 2026-06-12T13:33:08Z, status: gaps_found, score: 4/10)_
