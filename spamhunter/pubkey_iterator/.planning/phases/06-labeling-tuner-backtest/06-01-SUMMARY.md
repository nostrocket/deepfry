---
phase: 06-labeling-tuner-backtest
plan: 01
subsystem: store + detect (Phase-6 foundation)
tags: [schema-rename, ddl, linfa, combiner-extraction, tune-foundation]
requires: []
provides:
  - "backpropagation table (renamed from label) — operator ground-truth label store (TUNE-01/D-02)"
  - "review_queue table DDL — Phase-6 negative-sampling slice (Plan 03 populates)"
  - "detect::combine(weights, bias, xs) — single shared logistic combiner (Plan 02 backtest re-scores with it)"
  - "linfa 0.8 / linfa-logistic 0.8 / ndarray 0.16 dependency block (tune-only)"
affects:
  - "Plan 02 (tuner fit + backtest): consumes the renamed table, the linfa deps, and combine()"
  - "Plan 03 (negative sampling): populates review_queue"
tech-stack:
  added:
    - "linfa 0.8.1 (rust-ml ML framework, Dataset/Fit)"
    - "linfa-logistic 0.8.1 (binary LogisticRegression, L-BFGS via argmin 0.11 transitive)"
    - "ndarray 0.16.1 (pinned 0.16 — shares Array2/Dataset types with linfa; NOT 0.17)"
  patterns:
    - "Single shared combiner fn so live scoring + tuner backtest use byte-identical math"
    - "CREATE-rename (no data migration): old label table empty in every real DB"
key-files:
  created: []
  modified:
    - src/store/schema.rs
    - src/store/mod.rs
    - src/detect/mod.rs
    - src/model.rs
    - Cargo.toml
    - Cargo.lock
decisions:
  - "D-01: rename label->backpropagation (self-explanatory project name); update all four reference sites"
  - "D-02: humans INSERT labels directly via SQLite — intentionally NO label subcommand"
  - "D-03: linfa stack is Phase-6-owned; ndarray pinned 0.16 (Pitfall 1)"
  - "Label struct name kept (internal, no SQL depends on it) — only its doc-comment updated"
metrics:
  duration: ~4 min
  completed: 2026-06-25
  tasks: 2
  files: 6
status: complete
---

# Phase 6 Plan 01: Labeling/Tuner Foundation Summary

Renamed the unused Phase-1 `label` table to `backpropagation`, added the `review_queue` DDL, pulled the Phase-6 linfa logistic-regression stack (ndarray pinned 0.16), and extracted the combiner math into one shared `detect::combine()` so the tuner backtest re-scores with the exact function live scoring applies.

## What Was Built

**Task 1 — schema rename + review_queue + reference sites (TDD).**
- `SCHEMA_DDL` now creates `backpropagation` (renamed from `label`, all five columns unchanged: pubkey PK / is_spam / labeled_at / source / note) and a new `review_queue` table (run_id, pubkey, score, sampled_at, PK (run_id, pubkey)). CREATE-rename, no data migration — the old `label` table was empty in every real DB.
- Head-comment inventory + the `SCHEMA_DDL` doc bumped to 9 tables.
- Fixed all four rename reference sites: schema.rs DDL, `store::tests::open_creates_wal_and_schema` table-list array (now also asserts `review_queue`), `detect::tests::no_enforcement_side_effect` (counts `backpropagation`, message rephrased), `model.rs` `Label` doc-comment.
- New `store::tests::backpropagation_insert_roundtrip` proves the TUNE-01/D-02 human-insert contract: a direct `rusqlite::Connection` INSERT of (pubkey, is_spam=1, labeled_at, source, note) round-trips. RED-first (failed `no such table: backpropagation`), then GREEN.

**Task 2 — linfa deps + shared combine() extraction (TDD).**
- Added `linfa = "0.8"`, `linfa-logistic = "0.8"`, `ndarray = "0.16"` (default features, pure Rust no BLAS). Resolved to linfa 0.8.1 / linfa-logistic 0.8.1 / ndarray 0.16.1 / argmin 0.11.0 (transitive). Documenting comment notes the 0.16 pin rationale (Pitfall 1) and tune-only usage.
- Extracted `pub fn combine(weights: &[f64], bias: f64, xs: &[f64]) -> f64` (module-level, `sigmoid(bias + Σ wᵢxᵢ)` with a `debug_assert_eq!` on slice lengths). `ScoringStage::score` now collects per-layer sub-score values into a `Vec<f64>` in the same fixed index order and calls `combine(&self.weights, self.bias, &xs)` instead of the inline `z` accumulation.
- New `detect::tests::combine_matches_inline_sigmoid` (RED-first) asserts `combine` equals the hand-computed inline form and the bias-only midpoint (0.5). The existing `score_is_deterministic` and `single_layer_sigmoid_and_subscore` guards pass UNCHANGED — the refactor is behaviour-preserving.

## Verification

- `cargo test` full suite: **102 lib tests pass** (100 baseline + new round-trip + new combine test), 1 ignored, integration tests green.
- `cargo build`: linfa stack resolves cleanly, no duplicate-ndarray trait error.
- `cargo clippy --all-targets -- -D warnings`: clean.
- Acceptance greps: `CREATE TABLE IF NOT EXISTS backpropagation` = 1 hit; old `CREATE TABLE IF NOT EXISTS label ` = 0; `^ndarray = "0.16"` = 1; `ndarray = "0.17"` = 0; `combine(` in detect/mod.rs = 4.

## Deviations from Plan

None — plan executed exactly as written. No auto-fixes (Rules 1–3) were needed; no architectural decisions (Rule 4) arose. The supply-chain checkpoint (T-06-SC) did not trigger: all three packages resolved exactly as RESEARCH's Package Legitimacy Audit predicted (verdict OK, rust-ml / rust-ndarray orgs), so no human verification gate was required.

## Authentication Gates

None.

## Threat Surface

No new threat surface beyond the plan's threat model. T-06-01 (operator-entered labels trusted) and T-06-SC (linfa supply chain, audited OK in RESEARCH) are both as planned. No new network endpoints, auth paths, or trust-boundary schema changes introduced.

## Known Stubs

`review_queue` is created empty by design — Plan 03 populates it from a run's scored-but-unlabeled tail. This is the intended foundation seam (documented in the schema comment and the plan), not an accidental stub.

## Commits

- `71ab5ee` test(06-01): add backpropagation insert round-trip + rename test refs (RED)
- `36aee14` feat(06-01): rename label->backpropagation, add review_queue DDL (GREEN)
- `2eff4c2` test(06-01): add combine_matches_inline_sigmoid for shared combiner (RED)
- `e0183fc` feat(06-01): add linfa stack + extract shared detect::combine() (GREEN)

## Self-Check: PASSED

- `06-01-SUMMARY.md` exists.
- All four task commits (71ab5ee, 36aee14, 2eff4c2, e0183fc) present in git history.
