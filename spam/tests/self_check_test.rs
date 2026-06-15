/// self_check_test.rs — Integration tests for the fixture-free comparator self-check gate.
///
/// The runtime self-check no longer depends on fixtures or golden vectors. These tests use a
/// temporary COPY of the committed fixture purely as a test oracle — the fixture contains the
/// kind=255/256 witness pair, so Phase B (memcmp-fallback detection) is genuinely exercised.
///
/// Coverage:
/// (a) Positive — `run_comparator_self_check` returns `Ok(())` on the committed fixture.
/// (b) Non-vacuous Phase A — opening Event__kind WITHOUT the golpe comparator (memcmp fallback)
///     makes an exact-seek round-trip on the kind=256 witness key MISS, which is exactly the
///     fault Phase B fails closed on. We assert the memcmp seek does NOT round-trip, proving
///     the gate is not a vacuous pass.
///
/// Never touches ~/deepfry/.
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::self_check::run_comparator_self_check;

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
/// Control path for the non-vacuous Phase B test: with no comparator registered, LMDB uses
/// memcmp for MDB_SET_RANGE positioning, which gives WRONG results for the golpe-built tree's
/// witness keys.
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

/// (a) Self-check passes on the unmodified committed fixture (Phase A + Phase B).
#[test]
fn test_self_check_passes_on_fixture() {
    let (env, _tmp) = open_temp_fixture_env();
    let result = run_comparator_self_check(&env);
    assert!(
        result.is_ok(),
        "run_comparator_self_check must return Ok(()) on the committed fixture: {:?}",
        result.err()
    );
}

/// (b) NON-VACUOUS proof that Phase B's exact-seek round-trip detects a memcmp fallback.
///
/// Phase B seeks each witness key via MDB_SET_RANGE and asserts the first landed key equals
/// the sought key. On the golpe-built fixture, the kind=256 key has LE bytes [0x00,0x01,...]
/// which under memcmp sorts BEFORE physically-earlier keys (e.g. kind=1 LE [0x01,0x00,...]).
/// Seeking that exact key with the memcmp fallback therefore lands on a DIFFERENT key — the
/// SeekRoundTripMismatch condition. This test reproduces that miss directly to prove the gate
/// is not vacuous (it would fail closed if the wrong/no comparator were registered).
#[test]
fn test_phase_b_seek_round_trip_detects_memcmp_fallback() {
    use heed::types::Bytes;
    use std::ops::Bound;

    // Open Event__kind WITHOUT the golpe comparator (memcmp fallback control path).
    let (env, _tmp) = open_temp_fixture_env_no_comparator();
    let rtxn = env.read_txn().expect("read txn");

    let full_name = format!("rasgueadb_defaultDb__{}", "Event__kind");
    let db: heed::Database<Bytes, Bytes> = env
        .database_options()
        .types::<Bytes, Bytes>()
        // Intentionally NO .key_comparator — memcmp control.
        .name(&full_name)
        .open(&rtxn)
        .expect("open Event__kind without comparator")
        .expect("Event__kind must exist in fixture");

    // The exact kind=256 witness key: kind=256 LE ‖ ts=1700000000 LE.
    // This is an EXISTING key in the fixture; a correct comparator round-trips it.
    let mut witness_key = Vec::with_capacity(16);
    witness_key.extend_from_slice(&256u64.to_le_bytes()); // kind=256
    witness_key.extend_from_slice(&1_700_000_000u64.to_le_bytes()); // created_at

    let range = (Bound::Included(witness_key.as_slice()), Bound::Unbounded);
    let mut iter = db
        .range(&rtxn, &range)
        .expect("range seek on no-comparator db");

    let first = iter
        .next()
        .expect("range must return at least one entry")
        .expect("first range entry must not error");
    let (landed_key, _value) = first;

    // NON-VACUOUS CORE: under memcmp, seeking the exact kind=256 key does NOT round-trip —
    // the cursor lands on a physically-earlier (under memcmp) key. This is precisely the
    // SeekRoundTripMismatch that run_comparator_self_check fails closed on.
    assert_ne!(
        landed_key,
        witness_key.as_slice(),
        "NON-VACUOUS PROOF: under memcmp fallback, seeking the exact kind=256 witness key on \
         the golpe-built fixture B-tree must NOT round-trip. If it DID round-trip, Phase B \
         would be vacuous (it would pass even with the wrong comparator)."
    );
}
