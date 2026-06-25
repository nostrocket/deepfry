//! Prepared-statement read helpers.
//!
//! The `ORDER BY` clauses are load-bearing: the round-trip and determinism
//! assertions depend on a stable read order. All binding is parameterized
//! (`?N`) — no value is `format!`-interpolated into SQL (T-01-01).

use rusqlite::Connection;

/// Read `(pubkey, score)` for a run, ordered by pubkey.
pub fn read_scores(conn: &Connection, run_id: i64) -> rusqlite::Result<Vec<(String, f64)>> {
    let mut stmt =
        conn.prepare("SELECT pubkey, score FROM score WHERE run_id = ?1 ORDER BY pubkey")?;
    let rows = stmt.query_map([run_id], |r| {
        Ok((r.get::<_, String>(0)?, r.get::<_, f64>(1)?))
    })?;
    rows.collect()
}

/// Read `(pubkey, layer, value)` EAV signals for a run, ordered by `(pubkey, layer)`.
pub fn read_signals(
    conn: &Connection,
    run_id: i64,
) -> rusqlite::Result<Vec<(String, String, f64)>> {
    let mut stmt = conn.prepare(
        "SELECT pubkey, layer, value FROM signal WHERE run_id = ?1 ORDER BY pubkey, layer",
    )?;
    let rows = stmt.query_map([run_id], |r| {
        Ok((
            r.get::<_, String>(0)?,
            r.get::<_, String>(1)?,
            r.get::<_, f64>(2)?,
        ))
    })?;
    rows.collect()
}
