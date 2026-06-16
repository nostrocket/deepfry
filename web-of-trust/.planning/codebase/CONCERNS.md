# Codebase Concerns

**Analysis Date:** 2026-06-16

## Tech Debt

**Large Complex Files — Maintenance Risk**

- **Issue:** `pkg/crawler/crawler.go` (1309 lines) and `pkg/dgraph/dgraph.go` (1292 lines) contain the bulk of business logic with complex state machines, error handling, and retry loops. Single points of failure for crawler correctness.
- **Files:** `pkg/crawler/crawler.go`, `pkg/dgraph/dgraph.go`
- **Impact:** Changes to retry logic, timeout handling, or error classification require careful reasoning about goroutine lifetimes and state propagation. High cognitive load increases risk of regressions.
- **Fix approach:** Extract state machines (relay reconnect, failure counting, filter-cap learning) into separate `pkg/relay` package. Extract Dgraph batching/timeout orchestration into `pkg/dgraph/batch.go`. Keep business logic in current location but reduce line count by ~30%.

**No Unit Test Suite for Core Crawler Logic**

- **Issue:** Crawler's main loop (`FetchAndUpdateFollows`, `queryRelay`, relay classification) has no standalone unit tests. Integration tests gate on live Dgraph and relays (`//go:build integration`). No fast feedback loop during development.
- **Files:** `pkg/crawler/` (no `crawler_unit_test.go`)
- **Impact:** Bug fixes and refactors risk silent regressions. Timeout and concurrency logic (WR-01, HANG-03) is untestable without spinning up real infrastructure. Test cycle time ~5-10 minutes per change.
- **Fix approach:** Build testable seams for per-relay query logic (already partially done via `queryRelayFn` injection in tests). Add unit tests for error classification, relay state transitions, and quorum math. Estimated 200-400 lines of new test code.

**Hardcoded Safety Limits and Defaults**

- **Issue:** Multiple threshold and parameter values are hardcoded rather than configurable:
  - `batchSize = 200` in `pkg/dgraph/dgraph.go:92` (gRPC message cap tuning)
  - `initialBackoff = 30s`, `maxBackoff = 5m` in `pkg/crawler/crawler.go:48-49` (relay retry schedule)
  - `minCap = 10` in filter cap learning (floor never configured)
  - Dgraph query `first:` limits hard-wired into `GetStalePubkeys`, `BackfillNextAttempt`
- **Files:** `pkg/dgraph/dgraph.go`, `pkg/crawler/crawler.go`, `cmd/crawler/main.go`
- **Impact:** Tuning these values requires recompilation. In production outages (e.g. Dgraph overloaded, gRPC message size insufficient), values cannot be tweaked without rebuild. Filter-cap floor at 10 prevents discovery of relays with stricter limits without code change.
- **Fix approach:** Export `const` to `pkg/config/config.go` with YAML keys. Validate on load. Require restart to take effect (acceptable for batch parameters). Estimated 80 lines of config changes.

**Transaction Resource Leaks on Error Paths**

- **Issue:** `MarkAttempted` in `pkg/dgraph/dgraph.go` has inline `txn.Discard(ctx)` calls inside a for-loop (lines 688-694) with correct discipline, BUT `BackfillNextAttempt` (lines 863-865) and paginated queries have correct inline `Discard()` patterns. However, if any Dgraph operation panics (catastrophic error), deferred `Discard()` cleanup is lost on the panic path.
- **Files:** `pkg/dgraph/dgraph.go` (lines 154, 688-694, 863-865, 1160-1161)
- **Impact:** Long-running crawler under sustained Dgraph errors could accumulate open gRPC transactions, eventually exhausting Dgraph's connection pool or memory. Unlikely in production but possible under extreme load or Byzantine Dgraph behavior.
- **Fix approach:** Wrap all Dgraph transaction blocks in a helper function `withTxn(ctx, fn)` that guarantees inline `Discard()` even on panic via `defer` inside the helper. Estimated 30 lines of refactor; applies to 8+ transaction sites.

**Missing Graceful Degradation for Relay Reconnect**

