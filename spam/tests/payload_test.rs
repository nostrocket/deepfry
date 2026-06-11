/// payload_test.rs — Integration tests for the EventPayload 0x00 decode path (LMDB-07).
///
/// These tests read live EventPayload values from the committed strfry fixture and decode
/// them, proving:
/// (a) each tested levId's stored value starts with the 0x00 raw-JSON type tag,
/// (b) the decoded NostrEvent.id matches the golden-vector id_prefix for that levId,
/// (c) DecodedEvent.raw_json is the exact JSON bytes — re-parsing them yields the same id
///     (D-01: the retained bytes are a faithful, txn-independent passthrough),
/// (d) all 11 fixture levIds decode without error (full-coverage smoke test).
///
/// The fixture is copied to a tempdir per test to avoid LMDB double-open issues.
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::payload::{decode_event_payload, get_event_payload};
use lmdb2graphql::lmdb::types::NostrEvent;

/// Copy the committed fixture to a temporary directory and open an env there.
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}

/// levId → expected id_prefix, drawn from tests/fixture/golden_vectors/Event__id.json
/// (`derivation_notes.event_ids_in_order`). Ground truth for the round-trip assertions.
const LEVID_ID_PREFIX: &[(u64, &str)] = &[
    (1, "1bdede2c"),
    (4, "ee5e90b9"),
    (11, "e7c4c1b0"),
];

/// Round-trip: for a representative set of levIds, fetch + decode the 0x00 payload and assert
/// (a) the stored value's leading byte is 0x00, (b) decoded id matches the golden prefix,
/// (c) the retained raw_json re-parses to the same id (D-01).
#[test]
fn test_decode_0x00_round_trip_against_golden_vectors() {
    let (env, _tmp) = open_temp_fixture_env();

    for &(lev_id, expected_prefix) in LEVID_ID_PREFIX {
        let raw = get_event_payload(&env, lev_id)
            .unwrap_or_else(|e| panic!("get_event_payload(levId={lev_id}) failed: {e}"));

        // (a) the fixture is a default 0x00 (raw-JSON) deployment.
        assert_eq!(
            raw.first().copied(),
            Some(0x00u8),
            "levId={lev_id} EventPayload must start with the 0x00 raw-JSON type tag"
        );

        let decoded = decode_event_payload(&raw)
            .unwrap_or_else(|e| panic!("decode_event_payload(levId={lev_id}) failed: {e}"));

        // (b) decoded id matches the golden-vector prefix for this levId.
        assert!(
            decoded.event.id.starts_with(expected_prefix),
            "levId={lev_id}: decoded id {} must start with golden prefix {expected_prefix}",
            decoded.event.id
        );

        // (c) the retained raw_json is the exact JSON — re-parsing yields the same id (D-01).
        let reparsed: NostrEvent = serde_json::from_slice(&decoded.raw_json)
            .unwrap_or_else(|e| panic!("levId={lev_id}: raw_json must re-parse as NostrEvent: {e}"));
        assert_eq!(
            reparsed.id, decoded.event.id,
            "levId={lev_id}: re-parsed raw_json id must equal the decoded event id (D-01 exact passthrough)"
        );
    }
}

/// Full-coverage smoke: all 11 fixture levIds (1..=11) decode without error.
#[test]
fn test_all_fixture_levids_decode() {
    let (env, _tmp) = open_temp_fixture_env();

    for lev_id in 1u64..=11 {
        let raw = get_event_payload(&env, lev_id)
            .unwrap_or_else(|e| panic!("get_event_payload(levId={lev_id}) failed: {e}"));
        assert_eq!(
            raw.first().copied(),
            Some(0x00u8),
            "levId={lev_id} EventPayload must be a 0x00 raw-JSON payload"
        );
        let decoded = decode_event_payload(&raw)
            .unwrap_or_else(|e| panic!("levId={lev_id} must decode without error: {e}"));
        // Sanity: the decoded id is a non-empty hex string.
        assert!(
            !decoded.event.id.is_empty(),
            "levId={lev_id} decoded event id must not be empty"
        );
    }
}
