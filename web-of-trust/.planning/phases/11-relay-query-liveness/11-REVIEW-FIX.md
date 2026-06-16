---
phase: 11-relay-query-liveness
fixed_at: 2026-06-16T07:00:00Z
review_path: .planning/phases/11-relay-query-liveness/11-REVIEW.md
iteration: 1
findings_in_scope: 7
fixed: 7
skipped: 0
status: all_fixed
---

# Phase 11: Code Review Fix Report

**Fixed at:** 2026-06-16T07:00:00Z
**Source review:** .planning/phases/11-relay-query-liveness/11-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 7 (CR-01, CR-02, WR-01, WR-02, WR-03, WR-04, WR-05)
- Fixed: 7
- Skipped: 0

All fixes verified against `make build`, `go test ./pkg/crawler/ -count=1 -race`
(full package, green), the three named liveness tests (all PASS under `-race`),
and `make test` (full suite green). The liveness guarantee and CR-02
single-threaded `markRelayDead` ownership were preserved.

## Fixed Issues

### CR-01: `c.relays` mutated via in-place compaction while being ranged in the timeout-exit path

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** add8be1 (committed jointly with WR-01 — single inseparable code block)
**Applied fix:** The timeout-exit loop no longer calls `markRelayDead` while
ranging `c.relays`. It first snapshots the URLs of outstanding relays
(`alive && !completedThisBatch`) into a local `stuck []string` slice, then
iterates that snapshot calling `markRelayDead(url, classTransport)`. This
decouples the iteration source from the in-place compaction target
(`kept := c.relays[:0]; ... c.relays = kept`), so an ejection that shifts
backing-array elements can no longer skip an outstanding relay (which would
leak its connection — the exact leak the phase prevents) or double-process one.
Requires human verification: this is a concurrency/ordering correctness fix;
syntax and the regression tests pass, but the multi-relay-with-ejection timing
it guards is deliberately not exercised by the unit tests.

### CR-02: Abandoned child Subscribe goroutine can leak a live subscription

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 50e8b34
**Applied fix:** On the `ctx.Done()` abandonment path of `queryRelay`, instead
of returning `ctx.Err()` and orphaning the still-pending `relay.Subscribe`
result, the parent now spawns a dedicated cleanup goroutine that drains the
buffered `subResultCh` and calls `sub.Unsub()` on any subscription the slow
Subscribe eventually returns. This bounds the subscription leak regardless of
which dispatcher exit branch ran (including the quorum-cancel path that never
closes the connection via `markRelayDead`).

Note: the review's suggested minimal snippet (`select { case ch <- ...: default:
s.Unsub() }`) was adapted because with an empty buffered-1 channel the send
always succeeds, so the `default` arm would never fire and the leak would
persist. A flag-based variant (`abandoned atomic.Bool` re-checked by the child)
has a TOCTOU window. The dedicated-reader approach is deterministic and
race-free — the cleanup goroutine is the sole consumer once the parent
abandons. Requires human verification: concurrency correctness fix; verified
green under `-race` but the slow-Subscribe-after-timeout timing is not directly
exercised by the unit tests.

### WR-01: `DeadlineExceeded` vs `Canceled` discrimination is racy

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** add8be1 (committed jointly with CR-01)
**Applied fix:** Added `batchStart := time.Now()` at the relay-query context
setup and changed the close-and-mark-dead gate from
`relayQueryContext.Err() == context.DeadlineExceeded` to
`relayQueryContext.Err() == context.DeadlineExceeded || time.Since(batchStart)
>= c.timeout`. When the deadline and a quorum `cancel()` fire near-
simultaneously the context may surface `context.Canceled`, but the wall-clock
budget signal still detects a genuinely-timed-out batch and marks the still-
outstanding relays dead, so a stuck relay can no longer stay `alive` and be
re-queried forever. A real quorum early-exit fires well before the budget
elapses, so it correctly does not enter the branch. Requires human
verification: logic/timing fix.

### WR-02: Non-blocking drain silently discards `TouchLastDBUpdate` errors

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 1a8731c
**Applied fix:** Captured the `(bool, error)` return of
`TouchLastDBUpdate` in both the drain loop and the pre-existing main event path
and debug-log a `WARN` on error, so a persistent Dgraph failure that keeps a
pubkey in the stale frontier is no longer silent.

### WR-03: Per-event `c.timeout` in the drain weakens the bounded-return guarantee

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** f497802
**Applied fix:** Derived a single `drainCtx` with one `c.timeout` budget for the
whole drain phase and passed it to `forwardEvent`, `updateFollowsFromEvent`, and
`TouchLastDBUpdate` instead of `relayContext`. `forwardEvent` still nests its own
`WithTimeout`, but now under the shared deadline, so total drain time is capped
at ~one `c.timeout` regardless of buffer depth. `drainCancel()` is invoked on
both the normal post-loop exit and the early-return error path (verified by
`go vet`, no lostcancel).

### WR-04: `queriedRelays` / goroutine-set coupling is fragile and undocumented

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 89eafe8 (committed jointly with WR-05 — both documentation-only,
non-overlapping hunks in the same file)
**Applied fix:** Added a comment documenting the invariant that `queriedRelays`
and the launched goroutine set are fixed for the batch duration because
`markRelayDead` runs only in the single-threaded dispatcher after dispatch. The
review judged a counter restructure unnecessary and potentially risky to the
quorum logic, so the smallest correct fix (document the invariant) was applied
rather than expanding scope.

### WR-05: Loop-variable capture relies on Go 1.22+ semantics with no guard

**Files modified:** `pkg/crawler/crawler.go`
**Commit:** 89eafe8 (committed jointly with WR-04)
**Applied fix:** Added a comment at the goroutine-launch loop noting the reset
and timeout-exit loops depend on Go 1.22+ per-iteration loop-variable semantics
(the launch loop already passes `rs` explicitly), and warning against
downgrading the toolchain `go` directive below 1.22. The review stated no
correctness change is required at the targeted Go 1.24.1.

## Skipped Issues

None. IN-01 and IN-02 (Info) were out of scope (`fix_scope: critical_warning`)
and were not attempted.

---

_Fixed: 2026-06-16T07:00:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
