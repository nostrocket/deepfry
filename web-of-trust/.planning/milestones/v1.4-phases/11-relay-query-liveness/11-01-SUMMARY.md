---
phase: 11-relay-query-liveness
plan: 01
subsystem: crawler
tags: [go, nostr, goroutine, context, relay, hang, liveness, timeout]

# Dependency graph
requires: []
provides:
  - "FetchAndUpdateFollows returns within a bounded multiple of c.timeout regardless of per-relay goroutine state (HANG-01)"
  - "queryRelay bounded relay.Subscribe via child goroutine + ctx-select (HANG-02)"
  - "Dispatcher closes and marks dead outstanding relays on DeadlineExceeded path (HANG-03)"
  - "Regression test TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks GREEN (TEST-02)"
  - "New tests: partial-return and close-on-timeout assertions"
affects: [crawler-loop, reconnect-relays, phase-12-onwards]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Child-goroutine + buffered-result-channel pattern for bounding context-ignoring library calls (HANG-02)"
    - "completedThisBatch atomic.Bool on relayState: batch-scoped marker reset at top of FetchAndUpdateFollows, read by dispatcher on timeout exit"
    - "Non-blocking eventsChan drain with labeled break (drainLoop) on relayQueryContext.Done()"
    - "Independent timeout exit path: dispatcher returns without gating on wg.Wait() or eventsChan close (HANG-01)"

key-files:
  created:
    - web-of-trust/pkg/crawler/crawler_hang_test.go
  modified:
    - web-of-trust/pkg/crawler/crawler.go

key-decisions:
  - "Wrap relay.Subscribe in child goroutine with buffered (size 1) result channel; select on result vs ctx.Done() in queryRelay (HANG-02) — unblocks queryRelay on timeout without waiting for Fire() to unpark"
  - "completedThisBatch atomic.Bool on relayState (not sync.Map) — per-relay field reset at batch start; goroutines set it on both success and error paths before wg.Done"
  - "relayQueryDoneCh case becomes independent exit: non-blocking drainLoop then return, no wg.Wait() dependency (HANG-01)"
  - "Mark-dead only on DeadlineExceeded, not context.Canceled (EOSE-quorum early exit is normal — must not penalise returning relays)"
  - "Partial-return test uses liveness+goroutine-completion assertions (not pubkeysWithEvents hit) because dgraph.Client is a concrete struct with no mock interface — dgClient would panic on nil if event processing path is hit"

patterns-established:
  - "Bounding context-ignoring library calls: run in child goroutine, select result vs ctx.Done(), use buffered channel of size 1 to avoid goroutine leak on send"
  - "Batch-scoped relay-state marker: reset before launch loop, set inside goroutine, read by single-threaded dispatcher after Done()"
  - "CR-02 preserved: conn-close and relay-state mutation only in dispatcher (FetchAndUpdateFollows) and ReconnectRelays, never in per-relay goroutines"

requirements-completed: [HANG-01, HANG-02, HANG-03, TEST-02]

# Metrics
duration: 25min
completed: 2026-06-16
---

# Phase 11 Plan 01: Relay Query Liveness Summary

**Eliminated 48-minute crawler hang by making FetchAndUpdateFollows return within a bounded multiple of c.timeout via child-goroutine-bounded Subscribe (HANG-02), independent dispatcher timeout exit (HANG-01), and conn-close+mark-dead for outstanding relays on timeout (HANG-03)**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-06-16T06:17:00Z
- **Completed:** 2026-06-16T06:42:01Z
- **Tasks:** 3
- **Files modified:** 2

## Accomplishments

- Fixed the root cause of the production 48-minute crawler hang: go-nostr `Subscription.Fire()` blocks on a bare channel receive over the relay write queue and ignores the context passed to `relay.Subscribe`. A half-open TCP connection caused a permanent park, wedging `wg.Wait()` → `eventsChan` never closed → dispatcher blocked forever.
- `queryRelay` now wraps `Subscribe` in a child goroutine with a buffered (size 1) result channel and selects on the result vs `ctx.Done()`, returning `ctx.Err()` immediately on timeout without waiting for `Fire()` to unblock (HANG-02).
- `FetchAndUpdateFollows` dispatcher no longer gates on `wg.Wait()` or `eventsChan` close. On `relayQueryContext.Done()`, it non-blocking-drains buffered events and returns `pubkeysWithEvents, nil` (HANG-01). On `DeadlineExceeded`, it closes and marks dead any outstanding alive relay via `markRelayDead(classTransport)` (HANG-03, CR-02 preserved).
- `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` now passes GREEN (was RED). Two new tests added: partial-return liveness gate and HANG-03 close-on-timeout assertion. `make test` fully green.

