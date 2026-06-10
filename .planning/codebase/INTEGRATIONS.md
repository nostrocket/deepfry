# External Integrations

**Analysis Date:** 2026-06-10

## Databases & Storage

**StrFry LMDB (primary event store):**
- Type: Embedded LMDB key-value store managed by StrFry relay
- Access: StrFry process owns the file; other services access events only via NIP-01 WebSocket subscription
- Data: Canonical Nostr events (all kinds)
- Config: `config/strfry/strfry.conf` — db path `./strfry-db/`, mapsize 10 TB (virtual)
- Container: `strfry` service, port 7777 (WebSocket)

**StrFry LMDB (quarantine store):**
- Separate LMDB instance for events from non-whitelisted pubkeys
- Container: `strfry-quarantine` service, port 7778 (WebSocket)
- Config: `config/strfry/strfry-quarantine.conf`
- Guard script: `config/strfry/quarantine-db-guard.sh` — prevents mainline DB from being mounted here

**Dgraph (graph store):**
- Type: Dgraph standalone v25.3.0 (graph database)
- Purpose: Stores pubkey social graph (follows/followers relationships only — no event payloads)
- HTTP/GraphQL+: `http://localhost:8080` (`DGRAPH_HTTP` or `dgraph:8080` within Docker network)
- gRPC: `localhost:9080` / `dgraph:9080`
- Client library: `github.com/dgraph-io/dgo/v210` via gRPC in `web-of-trust/`
- Schema: `config/dgraph/schema.graphql` — single `Profile` type with fields: `pubkey` (@id), `follows`, `followers`, `kind3CreatedAt`, `last_db_update`
- Data volume: `${DGRAPH_DATA_PATH:-./data/dgraph}` mounted to `/dgraph`
- Memory limit: 8 GB container cap

## External APIs & Services

**Upstream Nostr Relays (read sources for event-forwarder):**
- `wss://relay.damus.io` — live + historical forwarder instances
- `wss://relay.primal.net` — live + historical forwarder instances
- `wss://relay.nostr.lol` — live + historical forwarder instances
- Protocol: NIP-01 WebSocket, REQ subscriptions with time-window filters
- Auth: Nostr keypair signing (`NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`)

**Whitelist HTTP Server (internal):**
- Purpose: Serves pubkey whitelist decisions to the StrFry whitelist plugin
- Endpoint: `http://whitelist-server:8081` (internal Docker network)
- Health: `GET /health`
- Built from: `whitelist-plugin/cmd/server`
- Config: `config/whitelist/whitelist-server.yaml` → mounted at `/root/deepfry/whitelist.yaml`
- Queries Dgraph to determine which pubkeys may write

**Dgraph Ratel UI (development tool):**
- Image: `dgraph/ratel:latest`
- Port: `http://localhost:8000`
- Purpose: Visual query/schema browser for Dgraph

## Protocols & Standards

**Nostr (NIP-01):**
- Wire format: JSON over WebSocket
- Message types: `REQ`, `EVENT`, `EOSE`, `CLOSE`
- All relay-to-relay communication and forwarder↔relay communication uses NIP-01
- Event kinds used internally: kind 3 (follow lists, crawled by `web-of-trust`), kind 30078 (sync progress checkpoints with tags `d`, `from`, `to`)

**StrFry Plugin Protocol:**
- Interface: stdin/stdout JSON — StrFry feeds candidate events as JSON lines; plugin responds accept/reject
- Whitelist plugin (`whitelist-plugin/cmd/whitelist`) and router plugin (`whitelist-plugin/cmd/router`) implement this interface
- Plugin binaries are copied into `dockurr/strfry:latest` image at `/app/plugins/`

**Dgraph GraphQL+ / DQL:**
- Schema defined in `config/dgraph/schema.graphql`
- Seed data in `config/dgraph/seed_data.graphql`
- Schema applied via `config/dgraph/entrypoint.sh` on container start (only reloads when schema changes)

**gRPC (Dgraph client):**
- Used by `web-of-trust` to write pubkey graph mutations to Dgraph
- Transport: `google.golang.org/grpc v1.75.1`
- Protobuf serialization: `google.golang.org/protobuf v1.36.9`, `github.com/gogo/protobuf v1.3.2`

**YAML Configuration:**
- `~/deepfry/web-of-trust.yaml` — web-of-trust crawler config (parsed via `spf13/viper`)
- `~/deepfry/whitelist.yaml` — whitelist plugin config (parsed via `spf13/viper`)
- `config/whitelist/router.yaml` — router plugin config (mounted into strfry container)

## Webhooks & Callbacks

**Incoming:** None detected.
**Outgoing:** None detected.

---

*Integration audit: 2026-06-10*
