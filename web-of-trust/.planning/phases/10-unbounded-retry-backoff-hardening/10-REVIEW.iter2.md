---
phase: 10-unbounded-retry-backoff-hardening
reviewed: 2026-06-15T05:09:18Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - cmd/crawler/main.go
  - cmd/crawler/main_test.go
findings:
  critical: 0
  warning: 3
  info: 4
  total: 7
status: issues_found
---

# Phase 10: Code Review Report

**Reviewed:** 2026-06-15T05:09:18Z
**Depth:** standard
**Files Reviewed:** 2
**Status:** issues_found

## Summary

Reviewed the new `retryDgraph[T]` generic helper, the `callMetrics` accumulator, the four collapsed Dgraph call sites in `main()`, the OBS-01 log line, and the four unit tests. Confirmed: backoff doubling/cap math is correct (`[1m,2m,4m,5m,5m]`), no integer overflow (delay is capped after every double so it never exceeds 8m before clamping), `callMetrics` is single-threaded as designed (no data race), and tests pass (`go test ./cmd/crawler/` → ok, `go vet` clean).

The collapse is a net quality improvement over the four hand-rolled retry blocks. However, the shift from **bounded (5 attempts)** to **indefinite** retry introduces a livelock surface that the old code did not have: any error mapped to "transient" now retries forever. The transient-classification set (`ResourceExhausted` in particular) plus the indefinite loop means a permanently-failing-but-"transient"-coded request will spin until ctx cancellation rather than exiting loudly. There is also a behavioral semantics change for the read-path calls under shutdown, and a test coverage gap on the most realistic cancellation path. No security issues. No blockers.

## Warnings

### WR-01: `ResourceExhausted` classified as transient + indefinite retry = livelock on a permanently-failing request

