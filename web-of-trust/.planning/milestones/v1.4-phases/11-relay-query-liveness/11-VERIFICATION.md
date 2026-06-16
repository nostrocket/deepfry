---
phase: 11-relay-query-liveness
verified: 2026-06-16T06:47:04Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 11: Relay-Query Liveness Verification Report

**Phase Goal:** A stuck or half-open relay can never wedge FetchAndUpdateFollows — the dispatcher always returns within a bounded multiple of its relay-query timeout.
**Verified:** 2026-06-16T06:47:04Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | FetchAndUpdateFollows returns within a small bounded multiple of c.timeout even when every per-relay query goroutine blocks indefinitely and ignores its context (HANG-01) | VERIFIED | `relayQueryDoneCh` case at line 566 is an independent exit path; drainLoop drains buffered events non-blocking, then returns `pubkeysWithEvents, nil` without calling `wg.Wait()`. Proven by `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` PASS (100ms timeout, 2s budget). |
| 2 | queryRelay returns when the relay-query context expires even when relay.Subscribe / go-nostr Fire() ignores that context and blocks on the write queue (HANG-02) | VERIFIED | Lines 833-855: `relay.Subscribe` runs in a child goroutine sending to buffered `subResultCh` (cap 1). `queryRelay` selects on `subResultCh` vs `ctx.Done()`; on `ctx.Done()` first, returns `ctx.Err()` immediately without waiting for Subscribe to return. |
| 3 | When the relay-query timeout fires with a relay's query still outstanding, that relay's connection is closed and the relay is marked not-alive with a transport-class failure (HANG-03) | VERIFIED | Lines 631-638: on `relayQueryContext.Err() == context.DeadlineExceeded`, iterates `c.relays` and calls `markRelayDead(rs.url, classTransport)` for each alive relay where `completedThisBatch` is false. EOSE-quorum cancel (`context.Canceled`) does NOT enter this branch. Proven by `TestFetchAndUpdateFollows_ClosesAndMarksStuckRelayDead`: `rs.alive==false` and `rs.failTransport.Load()>=1` after return. |
| 4 | Relays that returned normally before the timeout have their hits preserved in the returned pubkeysWithEvents (HANG-01 partial path) | VERIFIED | `pubkeysWithEvents` accumulates throughout the normal event-processing loop; the drainLoop on timeout exit processes any remaining buffered events through the same signature-check/forward/update path. `completedThisBatch` is set on both success and error goroutine returns before the dispatcher reads it. Proven by `TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks`: good relay goroutine completes; dispatcher returns within budget. |
| 5 | make test (-short) is fully green with no failures or skips, and TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks passes GREEN (TEST-02) | VERIFIED | `make test` output: all packages ok, no failures, no skips. Specific run `go test ./pkg/crawler/ -run 'TestFetchAndUpdateFollows_(ReturnsWhenRelayQueryBlocks|PreservesHitsWhenOneRelayBlocks|ClosesAndMarksStuckRelayDead)' -count=1 -race -v`: all three PASS in 1.975s. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/crawler/crawler.go` | Bounded queryRelay Subscribe + dispatcher timeout-exit + conn-close-on-timeout-mark-dead | VERIFIED | Contains `FetchAndUpdateFollows` with independent timeout exit (HANG-01), `queryRelay` with child-goroutine-bounded Subscribe (HANG-02), `completedThisBatch atomic.Bool` on `relayState`, and DeadlineExceeded-gated `markRelayDead(classTransport)` loop (HANG-03). |
| `pkg/crawler/crawler_hang_test.go` | Regression gate + partial-return test + close-on-timeout unit test | VERIFIED | Contains all three required test functions: `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks`, `TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks`, `TestFetchAndUpdateFollows_ClosesAndMarksStuckRelayDead`. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `queryRelay` | `relayQueryContext.Done()` | select over Subscribe-in-child-goroutine vs ctx | WIRED | Lines 844-855: `select { case res := <-subResultCh: ... case <-ctx.Done(): return ctx.Err() }` — confirmed exact pattern. |
| `FetchAndUpdateFollows` dispatcher (relayQueryDoneCh case) | `return pubkeysWithEvents, nil` | non-blocking drainLoop of buffered eventsChan then independent return | WIRED | Lines 566-642: `case <-relayQueryDoneCh:` → drainLoop → optional mark-dead block → `return pubkeysWithEvents, nil`. No `wg.Wait()` in this path. |
| `FetchAndUpdateFollows` dispatcher (timeout path) | `markRelayDead(classTransport)` | single-threaded dispatcher closes outstanding relays on DeadlineExceeded (CR-02) | WIRED | Lines 631-638: `if relayQueryContext.Err() == context.DeadlineExceeded { for _, rs := range c.relays { if rs.alive && !rs.completedThisBatch.Load() { c.markRelayDead(rs.url, classTransport) } } }`. `markRelayDead` is called only from dispatcher (lines 637, 730) and `ReconnectRelays` (line 356) — never from per-relay goroutines. CR-02 preserved. |

### Data-Flow Trace (Level 4)

Not applicable. This phase modifies goroutine-coordination logic, not data-rendering components. No component renders dynamic data from an external source that requires a Level 4 trace.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All three phase-11 tests pass with race detection | `go test ./pkg/crawler/ -run 'TestFetchAndUpdateFollows_(ReturnsWhenRelayQueryBlocks|PreservesHitsWhenOneRelayBlocks|ClosesAndMarksStuckRelayDead)' -count=1 -race -v` | All PASS in 1.975s; no data races | PASS |
| Full unit suite green | `make test` | All packages ok, no failures, no skips | PASS |
| Build clean | `make build` | All 5 binaries compiled without errors | PASS |
| Vet clean | `go vet ./pkg/crawler/` | No output (no issues) | PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` probes exist for this phase. Acceptance gate is `make test`, which passed.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| HANG-01 | 11-01-PLAN.md | FetchAndUpdateFollows returns within bounded multiple of c.timeout; dispatcher must not gate on wg.Wait/eventsChan close | SATISFIED | Independent `relayQueryDoneCh` exit at line 566; drainLoop + return without gating on cleanup goroutine. |
| HANG-02 | 11-01-PLAN.md | queryRelay returns on ctx expiry even when relay.Subscribe/Fire() ignores ctx | SATISFIED | Child goroutine + buffered result channel (size 1) + select vs ctx.Done() at lines 833-855. |
| HANG-03 | 11-01-PLAN.md | Relay connections enforce bounded write deadline/keepalive; half-open relay can't park indefinitely — on timeout, outstanding relay closed and marked dead (classTransport) | SATISFIED | DeadlineExceeded-gated `markRelayDead(classTransport)` loop at lines 631-638; conn-close happens inside markRelayDead (line 294). |
| TEST-02 | 11-01-PLAN.md | TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks GREEN; full make test suite green | SATISFIED | All three tests PASS with -race; make test exits 0 with no failures/skips. |

