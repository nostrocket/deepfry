# Phase 3: Query Engine - Pattern Map

**Mapped:** 2026-06-11
**Files analyzed:** 7 new/modified files
**Analogs found:** 7 / 7

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/query/mod.rs` | config | — | `src/lmdb/mod.rs` | exact — same module declaration file pattern |
| `src/query/filter.rs` | model | transform | `src/lmdb/types.rs` | role-match — data struct + thiserror enum; same derive conventions |
| `src/query/router.rs` | service | request-response | `src/lmdb/scan.rs` (dispatch block) | exact — same match-dispatch-by-name, IndexError reuse, per-call env pattern |
| `src/query/merge.rs` | service | streaming | `src/lmdb/scan.rs` (collect_bounded + windowed loop) | exact — same `(Vec<u8>, LevId)` input, `ScanDirection::Reverse`, heap merge on those pairs |
| `src/query/hydrate.rs` | service | request-response | `src/lmdb/payload.rs` (`get_event_payload` + `decode_payload_skip_on_error`) | exact — same get+decode+skip pattern; wraps existing Phase-2 functions |
| `src/query/engine.rs` | service | streaming | `src/lmdb/scan.rs` (`scan_index_windowed` loop) | role-match — same over-fetch loop structure, same short-txn-per-batch pattern, same `tracing::warn!` skip |
| `src/lib.rs` | config | — | `src/lib.rs` (extend) | exact — add `pub mod query;` alongside `pub mod lmdb;` |

---

## Pattern Assignments

### `src/query/mod.rs` (config)

**Analog:** `src/lmdb/mod.rs` (lines 1–8)

**Imports/declarations pattern:**
```rust
pub mod comparators;
pub mod env;
pub mod indexes;
pub mod meta;
pub mod payload;
pub mod scan;
pub mod self_check;
pub mod types;
```
Add a `query` module following the same pattern — one `pub mod` line per submodule, alphabetical order. New module declarations for Phase 3:
```rust
pub mod engine;
pub mod filter;
pub mod hydrate;
pub mod merge;
pub mod router;
```

---

### `src/query/filter.rs` (model, transform)

**Analog:** `src/lmdb/types.rs`

**Struct documentation style** (`src/lmdb/types.rs` lines 68–86):
```rust
/// Decoded Nostr event struct — deserialized from an `EventPayload` value (spec §3.2, D-01..D-04).
///
/// Deserialization is LENIENT: the 7 known fields are required and typed; unknown top-level
/// fields are silently ignored (the serde default — the strict unknown-field guard is
/// deliberately absent here). ...
#[derive(Debug, Clone, serde::Deserialize)]
pub struct NostrEvent {
```
Mirror this verbosity and spec-citation style for `NostrFilter`. Same `#[derive(Debug, Clone)]`. No `serde::Deserialize` needed on filter types — they are constructed by Phase-4 resolvers, not parsed from LMDB.

**Data struct pattern** (`src/lmdb/types.rs` lines 51–66):
```rust
#[derive(Debug, Clone)]
pub struct MetaRecord {
    pub db_version: u32,
    pub endianness: u32,
    pub negentropy_mod_counter: u64,
}
```
New types to define in `filter.rs`:
```rust
/// NIP-01 REQ filter — the engine's input contract.
///
/// All fields are `Option` because every filter predicate is optional; an all-`None`
/// filter is valid (triggers the default reverse `Event__created_at` walk, D-04).
/// `since`/`until` are pushed into scan bounds (D-03); the rest become residual predicates.
///
/// Constructed by Phase-4 GraphQL resolvers — NOT deserialized from LMDB bytes.
#[derive(Debug, Clone, Default)]
pub struct NostrFilter {
    /// Event ids to match (hex strings). Routes to `Event__id` (D-02).
    pub ids: Option<Vec<String>>,
    /// Author pubkeys to match. Routes to `Event__pubkey` or `Event__pubkeyKind` (D-02).
    pub authors: Option<Vec<String>>,
    /// Event kinds to match. Routes to `Event__kind` or `Event__pubkeyKind` (D-02).
    pub kinds: Option<Vec<u64>>,
    /// Tag predicates — e.g. `#e` / `#p`. Routes to `Event__tag` (D-02, QRY-02).
    pub tags: Option<Vec<TagFilter>>,
    /// Include events at or after this Unix timestamp (pushed into scan bounds, D-03).
    pub since: Option<u64>,
    /// Include events at or before this Unix timestamp (pushed into scan bounds, D-03).
    pub until: Option<u64>,
    /// Maximum number of events to return. `0` means unbounded (D-04 engine side).
    pub limit: usize,
}

