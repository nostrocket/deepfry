# External Integrations

**Analysis Date:** 2026-06-09

## APIs & External Services

**Nostr Relays (WebSocket, NIP-01):**
- Public Nostr relays - Source of kind-3 (contact list) events the crawler subscribes to, and an optional forward target it republishes events to.
  - SDK/Client: `github.com/nbd-wtf/go-nostr` (`nostr.RelayConnect`, `relay.Subscribe`, `relay.Publish`)
  - Implementation: `pkg/crawler/crawler.go` (connect at lines 86, 120, 215, 241; subscribe at 436; publish/forward at 152)
  - Default relays (`pkg/config/config.go` lines 51-57): `wss://relay.damus.io`, `wss://nos.lol`, `wss://relay.nostr.band`, `wss://nostr-pub.wellorder.net`, `wss://relay.primal.net`
  - Auth: none (public read; events are signature-verified, not authenticated connections)

**nostr.watch HTTP API:**
- Relay discovery directory - Lists online relays to test and rank for inclusion in config.
  - Endpoint: `https://api.nostr.watch/v1/online` (`cmd/discover-relays/main.go` line 38)
  - Client: Go stdlib `net/http`
  - Fallback: NIP-65 relay-list discovery from seed relays when the API is unavailable (`cmd/discover-relays/main.go` lines 45-53, 190-205), using `go-nostr/nip11` for relay metadata/latency probing.
  - Auth: none

## Data Storage

**Databases:**
- Dgraph - Stores the ID-only pubkey follow-graph (`Profile` nodes with `pubkey`, `follows`, `kind3CreatedAt`, `last_db_update`, `last_attempt`). No event payloads are stored (data-separation rule).
  - Connection: gRPC at `dgraph_addr` (default `localhost:9080`), set via `~/deepfry/web-of-trust.yaml`
  - Client: `github.com/dgraph-io/dgo/v210` wrapped by `Client` in `pkg/dgraph/dgraph.go`
  - Transport tuning: insecure (plaintext) gRPC credentials; `MaxCallRecvMsgSize` raised to 256 MiB to survive large frontier-selection responses (`pkg/dgraph/dgraph.go` lines 39-44)
  - Schema: declared and applied in-process via `EnsureSchema` (`pkg/dgraph/dgraph.go` lines 60-75) — `pubkey: string @index(exact) @upsert @unique`, `follows: [uid] @reverse`, plus int indexes. Mirrors the repo-root `config/dgraph/schema.graphql` `Profile` type.
  - Query language: DQL only, executed over the gRPC client (no HTTP/GraphQL path in this subsystem; clusterscan computation stays in DQL per `pkg/dgraph/clusterscan.go`)
  - Hosted via repo-root `docker-compose.dgraph.yml` (`dgraph/standalone:v25.3.0`, gRPC 9080, HTTP 8080, Ratel UI 8000)

**File Storage:**
- Local filesystem only - CSV/JSON exports written locally (e.g. pubkeys exporter `cmd/pubkeys/main.go`, clusterscan reports `cmd/clusterscan/main.go`). Config YAML persisted under `~/deepfry/`.

**Caching:**
- None. Relay connection state (alive/dead, backoff timers) is held in-memory only and lost on restart.

## Authentication & Identity

**Auth Provider:**
- None for service-to-service connections. Relay connections are unauthenticated; Dgraph uses insecure gRPC credentials (no TLS, no auth token).
- Nostr identity handling: pubkeys are normalized to 64-char hex; npub (NIP-19 bech32) inputs are decoded via `nip19.Decode` (`pkg/config/config.go` lines 112-118, 125-144). Event integrity is enforced via signature checks before processing (per architecture notes; `event.CheckSignature()` in the crawler path).

## Monitoring & Observability

**Error Tracking:**
- None. Errors are logged with `log.Printf`/`log.Fatalf`; no Sentry/APM integration.

**Logs:**
- Go standard `log` package to stderr. Connection lifecycle, relay dead/retry events, batch/processing milestones. No structured logging; no raw secrets logged.

## CI/CD & Deployment

**Hosting:**
- Runs on the StrFry host as a long-running crawler or one-shot CLIs. No managed hosting config in this subsystem.

**CI Pipeline:**
- None in this subsystem. Repo root contains `.github/copilot-instructions.md` only (no Actions workflows observed here). Builds are driven by `Makefile`.

## Environment Configuration

**Required configuration (file-based, not env vars):**
- `~/deepfry/web-of-trust.yaml` - `relay_urls`, `dgraph_addr`, `pubkey` (seed), `timeout`, `stale_pubkey_threshold`, optional `forward_relay_url`, and clusterscan tuning keys.
- Auto-created with defaults on first load if missing (`pkg/config/config.go` lines 80-99).

**Secrets location:**
- No secrets consumed by this subsystem. It performs unauthenticated relay reads and in/secure Dgraph writes. (Repo-root services such as the event-forwarder use `.env`; this crawler does not.)

## Webhooks & Callbacks

**Incoming:**
- None. The crawler has no HTTP server; it is a relay subscriber and Dgraph writer.

**Outgoing:**
- Optional event forwarding: when `forward_relay_url` is configured, validated kind-3 events are republished to that relay via `forwardEvent` (`pkg/crawler/crawler.go` lines 148-160). Failures mark the forward relay dead and schedule exponential-backoff retry.

---

*Integration audit: 2026-06-09*
