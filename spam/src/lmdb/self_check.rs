/// self_check.rs — Fixture-free, fail-closed comparator self-check.
///
/// The self-check validates that the reimplemented golpe comparators (registered with heed
/// via `mdb_set_compare`) are byte-identical to the comparators strfry used to BUILD its
/// B-trees. It runs against ANY live strfry DB with no fixture and no golden vectors at
/// runtime — the **physical B-tree order is the oracle**.
///
/// ### Phase A — FFI / logic correctness
///
/// `db.iter()` walks the B-tree in the physical order strfry's real golpe comparator
/// established when it built the tree. Over a bounded key sample (cap
/// [`SELF_CHECK_SAMPLE_CAP`]), for each adjacent physical pair `(prev, cur)` we assert
/// `Cmp::compare(prev, cur) != Ordering::Greater` (non-decreasing — `MDB_DUPSORT` means
/// adjacent equal keys are valid). A violation means our reimplemented comparator disagrees
/// with the tree's build order on REAL keys → fail closed. This catches wrong-comparator-on-
/// index and FFI/ABI/endianness drift.
///
/// ### Phase B — heed registration / memcmp-fallback detection
///
/// Phase A only proves `Cmp::compare` is internally correct; it never invokes the comparator
/// through LMDB. Phase B does: for keys adjacent to **witness pairs** — adjacent physical
/// pairs where `memcmp(prev, cur) == Greater` while the physical (golpe) order is `prev <
/// cur` — it performs `db.range(K..).next()` (an `MDB_SET_RANGE` seek, which consults the
/// registered comparator) and asserts the first returned key `== K`. A correctly-registered
/// comparator always round-trips an existing key; a silent memcmp fallback on a golpe-ordered
/// tree misses for divergent (witness) keys. If an index's sample yields zero witness pairs,
/// we `tracing::warn!` that registration could not be *adversarially* verified on this
/// dataset (the exact-seek round-trip still held).
///
/// `Event__created_at` uses `heed::IntegerComparator` (LMDB's built-in integer key, not a
/// golpe FFI comparator) — both phases apply with that comparator.
///
/// ## Bounded / read-only guarantees
///
/// Each index is scanned under its own short-lived `read_txn()`, bounded by the sample cap.
/// No write transaction is ever opened (`MDB_RDONLY` only). No code path formats an unbounded
/// vector: error variants carry only the index name, a truncated hex view of the offending
/// key pair, and integer positions.
use crate::lmdb::indexes::{
    open_index_created_at, open_index_string_uint64, open_index_string_uint64_uint64,
    open_index_uint64_uint64, IndexError, ALL_EVENT_INDEXES,
};
use heed::types::Bytes;
use std::cmp::Ordering;
use std::ops::Bound;

/// Maximum number of physical-order keys sampled per index for the self-check.
///
/// Bounds both the read-txn lifetime and memory use against million-entry production indexes.
pub const SELF_CHECK_SAMPLE_CAP: usize = 10_000;

/// Number of leading/trailing bytes shown in truncated hex error output.
const HEX_EDGE: usize = 8;

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

