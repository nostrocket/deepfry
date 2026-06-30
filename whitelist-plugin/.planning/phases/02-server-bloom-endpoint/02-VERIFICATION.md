---
phase: 02-server-bloom-endpoint
verified: 2026-06-30T00:00:00Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 02: Server Bloom Endpoint Verification Report

**Phase Goal:** The existing whitelist server rebuilds a bloom filter from its in-memory whitelist on every refresh and exposes it over HTTP with cheap conditional polling, without disturbing existing endpoints or read latency.
**Verified:** 2026-06-30
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | After each whitelist refresh, `GET /bloom` returns a serialized filter whose membership matches the current whitelist (newly-added pubkey is possibly-present after next refresh) | VERIFIED | `TestBloomReflectsWhitelist` passes: SwapFilter A → 200 + ETag; SwapFilter B with k2 added → 200 + new ETag; f2B.Contains(k2) == true. cmd/server/main.go wires SetOnRefresh before Start() so initial refresh builds first filter (commit 82d284a). |
| 2 | The bloom filter is swapped atomically alongside the existing map — concurrent `/check` and `/bloom` reads never stall or observe a torn filter | VERIFIED | `bloomSnapshot atomic.Pointer[bloomEntry]` is a completely separate field from `whitelist.list`; SwapFilter does a single `bloomSnapshot.Store`; handleBloom does a single `bloomSnapshot.Load`; whitelist.go is unmodified (D-03 confirmed by `git diff --stat` showing zero changes). Both reads are lock-free with independent atomic pointers. |
| 3 | A client that sends `If-None-Match` with the ETag from its last fetch receives `304 Not Modified` while unchanged, and a fresh `200` with a new ETag after a refresh changes it | VERIFIED | `TestHandleBloom_NotModified` (304 + ETag header + empty body) and `TestBloomReflectsWhitelist` (stale ETag on SwapFilter B yields 200 + different ETag) both PASS. |
| 4 | An operator can set the false-positive rate / sizing in the server YAML and the served filter reflects that setting (default 0.0001%) | VERIFIED | `BloomFPRate float64` field with `mapstructure:"bloom_fp_rate"` in `ServerConfig`; `v.SetDefault("bloom_fp_rate", 0.000001)` in `LoadServerConfig`; callback in main uses `cfg.BloomFPRate` as the `fp` argument to `bloom.NewBuilder` (commit 12af1f3, 82d284a). |
| 5 | The existing `/check`, `/health`, `/stats`, and `/version` endpoints behave exactly as before | VERIFIED | All five are registered unchanged in Handler(). All pre-existing tests (TestHandleCheck_*, TestHandleBulkCheck*, TestHandleHealth_*, TestHandleStats, TestHandleVersion) pass in `go test ./... -count=1`. No handler bodies were modified. `SetStats` confirmed NOT to call `s.ready.Store` (only `s.entries.Store` + `s.lastRefresh.Store`). |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/config/config.go` | `BloomFPRate float64` field + `v.SetDefault("bloom_fp_rate", 0.000001)` | VERIFIED | Line 23: `BloomFPRate float64 \`mapstructure:"bloom_fp_rate"\``; line 52: `v.SetDefault("bloom_fp_rate", 0.000001)` |
| `pkg/whitelist/whitelist_refresher.go` | `onRefresh` field + `SetOnRefresh` setter + success-branch callback site | VERIFIED | Line 20: `onRefresh func(keys [][32]byte)` field; lines 40-42: `SetOnRefresh` setter; lines 92-94: nil-guarded callback after `UpdateKeys`, inside success branch before `return`; NOT reachable from retry-exhausted path |
| `pkg/server/server.go` | `bloomSnapshot atomic.Pointer[bloomEntry]`, `SetStats`, `SwapFilter`, `handleBloom`, `GET /bloom` route | VERIFIED | Lines 19-22: `bloomEntry` struct; line 32: `bloomSnapshot` field; lines 55-58: `SetStats`; lines 62-69: `SwapFilter`; lines 112-133: `handleBloom`; line 79: `GET /bloom` route |
| `pkg/server/server_test.go` | `TestHandleBloom_NotReady`, `TestHandleBloom_OK`, `TestHandleBloom_NotModified`, `TestSetStats_LiveValues`, `TestBloomReflectsWhitelist` | VERIFIED | All five tests present (lines 229, 254, 312, 383, 434); all PASS |
| `cmd/server/main.go` | `SetOnRefresh` callback registered before `refresher.Start()` | VERIFIED | Line 49: `refresher.SetOnRefresh(...)` — line 69 is `refresher.Start()`; callback rebuilds filter via `bloom.NewBuilder(uint(len(keys)), cfg.BloomFPRate)`, calls `SwapFilter` and `SetStats` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/server/server.go (SwapFilter)` | `pkg/bloom (Filter.MarshalBinary / Filter.ETag)` | `SwapFilter` calls `f.MarshalBinary()` and `f.ETag()` to populate `bloomEntry` | WIRED | Lines 63-68: `b, err := f.MarshalBinary()` and `etag: f.ETag()` |
| `pkg/server/server.go (handleBloom)` | `pkg/server/server.go (bloomSnapshot)` | `handleBloom` calls `s.bloomSnapshot.Load()` and branches on nil / If-None-Match | WIRED | Lines 113-132: `snap := s.bloomSnapshot.Load()` then nil check, then ETag match check |
| `cmd/server/main.go (callback)` | `pkg/bloom (NewBuilder/Add/Build)` | Callback builds filter using `bloom.NewBuilder(uint(len(keys)), cfg.BloomFPRate)` | WIRED | Lines 51-59: `b := bloom.NewBuilder(...)` + `b.Add(k)` loop + `b.Build()` |
| `cmd/server/main.go (callback)` | `pkg/server (SwapFilter + SetStats)` | Callback calls `srv.SwapFilter(f)` and `srv.SetStats(len(keys), time.Now())` | WIRED | Lines 60-64: `srv.SwapFilter(f)` and `srv.SetStats(...)` |
| `cmd/server/main.go (SetOnRefresh)` | `pkg/whitelist (refresher.Start initial refresh)` | Callback registered before `Start()` — initial synchronous refresh triggers it | WIRED | Line 49 `SetOnRefresh` precedes line 69 `refresher.Start()` |

### Data-Flow Trace (Level 4)

Not applicable — no dynamic-data-rendering component. The bloom endpoint serves pre-serialized bytes swapped via atomic pointer; the data flow is: Dgraph → refresher → callback → bloom.Build → SwapFilter → bloomSnapshot → handleBloom → HTTP response. All linkages verified above.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full build compiles | `go build ./...` | exit 0, no output | PASS |
| Bloom + stats tests pass | `go test ./pkg/server/ -run 'Bloom\|Stats' -count=1 -v` | All 7 named tests PASS in 0.305s | PASS |
| Full suite passes | `go test ./... -count=1` | All 8 packages with test files pass; 0 failures | PASS |
| `go vet` clean | `go vet ./pkg/config/ ./pkg/whitelist/ ./pkg/server/ ./cmd/server/` | exit 0, no output | PASS |
| whitelist.go unmodified | `git diff --stat HEAD -- pkg/whitelist/whitelist.go` | No output (no changes) | PASS |

### Probe Execution

No probes declared in PLAN files. No conventional probe scripts found.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| SRV-01 | 02-01, 02-02 | Server rebuilds bloom filter from in-memory whitelist on each refresh and swaps it atomically (lock-free reads, no read stalls) | SATISFIED | `SetOnRefresh` callback wired in main; `atomic.Pointer[bloomEntry]` in server; `TestBloomReflectsWhitelist` PASSES |
| SRV-02 | 02-01 | `GET /bloom` returns the current serialized filter | SATISFIED | `handleBloom` returns 200 + `application/octet-stream` + cached bytes; `TestHandleBloom_OK` PASSES |
| SRV-03 | 02-01 | `/bloom` supports conditional GET (ETag / If-None-Match), returning 304 when unchanged | SATISFIED | `handleBloom` compares `If-None-Match` to snapshot etag; `TestHandleBloom_NotModified` and `TestBloomReflectsWhitelist` criteria-3 assertion PASS |
| SRV-04 | 02-01 | Bloom false-positive rate / sizing configurable via server YAML (default 0.0001%) | SATISFIED | `BloomFPRate float64` in `ServerConfig`; `v.SetDefault("bloom_fp_rate", 0.000001)`; callback uses `cfg.BloomFPRate` |

No orphaned requirements: REQUIREMENTS.md traceability table maps SRV-01..04 exclusively to Phase 2 and all four are satisfied.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none) | — | — | — | — |

No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER markers found in any of the five modified files. No stub return patterns (`return null`, `return {}`, `return []`) in production code.

### Human Verification Required

None. All success criteria are mechanically verifiable: build succeeds, all tests pass with named behavioral assertions, atomic pointer separation is structurally provable from source, and the wiring order (SetOnRefresh before Start) is confirmed in source.

### Gaps Summary

No gaps. All five success criteria are verified against actual codebase evidence.

---

_Verified: 2026-06-30T00:00:00Z_
_Verifier: Claude (gsd-verifier)_
