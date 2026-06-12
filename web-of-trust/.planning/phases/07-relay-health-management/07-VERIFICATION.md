---
phase: 07-relay-health-management
verified: 2026-06-12T13:33:08Z
status: gaps_found
score: 4/10 must-haves verified
overrides_applied: 0
gaps:
  - truth: "When a class counter exceeds its threshold the relay is removed from config (via EjectRelayURL) and one log line records which relay and why (class, count, threshold)"
    status: failed
    reason: "CR-01: After filter-rejection, markRelayDead closes rs.conn then the loop continues using the same closed connection — re-entering the rejection path and cascading failFilterRej from 0 to threshold=3 in milliseconds, permanently ejecting the relay on a single real rejection. The threshold design is defeated in practice."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Lines 654-664: after calling markRelayDead(relayURL, classFilterRej), code re-queues with 'authors = append(chunk, authors...)' then 'continue', reusing the closed relay variable for the next Subscribe call"
    missing:
      - "Return from queryRelay after calling markRelayDead — the relay is dead and must not be reused; remaining authors are picked up next cycle via staleness"

  - truth: "D-05, D-07: Failures are classified into transport, filter_rejection, and subscription_flap by mapping the existing typed error paths; each relayState tracks one counter per class and each class has its own configurable ejection threshold"
    status: failed
    reason: "CR-02: markRelayDead mutates c.relays slice in-place (kept := c.relays[:0]) with no mutex, while being called concurrently from per-relay goroutines (queryRelay runs in a goroutine per relay at lines 407-425). Two relays hitting filter-rejection simultaneously produce concurrent write/write access to c.relays — a data race that bypasses the per-class threshold accounting silently."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Line 226 markRelayDead: no sync.Mutex; called from goroutines at line 660 (queryRelay) and from the single-threaded errorsChan handler at lines 539-546. Concurrent calls from multiple relay goroutines race on c.relays without synchronization."
    missing:
      - "Either guard c.relays with a mutex in markRelayDead/ReconnectRelays/the launch loop, or (preferred) never call markRelayDead from relay goroutines — have queryRelay return a typed filterRejectionError and let the single-threaded errorsChan dispatcher call markRelayDead"

  - truth: "D-01: A flapping relay's per-class failure counters are halved on reconnect (failures = failures / 2, not reset to 0), so repeated flapping eventually pushes a class count past its configured threshold"
    status: failed
    reason: "CR-03: In New(), a single failed RelayConnect immediately calls cfg.OnConnectFail(url) which is wired to config.EjectRelayURL — permanently ejecting the relay with zero counter accrual, no threshold check. This contradicts D-03 and the plan's threat model (T-07-DOS). During transient startup outage (boot ordering, DNS not yet up), New() ejects ALL configured relays, persists empty relay_urls to YAML, returns error, and every subsequent start fails at LoadConfig 'at least one relay URL is required'. A transient failure becomes a permanent, self-inflicted denial of service."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Lines 131-136: 'if err != nil { cfg.OnConnectFail(url); continue }' — single failure fires ejection callback with no counter increment and no threshold comparison"
      - path: "cmd/crawler/main.go"
        issue: "Lines 80-85: OnConnectFail closure calls config.EjectRelayURL unconditionally"
    missing:
      - "Keep relayState in pool with alive=false on startup connect failure; increment failTransport; set retryAt; let ReconnectRelays/threshold logic govern ejection"
      - "Do not call cfg.OnConnectFail from New() — startup connect failure is not a threshold-based ejection event"

  - truth: "After 10 consecutive successful batches at cap C the next batch probes min(C*2, relay_filter_batch_size); a probe rejection reverts the cap and resets the streak without counting toward ejection"
    status: failed
    reason: "WR-03 (BLOCKER-level): defer rs.probing.Store(false) is registered inside the for len(authors) > 0 loop at line 623 — one deferred call accumulates per chunk iteration. The probe flag is NOT cleared per-iteration as readers expect; it stays set across chunks and is cleared in bulk at function return. This means the probe exemption (D-11, which reads rs.probing.Load()) can misfire when called from handleFilterNotice between iterations. Also, isProbing is reset to false at line 663 but rs.probing.Store(false) is deferred, leaving rs.probing stale-true for the remainder of the call. CR-01 also applies here: the probe-rejection branch (lines 654-657) sets isProbing=false then hits 'continue', reusing the closed connection identically to the non-probe path."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Line 623: defer rs.probing.Store(false) inside for loop — stacks N defers per queryRelay call. Lines 654-657: probe-rejection branch also continues the loop with a closed connection."
    missing:
      - "Move defer rs.probing.Store(false) to before the for loop (register once)"
      - "Return from queryRelay after probe rejection instead of continuing the loop"

  - truth: "A learned filterCap survives reconnect — the 50 → 25 → 12 → 10 cascade does not re-run on the next batch"
    status: failed
    reason: "WR-01: When filterCap is already at floor (10), queryRelay logs 'marking dead (filter_rejection)' but returns &transportError (line 668). The errorsChan dispatcher maps transportError to classTransport (line 541), so the failure counts against failTransport (threshold 10) instead of failFilterRej (threshold 3). Per-class accounting is wrong and the relay takes ~10 cycles to eject instead of 3 — also the dead-state log line from markRelayDead says '(transport N/10)' contradicting the immediately-preceding line. The filterCap floor semantics are undermined."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Line 668: returns &transportError on filter-cap floor reached; dispatcher at line 541 maps it to classTransport. Should use a dedicated filterRejectionError type routed to classFilterRej."
    missing:
      - "Introduce a filterRejectionError type (or annotated subscriptionError) and dispatch to classFilterRej in FetchAndUpdateFollows errorsChan handler"

  - truth: "A reconnect sweep emits at most one summary line, only when something changed; per-relay reconnect detail is debug-only"
    status: failed
    reason: "CR-02 applies to ReconnectRelays as well: its kept := c.relays[:0] compaction (line 274) is called from the main loop (single-threaded), but markRelayDead from goroutines can race the same c.relays slice. If a goroutine calls markRelayDead concurrently while ReconnectRelays is rebuilding the slice, both perform unsynchronized compaction on the shared backing array."
    artifacts:
      - path: "pkg/crawler/crawler.go"
        issue: "Line 274: ReconnectRelays uses kept := c.relays[:0] with no mutex; concurrent markRelayDead calls from relay goroutines can race this path"
    missing:
      - "Shared with CR-02 fix: add mutex protection to c.relays across markRelayDead and ReconnectRelays"

