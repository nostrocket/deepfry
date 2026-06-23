# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

DeepFry is a modular backend stack for Humble Horse that surrounds a **stock (unmodified) StrFry Nostr relay** with sidecar services for discovery, trust scoring, and search. The core principle: never fork StrFry — extend via its JSON plugin interface and external services.

**Data separation rule:** canonical events live only in StrFry's LMDB. Dgraph stores ID-only graphs (pubkey relationships). No event payloads outside StrFry.

## Project Boundary Rule

This repo is a monorepo of **independent projects**, each a self-contained subdirectory with its own module, build, and (where present) `.planning/` planning state — e.g. `web-of-trust`, `web-of-trust-explorer`, `event-forwarder`, `whitelist-plugin`, `quarantine-rescuer`, `LMDB2GraphQL`, `spam`, `spam-explorer`.

**Do not cross between projects in the same request without explicit permission from the user.** When the user is working in one project, scope ALL work — edits, planning, progress reports, status, routing, commands — to that project only.

- Determine the active project from the working directory the user invoked you in (and what they explicitly name). Treat each project's `.planning/` as authoritative ONLY for that project.
- If a task appears to require touching or reporting on another project, STOP and ask for explicit permission first, naming the other project and why.
- Recent git activity in a sibling project is NOT permission to act on or report it — confirm which project the user means.

## Architecture

```
Upstream Relays → Event Forwarder → StrFry Relay (LMDB, port 7777)
                                      ├── Whitelist Plugin → Dgraph (accept/reject writes)
                                      └── Search Plugin (planned) → Semantic Search (planned)

Web of Trust Crawler → subscribes kind 3 from StrFry → writes pubkey graph to Dgraph
```

**Production-ready subsystems:** event-forwarder, whitelist-plugin, web-of-trust, quarantine-rescuer
**Placeholder subsystems:** search-plugin, semantic-search, embeddings-generator, profile-builder, thread-inference

## Build & Test Commands

Each subsystem is an independent Go module with its own Makefile. Always `cd` into the subsystem first.

### Common targets (all subsystems)
```bash
make build          # Compile to bin/
make test           # Unit tests (-short)
make lint           # golangci-lint (warns, doesn't fail)
make lint-fix       # Auto-fix lint issues
make fmt            # go fmt
make vet            # go vet
make tidy           # go mod tidy
make clean          # Remove bin/
```

### Event Forwarder (`cd event-forwarder`)
```bash
make build-alpine       # Static binary for Alpine (CGO_ENABLED=0)
make docker-build       # Docker image with version injection
make test-integration   # Integration tests
```

### Web of Trust (`cd web-of-trust`)
```bash
make build-crawler      # Build crawler binary
make build-pubkeys      # Build pubkeys exporter
```

### Whitelist Plugin (`cd whitelist-plugin`)
```bash
make bench              # Run benchmarks
make build-alpine       # Static Alpine binary
```

### Quarantine Rescuer (`cd quarantine-rescuer`)
One-shot CLI that pulls events for newly-whitelisted pubkeys from the
quarantine LMDB back into the main StrFry instance. Reads `~/deepfry/whitelist.yaml`
for the whitelist server URL (same config the live plugin uses). Runs on the
strfry host; calls `docker exec` against the quarantine container.
```bash
make build              # ./bin/quarantine-rescue
./bin/quarantine-rescue --dry-run   # preview only
```

## Infrastructure

```bash
# Start Dgraph + Ratel UI + Whitelist Server
docker-compose -f docker-compose.dgraph.yml up -d

# Start StrFry
docker-compose -f docker-compose.strfry.yml up -d

# Start event forwarders (requires .env with keys)
docker-compose -f docker-compose.evtfwd.yml up -d
```

- StrFry: `ws://localhost:7777`
- Dgraph HTTP/GraphQL: `http://localhost:8080`
- Dgraph gRPC: `localhost:9080`
- Dgraph Ratel UI: `http://localhost:8000`

Secrets go in `.env` (see `.env.example`). Keys: `STRFRY_PRIVATE_KEY`, `NOSTR_SYNC_SECKEY_LIVE`, `NOSTR_SYNC_SECKEY_HISTORY`.

## Config Files

All DeepFry config files live in `~/deepfry/`. Never delete, overwrite, or `rm` files in this directory. When testing config loading, use a temporary directory instead.

- `~/deepfry/web-of-trust.yaml` — web-of-trust crawler config
- `~/deepfry/whitelist.yaml` — whitelist plugin config

## Protocol Rules

- StrFry must stay unmodified — extend only via its stdin/stdout JSON plugin protocol
- Use standard Nostr WebSocket (NIP-01) for all relay communication
- Sync progress events must be kind 30078 with tags: `d`, `from`, `to`
- One forwarder instance per source relay to avoid race conditions
- Secrets via env vars, never logged raw

## Key Dependencies

- Go 1.24.1+
- `github.com/nbd-wtf/go-nostr` — Nostr protocol library
- `github.com/dgraph-io/dgo/v210` — Dgraph gRPC client (web-of-trust)
- `github.com/spf13/viper` — YAML config (web-of-trust)
- golangci-lint (optional, for linting)

## Dgraph Schema

The `Profile` type in `config/dgraph/schema.graphql` has: `pubkey` (@id), `follows`, `followers`, `kind3CreatedAt`, `last_db_update`. The whitelist plugin queries this to determine which pubkeys may write events.
