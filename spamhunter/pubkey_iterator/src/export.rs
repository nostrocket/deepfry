//! The `export` materialization (SCORE-03 / D-05): turn a completed run's
//! `score WHERE suspected=1` rows into the reviewable `suspected_spammer` table.
//!
//! `export` is a pure-SQL materialize of an already-computed flag — the
//! `score.suspected` column already records "score > τ at run time" (the run
//! that produced it used the τ snapshotted in `run.config_json`), so this module
//! runs one idempotent `INSERT … SELECT … WHERE suspected = 1`, stamping each row
//! with the run's snapshot τ (denormalized for at-a-glance review) and a
//! score-DESC review rank. Per-layer evidence is NOT copied here — it stays in
//! `signal`, JOINed at read time via `(run_id, pubkey)` (D-05, T-05-12), so a
//! reviewer reads the verdict and joins to the reasons with any SQLite client.
//!
//! Single-writer invariant: `materialize_suspected` runs on a short-lived
//! `Store::export_write_conn` that touches ONLY `suspected_spammer` — never the
//! actor's `score`/`signal`/`pubkey` tables (T-05-10). Every value is bound with
//! `params![]`/`?N`; nothing is `format!`-interpolated into SQL (T-05-09).

use rusqlite::{params, Connection};
use std::time::{SystemTime, UNIX_EPOCH};

/// Current Unix time in whole seconds (the `exported_at` time unit). Saturates
/// to 0 on a pre-epoch clock, matching `store::now_epoch_secs`.
fn now_epoch_secs() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// Read τ from the run's `config_json` reproducibility snapshot (D-06).
///
/// The snapshot is the `{"tau": <f64>, "weights": {...}}` JSON that `run_batch`
/// lands on the canonical run via `set_run_config_json`. A run with no τ snapshot
/// (e.g. the `"{}"` placeholder of an aborted / enumerate-only run) yields a clear
/// error so the CLI can report "did `run` complete?" rather than silently
/// materializing against a bogus threshold (Pitfall 4).
fn read_tau_from_run_snapshot(conn: &Connection, run_id: i64) -> rusqlite::Result<f64> {
    let config_json: String = conn.query_row(
        "SELECT config_json FROM run WHERE run_id = ?1",
        params![run_id],
        |r| r.get::<_, String>(0),
    )?;
    let snapshot: serde_json::Value = serde_json::from_str(&config_json).map_err(|e| {
        rusqlite::Error::InvalidColumnType(
            0,
            format!("run {run_id} config_json is not valid JSON: {e}"),
            rusqlite::types::Type::Text,
        )
    })?;
    snapshot
        .get("tau")
        .and_then(|t| t.as_f64())
        .ok_or_else(|| {
            rusqlite::Error::InvalidColumnType(
                0,
                format!(
                    "run {run_id} has no τ snapshot in config_json — did `run` complete?"
                ),
                rusqlite::types::Type::Null,
            )
        })
}

/// Materialize the suspected-spammer snapshot for `run_id` into `suspected_spammer`.
///
/// Idempotent (Pitfall 5): clears the run's prior rows, then re-inserts from
/// `score WHERE run_id=?1 AND suspected=1`, stamping each row with the run's
/// snapshot τ and a `ROW_NUMBER() OVER (ORDER BY score DESC)` review rank, inside
/// one transaction. Open Q2 resolved: NO whitelist filter — a whitelisted pubkey
/// can still be content-suspected, so the predicate is `suspected = 1` only.
/// Returns the number of rows materialized.
///
/// Every value is `params![]`-bound; the SELECT predicate is the constant
/// `suspected = 1` — nothing is `format!`-interpolated into SQL (T-05-09).
pub fn materialize_suspected(conn: &mut Connection, run_id: i64) -> rusqlite::Result<usize> {
    // τ snapshot for this run (the D-06 point-in-time threshold).
    let tau: f64 = read_tau_from_run_snapshot(conn, run_id)?;

    let tx = conn.transaction()?;
    tx.execute(
        "DELETE FROM suspected_spammer WHERE run_id = ?1",
        params![run_id],
    )?;
    let n = tx.execute(
        "INSERT INTO suspected_spammer (run_id, pubkey, score, tau, rank, exported_at)
         SELECT run_id, pubkey, score, ?2,
                ROW_NUMBER() OVER (ORDER BY score DESC) AS rank,
                ?3
         FROM score
         WHERE run_id = ?1 AND suspected = 1",
        params![run_id, tau, now_epoch_secs()],
    )?;
    tx.commit()?;
    Ok(n)
}