/// A single tag predicate: `#<tag_name>` with one or more values.
/// e.g. `#e` → `TagFilter { name: "e", values: ["deadbeef..."] }`.
#[derive(Debug, Clone)]
pub struct TagFilter {
    /// Single character tag name (e.g. "e", "p").
    pub name: String,
    /// One or more values to match against `tags[i][1]`.
    pub values: Vec<String>,
}

/// Opaque pagination cursor — encoding of the last emitted `(created_at, lev_id)` pair (D-11).
///
/// Encoding: `base64(created_at(8 LE) ‖ lev_id(8 LE))` — 16 raw bytes, 24 base64 chars.
/// Consumers treat this as a blob and never inspect the internals. The engine decodes it
/// to construct `start_key` bytes for the next page's scan (Phase-2 `Bound::Excluded` resume).
///
/// NOT a GraphQL type — Phase 4 wraps this in a Connection cursor type.
#[derive(Debug, Clone)]
pub struct PageCursor {
    pub created_at: u64,
    pub lev_id: u64,
}
```

**Error type pattern** (`src/lmdb/indexes.rs` lines 62–69):
```rust
#[derive(Debug, thiserror::Error)]
pub enum IndexError {
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    #[error("Sub-DB '{name}' not found in strfry env — is this the right LMDB directory?")]
    SubDbNotFound { name: String },
}
```
Engine error enum follows the same `thiserror` house style — `#[from]` for upstream error kinds, named-field struct variants for context:
```rust
#[derive(Debug, thiserror::Error)]
pub enum QueryError {
    /// Underlying LMDB / heed error propagated from scan or hydrate.
    #[error("LMDB error: {0}")]
    Lmdb(#[from] crate::lmdb::indexes::IndexError),

    /// Payload decode / hydration error propagated from payload.rs.
    #[error("Payload error: {0}")]
    Payload(#[from] crate::lmdb::payload::PayloadError),

    /// Cursor bytes could not be decoded (malformed base64 or wrong length).
    #[error("Cursor decode error: {reason}")]
    CursorDecode { reason: String },
}
```

---

### `src/query/router.rs` (service, request-response)

**Analog:** `src/lmdb/scan.rs` (lines 104–144) and `src/lmdb/indexes.rs` (lines 157–185)

**Imports pattern** (`src/lmdb/scan.rs` lines 37–43):
```rust
use crate::lmdb::indexes::{
    open_index_created_at, open_index_string_uint64, open_index_string_uint64_uint64,
    open_index_uint64_uint64, IndexError,
};
use crate::lmdb::types::LevId;
use heed::types::Bytes;
use std::ops::Bound;
```
Router needs:
```rust
use crate::lmdb::indexes::IndexError;
use crate::lmdb::scan::{scan_index_bounded, ScanDirection};
use crate::lmdb::types::LevId;
use crate::query::filter::NostrFilter;
```

