---
phase: 08-frontier-prioritization-timeout-observability
reviewed: 2026-06-13T00:00:00Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - pkg/dgraph/backoff.go
  - pkg/dgraph/backoff_test.go
  - pkg/dgraph/dgraph.go
  - pkg/dgraph/dgraph_stale_test.go
  - pkg/dgraph/dgraph_validation_test.go
  - pkg/config/config.go
  - pkg/crawler/crawler.go
  - pkg/crawler/crawler_quorum_test.go
  - cmd/crawler/main.go
findings:
  critical: 1
  warning: 6
  info: 4
  total: 11
status: issues_found
resolution:
  resolved_in_phase: [CR-01, WR-01, WR-06]
  deferred_to_followup: [WR-02, WR-03, WR-04, WR-05, IN-01, IN-02, IN-03, IN-04]
  followup_todo: .planning/todos/pending/phase08-code-review-hardening-followups.md
  updated: 2026-06-13
---

> **Resolution (2026-06-13, during phase execution):**
> - **CR-01** FIXED — `fix(08): correct BackoffInterval premature cap on non-aligned configs`; overflow guard now compares the running product against cap (`result > (cap-1)/ratio`), with non-power-aligned regression tests.
> - **WR-01** FIXED — `staleRemaining` clamped with `max(0, totalStale-len(pubkeys))`.
> - **WR-06** FIXED — corrected the post-cancel-drain comment to document the intentional lossy 70%-quorum trade-off.
> - **WR-02, WR-03, WR-04, WR-05, IN-01..04** DEFERRED to a gap-closure pass (they touch live-verified runtime / the VALID-03 block and need live re-verification). Tracked in `.planning/todos/pending/phase08-code-review-hardening-followups.md`. WR-05 was verified live via the D-09 checkpoint; only its test coverage is outstanding.

# Phase 8: Code Review Report

**Reviewed:** 2026-06-13
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

Phase 8 adds frontier-prioritized stale selection, hit/miss exponential backoff
(`next_attempt`/`miss_count`), a `CountStalePubkeys` honest-remaining metric, a
15s timeout, and an EOSE-quorum early-exit. The concurrency design around the
quorum counter is sound (function-local `atomic.Int32`, idempotent `cancel()`,
single-threaded `markRelayDead` dispatcher) — no new data race was found.

However the core backoff math helper has a **provable correctness bug**: the
overflow guard caps prematurely for non-power-aligned `cap/base` ratios,
producing a longer-than-specified backoff. The unit tests pass only because they
exercise ratios where `cap/base` happens to align with `ratio^n`. Additional
warnings cover an off-by-the-metric `staleRemaining` calculation, a non-atomic
`MarkAttempted` multi-transaction sequence, and several robustness gaps.

## Critical Issues

### CR-01: BackoffInterval overflow guard caps prematurely (incorrect backoff schedule)

**File:** `pkg/dgraph/backoff.go:59-66`
**Issue:** The overflow guard uses `threshold := int64(cap / base)` (integer
truncation) and returns `cap` as soon as `power >= threshold`. When `cap/base`
is not an exact power of `ratio`, the truncated threshold fires one step too
early and the function returns `cap` even though `base * ratio^missCount` is
still strictly below `cap`.

Reproduced directly:
- `BackoffInterval(1, base=2h, ratio=2, cap=5h)` returns `5h`; correct value is
  `2h * 2 = 4h` (threshold = `int64(5h/2h)` = `2`, and `power=2 >= 2` fires).
- `BackoffInterval(2, base=3h, ratio=2, cap=13h)` returns `13h`; correct value
  is `3h * 4 = 12h` (threshold = `int64(13h/3h)` = `4`, and `power=4 >= 4`
  fires).

The locked default schedule (`base=2h, cap=168h`, threshold=84) avoids the bug
by luck, which is exactly why `TestBackoffInterval` and `TestBackoffIntervalRatio3`
both pass — every tabulated case sits on a power-aligned boundary. Any operator
who tunes `miss_backoff.base`/`cap` to non-aligned values silently gets a
backoff longer than configured, delaying re-crawl of recoverable pubkeys.

**Fix:** Guard against genuine `int64` overflow without truncation-based early
capping. Compute the next `power` and compare the actual interval against `cap`,
breaking only when the multiplication would overflow or the result meets/exceeds
`cap`:
```go
func BackoffInterval(missCount int, base time.Duration, ratio int, cap time.Duration) time.Duration {
	if missCount <= 0 || ratio <= 1 {
		return min64(base, cap)
	}
	result := base
	for i := 0; i < missCount; i++ {
		// Overflow / cap guard BEFORE multiplying.
		if result >= cap/time.Duration(ratio) {
			return cap
		}
		result *= time.Duration(ratio)
	}
	return min64(result, cap)
}
```
This returns `cap` only when the next step would actually meet or exceed it, and
the `result >= cap/ratio` check prevents `int64` overflow in the multiply.
Add table cases with non-power-aligned cap/base (e.g. `base=3h, cap=13h`) so the
regression is locked.

