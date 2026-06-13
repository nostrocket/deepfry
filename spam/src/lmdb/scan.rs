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
/// ## DUPSORT resume semantics (CR-01 — key-granular windowing)
///
/// `Event__*` indexes use `MDB_DUPSORT + MDB_INTEGERDUP`: multiple events can share the same
/// composite key (e.g., two events with the same `kind + created_at`). A naive batch boundary
/// can fall in the middle of a duplicate-key group, and resuming correctly across that split
/// is direction-asymmetric in heed 0.22.1 (PROVEN ordering, see CR-01):
///
/// - **Forward** (`range`, end-open): resuming with `Bound::Included(last_key)` re-walks the
///   ENTIRE dup group from its smallest dup, so a `levId <= last_seen` skip would suffice.
/// - **Reverse** (`rev_range`, the bound is the START of the reverse walk): resuming with
///   `Bound::Included(last_key)` makes heed position the cursor at the SMALLEST dup of that
///   key (`MDB_SET_RANGE` semantics in `move_on_range_end`) and then immediately step to the
///   previous KEY — it does NOT re-walk the higher, still-unemitted dups of the boundary key.
///   A levId skip predicate cannot recover them because heed never yields them. This silently
///   drops levIds (the CR-01 blocker).
///
/// **Fix — key-granular windowing (uniform across both directions):** a window never splits a
/// dup group. `collect_window` fills up to `window_size` entries, then DRAINS the remaining
/// dups of the last key so the window always ends on a KEY boundary. The next window resumes
/// with `Bound::Excluded(resume_key)`, which skips the fully-consumed boundary key entirely.
/// No levId is ever dropped or re-emitted, in either direction. A window may exceed
/// `window_size` by at most one dup group's worth of entries (bounded; dup groups are small).
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
    // Resume cursor is KEY-only. The first window includes `start_key` (Included); every
    // subsequent window excludes the fully-drained boundary key (Excluded). Because
    // `collect_window` drains the boundary dup group, no per-levId resume state is needed.
    let mut resume_key: Vec<u8> = start_key.to_vec();
    let mut first_batch = true;

    loop {
        let rtxn = env.read_txn()?;

        let batch = match short_name {
            "Event__id" | "Event__pubkey" | "Event__tag" => {
                let db = open_index_string_uint64(env, &rtxn, short_name)?;
                collect_window(&db, &rtxn, direction, &resume_key, first_batch, window_size)?
            }
            "Event__kind" => {
                let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
                collect_window(&db, &rtxn, direction, &resume_key, first_batch, window_size)?
            }
            "Event__pubkeyKind" => {
                let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
                collect_window(&db, &rtxn, direction, &resume_key, first_batch, window_size)?
            }
            "Event__created_at" => {
                let db = open_index_created_at(env, &rtxn)?;
                collect_window(&db, &rtxn, direction, &resume_key, first_batch, window_size)?
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

        // `collect_window` guarantees the batch ends on a KEY boundary (boundary dup group
        // fully drained). The next window resumes with Bound::Excluded(resume_key), so the
        // boundary key is not revisited and no dup is dropped or duplicated.
        let (last_key, _last_lev_id) = batch.last().unwrap();
        resume_key = last_key.clone();

        all_results.extend(batch);
    }

    Ok(all_results)
}

// ---------------------------------------------------------------------------
// Single-window scan with exclusive-resume state (for engine.rs)
// ---------------------------------------------------------------------------

/// Collect exactly one dup-group-complete window from a named `Event__*` index.
///
/// This is the per-stream primitive used by the engine's over-fetch loop. Unlike
/// `scan_index_windowed` (which loops until the index is exhausted), this function
/// collects a SINGLE window of at most `window_size` entries, drains the trailing
/// dup group so the window ends on a KEY boundary, and returns the batch PLUS the
/// resume key for the next call.
///
/// ## Resume semantics (CR-03 fix)
///
/// On the first call, `first_batch = true` → `Bound::Included(resume_key)` (the start key
/// is included). On subsequent calls, `first_batch = false` → `Bound::Excluded(resume_key)`
/// (the boundary key was fully drained in the previous batch; skip it entirely).
///
/// This is the PROVEN key-granular exclusive-resume pattern from `scan_index_windowed` /
/// `collect_window`. A window never splits a dup group, so no levId is dropped or re-emitted
/// across window boundaries in either direction.
///
/// ## Returns
///
/// `Ok((batch, next_resume_key, next_first_batch))` where:
/// - `batch` — ≤ `window_size + one_dup_group_drain` entries in scan order.
/// - `next_resume_key` — the last key in `batch` (or unchanged if batch was empty).
///   Pass this back to the next call as `resume_key`.
/// - `next_first_batch` — always `false` after the first call. Pass back as `first_batch`.
///
/// `Ok((vec![], resume_key, false))` if the stream is exhausted.
///
/// `Err(IndexError)` — LMDB or sub-DB error.
pub fn scan_index_one_window(
    env: &heed::Env,
    short_name: &str,
    direction: ScanDirection,
    resume_key: &[u8],
    first_batch: bool,
    window_size: usize,
) -> Result<(Vec<(Vec<u8>, LevId)>, Vec<u8>, bool), IndexError> {
    let rtxn = env.read_txn()?;

    let batch = match short_name {
        "Event__id" | "Event__pubkey" | "Event__tag" => {
            let db = open_index_string_uint64(env, &rtxn, short_name)?;
            collect_window(&db, &rtxn, direction, resume_key, first_batch, window_size)?
        }
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
            collect_window(&db, &rtxn, direction, resume_key, first_batch, window_size)?
        }
        "Event__pubkeyKind" => {
            let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
            collect_window(&db, &rtxn, direction, resume_key, first_batch, window_size)?
        }
        "Event__created_at" => {
            let db = open_index_created_at(env, &rtxn)?;
            collect_window(&db, &rtxn, direction, resume_key, first_batch, window_size)?
        }
        _ => {
            return Err(IndexError::SubDbNotFound {
                name: short_name.to_string(),
            })
        }
    };

    // Drop txn immediately — short-lived (D-08).
    drop(rtxn);

    if batch.is_empty() {
        // Stream exhausted. Return unchanged resume_key so caller can detect empty.
        return Ok((vec![], resume_key.to_vec(), false));
    }

    // The next call must use Excluded(last_key) so the fully-drained boundary key is
    // not revisited. collect_window guarantees batch ends on a key boundary.
    let next_resume_key = batch.last().unwrap().0.clone();

    Ok((batch, next_resume_key, false))
}

