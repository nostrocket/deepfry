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
use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use zstd::dict::DecoderDictionary;

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

/// Maximum decompressed size for a 0x01 zstd-dictionary EventPayload (decompression-bomb guard).
///
/// Nostr events are typically < 64 KiB; strfry's default `maxEventSize` is 65536 bytes.
/// A 4 MiB ceiling is permissive enough for any legitimate event while bounding memory
/// usage against adversarial "decompression bomb" compressed payloads (T-02-05).
/// `Decompressor::decompress` returns `Err` (not UB, not panic) when the output would
/// exceed this limit — the error maps to `PayloadError::ZstdError`.
pub const MAX_EVENT_DECOMPRESSED_SIZE: usize = 4 * 1024 * 1024; // 4 MiB

/// Lazy, concurrency-safe dictionary cache keyed by `dictId` (D-09, D-10).
///
/// Holds `Arc<DecoderDictionary<'static>>` values built from `CompressionDictionary[dictId]`
/// bytes fetched via a short read txn on the first miss. Subsequent lookups for the same
/// `dictId` return the cached `Arc` without touching LMDB.
///
/// # Concurrency
///
/// Uses `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>`:
/// - `RwLock` allows concurrent reads (many GraphQL resolvers) with exclusive writes only
///   on cache misses — write-once, read-many pattern (D-10).
/// - `DecoderDictionary<'static>` is `Send + Sync` (verified: zstd docs).
/// - The dictionary bytes are loaded and `DecoderDictionary::copy` is called OUTSIDE both
///   the read txn and the write lock, so LMDB I/O never blocks other readers (anti-pattern
///   guard from Phase 2 RESEARCH Pattern 5).
///
/// # 0x00-only deployments
///
/// On a default (non-compacted) strfry deployment, all payloads are `0x00` and `get_or_load`
/// is never called — the cache is never populated and there is no zstd overhead (D-09).
pub struct DictCache {
    inner: RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>,
}

impl DictCache {
    /// Create a new, empty dictionary cache.
    pub fn new() -> Self {
        Self {
            inner: RwLock::new(HashMap::new()),
        }
    }

    /// Return the cached `Arc<DecoderDictionary>` for `dict_id`, loading it from LMDB on a miss.
    ///
    /// # Fast path (cache hit)
    /// Acquires a read lock, clones the `Arc`, and returns immediately.
    ///
    /// # Slow path (cache miss)
    /// 1. Opens a short read txn on `env`.
    /// 2. GETs the raw dictionary bytes from `CompressionDictionary[dict_id.to_ne_bytes()]`.
    /// 3. Copies the bytes to a `Vec<u8>` and drops the read txn (D-08).
    /// 4. Calls `DecoderDictionary::copy(&bytes)` OUTSIDE the txn — builds an owned `'static`
    ///    copy of the dictionary data (not experimental borrow API).
    /// 5. Acquires the write lock and inserts the new `Arc` via `entry().or_insert_with()`
    ///    (handles a concurrent racing insert without double-initialising).
    ///
    /// # Errors
    /// - `PayloadError::SubDbNotFound` if `CompressionDictionary` sub-DB is absent (0x00-only env).
    /// - `PayloadError::DictNotFound { dict_id }` if the dictId has no entry.
    /// - `PayloadError::Heed` on any underlying LMDB error.
    pub fn get_or_load(
        &self,
        dict_id: u32,
        env: &heed::Env,
    ) -> Result<Arc<DecoderDictionary<'static>>, PayloadError> {
        // Fast path: read lock.
        {
            let guard = self.inner.read().expect("DictCache RwLock poisoned");
            if let Some(dd) = guard.get(&dict_id) {
                return Ok(Arc::clone(dd));
            }
        }

        // Slow path: load raw bytes with a SHORT read txn, then drop the txn before building
        // the dictionary (anti-pattern: do NOT hold txn while calling DecoderDictionary::copy).
        let raw_dict_bytes = {
            let rtxn = env.read_txn()?;
            let dict_db = open_compression_dictionary_db(env, &rtxn)?;
            // Key is native-endian uint32 (MDB_INTEGERKEY; LE on co-located host, LMDB-03 gate).
            let key = dict_id.to_ne_bytes();
            let raw = dict_db
                .get(&rtxn, key.as_ref())?
                .ok_or(PayloadError::DictNotFound { dict_id })?;
            // Copy bytes OUT before the txn drops (D-08 — LMDB-mapped bytes tied to txn lifetime).
            let owned = raw.to_vec();
            drop(rtxn); // explicit: txn must drop BEFORE DecoderDictionary::copy below
            owned
        };

        // Build the DecoderDictionary OUTSIDE the read txn and OUTSIDE the write lock.
        // `DecoderDictionary::copy` makes an owned 'static copy — safe for process-lifetime cache.
        let dd = Arc::new(DecoderDictionary::copy(&raw_dict_bytes));

