//! Prepared-statement read helpers.
//!
//! Task 1 stub — `read_scores`/`read_signals` are implemented in Task 2. The
//! signatures exist now so the test harness compiles and runs RED.

use rusqlite::Connection;

/// Read `(pubkey, score)` for a run, `ORDER BY pubkey` (implemented in Task 2).
pub fn read_scores(_conn: &Connection, _run_id: i64) -> rusqlite::Result<Vec<(String, f64)>> {
    unimplemented!("read_scores is implemented in Task 2 (GREEN)")
}

/// Read `(pubkey, layer, value)` for a run, `ORDER BY pubkey, layer`
/// (implemented in Task 2).
pub fn read_signals(_conn: &Connection, _run_id: i64) -> rusqlite::Result<Vec<(String, String, f64)>> {
    unimplemented!("read_signals is implemented in Task 2 (GREEN)")
}