**Match-dispatch-by-index-name pattern** (`src/lmdb/scan.rs` lines 118–140):
```rust
let results = match short_name {
    "Event__id" | "Event__pubkey" | "Event__tag" => {
        let db = open_index_string_uint64(env, &rtxn, short_name)?;
        collect_bounded(&db, &rtxn, direction, start_key, limit)?
    }
    "Event__kind" => {
        let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
        collect_bounded(&db, &rtxn, direction, start_key, limit)?
    }
    "Event__pubkeyKind" => {
        let db = open_index_string_uint64_uint64(env, &rtxn, short_name)?;
        collect_bounded(&db, &rtxn, direction, start_key, limit)?
    }
    "Event__created_at" => {
        let db = open_index_created_at(env, &rtxn)?;
        collect_bounded(&db, &rtxn, direction, start_key, limit)?
    }
    _ => {
        return Err(IndexError::SubDbNotFound {
            name: short_name.to_string(),
        })
    }
};
```
The router's index-selection logic follows the same "one arm per index" pattern, but the dispatch is driven by `NostrFilter` fields (D-02 priority order) rather than a `short_name` string:
```rust
/// Select the most selective applicable index for `filter` (D-02 fixed priority order).
/// Returns `(index_short_name, Vec<start_key_per_prefix>)`.
/// The caller (merge.rs) issues one `scan_index_bounded` per start_key.
pub fn select_index(filter: &NostrFilter) -> SelectedIndex {
    if filter.ids.is_some() {
        SelectedIndex::Single("Event__id")
    } else if filter.authors.is_some() && filter.kinds.is_some() {
        SelectedIndex::Multi("Event__pubkeyKind")  // fan-out per (author, kind) pair
    } else if filter.authors.is_some() {
        SelectedIndex::Multi("Event__pubkey")       // fan-out per author
    } else if filter.kinds.is_some() {
        SelectedIndex::Multi("Event__kind")         // fan-out per kind
    } else if filter.tags.is_some() {
        SelectedIndex::Multi("Event__tag")          // QRY-02 tag scan
    } else {
        SelectedIndex::Single("Event__created_at")  // D-04: default global feed
    }
}
```

**Key construction pattern** (key-format table from `src/lmdb/indexes.rs` lines 1–12 comments):
The router builds composite `start_key` bytes using the same LE encoding as the scan tests in `scan.rs` (lines 439–452):
```rust
// From scan.rs test helpers — the pattern for building Event__kind start keys:
fn kind_forward_low_key() -> Vec<u8> {
    let mut k = Vec::with_capacity(16);
    k.extend_from_slice(&0u64.to_le_bytes()); // kind=0
    k.extend_from_slice(&0u64.to_le_bytes()); // ts=0
    k
}

fn kind_reverse_high_key() -> Vec<u8> {
    let mut k = Vec::with_capacity(16);
    k.extend_from_slice(&u64::MAX.to_le_bytes()); // kind=max
    k.extend_from_slice(&u64::MAX.to_le_bytes()); // ts=max
    k
}
```
The router applies the same pattern for each index:
- `Event__id` / `Event__pubkey` / `Event__tag`: `String(32 or var) ‖ created_at(8 LE)`
- `Event__kind`: `kind(8 LE) ‖ created_at(8 LE)`
- `Event__pubkeyKind`: `pubkey(32) ‖ kind(8 LE) ‖ created_at(8 LE)`
- `Event__created_at`: plain `created_at(8 LE)` (MDB_INTEGERKEY — same `.to_le_bytes()`)

`since`/`until` are pushed into the `start_key` as the trailing `created_at` bytes (D-03): for a reverse scan the `until` value (or `u64::MAX`) becomes the upper-bound key's timestamp; for a forward scan the `since` value (or `0`) becomes the lower-bound.

**Test pattern** (`src/lmdb/scan.rs` lines 423–431):
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
Copy verbatim into `router.rs` `#[cfg(test)]` block. All engine module unit tests use this same helper.

---

### `src/query/merge.rs` (service, streaming)

**Analog:** `src/lmdb/scan.rs` (lines 182–241 — `scan_index_windowed` loop + `collect_bounded`)

