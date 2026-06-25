---
phase: 01-persistence-foundation
plan: 01
subsystem: persistence
status: complete
tags: [sqlite, rusqlite, wal, single-writer, upsert, eav, tdd]
dependency_graph:
  requires: []
  provides:
    - "pubkey_iterator crate (lib) — persistence foundation"
    - "Store::open / begin_run / persist / close / reader public API"
    - "SCHEMA_DDL: 7-table schema (run, pubkey, score, signal EAV, fingerprint, label, weight) + idx_signal_layer, idx_fp_chash"
    - "writer_loop single-writer batched-transaction actor + UPSERT_SCORE/UPSERT_SIGNAL/UPSERT_PUBKEY consts"
    - "read_scores / read_signals deterministic read helpers"
    - "model structs: Run, Score, SubScore, Fingerprint, Label, Weight, Persist"
  affects:
    - "Phase 2 (enumeration) — fills run.max_lev_id_*/last_cursor, persists pubkeys"
    - "Phase 3 (fetch pipeline) — feeds Persist over the writer channel"
    - "Phase 4 (detection layers) — persists per-layer signals + evidence"
    - "Phase 5 (CLI/export) — reads via queries.rs"
    - "Phase 6 (tuner) — reads/writes label + weight tables"
    - "Phase 7 (clustering) — fills fingerprint table"
tech_stack:
  added:
    - "rusqlite 0.40 (bundled SQLite)"
    - "serde 1.0 (derive) + serde_json 1.0"
    - "flume 0.12 (analyze->writer channel)"
    - "tempfile 3.27 (dev-dependency, temp-FILE test substrate)"
  patterns:
    - "PRAGMA-first open: WAL + synchronous=NORMAL + foreign_keys=ON + temp_store=MEMORY + busy_timeout, THEN execute_batch(SCHEMA_DDL)"
    - "Idempotent UPSERT via INSERT ... ON CONFLICT(pk) DO UPDATE SET col = excluded.col"
    - "Single-writer actor: one owned Connection on one thread, batched transactions, prepare_cached"
    - "EAV signal table: new layer = new row, zero schema migration"
    - "Parameterized SQL only (params!/?N) — no format! interpolation (T-01-01)"
key_files:
  created:
    - "Cargo.toml"
    - ".gitignore"
    - "src/lib.rs"
    - "src/model.rs"
    - "src/store/mod.rs"
    - "src/store/schema.rs"
    - "src/store/writer.rs"
    - "src/store/queries.rs"
  modified: []
decisions:
  - "Used flume::unbounded for the analyze->writer channel (RESEARCH 'Claude's Discretion'); the channel closing on Sender drop is what makes close() flush the final batch."
  - "Embedded SCHEMA_DDL string with CREATE TABLE IF NOT EXISTS (not rusqlite_migration) for v1 — greenfield single schema version; adopt migration lib at first ALTER TABLE."
  - "begin_run opens a short-lived write connection for the single run-row INSERT (FK parent must exist before score/signal); all other writes funnel through the actor."
  - "Added a Drop impl on Store as best-effort flush if close() is never called."
metrics:
  duration_min: 16
  completed: 2026-06-25
  tasks: 3
  files_created: 8
  tests: 6
---

# Phase 1 Plan 01: Persistence Foundation Summary

A single SQLite store (WAL, `synchronous=NORMAL`) with a 7-table schema and a single-writer, batched-transaction, idempotent-UPSERT API that every later phase persists through — proven by a 6-test contract covering WAL+schema creation, idempotency (within and across batch boundaries), zero-migration EAV layers, round-trip identity, and deterministic re-runs.

## What Was Built

