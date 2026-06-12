---
phase: 07-relay-health-management
plan: "01"
subsystem: config
tags: [config, viper, relay-ejection, tdd]
dependency_graph:
  requires: []
  provides: [EjectionThresholds, EjectRelayURL, Config.RelayEjectionThresholds, Config.EjectedRelays]
  affects: [pkg/config/config.go, pkg/config/config_test.go]
tech_stack:
  added: []
  patterns: [viper-singleton-mutation, mapstructure-nested-struct, post-unmarshal-guard]
key_files:
  created:
    - pkg/config/config_test.go
  modified:
    - pkg/config/config.go
decisions:
  - "EjectionThresholds struct with Transport/FilterRej/SubFlap uses mapstructure tags matching YAML keys (transport/filter_rejection/subscription_flap) per D-06"
  - "Post-unmarshal positivity guard resets <=0 thresholds to hardcoded defaults to prevent STRIDE T-07-DOS (zero threshold ejects every relay on first failure)"
  - "EjectRelayURL mirrors RemoveRelayURL pattern (viper singleton + WriteConfig) — no metadata in YAML per D-08; caller logs reason/class/count"
  - "EjectedRelays initialized to non-nil empty slice after unmarshal to ensure safe range operations without nil checks at call sites"
metrics:
  duration: "2 minutes"
  completed: "2026-06-12"
  tasks: 2
  files: 2
---

# Phase 07 Plan 01: Config EjectionThresholds and EjectRelayURL Summary

Per-class ejection thresholds and relay ejection persistence added to the config layer via `EjectionThresholds` struct, two new `Config` fields, Viper defaults, positivity guard, and `EjectRelayURL` function with 5 unit tests.

## Tasks Completed

| # | Name | Commit | Files |
|---|------|--------|-------|
| RED | Failing tests for both tasks | d3a5cb3 | pkg/config/config_test.go (new) |
| GREEN | EjectionThresholds + EjectRelayURL implementation | 886738a | pkg/config/config.go |

## What Was Built

### Task 1: EjectionThresholds config type, fields, defaults, positivity guard

- `EjectionThresholds` struct exported from `pkg/config` with fields `Transport int`, `FilterRej int`, `SubFlap int` tagged with `mapstructure:"transport"`, `mapstructure:"filter_rejection"`, `mapstructure:"subscription_flap"`
- Two new `Config` struct fields: `RelayEjectionThresholds EjectionThresholds` and `EjectedRelays []string`
- Viper `SetDefault` calls in `LoadConfig` after clusterscan defaults: transport=10, filter_rejection=3, subscription_flap=5 via nested map; ejected_relays=[]
- Post-unmarshal positivity guard: any threshold <=0 reset to hardcoded default (STRIDE T-07-DOS mitigation)
- `EjectedRelays` normalized to non-nil empty slice after unmarshal

### Task 2: EjectRelayURL persistence function

- `EjectRelayURL(url string) error` exported from `pkg/config`
- Mirrors `RemoveRelayURL` pattern: reads relay_urls, filters, writes back; reads ejected_relays, appends, writes back; persists via `viper.WriteConfig()`
- Operates on the package-global viper singleton — no second Viper init

### Tests (5 total, all in pkg/config/config_test.go)

| Test | Status | Covers |
|------|--------|--------|
| TestLoadConfig_EjectionThresholdDefaults | PASS | transport=10, filter_rejection=3, subscription_flap=5 defaults |
| TestLoadConfig_EjectionThresholdGuard | PASS | zero/negative thresholds corrected to defaults |
| TestLoadConfig_EjectedRelaysAbsent | PASS | non-nil empty slice when key absent |
| TestEjectRelayURL_MovesToEjected | PASS | URL removed from relay_urls, added to ejected_relays, persisted to YAML |
| TestEjectRelayURL_AppendsNotReplaces | PASS | second ejection appends without dropping first |

## Verification

```
go test ./pkg/config/ — PASS (5/5 tests)
go vet ./pkg/config/ — PASS
make fmt — no diff in pkg/config/config.go
grep 'mapstructure:"relay_ejection_thresholds"' pkg/config/config.go — 1 match (line 43)
grep 'mapstructure:"ejected_relays"' pkg/config/config.go — 1 match (line 44)
grep 'func EjectRelayURL' pkg/config/config.go — 1 match (line 216)
grep -c 'deepfry/web-of-trust.yaml' pkg/config/config_test.go — 0 (no live config path)
```

## Deviations from Plan

None — plan executed exactly as written.

The two TDD tasks were committed as a single RED commit (all 5 tests) and a single GREEN commit (all implementation) since both tasks modify the same file (`pkg/config/config.go`) and the tests span both behaviors. The plan's TDD structure was followed: tests written and committed first (failing), then implementation committed (all passing).

## TDD Gate Compliance

- RED commit: `d3a5cb3` — `test(07-01): add failing tests for EjectionThresholds config type and EjectRelayURL`
- GREEN commit: `886738a` — `feat(07-01): add EjectionThresholds config type, EjectRelayURL, defaults and guard`
- REFACTOR: not needed — code clean on first pass

## Known Stubs

None — all config fields are fully wired. EjectionThresholds values flow from YAML through Viper defaults and the positivity guard into the Config struct. EjectRelayURL persists to disk immediately via WriteConfig.

## Threat Flags

No new security surface introduced beyond what the plan's threat model already covers. The positivity guard (T-07-DOS) and Viper-managed YAML write (T-07-TMP) are both implemented as specified.

## Self-Check: PASSED

- [x] `pkg/config/config_test.go` exists
- [x] `pkg/config/config.go` modified (EjectionThresholds struct at line 14, Config fields at lines 43-44, EjectRelayURL at line 216)
- [x] RED commit d3a5cb3 exists
- [x] GREEN commit 886738a exists
- [x] All 5 tests pass: `go test ./pkg/config/ -v`
- [x] `go vet ./pkg/config/` passes
