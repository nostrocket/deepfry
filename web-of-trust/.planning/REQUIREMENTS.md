# Requirements: Web-of-Trust Crawler

**Defined:** 2026-06-18
**Core Value:** The crawler must continuously expand the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

## v1.6 Requirements

### Loop Throughput

- [x] **LOOP-01**: Operator can configure a Dgraph frontier-selection batch size independently from the relay filter batch size.
- [x] **LOOP-02**: The crawler can query a larger frontier batch while each relay request remains chunked by the relay's learned filter cap.
- [x] **LOOP-03**: The main loop reports batch metrics using the actual number of pubkeys selected, queried, hit, skipped, and marked attempted after frontier decoupling.
- [x] **LOOP-04**: A larger frontier batch does not reintroduce the Phase 6 oversized-filter relay rejection failure mode.

### Count Overhead

- [x] **COUNT-01**: Operator can throttle `CountPubkeys` and `CountStalePubkeys` frequency without disabling crawl progress.
- [x] **COUNT-02**: Batch logs and final run records remain accurate when count queries are skipped between sampling intervals.
- [x] **COUNT-03**: Count-query failures remain recoverable through the existing Dgraph retry path when a sampled count is due.

### Measurement

- [x] **MEASURE-01**: Each optimization round can be compared against baseline using `BATCH_METRICS` and `~/deepfry/crawler-metrics.jsonl`.
- [x] **MEASURE-02**: Run records include the new frontier batch size and count-sampling settings so throughput results are self-describing.
- [x] **MEASURE-03**: The milestone defines an operator verification procedure for running baseline and optimized rounds on the strfry host.

### Dgraph Read Path

- [ ] **DSCALE-01**: `GetStalePubkeys` no longer recomputes `count(~follows)` over the entire frontier on each call — frontier ordering reads a stored, indexed `follower_count` predicate instead of an on-the-fly aggregate sort.
- [ ] **DSCALE-03**: The stored `follower_count` is maintained correctly as follow edges change (`AddFollowers`), so frontier ordering does not drift from the true follower count, with a one-time backfill for existing nodes.

### Testing

- [x] **TEST-01**: Unit tests cover config loading/defaults for frontier batch and count-sampling settings without touching `~/deepfry/`.
- [x] **TEST-02**: Unit tests cover loop accounting for larger selected batches, skipped attempts, and throttled count queries.
- [ ] **TEST-03**: Integration or operator-run verification covers a live Dgraph/relay round and records before-vs-after throughput evidence.

## Future Requirements

### Relay Tuning

- **TUNE-01**: Operator can run a measured quorum/timeout sweep and compare latency against hit-rate loss.
- **TUNE-02**: Operator can tune relay reconnect scheduling without scanning the full relay pool every batch.

### Dgraph Scaling

- **DSCALE-02**: Crawler can evaluate Dgraph write parallelism with a correctness-preserving transaction strategy.

### Dgraph Write Path (deferred — investigation closed)

- **DWRITE-01**: Investigation closed by Phase 13 + production metrics — `AddFollowers`/write-path (`MarkAttempted` ≈ 0.07s/batch) does NOT dominate per-batch overhead; the read-path frontier sort does. No write-path optimization is justified at this time.
- **DWRITE-02 / DWRITE-03**: Moot while no write-path optimization is undertaken; re-open only if a future milestone changes `AddFollowers`.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Changing StrFry or storing event payloads outside StrFry | Project protocol and data-separation rules require stock StrFry and ID-only Dgraph storage. |
| Blindly increasing relay filter size | Phase 6 proved oversized filters cause relay rejection/crashes; this milestone decouples DB selection from relay chunks instead. |
| Replacing Dgraph or sharding the graph | Too large for this optimization milestone; current focus is single-instance loop and write-path throughput. |
| Whitelist-plugin or quarantine behavior | Separate services; not in `web-of-trust` module scope. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| LOOP-01 | Phase 13 | Complete |
| LOOP-02 | Phase 13 | Complete |
| LOOP-03 | Phase 13 | Complete |
| LOOP-04 | Phase 13 | Complete |
| COUNT-01 | Phase 13 | Complete |
| COUNT-02 | Phase 13 | Complete |
| COUNT-03 | Phase 13 | Complete |
| MEASURE-01 | Phase 13 | Complete |
| MEASURE-02 | Phase 13 | Complete |
| MEASURE-03 | Phase 13 | Complete |
| DSCALE-01 | Phase 14 | Pending |
| DSCALE-03 | Phase 14 | Pending |
| TEST-01 | Phase 13 | Complete |
| TEST-02 | Phase 13 | Complete |
| TEST-03 | Phase 14 | Pending |

**Coverage:**

- v1.6 requirements: 16 total
- Mapped to phases: 16
- Unmapped: 0
- Phase 14 redefined 2026-06-20: read-path `follower_count` (DSCALE-01/03) replaces the write-path decision (DWRITE-01/02/03); see PROJECT.md Key Decisions.

---
*Requirements defined: 2026-06-18*
*Last updated: 2026-06-20 — Phase 14 redefined from write-path decision to read-path `follower_count` fix.*
