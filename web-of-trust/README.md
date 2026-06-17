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
make build-clusterscan
```

> Build the crawler via the Makefile (not a bare `go build`) so the git commit
> and build timestamp are injected via ldflags — the crawler uses the commit as
> the default `round_id` for speed metrics (see [Crawler Metrics](#crawler-metrics)).

### Run

1. **Configure**: Edit `~/deepfry/web-of-trust.yaml` with your settings (auto-created on first crawler run)
2. **Discover relays**: `./bin/discover-relays` (finds and tests the 50 fastest relays, adds them to config)
3. **Crawl follows**: `./bin/crawler`
4. **Export popular pubkeys**: `./bin/pubkeys`
5. **Check database health**: `./bin/healthcheck`
6. **Detect spam clusters**: `./bin/clusterscan`

## Configuration

Config file location: `~/deepfry/web-of-trust.yaml`. A default config is auto-created on first crawler run.

```yaml
relay_urls:                          # Nostr relays to connect to
    - wss://relay.damus.io
    - wss://nos.lol
dgraph_addr: "localhost:9080"        # Dgraph server address
pubkey: "npub1..."                   # Seed pubkey to crawl (hex or npub)
timeout: "15s"                       # Per-batch relay query timeout
stale_pubkey_threshold: 86400        # Seconds before a pubkey is re-crawled (default 24h)
relay_filter_batch_size: 100         # Pubkeys fetched per loop / max authors per relay filter
debug: false                         # Enable debug logging (verbose relay + metrics output)
forward_relay_url: "ws://localhost:7777"  # Relay to forward all received events to (optional)

# Relay health management — a relay is ejected when a failure class hits its threshold.
relay_ejection_thresholds:
    transport: 10                    # connection/transport failures
    filter_rejection: 3              # filter-too-large rejections
    subscription_flap: 5             # subscribe refused (non-size)
ejected_relays: []                   # relays auto-removed from relay_urls (managed by the crawler)

# Early-batch exit: cancel a batch once this fraction of queried relays reach EOSE or error.
relay_eose_quorum: 0.70              # 0 disables early exit (full timeout always)

# Backoff for chronic-miss pubkeys (pubkeys that never return a kind-3 event).
miss_backoff:
    base: "2h"                       # first retry interval after a miss
    ratio: 2                         # geometric growth per consecutive miss
    cap: "168h"                      # max backoff (7 days)
    hit_refresh_cadence: "24h"       # re-query interval after a HIT

# clusterscan (spam-cluster detection) settings — see ./bin/clusterscan.
seed_pubkeys: []                     # trusted roots; trust flows outward along follows
trust_k: 2                           # endorsements from the trusted set needed to join it
cluster_depth: 3                     # follows-hops walked when measuring a cluster
max_bridge_weight: 2                 # "weak bridge" if 1..N edges cross into trusted
min_cluster_size: 5                  # ignore bridges whose cluster is smaller than this
```

## File Structure

```text
web-of-trust/
├── cmd/
│   ├── crawler/           # Main crawler application
│   │   ├── main.go        # Fetches follows from Nostr and stores in Dgraph
│   │   └── metrics.go     # Per-batch + per-run speed metrics (round comparison)
│   ├── clusterscan/       # Spam-cluster detection tool
│   │   └── main.go        # Trust propagation, weak-bridge detection, cluster sizing
│   ├── discover-relays/   # Relay discovery and benchmarking tool
│   │   └── main.go        # Discovers, tests, and ranks relays for config
│   ├── healthcheck/       # Database health check tool
│   │   └── main.go        # Detects invalid/duplicate pubkeys, optional purge
│   └── pubkeys/           # Pubkey export utility
│       └── main.go        # Exports popular pubkeys to CSV
├── pkg/
│   ├── config/            # Shared configuration loading
│   ├── crawler/           # Core crawling logic
│   ├── dgraph/            # Dgraph client and operations
│   └── version/           # Build metadata (injected via ldflags)
├── queries/
│   └── explore.dql        # Sample Dgraph queries for data exploration
├── .planning/             # GSD planning state (roadmap, milestones, spikes, codebase map)
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

<a id="crawler-metrics"></a>

#### Crawler Metrics (speed measurement & optimization rounds)

The crawler is instrumented to measure real production throughput so optimization
changes can be compared round-over-round (see `cmd/crawler/metrics.go`).

- **Per batch**, a structured `BATCH_METRICS:` JSON line is logged with the
  relay-fetch vs overhead split (`fetch_ms` / `overhead_ms`), `hit_rate`,
  `new_pubkeys`, and `pubkeys_per_sec`.
