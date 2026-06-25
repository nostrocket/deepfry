# Phase 1: Persistence Foundation - Pattern Map

**Mapped:** 2026-06-25
**Files analyzed:** 8 (1 manifest + 7 source/test files)
**Analogs found:** 4 layout/convention analogs (LMDB2GraphQL) / 8 files. SQLite-specific behavior (rusqlite open, PRAGMA, UPSERT, writer actor) has NO codebase analog — it is greenfield; canonical reference is `01-RESEARCH.md`.

## Greenfield Notice

`pubkey_iterator/` has **no Rust source yet** (`.planning/` + `contract.md` only). The entire monorepo contains exactly **one** Rust project, `/Users/g/git/deepfry/LMDB2GraphQL`, and it uses **LMDB via `heed`, not SQLite** — `grep` for `rusqlite | Connection::open | execute_batch | ON CONFLICT | pragma_update | flume | mpsc | thread::spawn` across the monorepo returns **zero** matches. Therefore:

- **Cargo manifest, crate layout, `lib.rs` module style, resource-open function shape, and tempfile-backed integration tests** have a real external analog in LMDB2GraphQL (read-only reference — do NOT modify it).
- **rusqlite connection bootstrap, PRAGMA sequencing, idempotent UPSERT, single-writer batched-transaction actor, and round-trip read helpers** have **no analog**. The canonical, liftable reference for these is `01-RESEARCH.md` (Patterns 1–3 + Code Examples), which already contains concrete, citation-backed Rust. The planner should copy from RESEARCH.md verbatim for these files, NOT invent a codebase pattern.

All analogs below are **read-only external references** in a sibling project; this phase creates files **only** under `/Users/g/git/deepfry/spamhunter/pubkey_iterator`.

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `Cargo.toml` | config (manifest) | n/a | `/Users/g/git/deepfry/LMDB2GraphQL/Cargo.toml` | role-match (manifest layout; deps differ — heed→rusqlite) |
| `src/lib.rs` | config (crate root / module decls) | n/a | `/Users/g/git/deepfry/LMDB2GraphQL/src/lib.rs` | exact (module-declaration idiom) |
| `src/model.rs` | model | transform (structs ↔ rows) | none in codebase | greenfield — RESEARCH.md "Architectural Responsibility Map" + ARCHITECTURE.md `model.rs` |
| `src/store/mod.rs` | service (store API + `open`) | request-response (open → handle) | `/Users/g/git/deepfry/LMDB2GraphQL/src/lmdb/env.rs` (`open_*_env` resource-open shape only) | partial — same *open-a-DB-handle* shape; SQLite body greenfield |
| `src/store/schema.rs` | config (embedded DDL const) | n/a | none in codebase | greenfield — RESEARCH.md "Code Examples → Schema creation" (full liftable `SCHEMA_DDL`) |
| `src/store/writer.rs` | service (single-writer actor) | event-driven / batch (channel → batched txn) | none in codebase | greenfield — RESEARCH.md Pattern 3 (`writer_loop`) |
| `src/store/queries.rs` | service (read helpers) | CRUD (read) | none in codebase | greenfield — RESEARCH.md "Round-trip read-back" |
| `src/store/mod.rs` `#[cfg(test)] mod tests` | test | batch round-trip / idempotency | `/Users/g/git/deepfry/LMDB2GraphQL/tests/dupsort_resume_test.rs` (synthetic-DB + tempfile convention) | role-match (test structure + tempfile substrate) |

> Module layout target (from RESEARCH.md "Recommended Project Structure" / ARCHITECTURE.md): `src/model.rs` + `src/store/{mod,schema,writer,queries}.rs`. Tests live in `src/store/mod.rs` under `#[cfg(test)]` per the RESEARCH.md "Test Map" (`cargo test --lib store::`), exercising criteria 1–4 + determinism.

## Pattern Assignments

### `Cargo.toml` (config, manifest)

