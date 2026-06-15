---
phase: 10-unbounded-retry-backoff-hardening
plan: "01"
subsystem: web-of-trust/cmd/crawler
tags: [retry, backoff, resilience, generics, observability, unit-tests]

dependency_graph:
  requires: [Phase 9 RESIL-01 retry skeleton in cmd/crawler/main.go]
  provides: [indefinite transient retry, 1m→5m backoff, ctx-cancel-aware wait, callMetrics accumulator, retryDgraph generic helper]
  affects: [cmd/crawler/main.go, cmd/crawler/main_test.go]

tech_stack:
  added: []
  patterns:
    - "Generic helper retryDgraph[T any] with injected sleepFn for deterministic testing"
    - "callMetrics accumulator: running sum+count per callName, success-only timing"
    - "Shutdown-safe select: case <-sleepFn(delay) / case <-ctx.Done()"
    - "TDD: tests written, verified to fail (logically), then implementation confirmed passing"

key_files:
  modified:
    - cmd/crawler/main.go
  created:
    - cmd/crawler/main_test.go

decisions:
  - "D-01: Four near-identical bounded retry blocks collapsed into one generic retryDgraph[T any] helper"
  - "D-02: Helper returns fatal/ctx-cancel to caller; read calls break mainLoop, MarkAttempted warns+continues"
  - "D-03: sleepFn injected as func(time.Duration)<-chan time.Time; production uses time.After, tests use fake"
  - "D-04: dgraphRetryAttempts removed; constants changed to dgraphRetryInitial=1m / dgraphRetryMax=5m"
  - "D-07: Success-only timing in callMetrics.record(); failed/retried durations excluded"
  - "D-09: MarkAttempted warns+continues on fatal error instead of break mainLoop (best-effort write)"

metrics:
  duration: "2m 46s"
  completed: "2026-06-15"
  tasks_completed: 2
  tasks_total: 2
  files_modified: 1
  files_created: 1
---

# Phase 10 Plan 01: Unbounded Retry & Backoff Hardening Summary

**One-liner:** Generic `retryDgraph[T]` helper with 1m→5m indefinite backoff, ctx-cancel-aware sleep, and per-call-type cumulative-average observability replaces four bounded 5-attempt retry blocks in the crawler main loop.

## What Was Built

### `cmd/crawler/main.go` — refactored

**Constants updated (D-04):**
- `dgraphRetryInitial = 1 * time.Minute` (was 5s)
- `dgraphRetryMax = 5 * time.Minute` (was 2m)
- `dgraphRetryAttempts` removed entirely (indefinite retry replaces attempt cap)

**`callMetrics` accumulator type (D-06/D-07/D-08):**
- `newCallMetrics() *callMetrics` — constructor (map-backed, single-threaded)
- `record(callName string, d time.Duration)` — accumulates successful call durations only
- `avg(callName string) time.Duration` — returns sum/count, 0 when count is 0

**`retryDgraph[T any]` generic helper (D-01/D-02/D-03):**
- Loops indefinitely on transient gRPC codes (`isDgraphTransient` reused verbatim)
- Fatal/non-transient errors returned immediately to caller
- Wait uses `select { case <-sleepFn(delay): case <-ctx.Done(): }` (SHUTDOWN-01)
- Backoff: `delay *= 2; if delay > dgraphRetryMax { delay = dgraphRetryMax }` (1m→2m→4m→5m)
- Log line retains literal `"retrying in %v"` substring (SC#2 observable in console)

**Four collapsed call sites:**
- `GetStalePubkeys` — break mainLoop on error (RETRY-03)
- `CountPubkeys` — break mainLoop on error
- `CountStalePubkeys` — break mainLoop on error
- `MarkAttempted` — warn + continue on error (D-09 best-effort)

**OBS-01 metrics line** added after `Batch complete:` log:
```
Avg Dgraph call duration (cumulative): GetStalePubkeys=%v CountPubkeys=%v CountStalePubkeys=%v MarkAttempted=%v
```

### `cmd/crawler/main_test.go` — created

Four deterministic unit tests (package main, no real sleeps, complete in < 1s):

| Test | Verifies |
|------|---------|
| `TestRetryDgraph_BackoffSequence` | 5 transient errors then success; delays = [1m,2m,4m,5m,5m] (BACKOFF-01/02, RETRY-01) |
| `TestRetryDgraph_CtxCancelMidBackoff` | Pre-cancelled ctx + never-firing sleep; returns ctx.Err() not block (SHUTDOWN-01) |
| `TestRetryDgraph_FatalPassthrough` | codes.Unauthenticated; 0 delays, 1 call, error returned (RETRY-03) |
| `TestRetryDgraph_TransientThenSuccess` | 1 transient then success; metrics.avg("X") > 0, 1 delay (OBS-01/D-07) |

## Verification Results

```
go build ./cmd/crawler/     → OK
go vet ./cmd/crawler/       → OK
go test ./cmd/crawler/ -run TestRetryDgraph -count=1 → PASS (0.41s)
go test ./... -short        → all packages PASS
dgraphRetryAttempts references in source → 0
retryDgraph(ctx, call sites → 4
isDgraphTransient unchanged → verified
```

## Deviations from Plan

None — plan executed exactly as written. All four acceptance criteria for Task 1 and all four for Task 2 verified.

## Success Criteria Status

| SC | Description | Status |
|----|-------------|--------|
| SC#1 | Indefinite transient retry — no attempt cap | PASS (no dgraphRetryAttempts; loop has no cap) |
| SC#2 | Backoff 1m→2m→4m→5m observable in console | PASS (log retains "retrying in %v"; TestRetryDgraph_BackoffSequence asserts [1m,2m,4m,5m,5m]) |
| SC#3 | Fatal code exits loudly, unchanged from v1.2 | PASS (helper returns to caller; read calls break mainLoop; TestRetryDgraph_FatalPassthrough) |
| SC#4 | Ctrl-C mid-backoff exits within seconds | PASS (select on ctx.Done(); TestRetryDgraph_CtxCancelMidBackoff) |
| SC#5 | Cumulative avg-duration line per call type | PASS (OBS-01 log after Batch complete:; TestRetryDgraph_TransientThenSuccess metrics check) |

## Known Stubs

None.

## Threat Flags

None — no new network endpoints, auth paths, file access patterns, or schema changes introduced. The `retryDgraph` helper only classifies existing gRPC status codes (per the registered T-10-01/02/03 mitigations in the plan's threat model, all mitigated inline).

## Self-Check: PASSED

- `cmd/crawler/main.go` — exists and builds
- `cmd/crawler/main_test.go` — exists, all 4 tests pass
- Commit 94380be (Task 1 refactor) — present in git log
- Commit 87d5803 (Task 2 tests) — present in git log
