---
phase: 04-detection-layers-logistic-combiner
plan: 01
subsystem: detection
status: complete
tags: [detection, config, combiner, pipeline, determinism]
dependency_graph:
  requires:
    - src/store/mod.rs (Store, persist, weight_write_conn, begin_run, reader)
    - src/store/writer.rs (UPSERT_SCORE/UPSERT_SIGNAL, fixed-order subscore iteration)
    - src/store/queries.rs (read_scores, read_signals)
    - src/model.rs (Persist, SubScore, Weight)
    - src/pipeline.rs (run_pipeline async↔sync seam, consume_noop)
    - src/fetch.rs (match_groups, fetch_batch)
    - src/graphql/queries.rs (AuthorGroup, Event)
  provides:
    - src/config.rs (Config + path-arg load + ConfigError)
    - "src/detect/mod.rs (Layer trait, LayerOutput, ScoringStage, ScoredInput, read_weights, seed_weights_if_empty, BIAS_KEY/THRESHOLD_KEY)"
    - src/pipeline.rs (ScoredInput channel carrier; production_fetch D-15 wiring)
    - pubkey_iterator_config.example.toml
    - Store::weight_write_conn (weight-only short-lived write connection)
  affects:
    - Plan 04-02 (registers L1/L3 real layers into ScoringStage::from_config)
    - Plan 04-03 (registers L0/L4; fills ScoredInput.whitelisted in the fetch stage)
tech_stack:
  added:
    - unicode-segmentation 1.13 (declared; consumed by L1/L3 in 04-02)
    - url 2.5 (declared; consumed by L4 in 04-02/04-03)
    - toml 1.1 (config parsing, in use)
  patterns:
    - object-safe dyn Layer trait + fixed-order Vec<Box<dyn Layer>> registry
    - positional-Vec weighted sum (no HashMap in score path → OPS-02 determinism)
    - sigmoid(bias + Σ wᵢ·xᵢ) logistic combiner reading weights from the weight table
    - weight-table seed-on-empty via a weight-only short-lived connection (no actor race)
    - ScoredInput channel carrier so the fetch-stage whitelist bool reaches stage.score
    - match_groups wired in the fetch stage so zero-event pubkeys still get scored (D-15)
key_files:
  created:
    - src/config.rs
    - src/detect/mod.rs
    - pubkey_iterator_config.example.toml
  modified:
    - Cargo.toml
    - src/lib.rs
    - src/store/mod.rs
    - src/pipeline.rs
decisions:
  - "Config layers modeled as a typed struct (Layers { l0, l1, l3, l4 }) with serde rename, not a HashMap — keeps the fixed declaration order explicit and avoids a HashMap in any config-derived path."
  - "ScoringStage exposes from_layers (test/seam ctor) AND from_config; from_config's layer Vec is empty in Plan 01 (real layers register in 02/03) but the by-name weight lookup + sentinel bias/τ reads are already wired."
  - "_threshold sentinel stores τ in BOTH the weight column (NOT NULL constraint) and the threshold column; readers use the threshold column (from_config reads .threshold)."
  - "Whitelist bool is wrapped into ScoredInput in run_pipeline's producer (whitelisted=false) rather than in the fetch source, so Plan 03 only moves the real async L0 lookup into the fetch stage with no pipeline signature change."
metrics:
  duration: ~8 min
  completed: 2026-06-25
  tasks: 3
  files_created: 3
  files_modified: 4
  tests_added: 14
  tests_total: 58
---

# Phase 4 Plan 01: Detection-Layer Walking Slice Summary

The thinnest real end-to-end per-pubkey verdict: a TOML config seeds the `weight`
table, a shared `Layer` trait feeds a fixed-order `ScoringStage` whose
`sigmoid(Σwᵢxᵢ+b)` combiner fuses sub-scores into a persisted `score` row + per-layer
`signal` rows with evidence, every enumerated pubkey (zero-event included) is scored
via the now-wired `match_groups`, and the whole path is deterministic — proven by one
trivial layer; the four real layers plug into this same registry in Plans 02–03.

## What Was Built

### Task 1 — TOML config loader + Phase-4 deps + lib wiring (`7a14dd5`)
- Added the three Phase-4-owned deps (`unicode-segmentation` 1.13, `url` 2.5, `toml`
  1.1) — and only those (no gaoya/linfa/simhash/xxhash/ahash; dep discipline).
- `src/config.rs`: serde `Config` (adapter_url, whitelist_url, tau, bias, and four
  per-layer entries with enable+weight+layer-specific tunables) and a path-argument
  `load(&Path) -> Result<Config, ConfigError>`. `ConfigError` (thiserror) has Read +
  Parse variants and never panics.
- `pubkey_iterator_config.example.toml` at repo root documenting the conservative
  D-08 defaults.
- 4 tests, each writing into a `tempfile::TempDir` path (D-09 — never `~/deepfry`).

### Task 2 — Layer trait + ScoringStage combiner + weight seed/read (`0c38e3c`)
- `src/detect/mod.rs`: object-safe `Layer: Send + Sync` trait (`name()`,
  `score(events, whitelisted) -> LayerOutput`), `LayerOutput { value, evidence }`.
- `ScoringStage { layers, weights, bias, tau }` with `from_layers` (test/seam ctor)
  and `from_config`. `score()` accumulates `z = bias + Σ weights[i]·xᵢ` over a fixed
  positional `Vec` (never a HashMap → OPS-02), `debug_assert`s `xᵢ∈[0,1]`, and builds
  a `Persist` with per-layer `SubScore` carrying the evidence JSON.
