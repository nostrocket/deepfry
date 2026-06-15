# Codebase Structure

**Analysis Date:** 2026-06-15

## Directory Layout

```
deepfry/
├── config/                          # Configuration files for services
│   ├── dgraph/                      # Dgraph GraphQL schema and entrypoint
│   │   ├── schema.graphql           # Profile type definition
│   │   └── entrypoint.sh            # Schema bootstrap script
│   ├── strfry/                      # StrFry relay configuration
│   │   ├── strfry.conf              # Main relay config
│   │   ├── strfry-quarantine.conf   # Quarantine relay config
│   │   └── quarantine-db-guard.sh   # Safety guard prevents mainline corruption
│   └── whitelist/                   # Whitelist plugin configuration
│       ├── whitelist.yaml           # Whitelist server URL
│       └── router.yaml              # Router plugin config
├── data/                            # Local database storage (on-disk)
│   ├── strfry-db/                   # Main StrFry LMDB (canonical event store)
│   ├── strfry-quarantine-db/        # Quarantine StrFry LMDB
│   └── dgraph/                      # Dgraph data directory
├── docs/                            # Documentation and architecture diagrams
│   ├── architecture/                # Architecture documentation
│   ├── images/                      # Diagrams and visual references
│   └── *.md                         # Deployment, setup guides
├── web-of-trust/                    # Go module: trust graph crawler & analysis
│   ├── cmd/                         # Executable entry points
│   │   ├── crawler/                 # Main crawler loop for pubkey discovery
│   │   ├── clusterscan/             # Trust propagation & spam detection
│   │   ├── pubkeys/                 # Export pubkeys with followers to CSV
│   │   ├── discover-relays/         # NIP-65 relay discovery
│   │   └── healthcheck/             # Dgraph health & cleanup tool
│   ├── pkg/                         # Shared libraries
│   │   ├── config/                  # YAML config loader (~/deepfry/web-of-trust.yaml)
│   │   ├── crawler/                 # Relay pool, kind 3 subscription, chunking
│   │   └── dgraph/                  # Dgraph client: upsert, queries, clusterscan
│   ├── go.mod                       # Go module definition
│   ├── Makefile                     # Build targets: build-crawler, build-pubkeys, test
│   └── README.md                    # Module documentation
├── whitelist-plugin/                # Go module: event write policy plugins
│   ├── cmd/                         # Executable entry points
│   │   ├── whitelist/               # StrFry plugin: stdin/stdout JSON handler
│   │   ├── server/                  # Whitelist server: HTTP /check endpoint
│   │   └── router/                  # StrFry plugin: route rejected events to quarantine
│   ├── pkg/                         # Shared libraries
│   │   ├── handler/                 # JSONL parsing, accept/reject logic
│   │   ├── client/                  # Whitelist server client
│   │   ├── repository/              # Dgraph repository for pubkey lookups
│   │   ├── heuristics/              # Decision heuristics (flags, cutoffs)
│   │   ├── quarantine/              # Router-side event publisher
│   │   ├── server/                  # HTTP server setup, endpoints
│   │   ├── version/                 # Build version injection
│   │   └── config/                  # Plugin configuration
│   ├── go.mod                       # Go module definition
│   ├── Makefile                     # Build targets: build, test, bench
│   └── README.md                    # Module documentation
├── event-forwarder/                 # Go module: upstream relay event syncing
│   ├── cmd/fwd/                     # Event forwarder CLI/TUI
│   │   ├── main.go                  # Entry point
│   │   ├── cli.go                   # CLI mode (quiet, progress bars)
│   │   └── tui.go                   # TUI mode (interactive)
│   ├── pkg/                         # Shared libraries
│   │   ├── config/                  # Load config from env vars
│   │   ├── forwarder/               # Main forwarder loop, modes (windowed/realtime)
│   │   ├── crypto/                  # Key signing and derivation
│   │   ├── telemetry/               # Metrics aggregation
│   │   └── version/                 # Build version
│   ├── go.mod                       # Go module definition
│   ├── Makefile                     # Build targets: build, test-integration
│   ├── docs/telemetry.md            # Telemetry documentation
│   └── README.md                    # Module documentation
├── quarantine-rescuer/              # Go module: quarantine event re-qualification
│   ├── cmd/quarantine-rescue/       # CLI entry point
│   ├── internal/                    # Internal packages (not exported)
│   │   ├── lmdbreader/              # Read-only quarantine LMDB cursor
│   │   ├── exporter/                # Export events from quarantine
│   │   ├── whitelist/               # Check pubkey whitelist
│   │   ├── forwarder/               # Publish to mainline StrFry
│   │   ├── deleter/                 # Batch delete from quarantine
│   │   ├── runner/                  # Orchestrate all phases
│   │   ├── envfile/                 # Parse docker env files
│   │   └── event/                   # Event types and helpers
│   ├── go.mod                       # Go module definition
│   ├── Makefile                     # Build targets
│   └── README.md                    # Module documentation
├── spam/                            # Rust crate: LMDB to GraphQL access layer
│   ├── src/                         # Rust source
│   │   ├── main.rs                  # Entry point: startup gate, listener bind, LMDB init
│   │   ├── lib.rs                   # Library crate definition
│   │   ├── config.rs                # Config loader (~/deepfry/lmdb2graphql.yaml)
│   │   ├── server.rs                # HTTP server (axum) routing
│   │   ├── lmdb/                    # LMDB reader, validation, payload decoding
│   │   │   ├── env.rs               # LMDB environment setup
│   │   │   ├── meta.rs              # Metadata validation
│   │   │   ├── self_check.rs        # Comparator & schema validation gate
│   │   │   ├── scan.rs              # Event iteration
│   │   │   ├── payload.rs           # Event decoding
│   │   │   ├── indexes.rs           # B-tree index scanning
│   │   │   ├── types.rs             # LMDB types
│   │   │   └── comparators.rs       # B-tree comparators
│   │   ├── query/                   # Query engine (Phase 3)
│   │   │   ├── engine.rs            # Query execution
│   │   │   ├── filter.rs            # Filter logic
│   │   │   ├── hydrate.rs           # Payload hydration
│   │   │   ├── merge.rs             # Result merging
│   │   │   ├── router.rs            # Request routing
│   │   │   └── mod.rs               # Module exports
│   │   ├── graphql/                 # GraphQL layer (Phase 4)
│   │   │   ├── schema.rs            # Schema builder, AppState
│   │   │   ├── types.rs             # GraphQL output types
│   │   │   ├── resolvers.rs         # Resolver implementations
│   │   │   └── mod.rs               # Module exports
│   └── Cargo.toml                   # Rust manifest
│   ├── rust-toolchain.toml          # Rust version pin
│   ├── Makefile                     # Build targets
│   └── README.md                    # Module documentation
├── docker-compose.dgraph.yml        # Dgraph + Ratel + Whitelist Server services
├── docker-compose.strfry.yml        # StrFry + StrFry Quarantine services
├── docker-compose.evtfwd.yml        # Event Forwarders (per upstream relay)
├── docker-compose.lmdb2graphql.yml  # LMDB to GraphQL service
├── Dockerfile.strfry                # StrFry image with plugins
├── Dockerfile.whitelist-server      # Whitelist Server image
├── .env.example                     # Example environment variables
├── .gitignore                       # Git ignore rules
├── CLAUDE.md                        # Project context for Claude Code
├── README.md                        # Top-level documentation
└── LICENSE                          # License

Placeholder subsystems (not yet production):
├── search-plugin/                   # NIP-50 search plugin stub
├── embeddings-generator/            # Embeddings generator stub
├── profile-builder/                 # Profile aggregation stub
├── thread-inference/                # Thread graph builder stub
└── quarantine/                      # Quarantine relay spec and setup
```

