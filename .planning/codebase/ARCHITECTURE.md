<!-- refreshed: 2026-06-10 -->
# Architecture

**Analysis Date:** 2026-06-10

## System Overview

```text
┌──────────────────────────────────────────────────────────────────────┐
│                         Upstream Nostr Relays                        │
│                   (damus.io, nos.lol, relay.primal.net …)            │
└──────────────────────────┬───────────────────────────────────────────┘
                           │  WebSocket (NIP-01)
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│                      event-forwarder (fwd)                           │
│  `event-forwarder/cmd/fwd/main.go`                                   │
│  Windowed + real-time sync; one instance per source relay            │
└──────────────────────────┬───────────────────────────────────────────┘
                           │  WebSocket (NIP-01) publish
                           ▼
┌──────────────────────────────────────────────────────────────────────┐
│               StrFry Relay  (LMDB, ws://localhost:7777)              │
│               UNMODIFIED STOCK BINARY                                │
│  ├── Whitelist Plugin (stdin/stdout JSON protocol)                   │
│  │   `whitelist-plugin/cmd/whitelist/main.go`                        │
│  │   ──► HTTP /check  ──► Whitelist Server                           │
│  │       `whitelist-plugin/cmd/server/main.go`                       │
│  │       ──► GraphQL ──► Dgraph :8080                                │
│  └── Search Plugin (placeholder) `search-plugin/README.md`          │
└──────────────────────────────────────────────────────────────────────┘
         │  NIP-01 subscribe (kind 3)          │  gRPC :9080
         ▼                                     ▼
┌─────────────────────────┐       ┌────────────────────────────────────┐
│  web-of-trust crawler   │──────▶│    Dgraph (ID-only pubkey graph)   │
│  `web-of-trust/cmd/     │ DQL/  │    `config/dgraph/schema.graphql`  │
│   crawler/main.go`      │ gRPC  │    Profile{pubkey,follows,         │
└─────────────────────────┘       │    followers,kind3CreatedAt,       │
                                  │    last_db_update}                 │
┌─────────────────────────┐       └────────────────────────────────────┘
│  quarantine-rescuer     │
│  `quarantine-rescuer/   │  docker exec → quarantine StrFry LMDB
│   cmd/quarantine-rescue/│  HTTP /check → whitelist server
│   main.go`              │  ws://localhost:7777 (publish)
└─────────────────────────┘
```

## Components

| Component | Role | Entry Point |
|-----------|------|-------------|
| **event-forwarder** | Forwards events from upstream relays to local StrFry; supports windowed (historical backfill) and real-time sync modes | `event-forwarder/cmd/fwd/main.go` |
| **whitelist-plugin** (binary) | StrFry JSON plugin: reads event from stdin, calls whitelist server HTTP `/check`, writes accept/reject to stdout | `whitelist-plugin/cmd/whitelist/main.go` |
| **whitelist-server** | HTTP server that holds an in-memory whitelist refreshed from Dgraph; serves `/check` and `/health` endpoints | `whitelist-plugin/cmd/server/main.go` |
| **web-of-trust crawler** | Subscribes to kind 3 events from Nostr relays; upserts pubkey follow-graph into Dgraph | `web-of-trust/cmd/crawler/main.go` |
| **clusterscan** | Read-only graph analysis; trust propagation from seed pubkeys; spam-cluster sizing and weak-bridge detection | `web-of-trust/cmd/clusterscan/main.go` |
| **quarantine-rescuer** | One-shot CLI; exports quarantine LMDB via `docker exec`, checks whitelist, forwards matching events to main relay, deletes from quarantine | `quarantine-rescuer/cmd/quarantine-rescue/main.go` |
| **Dgraph** | Graph database; stores ID-only pubkey relationships (`Profile` nodes with `follows`/`followers` edges) | `config/dgraph/schema.graphql` |
| **StrFry** | Stock Nostr relay; canonical event store (LMDB); extended only via plugin protocol | `docker-compose.strfry.yml` |
| **embeddings-generator** | Placeholder | `embeddings-generator/README.md` |
| **search-plugin** | Placeholder | `search-plugin/README.md` |
| **profile-builder** | Placeholder | `profile-builder/README.md` |
| **thread-inference** | Placeholder | `thread-inference/README.md` |

