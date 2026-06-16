---
phase: 11-relay-query-liveness
reviewed: 2026-06-16T00:00:00Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - web-of-trust/pkg/crawler/crawler.go
  - web-of-trust/pkg/crawler/crawler_hang_test.go
findings:
  critical: 0
  warning: 1
  info: 1
  total: 2
status: issues_found
---

# Phase 11: Code Review Report

**Reviewed:** 2026-06-16
**Depth:** standard (iteration 3, final acceptance re-review)
**Files Reviewed:** 2
**Status:** issues_found (1 WARNING, 1 INFO — no BLOCKERs)

## Summary

Final re-review of the Phase 11 relay-query-liveness fixes (commits c3a42f5 WR-03,
c233ab6 WR-02, 788ef89 WR-01). The milestone goal — "a stuck/half-open relay can
never wedge the crawler AND does not leak unboundedly" — is **met**. All three
latest fixes are correct and introduce no new data race, no double-close, and no
new dispatcher-blocking path. Build is clean, `go vet` is clean, and the hang-test
suite passes under `-race` (`-count=5`).

The dispatcher's bounded-return guarantee is sound on all three exit paths:
- `relayQueryContext.Done()` always fires at the `c.timeout` deadline regardless
  of whether any per-relay goroutine ever returns (HANG-01).
- The drain loop is strictly non-blocking (`default: break drainLoop`) and the
  only blocking call on the exit path, `c.dbUpdateMutex.Lock()`, contends only
  with the same single-threaded dispatcher — never with a per-relay goroutine —
  so no goroutine can wedge it.
- `forwardEvent` / `updateFollowsFromEvent` during drain are bounded by `drainCtx`
  (one shared `c.timeout`, WR-03).

Goroutine boundedness is also met: on the quorum early-exit path WR-01 now closes
the connection of any genuinely-outstanding relay, which unblocks the parked
`relay.Write` inside go-nostr `Subscription.Fire()`, lets the abandoned Subscribe
child deliver, and reaps both that child and the CR-02 cleanup goroutine **within
the same batch**. There is therefore no per-batch accumulation on either path.

`c.relays` is mutated only by the single-threaded main loop (cmd/crawler/main.go
calls `ReconnectRelays` then `FetchAndUpdateFollows` sequentially in the same
goroutine), and `markRelayDead` runs exclusively in the dispatcher — never from a
per-relay goroutine — so there is no data race on the relay slice.

### Confirmation of the three fixes

- **WR-01 (788ef89) — CORRECT.** New `else if relayQueryContext.Err() != nil`
  branch (lines 721-766) closes `rs.conn` for `alive && !completedThisBatch && conn != nil`
  relays without `markRelayDead`/penalty. Verified:
  - **Bounds goroutines on BOTH paths.** Timeout path: `markRelayDead` closes the
    conn (line 292-294). Quorum path: this new branch closes it (line 761). Both
    unblock the wedged write; the leftover goroutine then completes its buffered
    `errorsChan` send (channel cap = `len(c.relays)`, never full on these paths,
    so no send-deadlock), `wg.Done()`s, and the closer goroutine reaps the
    channels. Bounded per batch.
  - **No double-close.** The two branches are mutually exclusive
    (`if budgetExhausted {...} else if ...`). Inside the quorum branch `rs.conn = nil`
    after `Close()` prevents re-close; `markRelayDead` is never reached on this path.
  - **No data race on `rs.conn`.** `queryRelay` reads `rs.conn` only once at entry
    (line 908) into a `relay` local; the deeper Subscribe-child / drain path uses
    that local, never re-reading `rs.conn`. So the dispatcher's `rs.conn = nil`
    write (line 762) does not race a concurrent goroutine read of that field.
    `rs.conn.Close()` on the shared `*nostr.Relay` is concurrency-safe by design.
  - **Healthy-relay risk is the documented, harmless tradeoff.** A relay that
    finished microseconds before the quorum `cancel()` but had not yet executed
    `rs.completedThisBatch.Store(true)` (line 550) can be closed. The relay that
    *triggered* quorum is safe — it stores completed=true (line 550) before calling
    `cancel()` (line 554/568). A falsely-closed racer pays no failure penalty and
    `ReconnectRelays` restores it next loop; the only loss is this batch's in-flight
    query whose events the lossy quorum drain (WR-06) would have discarded anyway.

- **WR-02 (c233ab6) — CORRECT.** `budgetExhausted` is now captured at lines 627-628,
  immediately after the relay-query context fires and **before** the drain acquires
  `dbUpdateMutex` and does DB writes. This correctly ties the timeout-vs-quorum
  decision to *why* the dispatcher woke rather than how long the drain took,
  preventing a healthy late quorum exit from being mis-classified as a timeout and
  over-penalising slow-but-alive relays via `markRelayDead(classTransport)`.

