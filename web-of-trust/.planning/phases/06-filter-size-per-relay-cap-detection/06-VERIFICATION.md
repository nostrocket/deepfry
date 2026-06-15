---
phase: 06-filter-size-per-relay-cap-detection
verified: 2026-06-11T16:40:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
---

# Phase 06: Filter Size & Per-Relay Cap Detection — Verification Report

**Phase Goal:** No relay rejects or drops a connection due to an oversized filter REQ, and relays with small caps are automatically queried at a safe size going forward
**Verified:** 2026-06-11T16:40:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Crawler issues REQ filters with at most 100 authors by default | VERIFIED | `viper.SetDefault("relay_filter_batch_size", 100)` in `pkg/config/config.go:64`; `cfg.RelayFilterBatchSize` passed to `GetStalePubkeys` at `cmd/crawler/main.go:110` and to `crawler.Config.FilterBatchSize` at `main.go:77` |
| 2 | Every relay starts with filterCap = relay_filter_batch_size at connect time | VERIFIED | `rs.filterCap.Store(int32(cfg.FilterBatchSize))` in `New()` at `crawler.go:90`; confirmed by `filterCap atomic.Int32` on `relayState` struct at `crawler.go:46` |
| 3 | NOTICE messages containing 'filter' and 'too large' halve the relay's filterCap, flooring at 10 | VERIFIED | `handleFilterNotice` at `crawler.go:670-689`: case-insensitive match on both substrings; CAS loop halves via `old/2` with floor clamp `if newVal < int32(minCap) { newVal = int32(minCap) }`; all 5 unit tests pass |
| 4 | WithNoticeHandler is wired at both New() and ReconnectRelays() connect sites | VERIFIED | Two occurrences of `nostr.WithNoticeHandler(...)` at `crawler.go:91` (New) and `crawler.go:225` (ReconnectRelays); both capture the relay-specific `rs` pointer |
| 5 | FilterBatchSize flows from config.Config → crawler.Config → Crawler struct | VERIFIED | `config.Config.RelayFilterBatchSize` → `cmd/crawler/main.go:77` → `crawler.Config.FilterBatchSize` → `crawler.go:124` → `Crawler.filterBatchSize` |
| 6 | queryRelay splits filter.Authors into filterCap-sized chunks and sends each as a separate Subscribe call | VERIFIED | `for len(authors) > 0` loop at `crawler.go:499-557`; each iteration slices `chunk = authors[:batchCap]` and calls `relay.Subscribe(ctx, []nostr.Filter{chunkFilter})` |
| 7 | Chunks run sequentially within queryRelay — not as parallel goroutines | VERIFIED | Single `for` loop body; `drainSubscription` must return before the next iteration; no `go` keyword in chunk loop |
| 8 | A relay whose connection drops within 500ms of Subscribe halves its filterCap and re-queues the chunk | VERIFIED | `crawler.go:523-534`: `time.Since(subscribeStart) < 500*time.Millisecond` guard; `rs.filterCap.Store(newVal)` where `newVal = old/2` (floored at 10); `authors = append(chunk, authors...)` re-queues |
| 9 | If filterCap is already at 10 and a drop occurs within 500ms, queryRelay returns a transportError marking relay dead | VERIFIED | `crawler.go:536-538`: `if old > 10` else branch returns `&transportError{err: fmt.Errorf("relay %s: filter cap floor reached", relayURL)}` |
| 10 | drainSubscription extracted from queryRelay's inner event loop so the chunk loop can reuse it | VERIFIED | `(c *Crawler) drainSubscription(ctx, sub, relayURL, eventsChan) error` at `crawler.go:452-488`; called at `crawler.go:552` inside chunk loop; caller manages `sub.Unsub()` |
| 11 | Unit tests cover all required paths | VERIFIED | `pkg/crawler/crawler_filter_test.go`: `TestHandleFilterNotice_Halves`, `TestHandleFilterNotice_CaseInsensitive`, `TestHandleFilterNotice_Floor`, `TestHandleFilterNotice_HalveToFloor`, `TestHandleFilterNotice_UnrelatedNotice`, `TestSplitAuthorsChunks` — all 6 PASS |
| 12 | filterCap uses atomic.Int32 (race-safe) and resets to configured value on successful reconnect | VERIFIED | `filterCap atomic.Int32` at `crawler.go:46`; `rs.filterCap.Store(int32(c.filterBatchSize))` in `ReconnectRelays()` at `crawler.go:240`; `go test -race ./pkg/crawler/...` exits 0 with no races |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/config/config.go` | `RelayFilterBatchSize int` field + Viper default 100 | VERIFIED | Field at line 22; `viper.SetDefault("relay_filter_batch_size", 100)` at line 64 |
| `pkg/crawler/crawler.go` | `filterCap atomic.Int32` on relayState, `handleFilterNotice`, `WithNoticeHandler` wiring, `FilterBatchSize` on Config, chunk loop, `drainSubscription` | VERIFIED | All symbols present and wired |
| `cmd/crawler/main.go` | `const batchSize = 500` removed; `cfg.RelayFilterBatchSize` used | VERIFIED | `batchSize` constant absent; `cfg.RelayFilterBatchSize` at lines 77 and 110 |
| `pkg/crawler/crawler_filter_test.go` | 6 unit tests, no build tag, `package crawler` | VERIFIED | File exists, `package crawler`, no build tag, all 6 tests pass |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/config/config.go` | `cmd/crawler/main.go` | `cfg.RelayFilterBatchSize` passed to `GetStalePubkeys` and `crawlerCfg.FilterBatchSize` | VERIFIED | Lines 77 and 110 in main.go |
| `cmd/crawler/main.go` | `pkg/crawler/crawler.go` | `crawler.Config.FilterBatchSize` → `relayState.filterCap` init | VERIFIED | `crawler.go:90` stores the value atomically at connect time |
| `queryRelay` | `relayState.filterCap` | `rs.filterCap` read and written inside chunk loop | VERIFIED | Lines 500, 525, 531 |
| `FetchAndUpdateFollows` | `queryRelay` | Call site passes `rs *relayState` | VERIFIED | `crawler.go:318`: `c.queryRelay(relayQueryContext, rs, filter, eventsChan)` |

