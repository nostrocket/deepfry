---
phase: "06"
reviewed: 2026-06-11T00:00:00Z
depth: standard
files_reviewed: 4
files_reviewed_list:
  - pkg/config/config.go
  - pkg/crawler/crawler.go
  - cmd/crawler/main.go
  - pkg/crawler/crawler_filter_test.go
findings:
  critical: 1
  warning: 3
  info: 3
  total: 7
status: issues_found
---

# Phase 06: Code Review Report

**Reviewed:** 2026-06-11
**Depth:** standard
**Files Reviewed:** 4
**Status:** issues_found

## Summary

Phase 06 introduced `relay_filter_batch_size` config, per-relay `filterCap` state, a NOTICE-based cap-halving handler, and a chunked `queryRelay` loop. The core mechanics are sound but several correctness and concurrency defects were found. One is a data race that will corrupt `filterCap` under the Go race detector and cause undefined behaviour in production. Three warnings cover a real logic error in a log/status message and two behavioural gaps. Three info items flag minor quality issues.

---

## Critical Issues

### CR-01: Data race on `relayState.filterCap`

**File:** `pkg/crawler/crawler.go:46,91,225,498,524,666-667`

**Issue:** `filterCap` is a plain `int` field on `relayState`. It is read and written from at least two goroutines without synchronization:

1. The NOTICE handler closure passed to `nostr.WithNoticeHandler` is invoked on go-nostr's internal reader goroutine.
2. `queryRelay` reads and writes `rs.filterCap` (lines 498, 523-524) while running in a goroutine launched by `FetchAndUpdateFollows` (line 314).

Both paths operate on the same `*relayState` pointer simultaneously. This is a data race. Under `-race` the program will flag it; in production it can silently corrupt the cap value or produce torn reads.

`failures` on the same struct is correctly protected via `atomic.Int32`; `filterCap` must receive equivalent treatment.

**Fix:** Replace `filterCap int` with `atomic.Int32` (or `atomic.Int64`) and use `.Load()` / `.Store()` / `.CompareAndSwap()` at every access site. Example:

```go
// In relayState struct:
filterCap atomic.Int32

// Initialize:
rs := &relayState{url: url, backoff: initialBackoff}
rs.filterCap.Store(int32(cfg.FilterBatchSize))

// Read in queryRelay:
cap := int(rs.filterCap.Load())
if cap <= 0 {
    cap = 10
}

// Write in handleFilterNotice:
func handleFilterNotice(rs *relayState, notice string, minCap int) {
    lower := strings.ToLower(notice)
    if strings.Contains(lower, "filter") && strings.Contains(lower, "too large") {
        for {
            old := rs.filterCap.Load()
            if old <= int32(minCap) {
                log.Printf("Relay %s NOTICE filter-too-large: cap already at floor %d", rs.url, minCap)
                return
            }
            newVal := old / 2
            if newVal < int32(minCap) {
                newVal = int32(minCap)
            }
            if rs.filterCap.CompareAndSwap(old, newVal) {
                log.Printf("Relay %s NOTICE filter-too-large: halved cap to %d", rs.url, newVal)
                return
            }
        }
    }
}
```

---

## Warnings

### WR-01: `staleRemaining` is always 0

**File:** `cmd/crawler/main.go:135,161`

**Issue:** `totalStale` is assigned `len(pubkeys)` at line 135. By line 161 the map has not changed size, so `staleRemaining := totalStale - len(pubkeys)` is always `0`. The progress log line therefore always reports `0 stale remaining`, which is misleading and hides whether the frontier is shrinking.

The intent appears to be to report how many stale pubkeys were not yet processed in this batch — information that is knowable only if the total stale count from the DB query is also retained separately from the batch size.

**Fix:** Keep the total stale count separate from the batch length. `GetStalePubkeys` is already capped to `cfg.RelayFilterBatchSize`, so the actual remaining count requires a separate count query or a design decision to report "batch size" rather than "remaining". The simplest honest fix is to drop the misleading field from the log line until it is computed correctly:

```go
log.Printf("Batch complete: queried %d pubkeys (%d had events) | %d total in DB",
    len(pubkeys), hadEvents, totalPubkeys)
```

---

### WR-02: Busy-spin when relay query context times out

**File:** `pkg/crawler/crawler.go:339-352`

**Issue:** When `relayQueryContext` expires with `context.DeadlineExceeded`, the `select` case at line 339 does not `return` or `break` out of the loop — it falls through and loops again. Because a cancelled context's `Done()` channel remains permanently closed, every subsequent iteration of the `for { select { ... } }` loop will have both `relayQueryContext.Done()` and `eventsChan` as selectable cases. Go's `select` is non-deterministic across ready cases. The result is a busy-spin between the timeout case (which does nothing) and event processing, continuing until `eventsChan` is closed by the goroutine watcher.