**Core loop pattern** (`src/lmdb/scan.rs` lines 196–240):
```rust
loop {
    let rtxn = env.read_txn()?;

    let batch = match short_name {
        "Event__kind" => {
            let db = open_index_uint64_uint64(env, &rtxn, short_name)?;
            collect_window(&db, &rtxn, direction, &resume_key, first_batch, window_size)?
        }
        // ... other arms ...
        _ => { return Err(...) }
    };

    // Drop txn BEFORE accumulating results (D-08: no txn held across window boundary).
    drop(rtxn);
    first_batch = false;

    if batch.is_empty() {
        break;
    }

    let (last_key, _last_lev_id) = batch.last().unwrap();
    resume_key = last_key.clone();

    all_results.extend(batch);
}
```
The k-way merge (D-05) follows the same per-prefix-scan + accumulate pattern, replacing `all_results.extend(batch)` with a heap-push of `(created_at, lev_id)` pairs extracted from the returned key bytes:

```rust
/// A candidate entry in the k-way merge heap.
/// Ordered by (created_at DESC, lev_id DESC) — D-10 total order.
#[derive(Eq, PartialEq)]
struct MergeCandidate {
    created_at: u64,
    lev_id: LevId,
    key_bytes: Vec<u8>,
    stream_idx: usize,  // which per-prefix stream this came from
}

impl Ord for MergeCandidate {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        // BinaryHeap is a max-heap; we want newest-first, so larger (created_at, lev_id) wins.
        self.created_at.cmp(&other.created_at)
            .then(self.lev_id.cmp(&other.lev_id))
    }
}
impl PartialOrd for MergeCandidate { fn partial_cmp(&self, o: &Self) -> Option<std::cmp::Ordering> { Some(self.cmp(o)) } }
```

**`created_at` extraction from key bytes** — the router/merge modules extract `created_at` from the trailing 8 bytes of each returned key WITHOUT hydrating:
```rust
// From the key-format table (indexes.rs lines 1-12):
// All Event__* keys end with created_at(8 LE). Extract without decode.
fn created_at_from_key(key: &[u8]) -> u64 {
    // last 8 bytes are created_at (LE u64)
    let offset = key.len().saturating_sub(8);
    u64::from_le_bytes(key[offset..offset + 8].try_into().unwrap_or([0u8; 8]))
}
```

**Per-call short txn pattern** (`src/lmdb/scan.rs` lines 116–143):
```rust
pub fn scan_index_bounded(
    env: &heed::Env,      // NOT &RoTxn — structural short-txn guarantee (D-08)
    ...
) -> Result<..., IndexError> {
    let rtxn = env.read_txn()?;
    // ... use rtxn ...
    Ok(results)
    // rtxn dropped here — short-lived, per-call (LMDB-09 / spec §6.4)
}
```
`merge.rs` calls `scan_index_bounded` which already owns its txn. The merge loop itself must not hold a txn across calls — it calls `scan_index_bounded` (which owns its own txn) rather than opening one around the loop.

**DUPSORT + `move_through_duplicate_values` pattern** (`src/lmdb/scan.rs` lines 272–285):
```rust
let range = (Bound::Included(start_key), Bound::Unbounded);
// MUST call .move_through_duplicate_values() — DUPSORT default skips duplicates
let iter = db.range(rtxn, &range)?.move_through_duplicate_values();
for item in iter.take(limit) {
    let (key, value) = item?;
    if value.len() < 8 {
        tracing::warn!(
            value_len = value.len(),
            "Event__* index VALUE shorter than 8 bytes in forward scan — skipping (T-02-11)"
        );
        continue;
    }
    let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
    results.push((key.to_vec(), lev_id));
}
```
Merge calls `scan_index_bounded` which already handles DUPSORT correctly. Merge does NOT open its own LMDB iterators — it composes `scan_index_bounded` and re-uses its output.

---

### `src/query/hydrate.rs` (service, request-response)

**Analog:** `src/lmdb/payload.rs` (lines 370–443)

**Core get+decode pattern** (`src/lmdb/payload.rs` lines 370–384 + 425–443):
```rust
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
```
`hydrate.rs` wraps these into a single hydrate-batch function. The over-fetch loop in `engine.rs` calls this; the pattern is: for each `lev_id` in a batch, call `get_event_payload` then `decode_event_payload_with_cache`, using `decode_payload_skip_on_error` semantics (skip+warn+count) so one bad payload never aborts a query (D-11):

