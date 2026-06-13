/// indexes.rs — Open all six Event__* sub-DBs with their correct golpe comparators.
///
/// ## Index → Comparator mapping (authoritative from golpe.yaml + spec §3)
///
/// | Index               | Full sub-DB name                              | Comparator           | Key format                        |
/// |---------------------|-----------------------------------------------|----------------------|-----------------------------------|
/// | `Event__id`         | `rasgueadb_defaultDb__Event__id`              | `StringUint64Cmp`    | id(32) ‖ created_at(8 LE)         |
/// | `Event__pubkey`     | `rasgueadb_defaultDb__Event__pubkey`          | `StringUint64Cmp`    | pubkey(32) ‖ created_at(8 LE)     |
/// | `Event__tag`        | `rasgueadb_defaultDb__Event__tag`             | `StringUint64Cmp`    | tagName(1) ‖ tagValue(var) ‖ created_at(8 LE) |
/// | `Event__kind`       | `rasgueadb_defaultDb__Event__kind`            | `Uint64Uint64Cmp`    | kind(8 LE) ‖ created_at(8 LE)     |
/// | `Event__pubkeyKind` | `rasgueadb_defaultDb__Event__pubkeyKind`      | `StringUint64Uint64Cmp` | pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE) |
/// | `Event__created_at` | `rasgueadb_defaultDb__Event__created_at`      | `IntegerComparator`  | created_at (MDB_INTEGERKEY)       |
///
/// ## Sub-DB naming
///
/// All strfry sub-DBs are prefixed with `rasgueadb_defaultDb__`.
/// Discovered by iterating the unnamed root DB of the committed fixture (plan 01-03 Task 1 probe).
///
/// ## Critical: always use `.open()` never `.create()`
///
/// `create()` sets `MDB_CREATE` and would create new sub-DBs in strfry's env — catastrophic
/// for a read-only consumer. `.open()` on a non-existent sub-DB returns `Ok(None)`.
///
/// ## Critical: comparator MUST be registered on every open
///
/// LMDB comparators are process-memory state, not persistent. Forgetting `.key_comparator::<T>()`
/// causes silent wrong range scan results (Pitfall 1, RESEARCH.md). Every `Event__*` open
/// in this file uses an explicit comparator type parameter.
use crate::lmdb::comparators::{StringUint64Cmp, StringUint64Uint64Cmp, Uint64Uint64Cmp};
use heed::types::Bytes;
use std::ops::Bound;

// ---------------------------------------------------------------------------
// Sub-DB name constants (rasgueadb_defaultDb__ prefix, confirmed by fixture probe)
// ---------------------------------------------------------------------------

/// All six Event__* index sub-DB names (D-07: self-check covers all six).
/// Used by `open_all_event_indexes` and the comparator self-check scanner.
pub const ALL_EVENT_INDEXES: [&str; 6] = [
    "Event__id",
    "Event__pubkey",
    "Event__created_at",
    "Event__kind",
    "Event__pubkeyKind",
    "Event__tag",
];

/// Prefix that golpe/rasgueadb prepends to all named sub-DB names.
/// Discovered by iterating the unnamed root DB of the fixture (2026-06-10 probe).
pub const RASGUEADB_PREFIX: &str = "rasgueadb_defaultDb__";

/// Resolve the full sub-DB name from a short name (e.g. "Event__id").
pub fn full_db_name(short_name: &str) -> String {
    format!("{RASGUEADB_PREFIX}{short_name}")
}

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

/// Error opening an Event__* index sub-DB.
#[derive(Debug, thiserror::Error)]
pub enum IndexError {
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    #[error("Sub-DB '{name}' not found in strfry env — is this the right LMDB directory?")]
    SubDbNotFound { name: String },

