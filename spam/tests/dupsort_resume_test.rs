/// dupsort_resume_test.rs — CR-01 regression coverage for the reverse windowed-scan
/// DUPSORT resume predicate.
///
/// ## Background (CR-01)
///
/// The original `scan_index_windowed` resumed every window with `Bound::Included(resume_key)`
/// plus a per-levId skip predicate (forward `lev_id <= resume_lev_id`, reverse
/// `lev_id >= resume_lev_id`). The reverse branch was broken: heed 0.22.1 resumes reverse
/// DUPSORT groups by positioning the cursor at the SMALLEST dup of the boundary key (via
/// `move_on_range_end`'s `MDB_SET_RANGE`) and immediately stepping to the previous key — the
/// higher, still-unemitted dups of that key are NEVER yielded, so the skip predicate cannot
/// recover them. The result was either silent data loss OR (with the lev_id skip in place) a
/// non-terminating window loop. This file:
///
/// 1. EMPIRICALLY PROVES heed's per-key duplicate iteration order under BOTH `range` and
///    `rev_range` with `.move_through_duplicate_values()` against a synthetic DUPSORT +
///    INTEGERDUP sub-DB built with strfry's real `Uint64Uint64Cmp` key comparator and the
///    real `rasgueadb_defaultDb__Event__kind` sub-DB name. PROVEN: forward dups ASCENDING,
///    reverse dups DESCENDING (rev_range fully reverses the forward sequence).
///
/// 2. REGRESSION: runs `scan_index_windowed(Reverse, ..., window=2)` over synthetic indexes
///    whose keys carry dup groups of size 3 ({5,6,7}) straddling the window boundary, and
///    asserts the FULL set of levIds is returned with no missing or duplicated levId. The
///    accompanying non-vacuity test reproduces the OLD loop logic and proves it drops a levId.
///
/// FIX: key-granular windowing — `collect_window` drains the boundary dup group so a window
/// always ends on a KEY boundary, and the loop resumes with `Bound::Excluded(resume_key)`.
///
/// The synthetic env legitimately uses write transactions (it is NOT strfry's env); the
/// read-only rule applies only to strfry's live database.
///
/// NOTE: `DatabaseFlags::INTEGER_DUP` is deprecated in heed 0.22 (it suggests
/// `dup_sort_comparator::<IntegerComparator>()` instead). We use the literal MDB_INTEGERDUP
/// flag DELIBERATELY — it is exactly the flag strfry sets on its Event__* indexes, so the
/// synthetic fixture must replicate it to faithfully reproduce the on-disk dup ordering these
/// tests assert against. `#[allow(deprecated)]` annotates each use site.
use heed::types::Bytes;
use heed::{DatabaseFlags, EnvOpenOptions};
use lmdb2graphql::lmdb::comparators::Uint64Uint64Cmp;
use lmdb2graphql::lmdb::scan::{scan_index_windowed, ScanDirection};

/// Full sub-DB name strfry/golpe uses for Event__kind. `scan_index_windowed` resolves the
/// `"Event__kind"` short name to this via `full_db_name`, so the synthetic DB MUST use it.
const EVENT_KIND_FULL: &str = "rasgueadb_defaultDb__Event__kind";

/// Build a Uint64Uint64-shaped key: kind(8 LE) ‖ created_at(8 LE) = 16 bytes.
///
/// NOTE on the golpe-comparator key-width SIGABRT quirk: the FFI comparator
/// (`lmdb_comparator__Uint64Uint64_safe`) calls `std::abort()` on keys that are not exactly
/// 16 bytes. Every key handed to a `Uint64Uint64Cmp`-typed DB — including range start keys —
/// MUST be a full 16-byte composite, or LMDB's B-tree positioning will invoke the comparator
/// on a short key and SIGABRT the test process. The reverse-scan high key below is therefore
/// (kind=MAX, ts=MAX), a full 16 bytes, never an empty or truncated slice.
fn kind_key(kind: u64, created_at: u64) -> Vec<u8> {
    let mut k = Vec::with_capacity(16);
    k.extend_from_slice(&kind.to_le_bytes());
    k.extend_from_slice(&created_at.to_le_bytes());
    k
}