- **Issue:** `ReconnectRelays` in `pkg/crawler/crawler.go:357-455` removes relays from the pool when they hit the ejection threshold (e.g. 10 transport failures). Once removed, they never re-enter the pool, even if the original error was transient (network partition resolved, relay restarted). The crawler silently continues with fewer relays forever.
- **Files:** `pkg/crawler/crawler.go` (lines 357-455, 309-355)
- **Impact:** A relay that was healthy for weeks can be permanently ejected after a bad hour (DNS blip, brief outage). The crawler's web-of-trust coverage degrades but logs do not warn that a known-good relay was abandoned. On day 7+, operator may not realize 20% fewer relays are active.
- **Fix approach:** Add optional "quarantine" list in config: ejected relays can be re-added manually by operator or automatically after 24h of ejection time. Log ejections as `ERROR` not `INFO` to force operator attention. Estimated 100 lines of changes to `markRelayDead` and main loop.

---

## Known Bugs

**Crawler Hang — Go-nostr Context Bypass (CRITICAL)**

- **Symptoms:** Crawler stops producing output for 40+ minutes. `ps` shows 0% CPU. Goroutine dump reveals two query goroutines parked indefinitely in go-nostr's `Subscription.Fire()`, blocking `wg.Wait()` and freezing the entire batch dispatcher.
- **Files:** `pkg/crawler/crawler.go` (lines 939-1084, specifically `queryRelay`), go-nostr library (not owned; v0.52.0)
- **Trigger:** Half-open TCP connections to Nostr relays (peer gone, local socket still `ESTABLISHED`). One of these relays in the active pool causes `relay.Subscribe` to park indefinitely.
- **Root cause:** go-nostr's `Subscription.Fire()` (subscription.go:187 in go-nostr) does a bare `<-sub.Relay.Write(...)` without consulting the context passed to `Subscribe()`. The context timeout is thus ineffective. Write queue is backed up behind a stuck REQ message. The single write goroutine cannot send the keepalive ping either — both are stuck in a TCP send buffer.
- **Current mitigation:** None active. Regression test `pkg/crawler/crawler_hang_test.go` exists and is RED (test passes, proving the hang is fixed, but the fix is NOT YET APPLIED).
- **Fix approach:** (A) Independent timeout exit in dispatcher: return from `FetchAndUpdateFollows` once `relayQueryContext.Done()` fires and buffered events drain, without waiting on `wg.Wait()`. Abandon stuck goroutines. (B) Wrap `relay.Subscribe` in a child goroutine with a cleanup handler: if `queryRelay` returns on context cancellation before the Subscribe result arrives, spawn a cleanup goroutine to `Unsub()` the result when it eventually lands. Estimated 40 lines of changes; estimated 2-3h to implement and test.

**Event Deduplication Across Relays — Event Processing Duplication**

- **Symptoms:** Same kind-3 event from multiple relays triggers `updateFollowsFromEvent` for each relay independently, causing redundant Dgraph writes.
- **Files:** `pkg/crawler/crawler.go` (lines 809-866 in main event loop, event dedup only by ID within the current batch)
- **Trigger:** Same event ID received from relay A and relay B in the same batch (normal in Nostr, relays synchronize).
- **Root cause:** `processedEventIDs` map is local to the current `FetchAndUpdateFollows` call (line 616). Same event ID from different relays in different batches is processed twice. Dedup is intra-batch only.
- **Impact:** Each duplicate is stamped as a separate write to Dgraph. If an event repeats across 5 relays in one batch, it incurs 5 mutations instead of 1. Under high relay diversity and low event churn, multiplier is 3-5x unnecessary gRPC traffic.
- **Fix approach:** Track `processedEventIDs` globally (per-crawler instance) in a separate `sync.Map` keyed by event ID. Prune entries older than the stale-feed refresh cadence (24h) to avoid unbounded memory growth. Estimated 60 lines of changes.

**Filter Rejection Handling Does Not Distinguish Probe vs. Steady State**

