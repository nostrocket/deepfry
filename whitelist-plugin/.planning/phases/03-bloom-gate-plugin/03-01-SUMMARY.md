---
phase: 03-bloom-gate-plugin
plan: "01"
subsystem: pkg/config + pkg/bloomgate
tags: [bloom, config, checker, gate, tdd]
dependency_graph:
  requires: [pkg/bloom, pkg/handler]
  provides: [BloomConfig, LoadBloomConfig, BloomChecker]
  affects: [cmd/bloom (Plan 02)]
tech_stack:
  added: [pkg/bloomgate]
  patterns: [atomic.Pointer single-writer/many-reader, ready-channel cold-start gate, viper config additive pattern]
key_files:
  created:
    - pkg/bloomgate/checker.go
    - pkg/bloomgate/checker_test.go
    - pkg/config/bloom_config_test.go
  modified:
    - pkg/config/config.go
decisions:
  - "BloomChecker uses sync.Once to guard the single close of the ready channel — idempotent Store with no panic on repeated calls"
  - "IsWhitelisted fast-path checks filter.Load() != nil before waiting on ready — avoids channel overhead on the steady-state hot path"
  - "BloomConfig.BloomPath default computed from ensureConfigDir() result at load time, resolving to ~/deepfry/bloom.dfbf"
metrics:
  duration: "12m"
  completed: "2026-06-30"
  tasks_completed: 2
  files_modified: 4
status: complete
---

# Phase 03 Plan 01: BloomConfig + BloomChecker Foundation Summary

BloomConfig/LoadBloomConfig reading whitelist.yaml with five bloom_-prefixed defaults, and BloomChecker — a network-free handler.Checker over an atomic *bloom.Filter with a GATE-06 cold-start ready gate.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 RED | Add failing tests for BloomConfig | 7c11af2 | pkg/config/bloom_config_test.go |
| 1 GREEN | Implement BloomConfig + LoadBloomConfig | cca97ce | pkg/config/config.go |
| 2 RED | Add failing tests for BloomChecker | 801f4f4 | pkg/bloomgate/checker_test.go |
| 2 GREEN | Implement BloomChecker | 0788297 | pkg/bloomgate/checker.go |

## What Was Built

### Task 1: BloomConfig + LoadBloomConfig (GATE-07)

Added to `pkg/config/config.go` (additive only — existing loaders untouched):

- `BloomConfig` struct with five `mapstructure`-tagged fields:
  - `ServerURL` (`server_url`, default `http://localhost:8081`) — reuses shared key per D-02
  - `BloomRefreshInterval` (`bloom_refresh_interval`, default `6h`) — D-03
  - `BloomPath` (`bloom_path`, default `~/deepfry/bloom.dfbf`) — D-03
  - `BloomFetchTimeout` (`bloom_fetch_timeout`, default `30s`) — D-03
  - `RefreshRetryCount` (`refresh_retry_count`, default `3`) — D-03

- `LoadBloomConfig()` mirrors `LoadClientConfig` exactly: `viper.New()`, `ensureConfigDir()`, `SetConfigName("whitelist")`, `SetConfigType("yaml")`, `AddConfigPath(configDir)`, `SetDefault(...)`, `readConfig(v, configDir, "whitelist.yaml")`, `v.Unmarshal(&cfg)`.

Tests: `TestLoadBloomConfigDefaults` (HOME isolated to temp dir, all five defaults asserted) + `TestLoadBloomConfigOverrides` (yaml file values override defaults).

### Task 2: BloomChecker in pkg/bloomgate (GATE-01/GATE-02/D-06/D-12)

New package `pkg/bloomgate/checker.go`:

- `BloomChecker` struct: `atomic.Pointer[bloom.Filter]` (single-writer/many-reader), `chan struct{} ready` (closed on first Store), `sync.Once` (guards single close)
- `NewBloomChecker(logger *log.Logger) *BloomChecker`
- `Store(f *bloom.Filter)`: atomically swaps filter, closes ready via Once on first call
- `IsWhitelisted(pubkey string) (bool, error)`:
  - Fast path: if filter already loaded, calls `filter.ContainsHex(pubkey)` directly
  - Slow path: waits `<-c.ready` then re-loads and calls `ContainsHex` (GATE-06 blocking)
  - No net/http, no LittleEndian, no hex decode in checker body

Compile-time assertion `var _ handler.Checker = (*BloomChecker)(nil)` in checker.go.

Tests: query pass-through, block-before-ready (goroutine + timeout), subsequent-calls-immediate, Store-idempotent.

## Acceptance Criteria Verification

```
go build ./pkg/config/ ./pkg/bloomgate/         PASS
go test ./pkg/config/ ./pkg/bloomgate/ -count=1 PASS (4 tests)
go vet ./pkg/config/ ./pkg/bloomgate/           PASS

grep -c 'func LoadBloomConfig' pkg/config/config.go  → 1
grep -c 'func LoadClientConfig' pkg/config/config.go → 1 (unchanged)
grep -c 'func LoadServerConfig' pkg/config/config.go → 1 (unchanged)
grep -c 'net/http' pkg/bloomgate/checker.go          → 0 (no per-event HTTP)
grep -c 'ContainsHex' pkg/bloomgate/checker.go       → 3 (≥1)
grep -c 'LittleEndian' pkg/bloomgate/checker.go      → 0 (hard invariant)
grep -c 'ready' pkg/bloomgate/checker.go             → 14 (≥2)
```

Prohibited packages (cmd/whitelist, cmd/router, pkg/client, pkg/whitelist, pkg/handler, pkg/bloom): zero diff.

## Deviations from Plan

None — plan executed exactly as written. TDD RED/GREEN cycle completed for both tasks.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. `BloomChecker.IsWhitelisted` is purely in-memory (atomic load + channel wait). `LoadBloomConfig` reads a local operator-controlled file. Both are within the T-03-SC / T-03-03 accepted-risk envelope documented in the plan's threat model.

## Self-Check

Files created/modified:
- pkg/bloomgate/checker.go: FOUND
- pkg/bloomgate/checker_test.go: FOUND
- pkg/config/bloom_config_test.go: FOUND
- pkg/config/config.go: FOUND (modified)

Commits:
- 7c11af2: test(03-01) bloom config RED
- cca97ce: feat(03-01) bloom config GREEN
- 801f4f4: test(03-01) bloomgate checker RED
- 0788297: feat(03-01) bloomgate checker GREEN