// ---------------------------------------------------------------------------
// Generic low-level helpers — comparator-agnostic
// ---------------------------------------------------------------------------

/// Build the upper bound key for a Reverse scan over an `Event__*` index when the
/// caller-supplied `start_key` is a FINITE boundary (not `u64::MAX`).
///
/// ## Why ts+1 is required (CR-01)
///
/// heed 0.22.1's `rev_range(Bound::Included(K))` with `.move_through_duplicate_values()`
/// positions the LMDB cursor via `MDB_SET_RANGE` at the SMALLEST dup of `K` and then
/// immediately steps to the PREVIOUS key — it never yields the larger dups of `K`.
/// To land on the LARGEST dup of `K` (the first entry a descending walk needs), the
/// upper bound must be strictly ABOVE `K` — i.e., `Bound::Excluded(key_with_ts+1)`.
/// `rev_range(Bound::Excluded(K'))` positions at the largest key strictly less than `K'`,
/// which is the largest dup of the real timestamp `ts`. The full dup group is then walked
/// descending.
///
/// ## Key layout
///
/// Every `Event__*` composite key ends with a trailing 8-byte little-endian `created_at`.
/// The rebuilt key preserves the EXACT byte width — `prefix ‖ ts1.to_le_bytes()` — so the
/// golpe/IntegerComparator never sees a short key (T-03-CR01-D mitigation).
///
/// For `Event__created_at` the prefix is empty (key len == 8) — the rebuilt key is just
/// the 8 timestamp bytes.
///
/// ## Returns
///
/// `(rebuilt_key, true)` — `ts < u64::MAX`: a new key with `ts+1` in the trailing 8 bytes;
/// use `Bound::Excluded(rebuilt_key)` for the rev_range upper bound.
///
/// `(original_key.to_vec(), false)` — `ts == u64::MAX`: overflow; keep
/// `Bound::Included(original_key)` (the caller must handle the `false` case).
fn reverse_upper_bound(start_key: &[u8]) -> (Vec<u8>, bool) {
    debug_assert!(
        start_key.len() >= 8,
        "reverse_upper_bound: start_key too short ({} bytes) — every Event__* key ends with 8-byte created_at",
        start_key.len()
    );

    // Fail-soft guard: sub-8-byte keys cannot carry a trailing created_at.
    // Return the Included-fallback tuple (old non-panicking behavior) rather than
    // panicking/aborting in release builds via the usize-wrapping slice index below.
    // The debug_assert above fires in dev/test builds for visibility (T-03-PANIC).
    if start_key.len() < 8 {
        return (start_key.to_vec(), false);
    }

    let len = start_key.len();
    // Decode the trailing 8-byte created_at.
    let ts = u64::from_le_bytes(start_key[len - 8..len].try_into().unwrap_or([0u8; 8]));

    match ts.checked_add(1) {
        Some(ts1) => {
            // Rebuild key with ts+1 in the trailing 8 bytes; prefix bytes unchanged.
            let mut rebuilt = start_key[..len - 8].to_vec();
            rebuilt.extend_from_slice(&ts1.to_le_bytes());
            (rebuilt, true)
        }
        None => {
            // ts == u64::MAX: saturating overflow — keep original key, caller uses Included.
            (start_key.to_vec(), false)
        }
    }
}

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
///
/// ## Reverse upper bound (CR-01)
///
/// For the Reverse arm with a finite `start_key`, the upper bound is built via
/// `reverse_upper_bound(start_key)` which computes `ts+1` and returns
/// `Bound::Excluded(rebuilt_key)`. This positions `rev_range` ABOVE the boundary key so
/// it lands on the LARGEST dup of `ts` — the full dup group is then walked descending.
/// When `ts == u64::MAX` the helper returns `is_excluded=false` and the Included path
/// is used (no overflow; u64::MAX start keys are unbounded-high scans where no dup is at
/// exactly u64::MAX in practice).
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
            // CR-01: build the upper bound from ts+1 so rev_range positions ABOVE the
            // boundary key, landing on its LARGEST dup. Bind rebuilt_key to a local so
            // the Bound borrows a value that outlives the range tuple.
            let (rebuilt_key, is_excluded) = reverse_upper_bound(start_key);
            let upper: Bound<&[u8]> = if is_excluded {
                Bound::Excluded(rebuilt_key.as_slice())
            } else {
                Bound::Included(start_key)
            };
            let range = (Bound::Unbounded, upper);
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

