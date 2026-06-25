# Roadmap: Spamhunter — Pubkey Spam Classifier

## Overview

The journey from empty repo to a correctable, low-false-positive spammer list runs along a single dependency-honest spine: a place to **persist** results, a proven **connection** to the corpus, a **bounded-memory pipeline** that can stream the whole keyspace, a **scoring engine** of weighted detection layers fused by a logistic combiner, a **shippable list** with per-layer reasons, and finally the **correctability loop** that re-tunes weights from human labels under a backtest gate.

Built MVP-first: get a (rough) per-pubkey spammer verdict produced **end-to-end** as early as possible (Phases 1–4 deliver the first real verdict), then make it shippable (Phase 5) and self-correcting (Phase 6). Each phase is a thin vertical slice that leaves a runnable system behind it. The combiner (L7) is deliberately early — it is the integration seam every detection layer plugs into. Cross-pubkey clustering (L6) and the extra layers (L2/L5/L8) are explicitly deferred to v2.

The dominant project risk is **false positives**, not throughput. That risk is baked into the success criteria of every phase that introduces a detection layer or a weight change: layers are weighted terms (never hard gates), output is a reviewable list (never enforcement), and any weight change must survive a backtest against the human-labeled set before adoption.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Persistence Foundation** - SQLite store, schema, single-writer idempotent UPSERT API — the dependency root (completed 2026-06-25)
- [x] **Phase 2: GraphQL Client + Author Enumeration** - Prove the contract: paginate every distinct pubkey, resumably, with graceful adapter-error handling (completed 2026-06-25)
- [x] **Phase 3: Fetcher + Bounded Streaming Pipeline** - tokio → bounded channel → rayon; fetch ~100 events/pubkey at corpus scale with proven bounded memory (completed 2026-06-25)
- [x] **Phase 4: Detection Layers + Logistic Combiner (first end-to-end verdict)** - Layer trait + L7 combiner + P1 layers (L0/L1/L3/L4) → first per-pubkey spam score with per-layer evidence (completed 2026-06-25)
- [ ] **Phase 5: CLI `run` + `export` (first shippable list)** - Drive a full batch and export the reviewable suspected-spammer list with per-layer reasons
- [ ] **Phase 6: Labeling + Logistic Tuner + Backtest Gate** - Capture human labels, re-fit weights from labels, and gate adoption on a no-regression backtest

## Phase Details

### Phase 1: Persistence Foundation

**Goal**: A single SQLite store with the full schema and an idempotent single-writer API exists, so every later stage has somewhere to persist runs, per-pubkey scores, per-layer signals, labels, and weights.
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: SCORE-02
**Success Criteria** (what must be TRUE):

  1. A fresh SQLite file is created on demand with WAL mode and the full schema (`run`, `pubkey`, `score`, `signal` EAV, `fingerprint`, `label`, `weight`).
  2. Writing the same `(run_id, pubkey)` (and `(run_id, pubkey, layer)`) twice leaves exactly one row — re-processing a pubkey within a run is idempotent (UPSERT), never duplicated.
  3. A new detection layer can record a sub-score by inserting a `signal` row with a new `layer` name without any schema migration.
  4. Writes go through a single writer using batched transactions; a developer can persist a batch of synthetic scores and read them back identically.

**Plans**: 1/1 plans complete

- [x] 01-01-PLAN.md — SQLite store: scaffold + WAL schema + single-writer idempotent UPSERT API + 5-test round-trip contract (SCORE-02)

### Phase 2: GraphQL Client + Author Enumeration

**Goal**: The engine can enumerate every distinct pubkey in the live corpus through the LMDB2GraphQL adapter, resumably and terminating cleanly, while handling the adapter's real failure modes — proving connectivity against the actual contract before any analysis exists.
**Mode:** mvp
**Depends on**: Phase 1
**Requirements**: INGEST-01, INGEST-04
**Success Criteria** (what must be TRUE):

  1. Running the enumerator against the adapter walks the entire `authors` keyspace via cursor pagination, visits each distinct pubkey exactly once, and terminates cleanly when `hasMore` is false.
  2. The walk is resumable: the latest `endCursor` and `stats.maxLevId` are persisted per batch into the `run` row, and a restart with `--resume` continues from the stored cursor instead of starting over.
  3. A `503` causes a backoff-and-retry without advancing the cursor; an `INVALID_CURSOR` error restarts pagination from page 1; GraphQL `errors[]`/`extensions.code` in a `200` body are parsed rather than ignored.
  4. `maxLevId` is recorded at run start and end as a snapshot-drift probe, and a corpus change mid-pagination does not abort the run.

**Plans**: 3/3 plans complete
**Wave 1**

