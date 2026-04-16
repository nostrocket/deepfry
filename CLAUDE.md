# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

DeepFry is a modular backend stack for Humble Horse that surrounds a **stock (unmodified) StrFry Nostr relay** with sidecar services for discovery, trust scoring, and search. The core principle: never fork StrFry — extend via its JSON plugin interface and external services.

**Data separation rule:** canonical events live only in StrFry's LMDB. Dgraph stores ID-only graphs (pubkey relationships). No event payloads outside StrFry.

## Architecture

```
Upstream Relays → Event Forwarder → StrFry Relay (LMDB, port 7777)
                                      ├── Whitelist Plugin → Dgraph (accept/reject writes)
                                      └── Search Plugin (planned) → Semantic Search (planned)

Web of Trust Crawler → subscribes kind 3 from StrFry → writes pubkey graph to Dgraph
```

**Production-ready subsystems:** event-forwarder, whitelist-plugin, web-of-trust
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

## Infrastructure

```bash
# Start StrFry + Dgraph + Ratel UI
docker-compose up -d

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
