//! TOML configuration (OPS-03 / D-09): the operator-supplied `pubkey_iterator_
//! config.toml` parsed into typed serde structs.
//!
//! D-09 repo rule: the loader takes a `&Path` argument so callers (production:
//! `~/deepfry/pubkey_iterator_config.toml`) inject the path, and config-loading
//! TESTS write into a `tempfile::TempDir` — never the real `~/deepfry` file.
//! Other files under `~/deepfry/` are off-limits.
//!
//! The shape mirrors RESEARCH §Config TOML: top-level adapter/whitelist URLs,
//! the combiner `tau` (τ) + `bias`, and four per-layer entries (L0/L1/L3/L4)
//! each carrying `enabled` + `weight` plus its layer-specific tunables. Every
//! magnitude is a conservative, false-positive-averse default (D-08); the
//! committed `pubkey_iterator_config.example.toml` documents the shape.

use serde::Deserialize;
use std::path::Path;

/// A typed load error (never a panic): the file could not be read, or its TOML
/// did not parse into [`Config`]. Mirrors the project's `thiserror` convention.
#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    /// The config file at the supplied path could not be read (missing,
    /// permissions, etc.). Carries the originating `io::Error`.
    #[error("failed to read config file: {0}")]
    Read(#[from] std::io::Error),
    /// The file was read but its contents did not parse as the expected TOML
    /// `Config` shape.
    #[error("failed to parse config TOML: {0}")]
    Parse(#[from] toml::de::Error),
}

/// The top-level configuration. `tau` (τ) is the flag threshold and `bias` the
/// logistic combiner's `b`; both are seeded into the `weight` table on first run
/// (SCORE-04). The four `layers.*` entries are the fixed-order detection layers.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Config {
    /// The LMDB2GraphQL adapter endpoint (operator-trusted URL).
    pub adapter_url: String,
    /// The whitelist-plugin base URL for the L0 `GET /check/{pubkey}` lookup.
    pub whitelist_url: String,
    /// Flag threshold τ: `suspected = score > tau`. Conservative default 0.5.
    pub tau: f64,
    /// Logistic combiner bias `b`. Conservative default −4.0 (baseline ≈ 0.018).
    pub bias: f64,
    /// The per-layer entries, keyed by stable `signal.layer` name (D-02).
    pub layers: Layers,
    /// Phase-6 offline tuner hyperparameters (TUNE-02/TUNE-04). `#[serde(default)]`
    /// so existing config files (written before Phase 6) keep loading — a missing
    /// `[tune]` section yields the conservative defaults.
    #[serde(default)]
    pub tune: TuneConfig,
}

/// `[tune]` hyperparameters for the offline logistic tuner (D-05, Claude's
/// discretion). Every field carries a `#[serde(default)]` so the whole section
/// — or any individual field — may be omitted from the TOML.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct TuneConfig {
    /// L2 regularization strength for the logistic fit. Default 0.01 — DELIBERATELY
    /// weaker than RESEARCH A1's suggested 1.0, which over-shrinks the weights of a
    /// small labeled set (a few rows) so far that even strongly-separable classes
    /// collapse below τ. 0.01 lets a separable synthetic fixture actually separate
    /// while still penalising over-large coefficients. Tunable per-deployment as
    /// the labeled set grows.
    #[serde(default = "default_alpha")]
    pub alpha: f64,
    /// L-BFGS iteration cap for the fit. Default 100 (RESEARCH A1).
    #[serde(default = "default_max_iterations")]
    pub max_iterations: u64,
    /// Negative-sampling review-queue size (consumed by Plan 03). Default 100
    /// (RESEARCH A2).
    #[serde(default = "default_review_sample_size")]
    pub review_sample_size: usize,
}

fn default_alpha() -> f64 {
    0.01
}
fn default_max_iterations() -> u64 {
    100
}
fn default_review_sample_size() -> usize {
    100
}