- [x] 02-01-PLAN.md — Store run-state helpers + pubkey-only single-writer insert path (WriteMsg enum) for resume/abort/drift (INGEST-01, INGEST-04)
- [x] 02-02-PLAN.md — Reusable async GraphQL client: hand-written `authors`/`stats` queries + envelope with two-layer error dispatch (INGEST-01, INGEST-04)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 02-03-PLAN.md — `authors` opaque-cursor walk + minimal `--resume` binary; bounded 503 retry, INVALID_CURSOR restart, drift probe, abort-preserves-cursor (INGEST-01, INGEST-04)

### Phase 3: Fetcher + Bounded Streaming Pipeline

**Goal**: Enumerated pubkeys flow through a bounded-memory streaming pipeline (tokio fetch → bounded channel → rayon analysis) that fetches each pubkey's ~100 recent events without ever buffering the corpus — locking in the structural decision before any layer depends on it.
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: INGEST-02, INGEST-03
**Success Criteria** (what must be TRUE):

  1. The fetcher retrieves each pubkey's most-recent ~100 events via batched `latestPerAuthor(kind:1, perAuthor:100)` at ≤1000 authors per call, respecting the 256 KiB body limit and the silent ≤500 page clamp.
  2. Fetched author groups are matched back to requested pubkeys by the `author` field (never zipped by index), and authors omitted because they have zero matching events are handled without misalignment.
  3. Running the pipeline over a large synthetic author set holds memory bounded by the channel capacity (not by corpus size) — fetch back-pressures on the channel and CPU analysis runs off the tokio threads.
  4. A no-op (pass-through) consumer at the end of the pipeline proves end-to-end flow from enumeration through fetch to a rayon stage with no unbounded buffering.

**Plans**: 2/2 plans complete
**Wave 1**

- [x] 03-01-PLAN.md — Additive `latestPerAuthor` query + serde structs + client wrapper; `fetch_batch` (match-by-author, 413 shrink-retry) + `read_pubkeys` enumeration source (INGEST-02)

**Wave 2** *(blocked on Wave 1 completion)*

- [x] 03-02-PLAN.md — `run_pipeline`: tokio fetcher → bounded `flume` channel → rayon no-op consumer (injected-closure Phase-4 seam); bounded-memory watermark proof + live `latestPerAuthor` deserialization check (INGEST-03, INGEST-02)

### Phase 4: Detection Layers + Logistic Combiner (first end-to-end verdict)

**Goal**: The pipeline produces a real per-pubkey spam score: each P1 detection layer emits a normalized sub-score through a common Layer contract, the logistic combiner fuses them, and the score plus per-layer evidence is persisted — the first end-to-end runnable verdict.
**Mode:** mvp
**Depends on**: Phase 3
**Requirements**: DETECT-01, DETECT-02, DETECT-03, DETECT-04, DETECT-05, SCORE-01, SCORE-04, SCORE-05, OPS-02, OPS-03
**Success Criteria** (what must be TRUE):

  1. Each P1 layer emits a normalized sub-score xᵢ∈[0,1] through one shared Layer contract: L0 whitelist (absence emits a weighted spam sub-score, presence clears only this layer — never a gate/exemption), L1 within-pubkey near-duplicate (SimHash + Hamming), L3 content entropy/templated-text, L4 link & mention ratios.
  2. The combiner fuses the sub-scores into one per-pubkey score via weighted logistic combination `sigmoid(Σwᵢxᵢ + b)`, using hand-set conservative starting weights read from the `weight` table.
  3. For every scored pubkey the `score` row and the per-layer `signal` rows are persisted, and each flagged pubkey carries a per-layer explanation (which layers fired, each sub-score, the contributing evidence) sufficient for a human to understand *why*.
  4. Verdicts are pubkey-level only (per-event signals are inputs, never the deliverable) and no enforcement action is taken; each layer can be independently enabled/disabled and has a tunable threshold/weight set from a config file without recompiling.
  5. Re-running the same corpus snapshot with the same weights produces identical verdicts (deterministic: seeded RNG, fixed layer-sum order, UPSERT on `(run_id, pubkey)`).

**Plans**: 3/3 plans complete

**Wave 1**

- [x] 04-01-PLAN.md — Walking slice: TOML config + weight-table seed + Layer trait + ScoringStage logistic combiner + match_groups wiring (zero-event scored) + one trivial layer end-to-end, deterministic (DETECT-05, SCORE-01, SCORE-04, SCORE-05, OPS-02, OPS-03)

**Wave 2** *(blocked on Wave 1)*

- [x] 04-02-PLAN.md — L1 near_duplicate (hand-rolled deterministic SimHash + Hamming ratio) + L3 content_entropy (Shannon + emoji/hashtag density) + WriteMsg::Fingerprints (DETECT-02, DETECT-03)

