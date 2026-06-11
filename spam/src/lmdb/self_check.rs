/// self_check.rs — Fail-closed comparator self-check.
///
/// `run_comparator_self_check` performs two phases:
///
/// ### Phase 1: Physical-order integrity scan
///
/// Scans all six `Event__*` indexes with a forward `db.iter()` walk (which traverses
/// the B-tree in physically-stored page order), collects the VALUE-side levId sequence,
/// and asserts it equals the committed golden vector. This confirms DATA INTEGRITY —
/// the actual levId sequence stored in strfry's B-tree matches the oracle.
///
/// **Important limitation:** a forward `iter()` walk follows physical page order and
/// never invokes the registered comparator. The fixture B-tree was already built by
/// strfry with the real golpe comparator, so this scan yields the golden order even
/// if our reimplemented comparator is wrong or unregistered. The integrity scan alone
/// does NOT validate comparator correctness. (CR-01 finding)
///
/// ### Phase 2: Comparator-dependent seek gate (CR-01 closure)
///
/// After the integrity scan, drives `db.range()` (`MDB_SET_RANGE`) seeks on adversarial
/// key pairs — where golpe's numeric ordering disagrees with memcmp — and asserts the
/// cursor lands on the numerically-correct neighbor. This operation DOES invoke the
/// registered comparator (unlike `iter()`), so a wrong/unregistered comparator causes
/// a different cursor landing position → `Err(SelfCheckError::ComparatorSeekMismatch)`.
///
/// Adversarial pairs (from golden vector `ordering_groups`):
/// - `Event__kind`: seek lower_bound=(kind=256, ts=0) → must land on levId=2 (kind=256
///   entry). Under golpe: kind=255 < kind=256 → cursor skips kind=255 → levId=2.
///   Under memcmp: kind=256 LE `[0x00,0x01,…]` < kind=255 LE `[0xFF,0x00,…]` → kind=255
///   entry appears first in a memcmp scan of the golpe-ordered leaf → wrong result.
/// - `Event__pubkeyKind`: same kind inversion within the `79be…` pubkey prefix.
///
/// ## Why this gate matters (D-04/D-05/D-06)
///
/// The combined scan + seek check catches four silent failure modes:
///   1. heed comparator registration not taking effect (LMDB falls back to memcmp) — Phase 2
///   2. Wrong comparator bound to wrong index (e.g. StringUint64 on Event__kind) — Phase 2
///   3. Build/host/endianness drift (ABI mismatch between golpe C++ and Rust FFI) — Phase 2
///   4. Vacuous pass (self-check reads no entries) — caught by T-03-04 mutation test
///
/// ## levId extraction contract (spec §3.1)
///
/// In every `Event__*` index sub-DB:
///   - KEY  = composite field (e.g. pubkey(32) ‖ created_at(8 LE))
///   - VALUE = 8-byte little-endian levId (uint64)
///
/// The self-check collects VALUE-side levIds. This is the IDENTICAL extraction plan
/// 01-02 Task 5 used to compute the golden vectors.
///
/// ## Reusability (D-13)
///
/// `run_comparator_self_check` is a standalone public function. Phase 5's `/ready` endpoint
/// calls it directly without code duplication. It is NOT inlined in main.rs.
use crate::lmdb::indexes::{scan_lev_ids_for_index, seek_first_ge_lev_id, ALL_EVENT_INDEXES};
use serde::Deserialize;
use std::collections::HashMap;

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

/// Error returned by `run_comparator_self_check`.
#[derive(Debug, thiserror::Error)]
pub enum SelfCheckError {
    /// Full-sequence mismatch for one index (physical-order integrity scan).
    /// The stored levId sequence does not match the committed golden vector.
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

