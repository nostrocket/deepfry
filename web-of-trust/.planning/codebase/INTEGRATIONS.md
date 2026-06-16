# External Integrations

**Analysis Date:** 2026-06-16

## APIs & External Services

**Nostr Relay Network:**
- Multiple public Nostr relays - WebSocket-based event subscription and publication
  - SDK/Client: `github.com/nbd-wtf/go-nostr` v0.52.0
  - Auth: Nostr keypair signing via secp256k1 (per NIP-01)
  - Default relays: `wss://relay.damus.io`, `wss://nos.lol`, `wss://relay.nostr.band`, `wss://nostr-pub.wellorder.net`, `wss://relay.primal.net` (configurable via `~/deepfry/web-of-trust.yaml`)
  - Usage: `pkg/crawler/crawler.go` subscribes to kind 3 (contact lists) events; `cmd/discover-relays/main.go` uses NIP-65 (kind 10002) for relay discovery
  - Protocols: NIP-01 (WebSocket), NIP-11 (Relay Info), NIP-65 (Relay List Metadata)

**Relay Discovery API:**
- nostr.watch API - Discovers online Nostr relays
  - Endpoint: `https://api.nostr.watch/v1/online` (HTTP GET)
  - Usage: `cmd/discover-relays/main.go:discoverFromAPI()` (line 204-235)
  - Timeout: 15 seconds (apiTimeout constant)
  - Format: JSON array of relay URLs
  - Fallback: NIP-65 relay discovery via seed relays if API unavailable (line 194-201)

**NIP-11 Relay Info:**
- NIP-11 info document fetch for relay health testing
  - SDK: `github.com/nbd-wtf/go-nostr/nip11`
  - Usage: `cmd/discover-relays/main.go:testSingleRelay()` (line 383-385)
  - Timeout: 5 seconds (nip11Timeout constant)
  - Purpose: Measures NIP-11 fetch latency as part of relay selection

## Data Storage

**Databases:**
- Dgraph (gRPC)
  - Connection: `localhost:9080` (gRPC, configurable via `dgraph_addr` in `~/deepfry/web-of-trust.yaml`)
  - Client: `github.com/dgraph-io/dgo/v210` (gRPC client in `pkg/dgraph/dgraph.go`)
  - Credentials: No authentication (insecure credentials via `grpc.WithTransportCredentials(insecure.NewCredentials())`)
  - Max receive message size: 256 MiB (tuned for large follow-list payloads)
  - Schema: Profile type with pubkey (@unique @upsert), follows, kind3CreatedAt, last_db_update, last_attempt, next_attempt, miss_count predicates
  - Data model: ID-only pubkey graph (no event payloads); mutation patterns use upsert blocks for deduplication

**File Storage:**
- Local filesystem only
  - Config: `~/deepfry/web-of-trust.yaml` (YAML format, auto-created if missing)
  - Outputs: CSV files via `cmd/pubkeys/main.go` (timestamped exports)
  - Logs: stdout/stderr (no persistent log files configured)

**Caching:**
- None (all reads/writes hit Dgraph directly)

## Authentication & Identity

**Auth Provider:**
- Custom (Nostr keypair signing)
  - Implementation: secp256k1 signatures per NIP-01; hardcoded seed pubkeys for trusted roots in clusterscan (`pkg/config/config.go` lines 106-112)
  - Trusted roots: 5 hardcoded seed pubkeys (live event forwarder, history forwarder, gsov, rocketdog8, macro88)
  - Config option: `seed_pubkeys` (YAML array, configurable for clusterscan)

## Monitoring & Observability

**Error Tracking:**
- None (built-in; errors logged to stdout via `log` package)

**Logs:**
- Approach: Structured `log.Printf()` calls throughout codebase
  - Connection lifecycle: connect, disconnect, reconnect events logged in `pkg/crawler/crawler.go`
  - Relay health: dead relays, retry scheduling, filter rejections logged in crawler
  - Dgraph operations: schema creation, mutation batches logged in `pkg/dgraph/`
  - Configuration: config file location, defaults, loaded values logged in `pkg/config/config.go`
  - Processing: pubkeys processed, batch completed, staleness detection logged in crawler
  - Debug flag: Optional verbose logging guarded by `if c.debug { log.Printf(...) }` in `pkg/crawler/crawler.go`

## CI/CD & Deployment

**Hosting:**
- Containerized (via docker-compose in parent DeepFry project)
  - Docker support: Makefile includes cross-platform build targets (Windows .exe, Unix binaries)
  - Static binary option: `CGO_ENABLED=0` for Alpine Linux deployment

**CI Pipeline:**
- Makefile-driven test & build (`make all`, `make test`, `make test-integration`)
  - Unit tests: `go test ./... -short -cover` (excludes integration tests)
  - Integration tests: `go test -tags=integration ./...` (requires live Dgraph + relays)
  - Lint: `golangci-lint run` (optional, non-failing)
  - Fmt: `go fmt ./...` (enforced before commit)
  - Vet: `go vet ./...` (enforced before commit)

## Environment Configuration

**Required env vars:**
- No environment variables required at runtime (all config via `~/deepfry/web-of-trust.yaml`)
- Build-time env vars (optional):
  - `VERSION` - Override version string (default: "dev")
  - `GIT_COMMIT` - Git commit hash (auto-detected via `git rev-parse --short HEAD`)
  - `BUILD_TIME` - Build timestamp (auto-generated via `date -u`)

**Secrets location:**
- No secrets stored in code or config
- Nostr keypair signing: secp256k1 operations built into `go-nostr` library
- Private key handling: not exposed in this module (parent DeepFry services handle key material)

## Webhooks & Callbacks

**Incoming:**
- None

**Outgoing:**
- None (event-forwarder is separate service in DeepFry stack)

## Discovery & Relay Testing

**Relay Discovery Mechanisms:**
1. **nostr.watch API** (primary): `https://api.nostr.watch/v1/online` fetches list of online relays
2. **NIP-65 Fallback**: If API unavailable, connects to seed relays and queries kind 10002 events for relay metadata
3. **NIP-11 Health Check**: Fetches relay info documents to test connectivity and measure latency

**Relay Selection Process** (`cmd/discover-relays/main.go`):
- Discovers relays via API or NIP-65
- Tests up to 500 relays concurrently (configurable via `-max-test`, `-concurrency` flags)
- Measures three latencies per relay:
  - NIP-11 info fetch latency
  - WebSocket connect latency
  - Kind 3 subscription latency
- Ranks by total latency and selects top N (configurable via `-count`, default 50)
- Merges or replaces existing relay URLs in config (configurable via `-replace` flag)
- Dry-run mode available (no config modification)

## Configuration Persistence

**Config Update Mechanisms:**
- `pkg/config/config.go`: `SaveForwardRelayURL()` and `RemoveRelayURL()` functions update config file on relay failures
- `cmd/discover-relays/main.go`: `writeRelayURLsToConfig()` updates relay_urls section in YAML via yaml.v3 node manipulation (preserves comments and structure)
- Viper library handles YAML marshalling/unmarshalling with support for safe writes (SetDefault + SafeWriteConfigAs pattern)

---

*Integration audit: 2026-06-16*
