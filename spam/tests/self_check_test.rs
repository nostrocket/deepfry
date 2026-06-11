/// self_check_test.rs — Integration tests for the comparator self-check gate.
///
/// Tests:
/// (a) run_comparator_self_check returns Ok(()) on the unmodified committed fixture
///     (all six indexes, full-sequence equality against golden vectors, NOW INCLUDING
///     comparator-dependent seeks on the adversarial key pairs — CR-01 gate)
/// (b) run_comparator_self_check returns Err(SelfCheckError::OrderMismatch) when a
///     golden vector is mutated to a wrong order (proves it is NOT a vacuous pass — T-03-04)
/// (c) Non-vacuous seek gate test: proves the seek gate detects a wrong/absent comparator.
///     On the fixture B-tree (built by strfry with golpe comparators), re-opening without
///     any comparator (memcmp fallback) causes a seek for (kind=256, ts=0) to land on
///     kind=255 (levId=3), NOT kind=256 (levId=2) — because in the golpe-order leaf page,
///     kind=255 LE bytes [0xFF…] are "≥" our lower_bound [0x00 0x01…] under memcmp.
///     This directly proves that run_comparator_self_check's seek gate WOULD return
///     Err(ComparatorSeekMismatch) if the wrong comparator were registered.
///
/// These tests use a temporary copy of the fixture to avoid LMDB double-open issues.
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::self_check::{run_comparator_self_check, GoldenVectors, SelfCheckError};

/// Copy the committed fixture to a temporary directory and open an env there.
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}

/// Open a fixture copy WITHOUT any custom comparator registered (memcmp fallback).
///
/// This is the control path for the non-vacuous seek gate test: by opening the fixture
/// env with no comparator, LMDB falls back to memcmp for MDB_SET_RANGE positioning.
/// On a golpe-built B-tree, memcmp positioning gives WRONG results for adversarial pairs.
///
/// Safety: uses NO_LOCK like open_fixture_env (CI/test-only; no concurrent writer).
fn open_temp_fixture_env_no_comparator() -> (heed::Env, tempfile::TempDir) {
    use heed::{EnvFlags, EnvOpenOptions};
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir for no-comparator env");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb"))
        .expect("copy data.mdb (no-comparator)");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb"))
        .expect("copy lock.mdb (no-comparator)");
    let env = unsafe {
        EnvOpenOptions::new()
            .max_dbs(20)
            .map_size(10_995_116_277_760)
            .flags(EnvFlags::READ_ONLY | EnvFlags::NO_LOCK)
            .open(tmp.path())
            .expect("open fixture env without comparator")
    };
    (env, tmp)
}

/// (a) Self-check passes on the unmodified committed fixture.
///
/// Verifies run_comparator_self_check returns Ok(()) when the LMDB indexes match
/// the committed golden vectors — all six indexes, full-sequence equality.
/// This is the primary correctness test (LMDB-06 / D-06 / D-07).
#[test]
fn test_self_check_passes_on_fixture() {
    let (env, _tmp) = open_temp_fixture_env();
    let golden = GoldenVectors::load_committed().expect("load committed golden vectors");
    let result = run_comparator_self_check(&env, &golden);
    if let Err(ref e) = result {
        eprintln!("Self-check unexpectedly failed: {e}");
        if let SelfCheckError::OrderMismatch { index, expected, actual } = e {
            eprintln!("  Index: {index}");
            eprintln!("  Expected: {expected:?}");
            eprintln!("  Actual:   {actual:?}");
        }
    }
    assert!(
        result.is_ok(),
        "run_comparator_self_check must return Ok(()) on the committed fixture: {:?}",
        result.err()
    );
}