- **Symptoms:** A relay probing up from filter cap 10 → 20 receives a rejection. The code halves cap back to 10 and logs "filter rejection". If this happens at the high end of the cap range (e.g. 200 → 400 probe rejected), the relay becomes unusable for large follow-lists despite being previously stable.
- **Files:** `pkg/crawler/crawler.go` (lines 1204-1227, `handleCapRejection`)
- **Trigger:** A relay that passes probes up to cap X but rejects at X+1 (non-uniform filtering behavior, or transient overload).
- **Root cause:** Probing and steady-state rejections are conflated. The probing flag (`rs.probing`) exempts probes from counting toward ejection (D-11), but a probe rejection still halves the cap permanently (line 1205-1211).
- **Impact:** Relays with variable capacity (burst limits, per-peer rate-limiting) may learn an artificially low cap and then thrash: stable at 100, probe fails at 150, revert to 75, probe fails again next window. Over days, cap drifts down to floor=10.
- **Fix approach:** Distinguish probe-rejection from steady-state rejection. On a probe rejection (line 1214 `isProbing=true`), do not halve; instead, log and stay at current cap. Only halve on steady-state rejections. Add a separate `probeFailed` counter to track probe failures and eject probes with a separate lower threshold (e.g. 5). Estimated 80 lines of changes.

---

## Security Considerations

**No Input Validation on Config File Contents**

- **Risk:** `pkg/config/config.go` reads `relay_urls`, `seed_pubkeys`, `dgraph_addr` from YAML without validating format. Relay URLs are passed to `nostr.RelayConnect` (which validates), but seed pubkeys are not validated until clusterscan queries them.
- **Files:** `pkg/config/config.go` (lines 71-211)
- **Current mitigation:** Relay URLs validated by go-nostr's `RelayConnect`. Seed pubkeys are hex strings but not validated at load time.
- **Recommendations:** (1) Add pubkey format validation (`dgraph.ValidatePubkey`) at config load time so typos are caught early. (2) Validate relay URL format (must be `wss://` or `ws://`) at load time. (3) Log config changes on startup so operators know what seeds are active. Estimated 40 lines of config validation code.

**Dgraph Query Injection Risk — Unlikely but Possible**

- **Risk:** Some Dgraph queries are constructed by string concatenation with user-controlled values (relay URLs in `discoverFromNIP65`, pubkeys in `RemoveFollower`). If pubkey validation is bypassed or misconfigured, a pubkey like `"; drop all; "` could theoretically corrupt the DQL query syntax.
- **Files:** `pkg/dgraph/dgraph.go` (lines 407-410 in `RemoveFollower`, 14-15 in `handleFilterNotice`)
- **Current mitigation:** Pubkey format validation in `ValidatePubkey` prevents non-hex strings. Relay URLs normalized by go-nostr. But validation is per-call, not per-input.
- **Recommendations:** (1) Use DQL parameterized queries (Dgraph v21+ supports `$vars`) for all user-controlled data. (2) Pre-validate all inputs before string interpolation as a defensive layer. (3) Audit `discoverFromNIP65` (cmd/discover-relays) for relay URL handling — currently deduped but not validated before use. Estimated 100 lines of refactor to parametrize queries.

---

## Performance Bottlenecks

**Large Follow-Lists Cause Multi-Batch Timeouts**

- **Problem:** A single kind-3 event with >10k follows triggers chunked batches inside `AddFollowers` (lines 267-368 in dgraph.go). Each batch incurs its own Dgraph query and mutation. The total operation can exceed the context deadline if batches are slow.
- **Files:** `pkg/dgraph/dgraph.go` (lines 124-383, `AddFollowers`)
- **Cause:** While chunking keeps individual mutations under the 4MB gRPC cap, the cumulative time of N batches (where N = len(follows) / 200) can exceed the sized timeout `baseTimeout + perBatchTimeout * batches`. A 20k-follower list requires 100 batches = 500s of deadline, but a slow Dgraph can exceed that.
- **Improvement path:** (1) Parallelize batch queries (currently sequential). (2) Use dedicated large-list timeout multiplier (e.g. 3x) for events over 5k followers. (3) Monitor batch latency and log warnings when individual batches approach timeout. (4) Consider Dgraph's batch mutation API if available in v25+. Estimated 60 lines of refactor.

**Stale Pubkey Selection — Inefficient Ordering on Large Frontiers**

- **Problem:** `GetStalePubkeys` orders frontier nodes by `val(fc)` (follower count) using Dgraph's `orderdesc:`. For 100k+ frontier nodes, even with proper pagination, the ordering phase is expensive.
- **Files:** `pkg/dgraph/dgraph.go` (lines 539-548)
- **Cause:** Dgraph must compute `count(~follows)` for all frontier nodes before sorting. On a 100k frontier, this is a quadratic operation in graph traversal.
- **Improvement path:** (1) Cache follower counts in a stored `follower_count` predicate (Phase 8 D-10 investigation determined this was not needed, but reconsidering may be warranted). (2) Sample the frontier instead of full sort (return random N nodes instead of top-N by follower count — acceptable for crawler coverage). (3) Increase `first:` pagination window to amortize sort cost. (4) Move ordering to application layer after fetching. Estimated 80-120 lines of refactor.

