# Phase 11: Relay-Query Liveness - Context

**Gathered:** 2026-06-16
**Status:** Ready for planning

<domain>
## Phase Boundary

A stuck or half-open relay must never wedge `FetchAndUpdateFollows`. The dispatcher must always return within a small bounded multiple of its relay-query timeout (`c.timeout`), regardless of whether any per-relay query goroutine ever returns or whether go-nostr's `Subscription.Fire()` honors the relay-query context.

Scope is confined to `pkg/crawler/crawler.go` (the `FetchAndUpdateFollows` dispatcher and `queryRelay`) plus tests in `pkg/crawler/`. Root cause, evidence (SIGQUIT goroutine dump), and fix options are documented in `web-of-trust/HANG-FINDINGS.md`.

</domain>

<decisions>
## Implementation Decisions

### Dispatcher exit model (HANG-01)
- On `relayQueryContext.Done()`, treat it as an independent exit path: drain only the events already buffered in `eventsChan` (non-blocking), then return. Do NOT block the dispatcher on `wg.Wait()` / `eventsChan` close.
- Keep the existing cleanup goroutine (`wg.Wait()` → `close(eventsChan)`) for the normal all-goroutines-returned path, but the dispatcher must never depend on it to exit.
- On early (timeout) exit, return the `pubkeysWithEvents` accumulated so far with a `nil` error — a relay-query timeout is not an error (consistent with the existing `DeadlineExceeded` handling that continues rather than failing the batch).

### Bounding relay.Subscribe (HANG-02)
- `relay.Subscribe` / go-nostr `Fire()` ignores the per-call context and blocks on the relay write queue. Bound it: run `Subscribe` in a child goroutine and have `queryRelay` select on the subscribe result vs `relayQueryContext.Done()`. On ctx-done, `queryRelay` returns (ctx error) without waiting for `Subscribe`.
- The child goroutine left behind by a stuck `Subscribe` is acceptable, but its lifetime is bounded by closing the relay connection on timeout (see HANG-03) — once `connectionContext` cancels, the parked `Relay.Write` unblocks and the child returns.
- `drainSubscription` already selects on ctx and needs no change; only the `Subscribe`/`Fire` call needs wrapping.

### Bounding how long a stuck relay persists (HANG-03)
- go-nostr v0.52.0 exposes no RelayOption for a websocket write deadline or ping period, and `WriteMessage` uses the relay's own `connectionContext`. A literal write deadline is therefore infeasible without forking go-nostr. Use the requirement's "or equivalent keepalive" clause instead.
- On a relay-query timeout where a relay's query is still outstanding, close that relay's connection (`rs.conn.Close()`). This cancels its `connectionContext`, which (a) unblocks go-nostr's shared ping/write-loop goroutine and (b) unblocks our leaked `queryRelay` child goroutine so it can return.
- Mark the timed-out relay dead with a transport-class failure so the existing threshold-based ejection (RELAY-01/02) handles repeat offenders — do not immediately eject on a single timeout.
- Perform the connection close + mark-dead in the dispatcher, which is the single-threaded owner of `c.relays`/conn mutation (CR-02) — do not mutate relay state from per-relay goroutines.

### Testing & verification (TEST-02)
- Keep `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` (pkg/crawler/crawler_hang_test.go) as the acceptance gate.
- Add a "partial" test: some relays return normally while one query blocks indefinitely → `FetchAndUpdateFollows` still returns within budget AND the hits from the returning relays are preserved.
- Add a unit test asserting a timed-out/stuck relay is closed and marked not-alive (HANG-03 behavior).
- Verification gate: `make test` (`-short`) is fully green with no failures or skips in the unit suite. Integration tests (live Dgraph, `//go:build integration`) are unaffected — these fixes are pure `pkg/crawler` logic — and are not part of the gate.

### Claude's Discretion
- Exact internal structure of the timeout/exit refactor (helper functions, naming), the precise non-blocking drain loop, and the test fixture details are at Claude's discretion, provided the decisions above hold and the regression test passes.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `relayState` (crawler.go:74) already has `conn`, `alive`, per-class atomic failure counters, and `markRelayDead` (crawler.go:269) classifies + threshold-ejects via `onConnectFail`.
- The dispatcher's `select` loop (crawler.go:~529) already handles `relayQueryContext.Done()` and `relayContext.Done()` cases and already ignores "context deadline exceeded" relay errors — the exit-gating on `eventsChan` close is the only structural defect.
- `quorumReached` / EOSE-quorum early-exit (crawler.go:422) already cancels `relayQueryContext` early; the timeout path is the same `relayQueryContext.Done()` branch.
- The `queryRelayFn` test seam (crawler.go) already exists and injects a blocking, context-ignoring query — reuse it for the new tests.

### Established Patterns
- Single-threaded conn/relay-state mutation: `markRelayDead` is called only from the dispatcher and `ReconnectRelays` (CR-02). New conn-close-on-timeout must follow this.
- Typed relay errors (`transportError`/`subscriptionError`/`filterRejectionError`) drive `classifyRelayError` → `markRelayDead`.
- Bounded operations elsewhere (`forwardEvent` wraps publish in `c.timeout`, HARD-03) set the precedent for wrapping a context-ignoring call.

### Integration Points
- `FetchAndUpdateFollows` (crawler.go:432) and `queryRelay` (crawler.go:686) are the only functions changed.
- go-nostr internals confirmed: `Subscription.Fire` (subscription.go:187) blocks on `<-Relay.Write()`; `Relay.Write` (relay.go:307) only unblocks on writeQueue drain or `connectionContext.Done()`; the ping ticker and writeQueue share one goroutine (relay.go:170-200) — head-of-line block.

</code_context>

<specifics>
## Specific Ideas

- Full root-cause analysis and the three ranked fix options live in `web-of-trust/HANG-FINDINGS.md` — the planner should treat it as the spec.
- The production incident: ~48-minute hang at 0% CPU, two relays wedged in `Subscription.Fire`, confirmed via SIGQUIT goroutine dump (`/tmp/dump.txt`).

</specifics>

<deferred>
## Deferred Ideas

- Upstreaming a context-aware `Fire()` to go-nostr (out of this module's control; fixes #1/#2 make it unnecessary).
- Reworking the EOSE-quorum logic (orthogonal to the hang).
- TUNE-01 config-driven timeouts (carried-forward backlog item).

</deferred>
