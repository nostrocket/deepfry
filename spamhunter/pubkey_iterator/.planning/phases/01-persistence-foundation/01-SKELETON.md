# Walking Skeleton — Spamhunter (pubkey_iterator)

**Phase:** 1
**Generated:** 2026-06-25

## Capability Proven End-to-End

A developer can call `Store::open(path)` to create a fresh WAL SQLite database with the full schema, persist a batch of synthetic per-pubkey scores + per-layer signals through the single-writer batched-transaction actor, and read them back identically — the thinnest write-then-read path that exercises the whole persistence stack this project is built on.

> This is a re-runnable Rust **batch engine** (library crate), NOT a web app. There is no UI, no HTTP route, and no remote deployment. The template's "UI interaction" row maps to a real test/binary invocation that writes-then-reads the DB; "deployment" is N/A (local batch tool).

## Architectural Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language / crate shape | Rust, library crate `pubkey_iterator` (edition 2021), no binary yet | Locked by PROJECT.md/STACK.md. Library form because the validation contract is `cargo test --lib store::`; Phase 5 adds the `clap` binary. |
| Data layer | SQLite via `rusqlite` 0.40 (`bundled` feature) | STACK.md-locked; `bundled` compiles SQLite in (no host libsqlite3, reproducible). `sqlx` explicitly rejected — async + compile-time query checks are the wrong tradeoff for a single-writer local batch. |
| Durability | WAL mode + `synchronous=NORMAL` + `foreign_keys=ON` + `busy_timeout` | SCORE-02 mandates WAL + batched writes. NORMAL is WAL-consistent and far faster; a crash loses at most the last uncommitted (idempotent, re-generatable) batch. |
| Concurrency / write model | Single writer thread owning the only `Connection`, fed over a channel (`flume` or `std::sync::mpsc`), committing batched transactions | SQLite is single-writer; funnelling all writes to one thread now means the future tokio/rayon pipeline (Phase 3) has exactly one place that writes and never contends for the write lock. |
| Idempotency | `INSERT ... ON CONFLICT(<key>) DO UPDATE SET col = excluded.col`, keyed `(run_id, pubkey)` for `score` and `(run_id, pubkey, layer)` for `signal` | The DB constraint *is* the idempotency guarantee. A re-run is a new `run_id`; nothing mutates prior runs. |
| Extensibility | Tall EAV `signal` table (`run_id, pubkey, layer, value, evidence`) | A new detection layer (Phase 4+) is a new *row*, never a schema migration — the central reason the schema is shaped this way (success criterion #3, v2 layers DETECT-06..09). |
| Schema management | Single embedded `SCHEMA_DDL` const run via `execute_batch` with `CREATE TABLE IF NOT EXISTS` at `open()` | Greenfield, one schema version; EAV absorbs most future change as rows. Adopt `rusqlite_migration` at the first real `ALTER TABLE`, not before. |
| Directory layout | `src/model.rs` + `src/store/{mod,schema,writer,queries}.rs` | Matches ARCHITECTURE.md's structure; the `pipeline/`, `graphql/`, `layers/`, `aggregate/`, `tune/` siblings slot in identically in later phases. |

## Stack Touched in Phase 1

- [x] Project scaffold — `cargo init --lib`, `Cargo.toml` with Phase-1 deps, `cargo build`/`cargo test` runner, `cargo clippy` gate
- [x] "Routing" (N/A for a batch engine) — replaced by the public store API surface (`Store::open`, `begin_run`, `persist`, `close`, `reader`)
- [x] Database — one real write (writer actor persists synthetic scores + signals via batched WAL transactions) AND one real read (`read_scores`/`read_signals` round-trip)
- [x] "UI interaction" (N/A) — mapped to a real test invocation that writes-then-reads the DB (`batch_roundtrip_identity`)
- [x] Deployment — N/A (local batch tool). Local full-stack run is `cargo test` from the project root; documented run command is the validation suite.

## Out of Scope (Deferred to Later Slices)

- GraphQL client / `authors` enumeration / cursor-resume logic — Phase 2 (this phase only creates the `run.last_cursor`/`max_lev_id_*` columns).
- tokio fetch → bounded channel → rayon streaming pipeline — Phase 3.
- Detection layers (L0/L1/L3/L4), `Layer` trait, logistic combiner, evidence population — Phase 4 (this phase only creates the `signal.evidence` column).
- `clap` CLI (`run`/`export`/`label`/`tune`) — Phases 5–6 (no binary this phase).
- Logistic tuner (`linfa-logistic`) writing the `weight` table; human labels in `label` — Phase 6 (tables created here, unused this phase).
- Phase B cross-pubkey clustering populating `fingerprint` (`gaoya` MinHash/LSH) — Phase 7 / v2 DETECT-08.
- Direct strfry LMDB reads via `heed` — v2 PERF-01.
- A migration library — adopt only at the first non-additive `ALTER TABLE`.

## Subsequent Slice Plan

Each later phase adds one vertical slice on top of this store without altering its architectural decisions:

- Phase 2: enumerate every distinct pubkey via the LMDB2GraphQL `authors` query, resumably (persists cursor/`maxLevId` into the `run` row created here).
- Phase 3: bounded-memory streaming pipeline (tokio → flume → rayon) feeding the writer actor created here.
- Phase 4: detection layers + logistic combiner → first per-pubkey verdict, persisting `score` + EAV `signal` rows + evidence through this store.
- Phase 5: CLI `run` + `export` — drive a full batch and export the suspected-spammer list from this store.
- Phase 6: `label` + `tune` + backtest gate — re-fit weights into the `weight` table created here, read back at run start.