No orphaned requirements: REQUIREMENTS.md maps HANG-01, HANG-02, HANG-03, TEST-02 all to Phase 11, and all four are covered by 11-01-PLAN.md. Coverage: 4/4.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | — | — | No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER markers found in either modified file. |

No debt markers. No stub patterns. No hardcoded empty returns. `return nil` in `queryRelay` at line 894 is a successful-loop-completion return, not a stub.

### Human Verification Required

None. All acceptance criteria are machine-verifiable and have been verified:

- Liveness: test suite runtime proves bounded return (100ms timeout, sub-2s actual return).
- State mutation: `alive` and `failTransport` fields are directly readable from test goroutines.
- Race safety: `-race` flag on all three tests found no data races.
- CR-02 (single-threaded markRelayDead ownership): confirmed by grep — `markRelayDead` callsites are lines 637 (dispatcher timeout path), 730 (dispatcher error path), and 356 (ReconnectRelays). Zero calls inside per-relay goroutines.

### Gaps Summary

No gaps. All five must-have truths are VERIFIED against the actual codebase with direct code and test evidence.

---

## Implementation Quality Notes

**HANG-02 correctness:** The buffered channel of size 1 (`make(chan subscribeResult, 1)`) is critical — without it, the abandoned child goroutine would block on send forever if the dispatcher has already exited the select. This is correctly implemented at line 838.

**HANG-03 precision:** The `DeadlineExceeded` vs `Canceled` distinction is correctly implemented. EOSE-quorum cancel arrives as `context.Canceled` (via `cancel()` call from goroutines) and does NOT trigger mark-dead. Only a hard deadline expiry (`context.DeadlineExceeded`, from `context.WithTimeout`) triggers the close-and-mark-dead loop. This matches the spec exactly.

**CR-02 preserved:** Per-relay goroutines (lines 513-547) set only `completedThisBatch` and send to `errorsChan`/quorum counter — they do not touch `c.relays`, `rs.conn`, or call `markRelayDead`. All relay-state mutation stays in the dispatcher and `ReconnectRelays`.

**completedThisBatch reset:** Line 487 resets all `completedThisBatch` markers before the launch loop on every `FetchAndUpdateFollows` call, preventing stale markers from a prior batch from suppressing a close.

**Commit traceability:** Three commits present in git history — 47d1dbb (HANG-02), b843635 (HANG-01/HANG-03), 17436ad (TEST-02) — matching SUMMARY claims exactly.

---

_Verified: 2026-06-16T06:47:04Z_
_Verifier: Claude (gsd-verifier)_
