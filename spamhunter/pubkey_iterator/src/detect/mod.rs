//! The detection-layer integration seam: the shared [`Layer`] trait, the
//! fixed-order [`ScoringStage`] registry + logistic combiner, the `weight`-table
//! seed/read helpers, and the [`ScoredInput`] channel carrier.
//!
//! This is the Phase-4 walking slice (Plan 01): one trivial end-to-end layer
//! proves the seam; the four real layers (L0/L1/L3/L4) plug into this same
//! `ScoringStage` registry in Plans 02–03 with no plumbing changes.
//!
//! Determinism (OPS-02): the combiner sums `wᵢ·xᵢ` over a fixed positional `Vec`
//! (NEVER a HashMap — HashMap iteration order is unstable → non-deterministic
//! floating-point sum). There is zero RNG. The persistence layer already UPSERTs
//! on `(run_id, pubkey)` / `(run_id, pubkey, layer)`, so a re-run over the same
//! events with the same `weight` rows yields byte-identical `score`/`signal` rows.
//!
//! No enforcement side-effect (SCORE-04): the only outputs are `weight` rows
//! (seed) and the returned [`crate::model::Persist`] — no whitelist/quarantine
//! write, no event mutation.

use crate::config::Config;
use crate::graphql::queries::{AuthorGroup, Event};
use crate::model::{Persist, SubScore, Weight};
use crate::store::Store;
use rusqlite::{params, Connection};

pub mod near_duplicate;

/// Sentinel `weight.layer` key holding the combiner bias `b`.
pub const BIAS_KEY: &str = "_bias";
/// Sentinel `weight.layer` key holding the flag threshold τ (in the `threshold`
/// column, per the schema comment).
pub const THRESHOLD_KEY: &str = "_threshold";

/// One detection layer's output: a normalized sub-score plus a structured JSON
/// explanation serialized into `signal.evidence` (SCORE-05).
#[derive(Debug, Clone)]
pub struct LayerOutput {
    /// xᵢ ∈ [0,1]. The caller `debug_assert`s the bound in [`ScoringStage::score`].
    pub value: f64,
    /// Per-layer JSON explanation; serialized to the `signal.evidence` column.
    pub evidence: serde_json::Value,
}

/// The shared detection-layer contract (D-01 / DETECT-05). Object-safe (no
/// generics, no associated types, `&self` only) so `Box<dyn Layer>` works and
/// layers live one-per-file.
pub trait Layer: Send + Sync {
    /// Stable EAV layer name persisted into `signal.layer` (D-02). NEVER renamed
    /// once shipped — it is the `signal` table's contract key.
    fn name(&self) -> &'static str;

    /// Pure, deterministic function of this pubkey's events. `whitelisted` is the
    /// pre-resolved L0 membership (resolved in the fetch stage, so layers stay
    /// CPU-only and never block on HTTP — RESEARCH note A / Pitfall 5).
    fn score(&self, events: &[Event], whitelisted: bool) -> LayerOutput;
}

/// The fixed-order layer registry + logistic combiner (SCORE-01 / OPS-02).
///
/// `weights[i]` pairs positionally with `layers[i]` (built by name lookup from
/// the `weight` table in [`ScoringStage::from_config`]). The combiner computes
/// `z = bias + Σ weights[i]·layers[i].score(...)`, `score = sigmoid(z)`, and
/// `suspected = score > tau`.
pub struct ScoringStage {
    /// Fixed declaration order (L0, L1, L3, L4) — only the ENABLED layers. A
    /// disabled layer is omitted at build time (writes no `signal` row), never
    /// evaluated-then-zeroed.
    layers: Vec<Box<dyn Layer>>,
    /// Positional weights: `weights[i]` is `layers[i]`'s `wᵢ`.
    weights: Vec<f64>,
    /// Logistic bias `b`.
    bias: f64,
    /// Flag threshold τ.
    tau: f64,
}

impl ScoringStage {
    /// Build a stage directly from a layer set + parameters. The primary
    /// constructor for tests (and the seam Plans 02–03 register real layers
    /// through via [`ScoringStage::from_config`]). `layers` and `weights` MUST be
    /// the same length and positionally paired.
    pub fn from_layers(layers: Vec<Box<dyn Layer>>, weights: Vec<f64>, bias: f64, tau: f64) -> Self {
        assert_eq!(
            layers.len(),
            weights.len(),
            "layers and weights must be positionally paired (OPS-02 fixed-order sum)"
        );
        ScoringStage {
            layers,
            weights,
            bias,
            tau,
        }
    }