/// Default run selection: the latest COMPLETED run (Pitfall 4 — `max(run_id)`
/// over `status='done'`, never a half-finished `running`/`aborted` run).
///
/// `max()` over an empty set is SQL NULL → `None` (no completed run yet); the CLI
/// turns that into a clear "no completed run to export" error.
pub fn latest_done_run(conn: &Connection) -> rusqlite::Result<Option<i64>> {
    conn.query_row(
        "SELECT max(run_id) FROM run WHERE status = 'done'",
        [],
        |r| r.get::<_, Option<i64>>(0),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::Store;
    use tempfile::TempDir;

    /// Build a fresh temp-FILE SQLite path (mirrors `store::tests::temp_db` — a
    /// temp FILE, not `:memory:`, so WAL sidecars behave).
    fn temp_db() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// Seed a `score` row (and a matching `signal` evidence row) directly on the
    /// export write conn — bypasses the writer actor, which is fine for a test
    /// fixture and keeps the seed synchronous. FK targets (`pubkey`) are inserted
    /// first so `foreign_keys=ON` is satisfied.
    fn seed_scored_pubkey(
        store: &Store,
        run_id: i64,
        pubkey: &str,
        score: f64,
        suspected: bool,
    ) {
        let conn = store.export_write_conn().expect("export write conn");
        conn.execute(
            "INSERT OR IGNORE INTO pubkey (pubkey) VALUES (?1)",
            params![pubkey],
        )
        .expect("insert pubkey");
        conn.execute(
            "INSERT INTO score (run_id, pubkey, score, whitelisted, suspected) \
             VALUES (?1, ?2, ?3, 0, ?4)",
            params![run_id, pubkey, score, suspected as i64],
        )
        .expect("insert score");
        conn.execute(
            "INSERT INTO signal (run_id, pubkey, layer, value, evidence) \
             VALUES (?1, ?2, 'L1_near_dup', ?3, ?4)",
            params![
                run_id,
                pubkey,
                score,
                format!("{{\"reason\":\"seed-{pubkey}\"}}")
            ],
        )
        .expect("insert signal");
    }

    /// SCORE-03: `materialize_suspected` inserts exactly the `suspected=1` pubkeys
    /// for a run, each stamped with the run's snapshot τ and the source score, and
    /// a dense score-DESC rank (1..N).
    #[test]
    fn materialize_selects_suspected() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        // Snapshot carries τ=0.5 (the D-06 reproducibility snapshot).
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");

        // Two suspected (0.9, 0.7) + one not-suspected (0.2).
        let pk_hi = "aa00000000000000000000000000000000000000000000000000000000000009";
        let pk_mid = "aa00000000000000000000000000000000000000000000000000000000000007";
        let pk_lo = "aa00000000000000000000000000000000000000000000000000000000000002";
        seed_scored_pubkey(&store, run_id, pk_hi, 0.9, true);
        seed_scored_pubkey(&store, run_id, pk_mid, 0.7, true);
        seed_scored_pubkey(&store, run_id, pk_lo, 0.2, false);
        store.mark_run_done(run_id, 100).expect("mark done");

        let mut conn = store.export_write_conn().expect("export write conn");
        let n = materialize_suspected(&mut conn, run_id).expect("materialize");
        assert_eq!(n, 2, "exactly the two suspected=1 pubkeys are materialized");

        // Read the materialized rows ordered by rank.
        let mut stmt = conn
            .prepare(
                "SELECT pubkey, score, tau, rank FROM suspected_spammer \
                 WHERE run_id = ?1 ORDER BY rank",
            )
            .unwrap();
        let rows: Vec<(String, f64, f64, i64)> = stmt
            .query_map(params![run_id], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?))
            })
            .unwrap()
            .map(|r| r.unwrap())
            .collect();

        assert_eq!(rows.len(), 2, "two rows present");
        // rank 1 == highest score (0.9), rank 2 == next (0.7).
        assert_eq!(rows[0].0, pk_hi, "rank 1 is the highest-score pubkey");
        assert_eq!(rows[0].1, 0.9, "score copied from source");
        assert_eq!(rows[0].2, 0.5, "τ stamped from the run snapshot");
        assert_eq!(rows[0].3, 1, "dense rank starts at 1");
        assert_eq!(rows[1].0, pk_mid, "rank 2 is the next-highest score");
        assert_eq!(rows[1].2, 0.5, "τ stamped from the run snapshot");
        assert_eq!(rows[1].3, 2, "rank is dense 1..N by score DESC");

        // The not-suspected pubkey is NOT materialized.
        let lo_present: i64 = conn
            .query_row(
                "SELECT count(*) FROM suspected_spammer WHERE run_id = ?1 AND pubkey = ?2",
                params![run_id, pk_lo],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(lo_present, 0, "suspected=0 pubkeys are excluded");
    }

    /// SCORE-03: re-exporting a run is idempotent — exactly one row per
    /// (run_id, pubkey) after two materializations (DELETE-then-INSERT, Pitfall 5).
    #[test]
    fn reexport_is_idempotent() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        let pk = "ab00000000000000000000000000000000000000000000000000000000000009";
        seed_scored_pubkey(&store, run_id, pk, 0.9, true);
        store.mark_run_done(run_id, 100).expect("mark done");

        let mut conn = store.export_write_conn().expect("export write conn");
        let n1 = materialize_suspected(&mut conn, run_id).expect("materialize #1");
        let n2 = materialize_suspected(&mut conn, run_id).expect("materialize #2");
        assert_eq!(n1, 1, "first export materializes one row");
        assert_eq!(n2, 1, "second export re-materializes the same one row");

        let total: i64 = conn
            .query_row(
                "SELECT count(*) FROM suspected_spammer WHERE run_id = ?1",
                params![run_id],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(total, 1, "exactly one row per (run_id, pubkey) after re-export");
    }

    /// SCORE-03 / D-05: an exported pubkey's per-layer evidence is JOINable from
    /// `signal` USING (run_id, pubkey) — proving evidence lives in `signal`, never
    /// duplicated into `suspected_spammer` (T-05-12).
    #[test]
    fn evidence_joinable_from_signal() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        let pk = "ac00000000000000000000000000000000000000000000000000000000000009";
        seed_scored_pubkey(&store, run_id, pk, 0.9, true);
        store.mark_run_done(run_id, 100).expect("mark done");

        let mut conn = store.export_write_conn().expect("export write conn");
        materialize_suspected(&mut conn, run_id).expect("materialize");

        // The verdict (suspected_spammer) JOINs to its evidence (signal).
        let (layer, evidence): (String, String) = conn
            .query_row(
                "SELECT sig.layer, sig.evidence \
                 FROM suspected_spammer s JOIN signal sig USING (run_id, pubkey) \
                 WHERE s.run_id = ?1 AND s.pubkey = ?2",
                params![run_id, pk],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .expect("join returns the per-layer evidence row");
        assert_eq!(layer, "L1_near_dup", "evidence layer JOINed from signal");
        assert!(
            evidence.contains("seed-"),
            "evidence JSON lives in signal, not copied into suspected_spammer"
        );

        // suspected_spammer has no evidence/layer column of its own (verdict only).
        let cols: Vec<String> = conn
            .prepare("SELECT name FROM pragma_table_info('suspected_spammer')")
            .unwrap()
            .query_map([], |r| r.get::<_, String>(0))
            .unwrap()
            .map(|r| r.unwrap())
            .collect();
        assert!(
            !cols.iter().any(|c| c == "evidence" || c == "layer"),
            "suspected_spammer holds only the verdict; evidence stays in signal"
        );
    }

    /// SCORE-03: `latest_done_run` returns the max run_id among `status='done'`
    /// runs, ignoring a newer half-finished (`running`) run (Pitfall 4).
    #[test]
    fn default_picks_latest_done_run() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");

        // No runs → None.
        let conn = store.reader().expect("reader");
        assert_eq!(
            latest_done_run(&conn).expect("query"),
            None,
            "no completed run yet → None"
        );
        drop(conn);

        // An older DONE run, then a newer RUNNING (half-finished) run.
        let old_done = store.begin_run("{\"tau\":0.5}").expect("begin old");
        store.mark_run_done(old_done, 10).expect("mark old done");
        let _newer_running = store.begin_run("{\"tau\":0.5}").expect("begin newer running");

        let conn = store.reader().expect("reader");
        assert_eq!(
            latest_done_run(&conn).expect("query"),
            Some(old_done),
            "latest_done_run picks the older DONE run, never the newer running one"
        );
    }

    /// A run with no τ snapshot (the `"{}"` placeholder of an aborted /
    /// enumerate-only run) yields a clear error rather than materializing against
    /// a bogus threshold (Pitfall 4).
    #[test]
    fn missing_tau_snapshot_errors() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run"); // no τ
        store.mark_run_done(run_id, 100).expect("mark done");

        let mut conn = store.export_write_conn().expect("export write conn");
        let err = materialize_suspected(&mut conn, run_id).expect_err("must error with no τ");
        assert!(
            format!("{err}").contains("no τ snapshot")
                || format!("{err}").contains("did `run` complete"),
            "error names the missing τ snapshot (got: {err})"
        );
    }
}