/// (b) Self-check fails when a golden vector is mutated to a wrong order.
///
/// Mutates an in-memory copy of one golden vector (reverses the ordered_lev_ids)
/// and verifies run_comparator_self_check returns Err(OrderMismatch).
/// This PROVES the self-check is not a vacuous pass (T-03-04 mitigation).
/// The fixture file is NOT modified — mutation is in-memory only.
#[test]
fn test_self_check_fails_on_mutated_golden_vector() {
    let (env, _tmp) = open_temp_fixture_env();

    // Load the committed golden vectors and mutate one in-memory.
    let mut golden = GoldenVectors::load_committed().expect("load committed golden vectors");

    // Mutate: reverse the expected order for Event__id.
    // The reversed sequence is guaranteed to be wrong since the fixture has 11 events
    // in non-palindromic scan order (3, 4, 1, 8, 7, 10, 6, 2, 9, 5, 11 reversed =
    // 11, 5, 9, 2, 6, 10, 7, 8, 1, 4, 3 — which is not the LMDB scan order).
    let original_len = golden
        .get("Event__id")
        .expect("Event__id golden vector must exist")
        .len();
    assert!(original_len > 1, "fixture must have >1 entry to test mutation");

    golden.mutate_reverse("Event__id");

    let result = run_comparator_self_check(&env, &golden);
    assert!(
        result.is_err(),
        "run_comparator_self_check must return Err when golden vector is mutated (T-03-04)"
    );
    match result.unwrap_err() {
        SelfCheckError::OrderMismatch { index, .. } => {
            assert_eq!(
                index, "Event__id",
                "OrderMismatch must identify the mutated index"
            );
        }
        e => panic!("Expected SelfCheckError::OrderMismatch, got: {e}"),
    }
}

/// (c) run_comparator_self_check now includes the seek gate: verify it still returns
///     Ok on the committed fixture WITH the comparator-dependent seeks active.
///
/// This supersedes the original test_self_check_passes_on_fixture semantics —
/// it now exercises the comparator gate (seek phase) in addition to the existing
/// physical-order integrity scan (full forward iter phase). Both phases must pass.
///
/// Also verifies the ComparatorSeekMismatch error variant is correctly named/typed
/// (compile-time proof that the variant exists post-implementation).
#[test]
fn test_self_check_with_seek_gate_passes_on_fixture() {
    let (env, _tmp) = open_temp_fixture_env();
    let golden = GoldenVectors::load_committed().expect("load committed golden vectors");
    let result = run_comparator_self_check(&env, &golden);
    if let Err(ref e) = result {
        eprintln!("Self-check (with seek gate) unexpectedly failed: {e}");
        // Pattern-match on ComparatorSeekMismatch to verify the variant exists.
        // This is a compile-time check — if the variant is missing, this won't compile.
        if let SelfCheckError::ComparatorSeekMismatch { index, expected_lev_id, actual_lev_id } = e {
            eprintln!(
                "  ComparatorSeekMismatch: index={index} expected={expected_lev_id} actual={actual_lev_id}"
            );
        }
    }
    assert!(
        result.is_ok(),
        "run_comparator_self_check (including seek gate) must return Ok(()) \
         on the committed fixture with golpe comparators registered: {:?}",
        result.err()
    );
    println!("PASS: seek gate + physical-order scan both pass on committed fixture");
}

