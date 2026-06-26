---
status: passed
phase: 06-labeling-tuner-backtest
verified: 2026-06-26
verifier: inline (orchestrator — subagents unavailable, org spend limit)
score: 4/4 success criteria, 5/5 requirements
requirement_ids: [TUNE-01, TUNE-02, TUNE-03, TUNE-04, TUNE-05]
---

# Phase 6 — Verification

**Status: PASSED.** All 4 ROADMAP success criteria verified TRUE against the actual codebase (src/tune.rs, src/store/schema.rs, src/detect/mod.rs, src/run.rs read directly); `cargo test` 110 lib + 2 bin pass (1 ignored live test), `cargo build` clean, `cargo clippy --all-targets -- -D warnings` clean.

> Note: this phase was verified by the orchestrator inline because the org monthly spend limit blocked spawning the gsd-verifier and gsd-code-reviewer subagents. The checks below were performed by reading the source and running the gates directly.

## Success Criteria

| # | Criterion | Evidence | Verdict |
|---|-----------|----------|---------|
| 1 | Humans record labels in SQLite; review queue includes a random sample of UNFLAGGED pubkeys (negative sampling) | `backpropagation` table (renamed from `label`, schema.rs:75) — pubkey/is_spam/labeled_at(+source,note); `backpropagation_insert_roundtrip` proves direct INSERT. `review_queue` (schema.rs:105) populated by `populate_review_queue` with `WHERE suspected=0`, FNV-1a-ordered, capped; `review_queue_samples_unflagged_deterministically` proves unflagged-only + deterministic. | TRUE |
| 2 | `tune` fits linfa-logistic over `signal × backpropagation` and writes weights+bias to `weight` with provenance | `tune::run_tune` → `labeled_features` (join) → `fit` (linfa `LogisticRegression`) → `write_weights` UPDATEs LAYERS+`_bias` with `tuned_at`/`tuned_from_run`. `tune_writes_weights_with_provenance` asserts provenance set. | TRUE |
| 3 | Each run reads latest weights at startup and snapshots them into run metadata | run_batch (Phase 5) snapshots τ+weights into `run.config_json`; the TUNE-03 confirming test proves a retuned weight set propagates into the next run's snapshot. | TRUE |
| 4 | New weights backtested against the full labeled set before adoption; regression BLOCKS adoption | `backtest` re-scores via the SHARED `detect::combine` (identical to live scoring), STRICT (any new FN OR FP → Err). On `BlockedByRegression`, `write_weights` is NOT called — no-op on live weights. `backtest_blocks_regression_and_leaves_weights` asserts all weight rows keep `tuned_at = NULL` after a block; `backtest_passes_and_adopts` proves the pass path. | TRUE |

## Invariants checked in code

- **Deterministic fit (OPS-02):** L-BFGS + zero-init, no RNG, fixed hyperparameters, fixed feature-column order (`LAYERS`); `fit_is_deterministic` asserts byte-identical weights across two fits.
- **Sign-correctness:** weights canonicalized toward the spam class (`negate unless labels().pos.class == 1`) so `combine` == P(spam); prevents sign-inverted verdicts. Exercised by the adopt/backtest tests.
- **Guards:** single-class labels → `BlockedByPrecondition` (no write); sparse signals → 0.0 feature (`sparse_signals_default_to_zero`).
- **τ fixed (Open Q1):** `write_weights` never touches `_threshold`; the provenance test asserts `_threshold` keeps `tuned_at = NULL` and value 0.5.
- **CLAUDE.md / security:** all SQL parameterized (`params![]`, no `format!` in SQL); single-writer invariant preserved (`weight_write_conn` / `review_queue_write_conn` are short-lived conns touching only their own tables); SQLite-only; only Phase-6 deps added (linfa 0.8 / linfa-logistic 0.8 / ndarray 0.16 — pinned, not 0.17); no `label` CLI subcommand (intentional OPS-01 deviation — labels are direct SQL inserts); no enforcement.

## Requirement traceability

TUNE-01 ✓ (backpropagation + review_queue), TUNE-02 ✓ (fit + provenance), TUNE-03 ✓ (run snapshot propagation), TUNE-04 ✓ (deterministic negative sampling), TUNE-05 ✓ (strict backtest gate, no-op on block). All five appear in plan `requirements` and have implementing code + tests.

## Manual / deferred

- A real-label tune+backtest cycle (humans INSERT into `backpropagation`, run `tune`) is the manual must_have — synthetic fixtures fully exercise the logic in CI.
