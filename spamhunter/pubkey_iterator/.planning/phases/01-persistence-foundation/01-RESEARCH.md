# Phase 1: Persistence Foundation - Research

**Researched:** 2026-06-25
**Domain:** Embedded SQLite persistence in Rust — single-writer, WAL, idempotent batched UPSERT, EAV signal schema
**Confidence:** HIGH

## Summary

This phase builds the **dependency root** of the whole engine: a single SQLite file with the full 7-table schema and a single-writer, batched-transaction, idempotent UPSERT API that every later phase plugs into. The technology decision is already locked by the project-wide STACK research and confirmed live this session: **`rusqlite` 0.40.1 with the `bundled` feature**, opened in **WAL mode** with `synchronous=NORMAL`, `foreign_keys=ON`, and a `busy_timeout`, written by **one dedicated writer thread** fed over a channel, committing batches of ~5–10k rows per transaction. `sqlx` is explicitly rejected for v1 (async + compile-time query checking is the wrong tradeoff for a single-writer local batch).

The schema is already specified in `.planning/research/ARCHITECTURE.md` and is reproduced here as concrete, liftable DDL. The two load-bearing design choices are: (1) idempotency via `INSERT ... ON CONFLICT(...) DO UPDATE SET col = excluded.col` keyed on `(run_id, pubkey)` for `score` and `(run_id, pubkey, layer)` for `signal`; (2) a **tall EAV `signal` table** (`run_id, pubkey, layer, value, evidence`) so a brand-new detection layer is a new *row*, never a schema migration — the explicit reason success criterion #3 exists.

**Primary recommendation:** `rusqlite = { version = "0.40", features = ["bundled"] }`; open once, apply PRAGMAs on the writer connection, create all 7 tables with `CREATE TABLE IF NOT EXISTS` from a single embedded `schema.rs` string (no migration library needed for v1 — use `rusqlite_migration` only if you want the upgrade path pre-wired); funnel all writes through one writer thread doing `ON CONFLICT DO UPDATE` upserts inside batched transactions; test idempotency and round-trip identity with a `tempfile`-backed DB.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Schema creation on demand | Database / Storage | — | `CREATE TABLE IF NOT EXISTS` + PRAGMAs run once at store open; pure storage concern |
| Idempotent persistence (UPSERT) | Database / Storage | — | Enforced by SQLite UNIQUE/PK constraints + `ON CONFLICT DO UPDATE`; the DB *is* the idempotency guarantee, not application logic |
| Single-writer serialization | Application (writer actor) | Database / Storage | SQLite permits one writer; the actor thread funnels all writes so the future tokio/rayon pipeline never contends for the write lock |
| Batched-transaction commit cadence | Application (writer actor) | Database / Storage | Transaction batching is an app-side throughput decision; durability semantics are DB-side (WAL + synchronous) |
| Round-trip read-back (verification) | Database / Storage | — | Plain `SELECT` via prepared statements; the store exposes typed read helpers |
| Type model (`Run`, `Score`, `SubScore`, `Fingerprint`, `Label`, `Weight`) | Application (model) | — | Rust structs serialized to/from rows; `serde` only where a column is JSON (`config_json`, `evidence`) |

## User Constraints

> No `CONTEXT.md` exists for this phase (research ran standalone, before `/gsd-discuss-phase`). The binding constraints below are lifted verbatim from `PROJECT.md` (Constraints / Key Decisions) and `REQUIREMENTS.md` (SCORE-02), and act as locked decisions for the planner.

