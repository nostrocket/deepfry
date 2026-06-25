//! Embedded schema DDL.
//!
//! `SCHEMA_DDL` is lifted verbatim from `01-RESEARCH.md` "Code Examples → Schema
//! creation" — the 7 `CREATE TABLE IF NOT EXISTS` (run, pubkey, score, signal,
//! fingerprint, label, weight) plus `idx_signal_layer` and `idx_fp_chash`. Every
//! table uses `IF NOT EXISTS` so `Store::open` is idempotent.
//!
//! The `signal` table is intentionally EAV (`run_id, pubkey, layer, value,
//! evidence`): a brand-new detection layer is a new ROW, never a schema
//! migration (SCORE-02 criterion #3).

/// The full schema: 7 tables + 2 indexes, run once via `conn.execute_batch`.
pub const SCHEMA_DDL: &str = r#"
CREATE TABLE IF NOT EXISTS run (
  run_id            INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at        INTEGER NOT NULL,
  finished_at       INTEGER,
  max_lev_id_start  INTEGER,
  max_lev_id_end    INTEGER,
  last_cursor       TEXT,
  config_json       TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'running'   -- running | done | aborted
);

CREATE TABLE IF NOT EXISTS pubkey (
  pubkey TEXT PRIMARY KEY                              -- 64-char lowercase hex
);

CREATE TABLE IF NOT EXISTS score (
  run_id      INTEGER NOT NULL REFERENCES run(run_id),
  pubkey      TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score       REAL    NOT NULL,                        -- sigmoid(Σwᵢxᵢ+b) ∈ [0,1]
  whitelisted INTEGER NOT NULL,                        -- 0/1, denormalized L0
  suspected   INTEGER NOT NULL,                        -- score > τ at run time
  PRIMARY KEY (run_id, pubkey)
);

CREATE TABLE IF NOT EXISTS signal (
  run_id   INTEGER NOT NULL REFERENCES run(run_id),
  pubkey   TEXT    NOT NULL REFERENCES pubkey(pubkey),
  layer    TEXT    NOT NULL,                           -- e.g. 'L1_near_dup' (stable contract)
  value    REAL    NOT NULL,                           -- xᵢ ∈ [0,1]
  evidence TEXT,                                        -- JSON: per-layer explanation (SCORE-05)
  PRIMARY KEY (run_id, pubkey, layer)
);
CREATE INDEX IF NOT EXISTS idx_signal_layer ON signal(layer);

CREATE TABLE IF NOT EXISTS fingerprint (
  run_id       INTEGER NOT NULL REFERENCES run(run_id),
  pubkey       TEXT    NOT NULL REFERENCES pubkey(pubkey),
  content_hash INTEGER NOT NULL,                        -- xxh3 of normalized content
  simhash      INTEGER NOT NULL,                        -- 64-bit SimHash
  minhash      BLOB,                                    -- packed MinHash sig (Phase 7, optional)
  PRIMARY KEY (run_id, pubkey, content_hash)
);
CREATE INDEX IF NOT EXISTS idx_fp_chash ON fingerprint(run_id, content_hash);

CREATE TABLE IF NOT EXISTS label (
  pubkey     TEXT PRIMARY KEY REFERENCES pubkey(pubkey),
  is_spam    INTEGER NOT NULL,                          -- 1 spam, 0 ham
  labeled_at INTEGER NOT NULL,
  source     TEXT,                                       -- label provenance (leakage audit)
  note       TEXT
);

CREATE TABLE IF NOT EXISTS weight (
  layer          TEXT PRIMARY KEY,                       -- layer name, or '_bias' / '_threshold'
  weight         REAL NOT NULL,
  threshold      REAL,
  tuned_at       INTEGER,                                -- NULL = hand-set default
  tuned_from_run INTEGER                                 -- provenance
);
"#;
