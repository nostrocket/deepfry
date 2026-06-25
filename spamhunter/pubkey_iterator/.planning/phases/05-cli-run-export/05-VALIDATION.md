---
phase: 5
slug: cli-run-export
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-26
---

# Phase 5 — Validation Strategy

> Per-phase validation contract. Source: 05-RESEARCH.md §Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust built-in `#[test]` + `tempfile` (temp-FILE DBs) |
| **Config file** | none — `cargo test` |
| **Quick run command** | `cargo test --lib export` (+ run-module tests) |
| **Full suite command** | `cargo test` |
| **Estimated runtime** | ~12s mocked; live `run` adds network round-trips |

Reuse the established idiom: loopback `TcpListener` HTTP stubs (`StubServer`, `omitting_stub`, `whitelist_stub`) + `tempfile::TempDir` temp-FILE SQLite. No new test infra.

---

## Sampling Rate

- **After every task commit:** `cargo test --lib <module>::tests` (touched module, <5s)
- **After every wave:** `cargo test` (full suite)
- **Phase gate:** `cargo test` green + a manual live `run` against `:8080`/`:8081` producing a non-empty `suspected_spammer` (self-skip degrades to deferred)
- **Max feedback latency:** ~12s

---

## Phase Requirements → Test Map

| Req | Behavior | Test Type | Automated Command | File Exists |
|--------|----------|-----------|-------------------|-------------|
| SCORE-03 export | `export` materializes `suspected=1` pubkeys into `suspected_spammer`, keyed run_id, stamped τ | unit | `cargo test --lib export::tests::materialize_selects_suspected` | ❌ W0 |
| SCORE-03 idempotent | re-export of a run → one row per `(run_id,pubkey)` | unit | `cargo test --lib export::tests::reexport_is_idempotent` | ❌ W0 |
| SCORE-03 evidence | `suspected_spammer` row JOINs to its `signal` evidence (no duplication) | unit | `cargo test --lib export::tests::evidence_joinable_from_signal` | ❌ W0 |
| SCORE-03 selection | `export` with no `--run-id` picks the latest `done` run | unit | `cargo test --lib export::tests::default_picks_latest_done_run` | ❌ W0 |
| OPS-01 run e2e | `run` enumerates→fetches→scores→persists `score`+`signal`, marks `done`, snapshot in `config_json` | integration | `cargo test --lib run::tests::run_batch_endtoend_mocked` | ❌ W0 |
| OPS-01 CLI parse | clap parses `run --resume`, `export --run-id N`, global `--config`/`--db` | unit | `cargo test --lib cli::tests::parses_subcommands` | ❌ W0 |
| OPS-01 repro | `run.config_json` for the scored run contains τ + weight set | unit | `cargo test --lib run::tests::snapshot_records_tau_and_weights` | ❌ W0 |
| OPS-01 live | live full `run` against `:8080`+`:8081` scores ≥1 pubkey; self-skips on outage | integration (self-skip) | `cargo test --lib run::tests::live_run_self_skipping` | ❌ W0 |

---

## Wave 0 Requirements

- [ ] `src/export.rs` — module + `export::tests` (4 unit tests)
- [ ] `src/run.rs` — module + `run::tests` (e2e mocked, snapshot, live-self-skipping)
- [ ] `src/main.rs` — clap `Cli` + `cli::tests` (`Cli::try_parse_from`)
- [ ] `store/schema.rs` — add `suspected_spammer` table (+ index) to `SCHEMA_DDL`; extend `open_creates_wal_and_schema` to assert the new table
- [ ] run-lifecycle unification (Open Q1 / A5): one run_id across enumerate+score — the e2e test pins the contract
- [ ] framework install: none — `#[test]` + `tempfile` present; add `clap` 4.6 (toolchain 1.96 ≥ MSRV 1.85)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live full `run` → non-empty `suspected_spammer` | OPS-01 / SCORE-03 | Requires live adapter `:8080` + whitelist `:8081` | Run `run` against the live services, confirm it completes, scores ≥1 pubkey, and `export` materializes a non-empty `suspected_spammer` with τ + weight snapshot recorded. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] run-lifecycle unification pinned by the e2e test
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
