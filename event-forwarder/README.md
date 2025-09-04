# Event Forwarder

A Nostr event forwarding service that synchronizes events between relays with real-time monitoring and resumable progress tracking.

## Overview

**Problem**: DeepFry aims to be a mega relay for Nostr. This subsystem fetches and forwards all events from a single Nostr relay for a defined sync-window, ensuring progress is stored and resumable.

**Goals**:

- Fetch all events from a Nostr relay for a sync-window
- Forward events to DeepFry relay in near real-time (<1s latency)
- Store sync progress as Nostr events on DeepFry relay (fire-and-forget)
- 100% event coverage per sync-window with resumable operation after failures

**Key Features**:

- 🔄 Real-time event synchronization between Nostr relays
- 📊 Live TUI dashboard with event statistics and connection status
- 🔒 Cryptographic signing of sync progress events
- ⚡ Auto-reconnection with exponential backoff and jitter
- 📈 Built-in telemetry and observability

## Quick Start

### Prerequisites

- Go 1.21 or later
- A Nostr secret key (nsec format or 32-byte hex)
- Source relay URL (WebSocket endpoint)
- DeepFry relay URL (WebSocket endpoint)

### Installation

```bash
# Clone the repository
git clone <repository-url>
cd event-forwarder

# Build the application
make build

# Or run directly
go run cmd/fwd/main.go --help
```

## Usage

### Basic Usage

```bash
# Run with command line flags
./bin/fwd --source wss://relay.damus.io \
          --deepfry wss://your-deepfry-relay.com \
          --secret-key nsec1abc123...

# Or use environment variables
export SOURCE_RELAY_URL="wss://relay.damus.io"
export DEEPFRY_RELAY_URL="wss://your-deepfry-relay.com"
export NOSTR_SYNC_SECKEY="nsec1abc123..."
./bin/fwd
```

### TUI Interface

The application provides a real-time terminal user interface showing:

![TUI Overview](../docs/images/fwd/tui-overview.png)
*Main TUI interface showing event statistics and relay status*

![TUI Event Flow](../docs/images/fwd/tui-event-flow.png)
*Event flow monitoring with detailed metrics*

#### TUI Controls

- **Ctrl+C** or **Esc**: Exit the application
- **Auto-refresh**: Updates every 1000ms with live statistics

#### TUI Sections

- **Status**: Connection state and error messages
- **Relays**: Source and DeepFry relay connection status
- **Event Stats**: Real-time event counts, rates, and processing metrics
- **Event Kinds**: Breakdown of event types being processed
- **Current Window**: Active sync window progress and timeline

## Configuration

### Command Line Flags

| Flag | Description | Required | Default |
|------|-------------|----------|---------|
| `--source` | Source relay URL (WebSocket) | ✅ | - |
| `--deepfry` | DeepFry relay URL (WebSocket) | ✅ | - |
| `--secret-key` | Nostr secret key (nsec or hex) | ✅ | - |
| `--sync-window-seconds` | Sync window duration | ❌ | 5 |
| `--sync-max-batch` | Max events per batch | ❌ | 1000 |
| `--sync-max-catchup-lag-seconds` | Max catchup lag tolerance | ❌ | 10 |
| `--sync-start-time` | Start time (RFC3339 format) | ❌ | (recent) |
| `--network-initial-backoff-seconds` | Initial reconnect delay | ❌ | 1 |
| `--network-max-backoff-seconds` | Max reconnect delay | ❌ | 30 |
| `--network-backoff-jitter` | Backoff randomization | ❌ | 0.2 |
| `--timeout-publish-seconds` | Publish timeout | ❌ | 10 |
| `--timeout-subscribe-seconds` | Subscribe timeout | ❌ | 10 |
| `--help` | Show help message | ❌ | - |

### Environment Variables

All command line flags can be set via environment variables. CLI flags take precedence.