    /// WR-07: a key in a sub-DB was not the expected width (e.g. an `EventPayload`
    /// IntegerKey that is not exactly 8 bytes). Signals a structural surprise from the
    /// externally-owned strfry DB rather than masking it as a zero/empty result.
    #[error("malformed key in sub-DB '{name}': expected {expected} bytes, got {actual}")]
    MalformedKey {
        name: String,
        expected: usize,
        actual: usize,
    },
}

// ---------------------------------------------------------------------------
// Open helpers — one per comparator type, forced by type parameter
// ---------------------------------------------------------------------------
//
// The comparator is a required type parameter at each open site to prevent the
// silent misregistration footgun (Pitfall 1: open without comparator → memcmp fallback).
// The self-check catches wiring errors, but preventing them at the call site is better.

/// Open an `Event__*` sub-DB with the `StringUint64Cmp` comparator.
/// Used for: `Event__id`, `Event__pubkey`, `Event__tag`.
pub fn open_index_string_uint64<'env>(
    env: &'env heed::Env,
    rtxn: &heed::RoTxn<'env>,
    short_name: &str,
) -> Result<heed::Database<Bytes, Bytes, StringUint64Cmp>, IndexError> {
    let full = full_db_name(short_name);
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<StringUint64Cmp>()
        .name(&full)
        .open(rtxn)?
        .ok_or_else(|| IndexError::SubDbNotFound { name: full })
}

/// Open an `Event__*` sub-DB with the `Uint64Uint64Cmp` comparator.
/// Used for: `Event__kind`.
pub fn open_index_uint64_uint64<'env>(
    env: &'env heed::Env,
    rtxn: &heed::RoTxn<'env>,
    short_name: &str,
) -> Result<heed::Database<Bytes, Bytes, Uint64Uint64Cmp>, IndexError> {
    let full = full_db_name(short_name);
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<Uint64Uint64Cmp>()
        .name(&full)
        .open(rtxn)?
        .ok_or_else(|| IndexError::SubDbNotFound { name: full })
}

/// Open an `Event__*` sub-DB with the `StringUint64Uint64Cmp` comparator.
/// Used for: `Event__pubkeyKind`.
pub fn open_index_string_uint64_uint64<'env>(
    env: &'env heed::Env,
    rtxn: &heed::RoTxn<'env>,
    short_name: &str,
) -> Result<heed::Database<Bytes, Bytes, StringUint64Uint64Cmp>, IndexError> {
    let full = full_db_name(short_name);
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<StringUint64Uint64Cmp>()
        .name(&full)
        .open(rtxn)?
        .ok_or_else(|| IndexError::SubDbNotFound { name: full })
}

/// Open `Event__created_at` with `IntegerComparator` (MDB_INTEGERKEY — NOT a golpe comparator).
/// Pitfall 5: this index uses LMDB's built-in integer key, not a custom golpe comparator.
pub fn open_index_created_at<'env>(
    env: &'env heed::Env,
    rtxn: &heed::RoTxn<'env>,
) -> Result<heed::Database<Bytes, Bytes, heed::IntegerComparator>, IndexError> {
    let full = full_db_name("Event__created_at");
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name(&full)
        .open(rtxn)?
        .ok_or_else(|| IndexError::SubDbNotFound { name: full })
}

// ---------------------------------------------------------------------------
// Unified scan helper for the self-check (operates on raw Bytes)
// ---------------------------------------------------------------------------

