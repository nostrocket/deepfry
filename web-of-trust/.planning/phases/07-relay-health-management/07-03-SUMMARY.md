---
phase: 07-relay-health-management
plan: "03"
subsystem: crawler
tags: [relay-state-machine, gap-closure, filterRejectionError, data-race, ejection, tdd]
gap_closure: true

dependency_graph:
  requires:
    - phase: 07-01
      provides: [EjectionThresholds, EjectRelayURL]
    - phase: 07-02
      provides: [failureClass, relayState counters, markRelayDead, ReconnectRelays, queryRelay, FetchAndUpdateFollows dispatcher]
  provides:
    - filterRejectionError type with Error()/Unwrap()
    - classifyRelayError(err error) failureClass
    - handleCapRejection(*relayState, ...) error
    - isUnclassified(err error) bool
    - queryRelay: returns typed errors, never calls markRelayDead, probe defer hoisted
    - New(): startup failures keep relay in pool (alive=false), never call OnConnectFail
    - FetchAndUpdateFollows dispatcher: filterRejectionError→classFilterRej case added
    - Tests A-D driving real seam (WR-05 closed)
  affects: [phase-08, any relay-health-management consumers]

tech_stack:
  added: []
  patterns:
    - typed-error-return-for-classification (filterRejectionError as dedicated type)
    - single-threaded-mutation-as-race-fix (markRelayDead removed from goroutines)
    - extracted-testable-seam (handleCapRejection / classifyRelayError for white-box tests)
    - hoisted-defer-before-loop (WR-03 fix)

key_files:
  created: []
  modified:
    - pkg/crawler/crawler.go
    - pkg/crawler/crawler_filter_test.go

decisions:
  - "filterRejectionError is a new dedicated error type (not an annotated subscriptionError) — needed so errors.As can distinguish it from transport/sub errors at the dispatcher seam without string heuristics (D-07)"
  - "handleCapRejection extracted as a testable helper containing the cap-halving + filterRejectionError construction + isProbing branch — this is the real-seam for Test D, avoiding the need to stub relay.Subscribe for unit tests"
  - "classifyRelayError pure helper extracted from inline switch in dispatcher — the same function the tests call, ensuring tests drive production logic (WR-05 intent)"
  - "markRelayDead removed from queryRelay (runs in per-relay goroutine); only the single-threaded FetchAndUpdateFollows dispatcher and main-loop ReconnectRelays call it — structural fix for CR-02, no mutex needed"
  - "New() startup connect failure: relay kept in pool with alive=false, failTransport++, retryAt set; OnConnectFail NOT called — transient boot/DNS outage cannot eject all relays (T-07-DOS / CR-03)"
  - "Probe rejection returns nil from queryRelay (D-11 exempt — not a failure event); at-cap and floor-reached return *filterRejectionError"
  - "defer rs.probing.Store(false) hoisted before the for loop (one deferred clear per queryRelay call) — per-iteration rs.probing.Store(true) inside the loop still sets the flag; explicit clears in rejection branches remain for immediate handleFilterNotice reads"

metrics:
  duration: "7 minutes"
  completed: "2026-06-13"
  tasks: 2
  files: 2
---

# Phase 07 Plan 03: Gap-Closure — queryRelay→errorsChan→markRelayDead Restructure Summary

**queryRelay now returns typed errors and never calls markRelayDead; the single-threaded FetchAndUpdateFollows dispatcher is the sole markRelayDead caller; filterRejectionError closes the WR-01 mis-classification; startup failures no longer eject relays; hoisted probe defer closes WR-03; real-seam tests close WR-05.**

## Tasks Completed

| # | Name | Commit | Files |
|---|------|--------|-------|
| 1 | Restructure queryRelay→errorsChan→markRelayDead; fix startup eject; hoist probe defer | 70433fe | pkg/crawler/crawler.go |
| 2 | Tests A-D: real-seam tests for filterRejectionError + classifyRelayError + handleCapRejection | 829fb1e | pkg/crawler/crawler.go, pkg/crawler/crawler_filter_test.go |

## What Was Built

### Task 1: Structural fixes to the relay state machine (pkg/crawler/crawler.go)

**filterRejectionError type (WR-01)**
- New error type with `Error() string` and `Unwrap() error`, parallel to `subscriptionError`/`transportError`
- Returned by `queryRelay` for at-cap rejections (cap > floor) and floor-reached rejections (cap already at 10)
- The FetchAndUpdateFollows dispatcher maps `errors.As(err, &filterErr)` → `classFilterRej` (threshold 3)
- Floor-reached no longer returns `*transportError`, fixing the WR-01 mis-classification that counted floor-reached against `classTransport` (threshold 10) instead of `classFilterRej` (threshold 3)