    /// Build the fixed-order stage from config + the stored `weight` rows. Only
    /// the ENABLED layers (in declared order L0, L1, L3, L4) are pushed; the
    /// positional `weights` Vec is built by name lookup against `weights_table`,
    /// falling back to the config weight when a row is absent. `bias`/`tau` come
    /// from the `_bias`/`_threshold` sentinel rows when present, else config.
    ///
    /// For THIS walking slice (Plan 01) the four real layer structs do not exist
    /// yet, so no layer is pushed here unless one is registered — Plans 02–03
    /// push the real layers. Tests use [`ScoringStage::from_layers`] with a
    /// trivial layer to exercise the combiner.
    pub fn from_config(config: &Config, weights_table: &[Weight]) -> Self {
        let weight_of = |layer: &str, fallback: f64| -> f64 {
            weights_table
                .iter()
                .find(|w| w.layer == layer)
                .map(|w| w.weight)
                .unwrap_or(fallback)
        };
        let bias = weights_table
            .iter()
            .find(|w| w.layer == BIAS_KEY)
            .map(|w| w.weight)
            .unwrap_or(config.bias);
        let tau = weights_table
            .iter()
            .find(|w| w.layer == THRESHOLD_KEY)
            .and_then(|w| w.threshold)
            .unwrap_or(config.tau);

        // Fixed declaration order (L0, L1, L3, L4) — only ENABLED layers are
        // pushed; a disabled layer is omitted at build time (writes no `signal`
        // row), never evaluated-then-zeroed. The positional `weights` Vec is
        // built by name lookup so `weights[i]` pairs with `layers[i]` (OPS-02).
        // L0 (whitelist) and L4 (link/mention) register in their own plans; this
        // plan (04-02) registers the L1 and L3 content layers in the fixed slots.
        let mut layers: Vec<Box<dyn Layer>> = Vec::new();
        let mut weights: Vec<f64> = Vec::new();

        // L1 — within-pubkey near-duplicate (DETECT-02).
        if config.layers.l1_near_duplicate.enabled {
            layers.push(Box::new(near_duplicate::NearDuplicateLayer::new(
                &config.layers.l1_near_duplicate,
            )));
            weights.push(weight_of(
                "L1_near_duplicate",
                config.layers.l1_near_duplicate.weight,
            ));
        }

        // L3 — content entropy + emoji/hashtag density (DETECT-03) registers in
        // Task 2 of this plan (the L3 slot, immediately after L1).

        ScoringStage {
            layers,
            weights,
            bias,
            tau,
        }
    }

    /// τ accessor (the flag threshold used by `suspected`).
    pub fn tau(&self) -> f64 {
        self.tau
    }

    /// Score one pubkey → a [`Persist`] payload. No RNG, no HashMap in the sum:
    /// `layers` is iterated in index order (the OPS-02 fixed-sum-order guarantee,
    /// mirroring `writer.rs`'s fixed-order subscore iteration).
    pub fn score(&self, run_id: i64, pubkey: &str, events: &[Event], whitelisted: bool) -> Persist {
        let mut z = self.bias;
        let mut subscores = Vec::with_capacity(self.layers.len());
        for (i, layer) in self.layers.iter().enumerate() {
            let out = layer.score(events, whitelisted);
            debug_assert!(
                (0.0..=1.0).contains(&out.value),
                "layer {} emitted out-of-range value {} (must be in [0,1])",
                layer.name(),
                out.value
            );
            z += self.weights[i] * out.value;
            subscores.push(SubScore {
                layer: layer.name().to_string(),
                value: out.value,
                evidence: Some(out.evidence.to_string()),
            });
        }
        let score = 1.0 / (1.0 + (-z).exp()); // sigmoid
        let suspected = score > self.tau;
        Persist {
            run_id,
            pubkey: pubkey.to_string(),
            score,
            whitelisted,
            suspected,
            subscores,
        }
    }
}

/// The bounded-channel carrier (D-15): a fetched [`AuthorGroup`] plus the
/// fetch-stage-resolved whitelist membership. Defined in THIS plan so Plan 03
/// only fills in the bool (the real async L0 lookup), not the plumbing. In this
/// walking slice the fetch stage sets `whitelisted = false`.
#[derive(Debug, Clone)]
pub struct ScoredInput {
    /// The pubkey's events (empty for an adapter-omitted zero-event author, D-15).
    pub group: AuthorGroup,
    /// Pre-resolved whitelist membership (false placeholder in Plan 01).
    pub whitelisted: bool,
}

