/// self_check_test.rs — Integration tests for the comparator self-check gate.
///
/// Tests:
/// (a) run_comparator_self_check returns Ok(()) on the unmodified committed fixture
///     (all six indexes, full-sequence equality against golden vectors)
/// (b) run_comparator_self_check returns Err(SelfCheckError::OrderMismatch) when a
///     golden vector is mutated to a wrong order (proves it is NOT a vacuous pass — T-03-04)
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
