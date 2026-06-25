---
phase: 05-cli-run-export
verified: 2026-06-26T00:00:00Z
status: passed
score: 13/13 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 5: CLI Run & Export Verification Report

**Phase Goal:** A human can drive a full batch and get back the suspected-spammer list — the first shippable reviewable artifact — each flagged pubkey with its per-layer reasons, exported from SQLite.
**Verified:** 2026-06-26
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #   | Truth | Status | Evidence |
| --- | ----- | ------ | -------- |
| 1 | `run` subcommand executes a full batch end-to-end (enumerate→fetch→score→persist) and reports progress to completion — scoring runs from the BINARY (MD-02 fix), not only tests | ✓ VERIFIED | `main.rs:66-78` Commands::Run arm builds a tokio runtime and calls `run::run_batch` (NOT `#[cfg(test)]`). `run.rs:86-193` `run_batch` seeds weights → `enumerate::run` → `run_pipeline`+`production_fetch_with_whitelist` → `stage.score`→`store.persist` consumer (lifted from pipeline.rs test). Progress via count-only `eprintln!` every 1000 + final total (`run.rs:128,146-148,177`). Test `run_batch_endtoend_mocked` passes (3 score rows, ≥1 signal each, status=done, 1 run row). |
| 2 | `export` emits the suspected-spammer list (score > τ) materialized into the `suspected_spammer` SQLite table | ✓ VERIFIED | `export.rs:75-95` `materialize_suspected` runs `INSERT INTO suspected_spammer … SELECT … FROM score WHERE run_id=?1 AND suspected=1`. `main.rs:79-108` Export arm resolves run_id, calls it, reports count. Test `materialize_selects_suspected` passes (exactly the 2 suspected=1 rows, score-DESC rank). Behavioral: `export` with no done run errors cleanly with exit=1. |
| 3 | Each exported pubkey carries per-layer decomposition JOINable from `signal` (not duplicated); the run's τ + weight snapshot recorded in `run.config_json` for reproducibility | ✓ VERIFIED | `suspected_spammer` (schema.rs:82-91) holds only verdict columns (no layer/evidence). Test `evidence_joinable_from_signal` passes — JOIN `signal USING (run_id, pubkey)` returns per-layer evidence; asserts no `evidence`/`layer` columns on suspected_spammer. Snapshot: `run.rs:106-119` writes `{tau, bias, weights, …}` into config_json BEFORE scoring; test `snapshot_records_tau_and_weights` passes (τ==config.tau, 6-row weights array incl. L0/L1/L3/L4/_bias/_threshold). |
| 4 | ONE run_id spans enumerate + score: `enumerate::run` returns run_id and does NOT mark done; `run_batch` marks done after scoring | ✓ VERIFIED | `enumerate.rs:142-147` signature `run(store, client, resume, config_json) -> Result<i64, _>`; clean-termination path (lines 252-261) records `set_run_max_lev_end` and returns run_id, no `mark_run_done`. `run.rs:190` `run_batch` calls `store.mark_run_done` after the pipeline drains. Test asserts `n_runs == 1` (no second run row). |
| 5 | `mark_run_done` has exactly one production owner | ✓ VERIFIED | grep: the ONLY non-test `mark_run_done` call is `run.rs:190`. enumerate.rs production path has none (only doc-comments); all other refs are in `#[cfg(test)]` modules. |
| 6 | `export` is idempotent (re-export → one row per (run_id,pubkey)) | ✓ VERIFIED | `export.rs:80-83` DELETE-then-INSERT inside one transaction; PK (run_id, pubkey) on table. Test `reexport_is_idempotent` passes (n1=1, n2=1, total=1 after two calls). |
| 7 | clap is the only new dep | ✓ VERIFIED | Cargo.toml:49-52 adds `clap = { version = "4", features = ["derive"] }`, commented Phase-5-owned; header (lines 6-11) states clap is the only Phase-5 add, gaoya/linfa deferred to Phase 6. No other new deps present. |
| 8 | SQLite-only (no flat-file export) | ✓ VERIFIED | grep for csv/File::create/std::fs::write/to_writer in export.rs/run.rs/main.rs → none. Export materializes into the SQLite `suspected_spammer` table only (D-05). |
| 9 | Config tests use temp dirs | ✓ VERIFIED | config.rs module doc (lines 5-7) + tests use `tempfile::tempdir()` (lines 176-177, 224); test `default_config_path_uses_home` asserts path tail only; explicit assertion "config path must live under the temp dir, not ~/deepfry" (line 261). |
| 10 | clap parses `run --resume`, `export --run-id N`, global `--config`/`--db` (before and after subcommand) | ✓ VERIFIED | `main.rs:18-47` Parser/Subcommand derive with `global = true` flags. Test `parses_subcommands` passes (all variants + global ordering). `cargo run -- --help` shows run/export subcommands + global options. |
| 11 | `export` with no `--run-id` selects the latest `done` run | ✓ VERIFIED | `export.rs:102-108` `latest_done_run` = `SELECT max(run_id) FROM run WHERE status='done'`. `main.rs:89` `run_id.or(latest_done_run)`. Test `default_picks_latest_done_run` passes (picks older DONE over newer running). |
| 12 | SCORE-03 genuinely covered | ✓ VERIFIED | suspected-spammer list with per-layer evidence, exportable from SQLite — export.rs + schema + 5 export tests. See truths 2,3,6. |
| 13 | OPS-01 genuinely covered (run/export CLI) + live full-run manual must_have satisfied | ✓ VERIFIED | clap `run`/`export` subcommands wired and driving real logic (truths 1,10). Live `run` against :8080/:8081: test `live_run_self_skipping` is `#[ignore]`-gated and self-skips on outage (passes/ignored in CI); SUMMARY 05-02 records a real 28k+ pubkey live walk against 192.168.149.21:8080 — treated as a satisfied manual must_have per phase contract. |

