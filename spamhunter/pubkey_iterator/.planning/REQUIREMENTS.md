# Requirements: Spamhunter — Pubkey Spam Classifier

**Defined:** 2026-06-25
**Core Value:** Produce an accurate, low-false-positive list of suspected spammer pubkeys as fast as possible, with every layer independently tunable and the whole system correctable from human-labeled false positives.

## v1 Requirements

Requirements for the initial release. Each maps to a roadmap phase.

### Ingestion

- [ ] **INGEST-01**: Engine enumerates all distinct pubkeys via the LMDB2GraphQL `authors` query with cursor pagination, resumable and terminating cleanly at the end of the keyspace
- [ ] **INGEST-02**: Engine fetches each pubkey's most-recent ~100 events via batched `latestPerAuthor` (≤1000 authors/call), respecting the 256 KiB body limit and the ≤500 page clamp
- [ ] **INGEST-03**: Fetch (I/O) and analysis (CPU) run as a bounded-memory streaming pipeline (tokio → bounded channel → rayon) that never buffers the full corpus
- [ ] **INGEST-04**: Engine handles adapter conditions gracefully — `503` (back off, do not advance cursor), `INVALID_CURSOR` (restart pagination), empty-group omission (match by author, never zip by index), and snapshot drift (record `maxLevId` start/end, do not abort)

### Detection

- [ ] **DETECT-01**: Whitelist layer queries the whitelist (Dgraph `Profile` / whitelist-plugin); **absence emits a weighted spam sub-score, presence clears only this layer** (no exemption)
- [ ] **DETECT-02**: Within-pubkey near-duplicate layer detects repeated / copy-paste content across a pubkey's own events (SimHash + Hamming threshold) and emits a sub-score
- [ ] **DETECT-03**: Content-entropy layer flags low-entropy templated text and high-entropy gibberish, plus URL/emoji/hashtag density, and emits a sub-score
- [ ] **DETECT-04**: Link & mention layer scores URL ratio, repeated domains, mass `p`-tag mentions, and hashtag stuffing, and emits a sub-score
- [ ] **DETECT-05**: Every layer emits a normalized sub-score xᵢ∈[0,1] through a common Layer contract, is independently enable/disable-able, and exposes a tunable threshold/weight

### Scoring & Output

- [ ] **SCORE-01**: A combiner fuses per-layer sub-scores into a per-pubkey spam score via weighted logistic combination (`sigmoid(Σwᵢxᵢ + b)`)
- [x] **SCORE-02**: Per-pubkey scores, per-layer sub-scores (EAV signal table), and run metadata persist to SQLite (WAL, batched writes), idempotent on `(run_id, pubkey)`
- [ ] **SCORE-03**: Engine produces the suspected-spammer list (pubkeys above a tunable threshold τ) with per-layer evidence, exportable from SQLite
- [ ] **SCORE-04**: Output is pubkey-level only — per-event signals are inputs, never the deliverable; no live enforcement
- [ ] **SCORE-05**: Every flagged pubkey carries a per-layer explanation — which layers fired, each layer's sub-score, and the contributing evidence (e.g. matched duplicate clusters, offending URLs/domains, entropy values) — persisted and exported so reviewers understand *why* and the feedback loop can consume the reasons

### Tuning & Feedback

- [ ] **TUNE-01**: Humans can record confirmed false positives (and true positives) as run-independent labels in SQLite
- [ ] **TUNE-02**: An offline `tune` step fits a logistic model (`linfa-logistic`) over stored signals × labels and writes new layer weights to a weights table
- [ ] **TUNE-03**: Each run reads the latest weights at startup and snapshots them into run metadata for reproducibility
- [ ] **TUNE-04**: The review/labeling queue includes randomly-sampled unflagged pubkeys to counter selection bias (negative sampling)
- [ ] **TUNE-05**: Any weight or algorithm change is backtested against the full human-labeled set before adoption — confirmed-spam pubkeys must remain flagged (guard against new false negatives) and confirmed-non-spam pubkeys must remain unflagged (guard against new false positives); regressions are surfaced and block/flag adoption of the new weights