/// Error returned by `run_comparator_self_check`.
///
/// No variant formats an unbounded vector — only the index name, a truncated hex view of the
/// offending key pair, and integer positions/levIds are included.
#[derive(Debug, thiserror::Error)]
pub enum SelfCheckError {
    /// Phase A: the registered comparator reports `prev > cur` for an adjacent physical pair.
    ///
    /// The reimplemented golpe comparator disagrees with the order strfry's real comparator
    /// used to build the B-tree (wrong comparator on index, or FFI/ABI/endianness drift).
    #[error(
        "Comparator self-check FAILED (Phase A) for index '{index}': \
         registered comparator reports prev > cur for adjacent physical keys at pos {pos}.\n\
         prev = {prev_hex}\n\
         cur  = {cur_hex}\n\
         The reimplemented comparator does not reproduce strfry's B-tree build order."
    )]
    MonotonicityViolation {
        index: String,
        pos: usize,
        prev_hex: String,
        cur_hex: String,
    },

    /// Phase B: an exact-seek round-trip on an existing key landed on a different key.
    ///
    /// `db.range(K..).next()` for a key `K` that exists in the index returned a key other
    /// than `K`. A correctly-registered comparator always round-trips an existing key; a
    /// silent memcmp fallback on a golpe-ordered tree misses for divergent keys.
    #[error(
        "Comparator self-check FAILED (Phase B) for index '{index}': \
         MDB_SET_RANGE seek for an existing key did not round-trip.\n\
         sought = {sought_hex}\n\
         landed = {landed_hex}\n\
         The registered comparator is wrong, unregistered, or falls back to memcmp."
    )]
    SeekRoundTripMismatch {
        index: String,
        sought_hex: String,
        landed_hex: String,
    },

    /// LMDB index scan error.
    #[error("LMDB scan error for index '{index}': {source}")]
    IndexScan { index: String, source: IndexError },
}

// ---------------------------------------------------------------------------
// Hex / sampling helpers
// ---------------------------------------------------------------------------

/// Render a key as truncated hex: full if short, else `first8…last8` with the byte length.
///
/// Never emits more than `2 * HEX_EDGE` bytes of hex, so it is safe on million-byte keys.
fn hex_trunc(key: &[u8]) -> String {
    if key.len() <= 2 * HEX_EDGE {
        return hex_of(key);
    }
    format!(
        "{}…{} (len={})",
        hex_of(&key[..HEX_EDGE]),
        hex_of(&key[key.len() - HEX_EDGE..]),
        key.len()
    )
}

fn hex_of(bytes: &[u8]) -> String {
    let mut s = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        s.push_str(&format!("{b:02x}"));
    }
    s
}

/// Collect up to `cap` KEYS (owned) from a typed database in physical (B-tree) iteration order.
///
/// Read-only; the caller's `rtxn` bounds the lifetime. Bounded by `cap`.
fn collect_keys_bounded<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    cap: usize,
    index: &str,
) -> Result<Vec<Vec<u8>>, SelfCheckError>
where
    C: heed::Comparator,
{
    let mut keys = Vec::new();
    let iter = db.iter(rtxn).map_err(|e| SelfCheckError::IndexScan {
        index: index.to_string(),
        source: IndexError::Heed(e),
    })?;
    for result in iter {
        let (key, _value) = result.map_err(|e| SelfCheckError::IndexScan {
            index: index.to_string(),
            source: IndexError::Heed(e),
        })?;
        keys.push(key.to_vec());
        if keys.len() >= cap {
            break;
        }
    }
    Ok(keys)
}

/// Outcome of running both phases on a single index's key sample.
#[derive(Debug)]
struct IndexCheckReport {
    sampled: usize,
    witnesses: usize,
}

