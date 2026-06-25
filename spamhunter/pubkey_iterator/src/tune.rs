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
        match self {
            TuneReport::Adopted {
                weights,
                bias,
                run_id,
            } => {
                let ws: Vec<String> =
                    weights.iter().map(|(l, w)| format!("{l}={w:.4}")).collect();
                format!(
                    "adopted new weights from run {run_id}: [{}] _bias={bias:.4} \
                     (backtest passed: zero new FN, zero new FP)",
                    ws.join(", ")
                )
            }
            TuneReport::BlockedByRegression(reg) => {
                format!(
                    "BLOCKED by backtest regression — weights UNCHANGED. \
                     new false negatives ({}): [{}]; new false positives ({}): [{}]",
                    reg.new_false_negatives.len(),
                    reg.new_false_negatives.join(", "),
                    reg.new_false_positives.len(),
                    reg.new_false_positives.join(", "),
                )
            }
            TuneReport::BlockedByPrecondition(msg) => {
                format!("BLOCKED by precondition — weights UNCHANGED: {msg}")
            }
        }
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

    // Orient the sign to the SPAM class (CRITICAL — correctness, not cosmetic).
    // linfa-logistic's `label_classes` assigns POSITIVE_LABEL to whichever class
    // it counts first (with a count-based, NOT value-based, tiebreak — the doc's
    // "smaller class" claim is misleading); `params()`/`intercept()` are then
    // oriented toward `labels().pos.class`. For a balanced both-class fixture the
    // positive class is therefore ROW-ORDER-dependent, so the raw `params()` may
    // be oriented toward ham. `predict_probabilities` = sigmoid(x·params+b) is the
    // probability of `labels().pos.class`. The combiner the live run applies
    // expects weights oriented toward SPAM (score > τ ⇒ suspected). So if the
    // fitted positive class is NOT spam (==1), negate weights + bias so the
    // returned `(weights, bias)` predict spam — making the tuner's output sign
    // canonical and order-independent (and combine() == predict_probabilities of
    // spam). Without this, a retune could write sign-inverted weights that flag
    // ham and clear spam.
    let pos_is_spam = model.labels().pos.class == 1;
    let coeffs = model.params(); // &Array1<f64>, one per feature column, LAYERS order
    let intercept = model.intercept();
    let (weights, bias): (Vec<f64>, f64) = if pos_is_spam {
        (coeffs.to_vec(), intercept)
    } else {
        (coeffs.iter().map(|w| -w).collect(), -intercept)
    };
    Ok((weights, bias))
}

/// STRICT backtest gate (TUNE-05 / D-06, Open Q2): re-score every labeled pubkey
/// with the STAGING weights via the SHARED [`detect::combine`] (NOT a private
/// sigmoid — the gate must measure the exact function live scoring applies), then
/// compare the staging verdict (`score > τ`) against the human label. ANY new
/// false negative (confirmed-spam now unflagged) OR new false positive
/// (confirmed-ham now flagged) → `Err(Regression)`. `Ok(())` only when BOTH are
/// empty (strict, zero-tolerance — the FP-averse default).
fn backtest(
    labeled: &[(String, bool, Vec<f64>)],
    staging_w: &[f64],
    staging_b: f64,
    tau: f64,
) -> Result<(), Regression> {
    let mut new_false_negatives = Vec::new();
    let mut new_false_positives = Vec::new();
    for (pubkey, is_spam, xs) in labeled {
        // Identical math to live scoring (detect::combine), so the gate measures
        // exactly what the next `run` will compute.
        let flagged = detect::combine(staging_w, staging_b, xs) > tau;
        if *is_spam && !flagged {
            new_false_negatives.push(pubkey.clone()); // confirmed-spam now unflagged
        }
        if !*is_spam && flagged {
            new_false_positives.push(pubkey.clone()); // confirmed-ham now flagged
        }
    }
    if new_false_negatives.is_empty() && new_false_positives.is_empty() {
        Ok(())
    } else {
        Err(Regression {
            new_false_negatives,
            new_false_positives,
        })
    }
}

/// Adopt the staging weights + bias into the `weight` table with provenance.
/// Only called on a backtest PASS. UPDATEs the four `LAYERS` rows + the `_bias`
/// row (setting `tuned_at = now`, `tuned_from_run = run_id`) in ONE transaction
/// on the short-lived `weight_write_conn` — which touches ONLY the `weight`
/// table, preserving the actor's single-writer invariant. `_threshold` (τ) is
/// NEVER touched (Open Q1). Every value is bound with `params![]` (T-06-02 / V5).
fn write_weights(
    store: &Store,
    staging_w: &[f64],
    staging_b: f64,
    run_id: i64,
) -> rusqlite::Result<()> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);

    let mut conn = store.weight_write_conn()?;
    let tx = conn.transaction()?;
    for (layer, w) in LAYERS.iter().zip(staging_w.iter()) {
        tx.execute(
            "UPDATE weight SET weight = ?2, tuned_at = ?3, tuned_from_run = ?4 WHERE layer = ?1",
            rusqlite::params![layer, w, now, run_id],
        )?;
    }
    tx.execute(
        "UPDATE weight SET weight = ?2, tuned_at = ?3, tuned_from_run = ?4 WHERE layer = ?1",
        rusqlite::params![BIAS_KEY, staging_b, now, run_id],
    )?;
    tx.commit()?;
    Ok(())
}