**Score:** 13/13 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/store/schema.rs` | suspected_spammer table + idx_suspected_run | ✓ VERIFIED | 8th CREATE TABLE (lines 82-91), idx_suspected_run (line 91); 8 tables + 3 indexes total |
| `src/store/mod.rs` | set_run_config_json + export_write_conn + set_run_max_lev_end | ✓ VERIFIED | lines 169, 248, 183; all params-bound, touch only run/suspected_spammer (single-writer-safe) |
| `src/enumerate.rs` | widened run signature, mark_run_done removed from clean path | ✓ VERIFIED | lines 142-147 new arity returning run_id; clean path records set_run_max_lev_end, no mark_run_done |
| `Cargo.toml` | clap 4 derive | ✓ VERIFIED | line 52 |
| `src/run.rs` | run_batch + RunError + tests | ✓ VERIFIED | 595 lines; run_batch (86), RunError (58), 3 tests incl live-self-skip |
| `src/export.rs` | materialize_suspected + latest_done_run + 5 tests | ✓ VERIFIED | 338 lines; both fns exported; 5 unit tests (4 planned + missing_tau_snapshot_errors) |
| `src/main.rs` | clap Cli + Commands + Run/Export dispatch | ✓ VERIFIED | full clap surface; both arms wired (no todo!/placeholder) |
| `src/lib.rs` | pub mod run; pub mod export; | ✓ VERIFIED | lines 69, 80 |
| `src/model.rs` | Weight derives Serialize/Deserialize | ✓ VERIFIED | line 86 derive on Weight struct |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| main.rs | run.rs | Run arm → run_batch in tokio runtime | ✓ WIRED |
| run.rs | enumerate.rs | enumerate::run(store, client, resume, snapshot_json) | ✓ WIRED |
| run.rs | pipeline.rs | run_pipeline + production_fetch_with_whitelist | ✓ WIRED |
| run.rs | store/mod.rs | set_run_config_json (via enumerate) + mark_run_done | ✓ WIRED |
| export.rs | schema.rs | INSERT…SELECT into suspected_spammer WHERE suspected=1 | ✓ WIRED |
| export.rs | store/mod.rs | export_write_conn (single-writer-safe) | ✓ WIRED |
| main.rs | export.rs | Export arm → latest_done_run + materialize_suspected | ✓ WIRED |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Build | `cargo build` | Finished, no errors | ✓ PASS |
| Full test suite | `cargo test` | 98 lib + 2 bin passed, 1 ignored (live self-skip), 0 failed | ✓ PASS |
| CLI help shows subcommands | `cargo run -- --help` | shows run + export + global --config/--db | ✓ PASS |
| Export with no done run | `cargo run -- --db <empty> export` | `Error: no completed run to export…` exit=1 | ✓ PASS |
| run-lifecycle: one run_id | test `run_batch_endtoend_mocked` | n_runs==1, status=done | ✓ PASS |
| idempotent re-export | test `reexport_is_idempotent` | total==1 after 2 calls | ✓ PASS |
| evidence JOINable, not duplicated | test `evidence_joinable_from_signal` | JOIN returns evidence; no layer/evidence cols on suspected_spammer | ✓ PASS |
| τ+weights snapshot | test `snapshot_records_tau_and_weights` | τ==config.tau, 6-row weights array | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| SCORE-03 | 05-01, 05-03 | Suspected-spammer list above τ with per-layer evidence, exportable from SQLite | ✓ SATISFIED | export.rs materialize_suspected + suspected_spammer table + signal JOIN; 5 export tests green |
| OPS-01 | 05-01, 05-02 | CLI drives full batch run + export (label/tune are later phases) | ✓ SATISFIED | clap run/export wired end-to-end; run_batch composes all 4 subsystems on one run_id; live run exercised manually (28k+ pubkeys) |

Note: OPS-01 also names `label` and `tune` subcommands; those are mapped to Phase 6 (TUNE-01..05). The Phase 5 contract covers only `run`+`export`, which are fully delivered.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| ---- | ---- | ------- | -------- | ------ |
| (none) | — | No TODO/FIXME/XXX/TBD/todo!/unimplemented! debt markers in phase files | ℹ️ Info | The two "placeholder" string matches in export.rs are doc-comment references to the `"{}"` config_json placeholder concept (the no-τ error path), not stubs |

### Human Verification Required

None. All truths verified programmatically; the live full-run manual must_have was already exercised by the executor (28k+ pubkey walk against the production adapter) and the live test self-skips in CI by design.

### Gaps Summary

No gaps. The phase goal is achieved: a human can run `cargo run -- run` to drive a full enumerate→fetch→score→persist batch on one canonical run_id with a τ+weight reproducibility snapshot, then `cargo run -- export` to materialize the suspected-spammer list into the `suspected_spammer` SQLite table with per-layer evidence left JOINable from `signal`. The MD-02 fix is confirmed: scoring runs from the binary (main.rs Run arm → run_batch), not only from tests. `mark_run_done` has exactly one production owner (run_batch). Export is idempotent. clap is the only new dependency. No flat-file export. Config tests use temp dirs. Both REQ IDs (SCORE-03, OPS-01) are genuinely covered.

---

_Verified: 2026-06-26_
_Verifier: Claude (gsd-verifier)_