    /// Comparator-dependent seek landed on the wrong entry (seek gate — Phase 2).
    ///
    /// The registered comparator is wrong, unregistered, or falls back to memcmp.
    /// A `MDB_SET_RANGE` seek for an adversarial lower-bound key landed on a different
    /// levId than the one the golpe comparator should position on.
    ///
    /// This is the fail-closed gate for comparator registration correctness (CR-01 / LMDB-06).
    #[error(
        "Comparator seek gate FAILED for index '{index}': \
         MDB_SET_RANGE seek landed on wrong levId.\n\
         Expected (golpe-correct): levId={expected_lev_id}\n\
         Actual (landed on):       levId={actual_lev_id}\n\
         Comparator is wrong, unregistered, or falls back to memcmp."
    )]
    ComparatorSeekMismatch {
        index: String,
        expected_lev_id: u64,
        actual_lev_id: u64,
    },

    /// Comparator seek returned no entry for an adversarial lower-bound that must match.
    #[error(
        "Comparator seek gate FAILED for index '{index}': \
         MDB_SET_RANGE seek returned no entry for lower_bound that must have a match \
         (index non-empty, expected levId={expected_lev_id})."
    )]
    ComparatorSeekEmpty { index: String, expected_lev_id: u64 },

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
// Adversarial seek pairs for the comparator gate (Phase 2)
// ---------------------------------------------------------------------------

/// One adversarial seek assertion: seek lower_bound_key → expect landing on expected_lev_id.
struct AdversarialSeek {
    /// Short index name (e.g. "Event__kind").
    index: &'static str,
    /// Lower-bound key bytes for the MDB_SET_RANGE seek.
    lower_bound: Vec<u8>,
    /// The levId the cursor must land on under the golpe comparator.
    /// Derived from the committed golden vector's `ordering_groups`.
    expected_lev_id: u64,
}

/// Build the adversarial seek pairs from static golden-vector knowledge.
///
/// These lower-bound keys are chosen so that:
/// - Under golpe (numeric ordering): the cursor skips the kind=255 entry and lands on
///   the kind=256 entry (levId=2 in both Event__kind and Event__pubkeyKind).
/// - Under memcmp on the golpe-built B-tree: the cursor does NOT skip kind=255 entries
///   (because memcmp sees kind=256 LE bytes as "smaller" than kind=255 LE bytes, leading
///   to incorrect B-tree positioning) → lands on a different levId → gate trips.
///
/// Pubkey `79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798` is the
/// secp256k1 generator-point public key used as the seed's fixed test keypair (PROVENANCE.md).
fn build_adversarial_seeks() -> Vec<AdversarialSeek> {
    // -----------------------------------------------------------------------
    // Event__kind: key format = kind(8 LE) ‖ created_at(8 LE)
    // Lower bound: (kind=256, ts=0)
    // Golpe-correct landing: (kind=256, ts=1700000000) = levId=2
    //   (per Event__kind.json ordering_groups, kind=256 → lev_ids=[2])
    // -----------------------------------------------------------------------
    let kind_lower_bound = {
        let mut key = Vec::with_capacity(16);
        key.extend_from_slice(&256u64.to_le_bytes()); // kind=256 LE
        key.extend_from_slice(&0u64.to_le_bytes());   // ts=0 LE (seek to first kind=256 entry)
        key
    };

    // -----------------------------------------------------------------------
    // Event__pubkeyKind: key format = pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE)
    // Lower bound: (pubkey=79be..., kind=256, ts=0)
    // Golpe-correct landing: (pubkey=79be..., kind=256, ts=1700000000) = levId=2
    //   (per Event__pubkeyKind.json ordering_groups, pk=79be..., kind=256 → lev_ids=[2])
    // -----------------------------------------------------------------------
    let pubkey_79be: [u8; 32] = [
        0x79, 0xbe, 0x66, 0x7e, 0xf9, 0xdc, 0xbb, 0xac,
        0x55, 0xa0, 0x62, 0x95, 0xce, 0x87, 0x0b, 0x07,
        0x02, 0x9b, 0xfc, 0xdb, 0x2d, 0xce, 0x28, 0xd9,
        0x59, 0xf2, 0x81, 0x5b, 0x16, 0xf8, 0x17, 0x98,
    ];
    let pubkey_kind_lower_bound = {
        let mut key = Vec::with_capacity(48);
        key.extend_from_slice(&pubkey_79be); // pubkey(32 bytes)
        key.extend_from_slice(&256u64.to_le_bytes()); // kind=256 LE
        key.extend_from_slice(&0u64.to_le_bytes());   // ts=0 LE
        key
    };

    vec![
        AdversarialSeek {
            index: "Event__kind",
            lower_bound: kind_lower_bound,
            expected_lev_id: 2,
        },
        AdversarialSeek {
            index: "Event__pubkeyKind",
            lower_bound: pubkey_kind_lower_bound,
            expected_lev_id: 2,
        },
    ]
}

// ---------------------------------------------------------------------------
// Main self-check function (D-13: reusable, callable by Phase 5's /ready)
// ---------------------------------------------------------------------------

