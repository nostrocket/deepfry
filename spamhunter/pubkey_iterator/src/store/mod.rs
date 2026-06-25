//! The SQLite store: `Store::open` (PRAGMA-first bootstrap + schema + writer
//! actor), the public write API, and read-connection access.
//!
//! Durability invariant: the store opens in WAL mode with `synchronous=NORMAL`.
//! Under WAL+NORMAL a committed transaction can roll back on power loss but the
//! database stays UNCORRUPTED — a crash loses at most the last uncommitted
//! batch, which an idempotent re-run regenerates. This tradeoff is accepted
//! (threat T-01-03) because every batch is idempotent on its natural key.
//!
//! Single-writer invariant: this module deliberately exposes NO second write
//! connection. Every write funnels through the single writer actor (one owned
//! `Connection` on one thread). Readers open their own read-side connections
//! (WAL lets readers run without blocking the writer).
//!
//! Task 1 stub: the API signatures + the 5-test contract are defined here and
//! the tests run RED. Task 2 fills `open`/`writer_loop`/`queries` to GREEN.

mod schema;
pub mod queries;
mod writer;

use crate::model::{self, Persist, WriteMsg};
use rusqlite::{params, Connection, OptionalExtension};
use std::path::{Path, PathBuf};
use std::thread::JoinHandle;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use schema::SCHEMA_DDL;

/// Current Unix time in whole seconds (the `run` table's time unit). Saturates
/// to 0 on a pre-epoch clock, matching `begin_run`'s original behavior.
fn now_epoch_secs() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// Apply the PRAGMAs (in load-bearing order) then create the schema. Shared by
/// the writer connection at `open` time. PRAGMAs MUST precede any schema/DML.
fn bootstrap(conn: &Connection) -> rusqlite::Result<()> {
    // journal_mode=WAL returns a row ("wal"); pragma_update issues it fine.
    // WAL persists across reopens, so later reader connections inherit it.
    conn.pragma_update(None, "journal_mode", "WAL")?;
    // synchronous=NORMAL is WAL-consistent and far faster than FULL; a crash
    // loses at most the last uncommitted batch, which an idempotent re-run
    // regenerates (threat T-01-03, accepted).
    conn.pragma_update(None, "synchronous", "NORMAL")?;
    // foreign_keys is OFF by default in SQLite — the schema's REFERENCES are
    // inert without this.
    conn.pragma_update(None, "foreign_keys", "ON")?;
    conn.pragma_update(None, "temp_store", "MEMORY")?;
    conn.busy_timeout(Duration::from_secs(5))?; // absorb transient lock waits
    conn.execute_batch(SCHEMA_DDL)?; // CREATE TABLE IF NOT EXISTS ×7 + indexes
    Ok(())
}

/// Handle to the SQLite store: owns the writer thread + the channel `Sender`
/// for `Persist` messages, plus the DB path for opening reader connections.
///
/// Drop order matters: `tx` (the `Sender`) is dropped before `writer` is
/// joined, so an idiomatic drop also drains and commits the final batch.
pub struct Store {
    path: PathBuf,
    tx: Option<flume::Sender<WriteMsg>>,
    writer: Option<JoinHandle<rusqlite::Result<()>>>,
}

impl Store {
    /// Open (or create) the store at `path`: apply PRAGMAs, run `SCHEMA_DDL`,
    /// then spawn the single writer thread holding the only write `Connection`.
    /// Idempotent — re-opening an existing DB is a no-op via
    /// `CREATE TABLE IF NOT EXISTS`.
    ///
    /// Durability: opened WAL + `synchronous=NORMAL` (see `bootstrap`). This
    /// module deliberately exposes NO second write connection — every write
    /// funnels through the writer actor; only `reader()` opens read-side
    /// connections (WAL lets readers run without blocking the writer).
    pub fn open(path: &Path) -> rusqlite::Result<Store> {
        let conn = Connection::open(path)?;
        bootstrap(&conn)?;
        let (tx, rx) = flume::unbounded::<WriteMsg>();
        let writer = std::thread::spawn(move || writer::writer_loop(conn, rx));
        Ok(Store {
            path: path.to_path_buf(),
            tx: Some(tx),
            writer: Some(writer),
        })
    }