### Operations

- [ ] **OPS-01**: A CLI drives the engine — full batch `run`, `export`, `label`, and `tune` subcommands
- [ ] **OPS-02**: Scoring is deterministic — same corpus snapshot + same weights → identical verdicts
- [ ] **OPS-03**: Layer weights and thresholds are configurable without recompiling (config file)

## v2 Requirements

Deferred to a future release. Tracked but not in the current roadmap. (The EAV signal schema means new detection layers are migration-free additions.)

### Additional Detection Layers

- **DETECT-06**: Posting-cadence / burst layer (L2) — co-signal-gated to avoid crawler-refresh-artifact false positives (the spam-clusters spike proved timing alone is FP-dominated)
- **DETECT-07**: Tag/kind fingerprint layer (L5) — templated tag structures, abnormal kind distributions
- **DETECT-08**: Cross-pubkey duplicate-clustering layer (L6) — corpus-wide MinHash+LSH aggregation (the distinct "Phase B"); strongest coordination signal, architecturally heaviest. **Top v2 priority.**
- **DETECT-09**: Language/script & homoglyph layer (L8) — UTS#39 confusable skeleton + mixed-script detection

### Performance & Runtime

- **PERF-01**: Direct strfry LMDB reads via `heed` to bypass the GraphQL hop (profiling-gated — only if the HTTP round-trip proves to be the bottleneck)
- **SVC-01**: Incremental service mode — track a `maxLevId` cursor and only (re)score new/changed pubkeys

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Local LLM / on-device model inference | Forbidden by the user — too slow for the speed goal |
| Per-event spam verdicts as the deliverable | Detection aggregates to the pubkey level; per-event signals are inputs only |
| Live enforcement / event rejection | Deliverable is a reviewable list; enforcement (feeding whitelist/quarantine) is a separate later concern with high blast radius |
| Structural graph spam detection | That is `spam-explorer`'s job; this engine consumes content, not the follow graph |
| Writing to / mutating strfry | LMDB2GraphQL is read-only by design; this engine only reads |
| Hard single-layer cutoffs | Anti-feature — false-positive-prone; verdicts require multi-signal agreement via the combiner |

## Traceability

Which phases cover which requirements. Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| INGEST-01 | Phase 2 | Mapped |
| INGEST-02 | Phase 3 | Mapped |
| INGEST-03 | Phase 3 | Mapped |
| INGEST-04 | Phase 2 | Mapped |
| DETECT-01 | Phase 4 | Mapped |
| DETECT-02 | Phase 4 | Mapped |
| DETECT-03 | Phase 4 | Mapped |
| DETECT-04 | Phase 4 | Mapped |
| DETECT-05 | Phase 4 | Mapped |
| SCORE-01 | Phase 4 | Mapped |
| SCORE-02 | Phase 1 | Mapped |
| SCORE-03 | Phase 5 | Mapped |
| SCORE-04 | Phase 4 | Mapped |
| SCORE-05 | Phase 4 | Mapped |
| TUNE-01 | Phase 6 | Mapped |
| TUNE-02 | Phase 6 | Mapped |
| TUNE-03 | Phase 6 | Mapped |
| TUNE-04 | Phase 6 | Mapped |
| TUNE-05 | Phase 6 | Mapped |
| OPS-01 | Phase 5 | Mapped |
| OPS-02 | Phase 4 | Mapped |
| OPS-03 | Phase 4 | Mapped |

**Coverage:**

- v1 requirements: 22 total
- Mapped to phases: 22 ✓
- Unmapped: 0 ✓

---
*Requirements defined: 2026-06-25*
*Last updated: 2026-06-25 after roadmap creation*
