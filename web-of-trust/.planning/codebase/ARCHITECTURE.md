<!-- refreshed: 2026-06-15 -->
# Architecture

**Analysis Date:** 2026-06-15

## System Overview

DeepFry is a modular, multi-subsystem backend stack surrounding a **stock (unmodified) StrFry Nostr relay** with sidecar services for event forwarding, trust scoring, graph analysis, and GraphQL access. The core principle: never fork StrFry — extend via its JSON stdin/stdout plugin interface and external services.

```text
┌────────────────────────────────────────────────────────────────────┐
│                       Nostr Ecosystem                              │
│   Upstream Relays ──→ Public Clients  ←──  StrFry Relay (port 7777)
└────────────────────────────────────────────────────────────────────┘
         ▲                                           ▲     ▲
         │                                           │     │
    Event                                    Plugin (in-process)
   Forwarder                                         │     │
    (Go, per-source)                         Whitelist │ Search
         │                                    Plugin   └─→ Plugin
         │                                      │
         │                                      ▼
         │                              ┌──────────────────┐
         │                              │ Dgraph (port 8080│
         │                              │  gRPC 9080)      │
         │                              │ Whitelist Server │
         │                              │ (port 8081)      │
         │                              └──────────────────┘
         │                                      ▲
         │                                      │
    ┌────┴──────────────────────────────────────┤
    │                                            │
    ▼                                            ▼
Web of Trust Crawler (Go) ─ reads kind 3  Clusterscan (Go)
discovers pubkey graph  ─ writes  pubkeys  trust propagation
                           & follows        spam analysis

Quarantine Rescuer (Go)  ─ one-shot CLI: re-qualify and replay
                            quarantine events into mainline

LMDB to GraphQL (Rust)    ─ read-only StrFry LMDB → GraphQL API
                            (lmdb2graphql service)
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| **StrFry Relay** | Canonical event storage (LMDB); fast ingest/distribution; NIP-01 WebSocket | `config/strfry/strfry.conf` |
| **Whitelist Plugin** | StrFry plugin; JSON stdin/stdout; accept/reject writes based on Dgraph query | `whitelist-plugin/cmd/whitelist/main.go` |
| **Whitelist Server** | Centralized pubkey cache; refreshes from Dgraph; serves `/check` endpoint | `whitelist-plugin/cmd/server/main.go` |
| **Router Plugin** | StrFry plugin; quarantine writes from unknown pubkeys to secondary relay | `whitelist-plugin/cmd/router/main.go` |
| **Event Forwarder** | Subscribe upstream relays; republish events to StrFry; windowed sync or realtime | `event-forwarder/cmd/fwd/main.go` |
| **Web of Trust Crawler** | Subscribe kind 3 from StrFry; discover pubkeys; write graph to Dgraph | `web-of-trust/cmd/crawler/main.go` |
| **Clusterscan** | Analyze trust propagation; detect spam clusters; output CSV/JSON reports | `web-of-trust/cmd/clusterscan/main.go` |
| **Pubkeys Exporter** | Query Dgraph for all pubkeys with ≥1 follower; write CSV export | `web-of-trust/cmd/pubkeys/main.go` |
| **Discover Relays** | NIP-65 relay discovery; test latency; update config | `web-of-trust/cmd/discover-relays/main.go` |
| **Healthcheck** | Scan Dgraph for invalid/duplicate pubkeys; optionally purge | `web-of-trust/cmd/healthcheck/main.go` |
| **Quarantine Rescuer** | One-shot CLI: re-publish quarantine events for whitelisted pubkeys; delete from quarantine | `quarantine-rescuer/cmd/quarantine-rescue/main.go` |
| **LMDB to GraphQL** | Read-only StrFry LMDB accessor; GraphQL API over Rust axum; fail-closed startup | `spam/src/main.rs` |

## Pattern Overview

**Overall:** Multi-layer, service-oriented architecture with clear separation of concerns.

**Key Characteristics:**
- **Unmodified StrFry core:** All extension via JSON plugin protocol and external services
- **ID-only graphs:** Dgraph stores only pubkey relationships; event payloads stay in StrFry LMDB
- **Stateless plugins:** Whitelist/Router plugins are stateless; Dgraph is the only truth for whitelists
- **Graceful degradation:** Failed relays removed from config; write rejections quarantined automatically
- **Chunked writes:** Large follow-lists split into 200-item batches to avoid gRPC limits
- **Fail-closed startup:** LMDB to GraphQL service returns 503 on /graphql until schema validation passes

## Layers

**Data Layer:**
- Purpose: Persistent storage for events, graphs, and relationships
- Location: `config/strfry/` (StrFry LMDB config), `config/dgraph/` (Dgraph schema), `spam/src/lmdb/` (LMDB reader)
- Contains: LMDB schema (StrFry), Dgraph GraphQL schema, LMDB metadata validation
- Depends on: None (foundational)
- Used by: All other layers read from or write to these stores

**Event Ingest Layer:**
- Purpose: Bring external Nostr events into StrFry; apply write policy
- Location: `event-forwarder/cmd/fwd/`, `whitelist-plugin/cmd/whitelist/`, `whitelist-plugin/cmd/router/`
- Contains: Relay subscription, event forwarding, accept/reject filtering, quarantine forwarding
- Depends on: StrFry (JSON plugin protocol), Dgraph (whitelist queries), WebSocket/gRPC clients
- Used by: Public Nostr clients, upstream relays

**Graph Building Layer:**
- Purpose: Extract trust relationships from events; maintain pubkey graph
- Location: `web-of-trust/pkg/crawler/`, `web-of-trust/pkg/dgraph/`, `web-of-trust/cmd/crawler/`
- Contains: Kind 3 subscription, follow-list parsing, upsert with dedup, stale-pubkey queries
- Depends on: StrFry (kind 3 subscribe), Dgraph (pubkey mutations/queries), go-nostr (relay client)
- Used by: Trust analysis (Clusterscan), whitelist lookups

**Analysis Layer:**
- Purpose: Compute trust metrics; detect spam patterns; export data for downstream systems
- Location: `web-of-trust/cmd/clusterscan/`, `web-of-trust/cmd/pubkeys/`, `web-of-trust/pkg/dgraph/clusterscan.go`
- Contains: Trust propagation queries, weak-bridge detection, cluster sizing, CSV export
- Depends on: Dgraph (read-only queries)
- Used by: Operators, downstream analytics systems

**Access & Query Layer:**
- Purpose: Expose event data via GraphQL; single read-only accessor for StrFry LMDB
- Location: `spam/src/main.rs`, `spam/src/graphql/`, `spam/src/query/`, `spam/src/server.rs`
- Contains: LMDB reader, query engine, GraphQL schema, axum HTTP server
- Depends on: StrFry LMDB (read-only), tokio (async runtime)
- Used by: Public GraphQL clients

**Quarantine Recovery Layer:**
- Purpose: Re-qualify events from quarantine relay; replay into mainline when author becomes whitelisted
- Location: `quarantine-rescuer/cmd/quarantine-rescue/main.go`, `quarantine-rescuer/internal/`
- Contains: LMDB reader (quarantine), whitelist checker, event forwarder, batch deleter
- Depends on: StrFry LMDBs (both mainline and quarantine), whitelist server, docker CLI
- Used by: Operators (one-shot CLI)

## Data Flow

### Primary Request Path (Crawler Loop — Trust Graph Discovery)

1. **Config Load** (`web-of-trust/cmd/crawler/main.go:~200`) — Load seed relays and Dgraph address from `~/deepfry/web-of-trust.yaml`
2. **Relay Connect** (`web-of-trust/pkg/crawler/crawler.go:~300`) — Open WebSocket subscriptions to all seed relays
3. **Query Stale Pubkeys** (`web-of-trust/pkg/dgraph/dgraph.go:GetStalePubkeys()`) — Find pubkeys with `last_db_update` older than cutoff
4. **Batch Kind 3 Subscribe** (`web-of-trust/pkg/crawler/crawler.go:FetchAndUpdateFollows()`) — Subscribe kind 3 for stale pubkeys across all relays
5. **Collect Events** — Accumulate kind 3 events on each relay; extract follow-lists (P-tags)
6. **Validate Pubkeys** (`web-of-trust/pkg/crawler/crawler.go:~500`) — Verify signature, normalize hex pubkey, check length
7. **Chunk Large Lists** (`web-of-trust/pkg/crawler/chunks.go`) — Split follow-lists >10k into 200-item batches
8. **Upsert to Dgraph** (`web-of-trust/pkg/dgraph/dgraph.go:AddFollowers()`) — Atomic @upsert mutations with deduplication; update `last_db_update` timestamp
9. **Handle Errors** (`web-of-trust/cmd/crawler/main.go:isDgraphTransient()`) — Retry transient gRPC errors (Unavailable, DeadlineExceeded) indefinitely with exponential backoff; fail-fast on fatal (ResourceExhausted, etc.)
10. **Report Stats** (`web-of-trust/cmd/crawler/main.go:~600`) — Log cumulative pubkey counts, call metrics (avg latency per operation)

**State Management:**
- StrFry is the canonical source for all kind 3 events; relays are ephemeral
- Dgraph owns all pubkey state: `pubkey`, `follows`, `followers`, `kind3CreatedAt`, `last_db_update`
- Relay connection state (alive/dead, backoff timers) held in-memory; lost on restart
- Timestamps enforce ordering: `kind3CreatedAt` prevents older events from overwriting newer; `last_db_update` drives staleness

### Whitelist Request Path (Event Write Acceptance)

1. **StrFry Receives Event** — Event arrives at plugin stdin as JSON
2. **Whitelist Plugin Reads** (`whitelist-plugin/cmd/whitelist/main.go:~44`) — Parse JSONL, extract pubkey
3. **Cache Lookup** (`whitelist-plugin/pkg/handler/whitelist_handler.go`) — Check in-memory cache (refreshed on startup)
4. **If Miss: Query Server** (`whitelist-plugin/pkg/client/client.go:Check()`) — Call `/check/{pubkey}` on whitelist server (default 50ms timeout)
5. **Server Queries Dgraph** (`whitelist-plugin/cmd/server/main.go`) — Fetch pubkey from Dgraph; return 200/404
6. **Plugin Returns Decision** — Write `accept` or `reject` to StrFry stdout
7. **Quarantine Fallback** (if router plugin enabled) — Rejected events forwarded to `strfry-quarantine` instead

### Quarantine Rescue Path (Event Re-qualification)

1. **Operator Runs CLI** (`quarantine-rescuer/cmd/quarantine-rescue/main.go`) — `./quarantine-rescue --main-relay ws://localhost:7777`
2. **Load Quarantine LMDB** (`quarantine-rescuer/internal/lmdbreader/reader.go`) — Open read-only handle to quarantine DB
3. **Export Events** (`quarantine-rescuer/internal/exporter/exporter.go`) — Scan quarantine; extract event metadata (pubkey, event ID, raw JSON)
4. **Check Whitelist** (`quarantine-rescuer/internal/whitelist/client.go`) — Parallel /check requests for each unique pubkey (default 8 concurrent)
5. **Forward Qualified** (`quarantine-rescuer/internal/forwarder/forwarder.go`) — Publish whitelisted events to mainline StrFry (sequential per pubkey; 4 pubkeys in parallel)
6. **Batch Delete** (`quarantine-rescuer/internal/deleter/deleter.go`) — Call `strfry delete` via docker exec to remove successfully-forwarded events
7. **Report Summary** — Log success/failure counts; exit

