# Whitelist Plugin

A two-component system that enforces web-of-trust based write access on StrFry relays. A centralized **server** holds an in-memory whitelist refreshed from Dgraph, and a thin **client plugin** on each StrFry instance forwards pubkey checks over HTTP.

## Overview

**Problem**: StrFry's writePolicy plugin interface spawns a subprocess per relay process. When streaming from 20+ upstream relays, each subprocess independently fetches the full whitelist from Dgraph, wasting resources and creating unnecessary load.

**Solution**: Split into two binaries:

- **Whitelist Server** (`cmd/server`) -- single instance that owns the Dgraph connection, maintains the in-memory whitelist cache, and exposes an HTTP API for pubkey checks.
- **Whitelist Client Plugin** (`cmd/whitelist`) -- thin StrFry writePolicy plugin that translates between StrFry's JSONL stdin/stdout protocol and the server's HTTP API.

```
                    Whitelist Server (single instance)
                    ┌──────────────────────────────────┐
                    │  Dgraph ←── WhitelistRefresher    │
                    │               ↓                   │
                    │  atomic.Pointer[map] (lock-free)  │
                    │               ↓                   │
                    │  HTTP :8081                        │
                    │    GET /check/{pubkey}             │
                    │    GET /health                     │
                    │    GET /stats                      │
                    └──────────────┬───────────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                     │
        StrFry stream 1      StrFry stream 2       StrFry stream N
        ┌─────────────┐     ┌─────────────┐      ┌─────────────┐
        │ stdin/stdout │     │ stdin/stdout │      │ stdin/stdout │
        │  ↕           │     │  ↕           │      │  ↕           │
        │ thin client  │     │ thin client  │      │ thin client  │
        │  → HTTP GET  │     │  → HTTP GET  │      │  → HTTP GET  │
        └─────────────┘     └─────────────┘      └─────────────┘
```

## Quick Start

### Prerequisites

- Go 1.24.1+
- Dgraph server running (for the server)
- Whitelist server running (for the client plugin)

### Build

```bash
# Build both binaries
make

# Or individually
make build          # Client plugin
make build-server   # Server
```

### Run

1. **Start the server** (needs Dgraph running):

```bash
./bin/whitelist-server
```

The server loads the whitelist from Dgraph, then listens on `:8081`.

2. **Verify the server**:

```bash
# Health check (200 = ready, 503 = still loading)
curl http://localhost:8081/health

# Check a pubkey
curl http://localhost:8081/check/d91191e30e00444b942c0e82cad470b32af171764c2275bee0bd99377efd4075

# Stats
curl http://localhost:8081/stats
```

3. **StrFry uses the client plugin** automatically -- it's compiled into the Docker image via `Dockerfile.strfry` and configured as the writePolicy plugin in `strfry.conf`.

## Server

### HTTP API

| Endpoint | Method | Description | Response |
|----------|--------|-------------|----------|
| `/check/{pubkey}` | GET | Check if a 64-char hex pubkey is whitelisted | `{"whitelisted": true}` or `{"whitelisted": false}` |
| `/health` | GET | Readiness check | `200 ok` when whitelist loaded, `503` before |
| `/stats` | GET | Cache statistics | `{"entries": 45000, "last_refresh": "2026-04-16T07:00:00Z"}` |

### How It Works

1. On startup, fetches all pubkeys from Dgraph via paginated GraphQL queries
2. Merges with a hardcoded set of known forwarder/admin pubkeys
3. Stores as a lock-free `atomic.Pointer[map[[32]byte]struct{}]` for O(1) lookups with zero contention
4. Refreshes on a configurable interval (default 6h) with retry and exponential backoff
5. Only starts accepting HTTP requests after the initial load completes

### Staleness

Between refreshes, the whitelist may be up to one `refresh_interval` behind Dgraph. A pubkey added to the web-of-trust graph will not be accepted until the next refresh completes.

### Failure Handling

If a refresh fails (Dgraph unreachable, timeout, etc.), the existing in-memory whitelist is preserved -- the server continues responding based on the last successful snapshot. Failures are logged to stderr.

### Configuration

Config file: `~/deepfry/whitelist.yaml` (auto-created with defaults if missing).

For Docker, the server config is mounted from `config/whitelist/whitelist-server.yaml`.

```yaml
dgraph_graphql_url: "http://localhost:8080/graphql"
refresh_interval: 6h
refresh_retry_count: 3
idle_conn_timeout: 90s
http_timeout: 30s
query_timeout: 20m
server_listen_addr: ":8081"
```

| Field | Default | Description |
|-------|---------|-------------|
| `dgraph_graphql_url` | `http://localhost:8080/graphql` | Dgraph GraphQL endpoint |
| `refresh_interval` | `6h` | How often to re-fetch the whitelist |
| `refresh_retry_count` | `3` | Retries per refresh cycle on failure |
| `idle_conn_timeout` | `90s` | HTTP keep-alive timeout to Dgraph |
| `http_timeout` | `30s` | Per-request timeout for Dgraph queries |
| `query_timeout` | `20m` | Total timeout for a full paginated fetch |
| `server_listen_addr` | `:8081` | Address to bind the HTTP server |

## Client Plugin

### How It Works

StrFry spawns the plugin as a subprocess and communicates via JSONL over stdin/stdout:

1. StrFry writes a JSON line to stdin with the event details
2. Plugin extracts the pubkey and makes `GET /check/{pubkey}` to the whitelist server
3. Plugin writes a JSON line to stdout with `accept` or `reject`

**Fail-closed**: if the server is unreachable or returns an error, the plugin rejects the event.

### StrFry Protocol

