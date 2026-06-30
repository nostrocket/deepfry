---
phase: "02-server-bloom-endpoint"
plan: "01"
subsystem: "whitelist-plugin"
status: complete
tags: ["bloom", "server", "config", "whitelist-refresher", "atomic", "tdd"]
dependency_graph:
  requires: ["01-shared-bloom-library"]
  provides: ["bloom_fp_rate config", "onRefresh callback seam", "GET /bloom endpoint"]
  affects: ["pkg/config", "pkg/whitelist", "pkg/server"]
tech_stack:
  added: ["whitelist-plugin/pkg/bloom (imported in server.go)"]
  patterns: ["atomic.Pointer store (single-writer/many-reader)", "503-while-loading JSON guard", "TDD RED/GREEN cycle"]
key_files:
  modified:
    - pkg/config/config.go
    - pkg/whitelist/whitelist_refresher.go
    - pkg/server/server.go
    - pkg/server/server_test.go
decisions:
  - "bloomEntry is an unexported struct holding pre-serialized bytes + ETag; SwapFilter serializes once per generation so handleBloom is alloc-free per request (D-05)"
  - "SetStats stores entries/lastRefresh atomically without touching s.ready; readiness is owned solely by SetReady (D-10)"
  - "onRefresh callback fires only in the success branch of refresh(), after UpdateKeys, never on error paths (D-02)"
  - "bloomSnapshot is a completely separate atomic.Pointer[bloomEntry] from the whitelist.list pointer — zero coupling (D-03)"
metrics:
  duration: "~15 minutes"
  completed: "2026-06-30"
  tasks_completed: 3
  files_modified: 4
---

# Phase 02 Plan 01: Server Bloom Endpoint (Producer Seams) Summary

**One-liner:** Added bloom_fp_rate config key (1e-6 default), onRefresh callback hook on WhitelistRefresher, and the server's separate atomic bloom filter pointer with SwapFilter/SetStats/handleBloom wired to GET /bloom (200/304/503).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add bloom_fp_rate config key (SRV-04, D-09) | 12af1f3 | pkg/config/config.go |
| 2 | Add onRefresh callback to WhitelistRefresher (D-01, D-02) | 8dc4991 | pkg/whitelist/whitelist_refresher.go |
| 3 (RED) | Add failing tests for bloom endpoint | 379d746 | pkg/server/server_test.go |
| 3 (GREEN) | Add bloomEntry/SetStats/SwapFilter/handleBloom/GET /bloom | e378111 | pkg/server/server.go |

## Verification Evidence

- `go build ./...` exits 0
- `go test ./... -count=1` exits 0 (all packages pass)
- `go vet ./pkg/config/ ./pkg/whitelist/ ./pkg/server/` clean
- `git diff --stat whitelist-plugin/pkg/whitelist/whitelist.go` shows NO changes (D-03 confirmed)
- handleCheck/handleBulkCheck/handleHealth/handleStats/handleVersion handler bodies byte-identical to pre-phase
- /stats JSON shape `{entries, last_refresh}` unchanged

## Deviations from Plan

None — plan executed exactly as written.

## TDD Gate Compliance

Task 3 followed the RED/GREEN cycle:
- RED commit (379d746): `test(02-01): add failing tests for bloom endpoint` — build failed (SwapFilter/SetStats undefined)
- GREEN commit (e378111): `feat(02-01): add bloomEntry, SetStats, SwapFilter, handleBloom, GET /bloom route` — all tests pass

## Known Stubs

None. This plan establishes producer seams only. The integration wiring (Plan 02, Wave 2: cmd/server/main.go) is intentionally deferred and will wire SetOnRefresh + SwapFilter + SetStats together.

## Threat Flags

No new threat surface beyond what was modeled in the plan's threat register. handleBloom's If-None-Match header is used only for 200 vs 304 branching — no injection surface (T-02-01). Pre-serialization means no per-request build work (T-02-02).

## Self-Check: PASSED

- pkg/config/config.go: FOUND (BloomFPRate field + SetDefault)
- pkg/whitelist/whitelist_refresher.go: FOUND (onRefresh field + SetOnRefresh setter + callback site)
- pkg/server/server.go: FOUND (bloomSnapshot + SetStats + SwapFilter + handleBloom + GET /bloom route)
- pkg/server/server_test.go: FOUND (TestHandleBloom_NotReady, TestHandleBloom_OK, TestHandleBloom_NotModified, TestSetStats_LiveValues)
- Commits 12af1f3, 8dc4991, 379d746, e378111: all present in git log
- pkg/whitelist/whitelist.go: UNCHANGED (D-03)
