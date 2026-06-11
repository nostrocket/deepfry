# Phase 2: Payload Decoding & Index Scan Primitives - Pattern Map

**Mapped:** 2026-06-11
**Files analyzed:** 5 new/modified files
**Analogs found:** 5 / 5

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/lmdb/payload.rs` | service | request-response | `src/lmdb/meta.rs` | exact — same IntegerComparator open, per-call read_txn, thiserror enum, byte-offset doc style |
| `src/lmdb/scan.rs` | service | CRUD / streaming | `src/lmdb/indexes.rs` | exact — same range-scan pattern, levId-from-VALUE extraction, DUPSORT iteration, warn+skip contract |
| `src/lmdb/types.rs` | model | transform | `src/lmdb/types.rs` (extend) | exact — same module; add NostrEvent + DecodedEvent alongside LevId + MetaRecord |
| `src/lmdb/mod.rs` | config | — | `src/lmdb/mod.rs` (extend) | exact — same module; add `pub mod payload; pub mod scan;` |
| `tests/payload_test.rs` | test | request-response | `tests/self_check_test.rs` | exact — same open_temp_fixture_env helper, same fixture copy + open_fixture_env pattern |

---

## Pattern Assignments

### `src/lmdb/payload.rs` (service, request-response)

**Analog:** `src/lmdb/meta.rs`

**Imports pattern** (`src/lmdb/meta.rs` lines 25–27):
```rust
use crate::lmdb::types::MetaRecord;
use heed::types::Bytes;
```
For `payload.rs`, extend to:
```rust
use crate::lmdb::types::{DecodedEvent, LevId, NostrEvent};
use heed::types::Bytes;
use std::collections::HashMap;
use std::sync::{Arc, RwLock};
```

**IntegerComparator open pattern** (`src/lmdb/meta.rs` lines 86–96):
```rust
pub fn read_meta(env: &heed::Env) -> Result<MetaRecord, MetaError> {
    let rtxn = env.read_txn()?;

    let meta_db: heed::Database<Bytes, Bytes, heed::IntegerComparator> = env
        .database_options()
        .types::<Bytes, Bytes>()
        .key_comparator::<heed::IntegerComparator>()
        .name(META_DB_NAME)
        .open(&rtxn)?
        .ok_or(MetaError::SubDbNotFound { name: META_DB_NAME })?;
```
Copy this exactly for `open_event_payload_db` and `open_compression_dictionary_db`. Replace sub-DB name constant and error variant. Use `.open(&rtxn)` NEVER `.create()`.

**Integer key GET pattern** (`src/lmdb/meta.rs` lines 98–103):
```rust
    // Meta key = record id 1, stored as native-endian uint64 (MDB_INTEGERKEY).
    let key_bytes = 1u64.to_ne_bytes();
    let meta_bytes = meta_db
        .get(&rtxn, key_bytes.as_ref())?
        .ok_or(MetaError::RecordNotFound)?;
```
For EventPayload lookup, replace `1u64` with the caller-supplied `lev_id: LevId`:
```rust
    let key_bytes = lev_id.to_ne_bytes(); // native-endian for MDB_INTEGERKEY; LE on co-located host
    let raw_payload = event_payload_db
        .get(&rtxn, key_bytes.as_ref())?
        .ok_or(PayloadError::LevIdNotFound { lev_id })?;
    // MUST copy bytes out before txn drop (D-08 short txn; heed bytes tied to txn lifetime)
    let owned: Vec<u8> = raw_payload.to_vec();
    // rtxn drops here
```

**thiserror enum pattern** (`src/lmdb/meta.rs` lines 44–74):
```rust
#[derive(Debug, thiserror::Error)]
pub enum MetaError {
    #[error("LMDB error opening Meta sub-DB: {0}")]
    Heed(#[from] heed::Error),

    #[error("Meta sub-DB not found — is this a strfry LMDB env? (looked for '{name}')")]
    SubDbNotFound { name: &'static str },

    #[error("Meta record id=1 not found in the Meta sub-DB")]
    RecordNotFound,

    #[error("Meta value too short to parse: got {len} bytes, need at least {need}")]
    ValueTooShort { len: usize, need: usize },
    // ...
```
Mirror this for `PayloadError` — same `#[from] heed::Error` base, `SubDbNotFound { name: &'static str }`, then phase-specific variants:
```rust
#[derive(Debug, thiserror::Error)]
pub enum PayloadError {
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    #[error("Sub-DB '{name}' not found in strfry env")]
    SubDbNotFound { name: &'static str },

    #[error("levId {lev_id} not found in EventPayload sub-DB")]
    LevIdNotFound { lev_id: u64 },

    #[error("EventPayload byte[0] is unknown type tag 0x{tag:02x} (expected 0x00 or 0x01)")]
    UnknownTypeTag { tag: u8 },

    #[error("0x01 payload too short: {len} bytes (need at least 5 for dictId + zstd frame)")]
    TruncatedZstdPayload { len: usize },

    #[error("CompressionDictionary[{dict_id}] not found in strfry env")]
    DictNotFound { dict_id: u32 },

    #[error("zstd decompression error: {0}")]
    ZstdError(std::io::Error),

    #[error("JSON decode error: {0}")]
    JsonDecode(#[from] serde_json::Error),
}
```

**Sub-DB name constant pattern** (`src/lmdb/meta.rs` line 41):
```rust
pub const META_DB_NAME: &str = "rasgueadb_defaultDb__Meta";
```
Replicate for payload:
```rust
pub const EVENT_PAYLOAD_DB_NAME: &str = "rasgueadb_defaultDb__EventPayload";
pub const COMPRESSION_DICTIONARY_DB_NAME: &str = "rasgueadb_defaultDb__CompressionDictionary";
```

**Byte-offset documentation style** (`src/lmdb/types.rs` lines 28–50, `src/lmdb/meta.rs` lines 1–19):
The FlatBuffer byte-offset comment block in `meta.rs` and the layout diagram in `types.rs` are the house style. Mirror it for the `0x00`/`0x01` payload encoding:
```rust
/// EventPayload value encoding (spec §3.2, verified from src/events.cpp:196-215):
///   byte[0] = type tag:
///     0x00 → raw JSON in bytes[1..]
///     0x01 → dictId (u32 native-LE) in bytes[1..5], zstd frame in bytes[5..]
///   levId (key) is native-endian uint64 (MDB_INTEGERKEY, same as Meta key).
```

**Test pattern** (`src/lmdb/meta.rs` lines 291–306):
```rust
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}
```
Copy verbatim into `payload.rs` `#[cfg(test)]` block. Every Phase 1 test module uses this same helper — Phase 2 tests do the same.

---

### `src/lmdb/scan.rs` (service, CRUD/streaming)

**Analog:** `src/lmdb/indexes.rs`

**Imports pattern** (`src/lmdb/indexes.rs` lines 29–31):
```rust
use crate::lmdb::comparators::{StringUint64Cmp, StringUint64Uint64Cmp, Uint64Uint64Cmp};
use heed::types::Bytes;
use std::ops::Bound;
```
`scan.rs` needs the same comparator imports plus `LevId`:
```rust
use crate::lmdb::comparators::{StringUint64Cmp, StringUint64Uint64Cmp, Uint64Uint64Cmp};
use crate::lmdb::indexes::{full_db_name, open_index_created_at, open_index_string_uint64,
                            open_index_string_uint64_uint64, open_index_uint64_uint64};
use crate::lmdb::types::LevId;
use heed::types::Bytes;
use std::ops::Bound;
```

**Per-call read_txn pattern** (`src/lmdb/indexes.rs` lines 157–158, 226–227):
```rust
pub fn scan_lev_ids_for_index(env: &heed::Env, short_name: &str) -> Result<Vec<u64>, IndexError> {
    let rtxn = env.read_txn()?;
    // ... use rtxn ...
    Ok(lev_ids)
    // rtxn dropped here — short-lived
}
```
```rust
pub fn seek_first_ge_lev_id(...) -> Result<Option<u64>, IndexError> {
    let rtxn = env.read_txn()?;
    // ...
    Ok(lev_id)
    // rtxn dropped here — short-lived, bounded to single MDB_SET_RANGE + first-entry read
}
```
`scan.rs` primitives must follow the same contract: `env: &heed::Env` as input (NOT a caller-supplied `&RoTxn`), open `read_txn()` inside the function, drop before return.

**DUPSORT levId-from-VALUE extraction pattern** (`src/lmdb/indexes.rs` lines 294–318):
```rust
fn collect_lev_ids_dup<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
) -> Result<Vec<u64>, IndexError>
where
    C: heed::Comparator,
{
    let mut lev_ids = Vec::new();
    let iter = db.iter(rtxn)?;
    for result in iter {
        let (_key, value) = result?;
        if value.len() < 8 {
            tracing::warn!(
                value_len = value.len(),
                "Event__* index VALUE shorter than 8 bytes — skipping (expected levId u64)"
            );
            continue;
        }
        let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
        lev_ids.push(lev_id);
    }
    Ok(lev_ids)
}
```
This is the exact pattern to reuse in `scan.rs` bounded scan loops — same `value.len() < 8` guard, same `tracing::warn!` with structured field `value_len`, same `u64::from_le_bytes(value[0..8].try_into().unwrap())`, same `continue` skip. The only change: add `.move_through_duplicate_values()` on `db.range()` / `db.rev_range()` (required for DUPSORT — see anti-patterns in RESEARCH.md). `collect_lev_ids_dup` uses `db.iter()` which already moves through duplicate values; the bounded range equivalents require the explicit call.

**Range seek pattern** (`src/lmdb/indexes.rs` lines 258–285):
```rust
fn seek_range_first_lev_id<C>(
    db: &heed::Database<Bytes, Bytes, C>,
    rtxn: &heed::RoTxn<'_>,
    lower_bound_key: &[u8],
) -> Result<Option<u64>, IndexError>
where
    C: heed::Comparator,
{
    let range = (Bound::Included(lower_bound_key), Bound::Unbounded);
    let mut iter = db.range(rtxn, &range)?;
    match iter.next() {
        None => Ok(None),
        Some(result) => {
            let (_key, value) = result?;
            if value.len() < 8 {
                tracing::warn!(
                    value_len = value.len(),
                    "seek_range_first_lev_id: VALUE shorter than 8 bytes — expected levId u64"
                );
                return Ok(None);
            }
            let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
            Ok(Some(lev_id))
        }
    }
}
```
`scan.rs` bounded forward scan extends `(Bound::Included(start_key), Bound::Unbounded)` with `.move_through_duplicate_values()` and `.take(limit)`. Bounded reverse scan uses `db.rev_range(rtxn, &(Bound::Unbounded, Bound::Included(upper_key)))` with `.move_through_duplicate_values()` and `.take(limit)`.

**Match-dispatch-by-index-name pattern** (`src/lmdb/indexes.rs` lines 160–184):
```rust
let lev_ids = match short_name {
    "Event__id" | "Event__pubkey" | "Event__tag" => {
        let db = open_index_string_uint64(env, &rtxn, short_name)?;
        collect_lev_ids_dup(&db, &rtxn)?
    }
    "Event__kind" => {
        let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
        collect_lev_ids_dup(&db, &rtxn)?
    }
    "Event__pubkeyKind" => {
        let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
        collect_lev_ids_dup(&db, &rtxn)?
    }
    "Event__created_at" => {
        let db = open_index_created_at(env, &rtxn)?;
        collect_lev_ids_dup(&db, &rtxn)?
    }
    _ => { return Err(IndexError::SubDbNotFound { name: short_name.to_string() }) }
};
```
`scan.rs` `scan_index_bounded` needs the same dispatch. The comparator-typed open helpers (`open_index_string_uint64` etc.) are already in `indexes.rs` — import and reuse them directly.

**Error type reuse** (`src/lmdb/indexes.rs` lines 62–69):
```rust
#[derive(Debug, thiserror::Error)]
pub enum IndexError {
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    #[error("Sub-DB '{name}' not found in strfry env — is this the right LMDB directory?")]
    SubDbNotFound { name: String },
}
```
`scan.rs` bounded scan functions should return `Result<_, IndexError>` and reuse this type from `indexes.rs` — no separate error enum needed for the scan layer.

**Test pattern** (`src/lmdb/indexes.rs` lines 326–332):
```rust
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}
```
Same helper in `scan.rs` `#[cfg(test)]` block. Golden vector data for result assertions is at `tests/fixture/golden_vectors/Event__kind.json` — the `ordered_lev_ids` field (`[4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]`) is the ground truth for forward scan order.

---

### `src/lmdb/types.rs` (model, transform) — extend existing file

**Analog:** `src/lmdb/types.rs` (extend in-place)

**Existing type documentation style** (`src/lmdb/types.rs` lines 6–19):
```rust
/// Internal event identifier in strfry.
///
/// `levId` ("Local EVent ID") is an auto-incrementing uint64 primary key.
/// It is monotonic (newer events get larger levIds) but NOT chronological
/// (events arrive out-of-order relative to created_at). It is the key of
/// both the `Event` and `EventPayload` sub-DBs (spec §3.4).
///
/// In every `Event__*` index sub-DB:
///   - The KEY is the composite field (e.g. `pubkey(32) ‖ created_at(8 LE)`)
///   - The VALUE is the 8-byte little-endian levId
pub type LevId = u64;
```
Add `NostrEvent` and `DecodedEvent` below `MetaRecord` with the same documentation verbosity. Reference spec section and source file in the doc comment. Do NOT add `#[serde(deny_unknown_fields)]` (D-02).

**MetaRecord struct pattern** (`src/lmdb/types.rs` lines 51–66):
```rust
#[derive(Debug, Clone)]
pub struct MetaRecord {
    pub db_version: u32,
    pub endianness: u32,
    pub negentropy_mod_counter: u64,
}
```
`NostrEvent` and `DecodedEvent` follow the same `#[derive(Debug, Clone)]` pattern. `NostrEvent` adds `#[derive(serde::Deserialize)]` for serde. No `pub` on the module itself — types are already in `pub mod types`.

New types to add:
```rust
/// Decoded Nostr event struct — deserialized from EventPayload (spec §3.2, D-01..D-04).
///
/// Deserialization is LENIENT: 7 known fields are required and typed; unknown top-level
/// fields are silently ignored (serde default — NOT deny_unknown_fields). This is
/// intentional for forward-compat with events strfry accepted that carry extra fields (D-02).
///
/// Do NOT use the `nostr` crate — use serde_json + this local struct (D-04, CLAUDE.md).
/// Signatures are NOT re-verified on the decode path (strfry already validated on ingest, D-04).
#[derive(Debug, Clone, serde::Deserialize)]
pub struct NostrEvent {
    pub id: String,
    pub pubkey: String,
    pub created_at: u64,
    pub kind: u64,
    pub tags: Vec<Vec<String>>,  // D-03: typed for Phase 3 tag scans and NIP-40 expiration
    pub content: String,
    pub sig: String,
    // No #[serde(deny_unknown_fields)] — D-02: lenient, forward-compat
}

/// Output of a single EventPayload decode — both typed struct AND retained raw bytes (D-01).
///
/// The struct gives Phase 3 typed field access (filter routing, latestPerAuthor, NIP-40).
/// The raw bytes give Phase 4 an exact passthrough field without re-serializing.
/// One decode produces both — no double parse.
///
/// raw_json is a Vec<u8> (owned) because heed LMDB bytes are only valid for the txn lifetime;
/// the payload decoder copies bytes out before dropping the read txn (D-08).
#[derive(Debug, Clone)]
pub struct DecodedEvent {
    pub event: NostrEvent,
    pub raw_json: Vec<u8>,  // exact JSON bytes for Phase 4 passthrough (no re-serialize)
}
```

---

### `src/lmdb/mod.rs` (config) — extend existing file

**Analog:** `src/lmdb/mod.rs` (current content, lines 1–6):
```rust
pub mod comparators;
pub mod env;
pub mod indexes;
pub mod meta;
pub mod self_check;
pub mod types;
```
Add two lines in alphabetical order:
```rust
pub mod payload;
pub mod scan;
```

---

### `tests/payload_test.rs` (test, request-response)

**Analog:** `tests/self_check_test.rs`

**File header and imports pattern** (`tests/self_check_test.rs` lines 1–19):
```rust
/// self_check_test.rs — Integration tests for the comparator self-check gate.
///
/// Tests:
/// (a) run_comparator_self_check returns Ok(()) on the unmodified committed fixture
/// ...
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::self_check::{run_comparator_self_check, GoldenVectors, SelfCheckError};
```
Mirror for `payload_test.rs`:
```rust
/// payload_test.rs — Integration tests for EventPayload decode and scan primitives.
///
/// Tests:
/// (a) decode_event_payload returns DecodedEvent for each 0x00 seed event in the fixture
/// (b) scan_index_bounded forward on Event__kind returns first N levIds from golden vector
/// (c) scan_index_bounded reverse on Event__kind returns last N levIds in reverse order
/// (d) windowed scan (limit=0) returns all levIds matching the full golden vector
use lmdb2graphql::lmdb::env::open_fixture_env;
use lmdb2graphql::lmdb::payload::{decode_event_payload, DictCache};
use lmdb2graphql::lmdb::scan::{scan_index_bounded, ScanDirection};
```

**open_temp_fixture_env helper** (`tests/self_check_test.rs` lines 22–29):
```rust
fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
    let src = std::path::Path::new("tests/fixture");
    let tmp = tempfile::tempdir().expect("create tempdir");
    std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
    std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
    let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
    (env, tmp)
}
```
Copy verbatim. This pattern appears in `meta.rs`, `indexes.rs`, and `self_check_test.rs` — it is the established fixture setup idiom.

**Test structure pattern** (`tests/self_check_test.rs` lines 62–80):
```rust
#[test]
fn test_self_check_passes_on_fixture() {
    let (env, _tmp) = open_temp_fixture_env();
    let golden = GoldenVectors::load_committed().expect("load committed golden vectors");
    let result = run_comparator_self_check(&env, &golden);
    if let Err(ref e) = result {
        eprintln!("Self-check unexpectedly failed: {e}");
        // ...
    }
    assert!(
        result.is_ok(),
        "run_comparator_self_check must return Ok(()) on the committed fixture: {:?}",
        result.err()
    );
}
```
Same shape: open fixture, call primitive, assert with informative message including the error value on failure.

**Golden vector data for scan assertions** (from `tests/fixture/golden_vectors/Event__kind.json`):
```json
"ordered_lev_ids": [4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]
```
Forward scan limit=3 on `Event__kind` must return `[4, 5, 6]`. Reverse scan limit=3 must return `[2, 3, 9]`. Use `include_str!` or `serde_json::from_str` to load golden vectors in test assertions (same approach as `GoldenVectors::load_committed` in `self_check.rs`).

---

## Shared Patterns

### Per-call short read transaction (D-08)
**Source:** `src/lmdb/indexes.rs` lines 157–158 and 226–227; `src/lmdb/meta.rs` lines 85–86
**Apply to:** All functions in `payload.rs` and `scan.rs`
```rust
// Pattern: fn takes &heed::Env (NOT &RoTxn), opens txn inside, drops before return.
pub fn my_primitive(env: &heed::Env, ...) -> Result<..., ...> {
    let rtxn = env.read_txn()?;
    // ... use rtxn ...
    Ok(result)
    // rtxn dropped here — per-call short txn (D-08 / spec §6.4)
}
```

### `.open()` never `.create()` for sub-DB opens
**Source:** `src/lmdb/indexes.rs` lines 19–22 (doc comment), lines 87–92 (call site)
**Apply to:** All sub-DB open sites in `payload.rs` and `scan.rs`
```rust
// CRITICAL: .open() returns Ok(None) for non-existent sub-DB.
// .create() would set MDB_CREATE — catastrophic for a read-only consumer.
env.database_options()
    .types::<Bytes, Bytes>()
    .key_comparator::<heed::IntegerComparator>()
    .name(EVENT_PAYLOAD_DB_NAME)
    .open(&rtxn)?                                // NOT .create()
    .ok_or(PayloadError::SubDbNotFound { name: EVENT_PAYLOAD_DB_NAME })?;
```

### RASGUEADB_PREFIX for sub-DB names
**Source:** `src/lmdb/indexes.rs` lines 49–55
```rust
pub const RASGUEADB_PREFIX: &str = "rasgueadb_defaultDb__";

pub fn full_db_name(short_name: &str) -> String {
    format!("{RASGUEADB_PREFIX}{short_name}")
}
```
**Apply to:** `payload.rs` — use `full_db_name("EventPayload")` and `full_db_name("CompressionDictionary")`, OR define constants directly as `"rasgueadb_defaultDb__EventPayload"` (same as `META_DB_NAME` pattern in `meta.rs`).

### tracing::warn! skip-and-continue for malformed values (D-11)
**Source:** `src/lmdb/indexes.rs` lines 304–313
```rust
if value.len() < 8 {
    tracing::warn!(
        value_len = value.len(),
        "Event__* index VALUE shorter than 8 bytes — skipping (expected levId u64)"
    );
    continue;
}
```
**Apply to:** All scan loops in `scan.rs`; payload decode error handling in `payload.rs` (skip-and-count pattern). Use structured fields (`lev_id = lev_id`, `reason = %e`) in the warn! call.

### native-endian integer key bytes for MDB_INTEGERKEY
**Source:** `src/lmdb/meta.rs` lines 98–100
```rust
// Meta key = record id 1, stored as native-endian uint64 (MDB_INTEGERKEY).
// On a little-endian host: 1u64 = bytes [0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]
let key_bytes = 1u64.to_ne_bytes();
```
**Apply to:** All integer key lookups in `payload.rs` (levId as u64, dictId as u32). Comment should reference the endianness gate: `// .to_ne_bytes(): LE on co-located host (asserted by LMDB-03 gate)`.

### Heed range tuple syntax for Bytes-keyed DBs
**Source:** `src/lmdb/indexes.rs` lines 265–269
```rust
// Use (Bound::Included(&[u8]), Bound::Unbounded) — the pattern for Bytes-keyed
// databases in heed 0.22 (see heed cookbook "Use Bytes as Cursor Ranges").
let range = (Bound::Included(lower_bound_key), Bound::Unbounded);
let mut iter = db.range(rtxn, &range)?;
```
**Apply to:** All `db.range()` and `db.rev_range()` calls in `scan.rs`. Reverse uses `(Bound::Unbounded, Bound::Included(upper_key))`. Resume windows use `Bound::Excluded(last_key)`.

---

## No Analog Found

All Phase 2 files have close analogs. However, two new capabilities have no existing codebase precedent:

| Capability | Lives In | Reason |
|------------|----------|--------|
| `DictCache` (`RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>`) | `src/lmdb/payload.rs` | No existing concurrent cache in the codebase. Use RESEARCH.md Pattern 5 exactly. |
| `ScanDirection` enum + windowing loop (`limit = 0`) | `src/lmdb/scan.rs` | No reverse scan or windowing loop exists yet. Use RESEARCH.md Patterns 7–8 exactly. |
| `zstd::bulk::Decompressor` call | `src/lmdb/payload.rs` | `zstd` crate not yet in Cargo.toml. Add `zstd = "0.13.3"` first (RESEARCH.md Pitfall 7). |

---

## Metadata

**Analog search scope:** `src/lmdb/` (all 6 existing modules read), `tests/` (2 integration test files read)
**Files scanned:** 10 (indexes.rs, env.rs, types.rs, meta.rs, mod.rs, comparators.rs referenced; self_check_test.rs, comparator_hook_smoke.rs, seed_events.jsonl, Event__kind.json)
**Pattern extraction date:** 2026-06-11
