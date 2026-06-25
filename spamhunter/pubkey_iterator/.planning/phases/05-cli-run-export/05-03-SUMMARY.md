---
phase: 05-cli-run-export
plan: 03
subsystem: export
tags: [rusqlite, sqlite, materialization, window-function, idempotent, clap, reproducibility]

# Dependency graph
requires:
  - phase: 05-01
    provides: "suspected_spammer table (PK (run_id,pubkey); score/tau/rank/exported_at) + idx_suspected_run; Store::export_write_conn (single-writer-safe short-lived write conn); score.suspected flag; run.config_json + run.status"
  - phase: 05-02
    provides: "run.config_json τ+weight snapshot (D-04/D-06); run lifecycle (begin_run/mark_run_done); the placeholder Export CLI arm in main.rs"
provides:
  - "export::materialize_suspected(conn, run_id) — idempotent INSERT…SELECT of score WHERE suspected=1 into suspected_spammer, τ + score-DESC rank stamped, returns the row count"
  - "export::latest_done_run(conn) — max(run_id) over status='done' (default export target)"
  - "Wired Export CLI arm: resolve --run-id or latest_done_run, materialize, report the count + reviewer JOIN hint"
affects: [06-* tuning/review (reads suspected_spammer JOIN signal for the labelled review set)]

# Tech tracking
tech-stack:
  added: []   # no new dep — rusqlite/serde_json already owned
  patterns:
    - "materialize-not-recompute: export reads the pre-computed score.suspected flag (point-in-time-correct per run, D-06) — no scoring at export time"
    - "idempotent DELETE-then-INSERT inside one transaction (re-export is safe; one row per (run_id,pubkey))"
    - "ROW_NUMBER() OVER (ORDER BY score DESC) for the review rank (SQLite window fn, bundled SQLite >> 3.25)"
    - "τ stamped inline from run.config_json; per-layer evidence NOT duplicated — stays JOINable from signal USING (run_id,pubkey) (D-05)"
    - "export arm stays synchronous (no tokio runtime — pure SQLite); only Run builds a runtime"

key-files:
  created:
    - src/export.rs
  modified:
    - src/lib.rs
    - src/main.rs

key-decisions:
  - "NO whitelist filter on the export predicate (Open Q2 resolved): a whitelisted pubkey can still be content-suspected, so the SELECT is suspected=1 only."
  - "A run with no τ snapshot (the '{}' placeholder of an aborted/enumerate-only run) ERRORS clearly ('no τ snapshot — did `run` complete?') rather than materializing against a bogus threshold (Pitfall 4) — surfaced as a non-zero CLI exit."
  - "Evidence is NOT copied into suspected_spammer (only the verdict columns pubkey/score/τ/rank); it stays in signal, JOINed at read time (D-05, T-05-12)."
  - "latest_done_run uses max(run_id) over status='done' (never plain max(run_id), which could be a half-finished run, Pitfall 4)."
  - "materialize_suspected runs on export_write_conn touching ONLY suspected_spammer — the actor's single-writer invariant for score/signal/pubkey is preserved (T-05-10)."

metrics:
  duration: ~18m
  completed: 2026-06-26
  tasks: 2
  files_created: 1
  files_modified: 2

status: complete
---

# Phase 5 Plan 03: Export Slice Summary

`export` materializes a completed run's `score WHERE suspected=1` rows into the
reviewable `suspected_spammer` table via one idempotent `INSERT…SELECT`, stamping
the run's snapshot τ and a score-DESC review rank, leaving per-layer evidence
JOINable from `signal` — completing the SCORE-03 CLI surface (SQLite-only, D-05).

## What Was Built

**Task 1 — `src/export.rs` (`materialize_suspected` + `latest_done_run`, TDD):**
- `materialize_suspected(conn, run_id) -> Result<usize>`: reads τ from the run's
  `config_json` snapshot (D-06), then inside one transaction `DELETE`s the run's
  prior `suspected_spammer` rows and re-`INSERT…SELECT`s from
  `score WHERE run_id=?1 AND suspected=1`, stamping τ (`?2`) and
  `ROW_NUMBER() OVER (ORDER BY score DESC)` as rank, with `exported_at` (`?3`).
  Returns the materialized count. Every value `params![]`-bound (T-05-09).
