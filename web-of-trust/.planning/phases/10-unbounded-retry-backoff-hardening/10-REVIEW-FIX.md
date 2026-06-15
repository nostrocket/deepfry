---
phase: 10-unbounded-retry-backoff-hardening
fixed_at: 2026-06-15T13:40:00Z
review_path: .planning/phases/10-unbounded-retry-backoff-hardening/10-REVIEW.md
iteration: 2
findings_in_scope: 1
fixed: 1
skipped: 0
status: all_fixed
---

# Phase 10: Code Review Fix Report

**Fixed at:** 2026-06-15T13:40:00Z
**Source review:** .planning/phases/10-unbounded-retry-backoff-hardening/10-REVIEW.md
**Iteration:** 2

**Summary:**
- Findings in scope: 1 (WR-04 — critical_warning scope; 3 Info findings IN-01..IN-03 excluded)
- Fixed: 1
- Skipped: 0

The single in-scope warning was fixed. The flaky test is now deterministic and the
full short test suite is green.

## Fixed Issues

### WR-04: `TestRetryDgraph_TransientThenSuccess` is flaky — fails ~34% of runs, breaking the `-short` test gate

**Files modified:** `cmd/crawler/main_test.go`
**Commit:** eabda69
**Applied fix:** Replaced the non-deterministic `m.avg("X") > 0` assertion with
`m.count["X"] != 1`. The success duration recorded by `retryDgraph` is
`time.Since(start)` over a no-op `fn`, which truncates to `0ns` on a fast machine,
so `avg("X")` returned `0` and the strict-positive assertion failed ~17/50 runs.
Confirmed against the `callMetrics` type in `main.go` (`sum map[string]time.Duration`,
`count map[string]int`): `record` increments `m.count[callName]` on every successful
call, so asserting `m.count["X"] == 1` preserves the test's intent — one transient
failure then success records exactly one successful call — without depending on
wall-clock nanoseconds. The other assertions (`err == nil`, `got == 99`,
`len(slept) == 1`) were left unchanged; no assertion was weakened. The function doc
comment was updated to explain the rationale.

**Verification:**
- `go vet ./cmd/crawler/` — pass (exit 0)
- `go test ./cmd/crawler/ -run TestRetryDgraph_TransientThenSuccess -count=50` — 50/50 pass (exit 0)
- `go test ./... -short` — pass (exit 0)

## Skipped Issues

None — the single in-scope finding was fixed.

The 3 Info findings (IN-01 time.After timer stop, IN-02 unbounded metric sum,
IN-03 nil metrics guard) were out of scope (critical_warning) and not addressed.

---

_Fixed: 2026-06-15T13:40:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 2_