- `read_weights` (parameterized SELECT, ORDER BY layer) and `seed_weights_if_empty`
  (writes the six combiner rows — four layer weights + `_bias` + `_threshold` — from
  config on first run, no-op thereafter) via the new `Store::weight_write_conn`
  short-lived weight-only connection. Every write uses `params![]` (T-04-01).
- `ScoredInput { group, whitelisted }` carrier defined here (consumed in Task 3).
- 6 tests: out-of-range `debug_assert`, single-layer sigmoid + subscore, multi-signal
  agreement (single layer < τ, multi-signal > τ), seed-then-read (six rows, second
  call reads stored values), determinism, `from_config` sentinel reads.

### Task 3 — ScoredInput carrier + match_groups wiring, D-15 (`0cdfc84`)
- Changed `run_pipeline`'s bounded channel payload from `AuthorGroup` to
  `ScoredInput`; the producer wraps each fetched group as
  `ScoredInput { group, whitelisted: false }`. Consumer bound is now
  `C: Fn(&ScoredInput) + Send + Sync + 'static`. `consume_noop` reads
  `.group.events`; the existing watermark / injected-consumer tests were updated to
  unwrap the carrier. Bounded back-pressure semantics unchanged.
- `production_fetch`: `fetch_batch` then `match_groups(batch, groups)` rebuilds one
  `AuthorGroup` per REQUESTED pubkey (omitted → empty events) — closing the Phase-3
  D-15 seam so every enumerated pubkey reaches the consumer.
- 4 tests: carrier round-trips the whitelist bool into `Persist`; a zero-event
  (adapter-omitted) pubkey still gets a `score` row; a normal pubkey persists a score
  + non-empty-evidence signal row (SCORE-05); a re-run over the same corpus into two
  fresh temp DBs is byte-identical (OPS-02).

## Verification

- `cargo test --lib`: **58 passed**, 0 failed (44 baseline + 4 config + 6 detect + 4
  pipeline). All pre-existing tests stay green after the `ScoredInput` payload change.
- `cargo clippy --all-targets -- -D warnings`: clean.
- `cargo build` / `cargo build --bins`: clean.
- Config loader is path-argument-based; no test references `~/deepfry`.
- `ScoringStage::score` iterates a positional `Vec` (no HashMap) in the score path.
- Zero-event pubkey gets a score row; end-to-end re-run determinism holds.
- Only `unicode-segmentation`, `url`, `toml` added to Cargo.toml.

## Requirements Satisfied (in this slice)

- **DETECT-05** — shared object-safe `Layer` contract (`xᵢ∈[0,1]`, enable/disable via
  config omission, tunable weight/threshold).
- **SCORE-01** — `sigmoid(Σwᵢxᵢ+b)` combiner reading weights from the `weight` table.
- **SCORE-04** — pubkey-level only, no enforcement side-effect (only weight/score/
  signal rows written).
- **SCORE-05** — per-layer `signal` rows with non-empty evidence JSON.
- **OPS-02** — deterministic: positional sum, zero RNG, UPSERT idempotency; re-run
  byte-identical.
- **OPS-03** — TOML config seeds the `weight` table; retune without recompile.
- **D-15** — `match_groups` wired into `production_fetch`; zero-event pubkey scored.

## Deviations from Plan

None — plan executed exactly as written. No Rule 1–4 deviations were required.

## Known Stubs

- **`ScoringStage::from_config` builds an empty `layers` Vec (intentional, Plan 01).**
  File: `src/detect/mod.rs`. The four real layer structs (L0/L1/L3/L4) do not exist
  yet; Plans 02–03 push the enabled layers into this Vec in fixed order using the
  already-wired by-name `weight_of` lookup. The walking slice proves the combiner
  end-to-end via `from_layers` + a trivial layer (real persisted scores), so this
  stub does NOT block the slice goal — it is the documented forward-extension seam.
- **`ScoredInput.whitelisted` is hardcoded `false` in `run_pipeline`'s producer
  (intentional, Plan 01).** File: `src/pipeline.rs`. The carrier field and its flow
  into `stage.score(...)` already exist and are tested; Plan 03 moves the real async
  L0 whitelist lookup into the fetch stage and sets the bool — no pipeline signature
  change needed.

## Notes for Plans 02–03

- Register real layers in `ScoringStage::from_config`: for each enabled layer push
  `Box::new(<Layer>)` and `weight_of("<name>", config.layers.<x>.weight)` in fixed
  order L0, L1, L3, L4.
- Per-layer tunables already live on the config structs (`L1Config.hamming_threshold`
  /`shingle_size`/`min_events`, `L3Config.*`, `L4Config.*`, `L0Config.absence_subscore`).
- `unicode-segmentation` and `url` are declared but unused until 02/03 consume them.
- Plan 03's only pipeline change: resolve the whitelist bool in the fetch stage (e.g.
  inside or alongside `production_fetch`) and set `ScoredInput.whitelisted` — keep the
  HTTP out of the consumer (Pitfall 5).

## Self-Check: PASSED

- FOUND: src/config.rs
- FOUND: src/detect/mod.rs
- FOUND: pubkey_iterator_config.example.toml
- FOUND commit 7a14dd5 (Task 1)
- FOUND commit 0c38e3c (Task 2)
- FOUND commit 0cdfc84 (Task 3)