/// High start key for a reverse scan spanning the whole index: (kind=MAX, ts=MAX).
/// Full 16 bytes — avoids the comparator key-width SIGABRT quirk documented above.
fn kind_reverse_high_key() -> Vec<u8> {
    kind_key(u64::MAX, u64::MAX)
}

/// Open a throwaway test env (tempdir — NOT strfry's env). Write transactions are legitimate
/// here. `max_dbs >= 2` so the named sub-DB plus the unnamed root both fit.
fn open_test_env(path: &std::path::Path) -> heed::Env {
    unsafe {
        EnvOpenOptions::new()
            .max_dbs(5)
            .map_size(10 * 1024 * 1024) // 10 MiB — ample for a handful of entries
            .open(path)
            .expect("open throwaway test LMDB env")
    }
}

/// Create the synthetic `rasgueadb_defaultDb__Event__kind` sub-DB with strfry's exact on-disk
/// shape — `Uint64Uint64Cmp` key comparator + `MDB_DUPSORT | MDB_INTEGERDUP` — and populate
/// `entries` as (key, levId) pairs. Returns the populated env.
///
/// strfry stores the levId as the VALUE, an 8-byte LE u64, under DUPSORT+INTEGERDUP. We
/// replicate that exactly so `scan_index_windowed` reads it back the same way it reads the
/// real index.
fn build_synthetic_event_kind(path: &std::path::Path, entries: &[(Vec<u8>, u64)]) {
    let env = open_test_env(path);
    let mut wtxn = env.write_txn().expect("write txn");
    let db: heed::Database<Bytes, Bytes, Uint64Uint64Cmp> = {
        let mut opts = env
            .database_options()
            .types::<Bytes, Bytes>()
            .key_comparator::<Uint64Uint64Cmp>();
        // DUPSORT + INTEGERDUP: identical to strfry's Event__* indexes (CLAUDE.md / spec §3).
        // INTEGERDUP makes LMDB sort the 8-byte LE levId VALUEs as native integers.
        #[allow(deprecated)] // MDB_INTEGERDUP: deliberate strfry on-disk replication
        opts.flags(DatabaseFlags::DUP_SORT | DatabaseFlags::INTEGER_DUP);
        opts.name(EVENT_KIND_FULL);
        opts.create(&mut wtxn)
            .expect("create synthetic Event__kind sub-DB")
    };
    for (key, lev_id) in entries {
        db.put(&mut wtxn, key.as_slice(), &lev_id.to_le_bytes())
            .expect("put (key, levId)");
    }
    wtxn.commit().expect("commit synthetic data");
}

// ---------------------------------------------------------------------------
// (1) EMPIRICAL PROOF of per-key dup iteration order
// ---------------------------------------------------------------------------

