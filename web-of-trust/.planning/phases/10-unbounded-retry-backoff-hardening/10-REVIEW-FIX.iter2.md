---
phase: 10-unbounded-retry-backoff-hardening
fixed_at: 2026-06-15T13:13:37Z
review_path: .planning/phases/10-unbounded-retry-backoff-hardening/10-REVIEW.md
iteration: 1
findings_in_scope: 3
fixed: 3
skipped: 0
status: all_fixed
---

# Phase 10: Code Review Fix Report

**Fixed at:** 2026-06-15T13:13:37Z
**Source review:** .planning/phases/10-unbounded-retry-backoff-hardening/10-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 3 (WR-01, WR-02, WR-03 — critical_warning scope; 4 Info findings excluded)
- Fixed: 3
- Skipped: 0

All three warnings fixed. Build, vet, and full short test suite pass from the
`web-of-trust` module dir (`go build ./cmd/crawler/`, `go vet ./cmd/crawler/`,
`go test ./... -short` — all green). The phase's verified behavior is preserved:
backoff sequence `1m→2m→4m→5m→5m` (TestRetryDgraph_BackoffSequence still passes),
indefinite transient retry for genuinely-transient codes, ctx-cancel interruption,
and OBS success-only metrics.

## Fixed Issues

### WR-01: `ResourceExhausted` classified as transient + indefinite retry = livelock

**Files modified:** `cmd/crawler/main.go`
**Commit:** 6ee7154
**Applied fix:** Dropped `codes.ResourceExhausted` from the transient set in
`isDgraphTransient`. The switch now matches only `codes.Unavailable,
codes.DeadlineExceeded`; `ResourceExhausted` and all other codes fall through the
`default` to fatal. This is exactly the reviewer's minimal option. Rationale:
`ResourceExhausted` is the code emitted when a payload exceeds the ~4MB gRPC limit
(CLAUDE.md "Large Follow-Lists" anti-pattern) — structurally fixed for a given
payload, so under indefinite retry it would hot-loop forever instead of surfacing
the error. Treating it as fatal makes the oversized request exit loudly (matching
the pre-collapse bounded behavior's failure containment). Added a doc comment
explaining the classification. No max-attempts ceiling was added since the
reviewer's primary recommendation (treat as fatal) is the cleaner fix.

### WR-02: Read-path calls log ctx-cancellation as a Dgraph error

**Files modified:** `cmd/crawler/main.go`
**Commit:** 4402b69
**Applied fix:** Added `ctx.Err()` discrimination at all three read-path call
sites (`GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`). On error, each now
checks `ctx.Err() != nil` first: if cancelled, logs a clean "Shutdown requested
during {Call}, breaking main loop" message; otherwise logs the existing
"Dgraph ... failed: %v" error line. Both paths still `break mainLoop`. This mirrors
the established pattern already used by the `FetchAndUpdateFollows` caller, so the
three new retry sites are now consistent with it. A clean SIGINT/SIGTERM no longer
logs as a Dgraph outage (SHUTDOWN-01 intent preserved).

### WR-03: Cancellation-as-transient depends on unverified gRPC code mapping

**Files modified:** `cmd/crawler/main.go`, `cmd/crawler/main_test.go`
**Commit:** 2c95490
**Applied fix:** Added a deterministic `if err := ctx.Err(); err != nil { return
zero, err }` short-circuit at the top of the `retryDgraph` loop, before calling
`fn()` or classifying its error. Cancellation now exits independently of whatever
gRPC code an interrupted in-flight call surfaces (`Unavailable`/`DeadlineExceeded`
no longer race the ready `sleepFn` channel against `ctx.Done()`). Added
`TestRetryDgraph_TransientOnCancelledCtx`: a pre-cancelled ctx with a `fn()` that
returns `codes.Unavailable`, asserting `retryDgraph` returns a non-nil error within
a bounded 2s window, does not loop, and (because the short-circuit precedes `fn()`)
invokes `fn` zero times with zero recorded delays. The existing
`TestRetryDgraph_CtxCancelMidBackoff` and `TestRetryDgraph_BackoffSequence` still
pass, confirming the backoff-sleep arm and full delay sequence are unaffected.

## Skipped Issues

None — all in-scope findings were fixed.

The 4 Info findings (IN-01 time.After timer stop, IN-02 unbounded metric sum,
IN-03 nil metrics guard, IN-04 neverSleep duration) were out of scope
(critical_warning) and not addressed.

---

_Fixed: 2026-06-15T13:13:37Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
