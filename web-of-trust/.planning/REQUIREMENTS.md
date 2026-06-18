# Requirements: Web-of-Trust Crawler

**Defined:** 2026-06-18
**Core Value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

## v1.5 Requirements

Requirements for Dgraph follow-update timeout resilience. Each maps to Phase 12.

### Dgraph Follow Writes

- [x] **DWRITE-01**: A transient Dgraph `DeadlineExceeded` during follow updates does not abort the crawler process or discard the rest of the batch.
- [x] **DWRITE-02**: Follow-edge writes for large contact lists are bounded into safe units with per-unit context deadlines and partial-progress accounting.
- [x] **DWRITE-03**: Failed follow updates leave the pubkey eligible for a later retry without corrupting existing follow edges or permanently aging it out as a clean miss.
- [x] **DWRITE-04**: Fatal Dgraph write errors still fail loudly rather than being hidden by the transient-retry path.

### Observability

- [x] **OBS-02**: Production logs identify slow follow-update operations by pubkey, follow-count/chunk, elapsed time, retry count, and final outcome.

### Testing

- [x] **TEST-06**: Unit or integration-style tests cover timeout classification, chunk/partial-progress behavior, retry scheduling, and fatal-error passthrough.

## Future Requirements

Deferred to future milestones. Tracked but not in the current roadmap.

### Crawl Throughput

- **THRU-01**: Frontier batch selection can be decoupled from relay filter chunk size to amortize per-loop Dgraph overhead.
- **THRU-02**: Per-batch `CountPubkeys` and `CountStalePubkeys` calls can be throttled or made asynchronous when they are only needed for observability.
- **THRU-03**: Frontier prefetch can overlap Dgraph reads with relay fetches without breaking shutdown or retry semantics.
- **THRU-04**: Relay quorum and timeout settings can be swept in measurable production rounds using the Spike 001 metrics.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Broad crawl throughput tuning | The milestone targets the aborting Dgraph follow-update failure first; throughput candidates remain in `.planning/spikes/MANIFEST.md`. |
| StrFry changes | Protocol rule: StrFry stays stock and unmodified. |
| Event payload storage outside StrFry | Data separation rule: Dgraph stores ID-only pubkey graph data only. |
| Whitelist-plugin behavior | The failure is in the crawler's Dgraph write path, not whitelist admission logic. |
| Manual edits to `~/deepfry/web-of-trust.yaml` | Live config must not be overwritten during testing; use temp HOME/config fixtures. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| DWRITE-01 | Phase 12 | Complete |
| DWRITE-02 | Phase 12 | Complete |
| DWRITE-03 | Phase 12 | Complete |
| DWRITE-04 | Phase 12 | Complete |
| OBS-02 | Phase 12 | Complete |
| TEST-06 | Phase 12 | Complete |

**Coverage:**
- v1.5 requirements: 6 total
- Mapped to phases: 6
- Unmapped: 0

---
*Requirements defined: 2026-06-18*
*Last updated: 2026-06-18 after Phase 12 completion*