/// FNV-1a 64-bit over the pubkey string — the codebase's existing deterministic,
/// zero-dep feature hash (mirrors `detect::near_duplicate`'s `fnv1a64`; NOT a
/// cryptographic hash, ASVS V6). Used purely as a STABLE ordering key for the
/// negative sample so the selection is reproducible WITHOUT a `rand` dependency
/// (D-08/OPS-02): the same run + same `review_sample_size` → identical rows.
fn fnv1a64(bytes: &[u8]) -> u64 {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for &b in bytes {
        h ^= b as u64;
        h = h.wrapping_mul(0x0000_0100_0000_01b3);
    }
    h
}

/// TUNE-04 / D-05 (RESEARCH §Pattern 3, Pitfall 2): populate `review_queue` with
/// a DETERMINISTIC sample of UNFLAGGED (`suspected = 0`) pubkeys for `run_id`, so
/// the next labeling round draws real negatives — not only the flagged export —
/// and the logistic fit isn't biased toward "everything is spam".
///
/// Selection (no RNG, no `rand` dep): read every `suspected = 0` scored pubkey for
/// the run, order them by a stable FNV-1a hash of the pubkey string (tie-broken by
/// the pubkey itself for total order), and take the first `sample_size`. Re-running
/// over the same run + same sample size yields the identical set (D-08/OPS-02).
///
/// Write path (T-06-06): a short-lived `review_queue_write_conn` that touches ONLY
/// the `review_queue` table — never the writer actor's tables — so the
/// single-writer invariant holds. Idempotent: in ONE transaction, `DELETE` the
/// run's rows then `INSERT` the fresh sample (never duplicates). Every value is
/// bound with `params![]`/`?N`; nothing is `format!`-interpolated into SQL (V5).
fn populate_review_queue(
    store: &Store,
    reader: &rusqlite::Connection,
    run_id: i64,
    sample_size: usize,
) -> rusqlite::Result<usize> {
    // 1. Read the UNFLAGGED scored pubkeys for the run (binds only run_id — V5).
    let mut unflagged: Vec<(String, f64)> = {
        let mut stmt =
            reader.prepare("SELECT pubkey, score FROM score WHERE run_id = ?1 AND suspected = 0")?;
        let rows = stmt.query_map(rusqlite::params![run_id], |r| {
            Ok((r.get::<_, String>(0)?, r.get::<_, f64>(1)?))
        })?;
        rows.collect::<rusqlite::Result<Vec<_>>>()?
    };

    // 2. Deterministic order by a stable pubkey hash (no RNG); pubkey breaks ties
    //    so the total order is reproducible regardless of SELECT row order.
    unflagged.sort_by(|a, b| {
        fnv1a64(a.0.as_bytes())
            .cmp(&fnv1a64(b.0.as_bytes()))
            .then_with(|| a.0.cmp(&b.0))
    });
    unflagged.truncate(sample_size);

    // 3. Idempotent replace on the short-lived non-actor conn, in one transaction.
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    let mut conn = store.review_queue_write_conn()?;
    let tx = conn.transaction()?;
    tx.execute(
        "DELETE FROM review_queue WHERE run_id = ?1",
        rusqlite::params![run_id],
    )?;
    for (pubkey, score) in &unflagged {
        tx.execute(
            "INSERT INTO review_queue (run_id, pubkey, score, sampled_at) \
             VALUES (?1, ?2, ?3, ?4)",
            rusqlite::params![run_id, pubkey, score, now],
        )?;
    }
    tx.commit()?;
    Ok(unflagged.len())
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

    // Negative sampling (TUNE-04 / D-05): surface a deterministic sample of this
    // run's UNFLAGGED pubkeys into review_queue for the NEXT labeling round. This
    // runs regardless of whether the backtest below adopts new weights — the queue
    // exists to counter selection bias (Pitfall 2), not to gate adoption. Done on
    // the short-lived review_queue_write_conn (single-writer invariant preserved).
    populate_review_queue(store, &conn, run_id, config.tune.review_sample_size)
        .map_err(|e| format!("populate review_queue: {e}"))?;

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

    // Fit the staging weights + bias (deterministic). Hyperparameters come from
    // the `[tune]` config section (D-05) — defaulted when the section is absent.
    let (staging_w, staging_b) =
        fit(&labeled, config.tune.alpha, config.tune.max_iterations)?;

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

    /// Seed a `score` row on a short-lived write conn (FK target inserted first).
    /// Mirrors export::tests::seed_scored_pubkey — synchronous, bypasses the
    /// writer actor (fine for a fixture), so review_queue's `suspected=0` SELECT
    /// has rows to sample.
    fn seed_scored(store: &Store, run_id: i64, pubkey: &str, score: f64, suspected: bool) {
        let conn = store.review_queue_write_conn().expect("write conn");
        conn.execute(
            "INSERT OR IGNORE INTO pubkey (pubkey) VALUES (?1)",
            rusqlite::params![pubkey],
        )
        .expect("insert pubkey");
        conn.execute(
            "INSERT INTO score (run_id, pubkey, score, whitelisted, suspected) \
             VALUES (?1, ?2, ?3, 0, ?4)",
            rusqlite::params![run_id, pubkey, score, suspected as i64],
        )
        .expect("insert score");
    }

    /// TUNE-04 (D-05, RESEARCH §Pattern 3 / Pitfall 2): `run_tune` populates
    /// `review_queue` with a DETERMINISTIC sample of UNFLAGGED (`suspected=0`)
    /// pubkeys — never a flagged one — capped at `review_sample_size`, and a
    /// second call yields the IDENTICAL set (hash-ordered, no RNG). A small
    /// sample_size (2) exercises the cap.
    #[test]
    fn review_queue_samples_unflagged_deterministically() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let mut config = sample_config();
        config.tune.review_sample_size = 2; // exercise the cap (< n_unflagged)
        detect::seed_weights_if_empty(&store, &config).expect("seed weights");
        let run_id = store.begin_run("{\"tau\":0.5}").expect("begin_run");
        store.mark_run_done(run_id, 100).expect("mark done");

        // Both-class labels so run_tune proceeds past the precondition guard and
        // fits/backtests; the review-queue step then runs over this run's scores.
        seed_labeled(&store, run_id, &pk("s1"), true, &[("L1_near_duplicate", 1.0), ("L3_content_entropy", 1.0)]);
        seed_labeled(&store, run_id, &pk("h1"), false, &[("L1_near_duplicate", 0.0)]);

        // Several UNFLAGGED (suspected=0) scored pubkeys — the negative pool.
        seed_scored(&store, run_id, &pk("u1"), 0.10, false);
        seed_scored(&store, run_id, &pk("u2"), 0.20, false);
        seed_scored(&store, run_id, &pk("u3"), 0.30, false);
        seed_scored(&store, run_id, &pk("u4"), 0.40, false);
        // Two FLAGGED (suspected=1) pubkeys — must NEVER be sampled.
        seed_scored(&store, run_id, &pk("f1"), 0.90, true);
        seed_scored(&store, run_id, &pk("f2"), 0.95, true);

        super::run_tune(&store, &config, None).expect("run_tune 1");

        let conn = store.reader().expect("reader");
        let first: Vec<String> = {
            let mut stmt = conn
                .prepare("SELECT pubkey FROM review_queue WHERE run_id = ?1 ORDER BY pubkey")
                .expect("prepare");
            let rows = stmt
                .query_map(rusqlite::params![run_id], |r| r.get::<_, String>(0))
                .expect("query");
            rows.map(|r| r.expect("row")).collect()
        };

        // Capped at sample_size = 2.
        assert_eq!(first.len(), 2, "review_queue capped at review_sample_size");

        // Every sampled pubkey is UNFLAGGED (suspected=0) in `score`.
        for pubkey in &first {
            let suspected: i64 = conn
                .query_row(
                    "SELECT suspected FROM score WHERE run_id = ?1 AND pubkey = ?2",
                    rusqlite::params![run_id, pubkey],
                    |r| r.get(0),
                )
                .expect("score row for sampled pubkey");
            assert_eq!(suspected, 0, "only UNFLAGGED pubkeys sampled (got {pubkey})");
        }

        // Re-running yields the IDENTICAL set (deterministic, hash-ordered).
        super::run_tune(&store, &config, None).expect("run_tune 2");
        let second: Vec<String> = {
            let mut stmt = conn
                .prepare("SELECT pubkey FROM review_queue WHERE run_id = ?1 ORDER BY pubkey")
                .expect("prepare");
            let rows = stmt
                .query_map(rusqlite::params![run_id], |r| r.get::<_, String>(0))
                .expect("query");
            rows.map(|r| r.expect("row")).collect()
        };
        assert_eq!(first, second, "re-run yields the identical deterministic sample");

        drop(conn);
        store.close().expect("close");
    }
}




