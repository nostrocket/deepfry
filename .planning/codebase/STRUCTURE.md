# Codebase Structure

**Analysis Date:** 2026-06-10

## Directory Layout

```
deepfry/
├── event-forwarder/          # Production: forwards events from upstream relays to StrFry
├── whitelist-plugin/         # Production: StrFry JSON plugin + whitelist HTTP server
├── web-of-trust/             # Production: kind-3 crawler; builds pubkey graph in Dgraph
├── quarantine-rescuer/       # Production: one-shot CLI to rescue events from quarantine LMDB
├── embeddings-generator/     # Placeholder (README only)
├── search-plugin/            # Placeholder (README only)
├── profile-builder/          # Placeholder (README only)
├── thread-inference/         # Placeholder (README only)
├── spam/                     # Research/planning area for spam detection (in-progress)
├── config/
│   ├── dgraph/               # Dgraph GraphQL schema (`schema.graphql`)
│   ├── strfry/               # StrFry config file(s)
│   └── whitelist/            # Whitelist server config templates
├── data/
│   ├── dgraph/               # Dgraph persistent data volumes (p, t, w, zw)
│   └── strfry-db/            # StrFry LMDB data volume
├── docs/
│   ├── architecture/         # Architecture decision records
│   └── images/               # Diagrams
├── quarantine/               # Quarantine subsystem spec/docs
├── quarantine-cleaner/       # Related tooling (separate from rescuer)
├── embeddings/               # Embeddings-related research
├── .planning/
│   └── codebase/             # GSD codebase map documents (this directory)
├── docker-compose.dgraph.yml # Dgraph + Ratel UI
├── docker-compose.strfry.yml # StrFry relay
├── docker-compose.evtfwd.yml # Event forwarder(s)
├── Dockerfile.strfry         # StrFry Docker image
├── Dockerfile.whitelist-server # Whitelist server Docker image
├── CLAUDE.md                 # Project instructions for Claude Code
└── README.md
```

## Module Organization

Each production subsystem is an **independent Go module** with its own `go.mod`, `go.sum`, and `Makefile`. There is no shared Go module or workspace file at the repo root.

### event-forwarder

```
event-forwarder/
├── cmd/fwd/          # main package — TUI/CLI entry point
├── pkg/
│   ├── config/       # Config loading (env vars, flags)
│   ├── crypto/       # Key utilities
│   ├── forwarder/    # Core orchestration: windowed + real-time sync modes
│   ├── nsync/        # Sync progress tracking (kind 30078 events)
│   ├── relay/        # WebSocket relay abstraction over go-nostr
│   ├── telemetry/    # Metrics aggregation; TUI/CLI display
│   ├── testutil/     # Shared test helpers
│   ├── utils/        # Misc utilities
│   └── version/      # Version injection via ldflags
└── Makefile
```

### whitelist-plugin

```
whitelist-plugin/
├── cmd/
│   ├── whitelist/    # main package — StrFry JSON plugin (stdin/stdout)
│   ├── server/       # main package — whitelist HTTP server
│   └── router/       # main package — router variant (HTTP routing)
├── pkg/
│   ├── client/       # HTTP client for /check endpoint
│   ├── config/       # LoadClientConfig / LoadServerConfig
│   ├── handler/      # Handler + IOAdapter interfaces; whitelist_handler, jsonl_io_adapter
│   ├── heuristics/   # Additional event heuristics
│   ├── quarantine/   # Quarantine routing logic
│   ├── repository/   # KeyRepository interface + GraphQL Dgraph client
│   ├── server/       # HTTP server (WhitelistServer, /check, /health)
│   ├── version/      # Version injection
│   └── whitelist/    # In-memory Whitelist (atomic.Pointer); WhitelistRefresher
└── Makefile
```

### web-of-trust

```
web-of-trust/
├── cmd/
│   ├── crawler/         # main package — crawl loop entry point
│   ├── clusterscan/     # main package — trust propagation + spam cluster analysis
│   ├── discover-relays/ # main package — NIP-65 relay discovery + latency testing
│   ├── healthcheck/     # main package — Dgraph pubkey integrity scan
│   └── pubkeys/         # main package — export pubkeys to CSV
├── pkg/
│   ├── config/          # LoadConfig / SaveForwardRelayURL / RemoveRelayURL (Viper)
│   ├── crawler/         # Relay pool; kind-3 subscription; chunked Dgraph writes
│   └── dgraph/          # Dgraph gRPC client; all mutations and queries
├── queries/             # Raw DQL query files
└── Makefile
```

### quarantine-rescuer

```
quarantine-rescuer/
├── cmd/quarantine-rescue/ # main package — one-shot rescue CLI
├── internal/
│   ├── deleter/         # Calls strfry delete inside quarantine container
│   ├── envfile/         # .env file parsing
│   ├── event/           # RawEvent type
│   ├── exporter/        # Streams events from quarantine via docker exec
│   ├── forwarder/       # Publishes events to main relay WebSocket
│   ├── lmdbreader/      # Direct LMDB read utilities
│   ├── runner/          # Runner interface + Exec implementation (docker exec)
│   └── whitelist/       # Whitelist client (reuses whitelist-server /check)
└── Makefile
```

## Naming Conventions

### Files

