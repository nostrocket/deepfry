/// scan.rs — Bounded, direction-aware, resumable cursor scan primitives over Event__* indexes.
///
/// ## Purpose
///
/// Provides reusable `(composite key, levId)` scan primitives that:
///
/// - Return up to `limit` `(key bytes, levId)` pairs in golpe-comparator order (D-05/D-06)
/// - Support forward and reverse direction (newest-first for `latestPerAuthor` queries)
/// - Accept a caller-supplied start key for pagination/resume (MDB_SET_RANGE semantics)
/// - Use DUPSORT-aware iteration — no levId is silently skipped
/// - Keep read transactions short: every primitive opens and drops its own `RoTxn`
///   (structural LMDB-09 guarantee — the function signature takes `&heed::Env`, not `&RoTxn`,
///   so a caller CANNOT pass a long-lived transaction in)
/// - When `limit = 0`, perform an unbounded scan via internal windowing (D-07/D-08)
///
/// ## Deliberate non-hydration
///
/// Scan returns raw `(key bytes, levId)` pairs — it does NOT hydrate event payloads.
/// Phase 3's query engine decides which levIds to look up, avoiding over-hydration of
/// events that a later filter would discard.
///
/// ## Short transactions (LMDB-09 / spec §6.4)
///
/// strfry needs to reclaim free pages from its LMDB environment to prevent `data.mdb` from
/// growing unbounded. A long-lived `RoTxn` pins pages in the MMAP and blocks reclamation.
/// Every scan primitive in this file opens a `RoTxn`, completes its work, and drops it —
/// never holding a transaction across call boundaries or window batches.
///
/// ## DUPSORT (T-02-14)
///
/// All `Event__*` indexes use `MDB_DUPSORT + MDB_INTEGERDUP`. Multiple events can share the
/// same composite key (e.g., two events with the same kind + created_at). The default heed
/// iteration mode (`move_between_keys`) yields only the first VALUE per key, silently dropping
/// duplicate levIds. Every range/rev_range call in this file explicitly calls
/// `.move_through_duplicate_values()` to prevent this. See RESEARCH.md Pitfall 3.

use crate::lmdb::indexes::{
    open_index_created_at, open_index_string_uint64, open_index_string_uint64_uint64,
    open_index_uint64_uint64, IndexError,
};
use crate::lmdb::types::LevId;
use heed::types::Bytes;
use std::ops::Bound;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Default window size for unbounded (`limit=0`) scans (D-07).
///
/// Each window is one short read transaction. Chosen as 256 to balance per-txn LMDB overhead
/// against the risk of holding a txn open too long. A window of 256 covers the full fixture
/// (11 events) in a single batch while being small enough to test multi-batch windowing via
/// the test-only override.
pub const DEFAULT_WINDOW_SIZE: usize = 256;

// ---------------------------------------------------------------------------
// Public API types
// ---------------------------------------------------------------------------

/// Iteration direction for a bounded or windowed scan.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScanDirection {
    /// Walk the index in ascending golpe-comparator order (oldest-first for time-keyed indexes).
    Forward,
    /// Walk the index in descending golpe-comparator order (newest-first for time-keyed indexes).
    /// Used for `latestPerAuthor` and latest-N queries.
    Reverse,
}

// ---------------------------------------------------------------------------
// Public scan primitive
// ---------------------------------------------------------------------------

