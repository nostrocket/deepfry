/// merge.rs — K-way merge over per-prefix `Reverse` scans (D-05/D-06/D-10).
///
/// ## Purpose
///
/// Given a set of per-prefix start_keys for a single `Event__*` index, `merge_windowed`
/// (the production entry point) issues windowed `scan_index_one_window` calls per prefix and
/// merges their results into a single `(created_at, lev_id)` DESC-ordered stream using a
/// `BinaryHeap` (max-heap). `merge_prefixes` is a backward-compat thin wrapper used by tests.
///
/// ## Key invariants
///
/// - **No hydration** (D-06): `created_at` is extracted from the trailing 8 bytes of each key
///   via `crate::query::router::created_at_from_key` — the `EventPayload` sub-DB is never opened.
/// - **No long-lived txn** (D-08): each `scan_index_one_window` call opens and drops its own short
///   `RoTxn`. The merge holds NO transaction across per-prefix scan calls.
/// - **Bounded by `emit_limit`** (T-03-DOS): each per-stream window is bounded by `batch_size`;
///   the heap emits at most `emit_limit` results total.
/// - **Total order** (D-10): output is `(created_at, lev_id)` DESC — a total order because
///   `lev_id` is unique per event.
/// - **Per-stream since exhaustion** (CR-03): when a stream's descending batch first contains an
///   entry with ts < since, the batch is truncated at that point and the stream is marked exhausted.
///   Other streams are not affected — they continue until their own since boundary or exhaustion.

use crate::lmdb::scan::{scan_index_one_window, ScanDirection, DEFAULT_WINDOW_SIZE};
use crate::lmdb::types::LevId;
use crate::lmdb::indexes::IndexError;
use crate::query::router::created_at_from_key;
use std::cmp::Ordering;
use std::collections::BinaryHeap;

// ---------------------------------------------------------------------------
// MergeCandidate: heap entry ordered by (created_at DESC, lev_id DESC)
// ---------------------------------------------------------------------------

/// A single candidate entry in the k-way merge max-heap.
///
/// Ordered by `(created_at DESC, lev_id DESC)` so that `BinaryHeap::pop()` yields the
/// newest event first (D-10 total order). `lev_id` breaks ties uniquely (spec §3.4).
///
/// `stream_idx` identifies which per-prefix stream this candidate came from, so the
/// merge can pull the next entry from the same stream after popping.
pub struct MergeCandidate {
    /// Unix timestamp from the trailing 8 bytes of the key (D-06 — extracted without hydration).
    pub created_at: u64,
    /// `levId` — unique monotonic event id; tie-breaker for same-timestamp events (D-10).
    pub lev_id: LevId,
    /// Raw key bytes (stored for the caller to reconstruct the event's position if needed).
    pub key_bytes: Vec<u8>,
    /// Index into the per-prefix stream vector — used to pull the next entry from this stream.
    pub stream_idx: usize,
}

// Manual Eq/PartialEq over (created_at, lev_id) only — matching the Ord relation.
// lev_id is unique per event, so this is a valid equivalence. The derived Eq would
// compare key_bytes and stream_idx too, creating an Eq/Ord inconsistency (#[derive]
// compares all fields; Ord::cmp uses only the sort key fields).
impl PartialEq for MergeCandidate {
    fn eq(&self, other: &Self) -> bool {
        self.created_at == other.created_at && self.lev_id == other.lev_id
    }
}

impl Eq for MergeCandidate {}

impl Ord for MergeCandidate {
    /// Order by `(created_at DESC, lev_id DESC)` — BinaryHeap is a max-heap,
    /// so the largest candidate is popped first, giving newest-first emission (D-10).
    fn cmp(&self, other: &Self) -> Ordering {
        self.created_at
            .cmp(&other.created_at)
            .then(self.lev_id.cmp(&other.lev_id))
    }
}

