---
phase: 01-persistence-foundation
verified: 2026-06-25T00:00:00Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 1: Persistence Foundation Verification Report

**Phase Goal:** A single SQLite store with the full schema and an idempotent single-writer API exists, so every later stage has somewhere to persist runs, per-pubkey scores, per-layer signals, labels, and weights.
**Verified:** 2026-06-25
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | `Store::open(path)` creates a fresh SQLite file in WAL mode with all 7 tables (run, pubkey, score, signal, fingerprint, label, weight) | ✓ VERIFIED | `open_creates_wal_and_schema` PASSED — asserts `journal_mode=="wal"`, `-wal` sidecar exists, all 7 tables present in `sqlite_master`. Ran live: test green. Schema DDL has exactly 7 `CREATE TABLE IF NOT EXISTS` (mod.rs:45 `execute_batch(SCHEMA_DDL)` after PRAGMAs). |
| 2 | Writing the same `(run_id, pubkey)` and `(run_id, pubkey, layer)` twice leaves exactly one row each — idempotent UPSERT, incl. across batch boundaries | ✓ VERIFIED | Behavior-dependent (state transition / dedup invariant). Two passing tests: `upsert_is_idempotent` (same batch) and `upsert_idempotent_across_batches` (forces two SEPARATE committed transactions via close→reopen, asserts `count(*)==1` and second value wins). Backed by `ON CONFLICT(...) DO UPDATE SET col=excluded.col` (writer.rs UPSERT_SCORE/UPSERT_SIGNAL). |
| 3 | A new detection layer records a sub-score by inserting a signal row with a new layer name without any schema migration (EAV) | ✓ VERIFIED | `new_layer_no_migration` PASSED — persists `layer="L99_brand_new"`, reads it back via `read_signals`, NO DDL executed between insert and read. Schema `signal` table is EAV `(run_id,pubkey,layer)` PK. |
| 4 | A developer can persist a batch of synthetic scores through the single writer and read them back identically | ✓ VERIFIED | Behavior-dependent (round-trip identity). `batch_roundtrip_identity` PASSED — persists 3 synthetic `Persist` records through the single `writer_loop`, reads scores + signals back via queries.rs, asserts value + structural equality. |
| 5 | A re-run is deterministic (same synthetic batch → row-set + value-equal tables) | ✓ VERIFIED | Behavior-dependent (ordering invariant). `rerun_is_deterministic` PASSED — same batch into two fresh DBs yields equal score+signal tables. Subscores iterated in fixed `Vec` order (writer.rs:73); reads `ORDER BY` key columns (queries.rs). No HashMap/HashSet anywhere in src/. |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

