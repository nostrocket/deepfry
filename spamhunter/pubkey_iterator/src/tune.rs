//! The offline logistic tuner (TUNE-02 / TUNE-05, D-03/D-06): the `tune`
//! subcommand's engine. Reads the `signal × backpropagation` join for one run,
//! pivots it into a dense feature matrix in the FIXED [`LAYERS`] order, fits a
//! binary `linfa-logistic` `LogisticRegression` (deterministic L-BFGS, no RNG),
//! then — before adopting anything — backtests the freshly-fit STAGING weights
//! against the full labeled set using the SHARED [`crate::detect::combine`] so
//! the gate measures the exact function live scoring applies.
//!
//! Safety interlock (D-06, the heart of Phase 6): new weights are written to the
//! `weight` table with provenance (`tuned_at`, `tuned_from_run`) ONLY on a STRICT
//! backtest PASS — zero new false negatives AND zero new false positives. ANY
//! regression BLOCKS adoption and is a pure no-op on the live `weight` rows (the
//! FP-averse default the project's dominant risk demands).
//!
//! Determinism (OPS-02 / D-08): the feature columns are a fixed positional `Vec`
//! in [`LAYERS`] order (never a HashMap), the hyperparameters are fixed, and
//! linfa-logistic's L-BFGS + More–Thuente line search uses zero-initialised
//! params with no RNG — so the same fixture fits to byte-identical weights.
//!
//! τ (`_threshold`) is a DECISION threshold, not a fitted logistic parameter
//! (Open Q1, RESEARCH Pattern 2): the fit produces layer weights + bias only and
//! `_threshold` is NEVER rewritten. The backtest reads the live τ from the
//! `_threshold` weight row.
//!
//! Tier (RESEARCH Architectural Responsibility Map): `tune` is pure SQLite +
//! in-process math — NO network, NO async, NO tokio. It runs synchronously like
//! `export`; the weight write goes through the short-lived
//! [`crate::store::Store::weight_write_conn`] that touches ONLY the `weight`
//! table, preserving the single-writer invariant. Every value is bound with
//! `params![]`/`?N`; nothing is `format!`-interpolated into SQL (T-06-02 / V5).

use crate::config::Config;
use crate::detect::{self, BIAS_KEY, THRESHOLD_KEY};
use crate::store::Store;
use linfa::traits::Fit;
use linfa::Dataset;
use linfa_logistic::LogisticRegression;
use ndarray::{Array1, Array2};
use std::collections::BTreeMap;

/// The fixed feature-column order — MUST match `detect::config_layer_keys()`
/// exactly (L0, L1, L3, L4). This positional order is the OPS-02 determinism
/// guarantee: `model.params()[j]` maps back to `LAYERS[j]`, and the same order is
/// what `detect::read_weights` / `ScoringStage::from_config` read the weights in.
pub const LAYERS: [&str; 4] = [
    "L0_whitelist_absence",
    "L1_near_duplicate",
    "L3_content_entropy",
    "L4_link_mention",
];

