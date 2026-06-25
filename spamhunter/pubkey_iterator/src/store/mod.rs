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

use crate::model::Persist;
use rusqlite::Connection;
use std::path::{Path, PathBuf};
use std::thread::JoinHandle;

/// Handle to the SQLite store: owns the writer thread + the channel `Sender`
/// for `Persist` messages, plus the DB path for opening reader connections.
pub struct Store {
    path: PathBuf,
    tx: Option<flume::Sender<Persist>>,
    writer: Option<JoinHandle<rusqlite::Result<()>>>,
}

impl Store {
    /// Open (or create) the store at `path`: apply PRAGMAs, run `SCHEMA_DDL`,
    /// then spawn the single writer thread. Idempotent — re-opening an existing
    /// DB is a no-op via `CREATE TABLE IF NOT EXISTS`.
    pub fn open(_path: &Path) -> rusqlite::Result<Store> {
        // Task 1 stub: return a value so the type-checker is satisfied, but the
        // store is non-functional until Task 2. Tests run RED.
        todo!("Store::open is implemented in Task 2 (GREEN)")
    }

    /// Insert a `run` row and return its `run_id`.
    pub fn begin_run(&self, _config_json: &str) -> rusqlite::Result<i64> {
        todo!("begin_run is implemented in Task 2 (GREEN)")
    }

    /// Enqueue a `Persist` payload to the writer actor.
    pub fn persist(&self, _p: Persist) {
        todo!("persist is implemented in Task 2 (GREEN)")
    }

    /// Flush + shut down: drop the `Sender`, join the writer thread, surface its
    /// `rusqlite::Result`. After `close()` all batches are committed.
    pub fn close(self) -> rusqlite::Result<()> {
        todo!("close is implemented in Task 2 (GREEN)")
    }

    /// Open a fresh read-side `Connection` on the same path.
    pub fn reader(&self) -> rusqlite::Result<Connection> {
        Connection::open(&self.path)
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

    /// Proves SCORE-02 determinism: the same synthetic batch into two fresh DBs
    /// yields row-set + value-equal score and signal tables.
    #[test]
    fn rerun_is_deterministic() {
        let read_both = || -> (Vec<(String, f64)>, Vec<(String, String, f64)>) {
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
