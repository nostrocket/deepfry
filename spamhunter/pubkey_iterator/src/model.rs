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

/// The writer-channel payload funnelled to the single writer.
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