- `read_tau_from_run_snapshot` (private): parses `run.config_json.tau`; a run with
  no τ (the `"{}"` placeholder) returns a clear `rusqlite::Error` (Pitfall 4).
- `latest_done_run(conn) -> Result<Option<i64>>`: `max(run_id)` over
  `status='done'` (never a half-finished run, Pitfall 4).
- 5 unit tests (RED → GREEN): `materialize_selects_suspected`,
  `reexport_is_idempotent`, `evidence_joinable_from_signal`,
  `default_picks_latest_done_run`, plus `missing_tau_snapshot_errors` (Rule 2 —
  added the Pitfall-4 error-path test the plan's behavior block called for).
- `pub mod export;` declared in `src/lib.rs`.

**Task 2 — Export CLI arm (`src/main.rs`):**
- Replaced the non-zero-exit placeholder with real dispatch: open `Store`, open
  `export_write_conn`, resolve `run_id.or(latest_done_run)` (clear error when no
  `done` run), `materialize_suspected`, `eprintln!` the count + a one-line reviewer
  JOIN hint, then `store.close()`. Stays synchronous (no tokio runtime — only `Run`
  builds one).

## How It Works

`score.suspected` already records "score > τ at run time", so `export` is a pure
materialize of an already-computed flag — point-in-time-correct per run (the run
that produced the row used the τ snapshotted in its `config_json`). The verdict
columns (pubkey, score, τ, rank) are denormalized into `suspected_spammer` for
at-a-glance review; the per-layer reasons stay in `signal`, read via
`suspected_spammer s JOIN signal USING (run_id, pubkey)`.

## Verification

- `cargo test --lib export::tests` — 5 SCORE-03 unit tests green.
- `cargo test` — full suite green (98 lib + 2 doc/integration, 1 ignored live test).
- `cargo build` green; `cargo clippy --all-targets -- -D warnings` clean.
- Manual end-to-end (seeded temp DB via `sqlite3`):
  - `export` with no done run → `Error: "no completed run to export …"`, exit 1.
  - `export` (default) on a seeded `done` run → "exported 2 suspected pubkeys for
    run 1"; rows ranked 1/2 by score DESC (0.9, 0.7), τ=0.5 stamped, evidence
    JOINs from `signal`.
  - Re-running `export` → count stays 2 (idempotent).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical test] Added `missing_tau_snapshot_errors` test**
- **Found during:** Task 1
- **Issue:** The plan's `<action>` mandates erroring when a run has no τ snapshot
  (Pitfall 4), and the threat register treats the τ provenance as a correctness
  requirement (T-05-11), but the behavior block enumerated only 4 tests — none
  covering the no-τ error path the implementation guards.
- **Fix:** Added a 5th unit test asserting `materialize_suspected` returns a clear
  error (naming the missing τ snapshot) for a `"{}"`-config run.
- **Files modified:** src/export.rs
- **Commit:** b3cff1c

## Threat Surface Scan

No new security-relevant surface beyond the plan's `<threat_model>`. All export SQL
is `params![]`-bound (T-05-09); `export_write_conn` touches only `suspected_spammer`
(T-05-10); τ is stamped from the run snapshot (T-05-11); evidence is not duplicated
(T-05-12). No new network endpoints, auth paths, or trust-boundary schema changes.

## Known Stubs

None — both `materialize_suspected`/`latest_done_run` and the Export CLI arm are
fully wired and verified end-to-end.

## TDD Gate Compliance

RED gate (`test(05-03): add failing export…`, c1b39b7) precedes the GREEN gate
(`feat(05-03): implement materialize_suspected…`, b3cff1c). No REFACTOR commit was
needed.

## Self-Check: PASSED

All created/modified files exist on disk (src/export.rs, src/lib.rs, src/main.rs,
05-03-SUMMARY.md); all three task commits (c1b39b7, b3cff1c, efb2ad8) are present
in git history.