## Task Commits

Each task was committed atomically:

1. **Task 1: Bound relay.Subscribe against context-ignoring Fire()** - `47d1dbb` (fix)
2. **Task 2: Dispatcher independent timeout exit + close-and-mark-dead** - `b843635` (fix)
3. **Task 3: Tests — regression gate, partial-return, close-on-timeout** - `17436ad` (test)

## Files Created/Modified

- `web-of-trust/pkg/crawler/crawler.go` — Added `completedThisBatch atomic.Bool` to `relayState`; wrapped `relay.Subscribe` call in child goroutine with buffered result channel + ctx-select in `queryRelay`; restructured `relayQueryDoneCh` dispatcher case to non-blocking drain + optional mark-dead + independent return
- `web-of-trust/pkg/crawler/crawler_hang_test.go` — New file (created by this plan); contains original regression test plus `TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks` and `TestFetchAndUpdateFollows_ClosesAndMarksStuckRelayDead`

## Decisions Made

- **Buffered channel size 1 for Subscribe result:** Prevents the abandoned child goroutine from blocking on send once Subscribe eventually unblocks (when the dispatcher closes the connection via HANG-03).
- **`completedThisBatch` field vs sync.Map:** Per-relayState field chosen over a function-local `sync.Map` keyed by `*relayState` — simpler, avoids a heap allocation per batch, and the single-threaded dispatcher is the sole reader so no concurrent-read hazard after Done() fires.
- **DeadlineExceeded-only mark-dead:** EOSE-quorum-cancel path (`context.Canceled`) does not mark relays dead — that is a normal healthy early exit, not a fault. Only `context.DeadlineExceeded` (hard timeout) triggers mark-dead.
- **Partial-return test uses liveness assertion:** `dgraph.Client` is a concrete struct with no mock interface; calling `dgClient.AddFollowers`/`TouchLastDBUpdate` on a nil pointer would panic. Asserting goroutine-completion + return-within-budget is the meaningful alternative that proves partial progress without requiring a live Dgraph.
- **`drainSubscription` unchanged:** Already ctx-aware (selects on `sub.Events`, `sub.EndOfStoredEvents`, `sub.Context.Done()`, `ctx.Done()`); only the `Subscribe`/`Fire` call needed wrapping.

## Deviations from Plan

None - plan executed exactly as written. The implementation matched the specified HANG-02/HANG-01/HANG-03/TEST-02 changes. The only discretionary choice was the partial-return test's assertion style (liveness vs pubkeysWithEvents hit), which the plan explicitly permitted.

## Issues Encountered

One design consideration encountered: the partial-return test could not assert `pubkeysWithEvents` contains a specific pubkey because `dgraph.Client` is a concrete struct (not an interface), making it impossible to inject a mock without modifying production code. The plan's stated alternative ("assert liveness+budget") was used instead. The assertion is still meaningful: it proves the good relay's goroutine completed and the dispatcher returned within budget while the stuck relay was still blocking.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. The fix is confined to `pkg/crawler` goroutine coordination logic. Existing `event.CheckSignature()` validation unchanged (T-11-03 accepted).

## User Setup Required

None - no external service configuration required. Fix is pure `pkg/crawler` logic; existing deployment and config unchanged.

## Next Phase Readiness

- Crawler hang fully fixed; a stuck or half-open relay can no longer wedge `FetchAndUpdateFollows` for more than `c.timeout` + small overhead.
- `make test` fully green; regression gate in place.
- Existing threshold ejection (RELAY-01/02) now governs repeated-timeout relays via the new mark-dead path.
- `ReconnectRelays` and the normal all-goroutines-returned path are unchanged.

## Self-Check: PASSED

- `web-of-trust/pkg/crawler/crawler.go` exists and was modified
- `web-of-trust/pkg/crawler/crawler_hang_test.go` exists (created)
- Commits 47d1dbb, b843635, 17436ad all present in git log
- `make test` exits 0, all tests green including `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` GREEN

---
*Phase: 11-relay-query-liveness*
*Completed: 2026-06-16*