/// Collect a single window for the windowing loop, ending on a KEY boundary (CR-01).
///
/// ## Resume semantics — key-granular, DUPSORT-correct in BOTH directions
///
/// - `first_batch = true`: the bound on `resume_key` is INCLUSIVE — `resume_key` is the
///   caller-supplied scan start and must be considered.
/// - `first_batch = false`: the bound is EXCLUSIVE — the previous window fully drained
///   `resume_key`'s dup group (see below), so the boundary key must NOT be revisited.
///   Forward uses `Bound::Excluded` as the range START; reverse uses `Bound::Excluded` as the
///   `rev_range` END (which `move_on_range_end` resolves to "largest key strictly less than").
///
/// ## Why drain to a key boundary (the CR-01 fix)
///
/// heed 0.22.1 resumes forward and reverse DUPSORT groups asymmetrically. A forward
/// `Bound::Included(key)` re-walks a key's entire dup group from its smallest dup, so a
/// levId-skip predicate would work. But a reverse `rev_range` with `Bound::Included(key)` as
/// its END bound positions the cursor at the SMALLEST dup of `key` and then steps to the
/// previous KEY — the higher, still-unemitted dups of that key are NEVER yielded, so no skip
/// predicate can recover them (PROVEN: see tests/dupsort_resume_test.rs). To make resume
/// correct and symmetric, we never split a dup group across a window: once `window_size`
/// entries are collected, we keep consuming while the next entry shares the last emitted
/// key, so every window ends exactly on a key boundary. The loop then resumes with the
/// EXCLUSIVE bound, skipping the fully-consumed key. A window may exceed `window_size` by at
/// most one dup group (bounded — dup groups are small in practice).
fn collect_window<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    direction: ScanDirection,
    resume_key: &[u8],
    first_batch: bool,
    window_size: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError>
