# External Integrations

**Analysis Date:** 2026-06-15

## Databases & Storage

**StrFry LMDB (primary event store):**
- Type: Embedded LMDB key-value store managed by StrFry relay
- Access: StrFry process owns the file; other services access events only via NIP-01 WebSocket subscription
- Data: Canonical Nostr events (all kinds)
- Config: `config/strfry/strfry.conf` — db path `./strfry-db/`, mapsize 10 TB (virtual)
- Container: `strfry` service, port 7777 (WebSocket)
- Mount: `${STRFRY_DB_PATH:-./data/strfry-db}` (read-write)

**StrFry LMDB (quarantine store):**
- Separate LMDB instance for events from non-whitelisted pubkeys
- Container: `strfry-quarantine` service, port 7778 (WebSocket)
- Config: `config/strfry/strfry-quarantine.conf`
- Guard script: `config/strfry/quarantine-db-guard.sh` — prevents mainline DB from being mounted here
- Mount: `${STRFRY_QUARANTINE_DB_PATH:-./data/strfry-quarantine-db}` (read-write)

**Dgraph (graph store):**
- Type: Dgraph standalone v25.3.0 (graph database)
- Purpose: Stores pubkey social graph (follows/followers relationships only — no event payloads)
- HTTP/GraphQL+: `http://localhost:8080` (`DGRAPH_HTTP` or `dgraph:8080` within Docker network)
- gRPC: `localhost:9080` / `dgraph:9080`
- Client library: `github.com/dgraph-io/dgo/v210` via gRPC in `web-of-trust/`
- Schema: `config/dgraph/schema.graphql` — single `Profile` type with fields: `pubkey` (@id), `follows`, `followers`, `kind3CreatedAt`, `last_db_update`
- Data volume: `${DGRAPH_DATA_PATH:-./data/dgraph}` mounted to `/dgraph`
- Memory limit: 8 GB container cap
- Seed: `config/dgraph/seed_data.graphql` applied on startup

**LMDB Read-Only Access (lmdb2graphql):**
- Component: `spam/` subsystem (lmdb2graphql Rust service)
- Mount: strfry LMDB at `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db:ro` (read-only kernel enforcement)
- Purpose: Parse StrFry LMDB binary format, expose events via GraphQL API
- Protocol: Query Nostr events by kind, author, time range, tags
- HTTP endpoint: `http://localhost:8080` (GraphQL POST at `/graphql`)
- Healthcheck: `/health` (liveness), `/ready` (readiness after self-check)

## External APIs & Services

**Upstream Nostr Relays (read sources for event-forwarder):**
- `wss://relay.damus.io` — live + historical sync forwarder instances
- `wss://relay.primal.net` — live + historical sync forwarder instances
- `wss://relay.nostr.lol` (nos.lol) — live + historical sync forwarder instances
- `wss://nostr.wine` — example in docker-compose template
- Protocol: NIP-01 WebSocket, REQ subscriptions with time-window filters
- Auth: Nostr keypair signing (`NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`)
- Event source: Raw Nostr events forwarded to DeepFry relay (`DEEPFRY_RELAY_URL`)

**Nostr Relay Discovery (NIP-65):**
- Component: `web-of-trust/cmd/discover-relays` tool
- Protocol: NIP-65 relay list discovery + NIP-11 relay info
- Sources: Relay metadata events (kind 10002) from upstream relays
- Output: Updates `~/deepfry/web-of-trust.yaml` with discovered relays

**Whitelist HTTP Server (internal):**
- Purpose: Serves pubkey whitelist decisions to the StrFry whitelist plugin
- Endpoint: `http://whitelist-server:8081` (internal Docker network)
- Health: `GET /health` (returns 200 when ready, 503 before initial load)
- Built from: `whitelist-plugin/cmd/server`
- Config: `config/whitelist/whitelist-server.yaml` → mounted at `/root/deepfry/whitelist.yaml`
- Queries Dgraph GraphQL endpoint (`http://dgraph:8080`) to determine which pubkeys may write
- API Endpoints:
  - `/check/{pubkey}` — POST returns 200 (whitelisted) or 403 (rejected)
  - `/health` — GET liveness probe
  - `/stats` — GET whitelist entry count
  - `/version` — GET binary version info (from -buildvcs stamping)

**Dgraph Ratel UI (development tool):**
- Image: `dgraph/ratel:latest`
- Port: `http://localhost:8000`
- Purpose: Visual query/schema browser for Dgraph
- Type: Not used in production; development only