```rust
/// Hydrate a slice of levIds into DecodedEvents, skipping corrupt payloads (D-06/D-11).
///
/// Opens one short read txn per levId via `get_event_payload` (D-08).
/// On decode failure: logs `tracing::warn!(lev_id, reason = %e, ...)`, increments `skip_count`,
/// skips the entry — consistent with `decode_payload_skip_on_error` in payload.rs.
pub fn hydrate_lev_ids(
    env: &heed::Env,
    lev_ids: &[LevId],
    dict_cache: &DictCache,
    skip_count: &mut usize,
) -> Result<Vec<DecodedEvent>, QueryError> {
    let mut results = Vec::with_capacity(lev_ids.len());
    for &lev_id in lev_ids {
        let raw = get_event_payload(env, lev_id)?;
        match decode_event_payload_with_cache(&raw, dict_cache, env) {
            Ok(decoded) => results.push(decoded),
            Err(e) => {
                tracing::warn!(lev_id, reason = %e, "skipping undecodable EventPayload in hydrate batch");
                *skip_count += 1;
            }
        }
    }
    Ok(results)
}
```

**Imports pattern** (`src/lmdb/payload.rs` lines 31–35):
```rust
use crate::lmdb::types::{DecodedEvent, LevId, NostrEvent};
use heed::types::Bytes;
use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use zstd::dict::DecoderDictionary;
```
`hydrate.rs` imports:
```rust
use crate::lmdb::payload::{decode_event_payload_with_cache, get_event_payload, DictCache};
use crate::lmdb::types::{DecodedEvent, LevId};
use crate::query::filter::QueryError;
```

---

### `src/query/engine.rs` (service, streaming)

**Analog:** `src/lmdb/scan.rs` (`scan_index_windowed` lines 182–241) + `src/lmdb/payload.rs` (`decode_payload_skip_on_error` lines 430–443)

**Over-fetch + backfill loop pattern** (D-07) — the same structure as `scan_index_windowed`'s window loop, but the termination condition is "collected enough valid events" rather than "exhausted index":

From `scan_index_windowed` (lines 196–240):
```rust
loop {
    let rtxn = env.read_txn()?;
    let batch = /* scan one window */;
    drop(rtxn);   // D-08: drop BEFORE accumulating

    if batch.is_empty() { break; }

    let (last_key, _) = batch.last().unwrap();
    resume_key = last_key.clone();
    all_results.extend(batch);
}
```
The engine's over-fetch loop follows the same structure, replacing `all_results.extend(batch)` with hydrate+NIP-40-filter+accumulate, and replacing `if batch.is_empty() { break; }` with `if valid.len() >= limit || batch.is_empty() { break; }`:

```rust
/// Execute a NostrFilter query returning up to `filter.limit` valid DecodedEvents (D-07).
///
/// Over-fetch + backfill pattern: pulls candidates from the merge in batches, hydrates,
/// filters expired (NIP-40 D-08), accumulates valid events. Continues until `limit` valid
/// events are collected OR all merge streams are exhausted. Returns a full N whenever N
/// valid events exist, never returning short due to expiry (D-07).
///
/// NIP-40 expiration check: `expiration != 0 && expiration <= now` where `now` is direct
/// system time (D-09 — NOT injectable; tests must use future-dated expiration values).
pub fn execute_query(
    env: &heed::Env,
    filter: &NostrFilter,
    dict_cache: &DictCache,
    cursor: Option<&PageCursor>,
) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError> {
    let over_fetch_batch = DEFAULT_WINDOW_SIZE; // reuse scan.rs constant (D-07 batch sizing)
    let mut valid: Vec<DecodedEvent> = Vec::new();
    let mut skip_count: usize = 0;
    // ... merge + hydrate + filter loop ...
}
```

