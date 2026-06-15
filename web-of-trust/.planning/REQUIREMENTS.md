# Requirements: Web-of-Trust Crawler — v1.2 Crawler Reliability & Efficiency

**Defined:** 2026-06-10
**Core Value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

## v1.2 Requirements

Operational reliability and throughput improvements motivated by a 40-batch production run (20,000 pubkeys queried, 172 relays, 0.76% event hit rate).

### Pubkey Validation

- [x] **VALID-01**: `updateFollowsFromEvent` validates each p-tag pubkey against `^[0-9a-f]{64}$` before writing to Dgraph — `nostr.GetPublicKey` (a private-key→public-key derivation function) is no longer used as a validator. Malformed pubkeys (uppercase hex, binary blobs, truncated values) are logged and skipped.
- [x] **VALID-02**: Existing garbage pubkeys in Dgraph (those whose `pubkey` field does not match `^[0-9a-f]{64}$`) are purged — either via a startup migration in `EnsureSchema` or by running `healthcheck -purge`. After the purge, the stale frontier no longer contains invalid-pubkey nodes.
- [x] **VALID-03**: When `MarkAttempted` encounters a pubkey that fails the hex validator, it writes `last_attempt = now` via a UID-based mutation (bypassing the pubkey uniqueness key) so the node ages out of the stale frontier and is not re-queued on every batch.

### Filter Size

- [x] **FILTER-01**: The default `batchSize` for relay REQ filters is reduced from 500 to 100 authors, staying within the default `maxFilterAuthors` of StrFry and the SQLite variable limit of Cloudflare D1 relays.
- [x] **FILTER-02**: `queryRelay` intercepts NOTICE messages from the relay's subscription and detects "filter item too large" (or equivalent); a relay that sends this NOTICE has its per-relay filter cap recorded and future REQs to that relay use chunked sub-queries at the detected cap. Relays that respond to an oversized REQ by closing the connection (connection-drop-on-REQ pattern) are also classified as having a small filter cap.

### Frontier Prioritization

- [x] **PERF-01**: `GetStalePubkeys` orders the stale frontier by incoming follower count (`count(~follows)`) descending so pubkeys with many followers are queried first; these are more likely to have kind-3 events on major relays, improving event hit rate.
- [x] **PERF-02**: Pubkeys that return no kind-3 event after N consecutive crawl attempts have their `last_attempt` advanced by an exponentially increasing interval (e.g. 1h → 4h → 16h → …, capped at a configurable max). They are never permanently abandoned — the interval eventually expires — but they stop consuming batch capacity on every cycle.

### Relay Health Management

- [x] **RELAY-01**: The relay failure counter is no longer reset to zero on successful reconnect. On reconnect it is decayed (e.g. halved) so a relay that repeatedly connects-and-drops can still accumulate past `maxConsecutiveFailures` and be removed from the config.
- [x] **RELAY-02**: Relay failure reasons are classified into at least three buckets — transport error (connection lost), filter rejection (NOTICE or connection-drop on REQ), and subscription flap (connects but drops immediately on first subscription). Per-class ejection thresholds are configurable. A relay exceeding its threshold for a class is automatically removed from the config file and logged with the reason class.
- [x] **RELAY-03**: Learned per-relay filter caps persist across reconnects instead of being reset (reverses the Phase 6 reset-on-reconnect decision). A recovery mechanism (e.g. periodic probe-up or slow decay) lets a relay that raised its limit regain a larger cap, but the 50→25→12→10 halving cascade is not re-run — and floor-capped relays are not re-marked dead — on every batch.

### Logging Noise

Production logs are dominated by per-relay, per-event lines: ~100 `Reconnected to relay` lines per sweep, 6-line filter-cap halving cascades per relay per batch, and duplicate `WARN: Connection timed out` + `marked dead` pairs (with misleading "timed out" wording for filter-cap failures).

- [x] **LOG-01**: A reconnect sweep emits a single summary line (e.g. `Reconnected 96/103 relays, 1 removed, 6 still dead`) instead of one line per relay; per-relay reconnect detail is only emitted when the debug flag is enabled.
- [x] **LOG-02**: Filter-cap negotiation logs at most one line per relay per batch stating the final outcome (`cap learned at N` or `floor reached`) — individual halving steps are not logged (or are debug-only).
- [x] **LOG-03**: A relay entering the dead state produces exactly one log line carrying the failure class, failure count, and next retry time — the duplicate `WARN: Connection timed out` + `marked dead` pair is collapsed, and filter-cap failures are no longer described as timeouts.

### Timeout & Observability

- [x] **TIMEOUT-01**: The per-batch relay query timeout is reduced from 30s to 15s. Relays that do not send EOSE within 15s are cancelled.
- [x] **TIMEOUT-02**: The batch relay context is cancelled early once ≥70% of alive relays have either sent EOSE or returned an error — without waiting for the full timeout. This reduces average batch time when fast relays have already covered the data.
- [x] **METRIC-01**: The `staleRemaining` value in the crawler's progress log reflects the actual count of stale pubkeys remaining in Dgraph after the batch (a separate `CountStalePubkeys` query before the batch, minus `len(pubkeys)`) — not zero due to the current off-by-one in `cmd/crawler/main.go`.

### Hardening & Resilience (Phase 8 follow-ups)