/// Run the comparator self-check against all six `Event__*` indexes.
///
/// Performs two phases:
///
/// **Phase 1: Physical-order integrity scan.** Scans each index in forward (physical page)
/// order, collects the VALUE-side levId sequence, and asserts full-sequence equality against
/// the provided golden vectors. Detects corrupted or reorganized B-tree data.
///
/// **Note:** a forward `iter()` walk never invokes the registered comparator. The fixture was
/// built by strfry with its real comparator, so this scan yields the golden order regardless
/// of which comparator (or none) the consumer registers. Phase 1 validates DATA INTEGRITY
/// only — it does NOT prove the comparator is correctly registered.
///
/// **Phase 2: Comparator-dependent seek gate.** Performs `db.range()` (`MDB_SET_RANGE`)
/// seeks on adversarial key pairs (Event__kind, Event__pubkeyKind) where golpe's numeric
/// ordering disagrees with memcmp. The seek operation consults the registered comparator.
/// If the wrong comparator is registered, the cursor lands on the wrong entry →
/// `Err(SelfCheckError::ComparatorSeekMismatch)` (fail-closed, CR-01 / LMDB-06 / D-04).
///
/// Returns `Ok(())` only if ALL six indexes pass Phase 1 AND both adversarial seeks in
/// Phase 2 land on the golpe-correct entry.
///
/// ## Usage
///
/// ```rust,no_run
/// # use lmdb2graphql::lmdb::self_check::{GoldenVectors, run_comparator_self_check};
/// # fn doc_example(env: &heed::Env) -> anyhow::Result<()> {
/// let golden = GoldenVectors::load_committed()?;
/// run_comparator_self_check(&env, &golden)?;
/// # Ok(())
/// # }
/// ```
///
/// Phase 5's `/ready` endpoint calls this function directly — do NOT inline in main.rs.
pub fn run_comparator_self_check(
    env: &heed::Env,
    golden: &GoldenVectors,
) -> Result<(), SelfCheckError> {
    // -----------------------------------------------------------------------
    // Phase 1: Physical-order integrity scan
    //
    // Forward iter() walk — validates DATA INTEGRITY (actual levId sequence vs oracle).
    // Does NOT exercise the registered comparator (follows physical page order).
    // -----------------------------------------------------------------------
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
                "Self-check physical-order integrity FAILED: levId sequence mismatch"
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
            "Physical-order integrity check passed for index"
        );
    }

    // -----------------------------------------------------------------------
    // Phase 2: Comparator-dependent seek gate (CR-01 closure)
    //
    // MDB_SET_RANGE seeks on adversarial key pairs — exercises the registered comparator.
    // A wrong/absent comparator causes a different landing position → Err (fail-closed).
    // -----------------------------------------------------------------------
    for seek in build_adversarial_seeks() {
        let landed = seek_first_ge_lev_id(env, seek.index, &seek.lower_bound)
            .map_err(|e| SelfCheckError::IndexScan {
                index: seek.index.to_string(),
                source: e,
            })?;

        match landed {
            None => {
                tracing::error!(
                    index = seek.index,
                    expected_lev_id = seek.expected_lev_id,
                    "Comparator seek gate FAILED: seek returned empty range (no entry >= lower_bound)"
                );
                return Err(SelfCheckError::ComparatorSeekEmpty {
                    index: seek.index.to_string(),
                    expected_lev_id: seek.expected_lev_id,
                });
            }
            Some(actual_lev_id) if actual_lev_id != seek.expected_lev_id => {
                tracing::error!(
                    index = seek.index,
                    expected_lev_id = seek.expected_lev_id,
                    actual_lev_id,
                    "Comparator seek gate FAILED: seek landed on wrong levId — \
                     comparator is wrong, unregistered, or falls back to memcmp"
                );
                return Err(SelfCheckError::ComparatorSeekMismatch {
                    index: seek.index.to_string(),
                    expected_lev_id: seek.expected_lev_id,
                    actual_lev_id,
                });
            }
            Some(actual_lev_id) => {
                tracing::debug!(
                    index = seek.index,
                    lev_id = actual_lev_id,
                    "Comparator seek gate passed for index"
                );
            }
        }
    }

    tracing::info!(
        "Comparator self-check passed: physical-order integrity scan + comparator seek gate"
    );
    Ok(())
}
