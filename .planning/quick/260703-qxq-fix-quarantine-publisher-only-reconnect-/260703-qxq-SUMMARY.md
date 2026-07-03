---
phase: quick-260703-qxq
plan: 01
subsystem: whitelist-plugin
status: complete
tags: [quarantine, publisher, reconnect, go-nostr, whitelist-plugin]
requires:
  - github.com/nbd-wtf/go-nostr Relay.IsConnected
provides:
  - reconnect-only-on-unusable-connection classification in the quarantine publisher
  - Rejected metric counter for benign OK:false rejections
affects:
  - whitelist-plugin bloom + router plugins (both reuse pkg/quarantine.Publisher)
tech-stack:
  added:
    - stdlib errors (error classification)
  patterns:
    - "reconnect iff !relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)"
key-files:
  created: []
  modified:
    - whitelist-plugin/pkg/quarantine/publisher.go
    - whitelist-plugin/pkg/quarantine/publisher_test.go
decisions:
  - "Classify publish failures instead of unconditionally reconnecting; benign application-level OK:false stays on the same live connection"
  - "Split observability: rejected counter for OK:false, publishErrors reserved for transport/timeout failures"
  - "Reconnect on publish timeout (DeadlineExceeded) is retained as intended operator behavior despite possible slow-relay churn"
metrics:
  duration: ~10m
  completed: 2026-07-03
  tasks: 2
  files: 2
---

# Phase quick-260703-qxq Plan 01: Fix Quarantine Publisher Only-Reconnect Summary

Quarantine publisher now reconnects only when the connection is genuinely unusable (`!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)`); benign `OK:false` rejections increment a new `Rejected` counter and keep the same live connection, eliminating the production reconnect churn (~10/min where `reconnects ≈ publishErrors`).

## What Was Built

### Task 1 — Reconnect only on unusable connection; add Rejected counter (`9a51ead`)
- Imported stdlib `errors`.
- Added `rejected atomic.Uint64` to the `Publisher` struct (next to `publishErrors`) and `Rejected uint64` to the `Metrics` struct (next to `PublishErrors`), populated in `Metrics()` from `p.rejected.Load()`.
- Rewrote the post-`publishOne` error handling in `runDrain`:
  - Unusable connection (`!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)`) → increment `publishErrors`, log the failure, `relay.Close()` + `relay = nil` + `connected.Store(false)` (existing force-reconnect behavior preserved).
  - Otherwise (benign OK:false on a live connection) → increment `rejected`, log at a quieter "rejected" level, and do NOT touch the connection.
  - OK:true path still increments `published`.
- Added a comment documenting the accepted tradeoff (reconnect-on-timeout can add churn under load but is the intended operator decision) and that a benign rejection is exactly the complement of the reconnect condition.
- Added `rejected=%d` to the `runMetrics` periodic log line.
- Left `connect()`, `publishOne()`, backpressure/`Enqueue`, `Stop`/idempotency, backoff, and the reconnectCount increment untouched.
- Verify: `go build ./...` and `go vet ./pkg/quarantine/` clean.

### Task 2 — Extend fakeRelay + regression test (`6d13635`)
- Added `rejectReason atomic.Bool` to `fakeRelay`. When set, `handle` replies `["OK", evt.ID, false, "replaced: have newer event"]` while keeping the connection open and continuing the read loop; it does not push onto `received` (matching the existing `if ok` guard). Default OK:true behavior and the existing `rejectAll` path are unchanged.
- Added `TestPublisher_NoReconnectOnBenignRejection`: configures the relay to reply OK:false, waits for the initial connect, enqueues 5 events, asserts `Metrics().Rejected >= 5`, then asserts `ReconnectCount` did not increase past the post-initial-connect value, `PublishErrors == 0`, and `Connected == true`.
- All pre-existing publisher tests remain valid and pass.

## Verification

- `go build ./...` — clean.
- `go vet ./pkg/quarantine/` — clean.
- `go test ./pkg/quarantine/ -count=1 -run 'TestPublisher'` — **PASS** (full suite, all tests including the new regression test).
- `TestPublisher_NoReconnectOnBenignRejection` — PASS in isolation; all assertions (Rejected rises, ReconnectCount does not churn, PublishErrors stays 0, Connected stays true) hold.

## Deviations from Plan

None to the implementation. The plan was executed exactly as written for both tasks.

## Deferred Issues

**Pre-existing `-race` failures in `pkg/quarantine` (out of scope — NOT caused by this task)**

The plan's verification command `go test ./pkg/quarantine/ -count=1 -race` fails, but the failures are pre-existing and reproduce on the baseline commit (`HEAD~1`, before any change from this task). They were confirmed by checking out the baseline files and re-running under `-race`:

1. **go-nostr library internal race** — `github.com/nbd-wtf/go-nostr@v0.52.3` races between `(*Relay).ConnectWithTLS.func1()` (write at `relay.go:175`) and `(*Relay).close()` (read at `relay.go:576`) during any connect+close cycle. This fires in every test that dials a real relay (`TestPublisher_HappyPath`, `TestPublisher_ReconnectAfterRelayDrops`, and the new `TestPublisher_NoReconnectOnBenignRejection` at teardown via `defer p.Stop()`). It is a third-party library bug, not in `pkg/quarantine` code.
2. **Test-harness race in `TestPublisher_MetricsTickerAndContextCancel`** — the test shares an unsynchronized `strings.Builder` between the test goroutine (`logBuf.String()` at line 351) and the logging goroutines (`log.Logger.output` → `Builder.Write`). Also pre-existing.

Neither is introduced by the publisher classification change; both were verified present on `HEAD~1`. Fixing them would require touching the third-party dependency (#1) and rewriting an unrelated pre-existing test's logging harness (#2), which is outside this task's scope (`pkg/quarantine/` publisher.go + publisher_test.go, reconnect fix only). The functional behavior of this task is fully verified via the non-`-race` run, where the entire suite passes.

## Threat Flags

None. No new security surface introduced; only the stdlib `errors` package was added. Classification relies on connection state + `errors.Is`, not on string-matching the untrusted relay reason (per threat T-qxq-02).

## Self-Check: PASSED

- FOUND: whitelist-plugin/pkg/quarantine/publisher.go
- FOUND: whitelist-plugin/pkg/quarantine/publisher_test.go
- FOUND commit: 9a51ead (fix — classification + Rejected counter)
- FOUND commit: 6d13635 (test — regression test + fakeRelay extension)
- Classification condition present at publisher.go:198 — `!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)`