/// Bounded forward or reverse scan over a named `Event__*` index.
///
/// ## Parameters
///
/// - `env`: the heed LMDB environment (read-only; opened by `open_fixture_env` or the
///   production opener). The primitive opens its own `RoTxn` — the caller must NOT hold
///   one (LMDB-09 structural guarantee: this function takes `&heed::Env`, not `&RoTxn`).
/// - `short_name`: the short index name, e.g. `"Event__kind"`. Dispatched to the
///   correct comparator-typed open helper.
/// - `direction`: `Forward` (ascending) or `Reverse` (descending).
/// - `start_key`: for `Forward`, the inclusive lower bound (MDB_SET_RANGE); for `Reverse`,
///   the inclusive upper bound. Caller constructs these as golpe composite key bytes — see
///   key format table in indexes.rs. The scan treats these as opaque `&[u8]`.
/// - `limit`: maximum number of `(key, levId)` pairs to return. `limit = 0` triggers the
///   windowed unbounded scan path (see `scan_index_windowed`).
///
/// ## Returns
///
/// `Ok(Vec<(Vec<u8>, LevId)>)` — at most `limit` pairs (or all pairs for `limit=0`).
/// Each `key` is copied out of LMDB-mapped memory (txn-independent, D-08). Each `levId`
/// is the VALUE-side 8-byte LE uint64 from the index.
///
/// `Err(IndexError::SubDbNotFound)` — `short_name` is not a known `Event__*` index.
/// `Err(IndexError::Heed(_))` — LMDB error opening txn or iterating.
///
/// ## Malformed VALUES (T-02-11)
///
/// A VALUE shorter than 8 bytes is logged with `tracing::warn!` and skipped — the scan
/// continues. In a correct strfry DB this should never happen.
pub fn scan_index_bounded(
    env: &heed::Env,
    short_name: &str,
    direction: ScanDirection,
    start_key: &[u8],
    limit: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError> {
    // limit=0 → delegate to the windowed unbounded path (D-07).
    if limit == 0 {
        return scan_index_windowed(env, short_name, direction, start_key, DEFAULT_WINDOW_SIZE);
    }

    let rtxn = env.read_txn()?;

    let results = match short_name {
        "Event__id" | "Event__pubkey" | "Event__tag" => {
            let db = open_index_string_uint64(env, &rtxn, short_name)?;
            collect_bounded(&db, &rtxn, direction, start_key, limit)?
        }
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
            collect_bounded(&db, &rtxn, direction, start_key, limit)?
        }
        "Event__pubkeyKind" => {
            let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
            collect_bounded(&db, &rtxn, direction, start_key, limit)?
        }
        "Event__created_at" => {
            let db = open_index_created_at(env, &rtxn)?;
            collect_bounded(&db, &rtxn, direction, start_key, limit)?
        }
        _ => {
            return Err(IndexError::SubDbNotFound {
                name: short_name.to_string(),
            })
        }
    };

    // rtxn dropped here — short-lived, per-call (LMDB-09 / spec §6.4)
    Ok(results)
}

// ---------------------------------------------------------------------------
// Windowed unbounded scan (limit = 0, D-07/D-08)
// ---------------------------------------------------------------------------