/// (d) NON-VACUOUS seek gate test — proves the seek gate detects a wrong/absent comparator.
///
/// This test opens the fixture WITHOUT any custom comparator registered (memcmp fallback)
/// and performs the same seek that run_comparator_self_check uses: lower_bound=(kind=256, ts=0).
///
/// On the golpe-built fixture B-tree, seeking with memcmp for lower_bound=(kind=256, ts=0)
/// lands on a WRONG entry (NOT levId=2, the golpe-correct kind=256 answer).
///
/// Why the divergence occurs:
///   - The fixture B-tree physical order is golpe order: kind=1, kind=2, kind=255, kind=256
///   - Lower bound bytes for (kind=256, ts=0): [0x00, 0x01, 0x00…, 0x00…]
///   - Under memcmp, kind=1 LE [0x01, 0x00, …] > lower_bound [0x00, 0x01, …] at byte[0]
///   - So the FIRST entry in the golpe-ordered leaf that memcmp considers "≥ lower_bound"
///     is kind=1 (levId=4) — not kind=256 (levId=2, the golpe-correct answer)
///   - (kind=256 itself, LE [0x00, 0x01, …], is physically last in golpe order; on the
///      leaf page it comes after kind=255 LE [0xFF, 0x00, …])
///
/// Therefore: run_comparator_self_check expects levId=2 (golpe-correct) but a memcmp seek
/// would return a different levId → Err(SelfCheckError::ComparatorSeekMismatch) — gate TRIPS.
///
/// This test asserts the memcmp-landing levId is NOT 2 (proving divergence), confirming the
/// gate is non-vacuous.  The exact memcmp landing position on the fixture is also asserted
/// for pinned reproducibility.
#[test]
fn test_seek_gate_detects_memcmp_comparator_on_fixture() {
    use heed::types::Bytes;
    use std::ops::Bound;

    // Open fixture WITHOUT custom comparator — LMDB uses memcmp fallback.
    let (env, _tmp) = open_temp_fixture_env_no_comparator();

    let rtxn = env.read_txn().expect("read txn");

    // Open Event__kind sub-DB WITHOUT comparator (raw Bytes, no key_comparator call).
    // This is the memcmp/default path — no golpe Uint64Uint64Cmp registered.
    let full_name = format!("rasgueadb_defaultDb__{}", "Event__kind");
    let db: heed::Database<Bytes, Bytes> = env
        .database_options()
        .types::<Bytes, Bytes>()
        // Intentionally NO .key_comparator::<Uint64Uint64Cmp>() — this is the memcmp control
        .name(&full_name)
        .open(&rtxn)
        .expect("open Event__kind without comparator")
        .expect("Event__kind must exist in fixture");

    // Seek lower_bound: (kind=256, ts=0) — same bytes run_comparator_self_check uses
    // kind=256 LE: [0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
    // ts=0 LE:     [0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
    let mut lower_bound = Vec::with_capacity(16);
    lower_bound.extend_from_slice(&256u64.to_le_bytes()); // kind=256
    lower_bound.extend_from_slice(&0u64.to_le_bytes());   // ts=0

    let range = (Bound::Included(lower_bound.as_slice()), Bound::Unbounded);
    let mut iter = db.range(&rtxn, &range).expect("range seek on no-comparator db");

    let first = iter.next().expect("must find an entry — kind=1 entries exist in fixture")
        .expect("first range entry must not error");
    let (_key, value) = first;

    assert!(value.len() >= 8, "VALUE must be at least 8 bytes (levId)");
    let landing_lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());

    // NON-VACUOUS CORE ASSERTION: the memcmp seek does NOT land on levId=2.
    // run_comparator_self_check expects levId=2; any other result causes Err(ComparatorSeekMismatch).
    assert_ne!(
        landing_lev_id, 2,
        "NON-VACUOUS PROOF: memcmp seek for lower_bound=(kind=256, ts=0) on the golpe-built \
         fixture B-tree must NOT land on levId=2 (kind=256, the golpe-correct answer). \
         Got levId={landing_lev_id}. If it DID land on levId=2, the gate would be vacuous \
         (it would pass even with a wrong comparator)."
    );

    // Pinned reproducibility assertion: on this fixture, memcmp lands on levId=4 (kind=1).
    // kind=1 LE bytes [0x01, 0x00, …] > lower_bound [0x00, 0x01, …] at byte[0] under memcmp,
    // making kind=1 the first entry the memcmp linear scan considers "≥ lower_bound".
    assert_eq!(
        landing_lev_id, 4,
        "PINNED: memcmp seek on the committed fixture must land on levId=4 (kind=1 — \
         first golpe-order entry with LE bytes > lower_bound under memcmp). \
         Got levId={landing_lev_id}."
    );

    println!(
        "NON-VACUOUS PASS: memcmp seek landed on levId={landing_lev_id} (NOT the \
         golpe-correct levId=2). → run_comparator_self_check would return \
         Err(ComparatorSeekMismatch {{ expected: 2, actual: {landing_lev_id} }}) on this path."
    );
}