/// Run Phase A + Phase B for one index given its physical-order key sample and the registered
/// comparator type `C`. Returns the per-index report or the first fail-closed violation.
fn check_index<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    index: &str,
    keys: &[Vec<u8>],
) -> Result<IndexCheckReport, SelfCheckError>
where
    C: heed::Comparator,
{
    // -------------------------------------------------------------------
    // Phase A — FFI/logic correctness: comparator must be non-decreasing
    // over the physical iteration order. Also collect witness positions
    // (memcmp disagrees with golpe order) for Phase B.
    // -------------------------------------------------------------------
    let mut witness_positions: Vec<usize> = Vec::new();
    for pos in 1..keys.len() {
        let prev = keys[pos - 1].as_slice();
        let cur = keys[pos].as_slice();

        if C::compare(prev, cur) == Ordering::Greater {
            return Err(SelfCheckError::MonotonicityViolation {
                index: index.to_string(),
                pos,
                prev_hex: hex_trunc(prev),
                cur_hex: hex_trunc(cur),
            });
        }

        // Witness pair: physical order is prev < cur (it precedes cur in the tree the real
        // comparator built), yet raw memcmp would order prev AFTER cur. Such keys can only
        // round-trip through a correctly-registered custom comparator — memcmp falls over.
        if prev.cmp(cur) == Ordering::Greater {
            witness_positions.push(pos);
        }
    }

    // -------------------------------------------------------------------
    // Phase B — heed registration / memcmp-fallback detection: an exact
    // seek for an existing key must round-trip. Prioritize witness keys
    // (both members of each witness pair). Fall back to a bounded spread
    // of plain keys if there are no witnesses, so the round-trip is still
    // exercised (it just is not adversarial).
    // -------------------------------------------------------------------
    let witnesses = witness_positions.len();

    let mut seek_targets: Vec<usize> = Vec::new();
    for &pos in &witness_positions {
        seek_targets.push(pos - 1);
        seek_targets.push(pos);
    }
    seek_targets.sort_unstable();
    seek_targets.dedup();

    if seek_targets.is_empty() && !keys.is_empty() {
        // No witnesses on this dataset: exercise a bounded spread of round-trips anyway.
        let step = keys.len().div_ceil(64).max(1);
        let mut i = 0;
        while i < keys.len() {
            seek_targets.push(i);
            i += step;
        }
    }

    for &idx in &seek_targets {
        let sought = keys[idx].as_slice();
        let range = (Bound::Included(sought), Bound::Unbounded);
        let mut iter = db
            .range(rtxn, &range)
            .map_err(|e| SelfCheckError::IndexScan {
                index: index.to_string(),
                source: IndexError::Heed(e),
            })?;
        match iter.next() {
            Some(result) => {
                let (landed, _value) = result.map_err(|e| SelfCheckError::IndexScan {
                    index: index.to_string(),
                    source: IndexError::Heed(e),
                })?;
                if landed != sought {
                    return Err(SelfCheckError::SeekRoundTripMismatch {
                        index: index.to_string(),
                        sought_hex: hex_trunc(sought),
                        landed_hex: hex_trunc(landed),
                    });
                }
            }
            None => {
                // Seeking for a key that exists in the sample must return at least that key.
                return Err(SelfCheckError::SeekRoundTripMismatch {
                    index: index.to_string(),
                    sought_hex: hex_trunc(sought),
                    landed_hex: "<empty range>".to_string(),
                });
            }
        }
    }

    if witnesses == 0 {
        tracing::warn!(
            index = index,
            sampled = keys.len(),
            "Self-check Phase B: no witness pairs in this index sample — comparator \
             registration could not be adversarially verified on this dataset (exact-seek \
             round-trip still held)."
        );
    }

    Ok(IndexCheckReport {
        sampled: keys.len(),
        witnesses,
    })
}

