# Web of Trust

A Go service that crawls Nostr follow relationships (NIP-02) and builds a web of trust graph in Dgraph.

## Overview

This module fetches follow lists from Nostr relays and stores them as a directed graph in Dgraph, enabling web-of-trust calculations and social graph analysis.

## Quick Start

### Prerequisites

- Go 1.24.1+
- Dgraph server running on `localhost:9080`
- Access to Nostr relays

### Build

```bash
make build
```

Or individually:

```bash
make build-crawler
make build-pubkeys
make build-discover-relays
make build-healthcheck
```

### Run

1. **Configure**: Edit `~/deepfry/web-of-trust.yaml` with your settings (auto-created on first crawler run)
2. **Discover relays**: `./bin/discover-relays` (finds and tests the 50 fastest relays, adds them to config)
3. **Crawl follows**: `./bin/crawler`
4. **Export popular pubkeys**: `./bin/pubkeys`
5. **Check database health**: `./bin/healthcheck`

## Configuration

Config file location: `~/deepfry/web-of-trust.yaml`. A default config is auto-created on first crawler run.

```yaml
relay_urls:                          # Nostr relays to connect to
    - wss://relay.damus.io
    - wss://nos.lol
dgraph_addr: "localhost:9080"        # Dgraph server address
pubkey: "npub1..."                   # Starting pubkey to crawl (hex or npub)
timeout: "30s"                       # Relay operation timeout
stale_pubkey_threshold: 86400        # Seconds before a pubkey is re-crawled (default 24h)
debug: false                         # Enable debug logging
forward_relay_url: "ws://localhost:7777"  # Relay to forward all received events to (optional)
```

## File Structure

```text
web-of-trust/
├── cmd/
│   ├── crawler/           # Main crawler application
│   │   └── main.go        # Fetches follows from Nostr and stores in Dgraph
│   ├── discover-relays/   # Relay discovery and benchmarking tool
│   │   └── main.go        # Discovers, tests, and ranks relays for config
│   ├── healthcheck/       # Database health check tool
│   │   └── main.go        # Detects invalid/duplicate pubkeys, optional purge
│   └── pubkeys/           # Pubkey export utility
│       └── main.go        # Exports popular pubkeys to CSV
├── pkg/
│   ├── config/            # Shared configuration loading
│   ├── crawler/           # Core crawling logic
│   └── dgraph/            # Dgraph client and operations
├── queries/
│   └── explore.dql        # Sample Dgraph queries for data exploration
├── go.mod                 # Go module dependencies
└── README.md              # This file
```

## Components

### Crawler (`cmd/crawler/`)

The main application that:

- Connects to configured Nostr relays concurrently
- Fetches NIP-02 follow lists for specified pubkeys
- Stores follow relationships in Dgraph as directed edges
- Forwards all valid received events to a configurable relay (e.g., local StrFry instance)
- Automatic reconnection with exponential backoff for dead relays
- Provides crawling statistics and progress updates

**Usage**: `./bin/crawler`

### Discover Relays (`cmd/discover-relays/`)

A relay discovery and benchmarking tool that:

- Discovers relays via the nostr.watch API (NIP-66), with automatic fallback to NIP-65 relay list discovery from seed relays
- Pings each relay with a NIP-11 info document fetch to remove dead relays
- Tests a kind 3 subscription on each relay to verify responsiveness
- Ranks relays by total latency and adds the fastest to the config file

**Usage**: `./bin/discover-relays [flags]`

| Flag | Default | Description |
|------|---------|-------------|
| `--count` | 50 | Number of fastest relays to add |
| `--max-test` | 500 | Max relays to test from discovered pool (0 = all) |
| `--concurrency` | 50 | Parallel relay test workers |
| `--replace` | false | Replace existing relay_urls instead of merging |
| `--dry-run` | false | Print results without modifying config |

### Health Check (`cmd/healthcheck/`)

A database integrity tool that:

- Scans all pubkey nodes in Dgraph for invalid entries (not 64-char lowercase hex)
- Detects duplicate pubkey nodes that may exist from before the `@unique` constraint
- Reports findings with optional verbose detail (`-v`)
- Optionally purges bad entries (`-purge`), keeping the node with the newest event data

**Usage**: `./bin/healthcheck [flags]`

| Flag | Default | Description |
|------|---------|-------------|
| `-dgraph-addr` | `localhost:9080` | Dgraph gRPC address |
| `-v` | false | Print details of each bad entry |
| `-purge` | false | Delete invalid and duplicate nodes (prompts for confirmation) |

### Pubkeys Exporter (`cmd/pubkeys/`)

A utility that:

- Queries Dgraph for pubkeys with minimum follower counts
- Exports results to timestamped CSV files
- Useful for identifying popular/influential accounts

**Usage**: `./bin/pubkeys`

### Core Packages

- **`pkg/config/`**: Shared configuration loading via Viper (YAML, `~/deepfry/web-of-trust.yaml`)
- **`pkg/crawler/`**: Core crawling logic, multi-relay management, and Nostr client handling
- **`pkg/dgraph/`**: Dgraph client wrapper with graph operations for pubkey relationships

### Queries (`queries/`)

Contains sample Dgraph queries for exploring the web of trust data:

- Get all pubkeys with follow counts
- Query specific user relationships
- Explore multi-hop connections

## Data Model

The service stores pubkeys as nodes in Dgraph with follow relationships as directed edges:

```text
Pubkey Node:
- pubkey (string): hex-encoded public key
- kind3CreatedAt (timestamp): when the follow list was created
- last_db_update (timestamp): when this node was last updated
- follows -> [Pubkey]: directed edges to followed pubkeys
```

## Integration

This module is part of the DeepFry Nostr infrastructure:

- **Input**: Nostr NIP-02 follow events from relays
- **Output**: Web of trust graph in Dgraph
- **Dependencies**: Dgraph database
- **Consumers**: Other DeepFry services can query the trust graph

## Development

### Dependencies

Key dependencies (see `go.mod`):

- `github.com/dgraph-io/dgo/v210`: Dgraph client
- `github.com/nbd-wtf/go-nostr`: Nostr protocol implementation
- `github.com/spf13/viper`: Configuration management
- `google.golang.org/grpc`: gRPC communication

### Testing

```bash
go test ./...
```

### Exploring Data

Use Dgraph Ratel UI or the queries in `queries/explore.dql` to explore the crawled web of trust data.

## Future Enhancements

- Trust score calculations
- Integration with other DeepFry subsystems