/// Read every `weight` row, ORDER BY `layer` for a stable read order (OPS-02).
/// Parameterless constant SELECT; nothing is interpolated (T-04-01).
pub fn read_weights(conn: &Connection) -> rusqlite::Result<Vec<Weight>> {
    let mut stmt = conn.prepare(
        "SELECT layer, weight, threshold, tuned_at, tuned_from_run \
         FROM weight ORDER BY layer",
    )?;
    let rows = stmt.query_map([], |r| {
        Ok(Weight {
            layer: r.get::<_, String>(0)?,
            weight: r.get::<_, f64>(1)?,
            threshold: r.get::<_, Option<f64>>(2)?,
            tuned_at: r.get::<_, Option<i64>>(3)?,
            tuned_from_run: r.get::<_, Option<i64>>(4)?,
        })
    })?;
    rows.collect()
}

/// Seed the `weight` table from config on first run (SCORE-04). When the table
/// has NO rows for the six combiner keys (four layer weights + `_bias` +
/// `_threshold`), INSERT them from config; otherwise leave the stored values
/// untouched so a Phase-6 retune persists.
///
/// Writes go through a short-lived `weight`-only connection
/// ([`Store::weight_write_conn`]) that touches ONLY the `weight` table — it does
/// not race the single-writer actor's `score`/`signal`/`pubkey` tables. Every
/// value is bound with `params![]` (T-04-01); nothing is `format!`-interpolated.
pub fn seed_weights_if_empty(store: &Store, config: &Config) -> rusqlite::Result<()> {
    let conn = store.weight_write_conn()?;
    // First-run detection: any of the six combiner keys already present → seeded.
    let existing: i64 = conn.query_row(
        "SELECT count(*) FROM weight WHERE layer IN \
         (?1, ?2, ?3, ?4, ?5, ?6)",
        params![
            config_layer_keys()[0],
            config_layer_keys()[1],
            config_layer_keys()[2],
            config_layer_keys()[3],
            BIAS_KEY,
            THRESHOLD_KEY
        ],
        |r| r.get(0),
    )?;
    if existing > 0 {
        return Ok(()); // already seeded — read the stored values, do not re-seed
    }

    // Layer weight rows (threshold column NULL — τ lives in the _threshold row).
    let layer_rows: [(&str, f64); 4] = [
        (
            "L0_whitelist_absence",
            config.layers.l0_whitelist_absence.weight,
        ),
        ("L1_near_duplicate", config.layers.l1_near_duplicate.weight),
        (
            "L3_content_entropy",
            config.layers.l3_content_entropy.weight,
        ),
        ("L4_link_mention", config.layers.l4_link_mention.weight),
    ];
    for (layer, weight) in layer_rows {
        conn.execute(
            "INSERT INTO weight (layer, weight, threshold, tuned_at, tuned_from_run) \
             VALUES (?1, ?2, NULL, NULL, NULL)",
            params![layer, weight],
        )?;
    }
    // _bias sentinel: the bias in the weight column.
    conn.execute(
        "INSERT INTO weight (layer, weight, threshold, tuned_at, tuned_from_run) \
         VALUES (?1, ?2, NULL, NULL, NULL)",
        params![BIAS_KEY, config.bias],
    )?;
    // _threshold sentinel: τ in the threshold column (weight column carries τ too
    // for a non-NULL NOT NULL `weight`, but readers use the threshold column).
    conn.execute(
        "INSERT INTO weight (layer, weight, threshold, tuned_at, tuned_from_run) \
         VALUES (?1, ?2, ?3, NULL, NULL)",
        params![THRESHOLD_KEY, config.tau, config.tau],
    )?;
    Ok(())
}

