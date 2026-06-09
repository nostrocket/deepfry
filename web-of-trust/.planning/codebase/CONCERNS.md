# Codebase Concerns

**Analysis Date:** 2026-06-09

## Tech Debt

### Critical: Stubborn Stubs — The 8% Crawling Bug

**Issue:** The crawler only ever crawls ~8% of known pubkeys because `GetStalePubkeys` starves stub nodes (nodes created only as followees but never had their own kind-3 ingested).

**Files:** `pkg/dgraph/dgraph.go:430-468`, `cmd/crawler/main.go:108-146`

**Impact:** Over 173,975 pubkeys (92% of the graph) are stuck as stubs. The web of trust cannot grow past the initial seed neighborhood. The crawler re-crawls the same ~15k already-crawled accounts forever, wasting bandwidth and CPU.

**Root cause:** Two defects combine:
1. `GetStalePubkeys` uses `orderasc: last_db_update`. Stubs have **no** `last_db_update` predicate, so Dgraph sorts them **last**. All crawled accounts come before any stub.
2. No explicit `first:` limit. Dgraph caps the sorted result at its default 1000 rows. The query returns 1000 already-crawled accounts and never a single stub.

**Secondary defect — no "attempted" marker:** When a relay returns nothing for a pubkey, that pubkey is left untouched (no timestamp). Even after the primary fix, any pubkey whose kind-3 is not retrievable (~15% of stubs, plus invalid pubkeys) will never get a timestamp, stay in the "never attempted" set, and be re-selected every cycle.

**Fix approach:** See `8pc_crawled.md` in the repository root (fully specified fix with 5 coordinated changes):
- Add `last_attempt` predicate to schema to distinguish "tried, nothing there" from "never tried"
- Rewrite `GetStalePubkeys` with Phase 1 selecting never-attempted nodes first (explicit `first:`, no `orderasc`), Phase 2 topping up with aged previously-attempted nodes
- Call new `MarkAttempted` helper to stamp every queried pubkey, even if no event came back
- Update crawler main loop to pass batch size to `GetStalePubkeys` and call `MarkAttempted` after fetch
- One-time backfill to initialize `last_attempt` from `last_db_update` for existing crawled nodes

**Priority:** CRITICAL — This is the root cause of graph stagnation.

---

## Known Bugs

### Relay Connection Stalls Under Timeout Conditions

**Symptoms:** When `FetchAndUpdateFollows` hits the relay timeout (default 30s from `config:Timeout`), the `relayQueryContext` deadline is exceeded but goroutines handling individual relay subscriptions may still hold open WebSocket connections until the main context is cancelled.

**Files:** `pkg/crawler/crawler.go:287-432`, `pkg/crawler/crawler.go:434-495`

**Trigger:** Run crawler with many relays (136 configured) and a large batch of pubkeys (500 per cycle). Some relays are slow to respond to the kind-3 filter. At 30s timeout, the `relayQueryContext` (line 293) times out, triggering `context.DeadlineExceeded` in the event loop (line 333-340). The code logs and continues processing received events, but `queryRelay` goroutines may still be blocked in `<-sub.Events` if they haven't received EOSE or an error.

**Workaround:** The `defer sub.Unsub()` (line 454) will clean up the subscription when the goroutine exits, but only **when the relay or context actually signals**. If a relay is unresponsive, the goroutine may block indefinitely waiting for `sub.Events` or `sub.EndOfStoredEvents`, even after the main context exits.

**Why it matters:** Under the observed git log message "problem: too many open sockets" (commit 3e77e90), this pattern can exhaust file descriptor limits. The symptom was confirmed in quarantine-rescuer (related service), where idle subscriptions accumulated.

---

### Large Follow Lists Cause Dgraph Timeouts

**Symptoms:** When a pubkey has > 10,000 follows (e.g. large media accounts), `AddFollowers` times out even with chunk processing.

**Files:** `pkg/crawler/crawler.go:527-539`, `pkg/crawler/chunks.go:14-81`