## Warnings

### WR-01: staleRemaining metric subtracts batch size from a freshly-recounted total

**File:** `cmd/crawler/main.go:140,180`
**Issue:** `totalStale` is computed by `CountStalePubkeys` at the top of the loop
(line 140), then `staleRemaining := totalStale - len(pubkeys)` (line 180). But
`CountStalePubkeys` is re-run every iteration and counts frontier + aged-eligible
*after* the previous batch was stamped. The phase comment claims this fixes the
"always-zero (totalStale - batch)" problem, yet the subtraction reintroduces a
skew: the batch's pubkeys were already excluded from the *next* loop's count via
`MarkAttempted`, so within the same iteration subtracting `len(pubkeys)` from a
count that still includes them under-reports by up to `len(pubkeys)`, and can go
negative on the final batch (e.g. `totalStale=50`, `len(pubkeys)=100` when frontier
shrinks between count and select). The logged number is misleading and may print
a negative "stale remaining".
**Fix:** Log `totalStale` directly as the honest remaining count (it is already
recomputed each loop), or clamp: `staleRemaining := max(0, totalStale - len(pubkeys))`.
Prefer logging `totalStale` and dropping the subtraction, matching the stated
METRIC-01 intent.

### WR-02: MarkAttempted performs many separate CommitNow transactions — not atomic, partial failures leave inconsistent state

**File:** `pkg/dgraph/dgraph.go:666-696,733-738`
**Issue:** `MarkAttempted` issues independent `CommitNow: true` mutations for each
recovery/purge action (lines 671, 656, 691) and a final separate stamp mutation
(line 735). A failure midway (e.g. context deadline, transient Dgraph error)
commits some recoveries/purges but not the backoff stamp, or stamps some nodes
and not others on retry. There is no rollback. For chronic-miss aging this means
a pubkey can be recovered but never stamped (re-enters frontier — acceptable) or,
worse, the whole batch's `last_attempt`/`next_attempt`/`miss_count` stamp can be
lost while recoveries persisted, so those pubkeys are retried every loop
(defeating PERF-02). Each recovery also opens a `txn` that is only `Discard`ed on
the error branch (line 672) — on the success branch (line 671 `CommitNow`) the
txn is committed but never explicitly discarded, relying on GC.
**Fix:** Batch the stamp mutation into a single transaction (already done) and
ensure recovery/purge txns are always `defer txn.Discard(ctx)`. Consider folding
the stamp writes into the same transaction as much as possible, or at minimum
document that recovery and stamping are independent and stamp failure is
non-fatal/retry-safe. At a minimum, add `defer txn.Discard(ctx)` to the in-place
recovery txn at line 666.

### WR-03: BackfillNextAttempt loads entire candidate set into memory and writes one unbounded mutation

**File:** `pkg/dgraph/dgraph.go:814-861`
**Issue:** `BackfillNextAttempt` queries every node with `last_attempt` and no
`next_attempt` in a single query, then builds one `strings.Builder` of nquads and
issues one mutation. On the production graph (hundreds of thousands of attempted
nodes per CLAUDE.md), this query response and the resulting mutation can exceed
the gRPC message cap — the same failure mode the codebase carefully chunks around
in `AddFollowers` (batchSize=200) and raises `MaxCallRecvMsgSize` for in
`NewClient`. Since this runs once at startup (`New` → `BackfillNextAttempt`), a
`ResourceExhausted` failure is caught as non-fatal (crawler.go:140) but then the
backfill never completes and those legacy nodes are never aged — silently
defeating D-06.
**Fix:** Paginate the query (`first:`/`offset:` like `GetAllPubkeysPaginated`) and
commit the stamp mutation in `batchSize` windows, mirroring the existing
chunking discipline.

### WR-04: forwardEvent uses the relay-query context's parent but is called during event drain after timeout

**File:** `pkg/crawler/crawler.go:581`
**Issue:** `c.forwardEvent(relayContext, event)` is invoked inside the event-drain
loop, which by design continues draining buffered events *after* `relayQueryContext`
fired (timeout or quorum cancel). `forwardEvent` publishes using `relayContext`
(the long-lived main ctx), so it can block on a slow/dead forward relay
Publish with no per-publish timeout, stalling the single-threaded drain loop and
delaying `MarkAttempted`/next batch indefinitely. The forward relay's own health
is only updated on Publish error, not on hang.
**Fix:** Wrap the forward publish in a short bounded context
(`context.WithTimeout(relayContext, c.timeout)` or a dedicated forward timeout)
so a hung forward relay cannot stall the crawler loop.

### WR-05: GetStalePubkeys frontier phase still relies on an unbounded val()-ordered query that Dgraph caps at 1000