Deferred Phase 8 code-review warnings (`08-REVIEW.md` WR-02/03/04/05) plus a transient-Dgraph-error retry surfaced during the 08-02 live-host verification run. These are latent failure modes the live run happened to dodge.

- [x] **HARD-01**: `BackfillNextAttempt` paginates its `has(last_attempt) ∧ ¬has(next_attempt)` query (`first:`/`offset:`) and commits stamps in `batchSize` windows so a large legacy frontier cannot exceed the gRPC message cap and silently skip backfill (defeating D-06). (WR-03)
- [x] **HARD-02**: `MarkAttempted`'s in-place recovery transaction is discarded on every exit path without accumulating undiscarded txns across the recover/purge/stamp sequence; stamp-vs-recovery independence and retry-safety are documented. VALID-03 behavior preserved verbatim. (WR-02)
- [x] **HARD-03**: `forwardEvent` publishes within a short bounded context (e.g. `c.timeout`) so a hung forward relay cannot stall the single-threaded drain loop or delay `MarkAttempted` / the next batch. (WR-04)
- [x] **HARD-04**: The >1000-row frontier sort-cap regime is covered by an integration test (frontier larger than the order-by sort cap, proving top-N is honored not pre-truncated), or the live-verified D-09 guarantee is documented as standing evidence. (WR-05)
- [x] **RESIL-01**: The main crawl loop classifies transient Dgraph gRPC errors (`codes.Unavailable`, EOF, deadline-exceeded) and retries with backoff instead of exiting; genuinely fatal errors still terminate loudly. (08-02 live-host finding)

## v1.1 Requirements (Complete)

All shipped in Phase 3.

- ✓ **CHUNK-01**: Full follow-list persistence for >chunk-size pubkeys — per-chunk `kind3CreatedAt` version guard no longer discards chunks 2…N.
- ✓ **CHUNK-02**: Genuine-duplicate dedup preserved — re-crawl at same/older `kind3CreatedAt` still short-circuits.
- ✓ **LEAK-01**: `defer cancel()` accumulation in `processFollowsInChunks` eliminated.
- ✓ **TEST-03**: Integration regression test for chunk data-drop.
- ✓ **TEST-04**: Unit tests for chunk-splitting boundary logic.

SEC-01/02 (RemoveFollower injection hardening) from v1.1 Phase 4 — deferred indefinitely; documented as a future idea. No active phase.

## Future Requirements

### Relay Discovery

- **DISC-01**: Run `discover-relays` periodically to replace ejected relays with stable alternatives.
- **DISC-02**: Bloom filter per relay for "seen authors" — skip querying relay X for pubkey Y if that relay has never returned an event for any similar pubkey.

### Injection Hardening

- **SEC-01**: `RemoveFollower` uses parameterised `$`-Vars / `%q`-quoted nquads instead of raw string concatenation.
- **SEC-02**: `RemoveFollower` validates 64-char hex before mutating. (Latent dead code — no callers. Low urgency.)

### Observability

- **OBS-01**: Per-relay metrics (events returned, EOSE latency, failure class counts) exposed via a lightweight HTTP endpoint or periodic log summary.

### Tuning

- **TUNE-01**: Raise `stale_pubkey_threshold` toward the `86400` code default to spend more budget expanding the graph vs re-refreshing known accounts.

### Coverage

- **TEST-05**: Broaden unit/integration suite beyond the write path (relay state machine, config load, clusterscan).

## Out of Scope

| Feature | Reason |
|---------|--------|
| Manual relay blacklisting by name | Automatic ejection (RELAY-01/02) replaces this |
| Modifying StrFry | Protocol rule: StrFry stays unmodified |
| Editing `~/deepfry/web-of-trust.yaml` for testing | Live config must not change; use a temp `HOME` |
| Storing event payloads in Dgraph | Data-separation rule: payloads live in StrFry LMDB |
| Whitelist-plugin / quarantine / cache fixes | Separate concerns; own future milestones |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| VALID-01 | Phase 5 | Complete |
| VALID-02 | Phase 5 | Complete |
| VALID-03 | Phase 5 | Complete |
| FILTER-01 | Phase 6 | Complete |
| FILTER-02 | Phase 6 | Complete |
| PERF-01 | Phase 8 | Complete |
| PERF-02 | Phase 8 | Complete |
| RELAY-01 | Phase 7 | Complete |
| RELAY-02 | Phase 7 | Complete |
| RELAY-03 | Phase 7 | Complete |
| LOG-01 | Phase 7 | Complete |
| LOG-02 | Phase 7 | Complete |
| LOG-03 | Phase 7 | Complete |
| TIMEOUT-01 | Phase 8 | Complete |
| TIMEOUT-02 | Phase 8 | Complete |
| METRIC-01 | Phase 8 | Complete |
| HARD-01 | Phase 9 | Complete |
| HARD-02 | Phase 9 | Complete |
| HARD-03 | Phase 9 | Complete |
| HARD-04 | Phase 9 | Complete |
| RESIL-01 | Phase 9 | Complete |

**Coverage:**

- v1.2 requirements: 21 total (16 original + 5 Phase-9 hardening/resilience)
- Mapped to phases: 21
- Unmapped: 0

---
*Requirements defined: 2026-06-10*