### GraphQL Query Path (Read-Only StrFry Access)

1. **Client POST /graphql** → request query JSON
2. **Schema Readiness Gate** (`spam/src/main.rs:~40–100`) — Return 503 if `ready` flag is false (startup not complete)
3. **Query Router** (`spam/src/server.rs:build_router()`) — Match request to GraphQL resolver
4. **Hydrate LMDB** (`spam/src/query/hydrate.rs`) — Open read-only LMDB cursor; decode event payloads
5. **Filter & Merge** (`spam/src/query/filter.rs`, `spam/src/query/merge.rs`) — Apply filters, concatenate pages
6. **Return Response** — JSON-encode result; 200 OK

## Key Abstractions

**Relay State Machine:**
- Purpose: Track relay health across multiple subsystems
- Examples: `web-of-trust/pkg/crawler/crawler.go:relayState`, relay connection pool in forwarder
- Pattern: Exponential backoff on failure; marked dead after threshold; automatically retried on recovery

**Follow-List Chunking:**
- Purpose: Split large P-tag lists into batches to avoid gRPC message size limits (~4MB)
- Examples: `web-of-trust/pkg/crawler/chunks.go:ChunkFollows()`
- Pattern: Split into 200-item chunks; retry each chunk independently; timestamp consistency maintained across batch

