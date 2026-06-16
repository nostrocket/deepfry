---
phase: 11-relay-query-liveness
reviewed: 2026-06-16T06:50:11Z
depth: standard
files_reviewed: 2
files_reviewed_list:
  - web-of-trust/pkg/crawler/crawler.go
  - web-of-trust/pkg/crawler/crawler_hang_test.go
findings:
  critical: 2
  warning: 5
  info: 2
  total: 9
status: issues_found
---

# Phase 11: Code Review Report

**Reviewed:** 2026-06-16T06:50:11Z
**Depth:** standard
**Files Reviewed:** 2
**Status:** issues_found

## Summary

Phase 11 fixes a real, well-documented crawler hang: a stuck `relay.Subscribe`/`Fire()`
call (context-ignoring) wedges the `FetchAndUpdateFollows` dispatcher forever. The fix has
three parts: (1) a bounded child goroutine around `relay.Subscribe` in `queryRelay`, (2) an
independent timeout-exit path in the dispatcher that drains buffered events non-blocking and
returns rather than gating on `wg.Wait()`, and (3) a close-and-mark-dead pass over
outstanding relays on the `DeadlineExceeded` branch. The structural shape of the fix is sound
and the regression tests exercise the core invariant (returns within budget).

However, the timeout-exit path introduces a **slice-mutation-during-range** defect
(CR-01) that the single-relay/high-threshold tests cannot catch, and the bounded-Subscribe
goroutine has a **latent send-block leak** (CR-02) when the abandoned child's `Subscribe`
eventually succeeds but the dispatcher has already returned. Both are concurrency defects in
a concurrency fix. Several quality/robustness issues round out the findings.

The phase claim that `go test -race` passes is not evidence of correctness here: the race
detector only flags races on memory the running goroutines actually touch, and the latent
defects below require specific timing (mid-batch ejection; a stuck relay that later returns
with a full event buffer) that the unit tests deliberately avoid.

## Critical Issues

### CR-01: `c.relays` mutated via in-place compaction while being ranged in the timeout-exit path

**File:** `web-of-trust/pkg/crawler/crawler.go:631-640` (interacting with `markRelayDead` at `285-329`)

**Issue:** The timeout-exit loop ranges over `c.relays` and calls `c.markRelayDead(rs.url, classTransport)` inside the loop body:

```go
for _, rs := range c.relays {
    if rs.alive && !rs.completedThisBatch.Load() {
        c.markRelayDead(rs.url, classTransport)   // mutates c.relays
    }
}
```

`markRelayDead` reassigns `c.relays` via in-place compaction (`kept := c.relays[:0]; ...; c.relays = kept`) writing into the **same backing array** the outer `range` is iterating. The `range` captured the original slice header at loop entry, so it keeps using the old length while the backing array is being overwritten underneath it.

In the no-ejection case the indices line up (each kept element is written back to its own index), so it happens to be benign. But when **two or more** relays are outstanding at timeout and the **first** ejection actually removes an element (failTransport already at threshold), `markRelayDead` shifts all subsequent kept elements down by one in the backing array. The outer `range`, still walking by original index, then reads a shifted/duplicated entry — skipping an outstanding relay (its connection never gets closed → the exact leak this phase is trying to prevent) or double-processing one. With a single relay and `classTransport` threshold 10 (the test config), neither condition is reached, so the tests pass while the defect is latent.

**Fix:** Snapshot the URLs to act on before mutating `c.relays`, so the iteration source is decoupled from the mutation target:

```go
if relayQueryContext.Err() == context.DeadlineExceeded {
    var stuck []string
    for _, rs := range c.relays {
        if rs.alive && !rs.completedThisBatch.Load() {
            if c.debug {
                log.Printf("Relay %s timed out with outstanding query, closing and marking dead", rs.url)
            }
            stuck = append(stuck, rs.url)
        }
    }
    for _, url := range stuck {
        c.markRelayDead(url, classTransport)
    }
}
```

### CR-02: Abandoned child Subscribe goroutine can send on a closed event channel / block forever, defeating the leak bound

**File:** `web-of-trust/pkg/crawler/crawler.go:838-895` (child goroutine + `drainSubscription` send at `749-754`)

**Issue:** The HANG-02 comment claims the abandoned child goroutine's lifetime is "bounded by the dispatcher closing the connection on the timeout path (HANG-03)." That bound is incomplete:

1. The buffered-1 `subResultCh` correctly prevents the child from blocking on its *own* send. Good.
2. But on the `ctx.Done()` branch (line 849-854) `queryRelay` returns `ctx.Err()` **without unsubscribing or consuming the eventual `sub`**. If `relay.Subscribe` later returns successfully (the connection was merely slow, not dead — the timeout fired first), the returned `*nostr.Subscription` is leaked: its `Unsub()` is never called and its goroutines/channel registrations on the relay persist. The "dispatcher closes the connection" claim only holds when `markRelayDead` actually closes that relay — which only happens on the `DeadlineExceeded` branch for relays that are still `alive && !completedThisBatch`. On the **quorum-cancel** (`context.Canceled`) branch (line 631 guard is false), nothing closes the connection, so a slow-but-alive relay's child goroutine leaks a live subscription every batch. Over a long-running crawler that is an unbounded accumulation.
3. Separately, once `queryRelay` returns `ctx.Err()` at line 854, the per-relay goroutine still sets `completedThisBatch=true` and sends to `errorsChan`. That is fine. But the *child* goroutine spawned at 839 is the one that outlives the call; if `drainSubscription` were ever reached on a late path it would `eventsChan <- event` — and after the dispatcher returns, `eventsChan` is still open (it is only closed by the `wg.Wait()` closer goroutine, which never fires because stuck goroutines never call `wg.Done()`), so a late send blocks forever rather than panicking. That is a permanent goroutine leak, not a crash, but it is *not* bounded by connection close on the quorum path.

**Fix:** On the `ctx.Done()` abandonment path, drain and unsubscribe the eventual subscription in the child goroutine so it never leaks a live `Subscription`, and make the quorum-cancel path also responsible for closing connections of relays whose child goroutine is still in flight. Minimal version — have the child own cleanup:

```go
subResultCh := make(chan subscribeResult, 1)
go func() {
    s, e := relay.Subscribe(ctx, []nostr.Filter{chunkFilter})
    select {
    case subResultCh <- subscribeResult{sub: s, err: e}:
    default:
        // dispatcher already abandoned us — clean up the subscription we just got
        if s != nil {
            s.Unsub()
        }
    }
}()
```

This bounds the subscription leak regardless of which exit branch the dispatcher took, instead of relying on `markRelayDead` closing the connection (which does not run on the quorum path).

## Warnings

### WR-01: `DeadlineExceeded` vs `Canceled` discrimination is racy when the deadline and a quorum `cancel()` fire near-simultaneously

**File:** `web-of-trust/pkg/crawler/crawler.go:631`

**Issue:** The decision to mark relays dead hinges on `relayQueryContext.Err() == context.DeadlineExceeded`. The context can be cancelled by either the timeout or `cancel()` (quorum). If the deadline and a quorum-triggered `cancel()` race, `context.Context` records whichever cause won — and per the stdlib, once a context is cancelled by `cancel()`, a subsequent deadline does **not** overwrite `Err()` (first cancellation wins). So a batch that *actually* timed out, but where a final goroutine called `cancel()` microseconds earlier on the quorum path, will report `context.Canceled` and **skip** marking the genuinely-stuck relays dead. The stuck relay then stays `alive=true` and is re-queried next batch — repeating the hang-prone path indefinitely without ever accumulating failTransport. The comment asserts "quorum cancel must not mark relays dead," but the inverse failure (real timeout misclassified as quorum cancel because quorum fired last) is unhandled.

**Fix:** Do not rely on the single `Err()` value to discriminate. Track the exit cause explicitly — e.g. compare `time.Since(batchStart) >= c.timeout` as a secondary signal, or have the timeout path set a dedicated `timedOut` flag distinct from the quorum `cancel()`. At minimum, mark any `alive && !completedThisBatch` relay dead whenever the wall-clock budget was actually exhausted, regardless of which `cancel` cause won the race.

### WR-02: Non-blocking drain silently discards `TouchLastDBUpdate` errors and return value

**File:** `web-of-trust/pkg/crawler/crawler.go:617` (and pre-existing at `693`)

**Issue:** `c.dgClient.TouchLastDBUpdate(relayContext, ev.PubKey)` returns `(bool, error)` but both are discarded in the drain loop. On the main event path (line 693) the same call already discards the result, so this is a duplicated pattern — but in the drain loop it matters more: a failed `TouchLastDBUpdate` means the pubkey's `last_db_update` is not advanced, so it stays in the stale frontier and gets re-queried forever. Because the caller (`cmd/crawler/main.go`) treats the pubkey as "attempted" via `MarkAttempted` regardless, a persistent Dgraph error here produces silent staleness drift with no log.

**Fix:** Capture and at least debug-log the error:

```go
if _, err2 := c.dgClient.TouchLastDBUpdate(relayContext, ev.PubKey); err2 != nil && c.debug {
    log.Printf("WARN: TouchLastDBUpdate failed for %s: %v", ev.PubKey, err2)
}
```

Apply the same at line 693 for consistency.

### WR-03: `forwardEvent` and Dgraph writes run inside the drain under `dbUpdateMutex` using `relayContext`, not the (already-expired) query context — but `relayContext` may itself be cancelled

**File:** `web-of-trust/pkg/crawler/crawler.go:608-617`