/// Dispatch one index by short name to `check_index` with the correct comparator type.
///
/// Opens a short-lived read transaction, collects a bounded key sample, and runs both phases.
fn check_index_by_name(
    env: &heed::Env,
    short_name: &str,
) -> Result<IndexCheckReport, SelfCheckError> {
    let rtxn = env.read_txn().map_err(|e| SelfCheckError::IndexScan {
        index: short_name.to_string(),
        source: IndexError::Heed(e),
    })?;

    let report = match short_name {
        "Event__id" | "Event__pubkey" | "Event__tag" => {
            let db = open_index_string_uint64(env, &rtxn, short_name).map_err(|e| {
                SelfCheckError::IndexScan {
                    index: short_name.to_string(),
                    source: e,
                }
            })?;
            let keys = collect_keys_bounded(&db, &rtxn, SELF_CHECK_SAMPLE_CAP, short_name)?;
            check_index(&db, &rtxn, short_name, &keys)?
        }
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name).map_err(|e| {
                SelfCheckError::IndexScan {
                    index: short_name.to_string(),
                    source: e,
                }
            })?;
            let keys = collect_keys_bounded(&db, &rtxn, SELF_CHECK_SAMPLE_CAP, short_name)?;
            check_index(&db, &rtxn, short_name, &keys)?
        }
        "Event__pubkeyKind" => {
            let db = open_index_string_uint64_uint64(env, &rtxn, short_name).map_err(|e| {
                SelfCheckError::IndexScan {
                    index: short_name.to_string(),
                    source: e,
                }
            })?;
            let keys = collect_keys_bounded(&db, &rtxn, SELF_CHECK_SAMPLE_CAP, short_name)?;
            check_index(&db, &rtxn, short_name, &keys)?
        }
        "Event__created_at" => {
            let db = open_index_created_at(env, &rtxn).map_err(|e| SelfCheckError::IndexScan {
                index: short_name.to_string(),
                source: e,
            })?;
            let keys = collect_keys_bounded(&db, &rtxn, SELF_CHECK_SAMPLE_CAP, short_name)?;
            check_index(&db, &rtxn, short_name, &keys)?
        }
        other => {
            return Err(SelfCheckError::IndexScan {
                index: other.to_string(),
                source: IndexError::SubDbNotFound {
                    name: other.to_string(),
                },
            });
        }
    };

    Ok(report)
    // rtxn dropped here — short-lived, bounded by SELF_CHECK_SAMPLE_CAP
}

// ---------------------------------------------------------------------------
// Main self-check function (D-13: reusable, callable by Phase 5's /ready)
// ---------------------------------------------------------------------------