In practice this means: after timeout, the goroutines draining relays are cancelled (they check `ctx.Done()`), stop sending events, and close the channel — so the busy-spin terminates quickly. But the loop burns CPU unnecessarily, and the invariant "context cancelled means we stop immediately" is violated for the DeadlineExceeded path. If goroutines somehow keep sending events past the timeout (e.g., via buffered channel), processing continues past the intended deadline.

**Fix:** Break out of the select and drain `eventsChan` explicitly, or use a flag to stop re-entering the deadline case:

```go
case <-relayQueryContext.Done():
    if relayQueryContext.Err() != context.DeadlineExceeded {
        return len(pubkeysWithEvents), relayQueryContext.Err()
    }
    // Timeout: drain any already-buffered events then exit.
    for {
        select {
        case event, ok := <-eventsChan:
            if !ok {
                return len(pubkeysWithEvents), nil
            }
            // ... process event (or accept partial results)
        default:
            return len(pubkeysWithEvents), nil
        }
    }
```

---

### WR-03: `filterCap` not reset when relay reconnects

**File:** `pkg/crawler/crawler.go:235-239`

**Issue:** In `ReconnectRelays`, after a successful reconnect, `backoff` and `failures` are reset but `filterCap` is not:

```go
rs.conn = relay
rs.alive = true
rs.backoff = initialBackoff
rs.failures.Store(0)
// filterCap is left at whatever degraded value it reached
```

A relay that was progressively degraded to cap=10 due to "filter too large" NOTICEs or connection-drop attribution will remain at cap=10 after reconnect, even though the new connection is fresh and may accept larger filters. The relay is therefore permanently capped at the degraded floor until the process restarts.

**Fix:** Reset `filterCap` to `c.filterBatchSize` on reconnect:

```go
rs.conn = relay
rs.alive = true
rs.backoff = initialBackoff
rs.failures.Store(0)
rs.filterCap.Store(int32(c.filterBatchSize)) // reset after fix for CR-01
```

---

## Info

### IN-01: `handleFilterNotice` logs "halved cap" even when cap is already at floor

**File:** `pkg/crawler/crawler.go:665-669`

**Issue:** The `log.Printf` call is inside the outer `if` block (notice matched) but outside the inner `if rs.filterCap > minCap` block. When `filterCap == minCap` (already at floor), the function logs `"halved cap to 10"` despite performing no halving. The message is factually incorrect.

```go
if strings.Contains(lower, "filter") && strings.Contains(lower, "too large") {
    if rs.filterCap > minCap {
        rs.filterCap = max(rs.filterCap/2, minCap)
    }
    log.Printf("Relay %s NOTICE filter-too-large: halved cap to %d", ...)  // fires even at floor
}
```

**Fix:** Move the log inside the inner `if`, or add an `else` branch with a different message:

```go
if rs.filterCap > minCap {
    rs.filterCap = max(rs.filterCap/2, minCap)
    log.Printf("Relay %s NOTICE filter-too-large: halved cap to %d", rs.url, rs.filterCap)
} else {
    log.Printf("Relay %s NOTICE filter-too-large: cap already at floor %d", rs.url, rs.filterCap)
}
```

---

### IN-02: `fmt.Println` used for debug output instead of `log.Printf`

**File:** `pkg/crawler/crawler.go:401`

**Issue:** One debug log statement uses `fmt.Println` while the entire codebase uses `log.Printf`. This is inconsistent and bypasses the log infrastructure (no timestamp prefix, no log level):

```go
fmt.Println("already have newer event for " + event.PubKey)
```

**Fix:**

```go
log.Printf("DEBUG: already have newer event for %s", event.PubKey)
```

---

### IN-03: `cap` variable shadows built-in

**File:** `pkg/crawler/crawler.go:498`

**Issue:** The local variable `cap := rs.filterCap` shadows the built-in `cap()` function. This is not a bug at this call site (the built-in is not used nearby), but it is flagged by `golangci-lint` (predeclared-ident shadow rule) and can cause confusion when reading or extending the function.

**Fix:** Rename to `filterCap` or `batchCap`:

```go
batchCap := rs.filterCap
if batchCap <= 0 {
    batchCap = 10
}
chunk := authors
if len(authors) > batchCap {
    chunk = authors[:batchCap]
}
```

---

_Reviewed: 2026-06-11_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