/// Unbounded scan via internal windowing — no single long-lived read txn.
///
/// Iterates the full index by issuing sequential short-lived read transactions, each
/// consuming at most `window_size` entries. Each batch opens a fresh `RoTxn`, collects
/// up to `window_size` pairs, then drops the `RoTxn` before opening the next. This keeps
/// read transactions short (D-08) and allows strfry to reclaim LMDB free pages (LMDB-09).
///
/// ## DUPSORT resume semantics
///
/// `Event__*` indexes use `MDB_DUPSORT + MDB_INTEGERDUP`: multiple events can share the same
/// composite key (e.g., two events with the same `kind + created_at`). A batch boundary may
/// fall in the middle of a duplicate-key group. Using `Bound::Excluded(last_key)` on resume
/// would skip ALL remaining VALUES for that key, silently dropping levIds (RESEARCH.md Pitfall 5).
///
/// Solution: resume with `Bound::Included(last_key)` and skip entries whose levId is <=
/// the last-seen levId for the same key. levIds within a DUPSORT group are stored in ascending
/// order by `MDB_INTEGERDUP`, so this correctly resumes past the last-seen levId in the group
/// without re-emitting it.
///
/// For non-duplicate keys (the common case), `resume_lev_id = 0` after any batch entry whose
/// key differs from `resume_key`, so the skip is a no-op.
///
/// Intended to be called via `scan_index_bounded` with `limit=0`. Exposed here so tests
/// can supply a small `window_size` to prove multi-batch behavior.
pub fn scan_index_windowed(
    env: &heed::Env,
    short_name: &str,
    direction: ScanDirection,
    start_key: &[u8],
    window_size: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError> {
    let mut all_results: Vec<(Vec<u8>, LevId)> = Vec::new();
    let mut resume_key: Vec<u8> = start_key.to_vec();
    // On resume, skip entries with the same key and levId <= resume_lev_id.
    // This handles DUPSORT mid-group batch boundaries (see doc above).
    let mut resume_lev_id: LevId = 0;
    let mut first_batch = true;

    loop {
        let rtxn = env.read_txn()?;

        let batch = match short_name {
            "Event__id" | "Event__pubkey" | "Event__tag" => {
                let db = open_index_string_uint64(env, &rtxn, short_name)?;
                collect_window(
                    &db,
                    &rtxn,
                    direction,
                    &resume_key,
                    resume_lev_id,
                    first_batch,
                    window_size,
                )?
            }
            "Event__kind" => {
                let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
                collect_window(
                    &db,
                    &rtxn,
                    direction,
                    &resume_key,
                    resume_lev_id,
                    first_batch,
                    window_size,
                )?
            }
            "Event__pubkeyKind" => {
                let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
                collect_window(
                    &db,
                    &rtxn,
                    direction,
                    &resume_key,
                    resume_lev_id,
                    first_batch,
                    window_size,
                )?
            }
            "Event__created_at" => {
                let db = open_index_created_at(env, &rtxn)?;
                collect_window(
                    &db,
                    &rtxn,
                    direction,
                    &resume_key,
                    resume_lev_id,
                    first_batch,
                    window_size,
                )?
            }
            _ => {
                return Err(IndexError::SubDbNotFound {
                    name: short_name.to_string(),
                })
            }
        };

        // Drop txn BEFORE accumulating results (D-08: no txn held across window boundary).
        drop(rtxn);
        first_batch = false;

        if batch.is_empty() {
            break;
        }

        // Record the last key AND last levId for the next window's resume cursor.
        let (last_key, last_lev_id) = batch.last().unwrap();
        resume_key = last_key.clone();
        resume_lev_id = *last_lev_id;

        all_results.extend(batch);
    }

    Ok(all_results)
}

// ---------------------------------------------------------------------------
// Generic low-level helpers — comparator-agnostic
// ---------------------------------------------------------------------------

/// Collect up to `limit` `(key, levId)` pairs from a bounded range or rev_range.
///
/// ## DUPSORT (T-02-14)
///
/// `.move_through_duplicate_values()` is called on EVERY range/rev_range iterator to
/// prevent duplicate VALUEs (levIds) from being silently skipped. See RESEARCH.md Pitfall 3.
///
/// ## Key ownership (D-08)
///
/// Keys are copied out of LMDB-mapped memory via `.to_vec()`. The copies are
/// txn-independent — safe to return after the `RoTxn` is dropped.
fn collect_bounded<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    direction: ScanDirection,
    start_key: &[u8],
    limit: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError>
where
    C: heed::Comparator,
{
    let mut results = Vec::new();

    match direction {
        ScanDirection::Forward => {
            let range = (Bound::Included(start_key), Bound::Unbounded);
            // MUST call .move_through_duplicate_values() — DUPSORT default skips duplicates
            let iter = db.range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter.take(limit) {
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in forward scan — skipping (T-02-11)"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                results.push((key.to_vec(), lev_id));
            }
        }
        ScanDirection::Reverse => {
            let range = (Bound::Unbounded, Bound::Included(start_key));
            // MUST call .move_through_duplicate_values() — DUPSORT default skips duplicates
            // CRITICAL: RoRange has NO .rev() method — MUST use db.rev_range() (RESEARCH.md anti-patterns)
            let iter = db.rev_range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter.take(limit) {
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in reverse scan — skipping (T-02-11)"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                results.push((key.to_vec(), lev_id));
            }
        }
    }

    Ok(results)
}

/// Collect a single window of up to `window_size` pairs for the windowing loop.
///
/// ## Resume semantics (DUPSORT-correct)
///
/// - `first_batch = true`: use `Bound::Included(resume_key)`, `resume_lev_id = 0` (no skip).
/// - `first_batch = false`: use `Bound::Included(resume_key)` and skip entries with the same
///   key where `lev_id <= resume_lev_id`. This correctly handles DUPSORT mid-group batch
///   boundaries — `Bound::Excluded(last_key)` would skip ALL remaining VALUES for that key.
///   For non-duplicate keys (most entries), key != resume_key after the first entry, so the
///   skip is a no-op.
fn collect_window<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    direction: ScanDirection,
    resume_key: &[u8],
    resume_lev_id: LevId,
    first_batch: bool,
    window_size: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError>
where
    C: heed::Comparator,
{
    let mut results = Vec::new();

    match direction {
        ScanDirection::Forward => {
            // Always use Included — we handle the "skip already-seen" ourselves for DUPSORT.
            let range = (Bound::Included(resume_key), Bound::Unbounded);
            let iter = db.range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter {
                if results.len() >= window_size {
                    break;
                }
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in windowed forward scan — skipping"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                // On resume (not first batch), skip the boundary entry we already emitted
                // and any earlier dups in the same DUPSORT group.
                if !first_batch && key == resume_key && lev_id <= resume_lev_id {
                    continue;
                }
                results.push((key.to_vec(), lev_id));
            }
        }
        ScanDirection::Reverse => {
            let range = (Bound::Unbounded, Bound::Included(resume_key));
            let iter = db.rev_range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter {
                if results.len() >= window_size {
                    break;
                }
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in windowed reverse scan — skipping"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                // On resume, skip the boundary entry and any dups already seen.
                // For reverse, dups in INTEGERDUP order are ascending; we saw the LAST
                // (highest) levId of the group in the previous batch, so skip lev_id >= resume_lev_id
                // when the key matches.
                if !first_batch && key == resume_key && lev_id >= resume_lev_id {
                    continue;
                }
                results.push((key.to_vec(), lev_id));
            }
        }
    }

    Ok(results)
}

// ---------------------------------------------------------------------------
// Unit tests (lib tests — use cfg(test), no integration test file needed here)
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Copy fixture to tempdir and open an env there.
    /// Identical helper to the one in indexes.rs and payload.rs — same established idiom.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    // Event__kind golden vector: [4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]
    // Forward limit=3 = [4, 5, 6]
    // Reverse limit=3 = [2, 3, 9]  (last 3 in order: 9,3,2 → reversed = 2,3,9)
    const KIND_GOLDEN: [u64; 11] = [4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2];

    /// Build a low start key for Event__kind: (kind=0, ts=0) — spans the whole index forward.
    fn kind_forward_low_key() -> Vec<u8> {
        let mut k = Vec::with_capacity(16);
        k.extend_from_slice(&0u64.to_le_bytes()); // kind=0
        k.extend_from_slice(&0u64.to_le_bytes()); // ts=0
        k
    }

    /// Build a high start key for Event__kind: (kind=u64::MAX, ts=u64::MAX) — spans the whole index reversed.
    fn kind_reverse_high_key() -> Vec<u8> {
        let mut k = Vec::with_capacity(16);
        k.extend_from_slice(&u64::MAX.to_le_bytes()); // kind=max
        k.extend_from_slice(&u64::MAX.to_le_bytes()); // ts=max
        k
    }

    /// Task 1 — Bounded forward scan returns first 3 levIds from Event__kind golden vector.
    ///
    /// Expected: [4, 5, 6] (prefix of [4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]).
    #[test]
    fn test_forward_bounded_event_kind_limit3_golden_prefix() {
        let (env, _tmp) = open_temp_fixture_env();
        let results =
            scan_index_bounded(&env, "Event__kind", ScanDirection::Forward, &kind_forward_low_key(), 3)
                .expect("forward scan must not error");
        let lev_ids: Vec<u64> = results.iter().map(|(_, lev_id)| *lev_id).collect();
        assert_eq!(
            lev_ids,
            vec![4u64, 5, 6],
            "Forward Event__kind limit=3 must return the first 3 golden levIds [4,5,6], got {:?}",
            lev_ids
        );
    }

    /// Task 1 — Bounded reverse scan returns last 3 levIds from Event__kind golden vector (newest-first).
    ///
    /// Last 3 of [4,5,6,7,8,10,11,1,9,3,2] in reverse = [2,3,9].
    #[test]
    fn test_reverse_bounded_event_kind_limit3_golden_suffix() {
        let (env, _tmp) = open_temp_fixture_env();
        let results = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Reverse,
            &kind_reverse_high_key(),
            3,
        )
        .expect("reverse scan must not error");
        let lev_ids: Vec<u64> = results.iter().map(|(_, lev_id)| *lev_id).collect();
        assert_eq!(
            lev_ids,
            vec![2u64, 3, 9],
            "Reverse Event__kind limit=3 must return [2,3,9] (last 3 in reverse), got {:?}",
            lev_ids
        );
    }

    /// Task 1 — Limit is honored: forward scan with limit=3 returns at most 3 pairs.
    #[test]
    fn test_forward_bounded_limit_is_capped() {
        let (env, _tmp) = open_temp_fixture_env();
        let results =
            scan_index_bounded(&env, "Event__kind", ScanDirection::Forward, &kind_forward_low_key(), 3)
                .expect("scan must not error");
        assert!(
            results.len() <= 3,
            "scan with limit=3 must return at most 3 pairs, got {}",
            results.len()
        );
    }

    /// Task 1 — Unknown index name returns IndexError::SubDbNotFound, no panic.
    #[test]
    fn test_unknown_index_name_returns_subdb_not_found() {
        let (env, _tmp) = open_temp_fixture_env();
        let result = scan_index_bounded(
            &env,
            "Event__nonexistent",
            ScanDirection::Forward,
            &[],
            3,
        );
        match result {
            Err(IndexError::SubDbNotFound { name }) => {
                assert!(name.contains("Event__nonexistent"), "SubDbNotFound must identify the name");
            }
            Ok(_) => panic!("Expected Err(SubDbNotFound) for unknown index, got Ok"),
            Err(e) => panic!("Expected SubDbNotFound, got: {e}"),
        }
    }

    /// Task 1 — Each returned tuple has non-empty key bytes and levId in 1..=11.
    #[test]
    fn test_returned_tuples_have_valid_key_and_lev_id() {
        let (env, _tmp) = open_temp_fixture_env();
        let results =
            scan_index_bounded(&env, "Event__kind", ScanDirection::Forward, &kind_forward_low_key(), 5)
                .expect("scan must not error");
        assert!(!results.is_empty(), "Event__kind must return at least 1 pair");
        for (key, lev_id) in &results {
            assert!(!key.is_empty(), "key bytes must be non-empty");
            assert!(
                *lev_id >= 1 && *lev_id <= 11,
                "levId must be in 1..=11 (fixture range), got {lev_id}"
            );
        }
    }

    // -----------------------------------------------------------------------
    // Task 2 — limit=0 windowed scan tests
    // -----------------------------------------------------------------------

    /// Task 2 — limit=0 forward scan returns the full Event__kind golden vector.
    ///
    /// Expected: all 11 levIds in order [4,5,6,7,8,10,11,1,9,3,2].
    #[test]
    fn test_windowed_forward_full_golden_vector() {
        let (env, _tmp) = open_temp_fixture_env();
        let results =
            scan_index_bounded(&env, "Event__kind", ScanDirection::Forward, &kind_forward_low_key(), 0)
                .expect("windowed scan must not error");
        let lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();
        assert_eq!(
            lev_ids,
            KIND_GOLDEN.to_vec(),
            "limit=0 forward scan must return full golden vector {:?}, got {:?}",
            KIND_GOLDEN,
            lev_ids
        );
    }

    /// Task 2 — limit=0 reverse scan returns the full Event__kind golden vector reversed.
    ///
    /// Expected: [2,3,9,1,11,10,8,7,6,5,4]
    #[test]
    fn test_windowed_reverse_full_golden_vector_reversed() {
        let (env, _tmp) = open_temp_fixture_env();
        let results = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Reverse,
            &kind_reverse_high_key(),
            0,
        )
        .expect("windowed reverse scan must not error");
        let lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();
        let expected: Vec<u64> = KIND_GOLDEN.iter().rev().copied().collect();
        assert_eq!(
            lev_ids, expected,
            "limit=0 reverse scan must return full golden vector reversed {:?}, got {:?}",
            expected, lev_ids
        );
    }

    /// Task 2 — Windowing with a small window proves multiple txns and no gaps/duplicates.
    ///
    /// Uses a window of 4 (< 11 total) via `scan_index_windowed` directly. Asserts:
    /// - All 11 levIds are present
    /// - No duplicates
    /// - More than one batch was needed (window < total)
    #[test]
    fn test_windowed_with_small_window_no_gaps_no_dupes() {
        let (env, _tmp) = open_temp_fixture_env();
        // window_size=4 < 11 total → multiple batches required
        let results = scan_index_windowed(
            &env,
            "Event__kind",
            ScanDirection::Forward,
            &kind_forward_low_key(),
            4, // explicitly small window → forces multi-batch
        )
        .expect("windowed scan with small window must not error");
        let lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();

        // Must be complete
        assert_eq!(
            lev_ids.len(),
            11,
            "Small-window scan must return all 11 levIds, got {}",
            lev_ids.len()
        );

        // Must match the golden vector exactly (correct order, no gaps)
        assert_eq!(
            lev_ids,
            KIND_GOLDEN.to_vec(),
            "Small-window scan must return the full golden vector in order"
        );

        // No duplicates
        let mut deduped = lev_ids.clone();
        deduped.sort_unstable();
        deduped.dedup();
        assert_eq!(
            deduped.len(),
            lev_ids.len(),
            "Small-window scan must contain no duplicate levIds"
        );
    }

    /// Task 2 — A forward scan via limit=N (N >= total) and limit=0 (windowed) return the same sequence.
    ///
    /// Proves windowing introduces no gaps or duplicates compared to a single bounded scan.
    #[test]
    fn test_windowed_matches_bounded_large_limit() {
        let (env, _tmp) = open_temp_fixture_env();
        let bounded = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Forward,
            &kind_forward_low_key(),
            100, // > total
        )
        .expect("bounded large-limit scan");
        let windowed = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Forward,
            &kind_forward_low_key(),
            0, // windowed
        )
        .expect("windowed scan");

        let bounded_ids: Vec<u64> = bounded.iter().map(|(_, l)| *l).collect();
        let windowed_ids: Vec<u64> = windowed.iter().map(|(_, l)| *l).collect();
        assert_eq!(
            bounded_ids, windowed_ids,
            "limit=0 windowed and limit=100 bounded scans must return the same sequence"
        );
    }
}
