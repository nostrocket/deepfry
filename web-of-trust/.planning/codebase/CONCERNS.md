# Codebase Concerns

**Analysis Date:** 2026-06-09

## Tech Debt

**Mutex held across Dgraph writes in the event loop:**
- Issue: `dbUpdateMutex` is locked at the top of the `eventsChan` case and held through `event.CheckSignature()`, `forwardEvent()` (a network publish), `TouchLastDBUpdate()` and the full `updateFollowsFromEvent()` → `AddFollowers()` Dgraph transaction. All per-event work is serialized behind a single mutex even though events arrive concurrently from multiple relays.
- Files: `pkg/crawler/crawler.go:350-402`
- Impact: The crawler processes events strictly one-at-a-time; a slow Dgraph commit or a slow forward-relay publish stalls processing of every other relay's events. Defeats the concurrent relay fan-in. Signature verification (CPU-bound) also runs under the lock.
- Fix approach: Narrow the critical section to only the Dgraph mutation, or move signature checks and forwarding outside the lock. Consider a worker pool draining `eventsChan` with per-pubkey serialization instead of one global mutex.

**`CountPubkeys` runs twice every loop iteration:**
- Issue: The main loop calls `CountPubkeys` on every batch (`totalPubkeys`) plus once at start/end. `CountPubkeys` runs `count(func: has(pubkey))` which scans the entire pubkey index.
- Files: `cmd/crawler/main.go:116-120`, `pkg/dgraph/dgraph.go:540-571`
- Impact: As the graph grows into hundreds of thousands of nodes, a full count per batch adds avoidable latency to every cycle. The value is only used for a log line and an empty-DB check.
- Fix approach: Count once before the loop; only re-check for empty-DB on the first iteration. Drop the per-batch count or run it on a timer.

**Redundant `fmt.Sprintf("%s", strings.Join(...))`:**
- Issue: `bulkQuery := fmt.Sprintf("{ %s }", fmt.Sprintf("%s", strings.Join(queryParts, "\n")))` wraps a `strings.Join` in a no-op `Sprintf("%s", ...)`.
- Files: `pkg/dgraph/dgraph.go:226-227`
- Impact: `go vet` flags this; minor wasted allocation. Cosmetic but indicates the file has not been vet-clean.
- Fix approach: `bulkQuery := fmt.Sprintf("{ %s }", strings.Join(queryParts, "\n"))`.

**Stale stub crawl problem (documented, partially mitigated):**
- Issue: Per `8pc_crawled.md`, the crawler historically re-refreshed the ~15k known accounts and only crawled ~8% of known pubkeys, never reaching the stub frontier. `GetStalePubkeys` was reworked to a frontier-first query (`NOT has(last_attempt)`), and `MarkAttempted` was added so un-fetchable stubs age out.
- Files: `pkg/dgraph/dgraph.go:443-537`, `cmd/crawler/main.go:151-159`, `8pc_crawled.md`
- Impact: The fix is recent and only covered by a single integration test. The `last_attempt` predicate must be backfilled from `last_db_update` for already-crawled nodes (noted in `8pc_crawled.md:344`); if that backfill did not run, the aged-query phase behaves differently than intended on the live graph.
- Fix approach: Verify the live `last_attempt` backfill completed; add coverage for the Phase-2 aged top-up path, not just the frontier path.

## Known Bugs

**Chunked large follow-lists trip the kind3CreatedAt version guard:**
- Symptoms: For follow lists >10,000, `processFollowsInChunks` calls `AddFollowers` once per 200-item chunk with the same `createdAt`. The first chunk writes `kind3CreatedAt`; every subsequent chunk hits the `kind3createdAt <= existingKind3CreatedAt` guard (`==` case) and returns early at `pkg/dgraph/dgraph.go:165-168`, so only the first 200 follows are ever persisted.
- Files: `pkg/crawler/chunks.go:37-75`, `pkg/dgraph/dgraph.go:163-168`
- Trigger: Any pubkey with more than 10,000 follows.
- Workaround: None in code. The guard treats equal timestamps as "already have it," which is correct for whole-event replay but wrong for the chunked path that reuses one timestamp across calls.
- Fix approach: Have `AddFollowers` support an "append additional follows for this same event" mode, or accumulate all chunks into a single replace operation, or pass a flag that bypasses the version guard for continuation chunks.