- Lowercase with underscores: `whitelist_handler.go`, `jsonl_io_adapter.go`, `connection_retry.go`
- Test files co-located with source: `whitelist_handler_test.go` alongside `whitelist_handler.go`
- `main.go` for every `cmd/*` entry point
- Interface definitions in dedicated file: `handler.go`, `interfaces.go`

### Packages

- Package name matches directory name: `package handler` in `pkg/handler/`, `package dgraph` in `pkg/dgraph/`
- `cmd/*` packages use `package main`
- `internal/*` packages are internal to their module (Go visibility enforcement)

### Types

- PascalCase for all exported types: `Whitelist`, `WhitelistRefresher`, `Forwarder`, `Crawler`, `Client`
- Constructor pattern: `func New(cfg Config) (*Type, error)` or `func NewType(...) *Type`
- Config structs: `Config` (per package), exported fields in PascalCase with `mapstructure:` tags for YAML

### Functions & Methods

- Exported: PascalCase — `IsWhitelisted`, `AddFollowers`, `GetStalePubkeys`, `FetchAndUpdateFollows`
- Unexported: camelCase — `normalizeSeedPubkeys`, `cleanSubscribeError`
- Receivers: single lowercase letter — `(wl *Whitelist)`, `(c *Client)`, `(f *Forwarder)`
- Context always first parameter: `func (c *Client) GetStalePubkeys(ctx context.Context, ...) (...)`

### Variables & Fields

- camelCase for unexported: `dgClient`, `relayState`, `signerPubkey`
- PascalCase for exported struct fields: `RelayURLs`, `DgraphAddr`, `ServerURL`
- Boolean fields prefixed with state: `alive`, `deleted`, `dryRun`
- Timestamps with suffix: `kind3CreatedAt`, `last_db_update`, `olderThanUnix`

## Entry Points

| Binary | Package | Command |
|--------|---------|---------|
| `fwd` | `event-forwarder/cmd/fwd` | `cd event-forwarder && make build` |
| `whitelist` (plugin) | `whitelist-plugin/cmd/whitelist` | `cd whitelist-plugin && make build` |
| `whitelist-server` | `whitelist-plugin/cmd/server` | `cd whitelist-plugin && make build` |
| `router` | `whitelist-plugin/cmd/router` | `cd whitelist-plugin && make build` |
| `crawler` | `web-of-trust/cmd/crawler` | `cd web-of-trust && make build-crawler` |
| `clusterscan` | `web-of-trust/cmd/clusterscan` | `cd web-of-trust && make build` |
| `discover-relays` | `web-of-trust/cmd/discover-relays` | `cd web-of-trust && make build` |
| `pubkeys` | `web-of-trust/cmd/pubkeys` | `cd web-of-trust && make build-pubkeys` |
| `healthcheck` | `web-of-trust/cmd/healthcheck` | `cd web-of-trust && make build` |
| `quarantine-rescue` | `quarantine-rescuer/cmd/quarantine-rescue` | `cd quarantine-rescuer && make build` |

All binaries output to `bin/` within their subsystem directory.

## Where to Add New Code

**New StrFry plugin (admission control variant):**
- Add `cmd/<name>/main.go` in `whitelist-plugin/`
- Implement `pkg/handler.Handler` and `pkg/handler.IOAdapter` interfaces
- Reuse `pkg/client.WhitelistClient` for whitelist checks

**New Dgraph query or mutation (web-of-trust):**
- Add method to `web-of-trust/pkg/dgraph/dgraph.go` on `*Client`
- Place raw DQL in `web-of-trust/queries/` if reusable

**New relay subscription type (web-of-trust):**
- Add to `web-of-trust/pkg/crawler/crawler.go` or new file in `web-of-trust/pkg/crawler/`
- Wire entry point in `web-of-trust/cmd/crawler/main.go`

**New forwarder sync strategy (event-forwarder):**
- Add `sync_<name>.go` in `event-forwarder/pkg/forwarder/`
- Implement the strategy interface alongside `sync_realtime.go` and `sync_windowed.go`

**New one-shot CLI tool:**
- Create `<name>/cmd/<name>/main.go` as a new Go module
- Add `Makefile` with standard targets: `build`, `test`, `lint`, `fmt`, `vet`, `tidy`, `clean`
- Use `internal/` for packages not intended for external reuse

**Config additions:**
- `whitelist-plugin`: add fields to `pkg/config/config.go`, update `LoadClientConfig` or `LoadServerConfig`
- `web-of-trust`: add fields to `pkg/config/config.go` Config struct with `mapstructure:` tags; defaults via Viper

## Special Directories

**`data/`**
- Purpose: Docker volume mounts for Dgraph and StrFry persistent data
- Generated: Yes (by running containers)
- Committed: No (gitignored contents)

**`config/dgraph/`**
- Purpose: Dgraph GraphQL schema (`schema.graphql`) — authoritative source for `Profile` type
- Committed: Yes — schema changes here must be applied to the running Dgraph instance

**`.planning/codebase/`**
- Purpose: GSD codebase map documents consumed by `/gsd-plan-phase` and `/gsd-execute-phase`
- Committed: Yes

**`web-of-trust/.planning/`** and **`spam/.planning/`**
- Purpose: Subsystem-level GSD planning artifacts (phases, research)
- Committed: Yes

---

*Structure analysis: 2026-06-10*