- **`pubkey_iterator` library crate** (edition 2021) with the Phase-1 dependency subset only — `rusqlite` (bundled), `serde`, `serde_json`, `flume`, and `tempfile` (dev).
- **`src/model.rs`** — row-mapped structs (`Run`, `Score`, `SubScore`, `Fingerprint`, `Label`, `Weight`) plus the `Persist` writer-channel payload, all deriving `Debug, Clone, PartialEq` (serde only on the JSON-carrying structs).
- **`src/store/schema.rs`** — `SCHEMA_DDL` with the 7 `CREATE TABLE IF NOT EXISTS` (run, pubkey, score, signal EAV, fingerprint, label, weight) plus `idx_signal_layer` and `idx_fp_chash`, lifted verbatim from RESEARCH.
- **`src/store/mod.rs`** — `Store::open` (PRAGMA-first bootstrap then schema then writer-thread spawn), `begin_run`, `persist`, `close` (drops sender → drains final batch → joins writer), `reader`, plus a `Drop` best-effort flush and the 6 `#[cfg(test)]` tests.
- **`src/store/writer.rs`** — `writer_loop` single-writer batched-transaction actor + the 3 UPSERT consts using `ON CONFLICT DO UPDATE` / `DO NOTHING`.
- **`src/store/queries.rs`** — `read_scores` / `read_signals` with deterministic `ORDER BY`.

## Task Commits

| Task | Name | Commit | Gate |
|------|------|--------|------|
| 1 | Scaffold crate + failing test contract | `72d85c8` | RED (5 tests fail via todo!()) |
| 2 | Implement schema, WAL open, writer actor, reads | `2933af1` | GREEN (5 tests pass) |
| 3 | Harden idempotency across batches + clippy clean | `5812cd4` | 6 tests pass, clippy clean |

## Success Criteria — Verified

1. `Store::open(path)` creates a fresh WAL DB with all 7 tables — `open_creates_wal_and_schema` (asserts `journal_mode == "wal"`, `-wal` sidecar exists, all 7 tables in `sqlite_master`).
2. Double-writing `(run_id, pubkey)` and `(run_id, pubkey, layer)` leaves exactly one row each, second value wins — `upsert_is_idempotent` (same batch) **and** `upsert_idempotent_across_batches` (separate committed transactions across a close/reopen boundary).
3. A `signal` with a brand-new `layer` name persists with zero migration — `new_layer_no_migration`.
4. A synthetic batch reads back identically and re-runs deterministically — `batch_roundtrip_identity` + `rerun_is_deterministic`.

`cargo build` succeeds, `cargo test` reports 6/6 green, `cargo clippy --all-targets` is clean. SCORE-02 satisfied.

## TDD Gate Compliance

RED gate (`test(01-01)` commit `72d85c8`) → GREEN gate (`feat(01-01)` commit `2933af1`) → harden (`test(01-01)` commit `5812cd4`). Gate sequence intact: the 5 tests verifiably failed before implementation and passed after.

## Threat Mitigations Applied

- **T-01-01 (SQL injection / Tampering):** every write and read binds via `params!` / `?N`; no pubkey, layer, or value is `format!`-interpolated into SQL. Grep of `src/` confirms no `format!` near SQL.
- **T-01-02 (Information disclosure):** schema stores only pubkeys, numeric scores, and layer evidence summaries — no `content`/`raw` columns (deepfry data-separation rule).
- **T-01-03 (durability) / T-01-04 (network FS) / T-01-05 (file perms):** accepted-and-documented on `Store::open` / `bootstrap` (WAL+NORMAL crash semantics; local-filesystem requirement; default umask).

## Deviations from Plan

None for Rules 1–4. Two test-quality adjustments made within scope while satisfying the phase clippy gate (Task 3):
- `writer_loop` rewritten from `loop { match rx.recv() ... }` to `while let Ok(first) = rx.recv()` to clear clippy `while_let_loop`. Behavior identical (loop still exits when the channel closes).
- The `rerun_is_deterministic` closure's return tuple was factored into a `RunTables` type alias to clear clippy `type_complexity`.
Both are test/style-only and change no production behavior.

## Known Stubs

None. The `fingerprint`, `label`, and `weight` tables are created but not yet written/read — this is intentional per the plan (those columns are created here; Phases 6/7 populate them). No stub blocks the Phase-1 goal (write-then-read of scores + signals), which is fully exercised.

## Self-Check: PASSED

- Files exist: Cargo.toml, src/lib.rs, src/model.rs, src/store/{mod,schema,writer,queries}.rs — all present.
- Commits exist: `72d85c8`, `2933af1`, `5812cd4` — all in git log.
- `cargo test` 6/6 green; `cargo clippy --all-targets` 0 warnings/errors.