**`defer cancel()` accumulates inside the chunk loop:**
- Symptoms: In `processFollowsInChunks` each iteration does `chunkCtx, cancel := context.WithTimeout(...)` followed by `defer cancel()`. The deferred cancels do not fire until the whole function returns, so for an N-chunk list, N contexts (and their timers) stay live until completion.
- Files: `pkg/crawler/chunks.go:38-40`
- Trigger: Large follow lists processed in many chunks.
- Workaround: None.
- Fix approach: Call `cancel()` explicitly at the end of each loop iteration instead of deferring, or wrap the body in a closure.

## Security Considerations

**DQL queries built by string concatenation / `fmt.Sprintf`:**
- Risk: Pubkeys and other values are interpolated directly into DQL query strings throughout the Dgraph layer rather than always using `Vars`. `AddFollowers` uses `%q` (Go-quoted) which escapes quotes, and `RemoveFollower` concatenates `signerPubkey`/`followee` straight into the query with `+`.
- Files: `pkg/dgraph/dgraph.go:104-114` (`%q`), `pkg/dgraph/dgraph.go:344-358` (`RemoveFollower`, raw concatenation), `pkg/dgraph/clusterscan.go:57-63` (`strconv.Quote`)
- Current mitigation: Pubkeys are validated as 64-char hex via `nostr.GetPublicKey` before reaching most write paths (`pkg/crawler/crawler.go:266`, `:507`); `%q` and `strconv.Quote` escape embedded quotes.
- Recommendations: `RemoveFollower` does NOT validate its inputs and uses raw `+` concatenation — route it through `Vars` or validate inputs like the other paths. Standardize on parameterized `Vars` for all value-bearing queries to remove the injection surface entirely.

**No authentication on Dgraph or relay connections:**
- Risk: Dgraph gRPC uses `insecure.NewCredentials()` (no TLS, no auth) and relays connect over plain WebSocket. Anyone with network access to `localhost:9080` can read/write the graph.
- Files: `pkg/dgraph/dgraph.go:40-44`
- Current mitigation: Relies on the service binding to localhost / private network only (per `CLAUDE.md` infra notes).
- Recommendations: Acceptable for a localhost sidecar, but document the trust boundary explicitly; if Dgraph is ever exposed beyond localhost, TLS + ACL become mandatory.

## Performance Bottlenecks

**Frontier selection can load hundreds of thousands of stubs into one response:**
- Problem: `GetStalePubkeys` runs `frontier(func: has(pubkey), first: limit) @filter(NOT has(last_attempt))` and the gRPC client raises `MaxCallRecvMsgSize` to 256 MiB specifically because this response "can return well over gRPC's default 4MB cap."
- Files: `pkg/dgraph/dgraph.go:34-44`, `:451-457`
- Cause: An unindexed-style filter over the whole pubkey set with a large `first:` materializes a huge result. The 256 MiB cap is a band-aid that lets large batches succeed but also masks unbounded memory growth.
- Improvement path: Keep `limit` small (the main loop uses `batchSize = 500`, which is fine), but the cap suggests other callers pass large limits. Audit all `GetStalePubkeys` callers; consider an index/order strategy so the frontier query is bounded and cheap.

