# Requirements: Web-of-Trust Crawler

**Defined:** 2026-06-18
**Core Value:** The crawler must continuously expand the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

## v1.6 Requirements

### Loop Throughput

- [ ] **LOOP-01**: Operator can configure a Dgraph frontier-selection batch size independently from the relay filter batch size.
- [ ] **LOOP-02**: The crawler can query a larger frontier batch while each relay request remains chunked by the relay's learned filter cap.
- [ ] **LOOP-03**: The main loop reports batch metrics using the actual number of pubkeys selected, queried, hit, skipped, and marked attempted after frontier decoupling.
- [ ] **LOOP-04**: A larger frontier batch does not reintroduce the Phase 6 oversized-filter relay rejection failure mode.

### Count Overhead

- [ ] **COUNT-01**: Operator can throttle `CountPubkeys` and `CountStalePubkeys` frequency without disabling crawl progress.
- [ ] **COUNT-02**: Batch logs and final run records remain accurate when count queries are skipped between sampling intervals.
- [ ] **COUNT-03**: Count-query failures remain recoverable through the existing Dgraph retry path when a sampled count is due.

### Measurement

- [ ] **MEASURE-01**: Each optimization round can be compared against baseline using `BATCH_METRICS` and `~/deepfry/crawler-metrics.jsonl`.
- [ ] **MEASURE-02**: Run records include the new frontier batch size and count-sampling settings so throughput results are self-describing.
- [ ] **MEASURE-03**: The milestone defines an operator verification procedure for running baseline and optimized rounds on the strfry host.

### Dgraph Write Path

- [ ] **DWRITE-01**: Follow-update throughput investigation identifies whether `AddFollowers` remains the dominant overhead after loop fixes.
- [ ] **DWRITE-02**: Any Dgraph write-path optimization preserves all-or-nothing kind-3 replacement semantics.
- [ ] **DWRITE-03**: Any Dgraph write-path optimization preserves `SkipAttempt` retry eligibility for transient follow-update failures.

### Testing

- [ ] **TEST-01**: Unit tests cover config loading/defaults for frontier batch and count-sampling settings without touching `~/deepfry/`.
- [ ] **TEST-02**: Unit tests cover loop accounting for larger selected batches, skipped attempts, and throttled count queries.
- [ ] **TEST-03**: Integration or operator-run verification covers a live Dgraph/relay round and records before-vs-after throughput evidence.

## Future Requirements

### Relay Tuning

- **TUNE-01**: Operator can run a measured quorum/timeout sweep and compare latency against hit-rate loss.
- **TUNE-02**: Operator can tune relay reconnect scheduling without scanning the full relay pool every batch.

### Dgraph Scaling

- **DSCALE-01**: Crawler can cache follower counts or otherwise avoid expensive full-frontier `count(~follows)` sorts on very large frontiers.
- **DSCALE-02**: Crawler can evaluate Dgraph write parallelism with a correctness-preserving transaction strategy.

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
| LOOP-01 | Phase 13 | Pending |
| LOOP-02 | Phase 13 | Pending |
| LOOP-03 | Phase 13 | Pending |
| LOOP-04 | Phase 13 | Pending |
| COUNT-01 | Phase 13 | Pending |
| COUNT-02 | Phase 13 | Pending |
| COUNT-03 | Phase 13 | Pending |
| MEASURE-01 | Phase 13 | Pending |
| MEASURE-02 | Phase 13 | Pending |
| MEASURE-03 | Phase 13 | Pending |
| DWRITE-01 | Phase 14 | Pending |
| DWRITE-02 | Phase 14 | Pending |
| DWRITE-03 | Phase 14 | Pending |
| TEST-01 | Phase 13 | Pending |
| TEST-02 | Phase 13 | Pending |
| TEST-03 | Phase 14 | Pending |

**Coverage:**
- v1.6 requirements: 16 total
- Mapped to phases: 16
- Unmapped: 0

---
*Requirements defined: 2026-06-18*
*Last updated: 2026-06-18 after v1.6 milestone definition*