**Issue:** The drain loop calls `c.forwardEvent(relayContext, ev)` and `c.updateFollowsFromEvent(relayContext, ev)` while holding `dbUpdateMutex`. The drain is entered specifically because `relayQueryContext` expired. If the *parent* `relayContext` (SIGINT) is what cancelled things, the dispatcher took the `relayContext.Done()` case instead — so reaching the drain means `relayContext` is likely still live. That is fine. But note `forwardEvent` itself opens `context.WithTimeout(ctx, c.timeout)` on a context whose budget has already been consumed by the just-elapsed relay-query timeout; on the timeout path you are now spending up to *another* full `c.timeout` per forwarded event, serially, under the mutex. With a full event buffer that multiplies the worst-case return time well beyond the "small multiple of c.timeout" the tests assert (they send zero events, so never exercise this). Not a hang, but the bounded-return claim is weaker than documented.

**Fix:** Use a single short shared deadline for the entire drain phase (e.g. derive one `drainCtx` with a small fixed budget and pass it to `forwardEvent`/`updateFollowsFromEvent`) instead of a fresh `c.timeout` per event.

### WR-04: `done` quorum counter and `completedThisBatch` use distinct mechanisms for the same "goroutine finished" fact, risking divergence

**File:** `web-of-trust/pkg/crawler/crawler.go:522, 526, 540`

**Issue:** A goroutine signals completion three ways that must stay consistent: `completedThisBatch.Store(true)` (line 522), then `done.Add(1)` on either the error (526) or success (540) branch. The `completedThisBatch` store happens *before* the quorum increment, so there is a window where a relay is marked complete but has not yet counted toward quorum. More importantly, the quorum denominator `queriedRelays` (line 491-496) is computed from `rs.alive` snapshotted *before* the goroutines launch, while `completedThisBatch` is reset for all relays including dead ones (line 486-488). If `markRelayDead` from a prior `errorsChan` handler flips a relay to `alive=false` mid-batch, `queriedRelays` no longer matches the live set and `quorumReached` can fire early or never. The logic mostly works because `markRelayDead` only runs in the dispatcher after the loop, but the coupling is fragile and undocumented.

**Fix:** Document the invariant that `queriedRelays` and the goroutine set are fixed for the batch duration, and consider deriving quorum completion from a single counter incremented in the `defer` rather than splitting across two branches.

### WR-05: Loop-variable capture relies on Go 1.22+ semantics with no explicit guard

**File:** `web-of-trust/pkg/crawler/crawler.go:508-546` and `486-488`, `632`

**Issue:** `for _, rs := range c.relays { ... go func(rs *relayState){...}(rs) }` passes `rs` explicitly (safe). But the reset loop (486) and the timeout loop (632) capture `rs` only via the loop body, which is fine for Go 1.22+ per-iteration semantics. The module targets Go 1.24.1 so this is correct today, but there is no build constraint or comment pinning the assumption. Given this file's heavy goroutine use, an accidental downgrade of the toolchain `go` directive below 1.22 would silently reintroduce capture bugs.

**Fix:** None required for correctness at 1.24.1; optionally add a brief comment noting the per-iteration capture dependency, or keep passing `rs` as an explicit arg everywhere goroutines are involved.

## Info

### IN-01: Misplaced doc comment — `ReturnsWhenRelayQueryBlocks` doc block sits above `PreservesHitsWhenOneRelayBlocks`

**File:** `web-of-trust/pkg/crawler/crawler_hang_test.go:31-65`

**Issue:** The long doc comment describing `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` (lines 31-52) is immediately followed, with no blank separation, by the doc comment for `TestFetchAndUpdateFollows_PreservesHitsWhenOneRelayBlocks` (53-65), and the first function it actually precedes is `PreservesHits...`. The `ReturnsWhenRelayQueryBlocks` function is defined far below at line 184 with no doc comment of its own. A reader (and `go doc`) will attribute the wrong description to each test.

**Fix:** Move the `ReturnsWhenRelayQueryBlocks` doc block down to immediately precede its function at line 184.

### IN-02: Drain-loop `continue` after dedup/signature-skip does not re-check the outer exit — only the inner `default` breaks

**File:** `web-of-trust/pkg/crawler/crawler.go:599-621`

**Issue:** Inside `drainLoop`, a `continue` on the dedup-hit (line 600) or invalid-signature (line 605) path re-enters the inner `select`, which is correct for draining, but means a buffer full of duplicate/invalid events is fully walked while holding `dbUpdateMutex`. Bounded by buffer size, so not a hang, but worth noting the mutex is held across the entire drain including skipped events.

**Fix:** No change required; the buffer is finite. Consider releasing/reacquiring the mutex per event if drain latency under the mutex becomes a concern.

---

_Reviewed: 2026-06-16T06:50:11Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
