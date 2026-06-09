<!-- refreshed: 2026-06-09 -->
# Architecture

**Analysis Date:** 2026-06-09

## System Overview

The web-of-trust module is a modular Nostr crawler that subscribes to kind-3 (contact list) events from multiple relays and stores the ID-only pubkey follow-graph in Dgraph. It never stores event payloads—all event data lives in StrFry LMDB; only the follow graph (pubkey relationships) is stored in Dgraph.

```text
┌────────────────────────────────────────────────────────────────┐
│                      Entry Points                              │
├─────────────────────┬──────────────────┬──────────────────────┤
│  Crawler (main)     │  Clusterscan     │  Utilities           │
│  `cmd/crawler`      │  `cmd/clusterscan│  (pubkeys, health-   │
│                     │  `cmd/discover   │   check, discover)   │
└─────────────────────┴──────────────────┴──────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────┐
│                 Core Crawler Loop                              │
│         `pkg/crawler` (FetchAndUpdateFollows)                  │
│  • Relay management (connect, reconnect, backoff)              │
│  • Kind 3 event subscriptions from multiple relays             │
│  • Event validation (signature, pubkey format)                 │
│  • Follow-list chunking for large lists (>10k)                │
└────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────┐
│              Dgraph Abstraction Layer                          │
│            `pkg/dgraph` (Client)                              │
│  • Upsert-based pubkey nodes with @unique @index(exact)        │
│  • Follow edge mutations (add, remove, replace)                │
│  • Stale pubkey queries (for crawler feed)                     │
│  • Graph traversal queries (clusterscan: trust, bridges)       │
└────────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────┐
│              Dgraph (gRPC gw + DQL queries)                    │
│          localhost:9080 (configurable)                         │
│  • Schema: Profile type with pubkey, follows, timestamps       │
└────────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| **Crawler** | Main event loop; fetches stale pubkeys; orchestrates relay queries; forwards events | `cmd/crawler/main.go` |
| **Crawler (pkg)** | Relay connection pool; kind 3 subscription; event validation; chunked Dgraph writes | `pkg/crawler/crawler.go` |
| **Chunks** | Splits large follow-lists into batches to prevent timeouts | `pkg/crawler/chunks.go` |
| **Dgraph Client** | Upsert pubkeys; query/mutate follows edges; timestamp management | `pkg/dgraph/dgraph.go` |
| **Clusterscan (pkg)** | Trust propagation; weak-bridge detection; cluster sizing queries | `pkg/dgraph/clusterscan.go` |
| **Clusterscan (cmd)** | Main CLI; reads trust roots; ranks spam clusters; CSV/JSON reports | `cmd/clusterscan/main.go` |
| **Config** | Load YAML from `~/deepfry/web-of-trust.yaml`; relay URLs; cluster settings | `pkg/config/config.go` |
| **Discover Relays** | NIP-65 relay discovery; relay latency testing; config update | `cmd/discover-relays/main.go` |
| **Pubkeys Exporter** | Export all pubkeys with 1+ followers to CSV | `cmd/pubkeys/main.go` |
| **Healthcheck** | Scan for invalid/duplicate pubkeys; optional purge | `cmd/healthcheck/main.go` |

## Pattern Overview

**Overall:** Event-driven fan-out with request batching. Multiple relay sources feed a single crawler loop that processes kind-3 events sequentially per pubkey (but concurrently across relays). Follow updates are batched and upser-ted into Dgraph for idempotent deduplication.

**Key Characteristics:**
- **Upsert-based dedup:** Pubkey uniqueness enforced via Dgraph @unique + @upsert schema
- **Stale-feed:** Crawler queries `last_db_update` timestamp to identify outdated pubkeys; restarts from seed if empty
- **Graceful relay degradation:** Failed relays removed from config; dead relays retry with exponential backoff
- **Large-list handling:** Follow lists >10k split into 200-item chunks to avoid gRPC message size limits
- **Read-only graph analysis:** Clusterscan uses read-only queries; no mutations during analysis phase

## Layers

**Entry Point (CLI):**
- Purpose: Spawn crawler loop; handle signals; manage initialization
- Location: `cmd/crawler/main.go`, `cmd/clusterscan/main.go`, `cmd/pubkeys/main.go`, etc.
- Contains: Main loop, config loading, signal handlers, final reporting
- Depends on: `pkg/config`, `pkg/crawler`, `pkg/dgraph`
- Used by: User (binary execution)

**Crawler Layer:**
- Purpose: Subscribe to kind 3 events from multiple Nostr relays; validate and parse follow-lists; coordinate writes to Dgraph
- Location: `pkg/crawler/`
- Contains: `Crawler` struct, relay state machines, event subscriptions, chunked writes
- Depends on: `pkg/dgraph`, `go-nostr` (relay subscribe/publish)
- Used by: `cmd/crawler/main.go`

**Dgraph Abstraction Layer:**
- Purpose: Encapsulate all Dgraph mutations and queries; ensure pubkey uniqueness via upsert; provide both crawler writes and read-only queries for analysis
- Location: `pkg/dgraph/`
- Contains: `Client` struct with methods for AddFollowers, GetStalePubkeys, ResolvePubkeysToUIDs, ExpandTrustedSet, GetWeakBridges, ClusterBeneath
- Depends on: `dgo/v210` (Dgraph gRPC client)
- Used by: Crawler, Clusterscan, Pubkeys exporter, Healthcheck

**Config Layer:**
- Purpose: Load YAML config from `~/deepfry/web-of-trust.yaml`; supply defaults; manage runtime config mutations (relay updates)
- Location: `pkg/config/config.go`
- Contains: `Config` struct, LoadConfig, SaveForwardRelayURL, RemoveRelayURL
- Depends on: `spf13/viper` (YAML parser)
- Used by: All entry points

## Data Flow

### Primary Request Path (Crawler Loop)

1. **Initialization** (`cmd/crawler/main.go:20-89`)
   - LoadConfig from `~/deepfry/web-of-trust.yaml`
   - Create Dgraph client; call EnsureSchema
   - Create Crawler (connects to all relay URLs)

2. **Main Loop** (`cmd/crawler/main.go:98-165`)
   - Query GetStalePubkeys from Dgraph, ordered by staleness
   - If DB is empty, seed with configured seed pubkey
   - Batch limit to 500 pubkeys per iteration
   - Call FetchAndUpdateFollows

3. **Relay Query** (`pkg/crawler/crawler.go:260-432`)
   - Launch concurrent goroutines (one per alive relay)
   - Each relay: Create Filter{Kinds:[3], Authors:pubkeys}, subscribe
   - Collect kind 3 events from all relays; de-dup by event ID
   - Validate signature; skip if older than DB version
   - Forward to forward relay if configured

4. **Event Processing** (`pkg/crawler/crawler.go:497-553`)
   - Extract p-tags from kind 3 event
   - De-dup p-tags in-memory
   - If >10k follows: call processFollowsInChunks (200-item batches)
   - Otherwise: call dgClient.AddFollowers once

5. **Dgraph Update** (`pkg/dgraph/dgraph.go:72-313`)
   - Upsert follower node (pubkey signer)
   - Query existing follows for replacement (kind 3 is replaceable)
   - Bulk-query all followees (single DQL query with all pubkeys)
   - Create missing followees as stub nodes
   - Delete old follows; create new follows edges; commit transaction

6. **Completion**
   - Return count of pubkeys with events
   - Update final stats; log final report

**State Management:**
- Dgraph is source-of-truth for all pubkeys and follows
- `last_db_update` (unix timestamp) tracks when each pubkey's follow-list was last queried
- `kind3CreatedAt` stores the event's created_at time for version checking
- Relay connection state (alive, failures, backoff timers) held in-memory; lost on restart

### Clusterscan Path (Spam Detection)

1. **Load Config** (`cmd/clusterscan/main.go:40-73`)
   - Read seed pubkeys from config
   - Dgraph client creation

2. **Phase 0: Resolve Seeds** (`pkg/dgraph/clusterscan.go:45-88`)
   - Call ResolvePubkeysToUIDs for all seed pubkeys (trusted roots)
   - Fail if none exist in graph

3. **Phase 1: Trust Propagation** (`cmd/clusterscan/main.go:82-99`)
   - Loop ExpandTrustedSet until no new nodes added (k-step closure)
   - Each round: find nodes with ≥K trusted followers; add to trusted set

4. **Phase 2: Weak Bridges** (`cmd/clusterscan/main.go:109-117`)
   - Call GetWeakBridges: find non-trusted nodes with 1..maxWeight edges to trusted
   - Sort by weight; truncate at limit if needed

5. **Phase 3: Cluster Sizing** (`cmd/clusterscan/main.go:119-153`)
   - For each bridge: call ClusterBeneath to walk follows tree up to depth
   - Filter members: keep only non-trusted
   - Score = cluster_size / weight; rank by score

6. **Output**
   - Write timestamped CSV (rank, pubkey, metrics)
   - Write timestamped JSON (same + optional member lists)

## Key Abstractions

**Crawler:**
- Purpose: Manages relay pool and coordinates follow-list fetches from multiple sources
- Examples: `cmd/crawler/main.go` uses `crawler.FetchAndUpdateFollows()`
- Pattern: Relay state machine with exponential backoff; subscription-based event collection; concurrent execution across relays; sequential writing to Dgraph (mutex-protected)

**Dgraph Client:**
- Purpose: Single access point for all graph queries and mutations
- Examples: All entry points use `dgraph.NewClient(addr)` and call methods on the returned `*Client`
- Pattern: Upsert-based writes (idempotent); transaction-wrapped mutations; read-only queries for analysis; pagination helpers for large result sets

**Config:**
- Purpose: Centralize all environment configuration; support runtime persistence
- Examples: YAML from `~/deepfry/web-of-trust.yaml` loaded into `Config` struct; RemoveRelayURL updates config file on relay failures
- Pattern: Viper-backed; defaults for relays and cluster-scan parameters; support for both hex and npub pubkey formats

## Entry Points

**Crawler:**
- Location: `cmd/crawler/main.go`
- Triggers: User runs `./bin/crawler`; signal SIGINT/SIGTERM for graceful shutdown
- Responsibilities: Load config, connect to relays, loop GetStalePubkeys → FetchAndUpdateFollows, report stats

**Clusterscan:**
- Location: `cmd/clusterscan/main.go`
- Triggers: User runs `./bin/clusterscan [flags]`
- Responsibilities: Resolve seeds, propagate trust, find weak bridges, size clusters, write CSV/JSON

**Discover Relays:**
- Location: `cmd/discover-relays/main.go`
- Triggers: User runs `./bin/discover-relays [flags]`
- Responsibilities: Poll nostr.watch API or NIP-65 relays for relay URLs, test latency, update config

**Pubkeys Exporter:**
- Location: `cmd/pubkeys/main.go`
- Triggers: User runs `./bin/pubkeys`
- Responsibilities: Query all pubkeys with ≥1 follower, write to timestamped CSV

**Healthcheck:**
- Location: `cmd/healthcheck/main.go`
- Triggers: User runs `./bin/healthcheck [-purge]`
- Responsibilities: Scan for invalid pubkey format and duplicate nodes, optionally delete

## Architectural Constraints

- **Threading:** Single-threaded event loop in crawler main; concurrent relay subscriptions in FetchAndUpdateFollows; mutex-protected Dgraph writes (dbUpdateMutex)
- **Global state:** Relay connection state (alive/dead, backoff timers) stored in relayState slice; Crawler holds exclusive reference to dgClient
- **Circular imports:** None detected; pkg/crawler depends on pkg/dgraph; pkg/config is leaf
- **Dgraph schema uniqueness:** Pubkey field marked @unique and @upsert; guarantees at most one node per pubkey across all writes
- **Message size limits:** Large follow-lists (>10k) chunked into 200-item batches to stay under gRPC limit (~4MB)
- **Stale-feed model:** Crawler depends on last_db_update timestamps being monotonically increasing; kind3CreatedAt version check prevents older events from overwriting newer ones

## Anti-Patterns

### Large Follow-Lists Causing Timeouts

**What happens:** If a pubkey follows >10k accounts, a single Dgraph mutation with all edges hits gRPC message size or execution timeout limits.

**Why it's wrong:** Mutation fails; follow-list update is lost; pubkey marked stale again on next loop.

**Do this instead:** `pkg/crawler/chunks.go` splits follow-lists into 200-item chunks. FetchAndUpdateFollows checks list size and routes to processFollowsInChunks if needed. Each chunk gets its own timeout context.

### Event Deduplication Race with Multiple Relays

**What happens:** Two relays deliver the same kind 3 event (same event.ID) concurrently; both goroutines process it.

**Why it's wrong:** Duplicate pubkey nodes created; follow edges added twice (idempotent in Dgraph, but wasteful).

**Do this instead:** `pkg/crawler/crawler.go:321-372` maintains processedEventIDs map (within dbUpdateMutex) and skips already-processed events. Mutex ensures visibility across relay goroutines.

### Relay Dead-Lock Without Automatic Removal

**What happens:** A relay consistently fails; crawler keeps retrying it every 5 minutes, filling logs with warnings.

**Why it's wrong:** Wastes bandwidth and CPU; doesn't signal the user.

**Do this instead:** `pkg/crawler/crawler.go:170-199` marks relay dead after maxConsecutiveFailures (5); calls onConnectFail callback. Crawler main registers callback that removes URL from config file. Next restart won't try the dead relay.

## Error Handling

**Strategy:** Log all errors; continue processing. No panic. Network errors are expected and gracefully degraded. Missing/stale data is a normal condition.

**Patterns:**
- Relay errors (subscribe, transport): Mark relay dead and log; continue with remaining relays
- Dgraph errors (query, mutation): Log with context; propagate up to main loop; break and restart
- Config load errors: Fatal (no recovery)
- Invalid pubkeys in event: Log and skip; continue processing other pubkeys
- Large follow-lists: Chunk and retry with smaller batches

## Cross-Cutting Concerns

**Logging:** All components use `log` package. Metrics logged as JSON-structured logs (e.g., `METRICS: {...}`) for external parsing. Debug flag enables verbose relay/query logs.

**Validation:** 
- Pubkey format: `nostr.GetPublicKey()` used everywhere (hex, 64 chars)
- Event signature: `event.CheckSignature()` required before processing
- Follow-list integrity: P-tags parsed and de-duped; invalid pubkeys skipped

**Authentication:** Crawler publishes events to forward relay; uses configured signing key for event creation (via event-forwarder flow, not in crawler itself; crawler only forwards events it receives).

---

*Architecture analysis: 2026-06-09*