- **Per run** (on graceful shutdown, incl. Ctrl-C / SIGTERM), one comparable
  record is appended to `~/deepfry/crawler-metrics.jsonl` — tagged by a
  `round_id` and carrying a config snapshot (timeout, batch size, quorum, relay
  count) so each round's numbers are self-describing.

Tag a run for comparison (defaults to the build's git commit):

```bash
make build-crawler                       # ldflags inject the commit as default round_id
WOT_ROUND=baseline ./bin/crawler         # named experiment round

# Compare rounds:
jq -r '[.round_id,.pubkeys_per_sec,.new_pubkeys_per_sec,.avg_fetch_ms,.avg_batch_ms]|@tsv' \
  ~/deepfry/crawler-metrics.jsonl
```

Reading the split: `fetch_ms` ≫ `overhead_ms` → relay-bound (tune quorum/timeout);
large `overhead_ms` → DB/bookkeeping-bound (tune frontier batch size / per-batch
counts). See [Next Tasks & Future Improvements](#next-tasks--future-improvements).

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

### Cluster Scan (`cmd/clusterscan/`)

A spam-cluster detection tool that:

- Resolves trusted root pubkeys (`seed_pubkeys`) and propagates trust outward along follow edges
- Detects "weak bridges" — accounts with only 1..N follow edges crossing into the trusted set
- Sizes the cluster beneath each weak bridge to rank likely spam clusters
- Writes CSV/JSON reports (read-only analysis; no graph mutations)

**Usage**: `./bin/clusterscan [flags]` (tuned via the `seed_pubkeys`, `trust_k`, `cluster_depth`, `max_bridge_weight`, `min_cluster_size` config keys)

### Core Packages

- **`pkg/config/`**: Shared configuration loading via Viper (YAML, `~/deepfry/web-of-trust.yaml`)
- **`pkg/crawler/`**: Core crawling logic, multi-relay management, and Nostr client handling
- **`pkg/dgraph/`**: Dgraph client wrapper with graph operations for pubkey relationships
- **`pkg/version/`**: Build metadata (`Version`, `Commit`, `Built`) injected via Makefile ldflags

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

## Next Tasks & Future Improvements

This section is the resume point for a new engineer. Current state: milestone
**v1.4 (Crawler Hang Fix)** is complete and archived; the active effort is a
**measurement-driven crawl-speed optimization** kicked off by spike 001.

### In progress — crawl-speed optimization

Spike 001 added the production metrics instrumentation described under
[Crawler Metrics](#crawler-metrics). The instrumentation is built, unit-tested,
and committed; the next concrete step is to **capture a baseline round on the
strfry host** (live Dgraph + relays), then work the backlog below one variable
at a time, comparing `~/deepfry/crawler-metrics.jsonl` records between rounds.

Full context and protocol: `.planning/spikes/MANIFEST.md` and
`.planning/spikes/CONVENTIONS.md`.

**Optimization backlog** (each is one measured round — change one variable, run, compare):

1. **Decouple frontier batch from relay filter cap.** `GetStalePubkeys` fetches
   only `relay_filter_batch_size` (default 100) pubkeys per loop. Fetching N× more
   per loop while chunking relay filters independently amortizes per-loop Dgraph
   overhead. Watch `overhead_ms` and `pubkeys_per_sec`.
2. **Throttle per-batch counts.** `CountPubkeys` + `CountStalePubkeys` run every
   batch only to print a log line. Run them every N batches or asynchronously.
   Watch `overhead_ms`, `avg_countpubkeys_ms`, `avg_countstale_ms`.
3. **Pipeline / prefetch the next frontier** concurrently with the current relay
   fetch to hide DB latency. Watch `overhead_ms` vs `fetch_ms`.
4. **Sweep quorum & timeout** (`relay_eose_quorum`, `timeout`) to map batch
   latency (`avg_fetch_ms`) against completeness (`hit_rate`).

### Longer-term enhancements

- Trust score calculations on the follow graph
- Deeper integration with other DeepFry subsystems (whitelist plugin, search)
- Address items tracked in `.planning/codebase/CONCERNS.md`

### Where to look when resuming

| Topic | Location |
|-------|----------|
| Project state & current focus | `.planning/STATE.md` |
| Roadmap & milestones | `.planning/ROADMAP.md`, `.planning/MILESTONES.md` |
| Lessons from the last milestone | `.planning/RETROSPECTIVE.md` |
| Codebase map (architecture, concerns) | `.planning/codebase/` |
| Speed-optimization effort | `.planning/spikes/` |
| Build/test commands | `Makefile`, root `CLAUDE.md` |