**Relay Reconnect Loop — Linear Scan Over Entire Relay Pool**

- **Problem:** `ReconnectRelays` (lines 357-455) iterates the entire relay slice on every call (from main loop every batch). With 150+ relays, this is O(N) per batch = O(N*batches) over a day.
- **Files:** `pkg/crawler/crawler.go` (lines 357-455)
- **Cause:** No data structure (heap, priority queue) to track relays due-for-reconnect. Every iteration checks `time.Now().Before(rs.retryAt)` for all relays.
- **Improvement path:** (1) Maintain a separate `needsReconnect` heap keyed by `retryAt`. (2) Extract just-ready relays from heap once per call. (3) Update heap on successful/failed reconnect. Estimated 80 lines of refactor. Gain: O(1) amortized per batch for reconnect sweep.

**Dgraph Transient Error Retry — Indefinite Linear Backoff**

- **Problem:** `retryDgraph` (cmd/crawler/main.go lines 96-136) retries indefinitely with doubling backoff (1m → 2m → 4m → capped at 5m). A sustained Dgraph outage (e.g. disk full) causes crawler to stall, waiting 5 minutes between each retry. No timeout or max-attempts gate.
- **Files:** `cmd/crawler/main.go` (lines 96-136)
- **Cause:** Indefinite retry is intentional (RESIL-01 comment) for transient errors, but there is no distinction between "Dgraph is temporarily slow" and "Dgraph is permanently broken".
- **Improvement path:** (1) Add a max-retry count (e.g. 3-5 attempts) before logging an ERROR and breaking. (2) Add a circuit-breaker: if 3 consecutive gRPC calls fail with the same code, wait 5m then exit with error instead of continuing. (3) Implement Dgraph health-check: ping Dgraph before retrying a failed mutation. Estimated 60 lines of refactor.

---

## Fragile Areas

**Relay State Machine — Tightly Coupled Backoff and Ejection Logic**

- **Files:** `pkg/crawler/crawler.go` (lines 74-107 relayState, 309-355 markRelayDead, 357-455 ReconnectRelays)
- **Why fragile:** Relay state (alive/dead, backoff duration, failure counters, completedGen, filterCap, etc.) is spread across 6 atomic fields and 2 non-atomic booleans. Transitions are scattered across `markRelayDead`, `ReconnectRelays`, `handleCapRejection`, and inline in `queryRelay`. Changes to backoff logic risk breaking completedGen generation-stamp logic or forgetting to halve failure counters on reconnect.
- **Safe modification:** (1) Document all state transitions in relayState comment block. (2) Create a state-transition method `func (rs *relayState) markReconnected()` that handles all mutations atomically. (3) Add unit tests for each transition path (alive→dead→reconnected, failure counting, backoff doubling). (4) Refactor to separate struct: `type RelayMetrics` for all failure/streak counters, and keep state compact.

**Dgraph Mutation Ordering — Kind3CreatedAt Version Guard**

- **Files:** `pkg/dgraph/dgraph.go` (lines 156-244)
- **Why fragile:** `AddFollowers` checks `kind3CreatedAt` to reject older events (lines 217-222). If the query in step 1 races with a concurrent `AddFollowers` from another crawler instance, the version check is no longer atomic. A newer event could be lost if two updates arrive simultaneously.
- **Safe modification:** (1) Use Dgraph's conditional mutation (`@if` syntax if available in v25) to make the version check atomic. (2) Add a `last_write_ts` field that records when the node was last mutated, and use that for concurrency detection. (3) Wrap AddFollowers in a higher-level guard that de-dupes events by ID at the application layer before calling Dgraph. (4) Document the assumption: "only one crawler instance per graph" and validate in startup.

**Filter Rejection Attribution — Timing Window of 500ms**