deferred: []

human_verification: []
---

# Phase 7: Relay Health Management Verification Report

**Phase Goal:** Relays that repeatedly fail are automatically removed from the config without manual intervention, failure tracking and learned filter caps survive reconnects, and relay lifecycle logging is one line per state change instead of per-event spam.
**Verified:** 2026-06-12T13:33:08Z
**Status:** GAPS_FOUND
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Per-class ejection thresholds (transport/filter_rejection/subscription_flap) readable from config with defaults 10/3/5 | VERIFIED | EjectionThresholds struct at config.go:18-22; SetDefault map at lines 96-100; positivity guard at lines 132-140; 5 config tests all PASS |
| 2 | A config parse with zero/negative threshold is corrected to its default (never ejects immediately) | VERIFIED | Post-unmarshal guard at config.go:132-140; TestLoadConfig_EjectionThresholdGuard PASS |
| 3 | Ejecting a relay removes it from relay_urls and appends it to ejected_relays in YAML | VERIFIED | EjectRelayURL at config.go:216-233; TestEjectRelayURL_MovesToEjected and TestEjectRelayURL_AppendsNotReplaces PASS |
| 4 | D-01: Per-class failure counters halve on reconnect (not reset to 0) | VERIFIED (struct only) | ReconnectRelays lines 323-326 halve all three counters; TestDecayCounters_HalveOnReconnect PASS. However CR-03 means startup failures bypass this path entirely — the relay is ejected before reaching ReconnectRelays |
| 5 | When a counter exceeds its threshold the relay is removed via EjectRelayURL with one log line (class/count/threshold) | FAILED | CR-01: filter-rejection cascade drives counter 0→threshold in milliseconds via the closed-connection continue-loop at lines 654-664. Single real rejection becomes permanent ejection. |
| 6 | D-05/D-07: Three-class classification via errors.As; per-class counters; per-class configurable thresholds | FAILED | CR-02: markRelayDead mutates c.relays without a mutex, called concurrently from per-relay goroutines. Data race bypasses accounting. WR-01: floor-reached path returns transportError, mis-classified as classTransport. |
| 7 | D-01: Flapping relay counters halve on reconnect so repeated flapping trends past threshold | FAILED | CR-03: startup connect failure fires EjectRelayURL with zero counter accrual and no threshold check — relays can never accumulate failure counts if they fail at startup |
| 8 | A probe rejection reverts cap and resets streak without counting toward ejection | FAILED | WR-03: defer rs.probing.Store(false) inside loop (stacks N defers); CR-01 applies to probe-rejection branch too — continues with closed connection |
| 9 | Learned filterCap survives reconnect; 50→25→12→10 cascade not re-run per batch | FAILED | filterCap reset-on-reconnect DELETED (RELAY-03 PASS). But WR-01: floor-reached path mis-classified as transport not filter_rejection, disrupting the floor semantics |
| 10 | One dead-state log line (class/count/retry); no duplicate WARN+dead; no 'timed out' wording for filter-cap failures | VERIFIED (partially) | No 'timed out' wording confirmed (grep clean). markRelayDead is single log owner (lines 221-225). BUT IN-04: at-cap rejection logs TWO lines (cap-halving line + markRelayDead dead-state line); CR-01 means cascade emits multiple lines anyway |

