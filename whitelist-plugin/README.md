# Whitelist Plugin

A system that enforces web-of-trust based write access on StrFry relays. A centralized **server** holds an in-memory whitelist refreshed from Dgraph; thin **plugin binaries** on each StrFry instance forward pubkey checks over HTTP.

## Overview

**Problem**: StrFry's writePolicy plugin interface spawns a subprocess per relay process. When streaming from 20+ upstream relays, each subprocess independently fetches the full whitelist from Dgraph, wasting resources and creating unnecessary load.

**Solution**: Three binaries in this module:

- **Whitelist Server** (`cmd/server`) -- single instance that owns the Dgraph connection, maintains the in-memory whitelist cache, and exposes an HTTP API for pubkey checks.
- **Whitelist Client Plugin** (`cmd/whitelist`) -- thin StrFry writePolicy plugin that translates between StrFry's JSONL stdin/stdout protocol and the server's HTTP API. Accepts events from whitelisted pubkeys, rejects everything else.
- **Router Plugin** (`cmd/router`) -- drop-in alternative to the whitelist plugin that additionally forwards non-whitelisted events (kinds 0, 1, 3) to a quarantine relay for later analysis. Used as a data-gathering tool to detect parallel webs-of-trust and inform future classifier/review work. See [`quarantine/SPEC.md`](../quarantine/SPEC.md) for the full specification. The whitelist plugin remains available and unchanged — the router is opt-in.

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
# Build all three binaries
make

# Or individually
make build          # Client plugin (cmd/whitelist)
make build-server   # Whitelist server (cmd/server)
make build-router   # Router plugin (cmd/router)
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

## Router Plugin (optional)

A drop-in alternative to the client plugin that performs the same accept/reject decision on mainline StrFry, but **additionally** forwards rejected events (kinds 0, 1, 3 only) to a separate quarantine StrFry relay. This gives operators a queryable record of what the whitelist is rejecting — needed for detecting parallel webs-of-trust and for any future spam classifier / review pipeline.

The router is opt-in: the existing `whitelist` binary is unaffected. Swap by changing `config/strfry/strfry.conf:99` from `plugin = "/app/plugins/whitelist"` to `plugin = "/app/plugins/router"` and restarting StrFry.

Full specification: [`quarantine/SPEC.md`](../quarantine/SPEC.md).

### How It Works

1. Receives an event from StrFry over stdin (same JSONL protocol as the whitelist plugin).
2. Calls the whitelist server (`GET /check/{pubkey}`).
3. Whitelisted → returns `accept`.
4. Not whitelisted → applies a **heuristic filter** (kind ∈ {0, 1, 3}, content ≤ 256 KiB, non-empty id/pubkey). If the event passes, it is enqueued for async publication to the quarantine relay via a persistent WebSocket connection. Either way, returns `reject` to mainline.

The quarantine publish is **fire-and-forget**: the plugin's stdout response is never delayed by the quarantine path. The publish runs on a background goroutine with a bounded channel; when the channel is full the event is dropped and a counter is incremented.

### Quarantine Path Invariants

- Mainline StrFry's accept/reject decision is never affected by quarantine failures (connection loss, queue full, upstream errors).
- Only kinds 0 (profile), 1 (text note), 3 (contacts) are forwarded — everything else is dropped at the heuristic gate to keep the quarantine LMDB focused on signal relevant to parallel-WoT analysis.
- Event payloads only live in StrFry LMDB, never in logs or Dgraph (plugin stderr logs pubkey prefix + event id, never content).

### Configuration

Config file: `~/deepfry/router.yaml` (auto-created with defaults if missing). Env overrides use the `ROUTER_` prefix (e.g. `ROUTER_QUARANTINE_ENABLED=false`).

```yaml
server_url: "http://whitelist-server:8081"
check_timeout: 2s

quarantine:
  enabled: true
  relay_url: "ws://strfry-quarantine:7778"
  buffer_size: 10000
  publish_timeout: 5s
  metrics_interval: 60s
```

| Field | Default | Description |
|-------|---------|-------------|
| `server_url` | `http://localhost:8081` | Whitelist server URL |
| `check_timeout` | `2s` | HTTP request timeout per pubkey check |
| `quarantine.enabled` | `true` | When false, behaves byte-identically to the whitelist plugin (no side-channel) |
| `quarantine.relay_url` | `ws://strfry-quarantine:7778` | WebSocket URL of the quarantine relay |
| `quarantine.buffer_size` | `10000` | Bounded channel capacity; events dropped when full |
| `quarantine.publish_timeout` | `5s` | Per-publish and per-connect timeout |
| `quarantine.metrics_interval` | `60s` | How often the publisher logs counters to stderr |

### Quarantine StrFry

A second StrFry instance on port 7778 (internal-only, no host port published) with no writePolicy plugin. Config lives in `config/strfry/strfry-quarantine.conf`.