## Directory Purposes

**`config/`:**
- Purpose: Configuration files for all services (shared mounts into Docker containers)
- Contains: YAML files, shell scripts, SQL schema definitions
- Key files: `dgraph/schema.graphql` (pubkey type), `strfry/*.conf` (relay configs), `whitelist/whitelist.yaml` (server URL)

**`data/`:**
- Purpose: Local on-disk storage for databases (volume mounts into Docker)
- Contains: LMDB databases (StrFry, quarantine), Dgraph data directory
- Generated: Yes (created on first run)
- Committed: No (git-ignored)

**`web-of-trust/`:**
- Purpose: Go module for crawling the trust graph (pubkey relationships)
- Contains: Crawler loop, Dgraph client, clustering/analysis tools
- Key tools: `bin/crawler` (main), `bin/clusterscan`, `bin/pubkeys`, `bin/healthcheck`

**`whitelist-plugin/`:**
- Purpose: Go module for event write policy and quarantine routing
- Contains: StrFry plugins (stdin/stdout handlers), whitelist server (HTTP)
- Key tools: `bin/whitelist` (plugin), `bin/server` (HTTP service), `bin/router` (plugin)

**`event-forwarder/`:**
- Purpose: Go module for syncing events from upstream relays to StrFry
- Contains: Relay subscription, windowed/realtime sync modes, TUI/CLI
- Key tool: `bin/fwd` (main, one per upstream relay)