**Input** (stdin, one JSON line per event):
```json
{"type":"new","event":{"id":"abc123","pubkey":"d91191e3..."},"receivedAt":1757318499,"sourceType":"IP4","sourceInfo":"172.20.0.1"}
```

**Output** (stdout, one JSON line per event):
```json
{"id":"abc123","action":"accept","msg":""}
```

Or rejected:
```json
{"id":"abc123","action":"reject","msg":"rejected: not in web of trust"}
```

### Configuration

Config file: `~/deepfry/whitelist.yaml` (on the StrFry host/container).

For Docker, the client config is mounted from `config/whitelist/whitelist.yaml`.

```yaml
server_url: "http://whitelist-server:8081"
check_timeout: 2s
```

| Field | Default | Description |
|-------|---------|-------------|
| `server_url` | `http://localhost:8081` | Whitelist server URL |
| `check_timeout` | `2s` | HTTP request timeout per pubkey check |

## Docker Deployment

The whitelist system spans two compose files:

**`docker-compose.dgraph.yml`** -- contains the server alongside Dgraph:
```bash
docker-compose -f docker-compose.dgraph.yml up -d
```

**`docker-compose.strfry.yml`** -- contains StrFry with the client plugin:
```bash
docker-compose -f docker-compose.strfry.yml up -d
```

The server waits for Dgraph to be healthy before starting. StrFry's plugin connects to the server over the shared `deepfry-net` Docker network.

### Config Files in Docker

| File | Mounted to | Used by |
|------|-----------|---------|
| `config/whitelist/whitelist-server.yaml` | `/root/deepfry/whitelist.yaml` in whitelist-server | Server |
| `config/whitelist/whitelist.yaml` | `/root/deepfry/whitelist.yaml` in strfry | Client plugin |

## File Structure

```text
whitelist-plugin/
├── cmd/
│   ├── server/
│   │   └── main.go              # Server entry point
│   └── whitelist/
│       └── main.go              # Client plugin entry point (StrFry subprocess)
├── pkg/
│   ├── client/
│   │   ├── client.go            # HTTP client (Checker implementation)
│   │   └── client_test.go
│   ├── config/
│   │   └── config.go            # ServerConfig and ClientConfig with Viper
│   ├── handler/
│   │   ├── handler.go           # Checker, Handler, and IOAdapter interfaces
│   │   ├── messages.go          # StrFry JSONL protocol types
│   │   ├── whitelist_handler.go # Core accept/reject logic
│   │   └── jsonl_io_adapter.go  # JSONL serialization
│   ├── repository/
│   │   ├── repository.go        # KeyRepository interface
│   │   ├── dgraph_repository.go # Paginated GraphQL fetch from Dgraph
│   │   └── simple_repository.go # Hardcoded keys for testing
│   ├── server/
│   │   ├── server.go            # HTTP server (/check, /health, /stats)
│   │   └── server_test.go
│   └── whitelist/
│       ├── whitelist.go         # Lock-free in-memory map (atomic.Pointer)
│       └── whitelist_refresher.go # Background refresh goroutine
├── Makefile
└── go.mod
```

## Development

### Build Commands

```bash
make                     # Build both binaries
make build               # Build client plugin only
make build-server        # Build server only
make build-alpine        # Static client plugin for Alpine
make build-server-alpine # Static server for Alpine
make test                # Run all tests
make bench               # Run benchmarks
make fmt                 # Format code
make vet                 # Vet code
make lint                # Run golangci-lint
make clean               # Remove bin/
```

### Testing

```bash
# All tests
go test ./... -short

# Specific packages
go test ./pkg/server/...    # Server HTTP handler tests
go test ./pkg/client/...    # Client HTTP tests
go test ./pkg/handler/...   # StrFry protocol + handler tests
go test ./pkg/whitelist/... # Cache and refresher tests

# Benchmarks
make bench
```

### Key Interfaces

```go
// Checker abstracts whitelist lookups -- satisfied by both
// *whitelist.Whitelist (server-side) and *client.WhitelistClient (plugin-side)
type Checker interface {
    IsWhitelisted(pubkey string) bool
}

// KeyRepository fetches pubkeys from a backend (Dgraph, hardcoded, etc.)
type KeyRepository interface {
    GetAll(ctx context.Context) ([][32]byte, error)
}

// Handler processes StrFry plugin events
type Handler interface {
    Handle(input InputMsg) (OutputMsg, error)
}
```

The `Checker` interface is the key abstraction that decouples the handler from the whitelist implementation. The server uses `*whitelist.Whitelist` (local map lookup), while the client plugin uses `*client.WhitelistClient` (HTTP call to server). The handler doesn't know which one it's using.

## Requirements

| ID | Description | Status |
|--------|-------------|--------|
| FR-01 | Implement StrFry's stdin/stdout JSON plugin protocol | Done |
| FR-02 | Only events from pubkeys on the whitelist are accepted | Done |
| FR-03 | Whitelist provider is an abstraction (Checker interface) | Done |
| FR-04 | Provider errors do not crash plugin; fail closed | Done |
| FR-07 | Cache whitelist in memory with O(1) lookups | Done |
| FR-08 | Async periodic refresh with atomic swap (no read stalls) | Done |
| FR-09 | Expose last-refresh metadata (/stats endpoint) | Done |
| NFR-02 | Handle malformed JSON gracefully | Done |
| NFR-04 | Fail closed by default | Done |
| NFR-06 | Handle 10k events/sec in handler path | Done (benchmark verified) |
| NFR-08 | RSS < 128 MiB at steady state with 1M keys | Done |
| NFR-09 | Configurable refresh and timeouts via YAML | Done |
| NFR-10 | Resilience during refresh failure (preserve last snapshot) | Done |