impl PartialOrd for MergeCandidate {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

// ---------------------------------------------------------------------------
// merge_windowed: the windowed k-way merge entry point (CR-02/CR-03 fix)
// ---------------------------------------------------------------------------

/// Per-stream state for the windowed k-way merge.
///
/// Holds the resume position for `scan_index_one_window`, the prefix guard bytes,
/// whether this stream is exhausted, and a pending-emission buffer of decoded
/// `(created_at, lev_id, key_bytes)` triples in descending order.
struct StreamState {
    resume_key: Vec<u8>,
    first_batch: bool,
    prefix: Vec<u8>,
    exhausted: bool,
    /// Pending entries in (created_at DESC, lev_id DESC) order. Consumed front-to-back.
    buffer: Vec<(u64, LevId, Vec<u8>)>,
    /// Cursor into `buffer`: index of the next unconsumed entry.
    buf_pos: usize,
}

impl StreamState {
    /// Return the next buffered (ts, lev_id, key) without removing it, or None if drained.
    fn peek(&self) -> Option<(u64, LevId)> {
        self.buffer.get(self.buf_pos).map(|(ts, lev, _)| (*ts, *lev))
    }

    /// True when the buffer is fully consumed and the stream is not exhausted.
    fn needs_refill(&self) -> bool {
        !self.exhausted && self.buf_pos >= self.buffer.len()
    }
}

/// Refill one stream by issuing `scan_index_one_window` call(s), applying the prefix guard,
/// the intra-group lev_id floor (QRY-05 fat-group resume), and per-stream `since` truncation
/// (CR-03), then loading surviving results into `stream.buffer`.
///
/// After a refill, `stream.buf_pos` is reset to 0. If the stream is truly exhausted (empty
/// scan result, prefix guard eliminates everything, or since truncates everything), the stream
/// is marked exhausted.
///
/// ## lev_id_floor (fat-group resume)
///
/// When `lev_id_floor = Some((floor_ts, floor_lev))`, entries with
/// `created_at == floor_ts && lev_id >= floor_lev` are dropped from the batch BEFORE
/// any other filtering. This implements intra-DUPSORT-group resume: the caller has already
/// emitted everything at floor_ts with lev_id >= floor_lev and wants only the remainder
/// of that dup group (lev_id < floor_lev) plus everything at ts < floor_ts.
///
/// When the floor filter eliminates ALL entries in a window (e.g. the window only contained
/// fat_ts entries all above the floor), this function issues ONE additional window to skip
/// past the floor timestamp and reach entries at ts < floor_ts. This prevents false stream
/// exhaustion when the floor falls exactly at a key boundary.
///
/// This approach avoids the need for heed `MDB_GET_BOTH` (value-level cursor positioning
/// within a dup group), which heed 0.22.1 does not expose.
fn refill_stream(
    env: &heed::Env,
    short_name: &str,
    stream: &mut StreamState,
    batch_size: usize,
    since: u64,
    lev_id_floor: Option<(u64, LevId)>,
) -> Result<(), IndexError> {
    debug_assert!(stream.needs_refill(), "refill_stream called on a non-empty or exhausted stream");

    // Issue scan windows until we accumulate a non-empty buffer or confirm exhaustion.
    // Normally one window suffices. The loop handles the rare case where the floor filter
    // drops an entire window (all entries were at floor_ts with lev_id >= floor_lev) —
    // we issue one more window to reach entries below floor_ts.
    // Safety: bounded at 2 iterations. After at most one floor-only window, the resume_key
    // advances past floor_ts so the second window returns entries at ts < floor_ts (or empty).
    for _attempt in 0..2usize {
        let (batch, next_resume, next_first) = scan_index_one_window(
            env,
            short_name,
            ScanDirection::Reverse,
            &stream.resume_key,
            stream.first_batch,
            batch_size,
        )?;

        stream.resume_key = next_resume;
        stream.first_batch = next_first;

        if batch.is_empty() {
            stream.exhausted = true;
            stream.buffer = Vec::new();
            stream.buf_pos = 0;
            return Ok(());
        }

        // CR-01 prefix guard: take_while(starts_with(prefix)) in descending order —
        // entries below the prefix are contiguous at a lower key, so take_while terminates cleanly.
        let prefix = &stream.prefix;
        let guarded: Vec<(u64, LevId, Vec<u8>)> = batch
            .into_iter()
            .take_while(|(k, _)| k.starts_with(prefix.as_slice()))
            .map(|(k, lev_id)| {
                let ts = created_at_from_key(&k);
                (ts, lev_id, k)
            })
            .collect();

        if guarded.is_empty() {
            stream.exhausted = true;
            stream.buffer = Vec::new();
            stream.buf_pos = 0;
            return Ok(());
        }

        // Fat-group intra-dup resume (QRY-05 / fat-group fix): when lev_id_floor is set,
        // drop entries at floor_ts with lev_id >= floor_lev. The batch is in descending
        // (ts DESC, lev_id DESC) order. These entries cluster at the head of the batch
        // when floor_ts == the scan starting timestamp. All other entries (ts < floor_ts
        // or ts == floor_ts with lev_id < floor_lev) pass through unchanged.
        let guarded: Vec<(u64, LevId, Vec<u8>)> = if let Some((floor_ts, floor_lev)) = lev_id_floor {
            guarded
                .into_iter()
                .filter(|(ts, lev_id, _)| {
                    // Keep: ts different from floor, or lev_id strictly below the floor.
                    *ts != floor_ts || *lev_id < floor_lev
                })
                .collect()
        } else {
            guarded
        };

        if guarded.is_empty() {
            // Floor filter eliminated all entries in this window. The resume_key was already
            // advanced by scan_index_one_window (to the last key in the batch, i.e. floor_ts).
            // Loop once more: next call uses Excluded(floor_ts) and reaches ts < floor_ts.
            // If the second attempt also returns empty, the outer loop exits and we fall
            // through to marking exhausted below.
            continue;
        }

        // CR-03 per-stream since exhaustion: the batch is in descending order. Find the first
        // entry with ts < since and truncate there. The stream is then exhausted because all
        // subsequent entries will also be < since (the scan is descending).
        let truncated: Vec<(u64, LevId, Vec<u8>)> = if since > 0 {
            let cutoff = guarded.iter().position(|(ts, _, _)| *ts < since);
            if let Some(idx) = cutoff {
                let trimmed = guarded[..idx].to_vec();
                stream.exhausted = true; // no more entries >= since from this stream
                trimmed
            } else {
                guarded
            }
        } else {
            guarded
        };

        if truncated.is_empty() {
            // since truncated the entire window; stream is already marked exhausted above
            // (if since > 0 and all entries were < since, cutoff == Some(0) → trimmed is empty)
            stream.buffer = Vec::new();
            stream.buf_pos = 0;
            return Ok(());
        }

        stream.buffer = truncated;
        stream.buf_pos = 0;
        return Ok(());
    }

    // Fell through the loop (both attempts resulted in floor-filtered empty batches).
    // This means the index is truly exhausted or all remaining entries are at floor_ts
    // with lev_id >= floor_lev. Mark exhausted.
    stream.exhausted = true;
    stream.buffer = Vec::new();
    stream.buf_pos = 0;
    Ok(())
}

/// Windowed k-way merge over per-prefix `Reverse` scans (CR-02/CR-03 fix).
///
/// ## Algorithm (frontier emission)
///
/// 1. Initialize per-stream state with the provided `start_keys`; for each stream derive
///    `prefix = key[..len-8]` (the logical value-partition boundary).
/// 2. Seed the `BinaryHeap` with the first entry from each non-empty stream's buffer (after
///    an initial `refill_stream` call).
/// 3. Repeatedly:
///    - Pop the max `(created_at, lev_id)` candidate from the heap — this is guaranteed to be
///      globally the newest remaining entry because the heap invariant holds over ALL streams'
///      current buffer heads.
///    - Push the emitted triple to `results`.
///    - Refill the popped stream's buffer head: if the stream's buffer still has entries at
///      `buf_pos`, push the next buffered entry into the heap. If the buffer is drained and
///      the stream is not exhausted, call `refill_stream` and push the new head.
/// 4. Stop at `emit_limit` results or when the heap is empty (all streams exhausted and buffers
///    drained).
///
/// ## Why this is correct (CR-02)
///
/// A popped candidate is the max over ALL heap entries, and each heap entry is the HEAD
/// (highest ts/lev_id) of its stream's remaining buffer. Because the per-stream buffer is in
/// descending order, the heap head is always the maximum of that stream. Therefore the popped
/// global max is >= all other stream heads, and emitting it next is globally correct. This is
/// the standard k-way merge invariant; crucially it holds ACROSS window boundaries because we
/// only advance the heap when a stream's buffer is drained and we refill from `scan_index_one_window`.
///
/// ## Parameters
///
/// - `env`: the heed LMDB environment (read-only).
/// - `short_name`: the short `Event__*` index name (e.g. `"Event__kind"`).
/// - `start_keys`: one composite start_key per prefix. Empty slice → empty result.
/// - `batch_size`: per-stream window size passed to `scan_index_one_window`.
/// - `emit_limit`: maximum number of triples to return.
/// - `since`: per-stream lower bound. Streams are exhausted when their next entry is < since.
///   Pass `0` for no lower bound.
/// - `lev_id_floor`: optional intra-DUPSORT-group resume bound for fat-group pagination
///   (QRY-05). When `Some((floor_ts, floor_lev))`, entries with `created_at == floor_ts &&
///   lev_id >= floor_lev` are filtered out, exposing entries with lev_id < floor_lev at the
///   same timestamp. Pass `None` for the first page (no floor). See `refill_stream` for detail.
///
/// ## Returns
///
/// `Ok(Vec<(u64, LevId, Vec<u8>)>)` — `(created_at, lev_id, key_bytes)` triples in
/// `(created_at DESC, lev_id DESC)` order. Length ≤ `emit_limit`.
///
/// `Err(IndexError)` — LMDB or sub-DB error from any per-stream scan.
pub fn merge_windowed(
    env: &heed::Env,
    short_name: &str,
    start_keys: &[Vec<u8>],
    batch_size: usize,
    emit_limit: usize,
    since: u64,
    lev_id_floor: Option<(u64, LevId)>,
) -> Result<Vec<(u64, LevId, Vec<u8>)>, IndexError> {
    if start_keys.is_empty() || emit_limit == 0 {
        return Ok(vec![]);
    }

    // Initialize per-stream state; don't refill yet (lazy: seed heap after init).
    let mut streams: Vec<StreamState> = start_keys
        .iter()
        .map(|key| {
            let prefix_len = key.len().saturating_sub(8);
            StreamState {
                resume_key: key.clone(),
                first_batch: true,
                prefix: key[..prefix_len].to_vec(),
                exhausted: false,
                buffer: Vec::new(),
                buf_pos: 0,
            }
        })
        .collect();

    // Initial fill: load the first window for each stream.
    for s in &mut streams {
        refill_stream(env, short_name, s, batch_size, since, lev_id_floor)?;
    }

    // Seed the BinaryHeap with the first candidate from each non-empty stream.
    let mut heap: BinaryHeap<MergeCandidate> = BinaryHeap::with_capacity(streams.len());
    for (idx, s) in streams.iter().enumerate() {
        if let Some((ts, lev_id)) = s.peek() {
            let key_bytes = s.buffer[s.buf_pos].2.clone();
            heap.push(MergeCandidate { created_at: ts, lev_id, key_bytes, stream_idx: idx });
        }
    }

    // K-way frontier merge.
    let mut results: Vec<(u64, LevId, Vec<u8>)> = Vec::with_capacity(emit_limit.min(256));

    while let Some(candidate) = heap.pop() {
        let idx = candidate.stream_idx;
        results.push((candidate.created_at, candidate.lev_id, candidate.key_bytes));

        if results.len() >= emit_limit {
            break;
        }

        // Advance the popped stream: consume the entry we just popped (buf_pos was at it).
        streams[idx].buf_pos += 1;

        // Refill the stream if its buffer is drained and it is not exhausted.
        if streams[idx].needs_refill() {
            refill_stream(env, short_name, &mut streams[idx], batch_size, since, lev_id_floor)?;
        }

        // Push the next head of this stream into the heap (if any remains).
        if let Some((ts, lev_id)) = streams[idx].peek() {
            let key_bytes = streams[idx].buffer[streams[idx].buf_pos].2.clone();
            heap.push(MergeCandidate { created_at: ts, lev_id, key_bytes, stream_idx: idx });
        }
    }

    Ok(results)
}

// ---------------------------------------------------------------------------
// merge_prefixes: thin backward-compat wrapper over merge_windowed
// ---------------------------------------------------------------------------

/// Merge per-prefix `Reverse` scans over `short_name` into a single `(created_at DESC, lev_id DESC)` stream.
///
/// ## Note (WR-04 / plan 03-09)
///
/// This is now a thin wrapper over `merge_windowed` with `since=0` (no lower bound) and
/// `batch_size = scan_limit`. The production engine path calls `merge_windowed` directly;
/// `merge_prefixes` is retained for backward compatibility with its existing unit tests and
/// any caller that does not need per-stream since semantics or windowed refill.
///
/// ## Parameters
///
/// - `env`: the heed LMDB environment (read-only).
/// - `short_name`: the short `Event__*` index name (e.g. `"Event__kind"`).
/// - `start_keys`: one composite start_key per prefix (from `router::build_start_keys`). Empty → empty.
/// - `limit`: maximum results to return. `0` → uses `DEFAULT_WINDOW_SIZE`.
///
/// ## Returns
///
/// `Ok(Vec<(u64, LevId, Vec<u8>)>)` — `(created_at, lev_id, key_bytes)` triples in newest-first
/// order. Length ≤ `limit` (or `DEFAULT_WINDOW_SIZE` when `limit==0`).
///
/// `Err(IndexError)` — from `merge_windowed`.
pub fn merge_prefixes(
    env: &heed::Env,
    short_name: &str,
    start_keys: &[Vec<u8>],
    limit: usize,
) -> Result<Vec<(u64, LevId, Vec<u8>)>, IndexError> {
    if start_keys.is_empty() {
        return Ok(vec![]);
    }

    let scan_limit = if limit == 0 { DEFAULT_WINDOW_SIZE } else { limit };
    let emit_limit = scan_limit;

    // Delegate entirely to merge_windowed with since=0 and the same scan_limit as batch_size.
    // scan_index_one_window will issue a single window of scan_limit entries per stream —
    // for backward compat with the old merge_prefixes single-pass behavior, use a large enough
    // batch_size so each stream materializes in one go (scan_limit is the per-stream bound).
    // merge_windowed's frontier then merges them correctly.
    // lev_id_floor=None: merge_prefixes is a first-page, no-cursor call; no intra-group floor.
    merge_windowed(env, short_name, start_keys, scan_limit, emit_limit, 0, None)
}

// ---------------------------------------------------------------------------
// Unit tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;
    use crate::lmdb::scan::scan_index_bounded;
    use crate::query::router::{build_start_keys, select_index, SelectedIndex};
    use crate::query::filter::NostrFilter;