/// Pivot the sparse `signal × backpropagation` join for `run_id` into dense
/// per-pubkey feature vectors aligned to `layers`, joined to the run-independent
/// labels. A missing `(pubkey, layer)` signal row defaults to `0.0` (Pitfall 4 /
/// sparse_signals_default_to_zero — a layer that wrote no signal contributed
/// nothing to the live score, so 0.0 is the correct neutral feature).
///
/// Returns one `(pubkey, is_spam, features)` per labeled pubkey, ordered by
/// pubkey (the `BTreeMap` iteration order — deterministic). All binds are
/// parameterized; the labels SELECT is a parameterless constant SELECT and the
/// signals SELECT binds only `run_id` (T-06-02 / V5).
pub fn labeled_features(
    conn: &rusqlite::Connection,
    run_id: i64,
    layers: &[&str],
) -> rusqlite::Result<Vec<(String, bool, Vec<f64>)>> {
    // 1. Run-independent labels: a parameterless constant SELECT (T-06-02).
    let mut labels: BTreeMap<String, bool> = BTreeMap::new();
    {
        let mut stmt = conn.prepare("SELECT pubkey, is_spam FROM backpropagation")?;
        let rows = stmt.query_map([], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)? != 0))
        })?;
        for row in rows {
            let (pk, is_spam) = row?;
            labels.insert(pk, is_spam);
        }
    }

    // 2. Initialise each labeled pubkey's feature vec to all-0.0 (the sparse
    // default — a layer that wrote no signal contributed nothing, Pitfall 4).
    let mut feats: BTreeMap<String, Vec<f64>> = labels
        .keys()
        .map(|pk| (pk.clone(), vec![0.0; layers.len()]))
        .collect();

    // 3. Fill in this run's signals (binds only run_id — T-06-02). A signal row
    // for an unlabeled pubkey is ignored (no entry in `feats`); a layer not in
    // `layers` (e.g. an unknown EAV name) is skipped, leaving the 0.0 default.
    {
        let mut stmt =
            conn.prepare("SELECT pubkey, layer, value FROM signal WHERE run_id = ?1")?;
        let rows = stmt.query_map(rusqlite::params![run_id], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, f64>(2)?,
            ))
        })?;
        for row in rows {
            let (pk, layer, value) = row?;
            if let Some(v) = feats.get_mut(&pk) {
                if let Some(j) = layers.iter().position(|l| *l == layer) {
                    v[j] = value;
                }
            }
        }
    }

    // 4. Emit one (pubkey, is_spam, features) per labeled pubkey, in pubkey
    // order (the BTreeMap iteration order — deterministic, OPS-02).
    Ok(labels
        .into_iter()
        .map(|(pk, is_spam)| {
            let f = feats.remove(&pk).expect("every label has an initialised vec");
            (pk, is_spam, f)
        })
        .collect())
}

/// A backtest regression: the labeled pubkeys whose STAGING-weight verdict
/// disagrees with the human label in the FP-averse direction.
#[derive(Debug, Clone, PartialEq)]
pub struct Regression {
    /// Confirmed-spam pubkeys the staging weights now leave UNflagged (score ≤ τ).
    pub new_false_negatives: Vec<String>,
    /// Confirmed-ham pubkeys the staging weights now flag (score > τ).
    pub new_false_positives: Vec<String>,
}

/// The outcome of a `tune` invocation, with a human-readable [`Self::summary`].
#[derive(Debug, Clone, PartialEq)]
pub enum TuneReport {
    /// Adopted: the backtest passed and the weight rows were rewritten with the
    /// fitted layer weights + bias and provenance.
    Adopted {
        /// `(layer, weight)` for each of the four LAYERS, in fixed order.
        weights: Vec<(String, f64)>,
        /// The fitted bias written to the `_bias` row.
        bias: f64,
        /// The run whose signals were trained + backtested against.
        run_id: i64,
    },
    /// Blocked by the backtest: a regression was found; the weight table is a
    /// no-op (old weights stay in force).
    BlockedByRegression(Regression),
    /// Blocked by a precondition (single-class labels): nothing fitted, nothing
    /// written.
    BlockedByPrecondition(String),
}

impl TuneReport {
    /// A one-line operator-facing summary (named pubkeys + counts; never event
    /// content — T-06-04).
    pub fn summary(&self) -> String {
        todo!("Task 2 GREEN")
    }
}

/// Fit a binary logistic regression over the labeled feature matrix and return
/// `(layer_weights, bias)` where `weights[j]` pairs with `LAYERS[j]` and `bias`
/// is the intercept. Deterministic (no RNG). Errors on a degenerate fit.
fn fit(
    labeled: &[(String, bool, Vec<f64>)],
    alpha: f64,
    max_iterations: u64,
) -> Result<(Vec<f64>, f64), String> {
    let n = labeled.len();
    if n == 0 {
        return Err("no labeled pubkeys to tune".to_string());
    }
    let n_features = labeled[0].2.len();

    // Single-class guard (Pitfall 3): logistic regression has no decision
    // boundary if every label is the same class. Surface a clear precondition
    // BEFORE fitting (linfa would otherwise error or produce garbage).
    let any_spam = labeled.iter().any(|(_, is_spam, _)| *is_spam);
    let any_ham = labeled.iter().any(|(_, is_spam, _)| !*is_spam);
    if !(any_spam && any_ham) {
        return Err("need both spam and ham labels to tune (single-class label set)".to_string());
    }

    // Build the feature matrix (rows = labeled pubkeys, cols = LAYERS order) and
    // the integer class target. Column order is the fixed positional order the
    // caller passed to `labeled_features`, so params()[j] maps to LAYERS[j].
    let mut x = Array2::<f64>::zeros((n, n_features));
    let mut y = Array1::<usize>::zeros(n);
    for (i, (_, is_spam, xs)) in labeled.iter().enumerate() {
        for (j, v) in xs.iter().enumerate() {
            x[[i, j]] = *v;
        }
        y[i] = *is_spam as usize; // 1 = spam (the positive class), 0 = ham
    }
    let dataset = Dataset::new(x, y);

    // Deterministic L-BFGS (argmin + More–Thuente, no RNG): fixed hyperparameters
    // + zero-initialised params → bit-reproducible (OPS-02 / D-08).
    let model = LogisticRegression::default()
        .alpha(alpha)
        .max_iterations(max_iterations)
        .with_intercept(true)
        .fit(&dataset)
        .map_err(|e| format!("logistic fit failed: {e}"))?;

    let coeffs = model.params(); // &Array1<f64>, one per feature column, LAYERS order
    let bias = model.intercept(); // f64 → the _bias sentinel
    Ok((coeffs.to_vec(), bias))
}