/// The four fixed-order layer keys (the `weight.layer` / config keys, D-02).
fn config_layer_keys() -> [&'static str; 4] {
    [
        "L0_whitelist_absence",
        "L1_near_duplicate",
        "L3_content_entropy",
        "L4_link_mention",
    ]
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tempfile::TempDir;

    /// A trivial deterministic layer emitting a fixed value + evidence — the
    /// stand-in proving the combiner end-to-end until the real L0/L1/L3/L4 layers
    /// land in Plans 02–03.
    struct TrivialLayer {
        name: &'static str,
        value: f64,
    }
    impl Layer for TrivialLayer {
        fn name(&self) -> &'static str {
            self.name
        }
        fn score(&self, events: &[Event], _whitelisted: bool) -> LayerOutput {
            LayerOutput {
                value: self.value,
                evidence: json!({ "layer": self.name, "n_events": events.len() }),
            }
        }
    }

    /// A layer that emits an out-of-range value, to trip the debug_assert.
    struct OutOfRangeLayer;
    impl Layer for OutOfRangeLayer {
        fn name(&self) -> &'static str {
            "L_out_of_range"
        }
        fn score(&self, _events: &[Event], _whitelisted: bool) -> LayerOutput {
            LayerOutput {
                value: 1.5, // > 1.0
                evidence: json!({}),
            }
        }
    }

    fn temp_db() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// Parse the committed example config body for the seed/read tests.
    fn sample_config() -> Config {
        let body = std::fs::read_to_string(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/pubkey_iterator_config.example.toml"
        ))
        .expect("read example config");
        toml::from_str(&body).expect("parse example config")
    }

    /// An out-of-range `LayerOutput.value` trips the debug_assert in `score`.
    #[test]
    #[should_panic(expected = "out-of-range")]
    fn out_of_range_value_trips_debug_assert() {
        // debug_assert only fires in debug builds; tests run in debug by default.
        let stage = ScoringStage::from_layers(vec![Box::new(OutOfRangeLayer)], vec![1.0], -4.0, 0.5);
        let _ = stage.score(1, "pk", &[], false);
    }

    /// One trivial layer at value=v under weight=w, bias=b → score = sigmoid(b+w*v),
    /// and the subscore carries the layer name + value + non-empty evidence.
    #[test]
    fn single_layer_sigmoid_and_subscore() {
        let v = 0.5;
        let w = 2.0;
        let b = -1.0;
        let stage = ScoringStage::from_layers(
            vec![Box::new(TrivialLayer {
                name: "L1_near_duplicate",
                value: v,
            })],
            vec![w],
            b,
            0.5,
        );
        let p = stage.score(7, "pkabc", &[], false);

        let expected = 1.0 / (1.0 + (-(b + w * v)).exp());
        assert!(
            (p.score - expected).abs() < 1e-12,
            "score must equal sigmoid(b + w*v): got {}, want {}",
            p.score,
            expected
        );
        assert_eq!(p.run_id, 7);
        assert_eq!(p.pubkey, "pkabc");
        assert!(!p.whitelisted);
        assert_eq!(p.subscores.len(), 1);
        let s = &p.subscores[0];
        assert_eq!(s.layer, "L1_near_duplicate");
        assert_eq!(s.value, v);
        let ev = s.evidence.as_deref().expect("non-empty evidence");
        assert!(!ev.is_empty(), "evidence JSON must be non-empty (SCORE-05)");
        assert!(ev.contains("L1_near_duplicate"));
    }

    /// Multi-signal-agreement structural property (seeded with the conservative
    /// weights): a SINGLE layer at 1.0 (others 0.0) → score < τ (not suspected);
    /// TWO+ strong layers → score > τ (suspected). No single-layer cutoff.
    #[test]
    fn multi_signal_agreement_property() {
        // Conservative starting set (RESEARCH §L7): weights L1=2.0, L3=1.5, L4=1.5,
        // bias=-4.0, τ=0.5. (L0 omitted from this synthetic test — the property is
        // about content layers stacking.)
        let bias = -4.0;
        let tau = 0.5;
        let mk = |vals: &[(&'static str, f64)]| -> Persist {
            let layers: Vec<Box<dyn Layer>> = vals
                .iter()
                .map(|(n, v)| Box::new(TrivialLayer { name: n, value: *v }) as Box<dyn Layer>)
                .collect();
            let weights: Vec<f64> = vals
                .iter()
                .map(|(n, _)| match *n {
                    "L1_near_duplicate" => 2.0,
                    "L3_content_entropy" => 1.5,
                    "L4_link_mention" => 1.5,
                    _ => 0.0,
                })
                .collect();
            ScoringStage::from_layers(layers, weights, bias, tau).score(1, "pk", &[], false)
        };

        // Single strongest layer (L1 @ 1.0) → ~0.119 < τ → NOT suspected.
        let single = mk(&[
            ("L1_near_duplicate", 1.0),
            ("L3_content_entropy", 0.0),
            ("L4_link_mention", 0.0),
        ]);
        assert!(
            single.score < tau && !single.suspected,
            "a single strong layer must NOT flag (score {} < τ {})",
            single.score,
            tau
        );

        // Three strong layers (all 1.0) → sigmoid(1.0) ≈ 0.731 > τ → suspected.
        let multi = mk(&[
            ("L1_near_duplicate", 1.0),
            ("L3_content_entropy", 1.0),
            ("L4_link_mention", 1.0),
        ]);
        assert!(
            multi.score > tau && multi.suspected,
            "multi-signal agreement must flag (score {} > τ {})",
            multi.score,
            tau
        );
    }

    /// Weight-table seed-on-empty writes the six combiner rows from config; a
    /// second call reads the stored values rather than re-seeding.
    #[test]
    fn weight_seed_on_empty_then_read() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();

        seed_weights_if_empty(&store, &config).expect("seed weights");

        let conn = store.reader().expect("reader");
        let weights = read_weights(&conn).expect("read weights");
        // Six rows: four layers + _bias + _threshold.
        assert_eq!(weights.len(), 6, "six combiner rows seeded");

        let by = |layer: &str| weights.iter().find(|w| w.layer == layer).expect("row");
        assert_eq!(by("L0_whitelist_absence").weight, 0.8);
        assert_eq!(by("L1_near_duplicate").weight, 2.0);
        assert_eq!(by("L3_content_entropy").weight, 1.5);
        assert_eq!(by("L4_link_mention").weight, 1.5);
        assert_eq!(by(BIAS_KEY).weight, -4.0);
        assert_eq!(by(THRESHOLD_KEY).threshold, Some(0.5));

        // Second call must NOT re-seed or duplicate; the stored values stand even
        // if config changes (simulating a Phase-6 retune that persisted).
        let mut mutated = sample_config();
        mutated.layers.l1_near_duplicate.weight = 99.0;
        seed_weights_if_empty(&store, &mutated).expect("second seed is a no-op");
        let conn2 = store.reader().expect("reader2");
        let weights2 = read_weights(&conn2).expect("read weights 2");
        assert_eq!(weights2.len(), 6, "no duplicate rows on second call");
        assert_eq!(
            weights2
                .iter()
                .find(|w| w.layer == "L1_near_duplicate")
                .unwrap()
                .weight,
            2.0,
            "second call reads the STORED value, not the mutated config"
        );
        store.close().expect("close");
    }

    /// OPS-02: `score` over the same events twice returns an equal `Persist`
    /// (no RNG, no HashMap iteration in the sum).
    #[test]
    fn score_is_deterministic() {
        let events = vec![
            crate::graphql::queries::Event {
                id: "e1".into(),
                pubkey: "pk".into(),
                kind: 1,
                created_at: 1_700_000_000,
                content: "hello".into(),
                tags: vec![],
            },
            crate::graphql::queries::Event {
                id: "e2".into(),
                pubkey: "pk".into(),
                kind: 1,
                created_at: 1_700_000_001,
                content: "world".into(),
                tags: vec![],
            },
        ];
        let mk_stage = || {
            ScoringStage::from_layers(
                vec![
                    Box::new(TrivialLayer {
                        name: "L1_near_duplicate",
                        value: 0.4,
                    }),
                    Box::new(TrivialLayer {
                        name: "L3_content_entropy",
                        value: 0.6,
                    }),
                ],
                vec![2.0, 1.5],
                -4.0,
                0.5,
            )
        };
        let a = mk_stage().score(1, "pk", &events, false);
        let b = mk_stage().score(1, "pk", &events, false);
        assert_eq!(a, b, "score must be byte-deterministic across re-runs (OPS-02)");
    }

    /// `from_config` reads bias/τ from the seeded sentinel rows when present.
    #[test]
    fn from_config_reads_seeded_bias_and_tau() {
        let (_dir, path) = temp_db();
        let store = Store::open(&path).expect("open store");
        let config = sample_config();
        seed_weights_if_empty(&store, &config).expect("seed");
        let conn = store.reader().expect("reader");
        let weights = read_weights(&conn).expect("read weights");

        let stage = ScoringStage::from_config(&config, &weights);
        // τ comes from the _threshold sentinel row.
        assert_eq!(stage.tau(), 0.5);
        // Plan 04-02 Task 1 registers the enabled L1 content layer (the example
        // config enables it). Scoring a ZERO-event pubkey: L1 emits 0.0 (below
        // min_events), so the sum reduces to sigmoid(bias) and exactly the L1
        // subscore row is present. (Task 2 adds L3 in the next slot.)
        let p = stage.score(1, "pk", &[], false);
        assert_eq!(p.subscores.len(), 1, "L1 registered (example config)");
        assert!(
            p.subscores.iter().all(|s| s.value == 0.0),
            "the content layer emits 0.0 on a zero-event pubkey"
        );
        assert_eq!(p.subscores[0].layer, "L1_near_duplicate");
        let expected = 1.0 / (1.0 + (-(-4.0_f64)).exp());
        assert!(
            (p.score - expected).abs() < 1e-12,
            "score = sigmoid(seeded bias) when L1 emits 0.0"
        );
        store.close().expect("close");
    }
}
