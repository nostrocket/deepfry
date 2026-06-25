//! Row-mapped model structs and the writer-channel payload.
//!
//! These mirror the SQLite tables defined in [`crate::store::schema`]. Every
//! struct derives `Debug, Clone, PartialEq` so tests can assert value equality.
//! `serde` derives are added only on the structs that round-trip a JSON column.
//!
//! Booleans (`whitelisted`, `suspected`, `is_spam`) are Rust `bool` here and
//! stored as SQLite `INTEGER` 0/1 (convert with `as i64` on write — SQLite has
//! no native boolean type).
//!
//! `u64 <-> i64` caveat: `content_hash`/`simhash` are conceptually `u64` but
//! SQLite `INTEGER` is signed 64-bit, so they are stored as `i64` via `as`
//! bit-reinterpret and read back via `as u64`. Equality/bucketing is preserved;
//! never apply signed numeric ordering to them.

use serde::{Deserialize, Serialize};

/// A single analysis run — mirrors the `run` table.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Run {
    pub run_id: i64,
    pub started_at: i64,
    pub finished_at: Option<i64>,
    pub max_lev_id_start: Option<i64>,
    pub max_lev_id_end: Option<i64>,
    pub last_cursor: Option<String>,
    /// JSON snapshot of the weight set used for this run.
    pub config_json: String,
    /// `running` | `done` | `aborted`.
    pub status: String,
}

/// A per-pubkey fused score for a run — mirrors the `score` table.
#[derive(Debug, Clone, PartialEq)]
pub struct Score {
    pub run_id: i64,
    pub pubkey: String,
    pub score: f64,
    pub whitelisted: bool,
    pub suspected: bool,
}

/// A per-layer EAV sub-score — mirrors one `signal` row.
///
/// Phase 4 uses `&'static str` layer names in the `Layer` trait; here the
/// persisted form is `String` so it can round-trip from a row.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SubScore {
    pub layer: String,
    pub value: f64,
    /// JSON per-layer explanation (SCORE-05); `None` when no evidence.
    pub evidence: Option<String>,
}

/// A content fingerprint — mirrors the `fingerprint` table.
#[derive(Debug, Clone, PartialEq)]
pub struct Fingerprint {
    pub run_id: i64,
    pub pubkey: String,
    /// `u64` xxh3 stored as `i64` (bit-reinterpret).
    pub content_hash: i64,
    /// 64-bit SimHash stored as `i64`.
    pub simhash: i64,
    /// Packed MinHash signature (Phase 7, optional).
    pub minhash: Option<Vec<u8>>,
}

/// A human-supplied ground-truth label — mirrors the `label` table.
#[derive(Debug, Clone, PartialEq)]
pub struct Label {
    pub pubkey: String,
    pub is_spam: bool,
    pub labeled_at: i64,
    /// Label provenance (leakage audit).
    pub source: Option<String>,
    pub note: Option<String>,
}

/// A tuned (or hand-set) layer weight — mirrors the `weight` table.
#[derive(Debug, Clone, PartialEq)]
pub struct Weight {
    /// Layer name, or `_bias` / `_threshold`.
    pub layer: String,
    pub weight: f64,
    pub threshold: Option<f64>,
    /// `None` = hand-set default.
    pub tuned_at: Option<i64>,
    pub tuned_from_run: Option<i64>,
}

/// The score/signal write payload funnelled to the single writer.
///
/// Phase 3/4 will add `Vec<Fingerprint>` when the analyzer emits them; for
/// Phase 1 the payload carries a `Score` plus its fixed-order `Vec<SubScore>`.
#[derive(Debug, Clone, PartialEq)]
pub struct Persist {
    pub run_id: i64,
    pub pubkey: String,
    pub score: f64,
    pub whitelisted: bool,
    pub suspected: bool,
    pub subscores: Vec<SubScore>,
}

/// The writer-channel payload funnelled to the single writer actor.
///
/// Widening the channel from a bare `Persist` to this enum is the sanctioned
/// single-writer extension point (D-11): every variant is committed by the one
/// writer thread, preserving message ordering. Phase 3 adds variants additively
/// (e.g. a fetch/run-update message) without disturbing existing paths.
#[derive(Debug, Clone)]
pub enum WriteMsg {
    /// The Phase-1 score/signal write path — wraps `Persist` unchanged so no
    /// Phase-1 field moves.
    Score(Persist),
    /// The D-04 enumeration write: pubkey-dimension rows only (idempotent
    /// INSERT OR IGNORE via the existing `UPSERT_PUBKEY` const), carrying no
    /// score/signal payload.
    Pubkeys(Vec<String>),
    /// The L1 content-fingerprint write path (Phase 4, DETECT-02): per-event
    /// `(run_id, pubkey, content_hash, simhash)` rows, committed by the one writer
    /// via `UPSERT_FINGERPRINT` (idempotent on `(run_id, pubkey, content_hash)`).
    /// Additive — moves no existing field, mirrors the `Pubkeys`/`Flush`
    /// extension pattern; preserves the single-writer ordering invariant (D-11).
    Fingerprints(Vec<Fingerprint>),
    /// A flush barrier (D-07 / Pitfall 2): the writer commits every message
    /// enqueued before this one, then acks on the channel. The enumerator awaits
    /// the ack before advancing the run cursor, so the cursor is never made
    /// durable past un-committed pubkeys (flush-before-cursor ordering). Carries
    /// no row data; the writer never `format!`s anything into SQL for it.
    Flush(flume::Sender<()>),
}