/// Run the fixture-free comparator self-check against all six `Event__*` indexes.
///
/// For each index, over a bounded physical-order key sample:
/// - **Phase A** asserts the registered comparator is non-decreasing over the tree's build
///   order (`Cmp::compare(prev, cur) != Greater`).
/// - **Phase B** seeks each witness key via `MDB_SET_RANGE` and asserts the first landed key
///   round-trips exactly, proving the comparator is actually registered (not silently memcmp).
///
/// Returns `Ok(())` only if every index passes both phases. Fails closed on the first
/// violation; no error path logs an unbounded vector.
///
/// ## Usage
///
/// ```rust,no_run
/// # use lmdb2graphql::lmdb::self_check::run_comparator_self_check;
/// # fn doc_example(env: &heed::Env) -> anyhow::Result<()> {
/// run_comparator_self_check(env)?;
/// # Ok(())
/// # }
/// ```
///
/// Phase 5's `/ready` endpoint calls this function directly — do NOT inline in main.rs.
pub fn run_comparator_self_check(env: &heed::Env) -> Result<(), SelfCheckError> {
    let mut total_sampled = 0usize;
    let mut total_witnesses = 0usize;

    for short_name in ALL_EVENT_INDEXES {
        let report = check_index_by_name(env, short_name)?;
        tracing::debug!(
            index = short_name,
            sampled = report.sampled,
            witnesses = report.witnesses,
            "Self-check passed for index (Phase A + Phase B)"
        );
        total_sampled += report.sampled;
        total_witnesses += report.witnesses;
    }

    tracing::info!(
        indexes_checked = ALL_EVENT_INDEXES.len(),
        sample_cap = SELF_CHECK_SAMPLE_CAP,
        total_keys_sampled = total_sampled,
        total_witnesses = total_witnesses,
        "Comparator self-check passed: Phase A (monotonicity) + Phase B (seek round-trip)"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Copy the committed fixture to a tempdir and open an env there.
    /// NEVER touches ~/deepfry/ — the fixture is the test oracle only.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// hex_trunc must never emit more than 2*HEX_EDGE bytes of hex regardless of key length.
    #[test]
    fn test_hex_trunc_is_bounded() {
        let big = vec![0xABu8; 1_000_000];
        let s = hex_trunc(&big);
        // first8 (16 hex) + "…" + last8 (16 hex) + " (len=1000000)"
        assert!(s.contains("…"), "long key must be truncated");
        assert!(s.contains("len=1000000"), "must report true length");
        // The hex portion is bounded: at most 2*HEX_EDGE bytes = 32 hex chars.
        let hex_chars = s.chars().filter(|c| c.is_ascii_hexdigit()).count();
        // Allow the digits inside "len=1000000" too, but the hex run itself is bounded.
        assert!(
            hex_chars < 64,
            "hex output must stay bounded even for million-byte keys, got {hex_chars}"
        );

        let small = [0x01, 0x02, 0x03];
        assert_eq!(hex_trunc(&small), "010203");
    }

    /// Phase A non-vacuous guard: a deliberately-reversed comparison trips
    /// MonotonicityViolation on the fixture's physical key order (T-03-04 spirit).
    ///
    /// We feed the real fixture key sample through `check_index` with a comparator whose
    /// `compare` is the REVERSE of the correct golpe order. Because the fixture keys are in
    /// ascending golpe order, a reversed comparator reports `prev > cur` for the first
    /// non-equal adjacent pair → fail closed. This proves the guard is real, not vacuous.
    #[test]
    fn test_phase_a_trips_on_reversed_comparator() {
        use crate::lmdb::comparators::Uint64Uint64Cmp;
        use crate::lmdb::indexes::open_index_uint64_uint64;

        /// A comparator that reverses the correct golpe Uint64Uint64 ordering.
        enum ReversedUint64Uint64Cmp {}
        impl heed::Comparator for ReversedUint64Uint64Cmp {
            fn compare(a: &[u8], b: &[u8]) -> Ordering {
                Uint64Uint64Cmp::compare(a, b).reverse()
            }
        }

        let (env, _tmp) = open_temp_fixture_env();
        let rtxn = env.read_txn().expect("read txn");

        // Collect the physical key sample with the CORRECT comparator open (build order).
        let db_correct =
            open_index_uint64_uint64(&env, &rtxn, "Event__kind").expect("open Event__kind");
        let keys = collect_keys_bounded(&db_correct, &rtxn, SELF_CHECK_SAMPLE_CAP, "Event__kind")
            .expect("collect keys");
        assert!(keys.len() > 1, "fixture Event__kind must have >1 key");

        // Re-open the SAME sub-DB with the REVERSED comparator and run the check.
        // Phase A must trip MonotonicityViolation.
        let db_rev: heed::Database<Bytes, Bytes, ReversedUint64Uint64Cmp> = env
            .database_options()
            .types::<Bytes, Bytes>()
            .key_comparator::<ReversedUint64Uint64Cmp>()
            .name(&crate::lmdb::indexes::full_db_name("Event__kind"))
            .open(&rtxn)
            .expect("open reversed")
            .expect("Event__kind must exist");

        let result = check_index(&db_rev, &rtxn, "Event__kind", &keys);
        match result {
            Err(SelfCheckError::MonotonicityViolation { index, .. }) => {
                assert_eq!(index, "Event__kind");
            }
            other => panic!("expected MonotonicityViolation, got {other:?}"),
        }
    }

    /// Phase B is genuinely exercised on the fixture: Event__kind has the kind=255/256
    /// witness pair, so the witness count for that index must be >= 1.
    #[test]
    fn test_fixture_event_kind_has_witness_pair() {
        let (env, _tmp) = open_temp_fixture_env();
        let report =
            check_index_by_name(&env, "Event__kind").expect("Event__kind self-check must pass");
        assert!(
            report.witnesses >= 1,
            "fixture Event__kind must contain >= 1 witness pair (kind=255/256) so Phase B is \
             adversarially exercised; got {}",
            report.witnesses
        );
    }

    /// Full self-check returns Ok(()) on the committed fixture.
    #[test]
    fn test_run_self_check_ok_on_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        run_comparator_self_check(&env).expect("self-check must pass on the committed fixture");
    }
}