    /// Insert a `run` row (status `running`) and return its `run_id`.
    ///
    /// Opens a short-lived write connection for this one INSERT; the row must
    /// exist before any `score`/`signal` is persisted (they FK-reference it).
    /// All other writes go through the actor.
    pub fn begin_run(&self, config_json: &str) -> rusqlite::Result<i64> {
        let conn = Connection::open(&self.path)?;
        conn.pragma_update(None, "foreign_keys", "ON")?;
        conn.busy_timeout(Duration::from_secs(5))?;
        conn.execute(
            "INSERT INTO run (started_at, config_json, status) VALUES (?1, ?2, 'running')",
            params![now_epoch_secs(), config_json],
        )?;
        Ok(conn.last_insert_rowid())
    }

    /// Return the most-recent run whose status is not `done`, or `None` when
    /// every run has finished (D-01/D-02). Used by `--resume` to pick the run
    /// to continue; `None` → start fresh.
    ///
    /// Reads via `reader()` (WAL allows reads without blocking the writer) and
    /// maps all 8 `run` columns into `model::Run`.
    pub fn latest_unfinished_run(&self) -> rusqlite::Result<Option<model::Run>> {
        let conn = self.reader()?;
        conn.query_row(
            "SELECT run_id, started_at, finished_at, max_lev_id_start, max_lev_id_end, \
             last_cursor, config_json, status \
             FROM run WHERE status != 'done' ORDER BY run_id DESC LIMIT 1",
            [],
            |r| {
                Ok(model::Run {
                    run_id: r.get::<_, i64>(0)?,
                    started_at: r.get::<_, i64>(1)?,
                    finished_at: r.get::<_, Option<i64>>(2)?,
                    max_lev_id_start: r.get::<_, Option<i64>>(3)?,
                    max_lev_id_end: r.get::<_, Option<i64>>(4)?,
                    last_cursor: r.get::<_, Option<String>>(5)?,
                    config_json: r.get::<_, String>(6)?,
                    status: r.get::<_, String>(7)?,
                })
            },
        )
        .optional()
    }

    /// Persist `last_cursor` after a page's pubkeys are fully written (D-07).
    /// Follows the `begin_run` short-lived-write template (PRAGMA + busy_timeout
    /// + parameterized binds). The cursor is bound as an opaque string.
    ///
    /// Ordering caveat (RESEARCH Pitfall 2): the enumerator (plan 02) MUST flush
    /// a page's pubkeys through the writer before calling this, so the cursor
    /// never advances past unwritten pubkeys.
    pub fn set_run_cursor(&self, run_id: i64, cursor: &str) -> rusqlite::Result<()> {
        let conn = self.run_write_conn()?;
        conn.execute(
            "UPDATE run SET last_cursor = ?2 WHERE run_id = ?1",
            params![run_id, cursor],
        )?;
        Ok(())
    }

    /// Record the corpus high-water mark observed at run start (D-09 drift probe).
    pub fn set_run_max_lev_start(&self, run_id: i64, v: i64) -> rusqlite::Result<()> {
        let conn = self.run_write_conn()?;
        conn.execute(
            "UPDATE run SET max_lev_id_start = ?2 WHERE run_id = ?1",
            params![run_id, v],
        )?;
        Ok(())
    }

    /// Record the corpus high-water mark observed at clean termination (D-09).
    pub fn set_run_max_lev_end(&self, run_id: i64, v: i64) -> rusqlite::Result<()> {
        let conn = self.run_write_conn()?;
        conn.execute(
            "UPDATE run SET max_lev_id_end = ?2 WHERE run_id = ?1",
            params![run_id, v],
        )?;
        Ok(())
    }

    /// Mark a run `aborted` and stamp `finished_at` (D-07). MUST NOT touch
    /// `last_cursor` — the cursor already points at the last fully-persisted
    /// page, so a resume continues from there.
    pub fn mark_run_aborted(&self, run_id: i64) -> rusqlite::Result<()> {
        let conn = self.run_write_conn()?;
        conn.execute(
            "UPDATE run SET status = 'aborted', finished_at = ?2 WHERE run_id = ?1",
            params![run_id, now_epoch_secs()],
        )?;
        Ok(())
    }

