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
go build -o bin/crawler ./cmd/crawler
go build -o bin/pubkeys ./cmd/pubkeys
```

### Run

1. **Configure**: Edit `config/config.yaml` with your settings
2. **Crawl follows**: `./bin/crawler`
3. **Export popular pubkeys**: `./bin/pubkeys`

## Configuration

Edit `config/config.yaml`:

```yaml
relay_url: "wss://relay.damus.io"    # Nostr relay to connect to
dgraph_addr: "localhost:9080"        # Dgraph server address
pubkey: "npub1..."                   # Starting pubkey to crawl (hex or npub)
timeout: "30s"                       # Relay operation timeout
```

## File Structure

```text
web-of-trust/
├── cmd/
│   ├── crawler/         # Main crawler application
│   │   └── main.go      # Fetches follows from Nostr and stores in Dgraph
│   └── pubkeys/         # Pubkey export utility
│       └── main.go      # Exports popular pubkeys to CSV
├── config/
│   └── config.yaml      # Configuration file
├── pkg/
│   ├── crawler/         # Core crawling logic
│   └── dgraph/          # Dgraph client and operations
├── queries/
│   └── explore.dql      # Sample Dgraph queries for data exploration
├── go.mod               # Go module dependencies
└── README.md            # This file
```

## Components

### Crawler (`cmd/crawler/`)

The main application that:

- Connects to a Nostr relay
- Fetches NIP-02 follow lists for specified pubkeys
- Stores follow relationships in Dgraph as directed edges
- Provides crawling statistics and progress updates

**Usage**: `./bin/crawler`

### Pubkeys Exporter (`cmd/pubkeys/`)

A utility that:
- Queries Dgraph for pubkeys with minimum follower counts
- Exports results to timestamped CSV files
- Useful for identifying popular/influential accounts

**Usage**: `./bin/pubkeys`

### Core Packages

- **`pkg/crawler/`**: Contains the core crawling logic and Nostr client handling
- **`pkg/dgraph/`**: Dgraph client wrapper with graph operations for pubkey relationships

### Queries (`queries/`)

Contains sample Dgraph queries for exploring the web of trust data:
- Get all pubkeys with follow counts
- Query specific user relationships
- Explore multi-hop connections

## Data Model

The service stores pubkeys as nodes in Dgraph with follow relationships as directed edges:

```
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

- Batch processing of multiple seed pubkeys
- Automatic detection of stale data for re-crawling
- Trust score calculations
- Integration with other DeepFry subsystems