**Details:** For follow lists > 10,000 follows, the code chunks them into 200-follow batches (line 20 in chunks.go) and processes sequentially. However:
- Each chunk gets its own `context.WithTimeout(ctx, c.timeout)` (chunks.go:39), inheriting the remaining context timeout from the relay query phase
- If the relay query timeout already consumed 20s, a chunk may have only 10s left
- A 200-follow bulk query + mutation against Dgraph can take 5-30s depending on DB load
- The chunk fails, and the entire follow list update fails

**Fix approach:** Give chunked operations their own independent timeout (not derived from relay context), or implement exponential backoff with retry for Dgraph timeouts specifically.

---

### Event Forwarding Loses Errors Silently

**Symptoms:** If the forward relay is down, events fail to forward, but the error is logged and swallowed (not propagated).

**Files:** `pkg/crawler/crawler.go:148-168`

**Details:** `forwardEvent` (line 148) swallows errors. It logs a warning (line 154) and marks the relay dead (lines 156-163), but the caller (`updateFollowsFromEvent`, line 383) doesn't know the event failed to forward. This can silently break assumptions if the caller expects all "processed" events to have been forwarded.

**Impact:** Low for correctness (local Dgraph is still updated), but if forward relay is critical (e.g. for propagation to StrFry), events disappear from the relay without audit trail.

---

### No Connection Pooling for Dgraph

**Symptoms:** The crawler creates two separate Dgraph gRPC connections: one in `New()` (`pkg/crawler/crawler.go:69`) and another in `cmd/crawler/main.go:48`. Each time `GetStalePubkeys` or `AddFollowers` is called, a new transaction and read-only transaction is created. On heavy load, this can cause connection exhaustion.

**Files:** `pkg/crawler/crawler.go:69`, `cmd/crawler/main.go:48`, `pkg/dgraph/dgraph.go:91-92, 443-444, 479-480`

**Details:** Go-grpc does implement connection pooling internally, but each `NewClient` creates a new gRPC connection. The multiple connections + transaction churn can accumulate if Dgraph is slow to respond.

**Fix approach:** Use a single shared Dgraph client (already done locally in the crawler, but the duplicate client in main.go should be removed or reused).

---

## Fragile Areas

### Transaction Semantics in `AddFollowers` Are Implicit

**Files:** `pkg/dgraph/dgraph.go:72-313`

**Why fragile:** The function does not commit the transaction until the very end (line 301). It performs ~5-6 separate `Mutate` calls on the same transaction, all with `CommitNow: false`. If any intermediate mutation fails (e.g., at line 180 or 265), the entire transaction is rolled back when `Discard` is called in the `defer` (line 92). However:
- The code does not explicitly check error returns from most mutations (lines 148, 180, 195, 263, 294).
- If a mutation fails mid-way (e.g., Step 3 bulk query at line 221), subsequent mutations are attempted anyway, potentially on inconsistent state.
- The final commit (line 301) can still fail (e.g., network timeout), and the error is returned, leaving the caller uncertain whether any mutations took effect.

**Safe modification:** Wrap the transaction in a helper that either commits or aborts cleanly, with explicit error checking at each mutation stage. Consider breaking `AddFollowers` into smaller, single-purpose functions (upsert follower, delete old edges, add new edges) each with their own transaction.

**Test coverage:** The function is exercised in the crawler's main loop, but no unit test explicitly verifies transaction atomicity or rollback behavior.

---

### Relay Reconnection Logic Does Not Track All Failure Modes

**Files:** `pkg/crawler/crawler.go:170-230`

**Why fragile:** The `markRelayDead` function (line 170) removes relays after 5 consecutive failures (line 183). However:
- The failure counter (`rs.failures`) is only incremented in `markRelayDead` (line 182), and only decremented when a reconnect succeeds (line 226).
- If a relay fails during a `FetchAndUpdateFollows` call, the error is logged but may not trigger `markRelayDead` if it's categorized as a "subscription error" rather than a "transport error" (line 422-429).
- Dead relays are removed from the slice in-place (line 171-198), but the config file is only updated if `onConnectFail` is called and succeeds. If the config write fails, a restarted crawler may re-attempt dead relays.

**Safe modification:** Ensure all failure paths that warrant relay death feed into `markRelayDead`. Add logging whenever a relay is permanently removed vs. temporarily backed off.

---

