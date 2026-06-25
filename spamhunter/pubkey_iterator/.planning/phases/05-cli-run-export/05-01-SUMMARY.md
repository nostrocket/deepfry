---
phase: 05-cli-run-export
plan: 01
subsystem: store + enumerate (Phase-5 foundation)
status: complete
tags: [schema, run-lifecycle, clap, single-writer, reproducibility]
requires:
  - "Phase-2 enumerate::run walk + store writer actor"
  - "Phase-4 run.config_json snapshot contract (D-04/D-06)"
provides:
  - "suspected_spammer materialization table (8th table)"
  - "Store::set_run_config_json (snapshot onto canonical run)"
  - "Store::export_write_conn (single-writer-safe materialization conn)"
  - "Store::set_run_max_lev_end (D-09 end-drift updater)"
  - "enumerate::run(store, client, resume, config_json) -> Result<i64, EnumerateError>"
affects:
  - "Plan 05-02 run_batch (owns mark_run_done; scores into the returned run_id)"
  - "Plan 05-03 export (INSERT…SELECT into suspected_spammer via export_write_conn)"
tech-stack:
  added:
    - "clap 4.6.1 (derive) — Phase-5-owned CLI dep"
  patterns:
    - "short-lived write conn touching one table (single-writer invariant preserved)"
    - "params!-bound SQL only (T-01-01/T-05-01)"
    - "run-lifecycle unification: enumerate yields run_id, caller owns done mark"
key-files:
  created: []
  modified:
    - Cargo.toml
    - src/store/schema.rs
    - src/store/mod.rs
    - src/enumerate.rs
    - src/main.rs
decisions:
  - "enumerate::run returns the run_id and no longer marks the run done; the scoring caller (05-02) owns mark_run_done — one run_id spans enumerate + score (RESEARCH Open Q1 / A5)."
  - "Added Store::set_run_max_lev_end (mirrors set_run_max_lev_start) so the end drift probe still persists from enumerate after mark_run_done moved out."
  - "suspected_spammer columns follow the PLAN's authoritative spec (run_id, pubkey, score, tau, rank, exported_at; PK (run_id, pubkey)), denormalizing τ + score inline; per-layer evidence stays in signal."
metrics:
  duration: "~25m"
  completed: "2026-06-26"
  tasks: 3
  files: 5
---

# Phase 05 Plan 01: CLI/run/export Foundation Summary

Added the single Phase-5 dependency (`clap` 4.6 derive), the `suspected_spammer`
materialization table (8th table, keyed `(run_id, pubkey)` with inline
`score`/`tau`/`rank`/`exported_at`), two single-writer-safe store helpers
(`set_run_config_json`, `export_write_conn`) plus `set_run_max_lev_end`, and
resolved the run-lifecycle unification by widening `enumerate::run` to accept a
`config_json` snapshot and return the `run_id` while moving `mark_run_done` out
of its clean-termination path.

## What Was Built

- **Task 1 (`6715bc6`)** — `clap = { version = "4", features = ["derive"] }`
  added (resolved as 4.6.1, MSRV 1.85 ≤ toolchain 1.96); dep-discipline header
  updated. `suspected_spammer` CREATE TABLE + `idx_suspected_run(run_id, rank)`
  appended to `SCHEMA_DDL` (now 8 tables + 3 indexes). The
  `open_creates_wal_and_schema` test asserts the 8th table.
- **Task 2 (`7ce1b01` RED, `a2583c9` GREEN)** — `Store::set_run_config_json`
  (params-bound `UPDATE run SET config_json` via the `run_write_conn` template),
  `Store::export_write_conn` (short-lived conn touching only `suspected_spammer`),
  and `Store::set_run_max_lev_end` (D-09 end-drift updater). Three unit tests:
  `set_run_config_json_roundtrip`, `export_write_conn_can_write_suspected`,
  `set_run_max_lev_end_roundtrip`.
- **Task 3 (`f9f24c9`)** — `enumerate::run` widened to
  `(store, client, resume, config_json) -> Result<i64, EnumerateError>`. The
  snapshot is passed to `begin_run` on fresh/`None` and refreshed via
  `set_run_config_json` on a continued resume run. The clean-termination path now
  records the end drift via `set_run_max_lev_end` and returns the `run_id`
  WITHOUT marking the run done; the BL-01 terminal flush barrier is kept and
  `mark_run_aborted` stays on the error path. `main.rs` updated to the new arity
  (interim `"{}"` + `mark_run_done(run_id, 0)`). All enumerate test call sites
  updated; done-asserting tests now mark done with the returned run_id; added
  `fresh_run_persists_config_json_snapshot`.

## Verification

- `cargo build` — green (clap present, main.rs on the new arity).
- `cargo test` — 91 passed, 0 failed (up from the 87 baseline: +4 new tests).
- `cargo clippy --all-targets -- -D warnings` — clean.

## Deviations from Plan

None — plan executed as written. One judgment note: `set_run_max_lev_end` (which
the plan describes under Task 3) was implemented + unit-tested in the Task 2
commit since it is a store helper mirroring `set_run_max_lev_start`; this keeps
all store-layer additions in one GREEN commit and Task 3 consumes it. No behavior
or scope change.

## Authentication Gates

None.

## Known Stubs

The `main.rs` `mark_run_done(run_id, 0)` placeholder end value and the `"{}"`
config_json are intentional interim shims — Plan 05-02 replaces `main.rs`
wholesale with the clap `run`/`export` surface and supplies the real τ + weight
snapshot and end value. Documented in the `main.rs` comment and acknowledged in
the plan's Task 3 action (step 4).

## Self-Check: PASSED

- src/store/schema.rs — FOUND (suspected_spammer + idx_suspected_run)
- src/store/mod.rs — FOUND (set_run_config_json, export_write_conn, set_run_max_lev_end)
- src/enumerate.rs — FOUND (run widened, returns run_id, mark_run_done removed)
- Cargo.toml — FOUND (clap)
- Commits 6715bc6, 7ce1b01, a2583c9, f9f24c9 — all in git log