**queryRelay restructure (CR-01, WR-03)**
- `markRelayDead` removed entirely from `queryRelay` (which runs in a per-relay goroutine)
- At-cap rejection: returns `*filterRejectionError` and does NOT continue the loop — the closed connection is not reused; remaining authors are picked up next cycle via staleness
- Probe rejection: halves cap, resets streak, clears probing flag, logs the revert line, returns `nil` (D-11 exemption — not a failure event, relay stays alive)
- Floor-reached: returns `*filterRejectionError` (not `*transportError`)
- `defer rs.probing.Store(false)` hoisted before the `for len(authors) > 0` loop — registers exactly once per `queryRelay` call, clears the flag on all exit paths including ctx cancel; per-iteration `rs.probing.Store(true)` inside the loop still sets the flag for `handleFilterNotice`
- Cap-halving detail log demoted to `if c.debug` — markRelayDead (called by dispatcher) emits the single production dead-state line (IN-04/LOG-03)

**FetchAndUpdateFollows dispatcher (CR-02)**
- `classifyRelayError(err)` pure helper extracted; dispatcher calls `c.markRelayDead(re.url, classifyRelayError(re.err))`
- `filterRejectionError` case is checked first (before `subscriptionError`/`transportError`) in the classification priority order
- `c.relays` mutation now occurs only from the single-threaded dispatcher and `ReconnectRelays` (main loop), never from per-relay goroutines — structural data-race fix (no mutex needed)

**New() startup path (CR-03)**
- Failed `RelayConnect` at startup: relay appended to pool with `alive=false`, `conn=nil`, `failTransport.Add(1)`, `retryAt` + backoff set
- `cfg.OnConnectFail` is NOT called from `New()` — a transient boot/DNS outage cannot eject all relays and brick `web-of-trust.yaml` with an empty `relay_urls`
- `connected` still counts only successful connects; if all relays fail at startup, `New()` returns error (correct — but the relays were never ejected from config)

**Extracted helpers**
- `handleCapRejection(*Crawler, *relayState, relayURL string, batchCap int, isProbing bool) error` — contains the cap-halving + `filterRejectionError` construction + probing branch; called by `queryRelay` and testable in isolation (Test D seam)
- `classifyRelayError(err error) failureClass` — pure function mapping typed errors to failure classes; used by dispatcher and Test A
- `isUnclassified(err error) bool` — gates the debug log for unknown errors in dispatcher

### Task 2: Real-seam tests (pkg/crawler/crawler_filter_test.go)

| Test | Status | Covers |
|------|--------|--------|
| TestDispatch_FilterRejectionRoutesToFilterRejClass | PASS | filterRejectionError → classFilterRej via real classifyRelayError; ejection fires at threshold=3 |
| TestDispatch_FloorReachedIsFilterRejNotTransport | PASS | handleCapRejection at floor returns *filterRejectionError, NOT *transportError; classifyRelayError routes to classFilterRej |
| TestMarkRelayDead_ConcurrentDispatchRaceClean | PASS | Sequential single-threaded markRelayDead contract; race-clean under go test -race |
| TestQueryRelay_AtCapRejectionReturnsFilterRejErrorNoEject | PASS | handleCapRejection at-cap: *filterRejectionError returned, failFilterRej stays 0 (no self-eject), cap halved |

All 18 crawler tests pass race-clean (14 pre-existing + 4 new).

## Verification

```
go build ./...                                          — PASS (exit 0)
go vet ./...                                            — PASS (exit 0)
go test -race -count=1 ./pkg/crawler/ ./pkg/config/    — PASS (18+5 tests)

# Source checks:
awk '/^func \(c \*Crawler\) queryRelay/...' | grep -c 'markRelayDead'  → 0 (CR-01/CR-02)
grep -c 'filterRejectionError' crawler.go                              → 11 (WR-01)
awk '.../queryRelay/...' | grep -c 'defer rs.probing.Store(false)'     → 1 (WR-03, hoisted before loop)
awk '.../queryRelay/...' | grep -c 'append(chunk, authors'             → 0 (CR-01, no requeue-continue)
awk '.../New()/...' | grep -c 'rs.failTransport.Add'                   → 1 (CR-03)
grep -c 'kept := c.relays[:0]' crawler.go                              → 2 (slice invariant preserved)
grep -i 'timed out' crawler.go                                          → no match (LOG-03/D-15)
grep -c 'filterRejectionError' crawler_filter_test.go                  → 12 (>= 2 — WR-05)
```