**Upsert Deduplication:**
- Purpose: Idempotent pubkey writes with guaranteed uniqueness
- Examples: `web-of-trust/pkg/dgraph/dgraph.go:AddFollowers()` — DQL @upsert + @unique schema
- Pattern: All pubkey writes are upserts; identical requests produce identical Dgraph state

**Dgraph Transaction Isolation:**
- Purpose: Ensure consistency of follow-list writes
- Examples: All write operations wrapped in `txn := dg.NewTxn()` → `txn.Mutate(ctx, req)`
- Pattern: Atomic mutation; commit or abort; single-threaded main loop reduces contention

**Plugin JSON Protocol:**
- Purpose: StrFry stdin/stdout interface for custom handlers
- Examples: Whitelist plugin reads JSONL events; router plugin re-publishes to quarantine
- Pattern: One event per line; parse/process/respond synchronously; fail-closed (reject on error)

## Entry Points

**Web of Trust Crawler:**
- Location: `web-of-trust/cmd/crawler/main.go`
- Triggers: User runs `./bin/crawler`; graceful shutdown on SIGINT/SIGTERM
- Responsibilities: Load config, connect to relays, loop GetStalePubkeys → FetchAndUpdateFollows, report cumulative stats

**Clusterscan:**
- Location: `web-of-trust/cmd/clusterscan/main.go`
- Triggers: User runs `./bin/clusterscan [--seeds pubkey,pubkey] [--output csv|json]`
- Responsibilities: Resolve seed pubkeys to UIDs; propagate trust; find weak bridges; size clusters; write report

