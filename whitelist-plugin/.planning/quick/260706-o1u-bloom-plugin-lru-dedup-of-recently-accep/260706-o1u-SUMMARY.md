---
phase: quick/260706-o1u
plan: 01
subsystem: infra
tags: [go, container-list, lru, dedup, strfry-plugin, bloom-plugin]

# Dependency graph
requires: []
provides:
  - Bounded, dependency-free accepted-ID LRU (pkg/dedup)
  - Dedup gate in RouterHandler.Handle's accept branch, opt-in via WithDedup
  - dedup_capacity config knob on BloomConfig (default 100000)
  - cmd/bloom wired to reject duplicate copies of already-accepted events
affects: [relay-fan-in, strfry-router, quarantine]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Lock-free single-writer LRU (container/list + map[[32]byte]*list.Element), matching pkg/bloomgate/checker.go's single-writer/many-reader contract"
    - "Opt-in behavior extension via fluent WithDedup(...) setter that keeps NewRouterHandler's signature and existing call sites untouched"
    - "Empty-Msg reject convention for high-frequency rejects to avoid per-event log flooding at the strfry router layer"

key-files:
  created:
    - /Users/g/git/deepfry/whitelist-plugin/pkg/dedup/lru.go
    - /Users/g/git/deepfry/whitelist-plugin/pkg/dedup/lru_test.go
  modified:
    - /Users/g/git/deepfry/whitelist-plugin/pkg/handler/messages.go
    - /Users/g/git/deepfry/whitelist-plugin/pkg/handler/router_handler.go
    - /Users/g/git/deepfry/whitelist-plugin/pkg/handler/router_handler_test.go
    - /Users/g/git/deepfry/whitelist-plugin/pkg/config/config.go
    - /Users/g/git/deepfry/whitelist-plugin/cmd/bloom/main.go

key-decisions:
  - "LRU keyed on decoded [32]byte event ID (not the 64-char hex string) to halve per-entry memory"
  - "Dup rejects use empty Msg (RejectDuplicate) so strfry's router does not log the blocked event per-dup"
  - "Per-dup handler logging is sampled (1 line per 1000 suppressed dups) instead of one log line per duplicate"
  - "capacity<=0 fully disables the LRU (no-op, never allocates/grows), preserving byte-identical legacy behavior when dedup_capacity is 0 or negative"
  - "Not-in-WoT event IDs are never inserted into the LRU (only the accept branch touches SeenOrAdd)"

patterns-established:
  - "Deduper interface kept in pkg/handler (not importing pkg/dedup) so tests can inject a fake without a concrete dependency"

requirements-completed:
  - DEDUP-01

# Metrics
duration: 25min
completed: 2026-07-06
status: complete
---

# Quick Task 260706-o1u: Bloom Plugin LRU Dedup of Recently-Accepted Events Summary

**Bounded container/list-backed LRU (pkg/dedup) gates the bloom plugin's accept branch so duplicate copies of an already-accepted event are rejected with an empty msg instead of re-queued in strfry's writer.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2/2 completed
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments
- New `pkg/dedup` package: hand-rolled, dependency-free bounded LRU keyed on `[32]byte` event IDs, with 100% test coverage (eviction, touch-on-hit ordering, disabled/no-op mode, Len bound).
- `RejectDuplicate` output constructor with empty `Msg`, documented against strfry's `cmd_router.cpp` logging behavior and the prior disk-fill incident.
- `RouterHandler.WithDedup` fluent opt-in gate: duplicate of an already-accepted event is rejected before reaching the existing accept/reject/quarantine branches, so dups never enqueue to quarantine and never re-log a full decision line (sampled summary logging instead).
- `dedup_capacity` config knob (default 100000) on `BloomConfig`; `cmd/bloom` wires `dedup.New(cfg.DedupCapacity)` into the handler and logs the configured capacity at startup.
- `cmd/router` and its 9 existing test call sites are untouched — `NewRouterHandler`'s signature did not change.

## Task Commits

1. **Task 1: Bounded accepted-ID LRU in pkg/dedup** - `22fc748` (feat)
2. **Task 2: Wire dedup gate into the bloom RouterHandler, config, and main** - `0fcf458` (feat)