**File:** `cmd/crawler/main.go:46` (classifier) / `cmd/crawler/main.go:99-120` (loop)
**Issue:** `isDgraphTransient` returns `true` for `codes.ResourceExhausted`, and `retryDgraph` now retries transient errors **indefinitely** (the old code capped at `dgraphRetryAttempts = 5`). `ResourceExhausted` is the gRPC code Dgraph/grpc emits when a message exceeds the max receive/send size (the ~4MB gRPC limit called out in CLAUDE.md's "Large Follow-Lists" anti-pattern). That condition is **not** transient for a given payload — the same oversized request will fail identically on every retry. Combined with indefinite retry, a single oversized `MarkAttempted`/`GetStalePubkeys` payload produces an infinite hot loop (gated only by the 5m backoff and ctx cancellation), with no upper bound and no escape. Under the previous bounded logic the loop would have given up after 5 attempts and surfaced the error. This is a regression in failure containment.
**Fix:** Treat `ResourceExhausted` as fatal (drop it from the transient set), or add a bounded retry budget for transient errors before falling through to a fatal return. Minimal option:
```go
case codes.Unavailable, codes.DeadlineExceeded:
    return true
default: // ResourceExhausted now falls through to fatal
    return false
```
If `ResourceExhausted` must stay retryable (e.g. server-side overload throttling), add a max-attempts ceiling so a structurally-impossible request cannot loop forever.

### WR-02: Read-path calls now treat ctx-cancellation as a hard failure and log it as an error

**File:** `cmd/crawler/main.go:226-229, 236-239, 261-264`
**Issue:** When the context is cancelled mid-backoff, `retryDgraph` returns `ctx.Err()` (`context.Canceled`). The three read-path callers (`GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`) then do `log.Printf("Dgraph ... failed: %v", err); break mainLoop`. This logs a normal graceful shutdown as a Dgraph **failure** ("Dgraph getting stale pubkeys failed: context canceled"), which is misleading in operational logs and conflates clean SIGINT/SIGTERM shutdown with a real outage. The previous code path broke out of the loop via the dedicated `case <-ctx.Done(): break mainLoop` without emitting a failure line. This is an observability regression (SHUTDOWN-01 was supposed to make shutdown clean, not noisy).
**Fix:** Distinguish cancellation from failure at the call site:
```go
if err != nil {
    if ctx.Err() != nil {
        log.Println("Shutdown requested during GetStalePubkeys, breaking main loop")
    } else {
        log.Printf("Dgraph getting stale pubkeys failed: %v", err)
    }
    break mainLoop
}
```
Apply to all three read-path sites. (The `FetchAndUpdateFollows` caller at lines 271-278 already does exactly this `ctx.Err()` discrimination — the new retry sites are inconsistent with that established pattern.)

### WR-03: Cancellation-as-transient depends on unverified gRPC code mapping; no test or guard covers the realistic in-flight-cancel path

**File:** `cmd/crawler/main.go:111-115` / `cmd/crawler/main_test.go:78-88`
**Issue:** The correctness of shutdown hinges on what gRPC code an in-flight Dgraph call returns when `ctx` is cancelled. If grpc-go surfaces it as `codes.Canceled`, `isDgraphTransient` returns false and the loop exits — correct. But during a relay/connection drop at shutdown, grpc commonly surfaces `codes.Unavailable` (and a deadline-bound parent ctx surfaces `codes.DeadlineExceeded`), both of which are classified **transient** here. In that case `fn()` returns transient, the loop logs "retrying in 1m", and only the `select`'s `ctx.Done()` arm rescues it. The only test of cancellation (`TestRetryDgraph_CtxCancelMidBackoff`) drives the *backoff-sleep* arm with `neverSleep` and a pre-cancelled ctx — it never exercises the case where `fn()` itself returns a transient error while ctx is already done (where `select` picks randomly between the ready `sleepFn` channel and `ctx.Done()`). The most operationally-likely cancellation path is therefore unverified.
**Fix:** Add a test where `fn()` returns `codes.Unavailable` on a ctx that is cancelled, asserting `retryDgraph` returns a non-nil error within a bounded time and does not loop. Optionally short-circuit explicitly at loop top: `if err := ctx.Err(); err != nil { return zero, err }` before classifying, so cancellation is handled deterministically regardless of the gRPC code mapping.

## Info

### IN-01: `time.After` timer is not stopped when the `ctx.Done()` arm wins

**File:** `cmd/crawler/main.go:112-115`
**Issue:** In production `sleepFn` is `time.After`, which allocates a `Timer` that is never `Stop()`-ed when the `select` exits via `ctx.Done()`. On Go <1.23 this leaks a timer for up to `dgraphRetryMax` (5m). The module targets `go 1.24.1` (go.mod), where Go 1.23+ makes unreferenced `time.After` timers GC-eligible, so there is no practical leak today — but the code is a latent footgun if the toolchain floor ever drops or the pattern is copied elsewhere.
**Fix:** Use an explicit `time.NewTimer(d)` + `defer t.Stop()` wrapper for the production `sleepFn`, or document the Go 1.23+ dependency at the `time.After` injection site.

### IN-02: `callMetrics.sum` accumulates unbounded for the process lifetime

**File:** `cmd/crawler/main.go:56-73`
**Issue:** `sum` accumulates total duration per call type for the entire (indefinite) crawler run. `time.Duration` is `int64` nanoseconds (~292 years of headroom), so overflow is not realistic, but the cumulative-since-start average will become increasingly insensitive to recent latency on a long-lived process — a slow Dgraph period months in is invisible against the accumulated baseline. This is a metric-design observation, not a defect.
**Fix:** If recent-latency visibility matters, consider a windowed/EWMA average or periodic reset. Otherwise document that the average is lifetime-cumulative by design (the comment already says so — acceptable to leave as-is).

### IN-03: `metrics` parameter is dereferenced without a nil guard

**File:** `cmd/crawler/main.go:103` (`metrics.record`) / `cmd/crawler/main.go:90-96`
**Issue:** `retryDgraph` calls `metrics.record(...)` with no nil check. All current callers pass a non-nil `newCallMetrics()`, so this never fires, but a future caller passing `nil` would panic on the first successful call rather than degrading gracefully. Minor robustness gap on an internal helper.
**Fix:** Either document that `metrics` must be non-nil, or guard: `if metrics != nil { metrics.record(...) }`.

### IN-04: `neverSleep` test helper ignores its duration argument silently

**File:** `cmd/crawler/main_test.go:32-34`
**Issue:** `neverSleep` discards the requested duration, which is correct for its purpose (forcing the `ctx.Done()` arm) but means the cancellation test does not assert anything about *which* delay was requested before cancellation. Minor — the helper's intent is clear from the comment. No action required beyond awareness when extending cancellation tests (see WR-03).
**Fix:** None required; noted for completeness.

---

_Reviewed: 2026-06-15T05:09:18Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