**Score:** 4/10 truths verified (truths 1, 2, 3 fully verified; truth 4 partially verified but circumvented by CR-03)

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/config/config.go` | EjectionThresholds struct, Config fields, EjectRelayURL, defaults, guard | VERIFIED | All symbols present and correct at documented line numbers |
| `pkg/config/config_test.go` | 5 unit tests using temp HOME | VERIFIED | All 5 tests PASS; 0 references to live config path |
| `pkg/crawler/crawler.go` | failureClass type, per-class relayState fields, rewritten markRelayDead/ReconnectRelays/queryRelay/handleFilterNotice/logRelayError | STUB (CR-01/CR-02/CR-03) | Artifacts exist and compile, but the primary ejection paths contain correctness-breaking bugs that defeat the stated goal |
| `pkg/crawler/crawler_filter_test.go` | 8 new unit tests for decay/probe/classification | VERIFIED (tests pass, but WR-05 applies) | All 14 crawler tests PASS; however probe tests re-implement logic inline and cannot catch regressions in queryRelay |
| `cmd/crawler/main.go` | OnConnectFail → EjectRelayURL; EjectionThresholds from cfg | VERIFIED (wiring present) | Lines 79-85 confirm wiring; HOWEVER CR-03 means the wrong code path calls it |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `crawler.go markRelayDead` | `cmd/crawler/main.go OnConnectFail → config.EjectRelayURL` | `c.onConnectFail` fired when count >= threshold | PARTIAL | Wired but CR-01 fires threshold breach via cascade not genuine repeated failure |
| `crawler.go New() startup` | `config.EjectRelayURL` | direct OnConnectFail call on first connect failure | BLOCKER | CR-03: single startup failure ejects without threshold; correct path would be keep-in-pool with alive=false |
| `crawler.go FetchAndUpdateFollows errorsChan → markRelayDead` | `errors.As on subscriptionError/transportError` | classification dispatch | PARTIAL | WR-01: floor-reached path returns wrong error type, misroutes to classTransport |
| `crawler.go New()` | `config.EjectionThresholds` | ejectionThresholds map populated at lines 161-165 | VERIFIED | Thresholds flow correctly from config into crawler |

### Data-Flow Trace (Level 4)

Not applicable — this phase delivers behavior (state machine), not data-rendering components.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build succeeds across all packages | `go build ./...` | exit 0, no output | PASS |
| All unit tests pass race-clean | `go test -race -count=1 ./pkg/crawler/ ./pkg/config/` | all 19 tests PASS | PASS |
| go vet clean | `go vet ./...` | exit 0, no output | PASS |
| maxConsecutiveFailures deleted | `grep maxConsecutiveFailures pkg/crawler/crawler.go` | no match | PASS |
| filterCap reset-on-reconnect deleted (RELAY-03) | `grep 'rs.filterCap.Store(int32(c.filterBatchSize))' pkg/crawler/crawler.go` | no match | PASS |
| No 'timed out' wording on filter-rejection path | `grep -i 'timed out' pkg/crawler/crawler.go` | no match | PASS |
| Sweep summary line present | `grep 'Reconnected %d/%d relays' pkg/crawler/crawler.go` | line 338 | PASS |
| logRelayError is debug-only | lines 837-839 show `if !c.debug { return }` | present | PASS |
| CR-01 cascade present | lines 654-664: markRelayDead then continue with closed conn | present | FAIL |
| CR-02 missing mutex | markRelayDead line 226: no mutex; called from goroutines at line 660 | present | FAIL |
| CR-03 startup eject | New() lines 131-136: single failure fires OnConnectFail | present | FAIL |
| WR-03 defer in loop | line 623: defer inside `for len(authors) > 0` | present | FAIL |

### Probe Execution

No probes defined for this phase.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| RELAY-01 | 07-02 | Failure counter halved on reconnect, not reset | FAILED | Halving in ReconnectRelays is implemented; CR-03 means startup connect failures bypass halving and eject immediately; also CR-01 means counters race to threshold via cascade, not via genuine repeated reconnect failures |
| RELAY-02 | 07-01, 07-02 | Three-class classification; per-class thresholds; auto-remove from config | FAILED | Config contract (Plan 01) is VERIFIED. State machine (Plan 02) has CR-01 (cascade), CR-02 (data race), and WR-01 (mis-classification at floor) that collectively defeat the threshold design in its primary active paths |
| RELAY-03 | 07-02 | Learned filterCap persists across reconnect; probe-up recovery | PARTIAL | filterCap reset-on-reconnect deleted (D-09 PASS). Probe-up structure present. WR-03 (defer in loop) and CR-01 apply to probe-rejection branch — probe exemption correctness is compromised |
| LOG-01 | 07-02 | Single sweep summary line; per-relay detail debug-only | VERIFIED | Lines 336-339; conditional on `> 0`; per-relay detail behind `c.debug` |
| LOG-02 | 07-02 | One cap-change line per relay per batch; halving steps debug-only | VERIFIED | handleFilterNotice lines 826-829; single CAS-success log; no per-step halving in production |
| LOG-03 | 07-02 | Single dead-state line; no WARN+dead duplicate; no 'timed out' for filter-cap failures | PARTIAL | markRelayDead is the single log owner (comments at lines 223-225); no 'timed out' wording confirmed. IN-04: at-cap rejection emits two lines (the cap-halving line at 659 AND the markRelayDead line). CR-01 cascade emits multiple lines per relay per batch. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/crawler/crawler.go` | 623 | `defer` inside `for` loop — stacks one deferred call per chunk iteration | BLOCKER | probe flag not cleared per-iteration; rs.probing.Load() reads stale-true between iterations (WR-03) |
| `pkg/crawler/crawler.go` | 660-664 | `markRelayDead` called then loop `continue`-d with closed conn | BLOCKER | CR-01 cascade: single rejection drives counter to threshold in milliseconds |
| `pkg/crawler/crawler.go` | 226-269 | `c.relays` mutated in-place with no mutex, from concurrent goroutines | BLOCKER | CR-02 data race |
| `pkg/crawler/crawler.go` | 131-136 | `cfg.OnConnectFail(url)` on first startup connect failure, no counter/threshold | BLOCKER | CR-03: transient boot failure permanently ejects all relays |
| `pkg/crawler/crawler.go` | 668 | Returns `&transportError` for filter-cap floor (mis-classification) | WARNING | WR-01: floor-reached counts as transport not filter_rejection |
| `pkg/crawler/crawler.go` | 659-660 | Two log lines before/after markRelayDead at-cap rejection | WARNING | IN-04: violates LOG-03 single-line invariant |

