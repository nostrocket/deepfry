/// self_check.rs — Fail-closed comparator self-check.
///
/// `run_comparator_self_check` scans all six `Event__*` indexes end-to-end, collects the
/// full ordered VALUE-side levId sequence per index, and asserts it equals the committed
/// golden vector for that index. Any mismatch → `Err(SelfCheckError::OrderMismatch)`.
///
/// ## Why this gate matters (D-04/D-05/D-06)
///
/// The self-check catches four silent failure modes that survive even with golpe's
/// real comparator compiled in:
///   1. heed comparator registration not taking effect (LMDB falls back to memcmp)
///   2. Wrong comparator bound to wrong index (e.g. StringUint64 on Event__kind)
///   3. Build/host/endianness drift (ABI mismatch between golpe C++ and Rust FFI)
///   4. Vacuous pass (self-check reads no entries) → caught by T-03-04 mutation test
///
/// ## levId extraction contract (spec §3.1)
///
/// In every `Event__*` index sub-DB:
///   - KEY  = composite field (e.g. pubkey(32) ‖ created_at(8 LE))
///   - VALUE = 8-byte little-endian levId (uint64)
///
/// The self-check collects VALUE-side levIds in KEY-scan order and compares to the golden
/// vector's `ordered_lev_ids`. This is the IDENTICAL extraction plan 01-02 Task 5 used
/// to compute the golden vectors; deriving from a different field would produce silent
/// false passes/failures.
///
/// ## Reusability (D-13)
///
/// `run_comparator_self_check` is a standalone public function. Phase 5's `/ready` endpoint
/// calls it directly without code duplication. It is NOT inlined in main.rs.
use crate::lmdb::indexes::{scan_lev_ids_for_index, ALL_EVENT_INDEXES};
use serde::Deserialize;
use std::collections::HashMap;

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

/// Error returned by `run_comparator_self_check`.
#[derive(Debug, thiserror::Error)]
pub enum SelfCheckError {
    /// Full-sequence mismatch for one index — comparator mis-registered or wrong.
    #[error(
        "Comparator self-check FAILED for index '{index}': \
         ordered levId sequence does not match golden vector.\n\
         Expected: {expected:?}\n\
         Actual:   {actual:?}"
    )]
    OrderMismatch {
        index: String,
        expected: Vec<u64>,
        actual: Vec<u64>,
    },

    /// Failed to load golden vectors from the embedded JSON.
    #[error("Failed to load golden vector for index '{index}': {source}")]
    GoldenVectorLoad {
        index: String,
        source: serde_json::Error,
    },

    /// Golden vector for index not found in the map.
    #[error("Golden vector for index '{index}' not found — was it provided?")]
    GoldenVectorMissing { index: String },

    /// LMDB index scan error.
    #[error("LMDB scan error for index '{index}': {source}")]
    IndexScan {
        index: String,
        source: crate::lmdb::indexes::IndexError,
    },
}

// ---------------------------------------------------------------------------
// Golden vector loading and mutation support
// ---------------------------------------------------------------------------

/// Raw JSON structure of a committed golden vector file.
#[derive(Debug, Deserialize)]
struct GoldenVectorJson {
    ordered_lev_ids: Vec<u64>,
}

/// Embedded golden vector JSON bytes, keyed by short index name.
///
/// These are compiled into the binary via `include_str!` to ensure the self-check
/// always has the oracle data regardless of the working directory.
/// The paths are relative to the crate root (Cargo.toml directory).
static GOLDEN_VECTOR_JSON: &[(&str, &str)] = &[
    ("Event__id", include_str!("../../tests/fixture/golden_vectors/Event__id.json")),
    ("Event__pubkey", include_str!("../../tests/fixture/golden_vectors/Event__pubkey.json")),
    ("Event__created_at", include_str!("../../tests/fixture/golden_vectors/Event__created_at.json")),
    ("Event__kind", include_str!("../../tests/fixture/golden_vectors/Event__kind.json")),
    ("Event__pubkeyKind", include_str!("../../tests/fixture/golden_vectors/Event__pubkeyKind.json")),
    ("Event__tag", include_str!("../../tests/fixture/golden_vectors/Event__tag.json")),
];

/// Mutable in-memory golden vector map (index name → ordered levIds).
///
/// The committed oracle is loaded from the embedded JSON. Callers (tests) may
/// mutate an in-memory copy to test that the self-check detects mismatches.
///
/// The `mutate_reverse` method is only used in tests (T-03-04 non-vacuous check).
#[derive(Debug, Clone)]
pub struct GoldenVectors {
    map: HashMap<String, Vec<u64>>,
}

impl GoldenVectors {
    /// Load the committed golden vectors from the embedded JSON files.
    ///
    /// Returns `Err` if any JSON cannot be parsed.
    pub fn load_committed() -> Result<Self, SelfCheckError> {
        let mut map = HashMap::new();
        for (index_name, json_str) in GOLDEN_VECTOR_JSON {
            let gv: GoldenVectorJson =
                serde_json::from_str(json_str).map_err(|e| SelfCheckError::GoldenVectorLoad {
                    index: index_name.to_string(),
                    source: e,
                })?;
            map.insert(index_name.to_string(), gv.ordered_lev_ids);
        }
        Ok(GoldenVectors { map })
    }

    /// Get the expected ordered levId sequence for an index.
    pub fn get(&self, index_name: &str) -> Option<&Vec<u64>> {
        self.map.get(index_name)
    }

    /// Mutate: reverse the expected order for a given index.
    /// Used only in tests to verify the self-check is not a vacuous pass (T-03-04).
    pub fn mutate_reverse(&mut self, index_name: &str) {
        if let Some(v) = self.map.get_mut(index_name) {
            v.reverse();
        }
    }
}

// ---------------------------------------------------------------------------
// Main self-check function (D-13: reusable, callable by Phase 5's /ready)
// ---------------------------------------------------------------------------

/// Run the comparator self-check against all six `Event__*` indexes.
///
/// Scans each index in key order, collects the VALUE-side levId sequence, and
/// asserts full-sequence equality against the provided golden vectors.
///
/// Returns `Ok(())` only if ALL six indexes match their golden vectors exactly.
/// Returns `Err(SelfCheckError::OrderMismatch)` on the first divergence (fail-closed, D-04).
///
/// ## Usage
///
/// ```rust,no_run
/// let golden = GoldenVectors::load_committed()?;
/// run_comparator_self_check(&env, &golden)?;
/// ```
///
/// Phase 5's `/ready` endpoint calls this function directly — do NOT inline in main.rs.
pub fn run_comparator_self_check(
    env: &heed::Env,
    golden: &GoldenVectors,
) -> Result<(), SelfCheckError> {
    for short_name in ALL_EVENT_INDEXES {
        let expected = golden
            .get(short_name)
            .ok_or_else(|| SelfCheckError::GoldenVectorMissing {
                index: short_name.to_string(),
            })?;

        let actual =
            scan_lev_ids_for_index(env, short_name).map_err(|e| SelfCheckError::IndexScan {
                index: short_name.to_string(),
                source: e,
            })?;

        if &actual != expected {
            tracing::error!(
                index = short_name,
                expected = ?expected,
                actual = ?actual,
                "Comparator self-check FAILED: ordered levId sequence mismatch"
            );
            return Err(SelfCheckError::OrderMismatch {
                index: short_name.to_string(),
                expected: expected.clone(),
                actual,
            });
        }

        tracing::debug!(
            index = short_name,
            entries = expected.len(),
            "Comparator self-check passed for index"
        );
    }

    Ok(())
}