### Batch Processing Loop Is Not Stateless

**Files:** `cmd/crawler/main.go:97-165`

**Why fragile:** The main loop (lines 98-165) fetches stale pubkeys, processes them, logs progress, then repeats. But:
- If `FetchAndUpdateFollows` fails (line 152-160), the code breaks the loop entirely (line 159).
- The `pubkeys` map from `GetStalePubkeys` is not persisted or checkpointed. If the crawler crashes after processing some pubkeys but before the next batch, those pubkeys may be re-selected (since `last_db_update` was only set if a follow list was actually ingested).
- The "stale remaining" count (line 162) is computed but not validated — it assumes the batch map was fully consumed, but if `FetchAndUpdateFollows` returns early on error, the count is misleading.

**Safe modification:** Implement transactional batch checkpointing or at least log exactly which pubkeys were processed before a crash. Consider making `FetchAndUpdateFollows` non-fatal (continue the loop on error) and just log/skip.

---

### Dgraph Schema Evolution Is Coupled to Startup

**Files:** `pkg/crawler/crawler.go:76`, `pkg/dgraph/dgraph.go:54-66`

**Why fragile:** `EnsureSchema` is called once at startup (line 76 in crawler.go). If the schema needs to change (e.g., the pending fix to add `last_attempt`), the production Dgraph must be updated by running the new schema, which:
- Assumes the crawler can reach Dgraph at startup
- Blocks startup if the schema call fails
- Does not verify what the current schema is before applying changes (though Dgraph's `Alter` is idempotent)
- Any schema change to an existing cluster requires downtime or coordination

**Fix approach:** Move schema validation/migration to a separate CLI tool (e.g., `cmd/schema-migrate/`) that can be run before the crawler. This is already partially documented in the fix guide (Fix E backfill).

---

## Security Considerations

### No Validation of Relay Configuration

**Risk:** The `relay_urls` in config are trusted and connected to without validation.

**Files:** `pkg/config/config.go:51-57`, `pkg/crawler/crawler.go:85-100`

**Current mitigation:** Relays are public WebSocket endpoints; no authentication is used. The config is read from the local filesystem (`~/deepfry/web-of-trust.yaml`).

**Recommendation:** If config is ever exposed to network input (e.g., via HTTP endpoint), validate relay URLs (scheme=wss:// or ws://, no embedded credentials). Currently low risk since config is local.

---

### Event Signature Validation Is Performed but Results Are Not Persisted

**Risk:** Invalid events are rejected and logged, but there is no audit trail or rate-limiting per relay.

**Files:** `pkg/crawler/crawler.go:375-381`

**Current approach:** Events are validated (line 375). If invalid, a warning is logged (line 376) and a metric is recorded (line 377). The event is skipped (line 379).

**Recommendation:** If a relay is forwarding many invalid signatures, consider marking it as suspect or degraded. Currently no such logic exists.

---

### Pubkey Validation Is Minimal

**Risk:** Invalid or zero-value pubkeys could be created as nodes in Dgraph.

**Files:** `pkg/crawler/crawler.go:266-271`, `pkg/dgraph/dgraph.go:240-254`

**Current mitigation:** Pubkeys are validated using `nostr.GetPublicKey()` before querying relays (line 266-271). In `AddFollowers`, followees are assumed to be already-valid (no re-validation on insert).

**Recommendation:** Add a validation helper and apply it consistently in `AddFollowers` when creating new followee stubs (line 245-253).

---

## Performance Bottlenecks

### Relay Subscription Filter Is Unbounded Per Batch

**Problem:** Each batch of 500 pubkeys generates a filter with `Authors: [500 pubkeys]` (line 276-279 in crawler.go). Large filters can cause relays to timeout or return slow results.

**Files:** `pkg/crawler/crawler.go:276-280`

**Current approach:** The filter is sent as-is to each relay. No sub-batching or pagination is done on the relay side.

**Improvement path:** Split large batches into smaller queries (e.g., 50 pubkeys per relay subscription) to reduce filter complexity and latency. This would increase relay round-trips but improve timeout resilience.

---

### Bulk Query in `AddFollowers` Is O(n) String Concatenation

**Problem:** The bulk query (line 208-218 in dgraph.go) is built by concatenating query parts in a loop.

**Files:** `pkg/dgraph/dgraph.go:208-218`

**Details:** For a follow list with 5,000 follows, this generates a 5,000-line DQL query. String concatenation is inefficient, but more importantly, Dgraph's query parsing may degrade with very large queries.

**Improvement path:** Use a `strings.Builder` and measure query performance under load. Consider breaking large queries into multiple parallel queries if Dgraph performance degrades.

---

### Mutex Contention in `FetchAndUpdateFollows`

**Problem:** The `dbUpdateMutex` (line 54 in crawler.go) serializes all Dgraph writes during the event processing loop.

**Files:** `pkg/crawler/crawler.go:54, 350, 356, 362, 370, 378, 392, 402`

**Details:** Every event received from any relay must acquire the mutex before updating Dgraph. With 136 relays running concurrently, this becomes a bottleneck. The mutex is held for the entire `updateFollowsFromEvent` call (line 397), which can take seconds for large follow lists.

**Improvement path:** Instead of a global mutex, use per-pubkey locking (e.g., sync.Map or a lock per signer) so two threads can update different pubkeys in parallel. Or queue updates asynchronously.

---

## Scaling Limits

### Maximum Concurrent Relay Connections

**Current:** The crawler opens one connection per relay at startup (line 86-100 in crawler.go), defaults to ~5-136 relays based on config. Each relay can have one active subscription per query batch.

**Limit:** Go's runtime has no hard limit on goroutines, but the system has a file descriptor limit. Each relay connection uses a WebSocket socket (typically 1 FD). At 136 relays, that's 136 FDs + overhead. On most systems, this is fine (default ulimit -n is 1024 on macOS, 65k on Linux).

**Scaling concern:** If the relay list grows to > 1000 and all are queried concurrently, the system may hit the FD limit. Additionally, the startup phase (line 85-100) connects to all relays sequentially, taking ~4 minutes for 136 relays (noted in `8pc_crawled.md:446`).

**Improvement path:** Implement connection pooling with a max-concurrency limit, or batch relay connections at startup.

---

### Dgraph Transaction Throughput

**Current:** The crawler issues one transaction per `FetchAndUpdateFollows` call, containing multiple mutations. At 500 pubkeys per batch, this is ~1 transaction per batch, with internal mutations for each follow list.

**Scaling concern:** Dgraph's transaction throughput is limited by the cluster's commit rate. With large follow lists (10k+), each chunk creates a separate transaction (chunks.go:63). High churn can cause Dgraph to queue or timeout.

**Improvement path:** Batch multiple follow lists into a single transaction where possible, or implement Dgraph connection pooling.

---

## Dependencies at Risk

### nbd-wtf/go-nostr Relay Subscription Lifecycle

**Risk:** The library's subscription semantics are not fully documented. Calling `Unsub()` on a subscription that has already ended may hang or panic.

**Files:** `pkg/crawler/crawler.go:454`

**Current code:** `defer sub.Unsub()` is called unconditionally. If the relay disconnects before `Unsub()` is called, the behavior is undefined.

**Mitigation:** The code does check `sub.Context.Done()` (line 481) and returns early, but `Unsub()` is still called in the defer. This should be safe, but depends on library behavior.

**Recommendation:** Test what happens if `Unsub()` is called on a dead subscription. Add a timeout to the `Unsub()` call if the library doesn't provide one.

---

## Test Coverage Gaps

### No Integration Test for the Full Crawl-to-Dgraph Path

**What's not tested:** End-to-end: fetch kind-3 from a relay, parse follows, upsert to Dgraph, verify graph is updated correctly.

**Files:** All of `pkg/crawler/crawler.go`, `pkg/dgraph/dgraph.go`

**Risk:** Bugs in the event -> Dgraph pipeline are only caught in production. The existing tests (if any) are unit-level.

**Priority:** HIGH — The 8% stub bug would have been caught by a simple integration test that verifies stubs are crawled after the fix.

---

### No Test for Relay Reconnection Under Fault Conditions

**What's not tested:** Relay goes down mid-query, crawler detects it, marks it dead, and successfully reconnects later.

**Files:** `pkg/crawler/crawler.go:170-230` (markRelayDead, ReconnectRelays)

**Risk:** The reconnection logic is complex and brittle. Changes can silently break it.

**Priority:** MEDIUM — Relay flakiness is common; this should be tested.

---

### No Test for Transaction Rollback in `AddFollowers`

**What's not tested:** Mutation fails mid-way (e.g., step 3 fails), transaction is rolled back, Dgraph is left in initial state.

**Files:** `pkg/dgraph/dgraph.go:72-313`

**Risk:** Partial updates could corrupt the graph.

**Priority:** MEDIUM — This is a critical path function.

---

### No Test for Stale Pubkey Selection (Regression Test for 8% Bug)

**What's not tested:** The specific regression that triggered the 8% bug: `GetStalePubkeys` returns stubs (nodes with no `last_attempt`).

**Files:** `pkg/dgraph/dgraph.go:430-468`

**Risk:** The fix could be reverted accidentally without being caught.

**Priority:** HIGH — The 8pc_crawled.md document includes a detailed regression test specification (§5).

---

## Missing Critical Features

### No Way to Restart Interrupted Crawls from a Checkpoint

**Problem:** If the crawler crashes after processing 100 of 500 pubkeys, the next run will re-query those 100 (wasting bandwidth) or skip them (missing fresh data).

**Impact:** On long-running crawls or flaky networks, progress is lost.

**Solution:** Implement batch-level checkpointing (e.g., atomically mark a batch as "in progress", then "complete") so interrupted batches are resumed cleanly.

---

### No Metrics Export (Prometheus, Datadog, etc.)

**Problem:** The crawler logs progress but doesn't expose metrics for monitoring.

**Files:** Logs are printed (e.g., line 163 in main.go: "Batch complete: ..."), but no structured metrics.

**Impact:** It's hard to know if the crawler is healthy without parsing logs. Alerting is manual.

**Solution:** Export metrics (batch throughput, relay latency, Dgraph transaction time, error rates) via Prometheus endpoint or similar.

---

### No Circuit Breaker for Dgraph

**Problem:** If Dgraph becomes unavailable, the crawler will fail on every write but keep retrying with no backoff.

**Files:** `pkg/crawler/crawler.go:152-160` (error handling logs and breaks the loop)

**Impact:** The crawler exits immediately instead of retrying with exponential backoff.

**Solution:** Implement a circuit breaker for Dgraph: fail fast if too many consecutive write errors occur, and back off before retrying.

---

## Configuration Issues

### No Validation of Config Values at Load Time

**Problem:** If config values are invalid (e.g., `timeout: 0`, `stale_pubkey_threshold: -1`), errors only occur at runtime.

**Files:** `pkg/config/config.go:102-120`

**Recommendation:** Add validation after unmarshal: check that timeout > 0, stale threshold >= 0, relay URLs are valid WebSocket URLs, pubkey is 64-char hex, etc.

---

### Hardcoded Defaults in Code Are Hard to Override

**Problem:** Defaults like `initialBackoff` (30s), `maxBackoff` (5m), `maxConsecutiveFailures` (5) are constants, not config values.

**Files:** `pkg/crawler/crawler.go:33-37`

**Impact:** Tuning retry behavior requires code changes and rebuilds.

**Recommendation:** Move these to config (`web-of-trust.yaml`).

---

# Summary

| Category | Severity | Count | Top Issues |
|----------|----------|-------|-----------|
| **Tech Debt** | CRITICAL | 1 | 8% crawling bug (stubborn stubs) |
| **Known Bugs** | HIGH | 3 | Relay timeout stalls, large follow lists, event forwarding errors |
| **Fragile Areas** | MEDIUM | 4 | Transaction semantics, relay reconnection, batch state, schema coupling |
| **Gaps** | MEDIUM | 3 | Integration tests, relay faults, transaction rollback tests |

**Immediate action:** Apply the fix from `8pc_crawled.md` (coordinated changes to GetStalePubkeys, schema, and crawler main loop). This unblocks graph growth and will take ~2-4 hours to implement and verify.

---

*Concerns audit: 2026-06-09*