### Human Verification Required

None — all failures are statically verifiable from the code.

### Gaps Summary

The phase delivers all its config-layer artifacts correctly (RELAY-02 config contract: EjectionThresholds struct, EjectRelayURL, Viper defaults, positivity guard). The crawler state machine compiles, all 14 unit tests pass race-clean, and structural requirements (deleted constants, correct wiring, LOG-01/02 logging consolidation) are met.

**However, three Critical bugs in the primary runtime paths defeat the phase goal:**

**CR-01 (filter-rejection cascade):** After classifying a Subscribe error as a filter rejection and halving the cap, `markRelayDead` closes the connection, then the loop continues using the same dead `relay` variable. The next Subscribe fails instantly with "not connected", triggering the same rejection path again. With the default `filter_rejection` threshold of 3, a relay that drops one real over-sized REQ is permanently ejected within milliseconds — not after 3 genuine failures over time. The threshold design exists in config but is bypassed by the cascade. Fix: return from queryRelay after calling markRelayDead.

**CR-02 (unsynchronized c.relays mutation):** `markRelayDead` performs in-place slice compaction on `c.relays` with no mutex, and is called from per-relay goroutines concurrently. Two simultaneous filter-rejection events race on `c.relays`, producing lost updates or inconsistent state. The plan specified `go test -race must stay clean` but the data race lives in a production code path the unit tests never exercise (WR-05 confirms the tests re-implement logic inline, not via queryRelay). Fix: guard `c.relays` with a mutex, or never call markRelayDead from goroutines.

**CR-03 (startup eject bypasses thresholds):** In `New()`, a single failed `RelayConnect` immediately fires `cfg.OnConnectFail(url)` → `config.EjectRelayURL`. Zero counter accrual, no threshold check. During any transient startup condition (DNS not up, relay momentarily down), every configured relay is permanently ejected from YAML, and subsequent starts fail at `LoadConfig` with "at least one relay URL is required". The phase intended `New()` to populate a pool of potentially-dead relays and let `ReconnectRelays`+thresholds govern ejection.

These three bugs are in the core runtime paths that implement RELAY-01, RELAY-02, and RELAY-03. The phase goal — "relays that repeatedly fail are automatically removed" — requires the threshold mechanism to function correctly; with CR-01 and CR-03 in place, relays are ejected on first failure rather than after crossing a threshold. With CR-02, the ejection state machine has an observable data race.

---

_Verified: 2026-06-12T13:33:08Z_
_Verifier: Claude (gsd-verifier)_