## Protocols & Standards

**Nostr (NIP-01):**
- Wire format: JSON over WebSocket
- Message types: `REQ`, `EVENT`, `EOSE`, `CLOSE`, `AUTH` (optional)
- All relay-to-relay communication and forwarder↔relay communication uses NIP-01
- Event kinds used:
  - kind 0 (metadata) — forwarded to quarantine by router plugin
  - kind 1 (text note) — not crawled
  - kind 3 (contact list) — crawled by web-of-trust crawler
  - kind 10002 (relay list, NIP-65) — used by discover-relays tool
  - kind 30078 (sync progress) — written by event-forwarder with tags: `d`, `from`, `to`

**Nostr NIP-65:**
- Standard: Relay list metadata (kind 10002)
- Used by: `web-of-trust/cmd/discover-relays`
- Purpose: Discover relay URLs from user metadata

**Nostr NIP-11:**
- Standard: Relay information document
- Used by: `web-of-trust/cmd/discover-relays`
- Endpoint: `http://<relay-domain>/.well-known/nostr.json`
- Purpose: Get relay name, description, supported NIPs

**StrFry Plugin Protocol:**
- Interface: stdin/stdout JSON — StrFry feeds candidate events as JSON lines; plugin responds accept/reject
- Whitelist plugin (`whitelist-plugin/cmd/whitelist`) — pure accept/reject against Dgraph whitelist
- Router plugin (`whitelist-plugin/cmd/router`) — accept/reject + forward rejected events to quarantine relay
- Plugin binaries are copied into `dockurr/strfry:latest` image at `/app/plugins/whitelist` and `/app/plugins/router`
- Config: Selected via `relay.writePolicy.plugin` in `config/strfry/strfry.conf`

**Dgraph GraphQL+ / DQL:**
- Schema defined in `config/dgraph/schema.graphql`
- Seed data in `config/dgraph/seed_data.graphql`
- Schema applied via `config/dgraph/entrypoint.sh` on container start (reloaded only on schema changes)
- HTTP endpoint: `http://localhost:8080/graphql` or `http://dgraph:8080/graphql` (internal)
- Used by: whitelist-plugin queries (GraphQL over HTTP)

**gRPC (Dgraph client):**
- Used by: `web-of-trust` to write pubkey graph mutations and read stale pubkeys
- Transport: `google.golang.org/grpc v1.75.1`
- Protobuf serialization: `google.golang.org/protobuf v1.36.9`, `github.com/gogo/protobuf v1.3.2`
- Endpoint: `localhost:9080` or `dgraph:9080` (internal)

**GraphQL (lmdb2graphql):**
- Protocol: GraphQL POST queries (async-graphql 7.2.1)
- Endpoint: `http://localhost:8080/graphql` (loopback-only via docker-compose publish rule)
- Access: Read-only corpus queries (no mutations; LMDB is read-only)
- Root query fields: `events`, `eventsByAuthor`, `eventsByKind`, `eventStats`
- Pagination: Cursor-based (base64-encoded D-11 opaque cursors)

**YAML Configuration:**
- `~/deepfry/web-of-trust.yaml` — web-of-trust crawler config (parsed via `spf13/viper`)
  - Keys: `relay_urls`, `cluster_scan`, `max_crawl_depth`
  - Defaults provided; auto-created if missing
- `~/deepfry/whitelist.yaml` — whitelist plugin config (parsed via `spf13/viper`)
  - Used by both whitelist and router plugins
- `~/deepfry/router.yaml` — router plugin config (quarantine forwarding rules)
  - Used by router plugin for routing decisions
- `~/deepfry/lmdb2graphql.yaml` — lmdb2graphql config (parsed via `serde_yaml_ng`)
  - Keys: `strfry_db_path`, `bind_address`, `map_size`, `pinned_strfry_version`, `pinned_strfry_commit`
  - Must be present at startup; failing to load causes process exit

## Webhooks & Callbacks

**Incoming:** None detected.

**Outgoing:** Event forwarding to StrFry relay (via NIP-01 EVENT messages):
- event-forwarder publishes sync progress events (kind 30078) to DeepFry relay (`DEEPFRY_RELAY_URL`)
- Router plugin forwards rejected kind 0/1/3 events to quarantine relay (port 7778) asynchronously

---

*Integration audit: 2026-06-15*