**Wave 3** *(blocked on Wave 2)*

- [x] 04-03-PLAN.md — L0 whitelist_absence (reqwest GET /check/{pubkey}, per-run cache, fail-safe) + L4 link_mention (url-crate hosts) + multi-signal-agreement + config enable/disable + no-enforcement verification (DETECT-01, DETECT-04, SCORE-04, SCORE-05, OPS-03)

### Phase 5: CLI `run` + `export` (first shippable list)

**Goal**: A human can drive a full batch and get back the suspected-spammer list — the first shippable, reviewable artifact — with each flagged pubkey accompanied by its per-layer reasons and evidence.
**Mode:** mvp
**Depends on**: Phase 4
**Requirements**: SCORE-03, OPS-01
**Success Criteria** (what must be TRUE):

  1. A `run` subcommand executes a full batch over the corpus end-to-end (enumerate → fetch → score → persist) and reports progress to completion.
  2. An `export` subcommand emits the suspected-spammer list — pubkeys whose score exceeds a tunable threshold τ — read from SQLite.
  3. Each exported pubkey carries its per-layer decomposition (which layers fired, sub-scores, sample evidence) so a reviewer can judge the verdict, and the run's threshold and weight snapshot are recorded for reproducibility.

**Plans**: TBD

### Phase 6: Labeling + Logistic Tuner + Backtest Gate

**Goal**: The correctability loop closes — humans label confirmed spam and false positives, an offline tuner re-fits layer weights from those labels, the next run consumes them reproducibly, and no weight change is adopted unless it survives a backtest against the full labeled set.
**Mode:** mvp
**Depends on**: Phase 5
**Requirements**: TUNE-01, TUNE-02, TUNE-03, TUNE-04, TUNE-05
**Success Criteria** (what must be TRUE):

  1. A `label` subcommand records run-independent labels (confirmed spam and confirmed false-positive/ham) for pubkeys in SQLite, and the review queue includes a random sample of *unflagged* pubkeys to counter selection bias.
  2. A `tune` subcommand fits a logistic model (`linfa-logistic`) over the stored `signal × label` join and writes new layer weights (plus bias and threshold) to the `weight` table with provenance recorded.
  3. Each run reads the latest weights at startup and snapshots them into run metadata, so any past score can be traced to the exact weights that produced it.
  4. New weights are backtested against the full human-labeled set before adoption: confirmed-spam pubkeys must remain flagged (no new false negatives) and confirmed-non-spam pubkeys must remain unflagged (no new false positives); a regression is surfaced and blocks/flags adoption of the new weights.

**Plans**: TBD

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Persistence Foundation | 1/1 | Complete    | 2026-06-25 |
| 2. GraphQL Client + Author Enumeration | 3/3 | Complete    | 2026-06-25 |
| 3. Fetcher + Bounded Streaming Pipeline | 2/2 | Complete    | 2026-06-25 |
| 4. Detection Layers + Logistic Combiner | 3/3 | Complete    | 2026-06-25 |
| 5. CLI `run` + `export` | 0/TBD | Not started | - |
| 6. Labeling + Logistic Tuner + Backtest Gate | 0/TBD | Not started | - |

## Coverage

✓ All 22 v1 requirements mapped to exactly one phase
✓ No orphaned requirements

| Requirement | Phase |
|-------------|-------|
| INGEST-01 | Phase 2 |
| INGEST-02 | Phase 3 |
| INGEST-03 | Phase 3 |
| INGEST-04 | Phase 2 |
| DETECT-01 | Phase 4 |
| DETECT-02 | Phase 4 |
| DETECT-03 | Phase 4 |
| DETECT-04 | Phase 4 |
| DETECT-05 | Phase 4 |
| SCORE-01 | Phase 4 |
| SCORE-02 | Phase 1 |
| SCORE-03 | Phase 5 |
| SCORE-04 | Phase 4 |
| SCORE-05 | Phase 4 |
| TUNE-01 | Phase 6 |
| TUNE-02 | Phase 6 |
| TUNE-03 | Phase 6 |
| TUNE-04 | Phase 6 |
| TUNE-05 | Phase 6 |
| OPS-01 | Phase 5 |
| OPS-02 | Phase 4 |
| OPS-03 | Phase 4 |

## Deferred to v2 (not in this roadmap)

Tracked in REQUIREMENTS.md; not v1 phases:

- DETECT-06 (L2 cadence/burst), DETECT-07 (L5 tag/kind fingerprint), DETECT-08 (L6 cross-pubkey clustering — top v2 priority), DETECT-09 (L8 language/script & homoglyph)
- PERF-01 (direct `heed` LMDB reads, profiling-gated), SVC-01 (incremental service mode)

---
*Roadmap created: 2026-06-25*