**NIP-40 expiration predicate** (D-08) — extracted from `NostrEvent.tags` (already `Vec<Vec<String>>`, Phase-2 D-03):
```rust
/// True if the event has expired per NIP-40.
/// tags[i] = ["expiration", "<unix_timestamp>"] — check tags[i][0] == "expiration".
/// `now` is direct system time (D-09: not injectable).
fn is_expired(event: &NostrEvent) -> bool {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    for tag in &event.tags {
        if tag.len() >= 2 && tag[0] == "expiration" {
            if let Ok(exp) = tag[1].parse::<u64>() {
                if exp != 0 && exp <= now {
                    return true;
                }
            }
        }
    }
    false
}
```

**Cursor encode/decode pattern** (D-11) — `base64(created_at(8 LE) ‖ lev_id(8 LE))`:
```rust
use base64::{engine::general_purpose::STANDARD, Engine as _};

impl PageCursor {
    /// Encode as opaque base64 blob — consumers never inspect internals (D-11).
    pub fn encode(&self) -> String {
        let mut buf = [0u8; 16];
        buf[0..8].copy_from_slice(&self.created_at.to_le_bytes());
        buf[8..16].copy_from_slice(&self.lev_id.to_le_bytes());
        STANDARD.encode(buf)
    }

    /// Decode from opaque blob. Returns `QueryError::CursorDecode` on malformed input.
    pub fn decode(s: &str) -> Result<Self, QueryError> {
        let bytes = STANDARD.decode(s).map_err(|e| QueryError::CursorDecode {
            reason: e.to_string(),
        })?;
        if bytes.len() != 16 {
            return Err(QueryError::CursorDecode {
                reason: format!("expected 16 bytes, got {}", bytes.len()),
            });
        }
        Ok(PageCursor {
            created_at: u64::from_le_bytes(bytes[0..8].try_into().unwrap()),
            lev_id: u64::from_le_bytes(bytes[8..16].try_into().unwrap()),
        })
    }
}
```

**`tracing::warn!` skip-and-continue pattern** (`src/lmdb/payload.rs` lines 436–440):
```rust
Err(e) => {
    tracing::warn!(lev_id, reason = %e, "skipping undecodable EventPayload");
    *skip_count += 1;
    None
}
```
Engine extends this uniformly — same structured fields, same `%e` format for the reason, same `skip_count` counter exposed to the caller.

**`DEFAULT_WINDOW_SIZE` reuse** (`src/lmdb/scan.rs` lines 49–55):
```rust
/// Default window size for unbounded (`limit=0`) scans (D-07).
pub const DEFAULT_WINDOW_SIZE: usize = 256;
```
Import `crate::lmdb::scan::DEFAULT_WINDOW_SIZE` as the default over-fetch batch size in the engine loop (D-07 in 03-CONTEXT.md explicitly references this constant for the batch sizing recommendation).

**`latestPerAuthor` function signature** (D-12) — per-author bucketed output:
```rust
/// latestPerAuthor: return ≤ `per_author` newest events per pubkey for the given `kind`.
///
/// Uses per-author `Reverse` `Event__pubkeyKind[pubkey‖kind]` prefix scans capped at
/// `per_author`, grouped into a HashMap<pubkey, Vec<DecodedEvent>>. Distinct from
/// `execute_query` which returns a flat merged stream (D-12).
///
/// Shares the same per-prefix-reverse-scan machinery as execute_query but different
/// output shape: one bucket per pubkey, each newest-first, ≤ per_author entries.
pub fn latest_per_author(
    env: &heed::Env,
    kind: u64,
    per_author: usize,
    authors: &[String],
    dict_cache: &DictCache,
) -> Result<std::collections::HashMap<String, Vec<DecodedEvent>>, QueryError>
```

**Test pattern** (`src/lmdb/scan.rs` lines 423–431):
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
Copy verbatim into `engine.rs` `#[cfg(test)]`. NIP-40 expiration tests must use **future-dated** expiration values (or absent `expiration` tags) because `now` is not pinnable (D-09). Assert on fixture seed events using the golden vectors — fixture has 11 events across two pubkeys and multiple kinds (from `tests/fixture/golden_vectors/Event__pubkeyKind.json`):
- `79be...` has events with `kind` 1, 2, 255, 256
- `c604...` has events with `kind` 1 only
- `latestPerAuthor(kind=1, per_author=2, authors=[*])` must return 2 events per pubkey