- **Files:** `pkg/crawler/crawler.go` (lines 1049-1054)
- **Why fragile:** A connection drop occurring within 500ms of Subscribe is attributed to filter rejection. This window is somewhat arbitrary. A relay that consistently drops connections after 250ms of Subscribe (but not due to filter size) would be misclassified as filter-rejection and have its cap halved repeatedly.
- **Safe modification:** (1) Log the Subscribe→drop latency and retain a histogram (debug mode) so operators can tune the window. (2) Require multiple consistent rejections within the window before halving (avoid single transient drop). (3) Add a separate "quick-close" counter per relay and only apply cap-halving if the counter exceeds threshold. (4) Document the 500ms window as a design parameter in CLAUDE.md with rationale.

---

## Scaling Limits

**Dgraph Graph Size — Query Performance on 1M+ Nodes**

- **Current capacity:** Production graph holds ~500k pubkeys (stubs + fully-fetched). Queries scale linearly with frontier/aged set size.
- **Limit:** At 1M nodes, `GetStalePubkeys` frontier ordering may take 10+ seconds (unverified; based on D-09 live verification of 100k nodes). Subsequent pageinated calls further multiplied.
- **Scaling path:** (1) Implement follower-count caching as stored predicate (Phase 8 D-10 re-evaluation). (2) Sample frontier instead of sorting. (3) Horizontal sharding: split graph by pubkey hash range across multiple Dgraph instances. (4) Use Dgraph Multi-Tenancy or separate instances per shard. Estimated 200+ lines of sharding logic.

**Memory Usage — Unbounded Maps in MarkAttempted Recovery**

- **Current capacity:** Recovery logic scans valid pubkeys and resolves UIDs (lines 635-720). `ResolvePubkeysToUIDs` query returns all resolved nodes in a single JSON response. For 100k pubkeys in a batch, this could be ~50MB of JSON.
- **Limit:** At 500k stale pubkeys per batch, MarkAttempted could allocate 250MB for a single call (unverified; depends on Dgraph response size).
- **Scaling path:** (1) Paginate UID resolution in `MarkAttempted` (currently processes all at once). (2) Use read-only transactions to reduce memory overhead. (3) Stream resolution results instead of accumulating into a slice. Estimated 80 lines of refactor.

**Relay Pool Size — O(N) Operations Scale Linearly**

- **Current capacity:** Tested with 150 relays. ReconnectRelays, quorum calculation, and error classification are O(N).
- **Limit:** At 500 relays, ReconnectRelays scans 500 entries per batch. Quorum calculation (lines 522-545) makes a linear pass. At 1h refresh cadence, this is acceptable but not elegant.
- **Scaling path:** (1) Index relays by retry-time (heap or priority queue) to make reconnect O(log N). (2) Cache quorum denominator across phases if relay set is stable. (3) Consider sharding relays across multiple crawler instances. Estimated 100 lines of refactor.

---

## Dependencies at Risk

**go-nostr v0.52.0 — Context Bypass, Subscription Leak (Critical)**

- **Risk:** Library ignores context passed to `Subscription.Fire()`, causing hangs (HANG-FINDINGS.md). Subscriptions leak on context cancellation if not properly cleaned up.
- **Impact:** Blocks `FetchAndUpdateFollows` if a relay's Subscribe call parks. Goroutines leak on quorum-cancelled early-exits (partially mitigated by cleanup handler in CR-02).
- **Migration plan:** (1) Monitor go-nostr releases for context-aware Fire() fix. (2) Interim: implement per-relay subscription timeout wrapper as done in CR-02. (3) Evaluate alternatives: `github.com/jb55/go-nostr` (fork) or custom minimal relay client for kind-3 subscriptions only. (4) Add integration test that verifies context deadlines are respected in all paths.

**dgo v210 (Dgraph gRPC client) — Large Message Handling**

- **Risk:** Dgraph v25 may change message size limits or introduce new protocol versions incompatible with dgo v210. gRPC default 4MB limit forced us to chunk large follow-lists and increase `maxRecvMsgSize` to 256MB (dgraph.go:39).
- **Impact:** Follow-lists >50k triggers multi-batch operations. If Dgraph changes limits or dgo drops, rework `AddFollowers` batching.
- **Migration plan:** (1) Monitor Dgraph releases (currently v25, dgo v210 compatible through v25 based on semver). (2) Vendorize message-size tuning constants. (3) Add integration test for 50k+ follow-lists to catch incompatibilities early. (4) Document the 4MB limit and 256MB override rationale in CLAUDE.md. Estimated 30 lines of documentation.