**`quarantine-rescuer/`:**
- Purpose: Go module for re-qualifying and replaying quarantine events
- Contains: LMDB reader, whitelist checker, event publisher, batch deleter
- Key tool: `bin/quarantine-rescue` (one-shot CLI)

**`spam/`:**
- Purpose: Rust crate for read-only GraphQL access to StrFry LMDB
- Contains: LMDB reader with validation gates, GraphQL schema, axum HTTP server
- Key service: `lmdb2graphql` (Docker service on port 8082)

## Key File Locations

**Entry Points:**
- `web-of-trust/cmd/crawler/main.go` — Web of Trust crawler loop (subscribes kind 3)
- `web-of-trust/cmd/clusterscan/main.go` — Trust analysis and spam detection
- `event-forwarder/cmd/fwd/main.go` — Event forwarder (sync upstream relays)
- `whitelist-plugin/cmd/whitelist/main.go` — Write policy plugin (StrFry)
- `whitelist-plugin/cmd/server/main.go` — Whitelist HTTP server
- `whitelist-plugin/cmd/router/main.go` — Quarantine router plugin (StrFry)
- `quarantine-rescuer/cmd/quarantine-rescue/main.go` — Rescue quarantine events
- `spam/src/main.rs` — GraphQL service startup (Rust)

**Configuration:**
- `config/dgraph/schema.graphql` — Dgraph Profile type: `pubkey` (@id), `follows`, `followers`, `kind3CreatedAt`, `last_db_update`
- `config/strfry/strfry.conf` — StrFry main relay config (listen port, plugin path)
- `config/strfry/strfry-quarantine.conf` — Quarantine relay config (separate DB)
- `config/whitelist/whitelist.yaml` — Whitelist server URL (loaded by plugin at startup)
- `.env.example` — Template for secrets and paths (`STRFRY_PRIVATE_KEY`, `STRFRY_DB_PATH`, etc.)

**Core Logic:**
- `web-of-trust/pkg/crawler/crawler.go` — Relay pool, kind 3 subscription, event collection
- `web-of-trust/pkg/dgraph/dgraph.go` — Dgraph client (upsert, queries, transactions)
- `web-of-trust/pkg/config/config.go` — YAML config loading and persistence
- `whitelist-plugin/pkg/handler/whitelist_handler.go` — Event accept/reject logic
- `whitelist-plugin/pkg/client/client.go` — HTTP client for whitelist server checks
- `event-forwarder/pkg/forwarder/forwarder.go` — Main forwarder loop
- `quarantine-rescuer/internal/runner/runner.go` — Rescue orchestration
- `spam/src/server.rs` — GraphQL HTTP server setup

