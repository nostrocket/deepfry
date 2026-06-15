# Requirements: Web-of-Trust Crawler — v1.3 Unbounded Dgraph Retry Resilience

**Defined:** 2026-06-15
**Core Value:** The crawler must continuously expand the web of trust — and to do that it must keep running across Dgraph outages instead of dying on the first transient gRPC error.

## v1.3 Requirements

Refines v1.2's RESIL-01 (Phase 9), which retries transient Dgraph errors but gives up after 5 attempts (~2.5 min) and exits the crawl loop. The observed failure is `count stale pubkeys failed: rpc error: code = Unavailable desc = error reading from server: EOF`.

### Resilience

- [x] **RETRY-01**: When a main-loop Dgraph call returns a transient gRPC error (`codes.Unavailable`, `codes.DeadlineExceeded`, `codes.ResourceExhausted`), the crawler retries indefinitely instead of exiting after a fixed attempt cap.
- [x] **RETRY-02**: The indefinite-retry behavior is applied uniformly to all four main-loop Dgraph calls: `GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, and `MarkAttempted`.
- [x] **RETRY-03**: Non-transient (fatal) Dgraph errors still terminate the read-path loop loudly with a logged error — behavior unchanged from v1.2.

### Backoff

- [x] **BACKOFF-01**: The first retry waits 1 minute after the transient error is encountered (was 5 s).
- [x] **BACKOFF-02**: Backoff grows exponentially (doubling) and is capped at 5 minutes (was capped at 2 min); the cap is sustained for all subsequent retries during a prolonged outage.

### Shutdown

- [x] **SHUTDOWN-01**: Context cancellation (SIGINT/SIGTERM) interrupts an in-progress retry wait immediately and shuts the crawler down cleanly, even mid-backoff.

### Observability

- [x] **OBS-01**: Each main-loop Dgraph call's execution time is measured, and the crawler periodically logs the average call duration (per call type: `GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, `MarkAttempted`) to the console.

### Testing

- [x] **TEST-01**: Unit tests cover the retry/backoff helper — indefinite retry on transient codes, immediate stop on a context-cancel, the 1m→2m→4m→5m(cap) backoff sequence, and that fatal codes are not retried.

## Future Requirements

Deferred to a future milestone. Tracked but not in the current roadmap.

### Configuration

- **TUNE-01**: Make the Dgraph retry backoff (initial / cap) config-driven via `web-of-trust.yaml` (mirroring the v1.2 `MissBackoff` pattern), instead of hardcoded constants.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Config-driven retry backoff (TUNE-01) | v1.3 ships fixed values (1m start, 5m cap) per the operator request; config tunability is a separate enhancement |
| Reconnecting/recreating the Dgraph gRPC client on `Unavailable` | The existing client recovers on retry; client re-creation is a heavier change not required to stop the crawler exiting |
| Retry for relay (non-Dgraph) failures | Relay health/auto-ejection already handled by v1.2 RELAY-01/02/03 |
| Any change to StrFry | Protocol rule: StrFry stays unmodified |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| RETRY-01 | Phase 10 | Complete |
| RETRY-02 | Phase 10 | Complete |
| RETRY-03 | Phase 10 | Complete |
| BACKOFF-01 | Phase 10 | Complete |
| BACKOFF-02 | Phase 10 | Complete |
| SHUTDOWN-01 | Phase 10 | Complete |
| OBS-01 | Phase 10 | Complete |
| TEST-01 | Phase 10 | Complete |

**Coverage:**

- v1.3 requirements: 8 total
- Mapped to phases: 8 (Phase 10)
- Unmapped: 0 ✓

---
*Requirements defined: 2026-06-15*
*Last updated: 2026-06-15 — traceability filled by roadmap creation*