The quarantine container runs with a **fail-fast DB isolation guard** (`config/strfry/quarantine-db-guard.sh`) as its entrypoint. The guard refuses to start if:

- the configured `db` path equals the mainline `db` path (exit 2),
- the configured path does not equal the expected quarantine path (exit 3), or
- the mainline DB path is visible inside the container's filesystem (exit 4, volume-mount leak).

This protects mainline data from any misconfiguration. The guard test matrix (`config/strfry/quarantine-db-guard_test.sh`) exercises all cases.

## Docker Deployment

The whitelist system spans two compose files:

**`docker-compose.dgraph.yml`** -- contains the server alongside Dgraph:
```bash
docker-compose -f docker-compose.dgraph.yml up -d
```

**`docker-compose.strfry.yml`** -- contains mainline StrFry (with both plugin binaries baked in) and the `strfry-quarantine` instance:
```bash
docker-compose -f docker-compose.strfry.yml up -d
```

The server waits for Dgraph to be healthy before starting. Both plugin binaries (`/app/plugins/whitelist` and `/app/plugins/router`) are shipped in the same image; mainline's `strfry.conf:99` selects which one runs. The StrFry plugin connects to the server over the shared `deepfry-net` Docker network. The router plugin additionally publishes to `strfry-quarantine:7778` over the same network.

### Config Files in Docker

| File | Mounted to | Used by |
|------|-----------|---------|
| `config/whitelist/whitelist-server.yaml` | `/root/deepfry/whitelist.yaml` in whitelist-server | Server |
| `config/whitelist/whitelist.yaml` | `/root/deepfry/whitelist.yaml` in strfry | Client plugin |
| `config/strfry/strfry-quarantine.conf` | `/etc/strfry.conf` in strfry-quarantine | Quarantine relay |
| `config/strfry/quarantine-db-guard.sh` | `/usr/local/bin/quarantine-db-guard.sh` in strfry-quarantine | DB isolation guard (entrypoint) |

## File Structure

```text
whitelist-plugin/
├── cmd/
│   ├── server/
│   │   └── main.go              # Server entry point
│   ├── whitelist/
│   │   └── main.go              # Client plugin entry point (StrFry subprocess)
│   └── router/
│   │   └── main.go              # Router plugin entry point (quarantine-routing variant)
├── pkg/
│   ├── client/
│   │   ├── client.go            # HTTP client (Checker implementation)
│   │   └── client_test.go
│   ├── config/
│   │   ├── config.go            # ServerConfig and ClientConfig with Viper
│   │   └── router_config.go     # RouterConfig (server + quarantine sections)
│   ├── handler/
│   │   ├── handler.go           # Checker, Handler, and IOAdapter interfaces
│   │   ├── messages.go          # StrFry JSONL protocol types (+ RouterInputMsg)
│   │   ├── whitelist_handler.go # Core accept/reject logic
│   │   ├── jsonl_io_adapter.go  # JSONL serialization (whitelist plugin)
│   │   ├── router_handler.go    # Router accept/reject/quarantine logic
│   │   └── router_io_adapter.go # JSONL serialization (router plugin)
│   ├── heuristics/
│   │   └── heuristics.go        # Pre-quarantine garbage gate (kind 0/1/3 allowlist)
│   ├── quarantine/
│   │   └── publisher.go         # Async go-nostr publisher with bounded channel + reconnect
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
make                     # Build all three binaries
make build               # Build client plugin only
make build-server        # Build whitelist server only
make build-router        # Build router plugin only
make build-alpine        # Static client plugin for Alpine
make build-server-alpine # Static server for Alpine
make build-router-alpine # Static router plugin for Alpine
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
go test ./pkg/server/...     # Server HTTP handler tests
go test ./pkg/client/...     # Client HTTP tests
go test ./pkg/handler/...    # StrFry protocol + both handler tests
go test ./pkg/whitelist/...  # Cache and refresher tests
go test ./pkg/heuristics/... # Router pre-quarantine filter
go test ./pkg/quarantine/... # Publisher backpressure + reconnect

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

// Handler processes StrFry plugin events (whitelist plugin; id/pubkey only).
type Handler interface {
    Handle(input InputMsg) (OutputMsg, error)
}

// EventEnqueuer is the subset of the quarantine publisher the router handler
// depends on. Kept as an interface so the handler can be tested without the
// WebSocket machinery.
type EventEnqueuer interface {
    Enqueue(evt nostr.Event) bool
}
```

The `Checker` interface is the key abstraction that decouples the handler from the whitelist implementation. The server uses `*whitelist.Whitelist` (local map lookup), while both plugins use `*client.WhitelistClient` (HTTP call to server). The router plugin additionally depends on `EventEnqueuer` (implemented by `*quarantine.Publisher`) but uses it only after a reject decision — it never affects whether the event is accepted.

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
