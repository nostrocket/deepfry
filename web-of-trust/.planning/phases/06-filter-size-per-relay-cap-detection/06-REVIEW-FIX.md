---
phase: "06"
fixed_at: 2026-06-11T08:36:00Z
review_path: .planning/phases/06-filter-size-per-relay-cap-detection/06-REVIEW.md
iteration: 1
findings_in_scope: 4
fixed: 4
skipped: 0
status: all_fixed
---

# Phase 06: Code Review Fix Report

**Fixed at:** 2026-06-11T08:36:00Z
**Source review:** .planning/phases/06-filter-size-per-relay-cap-detection/06-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 4 (CR-01, WR-03, IN-03; plus IN-01 log placement fixed as part of the CR-01 atomic rewrite)
- Fixed: 4
- Skipped: 0

## Fixed Issues

### CR-01: Data race on `relayState.filterCap`

**Files modified:** `pkg/crawler/crawler.go`, `pkg/crawler/crawler_filter_test.go`
**Commit:** 9272414
**Applied fix:**
- Changed `filterCap int` to `filterCap atomic.Int32` in the `relayState` struct (uses the standard library `sync/atomic.Int32` already imported for `failures`)
- Removed `filterCap: cfg.FilterBatchSize` from the struct literal in `New()`; replaced with `rs.filterCap.Store(int32(cfg.FilterBatchSize))` immediately after `rs` is constructed
- In `queryRelay`: replaced `cap := rs.filterCap` with `batchCap := int(rs.filterCap.Load())` (also fixes IN-03 shadow), and replaced the cap-halving block with `old := rs.filterCap.Load()` / `rs.filterCap.Store(newVal)` pattern
- In `handleFilterNotice`: rewrote to use a CAS loop (`rs.filterCap.CompareAndSwap(old, newVal)`) for correct concurrent halving; also moved the log call inside the halving branch and added a separate "already at floor" message (fixing IN-01 log placement as part of the same rewrite)
- Updated `crawler_filter_test.go`: all struct-literal `filterCap: N` initializations replaced with `.Store(N)` calls; all `rs.filterCap` reads replaced with `rs.filterCap.Load()`

### WR-03: `filterCap` not reset when relay reconnects

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 9272414
**Applied fix:** Added `rs.filterCap.Store(int32(c.filterBatchSize))` in `ReconnectRelays()` immediately after `rs.failures.Store(0)` on the successful-reconnect path, so a relay that was previously degraded to a low cap gets its cap reset to the configured starting value on reconnect.

### IN-03: `cap` variable shadows built-in

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 9272414
**Applied fix:** Renamed the local variable `cap` to `batchCap` throughout the chunk loop in `queryRelay`, eliminating the shadow of the `cap()` built-in. Applied as part of the CR-01 atomic conversion.

### IN-01: `handleFilterNotice` logs "halved cap" even when cap is already at floor

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 9272414
**Applied fix:** The CAS-loop rewrite of `handleFilterNotice` (required for CR-01) naturally resolved this: the early-return path when `old <= int32(minCap)` now logs `"cap already at floor %d"` instead of the incorrect "halved cap" message. The halved-cap log only fires after a successful CAS that actually changed the value.

## Verification

- `go build ./pkg/crawler/...`: exit 0
- `go test -short ./pkg/crawler/...`: exit 0, ok
- `go test -race ./pkg/crawler/...`: exit 0, no data races reported
- `make build`: exit 0 (all five binaries built)
- `make test`: exit 0 (pkg/crawler and pkg/dgraph pass)

---

_Fixed: 2026-06-11T08:36:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
