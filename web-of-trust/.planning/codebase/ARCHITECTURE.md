<!-- refreshed: 2026-06-16 -->
# Architecture

**Analysis Date:** 2026-06-16

## System Overview

```text
┌──────────────────────────────────────────────────────────────────┐
│                       Entry Points (cmd/)                         │
│  crawler │ clusterscan │ pubkeys │ healthcheck │ discover-relays  │
└────┬──────────┬──────────┬──────────┬──────────┬─────────────────┘
     │          │          │          │          │
     ▼          ▼          ▼          ▼          ▼
┌──────────────────────────────────────────────────────────────────┐
│              pkg/crawler (relay pooling & subscription)           │
│            `pkg/crawler/crawler.go`                               │
│  • Relay state machine (alive/dead/backoff/ejection)              │
│  • kind 3 event subscription from Nostr relays                    │
│  • Per-class failure counters + ejection thresholds               │
│  • Event forwarding to forward relay                              │
└────┬──────────────────────────────────────────────────────────────┘
     │
     ▼
┌──────────────────────────────────────────────────────────────────┐
│          pkg/dgraph (Dgraph graph operations & queries)           │
│            `pkg/dgraph/dgraph.go` + `clusterscan.go`              │
│  • Write path: AddFollowers (upsert, version guard, chunking)     │
│  • Read path: GetStalePubkeys (frontier-first selection)          │
│  • Analysis: ResolvePubkeysToUIDs, ExpandTrustedSet, GetWeakBridges
│  • Health: CountPubkeys, ValidatePubkey, DeleteNodes              │
│  • Backoff: MarkAttempted (miss/hit stamping with cadence)        │
└────┬──────────────────────────────────────────────────────────────┘
     │
     ▼
┌──────────────────────────────────────────────────────────────────┐
│            Dgraph (Port 9080 gRPC, 8080 HTTP)                    │
│  Schema: pubkey(@id,@upsert), follows([uid]), timestamps          │
│  Storage: Profile type with kind3CreatedAt, last_attempt, etc.    │
└──────────────────────────────────────────────────────────────────┘
     │
     ▼
┌──────────────────────────────────────────────────────────────────┐
│           pkg/config (YAML load/persist from ~/deepfry/)          │
│             `pkg/config/config.go`                                │
│  • Relay URLs, cluster-scan seeds, ejection thresholds            │
│  • Backoff parameters, EOSE quorum, stale pubkey threshold        │
└──────────────────────────────────────────────────────────────────┘
     │
     ▼
┌──────────────────────────────────────────────────────────────────┐
│      Upstream Nostr Relays (relay.damus.io, nos.lol, etc.)        │
│  Event stream sourced via WebSocket NIP-01                        │
└──────────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| **Crawler Entry** | Main loop: GetStalePubkeys → FetchAndUpdateFollows → MarkAttempted; signal handling; graceful shutdown | `cmd/crawler/main.go` |
| **Relay Pooling** | Maintain relay connections; subscribe kind 3; handle failures with per-class ejection; forward events | `pkg/crawler/crawler.go` |
| **Dgraph Client** | Upsert kind-3 follow-lists; query stale pubkeys (frontier-first); manage timestamps; chunked writes | `pkg/dgraph/dgraph.go` |
| **Clusterscan Analysis** | Trust propagation; weak-bridge detection; cluster sizing (read-only graph traversal) | `pkg/dgraph/clusterscan.go` |
| **Clusterscan Entry** | CLI: resolve seeds → expand trust closure → find bridges → rank & report CSV/JSON | `cmd/clusterscan/main.go` |
| **Pubkeys Export** | Paginated query for all pubkeys with ≥1 follower; CSV output | `cmd/pubkeys/main.go` |
| **Healthcheck** | Scan for invalid & duplicate pubkeys; optional purge with confirmation | `cmd/healthcheck/main.go` |
| **Discover Relays** | Poll nostr.watch API or NIP-65 for relay URLs; test latency; update config | `cmd/discover-relays/main.go` |
| **Configuration** | Load YAML from `~/deepfry/web-of-trust.yaml`; supply defaults; persist relay updates | `pkg/config/config.go` |

## Pattern Overview

**Overall:** Stale-feed loop with relay-pool parallelism and Dgraph upsert-based deduplication.

**Key Characteristics:**
- **Stale-feed model**: Crawler queries `last_db_update` timestamp to identify pubkeys older than a threshold; keeps database "warm" by continuously re-fetching kind 3 events from live sources
- **Parallel relay subscription**: Multiple relays queried concurrently for the same batch of pubkeys; events combined and written atomically to Dgraph
- **Upsert-based uniqueness**: Dgraph schema marks `pubkey` field @unique + @upsert; prevents duplicate nodes across all writes
- **Exponential backoff with ejection**: Relays fail in one of three classes (transport, filter_rejection, subscription_flap); per-class counters increment; once threshold reached, relay is ejected from pool and removed from config
- **EOSE-based early exit** (Phase 8): Quorum fraction of relays must reach EOSE or error before batch cancels; speeds up processing when majority is done
- **Miss-backoff scheduling** (Phase 8): Pubkeys that miss a fetch (no kind 3 event) get scheduled for retry after exponentially-growing intervals; hits refresh at hit_refresh_cadence
- **Event forwarding**: Crawler optionally forwards all received kind 3 events to a "forward relay" (e.g., a local staging relay)
- **Read-only analysis**: Clusterscan uses only read-only txns; never mutates the graph

## Layers

**Command Entry Points:**
- Purpose: Spawn execution; load configuration; coordinate shutdown
- Location: `cmd/crawler/main.go`, `cmd/clusterscan/main.go`, `cmd/pubkeys/main.go`, `cmd/healthcheck/main.go`, `cmd/discover-relays/main.go`
- Contains: Signal handlers, main loop orchestration, final reporting, CLI flag parsing
- Depends on: `pkg/config`, `pkg/crawler`, `pkg/dgraph`, `go-nostr` (relays in discover-relays)
- Used by: User (binary execution)

**Relay Pool Layer (pkg/crawler):**
- Purpose: Maintain live connections to multiple Nostr relays; subscribe to kind 3; handle per-class failures with threshold-driven ejection
- Location: `pkg/crawler/crawler.go`
- Contains: `Crawler` struct with relay state machine, `FetchAndUpdateFollows` dispatcher, `queryRelay` per-relay goroutine, ejection logic
- Depends on: `github.com/nbd-wtf/go-nostr` (WebSocket relay protocol), `pkg/dgraph` (write events), `pkg/config` (relay URLs, ejection thresholds)
- Used by: `cmd/crawler/main.go`, `cmd/crawler/main_test.go`

**Graph Operations Layer (pkg/dgraph):**
- Purpose: Single access point for all Dgraph mutations and queries; enforce pubkey uniqueness via upsert; provide both write path (AddFollowers) and read path (GetStalePubkeys)
- Location: `pkg/dgraph/dgraph.go` (core write/read/health ops), `pkg/dgraph/clusterscan.go` (read-only analysis)
- Contains: `Client` struct wrapping dgo.Dgraph, methods for upsert, timestamp stamping, chunked writes, staleness queries, validation, pagination, trust propagation
- Depends on: `github.com/dgraph-io/dgo/v210` (gRPC client), `google.golang.org/grpc` (transport)
- Used by: All entry points

**Configuration Layer (pkg/config):**
- Purpose: Load & persist YAML config from `~/deepfry/web-of-trust.yaml`; provide defaults for relays, cluster-scan parameters, backoff intervals
- Location: `pkg/config/config.go`
- Contains: `Config` struct with viper backing, LoadConfig, SaveForwardRelayURL, EjectRelayURL, RemoveRelayURL
- Depends on: `github.com/spf13/viper` (YAML parser), `gopkg.in/yaml.v3` (YAML marshal/unmarshal)
- Used by: All entry points

## Data Flow

### Primary Request Path (Crawler Loop)

1. **Config Load** (`cmd/crawler/main.go:160`)
   - Load `~/deepfry/web-of-trust.yaml` via `pkg/config.LoadConfig()`
   - Relay URLs, Dgraph address, thresholds, backoff params from config or defaults

2. **Crawler Initialization** (`cmd/crawler/main.go:210`)
   - Connect to all configured relays; mark unreachable ones as dead (not ejected yet, per CR-03)
   - Create Dgraph client; ensure schema via `EnsureSchema()`
   - Backfill `next_attempt` for existing nodes (D-06)

3. **Main Loop Start** (`cmd/crawler/main.go:225`)
   - Loop until shutdown or fatal error

4. **Stale Pubkey Selection** (`cmd/crawler/main.go:237`)
   - Call `GetStalePubkeys()` (frontier-first query, page limit 100 by default)
   - Query stale pubkeys: `last_db_update < now() - 24h` OR (`frontier` AND created_at old enough)
   - Seed with first pubkey if database empty

5. **Parallel Relay Query** (`pkg/crawler/crawler.go` FetchAndUpdateFollows)
   - Spawn one goroutine per relay to query the batch of pubkeys
   - Each relay: filter chunked by relay's learned filter cap; subscribe kind 3; collect events in channel
   - EOSE quorum: cancel after quorum fraction responds or timeout expires (Phase 8)
   - Events collected into `eventMap: map[pubkey]*event`; only latest kind3CreatedAt kept per pubkey

6. **Event Validation & Forwarding** (`pkg/crawler/crawler.go`)
   - Validate signature: `event.CheckSignature()`
   - Extract kind 3 p-tags; parse and de-duplicate pubkeys
   - Forward to forward relay if configured (non-blocking, timeout-wrapped)

7. **Dgraph Write** (`pkg/dgraph/dgraph.go:AddFollowers`)
   - Version guard: reject if `kind3CreatedAt < existing kind3CreatedAt` (replaceable event semantics)
   - Remove all existing follows (replace behavior)
   - Chunk followees into 200-item batches
   - Upsert follower node: create if missing, atomically update follows edges and timestamps
   - Set: `kind3CreatedAt` (from event), `last_db_update = now()`, `next_attempt = now() + hit_refresh_cadence` (hit path, D-03)

8. **MarkAttempted** (`pkg/dgraph/dgraph.go`)
   - For each pubkey in batch: if in hitSet, mark as hit; else mark as miss
   - Hit: `last_attempt = now()`, `next_attempt = now() + hit_refresh_cadence`
   - Miss: `last_attempt = now()`, `next_attempt = now() + exponential_backoff(miss_count)` (capped at 7d)
   - Increment `miss_count` on each miss; decrement it on hits (D-04)

9. **Relay Dead Detection & Ejection** (`pkg/crawler/crawler.go:markRelayDead`)
   - Each relay failure (transport, filter_rejection, subscription_flap) increments per-class counter
   - Once counter reaches threshold (transport=10, filter_rejection=3, subscription_flap=5), relay is ejected
   - Call `config.EjectRelayURL(url)` to remove from YAML and in-memory list
   - Log single authoritative line: "Relay ... ejected (class N/threshold)"
   - Non-ejected failures: mark dead, schedule retry with exponential backoff

10. **Batch Complete** (`cmd/crawler/main.go:339`)
    - Log: batch size, hit count, stale remaining, total pubkeys
    - Log: cumulative average Dgraph call duration per call type (success-only timing, D-07)

11. **Loop Repeat**
    - Select next batch of stale pubkeys; repeat from step 4

### Clusterscan Path (Spam Detection, Read-Only)

1. **Load Config & Connect** (`cmd/clusterscan/main.go:39`)
   - Load seed pubkeys from `cfg.SeedPubkeys` (hardcoded admins + crawler seed)
   - Connect to Dgraph; use read-only txns throughout

2. **Resolve Seeds to UIDs** (`cmd/clusterscan/main.go:64`)
   - Call `ResolvePubkeysToUIDs()` to fetch Dgraph UIDs for seed pubkeys
   - Skip any seeds not in graph; warn if < all configured

3. **Trust Closure (Rounds)** (`cmd/clusterscan/main.go:83`)
   - Round 1: trusted = {seed UIDs}
   - Each round: Call `ExpandTrustedSet(trusted_uids, k=2)`
     - Query: "find all pubkeys followed by ≥K trusted accounts"
     - Add new UIDs to trusted set
   - Repeat until no new UIDs added (closure reached)

4. **Find Weak Bridges** (`cmd/clusterscan/main.go:105`)
   - Call `GetWeakBridges(trusted_uids, maxWeight=2, limit=10000)`
   - Query: non-trusted pubkeys touched by 1–2 edges from trusted set
   - Sort by score: (trust-followers / (trust-followers + trust-followees)) * cluster-size
   - Report CSV + JSON with pubkey, weight, cluster stats, kind3CreatedAt

5. **Cluster Sizing** (`pkg/dgraph/clusterscan.go:ClusterBeneath`)
   - For each weak bridge, count nodes reachable via `follows` (depth-limited, default 3)
   - Size filter: ignore clusters < min_cluster_size (default 5)

**State Management:**
- **In-memory relay state**: Connection pool, alive/dead status, per-class failure counters, backoff timers; reset on reconnect (not filterCap)
- **Dgraph durable state**: pubkey, follows edges, kind3CreatedAt, last_db_update, last_attempt, next_attempt, miss_count, frontier flag
- **Config file state**: relay_urls, ejected_relays, cluster-scan seeds, ejection thresholds, EOSE quorum, miss-backoff params

## Key Abstractions

**Relay State Machine:**
- Purpose: Encapsulate per-relay health (alive/dead, failures, backoff, retry timing, filter cap learning)
- Examples: `pkg/crawler/crawler.go` lines 74–107 (`relayState` struct)
- Pattern: Atomic counters for thread-safe failure increment; `completedGen` monotonic counter for batch-generation tracking; probing state for filter cap learning

**Pubkey Stale Selection Query:**
- Purpose: Frontier-first query that balances expanding coverage (new pubkeys) with refreshing known accounts
- Examples: `pkg/dgraph/dgraph.go:GetStalePubkeys()`
- Pattern: Union of (a) frontier nodes (has no followers yet), AND (b) aged nodes (last_db_update old enough); ordered by last_db_update ascending; paginated

**Upsert-Based Write Path:**
- Purpose: Single write operation that guarantees uniqueness and version ordering without explicit locking
- Examples: `pkg/dgraph/dgraph.go:AddFollowers()` lines 158–250
- Pattern: Fetch existing follows in same txn; compute deltas; remove all; conditionally add new; all in one Dgraph mutation; Dgraph @upsert schema prevents duplicates

**Ejection Callback:**
- Purpose: Decouple relay connection logic from config persistence; allow main.go to remove ejected relay from YAML
- Examples: `cmd/crawler/main.go:202` (OnConnectFail callback), `pkg/crawler/crawler.go:309` (markRelayDead calls it)
- Pattern: Function closure passed at Crawler construction; invoked when threshold breached; non-blocking, main loop continues

## Entry Points

**Crawler (`cmd/crawler/main.go`):**
- Location: `cmd/crawler/main.go:138`
- Triggers: User runs `./bin/crawler`; signals SIGINT/SIGTERM for graceful shutdown
- Responsibilities: Load config, create Dgraph+relay clients, loop GetStalePubkeys → FetchAndUpdateFollows → MarkAttempted, report final stats, handle signal-based cancellation

**Clusterscan (`cmd/clusterscan/main.go`):**
- Location: `cmd/clusterscan/main.go:39`
- Triggers: User runs `./bin/clusterscan [flags]`
- Responsibilities: Resolve seed pubkeys, iteratively expand trusted set, query weak bridges, size clusters, rank by score, write CSV + JSON report to `out/` directory

**Pubkeys Exporter (`cmd/pubkeys/main.go`):**
- Location: `cmd/pubkeys/main.go:14`
- Triggers: User runs `./bin/pubkeys`
- Responsibilities: Paginated query for all pubkeys with ≥1 follower, write timestamped CSV to current directory

**Healthcheck (`cmd/healthcheck/main.go`):**
- Location: `cmd/healthcheck/main.go:16`
- Triggers: User runs `./bin/healthcheck [-purge]`
- Responsibilities: Scan for invalid (non-hex, wrong length) pubkeys and duplicate nodes, report counts, optionally delete after confirmation

**Discover Relays (`cmd/discover-relays/main.go`):**
- Location: `cmd/discover-relays/main.go` (not fully shown)
- Triggers: User runs `./bin/discover-relays [flags]`
- Responsibilities: Poll nostr.watch API for online relay URLs, test latency (NIP-11, connect, subscribe), filter by performance thresholds, update config YAML

## Architectural Constraints

- **Threading:** Single-threaded event loop in main (cmd/crawler); concurrent relay subscriptions via goroutines in FetchAndUpdateFollows (one per relay); mutex-protected Dgraph writes (dbUpdateMutex) to serialize mutations
- **Global state:** Relay connection pool (slice of relayState, one exclusive ref in Crawler); Crawler holds single dgraph.Client ref; no module-level singletons
- **Circular imports:** None detected; pkg/crawler depends on pkg/dgraph and pkg/config; pkg/config is a leaf; no reverse dependencies
- **Dgraph schema uniqueness:** pubkey field @unique + @upsert; enforced at database level; upsert blocks in AddFollowers guarantee at-most-one node per pubkey
- **Message size limits:** gRPC default ~4MB receive cap; large follow-lists (>10k) chunked into 200-item batches in AddFollowers to stay under limit; NewClient sets maxRecvMsgSize=256MB for large frontier queries
- **Generation tokens:** Per-batch batchSeq counter prevents hung relay goroutines from blocking FetchAndUpdateFollows; completedGen tracks batch generation per relay; stale goroutine's older generation will never match current batch, so it's always marked outstanding and closed (WR-01)
- **Stale-feed model:** Crawler depends on last_db_update and kind3CreatedAt timestamps being monotonically consistent; version guard in AddFollowers ensures older events do not overwrite newer ones; frontier flag marks nodes with no followers for prioritized re-fetch

## Anti-Patterns

### Large Follow-Lists Causing gRPC Timeouts

**What happens:** Follow-lists with 10k+ p-tags are batched in AddFollowers, but if a relay returns a single malformed or oversized event, the mutation still fails with ResourceExhausted after all batches succeed.

**Why it's wrong:** The follower's follow set is partially written (early batches succeeded), but the txn is discarded; no rollback guarantee in gRPC means the state is inconsistent until the next successful AddFollowers call.

**Do this instead:** Chunk the follow-list BEFORE querying followees (in AddFollowers, which already does this); validate p-tags and reject malformed pubkeys outright in event validation (pkg/crawler); use a size-scaled timeout (deadline = baseTimeout + batches * perBatchTimeout) to avoid premature context cancellation.

### Relay Dead-Lock Without Automatic Removal

**What happens:** A relay fails repeatedly but is never removed from the pool; it accumulates retries and consumes goroutines in FetchAndUpdateFollows, slowing down the entire batch.

**Why it's wrong:** The crawler stalls waiting for a dead relay to timeout; threshold-based ejection ensures it is removed once a per-class counter reaches the configured limit (e.g., 10 transport failures).

**Do this instead:** Per-relay failure counters (failTransport, failFilterRej, failSubFlap) increment on each failure; once any counter reaches the class threshold, call markRelayDead() with ejection (via onConnectFail callback); remove from YAML and in-memory pool; log exactly one authoritative line per ejection.

### Event Deduplication Race with Multiple Relays

**What happens:** Two relays both return the same kind 3 event for the same pubkey, and both attempt concurrent writes to Dgraph; the first succeeds, the second fails because of version guard or races AddFollowers.

**Why it's wrong:** kind 3 is a replaceable event; Dgraph upsert semantics guarantee only one node exists, but concurrent writes can cause one thread to see stale state, overwrite with older event data, or leave edges in inconsistent state.

**Do this instead:** Collect all events for a pubkey in a map (eventMap[pubkey] = event); keep only the highest kind3CreatedAt before any writes; batch all pubkey updates in a single AddFollowers call per batch; Dgraph @upsert + schema guards ensure atomicity.

---

*Architecture analysis: 2026-06-16*