| Environment Variable | CLI Flag Equivalent | Description |
|---------------------|-------------------|-------------|
| `SOURCE_RELAY_URL` | `--source` | Source relay WebSocket URL |
| `DEEPFRY_RELAY_URL` | `--deepfry` | DeepFry relay WebSocket URL |
| `NOSTR_SYNC_SECKEY` | `--secret-key` | Nostr secret key |
| `SYNC_WINDOW_SECONDS` | `--sync-window-seconds` | Sync window duration in seconds |
| `SYNC_MAX_BATCH` | `--sync-max-batch` | Maximum events per batch |
| `SYNC_MAX_CATCHUP_LAG_SECONDS` | `--sync-max-catchup-lag-seconds` | Max acceptable lag in seconds |
| `SYNC_START_TIME` | `--sync-start-time` | Sync start time (RFC3339) |
| `NETWORK_INITIAL_BACKOFF_SECONDS` | `--network-initial-backoff-seconds` | Initial backoff delay |
| `NETWORK_MAX_BACKOFF_SECONDS` | `--network-max-backoff-seconds` | Maximum backoff delay |
| `NETWORK_BACKOFF_JITTER` | `--network-backoff-jitter` | Backoff jitter factor |
| `TIMEOUT_PUBLISH_SECONDS` | `--timeout-publish-seconds` | Event publish timeout |
| `TIMEOUT_SUBSCRIBE_SECONDS` | `--timeout-subscribe-seconds` | Relay subscribe timeout |

### Configuration Examples

#### Sync from a specific time

```bash
./bin/fwd --source wss://relay.damus.io \
          --deepfry wss://your-relay.com \
          --secret-key nsec1abc123... \
          --sync-start-time "2024-01-01T00:00:00Z"
```

#### Extended sync window for historical data

```bash
./bin/fwd --source wss://relay.damus.io \
          --deepfry wss://your-relay.com \
          --secret-key nsec1abc123... \
          --sync-window-seconds 3600
```

#### Production environment with custom timeouts

```bash
export SOURCE_RELAY_URL="wss://production-source.com"
export DEEPFRY_RELAY_URL="wss://production-deepfry.com"
export NOSTR_SYNC_SECKEY="nsec1productionkey..."
export TIMEOUT_PUBLISH_SECONDS="30"
export TIMEOUT_SUBSCRIBE_SECONDS="30"
export NETWORK_MAX_BACKOFF_SECONDS="120"

./bin/fwd
```

## Development

### Development Prerequisites

- Go 1.21+
- Make

### Build Commands

```bash
# Format code
make fmt

# Lint and vet
make vet

# Run tests
make test

# Run integration tests
make test-integration

# Build binary
make build

# Clean build artifacts  
make clean

# Update dependencies
make tidy

# Install development tools
make install-tools
```

### Project Structure

```text
├── cmd/fwd/           # Main application entry point
├── pkg/
│   ├── config/        # Configuration management
│   ├── crypto/        # Cryptographic utilities
│   ├── forwarder/     # Core forwarding logic
│   ├── nsync/         # Sync window management
│   ├── telemetry/     # Metrics and observability
│   └── utils/         # Shared utilities
├── docs/              # Documentation
└── bin/               # Built binaries
```

### Testing

```bash
# Unit tests
go test ./... -short

# Integration tests (requires test relays)
go test ./... -tags=integration

# Test coverage
go test ./... -cover

# Specific package tests
go test ./pkg/forwarder -v
```

## Troubleshooting

### Common Issues

#### TUI Stats Not Updating

If the event statistics remain at zero:

1. **Check Connection Status**: Look for red "ERROR" messages in the TUI status section
2. **Verify Relay URLs**: Ensure both source and DeepFry relay URLs are valid WebSocket endpoints
3. **Check Sync Window**: Default window is 5 seconds. If testing with inactive relays, use a longer window:

   ```bash
   ./bin/fwd --sync-window-seconds 3600  # 1 hour window
   ```

4. **Use Active Relays**: Test with public relays that have recent activity:

   ```bash
   ./bin/fwd --source wss://relay.damus.io --deepfry wss://your-relay.com --secret-key nsec1...
   ```

