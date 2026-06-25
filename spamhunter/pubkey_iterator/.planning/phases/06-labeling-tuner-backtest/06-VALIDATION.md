---
phase: 6
slug: labeling-tuner-backtest
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-26
---

# Phase 6 — Validation Strategy

> Per-phase validation contract. Source: 06-RESEARCH.md §Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust built-in `#[test]` + `tempfile` (temp-FILE SQLite, never `:memory:`) |
| **Config file** | none — `cargo test` |
| **Quick run command** | `cargo test --lib tune` |
| **Full suite command** | `cargo test` |
| **Estimated runtime** | ~12s (fit over a handful of synthetic rows is instant) |

Fixtures are synthetic (D-08): seed `signal` + `backpropagation` rows on a write connection (mirroring `export::tests::seed_scored_pubkey`). No real labels, no network.

---

## Sampling Rate

- **After every task commit:** `cargo test --lib tune` (<5s)
- **After every wave:** `cargo test` (full suite — guards the `combine` refactor + schema-rename test edits)
- **Phase gate:** full suite green before `/gsd-verify-work`
- **Max feedback latency:** ~12s

---

## Phase Requirements → Test Map

| Req | Behavior | Test Type | Automated Command | File Exists |
|--------|----------|-----------|-------------------|-------------|
| TUNE-01 | `backpropagation` table created (right shape); direct INSERT round-trips | unit | `store::tests::open_creates_wal_and_schema` (update) + `backpropagation_insert_roundtrip` | ❌ W0 |
| TUNE-02 | seed signals+labels (both classes), run `tune`, `weight` rows updated w/ `tuned_at`/`tuned_from_run` | unit | `tune::tests::tune_writes_weights_with_provenance` | ❌ W0 |
| TUNE-02 | same fixture twice → identical weights (deterministic fit) | unit | `tune::tests::fit_is_deterministic` | ❌ W0 |
| TUNE-03 | retuned weight set appears in the next run's `run.config_json` snapshot | unit | `run::tests::snapshot_records_tau_and_weights` (+ confirming assertion) | ✅ exists |
| TUNE-04 | `review_queue` = deterministic sample of unflagged pubkeys; re-run → same sample | unit | `tune::tests::review_queue_samples_unflagged_deterministically` | ❌ W0 |
| TUNE-05 | regression fixture → gate BLOCKS, `weight` table UNCHANGED | unit | `tune::tests::backtest_blocks_regression_and_leaves_weights` | ❌ W0 |
| TUNE-05 | clean separable fixture → backtest PASSES, weights adopted | unit | `tune::tests::backtest_passes_and_adopts` | ❌ W0 |
| guard | single-class labels → clear precondition error, no write | unit | `tune::tests::single_class_labels_blocks_with_message` | ❌ W0 |
| guard | missing signal for a layer → feature defaults to 0.0 | unit | `tune::tests::sparse_signals_default_to_zero` | ❌ W0 |

---

## Wave 0 Requirements

- [ ] `src/tune.rs` — module (join read, feature matrix, fit, backtest gate, weight write, review_queue population) + tests
- [ ] `src/store/schema.rs` — rename `label`→`backpropagation`; add `review_queue` DDL
- [ ] `src/store/mod.rs` + `src/detect/mod.rs` — update existing `"label"` references (table-list assertion + no_enforcement counts/message)
- [ ] `src/lib.rs` — `pub mod tune;`
- [ ] `Cargo.toml` — Phase-6 dep block (linfa 0.8 / linfa-logistic 0.8 / ndarray **0.16** — NOT 0.17)
- [ ] `src/detect/mod.rs` — extract `pub fn combine(...)` and call it from `ScoringStage::score` (shared by tune's backtest re-score; guarded by existing determinism tests)
- [ ] optional: `tune.review_sample_size` + hyperparameter fields in `config.rs`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real-label tune+backtest cycle | TUNE-01..05 | Real human labels don't exist in an autonomous run | After humans INSERT real rows into `backpropagation`, run `tune`, confirm new weights written with provenance only when the backtest passes; a regression leaves weights unchanged. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Deterministic-fit test present; backtest-blocks-regression test present
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
