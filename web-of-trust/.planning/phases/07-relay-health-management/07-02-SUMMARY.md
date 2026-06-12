---
phase: 07-relay-health-management
plan: "02"
subsystem: crawler
tags: [relay-state-machine, atomic, failure-classification, probe-up, log-consolidation, tdd]

requires:
  - phase: 07-01
    provides: [EjectionThresholds, EjectRelayURL, Config.RelayEjectionThresholds, Config.EjectedRelays]

provides:
  - failureClass type with classTransport/classFilterRej/classSubFlap constants and String()
  - per-class relayState counters (failTransport/failFilterRej/failSubFlap atomic.Int32)
  - successStreak (atomic.Int32) and probing (atomic.Bool) on relayState for probe-up
  - Crawler.ejectionThresholds map[failureClass]int32 populated from Config.EjectionThresholds
  - crawler.Config.EjectionThresholds field
  - markRelayDead(url, class failureClass) — single-log-line dead state with threshold-driven ejection
  - ReconnectRelays: halving decay (D-01), no filterCap reset (D-09), D-03 counter path, D-13 sweep summary
  - queryRelay: probe-up after streak>=10, D-11 probe exemption, LOG-03 filter-rejection wording
  - handleFilterNotice: successStreak/probing reset on CAS, D-14 one cap-change line
  - logRelayError: debug-only (D-15)
  - OnConnectFail wired to config.EjectRelayURL in cmd/crawler/main.go
  - EjectionThresholds passed from cfg.RelayEjectionThresholds into crawler.Config

affects: [phase-08, any relay-health-management consumers]

tech-stack:
  added: []
  patterns:
    - per-class atomic.Int32 counters with integer-halving decay on reconnect (D-01/D-05)
    - threshold-driven ejection via map[failureClass]int32 with <=0 safety fallback
    - probe-up by doubling cap after 10 successes with defer-cleared atomic.Bool flag (D-10/D-11)
    - single log-line state-change pattern via markRelayDead (LOG-03/D-15)
    - sweep-summary line only when something changed (LOG-01/D-13)

key-files:
  created: []
  modified:
    - pkg/crawler/crawler.go
    - pkg/crawler/crawler_filter_test.go
    - cmd/crawler/main.go

key-decisions:
  - "Per-class counters use named atomic.Int32 fields (failTransport/failFilterRej/failSubFlap) not an array, matching patterns of filterCap from Phase 6"
  - "markRelayDead is the single owner of the dead-state log line; callers (FetchAndUpdateFollows error dispatch) must NOT emit a WARN before calling it (LOG-03/D-15)"
  - "Probe-up flag (probing atomic.Bool) is force-cleared via defer on all queryRelay exits including context cancel (Pitfall 3)"
  - "handleFilterNotice resets successStreak/probing on CAS success but does NOT call markRelayDead; D-11 ejection decision lives at queryRelay call site where isProbing local flag is available"
  - "successStreak incremented once per successful queryRelay call (not per chunk) in the FetchAndUpdateFollows goroutine success path"
  - "Forward relay confirmed exempt: failure path at ReconnectRelays lines 346-370 is self-contained and does not route through markRelayDead or onConnectFail (Pitfall 6)"

requirements-completed: [RELAY-01, RELAY-02, RELAY-03, LOG-01, LOG-02, LOG-03]

duration: 35min
completed: "2026-06-12"
tasks: 3
files: 3
---

# Phase 07 Plan 02: Relay Health State Machine Rewrite Summary

**Three-class failure counters with halving decay, probe-up filter-cap recovery, and single-line-per-state-change logging replace the single-counter reset-on-reconnect state machine**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-12T21:00:00Z
- **Completed:** 2026-06-12T21:35:00Z
- **Tasks:** 3 (Tasks 1+2 combined TDD, Task 3 wiring)
- **Files modified:** 3

## Accomplishments

- RELAY-01: failure counters halve on reconnect (not reset); flapping trends past threshold; full reset only on completed query
- RELAY-02: three classes (transport/filter_rejection/subscription_flap) with per-class configurable thresholds; ejection logs class/count/threshold and persists via EjectRelayURL
- RELAY-03: filterCap persists across reconnects (reset-on-reconnect deleted); probe-up doubles cap after 10 successes with exemption from ejection counting and force-cleared flag on all exits
- LOG-01/02/03: one sweep-summary line on change; one cap-change line per batch; single dead-state line (no duplicate WARN+dead); no "timed out" wording for filter-cap deaths; RELAY_ERROR JSON demoted to debug

## Task Commits

1. **RED: Failing tests (Tasks 1+2)** - `87e2499` (test)
2. **GREEN: Implementation (Tasks 1+2)** - `fc3e7f2` (feat)
3. **Task 3: Wiring and verification** - `c523aeb` (feat)