impl Default for TuneConfig {
    fn default() -> Self {
        TuneConfig {
            alpha: default_alpha(),
            max_iterations: default_max_iterations(),
            review_sample_size: default_review_sample_size(),
        }
    }
}

/// The four P1 detection-layer config entries, keyed by their stable layer
/// names. A missing entry is a parse error (all four are required in v1).
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct Layers {
    #[serde(rename = "L0_whitelist_absence")]
    pub l0_whitelist_absence: L0Config,
    #[serde(rename = "L1_near_duplicate")]
    pub l1_near_duplicate: L1Config,
    #[serde(rename = "L3_content_entropy")]
    pub l3_content_entropy: L3Config,
    #[serde(rename = "L4_link_mention")]
    pub l4_link_mention: L4Config,
}

/// L0 whitelist-absence layer config (DETECT-01). Absence emits `absence_subscore`
/// (weak weight); presence clears only this layer (D-03).
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct L0Config {
    pub enabled: bool,
    pub weight: f64,
    /// The sub-score `x` emitted when the pubkey is NOT whitelisted.
    pub absence_subscore: f64,
}

/// L1 within-pubkey near-duplicate layer config (DETECT-02): SimHash + Hamming.
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct L1Config {
    pub enabled: bool,
    pub weight: f64,
    /// Max Hamming distance (of 64 bits) to count two events as near-duplicate.
    pub hamming_threshold: u32,
    /// Word-shingle size for SimHash.
    pub shingle_size: usize,
    /// Below this event count the layer emits 0.0 (FP-averse — too little signal).
    pub min_events: usize,
}

/// L3 content-entropy + density layer config (DETECT-03).
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct L3Config {
    pub enabled: bool,
    pub weight: f64,
    /// Low-entropy cutoff (templated text), bits/char.
    pub entropy_low: f64,
    /// High-entropy cutoff (gibberish), bits/char.
    pub entropy_high: f64,
    /// Minimum content length to apply the low-entropy flag (short posts exempt).
    pub min_len_for_low: usize,
    /// Emoji-density knee → contributes 1.0 at/above this ratio.
    pub emoji_density_knee: f64,
    /// Hashtag-density knee → contributes 1.0 at/above this ratio.
    pub hashtag_density_knee: f64,
}

/// L4 link & mention layer config (DETECT-04).
#[derive(Debug, Clone, PartialEq, Deserialize)]
pub struct L4Config {
    pub enabled: bool,
    pub weight: f64,
    /// URL-ratio knee → contributes 1.0 at/above this ratio.
    pub url_ratio_knee: f64,
    /// Repeated-domain concentration knee → 1.0 at/above this ratio.
    pub domain_concentration_knee: f64,
    /// Mean p-tags/event knee (mass-mention) → 1.0 at/above this count.
    pub mean_ptags_knee: f64,
    /// Mean t-tags/event knee (hashtag stuffing) → 1.0 at/above this count.
    pub mean_ttags_knee: f64,
    /// Below this event count the layer emits 0.0 (FP-averse).
    pub min_events: usize,
}

/// Load and parse the TOML config at `path` (D-09: path-arg, never the real
/// `~/deepfry` file in tests). Returns a typed [`ConfigError`] on a read or
/// parse failure — NEVER panics.
pub fn load(path: &Path) -> Result<Config, ConfigError> {
    let text = std::fs::read_to_string(path)?;
    let config = toml::from_str(&text)?;
    Ok(config)
}

/// The committed example config body, embedded at compile time. Its
/// adapter/whitelist URLs are the operator-specified endpoints (adapter
/// `192.168.149.21:8080/graphql`, whitelist `127.0.0.1:8081`) and every
/// magnitude is the conservative D-08 default. Written verbatim when no config
/// file exists yet (see [`load_or_generate`]).
pub const DEFAULT_CONFIG: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/pubkey_iterator_config.example.toml"
));