/// Prove the per-key duplicate VALUE order under BOTH `range` (forward) and `rev_range`
/// (reverse), each with `.move_through_duplicate_values()`, on a DUPSORT+INTEGERDUP DB.
///
/// Single key `kind=1/ts=1000` carries dup levIds {5,6,7}. The CR question: does `rev_range`
/// reverse the per-key dup order, or only the KEY traversal?
///
/// PROVEN RESULT (asserted below, empirically, heed 0.22.1 + INTEGERDUP):
///   - forward `range`     → [5,6,7,8,9]  (keys ascending, per-key dups ASCENDING)
///   - reverse `rev_range` → [9,8,7,6,5]  (keys descending, per-key dups DESCENDING)
///
/// `rev_range` reverses BOTH the KEY traversal AND the within-key dup-cursor order — it is a
/// FULL reversal of the forward sequence. This contradicts the original scan.rs doc comment
/// (which claimed dups stay ascending under rev_range). The reverse resume predicate must be
/// built on THIS proven order.
#[test]
fn test_proven_dup_iteration_order_range_and_rev_range() {
    let dir = tempfile::tempdir().expect("tempdir");
    // Two keys so KEY traversal direction is observable distinctly from dup order.
    //   key_lo = kind=1/ts=1000 → dups {5,6,7}
    //   key_hi = kind=2/ts=1000 → dups {8,9}
    let key_lo = kind_key(1, 1000);
    let key_hi = kind_key(2, 1000);
    let entries = vec![
        (key_lo.clone(), 5u64),
        (key_lo.clone(), 6u64),
        (key_lo.clone(), 7u64),
        (key_hi.clone(), 8u64),
        (key_hi.clone(), 9u64),
    ];
    build_synthetic_event_kind(dir.path(), &entries);

    let env = open_test_env(dir.path());
    let rtxn = env.read_txn().expect("rtxn");
    let db: heed::Database<Bytes, Bytes, Uint64Uint64Cmp> = {
        let mut opts = env
            .database_options()
            .types::<Bytes, Bytes>()
            .key_comparator::<Uint64Uint64Cmp>();
        #[allow(deprecated)] // MDB_INTEGERDUP: deliberate strfry on-disk replication
        opts.flags(DatabaseFlags::DUP_SORT | DatabaseFlags::INTEGER_DUP);
        opts.name(EVENT_KIND_FULL);
        opts.open(&rtxn).expect("open").expect("must exist")
    };

    let read_lev = |v: &[u8]| u64::from_le_bytes(v[0..8].try_into().unwrap());

    // --- forward range ---
    let fwd: Vec<(Vec<u8>, u64)> = db
        .range(&rtxn, &(std::ops::Bound::Unbounded, std::ops::Bound::Unbounded))
        .expect("range")
        .move_through_duplicate_values()
        .map(|r| {
            let (k, v) = r.expect("item");
            (k.to_vec(), read_lev(v))
        })
        .collect();
    let fwd_levs: Vec<u64> = fwd.iter().map(|(_, l)| *l).collect();

    // --- reverse rev_range ---
    let rev: Vec<(Vec<u8>, u64)> = db
        .rev_range(&rtxn, &(std::ops::Bound::Unbounded, std::ops::Bound::Unbounded))
        .expect("rev_range")
        .move_through_duplicate_values()
        .map(|r| {
            let (k, v) = r.expect("item");
            (k.to_vec(), read_lev(v))
        })
        .collect();
    let rev_levs: Vec<u64> = rev.iter().map(|(_, l)| *l).collect();

    println!("PROVE: forward  range  levIds = {:?}", fwd_levs);
    println!("PROVE: reverse rev_range levIds = {:?}", rev_levs);

    // PROVEN INVARIANT 1: forward yields keys ascending, dups ASCENDING within key.
    assert_eq!(
        fwd_levs,
        vec![5, 6, 7, 8, 9],
        "forward range must yield keys ascending with per-key dups ascending"
    );

    // PROVEN INVARIANT 2: rev_range FULLY reverses the forward sequence — KEY traversal
    // descends (key_hi group first) AND per-key dups descend (key_hi dups are [9,8], key_lo
    // dups are [7,6,5]). This is the crux the reverse resume predicate depends on.
    assert_eq!(
        rev_levs,
        vec![9, 8, 7, 6, 5],
        "rev_range must FULLY reverse the forward order — keys descending AND per-key dups \
         descending. If dups stayed ascending this would be [8,9,5,6,7]."
    );

    // Explicit, narrow assertion isolating the dup-order claim for the boundary group key_lo:
    // the LAST three emitted (the key_lo group under rev_range) are DESCENDING [7,6,5].
    assert_eq!(
        &rev_levs[2..],
        &[7, 6, 5],
        "boundary-group dups under rev_range MUST be DESCENDING [7,6,5]. Therefore when a window
         boundary splits this group, the dups already emitted are the HIGHEST ones, and the
         last-emitted levId is the MINIMUM of the emitted set. The reverse resume predicate must
         skip dups >= the last-emitted (minimum) levId — i.e. `lev_id >= resume_lev_id` — which
         retains the still-unemitted LOWER dups."
    );
}

// ---------------------------------------------------------------------------
// (2) REGRESSION: reverse window smaller than a dup group
// ---------------------------------------------------------------------------