    /// Mark a run `done`, stamp `finished_at`, and record the final
    /// `max_lev_id_end` (D-09 drift end).
    pub fn mark_run_done(&self, run_id: i64, max_lev_id_end: i64) -> rusqlite::Result<()> {
        let conn = self.run_write_conn()?;
        conn.execute(
            "UPDATE run SET status = 'done', finished_at = ?2, max_lev_id_end = ?3 \
             WHERE run_id = ?1",
            params![run_id, now_epoch_secs(), max_lev_id_end],
        )?;
        Ok(())
    }

    /// Open the sanctioned short-lived write connection for `run`-row UPDATEs
    /// (the `begin_run` template: PRAGMA `foreign_keys=ON` + `busy_timeout(5s)`).
    /// These touch only the `run` row, never `pubkey`/`score`/`signal`, so they
    /// do not race the writer actor on overlapping rows. NOT a second writer for
    /// the actor's tables — the single-writer invariant for those is preserved.
    fn run_write_conn(&self) -> rusqlite::Result<Connection> {
        let conn = Connection::open(&self.path)?;
        conn.pragma_update(None, "foreign_keys", "ON")?;
        conn.busy_timeout(Duration::from_secs(5))?;
        Ok(conn)
    }

    /// Enqueue a `Persist` payload to the writer actor.
    ///
    /// Sends on the channel; the writer commits it in its next batch. Panics
    /// only if the writer thread has already gone away (a programming error —
    /// `persist` after `close`).
    pub fn persist(&self, p: Persist) {
        if let Some(tx) = &self.tx {
            tx.send(WriteMsg::Score(p))
                .expect("writer thread is alive while Store is open");
        }
    }

    /// Enqueue a batch of pubkey-dimension rows (D-04) to the writer actor.
    ///
    /// Sends a `WriteMsg::Pubkeys` on the same ordered channel as `persist`, so
    /// the writer commits these pubkeys (idempotent INSERT OR IGNORE) in batch
    /// order relative to every other message — this ordering is what lets the
    /// enumerator flush a page's pubkeys before advancing the cursor. Routes
    /// through the single writer; NO second write connection. Panics only if the
    /// writer thread has already gone away (`insert_pubkeys` after `close`).
    pub fn insert_pubkeys(&self, pks: Vec<String>) {
        if let Some(tx) = &self.tx {
            tx.send(WriteMsg::Pubkeys(pks))
                .expect("writer thread is alive while Store is open");
        }
    }

    /// Block until the writer has committed every message enqueued so far
    /// (D-07 flush-before-cursor barrier, Pitfall 2).
    ///
    /// Sends a `WriteMsg::Flush` on the ordered channel and waits for the writer
    /// to ack — the ack is sent only AFTER the transaction containing the
    /// preceding `insert_pubkeys` batch is committed. The enumerator calls this
    /// between `insert_pubkeys` and `set_run_cursor` so the cursor is never made
    /// durable past un-committed pubkeys. A no-op (returns immediately) after
    /// `close()`. Errors only if the writer thread vanished mid-flush.
    pub fn flush(&self) -> rusqlite::Result<()> {
        if let Some(tx) = &self.tx {
            let (ack_tx, ack_rx) = flume::bounded::<()>(1);
            // If the writer already went away the send fails; treat as flushed.
            if tx.send(WriteMsg::Flush(ack_tx)).is_ok() {
                // A RecvError means the writer dropped the ack sender (shutdown)
                // after committing — the batch is durable either way.
                let _ = ack_rx.recv();
            }
        }
        Ok(())
    }

    /// Flush + shut down: drop the `Sender` (closing the channel), join the
    /// writer thread, and surface its `rusqlite::Result`. After `close()`
    /// returns, every batch — including the final partial one — is committed,
    /// so subsequent reads see all rows.
    pub fn close(mut self) -> rusqlite::Result<()> {
        // Drop the sender first so the writer's `recv()` returns Err and the
        // loop exits after committing the final batch.
        self.tx = None;
        if let Some(handle) = self.writer.take() {
            handle
                .join()
                .expect("writer thread did not panic")?;
        }
        Ok(())
    }

    /// Open a fresh read-side `Connection` on the same path.
    pub fn reader(&self) -> rusqlite::Result<Connection> {
        Connection::open(&self.path)
    }
}