## Data Flow

### Event Write Path (admission control)

1. Client or event-forwarder publishes EVENT to StrFry WebSocket (`:7777`)
2. StrFry calls whitelist-plugin binary via stdin/stdout JSON protocol
3. `whitelist-plugin/cmd/whitelist/main.go` reads JSON line from stdin, calls `pkg/client.WhitelistClient.IsWhitelisted(pubkey)`
4. Client makes HTTP GET to whitelist-server `/check?pubkey=<hex>` (`whitelist-plugin/pkg/server/`)
5. Whitelist server checks in-memory `pkg/whitelist.Whitelist` (atomic pointer to map, lock-free reads)
6. Plugin writes accept/reject JSON line to stdout; StrFry stores or drops the event

### Whitelist Refresh Path

1. `whitelist-plugin/cmd/server/main.go` starts `pkg/whitelist.WhitelistRefresher` on a ticker
2. Refresher calls `pkg/repository.GraphQLRepository.GetAll()` — GraphQL query to Dgraph `:8080`
3. Dgraph returns all `Profile.pubkey` values with sufficient follower count
4. Refresher calls `Whitelist.UpdateKeys()` — atomic pointer swap, zero-downtime update

### Web-of-Trust Crawl Path

1. `web-of-trust/cmd/crawler/main.go` calls `dgraph.Client.GetStalePubkeys()` (batch of 500)
2. `pkg/crawler.Crawler.FetchAndUpdateFollows()` subscribes to kind 3 events from all configured relays
3. Each kind 3 p-tag list is split into 200-item chunks (`pkg/crawler/chunks.go`)
4. `pkg/dgraph.Client.AddFollowers()` upserts pubkey nodes and follows edges via gRPC (`localhost:9080`)
5. `dgraph.Client.MarkAttempted()` stamps `last_db_update` on all queried pubkeys
6. New pubkeys discovered in p-tags become candidates in the next stale-pubkey batch

### Event Forwarding Path

1. `event-forwarder/cmd/fwd/main.go` loads config; creates `pkg/forwarder.Forwarder`
2. Forwarder starts in windowed mode: queries time-windowed REQ filters against source relay
3. Receives events via NIP-01 EOSE; publishes to StrFry WebSocket (`pkg/relay`)
4. Writes sync progress as kind 30078 events with `d`, `from`, `to` tags (`pkg/nsync`)
5. Transitions to real-time mode when window catches up within `RealtimeToleranceSeconds`

### Quarantine Rescue Path

1. `quarantine-rescuer/cmd/quarantine-rescue/main.go` runs `docker exec strfry-quarantine strfry scan` via `internal/runner.Exec`
2. `internal/exporter.Stream()` parses JSONL event stream, groups by pubkey
3. `internal/whitelist.Client.IsWhitelisted()` checks each pubkey against whitelist-server (concurrent, configurable pool)
4. `internal/forwarder.Forwarder.Forward()` publishes whitelisted events to main relay WebSocket
5. `internal/deleter.Deleter.DeleteByIDs()` calls `strfry delete` inside the quarantine container for successfully forwarded event IDs

## Key Design Decisions

**StrFry stays unmodified.** All admission control, crawling, and search extension happens via StrFry's stdin/stdout JSON plugin protocol and external services. No forks, no patches. This guarantees easy StrFry upgrades.

**Dgraph stores IDs only.** Event payloads (content, tags) live exclusively in StrFry's LMDB. Dgraph holds only pubkey hex strings and `follows`/`followers` edges. This preserves the canonical event store and avoids payload duplication.

**Whitelist server is an in-process cache.** Rather than querying Dgraph on every write, the whitelist-server holds the entire whitelist in memory as a `map[[32]byte]struct{}` behind an `atomic.Pointer`. Reads are lock-free. The list refreshes from Dgraph on a configurable interval without taking a write lock on read paths.

**Plugin fail-closed.** If the whitelist-server is unreachable at startup, the plugin logs a warning and rejects all events until the server is reachable. This prevents unauthenticated writes during transient server restarts.

**One forwarder instance per source relay.** Config and code enforce a single `fwd` process per source relay to avoid race conditions on sync-progress kind 30078 events.