        // Write lock: insert via entry() to handle a racing concurrent insert gracefully.
        {
            let mut guard = self.inner.write().expect("DictCache RwLock poisoned");
            guard.entry(dict_id).or_insert_with(|| Arc::clone(&dd));
        }

        Ok(dd)
    }

    /// Test-only helper: pre-populate the cache with a pre-built dictionary entry.
    ///
    /// Allows unit tests to bypass LMDB entirely (synthetic 0x01 round-trip tests).
    #[cfg(test)]
    pub fn insert_for_test(&self, dict_id: u32, raw_dict_bytes: &[u8]) {
        let dd = Arc::new(DecoderDictionary::copy(raw_dict_bytes));
        let mut guard = self.inner.write().expect("DictCache RwLock poisoned");
        guard.insert(dict_id, dd);
    }

    /// Test-only helper: look up a dictId from the cache WITHOUT touching LMDB.
    ///
    /// Returns `Some(Arc)` on hit, `None` on miss. Used to verify cache-hit behaviour.
    #[cfg(test)]
    pub fn get_or_load_no_env(&self, dict_id: u32) -> Option<Arc<DecoderDictionary<'static>>> {
        let guard = self.inner.read().expect("DictCache RwLock poisoned");
        guard.get(&dict_id).map(Arc::clone)
    }
}

impl Default for DictCache {
    fn default() -> Self {
        Self::new()
    }
}

/// Decode a raw EventPayload value using a `DictCache` (the cache-aware entry point for Phase 3+).
///
/// Dispatches on the leading type-tag byte (`raw[0]`):
///   - `0x00` → same as [`decode_event_payload`]: strip tag byte, `serde_json::from_slice`,
///              retain `raw[1..]` as `raw_json`.
///   - `0x01` → zstd-dictionary compressed path (LMDB-08):
///              1. Guard `raw.len() >= 5` → [`PayloadError::TruncatedZstdPayload`].
///              2. Read `dict_id = u32::from_le_bytes(raw[1..5])` (LE: LMDB-03 gate).
///              3. `dict_cache.get_or_load(dict_id, env)` — lazy load from LMDB on first miss.
///              4. `Decompressor::with_prepared_dictionary`, `decompress(..., MAX_EVENT_DECOMPRESSED_SIZE)`.
///              5. `serde_json::from_slice` → `DecodedEvent`.
///   - any other byte → [`PayloadError::UnknownTypeTag`].
///
/// This function is the public surface used by scan loops in Phase 3. The `env` parameter is
/// only used on the first decode of a given `dictId` (the cache miss path); subsequent decodes
/// of the same `dictId` are cache hits and do not open a read txn.
pub fn decode_event_payload_with_cache(
    raw: &[u8],
    dict_cache: &DictCache,
    env: &heed::Env,
) -> Result<DecodedEvent, PayloadError> {
    decode_event_payload_with_cache_and_limit(raw, dict_cache, env, MAX_EVENT_DECOMPRESSED_SIZE)
}