_TDD: RED committed first (build-fails confirmed), then GREEN (all 14 tests passing race-clean)_

## Files Created/Modified

- `pkg/crawler/crawler.go` — full state machine rewrite: failureClass type, per-class relayState fields, markRelayDead(url, class), ReconnectRelays with decay+summary, FetchAndUpdateFollows error dispatch, queryRelay probe-up, handleFilterNotice D-14, logRelayError debug-only
- `pkg/crawler/crawler_filter_test.go` — 8 new unit tests for decay halving, String() matching, threshold ejection, zero-threshold guard, probe-up sizing, probe exemption
- `cmd/crawler/main.go` — OnConnectFail → config.EjectRelayURL; EjectionThresholds from cfg.RelayEjectionThresholds

## Decisions Made

- markRelayDead is the single authoritative dead-state log emitter; FetchAndUpdateFollows error dispatch calls it without emitting its own WARN line
- handleFilterNotice does NOT call markRelayDead (no *Crawler receiver); D-11 exemption is enforced at queryRelay call site using the local `isProbing` flag
- successStreak increments per queryRelay call (not per chunk) — aligns with RESEARCH.md Q2 guidance
- Forward relay block in ReconnectRelays unchanged and confirmed exempt from markRelayDead routing (Pitfall 6)
- Test for TestMarkRelayDead_EjectsAtThreshold uses failFilterRej=1 (below) and failFilterRej=2 (at threshold after Add(1)) to cover both branches; plan spec said "already 2" which maps to the at-threshold scenario

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test scenario A counter value corrected**
- **Found during:** Task 1 test run (TestMarkRelayDead_EjectsAtThreshold)
- **Issue:** Plan spec said "relay whose failFilterRej is already 2" for the "below threshold" scenario, but `markRelayDead` calls `Add(1)` internally — a starting value of 2 reaches the threshold of 3, causing the below-threshold scenario to eject
- **Fix:** Set starting value to 1 for "below threshold" scenario A (Add(1) → 2 < 3); kept 2 for "at threshold" scenario B (Add(1) → 3 = threshold = eject)
- **Files modified:** pkg/crawler/crawler_filter_test.go
- **Verification:** TestMarkRelayDead_EjectsAtThreshold passes with correct behavior in both scenarios
- **Committed in:** fc3e7f2

**2. [Rule 1 - Bug] Removed unused variable rsB and sync import from test**
- **Found during:** Task 1 compile check
- **Issue:** TestProbeRejection_ExemptFromEjection had an unused `rsB` variable (from makeC() call) and a stale `sync` import
- **Fix:** Inlined the scenario B relay state directly; removed the makeC() call and sync import
- **Files modified:** pkg/crawler/crawler_filter_test.go
- **Verification:** Build passes, test passes race-clean
- **Committed in:** fc3e7f2

---

**Total deviations:** 2 auto-fixed (both Rule 1 — test bugs)
**Impact on plan:** Both fixes trivial test code corrections. No scope creep. Production logic unchanged.

## Issues Encountered

None — the state machine surgery went cleanly. The defer-based probing flag cleanup pattern (Pitfall 3) required placing the `defer rs.probing.Store(false)` inside the per-author chunk loop after `isProbing` is set; this ensures the defer fires on any return from queryRelay regardless of exit path.

## Threat Flags

No new security surface introduced beyond what the plan's threat model already covers:
- T-07-RACE2 (atomic counter/cap concurrent writes): mitigated — all new fields use atomic.Int32/atomic.Bool; `go test -race` passes
- T-07-PROBE (probe rejections inflating filter_rejection counter): mitigated — D-11 exemption confirmed in TestProbeRejection_ExemptFromEjection
- T-07-DOS2 (zero threshold immediate ejection): mitigated — both markRelayDead and ReconnectRelays apply `<= 0 → 10` safety fallback, in addition to Plan 01's config-load guard
- T-07-FWD (forward relay accidentally ejected): mitigated — forward relay block confirmed exempt (no markRelayDead routing, documented)

## Known Stubs

None — all behavior fully implemented. Manual verification on live Dgraph + relays on the strfry host is the remaining out-of-band step per spec §6 (integration tests gate on live infrastructure via `//go:build integration`).

## User Setup Required

None — no external service configuration required beyond the existing Dgraph + relay infrastructure already in place.

## Next Phase Readiness

- Phase 7 is fully complete (plans 01 + 02): RELAY-01/02/03 + LOG-01/02/03 all delivered
- Phase 8 (frontier + timeout + observability) can proceed: PERF-01/02, TIMEOUT-01/02, METRIC-01 depend on Phase 5 (VALID) for MarkAttempted UID stamps — Phase 5 complete
- Forward relay and Dgraph infrastructure unchanged; no migration needed

---
*Phase: 07-relay-health-management*
*Completed: 2026-06-12*