All behavior-dependent truths (idempotency, round-trip, determinism) have passing behavioral tests exercising the actual transition/invariant — not merely symbol presence.

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `Cargo.toml` | rusqlite bundled + serde + serde_json + flume + tempfile dev-dep | ✓ VERIFIED | Exactly the 5 Phase-1 deps; no tokio/rayon/reqwest/clap. `rusqlite = {version="0.40", features=["bundled"]}`. |
| `src/lib.rs` | `pub mod model` + `pub mod store` | ✓ VERIFIED | Both declared with doc comments (lib.rs:8,15). |
| `src/model.rs` | Run, Score, SubScore, Fingerprint, Label, Weight, Persist | ✓ VERIFIED | All 7 structs present, derive Debug/Clone/PartialEq; `pub struct Persist` (model.rs:96). |
| `src/store/schema.rs` | SCHEMA_DDL: 7 tables + 2 indexes | ✓ VERIFIED | `pub const SCHEMA_DDL`; 7 CREATE TABLE + idx_signal_layer + idx_fp_chash. |
| `src/store/mod.rs` | `Store::open` + PRAGMA bootstrap + writer spawn + 5-test contract | ✓ VERIFIED | `pub fn open` (mod.rs:70); PRAGMA-first `bootstrap()`; spawns `writer_loop`; 6 tests (5-test contract + across-batch hardening). |
| `src/store/writer.rs` | `writer_loop` + 3 UPSERT consts | ✓ VERIFIED | `writer_loop` (writer.rs:45); UPSERT_SCORE/UPSERT_SIGNAL/UPSERT_PUBKEY; BATCH=8192; 3 `ON CONFLICT`. |
| `src/store/queries.rs` | `read_scores` + `read_signals` with deterministic ORDER BY | ✓ VERIFIED | Both present with `ORDER BY pubkey` / `ORDER BY pubkey, layer`; parameterized. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| store/mod.rs | store/schema.rs | `execute_batch(SCHEMA_DDL)` after PRAGMAs | ✓ WIRED | mod.rs:45. |
| store/writer.rs | store/schema.rs | UPSERTs via ON CONFLICT against schema tables | ✓ WIRED | 3 `ON CONFLICT` clauses targeting score/signal/pubkey. |
| store/mod.rs | store/writer.rs | `Store::open` spawns writer thread holding the only Connection | ✓ WIRED | mod.rs:74 `std::thread::spawn(move || writer::writer_loop(conn, rx))`. |
| store/queries.rs | store/schema.rs | `read_scores`/`read_signals` SELECT from score/signal | ✓ WIRED | queries.rs:12,25; consumed by 5 call sites in mod.rs tests. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Crate compiles | `cargo build` | Finished dev profile | ✓ PASS |
| Store test contract | `cargo test --lib store::` | 6 passed; 0 failed | ✓ PASS |
| Lint gate | `cargo clippy --all-targets` | 0 warnings, 0 errors | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| SCORE-02 | 01-01-PLAN (`requirements: [SCORE-02]`) | Per-pubkey scores, per-layer sub-scores (EAV signal table), run metadata persist to SQLite (WAL, batched writes), idempotent on `(run_id, pubkey)` | ✓ SATISFIED | REQUIREMENTS.md maps SCORE-02 → Phase 1 (marked `[x]`). All sub-clauses verified: EAV signal table (schema.rs), WAL (PRAGMA + test), batched writes (writer_loop BATCH=8192 per txn), idempotent on `(run_id,pubkey)` (UPSERT_SCORE + tests). No orphaned requirements for Phase 1. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| — | — | None | — | No debt markers (TODO/FIXME/XXX/TBD/unimplemented!/todo!) in src/. No HashMap/HashSet (determinism hazard) outside doc-comment. |

**Security (T-01-01 — no `format!`-interpolated SQL):** ✓ VERIFIED. The only `format!` occurrences in src/ are the mitigation doc-comments in writer.rs:9 and queries.rs:5; no actual `format!`/`push_str`/string-concat reaches any SQL. Every write binds via `params![]` and every read via `?N` placeholders. begin_run uses `params![now, config_json]`.

### Human Verification Required

None. All success criteria are exercised by passing behavioral tests; no visual/UX/external-service/runtime-only behavior remains unverified.

### Gaps Summary

No gaps. The phase goal is achieved in the codebase: `Store::open` creates a fresh WAL SQLite file with the full 7-table schema; the single-writer `writer_loop` commits batched idempotent UPSERTs; the EAV `signal` table accepts new layer names with zero migration; and round-trip identity + deterministic re-runs are proven by passing tests. The 5-test ROADMAP contract maps 1:1 onto the four success criteria (with an extra across-batch idempotency test hardening criterion #2). `cargo build`, `cargo test --lib store::` (6/6), and `cargo clippy --all-targets` (clean) all confirmed by direct execution. SCORE-02 satisfied. The fingerprint/label/weight tables are created-but-unwritten by design (populated in Phases 6/7) — not a gap for this phase's write-then-read goal.

---

_Verified: 2026-06-25_
_Verifier: Claude (gsd-verifier)_