/// CR-01 REGRESSION: a reverse windowed scan with `window_size=2` over an index whose single
/// key carries a dup group of size 3 ({5,6,7}) MUST return all of {5,6,7} with NO missing
/// levId and no duplicate emission.
///
/// Layout: one key `kind=1/ts=1000` with dups {5,6,7}. Under the PROVEN reverse order the dups
/// emit DESCENDING [7,6,5]. With window=2 the group straddles the boundary. The corrected
/// key-granular fix DRAINS the boundary group (so the single window actually yields all of
/// [7,6,5] before stopping on the key boundary), guaranteeing completeness.
///
/// Companion `test_reverse_window_straddle_non_first_group_no_drop` (non-first group) is where
/// the OLD code provably DROPPED a levId; `test_old_code_reverse_drops_levid_nonvacuity`
/// reproduces that data loss to prove the regression suite is non-vacuous.
#[test]
fn test_reverse_window_smaller_than_dup_group_no_drop() {
    let dir = tempfile::tempdir().expect("tempdir");
    let key = kind_key(1, 1000);
    let entries = vec![
        (key.clone(), 5u64),
        (key.clone(), 6u64),
        (key.clone(), 7u64),
    ];
    build_synthetic_event_kind(dir.path(), &entries);

    let env = open_test_env(dir.path());

    // window_size = 2 < dup-group size 3 → the group straddles the window boundary.
    let results = scan_index_windowed(
        &env,
        "Event__kind",
        ScanDirection::Reverse,
        &kind_reverse_high_key(),
        2,
    )
    .expect("reverse windowed scan must not error");

    let lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();
    println!("CR-01 regression: reverse window=2 over dup group {{5,6,7}} → {:?}", lev_ids);

    // No missing levId: all three dups must be present.
    let mut sorted = lev_ids.clone();
    sorted.sort_unstable();
    assert_eq!(
        sorted,
        vec![5, 6, 7],
        "reverse window=2 over a size-3 dup group MUST return all of {{5,6,7}} with no drop, \
         got {:?}",
        lev_ids
    );

    // No duplicate emission either.
    let mut deduped = sorted.clone();
    deduped.dedup();
    assert_eq!(
        deduped.len(),
        lev_ids.len(),
        "reverse windowed scan must not double-emit any levId, got {:?}",
        lev_ids
    );
}

/// Larger straddle: two keys, the SECOND (lower) key carrying a size-3 dup group, with a
/// window that splits that group. THIS is the layout where the OLD code provably dropped a
/// levId (see the non-vacuity test). Proves the fix holds when the boundary group is NOT the
/// first group emitted in reverse order.
///
/// Layout (reverse emits key_hi group first, then key_lo group, dups DESCENDING):
///   key_hi = kind=2/ts=1000 → dups {10}
///   key_lo = kind=1/ts=1000 → dups {5,6,7}
/// Reverse order of emission: [10, 7, 6, 5]. window=2 with the OLD code:
///   batch1 = [10, 7] (resume_lev_id=7, resume_key=key_lo); batch2 resumes Included(key_lo)
///   which re-positions at the SMALLEST dup 5 and steps away — yielding only [5], DROPPING 6.
///   The corrected key-granular fix drains the key_lo group in one window → all of {5,6,7,10}.
#[test]
fn test_reverse_window_straddle_non_first_group_no_drop() {
    let dir = tempfile::tempdir().expect("tempdir");
    let key_lo = kind_key(1, 1000);
    let key_hi = kind_key(2, 1000);
    let entries = vec![
        (key_lo.clone(), 5u64),
        (key_lo.clone(), 6u64),
        (key_lo.clone(), 7u64),
        (key_hi.clone(), 10u64),
    ];
    build_synthetic_event_kind(dir.path(), &entries);

    let env = open_test_env(dir.path());
    let results = scan_index_windowed(
        &env,
        "Event__kind",
        ScanDirection::Reverse,
        &kind_reverse_high_key(),
        2,
    )
    .expect("reverse windowed scan must not error");

    let lev_ids: Vec<u64> = results.iter().map(|(_, l)| *l).collect();
    println!("CR-01 regression (non-first group): reverse window=2 → {:?}", lev_ids);

    let mut sorted = lev_ids.clone();
    sorted.sort_unstable();
    assert_eq!(
        sorted,
        vec![5, 6, 7, 10],
        "all levIds across both groups must survive a window that splits the second group, got {:?}",
        lev_ids
    );
    let mut deduped = sorted.clone();
    deduped.dedup();
    assert_eq!(deduped.len(), lev_ids.len(), "no double emission, got {:?}", lev_ids);
}

