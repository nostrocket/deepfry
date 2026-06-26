---
status: clean
phase: 06-labeling-tuner-backtest
reviewed: 2026-06-26
reviewer: inline (orchestrator — subagents unavailable, org spend limit)
findings: 0 blocker, 0 high, 0 medium, 0 low
---

# Phase 6 — Code Review

**Verdict: clean.** Inline review of the Phase-6 source changes (src/tune.rs, src/store/schema.rs rename + review_queue, src/detect/mod.rs `combine()` extraction, src/store/mod.rs write-conn accessors, src/main.rs Tune arm, Cargo.toml). Reviewed by the orchestrator directly because the org monthly spend limit blocked the gsd-code-reviewer subagent.

## Highest-value invariants (attacked, both hold)

- **Backtest gate no-op-on-block:** `run_tune` calls `write_weights` only inside the `Ok(())` arm of `backtest`; `BlockedByRegression`/`BlockedByPrecondition` return before any write. `write_weights` itself is a single `conn.transaction()` over the LAYERS + `_bias` rows (atomic — no half-update). Verified by `backtest_blocks_regression_and_leaves_weights` (all rows keep `tuned_at = NULL`).
- **Fit sign-correctness:** the `pos_is_spam` canonicalization negates weights+bias when linfa's positive class isn't spam, so `(weights, bias)` predict spam and match `detect::combine` (== P(spam)). Without it a retune could write sign-inverted weights — the comment and code are correct.

## Other checks

- Determinism: fixed `LAYERS` column order, no RNG, fixed hyperparameters; backtest uses the shared `combine` (no divergent reimplementation).
- review_queue: deterministic FNV-1a ordering with pubkey tiebreak (total order), idempotent DELETE+INSERT, `suspected=0` only — no `rand::` anywhere.
- SQL: every statement parameterized (`params![]`/`?N`); `format!` used only for human-facing summary strings, never SQL.
- Single-writer invariant: `weight_write_conn` / `review_queue_write_conn` are short-lived connections touching only their own tables.
- ndarray pinned to 0.16 (not 0.17) so linfa types match; `tune` is sync (no tokio), matching `export`.
- No panics on data input (the lone `.expect` is on a map key guaranteed present; time `unwrap_or(0)`).

## Accepted documentation nit (non-actionable)

- A SUMMARY/RESEARCH comment references "xxh3" where the implementation uses FNV-1a (the codebase's sanctioned zero-dep hash). Harmless doc discrepancy; the code is correct and dependency-free. No fix required.