**Testing:**
- `web-of-trust/cmd/crawler/main_test.go` — Crawler tests
- `whitelist-plugin/pkg/handler/whitelist_handler_test.go` — Handler tests
- `whitelist-plugin/pkg/handler/router_handler_test.go` — Router plugin tests
- `event-forwarder/cmd/fwd/main_test.go` — Forwarder tests
- `quarantine-rescuer/internal/lmdbreader/reader_test.go` — LMDB reader tests
- `spam/tests/` — Integration tests for Rust crate

## Naming Conventions

**Files:**
- Go: `main.go`, `crawler.go`, `handler.go`, `types.go` (lowercase with underscores)
- Rust: `main.rs`, `lib.rs`, `server.rs`, `config.rs` (lowercase with underscores)
- Config: `*.yaml`, `*.conf`, `*.graphql` (service-specific)
- Tests: `*_test.go` (Go), `*_test.rs` (Rust, via `#[cfg(test)]` modules)

**Directories:**
- Commands: `cmd/` (each tool is a separate `cmd/toolname/` directory)
- Libraries: `pkg/` (Go packages shared across commands) or `src/` (Rust modules)
- Internal: `internal/` (Go packages not exported to other modules)
- Config: `config/servicename/` (mounted read-only into Docker)
- Data: `data/servicename/` (persistent volumes)

## Where to Add New Code

**New Feature (e.g., additional crawler logic):**
- Primary code: `web-of-trust/cmd/crawler/main.go` or `web-of-trust/pkg/crawler/` (new file)
- Tests: `web-of-trust/cmd/crawler/main_test.go` or `web-of-trust/pkg/crawler/newfeature_test.go`

**New GraphQL Query (Rust spam service):**
- Schema: `spam/src/graphql/schema.rs` (add field to AppSchema)
- Types: `spam/src/graphql/types.rs` (add output type)
- Resolver: `spam/src/graphql/resolvers.rs` (implement resolver function)
- Test: `spam/tests/graphql_test.rs` (integration test)

**New StrFry Plugin:**
- Entry: `whitelist-plugin/cmd/pluginname/main.go`
- Handler: `whitelist-plugin/pkg/handler/pluginname_handler.go`
- IO Adapter: `whitelist-plugin/pkg/handler/pluginname_io_adapter.go`
- Tests: `whitelist-plugin/pkg/handler/pluginname_handler_test.go`

**New CLI Tool (e.g., query utility):**
- Entry: `web-of-trust/cmd/toolname/main.go`
- Shared logic: `web-of-trust/pkg/existing-package/` (reuse existing modules)
- Makefile target: `web-of-trust/Makefile` (add `build-toolname` target)

**Utilities / Shared Helpers:**
- Shared helpers (crypto, config, etc.): `web-of-trust/pkg/config/`, `event-forwarder/pkg/crypto/`
- Test fixtures: in-package under `_test.go` files or exported from `pkg/testutil/`

**Docker Services:**
- New service: Create `docker-compose.servicename.yml` in repo root
- Image: Add `Dockerfile.servicename` or reference existing image
- Config: Create `config/servicename/` directory with mounted files
- Data: Create `data/servicename/` directory if persistent storage needed

## Special Directories

**`quarantine/`:**
- Purpose: Safety specification for the quarantine relay (prevents mainline corruption)
- Generated: No
- Committed: Yes
- Contains: SPEC.md, safety guards, database isolation rules

**`.planning/`:**
- Purpose: Phase planning and roadmap (generated by GSD tools)
- Generated: Yes (by `/gsd-plan-phase`, `/gsd-execute-phase`)
- Committed: Yes (tracking decision history)

**`.claude/`:**
- Purpose: Claude AI agent configuration and skills
- Generated: No (manually maintained)
- Committed: Yes

**`.github/`:**
- Purpose: GitHub-specific configuration (copilot instructions, workflows)
- Generated: No (manually maintained)
- Committed: Yes

**`docs/`:**
- Purpose: Developer and operator documentation
- Generated: No (manually written)
- Committed: Yes
- Key: `docs/architecture/` (system overview), deployment guides

---

*Structure analysis: 2026-06-15*
