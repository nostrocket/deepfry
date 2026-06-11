//! payload.rs — Read and decode strfry `EventPayload` values.
//!
//! strfry stores every event's canonical JSON in the `rasgueadb_defaultDb__EventPayload`
//! sub-DB, keyed by `levId` (native-endian uint64, same key encoding as the `Meta` record).
//! The value is a single type-tagged blob.
//!
//! ## EventPayload value encoding (spec §3.2, strfry src/events.cpp)
//!
//! ```text
//!   byte[0] = type tag:
//!     0x00 → raw JSON event in bytes[1..]                       (default deployment)
//!     0x01 → dictId (u32 native-LE) in bytes[1..5],
//!            zstd-dictionary-compressed JSON frame in bytes[5..] (after offline compaction)
//!   key (levId) is a native-endian uint64 (MDB_INTEGERKEY, identical to the Meta key).
//! ```
//!
//! This module (Plan 02-01) implements the `0x00` path only. The `0x01` zstd-dictionary path
//! is wired by Plan 02-02 — the relevant `PayloadError` variants and the
//! `COMPRESSION_DICTIONARY_DB_NAME` open helper are defined here so the enum / API is stable.
//!
//! ## Read-only + short-txn invariants
//!
//! - Sub-DBs are opened with the read-only `open` method and never the creating variant
//!   (read-only env invariant, T-02-04).
//! - `get_event_payload` opens its own short read txn (D-08), copies the value bytes OUT with
//!   `.to_vec()` before the txn drops (heed byte slices are tied to the txn lifetime), and
//!   returns an owned `Vec<u8>`.
//! - Decoded JSON is UNTRUSTED data (D-12): malformed / unknown-tag payloads are surfaced as
//!   `Err`, never panics; the `decode_payload_skip_on_error` helper skips+warns+counts (D-11).

use crate::lmdb::types::{DecodedEvent, LevId, NostrEvent};
use heed::types::Bytes;

/// Full sub-DB name for the EventPayload store in a strfry LMDB env.
/// golpe/rasgueadb prefixes all named sub-DBs with `rasgueadb_defaultDb__` (mirrors `META_DB_NAME`).
pub const EVENT_PAYLOAD_DB_NAME: &str = "rasgueadb_defaultDb__EventPayload";

/// Full sub-DB name for the zstd dictionary store (used only by the 0x01 path, Plan 02-02).
/// Present-but-possibly-absent: default `0x00` deployments have no CompressionDictionary sub-DB.
pub const COMPRESSION_DICTIONARY_DB_NAME: &str = "rasgueadb_defaultDb__CompressionDictionary";

/// EventPayload type tag for a raw (uncompressed) JSON event.
pub const PAYLOAD_TAG_RAW_JSON: u8 = 0x00;

/// EventPayload type tag for a zstd-dictionary-compressed event (Plan 02-02 path).
pub const PAYLOAD_TAG_ZSTD_DICT: u8 = 0x01;