**Trust-propagation and weak-bridge queries scan the entire graph:**
- Problem: `ExpandTrustedSet` and `GetWeakBridges` both use `cand as var(func: has(pubkey)) @filter(NOT uid(trusted))` — a full scan of every pubkey node, with a `count(~follows ...)` evaluated per node, on every propagation round.
- Files: `pkg/dgraph/clusterscan.go:102-111` (`ExpandTrustedSet`), `:151-168` (`GetWeakBridges`)
- Cause: No pagination and no narrowing; trust closure loops until no new nodes, re-scanning the whole graph each round.
- Improvement path: This is acceptable for an offline read-only CLI but will not scale to multi-million-node graphs. Consider expanding only from the newly-added frontier each round (nodes followed by the round's new members) rather than re-scanning all candidates.

**`GetPubkeysWithMinFollowers` (non-paginated) materializes all matches:**
- Problem: The non-paginated variant returns every matching pubkey in one query with no `first:`.
- Files: `pkg/dgraph/dgraph.go:618-653`
- Cause: No batching; superseded by `GetPubkeysWithMinFollowersPaginated` but still exported.
- Improvement path: Deprecate/remove the non-paginated version or document that it is unsafe for large graphs.

## Fragile Areas

**Manual upsert in `AddFollowers` races against `@unique` schema:**
- Files: `pkg/dgraph/dgraph.go:80-321`, schema at `:60-74`, cleanup tool at `cmd/healthcheck/main.go`
- Why fragile: Stub followees are created with a query-then-create pattern (`bulkQuery` to find existing, then `_:new_followee_N` blank nodes for missing ones) inside one transaction. Two transactions creating the same stub pubkey concurrently can both observe "not present" and both insert it. The existence of `healthcheck`'s duplicate-detection-and-purge logic confirms duplicates do occur in practice despite the `@unique` directive.
- Safe modification: Do not weaken the dedup logic in `healthcheck`. Prefer Dgraph upsert blocks (`uid(var)` with `@upsert`) over query-then-insert for stub creation. Always stop the crawler before running `healthcheck -purge` (the tool warns about this at `cmd/healthcheck/main.go:142`).
- Test coverage: No test exercises concurrent stub creation or the upsert race.

**In-memory relay state lost on restart:**
- Files: `pkg/crawler/crawler.go:39-46`, `:170-256`
- Why fragile: Relay liveness, backoff timers, and failure counts live only in the `relayState` slice. On restart all relays start "fresh," so a relay that was being backed off gets hammered again immediately. `markRelayDead` and `ReconnectRelays` mutate `c.relays` in place using the `c.relays[:0]` reuse trick — correct but easy to break.
- Safe modification: When editing the relay-pruning loops, preserve the slice-reuse invariant; do not hold references to `c.relays` across these calls. Relay state is not persisted by design.
- Test coverage: No tests for `markRelayDead` / `ReconnectRelays` backoff transitions.

**Config mutation via global Viper singleton:**
- Files: `pkg/config/config.go:146-166`
- Why fragile: `SaveForwardRelayURL` and `RemoveRelayURL` operate on the package-global Viper instance, which must have been populated by `LoadConfig` first. Calling them out of order, or from a context where Viper was re-initialized (e.g. `discover-relays` also calls `viper.SetConfigName`), can write to the wrong file or lose keys. `discover-relays` deliberately bypasses Viper write and edits YAML by hand (`writeRelayURLsToConfig`) to preserve formatting.
- Safe modification: Keep config writes going through the same Viper instance that loaded; do not introduce a second loader in the same process. Never edit the live `~/deepfry/web-of-trust.yaml` in tests — use a temp `HOME`.
- Test coverage: No tests for config save/remove round-trips.

## Scaling Limits

**Single-process, single-loop crawler:**
- Current capacity: One crawler process draining one batch of 500 stale pubkeys at a time, with all DB writes serialized behind `dbUpdateMutex`.
- Limit: Throughput is bounded by sequential Dgraph commit latency. The `8pc_crawled.md` analysis shows the graph has tens of thousands of known accounts and a far larger stub frontier; at one-event-at-a-time write throughput, full frontier coverage is slow.
- Scaling path: Parallelize Dgraph writes (per-pubkey locking instead of global mutex), and/or shard the frontier across multiple crawler instances keyed by pubkey prefix.

**gRPC receive cap as a proxy for unbounded memory:**
- Current capacity: 256 MiB max receive message (`pkg/dgraph/dgraph.go:39`).
- Limit: A single query response is held entirely in memory and JSON-unmarshalled; very large frontier/popular queries can approach the cap and the corresponding RAM.
- Scaling path: Enforce small `first:`/batch sizes on every materializing query; never rely on the 256 MiB headroom.

## Dependencies at Risk

**`github.com/dgraph-io/dgo/v210` pinned to a pre-release pseudo-version:**
- Risk: Version is `v210.0.0-20230328113526-b66f8ae53a2d` — a commit-pinned pseudo-version from 2023, not a tagged release. Dgraph's Go client has had multiple major reorganizations.
- Impact: All graph reads/writes flow through this client; an upstream breaking change or an abandoned `v210` line could strand the project.
- Migration plan: Track Dgraph's current supported Go client line; plan a deliberate upgrade with the integration test as the gate. The DQL queries themselves are the portable part.

**`go-nostr` is the only relay library and is fast-moving:**
- Risk: `github.com/nbd-wtf/go-nostr` v0.52.0 — the project relies on internal error-string shapes (`cleanSubscribeError` parses `"couldn't subscribe to"` substrings, and `queryRelay` matches `"not connected"` / `"failed to write"` substrings).
- Impact: A library change to those error messages silently breaks transport-vs-subscription error classification, which drives the relay dead/reconnect logic.
- Migration plan: Replace substring matching on error text with typed-error checks if go-nostr exposes them; pin and review go-nostr upgrades carefully.

## Missing Critical Features

**No automated verification harness:**
- Problem: Per `CLAUDE.md` and `8pc_crawled.md` §6, verifying crawler behavior requires manually running it against live Dgraph + relays on the strfry host. There is no scripted way to assert "the crawler expands the frontier" in CI.
- Blocks: Confident regression detection on the core value ("must continuously expand the web of trust"). The recent frontier-first fix is only guarded by one integration test that itself needs a live Dgraph.

**No metrics/observability beyond log lines:**
- Problem: "Metrics" are JSON blobs printed via `log.Printf` (`METRICS:`, `RELAY_ERROR:`, `DEBUG_METRICS:`), several gated behind `c.debug`.
- Files: `pkg/crawler/crawler.go:555-618`
- Blocks: No way to track frontier-coverage progress, write throughput, or relay health over time without scraping logs. No counters for the "only 8% crawled" problem to confirm the fix in production.

## Test Coverage Gaps

**Almost no test suite exists:**
- What's not tested: There is exactly one test file (`pkg/dgraph/dgraph_stale_test.go`), gated behind `//go:build integration` and requiring a live Dgraph on `localhost:9080`. It covers only the frontier-selection path of `GetStalePubkeys`. `CLAUDE.md` states "No unit-test suite exists yet."
- Files: `pkg/dgraph/dgraph_stale_test.go` (only test); untested: all of `pkg/crawler/`, `pkg/config/`, `pkg/dgraph/clusterscan.go`, every `cmd/`.
- Risk: Pure-logic units that are trivially unit-testable without Dgraph are untested and have known defects — e.g. `cleanSubscribeError` (error parsing), `normalizeSeedPubkeys` (dedup/decode), `processFollowsInChunks` chunk math + the version-guard bug, `rankNodes`, `dedupURLs`/`normalizeAndDedup`. The chunked-follow-list bug above would be caught by a unit test of the chunk path.
- Priority: High — add unit tests for the pure helpers first (no infra needed), then for the AddFollowers version-guard / chunk interaction.

**Aged top-up path untested:**
- What's not tested: Phase 2 of `GetStalePubkeys` (`aged(...) orderasc: last_attempt @filter(lt(last_attempt, ...))`). Only Phase 1 (frontier) is asserted.
- Files: `pkg/dgraph/dgraph.go:462-475`, `pkg/dgraph/dgraph_stale_test.go`
- Risk: The re-crawl scheduling that prevents staleness is unverified; a regression here recreates the "only re-refresh known accounts" failure mode from a different angle.
- Priority: Medium.

**Concurrent write / upsert race untested:**
- What's not tested: Concurrent `AddFollowers` calls creating the same stub followee (the duplicate source `healthcheck` cleans up).
- Files: `pkg/dgraph/dgraph.go:208-305`
- Risk: Silent duplicate-node growth.
- Priority: Medium.

---

*Concerns audit: 2026-06-09*