**Analog:** `/Users/g/git/deepfry/LMDB2GraphQL/Cargo.toml` (layout/convention only — its deps are heed/axum/async-graphql, NOT this phase's deps).

**Liftable conventions from the analog:**
- `[package]` with `edition = "2021"` (lines 1–4). Match this edition unless the planner deliberately moves to 2024.
- `[dev-dependencies] tempfile = "3"` (line 50) — **exact** match for this phase's test substrate. Use the same crate; RESEARCH.md pins `tempfile = "3.27"`.
- `serde = { version = "1", features = ["derive"] }` (line 22) — already the monorepo Rust convention; reuse verbatim for the JSON columns (`config_json`, `evidence`).
- Inline `# [APPROVED]`/version-rationale comments on dependency lines (lines 6–9, 24–26, 37–47) are the house style — annotate `rusqlite`'s `bundled` choice and the sqlx-rejection the same way.

**Phase-1 dependency set (canonical source = `01-RESEARCH.md` "Installation", lines 82–94):**
```toml
[dependencies]
rusqlite   = { version = "0.40", features = ["bundled"] }
serde      = { version = "1.0", features = ["derive"] }
serde_json = "1.0"
flume      = "0.12"            # analyze→writer seam (or std::sync::mpsc)

[dev-dependencies]
tempfile = "3.27"
# OPTIONAL (only if pre-wiring migrations): rusqlite_migration = "2.6"
```
> Do NOT pull in the full project-wide stack (tokio/rayon/reqwest/gaoya/etc. from STACK.md) — this phase is persistence-only. Add the rest in their owning phases.

---

### `src/lib.rs` (config, crate root)

**Analog:** `/Users/g/git/deepfry/LMDB2GraphQL/src/lib.rs` — **exact** idiom match.

**Pattern to copy** (entire file is the template — doc-comment-per-module, `pub mod`):
```rust
/// pubkey_iterator persistence library crate.
pub mod model;

/// SQLite store: schema, single-writer actor, read helpers.
/// Tests use tempfile::TempDir() — never :memory: (WAL sidecars differ). [RESEARCH.md Pitfall]
pub mod store;
```
The analog declares each module with a `///` doc comment naming its responsibility and which phase introduced it (lib.rs lines 1–21). Reuse that exact convention so later-phase modules (`pipeline`, `graphql`, `layers`, `tune`) slot in identically.

> If the planner chooses a binary-only crate, `src/main.rs` replaces `src/lib.rs`; but a `lib.rs` is preferred here because the RESEARCH.md test command is `cargo test --lib store::` (library tests).

---

### `src/store/mod.rs` (service — `Store::open` + public API)

**Analog (shape only):** `/Users/g/git/deepfry/LMDB2GraphQL/src/lmdb/env.rs` `open_read_only_env(db_path, map_size) -> heed::Result<Env>` (lines 15–23). This is the *open-a-DB-handle-from-a-path-returning-a-Result* shape — the SQLite body is entirely different.

**Conventions to carry over from `env.rs`:**
- A single `open(path: &Path) -> Result<Handle>` constructor function (env.rs line 15) — mirror with `Store::open(path: &Path) -> rusqlite::Result<Store>`.
- A doc comment documenting *safety/durability invariants and what the function deliberately does NOT expose* (env.rs lines 4–14: "No write-transaction helper is exposed by this module"). Apply the analog here: document WAL/`synchronous=NORMAL` durability tradeoff and that **all writes funnel through the writer actor**, never a second connection.

**Greenfield body — copy from `01-RESEARCH.md` Pattern 1 (lines 169–179):**
```rust
pub fn open(path: &std::path::Path) -> rusqlite::Result<Connection> {
    let conn = Connection::open(path)?;
    conn.pragma_update(None, "journal_mode", "WAL")?;
    conn.pragma_update(None, "synchronous", "NORMAL")?;
    conn.pragma_update(None, "foreign_keys", "ON")?;   // OFF by default — REFERENCES inert without this
    conn.pragma_update(None, "temp_store", "MEMORY")?;
    conn.busy_timeout(std::time::Duration::from_secs(5))?;
    conn.execute_batch(SCHEMA_DDL)?;                   // CREATE TABLE IF NOT EXISTS ×7
    Ok(conn)
}
```
Order is load-bearing: PRAGMAs before any schema/DML.

---

### `src/store/schema.rs` (config — embedded DDL)

**Analog:** none. **Canonical source = `01-RESEARCH.md` "Code Examples → Schema creation" (lines 295–355)** — a complete, liftable `pub const SCHEMA_DDL: &str` with all 7 tables (`run`, `pubkey`, `score`, `signal` EAV, `fingerprint`, `label`, `weight`) + `idx_signal_layer`, `idx_fp_chash`.

**Copy that const verbatim.** It is the refined schema (adds nullable `signal.evidence`, `label.source`, `run.finished_at/max_lev_id_*/last_cursor/status`, `weight.tuned_at/tuned_from_run`) over the bare ARCHITECTURE.md sketch — RESEARCH.md notes those additive columns at lines 357 and 374–380. Use `IF NOT EXISTS` everywhere so `open()` is idempotent.

> u64↔i64 caveat (RESEARCH.md line 382): `content_hash`/`simhash` are `u64` stored as `i64` via `as` bit-reinterpret; read back via `as u64`. Equality/bucketing preserved; never signed-order them. Phase 7 concern but the column types are created here.

---

### `src/store/writer.rs` (service — single-writer batched-transaction actor)

**Analog:** none. **Canonical source = `01-RESEARCH.md` Pattern 3 `writer_loop` (lines 217–240)** + Pattern 2 UPSERT consts (lines 190–207).

**Copy the actor loop** (block-for-one then greedily drain to `BATCH`, one `Transaction`, `prepare_cached` per UPSERT, `commit` per batch):
```rust
fn writer_loop(mut conn: Connection, rx: flume::Receiver<Persist>) -> rusqlite::Result<()> {
    let mut buf: Vec<Persist> = Vec::with_capacity(BATCH);
    loop {
        match rx.recv() { Ok(p) => buf.push(p), Err(_) => break }
        while buf.len() < BATCH { match rx.try_recv() { Ok(p) => buf.push(p), Err(_) => break } }
        let tx = conn.transaction()?;
        {
            let mut up_score  = tx.prepare_cached(UPSERT_SCORE)?;
            let mut up_signal = tx.prepare_cached(UPSERT_SIGNAL)?;
            let mut up_pubkey = tx.prepare_cached(UPSERT_PUBKEY)?;
            for p in buf.drain(..) {
                up_pubkey.execute([&p.pubkey])?;
                up_score.execute(params![p.run_id, p.pubkey, p.score, p.whitelisted as i64, p.suspected as i64])?;
                for s in &p.subscores { // FIXED layer order → determinism (RESEARCH Pitfall 2)
                    up_signal.execute(params![p.run_id, p.pubkey, s.layer, s.value, s.evidence])?;
                }
            }
        }
        tx.commit()?;
    }
    Ok(())
}
```
**UPSERT consts to copy** (RESEARCH.md lines 190–207): `UPSERT_SCORE` keyed `ON CONFLICT(run_id, pubkey)`, `UPSERT_SIGNAL` keyed `ON CONFLICT(run_id, pubkey, layer)`, `UPSERT_PUBKEY` `ON CONFLICT(pubkey) DO NOTHING`. All use `excluded.col`.

**Channel choice (RESEARCH "Claude's Discretion", line 42):** `flume` (project-standard) or `std::sync::mpsc` for this internal analyze→writer seam. The writer owns the only `Connection` on its own thread (`std::thread::spawn`) — no codebase analog for the spawn, follow RESEARCH "System Architecture Diagram" lines 126–137.

---

### `src/store/queries.rs` (service — read helpers)

**Analog:** none. **Canonical source = `01-RESEARCH.md` "Round-trip read-back" (lines 362–367):**
```rust
pub fn read_scores(conn: &Connection, run_id: i64) -> rusqlite::Result<Vec<(String, f64)>> {
    let mut stmt = conn.prepare(
        "SELECT pubkey, score FROM score WHERE run_id = ?1 ORDER BY pubkey")?;
    let rows = stmt.query_map([run_id], |r| Ok((r.get::<_, String>(0)?, r.get::<_, f64>(1)?)))?;
    rows.collect()
}
```
`ORDER BY` is load-bearing for the deterministic round-trip assertion (criterion #4). Add parallel `read_signals(conn, run_id)` for the EAV round-trip.

---

### `src/store/mod.rs` `#[cfg(test)] mod tests` (test)

**Analog:** `/Users/g/git/deepfry/LMDB2GraphQL/tests/dupsort_resume_test.rs` — for **test *structure and substrate convention***, not content (it tests LMDB scan ordering).

**Conventions to carry over:**
- **Synthetic-fixture-in-a-temp-dir** approach: the analog builds a synthetic DB in a tempfile env and asserts behavior against it (header lines 28–30: "The synthetic env legitimately uses write transactions... read-only rule applies only to strfry's live database"). Mirror: build a fresh `tempfile::TempDir` SQLite DB per test, persist synthetic `Persist`/`Score`/`SubScore`, assert.
- **Heavy explanatory doc-header** stating exactly what each test proves (analog lines 1–36) — match this rigor for the idempotency/determinism tests.
- `use tempfile` for the DB substrate, matching the analog's `[dev-dependencies] tempfile` (Cargo.toml line 50).

**Test list (canonical = `01-RESEARCH.md` "Phase Requirements → Test Map", lines 451–462):**
- `open_creates_wal_and_schema` — after `open(tmp)`: `PRAGMA journal_mode` == `"wal"`, `-wal` sidecar exists, `sqlite_master` lists all 7 tables (criterion #1).
- `upsert_is_idempotent` — double-write `(run_id,pubkey)` and `(run_id,pubkey,layer)` → `count(*)` == 1, value reflects second write (criterion #2).
- `new_layer_no_migration` — insert `signal` with `layer="L99_brand_new"`; persists with no DDL between (criterion #3).
- `batch_roundtrip_identity` — persist N synthetic records through the writer; `SELECT` back; structural+value equality (criterion #4).
- `rerun_is_deterministic` — same batch into two fresh DBs → row-set + value equal (determinism).

> **Use a temp FILE, not `:memory:`** (RESEARCH Anti-Pattern, line 247): in-memory DBs do not produce `-wal`/`-shm` sidecars, so criterion #1 cannot be asserted against them.

## Shared Patterns

### Parameterized statements (SQL-injection mitigation — ASVS V5)
**Source:** `01-RESEARCH.md` Security Domain (lines 486, 493) + Pattern 2/3.
**Apply to:** every write in `writer.rs` and every read in `queries.rs`.
Always bind via `?N` placeholders + `params![]` / slice params; **never** `format!` a pubkey or value into SQL. rusqlite binds parameters even for first-party data — this is non-negotiable per the phase's security gate (`block_on: high`).

### Resource-open with documented invariants
**Source:** `/Users/g/git/deepfry/LMDB2GraphQL/src/lmdb/env.rs` (lines 4–23).
**Apply to:** `Store::open` in `store/mod.rs`.
A single `open(path) -> Result<Handle>` whose doc comment states the durability/safety invariants and what the module deliberately does NOT expose (the analog refuses to expose a write-transaction helper; here, refuse a second writer connection — all writes go through the actor).

### Module-declaration crate root
**Source:** `/Users/g/git/deepfry/LMDB2GraphQL/src/lib.rs` (lines 1–21).
**Apply to:** `src/lib.rs`.
One `pub mod` per top-level concern, each preceded by a `///` doc comment naming its responsibility — keeps the later phases' modules (pipeline, graphql, layers, tune) drop-in.

### Idempotent UPSERT on the natural key
**Source:** `01-RESEARCH.md` Pattern 2 (lines 184–209).
**Apply to:** all writes in `writer.rs` (`score`, `signal`, `pubkey`; later `fingerprint`, `weight`).
`INSERT ... ON CONFLICT(<pk cols>) DO UPDATE SET col = excluded.col`. Conflict target MUST be a declared PK/UNIQUE. Prefer this over `INSERT OR REPLACE` (RESEARCH "State of the Art", line 397: OR REPLACE deletes+reinserts, drops unset columns, churns rowid).

### Deterministic write order
**Source:** `01-RESEARCH.md` Pitfall 2 (lines 271–275) + Anti-Patterns (line 249).
**Apply to:** `writer.rs` subscore iteration and any read `ORDER BY`.
Emit subscores in a **fixed layer order** (never HashMap iteration order); a re-run is a new `run_id`, never a mutation. This is what makes `rerun_is_deterministic` pass.

## No Analog Found

These files have no codebase match; the planner should use `01-RESEARCH.md` as the canonical, citation-backed reference (it already contains liftable Rust for each):

| File | Role | Data Flow | Reason | Canonical Reference |
|------|------|-----------|--------|---------------------|
| `src/model.rs` | model | transform | No Rust struct↔row models exist (LMDB2GraphQL maps to GraphQL types, not SQL rows) | RESEARCH.md "Architectural Responsibility Map" (line 24); ARCHITECTURE.md `model.rs` (`Run, Score, SubScore, Fingerprint, Label, Weight, Persist`) |
| `src/store/schema.rs` | config | n/a | No SQLite/DDL anywhere in monorepo | RESEARCH.md lines 295–355 (full `SCHEMA_DDL`) |
| `src/store/writer.rs` | service | event-driven/batch | No channel/actor or `Connection` write path exists; `grep flume\|mpsc\|thread::spawn` → 0 hits | RESEARCH.md Pattern 3 (lines 217–240) + Pattern 2 (lines 190–207) |
| `src/store/queries.rs` | service | CRUD (read) | No rusqlite `query_map` read helpers exist | RESEARCH.md lines 362–367 |
| `Store::open` body | service | request-response | `env.rs` gives the *shape*; SQLite PRAGMA/WAL bootstrap is greenfield | RESEARCH.md Pattern 1 (lines 169–179) |

## Metadata

**Analog search scope:** entire monorepo `/Users/g/git/deepfry` (`*.rs`, `*.toml`); the project root `/Users/g/git/deepfry/spamhunter/pubkey_iterator` (no Rust source — greenfield); sibling Rust project `/Users/g/git/deepfry/LMDB2GraphQL` (read-only reference).
**Files scanned:** LMDB2GraphQL — `Cargo.toml`, `src/lib.rs`, `src/lmdb/env.rs`, `src/config.rs`, `tests/dupsort_resume_test.rs`. Monorepo-wide greps for `rusqlite | Connection::open | execute_batch | ON CONFLICT | pragma_update | flume | mpsc | thread::spawn` → only LMDB2GraphQL matched (and it has none of the SQLite/actor patterns).
**Key finding:** no SQLite, no channel/actor, no single-writer pattern exists anywhere in the monorepo. The four real analogs are Cargo/layout/test-convention only; all SQLite-specific code is greenfield and the planner should lift it directly from `01-RESEARCH.md`, which is unusually complete (full DDL, PRAGMA bootstrap, UPSERT consts, writer loop, read helper, test map).
**Pattern extraction date:** 2026-06-25