/// STRICT backtest gate (Task 2 GREEN): re-score every labeled pubkey with the
/// STAGING weights via the shared combiner; `Err` on ANY new FN or FP.
fn backtest(
    _labeled: &[(String, bool, Vec<f64>)],
    _staging_w: &[f64],
    _staging_b: f64,
    _tau: f64,
) -> Result<(), Regression> {
    todo!("Task 2 GREEN")
}

/// Adopt the staging weights + bias into the `weight` table with provenance
/// (Task 2 GREEN). Only called on a backtest PASS.
fn write_weights(
    _store: &Store,
    _staging_w: &[f64],
    _staging_b: f64,
    _run_id: i64,
) -> rusqlite::Result<()> {
    todo!("Task 2 GREEN")
}

/// Re-fit layer weights from human labels and adopt them ONLY if the strict
/// backtest passes. See module docs for the full contract.
pub fn run_tune(
    store: &Store,
    config: &Config,
    run_id: Option<i64>,
) -> Result<TuneReport, String> {
    let conn = store.reader().map_err(|e| format!("open reader: {e}"))?;

    // Resolve the run: explicit --run-id, else the latest `done` run (Pitfall 5 —
    // train AND backtest on ONE run's signals, recorded as tuned_from_run).
    let run_id = match run_id {
        Some(rid) => rid,
        None => crate::export::latest_done_run(&conn)
            .map_err(|e| format!("resolve latest done run: {e}"))?
            .ok_or_else(|| "no completed run to tune — run `pubkey_iterator run` first".to_string())?,
    };

    let layers: Vec<&str> = LAYERS.to_vec();
    let labeled =
        labeled_features(&conn, run_id, &layers).map_err(|e| format!("read labeled features: {e}"))?;

    // Single-class / empty guard (Pitfall 3): a blocked-by-precondition is
    // distinct from a backtest regression — nothing is fitted, nothing written.
    let any_spam = labeled.iter().any(|(_, is_spam, _)| *is_spam);
    let any_ham = labeled.iter().any(|(_, is_spam, _)| !*is_spam);
    if labeled.is_empty() {
        return Ok(TuneReport::BlockedByPrecondition(
            "no labeled pubkeys for the chosen run — INSERT ground truth into backpropagation first"
                .to_string(),
        ));
    }
    if !(any_spam && any_ham) {
        return Ok(TuneReport::BlockedByPrecondition(
            "need both spam and ham labels to tune (single-class label set)".to_string(),
        ));
    }

    // Fit the staging weights + bias (deterministic).
    let (alpha, max_iterations) = (1.0_f64, 100_u64);
    let (staging_w, staging_b) =
        fit(&labeled, alpha, max_iterations).map_err(|e| e)?;

    // τ is read from the live `_threshold` row and NEVER re-fit (Open Q1).
    let live_weights =
        detect::read_weights(&conn).map_err(|e| format!("read live weights: {e}"))?;
    let tau = live_weights
        .iter()
        .find(|w| w.layer == THRESHOLD_KEY)
        .and_then(|w| w.threshold)
        .unwrap_or(config.tau);

    // Backtest the STAGING weights against the full labeled set via the SHARED
    // combiner (Task 2 implements `backtest` + the write-on-pass path).
    match backtest(&labeled, &staging_w, staging_b, tau) {
        Err(regression) => Ok(TuneReport::BlockedByRegression(regression)),
        Ok(()) => {
            write_weights(store, &staging_w, staging_b, run_id)
                .map_err(|e| format!("write weights: {e}"))?;
            let weights = LAYERS
                .iter()
                .zip(staging_w.iter())
                .map(|(l, w)| (l.to_string(), *w))
                .collect();
            Ok(TuneReport::Adopted {
                weights,
                bias: staging_b,
                run_id,
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::Store;
    use tempfile::TempDir;

    fn temp_db() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// Seed a labeled pubkey directly on a weight/export-style write conn: insert
    /// the pubkey FK target, the `backpropagation` label, and one `signal` row per
    /// `(layer, value)` for `run_id`. A layer omitted from `signals` writes no
    /// signal row (exercising the sparse → 0.0 default).
    fn seed_labeled(
        store: &Store,
        run_id: i64,
        pubkey: &str,
        is_spam: bool,
        signals: &[(&str, f64)],
    ) {
        let conn = store.weight_write_conn().expect("weight write conn");
        conn.execute(
            "INSERT OR IGNORE INTO pubkey (pubkey) VALUES (?1)",
            rusqlite::params![pubkey],
        )
        .expect("insert pubkey");
        conn.execute(
            "INSERT OR REPLACE INTO backpropagation (pubkey, is_spam, labeled_at, source, note) \
             VALUES (?1, ?2, ?3, ?4, ?5)",
            rusqlite::params![pubkey, is_spam as i64, 1_700_000_000_i64, "test", "synthetic"],
        )
        .expect("insert label");
        for (layer, value) in signals {
            conn.execute(
                "INSERT OR REPLACE INTO signal (run_id, pubkey, layer, value, evidence) \
                 VALUES (?1, ?2, ?3, ?4, NULL)",
                rusqlite::params![run_id, pubkey, layer, value],
            )
            .expect("insert signal");
        }
    }

    fn sample_config() -> Config {
        let body = std::fs::read_to_string(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/pubkey_iterator_config.example.toml"
        ))
        .expect("read example config");
        toml::from_str(&body).expect("parse example config")
    }

    /// A 64-hex pubkey from a short discriminator.
    fn pk(tag: &str) -> String {
        format!("{tag:0>64}")
    }

    /// Fit twice over the same fixture → byte-identical weights + bias
    /// (deterministic L-BFGS, no RNG — D-08/OPS-02).
    #[test]
    fn fit_is_deterministic() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");

        // Separable both-class fixture: spam fires L1+L3 high, ham stays low.
        seed_labeled(&store, run_id, &pk("s1"), true, &[("L1_near_duplicate", 0.95), ("L3_content_entropy", 0.9)]);
        seed_labeled(&store, run_id, &pk("s2"), true, &[("L1_near_duplicate", 0.9), ("L3_content_entropy", 0.95)]);
        seed_labeled(&store, run_id, &pk("h1"), false, &[("L1_near_duplicate", 0.05), ("L3_content_entropy", 0.1)]);
        seed_labeled(&store, run_id, &pk("h2"), false, &[("L1_near_duplicate", 0.1), ("L3_content_entropy", 0.05)]);

        let conn = store.reader().expect("reader");
        let layers: Vec<&str> = LAYERS.to_vec();
        let feats = labeled_features(&conn, run_id, &layers).expect("features");
        assert_eq!(feats.len(), 4, "four labeled pubkeys");

        let (w1, b1) = super::fit(&feats, 1.0, 100).expect("fit 1");
        let (w2, b2) = super::fit(&feats, 1.0, 100).expect("fit 2");
        for (a, b) in w1.iter().zip(w2.iter()) {
            assert!((a - b).abs() < 1e-12, "weights identical across fits: {a} vs {b}");
        }
        assert!((b1 - b2).abs() < 1e-12, "bias identical across fits: {b1} vs {b2}");
        store.close().expect("close");
    }

    /// A labeled pubkey with NO signal row for a layer → that feature is 0.0.
    #[test]
    fn sparse_signals_default_to_zero() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let run_id = store.begin_run("{}").expect("begin_run");

        // Only L1 + L3 signals; L0 (idx 0) and L4 (idx 3) are absent → 0.0.
        seed_labeled(&store, run_id, &pk("sp"), true, &[("L1_near_duplicate", 0.8), ("L3_content_entropy", 0.7)]);

        let conn = store.reader().expect("reader");
        let layers: Vec<&str> = LAYERS.to_vec();
        let feats = labeled_features(&conn, run_id, &layers).expect("features");
        assert_eq!(feats.len(), 1);
        let (_, _, xs) = &feats[0];
        assert_eq!(xs.len(), 4, "one feature per LAYERS entry");
        assert_eq!(xs[0], 0.0, "L0 absent → 0.0");
        assert_eq!(xs[1], 0.8, "L1 present");
        assert_eq!(xs[2], 0.7, "L3 present");
        assert_eq!(xs[3], 0.0, "L4 absent → 0.0 (the load-bearing sparse default)");
        store.close().expect("close");
    }

    /// A single-class label set (all spam) → `run_tune` returns the precondition
    /// block and writes nothing (weight rows unchanged, Pitfall 3).
    #[test]
    fn single_class_labels_blocks_with_message() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();
        detect::seed_weights_if_empty(&store, &config).expect("seed weights");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        store.mark_run_done(run_id, 100).expect("mark done");

        // ONLY spam labels — no ham.
        seed_labeled(&store, run_id, &pk("s1"), true, &[("L1_near_duplicate", 0.9)]);
        seed_labeled(&store, run_id, &pk("s2"), true, &[("L1_near_duplicate", 0.8)]);

        let report = super::run_tune(&store, &config, None).expect("run_tune");
        match &report {
            TuneReport::BlockedByPrecondition(msg) => {
                assert!(
                    msg.contains("both") || msg.contains("class"),
                    "precondition message names the need for both classes: {msg}"
                );
            }
            other => panic!("expected BlockedByPrecondition, got {other:?}"),
        }

        // No write: every weight row still has tuned_at == NULL.
        let conn = store.reader().expect("reader");
        let weights = detect::read_weights(&conn).expect("read weights");
        assert!(
            weights.iter().all(|w| w.tuned_at.is_none()),
            "single-class block writes nothing (all tuned_at still NULL)"
        );
        store.close().expect("close");
    }

    /// A clean separable fixture passes the backtest and the new weights are
    /// adopted with provenance: the four layer rows + the `_bias` row carry
    /// tuned_at != NULL and tuned_from_run == the run; `_threshold` (τ) is
    /// unchanged (NOT re-fit, Open Q1).
    #[test]
    fn tune_writes_weights_with_provenance() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();
        detect::seed_weights_if_empty(&store, &config).expect("seed weights");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        store.mark_run_done(run_id, 100).expect("mark done");

        // Cleanly separable: the current conservative weights ALREADY classify
        // these correctly, and the fit also separates them → backtest PASSES.
        // Spam fires content layers high; ham stays at 0.0 across the board.
        seed_labeled(&store, run_id, &pk("s1"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0), ("L4_link_mention", 1.0)]);
        seed_labeled(&store, run_id, &pk("s2"), true, &[("L1_near_duplicate", 0.95), ("L3_content_entropy", 0.95), ("L4_link_mention", 0.9)]);
        seed_labeled(&store, run_id, &pk("h1"), false, &[("L1_near_duplicate", 0.0), ("L3_content_entropy", 0.0)]);
        seed_labeled(&store, run_id, &pk("h2"), false, &[("L1_near_duplicate", 0.02), ("L3_content_entropy", 0.01)]);

        let report = super::run_tune(&store, &config, None).expect("run_tune");
        match &report {
            TuneReport::Adopted { run_id: r, .. } => assert_eq!(*r, run_id, "adopted from the run"),
            other => panic!("expected Adopted, got {other:?}"),
        }

        let conn = store.reader().expect("reader");
        let weights = detect::read_weights(&conn).expect("read weights");
        // Each of the four layer rows + the _bias row carries provenance.
        for layer in LAYERS.iter().chain(std::iter::once(&BIAS_KEY)) {
            let w = weights.iter().find(|w| w.layer == *layer).expect("row present");
            assert!(w.tuned_at.is_some(), "{layer} carries tuned_at after adopt");
            assert_eq!(w.tuned_from_run, Some(run_id), "{layer} records tuned_from_run");
        }
        // τ row is UNCHANGED (never re-fit) — tuned_at still NULL, threshold 0.5.
        let tau_row = weights.iter().find(|w| w.layer == THRESHOLD_KEY).expect("threshold row");
        assert!(tau_row.tuned_at.is_none(), "_threshold is NOT re-fit (tuned_at still NULL)");
        assert_eq!(tau_row.threshold, Some(0.5), "_threshold (τ) value unchanged");
        store.close().expect("close");
    }

    /// A clean separable fixture → backtest PASSES → adopted (TUNE-05 pass case).
    #[test]
    fn backtest_passes_and_adopts() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();
        detect::seed_weights_if_empty(&store, &config).expect("seed weights");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        store.mark_run_done(run_id, 100).expect("mark done");

        // Strongly separable classes.
        seed_labeled(&store, run_id, &pk("a1"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0)]);
        seed_labeled(&store, run_id, &pk("a2"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 0.9)]);
        seed_labeled(&store, run_id, &pk("a3"), true, &[("L1_near_duplicate", 0.9), ("L3_content_entropy", 1.0)]);
        seed_labeled(&store, run_id, &pk("b1"), false, &[("L1_near_duplicate", 0.0), ("L3_content_entropy", 0.0)]);
        seed_labeled(&store, run_id, &pk("b2"), false, &[("L1_near_duplicate", 0.0), ("L3_content_entropy", 0.05)]);
        seed_labeled(&store, run_id, &pk("b3"), false, &[("L1_near_duplicate", 0.05), ("L3_content_entropy", 0.0)]);

        let report = super::run_tune(&store, &config, None).expect("run_tune");
        assert!(
            matches!(report, TuneReport::Adopted { .. }),
            "separable fixture is adopted, got {report:?}"
        );
        assert!(report.summary().contains("adopt"), "summary names the adoption: {}", report.summary());
        store.close().expect("close");
    }

    /// A known-regression fixture: a labeled-ham pubkey whose signals are high on
    /// a layer the new fit up-weights so the STAGING score crosses τ → a new FP →
    /// the strict gate BLOCKS. Assert the weight rows still carry their PRE-tune
    /// values (tuned_at still NULL) — a blocked adoption is a no-op on live state.
    #[test]
    fn backtest_blocks_regression_and_leaves_weights() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();
        detect::seed_weights_if_empty(&store, &config).expect("seed weights");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        store.mark_run_done(run_id, 100).expect("mark done");

        // Construct a fit that necessarily produces a new FP. Make the classes
        // NON-separable in a way that drags a ham over τ: most spam AND one ham
        // share an identical high feature vector, but carry opposite labels. The
        // fit must up-weight that layer to catch the spam, which then pushes the
        // identically-featured ham above τ → a new false positive → BLOCK.
        seed_labeled(&store, run_id, &pk("sp1"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0)]);
        seed_labeled(&store, run_id, &pk("sp2"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0)]);
        seed_labeled(&store, run_id, &pk("sp3"), true, &[("L1_near_duplicate", 0.95), ("L3_content_entropy", 0.95)]);
        // The adversarial ham: identical strong signals to the spam, labeled ham.
        seed_labeled(&store, run_id, &pk("hm1"), false, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0)]);
        // A clearly-negative ham to keep both classes present.
        seed_labeled(&store, run_id, &pk("hm2"), false, &[("L1_near_duplicate", 0.0), ("L3_content_entropy", 0.0)]);

        let report = super::run_tune(&store, &config, None).expect("run_tune");
        match &report {
            TuneReport::BlockedByRegression(reg) => {
                assert!(
                    !reg.new_false_positives.is_empty() || !reg.new_false_negatives.is_empty(),
                    "regression names at least one regressed pubkey"
                );
            }
            other => panic!("expected BlockedByRegression, got {other:?}"),
        }
        assert!(report.summary().contains("BLOCKED"), "summary says BLOCKED: {}", report.summary());

        // The live weight rows are a NO-OP: never tuned (tuned_at still NULL),
        // and they still hold the seeded conservative values.
        let conn = store.reader().expect("reader");
        let weights = detect::read_weights(&conn).expect("read weights");
        assert!(
            weights.iter().all(|w| w.tuned_at.is_none() && w.tuned_from_run.is_none()),
            "a blocked adoption leaves every weight row untouched (tuned_at/tuned_from_run NULL)"
        );
        let l1 = weights.iter().find(|w| w.layer == "L1_near_duplicate").expect("L1 row");
        assert_eq!(l1.weight, 2.0, "L1 still holds its pre-tune seeded value");
        store.close().expect("close");
    }
}