---

### `src/lib.rs` (config) — extend existing file

**Analog:** `src/lib.rs` (current content, lines 1–8):
```rust
/// lmdb2graphql library crate
/// Exposes the lmdb module for integration tests and future GraphQL layer.
pub mod lmdb;

/// Config loader — reads ~/deepfry/lmdb2graphql.yaml.
/// Tests must use tempfile::tempdir() and call config::load_from() (CLAUDE.md).
pub mod config;
```
Add one line:
```rust
pub mod query;
```

---

## Shared Patterns

### Per-call short read transaction (D-08 / Phase-2 D-08)
**Source:** `src/lmdb/scan.rs` lines 104–109 (function signature — `env: &heed::Env` not `&RoTxn`); `src/lmdb/payload.rs` lines 370–384
**Apply to:** All public functions in `router.rs`, `hydrate.rs`, `engine.rs`
```rust
// Structural guarantee: function takes &heed::Env, not &RoTxn.
// Caller CANNOT pass a long-lived transaction in.
pub fn my_primitive(env: &heed::Env, ...) -> Result<..., ...> {
    // Either opens its own txn via env.read_txn(), or delegates to scan_index_bounded
    // which does so. Either way, no txn leaks across call boundaries.
}
```

### `.open()` never `.create()` for sub-DB opens
**Source:** `src/lmdb/indexes.rs` lines 19–22 (doc comment); `src/lmdb/payload.rs` lines 318–330
**Apply to:** Any new sub-DB open in engine code (none expected — engine uses existing open helpers)
```rust
// If any new sub-DB open is needed:
env.database_options()
    .types::<Bytes, Bytes>()
    .key_comparator::<SomeComparator>()
    .name(DB_NAME)
    .open(rtxn)?          // NOT .create()
    .ok_or(Error::SubDbNotFound { name: DB_NAME })?;
```

### tracing::warn! skip-and-continue for malformed/expired entries
**Source:** `src/lmdb/scan.rs` lines 276–283; `src/lmdb/payload.rs` lines 435–441
**Apply to:** All scan loops and hydrate calls in `merge.rs`, `hydrate.rs`, `engine.rs`
```rust
// From payload.rs — the skip-warn-count idiom:
Err(e) => {
    tracing::warn!(lev_id, reason = %e, "skipping undecodable EventPayload");
    *skip_count += 1;
    None
}

// From scan.rs — the short-value guard idiom:
if value.len() < 8 {
    tracing::warn!(
        value_len = value.len(),
        "Event__* index VALUE shorter than 8 bytes in forward scan — skipping (T-02-11)"
    );
    continue;
}
```
Use structured fields (`lev_id = lev_id`, `reason = %e`) in all new `warn!` calls.

### `move_through_duplicate_values()` on every range/rev_range
**Source:** `src/lmdb/scan.rs` lines 274, 292 (both `collect_bounded` arms)
**Apply to:** Any new `db.range()` or `db.rev_range()` call in engine code
```rust
// MUST call .move_through_duplicate_values() — DUPSORT default skips duplicates
let iter = db.range(rtxn, &range)?.move_through_duplicate_values();
// Reverse:
let iter = db.rev_range(rtxn, &range)?.move_through_duplicate_values();
```
The engine calls `scan_index_bounded` which handles this internally — engine code should not open raw range iterators itself.

### Fixture test helper (open_temp_fixture_env)
**Source:** `src/lmdb/scan.rs` lines 423–431; same in `src/lmdb/payload.rs` lines 453–460 and `src/lmdb/indexes.rs` lines 326–332
**Apply to:** All `#[cfg(test)]` blocks in every new query module
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
This is the canonical fixture setup idiom across the entire codebase. Copy verbatim every time.

