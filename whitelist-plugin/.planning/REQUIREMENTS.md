# Requirements: Whitelist Plugin — v1.1 Bloom Filter Gate Plugin

**Defined:** 2026-06-29
**Core Value:** Every event written to the relay comes from a pubkey in the web of trust — enforced cheaply, reliably, and without forking StrFry.

## v1.1 Requirements

Requirements for the Bloom Filter Gate Plugin milestone. Each maps to roadmap phases.

### Shared Bloom Library

- [x] **BLOOM-01**: A shared `pkg/bloom` package builds a filter from a set of pubkeys, sized to a configurable false-positive rate (default 0.0001% / 1e-6)
- [x] **BLOOM-02**: A filter serializes to and deserializes from a portable binary format carrying its parameters (bit size, hash count) and a generation/version marker
- [x] **BLOOM-03**: Membership query distinguishes "definitely not present" from "possibly present", with no false negatives

### Server Bloom Endpoint

- [x] **SRV-01**: The server rebuilds the bloom filter from the in-memory whitelist on each refresh and swaps it atomically alongside the existing map (lock-free reads, no read stalls)
- [x] **SRV-02**: `GET /bloom` returns the current serialized filter
- [x] **SRV-03**: `/bloom` supports conditional GET (ETag / `If-None-Match`), returning `304 Not Modified` when the filter is unchanged since the client's last fetch
- [x] **SRV-04**: The bloom false-positive rate / sizing is configurable via the server YAML (default 0.0001%)

### Bloom Gate Plugin

- [x] **GATE-01**: A new standalone `cmd/bloom` StrFry writePolicy plugin reuses the existing JSONL `Handler`/`IOAdapter` protocol abstractions
- [x] **GATE-02**: Per-event decisions use the local filter only — not-in-set → reject, maybe-in-set → accept — with zero per-event HTTP
- [x] **GATE-03**: The plugin fetches the filter from the server `/bloom` endpoint on startup and on a periodic interval (~6h, conditional GET), swapping it atomically
- [x] **GATE-04**: The plugin persists each successfully fetched filter to the config directory (`~/deepfry/`)
- [x] **GATE-05**: When the server is unreachable, the plugin loads and serves decisions from the persisted on-disk filter
- [x] **GATE-06**: Cold start blocks (returns no decisions) only when there is neither a reachable server nor a persisted filter on disk
- [x] **GATE-07**: The plugin is configured via `~/deepfry/` YAML (server URL, refresh interval, persisted-filter path)

### Ops & Integration

- [x] **OPS-01**: Makefile build targets for the bloom plugin (native + static Alpine)
- [x] **OPS-02**: The Docker image bakes the bloom binary; `strfry.conf` can select it as the writePolicy plugin
- [x] **OPS-03**: README documents the bloom plugin and the `/bloom` endpoint

## v2 / Future Requirements

Acknowledged but deferred — not in the current roadmap.

### Bloom Gate

- **GATE-F1**: Faster (minutes-scale) refresh option for near-real-time whitelist propagation
- **GATE-F2**: Metrics endpoint / stderr counters for bloom hit/miss/accept rates and filter generation age

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Modifying `cmd/whitelist` or `cmd/router` | Bloom gate is a separate, opt-in fourth binary; proven plugins stay byte-identical |
| Per-event HTTP fallback in steady state | Bloom is the sole local gate by design (maybe → accept); HTTP only ever used for the periodic filter fetch |
| Eliminating false-positive accepts | A ~1e-6 leak rate is deliberately tolerated for zero per-event network cost |
| Forking StrFry | All integration via the stdin/stdout JSON plugin protocol |
| Counting / cuckoo / deletable filters | Whitelist is rebuilt wholesale each refresh; a plain bloom rebuilt per cycle suffices |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| BLOOM-01 | Phase 1 | Complete |
| BLOOM-02 | Phase 1 | Complete |
| BLOOM-03 | Phase 1 | Complete |
| SRV-01 | Phase 2 | Complete |
| SRV-02 | Phase 2 | Complete |
| SRV-03 | Phase 2 | Complete |
| SRV-04 | Phase 2 | Complete |
| GATE-01 | Phase 3 | Complete |
| GATE-02 | Phase 3 | Complete |
| GATE-03 | Phase 3 | Complete |
| GATE-04 | Phase 3 | Complete |
| GATE-05 | Phase 3 | Complete |
| GATE-06 | Phase 3 | Complete |
| GATE-07 | Phase 3 | Complete |
| OPS-01 | Phase 4 | Complete |
| OPS-02 | Phase 4 | Complete |
| OPS-03 | Phase 4 | Complete |

**Coverage:**

- v1.1 requirements: 17 total
- Mapped to phases: 17 ✓
- Unmapped: 0

---
*Requirements defined: 2026-06-29*
*Last updated: 2026-06-29 after roadmap creation (4 phases, 17/17 mapped)*