#### Connection Errors

- **"failed to connect to source relay"**: Source relay URL is invalid or unreachable
- **"failed to connect to deepfry relay"**: DeepFry relay URL is invalid or unreachable
- **"websocket: bad handshake"**: Relay doesn't support WebSocket connections

#### Configuration Errors

- **"flag provided but not defined"**: Using old flag names (check command line help)
- **"failed to derive keypair"**: Invalid secret key format (use nsec1... or 32-byte hex)
- **"validation error"**: Missing required configuration (source, deepfry, secret-key)

### Debug Mode

For detailed debugging, check the application's telemetry events:

```bash
# Run with verbose logging (implementation pending)
./bin/fwd --log-level debug
```

### Performance Tuning

For high-throughput scenarios:

```bash
# Increase batch size and timeouts
./bin/fwd --sync-max-batch 5000 \
          --timeout-publish-seconds 30 \
          --timeout-subscribe-seconds 30
```

## Technical Specifications

### Architecture

The application follows an event-driven architecture with the following components:

- **Forwarder**: Core sync logic that connects to relays and manages event flow
- **Telemetry**: Real-time metrics collection and aggregation
- **TUI**: Terminal user interface with live dashboard
- **Config**: Centralized configuration management with CLI/env variable support
- **Crypto**: Nostr key management and event signing

### Event Flow

```text
Source Relay → WebSocket → Forwarder → Telemetry → TUI Display
                    ↓
              DeepFry Relay ← Sync Progress Event ← Event Batch
```

### Sync Progress Events

Progress is tracked using Nostr events (NIP-33, kind 30078) with tags:

- `d`: Source relay URL (identifier)
- `from`: Window start time (Unix timestamp)
- `to`: Window end time (Unix timestamp)

### Protocol Compliance

- **NIP-01**: Basic Nostr protocol for WebSocket communication
- **NIP-33**: Parameterized replaceable events for sync progress tracking

## License

MIT

## Contributing

Check the main repo for contribution guidelines.

## Functional Requirements (Traceable)

| ID      | Title               | Description                                                                                                                                                                                                                                                          | MoSCoW | Rationale                                    | Depends On |
| ------- | ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------ | -------------------------------------------- | ---------- |
| REQ-001 | Subscribe to Nostr  | Connect to Nostr relay via WebSocket and subscribe for events in sync-window                                                                                                                                                                                         | Must   | Core data source                             |            |
| REQ-002 | Forward to DeepFry  | Forward all fetched events to DeepFry relay                                                                                                                                                                                                                          | Must   | Enables mega relay ingestion                 | REQ-001    |
| REQ-003 | Store sync progress | After forwarding a batch of events to DeepFry relay, publish a Nostr event (kind 30078, NIP-33) to DeepFry to update sync-window. Tags required: (1) d = exact source relay URL, (2) from = UTC Unix seconds (window start), (3) to = UTC Unix seconds (window end). | Must   | Enables resumability; fire-and-forget policy | REQ-002    |
| REQ-004 | Resume on failure   | Resume from last stored sync-window event                                                                                                                                                                                                                            | Must   | Ensures reliability                          | REQ-003    |
| REQ-007 | Reconnect/backoff   | Auto-reconnect to both relays with exponential backoff and jitter; resume from last stored sync-window upon reconnect.                                                                                                                                               | Must   | Availability and stability                   | REQ-004    |
| REQ-005 | Sign sync events    | Maintain a Nostr keypair and sign the sync-window events; read secret key from environment variable (NOSTR_SYNC_SECKEY, nsec or hex), with configurable pubkey.                                                                                                      | Must   | Required for valid Nostr events              | REQ-003    |
| REQ-006 | Config management   | Expose configuration for: source relay URL, DeepFry relay URL, sync-window duration, batch size, acceptable catch-up lag, reconnect/backoff, timeouts. Provide sensible defaults documented below.                                                                   | Must   | Operability and tunability                   |            |

## Non-Functional Requirements (Traceable)