_Both tasks were single-commit (no separate TDD RED/GREEN commits — tests and implementation were written and verified together per task, then committed atomically)._

## Files Created/Modified
- `pkg/dedup/lru.go` - Bounded LRU: `New`, `SeenOrAdd`, `Len`; disabled no-op when capacity<=0
- `pkg/dedup/lru_test.go` - Unit tests: disabled mode, new-vs-dup, eviction, touch-on-hit, Len bound
- `pkg/handler/messages.go` - `RejectDuplicate(eventId string) OutputMsg` (empty Msg)
- `pkg/handler/router_handler.go` - `Deduper` interface, `WithDedup`, `decodeEventID`, dedup gate + sampled logging in `Handle`'s accept branch
- `pkg/handler/router_handler_test.go` - `fakeDeduper` + 4 new tests covering dedup reject, not-in-WoT non-recording, non-hex ID passthrough, no-deduper legacy path
- `pkg/config/config.go` - `DedupCapacity` field + `dedup_capacity` default (100000) on `BloomConfig`
- `cmd/bloom/main.go` - Imports `pkg/dedup`; wires `WithDedup(dedup.New(cfg.DedupCapacity))`; logs configured capacity at startup

## Decisions Made
- Key type is `[32]byte` (decoded ID), not the 64-char hex string — smaller LRU footprint at the target 100000-entry capacity.
- Dedup gate sits strictly inside the existing `if ok {` (whitelisted) branch, before the accept log/return — the not-in-WoT reject/quarantine branches are never reached for a dup, and never touch the dedup LRU.
- Sampled summary logging (`dupLogSampleN = 1000`) chosen over per-dup empty-msg-only silence, so operators retain periodic visibility into suppressed-dup volume without recreating the prior high-frequency-logging disk-fill incident.
- `decodeEventID` treats any non-64-hex ID as "skip dedup silently" rather than an error path, keeping the plugin's fail-open-on-malformed-input posture consistent with the rest of `Handle`.

## Deviations from Plan

None - plan executed exactly as written.

One incidental side effect worth noting (not a deviation from the plan's intent, but from byte-for-byte diff expectations): `pkg/handler/messages.go` and `pkg/config/config.go` had pre-existing CRLF line endings on disk. Editing them introduced a few LF-terminated lines, and running `gofmt -w` (required to keep the touched struct/const blocks properly aligned per `make lint`/`gofmt` conventions) normalized the entire file to LF. This produced a larger line-count diff in those two files than the logical change alone would suggest, but no logic changed beyond what's described above — confirmed via `go build ./...`, full `go test ./...`, and `go vet` all passing before and after the normalization.

## Issues Encountered

None - first test run for `TestLRU_EvictsOldestAtCapacity` had an incorrect assertion in my own test code (re-inserting the just-evicted key before checking the other still-tracked keys, which shifted eviction order) — fixed by reordering the assertions before the final `go test` run. Not a bug in `pkg/dedup`; caught and corrected during the same task, no separate commit needed since it was pre-commit.

## User Setup Required

None - no external service configuration required. `dedup_capacity` is a new optional key in `~/deepfry/whitelist.yaml`; if absent, viper's default (100000) applies with no action needed. Deployment/rollout to the strfry host is explicitly out of scope for this quick task.

## Next Phase Readiness
- Code, tests, and both build variants (`go build ./...`, alpine cross-build) are verified locally. Deployment to the relay-fan-in host (per `strfry-router-split-deploy` memory) is a separate, deliberate follow-up step, not part of this quick task.
- Suggested follow-up: after deployment, watch bloom plugin logs for the sampled `dedup: suppressed N duplicate accepts` lines to confirm the ~84% duplicate-write reduction measured in production analysis actually materializes, and re-tune `dedup_capacity` if the observed unique-accept rate differs materially from the ~9/sec assumption used to size the default.

## Self-Check: PASSED

All created/modified files verified present on disk; both task commits (`22fc748`, `0fcf458`) verified present in git log.

---
*Phase: quick/260706-o1u*
*Completed: 2026-07-06*