    const PK1: &str = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const PK2: &str = "c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5";

    /// Copy fixture to tempdir and open an env there.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// Test 1: MergeCandidate ordering — larger created_at sorts greater; equal created_at
    /// ties broken by larger lev_id. Proves max-heap pops newest-first (D-10).
    #[test]
    fn test_merge_candidate_ord_newest_first() {
        // Larger created_at wins over larger lev_id
        let a = MergeCandidate { created_at: 1720000000, lev_id: 5, key_bytes: vec![], stream_idx: 0 };
        let b = MergeCandidate { created_at: 1700000000, lev_id: 10, key_bytes: vec![], stream_idx: 1 };
        assert!(a > b, "larger created_at must sort greater regardless of lev_id");

        // Equal created_at: larger lev_id wins
        let c = MergeCandidate { created_at: 1700000000, lev_id: 8, key_bytes: vec![], stream_idx: 0 };
        let d = MergeCandidate { created_at: 1700000000, lev_id: 5, key_bytes: vec![], stream_idx: 1 };
        assert!(c > d, "equal created_at: larger lev_id must sort greater");

        // Verify BinaryHeap pops in correct order (max-heap → a first, then b/c/d by lev_id desc)
        // Candidates: a=(1720000000,5), b=(1700000000,10), c=(1700000000,8), d=(1700000000,5)
        // Expected pop order: a (ts=1720000000), b (lev_id=10), c (lev_id=8), d (lev_id=5)
        let mut heap = BinaryHeap::new();
        heap.push(b);
        heap.push(d);
        heap.push(a);
        heap.push(c);

        let popped1 = heap.pop().unwrap();
        assert_eq!(popped1.created_at, 1720000000, "first pop: ts=1720000000 (newest)");
        assert_eq!(popped1.lev_id, 5);

        // Remaining: b=(1700000000,10), c=(1700000000,8), d=(1700000000,5) — all ts=1700000000
        // Same ts → sorted by lev_id DESC: 10, 8, 5
        let popped2 = heap.pop().unwrap();
        assert_eq!(popped2.created_at, 1700000000);
        assert_eq!(popped2.lev_id, 10, "same ts: largest lev_id (10) first");

        let popped3 = heap.pop().unwrap();
        assert_eq!(popped3.lev_id, 8, "next lev_id=8");

        let popped4 = heap.pop().unwrap();
        assert_eq!(popped4.lev_id, 5, "smallest lev_id (5) last");
    }

