---
phase: 11-relay-query-liveness
fixed_at: 2026-06-16T07:15:00Z
review_path: .planning/phases/11-relay-query-liveness/11-REVIEW.md
iteration: 3
findings_in_scope: 3
fixed: 3
skipped: 0
status: all_fixed
---

# Phase 11: Code Review Fix Report

**Fixed at:** 2026-06-16T07:15:00Z
**Source review:** .planning/phases/11-relay-query-liveness/11-REVIEW.md
**Iteration:** 3

**Summary:**
- Findings in scope: 3 (WR-01, WR-02, WR-03 — critical_warning scope; no CRITICAL findings remained)
- Fixed: 3
- Skipped: 0

All three WARNING findings were applied to `web-of-trust/pkg/crawler/crawler.go`
and committed atomically in dependency order (WR-03 → WR-02 → WR-01), each commit
left in a compiling state. The two prior CRITICAL findings (CR-01, CR-02) were
already resolved per the review and were not re-touched except where WR-01
completes the CR-02 fix.

**Verification (all run inside the isolated worktree, post-WR-01):**
- `make build` — succeeds (all five binaries).
- `go vet ./pkg/crawler/` — clean.
- `go test ./pkg/crawler/ -count=1 -race` — green.
- Three liveness tests under `-race`
  (`ReturnsWhenRelayQueryBlocks`, `PreservesHitsWhenOneRelayBlocks`,
  `ClosesAndMarksStuckRelayDead`) — all pass.
- `make test` — fully green (config, crawler, dgraph, cmd/crawler).

## Fixed Issues

### WR-03: Quorum denominator and goroutine launch set derived from two separate passes

**Files modified:** `web-of-trust/pkg/crawler/crawler.go`
**Commit:** c3a42f5
**Applied fix:** Replaced the two-pass derivation (count `rs.alive` in one loop,
then re-range `c.relays` gated on `rs.alive` to launch goroutines) with a single
captured slice. The alive set is now collected once into `launchSet`; the quorum
denominator is `queriedRelays := int32(len(launchSet))`; and goroutines are
launched exclusively by ranging `launchSet`. The denominator and the launched set
are provably the same pass, so a future edit that mutates relay state between
"count" and "launch" can no longer silently desynchronise them. This is the
smallest structural enforcement of the previously comment-only invariant.

### WR-02: Wall-clock budget gate read after the drain (mis-classifies late quorum exit as timeout)

**Files modified:** `web-of-trust/pkg/crawler/crawler.go`
**Commit:** c233ab6
**Applied fix:** Captured the budget decision the moment `relayQueryContext` fires
— BEFORE the drain phase — as
`budgetExhausted := relayQueryContext.Err() == context.DeadlineExceeded || time.Since(batchStart) >= c.timeout`.
Removed the old post-drain `budgetExhausted := time.Since(batchStart) >= c.timeout`
(which counted the drain's own dbUpdateMutex-held DB writes/forwards plus
scheduling jitter against the budget) and switched the timeout branch to gate on
the pre-drain value (`if budgetExhausted`). A healthy late quorum early-exit whose
drain straddles the deadline is no longer mis-classified as a timeout, so
slow-but-alive relays are no longer over-penalised toward ejection.

### WR-01: CR-02 cleanup goroutine accumulates parked goroutines on the quorum-early-exit path

**Files modified:** `web-of-trust/pkg/crawler/crawler.go`
**Commit:** 788ef89
**Applied fix:** Added an `else if relayQueryContext.Err() != nil` branch (the
EOSE-quorum early-exit path: context cancelled but budget NOT exhausted) that
closes the connection of any genuinely-outstanding relay
(`rs.alive && !rs.completedThisBatch.Load() && rs.conn != nil`), sets
`rs.conn = nil` and `rs.alive = false`. Closing the connection unblocks the
relay.Write parked on the *connection* context inside go-nostr's
Subscription.Fire (the per-query `relayQueryContext` cancel does not free it),
which lets the abandoned Subscribe child deliver and both it and the CR-02 cleanup
goroutine reap — eliminating the per-batch ~2-goroutine accumulation against a
wedged-but-quorum-satisfied relay.

**Chosen tradeoff (per task instruction):** On the quorum path the fix closes the
connection but does NOT call `markRelayDead` and does NOT increment any failure
counter. This intentionally avoids the WR-02 over-penalisation concern: a
quorum-cancelled relay is slow this batch, not transport-failed. The cost of
closing a relay that merely *lost the quorum race* (would have completed
microseconds later) is bounded and minor — `ReconnectRelays` brings it back next
loop with no failure penalty, and the only loss is this batch's in-flight query,
whose events were already going to be discarded by the lossy quorum drain (WR-06).
We deliberately prefer unblocking the goroutine over preserving a racing relay's
connection, because the alternative (leaving it open) is precisely the goroutine
leak this phase exists to contain. The timeout path (`budgetExhausted`) retains
its existing `markRelayDead(classTransport)` penalty.

**Note on test coverage (IN-02, out of scope):** the existing three liveness
tests exercise the timeout path, not the new quorum-cancel close branch. The
quorum branch is covered by reasoning, build, vet, and the full race suite, but
not by a dedicated unit test (IN-02 already records this gap; adding such tests
was outside the WR-01/02/03 scope of this run). The new branch only adds
in-place per-relay field mutations while ranging `c.relays` (no `markRelayDead`,
hence no slice compaction), so it carries no CR-01-style iteration hazard.

## Skipped Issues

None — all in-scope findings were fixed.

---

_Fixed: 2026-06-16T07:15:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 3_