/// Error type for EventPayload open, lookup, and decode.
///
/// Mirrors the `MetaError` house style (`#[from] heed::Error` base + `SubDbNotFound`),
/// then adds payload-specific variants. Several variants (`TruncatedZstdPayload`,
/// `DictNotFound`, `ZstdError`, `DecompressedTooLarge`) are defined here for a stable enum
/// but are only produced by the `0x01` path wired in Plan 02-02.
#[derive(Debug, thiserror::Error)]
pub enum PayloadError {
    /// Underlying LMDB / heed error.
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    /// A required sub-DB was absent from the env.
    #[error("Sub-DB '{name}' not found in strfry env")]
    SubDbNotFound { name: &'static str },

    /// No EventPayload entry exists for the requested levId.
    #[error("levId {lev_id} not found in EventPayload sub-DB")]
    LevIdNotFound { lev_id: u64 },

    /// The leading type-tag byte is neither 0x00 nor 0x01 (T-02-02).
    #[error("EventPayload byte[0] is unknown type tag 0x{tag:02x} (expected 0x00 or 0x01)")]
    UnknownTypeTag { tag: u8 },

    /// A 0x01 payload is too short to hold the 4-byte dictId + a zstd frame (Plan 02-02).
    #[error("0x01 payload too short: {len} bytes (need at least 5 for dictId + zstd frame)")]
    TruncatedZstdPayload { len: usize },

    /// The dictId referenced by a 0x01 payload has no entry in CompressionDictionary (Plan 02-02).
    #[error("CompressionDictionary[{dict_id}] not found in strfry env")]
    DictNotFound { dict_id: u32 },

    /// libzstd reported a decompression failure (Plan 02-02). Not `#[from]` — wired explicitly there.
    #[error("zstd decompression error: {0}")]
    ZstdError(std::io::Error),

    /// A decompressed 0x01 frame exceeded the configured size limit (decompression-bomb guard, Plan 02-02).
    #[error("decompressed payload exceeds limit of {limit} bytes")]
    DecompressedTooLarge { limit: usize },

    /// `serde_json` failed to parse the JSON event body (T-02-01).
    #[error("JSON decode error: {0}")]
    JsonDecode(#[from] serde_json::Error),
}

/// Open the `EventPayload` sub-DB read-only with `IntegerComparator` (MDB_INTEGERKEY).
///
/// The levId key is a plain native-endian uint64 — identical key semantics to the Meta
/// record, so this uses the same `IntegerComparator` open chain as `read_meta` (NOT a golpe
/// custom comparator). Uses the read-only `open` method, never the creating variant
/// (read-only invariant, T-02-04).
///
/// # Errors
/// - `PayloadError::SubDbNotFound` if the sub-DB does not exist (wrong env).
/// - `PayloadError::Heed` on any underlying LMDB error.
pub fn open_event_payload_db(
    env: &heed::Env,
    rtxn: &heed::RoTxn<'_>,
) -> Result<heed::Database<Bytes, Bytes, heed::IntegerComparator>, PayloadError> {
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name(EVENT_PAYLOAD_DB_NAME)
        .open(rtxn)?
        .ok_or(PayloadError::SubDbNotFound {
            name: EVENT_PAYLOAD_DB_NAME,
        })
}

/// Open the `CompressionDictionary` sub-DB read-only with `IntegerComparator`.
///
/// Default `0x00` deployments have no dictionary sub-DB, so callers must handle
/// `PayloadError::SubDbNotFound` gracefully (it is NOT a fatal error on a 0x00-only DB).
/// Uses the read-only `open` method, never the creating variant. The dictId key is a
/// native-endian integer.
///
/// # Errors
/// - `PayloadError::SubDbNotFound` if the sub-DB does not exist (expected on 0x00-only DBs).
/// - `PayloadError::Heed` on any underlying LMDB error.
pub fn open_compression_dictionary_db(
    env: &heed::Env,
    rtxn: &heed::RoTxn<'_>,
) -> Result<heed::Database<Bytes, Bytes, heed::IntegerComparator>, PayloadError> {
    env.database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name(COMPRESSION_DICTIONARY_DB_NAME)
        .open(rtxn)?
        .ok_or(PayloadError::SubDbNotFound {
            name: COMPRESSION_DICTIONARY_DB_NAME,
        })
}

/// Fetch the raw EventPayload value bytes for a given `levId`.
///
/// Opens its own short read txn (D-08), looks up the integer key, and copies the value bytes
/// OUT with `.to_vec()` BEFORE the txn drops (heed byte slices are only valid for the txn
/// lifetime). The returned `Vec<u8>` is owned and txn-independent — it still carries the
/// leading type-tag byte; pass it to [`decode_event_payload`].
///
/// The key is `lev_id.to_ne_bytes()` — native-endian, matching MDB_INTEGERKEY. On the
/// co-located little-endian host (asserted by the LMDB-03 endianness gate) this is LE.
///
/// # Errors
/// - `PayloadError::SubDbNotFound` if the EventPayload sub-DB is absent.
/// - `PayloadError::LevIdNotFound` if no entry exists for `lev_id`.
/// - `PayloadError::Heed` on any underlying LMDB error.
pub fn get_event_payload(env: &heed::Env, lev_id: LevId) -> Result<Vec<u8>, PayloadError> {
    let rtxn = env.read_txn()?;
    let db = open_event_payload_db(env, &rtxn)?;

    // levId key = native-endian uint64 (MDB_INTEGERKEY; LE on co-located host).
    let key_bytes = lev_id.to_ne_bytes();
    let raw = db
        .get(&rtxn, key_bytes.as_ref())?
        .ok_or(PayloadError::LevIdNotFound { lev_id })?;

    // Copy the bytes OUT before the read txn drops (D-08; heed bytes tied to txn lifetime).
    let owned = raw.to_vec();
    drop(rtxn);
    Ok(owned)
}

/// Decode a raw EventPayload value into a [`DecodedEvent`] (typed struct + retained raw JSON).
///
/// Dispatches on the leading type-tag byte (`raw[0]`):
///   - `0x00` → strip the tag byte, `serde_json::from_slice(&raw[1..])` into a [`NostrEvent`],
///              and retain `raw[1..].to_vec()` as the exact `raw_json` passthrough (D-01).
///   - `0x01` → returns [`PayloadError::UnknownTypeTag`] for now — Plan 02-02 wires the zstd
///              path. (No zstd decompression is implemented in this plan.)
///   - any other byte → [`PayloadError::UnknownTypeTag`] (T-02-02).
///
/// Never panics on malformed input: an empty slice, an unknown tag, or invalid JSON all return
/// `Err` (T-02-01, T-02-02). Decoded field values are UNTRUSTED data (D-12) — no signature
/// re-verification is performed (strfry validated the event on ingest, D-04).
pub fn decode_event_payload(raw: &[u8]) -> Result<DecodedEvent, PayloadError> {
    let tag = match raw.first() {
        Some(&t) => t,
        // An empty value has no type tag — treat as an unknown (0x?? absent) tag, never panic.
        None => return Err(PayloadError::UnknownTypeTag { tag: 0 }),
    };

    match tag {
        PAYLOAD_TAG_RAW_JSON => {
            let json_bytes = &raw[1..];
            let event: NostrEvent = serde_json::from_slice(json_bytes)?;
            Ok(DecodedEvent {
                event,
                // Retain the EXACT JSON bytes (no re-serialize) for Phase 4 passthrough (D-01).
                raw_json: json_bytes.to_vec(),
            })
        }
        // Plan 02-02 wires the 0x01 zstd-dictionary path here (decompress, then serde_json).
        // Until then a 0x01 payload is treated as an unsupported tag rather than silently
        // mis-decoded.
        PAYLOAD_TAG_ZSTD_DICT => Err(PayloadError::UnknownTypeTag { tag }),
        other => Err(PayloadError::UnknownTypeTag { tag: other }),
    }
}

/// Decode wrapper implementing the skip+warn+count policy (D-11).
///
/// On success returns `Some(DecodedEvent)`. On any decode error it logs a structured
/// `tracing::warn!`, increments `skip_count`, and returns `None` — it NEVER panics. Phase 3
/// scan loops use this so a single corrupt payload does not abort a whole query.
pub fn decode_payload_skip_on_error(
    raw: &[u8],
    lev_id: LevId,
    skip_count: &mut usize,
) -> Option<DecodedEvent> {
    match decode_event_payload(raw) {
        Ok(decoded) => Some(decoded),
        Err(e) => {
            tracing::warn!(lev_id, reason = %e, "skipping undecodable EventPayload");
            *skip_count += 1;
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Copy the committed fixture to a temporary directory and open an env there.
    /// Required because LMDB cannot open the same path twice in the same process
    /// (even read-only). Each test gets its own copy to allow parallel execution.
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// open_event_payload_db succeeds on the committed fixture (the sub-DB exists).
    #[test]
    fn test_open_event_payload_db_succeeds_on_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        let rtxn = env.read_txn().expect("read_txn");
        open_event_payload_db(&env, &rtxn).expect("EventPayload sub-DB must open on fixture");
    }

    /// open_compression_dictionary_db either succeeds OR returns SubDbNotFound cleanly on the
    /// 0x00-only fixture — it must NEVER panic either way.
    #[test]
    fn test_open_compression_dictionary_db_no_panic_on_fixture() {
        let (env, _tmp) = open_temp_fixture_env();
        let rtxn = env.read_txn().expect("read_txn");
        match open_compression_dictionary_db(&env, &rtxn) {
            Ok(_) => { /* present — fine */ }
            Err(PayloadError::SubDbNotFound { name }) => {
                assert_eq!(name, COMPRESSION_DICTIONARY_DB_NAME);
            }
            Err(e) => panic!("unexpected error opening CompressionDictionary: {e}"),
        }
    }

    /// decode_event_payload on `[0x00] + valid-json` returns a DecodedEvent whose raw_json is
    /// exactly the bytes after the tag, and whose typed fields match.
    #[test]
    fn test_decode_0x00_valid_json() {
        let json = br#"{"id":"abc","pubkey":"def","created_at":42,"kind":1,"tags":[["e","x"]],"content":"hi","sig":"ff"}"#;
        let mut raw = vec![PAYLOAD_TAG_RAW_JSON];
        raw.extend_from_slice(json);

        let decoded = decode_event_payload(&raw).expect("0x00 valid JSON must decode");
        assert_eq!(decoded.event.id, "abc");
        assert_eq!(decoded.event.created_at, 42);
        assert_eq!(decoded.event.kind, 1);
        assert_eq!(decoded.event.content, "hi");
        assert_eq!(decoded.event.tags, vec![vec!["e".to_string(), "x".to_string()]]);
        // raw_json is exactly the bytes after the type tag (D-01 exact passthrough).
        assert_eq!(decoded.raw_json, json.to_vec());
    }

    /// decode_event_payload on an unknown leading byte returns UnknownTypeTag — never panics (T-02-02).
    #[test]
    fn test_decode_unknown_tag_errors() {
        let raw = [0xFFu8, 0x01, 0x02, 0x03];
        match decode_event_payload(&raw) {
            Err(PayloadError::UnknownTypeTag { tag }) => assert_eq!(tag, 0xFF),
            other => panic!("expected UnknownTypeTag, got {other:?}"),
        }
    }

    /// decode_event_payload on `[0x00] + invalid-json` returns JsonDecode — never panics (T-02-01).
    #[test]
    fn test_decode_0x00_invalid_json_errors() {
        let mut raw = vec![PAYLOAD_TAG_RAW_JSON];
        raw.extend_from_slice(b"{ this is not valid json ");
        match decode_event_payload(&raw) {
            Err(PayloadError::JsonDecode(_)) => { /* expected */ }
            other => panic!("expected JsonDecode error, got {other:?}"),
        }
    }

    /// An empty value (no type tag) returns an error rather than panicking.
    #[test]
    fn test_decode_empty_value_errors() {
        match decode_event_payload(&[]) {
            Err(PayloadError::UnknownTypeTag { .. }) => { /* expected */ }
            other => panic!("expected UnknownTypeTag for empty value, got {other:?}"),
        }
    }

    /// decode_payload_skip_on_error increments the skip count and returns None on bad input (D-11).
    #[test]
    fn test_decode_skip_on_error_counts() {
        let mut skip_count = 0usize;
        let bad = [0xFFu8];
        let out = decode_payload_skip_on_error(&bad, 7, &mut skip_count);
        assert!(out.is_none());
        assert_eq!(skip_count, 1);

        // A good payload returns Some and does NOT increment the count.
        let json = br#"{"id":"a","pubkey":"b","created_at":1,"kind":1,"tags":[],"content":"","sig":"c"}"#;
        let mut good = vec![PAYLOAD_TAG_RAW_JSON];
        good.extend_from_slice(json);
        let out = decode_payload_skip_on_error(&good, 8, &mut skip_count);
        assert!(out.is_some());
        assert_eq!(skip_count, 1, "skip_count must not increment on success");
    }
}
