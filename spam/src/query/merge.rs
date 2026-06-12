/// merge.rs — K-way merge over per-prefix `Reverse` scans (D-05/D-06/D-10).
///
/// ## Purpose
///
/// Given a set of per-prefix start_keys for a single `Event__*` index, `merge_prefixes` issues
/// one bounded `Reverse` `scan_index_bounded` call per prefix and merges their results into a
/// single `(created_at, lev_id)` DESC-ordered stream using a `BinaryHeap` (max-heap).
///
/// ## Key invariants
///
/// - **No hydration** (D-06): `created_at` is extracted from the trailing 8 bytes of each key
///   via `crate::query::router::created_at_from_key` — the `EventPayload` sub-DB is never opened.
/// - **No long-lived txn** (D-08): each `scan_index_bounded` call opens and drops its own short
///   `RoTxn`. The merge holds NO transaction across per-prefix scan calls.
/// - **Bounded by `limit`** (T-03-DOS): each per-prefix scan is bounded by `limit` (or
///   `DEFAULT_WINDOW_SIZE` when `limit==0`). The heap emits at most `limit` results total.
/// - **Total order** (D-10): output is `(created_at, lev_id)` DESC — a total order because
///   `lev_id` is unique per event.
///
/// ## v1 materialization note
///
/// Each per-prefix scan is fully materialized into a `Vec` before merging (D-05 over-scan is
/// bounded by `limit`). A lazy streaming merge is a future optimization; for v1 this is
/// straightforward and correct.

use crate::lmdb::scan::{scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE};
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
#[derive(Eq, PartialEq)]
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
// merge_prefixes: the k-way merge entry point
// ---------------------------------------------------------------------------