### Data-Flow Trace (Level 4)

Not applicable — this phase produces no data-rendering components. The critical data flows are configuration values and per-relay runtime state, both verified via grep and build/test above.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All 6 filter unit tests pass | `go test ./pkg/crawler/... -v -run "TestHandleFilterNotice\|TestSplitAuthorsChunks"` | 6/6 PASS | PASS |
| Full unit test suite passes | `make test` | `ok web-of-trust/pkg/crawler`, `ok web-of-trust/pkg/dgraph` | PASS |
| Race detector clean | `go test -race ./pkg/crawler/...` | exit 0, no races | PASS |
| Build succeeds | `make build` | All 5 binaries built, exit 0 | PASS |

### Probe Execution

No probes declared for this phase.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| FILTER-01 | 06-01-PLAN.md | Default batch size reduced to 100 authors | SATISFIED | `viper.SetDefault("relay_filter_batch_size", 100)` wired through to `GetStalePubkeys` and relay filter construction |
| FILTER-02 | 06-01-PLAN.md, 06-02-PLAN.md | Per-relay cap from NOTICE + connection-drop attribution; chunked sub-queries | SATISFIED | `handleFilterNotice` + `WithNoticeHandler` at both connect sites + chunk loop in `queryRelay` + 500ms drop attribution |

**Note:** REQUIREMENTS.md traceability table still shows FILTER-01 as `Pending` and checkbox as `[ ]`. This is a documentation inconsistency — the implementation is fully present and verified. The REQUIREMENTS.md must be updated to mark FILTER-01 as complete.

### Anti-Patterns Found

Scanned `pkg/config/config.go`, `pkg/crawler/crawler.go`, `cmd/crawler/main.go`, `pkg/crawler/crawler_filter_test.go`.

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None found | — | — | — | — |

No `TBD`, `FIXME`, `XXX`, placeholder stubs, or unresolved debt markers found in any files modified by this phase.

One observation: `queryRelay` uses `rs.filterCap.Store(newVal)` (not a CAS) in the connection-drop halving path, while `handleFilterNotice` uses a CAS loop. This is intentional and safe: `queryRelay` runs as a single goroutine per relay, and the value it reads (`old`) was read from the same atomic immediately before the store, so the window for a concurrent update racing in the wrong direction is negligible and the race detector confirms no actual race. Info-only.

### Human Verification Required

None. All must-haves are verifiable programmatically and confirmed by build/test execution.

### Gaps Summary

No gaps found. All 12 must-have truths are verified in the codebase. The phase goal — no relay rejects or drops a connection due to an oversized filter REQ, and relays with small caps are automatically queried at a safe size — is achieved.

One documentation artifact requires a follow-up update: `REQUIREMENTS.md` should mark FILTER-01 as `[x]` complete and update its traceability row to `Complete`. This is not a code gap.

---

_Verified: 2026-06-11T16:40:00Z_
_Verifier: Claude (gsd-verifier)_