where
    C: heed::Comparator,
{
    let mut results: Vec<(Vec<u8>, LevId)> = Vec::new();

    match direction {
        ScanDirection::Forward => {
            // First window: Included (consider the caller's start key). Resumed window:
            // Excluded (the previous window fully drained this key's dup group).
            let lower = if first_batch {
                Bound::Included(resume_key)
            } else {
                Bound::Excluded(resume_key)
            };
            let range = (lower, Bound::Unbounded);
            let iter = db.range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter {
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in windowed forward scan — skipping"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                // Stop once the window is full AND we have crossed onto a new key — i.e. drain
                // the current key's remaining dups so the window ends on a key boundary.
                if results.len() >= window_size
                    && results.last().map(|(k, _)| k.as_slice()) != Some(key)
                {
                    break;
                }
                results.push((key.to_vec(), lev_id));
            }
        }
        ScanDirection::Reverse => {
            // First window: build the upper bound from ts+1 (CR-01) — positions rev_range
            // ABOVE the boundary key so it lands on the LARGEST dup of ts. The full dup
            // group is then walked descending and drained (see key-granular windowing below).
            // Resumed window: Excluded(resume_key) — the boundary dup group was fully
            // drained in the prior window; skip the boundary key entirely. Do NOT apply
            // ts+1 on resume (the drained group already had its largest dup emitted).
            // Bind rebuilt_key to a local so the Bound borrows a value that outlives range.
            // CR-01: first_batch uses ts+1 Excluded bound to land on the largest dup.
            // Resumed window uses Excluded(resume_key) directly — no ts+1 needed.
            // `rebuilt_key` is bound BEFORE `upper` so its lifetime covers the &[u8] borrow.
            let (rebuilt_key, first_batch_is_excluded) = if first_batch {
                reverse_upper_bound(resume_key)
            } else {
                (Vec::new(), false) // not used when first_batch=false
            };
            let upper: Bound<&[u8]> = if first_batch {
                if first_batch_is_excluded {
                    Bound::Excluded(rebuilt_key.as_slice())
                } else {
                    // ts == u64::MAX: keep Included(resume_key)
                    Bound::Included(resume_key)
                }
            } else {
                // Resumed window: boundary dup group fully drained — skip it.
                Bound::Excluded(resume_key)
            };
            let range = (Bound::Unbounded, upper);
            let iter = db.rev_range(rtxn, &range)?.move_through_duplicate_values();
            for item in iter {
                let (key, value) = item?;
                if value.len() < 8 {
                    tracing::warn!(
                        value_len = value.len(),
                        "Event__* index VALUE shorter than 8 bytes in windowed reverse scan — skipping"
                    );
                    continue;
                }
                let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
                // Drain the current key's remaining dups before stopping (see Forward arm).
                if results.len() >= window_size
                    && results.last().map(|(k, _)| k.as_slice()) != Some(key)
                {
                    break;
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

    // -----------------------------------------------------------------------
    // CR-01 fix: Reverse arm ts+1 saturating Bound::Excluded tests
    // -----------------------------------------------------------------------

    /// Full sub-DB name for Event__kind (matches rasgueadb_defaultDb__ prefix).
    const EVENT_KIND_FULL: &str = "rasgueadb_defaultDb__Event__kind";

    /// Build a Uint64Uint64-shaped key for synthetic env tests.
    fn kind_key_synthetic(kind: u64, created_at: u64) -> Vec<u8> {
        let mut k = Vec::with_capacity(16);
        k.extend_from_slice(&kind.to_le_bytes());
        k.extend_from_slice(&created_at.to_le_bytes());
        k
    }

    /// Build a synthetic Event__kind env with the given (key, levId) pairs.
    /// Uses write transactions (legitimate — this is NOT strfry's env).
    fn build_synthetic_kind_env(entries: &[(Vec<u8>, u64)]) -> (heed::Env, tempfile::TempDir) {
        use crate::lmdb::comparators::Uint64Uint64Cmp;
        use heed::{DatabaseFlags, EnvOpenOptions};

        let dir = tempfile::tempdir().expect("tempdir for synthetic env");
        let env = unsafe {
            EnvOpenOptions::new()
                .max_dbs(5)
                .map_size(10 * 1024 * 1024)
                .open(dir.path())
                .expect("open synthetic env")
        };

        {
            let mut wtxn = env.write_txn().expect("write txn");
            let mut opts = env
                .database_options()
                .types::<heed::types::Bytes, heed::types::Bytes>()
                .key_comparator::<Uint64Uint64Cmp>();
            #[allow(deprecated)] // MDB_INTEGERDUP: deliberate strfry on-disk replication
            opts.flags(DatabaseFlags::DUP_SORT | DatabaseFlags::INTEGER_DUP);
            opts.name(EVENT_KIND_FULL);
            let db: heed::Database<heed::types::Bytes, heed::types::Bytes, Uint64Uint64Cmp> =
                opts.create(&mut wtxn).expect("create synthetic sub-DB");

            for (key, lev_id) in entries {
                db.put(&mut wtxn, key.as_slice(), &lev_id.to_le_bytes())
                    .expect("put entry");
            }
            wtxn.commit().expect("commit");
        }

        (env, dir)
    }

    // -----------------------------------------------------------------------
    // Task 2 — Fixture regression: kinds=[1], until=1700000256 yields levIds 7 and 8
    // -----------------------------------------------------------------------

    /// CR-01 fixture regression: reverse scan of Event__kind with start_key kind=1 ‖ ts=1700000256
    /// (an existing boundary timestamp) must return BOTH levId=7 and levId=8 and exactly 5
    /// kind=1 events at ts<=1700000256 (levIds 4,5,6,7,8).
    ///
    /// Before the CR-01 fix, scan_index_bounded Reverse with Bound::Included(start_key) on this
    /// existing key returned only levId=7 (the smallest dup of ts=1700000256), silently dropping
    /// levId=8. The ts+1 Excluded fix positions rev_range above ts=1700000256 so both dups are
    /// yielded descending.
    ///
    /// Fixture kind=1 layout (from Event__kind.json / 03-VERIFICATION.md):
    ///   levId=4   ts=1700000000
    ///   levId=5   ts=1700000255
    ///   levId=6   ts=1700000255
    ///   levId=7   ts=1700000256  ← boundary dup group
    ///   levId=8   ts=1700000256  ← boundary dup group (must NOT be dropped)
    ///   levId=10  ts=1710000000
    ///   levId=11  ts=1720000000
    ///
    /// scan_index_bounded("Event__kind", Reverse, kind=1‖ts=1700000256, limit=20) must include
    /// BOTH levId=7 and levId=8 among its returned events, and exactly 5 kind=1 entries total
    /// (levIds 4,5,6,7,8 at ts<=1700000256).
    #[test]
    fn test_scan_reverse_until_existing_ts_keeps_both_dups() {
        let (env, _tmp) = open_temp_fixture_env();

        // Build the Event__kind start key for kind=1, created_at=1700000256.
        let start_key = {
            let mut k = Vec::with_capacity(16);
            k.extend_from_slice(&1u64.to_le_bytes());         // kind=1
            k.extend_from_slice(&1700000256u64.to_le_bytes()); // ts=1700000256
            k
        };

        // Reverse scan from kind=1/ts=1700000256 downward, limit large enough for all kind=1 events.
        let results = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Reverse,
            &start_key,
            20,
        )
        .expect("scan_index_bounded Reverse must not error");

        // Extract kind=1 levIds from results (key[0..8] == 1u64.to_le_bytes()).
        let kind1_prefix = 1u64.to_le_bytes();
        let lev_ids: Vec<u64> = results
            .iter()
            .filter(|(k, _)| k.len() >= 8 && k[0..8] == kind1_prefix)
            .map(|(_, l)| *l)
            .collect();

        println!(
            "test_scan_reverse_until_existing_ts_keeps_both_dups: kind=1 levIds at ts<=1700000256 = {:?}",
            lev_ids
        );

        // Both levId=7 AND levId=8 must be present (the boundary dup group at ts=1700000256).
        assert!(
            lev_ids.contains(&7),
            "CR-01 fixture regression: levId=7 (ts=1700000256) must be returned, got {:?}",
            lev_ids
        );
        assert!(
            lev_ids.contains(&8),
            "CR-01 fixture regression: levId=8 (ts=1700000256) must be returned (was dropped before fix), got {:?}",
            lev_ids
        );

        // Exactly 5 kind=1 events at ts<=1700000256: levIds {4,5,6,7,8}.
        let mut sorted = lev_ids.clone();
        sorted.sort_unstable();
        assert_eq!(
            sorted,
            vec![4, 5, 6, 7, 8],
            "CR-01 fixture regression: exactly 5 kind=1 events at ts<=1700000256 must be returned \
             (levIds 4,5,6,7,8), got {:?}",
            sorted
        );
    }

    /// CR-01 fix: a reverse windowed scan starting on an existing 3-dup key returns all 3 dups.
    ///
    /// Bug: collect_window Reverse first_batch arm used Bound::Included(resume_key) which
    /// positions heed at the SMALLEST dup of resume_key and steps to the previous key —
    /// dropping the higher dups. Fix: build upper bound from ts+1 (Bound::Excluded) so
    /// rev_range positions ABOVE the boundary key and lands on the largest dup of ts.
    ///
    /// Layout: one key kind=1/ts=1000 with dups {5,6,7}.
    /// start_key = kind=1 ‖ ts=1000 (the existing key).
    /// Expected reverse results: all of {5,6,7} (sorted), none dropped.
    #[test]
    fn test_reverse_first_window_existing_key_keeps_all_dups() {
        let key = kind_key_synthetic(1, 1000);
        let entries = vec![
            (key.clone(), 5u64),
            (key.clone(), 6u64),
            (key.clone(), 7u64),
        ];
        let (env, _dir) = build_synthetic_kind_env(&entries);

        // scan_index_one_window with first_batch=true and resume_key equal to the existing key.
        let (batch, _next_key, _next_first) = scan_index_one_window(
            &env,
            "Event__kind",
            ScanDirection::Reverse,
            &key,
            true,  // first_batch: inclusive start
            100,   // large window — all dups should fit
        )
        .expect("scan_index_one_window must not error");

        let mut lev_ids: Vec<u64> = batch.iter().map(|(_, l)| *l).collect();
        lev_ids.sort_unstable();

        assert_eq!(
            lev_ids,
            vec![5, 6, 7],
            "CR-01: reverse scan starting on existing 3-dup key must return ALL dups [5,6,7], got {:?}",
            lev_ids
        );
    }

    /// CR-01 fix: scan_index_bounded Reverse with start_key equal to an existing key returns
    /// the full dup group.
    ///
    /// Same layout as test_reverse_first_window_existing_key_keeps_all_dups but uses the
    /// collect_bounded Reverse arm (scan_index_bounded with finite limit).
    #[test]
    fn test_reverse_bounded_existing_key_keeps_all_dups() {
        let key = kind_key_synthetic(1, 1000);
        let entries = vec![
            (key.clone(), 5u64),
            (key.clone(), 6u64),
            (key.clone(), 7u64),
        ];
        let (env, _dir) = build_synthetic_kind_env(&entries);

        let results = scan_index_bounded(
            &env,
            "Event__kind",
            ScanDirection::Reverse,
            &key,
            100, // large limit — all dups should fit
        )
        .expect("scan_index_bounded Reverse must not error");

        let mut lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();
        lev_ids.sort_unstable();

        assert_eq!(
            lev_ids,
            vec![5, 6, 7],
            "CR-01: scan_index_bounded Reverse starting on existing 3-dup key must return ALL dups [5,6,7], got {:?}",
            lev_ids
        );
    }
}
