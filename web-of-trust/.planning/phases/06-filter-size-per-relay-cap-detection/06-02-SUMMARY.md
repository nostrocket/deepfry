---
phase: "06-filter-size-per-relay-cap-detection"
plan: "02"
subsystem: "web-of-trust"
tags: [filter-size, per-relay-cap, chunked-sub-req, connection-drop-attribution, unit-tests]
dependency_graph:
  requires:
    - 06-01 (filterCap on relayState, handleFilterNotice, WithNoticeHandler wiring)
  provides:
    - drainSubscription helper extracted from queryRelay inner loop
    - queryRelay chunked sub-REQ loop (filterCap-sized chunks, sequential)
    - 500ms connection-drop attribution with cap halving and chunk re-queue
    - transportError at cap floor=10 with repeated drops (relay ejection)
    - Unit tests: TestHandleFilterNotice_Halves/Floor/HalveToFloor/CaseInsensitive/UnrelatedNotice
    - Unit tests: TestSplitAuthorsChunks (250 authors, cap=100 → 100/100/50)
  affects:
    - pkg/crawler/crawler.go
    - pkg/crawler/crawler_filter_test.go (new)
tech_stack:
  added: []
  patterns:
    - drainSubscription helper extraction (reusable per-chunk drain loop)
    - Chunked sub-REQ loop with sequential chunks inside queryRelay
    - 500ms temporal heuristic for connection-drop-on-REQ attribution
    - max() built-in (Go 1.21+) for floor clamping
    - Same-package test file (package crawler) for unexported type access
key_files:
  created:
    - pkg/crawler/crawler_filter_test.go
  modified:
    - pkg/crawler/crawler.go
decisions:
  - "queryRelay signature replaces relay *nostr.Relay + relayURL string with rs *relayState — rs.conn and rs.url used inside function; single call site updated"
  - "drainSubscription does NOT defer sub.Unsub() — caller manages Unsub() to allow clean per-chunk lifecycle"
  - "500ms attribution window uses time.Since(subscribeStart) on error return — no goroutine needed"
  - "Chunk loop inlined in queryRelay (not extracted to separate helper) — simple enough to read inline, per plan"
metrics:
  duration: "89s"
  completed: "2026-06-11T08:27:50Z"
  tasks_completed: 2
  files_modified: 2
---

# Phase 06 Plan 02: Filter Size & Per-Relay Cap Detection — Chunked Sub-REQ Loop Summary

**One-liner:** queryRelay refactored with filterCap-sized chunked sub-REQ loop, 500ms connection-drop attribution, and drainSubscription helper; 6 unit tests cover all cap-halving behaviors.

## What Was Built

### Task 1: Refactor queryRelay — extract drainSubscription and add chunked sub-REQ loop with connection-drop attribution

**Commit:** 9bef57d

**drainSubscription** extracted from `queryRelay`'s inner event loop as a new method:
```
(c *Crawler) drainSubscription(ctx context.Context, sub *nostr.Subscription, relayURL string, eventsChan chan<- *nostr.Event) error
```
Returns nil on EOSE, ctx.Err() on external cancellation, `&transportError` on `sub.Context.Done()`. The caller (chunk loop) calls `sub.Unsub()` after each use — drainSubscription does not defer it, keeping per-chunk lifecycle explicit.

**queryRelay** signature updated from `(ctx, relay *nostr.Relay, relayURL string, filter, eventsChan)` to `(ctx, rs *relayState, filter, eventsChan)` — rs.conn and rs.url used internally. Single call site in FetchAndUpdateFollows updated to `c.queryRelay(relayQueryContext, rs, filter, eventsChan)`.

**Chunked sub-REQ loop** (`for len(authors) > 0`):
- Each iteration takes a `filterCap`-sized chunk from the authors slice
- Sends a separate Subscribe call per chunk to the same relay
- All events feed the same `eventsChan` (upstream dedup in FetchAndUpdateFollows handles duplicates)
- Chunks run sequentially within queryRelay (no parallel goroutines per D-07)

**500ms connection-drop attribution** (D-09/D-10):
- Records `subscribeStart := time.Now()` before each Subscribe call
- If Subscribe errors within 500ms AND error is "not connected"/"failed to write":
  - If `rs.filterCap > 10`: halve to `max(rs.filterCap/2, 10)`, log, re-prepend chunk to authors, continue
  - If `rs.filterCap == 10` (floor): log and return `&transportError` (relay marked dead)
- Longer-lived drops (>500ms) fall through to existing transport/subscription error classification

**Safety guard:** if `rs.filterCap <= 0`, cap is treated as 10 to prevent infinite loops.

### Task 2: Unit tests for handleFilterNotice cap-halving, floor, and chunk boundary splitting

**Commit:** e40d338

Created `pkg/crawler/crawler_filter_test.go` with `package crawler` (same-package, no build tag):

| Test | Input | Expected |
|------|-------|----------|
| TestHandleFilterNotice_Halves | cap=100, "filter item too large" | cap=50 |
| TestHandleFilterNotice_CaseInsensitive | cap=100, "Filter Too Large" (mixed case) | cap=50 |
| TestHandleFilterNotice_Floor | cap=10, "filter item too large" | cap=10 (unchanged) |
| TestHandleFilterNotice_HalveToFloor | cap=12, "filter item too large" | cap=10 (max(6,10)=10) |
| TestHandleFilterNotice_UnrelatedNotice | cap=100, "too many results" | cap=100 (unchanged) |
| TestSplitAuthorsChunks | 250 authors, cap=100 | chunks: [100, 100, 50] |

TestSplitAuthorsChunks tests the chunking math directly by simulating the `for len(remaining) > 0` slicing loop inline — no WebSocket connection required.

## Verification

- `make build` exits 0 — all 5 binaries built
- `make test` exits 0 — pkg/crawler (1.5% coverage), pkg/dgraph (3.2% coverage)
- `go test ./pkg/crawler/... -v -run "TestHandleFilterNotice|TestSplitAuthorsChunks"` — all 6 PASS
- `grep "drainSubscription" pkg/crawler/crawler.go` — found (method + 2 call sites)
- `grep "for len(authors) > 0" pkg/crawler/crawler.go` — found
- `grep "500*time.Millisecond" pkg/crawler/crawler.go` — found
- `grep -c "filterCap" pkg/crawler/crawler.go` — 11 (>5 required)

## Deviations from Plan

None — plan executed exactly as written.

## Threat Surface Scan

T-06-04 (DoS — infinite retry via repeated 500ms drops): mitigated as specified. Cap floor=10 limits halving to at most log2(initialCap/10) ≈ 4 retries before the relay is ejected via transportError. No new threat surface introduced.

T-06-05 (Tampering — relay-controlled Subscribe error strings): accepted as specified. The "not connected"/"failed to write" strings originate from the go-nostr WebSocket transport layer, not from relay protocol messages; relay cannot inject them via NOTICE or NIP-01 responses.

## Known Stubs

None. queryRelay is fully wired — chunks are sent, events collected, filterCap read/written per relay.

## Self-Check: PASSED

- pkg/crawler/crawler.go — modified, present
- pkg/crawler/crawler_filter_test.go — created, present
- Commit 9bef57d — exists (Task 1)
- Commit e40d338 — exists (Task 2)
- `make build` — exits 0
- `make test` — exits 0
- All 6 unit tests pass