/// Scan all entries of a named Event__* index and collect the VALUE-side levIds.
///
/// Per spec §3.1: in every Event__* index sub-DB the levId is the 8-byte little-endian
/// LMDB VALUE of each entry. The composite field (id‖created_at, etc.) is the KEY.
/// The self-check therefore collects VALUE-side 8-byte LE levIds in key-scan order.
///
/// This function opens the correct comparator based on the short index name and
/// returns the ordered sequence of levIds.
///
/// The LMDB env is opened with the correct comparator for key-scan order. Each VALUE
/// is interpreted as an 8-byte LE uint64 (the levId).
pub fn scan_lev_ids_for_index(env: &heed::Env, short_name: &str) -> Result<Vec<u64>, IndexError> {
    let rtxn = env.read_txn()?;

    let lev_ids = match short_name {
        "Event__id" | "Event__pubkey" | "Event__tag" => {
            let db = open_index_string_uint64(env, &rtxn, short_name)?;
            collect_lev_ids_dup(&db, &rtxn)?
        }
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
            collect_lev_ids_dup(&db, &rtxn)?
        }
        "Event__pubkeyKind" => {
            let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
            collect_lev_ids_dup(&db, &rtxn)?
        }
        "Event__created_at" => {
            let db = open_index_created_at(env, &rtxn)?;
            collect_lev_ids_dup(&db, &rtxn)?
        }
        _ => {
            return Err(IndexError::SubDbNotFound {
                name: short_name.to_string(),
            })
        }
    };

    Ok(lev_ids)
}

// ---------------------------------------------------------------------------
// Comparator-exercising range-seek helper (CR-01 — startup comparator gate)
// ---------------------------------------------------------------------------

/// Seek the first entry with key >= `lower_bound_key` in the named Event__* index and
/// return its VALUE-side levId (spec §3.1: 8-byte LE u64).
///
/// ## Why this exercises the comparator (unlike `db.iter()`)
///
/// A forward `db.iter()` walks the B-tree in physically-stored page order — the order
/// strfry wrote when building the fixture — and never invokes the registered comparator.
/// `db.range(rtxn, &(lower_bound..))` maps to LMDB's `MDB_SET_RANGE` positioning
/// operation, which traverses B-tree branch nodes using the registered comparator to
/// decide which subtree to descend into. If the wrong comparator (or no comparator)
/// is registered, the cursor lands on the wrong entry — the call provably exercises
/// the registered comparator. (CR-01 mitigation, closes LMDB-06 gap.)
///
/// ## Adversarial key pairs used by the comparator gate
///
/// For `Event__kind` (Uint64Uint64Cmp):
///   lower_bound = (kind=256, ts=0) = `[0x00, 0x01, 0x00×6, 0x00×8]`
///   Golpe result: kind=255 < kind=256 numerically → cursor skips kind=255 → lands on
///     (kind=256, ts=1700000000) = levId=2.
///   Memcmp result (on golpe-built B-tree): kind=255 LE `[0xFF, 0x00, …]` > kind=256 LE
///     `[0x00, 0x01, …]` under memcmp → kind=255 entry is the FIRST >= lower_bound →
///     cursor lands on (kind=255, ts=1700000000) = levId=3.  (WRONG — gate trips.)
///
/// For `Event__pubkeyKind` (StringUint64Uint64Cmp):
///   Same logic applied to pubkey=79be… prefix, kind=256, ts=0 as lower bound.
///
/// ## Invariants
///
/// - Opens a per-call `read_txn()` (short-lived, dropped before return).
/// - Never opens a write transaction.  (T-04-03 / LMDB-01)
/// - Returns `Ok(None)` if the range starting at `lower_bound_key` is empty.
pub fn seek_first_ge_lev_id(
    env: &heed::Env,
    short_name: &str,
    lower_bound_key: &[u8],
) -> Result<Option<u64>, IndexError> {
    let rtxn = env.read_txn()?;

    let lev_id = match short_name {
        "Event__id" | "Event__pubkey" | "Event__tag" => {
            let db = open_index_string_uint64(env, &rtxn, short_name)?;
            seek_range_first_lev_id(&db, &rtxn, lower_bound_key)?
        }
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
            seek_range_first_lev_id(&db, &rtxn, lower_bound_key)?
        }
        "Event__pubkeyKind" => {
            let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
            seek_range_first_lev_id(&db, &rtxn, lower_bound_key)?
        }
        _ => {
            return Err(IndexError::SubDbNotFound {
                name: short_name.to_string(),
            })
        }
    };

    Ok(lev_id)
    // rtxn dropped here — short-lived, bounded to single MDB_SET_RANGE + first-entry read
}

