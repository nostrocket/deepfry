//! Single-writer batched-transaction actor.
//!
//! One thread owns the only write `Connection`. It drains the channel into a
//! batch buffer and commits ~`BATCH` upserts per `rusqlite::Transaction`, using
//! `prepare_cached` and the 3 UPSERT consts. Subscores are iterated in the
//! `Vec`'s FIXED order (never a HashMap) so re-runs are deterministic.
//!
//! T-01-01 mitigation: every value is bound with `params![]` / `?N` — no
//! pubkey, layer name, or value is ever `format!`-interpolated into SQL.

use crate::model::WriteMsg;
use rusqlite::{params, Connection};

/// Batch size: number of upserts committed per transaction (tunable, RESEARCH A3).
pub(crate) const BATCH: usize = 8192;

/// Idempotent score upsert keyed on `(run_id, pubkey)`; second write wins.
pub(crate) const UPSERT_SCORE: &str = "
  INSERT INTO score (run_id, pubkey, score, whitelisted, suspected)
  VALUES (?1, ?2, ?3, ?4, ?5)
  ON CONFLICT(run_id, pubkey) DO UPDATE SET
    score       = excluded.score,
    whitelisted = excluded.whitelisted,
    suspected   = excluded.suspected";

/// Idempotent EAV signal upsert keyed on `(run_id, pubkey, layer)`; one row per layer.
pub(crate) const UPSERT_SIGNAL: &str = "
  INSERT INTO signal (run_id, pubkey, layer, value, evidence)
  VALUES (?1, ?2, ?3, ?4, ?5)
  ON CONFLICT(run_id, pubkey, layer) DO UPDATE SET
    value    = excluded.value,
    evidence = excluded.evidence";

/// Insert-or-ignore the run-independent pubkey dimension row.
pub(crate) const UPSERT_PUBKEY: &str =
    "INSERT INTO pubkey (pubkey) VALUES (?1) ON CONFLICT(pubkey) DO NOTHING";

/// The single-writer actor loop.
///
/// Block for one message, greedily drain up to `BATCH`, then commit them in one
/// transaction. Loop until the channel closes (all `Sender`s dropped), at which
/// point the final partial batch has already been committed — so a `close()`
/// that drops the sender and joins this thread guarantees all rows are durable
/// before any post-close read.
pub(crate) fn writer_loop(mut conn: Connection, rx: flume::Receiver<WriteMsg>) -> rusqlite::Result<()> {
    let mut buf: Vec<WriteMsg> = Vec::with_capacity(BATCH);
    // Block for one message (loop exits when all senders drop → channel closed),
    // then greedily drain up to BATCH and commit the batch in one transaction.
    while let Ok(first) = rx.recv() {
        buf.push(first);
        while buf.len() < BATCH {
            match rx.try_recv() {
                Ok(m) => buf.push(m),
                Err(_) => break,
            }
        }

        let tx = conn.transaction()?; // BEGIN
        {
            let mut up_pubkey = tx.prepare_cached(UPSERT_PUBKEY)?;
            let mut up_score = tx.prepare_cached(UPSERT_SCORE)?;
            let mut up_signal = tx.prepare_cached(UPSERT_SIGNAL)?;
            for msg in buf.drain(..) {
                match msg {
                    WriteMsg::Score(p) => {
                        up_pubkey.execute([&p.pubkey])?;
                        up_score.execute(params![
                            p.run_id,
                            p.pubkey,
                            p.score,
                            p.whitelisted as i64,
                            p.suspected as i64
                        ])?;
                        // FIXED layer order (iterate the Vec as-is) → deterministic.
                        for s in &p.subscores {
                            up_signal.execute(params![
                                p.run_id, p.pubkey, s.layer, s.value, s.evidence
                            ])?;
                        }
                    }
                    // D-04: pubkey-dimension rows only, via the same UPSERT_PUBKEY
                    // const (INSERT OR IGNORE) — no score/signal touched.
                    WriteMsg::Pubkeys(pks) => {
                        for pk in &pks {
                            up_pubkey.execute([pk])?;
                        }
                    }
                }
            }
        }
        tx.commit()?; // COMMIT (one fsync of the WAL at synchronous=NORMAL)
    }
    Ok(())
}
