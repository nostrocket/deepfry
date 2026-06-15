---
phase: 10-unbounded-retry-backoff-hardening
reviewed: 2026-06-15T13:15:00Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - cmd/crawler/main.go
  - cmd/crawler/main_test.go
findings:
  critical: 0
  warning: 1
  info: 3
  total: 4
status: issues_found
---

# Phase 10: Code Review Report (Re-review)

**Reviewed:** 2026-06-15T13:15:00Z
**Depth:** standard
**Files Reviewed:** 2
**Status:** issues_found

## Summary

Re-review after the three prior warnings were addressed. Verified all three fixes are correct and complete:

- **WR-01 (RESOLVED):** `codes.ResourceExhausted` is dropped from the transient set (`main.go:51-56`). Only `Unavailable` and `DeadlineExceeded` return transient; everything else (including `ResourceExhausted`) falls through to fatal. This is self-contained — `isDgraphTransient` is called only from `retryDgraph`, which is called only from the four sites in `main()`. Dropping `ResourceExhausted` breaks no earlier assumption; it aligns with the chunking comment in `pkg/dgraph/dgraph.go:38` (chunking prevents oversized payloads; fatal-on-hit is the correct safety net so a structurally-impossible request surfaces loudly instead of livelocking under indefinite retry).
- **WR-02 (RESOLVED):** All three read-path call sites (`GetStalePubkeys` 244-248, `CountPubkeys` 259-263, `CountStalePubkeys` 289-293) now discriminate `ctx.Err() != nil` → "Shutdown requested" before the failure log. They still `break mainLoop` on genuine fatal errors, so real Dgraph failures remain loud. Consistent with the pre-existing `FetchAndUpdateFollows` pattern.
- **WR-03 (RESOLVED):** The `ctx.Err()` short-circuit at the loop top (`main.go:112-114`) returns before `fn()` and before classification, making shutdown deterministic regardless of the gRPC code mapping. It interacts correctly with the backoff `select`: cancellation *before* an iteration is caught by the short-circuit; cancellation *during* backoff is caught by the `select`'s `ctx.Done()` arm. The new test `TestRetryDgraph_TransientOnCancelledCtx` asserts `fn` is not called and no delay is recorded on a pre-cancelled ctx; it passes deterministically (20/20 runs).

`go build ./cmd/crawler/` and `go vet ./cmd/crawler/` both pass clean. However, `go test ./... -short` does **not** pass reliably: a pre-existing test (`TestRetryDgraph_TransientThenSuccess`) is flaky and fails ~34% of runs (17/50 observed), breaking the test gate. This is the one remaining actionable warning — it is not in any of the three fixes but it is in a reviewed file and it fails the verification the orchestrator requested. The four prior Info items are re-confirmed as still-open minor observations (IN-01 timer-stop, IN-02 unbounded cumulative metric, IN-03 nil metrics guard) and one is dropped as resolved-context. No security issues. No blockers.

## Warnings

### WR-04: `TestRetryDgraph_TransientThenSuccess` is flaky — fails ~34% of runs, breaking the `-short` test gate

**File:** `cmd/crawler/main_test.go:181-183`
**Issue:** The test asserts `m.avg("X") > 0` after one successful call. The recorded duration is `time.Since(start)` where `start := time.Now()` is taken immediately before `fn()` (`main.go:115-118`), and `fn` in this test does nothing but increment a counter and return. On a fast machine the elapsed time truncates to `0ns`, so `avg("X")` is `0` and the assertion fails. Measured: 17 of 50 isolated runs fail; the full `go test ./... -short` suite failed on the first run of this re-review for exactly this reason. A test that fails non-deterministically against the wall clock is itself a defect — it makes CI/the verification gate unreliable and will be dismissed as "just flaky," masking real regressions. The three target fixes are correct, but the suite the orchestrator was asked to confirm green is not green.
**Fix:** Make the timing observable without depending on real elapsed nanoseconds. Either inject a non-zero duration via a fake clock, or have `fn` sleep a bounded floor, or assert on call/record counts instead of `avg > 0`. Minimal robust option — assert that a measurement was recorded rather than that it is strictly positive:
```go
m := newCallMetrics()
got, err := retryDgraph(context.Background(), "X", fn, m, fakeSleep(&slept))
// ... existing err/got/slept assertions ...
if m.count["X"] != 1 {
    t.Errorf("expected exactly 1 recorded success for \"X\", got %d", m.count["X"])
}
// avg >= 0 is guaranteed; do not assert strictly > 0 against the wall clock.
```
If positive-duration coverage is genuinely wanted, force it deterministically (e.g. wrap `fn` to advance an injected clock) rather than relying on scheduler jitter.

## Info

### IN-01: `time.After` timer is not stopped when the `ctx.Done()` arm wins (re-confirmed, still open)

**File:** `cmd/crawler/main.go:126-130` (production `sleepFn` is `time.After`, injected at the four call sites)
**Issue:** In production `sleepFn` is `time.After`, which allocates a `Timer` that is never `Stop()`-ed when the `select` exits via `ctx.Done()`. The module targets `go 1.24.1` (Go 1.23+ makes unreferenced `time.After` timers GC-eligible), so there is no practical leak today, but it is a latent footgun if the toolchain floor drops or the pattern is copied.
**Fix:** Use an explicit `time.NewTimer(d)` + `defer t.Stop()` wrapper for the production `sleepFn`, or document the Go 1.23+ dependency at the injection site. Low priority.

### IN-02: `callMetrics.sum` accumulates cumulative-since-start for the process lifetime (re-confirmed, by design)

**File:** `cmd/crawler/main.go:62-89`
**Issue:** `sum` accumulates total duration per call type for the entire (indefinite) crawler run. `time.Duration` is `int64` nanoseconds so overflow is not realistic, but the cumulative-since-start average becomes insensitive to recent latency on a long-lived process. The doc comment already states this is intentional.
**Fix:** None required; acceptable as documented. Consider a windowed/EWMA average only if recent-latency visibility becomes operationally important.

### IN-03: `metrics` parameter is dereferenced without a nil guard (re-confirmed, still open)

**File:** `cmd/crawler/main.go:118` (`metrics.record`)
**Issue:** `retryDgraph` calls `metrics.record(...)` with no nil check. All current callers pass a non-nil `newCallMetrics()`, so this never fires, but a future caller passing `nil` panics on the first successful call. Minor robustness gap on an internal helper.
**Fix:** Document that `metrics` must be non-nil, or guard with `if metrics != nil { metrics.record(...) }`.

---

_Reviewed: 2026-06-15T13:15:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
