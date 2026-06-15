# Crawler Logic Flow

## 1. Startup (`cmd/crawler/main.go`)

1. Create a cancellable `context.Background()` and register `SIGINT`/`SIGTERM` on a `sigChan`. A background goroutine blocks on that channel; when a signal arrives it calls `cancel()`.
2. Call `config.LoadConfig()` — reads `~/deepfry/web-of-trust.yaml` via Viper. If the file is absent, write defaults (relay URLs, Dgraph address, seed pubkey, staleness threshold, filter batch size). Decode any `npub`-format pubkeys to hex.
3. Instantiate a **stats-only** `dgraph.Client` (used only for `CountPubkeys` before and after the run).
4. If `ForwardRelayURL` is empty, prompt stdin for one and persist it with `config.SaveForwardRelayURL`.
5. Call `crawler.New(cfg)` — see §2 below.
6. Record `startTime` and `startingPubkeys` count.

---

## 2. `crawler.New()` — Initialization

1. Create a second `dgraph.Client` (the crawler's own client, separate from the stats client).
2. Call `dgClient.EnsureSchema()` — idempotently applies the Dgraph schema: `pubkey @upsert @unique`, `kind3CreatedAt @index(int)`, `last_db_update @index(int)`, `last_attempt @index(int)`, `follows [uid] @reverse`, type `Profile`.
3. For each relay URL in config:
   - Attach a NOTICE handler: `handleFilterNotice(rs, notice, 10)` — fires whenever the relay sends a `NOTICE` message.
   - Call `nostr.RelayConnect()`. On failure: call `OnConnectFail(url)` which removes the URL from the config file via `config.RemoveRelayURL`.
   - On success: set `rs.alive = true`, initialise `rs.filterCap` to `cfg.FilterBatchSize`.
4. If zero relays connected, return an error (fatal in `main`).
5. Optionally connect to the forward relay (no `OnConnectFail` here; failures just schedule a retry).

---

## 3. Main Loop (`cmd/crawler/main.go:mainLoop`)

Each iteration:

**a. Cancellation check** — non-blocking `select` on `ctx.Done()`. If cancelled, break.

**b. `dgraphClient.GetStalePubkeys(ctx, now-stalePubkeyThreshold, filterBatchSize)`**

Returns `map[pubkey]kind3CreatedAt` of up to `filterBatchSize` pubkeys to crawl, in two phases:

- **Phase 1 — frontier**: `NOT has(last_attempt)` query with `first: limit`. These are pubkeys discovered as follow targets but never yet queried. They are always prioritised.
- **Phase 2 — aged-out**: fills remaining slots with pubkeys that have `last_attempt < olderThanUnix` (default: 24 hours ago), ordered ascending by `last_attempt`.

**c. `dgraphClient.CountPubkeys(ctx)`** — get total graph size for the progress log.

**d. Seed bootstrap** — if `CountPubkeys == 0`, inject `cfg.SeedPubkey → 0` into the pubkeys map and log "starting with seed pubkey".

**e. Empty check** — if the map is still empty after the above, log "work complete" and break.

**f. `crawler.ReconnectRelays(ctx)`** — attempt to restore dead relays (see §7).

**g. `crawler.FetchAndUpdateFollows(ctx, pubkeys)`** — see §4. Returns `(hadEvents int, err error)`. On error: if `ctx.Err() != nil` break (graceful shutdown), otherwise break (log error).

**h. `dgraphClient.MarkAttempted(ctx, batchKeys, now)`** — stamps `last_attempt` on every pubkey in the batch regardless of whether it had an event (see §8). This prevents un-fetchable stubs from cycling back to the frontier every loop.

**i. Log progress**: "queried N pubkeys (M had events) | stale remaining | total in DB".

---

## 4. `FetchAndUpdateFollows()` — Relay Fan-Out

1. Validate each pubkey in the input map with `dgraph.ValidatePubkey()` (must be 64 lowercase hex chars); skip invalids.
2. Build `nostr.Filter{Authors: authors, Kinds: [3], Limit: len(authors)}`.
3. Create `relayQueryContext` = `context.WithTimeout(relayContext, c.timeout)` (default 30 s). This scopes only the relay I/O; Dgraph writes use `relayContext` (the un-timed outer context).
4. For each `alive` relay, launch a goroutine calling `queryRelay(relayQueryContext, rs, filter, eventsChan)`. Errors are sent to `errorsChan`. On success: reset `rs.failures` to 0.
5. A collector goroutine: `wg.Wait()` then closes both channels.
6. Select loop consuming `eventsChan` and `errorsChan`:

   **Event received from `eventsChan`:**
   - Lock `dbUpdateMutex`.
   - Skip `nil` events.
   - Skip event IDs already in `processedEventIDs` (dedup across relays).
   - `event.CheckSignature()` — skip invalid signatures (log warning).
   - `c.forwardEvent(relayContext, event)` — if a forward relay is alive, publish the raw event to it. On publish failure: close connection, set `alive = false`, schedule exponential backoff retry.
   - Add `event.PubKey` to `pubkeysWithEvents`.
   - **Version check**: if `event.CreatedAt <= pubkeys[event.PubKey]` (the stored kind3CreatedAt), skip the write and call `dgClient.TouchLastDBUpdate()` to refresh the staleness clock without changing the graph.
   - Otherwise: call `c.updateFollowsFromEvent(relayContext, event)` (see §5).
   - Record event ID in `processedEventIDs`.
   - Unlock mutex.

   **Error received from `errorsChan`:**
   - Context cancel/deadline strings: log in debug mode, continue.
   - `*subscriptionError`: log warning.
   - `*transportError`: log warning + `c.markRelayDead(url)` (see §6).

   **`relayQueryContext.Done()` (relay timeout):** if `DeadlineExceeded`, continue draining already-buffered events. If cancelled externally, return.

   **`relayContext.Done()` (main ctx cancelled):** return immediately with error.

   **`eventsChan` closed (all goroutines done):** return `len(pubkeysWithEvents), nil`.

---

## 5. `queryRelay()` → `drainSubscription()` — Per-Relay Subscription

**`queryRelay`:**

1. Split `filter.Authors` into chunks of `rs.filterCap` (initially = `cfg.FilterBatchSize`; can be reduced by NOTICE handler or connection-drop logic).
2. For each chunk:
   - Record `subscribeStart = time.Now()`.
   - Call `relay.Subscribe(ctx, []nostr.Filter{chunkFilter})`.
   - On `Subscribe` error within 500 ms with "not connected" / "failed to write": halve `rs.filterCap` (floor: 10), re-prepend the chunk to `authors` and retry. If already at floor: return `&transportError`.
   - On slower connection errors with "not connected" / "failed to write": return `&transportError`.
   - On other errors: return `&subscriptionError`.
   - On success: call `drainSubscription(ctx, sub, relayURL, eventsChan)`.
   - Call `sub.Unsub()` regardless.

**`drainSubscription` select loop:**

| Channel | Action |
|---|---|
| `sub.Events` | Send event to `eventsChan` (non-blocking w.r.t. `ctx.Done()`). |
| `sub.EndOfStoredEvents` | EOSE — return `nil`. |
| `sub.Context.Done()` | Relay connection dropped — return `&transportError`. |
| `ctx.Done()` | External cancel/timeout — return `ctx.Err()`. |

---

## 6. `markRelayDead()` — Relay Failure Handling

1. Close the relay connection.
2. Increment `rs.failures` atomically.
3. If `failures >= maxConsecutiveFailures` (5): call `onConnectFail(url)` → removes URL from config file; **drop the relay from the slice entirely**.
4. Otherwise: schedule `retryAt = now + rs.backoff`; double `rs.backoff` (cap: 5 min); keep the relay in the slice with `alive = false`.

---

## 7. `ReconnectRelays()` — Dead Relay Recovery

Called at the top of each main-loop iteration.

1. For each dead relay: skip if `now < rs.retryAt`.
2. Call `nostr.RelayConnect()` with a fresh NOTICE handler.
   - On failure: call `onConnectFail` and drop from slice.
   - On success: `rs.alive = true`, reset `rs.backoff = 30s`, `rs.failures = 0`, `rs.filterCap = filterBatchSize`.
3. Forward relay: same retry logic, but no `onConnectFail` callback and no removal from the slice on failure — just reschedule the backoff.

---

## 8. `updateFollowsFromEvent()` → `dgClient.AddFollowers()` — Dgraph Write

**`updateFollowsFromEvent`:**

1. Iterate `event.Tags`; collect all `p`-tag values into `followsMap` (validated hex pubkeys only; duplicates discarded by the map).
2. Call `dgClient.AddFollowers(ctx, event.PubKey, int64(event.CreatedAt), followsMap, debug)`.

**`AddFollowers` — one atomic Dgraph transaction:**

1. Validate `signerPubkey` is 64 hex chars; reject otherwise.
2. Compute `deadline = 30s + ceil(len(follows)/200) × 5s`; wrap ctx in a timeout.
3. Open a transaction.
4. **Query**: fetch signer's UID, existing `kind3CreatedAt`, and full `follows` list (UIDs + pubkeys) in one round-trip.
5. **If signer is new**: mutate a blank node with `pubkey`, `dgraph.type = "Profile"`, `kind3CreatedAt`, `last_db_update`. Record the assigned UID.
6. **If signer exists**: if `kind3createdAt <= existingKind3CreatedAt` — **return nil** (event is stale; no write).
7. Update `kind3CreatedAt` and `last_db_update` on the signer node.
8. **Delete all existing follows edges** (kind-3 is a replaceable event; the new list is canonical).
9. **For each 200-item window of followees:**
   - Build a bulk DQL query resolving all pubkeys to UIDs in one query.
   - For any followee not yet in the graph: write a stub node (`pubkey` + `Profile` type).
   - Resolve blank node UIDs from the mutation response.
   - Accumulate `<followerUID> <follows> <followeeUID>` nquads.
10. **Write accumulated follow edges** in 200-item batches (so even huge follow lists stay under the ~4 MB gRPC cap).
11. `txn.Commit()`.

---

## 9. `MarkAttempted()` — Post-Batch Bookkeeping

1. For each pubkey in the batch:
   - **Valid** (64 lowercase hex): add to `valid` slice.
   - **Recoverable** (uppercase/mixed-case 64 hex): look up the garbage node's UID. If a lowercase form already exists in the graph: delete the garbage node. If not: update the `pubkey` field to lowercase in place (do **not** stamp `last_attempt` so it re-enters the fresh frontier).
   - **Unrecoverable** (short hex, relay URLs, etc.): look up the node's UID and `DeleteNodes`.
2. Resolve valid pubkeys to UIDs via `ResolvePubkeysToUIDs`.
3. Mutate `last_attempt = ts` on each UID in one batch write.

---

## 10. Shutdown

1. `ctx.Done()` fires (via signal handler) → main loop breaks.
2. `generateFinalReport` — logs start/end pubkey counts, delta, and total runtime.
3. `wg.Wait()` — waits for the signal-handler goroutine to exit.
4. `defer crawler.Close()` — closes all relay WebSocket connections and the crawler's Dgraph gRPC connection.
5. `defer dgraphClient.Close()` — closes the stats Dgraph connection.

---

## Key Invariants

- **Frontier-first selection**: `GetStalePubkeys` always fills the batch from never-attempted nodes before adding aged-out ones, so the graph grows outward rather than re-refreshing known accounts.
- **Idempotent writes**: Dgraph's `@upsert` schema + the version guard in `AddFollowers` make every write safe to retry.
- **Dedup across relays**: `processedEventIDs` ensures the same event received from multiple relays is only written to Dgraph once per batch.
- **Follow-list replacement**: each `AddFollowers` call deletes *all* existing follows before writing the new list, enforcing NIP-01 replaceable-event semantics for kind-3.
- **Filter-size adaptation**: `rs.filterCap` shrinks on relay NOTICE (`"filter too large"`) or on connection-drop-within-500ms, and is reset to the configured default on reconnect.