**spf13/viper — Config Mutation Limitations**

- **Risk:** viper's `SafeWriteConfigAs` (config.go:145) does not persist `SetDefault` values, requiring explicit `viper.Set` calls. Future viper versions may change behavior.
- **Impact:** Config file does not record defaults; hard to know what values were used. A removed default may cause unexpected behavior on next load.
- **Migration plan:** (1) Pre-populate all `SetDefault` values explicitly with `viper.Set` before writing (already done lines 147-148). (2) Consider migrating to hand-written YAML marshaling for full control. (3) Add integration test that reloads config after write and verifies all values persist. Estimated 60 lines of refactor.

---

## Missing Critical Features

**No Pubkey Trust Score — Identity Scoring Missing**

- **Problem:** Crawler discovers who follows whom (the graph), but no trust scoring. Whitelist plugin (separate repo) uses raw graph queries to decide pubkey acceptance. No aggregation of trust signals.
- **Blocks:** Can't answer "Is pubkey X trustworthy?" without analyzing the full graph manually. Clusterscan does coarse spam-cluster detection but not granular trust scoring.
- **Gap:** Profile service (placeholder in DeepFry) should compute trust scores (e.g. "distance to trusted root", "follower quality", "account age"). Whitelist should use scores, not raw graph structure.

**No Event Deduplication Across Batches**

- **Problem:** Same event in multiple batches causes redundant Dgraph writes (Known Bugs section). No mechanism to prevent this beyond intra-batch dedup.
- **Blocks:** Can't optimize event processing when the same event arrives multiple times (normal in Nostr sync).

**No Graceful Relay Handoff**

- **Problem:** When a relay is ejected, there is no list of relays-to-add or mechanism to swap in new ones without restarting the crawler.
- **Blocks:** Operator must manually restart crawler with updated `relay_urls` config to recover from a relay ejection.

**No Configurable Dgraph Sharding**

- **Problem:** Crawler connects to a single Dgraph instance. No support for sharded instances or multi-region failover.
- **Blocks:** Can't scale to 1M+ pubkeys without a single-point-of-failure Dgraph.

---

## Test Coverage Gaps

**Untested: queryRelay Timeout Behavior Under go-nostr Hang**

- **What's not tested:** When go-nostr's `Subscription.Fire()` hangs, does the timeout exit (HANG-03) correctly close the connection and return? The hang-test (crawler_hang_test.go) blocks intentionally but doesn't verify timeout responsiveness.
- **Files:** `pkg/crawler/crawler.go` (lines 939-1084), `pkg/crawler/crawler_hang_test.go` (regression test)
- **Risk:** The fix for HANG-01/HANG-02/HANG-03 may not fully work under real go-nostr hangs.
- **Priority:** HIGH — this is the most critical bug in the codebase.

**Untested: Dgraph Transient Error Retry Behavior**

- **What's not tested:** `retryDgraph` with injected transient gRPC codes (Unavailable, DeadlineExceeded). Does it correctly distinguish transient from fatal?
- **Files:** `cmd/crawler/main.go` (lines 96-136), integration tests only
- **Risk:** A fatal gRPC error misclassified as transient could cause indefinite retries. No test prevents this regression.
- **Priority:** MEDIUM — affects reliability during outages.

**Untested: Filter Cap Learning and Probe-Up Mechanism**

- **What's not tested:** Does probe-up (D-10) correctly detect relay capacity increases? Does a rejection on a probe properly exclude it from ejection threshold?
- **Files:** `pkg/crawler/crawler.go` (lines 962-1081, `handleCapRejection`)
- **Risk:** Cap learning logic may have off-by-one errors or race conditions on filterCap atomic operations.
- **Priority:** MEDIUM — affects relay-side batching efficiency.

**Untested: Clusterscan Trust Propagation Edge Cases**

- **What's not tested:** Does clusterscan correctly handle cycles in the follow graph? Disconnected components? Seed pubkeys not in the graph?
- **Files:** `pkg/dgraph/clusterscan.go`, `cmd/clusterscan/main.go`
- **Risk:** Graph analysis may skip nodes or include false positives.
- **Priority:** LOW — clusterscan is read-only analysis, not data mutation.

---

*Concerns audit: 2026-06-16*