**Event Forwarder:**
- Location: `event-forwarder/cmd/fwd/main.go`
- Triggers: Docker or binary; reads env vars for source relay URL, mode (windowed/realtime)
- Responsibilities: Subscribe upstream relay; sync window or realtime mode; republish to StrFry; CLI or TUI mode

**Whitelist Plugin (StrFry Plugin):**
- Location: `whitelist-plugin/cmd/whitelist/main.go`
- Triggers: StrFry spawns as plugin subprocess; inherits stdin/stdout/stderr
- Responsibilities: Read JSONL events from stdin; query whitelist server; write accept/reject to stdout

**Whitelist Server:**
- Location: `whitelist-plugin/cmd/server/main.go`
- Triggers: Docker service on port 8081
- Responsibilities: Load config; query Dgraph on `/check/{pubkey}`; serve health endpoint

**Router Plugin (StrFry Plugin):**
- Location: `whitelist-plugin/cmd/router/main.go`
- Triggers: StrFry spawns as plugin subprocess (if configured in strfry.conf)
- Responsibilities: Read JSONL events from stdin; forward rejected events to quarantine relay; write response to stdout

**Quarantine Rescuer:**
- Location: `quarantine-rescuer/cmd/quarantine-rescue/main.go`
- Triggers: Operator runs CLI with flags (--dry-run, --limit, --batch-size, etc.)
- Responsibilities: Scan quarantine LMDB; check whitelist; forward qualified events; delete from quarantine

**LMDB to GraphQL Service:**
- Location: `spam/src/main.rs`
- Triggers: Docker service on port 8082 (or configured bind_address)
- Responsibilities: Load config; bind listener; initialize LMDB reader; validate schema; serve GraphQL queries; return 503 until ready

**Pubkeys Exporter:**
- Location: `web-of-trust/cmd/pubkeys/main.go`
- Triggers: User runs `./bin/pubkeys`
- Responsibilities: Query Dgraph for all pubkeys with ≥1 follower; write timestamped CSV to stdout or file

**Discover Relays:**
- Location: `web-of-trust/cmd/discover-relays/main.go`
- Triggers: User runs `./bin/discover-relays [--source nostr.watch|nip65]`
- Responsibilities: Poll relay discovery endpoint; test latency; update config file with new relays

