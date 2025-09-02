# Title: Nostr Syncer

## Overview

- Problem: DeepFry aims to be a mega relay for Nostr. This subsystem fetches and forwards all events from a single Nostr relay for a defined sync-window, ensuring progress is stored and resumable.
- Goals and Success Metrics:
  - Fetch all events from a Nostr relay for a sync-window.
  - Forward events to DeepFry relay in near real-time.
  - Store sync progress as a Nostr event on DeepFry relay after publishing each batch (fire-and-forget; no OK required).
  - Success: 100% event coverage per sync-window, <1s forwarding latency, resumable after failure.
- Target Users and Personas:
  - Backend operators and automated systems managing DeepFry relay ingestion.
- Scope and Non-Goals:
  - In scope: Subscribe to Nostr relay, fetch events, forward to DeepFry relay, store sync-window progress.
  - Out of scope: Multi-relay support, event deduplication, ordering guarantees, user-facing UI.
- Assumptions:
  - Nostr relay is available via WebSocket, no authentication required.
  - DeepFry relay (StrFry) accepts Nostr events over WebSocket (NIP-01). No application-level OK is required before updating sync progress (fire-and-forget).
- Dependencies:
  - Nostr relay (WebSocket endpoint)
  - DeepFry relay (unmodified StrFry; client uses standard Nostr WebSocket to publish/subscribe; "stream" refers to standard Nostr over WS)
- Constraints:
  - No regulatory or compliance constraints.
  - Protocol conformance: NIP-01 (basic protocol), NIP-33 (parameterized replaceable events) for sync-window event.
  - Event kind: Use kind 30078 for the sync-window parameterized replaceable event.
  - Operational: Run exactly one process per source relay (per d tag). Multiple processes may run concurrently if each targets a different source relay.
- Risks:
  - Relay downtime; solution must be resumable.
- Glossary:
  - Sync-window: Time-bounded period for event fetching.
  - DeepFry relay: Target relay for event forwarding.
  - Fire-and-forget: Send without waiting for application-level acknowledgment; only rely on network send completion.
  - d tag: Parameter for Nostr parameterized replaceable events (NIP-33) used as an identifier.

## Development

- `make fmt` - Format code
- `make vet` - Vet code
- `make test` - Run tests
- `make tidy` - Update dependencies
- `make clean` - Clean build artifacts

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