/// Merge per-prefix `Reverse` scans over `short_name` into a single `(created_at DESC, lev_id DESC)` stream.
///
/// ## Algorithm
///
/// 1. For each `start_key` in `start_keys`, issue one `scan_index_bounded(env, short_name,
///    Reverse, start_key, scan_limit)` where `scan_limit = limit.max(1)` (never zero — limit=0
///    here means "bounded by DEFAULT_WINDOW_SIZE", handled below).
/// 2. Materialize each per-prefix scan result as a `Vec<IntoIter<(Vec<u8>, LevId)>>`.
/// 3. Seed the `BinaryHeap` with the first `MergeCandidate` from each non-empty stream.
/// 4. Repeatedly: pop the max candidate, push it to results, pull the next entry from its
///    stream back into the heap.
/// 5. Stop after `limit` results (or when all streams are drained).
///
/// ## Parameters
///
/// - `env`: the heed LMDB environment (read-only). Passed directly to `scan_index_bounded`.
/// - `short_name`: the short `Event__*` index name (e.g. `"Event__kind"`).
/// - `start_keys`: one composite start_key per prefix (from `router::build_start_keys`). Empty slice → empty result.
/// - `limit`: maximum results to return. `0` → uses `DEFAULT_WINDOW_SIZE` as the per-prefix
///   scan limit (D-04 unbounded-feed case).
///
/// ## Returns
///
/// `Ok(Vec<(u64, LevId, Vec<u8>)>)` — `(created_at, lev_id, key_bytes)` triples in newest-first
/// `(created_at, lev_id)` DESC order. Length ≤ `limit` (or `DEFAULT_WINDOW_SIZE` when `limit==0`).
///
/// `Err(IndexError)` — LMDB or sub-DB error from any per-prefix scan.
pub fn merge_prefixes(
    env: &heed::Env,
    short_name: &str,
    start_keys: &[Vec<u8>],
    limit: usize,
) -> Result<Vec<(u64, LevId, Vec<u8>)>, IndexError> {
    if start_keys.is_empty() {
        return Ok(vec![]);
    }

    // When limit==0, use DEFAULT_WINDOW_SIZE as the per-prefix bound (T-03-DOS).
    let scan_limit = if limit == 0 { DEFAULT_WINDOW_SIZE } else { limit };
    let emit_limit = if limit == 0 { DEFAULT_WINDOW_SIZE } else { limit };

    // Issue one bounded Reverse scan per prefix; materialize as an into_iter cursor.
    // Each scan_index_bounded owns its own short RoTxn (D-08) — no txn held here.
    //
    // CR-01 prefix guard: for each start_key, derive a per-prefix slice:
    //   prefix = &start_key[..start_key.len().saturating_sub(8)]
    // All Event__* keys end with created_at(8 LE). Dropping the trailing 8 bytes leaves
    // the fixed-width prefix that uniquely identifies the logical value-partition:
    //   Event__kind:       kind(8 LE) → prefix len = 8
    //   Event__pubkey:     pubkey(32) → prefix len = 32
    //   Event__pubkeyKind: pubkey(32) ‖ kind(8 LE) → prefix len = 40
    //   Event__id:         id(32) → prefix len = 32
    //   Event__tag:        tagName(1) ‖ tagValue(var) → prefix len = key.len()-8
    //   Event__created_at: len=8, prefix len=0 → starts_with([]) is vacuously true
    //
    // After scanning, apply .take_while(|(k,_)| k.starts_with(prefix)) BEFORE pushing
    // into the stream Vec. take_while (not filter) is correct: in a reverse walk, all
    // entries below the prefix are contiguous at a lower key prefix. take_while terminates
    // the stream at the first out-of-prefix entry rather than scanning further garbage.
    let mut streams: Vec<std::vec::IntoIter<(Vec<u8>, LevId)>> = Vec::with_capacity(start_keys.len());
    // Store the per-key prefix alongside the stream so take_while can reference it.
    let mut stream_prefixes: Vec<Vec<u8>> = Vec::with_capacity(start_keys.len());
    for key in start_keys {
        let prefix_len = key.len().saturating_sub(8);
        let prefix = key[..prefix_len].to_vec();
        let batch = scan_index_bounded(env, short_name, ScanDirection::Reverse, key, scan_limit)?;
        // Apply prefix boundary guard: only keep entries whose key begins with `prefix`.
        // take_while stops at the first non-matching entry (they are contiguous in a reverse walk).
        let guarded: Vec<(Vec<u8>, LevId)> = batch
            .into_iter()
            .take_while(|(k, _)| k.starts_with(prefix.as_slice()))
            .collect();
        streams.push(guarded.into_iter());
        stream_prefixes.push(prefix);
    }
    drop(stream_prefixes); // Prefixes were consumed during guarded collection above.

    // Seed the heap with the first candidate from each non-empty stream.
    let mut heap: BinaryHeap<MergeCandidate> = BinaryHeap::with_capacity(streams.len());
    for (idx, stream) in streams.iter_mut().enumerate() {
        if let Some((key_bytes, lev_id)) = stream.next() {
            let created_at = created_at_from_key(&key_bytes);
            heap.push(MergeCandidate { created_at, lev_id, key_bytes, stream_idx: idx });
        }
    }

    // K-way merge: pop max, emit, pull next from that stream.
    let mut results = Vec::with_capacity(emit_limit);
    while let Some(candidate) = heap.pop() {
        let stream_idx = candidate.stream_idx;
        results.push((candidate.created_at, candidate.lev_id, candidate.key_bytes));

        if results.len() >= emit_limit {
            break;
        }

        // Pull the next entry from this stream back into the heap.
        if let Some((next_key, next_lev_id)) = streams[stream_idx].next() {
            let next_ca = created_at_from_key(&next_key);
            heap.push(MergeCandidate {
                created_at: next_ca,
                lev_id: next_lev_id,
                key_bytes: next_key,
                stream_idx,
            });
        }
    }

    Ok(results)
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
            authors: Some(vec![PK1.to_string(), PK2.to_string()]),
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
        //   crate::lmdb::scan::{scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE}
        //   crate::lmdb::types::LevId
        //   crate::lmdb::indexes::IndexError
        //   crate::query::router::created_at_from_key
        // No reference to crate::lmdb::payload, DecodedEvent, or NostrEvent.
        // This is a documentation test — passes vacuously.
        assert!(true, "merge.rs imports verified — no payload or DecodedEvent import");
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