**Healthcheck:**
- Location: `web-of-trust/cmd/healthcheck/main.go`
- Triggers: User runs `./bin/healthcheck [-purge]`
- Responsibilities: Scan Dgraph for invalid pubkey format; detect duplicate nodes; optionally delete malformed records

## Architectural Constraints

- **Single StrFry instance:** All events flow through one relay; plugins run in-process
- **Stateless plugins:** Whitelist/router plugins have no persistent state; Dgraph is the only truth
- **LMDB read-only by GraphQL:** Spam service never writes to StrFry LMDB; reads via read-only cursor
- **Quarantine isolation:** Quarantine LMDB path enforced to differ from mainline (guard script prevents mount misconfiguration)
- **Relay connection pooling:** One Nostr relay connection per upstream source; one subscription per relay
- **Dgraph upsert dedup:** Pubkey uniqueness enforced via @unique schema; all writes are idempotent
- **Message size limit:** gRPC ~4MB cap requires chunking large follow-lists into 200-item batches
- **Startup gate (LMDB to GraphQL):** Schema cell populated only after LMDB validation passes; /graphql returns 503 until ready
- **Single-threaded main loops:** Crawler, forwarder, and rescuer all use single-threaded event loops (mutex protects Dgraph writes in crawler)
- **Context-based cancellation:** All async operations respect context.Context for graceful shutdown
- **Config file locations:** Web of Trust and whitelist config live at `~/deepfry/` (never overwritten; use temp HOME for testing)

## Anti-Patterns

### Large Follow-Lists Causing Timeouts (CHUNKING-01)

**What happens:** A single kind 3 event with >10k P-tags is upserted as one gRPC request, exceeding ~4MB message limit; gRPC returns ResourceExhausted; indefinite retry would livelock

**Why it's wrong:** Follow-lists are unbounded; many real Nostr pubkeys follow >10k accounts; without chunking, these events silently fail

**Do this instead:** Split into 200-item chunks before upsert (`web-of-trust/pkg/crawler/chunks.go:ChunkFollows()`); each chunk submitted independently; timestamp consistency maintained across batch

### Relay Dead-Lock Without Automatic Removal (RELAY-01)

**What happens:** A relay goes dead; crawler retries indefinitely with backoff; config file is never updated; operator unaware until manual restart

**Why it's wrong:** Failed relays accumulate; connection pool grows; crawler wastes time on known-bad endpoints

**Do this instead:** After threshold (e.g., 10 consecutive failures), mark relay dead and log loudly; remove from config via `RemoveRelayURL()` so restarted crawler skips it; operator alerted in logs

### Event Deduplication Race with Multiple Relays (DEDUP-01)

**What happens:** Same kind 3 event arrives from multiple relays in parallel; concurrent FetchAndUpdateFollows calls could race on Dgraph writes

**Why it's wrong:** Pubkey nodes might be created twice; follows edges duplicated; inconsistent graph state

**Do this instead:** Dgraph @upsert + @unique schema guarantees idempotent writes; all concurrent requests converge to same final state; no explicit locking needed at crawler level

### Whitelist Server Unavailable During Plugin Startup (FAIL-SAFE-01)

**What happens:** StrFry starts whitelist plugin before whitelist-server is ready; plugin cannot reach server on init; events start arriving; all rejected because cache empty

**Why it's wrong:** Writes are blocked until server becomes reachable; users see connection refused

**Do this instead:** Plugin logs warning and continues with empty cache; all events rejected until server reachable; once server responds, cache refreshes and accepts resume (whitelist-plugin/cmd/whitelist/main.go:~32)

### LMDB to GraphQL Not Ready on Startup (STARTUP-GATE-01)

**What happens:** Spam service binds listener, client connects, POSTs /graphql, but LMDB validation still running; query returns stale/incomplete schema

**Why it's wrong:** Queries execute against partially-initialized data; results wrong; client thinks service is ready

**Do this instead:** Bind listener first; serve /ready (always 200), /health (200 once gate passes), and /graphql (503 until gate passes) from same socket; only populate schema cell after LMDB validation completes; clients observe continuous observability without connection-refused gap (spam/src/main.rs:~40–100)

---

*Architecture analysis: 2026-06-15*
