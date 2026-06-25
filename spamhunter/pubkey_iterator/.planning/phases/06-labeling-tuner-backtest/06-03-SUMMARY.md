---
phase: 06-labeling-tuner-backtest
plan: 03
subsystem: ml-tuning
tags: [negative-sampling, review-queue, reproducibility, sqlite, fnv1a, deterministic, rust]
status: complete

# Dependency graph
requires:
  - phase: 06-01
    provides: "review_queue table (run_id, pubkey, score, sampled_at; PK (run_id, pubkey)), weight table + tuned_at/tuned_from_run provenance, weight_write_conn, score table (suspected flag)"
  - phase: 06-02
    provides: "src/tune.rs run_tune (run_id resolution, fit→backtest→write pipeline), [tune].review_sample_size config field (default 100)"
  - phase: 05
    provides: "src/run.rs run_batch — reads live weights at startup + snapshots τ + weight set into run.config_json BEFORE scoring (the TUNE-03 mechanism)"
provides:
  - "src/tune.rs populate_review_queue: deterministic FNV-1a-hash-ordered sample of UNFLAGGED (suspected=0) pubkeys into review_queue, capped at review_sample_size, idempotent DELETE+INSERT (TUNE-04 / D-05)"
  - "src/store/mod.rs Store::review_queue_write_conn: short-lived non-actor write conn for review_queue (single-writer invariant preserved)"
  - "src/run.rs retuned_weights_appear_in_next_run_snapshot: confirming test that a retuned weight set propagates into the next run's config_json snapshot (TUNE-03 / D-04 traceability)"
affects: [review-queue, negative-sampling, labeling, reproducibility]

# Tech tracking
tech-stack:
  added: []  # zero new deps — FNV-1a hashing is hand-rolled (mirrors detect::near_duplicate)
  patterns:
    - "Deterministic no-RNG sampling: order candidates by a stable FNV-1a hash of the key (tie-broken by the key for total order), take the first K — reproducible across runs without a rand dependency (D-08/OPS-02)"
    - "Short-lived non-actor write conn for a table outside the writer actor's set (review_queue), mirroring weight_write_conn / export_write_conn — preserves the single-writer invariant"
    - "Idempotent DELETE-then-INSERT in one transaction for per-run materialization (mirrors export::materialize_suspected)"
    - "Confirming test for already-implemented behaviour: simulate an adopted retune via weight_write_conn, then assert the next run's snapshot consumes it — guards run-to-weight traceability without a production change"

key-files:
  created: []
  modified:
    - "src/tune.rs"
    - "src/run.rs"
    - "src/store/mod.rs"

key-decisions:
  - "Used FNV-1a (the codebase's existing deterministic feature hash in detect::near_duplicate) as the stable ordering key, NOT xxh3 — the schema comment says xxh3 but the actual content-hash impl is fnv1a64. FNV-1a is zero-dep, deterministic, and already the project's sanctioned non-crypto hash (ASVS V6), so it keeps the no-rand-dependency invariant cleanly."
  - "populate_review_queue runs in run_tune AFTER run_id resolution but BEFORE the fit/backtest, so it executes regardless of adopt/block/precondition outcome — the review queue is for the NEXT labeling round (countering selection bias), not a gate on adoption."
  - "Tie-break the hash order by the pubkey string so the total order is fully reproducible even on FNV-1a collisions or SELECT row-order changes."
  - "TUNE-03 needed no production change — run_batch already reads live weights at startup and seed_weights_if_empty declines to overwrite stored rows, so an adopted retune survives into the next snapshot. Added a NEW confirming test rather than extending snapshot_records_tau_and_weights, keeping the Plan-01 refactor guard unchanged."

metrics:
  duration: "~20 min"
  completed: 2026-06-26
  tasks: 2
  files_changed: 3
  tests_added: 2
  tests_total: 110  # lib tests (was 108)
---

# Phase 6 Plan 03: Negative-Sampling review_queue + TUNE-03 Confirming Test Summary

Completes the correctability loop's honesty + traceability: `run_tune` now populates `review_queue` with a deterministic, no-RNG sample of UNFLAGGED pubkeys (TUNE-04/D-05 negative sampling — counters the "everything is spam" selection bias), and a confirming test proves a retuned weight set propagates into the next run's `config_json` snapshot (TUNE-03/D-04 run-to-weight traceability). This is the FINAL plan of the milestone.