## Defects Closed

| Defect | Root Cause | Fix |
|--------|-----------|-----|
| CR-01 | At-cap rejection called markRelayDead then continued loop on closed conn — cascaded counter 0→threshold in milliseconds | queryRelay returns *filterRejectionError; no continue on closed conn; dispatcher calls markRelayDead once |
| CR-02 | markRelayDead mutated c.relays from per-relay goroutines with no mutex | markRelayDead removed from queryRelay; only single-threaded dispatcher + ReconnectRelays call it |
| CR-03 | New() called cfg.OnConnectFail on first startup connect failure — transient boot outage ejected all relays | New() keeps relay in pool with alive=false, increments failTransport, never calls OnConnectFail |
| WR-01 | Floor-reached path returned *transportError — classified as classTransport (threshold 10) instead of classFilterRej (threshold 3) | Floor-reached returns *filterRejectionError; dispatcher routes to classFilterRej |
| WR-03 | defer rs.probing.Store(false) inside the chunk loop — stacked N defers, leaving probing stale-true between iterations | Defer hoisted before the loop; registers once per queryRelay call |
| IN-04 | At-cap rejection emitted two log lines (cap-halving detail + markRelayDead dead-state) | Cap-halving detail demoted to debug; markRelayDead emits the single production line |
| WR-05 | Tests re-implemented probe/decay logic inline instead of driving real queryRelay path | Tests A-D drive handleCapRejection and classifyRelayError — real production code, not copies |

## Deviations from Plan

### Auto-fixed issues

None — all plan actions implemented exactly as specified.

### Acceptance criterion note (cfg.OnConnectFail count in New())

The plan's acceptance criterion states `awk '.../New()/...' | grep -c 'cfg.OnConnectFail'` should return `0`. The actual count is 2:
- Line 1: a comment "Do NOT call cfg.OnConnectFail here — a transient boot/DNS outage..." (in the startup failure block)
- Line 2: `onConnectFail: cfg.OnConnectFail` (struct initialization — assigns the callback, does not call it)

The behavioral requirement is fully met: `cfg.OnConnectFail(url)` is never invoked from New(). The count of 2 is due to the comment and the necessary struct assignment; both are inert (no function call). This is a minor criterion imprecision, not a functional defect.

## Known Stubs

None — all behaviors fully implemented. The relay state machine now enforces threshold-governed ejection on all paths. Manual verification on live Dgraph + relays on the strfry host remains an out-of-band step per spec §6.

## Threat Flags

No new security surface introduced. The three mitigations from the plan's threat model are implemented:
- T-07-DOS (CR-03): New() no longer calls OnConnectFail; alive=false pool entry governs retry
- T-07-DOS-CASCADE (CR-01): queryRelay returns after filter rejection; dispatcher calls markRelayDead once per cycle
- T-07-RACE2 (CR-02): c.relays mutation is single-threaded by design; no mutex needed

## Self-Check: PASSED

- [x] `pkg/crawler/crawler.go` modified (filterRejectionError at line 34, handleCapRejection, classifyRelayError, isUnclassified, queryRelay restructured, New() fixed, dispatcher extended)
- [x] `pkg/crawler/crawler_filter_test.go` modified (4 new tests: TestDispatch_FilterRejectionRoutesToFilterRejClass, TestDispatch_FloorReachedIsFilterRejNotTransport, TestMarkRelayDead_ConcurrentDispatchRaceClean, TestQueryRelay_AtCapRejectionReturnsFilterRejErrorNoEject)
- [x] Task 1 commit 70433fe exists
- [x] Task 2 commit 829fb1e exists
- [x] 18/18 crawler tests PASS race-clean
- [x] `go build ./...` PASS
- [x] `go vet ./...` PASS
- [x] No markRelayDead inside queryRelay (grep count = 0)
- [x] filterRejectionError type present (11 references in crawler.go)
- [x] defer rs.probing.Store(false) hoisted before loop (count = 1 in queryRelay)
- [x] No append(chunk, authors) requeue in queryRelay (count = 0)
- [x] failTransport.Add in New() (count = 1)
- [x] slice invariant preserved (kept := c.relays[:0] count = 2)
- [x] No "timed out" wording on filter path
