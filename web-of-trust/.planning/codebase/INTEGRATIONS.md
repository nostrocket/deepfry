# External Integrations

**Analysis Date:** 2026-06-09

## APIs & External Services

**Nostr Relays:**
- Multiple upstream relays (configurable) - Fetch kind-3 events (contact lists) from live Nostr network
  - SDK/Client: `github.com/nbd-wtf/go-nostr` v0.52.0
  - Protocol: NIP-01 WebSocket
  - Default relays: `wss://relay.damus.io`, `wss://nos.lol`, `wss://relay.nostr.band`, `wss://nostr-pub.wellorder.net`, `wss://relay.primal.net`
  - Config location: `RelayURLs` in `pkg/config/config.go` (lines 51-57)

**Forward Relay (Optional):**
- Forward newly-discovered kind-3 events to a secondary relay (e.g., for backup/replication)
  - Configured via: `forward_relay_url` in `~/deepfry/web-of-trust.yaml`
  - Prompt on startup if not configured (`cmd/crawler/main.go` lines 55-68)
  - Retry logic with exponential backoff (initial: 30s, max: 5 min)

**Nostr.watch API (Relay Discovery):**
- External API for discovering active Nostr relays (`https://api.nostr.watch/v1/online`)
  - Used by: `cmd/discover-relays/main.go` (lines 204-235)
  - Timeout: 15s
  - Fallback: NIP-65 (kind 10002) relay list discovery from seed relays if API fails (lines 239-314)

**NIP-11 Info Endpoint:**
- Relay metadata fetch during relay health checks
  - Library: `github.com/nbd-wtf/go-nostr/nip11`
  - Timeout: 5s per relay
  - Used by: `cmd/discover-relays/main.go` (line 383)

## Data Storage

**Databases:**
- Dgraph (gRPC) - Stores pubkey follow-graph (ID-only, no event payloads)
  - Connection: `DgraphAddr` in config (default: `localhost:9080`)
  - Client: `github.com/dgraph-io/dgo/v210`
  - Location: `pkg/dgraph/dgraph.go` (lines 27-46 for client initialization)
  - Schema: `pubkey` (@id, @upsert), `follows` (uid @reverse), `kind3CreatedAt`, `last_db_update`, `type Profile`
  - Operations: `AddFollowers()`, `RemoveFollower()`, `GetStalePubkeys()`, `CountPubkeys()`, `GetKind3CreatedAt()` in `pkg/dgraph/dgraph.go`

**File Storage:**
- Local filesystem only
  - Exports: CSV files written to working directory by `cmd/pubkeys/main.go` (lines 25-27)
  - Reports: CSV + JSON written by `cmd/clusterscan/main.go` to `--out` directory (or current directory)

**Caching:**
- None (queries go directly to Dgraph on each request)

## Authentication & Identity

**Nostr Keypairs:**
- Static keypairs configured via environment variables (from CLAUDE.md)
  - `STRFRY_PRIVATE_KEY` - StrFry relay secret key (not used in web-of-trust)
  - `NOSTR_SYNC_SECKEY_LIVE` - Live forwarder secret key (not used in web-of-trust)
  - `NOSTR_SYNC_SECKEY_HISTORY` - History forwarder secret key (not used in web-of-trust)
- Web-of-trust crawler is read-only and does not sign events

**Public Key Extraction:**
- Nostr events are parsed and verified by `go-nostr` library
- Pubkeys are 64-char hex strings (Secp256k1) or npub format (Bech32, auto-converted to hex in `pkg/config/config.go` lines 112-114)

## Monitoring & Observability

**Error Tracking:**
- None (errors logged to stdout/stderr only)

**Logs:**
- Approach: `log` package (Go stdlib)
- Patterns:
  - Connection states: "Connected to relay: X", "Relay X marked dead"
  - Processing: "Batch complete: queried N pubkeys (M had events)"
  - Dgraph operations: "DEBUG: AddFollowers completed in Xms for pubkey Y" (when `debug: true`)
  - Signal handling: "Received signal: SIGTERM, initiating graceful shutdown..."

## CI/CD & Deployment

**Hosting:**
- Standalone service (no Docker/K8s requirement from this module)
- Runs on same host as Dgraph (or networked if dgraph_addr points elsewhere)
- One instance recommended per configuration (single crawler per config file)

**CI Pipeline:**
- None configured in this module (CI/CD likely at parent DeepFry level)
- Local test: `make test` (unit tests only, short timeout)
- Linting: `make lint` (golangci-lint optional, warns but doesn't fail build)

## Environment Configuration

**Required env vars:**
- None (all configuration via `~/deepfry/web-of-trust.yaml`)
- Optional Nostr secrets (for signing): Not used by web-of-trust, reserved for forwarders

**Secrets location:**
- Config file: `~/deepfry/web-of-trust.yaml` (YAML, human-readable)
- No .env file expected by this module
- No embedded secrets in code

## Webhooks & Callbacks

**Incoming:**
- None (pure pull model from Nostr relays)

**Outgoing:**
- Forward relay (optional): `cmd/crawler/main.go` line 152 calls `crawler.forwardEvent()`
  - Publishes kind-3 events (contact lists) to forward relay after discovery
  - Retry with backoff on failure

## Relay Connection Management

**Retry Logic:**
- Initial backoff: 30s
- Max backoff: 5 minutes
- Max consecutive failures before removal: 5
- Backoff doubles on each failure
- Dead relay removed from config after 5 failures (callback `pkg/crawler/crawler.go` line 184)

**Health & Reconnection:**
- Periodic reconnection attempts in main loop via `crawler.ReconnectRelays(ctx)` (`pkg/crawler/crawler.go` lines 201-249)
- Exponential backoff respects retry window before attempting reconnect
- Graceful shutdown: `context.WithCancel()` stops all goroutines on SIGINT/SIGTERM

## Graph Query Operations

**Read-Only (Clusterscan):**
- `ResolvePubkeysToUIDs()` - Lookup UIDs for seed pubkeys
- `ExpandTrustedSet()` - Trust propagation via graph shape
- `GetWeakBridges()` - Identify spam cluster entry points
- `GetClusterMembers()` - Enumerate accounts in a suspected cluster
- All operations use `NewReadOnlyTxn()` in `pkg/dgraph/clusterscan.go`

**Write Operations (Crawler):**
- `AddFollowers()` - Bulk upsert follow edges (kind-3 replaceable event behavior)
- `RemoveFollower()` - Delete single follow edge
- `RemovePubKeyIfNoFollowers()` - Garbage collection (delete orphaned nodes)
- `TouchLastDBUpdate()` - Timestamp updates for stale pubkey tracking

---

*Integration audit: 2026-06-09*