/// NON-VACUITY PROOF: faithfully reproduce the OLD reverse windowing loop (Bound::Included
/// resume + the `lev_id >= resume_lev_id` skip predicate + a hard `window_size` break that
/// does NOT drain the boundary dup group) over the non-first-group layout, and assert it
/// silently DROPS a levId.
///
/// Layout: key_hi=kind2 dups{10}, key_lo=kind1 dups{5,6,7}. Reverse emission (DESCENDING dups)
/// = [10,7,6,5]. OLD code, window=2:
///   batch1 = [10,7]; resume_key=key_lo, resume_lev_id=7.
///   batch2 resumes Included(key_lo): heed's rev_range END=Included positions at the SMALLEST
///   dup of key_lo (5) and steps to prev key — it yields only [5]. The `>=7` skip is moot.
///   levId 6 is NEVER yielded by heed → silently dropped.
/// OLD result = [10,7,5] (6 missing). This proves the bug is real and the regression suite is
/// non-vacuous; the corrected `scan_index_windowed` returns the complete {5,6,7,10}.
#[test]
fn test_old_code_reverse_drops_levid_nonvacuity() {
    let dir = tempfile::tempdir().expect("tempdir");
    let key_lo = kind_key(1, 1000);
    let key_hi = kind_key(2, 1000);
    let entries = vec![
        (key_lo.clone(), 5u64),
        (key_lo.clone(), 6u64),
        (key_lo.clone(), 7u64),
        (key_hi.clone(), 10u64),
    ];
    build_synthetic_event_kind(dir.path(), &entries);

    let env = open_test_env(dir.path());
    let read_lev = |v: &[u8]| u64::from_le_bytes(v[0..8].try_into().unwrap());

    // Faithful reproduction of the OLD windowing loop (pre-CR-01).
    let window_size = 2usize;
    let mut all: Vec<u64> = Vec::new();
    let mut resume_key = kind_reverse_high_key();
    let mut resume_lev_id: u64 = 0;
    let mut first_batch = true;

    loop {
        let rtxn = env.read_txn().expect("rtxn");
        let db: heed::Database<Bytes, Bytes, Uint64Uint64Cmp> = {
            let mut opts = env
                .database_options()
                .types::<Bytes, Bytes>()
                .key_comparator::<Uint64Uint64Cmp>();
            #[allow(deprecated)] // MDB_INTEGERDUP: deliberate strfry on-disk replication
            opts.flags(DatabaseFlags::DUP_SORT | DatabaseFlags::INTEGER_DUP);
            opts.name(EVENT_KIND_FULL);
            opts.open(&rtxn).expect("open").expect("exists")
        };
        // OLD resume bound: ALWAYS Bound::Included(resume_key).
        let range = (
            std::ops::Bound::Unbounded,
            std::ops::Bound::Included(resume_key.as_slice()),
        );
        let iter = db
            .rev_range(&rtxn, &range)
            .expect("rev_range")
            .move_through_duplicate_values();
        let mut batch: Vec<(Vec<u8>, u64)> = Vec::new();
        for item in iter {
            // OLD hard break at window_size (no group drain).
            if batch.len() >= window_size {
                break;
            }
            let (k, v) = item.expect("item");
            let lev_id = read_lev(v);
            // OLD (buggy) skip predicate:
            if !first_batch && k == resume_key.as_slice() && lev_id >= resume_lev_id {
                continue;
            }
            batch.push((k.to_vec(), lev_id));
        }
        drop(rtxn);
        first_batch = false;
        if batch.is_empty() {
            break;
        }
        let (last_key, last_lev) = batch.last().unwrap();
        resume_key = last_key.clone();
        resume_lev_id = *last_lev;
        all.extend(batch.iter().map(|(_, l)| *l));
    }

    println!("NON-VACUITY: OLD code over [10|7,6,5] window=2 → {:?}", all);
    assert!(
        !all.contains(&6),
        "non-vacuity proof FAILED: the OLD code was expected to DROP levId 6, but it appeared \
         in {:?}. If no levId is dropped the regression test cannot prove the fix.",
        all
    );
    assert_eq!(
        all,
        vec![10, 7, 5],
        "OLD code must emit [10,7,5] and silently drop levId 6 (the CR-01 data loss)"
    );
}