**Stale-feed crawl model.** The crawler does not maintain a persistent subscription. Instead it queries `last_db_update` to find pubkeys whose follow-lists are stale, fetches them in batches of 500, and marks all queried pubkeys as attempted. This keeps the crawl frontier bounded and ensures un-fetchable pubkeys age out.

**Large follow-list chunking.** Follow lists are split into 200-item batches before writing to Dgraph to stay under the gRPC 4 MB message size limit (`web-of-trust/pkg/crawler/chunks.go`).

**Quarantine as a second StrFry instance.** Events from untrusted pubkeys are routed to a separate quarantine StrFry LMDB (Docker container `strfry-quarantine`) rather than being discarded. This allows retroactive rescue when a pubkey is later whitelisted.

## Boundaries & Interfaces

### StrFry ↔ Whitelist Plugin

Protocol: StrFry JSON plugin protocol (newline-delimited JSON on stdin/stdout)

Input to plugin:
```json
{"type":"new","event":{...},"receivedAt":1234567890,"sourceType":"IP4","sourceInfo":"1.2.3.4"}
```

Output from plugin:
```json
{"id":"<event-id>","action":"accept"}
{"id":"<event-id>","action":"reject","msg":"not whitelisted"}
```

Interfaces: `whitelist-plugin/pkg/handler.Handler`, `IOAdapter` (`pkg/handler/handler.go`)

### Whitelist Plugin ↔ Whitelist Server

Protocol: HTTP GET `/check?pubkey=<64-char-hex>`

Response: `{"whitelisted": true}` or `{"whitelisted": false}`

Client: `whitelist-plugin/pkg/client.WhitelistClient`

### Whitelist Server ↔ Dgraph

Protocol: GraphQL HTTP (`localhost:8080`)

Query: all `Profile.pubkey` values with follower threshold

Client: `whitelist-plugin/pkg/repository.GraphQLRepository`

### Web-of-Trust Crawler ↔ Dgraph

Protocol: Dgraph gRPC (`localhost:9080`) via `dgo/v210`

Operations: upsert pubkey nodes, add/remove follows edges, query stale pubkeys, count pubkeys

Client: `web-of-trust/pkg/dgraph.Client`

### Web-of-Trust Crawler ↔ Nostr Relays

Protocol: NIP-01 WebSocket subscriptions (kind 3 filter)

Client: `github.com/nbd-wtf/go-nostr`

### Event Forwarder ↔ StrFry / Source Relays

Protocol: NIP-01 WebSocket (REQ for source, EVENT publish for StrFry)

Sync progress: kind 30078 events with tags `d`, `from`, `to`

Package: `event-forwarder/pkg/relay`, `event-forwarder/pkg/nsync`

### Quarantine Rescuer ↔ Quarantine Container

Protocol: `docker exec` shell commands (strfry scan, strfry delete)

Runner interface: `quarantine-rescuer/internal/runner.Runner` / `runner.Exec`

## Error Handling

**Whitelist plugin:** malformed input → reject with `{"action":"reject","msg":"malformed"}`; handler error → reject with internal error message; serialization failure → empty newline (fail-closed).

**Whitelist server:** Dgraph unavailable at refresh → retry with configurable count; HTTP health endpoint returns 503 until initial load complete.

**Web-of-trust crawler:** relay connect failure → remove relay from config file; Dgraph mutation failure → log and break main loop; invalid pubkey in p-tags → log and skip.

**Event forwarder:** connection failure → exponential backoff retry (`pkg/forwarder/connection_retry.go`); source relay disconnect → reconnect and resume from last sync window.

**Quarantine rescuer:** whitelist server unreachable → abort before any work; forward failure → log failed IDs, skip delete; delete failure → log but do not re-forward.

## Cross-Cutting Concerns

**Pubkey format:** 64-character lowercase hex throughout all components. `nostr.GetPublicKey()` used for derivation; `event.CheckSignature()` required before processing in crawler.

**Secrets:** All secrets via environment variables (`.env` file), never logged. Keys: `STRFRY_PRIVATE_KEY`, `NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`.

**Config files:** Live config in `~/deepfry/` — never overwrite for tests; use temp `HOME`.

**Build:** Each subsystem is an independent Go module with its own `go.mod`; no shared module or monorepo tooling.

---

*Architecture analysis: 2026-06-10*