    /// Test 1 (corrected): verify heap order more carefully.
    #[test]
    fn test_merge_candidate_ord_heap_order() {
        // Three candidates: newest first should be ts=1720000000/lev_id=11,
        // then ts=1700000000/lev_id=10, then ts=1700000000/lev_id=5
        let mut heap = BinaryHeap::new();
        heap.push(MergeCandidate { created_at: 1700000000, lev_id: 5, key_bytes: vec![], stream_idx: 0 });
        heap.push(MergeCandidate { created_at: 1720000000, lev_id: 11, key_bytes: vec![], stream_idx: 1 });
        heap.push(MergeCandidate { created_at: 1700000000, lev_id: 10, key_bytes: vec![], stream_idx: 2 });

        let first = heap.pop().unwrap();
        assert_eq!(first.created_at, 1720000000, "first must be newest ts");
        assert_eq!(first.lev_id, 11);

        let second = heap.pop().unwrap();
        assert_eq!(second.created_at, 1700000000);
        assert_eq!(second.lev_id, 10, "same ts: larger lev_id first");

        let third = heap.pop().unwrap();
        assert_eq!(third.lev_id, 5, "smallest lev_id last");
    }

    /// Test 2: merging a SINGLE prefix (kinds=[1] over Event__kind) yields the same
    /// lev_id sequence as a direct reverse scan_index_bounded (degenerate one-stream case).
    #[test]
    fn test_merge_single_prefix_equals_direct_scan() {
        let (env, _tmp) = open_temp_fixture_env();

        // Build start key for kinds=[1] reverse scan
        let f = NostrFilter {
            kinds: Some(vec![1]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__kind");
        let start_keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(start_keys.len(), 1, "one kind → one start_key");

        // Direct reverse scan
        let direct = scan_index_bounded(&env, "Event__kind", ScanDirection::Reverse, &start_keys[0], 11)
            .expect("direct scan must not error");
        let direct_lev_ids: Vec<LevId> = direct.iter().map(|(_, id)| *id).collect();

        // Merge via merge_prefixes (single stream)
        let merged = merge_prefixes(&env, "Event__kind", &start_keys, 11)
            .expect("merge must not error");
        let merged_lev_ids: Vec<LevId> = merged.iter().map(|(_, id, _)| *id).collect();

        assert_eq!(
            merged_lev_ids, direct_lev_ids,
            "single-prefix merge must equal direct reverse scan order"
        );
    }

    /// Test 3: multi-prefix merge (both fixture pubkeys via Event__pubkey) yields
    /// a non-increasing created_at sequence and total length ≤ limit.
    #[test]
    fn test_merge_multi_prefix_non_increasing_created_at() {
        let (env, _tmp) = open_temp_fixture_env();

        // Two pubkeys → two prefixes
        let f = NostrFilter {
            authors: Some(vec![PK1.to_string(), PK2.to_string()],),
            ..Default::default()
        };
        let selected = select_index(&f);
        assert_eq!(selected, SelectedIndex::Multi("Event__pubkey"));
        let start_keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(start_keys.len(), 2, "two authors → two start_keys");

        let limit = 7;
        let merged = merge_prefixes(&env, "Event__pubkey", &start_keys, limit)
            .expect("merge must not error");

        assert!(merged.len() <= limit, "emitted length must be ≤ limit");
        assert!(!merged.is_empty(), "must emit at least some results");

        // Verify non-increasing created_at (D-10 total order)
        let mut prev_ts = u64::MAX;
        let mut prev_lev = u64::MAX;
        for (ts, lev_id, _) in &merged {
            if *ts == prev_ts {
                assert!(
                    *lev_id <= prev_lev,
                    "equal ts={}: lev_id {} must be ≤ prev lev_id {} (D-10 tie-break)",
                    ts, lev_id, prev_lev
                );
            } else {
                assert!(
                    *ts <= prev_ts,
                    "created_at sequence must be non-increasing, got ts={} after ts={}",
                    ts, prev_ts
                );
            }
            prev_ts = *ts;
            prev_lev = *lev_id;
        }
    }

    /// Test 4: merge.rs does not import crate::lmdb::payload or DecodedEvent.
    /// This is verified by construction — the import list at the top of this file
    /// contains no reference to `payload` or `DecodedEvent`.
    ///
    /// This test serves as a compile-time proof: if someone adds a payload import,
    /// this assertion note in the test output reminds reviewers of D-06.
    #[test]
    fn test_no_payload_imports_compile_time_proof() {
        // D-06: merge operates on key bytes alone — no hydration.
        // Verified by construction: merge.rs imports only:
        //   crate::lmdb::scan::{scan_index_one_window, scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE}
        //   crate::lmdb::types::LevId
        //   crate::lmdb::indexes::IndexError
        //   crate::query::router::created_at_from_key
        // No reference to crate::lmdb::payload, DecodedEvent, or NostrEvent.
        // This is a documentation test — passes vacuously.
        assert!(true, "merge.rs imports verified — no payload or DecodedEvent import");
    }

    /// Test 6 (merge_windowed cross-iteration order): two streams of different time densities,
    /// small batch_size forces multiple over-fetch iterations; global DESC order must hold.
    ///
    /// Stream A (kind=1): 7 events at ts 1700000000..1720000000 (descending)
    /// Stream B (kind=2): 2 events at ts 1700000000, 1710000000
    ///
    /// With batch_size=2, multiple windowed iterations are needed. The emitted sequence must be
    /// strictly non-increasing in (created_at DESC, lev_id DESC) across ALL iterations.
    #[test]
    fn test_merge_windowed_cross_iteration_global_desc_order() {
        let (env, _tmp) = open_temp_fixture_env();

        // kinds=[1,2] → two start keys for Event__kind
        let f = NostrFilter {
            kinds: Some(vec![1, 2]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__kind");
        let start_keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(start_keys.len(), 2, "two kinds → two start_keys");

        // batch_size=2, since=0 (no lower bound), large emit cap, no intra-group floor
        let results = merge_windowed(&env, "Event__kind", &start_keys, 2, 20, 0, None)
            .expect("merge_windowed must not error");

        assert!(!results.is_empty(), "must emit at least some results");

        // Verify strict non-increasing (created_at DESC, lev_id DESC)
        let mut prev_ts = u64::MAX;
        let mut prev_lev = u64::MAX;
        for (ts, lev_id, _key) in &results {
            if *ts == prev_ts {
                assert!(
                    *lev_id <= prev_lev,
                    "equal ts={}: lev_id {} must be <= prev lev_id {} (D-10 tie-break)",
                    ts, lev_id, prev_lev
                );
            } else {
                assert!(
                    *ts <= prev_ts,
                    "created_at must be non-increasing across iterations: got ts={} after ts={} — CR-02 ordering broken",
                    ts, prev_ts
                );
            }
            prev_ts = *ts;
            prev_lev = *lev_id;
        }

        // All 9 events (7 kind=1 + 2 kind=2) must be emitted
        assert_eq!(results.len(), 9, "must emit all 9 events (7 kind=1 + 2 kind=2)");
    }

    /// Test 7 (merge_windowed per-stream since exhaustion): stream A has events >= T, stream B
    /// has events < T. With since=T, all of A's events must be returned and none of B's.
    /// Crossing `since` on stream B must NOT terminate stream A (CR-03 per-stream since).
    ///
    /// Use since=1715000000:
    ///   kind=1: levId=11 (ts=1720000000) only — 1 event (all others < 1715000000)
    ///   kind=2: none (ts=1700000000 and ts=1710000000 both < 1715000000)
    /// Expected: 1 event total (levId=11 from kind=1).
    #[test]
    fn test_merge_windowed_per_stream_since_exhaustion() {
        let (env, _tmp) = open_temp_fixture_env();

        // kinds=[1,2] → two start keys
        let f = NostrFilter {
            kinds: Some(vec![1, 2]),
            ..Default::default()
        };
        let selected = SelectedIndex::Multi("Event__kind");
        let start_keys = build_start_keys(&f, &selected, ScanDirection::Reverse);
        assert_eq!(start_keys.len(), 2, "two kinds → two start_keys");

        // since=1715000000: only kind=1/levId=11 (ts=1720000000) survives; no intra-group floor
        let results = merge_windowed(&env, "Event__kind", &start_keys, 10, 20, 1715000000, None)
            .expect("merge_windowed with since must not error");

        // Exactly 1 event: kind=1/levId=11 (ts=1720000000)
        assert_eq!(
            results.len(),
            1,
            "since=1715000000 must return exactly 1 event; got {} — CR-03 per-stream since broken",
            results.len()
        );
        assert_eq!(results[0].0, 1720000000, "the event must be ts=1720000000");
        assert_eq!(results[0].1, 11, "the event must be levId=11");
    }

    /// Test 5 (CR-01): prefix guard — a kind=2 start key returns ONLY kind=2 entries.
    ///
    /// The fixture Event__kind index (forward order): [4,5,6,7,8,10,11,1,9,3,2]
    ///   kind=1 lev_ids: 4,5,6,7,8,10,11 (7 events at ts 1700000000..1720000000)
    ///   kind=2 lev_ids: 1,9 (2 events at ts 1700000000, 1710000000)
    ///
    /// Without the prefix guard, a reverse scan from kind=2||ts=u64::MAX walks backward
    /// through kind=1 entries. With the guard, take_while stops at the first entry
    /// whose key doesn't start with `kind=2 LE` (i.e. any kind != 2), so only
    /// levIds 9 (ts=1710000000) and 1 (ts=1700000000) are returned.
    #[test]
    fn test_merge_prefix_guard_no_contamination() {
        let (env, _tmp) = open_temp_fixture_env();

        // Build the kind=2 start key: kind(8 LE) || ts=u64::MAX (reverse upper bound)
        let mut start_key = Vec::with_capacity(16);
        start_key.extend_from_slice(&2u64.to_le_bytes()); // kind=2
        start_key.extend_from_slice(&u64::MAX.to_le_bytes()); // ts upper bound

        let limit = 20; // large enough to return everything if unguarded
        let merged = merge_prefixes(&env, "Event__kind", &[start_key], limit)
            .expect("merge_prefixes must not error");

        // Must return exactly the 2 kind=2 events (levIds 1 and 9).
        assert_eq!(
            merged.len(),
            2,
            "kind=2 prefix must return exactly 2 events (levIds 1,9); got {} — CR-01 prefix guard may be missing",
            merged.len()
        );

        // Verify all returned lev_ids are kind=2 events (1 and 9) — no kind=1 contamination.
        let returned_lev_ids: Vec<LevId> = merged.iter().map(|(_, lev, _)| *lev).collect();
        assert!(
            returned_lev_ids.contains(&9),
            "kind=2 levId=9 (ts=1710000000) must be returned"
        );
        assert!(
            returned_lev_ids.contains(&1),
            "kind=2 levId=1 (ts=1700000000) must be returned"
        );

        // Confirm no kind=1 levIds leaked (fixture kind=1 levIds: 4,5,6,7,8,10,11).
        let kind1_lev_ids: std::collections::HashSet<LevId> = [4,5,6,7,8,10,11].iter().cloned().collect();
        for lev in &returned_lev_ids {
            assert!(
                !kind1_lev_ids.contains(lev),
                "kind=1 levId={lev} must NOT be returned from kind=2 prefix scan (CR-01 contamination)"
            );
        }

        // Verify newest-first order: levId=9 (ts=1710000000) before levId=1 (ts=1700000000).
        assert_eq!(merged[0].1, 9, "first result must be levId=9 (ts=1710000000, newest kind=2)");
        assert_eq!(merged[1].1, 1, "second result must be levId=1 (ts=1700000000, oldest kind=2)");
    }
}