### Locked Decisions (from PROJECT.md + STACK.md)
- **Tech stack: Rust** (user decision). Output store: **SQLite**.
- **`rusqlite` (sync, bundled SQLite), NOT `sqlx`** — STACK.md "What NOT to Use": sqlx's async + compile-time-checked queries require a live DB at build time and an async runtime around every query; wrong tradeoff for a single-writer local batch.
- **WAL mode + batched writes** (SCORE-02 verbatim): "Per-pubkey scores, per-layer sub-scores (EAV signal table), and run metadata persist to SQLite (WAL, batched writes), idempotent on `(run_id, pubkey)`".
- **Single writer** — STACK/ARCHITECTURE: SQLite is single-writer; do not write from many threads. Funnel results to one writer thread.
- **EAV `signal` table** — a new detection layer must be addable with **zero schema migration** (success criterion #3 / REQUIREMENTS v2 note).
- **Idempotent, re-runnable batch** — UPSERT keyed by `(run_id, pubkey)`; a full re-run is a new `run_id`; nothing mutates prior runs.

### Claude's Discretion
- Exact migration approach for v1 (single embedded `CREATE TABLE IF NOT EXISTS` string vs. `rusqlite_migration`) — recommendation below.
- Evidence storage shape on the `signal` table (JSON `TEXT` column vs. separate table) — recommendation below.
- Batch size / commit cadence (research recommends ~5–10k rows/txn; final value tunable).
- Writer-actor channel type (the project already standardizes on `flume` for the fetch→analyze seam; reuse `flume` or `std::sync::mpsc` for the analyze→writer seam — discretion).
- Test DB substrate (in-memory `:memory:` vs. `tempfile` temp-file) — recommendation below (temp-file, because WAL behavior differs in-memory).

### Deferred Ideas (OUT OF SCOPE for Phase 1)
- Direct strfry LMDB reads via `heed` (v2, PERF-01) — not a persistence concern.
- The tuner / logistic fit (`linfa-logistic`) — Phase 6; this phase only creates the `weight`/`label` tables it will read/write.
- Phase B cross-pubkey clustering populating `fingerprint` — Phase 7; this phase only creates the `fingerprint` table.
- Resumable cursor logic (`last_cursor`, `max_lev_id` *usage*) — Phase 2; this phase only creates the columns.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| SCORE-02 | Per-pubkey scores, per-layer sub-scores (EAV signal table), and run metadata persist to SQLite (WAL, batched writes), idempotent on `(run_id, pubkey)` | Standard Stack (`rusqlite` bundled + WAL), Schema Sketch (all 7 tables incl. EAV `signal`), Code Examples (PRAGMA setup, `ON CONFLICT DO UPDATE` upsert, single-writer batched txn), Validation Architecture (idempotency + round-trip tests) |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `rusqlite` | 0.40.1 | Synchronous embedded SQLite bindings | `[VERIFIED: crates.io]` De-facto Rust SQLite crate (1.7M weekly downloads, repo github.com/rusqlite/rusqlite). Zero-overhead, single-writer sweet spot. STACK.md-locked choice. |
| `serde` + `serde_json` | 1.0.228 / 1.0 | (De)serialize JSON columns (`config_json`, `evidence`) | `[VERIFIED: crates.io]` Universal; only needed where a column holds JSON. Already a project-wide dependency. |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `rusqlite_migration` | 2.6.0 | Versioned forward migrations over rusqlite | `[VERIFIED: crates.io]` OPTIONAL. Use only if you want the additive-migration upgrade path pre-wired now (repo github.com/cljoly/rusqlite_migration, 115k weekly dl, updated 2026-05-28). For v1 the lighter `CREATE TABLE IF NOT EXISTS` string is sufficient — see "Migrations" below. |
| `tempfile` | 3.27.0 | Temp-file-backed DB for tests | `[VERIFIED: crates.io]` Use a `NamedTempFile`/temp dir so tests exercise real WAL on-disk behavior (sidecar `-wal`/`-shm` files), which `:memory:` does not fully replicate. |
| `flume` | 0.12 | Channel for the analyze→writer seam (writer actor) | `[VERIFIED: crates.io]` Already the project's chosen channel (STACK.md). Reuse for funnelling `Persist` messages to the single writer. `std::sync::mpsc` is an acceptable alternative for this internal seam. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `rusqlite` (sync) | `sqlx` 0.9 (async) | `[CITED: STACK.md]` REJECTED for v1: async + compile-time query checking needs a live DB at build time and a runtime around every query, for zero benefit on a single-writer local batch. Reconsider only if the store becomes a concurrently-written service or migrates to Postgres. |
| Embedded `CREATE TABLE IF NOT EXISTS` string | `rusqlite_migration` / `refinery` | A migration library buys versioned, ordered, idempotent upgrades. For a greenfield v1 with one schema version, the embedded string is lighter and the EAV `signal` design means most future changes are *additive rows, not DDL*. Adopt a migration lib at the first real `ALTER TABLE`. |
| Single writer thread | `rusqlite` per-thread connections + `busy_timeout` | Multiple writer connections serialize on the SQLite write lock and risk `SQLITE_BUSY` under the future concurrent pipeline (PITFALLS #15). Single writer is the prescribed pattern. |

**Installation:**
```toml
# Cargo.toml — [dependencies]  (Phase 1 subset of the project-wide stack)
rusqlite = { version = "0.40", features = ["bundled"] }
serde    = { version = "1.0", features = ["derive"] }
serde_json = "1.0"
flume    = "0.12"            # analyze→writer seam (or std::sync::mpsc)

[dev-dependencies]
tempfile = "3.27"

# OPTIONAL, only if pre-wiring the migration upgrade path:
# rusqlite_migration = "2.6"
```

**Version verification:** All versions confirmed live against the crates.io API on 2026-06-25 (`max_stable`): `rusqlite` 0.40.1 (updated 2026-06-06), `rusqlite_migration` 2.6.0 (2026-05-28), `tempfile` 3.27.0, `serde`/`serde_json`/`flume` per STACK.md (same-day verification). The `bundled` feature compiles SQLite in — no system `libsqlite3`, reproducible across hosts.

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `rusqlite` | crates | 11 yrs (2014) | 1.7M/wk | github.com/rusqlite/rusqlite | OK | Approved |
| `serde` | crates | 11 yrs (2014) | 16.8M/wk | github.com/serde-rs/serde | OK | Approved |
| `serde_json` | crates | 10 yrs (2015) | 16.3M/wk | github.com/serde-rs/json | OK | Approved |
| `rusqlite_migration` | crates | 5 yrs (2020) | 116k/wk | github.com/cljoly/rusqlite_migration | OK | Approved (optional) |
| `tempfile` | crates | 11 yrs (2015) | 10.9M/wk | github.com/Stebalien/tempfile | OK | Approved (dev-dep) |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

*Verified via `gsd-tools query package-legitimacy check --ecosystem crates` (all `OK`, no postinstall, not deprecated) cross-checked with the crates.io API. The `bundled` SQLite source ships inside `libsqlite3-sys` (rusqlite's vetted, long-standing companion crate).*

## Architecture Patterns

### System Architecture Diagram

```
                          analyze stage (Phase 3+, future)
                          rayon workers produce results
                                     │
                                     ▼  Persist { run_id, pubkey, score, Vec<SubScore>, Vec<Fingerprint> }
                          ┌──────────────────────┐
                          │  flume / mpsc channel │   (back-pressure seam, future)
                          └───────────┬──────────┘
                                      ▼
        ┌───────────────────────────────────────────────────────────┐
        │   SINGLE WRITER THREAD  (this phase's deliverable)          │
        │                                                             │
        │   open() ─▶ apply PRAGMAs ─▶ CREATE TABLE IF NOT EXISTS ×7  │
        │                                                             │
        │   loop: drain channel into a batch buffer                   │
        │         ─▶ BEGIN  (rusqlite Transaction)                    │
        │            for each result:                                 │
        │              INSERT INTO run/pubkey/score/signal/fingerprint│
        │              ON CONFLICT(...) DO UPDATE SET col=excluded.col │
        │         ─▶ COMMIT  (every ~5–10k rows or flush tick)        │
        └──────────────────────────┬──────────────────────────────────┘
                                   ▼
                          spamhunter.sqlite  (+ -wal, -shm sidecars)
                          tables: run · pubkey · score · signal(EAV)
                                  · fingerprint · label · weight
                                   ▲
                                   │  plain SELECT via prepared stmts
                          readers (export Phase 5, tuner Phase 6, sqlite3 CLI)
```

Data flow to trace: a batch of synthetic `Score`+`SubScore` results enters the channel → the writer drains them into one transaction → upserts land exactly once per key → a reader `SELECT`s them back identically (this is success criterion #4, and the round-trip test).

### Recommended Project Structure
```
src/
├── model.rs        # Run, Score, SubScore, Fingerprint, Label, Weight, Persist payload
└── store/
    ├── mod.rs      # Store::open(path) -> applies PRAGMAs + schema; public API surface
    ├── schema.rs   # embedded DDL string + PRAGMA constants (CREATE TABLE IF NOT EXISTS)
    ├── writer.rs   # single-writer actor: channel recv loop + batched txn + upserts
    └── queries.rs  # prepared-statement read helpers (round-trip reads, feature matrix later)
```
(Matches `store/` in ARCHITECTURE.md's structure; the `pipeline/`, `layers/`, `graphql/` siblings are later phases.)

### Pattern 1: Connection bootstrap (PRAGMAs first, then schema)
**What:** On open, set PRAGMAs on the writer connection *before* any schema/DML, then create all tables idempotently.
**When to use:** Once, at `Store::open()`.
**Example:**
```rust
// Source: docs.rs/rusqlite (pragma_update / execute_batch) + sqlite.org/pragma.html
use rusqlite::Connection;

pub fn open(path: &std::path::Path) -> rusqlite::Result<Connection> {
    let conn = Connection::open(path)?;
    // journal_mode returns a row → use pragma_query/query_row, not execute.
    conn.pragma_update(None, "journal_mode", "WAL")?;     // persistent across reopen [CITED: sqlite.org/wal.html]
    conn.pragma_update(None, "synchronous", "NORMAL")?;    // safe under WAL for a re-runnable batch
    conn.pragma_update(None, "foreign_keys", "ON")?;       // FK enforcement is OFF by default in SQLite
    conn.pragma_update(None, "temp_store", "MEMORY")?;
    conn.busy_timeout(std::time::Duration::from_secs(5))?; // absorb transient lock waits
    conn.execute_batch(SCHEMA_DDL)?;                       // CREATE TABLE IF NOT EXISTS ×7 + indexes
    Ok(conn)
}
```
> Note: `PRAGMA journal_mode=WAL` is the one PRAGMA that *returns a value* ("wal"); `pragma_update` handles it, but if you assert the result use `conn.pragma_query`/`query_row`. WAL persists across connections once set `[CITED: sqlite.org/wal.html]`, so readers in later phases inherit it.

### Pattern 2: Idempotent UPSERT keyed on the natural key
**What:** `INSERT ... ON CONFLICT(<pk cols>) DO UPDATE SET col = excluded.col`. The conflict target must be a UNIQUE/PRIMARY KEY constraint or unique index `[CITED: sqlite.org/lang_UPSERT.html]`.
**When to use:** Every write to `score`, `signal`, `fingerprint`, `pubkey`, `weight`.
**Example:**
```rust
// Source: sqlite.org/lang_UPSERT.html (excluded. qualifier)
// score: idempotent on (run_id, pubkey)
const UPSERT_SCORE: &str = "
  INSERT INTO score (run_id, pubkey, score, whitelisted, suspected)
  VALUES (?1, ?2, ?3, ?4, ?5)
  ON CONFLICT(run_id, pubkey) DO UPDATE SET
    score       = excluded.score,
    whitelisted = excluded.whitelisted,
    suspected   = excluded.suspected";

// signal: idempotent on (run_id, pubkey, layer) — EAV, one row per layer
const UPSERT_SIGNAL: &str = "
  INSERT INTO signal (run_id, pubkey, layer, value, evidence)
  VALUES (?1, ?2, ?3, ?4, ?5)
  ON CONFLICT(run_id, pubkey, layer) DO UPDATE SET
    value    = excluded.value,
    evidence = excluded.evidence";

// pubkey dimension: insert-or-ignore (identity is run-independent)
const UPSERT_PUBKEY: &str = "INSERT INTO pubkey (pubkey) VALUES (?1) ON CONFLICT(pubkey) DO NOTHING";
```
Writing the same `(run_id, pubkey)` (or `(run_id, pubkey, layer)`) twice leaves exactly one row — success criterion #2.

### Pattern 3: Single-writer batched transaction
**What:** One thread owns the connection; it drains a channel into a buffer and commits ~5–10k upserts per `Transaction`.
**When to use:** The writer actor's run loop.
**Example:**
```rust
// Source: docs.rs/rusqlite (Transaction, prepare_cached)
fn writer_loop(mut conn: Connection, rx: flume::Receiver<Persist>) -> rusqlite::Result<()> {
    let mut buf: Vec<Persist> = Vec::with_capacity(BATCH);
    loop {
        // block for one, then greedily drain up to BATCH
        match rx.recv() { Ok(p) => buf.push(p), Err(_) => break }
        while buf.len() < BATCH { match rx.try_recv() { Ok(p) => buf.push(p), Err(_) => break } }

        let tx = conn.transaction()?;                 // BEGIN
        {
            let mut up_score  = tx.prepare_cached(UPSERT_SCORE)?;
            let mut up_signal = tx.prepare_cached(UPSERT_SIGNAL)?;
            let mut up_pubkey = tx.prepare_cached(UPSERT_PUBKEY)?;
            for p in buf.drain(..) {
                up_pubkey.execute([&p.pubkey])?;
                up_score.execute(params![p.run_id, p.pubkey, p.score, p.whitelisted as i64, p.suspected as i64])?;
                for s in &p.subscores {               // fixed layer order → deterministic (PITFALLS #12)
                    up_signal.execute(params![p.run_id, p.pubkey, s.layer, s.value, s.evidence])?;
                }
            }
        }
        tx.commit()?;                                  // COMMIT (one fsync of the WAL at NORMAL)
    }
    Ok(())
}
```
> `prepare_cached` reuses compiled statements across the batch. Iterate `subscores` in a **fixed layer order** so re-runs are bit-identical (PITFALLS #12 determinism).

### Anti-Patterns to Avoid
- **Multiple writer connections / per-worker autocommit:** serializes on the SQLite write lock → `SQLITE_BUSY`, fsync-per-row, negative scaling (PITFALLS #15). Use one writer + batched txns.
- **`PRAGMA foreign_keys` left default:** SQLite disables FK enforcement by default; the schema's `REFERENCES` are inert unless `foreign_keys=ON` is set per connection.
- **`:memory:` DB in tests that assert WAL/sidecar behavior:** in-memory DBs don't produce `-wal`/`-shm` files; use a temp file to exercise the real durability path.
- **`synchronous=FULL` "to be safe":** unnecessary under a re-runnable batch; NORMAL is WAL-consistent and far faster. A crash loses at most the last uncommitted batch, which a re-run regenerates.
- **Non-deterministic write order** (HashMap iteration, unordered subscores): breaks round-trip/idempotency reproducibility — fix the layer order.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Upsert / dedupe-on-write | Manual `SELECT then INSERT/UPDATE` | `INSERT ... ON CONFLICT DO UPDATE` | Atomic, race-free, one round-trip; the SQL engine enforces uniqueness |
| Bundling SQLite | System `libsqlite3` + build flags | `rusqlite` `bundled` feature | Reproducible, no host dependency, version-pinned |
| Connection pooling for the writer | Custom pool | One owned `Connection` on one thread | SQLite is single-writer; a "pool" of writers is an anti-pattern here |
| Temp DB lifecycle in tests | Manual `/tmp` file + cleanup | `tempfile::TempDir` | Auto-cleanup, unique paths, no cross-test collisions |
| Transaction management | Manual `BEGIN`/`COMMIT` strings | `rusqlite::Transaction` (RAII) | Auto-rollback on drop/error; correct nesting |

**Key insight:** SQLite + rusqlite already provide every primitive this phase needs (uniqueness, atomic upsert, transactions, WAL durability). The only *engineering* this phase contains is the single-writer actor and the schema shape — everything else is configuration of well-tested machinery.

## Common Pitfalls

### Pitfall 1: SQLite write contention / per-row commit overhead (PITFALLS #15)
**What goes wrong:** Future parallel scoring workers each open a connection and autocommit per row → `SQLITE_BUSY`, fsync-bound throughput, throughput *drops* as workers rise.
**Why it happens:** SQLite is single-writer; N independent writers serialize on the write lock and fsync every statement.
**How to avoid:** Build the single-writer actor + WAL + batched transactions + `busy_timeout` *now*, in Phase 1, so the pipeline (Phase 3) has only one place that writes.
**Warning signs:** `SQLITE_BUSY` errors; fsync dominates a profile; negative scaling.

### Pitfall 2: Non-deterministic / non-idempotent writes (PITFALLS #12)
**What goes wrong:** Re-runs produce different rows or jittered values even on identical data; resumed runs duplicate or skip.
**Why it happens:** HashMap iteration order, unordered subscore emission, missing UPSERT.
**How to avoid:** UPSERT keyed on `(run_id, pubkey[, layer])`; emit subscores in a fixed layer order; a re-run is a *new* `run_id`, never a mutation of a prior run. Two persists of the same key leave exactly one row.
**Warning signs:** `SELECT count(*)` after double-write > expected; diff of two runs shows reordered/duplicated rows.

### Pitfall 3: FK enforcement silently off / WAL durability misunderstanding
**What goes wrong:** `REFERENCES` constraints don't fire (FK off by default); or developers assume `synchronous=NORMAL` is lossless.
**Why it happens:** SQLite defaults `foreign_keys=OFF`; under WAL+NORMAL a committed txn *can* roll back on power loss (DB stays uncorrupted) `[CITED: sqlite.org/wal.html]`.
**How to avoid:** Set `foreign_keys=ON` per connection. Document NORMAL's tradeoff: acceptable because a crash loses at most the last batch, which a re-run regenerates (the batch is idempotent).
**Warning signs:** Orphan `score`/`signal` rows with no parent `run`; surprise at lost last-batch after a hard kill.

### Pitfall 4: WAL on a network filesystem
**What goes wrong:** WAL mode fails or corrupts if the DB file lives on NFS/SMB — WAL needs shared memory between processes `[CITED: sqlite.org/wal.html]`.
**Why it happens:** Processes on separate hosts can't share the `-shm` memory.
**How to avoid:** Keep `spamhunter.sqlite` on a local filesystem. Document this constraint for deployment (the engine runs where its DB is local).
**Warning signs:** `disk I/O error` / `database is locked` only when the DB path is a network mount.

## Code Examples

### Schema creation (the embedded DDL string)
```rust
// Source: schema lifted from .planning/research/ARCHITECTURE.md (idempotent, tuning-ready)
// Stored as a const in src/store/schema.rs and run via conn.execute_batch(SCHEMA_DDL)
pub const SCHEMA_DDL: &str = r#"
CREATE TABLE IF NOT EXISTS run (
  run_id            INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at        INTEGER NOT NULL,
  finished_at       INTEGER,
  max_lev_id_start  INTEGER,
  max_lev_id_end    INTEGER,
  last_cursor       TEXT,
  config_json       TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'running'   -- running | done | aborted
);

CREATE TABLE IF NOT EXISTS pubkey (
  pubkey TEXT PRIMARY KEY                              -- 64-char lowercase hex
);

CREATE TABLE IF NOT EXISTS score (
  run_id      INTEGER NOT NULL REFERENCES run(run_id),
  pubkey      TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score       REAL    NOT NULL,                        -- sigmoid(Σwᵢxᵢ+b) ∈ [0,1]
  whitelisted INTEGER NOT NULL,                        -- 0/1, denormalized L0
  suspected   INTEGER NOT NULL,                        -- score > τ at run time
  PRIMARY KEY (run_id, pubkey)
);

CREATE TABLE IF NOT EXISTS signal (
  run_id   INTEGER NOT NULL REFERENCES run(run_id),
  pubkey   TEXT    NOT NULL REFERENCES pubkey(pubkey),
  layer    TEXT    NOT NULL,                           -- e.g. 'L1_near_dup' (stable contract)
  value    REAL    NOT NULL,                           -- xᵢ ∈ [0,1]
  evidence TEXT,                                        -- JSON: per-layer explanation (SCORE-05)
  PRIMARY KEY (run_id, pubkey, layer)
);
CREATE INDEX IF NOT EXISTS idx_signal_layer ON signal(layer);

CREATE TABLE IF NOT EXISTS fingerprint (
  run_id       INTEGER NOT NULL REFERENCES run(run_id),
  pubkey       TEXT    NOT NULL REFERENCES pubkey(pubkey),
  content_hash INTEGER NOT NULL,                        -- xxh3 of normalized content
  simhash      INTEGER NOT NULL,                        -- 64-bit SimHash
  minhash      BLOB,                                    -- packed MinHash sig (Phase 7, optional)
  PRIMARY KEY (run_id, pubkey, content_hash)
);
CREATE INDEX IF NOT EXISTS idx_fp_chash ON fingerprint(run_id, content_hash);

CREATE TABLE IF NOT EXISTS label (
  pubkey     TEXT PRIMARY KEY REFERENCES pubkey(pubkey),
  is_spam    INTEGER NOT NULL,                          -- 1 spam, 0 ham
  labeled_at INTEGER NOT NULL,
  source     TEXT,                                       -- label provenance (leakage audit, PITFALLS #11)
  note       TEXT
);

CREATE TABLE IF NOT EXISTS weight (
  layer          TEXT PRIMARY KEY,                       -- layer name, or '_bias' / '_threshold'
  weight         REAL NOT NULL,
  threshold      REAL,
  tuned_at       INTEGER,                                -- NULL = hand-set default
  tuned_from_run INTEGER                                 -- provenance
);
"#;
```
> Two refinements over the bare ARCHITECTURE.md sketch, both additive and within phase scope: `signal.evidence TEXT` (JSON) is added now because SCORE-05 (Phase 4) requires per-layer evidence and the EAV table is its natural home — adding the nullable column now avoids an early migration; `label.source` is added for the leakage-audit requirement (PITFALLS #11). Both are nullable, so existing inserts are unaffected.

### Round-trip read-back (verifies success criterion #4)
```rust
// Source: docs.rs/rusqlite (query_map)
pub fn read_scores(conn: &Connection, run_id: i64) -> rusqlite::Result<Vec<(String, f64)>> {
    let mut stmt = conn.prepare(
        "SELECT pubkey, score FROM score WHERE run_id = ?1 ORDER BY pubkey")?;
    let rows = stmt.query_map([run_id], |r| Ok((r.get::<_, String>(0)?, r.get::<_, f64>(1)?)))?;
    rows.collect()
}
```

## Schema Sketch (column rationale, serving downstream consumers)

| Table | Key | Affinity notes | Serves (downstream phase) |
|-------|-----|----------------|---------------------------|
| `run` | `run_id INTEGER PK AUTOINCREMENT` | `max_lev_id_*`/`last_cursor` filled by Phase 2; `config_json` (TEXT/JSON) snapshots the weight set per run (Phase 6 / TUNE-03) | Phase 2 resume+drift, Phase 6 reproducibility |
| `pubkey` | `pubkey TEXT PK` | **TEXT, 64-char lowercase hex** — not INTEGER/BLOB. Human-readable in `sqlite3` CLI, joins cleanly, matches the GraphQL `author` field byte-for-byte. BLOB(32) would save ~32 B/row but kills inspectability; not worth it at this scale. | Phase 2 enumeration |
| `score` | `(run_id, pubkey) PK` | `score REAL` (f64), `whitelisted`/`suspected` INTEGER 0/1 (SQLite has no bool) | Phase 4 fused score, Phase 5 export |
| `signal` | `(run_id, pubkey, layer) PK` | **EAV** — `layer TEXT` (stable name), `value REAL`, `evidence TEXT` (JSON). New layer = new rows, **zero migration** (criterion #3). `idx_signal_layer` for per-layer tuner queries | Phase 4/5 evidence, Phase 6 feature matrix |
| `fingerprint` | `(run_id, pubkey, content_hash) PK` | `content_hash`/`simhash` INTEGER (store u64 as i64 — SQLite ints are signed 64-bit; cast on read/write), `minhash BLOB` nullable | Phase 7 cross-pubkey clustering |
| `label` | `pubkey TEXT PK` (run-independent) | `is_spam` INTEGER 0/1, `source` TEXT (provenance), `labeled_at` INTEGER | Phase 6 tuner target |
| `weight` | `layer TEXT PK` (+ `_bias`/`_threshold` rows) | `weight REAL`, `threshold REAL` nullable, provenance cols | Phase 4 reads hand-set, Phase 6 writes tuned |

**u64 ↔ i64 caveat:** SQLite INTEGER is signed 64-bit. `content_hash`/`simhash` are `u64`; store via `as i64` (bit-reinterpret) and read back via `as u64`. Equality/bucketing is unaffected (bit patterns preserved); do **not** do numeric ordering on them as signed. `[ASSUMED]` — standard rusqlite practice; flagged for the planner.

## Migrations / Schema Creation (recommendation)

**Recommendation for v1: single embedded `SCHEMA_DDL` string with `CREATE TABLE IF NOT EXISTS`, run via `conn.execute_batch()` at `Store::open()`.** Rationale:
- Greenfield, one schema version → no upgrade path to manage yet.
- The EAV `signal` table makes the most common future change (new detection layer) a *row insert, not DDL* — the central reason the schema is shaped this way.
- `IF NOT EXISTS` makes `open()` idempotent: re-opening an existing DB is a no-op.

**Adopt `rusqlite_migration` 2.6 at the first real `ALTER TABLE`** (e.g. if a later phase needs a non-additive change). It gives ordered, versioned, idempotent forward migrations tracked via `PRAGMA user_version`. Pre-wiring it now is optional and a judgment call for the planner — the lighter path is recommended unless the team wants the migration harness in place from day one.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `INSERT OR REPLACE` for upsert | `INSERT ... ON CONFLICT DO UPDATE` | SQLite 3.24 (2018) | `OR REPLACE` deletes+reinserts (fires triggers, changes rowid, drops unset columns); `ON CONFLICT DO UPDATE` updates in place — correct for partial idempotent updates `[CITED: sqlite.org/lang_UPSERT.html]` |
| Rollback-journal (default) | WAL mode | mature since SQLite 3.7 | Readers don't block the writer; far better single-writer-many-reader throughput `[CITED: sqlite.org/wal.html]` |

**Deprecated/outdated:** Using `INSERT OR REPLACE` for idempotency — prefer `ON CONFLICT DO UPDATE` to preserve unset columns and avoid trigger/rowid churn.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `content_hash`/`simhash` stored as `i64` via `as` bit-reinterpret is the right u64 storage in SQLite | Schema Sketch | Low — universal rusqlite practice; only affects Phase 7. Equality preserved; avoid signed numeric ordering. |
| A2 | `signal.evidence` JSON `TEXT` column (vs. a separate evidence table) is the right home for SCORE-05 evidence | Schema Sketch / Code Examples | Low — additive nullable column; if evidence grows structured-queryable, a child table can be added later without touching `signal` rows. |
| A3 | ~5–10k rows/transaction is a good batch size | Patterns | Low — tunable at runtime; STACK.md cites 100k+ inserts/sec with batched WAL; final value set by profiling in Phase 3. |
| A4 | `busy_timeout` of ~5s is sufficient headroom | Pattern 1 | Low — single writer means contention is rare; value is config. |

**Note:** All schema/UPSERT/WAL *facts* are `[CITED: sqlite.org]` or `[VERIFIED: crates.io]`; the assumptions above are engineering defaults, not factual claims, and all are low-risk and tunable.

## Open Questions

1. **Evidence column vs. evidence table for SCORE-05.**
   - What we know: SCORE-05 (Phase 4) needs per-layer explanations persisted; the EAV `signal` row is the natural owner.
   - What's unclear: whether evidence must be *queryable* (→ child table) or just *stored & exported* (→ JSON `TEXT`).
   - Recommendation: ship `signal.evidence TEXT` (JSON) now; it satisfies "persisted and exported." Revisit only if Phase 5/6 needs to filter on evidence contents.

2. **Whether to pre-wire `rusqlite_migration` now.**
   - What we know: v1 has one schema version; EAV absorbs most future change as rows.
   - What's unclear: team preference for having the migration harness present from the start.
   - Recommendation: embedded DDL string for v1; adopt the lib at the first `ALTER TABLE`.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Rust/cargo toolchain | building the crate | ✓ (per MEMORY: set PATH + `RUSTUP_TOOLCHAIN` explicitly for `spam*`/worktree agents) | 1.24.1+ | — |
| C compiler (for `bundled` SQLite) | `rusqlite` bundled build | ✓ (clang on macOS/cc on Linux; required to compile the bundled SQLite C source) | system | — |
| `sqlite3` CLI | manual DB inspection (dev convenience) | optional | — | DB Browser for SQLite |

**Missing dependencies with no fallback:** none — `bundled` SQLite removes the system-libsqlite dependency; only a C compiler is needed, which the Rust toolchain assumes.
**Missing dependencies with fallback:** `sqlite3` CLI is convenience only.

> Local filesystem required for the DB file (WAL does not work over network filesystems `[CITED: sqlite.org/wal.html]`). Per MEMORY, native-macOS LMDB access has an `MDB_BAD_RSLOT` issue — that is LMDB/heed (v2), **not** SQLite; SQLite has no such constraint and runs fine on native macOS.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Rust built-in `#[test]` (`cargo test`) + `tempfile` for DB-backed tests |
| Config file | none — Cargo manages tests; add `tempfile` to `[dev-dependencies]` |
| Quick run command | `cargo test --lib store::` |
| Full suite command | `cargo test` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SCORE-02 (criterion 1) | `Store::open()` creates a fresh file with WAL + all 7 tables | integration | `cargo test --lib store::tests::open_creates_wal_and_schema` | ❌ Wave 0 |
| SCORE-02 (criterion 2) | Double-writing `(run_id,pubkey)` and `(run_id,pubkey,layer)` leaves exactly one row | unit/integration | `cargo test --lib store::tests::upsert_is_idempotent` | ❌ Wave 0 |
| SCORE-02 (criterion 3) | Inserting a `signal` with a brand-new `layer` name needs no migration | integration | `cargo test --lib store::tests::new_layer_no_migration` | ❌ Wave 0 |
| SCORE-02 (criterion 4) | A batch of synthetic scores persists and reads back identically | integration | `cargo test --lib store::tests::batch_roundtrip_identity` | ❌ Wave 0 |
| SCORE-02 (determinism) | Two persists of the same batch produce bit-identical tables | integration | `cargo test --lib store::tests::rerun_is_deterministic` | ❌ Wave 0 |

**Observable assertions per criterion:**
- **#1:** after `open(tmp)`, assert `PRAGMA journal_mode` returns `"wal"`, the `-wal` sidecar exists, and `sqlite_master` lists all 7 tables.
- **#2:** insert `(1,"ab..",0.5)` twice into `score`; assert `SELECT count(*) WHERE run_id=1 AND pubkey="ab.."` == 1 and the value reflects the second write. Same for `signal` on `(run_id,pubkey,layer)`.
- **#3:** insert a `signal` row with `layer="L99_brand_new"`; assert it persists and reads back with no DDL executed in between.
- **#4:** persist N synthetic `Score`+`SubScore` records through the writer; `SELECT` them and assert structural+value equality with the inputs (fixed ordering).
- **determinism:** run the same batch twice into two fresh DBs; assert table dumps are byte-equal (or row-set + value equal).

### Sampling Rate
- **Per task commit:** `cargo test --lib store::` (fast, in-temp-file)
- **Per wave merge:** `cargo test`
- **Phase gate:** full suite green + `cargo clippy` clean before `/gsd-verify-work`

### Wave 0 Gaps
- [ ] `src/store/mod.rs` + `tests` module — covers SCORE-02 criteria 1–4 + determinism
- [ ] `[dev-dependencies] tempfile = "3.27"` — DB-backed test substrate
- [ ] Synthetic `Persist`/`Score`/`SubScore` fixtures in the test module
- [ ] No framework install needed — `cargo test` is built in

## Security Domain

> `security_enforcement` is enabled (ASVS Level 1, `block_on: high`). This phase is a local embedded-DB writer with no network surface, no untrusted input at the persistence boundary (data originates from the project's own pipeline), and no auth/session/access-control concerns.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | No auth surface — local file store |
| V3 Session Management | no | No sessions |
| V4 Access Control | no | OS file permissions own the DB file |
| V5 Input Validation | yes (light) | Use **parameterized statements** (`params![]`) for every write — never string-format pubkeys/values into SQL. rusqlite binds parameters, eliminating SQL injection even though inputs are first-party. |
| V6 Cryptography | no | No crypto in this phase (event sigs already verified upstream by strfry) |

### Known Threat Patterns for Rust + SQLite

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via string-built queries | Tampering | Always use `?N` placeholders + `params![]`; never `format!` values into SQL |
| Storing PII/raw event content | Information disclosure | Schema stores only pubkeys + numeric scores + layer evidence — no raw event bodies persisted (data-separation rule: canonical events live only in strfry's LMDB) |
| DB file world-readable | Information disclosure | Rely on default OS umask; the file holds only public-relay-derived pubkeys + scores (no secrets). No special hardening required for v1. |

**Net:** no `high`-severity security items; ASVS L1 satisfied by parameterized queries and the no-raw-content data-separation rule already mandated by `deepfry/CLAUDE.md`.

## Project Constraints (from CLAUDE.md)

From `/Users/g/git/deepfry/CLAUDE.md` (monorepo root — no subproject CLAUDE.md exists):
- **Single-project scope:** all Phase 1 work stays in `/Users/g/git/deepfry/spamhunter/pubkey_iterator`. Do not touch sibling projects (`spam/`, `spam-explorer/`, `web-of-trust/`, `LMDB2GraphQL/`).
- **Commit to main, no branches** (MEMORY): `config.json` `branching_strategy: "none"` agrees.
- **Data-separation rule:** canonical events live only in strfry's LMDB; **no event payloads outside strfry.** → the SQLite store persists pubkeys, scores, per-layer signals, evidence summaries, labels, and weights — **never raw event bodies.** This directly shapes the schema (no `content`/`raw` columns).
- **Rust toolchain gotcha** (MEMORY): for `spam*`/worktree agents, set `PATH` + `RUSTUP_TOOLCHAIN` explicitly; worktree agents may fork a stale base. The planner should ensure build/test tasks export the toolchain.
- **GSD subagents anchor to git root** (MEMORY): use absolute paths under the subproject; do not let artifacts land at the `deepfry` git toplevel.

## Sources

### Primary (HIGH confidence)
- crates.io API (User-Agent'd, 2026-06-25) — verified `max_stable`: `rusqlite` 0.40.1, `rusqlite_migration` 2.6.0, `tempfile` 3.27.0. `gsd-tools query package-legitimacy check` → all `OK`.
- `.planning/research/STACK.md` (2026-06-25) — `rusqlite` bundled choice, sqlx rejection, WAL/`synchronous=NORMAL`/batched-txn pattern, EAV signal table, single-writer rule.
- `.planning/research/ARCHITECTURE.md` (2026-06-25) — the concrete 7-table schema (lifted here), persistence component boundary, idempotency-via-UPSERT design, tuner read/write contract.
- `.planning/research/PITFALLS.md` (2026-06-25) — #12 (determinism/idempotency), #15 (SQLite write contention), persistence-phase mapping.

### Secondary (MEDIUM confidence — official docs)
- `sqlite.org/lang_UPSERT.html` — `ON CONFLICT(...) DO UPDATE SET col=excluded.col`; conflict-target must be UNIQUE/PK/unique index.
- `sqlite.org/wal.html` — WAL one-writer/many-readers, persistent journal mode, `-wal`/`-shm` sidecars + checkpoint, `synchronous=NORMAL` durability tradeoff, no-network-filesystem limitation.
- `sqlite.org/pragma.html` — `journal_mode`, `synchronous`, `busy_timeout`, `foreign_keys`, `temp_store`.
- docs.rs/rusqlite 0.40 — `Connection::open`, `pragma_update`, `execute_batch`, `Transaction`, `prepare_cached`, `query_map`, `params!`.

### Tertiary (LOW confidence)
- u64↔i64 storage convention (A1) — standard practice, not from a single citation; low-risk and tunable.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions verified live against crates.io + legitimacy seam; choice locked by STACK.md.
- Architecture/schema: HIGH — schema already specified in ARCHITECTURE.md; reproduced + minimally extended (additive nullable columns).
- SQLite semantics (UPSERT/WAL/PRAGMA): HIGH — confirmed against official sqlite.org docs this session.
- Pitfalls: HIGH — grounded in the project's own PITFALLS.md (#12, #15).

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (stable ecosystem; rusqlite/SQLite move slowly)
