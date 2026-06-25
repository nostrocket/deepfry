//! Single-writer batched-transaction actor.
//!
//! Task 1 stub — `writer_loop` and the UPSERT consts land in Task 2. The
//! signatures are declared now so the crate compiles (RED tests run against the
//! real `Store` surface).

use crate::model::Persist;
use rusqlite::Connection;

/// Batch size: number of upserts committed per transaction (tunable, RESEARCH A3).
pub(crate) const BATCH: usize = 8192;

/// Idempotent score upsert keyed on `(run_id, pubkey)` (filled in Task 2).
pub(crate) const UPSERT_SCORE: &str = "";
/// Idempotent EAV signal upsert keyed on `(run_id, pubkey, layer)` (filled in Task 2).
pub(crate) const UPSERT_SIGNAL: &str = "";
/// Insert-or-ignore pubkey dimension row (filled in Task 2).
pub(crate) const UPSERT_PUBKEY: &str = "";

/// The single-writer actor loop (implemented in Task 2).
pub(crate) fn writer_loop(_conn: Connection, _rx: flume::Receiver<Persist>) -> rusqlite::Result<()> {
    unimplemented!("writer_loop is implemented in Task 2 (GREEN)")
}