| ID      | Attribute     | Target                       | Measure/Method                      | MoSCoW | Notes                             |
| ------- | ------------- | ---------------------------- | ----------------------------------- | ------ | --------------------------------- |
| NFR-001 | Latency       | <1 s per event end-to-end    | Log span between ingest and forward | Must   | Near real-time                    |
| NFR-002 | Throughput    | ≥1k events/s sustained       | Load test + metrics                 | Should | Burst tolerant to 5k events/s     |
| NFR-003 | Availability  | 99.9% monthly                | SLO/Error Budget                    | Must   | Auto-reconnect with backoff       |
| NFR-004 | Observability | Metrics + structured logs    | Counters, histograms, traces        | Must   | events_received, forwarded, lag_s |
| NFR-005 | Security      | Secrets safe; no key in logs | Secret store/env + scans            | Must   | Key via env var NOSTR_SYNC_SECKEY |
| NFR-006 | Privacy       | No sensitive data in logs    | Log review                          | Should | Redact keys/headers               |
| NFR-007 | Time Sync     | Clock drift ≤ ±1 s           | NTP/OS time                         | Should | Accurate from/to timestamps       |
| NFR-008 | Cost          | Single small instance/core   | Resource usage                      | Should | CPU < 1 core avg, RAM < 512 MiB   |

## Acceptance Criteria (Traceable)

| ID     | Verifies | Given/When/Then                                                                                                                             | Evidence                         |
| ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| AC-001 | REQ-001  | Given a running Nostr relay, when the subsystem starts, then it connects and subscribes for events in the sync-window                       | Integration test                 |
| AC-002 | REQ-002  | When events are received, then they are forwarded to DeepFry relay within 1 s                                                               | Log timestamps                   |
| AC-003 | REQ-003  | Given a completed publish of a batch to DeepFry (send success), then a sync-window event with tags d, from, to exists on DeepFry            | Query DeepFry for latest event   |
| AC-008 | REQ-003  | The sync-window event uses kind 30078 and tags: d equals exact source relay URL; from/to are UTC Unix seconds matching the processed window | Event inspection via query       |
| AC-004 | REQ-004  | When subsystem restarts, then it resumes from last stored sync-window                                                                       | Integration test                 |
| AC-005 | REQ-005  | When publishing a sync-window, then the event is valid and signed by the configured key                                                     | Signature verified by clients    |
| AC-009 | REQ-005  | Given NOSTR_SYNC_SECKEY is set, when the process starts, then the derived pubkey matches expected and is used for signing                   | Pubkey derivation check + logs   |
| AC-006 | REQ-006  | Given configuration values, when process starts, then they are applied and observable via logs/metrics                                      | Config dump (redacted) + metrics |
| AC-007 | REQ-007  | Given relay outage, when connection drops, then the client reconnects with backoff and resumes from last sync-window                        | Fault-injection test + logs      |

## Phasing Plan

### MVP

- Requirements: REQ-001, REQ-002, REQ-003, REQ-004
- NFR minima: NFR-001, NFR-002

### MLP

- Not applicable; all required for initial release.

## Open Questions

None for v1.

## Test Plan Outline

- Integration tests for Nostr relay subscription and event forwarding
- Log-based latency measurement
- Failure/restart scenario for resumability
- Signature validity test for sync-window events
- Backoff/reconnect behavior under relay downtime
- Env var key read and pubkey derivation validation

## Next Steps

- Implement integration tests
- Review and iterate requirements as needed

## Configuration Defaults

- sync.windowSeconds: 5
- sync.maxBatch: 1000
- sync.maxCatchupLagSeconds: 10
- network.initialBackoffSeconds: 1
- network.maxBackoffSeconds: 30
- network.backoffJitter: 0.2
- timeouts.publishSeconds: 10
- timeouts.subscribeSeconds: 10
- secrets.envVar: NOSTR_SYNC_SECKEY (nsec-encoded or 32-byte hex)
- keys.pubkey: derived from secret by default (override via config if provided)
