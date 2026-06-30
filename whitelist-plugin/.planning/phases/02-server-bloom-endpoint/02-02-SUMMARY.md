---
phase: "02-server-bloom-endpoint"
plan: "02"
subsystem: "whitelist-plugin"
status: complete
tags: ["bloom", "server", "main", "callback", "end-to-end-test", "SRV-01"]
dependency_graph:
  requires: ["02-01-SUMMARY.md"]
  provides: ["SetOnRefresh wiring in cmd/server/main.go", "end-to-end bloom membership + ETag test"]
  affects: ["cmd/server/main.go", "pkg/server/server_test.go"]
tech_stack:
  added: []
  patterns: ["SetOnRefresh callback registered before Start() (initial-refresh lockstep)", "build-error-return without swap (D-02)", "SwapFilter + SetStats per successful refresh (D-10)"]
key_files:
  created: []
  modified:
    - cmd/server/main.go
    - pkg/server/server_test.go
decisions:
  - "Callback registered before refresher.Start() so the initial synchronous refresh builds the first filter — /bloom ready in lockstep with the whitelist (D-01)"
  - "Build or SwapFilter error logs and returns without updating stats — prior filter and stats preserved on any failure path (D-02)"
  - "SetStats called only after SwapFilter succeeds — entries and last_refresh stay consistent with the filter in place (D-10)"
  - "TestBloomReflectsWhitelist drives SwapFilter directly (no Dgraph) to exercise criteria 1 and 3 in isolation"
metrics:
  duration: "~2 minutes"
  completed: "2026-06-30"
  tasks_completed: 2
  files_modified: 2
---

# Phase 02 Plan 02: Server Bloom Endpoint (Integration Wiring) Summary

**One-liner:** Wired SetOnRefresh callback in cmd/server/main.go to rebuild + atomically swap the bloom filter and update live stats on every successful refresh; added end-to-end test proving membership tracks the whitelist and ETag changes after a key-set change.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Register SetOnRefresh callback in main (SRV-01, D-01, D-09, D-10) | 82d284a | cmd/server/main.go |
| 2 | End-to-end test — /bloom reflects whitelist and ETag changes after refresh | e3ec758 | pkg/server/server_test.go |

## Verification Evidence

- `go build ./...` exits 0
- `go test ./... -count=1` exits 0 (all 9 packages with test files pass)
- `go vet ./cmd/server/` clean
- `git diff --stat HEAD whitelist-plugin/pkg/whitelist/whitelist.go` shows NO changes (D-03 confirmed)
- `refresher.SetOnRefresh(...)` appears in cmd/server/main.go before `refresher.Start()`
- TestBloomReflectsWhitelist passes: SwapFilter A → 200 + ETag; SwapFilter B with stale If-None-Match → 200 + new ETag; k2 possibly-present in B

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None. All /bloom integration is live: SetOnRefresh registered, initial refresh builds the first filter, SwapFilter stores pre-serialized bytes, handleBloom serves them with conditional GET.

## Threat Flags

No new threat surface beyond what was modeled in the plan's threat register. The callback runs on the refresher goroutine (off the request path) and SwapFilter is a single atomic pointer store (T-02-05 mitigated). A failed build or serialize error returns without storing, preserving the prior filter (T-02-06 mitigated).

## Self-Check: PASSED

- cmd/server/main.go: FOUND (SetOnRefresh registered before refresher.Start())
- pkg/server/server_test.go: FOUND (TestBloomReflectsWhitelist added at line 432)
- Commits 82d284a, e3ec758: both present in git log
- pkg/whitelist/whitelist.go: UNCHANGED (D-03)
