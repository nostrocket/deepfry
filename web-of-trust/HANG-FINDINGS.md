# Crawler hang — root cause findings

**Date:** 2026-06-16
**Component:** `web-of-trust` crawler (`pkg/crawler`)
**Status:** Root cause confirmed via goroutine dump. Fix not yet applied. Regression test in place (RED until fixed).

## Symptom

The crawler stopped producing output for ~40+ minutes. Last log lines were a batch
completion followed by a reconnect sweep and a burst of `filter_rejection` "dead"
lines, then total silence:

```
17:14:05 Batch complete: queried 500 pubkeys (4 had events) | 611578 stale remaining | 615180 total in DB
17:14:29 Reconnected 3/148 relays, 0 removed, 0 still dead
17:14:29 Relay wss://staging.yabu.me/... dead (filter_rejection 1/3), retry in 30s
... (more "dead" lines) ...
<silence>
```

`ps` showed the process at 0.0% CPU — fully idle, not spinning.

## Evidence (goroutine dump)

Captured with `kill -QUIT <pid>` (only SIGINT/SIGTERM are trapped; SIGQUIT dumps all
goroutine stacks). Saved to `/tmp/dump.txt`. 210 goroutines total; the relevant ones,
all parked **48 minutes**:

| Goroutine | State | Location | Role |
|-----------|-------|----------|------|
| 1 (main) | `select` | `crawler.go:530` | Dispatcher — waiting for `eventsChan` to close |
| 6213 | `sync.WaitGroup.Wait` | `crawler.go:516` | Cleanup goroutine — closes `eventsChan` only after `wg.Wait()` |
| 6187, 6126 | `chan receive` | go-nostr `subscription.go:187` | Two query goroutines wedged in `relay.Subscribe`; never call `wg.Done()` |

Everything else (91 `[IO wait]` websocket read loops, 96 idle `[select]`) was normal.
No thread was in a `write`/`sendto` syscall — consistent with a Go-level park, not a
busy hang.

## Root cause

### 1. go-nostr's `Subscription.Fire()` ignores the caller's context

`queryRelay` calls `relay.Subscribe(relayQueryContext, …)` (`crawler.go:733`). Inside
go-nostr v0.52.0:

```go
// subscription.go:187 — Fire()
if err := <-sub.Relay.Write(reqb); err != nil {   // bare receive, no ctx select
```
```go
// relay.go:307 — Write()
ch := make(chan error)
select {
case r.writeQueue <- writeRequest{msg: msg, answer: ch}: // hand to the single writeLoop
case <-r.connectionContext.Done():                        // the relay's OWN lifetime ctx
}
return ch
```

`Fire` blocks on the write-result channel. The only things that can unblock it are
(a) the relay's single `writeLoop` draining the request, or (b) `r.connectionContext`
(the *connection's* context) being cancelled. **The `ctx` passed to `Subscribe` is never
consulted for the write.** So the crawler's `relayQueryContext` (15s timeout, config
`timeout: "15s"`) is silently ineffective for the `Subscribe` call.

### 2. Half-open TCP + single writer goroutine → permanent block

The two stuck relays were half-open TCP connections (locally `ESTABLISHED`, peer gone /
not ACKing). go-nostr uses one `writeLoop` goroutine per relay. That goroutine was parked
writing the REQ into a full TCP send buffer that never drains. Because there is only one
writer, the keepalive ping can't be written either — it's queued behind the stuck REQ — so
`connectionContext` never times out and the connection never dies. `Fire` therefore never
unblocks.

### 3. The crawler gates batch completion on *all* query goroutines finishing

`FetchAndUpdateFollows` exits its dispatcher loop only when `eventsChan` is closed
(`crawler.go:562`), and the cleanup goroutine closes `eventsChan` only after `wg.Wait()`
returns (`crawler.go:515-519`) — i.e. after *every* per-relay query goroutine has returned.

One context-ignoring `Subscribe` → one query goroutine that never calls `wg.Done()` →
`wg.Wait()` blocks forever → `eventsChan` never closes → the dispatcher blocks forever.
The 15s relay-query timeout fires and nils `relayQueryDoneCh` in the dispatcher, but that
does nothing to the stuck goroutine. **A single bad relay freezes the entire crawler.**

## Fix options

1. **Don't gate exit on `wg.Wait()` / `eventsChan` close.** Make the dispatcher return once
   `relayQueryContext` is done and buffered events are drained, abandoning stuck goroutines
   instead of waiting on them. Fully in our control; kills the symptom regardless of
   go-nostr. **Recommended first move.**
2. **Bound the `Subscribe` itself.** Run `relay.Subscribe` in a child goroutine and `select`
   on `relayQueryContext.Done()` so `queryRelay` returns on timeout even when `Fire` is
   wedged. The goroutine still leaks, but it no longer blocks the batch. Combine with #1 so
   leaks are bounded.
3. **Make dead connections die.** Set a websocket write deadline / shorten go-nostr's
   ping-timeout so a half-open peer cancels `connectionContext` and unblocks `Fire`. Reduces
   leak frequency; does not fix the structural gate — do it alongside #1.

Recommended: **#1 + #2**, with #3 as hardening.

## Regression test

`pkg/crawler/crawler_hang_test.go` →
`TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks`.

It injects a per-relay query (via the `queryRelayFn` seam on `Crawler`) that blocks
indefinitely and **ignores its context** — faithfully reproducing go-nostr's
context-ignoring `Fire()`. It asserts that `FetchAndUpdateFollows` returns within a small
multiple of its own relay-query timeout.

- **Before the fix:** the test fails (times out at its 2s budget) because the dispatcher
  waits on `wg.Wait()` forever. Confirmed RED against the current code.
- **After the fix (#1):** `FetchAndUpdateFollows` returns at ~`c.timeout` and the test passes.

The `queryRelayFn` field defaults to the real `(*Crawler).queryRelay` and is never
reassigned in production; it exists solely as a test seam (WR-05 precedent).