/// Perform a range seek on any typed database and return the VALUE-side levId of the
/// first entry with key >= `lower_bound_key`.
///
/// The range iterator maps to `MDB_SET_RANGE` — this IS the operation that forces
/// LMDB to consult the registered comparator for cursor positioning.
fn seek_range_first_lev_id<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    lower_bound_key: &[u8],
) -> Result<Option<u64>, IndexError>
where
    C: heed::Comparator,
{
    // Use (Bound::Included(&[u8]), Bound::Unbounded) — the pattern for Bytes-keyed
    // databases in heed 0.22 (see heed cookbook "Use Bytes as Cursor Ranges").
    let range = (Bound::Included(lower_bound_key), Bound::Unbounded);
    let mut iter = db.range(rtxn, &range)?;
    match iter.next() {
        None => Ok(None),
        Some(result) => {
            let (_key, value) = result?;
            if value.len() < 8 {
                tracing::warn!(
                    value_len = value.len(),
                    "seek_range_first_lev_id: VALUE shorter than 8 bytes — expected levId u64"
                );
                return Ok(None);
            }
            let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
            Ok(Some(lev_id))
        }
    }
}

/// Collect VALUE-side levIds from a database that may have duplicate VALUES per KEY.
///
/// strfry Event__* indexes use `MDB_DUPSORT` + `MDB_INTEGERDUP` (LMDB duplicate key support).
/// Each key may map to multiple VALUES (multiple events with the same index key).
/// The cursor iterates all key+duplicate-value pairs in sort order.
///
/// levId extraction: each VALUE is exactly 8 bytes, interpreted as u64 LE.
fn collect_lev_ids_dup<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
) -> Result<Vec<u64>, IndexError>
where
    C: heed::Comparator,
{
    let mut lev_ids = Vec::new();
    let iter = db.iter(rtxn)?;
    for result in iter {
        let (_key, value) = result?;
        if value.len() < 8 {
            // Malformed: levId must be exactly 8 bytes. Skip with a warning.
            // In a correct strfry DB this should never happen.
            tracing::warn!(
                value_len = value.len(),
                "Event__* index VALUE shorter than 8 bytes — skipping (expected levId u64)"
            );
            continue;
        }
        let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
        lev_ids.push(lev_id);
    }
    Ok(lev_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Copy fixture to tempdir and open an env there.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// All six Event__* indexes open successfully on the committed fixture.
    /// Verifies each sub-DB exists with the expected name.
    #[test]
    fn test_open_all_six_indexes_on_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        let rtxn = env.read_txn().expect("read txn");

        open_index_string_uint64(&env, &rtxn, "Event__id")
            .expect("Event__id must open");
        open_index_string_uint64(&env, &rtxn, "Event__pubkey")
            .expect("Event__pubkey must open");
        open_index_string_uint64(&env, &rtxn, "Event__tag")
            .expect("Event__tag must open");
        open_index_uint64_uint64(&env, &rtxn, "Event__kind")
            .expect("Event__kind must open");
        open_index_string_uint64_uint64(&env, &rtxn, "Event__pubkeyKind")
            .expect("Event__pubkeyKind must open");
        open_index_created_at(&env, &rtxn)
            .expect("Event__created_at must open");
    }

    /// Verify ALL_EVENT_INDEXES contains all six index names (D-07).
    #[test]
    fn test_all_event_indexes_constant_has_six_entries() {
        assert_eq!(
            ALL_EVENT_INDEXES.len(),
            6,
            "ALL_EVENT_INDEXES must list all 6 Event__* indexes"
        );
        let expected = [
            "Event__id",
            "Event__pubkey",
            "Event__created_at",
            "Event__kind",
            "Event__pubkeyKind",
            "Event__tag",
        ];
        for name in expected {
            assert!(
                ALL_EVENT_INDEXES.contains(&name),
                "ALL_EVENT_INDEXES must contain {name}"
            );
        }
    }

    /// scan_lev_ids_for_index returns a non-empty Vec for each index on the fixture.
    /// (Full-sequence correctness is validated by the self-check test.)
    #[test]
    fn test_scan_lev_ids_returns_events_for_all_indexes() {
        let (env, _tmp) = open_temp_fixture_env();
        for short_name in ALL_EVENT_INDEXES {
            let lev_ids = scan_lev_ids_for_index(&env, short_name)
                .unwrap_or_else(|e| panic!("scan_lev_ids_for_index({short_name}): {e}"));
            assert!(
                !lev_ids.is_empty(),
                "index {short_name} must have at least one entry in the fixture"
            );
            println!("{short_name}: {} entries, first levId={}", lev_ids.len(), lev_ids[0]);
        }
    }

    /// CR-01 comparator gate: seek_first_ge_lev_id on the Event__kind adversarial pair.
    ///
    /// Lower bound: (kind=256, ts=0) — the first valid key that is numerically >= any
    /// kind=256 entry.  In the committed fixture:
    ///   - golpe numeric order: kind=255 (levId=3) < kind=256 (levId=2)
    ///   - A seek with the registered Uint64Uint64Cmp comparator must skip kind=255
    ///     (because 255 < 256) and land on the kind=256 entry (levId=2).
    ///   - A memcmp-positioned cursor would NOT skip kind=255 because kind=255 LE bytes
    ///     [0xFF, 0x00, …] > kind=256 LE bytes [0x00, 0x01, …] under memcmp — kind=255
    ///     would appear to qualify first and the cursor would land on levId=3 (wrong).
    ///
    /// This test proves the golpe Uint64Uint64Cmp comparator is consulted at startup
    /// (assertion levId=2, not levId=3 — the memcmp neighbor).
    #[test]
    fn test_seek_first_ge_lev_id_event_kind_adversarial_pair() {
        let (env, _tmp) = open_temp_fixture_env();

        // Build lower-bound key: (kind=256, ts=0) as [kind(8 LE) || ts(8 LE)]
        // kind=256 LE: [0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
        // ts=0 LE:     [0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
        let mut lower_bound = Vec::with_capacity(16);
        lower_bound.extend_from_slice(&256u64.to_le_bytes()); // kind=256
        lower_bound.extend_from_slice(&0u64.to_le_bytes());   // ts=0 (lower bound)

        let result = seek_first_ge_lev_id(&env, "Event__kind", &lower_bound)
            .expect("seek_first_ge_lev_id must not error on fixture");

        let lev_id = result.expect("seek must find an entry — kind=256 exists in fixture");

        // Golpe-correct: cursor must land on kind=256 (levId=2), NOT kind=255 (levId=3).
        // If the comparator is wrong/absent, kind=255 LE bytes [0xFF...] > lower_bound [0x00...]
        // under memcmp, so kind=255 would be the first "≥ lower_bound" entry → levId=3.
        assert_eq!(
            lev_id, 2,
            "seek_first_ge_lev_id on Event__kind with lower_bound=(kind=256, ts=0) \
             MUST return levId=2 (kind=256 entry) — got levId={lev_id}. \
             levId=3 would indicate the comparator is NOT exercising golpe numeric ordering \
             (memcmp fallback — kind=255 LE bytes [0xFF..] are > kind=256 LE bytes [0x00..] \
             under memcmp, so memcmp incorrectly places kind=255 as the first ≥ entry)."
        );

        println!(
            "CR-01 gate PASS: Event__kind adversarial seek landed on levId={lev_id} \
             (golpe-correct kind=256 neighbor, not memcmp neighbor kind=255)"
        );
    }
}
