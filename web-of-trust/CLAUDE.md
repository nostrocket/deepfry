<!-- GSD:project-start source:PROJECT.md -->

## Project

**Web-of-Trust Crawler**

The `web-of-trust` Go module is a Nostr crawler that subscribes to kind-3 (contact list) events from many relays and stores an ID-only pubkey follow-graph in Dgraph. It is one of several sidecar services around a stock StrFry Nostr relay in the DeepFry stack; it never stores event payloads (those live in StrFry's LMDB) — only pubkey relationships. The follow-graph feeds trust scoring and the whitelist plugin that decides which pubkeys may write events.

**Core Value:** The crawler must continuously **expand** the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

### Constraints

- **Tech stack**: Go module; Dgraph via gRPC (`localhost:9080`) and DQL over HTTP (`localhost:8080`). Must stay compatible with the existing `Profile` schema.
- **Config**: Live config lives at `~/deepfry/web-of-trust.yaml` — never edit it for testing; use a temp `HOME` (per `8pc_crawled.md` §6).
- **Data separation**: ID-only graph in Dgraph; no event payloads. StrFry unmodified.
- **Testing**: Integration tests gate on a live Dgraph via `//go:build integration` / `make test-integration`. No unit-test suite exists yet.
- **Verification**: requires running the crawler on any host with access to the live Dgraph + public relays (manual step, per spec §6).

<!-- GSD:project-end -->

<!-- GSD:stack-start source:codebase/STACK.md -->

## Technology Stack

## Languages

- Go 1.24.1 - Core implementation language for all subsystems (crawler, clusterscan, pubkeys exporter, relay discovery, healthcheck)

## Runtime

- Go 1.24.1+ (required per CLAUDE.md)
- Go Modules (go.mod, go.sum)
- Lockfile: `go.sum` (present)

## Frameworks

- `github.com/nbd-wtf/go-nostr` v0.52.0 - Nostr protocol client library, WebSocket relay communication (NIP-01)
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Dgraph gRPC client for graph database access
- `github.com/spf13/viper` v1.18.2 - YAML configuration loader (reads `~/deepfry/web-of-trust.yaml`)
- `google.golang.org/grpc` v1.75.1 - gRPC transport for Dgraph communication (native gRPC client)
- `google.golang.org/protobuf` v1.36.9 - Protocol Buffers support (Dgraph API)

## Key Dependencies

- `github.com/nbd-wtf/go-nostr` v0.52.0 - Why it matters: only dependency for relay communication; must stay compatible with NIP-01 WebSocket protocol
- `github.com/dgraph-io/dgo/v210` v210.0.0-20230328113526-b66f8ae53a2d - Why it matters: exclusive gRPC client for Dgraph backend; all pubkey graph mutations/queries flow through this
- `google.golang.org/grpc` v1.75.1 - Low-level gRPC transport; Dgraph depends on it
- `gopkg.in/yaml.v3` v3.0.1 - YAML parsing for config files
- `github.com/bytedance/sonic` v1.14.1 - High-performance JSON codec (via go-nostr transitive)
- Cryptography: `github.com/btcsuite/btcd/btcec/v2` v2.3.5, `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.4.0 (Nostr key handling)
- Protobuf support: `github.com/gogo/protobuf` v1.3.2 (Dgraph API serialization)
- Concurrency: `go.uber.org/atomic` v1.9.0, `sourcegraph/conc` v0.3.0
- WebSocket: `github.com/coder/websocket` v1.8.14 (low-level relay transport)

## Configuration

- Config files: `~/deepfry/web-of-trust.yaml` (YAML format, auto-created if missing)
- Default Nostr relays: damus.io, nos.lol, relay.nostr.band, nostr-pub.wellorder.net, relay.primal.net
- Default Dgraph address: `localhost:9080` (gRPC)
- Default timeout: 30s
- Makefile targets: `make build`, `make build-crawler`, `make build-pubkeys`, `make build-discover-relays`, `make build-healthcheck`, `make build-clusterscan`
- Version injection: Git commit hash + build timestamp via ldflags
- Output directory: `bin/` (per subsystem)

## Platform Requirements

- Go 1.24.1+
- POSIX shell (for Makefile)
- Access to `~/deepfry/` config directory (auto-created on first load)
- Go 1.24.1+ runtime or static binary (Alpine-compatible via `CGO_ENABLED=0` if needed)
- Connectivity to Dgraph gRPC endpoint (default: `localhost:9080`)
- Connectivity to Nostr relays (WebSocket, wss://)
- Read/write access to `~/deepfry/web-of-trust.yaml`

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

## Language & Version

## Naming Patterns

- Lowercase with underscores: `clusterscan.go`, `chunks.go`, `main.go`
- Package follows directory name: `package dgraph` in `pkg/dgraph/`
- Receiver methods grouped with their type in same file
- PascalCase for exported functions: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`
- camelCase for unexported functions: `normalizeSeedPubkeys()`, `cleanSubscribeError()`
- Methods: receiver as single letter lowercase: `(c *Client)`, `(e *subscriptionError)`
- Constructor pattern: `func New(cfg Config) (*Crawler, error)`
- camelCase: `signerPubkey`, `followeeList`, `dgClient`, `relayState`
- Map keys as pubkey strings or UIDs: `pubkeys map[string]struct{}`
- Booleans prefixed with state: `alive`, `deleted`, `valid`
- Timestamps with suffix: `kind3CreatedAt`, `lastUpdate`, `olderThanUnix`
- PascalCase for all types: `Client`, `Crawler`, `Config`, `PubkeyNode`, `WeakBridge`
- Exported struct fields: PascalCase: `RelayURLs`, `DgraphAddr`, `Timeout`
- Struct tags for config mapping: `mapstructure:"relay_urls"` (snake_case in YAML)
- JSON tags for Dgraph unmarshalling: `json:"pubkey"`, `json:"kind3CreatedAt"`
- Unexported struct fields: camelCase: `dg`, `conn`, `relays`, `dgClient`
- UPPERCASE_WITH_UNDERSCORES: `maxBackoff`, `initialBackoff`, `maxConsecutiveFailures`
- Magic numbers extracted as named constants at package/function level
- No large interface definitions found; composition over interfaces
- Error interface implementation: `Error() string`, `Unwrap() error`

## Code Style

- `go fmt` enforced via Makefile (`make fmt`)
- Standard Go formatting rules: 8-space indentation (tabs)
- Line length: no strict limit observed, but functions kept readable
- `golangci-lint` optional (non-failing via Makefile)
- Makefile targets: `make lint`, `make lint-fix`
- Tool gracefully handles absence (doesn't break build)

## Error Handling

- All errors wrapped with context using `%w` verb for error chain
- Error message explains the operation that failed
- Example: `fmt.Errorf("query follower failed: %w", err)`
- `subscriptionError` - subscription-related failures on relay
- `transportError` - connection/transport failures
- Both implement `Error()` and `Unwrap()` methods
- Errors returned immediately to caller with context added
- No silent error suppression except explicit cases (e.g., config file not found)
- Named return values used in some methods: `([]string, error)`, `(bool, error)`

## Logging

- `log.Printf("Connected to relay: %s", url)` - Info level
- `log.Printf("WARN: Failed to connect: %v", err)` - Warning level
- `log.Printf("DEBUG: Starting AddFollowers for pubkey %s...", pubkey)` - Debug when debug flag enabled
- `log.Fatalf("Failed to create crawler: %v", err)` - Fatal errors in main()
- Connection lifecycle events: connect, disconnect, reconnect
- Major state changes: relay dead, retry scheduled, chunk processed
- Configuration loaded/saved
- Processing milestones: pubkeys processed, batch completed
- Debug output guarded by `if c.debug { log.Printf(...) }`
- No logging of raw secrets (env vars, private keys)

## Comments

- Explain purpose at top of file above package declaration
- Describe schema/structure for complex packages (e.g., Dgraph package)
- All exported functions have comment starting with function name
- Describe parameters, return values, and side effects
- Example from `pkg/dgraph/dgraph.go` (lines 32-33):
- Minimal; code is self-documenting with clear names
- Used for non-obvious logic: why a step is needed, workaround for external limitation
- Example from `pkg/dgraph/dgraph.go` (line 87): `// viper.SafeWriteConfigAs does not write SetDefault values...`

## Function Design

- `ctx context.Context` always first parameter (Go convention)
- Configuration struct passed as value: `cfg Config`
- Callback functions used for pagination: `callback func([]string) error`
- Debug flag passed explicitly: `debug bool`
- Error always last return value
- Boolean for optional success: `(deleted bool, error)`
- Map for bulk results: `map[string]int64`
- Explicit nil for not found (not error): `func() (int64, error)` returns `(0, nil)` if not exists

## Module Design

- `pkg/config` - configuration loading and persistence
- `pkg/dgraph` - Dgraph client and schema operations
- `pkg/crawler` - relay connection and event processing
- Public APIs: `NewClient()`, `AddFollowers()`, `GetStalePubkeys()`
- Config struct exported with public fields for mapstructure unmarshalling
- Helper types exported when needed: `PubkeyNode`, `WeakBridge`
- Constructor: `NewClient(addr string) (*Client, error)`
- Lifecycle: `Close() error`
- Schema setup: `EnsureSchema(ctx context.Context) error`
- Write operations: `AddFollowers()`, `RemoveFollower()`, `DeleteNodes()`
- Read operations: `GetStalePubkeys()`, `CountPubkeys()`, `GetKind3CreatedAt()`
- Paginated reads: `GetPubkeysWithMinFollowersPaginated()`, `GetAllPubkeysPaginated()`
- Graph analysis (clusterscan): `ResolvePubkeysToUIDs()`, `ExpandTrustedSet()`, `GetWeakBridges()`
- YAML file at `~/deepfry/web-of-trust.yaml`
- Viper library handles loading/saving
- Defaults provided if file missing
- Functions to update: `SaveForwardRelayURL()`, `RemoveRelayURL()`

## Concurrency

- `sync.Mutex` for protecting shared state: `dbUpdateMutex`
- `sync.WaitGroup` for waiting on goroutines: signal handling in main
- `atomic.Int32` for concurrent counter access: `failures` in relay state
- Context-based cancellation: `context.WithCancel()` for graceful shutdown
- No global state; all state in struct fields or function-local variables

## Dependencies & Integrations

- `github.com/dgraph-io/dgo/v210` - Dgraph gRPC client
- `github.com/nbd-wtf/go-nostr` - Nostr relay communication
- `github.com/spf13/viper` - YAML config loading
- `google.golang.org/grpc` - gRPC transport

<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

## System Overview

```text

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

- **Upsert-based dedup:** Pubkey uniqueness enforced via Dgraph @unique + @upsert schema
- **Stale-feed:** Crawler queries `last_db_update` timestamp to identify outdated pubkeys; restarts from seed if empty
- **Graceful relay degradation:** Failed relays removed from config; dead relays retry with exponential backoff
- **Large-list handling:** Follow lists >10k split into 200-item chunks to avoid gRPC message size limits
- **Read-only graph analysis:** Clusterscan uses read-only queries; no mutations during analysis phase

## Layers

- Purpose: Spawn crawler loop; handle signals; manage initialization
- Location: `cmd/crawler/main.go`, `cmd/clusterscan/main.go`, `cmd/pubkeys/main.go`, etc.
- Contains: Main loop, config loading, signal handlers, final reporting
- Depends on: `pkg/config`, `pkg/crawler`, `pkg/dgraph`
- Used by: User (binary execution)
- Purpose: Subscribe to kind 3 events from multiple Nostr relays; validate and parse follow-lists; coordinate writes to Dgraph
- Location: `pkg/crawler/`
- Contains: `Crawler` struct, relay state machines, event subscriptions, chunked writes
- Depends on: `pkg/dgraph`, `go-nostr` (relay subscribe/publish)
- Used by: `cmd/crawler/main.go`
- Purpose: Encapsulate all Dgraph mutations and queries; ensure pubkey uniqueness via upsert; provide both crawler writes and read-only queries for analysis
- Location: `pkg/dgraph/`
- Contains: `Client` struct with methods for AddFollowers, GetStalePubkeys, ResolvePubkeysToUIDs, ExpandTrustedSet, GetWeakBridges, ClusterBeneath
- Depends on: `dgo/v210` (Dgraph gRPC client)
- Used by: Crawler, Clusterscan, Pubkeys exporter, Healthcheck
- Purpose: Load YAML config from `~/deepfry/web-of-trust.yaml`; supply defaults; manage runtime config mutations (relay updates)
- Location: `pkg/config/config.go`
- Contains: `Config` struct, LoadConfig, SaveForwardRelayURL, RemoveRelayURL
- Depends on: `spf13/viper` (YAML parser)
- Used by: All entry points

## Data Flow

### Primary Request Path (Crawler Loop)

- Dgraph is source-of-truth for all pubkeys and follows
- `last_db_update` (unix timestamp) tracks when each pubkey's follow-list was last queried
- `kind3CreatedAt` stores the event's created_at time for version checking
- Relay connection state (alive, failures, backoff timers) held in-memory; lost on restart

### Clusterscan Path (Spam Detection)

## Key Abstractions

- Purpose: Manages relay pool and coordinates follow-list fetches from multiple sources
- Examples: `cmd/crawler/main.go` uses `crawler.FetchAndUpdateFollows()`
- Pattern: Relay state machine with exponential backoff; subscription-based event collection; concurrent execution across relays; sequential writing to Dgraph (mutex-protected)
- Purpose: Single access point for all graph queries and mutations
- Examples: All entry points use `dgraph.NewClient(addr)` and call methods on the returned `*Client`
- Pattern: Upsert-based writes (idempotent); transaction-wrapped mutations; read-only queries for analysis; pagination helpers for large result sets
- Purpose: Centralize all environment configuration; support runtime persistence
- Examples: YAML from `~/deepfry/web-of-trust.yaml` loaded into `Config` struct; RemoveRelayURL updates config file on relay failures
- Pattern: Viper-backed; defaults for relays and cluster-scan parameters; support for both hex and npub pubkey formats

## Entry Points

- Location: `cmd/crawler/main.go`
- Triggers: User runs `./bin/crawler`; signal SIGINT/SIGTERM for graceful shutdown
- Responsibilities: Load config, connect to relays, loop GetStalePubkeys → FetchAndUpdateFollows, report stats
- Location: `cmd/clusterscan/main.go`
- Triggers: User runs `./bin/clusterscan [flags]`
- Responsibilities: Resolve seeds, propagate trust, find weak bridges, size clusters, write CSV/JSON
- Location: `cmd/discover-relays/main.go`
- Triggers: User runs `./bin/discover-relays [flags]`
- Responsibilities: Poll nostr.watch API or NIP-65 relays for relay URLs, test latency, update config
- Location: `cmd/pubkeys/main.go`
- Triggers: User runs `./bin/pubkeys`
- Responsibilities: Query all pubkeys with ≥1 follower, write to timestamped CSV
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

### Event Deduplication Race with Multiple Relays

### Relay Dead-Lock Without Automatic Removal

## Error Handling

- Relay errors (subscribe, transport): Mark relay dead and log; continue with remaining relays
- Dgraph errors (query, mutation): Log with context; propagate up to main loop; break and restart
- Config load errors: Fatal (no recovery)
- Invalid pubkeys in event: Log and skip; continue processing other pubkeys
- Large follow-lists: Chunk and retry with smaller batches

## Cross-Cutting Concerns

- Pubkey format: `nostr.GetPublicKey()` used everywhere (hex, 64 chars)
- Event signature: `event.CheckSignature()` required before processing
- Follow-list integrity: P-tags parsed and de-duped; invalid pubkeys skipped

<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:

- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
