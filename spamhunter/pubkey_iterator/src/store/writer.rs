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

/// Idempotent L1 fingerprint upsert keyed on `(run_id, pubkey, content_hash)`;
/// the second write's `simhash` wins. `content_hash`/`simhash` are u64-as-i64
/// integers bound with `params![]` (never `format!`-interpolated — T-04-01).
pub(crate) const UPSERT_FINGERPRINT: &str = "
  INSERT INTO fingerprint (run_id, pubkey, content_hash, simhash)
  VALUES (?1, ?2, ?3, ?4)
  ON CONFLICT(run_id, pubkey, content_hash) DO UPDATE SET
    simhash = excluded.simhash";

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

        // Flush-barrier acks: any `Flush` message in this batch is acknowledged
        // only AFTER `tx.commit()`, so every row enqueued before it is durable
        // when the ack lands (D-07 flush-before-cursor ordering, Pitfall 2).
        let mut flush_acks: Vec<flume::Sender<()>> = Vec::new();
        let tx = conn.transaction()?; // BEGIN
        {
            let mut up_pubkey = tx.prepare_cached(UPSERT_PUBKEY)?;
            let mut up_score = tx.prepare_cached(UPSERT_SCORE)?;
            let mut up_signal = tx.prepare_cached(UPSERT_SIGNAL)?;
            let mut up_fingerprint = tx.prepare_cached(UPSERT_FINGERPRINT)?;
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
                    // L1 fingerprints: ensure the pubkey dimension row exists
                    // (FK target), then UPSERT each fingerprint with params![].
                    WriteMsg::Fingerprints(fps) => {
                        for fp in &fps {
                            up_pubkey.execute([&fp.pubkey])?;
                            up_fingerprint.execute(params![
                                fp.run_id,
                                fp.pubkey,
                                fp.content_hash,
                                fp.simhash
                            ])?;
                        }
                    }
                    // Defer the ack until after the commit below.
                    WriteMsg::Flush(ack) => flush_acks.push(ack),
                }
            }
        }
        tx.commit()?; // COMMIT (one fsync of the WAL at synchronous=NORMAL)
        // Now every preceding row is durable — release the flush waiters. A
        // dropped receiver (caller gave up) is harmless.
        for ack in flush_acks {
            let _ = ack.send(());
        }
    }
    Ok(())
}
