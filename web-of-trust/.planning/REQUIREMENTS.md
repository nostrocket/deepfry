# Requirements: Web-of-Trust Crawler — v1.2 Crawler Reliability & Efficiency

**Defined:** 2026-06-10
**Core Value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

## v1.2 Requirements

Operational reliability and throughput improvements motivated by a 40-batch production run (20,000 pubkeys queried, 172 relays, 0.76% event hit rate).

### Pubkey Validation

- [ ] **VALID-01**: `updateFollowsFromEvent` validates each p-tag pubkey against `^[0-9a-f]{64}$` before writing to Dgraph — `nostr.GetPublicKey` (a private-key→public-key derivation function) is no longer used as a validator. Malformed pubkeys (uppercase hex, binary blobs, truncated values) are logged and skipped.
- [ ] **VALID-02**: Existing garbage pubkeys in Dgraph (those whose `pubkey` field does not match `^[0-9a-f]{64}$`) are purged — either via a startup migration in `EnsureSchema` or by running `healthcheck -purge`. After the purge, the stale frontier no longer contains invalid-pubkey nodes.
- [ ] **VALID-03**: When `MarkAttempted` encounters a pubkey that fails the hex validator, it writes `last_attempt = now` via a UID-based mutation (bypassing the pubkey uniqueness key) so the node ages out of the stale frontier and is not re-queued on every batch.

### Filter Size

- [ ] **FILTER-01**: The default `batchSize` for relay REQ filters is reduced from 500 to 100 authors, staying within the default `maxFilterAuthors` of StrFry and the SQLite variable limit of Cloudflare D1 relays.
- [ ] **FILTER-02**: `queryRelay` intercepts NOTICE messages from the relay's subscription and detects "filter item too large" (or equivalent); a relay that sends this NOTICE has its per-relay filter cap recorded and future REQs to that relay use chunked sub-queries at the detected cap. Relays that respond to an oversized REQ by closing the connection (connection-drop-on-REQ pattern) are also classified as having a small filter cap.

### Frontier Prioritization

- [ ] **PERF-01**: `GetStalePubkeys` orders the stale frontier by incoming follower count (`count(~follows)`) descending so pubkeys with many followers are queried first; these are more likely to have kind-3 events on major relays, improving event hit rate.
- [ ] **PERF-02**: Pubkeys that return no kind-3 event after N consecutive crawl attempts have their `last_attempt` advanced by an exponentially increasing interval (e.g. 1h → 4h → 16h → …, capped at a configurable max). They are never permanently abandoned — the interval eventually expires — but they stop consuming batch capacity on every cycle.

### Relay Health Management

- [ ] **RELAY-01**: The relay failure counter is no longer reset to zero on successful reconnect. On reconnect it is decayed (e.g. halved) so a relay that repeatedly connects-and-drops can still accumulate past `maxConsecutiveFailures` and be removed from the config.
- [ ] **RELAY-02**: Relay failure reasons are classified into at least three buckets — transport error (connection lost), filter rejection (NOTICE or connection-drop on REQ), and subscription flap (connects but drops immediately on first subscription). Per-class ejection thresholds are configurable. A relay exceeding its threshold for a class is automatically removed from the config file and logged with the reason class.

### Timeout & Observability

- [ ] **TIMEOUT-01**: The per-batch relay query timeout is reduced from 30s to 15s. Relays that do not send EOSE within 15s are cancelled.
- [ ] **TIMEOUT-02**: The batch relay context is cancelled early once ≥70% of alive relays have either sent EOSE or returned an error — without waiting for the full timeout. This reduces average batch time when fast relays have already covered the data.
- [ ] **METRIC-01**: The `staleRemaining` value in the crawler's progress log reflects the actual count of stale pubkeys remaining in Dgraph after the batch (a separate `CountStalePubkeys` query before the batch, minus `len(pubkeys)`) — not zero due to the current off-by-one in `cmd/crawler/main.go`.

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
| VALID-01 | TBD | Pending |
| VALID-02 | TBD | Pending |
| VALID-03 | TBD | Pending |
| FILTER-01 | TBD | Pending |
| FILTER-02 | TBD | Pending |
| PERF-01 | TBD | Pending |
| PERF-02 | TBD | Pending |
| RELAY-01 | TBD | Pending |
| RELAY-02 | TBD | Pending |
| TIMEOUT-01 | TBD | Pending |
| TIMEOUT-02 | TBD | Pending |
| METRIC-01 | TBD | Pending |

**Coverage:**

- v1.2 requirements: 12 total
- Mapped to phases: 0 (roadmap pending)
- Unmapped: 12

---
*Requirements defined: 2026-06-10*