- **WR-03 (c3a42f5) — CORRECT.** The alive set is captured once into `launchSet`
  (lines 508-513), `queriedRelays` is derived from `len(launchSet)` (line 514), and
  goroutines launch exclusively over `launchSet` (line 539). The quorum denominator
  and the launched goroutine set are now structurally a single pass and cannot
  desynchronise. The per-batch reset loop (lines 487-489) runs before `launchSet`
  is captured, so `completedThisBatch` is reset for every relay that will be launched.

## Warnings

### WR-01: Cross-batch atomic logic race on `completedThisBatch` can defer one outstanding-relay close by a batch

**File:** `web-of-trust/pkg/crawler/crawler.go:550, 488`
**Issue:** On the quorum / timeout exit paths the dispatcher returns from
`FetchAndUpdateFollows` **without** `wg.Wait()`, so a batch-N per-relay goroutine
can still be unwinding when batch N+1 begins. That leftover goroutine executes
`rs.completedThisBatch.Store(true)` (line 550) on return. If this store lands
*after* batch N+1's reset loop has already executed `rs.completedThisBatch.Store(false)`
(line 488) but before batch N+1's exit-path outstanding check (lines 711 / 757),
the relay is falsely seen as "completed this batch" in batch N+1. Batch N+1 would
then skip closing/marking that relay even though *its* batch-N+1 query is genuinely
outstanding.

These are atomic operations, so this is **not** a data race (confirmed: `go test
-race -count=5` is clean — though note the injected test `queryRelayFn` never
touches `rs` fields, so the suite does not actively exercise this interleaving).
It is a *logic* race. Impact is bounded and self-healing: the worst case is that a
single stuck relay's close is deferred by one batch (it is caught the following
batch once `completedThisBatch` settles), so it cannot reintroduce the unbounded
wedge/leak this phase fixes. Because the harm is a one-batch deferral rather than a
hang or leak, this is a WARNING, not a BLOCKER.

**Fix:** Make the per-batch completion marker immune to stale cross-batch stores.
The cleanest option is a per-batch generation token instead of a boolean — the
goroutine captures the batch's generation at launch and the dispatcher treats a
relay as completed only when the stored generation matches the current batch:

```go
// relayState: replace completedThisBatch atomic.Bool with
//   completedGen atomic.Int64 // generation this relay last completed
// Crawler: add a monotonically increasing batchSeq atomic.Int64.
// FetchAndUpdateFollows: thisGen := c.batchSeq.Add(1)
//   - drop the reset loop entirely (no shared marker to reset)
//   - goroutine on return: rs.completedGen.Store(thisGen)
//   - exit-path "outstanding" test: rs.completedGen.Load() != thisGen
```

Alternatively, gate the next batch behind the prior batch's `wg` (store the
`*sync.WaitGroup` on the Crawler and `wg.Wait()` at the top of the next
`FetchAndUpdateFollows`, before the reset loop). This reintroduces a wait the
HANG-01 design deliberately removed, so the generation-token approach is preferred.

## Info

### IN-01: Hang-test seam does not cover the real `rs.conn`/`rs.alive`/atomic access surface

**File:** `web-of-trust/pkg/crawler/crawler_hang_test.go:76-92, 142-147, 195-201`
**Issue:** The injected `queryRelayFn` closures only read `rs.url` and block on a
release channel; they never read `rs.conn` or touch the per-relay atomics the real
`queryRelay` does (`rs.conn` at line 908, `rs.filterCap`, `rs.successStreak`,
`rs.probing`, `rs.completedThisBatch`). The `-race` runs therefore validate the
*dispatcher's* bounded-return and HANG-03 close-and-mark behaviour, but do not
exercise the WR-01 quorum-close interaction with a real relay-query body or the
cross-batch `completedThisBatch` interleaving in WR-01. This is a test coverage gap,
not a defect in the shipped code.
**Fix:** Add a quorum-exit-specific test (2 relays, quorum=0.5: one returns nil
immediately to trip quorum, one blocks) that asserts (a) the blocked relay's
connection was closed (use a closable sentinel / `*nostr.Relay` stub), (b)
`rs.alive == false` with `rs.failTransport == 0` (closed, not penalised), and (c)
`FetchAndUpdateFollows` returns within budget. Run it under `-race -count` across
batch boundaries to surface the WR-01 marker race.

---

_Reviewed: 2026-06-16_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