**File:** `pkg/dgraph/dgraph.go:527-536`
**Issue:** The function doc (lines 513-517) warns that `orderasc`/sorted queries
are capped by Dgraph at 1000 rows and that the frontier must use an explicit
`first:`. The frontier query does pass `first: %d` (good), but it is also
`orderdesc: val(fc)` — an ordered query over the entire `has(pubkey) NOT
has(last_attempt)` set computed in the `var` block. On a multi-hundred-thousand
stub graph, ordering the full frontier by computed `count(~follows)` every loop
is the exact unbounded-sort pattern the comment cautions against; if Dgraph
applies its sort row cap before `first:`, the frontier can again surface only a
capped subset, partially reintroducing the historical bug for very large
frontiers. The integration test `TestGetStalePubkeysOrder` sizes `limit` to
`countFrontier()+100`, so it never exercises the large-frontier sort-cap regime.
**Fix:** Verify on the production graph that `first:` is honored together with
`orderdesc: val(fc)` over a 100k+ frontier; if the sort cap applies, fall back to
the documented explicit-`first:` unordered selection for the frontier phase (the
comment already states this was the safe pattern), accepting that within a batch
ordering is best-effort.

### WR-06: relayQueryContext cancel from goroutines races the drain loop's reliance on buffered sends — drain can exit before slow goroutines flush events

**File:** `pkg/crawler/crawler.go:484-503,547-556`
**Issue:** When `quorumReached` fires `cancel()` from a relay goroutine, the
remaining in-flight `queryRelay` goroutines observe ctx cancellation and return
`ctx.Err()` from `drainSubscription` *before* forwarding events they had already
received from `sub.Events` but not yet pushed to `eventsChan` (the send is
guarded by `case <-ctx.Done(): return ctx.Err()` at crawler.go:648). The comment
at lines 526-529 claims the post-cancel drain is "safe because eventsChan is
buffered and goroutines send before closing" — but a goroutine that is between
receiving from `sub.Events` and selecting on the send can drop that event on
cancellation. With a 70% quorum this is by-design (trading completeness for
latency), but it is not the lossless behavior the comment asserts, and the
dropped pubkeys are still stamped as misses (their event was discarded), pushing
recoverable pubkeys into backoff.
**Fix:** Either document explicitly that quorum early-exit is lossy (events from
the slow 30% may be discarded and those pubkeys treated as misses), or have
`drainSubscription` attempt a non-blocking final send of an already-received
event before honoring cancellation. At minimum correct the misleading "safe /
lossless" comment.

## Info

### IN-01: TouchLastDBUpdate return value ignored

**File:** `pkg/crawler/crawler.go:589`
**Issue:** `c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey)` discards both
the `(bool, error)` return. A Dgraph error here is silently swallowed, so a
failed touch is invisible even in debug mode.
**Fix:** Capture and log the error (at least under `c.debug`).

### IN-02: Inconsistent transaction type for read-only counts

**File:** `pkg/dgraph/dgraph.go:872,1002`
**Issue:** `CountPubkeys` and `GetPubkeysWithMinFollowers` use `c.dg.NewTxn()` for
pure reads while the Phase 8 additions (`CountStalePubkeys`, `collectStale`,
`resolveUIDsWithMissCount`) correctly use `NewReadOnlyTxn()`. Read-only txns are
cheaper and avoid conflict bookkeeping.
**Fix:** Use `NewReadOnlyTxn()` for these read paths for consistency (low risk;
out of strict Phase 8 scope but adjacent).

### IN-03: Magic number 86400 duplicated as both a constant and the configurable HitRefreshCadence

**File:** `pkg/dgraph/dgraph.go:845`
**Issue:** `BackfillNextAttempt` hardcodes `const hitRefreshSec = 86400` (24h)
while `MarkAttempted` uses the config-driven `params.HitRefreshCadence`. The
backfill seed interval is therefore not tunable and can diverge from a configured
non-default hit cadence.
**Fix:** Accept `BackoffParams` (or just the cadence) in `BackfillNextAttempt`
and derive the seed offset from `params.HitRefreshCadence.Seconds()`.

### IN-04: quorumReached threshold uses float comparison that can misbehave at fractional edges

**File:** `pkg/crawler/crawler.go:419-420`
**Issue:** `float64(done) >= math.Ceil(float64(queried) * q)` compares an integer
count promoted to float against a ceil'd float. For the configured fractions this
is exact, but `q` values like `0.1` over large `queried` can accumulate float
error in the product before `Ceil`. The behavior is correct for the documented
0.70 default; flagged only as a robustness note.
**Fix:** Compute the threshold in integer space where possible, e.g.
`threshold := (int64(queried)*num + den - 1) / den` for a rational quorum, or
accept the float path and document the precision assumption.

---

_Reviewed: 2026-06-13_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