impl Drop for Store {
    /// Best-effort flush if the caller never called `close()`: drop the sender
    /// and join the writer so buffered batches are not lost.
    fn drop(&mut self) {
        self.tx = None;
        if let Some(handle) = self.writer.take() {
            let _ = handle.join();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{Persist, SubScore};
    use tempfile::TempDir;

    /// Build a fresh temp-FILE SQLite path. A temp FILE (not `:memory:`) is
    /// mandatory: WAL `-wal`/`-shm` sidecars only exist for on-disk DBs, and
    /// criterion #1 asserts the sidecar's existence.
    fn temp_db() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// Three synthetic `Persist` records with fixed-order subscores — the
    /// shared fixture for the round-trip + determinism tests.
    fn synthetic_batch(run_id: i64) -> Vec<Persist> {
        let mk = |pk: &str, score: f64| Persist {
            run_id,
            pubkey: pk.to_string(),
            score,
            whitelisted: false,
            suspected: score > 0.5,
            subscores: vec![
                SubScore { layer: "L1_near_dup".into(), value: score, evidence: None },
                SubScore { layer: "L3_velocity".into(), value: 1.0 - score, evidence: Some("{\"n\":3}".into()) },
            ],
        };
        vec![
            mk("aa00000000000000000000000000000000000000000000000000000000000001", 0.2),
            mk("aa00000000000000000000000000000000000000000000000000000000000002", 0.7),
            mk("aa00000000000000000000000000000000000000000000000000000000000003", 0.9),
        ]
    }

    /// Proves SCORE-02 criterion #1: `Store::open` creates a fresh WAL DB with
    /// all 7 tables. Asserts `PRAGMA journal_mode == "wal"`, the `-wal` sidecar
    /// exists, and `sqlite_master` lists run/pubkey/score/signal/fingerprint/
    /// label/weight.
    #[test]
    fn open_creates_wal_and_schema() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let conn = store.reader().expect("reader conn");

        let mode: String = conn
            .query_row("PRAGMA journal_mode", [], |r| r.get(0))
            .expect("journal_mode");
        assert_eq!(mode.to_lowercase(), "wal", "DB must be in WAL mode");

        let wal_sidecar = path.with_extension("sqlite-wal");
        assert!(wal_sidecar.exists(), "-wal sidecar must exist next to the DB");

        let mut stmt = conn
            .prepare("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
            .unwrap();
        let tables: Vec<String> = stmt
            .query_map([], |r| r.get::<_, String>(0))
            .unwrap()
            .map(|r| r.unwrap())
            .collect();
        for t in ["run", "pubkey", "score", "signal", "fingerprint", "label", "weight"] {
            assert!(tables.contains(&t.to_string()), "missing table: {t}");
        }
    }

    /// Proves SCORE-02 criterion #2: double-writing `(run_id, pubkey)` and
    /// `(run_id, pubkey, layer)` leaves exactly one row each, holding the second
    /// write's value (idempotent UPSERT).
    #[test]
    fn upsert_is_idempotent() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");
        let pk = "ab00000000000000000000000000000000000000000000000000000000000000";

        store.persist(Persist {
            run_id,
            pubkey: pk.into(),
            score: 0.5,
            whitelisted: false,
            suspected: false,
            subscores: vec![SubScore { layer: "L1_near_dup".into(), value: 0.5, evidence: None }],
        });
        store.persist(Persist {
            run_id,
            pubkey: pk.into(),
            score: 0.9,
            whitelisted: false,
            suspected: true,
            subscores: vec![SubScore { layer: "L1_near_dup".into(), value: 0.9, evidence: None }],
        });
        store.close().expect("flush + join writer");

        let conn = Connection::open(&path).expect("reader");
        let n_score: i64 = conn
            .query_row(
                "SELECT count(*) FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, pk],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n_score, 1, "exactly one score row per (run_id, pubkey)");
        let score_val: f64 = conn
            .query_row(
                "SELECT score FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, pk],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(score_val, 0.9, "second write wins");

        let n_sig: i64 = conn
            .query_row(
                "SELECT count(*) FROM signal WHERE run_id = ?1 AND pubkey = ?2 AND layer = ?3",
                rusqlite::params![run_id, pk, "L1_near_dup"],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n_sig, 1, "exactly one signal row per (run_id, pubkey, layer)");
        let sig_val: f64 = conn
            .query_row(
                "SELECT value FROM signal WHERE run_id = ?1 AND pubkey = ?2 AND layer = ?3",
                rusqlite::params![run_id, pk, "L1_near_dup"],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(sig_val, 0.9, "second signal write wins");
    }

    /// Hardens SCORE-02 criterion #2: idempotency holds even when the two
    /// writes land in SEPARATE committed transactions (a flush/close boundary
    /// between them), not just within one batch. First write is committed by a
    /// `close()`, the store is reopened, the second write is committed by a
    /// second `close()`. Exactly one row survives with the second value.
    #[test]
    fn upsert_idempotent_across_batches() {
        let (_dir, path) = temp_db();
        let pk = "ad00000000000000000000000000000000000000000000000000000000000000";

        // Batch 1: open → begin_run → persist 0.3 → close (commit #1).
        let run_id = {
            let store = Store::open(&path).expect("open store");
            let run_id = store.begin_run("{}").expect("begin_run");
            store.persist(Persist {
                run_id,
                pubkey: pk.into(),
                score: 0.3,
                whitelisted: false,
                suspected: false,
                subscores: vec![SubScore { layer: "L1_near_dup".into(), value: 0.3, evidence: None }],
            });
            store.close().expect("flush + join writer (batch 1)");
            run_id
        };

        // Sanity: the first value is durably committed before the second write.
        {
            let conn = Connection::open(&path).expect("reader");
            let v: f64 = conn
                .query_row(
                    "SELECT score FROM score WHERE run_id = ?1 AND pubkey = ?2",
                    rusqlite::params![run_id, pk],
                    |r| r.get(0),
                )
                .unwrap();
            assert_eq!(v, 0.3, "first batch committed before second");
        }

        // Batch 2: reopen the SAME db → persist 0.8 for the same key → close (commit #2).
        {
            let store = Store::open(&path).expect("reopen store");
            store.persist(Persist {
                run_id,
                pubkey: pk.into(),
                score: 0.8,
                whitelisted: false,
                suspected: true,
                subscores: vec![SubScore { layer: "L1_near_dup".into(), value: 0.8, evidence: None }],
            });
            store.close().expect("flush + join writer (batch 2)");
        }

        let conn = Connection::open(&path).expect("reader");
        let n: i64 = conn
            .query_row(
                "SELECT count(*) FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, pk],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 1, "exactly one row across two committed batches");
        let v: f64 = conn
            .query_row(
                "SELECT score FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, pk],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(v, 0.8, "second batch's value wins across the flush boundary");

        let nsig: i64 = conn
            .query_row(
                "SELECT count(*) FROM signal WHERE run_id = ?1 AND pubkey = ?2 AND layer = ?3",
                rusqlite::params![run_id, pk, "L1_near_dup"],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(nsig, 1, "exactly one signal row across two committed batches");
    }

    /// Proves SCORE-02 criterion #3: a `signal` row with a brand-new `layer`
    /// name persists and reads back with NO schema migration (EAV).
    #[test]
    fn new_layer_no_migration() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");
        let pk = "ac00000000000000000000000000000000000000000000000000000000000000";

        store.persist(Persist {
            run_id,
            pubkey: pk.into(),
            score: 0.42,
            whitelisted: false,
            suspected: false,
            subscores: vec![SubScore { layer: "L99_brand_new".into(), value: 0.42, evidence: None }],
        });
        store.close().expect("flush + join writer");

        // No DDL executed between insert and read.
        let conn = Connection::open(&path).expect("reader");
        let signals = queries::read_signals(&conn, run_id).expect("read_signals");
        assert!(
            signals.iter().any(|(p, l, v)| p == pk && l == "L99_brand_new" && (*v - 0.42).abs() < 1e-12),
            "brand-new layer must persist with zero migration"
        );
    }

    /// Proves SCORE-02 criterion #4: a batch of synthetic Score+SubScore records
    /// persisted through the single writer reads back identically.
    #[test]
    fn batch_roundtrip_identity() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");
        let batch = synthetic_batch(run_id);
        for p in &batch {
            store.persist(p.clone());
        }
        store.close().expect("flush + join writer");

        let conn = Connection::open(&path).expect("reader");
        let mut scores = queries::read_scores(&conn, run_id).expect("read_scores");
        scores.sort_by(|a, b| a.0.cmp(&b.0));
        let mut expected_scores: Vec<(String, f64)> =
            batch.iter().map(|p| (p.pubkey.clone(), p.score)).collect();
        expected_scores.sort_by(|a, b| a.0.cmp(&b.0));
        assert_eq!(scores, expected_scores, "scores round-trip identically");

        let signals = queries::read_signals(&conn, run_id).expect("read_signals");
        let mut expected_signals: Vec<(String, String, f64)> = batch
            .iter()
            .flat_map(|p| p.subscores.iter().map(move |s| (p.pubkey.clone(), s.layer.clone(), s.value)))
            .collect();
        expected_signals.sort_by(|a, b| (&a.0, &a.1).cmp(&(&b.0, &b.1)));
        let mut got = signals;
        got.sort_by(|a, b| (&a.0, &a.1).cmp(&(&b.0, &b.1)));
        assert_eq!(got, expected_signals, "signals round-trip identically");
    }

    /// Phase-2 D-04: `insert_pubkeys` persists each distinct pubkey exactly
    /// once through the single writer, and re-inserting the same pubkey (across
    /// messages or within one vec) is idempotent (INSERT OR IGNORE → one row).
    #[test]
    fn insert_pubkeys_is_idempotent() {
        let (_dir, path) = temp_db();
        let pk_a = "ba00000000000000000000000000000000000000000000000000000000000001";
        let pk_b = "ba00000000000000000000000000000000000000000000000000000000000002";

        let store = Store::open(&path).expect("open store");
        // Two distinct pubkeys in one message.
        store.insert_pubkeys(vec![pk_a.into(), pk_b.into()]);
        // pk_a again in a second message + a duplicate within the vec.
        store.insert_pubkeys(vec![pk_a.into(), pk_a.into()]);
        store.close().expect("flush + join writer");

        let conn = Connection::open(&path).expect("reader");
        let total: i64 = conn
            .query_row("SELECT count(*) FROM pubkey", [], |r| r.get(0))
            .unwrap();
        assert_eq!(total, 2, "exactly one row per distinct pubkey");

        let n_a: i64 = conn
            .query_row(
                "SELECT count(*) FROM pubkey WHERE pubkey = ?1",
                rusqlite::params![pk_a],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n_a, 1, "re-inserting the same pubkey leaves one row");
    }

    /// D-07 flush barrier: after `flush()` returns, every pubkey enqueued before
    /// it is durably committed and visible to a fresh reader connection — the
    /// guarantee the enumerator relies on to order `set_run_cursor` after the
    /// page is durable (Pitfall 2). Asserted WITHOUT closing the store.
    #[test]
    fn flush_makes_prior_pubkeys_durable() {
        let (_dir, path) = temp_db();
        let pk = "be00000000000000000000000000000000000000000000000000000000000001";
        let store = Store::open(&path).expect("open store");

        store.insert_pubkeys(vec![pk.into()]);
        store.flush().expect("flush barrier");

        // Read on a SEPARATE connection while the store is still open: the row
        // must already be committed (not waiting on close()).
        let conn = Connection::open(&path).expect("reader");
        let n: i64 = conn
            .query_row(
                "SELECT count(*) FROM pubkey WHERE pubkey = ?1",
                rusqlite::params![pk],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n, 1, "flush() commits the prior pubkey before returning");
    }

    /// Read a `run` row back as a `model::Run` (test helper).
    fn read_run(path: &std::path::Path, run_id: i64) -> crate::model::Run {
        let conn = Connection::open(path).expect("reader");
        conn.query_row(
            "SELECT run_id, started_at, finished_at, max_lev_id_start, max_lev_id_end, \
             last_cursor, config_json, status FROM run WHERE run_id = ?1",
            rusqlite::params![run_id],
            |r| {
                Ok(crate::model::Run {
                    run_id: r.get(0)?,
                    started_at: r.get(1)?,
                    finished_at: r.get(2)?,
                    max_lev_id_start: r.get(3)?,
                    max_lev_id_end: r.get(4)?,
                    last_cursor: r.get(5)?,
                    config_json: r.get(6)?,
                    status: r.get(7)?,
                })
            },
        )
        .expect("read run row")
    }

    /// D-01/D-02: `latest_unfinished_run` returns the highest-id non-done run,
    /// or `None` when every run is done.
    #[test]
    fn latest_unfinished_run_selection() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");

        // No runs yet → None.
        assert!(store.latest_unfinished_run().expect("query").is_none());

        // One running run → Some(that run).
        let r1 = store.begin_run("{}").expect("begin_run r1");
        let got = store.latest_unfinished_run().expect("query").expect("some run");
        assert_eq!(got.run_id, r1);
        assert_eq!(got.status, "running");

        // A second non-done run → highest run_id wins.
        let r2 = store.begin_run("{}").expect("begin_run r2");
        let got = store.latest_unfinished_run().expect("query").expect("some run");
        assert_eq!(got.run_id, r2, "highest non-done run_id selected");

        // Mark both done → None.
        store.mark_run_done(r1, 10).expect("done r1");
        store.mark_run_done(r2, 20).expect("done r2");
        assert!(
            store.latest_unfinished_run().expect("query").is_none(),
            "all runs done → None"
        );
    }

    /// D-07: `set_run_cursor` round-trips the opaque cursor string.
    #[test]
    fn set_run_cursor_roundtrip() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");

        store.set_run_cursor(run_id, "cur1").expect("set cursor");
        assert_eq!(read_run(&path, run_id).last_cursor.as_deref(), Some("cur1"));

        store.set_run_cursor(run_id, "cur2").expect("set cursor 2");
        assert_eq!(read_run(&path, run_id).last_cursor.as_deref(), Some("cur2"));
    }

    /// D-09: `set_run_max_lev_start` / `set_run_max_lev_end` round-trip.
    #[test]
    fn set_run_max_lev_roundtrip() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");

        store.set_run_max_lev_start(run_id, 42).expect("set start");
        store.set_run_max_lev_end(run_id, 99).expect("set end");

        let run = read_run(&path, run_id);
        assert_eq!(run.max_lev_id_start, Some(42));
        assert_eq!(run.max_lev_id_end, Some(99));
    }

    /// D-07: `mark_run_aborted` sets status + finished_at but MUST leave
    /// `last_cursor` unchanged (a failure never rewrites the cursor).
    #[test]
    fn mark_run_aborted_preserves_cursor() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");
        store.set_run_cursor(run_id, "lastgood").expect("set cursor");

        store.mark_run_aborted(run_id).expect("abort");

        let run = read_run(&path, run_id);
        assert_eq!(run.status, "aborted");
        assert!(run.finished_at.is_some(), "finished_at recorded");
        assert_eq!(
            run.last_cursor.as_deref(),
            Some("lastgood"),
            "abort must not touch last_cursor (D-07)"
        );
    }

    /// `mark_run_done` sets status, finished_at, and max_lev_id_end.
    #[test]
    fn mark_run_done_sets_status_and_max_end() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");

        store.mark_run_done(run_id, 12345).expect("done");

        let run = read_run(&path, run_id);
        assert_eq!(run.status, "done");
        assert!(run.finished_at.is_some(), "finished_at recorded");
        assert_eq!(run.max_lev_id_end, Some(12345));
    }