### Key byte construction (LE uint64, trailing created_at)
**Source:** `src/lmdb/scan.rs` test helpers lines 439–452; index key-format table `src/lmdb/indexes.rs` lines 1–12
**Apply to:** `router.rs` (start_key construction) and `engine.rs` (cursor start_key reconstruction)
```rust
// All Event__* keys end in created_at(8 LE). Use .to_le_bytes() for all integer fields.
// Examples from scan.rs tests (authoritative key construction for each index):
let mut k = Vec::with_capacity(16);
k.extend_from_slice(&kind_value.to_le_bytes());       // kind (8 LE)
k.extend_from_slice(&created_at_value.to_le_bytes()); // created_at (8 LE)
// For Event__pubkeyKind: pubkey(32 raw bytes) ‖ kind(8 LE) ‖ created_at(8 LE)
// For Event__id / Event__pubkey: id_or_pubkey(32 raw bytes) ‖ created_at(8 LE)
// For Event__tag: tag_name(1 byte as str) ‖ tag_value(var) ‖ created_at(8 LE)
```

---

## No Analog Found

No Phase-3 files lack a codebase analog. However, two capabilities are new in this phase and have no exact prior implementation in the codebase:

| Capability | Lives In | Reason / Reference |
|------------|----------|--------------------|
| `std::collections::BinaryHeap`-based k-way merge | `src/query/merge.rs` | No heap merge exists. Use `std::collections::BinaryHeap` (stdlib — no new crate). Pattern from D-05: one `MergeCandidate` per `(key, lev_id)` pair, `Ord` impl on `(created_at DESC, lev_id DESC)`. |
| `base64` cursor encode/decode | `src/query/filter.rs` + `engine.rs` | No base64 usage exists. Add `base64 = "0.22"` to `Cargo.toml`. The `PageCursor` encode/decode pattern above is self-contained. |
| NIP-40 expiration check via `std::time::SystemTime` | `src/query/engine.rs` | No system-time usage exists yet. Direct `SystemTime::now()` — no new crate. D-09 explicitly forbids an injectable clock. |

---

## Fixture Data Reference for Tests

From `tests/fixture/seed_events.jsonl` and `tests/fixture/golden_vectors/Event__pubkeyKind.json`:

**Two pubkeys in the fixture:**
- `pk1` = `79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798` (9 events)
- `pk2` = `c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5` (2 events)

**Event__pubkeyKind ordered_lev_ids:** `[5, 6, 7, 8, 11, 1, 9, 3, 2, 4, 10]`

**Per-author kind=1 events (newest-first):**
- `pk1 kind=1`: levIds 11 (ts=1720000000), 8/7 (ts=1700000256), 6/5 (ts=1700000255) — 5 events total
- `pk2 kind=1`: levIds 10 (ts=1710000000), 4 (ts=1700000000) — 2 events total

**`latestPerAuthor(kind=1, per_author=2)` expected result:**
- `pk1`: [levId=11 (ts=1720000000), levId=8 or 7 (ts=1700000256)] — 2 newest
- `pk2`: [levId=10 (ts=1710000000), levId=4 (ts=1700000000)] — both (only 2 exist)

**NIP-40 test guidance (D-09):** None of the 11 fixture seed events carry an `expiration` tag. Integration tests for NIP-40 filtering must use synthetic payloads with future-dated expiration (e.g., year 2099) or absent expiration tags — never a past timestamp, since `now` is live system time.

---

## Metadata

**Analog search scope:** `src/lmdb/` (all 8 modules read), `src/lib.rs`, `src/lmdb/mod.rs`, `tests/fixture/seed_events.jsonl`, `tests/fixture/golden_vectors/Event__pubkeyKind.json`
**Files scanned:** 13 (scan.rs, payload.rs, types.rs, indexes.rs, env.rs, meta.rs, mod.rs, lib.rs, comparators.rs referenced; self_check_test.rs, seed_events.jsonl, Event__pubkeyKind.json, 03-CONTEXT.md, 02-CONTEXT.md, 02-PATTERNS.md)
**Pattern extraction date:** 2026-06-11
