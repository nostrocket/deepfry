---
phase: "06-filter-size-per-relay-cap-detection"
plan: "01"
subsystem: "web-of-trust"
tags: [filter-size, per-relay-cap, config, crawler, notice-handler]
dependency_graph:
  requires: []
  provides:
    - relay_filter_batch_size config field (FILTER-01)
    - filterCap per-relay in-memory state (FILTER-02 foundation)
    - handleFilterNotice NOTICE-based cap halving (FILTER-02)
    - WithNoticeHandler wiring at both connect sites (FILTER-02)
  affects:
    - pkg/config/config.go
    - pkg/crawler/crawler.go
    - cmd/crawler/main.go
tech_stack:
  added: []
  patterns:
    - Viper default + struct field (existing pattern in pkg/config)
    - Per-relay in-memory state on relayState struct
    - WithNoticeHandler relay option from go-nostr
    - Closure-over-pointer for loop-variable capture safety
key_files:
  created: []
  modified:
    - pkg/config/config.go
    - pkg/crawler/crawler.go
    - cmd/crawler/main.go
decisions:
  - "Floor for filterCap halving is 10 (D-05); minCap=10 hardcoded in WithNoticeHandler closures"
  - "rs created before nostr.RelayConnect in both New() and ReconnectRelays() to allow closure capture"
  - "filterBatchSize stored on Crawler struct so ReconnectRelays() can access it without cfg in scope"
metrics:
  duration: "117s"
  completed: "2026-06-11T08:23:21Z"
  tasks_completed: 2
  files_modified: 3
---

# Phase 06 Plan 01: Filter Size & Per-Relay Cap Detection — Config + State Foundation Summary

**One-liner:** relay_filter_batch_size config field (default 100) wired through crawler with per-relay filterCap and NOTICE-based cap halving via WithNoticeHandler at both connect sites.

## What Was Built

### Task 1: relay_filter_batch_size config field and cmd/crawler wiring

- Added `RelayFilterBatchSize int \`mapstructure:"relay_filter_batch_size"\`` to `pkg/config/config.go` Config struct after `StalePubkeyThreshold`
- Added `viper.SetDefault("relay_filter_batch_size", 100)` in `LoadConfig()` immediately after the existing stale_pubkey_threshold default
- Removed `const batchSize = 500` from `cmd/crawler/main.go`
- Replaced `batchSize` with `cfg.RelayFilterBatchSize` in the `GetStalePubkeys` call
- Added `FilterBatchSize: cfg.RelayFilterBatchSize` to the `crawlerCfg` struct literal

**Commit:** a77e735

### Task 2: filterCap on relayState, handleFilterNotice helper, WithNoticeHandler wiring

- Added `filterCap int` field to `relayState` struct (plain int, not atomic — single goroutine per relay writes it)
- Added `FilterBatchSize int` to `crawler.Config` struct to carry the configured batch size into `New()` and `ReconnectRelays()`
- Added `filterBatchSize int` unexported field to `Crawler` struct for use at reconnect time
- Added `handleFilterNotice(rs *relayState, notice string, minCap int)` package-level function: case-insensitive substring match on "filter" and "too large"; halves `rs.filterCap` using `max(rs.filterCap/2, minCap)` with floor=10; logs the result
- Updated `New()`: creates `rs` before `nostr.RelayConnect` so the closure captures the pointer; passes `nostr.WithNoticeHandler(...)` as relay option; assigns `rs.conn = relay` and `rs.alive = true` after successful connect
- Stores `c.filterBatchSize = cfg.FilterBatchSize` in the Crawler constructor
- Updated `ReconnectRelays()`: adds same `WithNoticeHandler` closure over existing `rs` pointer before `nostr.RelayConnect` call; uses `c.filterBatchSize` is not directly needed (minCap is fixed at 10)

**Commit:** e043a32

## Verification

- `make build` exits 0 — all 5 binaries built successfully
- `make test` exits 0 — all unit tests pass (pkg/dgraph: 0.749s)
- `grep -r "RelayFilterBatchSize"` — found in both pkg/config/config.go and cmd/crawler/main.go
- `grep "filterCap"` — found in relayState struct, New(), handleFilterNotice
- `grep "WithNoticeHandler"` — found at both connect sites (New and ReconnectRelays)
- `grep "batchSize" cmd/crawler/main.go` — empty (constant removed)

## Deviations from Plan

None — plan executed exactly as written.

## Threat Surface Scan

T-06-01 (Tampering — relay-controlled NOTICE text): mitigated as planned. `handleFilterNotice` uses `strings.ToLower` + `strings.Contains` for substring matching only; NOTICE text is never written to Dgraph, config, or any persistent store. No format string evaluation.

T-06-02 (DoS — filterCap halving to floor): accepted as planned. Floor=10 prevents infinite halving.

No new threat surface beyond what was specified in the plan's threat model.

## Known Stubs

None. The filterCap field is initialized and used; handleFilterNotice is wired. Plan 02 builds the chunked sub-REQ loop on top of the filterCap state introduced here.

## Self-Check: PASSED

- pkg/config/config.go — modified and present
- pkg/crawler/crawler.go — modified and present
- cmd/crawler/main.go — modified and present
- Commit a77e735 — exists (git log confirms)
- Commit e043a32 — exists (git log confirms)
- `make build` — exits 0
- `make test` — exits 0