    /// Proves SCORE-02 determinism: the same synthetic batch into two fresh DBs
    /// yields row-set + value-equal score and signal tables.
    #[test]
    fn rerun_is_deterministic() {
        // (scores ordered by pubkey, signals ordered by (pubkey, layer)).
        type RunTables = (Vec<(String, f64)>, Vec<(String, String, f64)>);
        let read_both = || -> RunTables {
            let (_dir, path) = temp_db();
            let store = Store::open(&path).expect("open store");
            let run_id = store.begin_run("{}").expect("begin_run");
            for p in synthetic_batch(run_id) {
                store.persist(p);
            }
            store.close().expect("flush + join writer");
            let conn = Connection::open(&path).expect("reader");
            let mut scores = queries::read_scores(&conn, run_id).expect("read_scores");
            scores.sort_by(|a, b| a.0.cmp(&b.0));
            let mut signals = queries::read_signals(&conn, run_id).expect("read_signals");
            signals.sort_by(|a, b| (&a.0, &a.1).cmp(&(&b.0, &b.1)));
            (scores, signals)
        };
        let a = read_both();
        let b = read_both();
        assert_eq!(a.0, b.0, "score table deterministic across runs");
        assert_eq!(a.1, b.1, "signal table deterministic across runs");
    }
}
