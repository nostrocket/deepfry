---
phase: 10-unbounded-retry-backoff-hardening
reviewed: 2026-06-15T14:30:00Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - cmd/crawler/main.go
  - cmd/crawler/main_test.go
findings:
  critical: 0
  warning: 0
  info: 3
  total: 3
status: clean
---

# Phase 10: Code Review Report (Final Re-review)

**Reviewed:** 2026-06-15T14:30:00Z
**Depth:** standard
**Files Reviewed:** 2
**Status:** clean

## Summary

Final re-review after two fix iterations closed all four prior warning-level findings.
Every fix is confirmed present and correct in code, and no new defects were introduced.

Verification performed this pass:
- `go build ./cmd/crawler/` — clean
- `go vet ./cmd/crawler/` — clean
- `go test ./cmd/crawler/ -count=20` — `ok` (deterministic, no flakes across 20 runs)
- `go test ./... -short` — all packages `ok`

The previously-flaky `TestRetryDgraph_TransientThenSuccess` (WR-04) is now stable: 20
consecutive runs and the full `-short` suite passed without a single failure.

No blocker- or warning-level issues remain. Three Info-level findings are carried forward
unchanged; all three are non-reachable or performance-class and are not actionable
correctness bugs (status remains `clean`).

### Verified fixes

- **WR-01 (RESOLVED — ResourceExhausted now fatal):** `isDgraphTransient` (`main.go:43-57`)
  returns `true` only for `codes.Unavailable` and `codes.DeadlineExceeded`; `ResourceExhausted`
  and all other codes fall through `default` to `false` (fatal). This prevents an oversized-
  gRPC-message condition from livelocking under indefinite retry, and is consistent with the
  chunking anti-pattern in CLAUDE.md. `isDgraphTransient` is reachable only from `retryDgraph`,
  which is reachable only from the four sites in `main()`, so the change is self-contained.
- **WR-02 (RESOLVED — ctx.Err() discrimination at call sites):** All three read-path callers
  (`GetStalePubkeys` `main.go:244`, `CountPubkeys` `main.go:259`, `CountStalePubkeys`
  `main.go:289`) check `ctx.Err() != nil` → log "Shutdown requested" before the failure log,
  while still `break mainLoop` on a genuine fatal error. Real Dgraph failures remain loud;
  SIGINT/SIGTERM no longer logs as an outage.
- **WR-03 (RESOLVED — loop-top short-circuit):** `retryDgraph` checks `ctx.Err()` at the top
  of the loop (`main.go:112-114`) before invoking `fn()` or classifying its error, making
  shutdown exit independent of the gRPC code an interrupted in-flight call returns.
  `TestRetryDgraph_TransientOnCancelledCtx` (`main_test.go:96-131`) asserts `fn` is not called
  and no backoff delay is recorded on a pre-cancelled context; it passes 20/20.
- **WR-04 (RESOLVED — flaky timing assertion replaced):** `TestRetryDgraph_TransientThenSuccess`
  (`main_test.go:162-187`) now asserts `m.count["X"] == 1` instead of `avg("X") > 0`, removing
  the wall-clock dependence that caused the truncate-to-0ns flake. Confirmed stable across the
  20x run and the full `-short` suite.

### Correctness spot-checks (no defects found)

- **Backoff sequence:** `delay` starts at 1m, doubles after each sleep, capped post-double at
  5m → slept values `[1m, 2m, 4m, 5m, 5m]`, matching `TestRetryDgraph_BackoffSequence`. No
  off-by-one in the doubling/cap order.
- **Fatal passthrough:** `TestRetryDgraph_FatalPassthrough` confirms a fatal code returns after
  exactly 1 call with 0 recorded delays.
- **MarkAttempted best-effort path (`main.go:329-334`):** on error (including ctx-cancel) it
  logs WARN and continues rather than breaking `mainLoop`; the next iteration's
  `select { case <-ctx.Done(): }` (`main.go:228-234`) cleanly breaks on shutdown. No
  infinite-loop or stuck-write risk.
- **`avg` divide-by-zero (`main.go:83-88`):** guarded by `if c == 0 { return 0 }`.
- **`max` builtin (`main.go:338`):** valid on the Go 1.24.1+ toolchain (Go 1.21+); build
  confirms resolution.

## Info

### IN-01: `time.After` timer not stopped when the `ctx.Done()` arm wins (acceptable)

**File:** `cmd/crawler/main.go:126-130` (production `sleepFn` is `time.After`)
**Issue:** When the `select` exits via `ctx.Done()`, the `time.After(delay)` timer (up to 5m)
is left to fire on its own. The module targets Go 1.24.1, where unreferenced `time.After`
timers are GC-eligible, so there is no practical leak today; it is a shutdown-only, bounded
footgun and performance-class (out of v1 scope). Acceptable as Info.
**Fix:** If desired, use `time.NewTimer(d)` + `defer t.Stop()` for the production `sleepFn`,
or document the Go 1.23+ dependency at the injection site.

### IN-02: `callMetrics.sum` accumulates cumulative-since-start for process lifetime (by design)

**File:** `cmd/crawler/main.go:62-89`
**Issue:** `sum` accumulates total duration per call type over the entire (indefinite) run.
`time.Duration` is int64 nanoseconds, so overflow requires ~292 years and is not realistic;
the only effect is that the cumulative average becomes insensitive to recent latency on a
long-lived process. The doc comment states this is intentional. Acceptable as Info.
**Fix:** None required. Consider a windowed/EWMA average only if recent-latency visibility
becomes operationally important.

### IN-03: `metrics` parameter dereferenced without a nil guard (unreachable)

**File:** `cmd/crawler/main.go:118` (`metrics.record`)
**Issue:** `retryDgraph` calls `metrics.record(...)` with no nil check. All current callers
pass a non-nil accumulator (`main.go:222`) or `newCallMetrics()` in tests, so the nil path is
unreachable; a hypothetical future caller passing `nil` would panic on the first successful
call. Minor robustness gap on an internal helper. Acceptable as Info.
**Fix:** Document that `metrics` must be non-nil, or guard with `if metrics != nil { ... }`.

---

_Reviewed: 2026-06-15T14:30:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