## What Was Built

### Task 1 — review_queue negative sampling (TUNE-04 / D-05)
- `Store::review_queue_write_conn()` — a short-lived `review_queue`-only write connection mirroring `weight_write_conn` / `export_write_conn`. Touches only `review_queue` (never the writer actor's `score`/`signal`/`pubkey` tables), so the single-writer invariant holds (T-06-06).
- `populate_review_queue(store, reader, run_id, sample_size)` in `tune.rs`:
  1. `SELECT pubkey, score FROM score WHERE run_id = ?1 AND suspected = 0` (binds only `run_id`).
  2. Sort by a stable `fnv1a64(pubkey)` hash, tie-broken by the pubkey string, then `truncate(sample_size)` — deterministic, no `rand` dep.
  3. Idempotent `DELETE FROM review_queue WHERE run_id = ?1` then per-row `INSERT INTO review_queue (...)` in one transaction.
- Wired into `run_tune` after run_id resolution, before the fit — runs on every `tune` invocation regardless of adopt/block outcome (the queue feeds the *next* labeling round).
- Test `review_queue_samples_unflagged_deterministically` (RED→GREEN): seeds 4 unflagged + 2 flagged score rows with `review_sample_size = 2`; asserts the queue holds exactly 2 rows, every sampled pubkey has `suspected = 0`, and a second `run_tune` yields the identical set.

### Task 2 — TUNE-03 confirming test (D-04 traceability)
- `retuned_weights_appear_in_next_run_snapshot` in `run.rs`: runs `run_batch` once (seeds weights), simulates an adopted retune by `UPDATE`ing `L1_near_duplicate`'s weight to sentinel `7.5` on `weight_write_conn` (the exact write `run_tune` performs on a backtest PASS), then runs `run_batch` again and asserts the second run's `config_json` snapshot carries the retuned `7.5` for L1 — proving the run consumed the live retuned weights.
- No production change: `run_batch` already reads live weights at startup and `seed_weights_if_empty` leaves stored rows intact. The existing `snapshot_records_tau_and_weights` Plan-01 guard is unchanged and still passes.

## Deviations from Plan

**Hash choice — FNV-1a instead of xxh3.** The plan and `06-RESEARCH.md` §Pattern 3 reference "the existing xxh3/xxhash the fingerprint module already uses." The codebase has no xxh3 dependency or call site — `schema.rs` comments say `xxh3` but the actual deterministic content/feature hash is `fnv1a64` (`src/detect/near_duplicate.rs`). I reused that same FNV-1a algorithm (hand-rolled, zero-dep) as the stable ordering key. This is a documentation/naming discrepancy in upstream artifacts, not a behaviour change: FNV-1a satisfies every stated requirement (deterministic, no `rand`, project-sanctioned non-crypto hash). Tracked here rather than as a code deviation since no plan-mandated dependency exists to deviate from.

No auto-fixed bugs (Rules 1-3) and no architectural changes (Rule 4) were needed.

## Verification

- `cargo test --lib tune::tests::review_queue_samples_unflagged_deterministically` — PASS
- `cargo test --lib run::tests::retuned_weights_appear_in_next_run_snapshot` — PASS
- `cargo test --lib run::tests::snapshot_records_tau_and_weights` — PASS (unchanged)
- `cargo test` — 110 lib + 2 main tests PASS, 1 ignored (live, self-skipping); 0 failed (was 108 lib, +2)
- `cargo build` — clean
- `cargo clippy --all-targets -- -D warnings` — clean
- `grep -c 'review_queue' src/tune.rs` = 18 (≥ 1); `grep -c 'rand::' src/tune.rs` = 0; no `format!` inside any SQL string

## Threat Mitigations Applied

| Threat ID | Mitigation |
|-----------|------------|
| T-06-06 (Tampering, review_queue SQL) | All review_queue SELECT/DELETE/INSERT binds are `params![]`/`?N`; nothing `format!`-interpolated. |
| T-06-07 (Repudiation, weight snapshot) | Confirming test guards that each run snapshots the exact weight set it scored with (TUNE-03/D-04). |
| T-06-08 (Tampering, training-set integrity) | review_queue injects real negatives (unflagged pubkeys) so the logistic fit isn't trained only on flagged pubkeys (Pitfall 2). |

## Known Stubs

None.

## Self-Check: PASSED
