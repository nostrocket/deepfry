//! Embedded schema DDL.
//!
//! `SCHEMA_DDL` is the 9 `CREATE TABLE IF NOT EXISTS` (run, pubkey, score,
//! signal, fingerprint, backpropagation, weight, suspected_spammer, review_queue)
//! plus `idx_signal_layer`, `idx_fp_chash`, and `idx_suspected_run`. The first 7
//! tables + 2 indexes were lifted verbatim from `01-RESEARCH.md` "Code Examples →
//! Schema creation"; the `suspected_spammer` table + `idx_suspected_run` are the
//! Phase-5 export materialization (05-RESEARCH §"suspected_spammer schema"). The
//! `backpropagation` table is the Phase-1 operator-label table renamed in Phase 6
//! (D-01, self-explanatory name); `review_queue` is the Phase-6 negative-sampling
//! slice (Plan 03 populates it). Every table uses `IF NOT EXISTS` so `Store::open`
//! is idempotent.
//!
//! The `signal` table is intentionally EAV (`run_id, pubkey, layer, value,
//! evidence`): a brand-new detection layer is a new ROW, never a schema
//! migration (SCORE-02 criterion #3).
//!
//! `suspected_spammer` is a per-run materialization keyed by (run_id, pubkey) —
//! one table, many runs (D-05/D-06). τ and score are denormalized inline so a
//! reviewer reads the verdict without joining `config_json`; per-layer evidence
//! is NOT duplicated here (it stays in `signal`, JOINed at read time).

/// The full schema: 9 tables + 3 indexes, run once via `conn.execute_batch`.
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

-- Operator ground-truth labels (renamed from `label` in Phase 6, D-01: the
-- self-explanatory project name). Humans INSERT rows directly with any SQLite
-- client (TUNE-01 / D-02 — intentionally NO `label` subcommand); the tuner reads
-- them JOINed against `signal`. `source`/`note` retained for a leakage/poisoning
-- audit trail. Columns unchanged from the Phase-1 `label` table (CREATE-rename,
-- no data migration: the old table was empty in every real DB).
CREATE TABLE IF NOT EXISTS backpropagation (
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

CREATE TABLE IF NOT EXISTS suspected_spammer (
  run_id      INTEGER NOT NULL REFERENCES run(run_id),
  pubkey      TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score       REAL    NOT NULL,                          -- sigmoid score at export time
  tau         REAL    NOT NULL,                          -- run's τ threshold (denormalized)
  rank        INTEGER NOT NULL,                          -- descending-score rank within the run
  exported_at INTEGER NOT NULL,                          -- materialization timestamp
  PRIMARY KEY (run_id, pubkey)
);
CREATE INDEX IF NOT EXISTS idx_suspected_run ON suspected_spammer(run_id, rank);

-- Negative-sampling review queue (Phase 6, TUNE-04). Per-run sampled pubkeys a
-- reviewer triages into `backpropagation` ground truth. This plan only CREATEs
-- it; Plan 03 populates it from a run's scored-but-unlabeled tail.
CREATE TABLE IF NOT EXISTS review_queue (
  run_id     INTEGER NOT NULL REFERENCES run(run_id),
  pubkey     TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score      REAL    NOT NULL,                          -- the run's sigmoid score
  sampled_at INTEGER NOT NULL,                          -- sampling timestamp
  PRIMARY KEY (run_id, pubkey)
);
"#;