/// Load the config at `path`, first generating it from [`DEFAULT_CONFIG`] when
/// no file exists there. In the binary path a missing config is NOT a fatal
/// error (OPS-03): the conservative example — carrying the operator's
/// adapter/whitelist endpoints — is written to `path` (creating parent dirs as
/// needed), a note is printed to stderr, and the freshly-written file is loaded.
/// Any OTHER read error, or a parse failure on an existing file, still surfaces
/// as a typed [`ConfigError`]. An existing config is never overwritten. D-09:
/// tests inject a temp path, never the real `~/deepfry` file.
pub fn load_or_generate(path: &Path) -> Result<Config, ConfigError> {
    if !path.exists() {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        std::fs::write(path, DEFAULT_CONFIG)?;
        eprintln!(
            "no config at {} — wrote default (edit adapter_url/whitelist_url as needed)",
            path.display()
        );
    }
    load(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::TempDir;

    /// The canonical example body (the conservative D-08 defaults). Mirrors
    /// `pubkey_iterator_config.example.toml`.
    const SAMPLE: &str = r#"
adapter_url   = "http://192.168.149.21:8080/graphql"
whitelist_url = "http://127.0.0.1:8081"
tau           = 0.5
bias          = -4.0

[layers.L0_whitelist_absence]
enabled          = true
weight           = 0.8
absence_subscore = 1.0

[layers.L1_near_duplicate]
enabled           = true
weight            = 2.0
hamming_threshold = 3
shingle_size      = 3
min_events        = 5

[layers.L3_content_entropy]
enabled              = true
weight               = 1.5
entropy_low          = 2.0
entropy_high         = 5.5
min_len_for_low      = 200
emoji_density_knee   = 0.5
hashtag_density_knee = 0.3

[layers.L4_link_mention]
enabled                   = true
weight                    = 1.5
url_ratio_knee            = 0.8
domain_concentration_knee = 0.7
mean_ptags_knee           = 10.0
mean_ttags_knee           = 8.0
min_events                = 5
"#;

    /// Write `body` to a fresh temp-dir file and return `(dir, path)`. The
    /// `TempDir` guard must outlive the path (D-09: NEVER `~/deepfry`).
    fn temp_config(body: &str) -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("pubkey_iterator_config.toml");
        let mut f = std::fs::File::create(&path).expect("create config file");
        f.write_all(body.as_bytes()).expect("write config body");
        (dir, path)
    }

    /// A full TOML (all keys) loads into typed structs with the conservative
    /// defaults: URLs, τ=0.5, bias=−4.0, and four layer entries with their
    /// layer-specific params.
    #[test]
    fn loads_full_config_with_defaults() {
        let (_dir, path) = temp_config(SAMPLE);
        let cfg = load(&path).expect("load full config");

        assert_eq!(cfg.adapter_url, "http://192.168.149.21:8080/graphql");
        assert_eq!(cfg.whitelist_url, "http://127.0.0.1:8081");
        assert_eq!(cfg.tau, 0.5);
        assert_eq!(cfg.bias, -4.0);

        assert!(cfg.layers.l0_whitelist_absence.enabled);
        assert_eq!(cfg.layers.l0_whitelist_absence.weight, 0.8);
        assert_eq!(cfg.layers.l0_whitelist_absence.absence_subscore, 1.0);

        assert_eq!(cfg.layers.l1_near_duplicate.weight, 2.0);
        assert_eq!(cfg.layers.l1_near_duplicate.hamming_threshold, 3);
        assert_eq!(cfg.layers.l1_near_duplicate.shingle_size, 3);
        assert_eq!(cfg.layers.l1_near_duplicate.min_events, 5);

        assert_eq!(cfg.layers.l3_content_entropy.weight, 1.5);
        assert_eq!(cfg.layers.l3_content_entropy.entropy_low, 2.0);
        assert_eq!(cfg.layers.l3_content_entropy.entropy_high, 5.5);
        assert_eq!(cfg.layers.l3_content_entropy.min_len_for_low, 200);
        assert_eq!(cfg.layers.l3_content_entropy.emoji_density_knee, 0.5);
        assert_eq!(cfg.layers.l3_content_entropy.hashtag_density_knee, 0.3);

        assert_eq!(cfg.layers.l4_link_mention.weight, 1.5);
        assert_eq!(cfg.layers.l4_link_mention.url_ratio_knee, 0.8);
        assert_eq!(cfg.layers.l4_link_mention.domain_concentration_knee, 0.7);
        assert_eq!(cfg.layers.l4_link_mention.mean_ptags_knee, 10.0);
        assert_eq!(cfg.layers.l4_link_mention.mean_ttags_knee, 8.0);
        assert_eq!(cfg.layers.l4_link_mention.min_events, 5);
    }

    /// A missing path returns a typed `ConfigError::Read` — never a panic.
    #[test]
    fn missing_path_returns_typed_error() {
        let dir = tempfile::tempdir().expect("create temp dir");
        let missing = dir.path().join("does_not_exist.toml");
        let err = load(&missing).expect_err("missing file must error");
        assert!(
            matches!(err, ConfigError::Read(_)),
            "missing path → ConfigError::Read, got {err:?}"
        );
    }

    /// A disabled layer entry round-trips as `enabled = false`.
    #[test]
    fn disabled_layer_roundtrips() {
        let body = SAMPLE.replace(
            "[layers.L1_near_duplicate]\nenabled           = true",
            "[layers.L1_near_duplicate]\nenabled           = false",
        );
        let (_dir, path) = temp_config(&body);
        let cfg = load(&path).expect("load config with a disabled layer");
        assert!(
            !cfg.layers.l1_near_duplicate.enabled,
            "disabled layer must round-trip as enabled=false"
        );
        // The other layers stay enabled.
        assert!(cfg.layers.l0_whitelist_absence.enabled);
        assert!(cfg.layers.l3_content_entropy.enabled);
        assert!(cfg.layers.l4_link_mention.enabled);
    }

    /// `load_or_generate` writes the embedded default when the file is absent,
    /// then loads it — a missing config is auto-provisioned, not an error
    /// (OPS-03). The nested path proves parent dirs are created (mirrors
    /// `~/deepfry/...`), and the generated config carries the operator endpoints.
    #[test]
    fn load_or_generate_writes_default_when_absent() {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir
            .path()
            .join("deepfry")
            .join("pubkey_iterator_config.toml");
        assert!(!path.exists(), "precondition: no config yet");

        let cfg = load_or_generate(&path).expect("generate + load default");
        assert!(path.exists(), "default config must be written to disk");
        assert_eq!(cfg.adapter_url, "http://192.168.149.21:8080/graphql");
        assert_eq!(cfg.whitelist_url, "http://127.0.0.1:8081");
    }

    /// When a config already exists, `load_or_generate` loads it unchanged and
    /// never clobbers the operator's file.
    #[test]
    fn load_or_generate_preserves_existing() {
        let body = SAMPLE.replace("tau           = 0.5", "tau           = 0.9");
        let (_dir, path) = temp_config(&body);
        let cfg = load_or_generate(&path).expect("load existing config");
        assert_eq!(cfg.tau, 0.9, "existing config must not be overwritten");
    }

    /// D-09 repo rule: the loader is path-argument-based and the test writes
    /// into a `tempfile::TempDir`, never `~/deepfry`. (This test exists to
    /// document the invariant — the temp_config helper proves the seam.)
    #[test]
    fn loader_is_path_argument_based() {
        let (dir, path) = temp_config(SAMPLE);
        // The path is under the temp dir, never the real config file.
        assert!(
            path.starts_with(dir.path()),
            "config path must live under the temp dir (D-09), not ~/deepfry"
        );
        assert!(load(&path).is_ok(), "load accepts an injected &Path");
    }
}