/// Internal: like [`decode_event_payload_with_cache`] but with a configurable decompression ceiling.
///
/// Used in tests to force an over-ceiling error without allocating a 4 MiB frame.
/// The `env` parameter is passed to `DictCache::get_or_load` on a cache miss.
pub fn decode_event_payload_with_cache_and_limit(
    raw: &[u8],
    dict_cache: &DictCache,
    env: &heed::Env,
    max_decompressed: usize,
) -> Result<DecodedEvent, PayloadError> {
    let tag = match raw.first() {
        Some(&t) => t,
        None => return Err(PayloadError::UnknownTypeTag { tag: 0 }),
    };

    match tag {
        PAYLOAD_TAG_RAW_JSON => {
            let json_bytes = &raw[1..];
            let event: NostrEvent = serde_json::from_slice(json_bytes)?;
            Ok(DecodedEvent {
                event,
                raw_json: json_bytes.to_vec(),
            })
        }
        PAYLOAD_TAG_ZSTD_DICT => {
            // Guard: need at least 1 tag byte + 4 dictId bytes = 5 bytes total (T-02-06).
            if raw.len() < 5 {
                return Err(PayloadError::TruncatedZstdPayload { len: raw.len() });
            }
            // bytes[1..5] = dictId u32 little-endian (LE: LMDB-03 endianness gate asserts host=LE).
            // NOTE: raw[1..5] is 4 bytes; spec text says "[1..4]" but that is an inclusive-to typo
            // (events.cpp reads 4 bytes). Rust slices use exclusive end: [1..5] = 4 bytes = uint32.
            let dict_id = u32::from_le_bytes(
                raw[1..5].try_into().expect("slice len guaranteed above"),
            ); // LE asserted by LMDB-03 gate
            let zstd_frame = &raw[5..];

            // Lazy-load dictionary from cache (fast path on hit; opens short read txn on miss).
            let dd = dict_cache.get_or_load(dict_id, env)?;

            // One-shot decompression with the prepared dictionary.
            // `decompress` returns Err (not UB) when output would exceed `max_decompressed` (T-02-05).
            let mut decompressor =
                zstd::bulk::Decompressor::with_prepared_dictionary(&dd)
                    .map_err(PayloadError::ZstdError)?;
            let decompressed = decompressor
                .decompress(zstd_frame, max_decompressed)
                .map_err(PayloadError::ZstdError)?;

            // Parse the decompressed JSON — untrusted data (D-12), serde_json is the validator.
            let event: NostrEvent = serde_json::from_slice(&decompressed)?;
            Ok(DecodedEvent {
                event,
                // Retain exact decompressed bytes for Phase 4 passthrough (D-01).
                raw_json: decompressed,
            })
        }
        other => Err(PayloadError::UnknownTypeTag { tag: other }),
    }
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
///   - `0x01` → returns [`PayloadError::UnknownTypeTag`] — callers that need zstd-dict decode
///              must use [`decode_event_payload_with_cache`] which holds a `DictCache` reference.
///              This function intentionally does NOT accept a `DictCache` so that the Plan 01
///              call-sites and tests remain cache-free (only the 0x00 path is needed there).
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
        // 0x01 (zstd-dictionary) payloads require a DictCache reference.
        // Use decode_event_payload_with_cache instead (Plan 02-02 wires the full path).
        // This keeps the cache-less function valid for 0x00-only call-sites.
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

    // ------------------------------------------------------------------
    // Task 1 (TDD RED): DictCache tests
    // ------------------------------------------------------------------

    /// DictCache is Send + Sync (D-10 concurrency safety).
    #[test]
    fn test_dict_cache_send_sync() {
        fn assert_send_sync<T: Send + Sync>() {}
        assert_send_sync::<DictCache>();
    }

    /// get_or_load for a dictId absent from the fixture's CompressionDictionary returns
    /// Err(PayloadError::DictNotFound { dict_id }) — no panic.
    #[test]
    fn test_dict_cache_missing_dict_returns_dict_not_found() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let result = cache.get_or_load(999, &env);
        match result {
            Err(PayloadError::DictNotFound { dict_id: 999 }) => { /* expected */ }
            Err(PayloadError::SubDbNotFound { .. }) => {
                // Also acceptable: fixture has no CompressionDictionary sub-DB.
                // Both are clean DictNotFound-equivalent paths.
            }
            Err(e) => panic!("expected DictNotFound or SubDbNotFound, got Err({e})"),
            Ok(_) => panic!("expected DictNotFound or SubDbNotFound, got Ok"),
        }
    }

    /// get_or_load for a dictId pre-populated via insert_for_test returns an Arc<DecoderDictionary>
    /// on the first call; a second call returns the same Arc (cache hit — dictId found in map).
    #[test]
    fn test_dict_cache_hit_returns_cached_entry() {
        let cache = DictCache::new();
        // Use a small but valid dict bytes (any bytes — we test cache hit logic, not decompression).
        let dict_bytes = b"fake-dict-bytes-for-cache-test";
        cache.insert_for_test(42, dict_bytes);

        // First get — should hit the cache (we inserted above).
        let r1 = cache.get_or_load_no_env(42);
        assert!(r1.is_some(), "expected cache hit after insert_for_test");

        // Second get — same cache entry, same Arc pointer.
        let r2 = cache.get_or_load_no_env(42);
        assert!(r2.is_some());

        // Both arcs should point to the same allocation (Arc::ptr_eq).
        let a1 = r1.unwrap();
        let a2 = r2.unwrap();
        assert!(Arc::ptr_eq(&a1, &a2), "expected same Arc on cache hit");
    }

    // ------------------------------------------------------------------
    // Task 2 (TDD): 0x01 decode path tests
    // ------------------------------------------------------------------

    // Helper: build a synthetic 0x01 payload + return the dictionary bytes.
    // Uses from_continuous to train a real zstd dictionary from multiple samples.
    fn make_synthetic_0x01_payload(event_json: &[u8], dict_id: u32) -> (Vec<u8>, Vec<u8>) {
        use zstd::dict::from_continuous;
        let mut sample_data = Vec::new();
        let mut sample_sizes = Vec::new();
        for _ in 0..8usize {
            sample_data.extend_from_slice(event_json);
            sample_sizes.push(event_json.len());
        }
        let dict_bytes =
            from_continuous(&sample_data, &sample_sizes, 1024).expect("train dictionary");
        let mut compressor =
            zstd::bulk::Compressor::with_dictionary(3, &dict_bytes).expect("compressor");
        let compressed = compressor.compress(event_json).expect("compress");
        let mut payload = vec![PAYLOAD_TAG_ZSTD_DICT];
        payload.extend_from_slice(&dict_id.to_le_bytes()); // dictId u32 LE
        payload.extend_from_slice(&compressed);
        (payload, dict_bytes)
    }

    /// A 0x01 payload shorter than 5 bytes returns TruncatedZstdPayload (T-02-06).
    #[test]
    fn test_decode_0x01_truncated_returns_error() {
        // 4-byte payload: [0x01][3 bytes] — too short for dictId (need at least 5).
        let raw = [PAYLOAD_TAG_ZSTD_DICT, 0x00, 0x00, 0x00];
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        match decode_event_payload_with_cache(&raw, &cache, &env) {
            Err(PayloadError::TruncatedZstdPayload { len: 4 }) => { /* expected */ }
            Err(e) => panic!("expected TruncatedZstdPayload{{len:4}}, got Err({e})"),
            Ok(_) => panic!("expected TruncatedZstdPayload{{len:4}}, got Ok"),
        }
    }

    /// A 0x01 payload with an unknown dictId returns DictNotFound or SubDbNotFound (T-02-07).
    #[test]
    fn test_decode_0x01_unknown_dict_returns_dict_not_found() {
        // Build a minimal payload [0x01][dictId=1 LE][1 dummy byte] — cache is empty.
        let mut raw = vec![PAYLOAD_TAG_ZSTD_DICT];
        raw.extend_from_slice(&1u32.to_le_bytes());
        raw.push(0x00); // one byte so the truncation guard passes
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        match decode_event_payload_with_cache(&raw, &cache, &env) {
            Err(PayloadError::DictNotFound { dict_id: 1 }) => { /* expected */ }
            Err(PayloadError::SubDbNotFound { .. }) => {
                // Acceptable: fixture has no CompressionDictionary sub-DB.
            }
            Err(e) => panic!("expected DictNotFound or SubDbNotFound, got Err({e})"),
            Ok(_) => panic!("expected DictNotFound or SubDbNotFound, got Ok"),
        }
    }

    /// Synthetic 0x01 round-trip: compress event JSON with a real zstd dictionary,
    /// pre-populate a DictCache, decode via the cache-aware path, assert fields match.
    /// No LMDB CompressionDictionary required — insert_for_test bypasses LMDB (LMDB-08).
    #[test]
    fn test_decode_0x01_synthetic_round_trip() {
        let event_json = br#"{"id":"aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899","pubkey":"79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798","created_at":1700000000,"kind":1,"tags":[],"content":"hello nostr round trip","sig":"0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"}"#;

        let dict_id: u32 = 42;
        let (payload, dict_bytes) = make_synthetic_0x01_payload(event_json, dict_id);

        // Pre-populate the DictCache (bypasses LMDB — fixture env provided only for the
        // function signature; get_or_load fast-paths out without opening a read txn).
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        cache.insert_for_test(dict_id, &dict_bytes);

        // Decode and assert round-trip correctness (LMDB-08).
        let decoded = decode_event_payload_with_cache(&payload, &cache, &env)
            .expect("decode 0x01 payload must succeed");

        // Typed field assertions (LMDB-08: correctness via a typed NostrEvent field).
        assert_eq!(decoded.event.kind, 1, "decoded kind must match source");
        assert_eq!(decoded.event.created_at, 1700000000, "decoded created_at must match");
        assert_eq!(
            decoded.event.content, "hello nostr round trip",
            "decoded content must match"
        );

        // raw_json must equal the original event bytes exactly (D-01 exact passthrough).
        assert_eq!(
            decoded.raw_json,
            event_json.to_vec(),
            "raw_json must be byte-identical to the original event JSON"
        );
    }

    /// A 0x01 payload whose decompressed output exceeds the size limit returns Err
    /// (decompression-bomb guard, T-02-05). No panic, no unbounded allocation.
    #[test]
    fn test_decode_0x01_over_ceiling_returns_error() {
        let event_json = br#"{"id":"aa","pubkey":"bb","created_at":1,"kind":1,"tags":[],"content":"x","sig":"cc"}"#;
        let dict_id: u32 = 99;
        let (payload, dict_bytes) = make_synthetic_0x01_payload(event_json, dict_id);

        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        cache.insert_for_test(dict_id, &dict_bytes);

        // 1-byte ceiling — guarantees overflow for any real event JSON.
        let result = decode_event_payload_with_cache_and_limit(&payload, &cache, &env, 1);
        assert!(
            result.is_err(),
            "must return Err when decompressed size exceeds ceiling, got Ok"
        );
        // Must NOT panic (test completing without unwinding is the proof).
    }
}
