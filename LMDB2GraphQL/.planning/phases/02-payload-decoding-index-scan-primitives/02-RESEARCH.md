# Phase 2: Payload Decoding & Index Scan Primitives — Research

**Researched:** 2026-06-11
**Domain:** Rust — zstd dictionary decompression, heed 0.22.1 reverse/bounded DUPSORT scans, serde_json lenient struct deserialization, thiserror error enum design
**Confidence:** HIGH (primary APIs verified via docs.rs; on-disk format verified from spec.md and strfry source)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01:** Decoder returns a typed `NostrEvent` struct AND the retained decoded JSON bytes (one decode, no double parse).
- **D-02:** `NostrEvent` deserialization is lenient: 7 known fields required and typed; unknown top-level fields ignored (serde default, NOT `deny_unknown_fields`).
- **D-03:** `tags` typed as `Vec<Vec<String>>`.
- **D-04:** Use `serde_json` + local `NostrEvent` struct; NOT the `nostr` crate; do NOT re-verify signatures on decode path.
- **D-05:** Scan primitive yields `(composite key bytes, levId)` pairs — does NOT hydrate. Phase 3 decides what to hydrate.
- **D-06:** Scan parameterized by `limit` + `start-key` + `direction` (forward / reverse). Reverse iteration required for "latest N" queries.
- **D-07:** `limit = 0` means unbounded, implemented via internal windowing: bounded batches, close txn, reopen, resume — no single long-lived read txn.
- **D-08:** Read transactions are per-query and short — opened inside the primitive, dropped before return. No read txn held across primitive calls.
- **D-09:** Dictionaries lazily loaded and cached by `dictId` for process lifetime. Default `0x00` path pays nothing.
- **D-10:** Dictionary cache must be concurrency-safe (Phase 4 serves GraphQL on tokio — concurrent resolver calls).
- **D-11:** Single payload decode failure: skip, `tracing::warn!` with levId + reason, count. Query returns all decodable events.
- **D-12:** Decoded JSON treated as untrusted. Validate structure before use. No sig re-verification on hot path.

### Claude's Discretion

- Internal window batch size for `limit = 0` scans (sensible default, optionally a config knob).
- Exact concurrency primitive for the dictionary cache (`RwLock<HashMap<u32, Arc<DecoderDictionary>>>`, `OnceCell`-per-dict, or a small concurrent map).
- Decode error-type design (`thiserror` enum with LMDB / zstd / serde error kinds), module layout, and precise scan-primitive function signatures.
- Whether the scan primitive borrows a caller-supplied `RoTxn` or owns its own (recommendation: owns its own, consistent with Phase 1's per-call `read_txn()` pattern).
- Whether to do a startup `0x01` sampling/log line now or defer to Phase 5.

### Deferred Ideas (OUT OF SCOPE)

- Deletion reconciliation / staleness window (spec §6.5/§6.6) — Phase 3+.
- NIP-40 expiration filtering (spec §6.7) — Phase 3.
- Startup `0x01` sampling + richer drift surfacing — Phase 5.
- Doc-sync of stale `rusqlite`/SQLite wording in CLAUDE.md — separate maintenance pass.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| LMDB-07 | Decode `EventPayload` `0x00` (raw JSON) values to full event JSON | `serde_json::from_slice` on `bytes[1..]`; local `NostrEvent` struct design; retain raw bytes via `Bytes::copy_from_slice` or borrowed slice |
| LMDB-08 | Decode `EventPayload` `0x01` (zstd-dict-compressed) using `CompressionDictionary[dictId]` | `zstd::bulk::Decompressor::with_prepared_dictionary`; `DecoderDictionary::copy`; `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>` cache; byte layout `[0x01][dictId u32 LE][zstd frame]` |
| LMDB-09 | Keep read transactions short (per-query, bounded by limit) so strfry can reclaim pages | heed per-call `read_txn()` pattern established in Phase 1; windowing loop for `limit=0`; `drop(rtxn)` after each bounded batch |
</phase_requirements>

---

## Summary

Phase 2 builds two library-level primitives over the foundation established in Phase 1: (a) the `EventPayload` decoder that handles both `0x00` raw-JSON and `0x01` zstd-dictionary-compressed payloads, and (b) bounded, resumable, direction-aware cursor scans over each `Event__*` index. These are the two read primitives Phase 3's query engine will compose. Nothing here is GraphQL-facing or query-logic-bearing.

The on-disk byte layout is fully authoritative from `spec.md §3.2` and `src/events.cpp:196-215`. The `0x00` path is straightforward: strip the type byte, pass `bytes[1..]` to `serde_json::from_slice`, retain both the deserialized struct and the raw slice. The `0x01` path requires `zstd::bulk::Decompressor::with_prepared_dictionary`, using a `DecoderDictionary<'static>` built with `DecoderDictionary::copy(dict_bytes)`, cached in a `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>` for process lifetime. The capacity limit for decompression is the primary uncertainty — the zstd frame header contains the decompressed size for modern frames, but `Decompressor::decompress(data, capacity)` requires a caller-supplied ceiling; a safe default of 64 KiB × a multiplier (e.g., 4 MiB) or reading the frame content size via `zstd_safe::get_frame_content_size` is required.

For bounded scans, heed 0.22.1 provides `db.rev_range(rtxn, &range)` which returns `RoRevRange` — the correct primitive for "latest N" descending walks. The critical DUPSORT detail: `Event__*` indexes use `MDB_DUPSORT`, and the default `RoRevRange` iteration mode skips duplicate values per key. Callers MUST call `.move_through_duplicate_values()` to iterate all `(key, levId)` pairs. The windowing resume pattern for `limit = 0` requires capturing the last key seen, dropping the txn, reopening, and using `Bound::Excluded(last_key)` to resume without re-emitting the last pair.

**Primary recommendation:** Use `zstd::bulk::Decompressor::with_prepared_dictionary` (not the streaming `Decoder`) for one-shot small payload decompression; use `db.rev_range(...).move_through_duplicate_values()` for descending DUPSORT scans; keep decode and scan as separate modules mirroring Phase 1's `meta.rs` / `indexes.rs` split.

---

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| EventPayload decode (0x00 raw JSON) | Library/crate | — | Pure data transformation; no I/O after LMDB read |
| EventPayload decode (0x01 zstd-dict) | Library/crate | — | Pure decode; dictionary cache is process-local state |
| Dictionary cache (dict lookup + population) | Library/crate | LMDB `CompressionDictionary` sub-DB | Cache keyed on dictId; populated lazily from LMDB on first miss |
| Bounded forward index scan | LMDB layer | — | heed RoRange; cursor positioned by composite key |
| Bounded reverse index scan | LMDB layer | — | heed RoRevRange + move_through_duplicate_values(); needed for "latest N" |
| Windowed unbounded scan (limit=0) | Library/crate | — | Loop: open txn → batch → close txn → resume with Bound::Excluded |
| EventPayload point lookup by levId | LMDB layer | — | Integer key GET on `EventPayload` sub-DB; MDB_INTEGERKEY |
| CompressionDictionary point lookup by dictId | LMDB layer | — | Integer key GET on `CompressionDictionary` sub-DB; MDB_INTEGERKEY |

---

## Standard Stack

### Core (already in Cargo.toml)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `heed` | 0.22.1 (pinned) | LMDB typed wrapper — `rev_range`, `RoRevRange`, `IntegerComparator` | Established in Phase 1; verified comparator support |
| `serde_json` | 1.0.x (already in Cargo.toml) | JSON decode of Nostr event payloads | `from_slice` on raw bytes; no alloc of String intermediate |
| `serde` | 1.0.x (already in Cargo.toml) | `#[derive(Deserialize)]` on `NostrEvent` | Required for struct deserialization |
| `tracing` | 0.1.x (already in Cargo.toml) | `warn!` for skip-and-continue (D-11), `debug!` for scan ops | Established in Phase 1 |
| `thiserror` | 2.x (already in Cargo.toml) | `#[derive(thiserror::Error)]` on decode error enum | Established in Phase 1 (meta.rs, indexes.rs pattern) |

### New Dependency Required

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `zstd` | 0.13.3 (pin per CLAUDE.md) | zstd dictionary decompression — `Decompressor::with_prepared_dictionary`, `DecoderDictionary::copy` | Specified in CLAUDE.md; official libzstd bindings; `Send + Sync` on both `DecoderDictionary` and `Decompressor` |

`once_cell` is already resolved in `Cargo.lock` (version 0.46.0 via transitive deps) but not yet a direct dep. The recommended concurrency primitive (`RwLock` from `std`) does not require a new dependency. [VERIFIED: Cargo.lock]

### Installation

```toml
# Add to Cargo.toml [dependencies]:
zstd = "0.13.3"
```

No other new dependencies are required. `serde_json`, `serde`, `heed`, `tracing`, `thiserror` are already present.

### Alternatives Considered

| Recommended | Alternative | Tradeoff |
|-------------|-------------|----------|
| `zstd::bulk::Decompressor` | `zstd::Decoder` (streaming) | Streaming decoder is better for large payloads; bulk one-shot is simpler and lower-overhead for small event payloads (typical Nostr events are < 64 KB). Use bulk. |
| `DecoderDictionary::copy(bytes)` | `DecoderDictionary::new(bytes)` (experimental feature) | `::new` borrows bytes without copying — requires `experimental` feature flag. `::copy` makes a `'static` owned copy — cleaner lifetime for a process-lifetime cache. Use `::copy`. |
| `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>` | `OnceCell`-per-dict | `OnceCell` works for a single dict; `HashMap` is required for multiple dictIds. If the project expects exactly one dictId, `OnceCell` suffices, but `HashMap` is future-proof. Recommend `HashMap` behind `RwLock`. |
| `std::sync::RwLock` | `parking_lot::RwLock` | `parking_lot` is not yet a dependency; `std::sync::RwLock` is sufficient for a low-contention dictionary cache that is write-once, read-many. |

---

## Package Legitimacy Audit

Only one new package is required: `zstd`. All other dependencies are already present in `Cargo.toml` / `Cargo.lock`.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| `zstd` | crates.io | ~8 years | High (widely used) | github.com/gyscos/zstd-rs | [ASSUMED — slopcheck not available] | Approved [CITED: CLAUDE.md, docs.rs/zstd/0.13.3] |

**Packages removed due to slopcheck [SLOP] verdict:** none

**Packages flagged as suspicious [SUS]:** none

`zstd` 0.13.3 is cited explicitly in CLAUDE.md as a pinned stack dependency and confirmed present at docs.rs. [CITED: spam/CLAUDE.md]. The `slopcheck` tool was not available in this environment; however, `zstd` is a widely-known Rust binding to Facebook's libzstd, not a candidate for squatting. Planner should run `cargo add zstd@0.13.3` and verify registry resolution in `Cargo.lock` before proceeding.

---

## Architecture Patterns

### System Architecture Diagram

```
LMDB env (read-only)
        │
        ├── EventPayload sub-DB (MDB_INTEGERKEY, key=levId u64)
        │     │
        │     └── decode_event_payload(raw_bytes)
        │               │
        │               ├── [0x00] ──→ serde_json::from_slice(bytes[1..])
        │               │                   → (NostrEvent, raw_json: Bytes)
        │               │
        │               └── [0x01] ──→ dict_id = u32::from_le_bytes(bytes[1..5])
        │                             ├── cache.get(dict_id) hit  ──→ DecoderDictionary arc
        │                             └── cache miss ──→ LMDB GET CompressionDictionary[dict_id]
        │                                                → DecoderDictionary::copy(raw_dict_bytes)
        │                                                → cache.write().insert(dict_id, Arc::new(dd))
        │                                                → Decompressor::with_prepared_dictionary(&dd)
        │                                                → decompress(bytes[5..], capacity)
        │                                                → serde_json::from_slice(decompressed)
        │
        ├── CompressionDictionary sub-DB (MDB_INTEGERKEY, key=dictId u32)
        │     └── (accessed only for cache misses)
        │
        └── Event__* sub-DBs (DUPSORT, golpe comparators)
              │
              └── scan_index_bounded(env, index, start_key, direction, limit)
                        │
                        ├── direction=Forward ──→ db.range(rtxn, &(Bound::Included(start_key)..))
                        │                              .move_through_duplicate_values()
                        │                              .take(limit)       [limit>0]
                        │
                        └── direction=Reverse ──→ db.rev_range(rtxn, &(..=Bound::Included(start_key)))
                                                       .move_through_duplicate_values()
                                                       .take(limit)       [limit>0]
                        │
                        └── limit=0 ──→ windowing loop:
                                         batch = take(WINDOW_SIZE)
                                         collect → yield
                                         drop(rtxn)
                                         reopen rtxn
                                         resume from Bound::Excluded(last_key)
                                         repeat until exhausted
```

### Recommended Module Structure

```
src/
├── lmdb/
│   ├── mod.rs          # re-exports; existing
│   ├── env.rs          # open_read_only_env / open_fixture_env; existing
│   ├── types.rs        # LevId, MetaRecord; add NostrEvent, DecodedEvent here
│   ├── meta.rs         # read_meta, assert gates; existing
│   ├── comparators.rs  # golpe FFI comparators; existing
│   ├── indexes.rs      # index open-helpers, scan_lev_ids, seek_first_ge; existing
│   │                   # Phase 2: add scan_index_bounded + helpers
│   ├── self_check.rs   # comparator self-check; existing
│   ├── payload.rs      # NEW: EventPayload open, decode_event_payload, DictCache
│   └── scan.rs         # NEW: ScanParams, scan_index_bounded, windowing loop
└── ...
```

`payload.rs` owns the decoder and dictionary cache. `scan.rs` owns the bounded scan primitive. Both follow the same `thiserror` + `tracing::warn!` skip-and-continue pattern as Phase 1's `indexes.rs`.

### Pattern 1: EventPayload sub-DB open (IntegerComparator, NOT golpe)

`EventPayload` and `CompressionDictionary` are keyed by integer (`levId` uint64 / `dictId` uint32) with `MDB_INTEGERKEY`. Open with `IntegerComparator` — same as `Event__created_at` in Phase 1, NOT any golpe custom comparator.

```rust
// Source: heed 0.22.1 docs + Phase 1 open_index_created_at pattern
// EventPayload: key=levId (u64 MDB_INTEGERKEY), value=raw bytes
let event_payload_db: heed::Database<Bytes, Bytes, heed::IntegerComparator> = env
    .database_options()
    .types::<Bytes, Bytes>()
    .key_comparator::<heed::IntegerComparator>()
    .name("rasgueadb_defaultDb__EventPayload")
    .open(&rtxn)?
    .ok_or_else(|| PayloadError::SubDbNotFound { name: "EventPayload" })?;

// CompressionDictionary: key=dictId (u32 MDB_INTEGERKEY), value=raw dict bytes
let dict_db: heed::Database<Bytes, Bytes, heed::IntegerComparator> = env
    .database_options()
    .types::<Bytes, Bytes>()
    .key_comparator::<heed::IntegerComparator>()
    .name("rasgueadb_defaultDb__CompressionDictionary")
    .open(&rtxn)?
    .ok_or_else(|| PayloadError::SubDbNotFound { name: "CompressionDictionary" })?;
```

### Pattern 2: EventPayload point lookup by levId

levId is a native-endian uint64 (`MDB_INTEGERKEY`). On a little-endian host (the only supported host — LMDB-03 gate), use `.to_ne_bytes()`:

```rust
// Source: spec §3.4, Phase 1 meta.rs read_meta() pattern (record id key = 1u64.to_ne_bytes())
let key_bytes = lev_id.to_ne_bytes(); // native-endian for MDB_INTEGERKEY
let raw = event_payload_db.get(&rtxn, key_bytes.as_ref())?;
```

### Pattern 3: 0x00 raw-JSON decode — single pass, retain bytes

D-01 requires both a typed struct AND retained raw JSON bytes. The pattern is to deserialize from the slice and retain the slice (or copy it if the LMDB txn lifetime would expire):

```rust
// Source: serde_json docs, D-01/D-02/D-03
fn decode_raw_json(payload: &[u8]) -> Result<DecodedEvent, PayloadError> {
    debug_assert_eq!(payload[0], 0x00);
    let json_bytes = &payload[1..]; // strip type byte; ref to LMDB-owned bytes (txn lifetime)
    let event: NostrEvent = serde_json::from_slice(json_bytes)
        .map_err(PayloadError::JsonDecode)?;
    // Retain raw bytes — copy out of LMDB-mapped memory so caller is txn-independent:
    let raw_json = Bytes::copy_from_slice(json_bytes); // or use bytes::Bytes, or Vec<u8>
    Ok(DecodedEvent { event, raw_json })
}
```

Key detail: LMDB-mapped bytes are valid only for the lifetime of the read transaction. If `DecodedEvent` must outlive the txn, copy the bytes. Since D-08 says the txn is short-lived and dropped before return, the decoded output MUST own its bytes. Use `bytes::Bytes` (if added) or `Vec<u8>`. The simplest approach with current deps is `json_bytes.to_vec()`.

### Pattern 4: 0x01 zstd-dictionary decode

```rust
// Source: docs.rs/zstd/0.13.3/zstd/bulk/struct.Decompressor.html [VERIFIED]
//         spec §3.2, src/events.cpp:196-215
fn decode_zstd_payload(
    payload: &[u8],
    dict_cache: &DictCache,
    env: &heed::Env,
) -> Result<DecodedEvent, PayloadError> {
    debug_assert_eq!(payload[0], 0x01);
    if payload.len() < 5 {
        return Err(PayloadError::TruncatedZstdPayload { len: payload.len() });
    }
    // spec §3.2: bytes[1..5] = dictId uint32 NATIVE LE
    let dict_id = u32::from_le_bytes(payload[1..5].try_into().unwrap());
    let zstd_frame = &payload[5..];

    // Get or populate dictionary cache
    let dd = dict_cache.get_or_load(dict_id, env)?;

    // One-shot decompression with prepared dictionary
    let mut decompressor = zstd::bulk::Decompressor::with_prepared_dictionary(&dd)
        .map_err(PayloadError::ZstdError)?;
    let decompressed = decompressor.decompress(zstd_frame, MAX_EVENT_DECOMPRESSED_SIZE)
        .map_err(PayloadError::ZstdError)?;

    let event: NostrEvent = serde_json::from_slice(&decompressed)
        .map_err(PayloadError::JsonDecode)?;
    Ok(DecodedEvent { event, raw_json: decompressed })
}
```

`MAX_EVENT_DECOMPRESSED_SIZE`: the zstd frame header includes a content size field for frames written by modern zstd. `zstd_safe::get_frame_content_size` can read it (returns `u64` or an error), but it requires the `zstd-safe` feature on `zstd`. As a safe fallback: use a fixed `4 * 1024 * 1024` (4 MiB) ceiling — Nostr events are typically < 64 KiB, so this is permissive. A `decompress` call that would exceed the ceiling returns an error (not UB), which maps to `PayloadError::DecompressedTooLarge`. Claude's discretion on the exact limit.

### Pattern 5: DecoderDictionary cache shape

```rust
// Source: docs.rs/zstd/0.13.3 DecoderDictionary::copy — 'static lifetime [VERIFIED]
//         D-09, D-10 — lazy, concurrency-safe, process-lifetime cache

use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use zstd::dict::DecoderDictionary;

pub struct DictCache {
    inner: RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>,
}

impl DictCache {
    pub fn new() -> Self {
        Self { inner: RwLock::new(HashMap::new()) }
    }

    pub fn get_or_load(
        &self,
        dict_id: u32,
        env: &heed::Env,
    ) -> Result<Arc<DecoderDictionary<'static>>, PayloadError> {
        // Fast path: read lock
        {
            let guard = self.inner.read().unwrap();
            if let Some(dd) = guard.get(&dict_id) {
                return Ok(Arc::clone(dd));
            }
        }
        // Slow path: load from LMDB, then write lock
        // Important: load OUTSIDE the write lock to avoid holding the lock during LMDB I/O
        let dd = load_decoder_dictionary_from_lmdb(dict_id, env)?;
        let dd = Arc::new(dd);
        {
            let mut guard = self.inner.write().unwrap();
            // Re-check: another thread may have populated it
            guard.entry(dict_id).or_insert_with(|| Arc::clone(&dd));
        }
        Ok(dd)
    }
}

fn load_decoder_dictionary_from_lmdb(
    dict_id: u32,
    env: &heed::Env,
) -> Result<DecoderDictionary<'static>, PayloadError> {
    let rtxn = env.read_txn().map_err(PayloadError::Heed)?;
    let dict_db = open_compression_dictionary_db(env, &rtxn)?;
    let key = dict_id.to_ne_bytes();
    let raw_dict = dict_db
        .get(&rtxn, key.as_ref())
        .map_err(PayloadError::Heed)?
        .ok_or(PayloadError::DictNotFound { dict_id })?;
    // DecoderDictionary::copy() makes an owned 'static copy — safe for process-lifetime cache
    Ok(DecoderDictionary::copy(raw_dict)) // [VERIFIED: docs.rs/zstd/0.13.3/zstd/dict/struct.DecoderDictionary.html]
}
```

`DictCache` is `Send + Sync` because `RwLock<HashMap<...>>` is `Send + Sync` and `DecoderDictionary<'static>` is `Send + Sync`. [VERIFIED: docs.rs zstd — DecoderDictionary impl Send, Sync]

### Pattern 6: Bounded forward scan (heed 0.22.1 + DUPSORT)

```rust
// Source: heed 0.22.1 docs — db.range(), RoRange::move_through_duplicate_values() [VERIFIED]
// D-05, D-06, D-08

// CRITICAL: Event__* indexes are MDB_DUPSORT — default iteration skips duplicates.
// Must call .move_through_duplicate_values() to see all (key, levId) pairs.

fn scan_forward_bounded(
    db: &heed::Database<Bytes, Bytes, impl heed::Comparator>,
    rtxn: &heed::RoTxn<'_>,
    start_key: &[u8],
    limit: usize, // 0 means call scan_windowed instead
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError> {
    let range = (Bound::Included(start_key), Bound::Unbounded);
    let iter = db
        .range(rtxn, &range)?
        .move_through_duplicate_values();
    let mut results = Vec::new();
    for item in iter.take(limit) {
        let (key, value) = item?;
        if value.len() < 8 {
            tracing::warn!(value_len = value.len(), "Malformed levId VALUE in index scan — skipping");
            continue;
        }
        let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
        results.push((key.to_vec(), lev_id));
    }
    Ok(results)
}
```

### Pattern 7: Bounded reverse scan (newest-first)

```rust
// Source: heed 0.22.1 docs — db.rev_range() returns RoRevRange [VERIFIED]
// RoRevRange::move_through_duplicate_values() for DUPSORT [VERIFIED]
// D-06: reverse iteration for "latest N" queries

fn scan_reverse_bounded(
    db: &heed::Database<Bytes, Bytes, impl heed::Comparator>,
    rtxn: &heed::RoTxn<'_>,
    upper_bound_key: &[u8],  // walk backward from here (inclusive)
    limit: usize,
) -> Result<Vec<(Vec<u8>, LevId)>, IndexError> {
    // rev_range with inclusive upper bound — walks descending from upper_bound_key
    let range = (Bound::Unbounded, Bound::Included(upper_bound_key));
    let iter = db
        .rev_range(rtxn, &range)?
        .move_through_duplicate_values();
    let mut results = Vec::new();
    for item in iter.take(limit) {
        let (key, value) = item?;
        if value.len() < 8 {
            tracing::warn!(value_len = value.len(), "Malformed levId VALUE in reverse scan — skipping");
            continue;
        }
        let lev_id = u64::from_le_bytes(value[0..8].try_into().unwrap());
        results.push((key.to_vec(), lev_id));
    }
    Ok(results)
}
```

### Pattern 8: Windowed unbounded scan (limit = 0, D-07/D-08)

```rust
// D-07: limit=0 uses internal windowing — no single long-lived read txn
// D-08: each batch opens a fresh read txn, drops it before the next batch

const DEFAULT_WINDOW_SIZE: usize = 256; // Claude's discretion — configurable

fn scan_windowed_unbounded(
    env: &heed::Env,
    db_name: &str,
    start_key: Vec<u8>,
    direction: ScanDirection,
    emit: &mut impl FnMut(Vec<u8>, LevId) -> Result<(), IndexError>,
) -> Result<(), IndexError> {
    let mut resume_key = start_key;
    let mut first_batch = true;

    loop {
        let rtxn = env.read_txn()?;
        let db = open_index_by_name(env, &rtxn, db_name)?;

        let bound = if first_batch {
            Bound::Included(resume_key.as_slice())
        } else {
            Bound::Excluded(resume_key.as_slice())  // skip last-seen key on resume
        };

        let batch = match direction {
            ScanDirection::Forward => {
                let range = (bound, Bound::Unbounded);
                collect_batch_from_range(&db.range(&rtxn, &range)?.move_through_duplicate_values(), DEFAULT_WINDOW_SIZE)?
            }
            ScanDirection::Reverse => {
                let range = (Bound::Unbounded, bound);
                collect_batch_from_rev_range(&db.rev_range(&rtxn, &range)?.move_through_duplicate_values(), DEFAULT_WINDOW_SIZE)?
            }
        };

        // Drop txn before emitting to caller — short txn invariant (D-08)
        drop(rtxn);
        first_batch = false;

        if batch.is_empty() {
            break;
        }

        let last_key = batch.last().unwrap().0.clone();
        for (key, lev_id) in batch {
            emit(key, lev_id)?;
        }
        resume_key = last_key;
    }
    Ok(())
}
```

### Pattern 9: NostrEvent struct shape (D-02/D-03/D-04)

```rust
// Source: D-01..D-04, spec §3.2, CLAUDE.md (do NOT use the nostr crate)
// serde default = lenient (ignores unknown fields); do NOT add #[serde(deny_unknown_fields)]

#[derive(Debug, Clone, serde::Deserialize)]
pub struct NostrEvent {
    pub id: String,
    pub pubkey: String,
    pub created_at: u64,
    pub kind: u64,
    pub tags: Vec<Vec<String>>,  // D-03: typed for Phase 3 tag scans and NIP-40
    pub content: String,
    pub sig: String,
    // No #[serde(deny_unknown_fields)] — D-02: lenient, forward-compat
}

/// Output of a single EventPayload decode — both typed and raw (D-01).
#[derive(Debug, Clone)]
pub struct DecodedEvent {
    pub event: NostrEvent,
    pub raw_json: Vec<u8>,  // exact bytes for Phase 4 passthrough (no re-serialize)
}
```

`created_at` as `u64` is technically correct (Unix epoch, but negative timestamps are out-of-spec for Nostr). Using `i64` is also valid if strfry-produced events might carry unusual values — `u64` is the simpler choice given strfry's internal representation.

### Pattern 10: PayloadError enum (thiserror, consistent with Phase 1 pattern)

```rust
// Mirrors MetaError (meta.rs) and IndexError (indexes.rs) conventions from Phase 1.
#[derive(Debug, thiserror::Error)]
pub enum PayloadError {
    #[error("LMDB error: {0}")]
    Heed(#[from] heed::Error),

    #[error("Sub-DB '{name}' not found in strfry env")]
    SubDbNotFound { name: &'static str },

    #[error("EventPayload byte[0] is unknown type tag 0x{tag:02x} (expected 0x00 or 0x01)")]
    UnknownTypeTag { tag: u8 },

    #[error("0x01 payload too short: {len} bytes (need at least 5 for dictId + zstd frame)")]
    TruncatedZstdPayload { len: usize },

    #[error("CompressionDictionary[{dict_id}] not found in strfry env")]
    DictNotFound { dict_id: u32 },

    #[error("zstd decompression error: {0}")]
    ZstdError(#[from] std::io::Error),  // zstd bulk errors implement std::io::Error

    #[error("JSON decode error: {0}")]
    JsonDecode(#[from] serde_json::Error),

    #[error("Decompressed payload exceeds size limit ({limit} bytes)")]
    DecompressedTooLarge { limit: usize },
}
```

### Anti-Patterns to Avoid

- **Opening sub-DBs with `create_database` instead of `open_database`:** Setting `MDB_CREATE` would create new sub-DBs in strfry's read-only environment. Always use `env.database_options().open(rtxn)`. [CITED: CLAUDE.md, Phase 1 indexes.rs comments]
- **Calling `.rev()` on `RoRange` expecting a reverse cursor:** `RoRange` does not implement `DoubleEndedIterator`; `.rev()` is not a valid method on `RoRange`. Use `db.rev_range()` for reverse iteration. [VERIFIED: docs.rs heed 0.22.1 RoRange — no rev() method]
- **Iterating a DUPSORT index without `.move_through_duplicate_values()`:** Default iteration on `RoRange`/`RoRevRange` calls `move_between_keys()`, which yields only the first VALUE per KEY. `Event__*` indexes have multiple levIds per key (DUPSORT) — skipping duplicates silently drops events. Always call `.move_through_duplicate_values()`. [VERIFIED: docs.rs heed 0.22.1 RoRevRange]
- **Setting `IntegerComparator` on `Event__*` golpe-comparator indexes:** `EventPayload` and `CompressionDictionary` use `IntegerComparator`. `Event__*` indexes use golpe FFI comparators. Never mix them up — the error is silent and causes wrong scan order.
- **Holding a read txn while building `DecoderDictionary`:** `DecoderDictionary::copy` takes time proportional to dict size. Open a short txn to read raw dict bytes, copy them to `Vec<u8>`, close the txn, then build the `DecoderDictionary` outside the txn. [CITED: spec §6.4, D-08]
- **Not adding `zstd = "0.13.3"` to Cargo.toml:** `zstd` is not currently in the project's Cargo.toml or Cargo.lock. It must be added explicitly.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| zstd dictionary decompression | Custom zstd frame parser | `zstd::bulk::Decompressor::with_prepared_dictionary` | zstd frame format has several subtleties (magic number, frame header variants, block types); `zstd` crate wraps libzstd directly |
| JSON field extraction | Manual byte scanning of JSON | `serde_json::from_slice` + `#[derive(Deserialize)]` | Handles all JSON edge cases (escaped strings, Unicode, number formats) |
| Thread-safe lazy initialization | Double-checked locking by hand | `std::sync::RwLock` + `HashMap::entry` or `once_cell::sync::OnceCell` | Hand-rolled double-checked locking is incorrect on stable Rust without atomics; standard primitives are correct by construction |
| Integer key byte layout | Custom endianness conversion | `.to_ne_bytes()` (native-endian for `MDB_INTEGERKEY`) | strfry stores `levId`/`dictId` in host byte order via LMDB's `to_sv`; `.to_ne_bytes()` is the correct byte-exact representation |

**Key insight:** The `0x01` decompression path is the only genuinely new surface in Phase 2. Everything else is composing existing correct primitives from Phase 1 (heed LMDB access) with standard library functions (`serde_json`). The zstd decode is a single function call once the dictionary is loaded — the complexity is entirely in the cache shape and the capacity/sizing question.

---

## Common Pitfalls

### Pitfall 1: dictId byte layout — spec says "NATIVE endianness," not "always LE"

**What goes wrong:** Reading `bytes[1..5]` as `u32::from_le_bytes(...)` and assuming this is always correct.

**Why it happens:** On a little-endian host (the only supported platform — LMDB-03 gate asserts this) native endianness IS little-endian, so `from_le_bytes` and `from_ne_bytes` are equivalent. But spec §3.2 says "native endianness" and the endianness gate (LMDB-03) is what makes LE correct. Using `from_le_bytes` is correct given the co-location assumption, but the code comment should reference the gate.

**How to avoid:** Use `u32::from_le_bytes` and annotate with `// LE: asserted by LMDB-03 endianness gate`. [CITED: spec §6.3]

### Pitfall 2: `bytes[1..5]` vs `bytes[1..4]` — spec.md has an off-by-one in the text

**What goes wrong:** spec.md §3.2 says `bytes[1..4] is dictId`. In Rust slice notation, `bytes[1..4]` is 3 bytes. dictId is a uint32 = 4 bytes.

**Why it happens:** The spec text uses inclusive-to notation in one place. The authoritative source is `src/events.cpp:196-215` which reads 4 bytes for dictId.

**How to avoid:** Use `bytes[1..5]` (4 bytes, exclusive end) = `[1, 2, 3, 4]` = 4 bytes = uint32. `bytes[5..]` is the zstd frame. [CITED: spec §3.2 cross-checked with events.cpp decodeEventPayload]

### Pitfall 3: Reverse range on DUPSORT skips duplicate values by default

**What goes wrong:** `db.rev_range(rtxn, &range)` returns `RoRevRange` which defaults to `move_between_keys()` behavior — it yields only the first VALUE (lowest levId) for each KEY. For DUPSORT `Event__*` indexes where multiple events share the same composite key (same pubkey + same created_at), only one levId per unique key is returned.

**Why it happens:** heed's default iteration mode for DUPSORT is documented as `move_between_keys` ("Move on the first value of keys, ignoring duplicate values"). This is the safe default but wrong for this use case.

**How to avoid:** Always call `.move_through_duplicate_values()` on any `RoRange` or `RoRevRange` over an `Event__*` index. [VERIFIED: docs.rs heed 0.22.1 RoRevRange methods]

**Warning signs:** Tests showing fewer results than expected for events sharing the same `created_at` timestamp.

### Pitfall 4: Capacity ceiling too low causes spurious `DecompressedTooLarge` errors

**What goes wrong:** Setting `MAX_EVENT_DECOMPRESSED_SIZE` too low (e.g., 64 KiB) causes legitimate events with large content fields to fail decompression.

**Why it happens:** Nostr events can have arbitrarily large `content` fields. strfry's ingestion does have its own limits but an adversarial or legitimate relay may have stored events up to strfry's configured `maxEventSize` (default varies, often 65536 or higher).

**How to avoid:** Use 4 MiB as the safe default ceiling. Log the capacity used when events approach the limit. Document the configurable limit in CLAUDE.md after Phase 2. [ASSUMED — strfry's default maxEventSize is not verified in this session]

### Pitfall 5: Windowing resume with `Bound::Included` re-emits the last key

**What goes wrong:** On window resume, using `Bound::Included(last_key)` re-emits the last `(key, levId)` pair from the previous batch.

**Why it happens:** The last key of the previous batch is the resume cursor. `Included` means "start at this key" — which was already emitted.

**How to avoid:** Use `Bound::Excluded(last_key)` for all resume windows after the first. [CITED: std::ops::Bound documentation]

**Warning signs:** Duplicate `(key, levId)` pairs in the output of a windowed scan.

### Pitfall 6: Txn lifetime and output data ownership

**What goes wrong:** Returning references into LMDB-mapped memory (`&[u8]` slices from `db.get`) after the `RoTxn` is dropped. Rust's lifetime system catches this at compile time if the txn is a local variable, but patterns using `unsafe` or `Arc<Txn>` can subvert this.

**Why it happens:** heed's `get` returns `Option<&[u8]>` with lifetime tied to `&'txn RoTxn`. When the txn drops, the pages may be recycled by the MMAP.

**How to avoid:** Always copy bytes out of LMDB memory before dropping the txn. Use `.to_vec()` for raw bytes. This is required by D-08 (short txns) and D-01 (DecodedEvent must outlive the txn).

### Pitfall 7: `zstd` crate not yet in Cargo.toml

**What goes wrong:** The project Cargo.toml does not currently list `zstd` as a dependency. Building a decoder that imports `zstd::bulk` fails with a compilation error until `zstd = "0.13.3"` is added.

**How to avoid:** The first task in the plan must be `cargo add zstd@0.13.3` and verify `Cargo.lock` is updated. [VERIFIED: Cargo.lock does not contain zstd entries]

---

## Validation Architecture

> `workflow.nyquist_validation` is `false` in `.planning/config.json` — skip formal test-map. This section provides isolation test strategy for the planner's Wave 0 task scaffold only.

### Test Strategy for Each Primitive

All tests follow the Phase 1 pattern: copy fixture to tempdir, open `open_fixture_env`, call the primitive, assert. Tests live in `#[cfg(test)]` blocks inside each module or in `tests/` integration tests.

#### LMDB-07: 0x00 raw-JSON decode

- **Test:** For each event in `tests/fixture/seed_events.jsonl`, GET its `EventPayload` by levId (from the golden vector ordering), call `decode_event_payload`, assert the decoded `NostrEvent.id` matches the seed event's `id` field.
- **Coverage:** Verifies round-trip: strfry wrote `0x00 + json`, decoder strips byte and deserializes correctly.
- **Fixture availability:** All 11 seed events in the fixture are `0x00` (default write path). No new fixture needed for this test.

#### LMDB-08: 0x01 zstd-dictionary decode

**Challenge:** The committed fixture `data.mdb` contains only `0x00` payloads (default strfry write path). The `0x01` path only appears after an offline `strfry compact` operation. There is no `0x01` payload in the existing fixture.

**Recommended approach — synthesize a `0x01` fixture payload in a unit test:**

```rust
// No live strfry needed — construct a synthetic 0x01 payload and test the decode path directly.
// This tests the decoder's zstd path without requiring a compacted LMDB fixture.

#[test]
fn test_decode_0x01_synthetic_payload() {
    use zstd::bulk::{compress_with_dictionary, decompress_with_dictionary};
    // Note: compress_with_dictionary is NOT in zstd::bulk per docs — use Compressor::with_dictionary

    // 1. Construct a raw Nostr event JSON string
    let event_json = r#"{"id":"aabbcc...","pubkey":"79be...","created_at":1700000000,"kind":1,"tags":[],"content":"test","sig":"ff..."}"#;
    
    // 2. Train or use a trivial zstd dictionary (or use an empty [] for a frameless dict)
    //    For testing, generate a real dict using zstd::dict::from_continuous:
    let dict_bytes = zstd::dict::from_continuous(event_json.as_bytes(), &[event_json.len()], 1024)
        .expect("train dict");
    
    // 3. Compress with the dictionary
    let mut compressor = zstd::bulk::Compressor::with_dictionary(3, &dict_bytes)
        .expect("build compressor");
    let compressed = compressor.compress(event_json.as_bytes()).expect("compress");

    // 4. Build the 0x01 payload: [0x01][dictId u32 LE][compressed bytes]
    let dict_id: u32 = 42;
    let mut payload = vec![0x01_u8];
    payload.extend_from_slice(&dict_id.to_le_bytes());
    payload.extend_from_slice(&compressed);

    // 5. Build a DictCache pre-populated with our synthetic dict
    let cache = DictCache::new();
    cache.insert_for_test(dict_id, &dict_bytes); // test-only helper

    // 6. Decode and assert
    let decoded = decode_zstd_payload_with_cache(&payload, &cache)
        .expect("decode 0x01 payload");
    assert_eq!(decoded.event.kind, 1);
    assert_eq!(decoded.raw_json, event_json.as_bytes());
}
```

This does not require a compacted `data.mdb`. The test is pure Rust with no LMDB dependency, making it fast and hermetic.

**Alternative — CI integration test:** Add a CI step that runs `strfry compact` against the fixture, then re-opens the compacted `data.mdb` and asserts `0x01` payloads decode correctly. This is a Phase 5 / OPS-03 concern (committed to as part of the fixture regeneration story). Phase 2 relies on the synthetic unit test for `0x01` correctness.

#### LMDB-09: Per-query short-txn invariant

The short-txn invariant (D-08) is a behavioral contract: "no read txn is held across primitive calls." It cannot be directly asserted via LMDB's own `mdb_stat` in a unit test. Recommended test approaches:

1. **Structural test:** Assert that the `scan_index_bounded` function signature takes `&heed::Env` (not `&heed::RoTxn`) as input — the primitive owns its own txn. This is verified by compilation: if the function signature is `fn scan_index_bounded(env: &heed::Env, ...)`, a caller cannot pass a long-lived txn in.

2. **Windowing invariant test:** For `limit = 0` windowed scan, instrument via a drop-detecting wrapper:
   ```rust
   // Test that a windowed scan over the fixture produces all N expected levIds
   // AND that it issued more than one txn (i.e., windowing actually occurred).
   // If WINDOW_SIZE < total_events, multiple windows are needed.
   let (lev_ids, txn_count) = scan_windowed_with_txn_counter(env, "Event__kind", ...);
   assert_eq!(lev_ids.len(), EXPECTED_TOTAL_EVENTS);
   assert!(txn_count > 1, "Windowed scan must open multiple short txns for limit=0");
   ```
   Implement `scan_windowed_with_txn_counter` as a test-only wrapper that counts `env.read_txn()` calls by wrapping the env in a counter struct.

3. **Smoke test:** Open the fixture, call `scan_index_bounded` for each index with `limit=5`, and assert the returned levIds are a prefix of the golden vector for that index (forward direction) or the last 5 entries (reverse direction).

#### Bounded scan correctness tests

- **Forward scan, `Event__kind`, limit=3:** Assert returns the first 3 levIds from `Event__kind.json` golden vector (levIds `[4, 5, 6]`).
- **Reverse scan, `Event__kind`, limit=3:** Assert returns the last 3 levIds in reverse order: `[2, 3, 11]` reversed = `[2, 3, 11]` → last 3 of `[4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]` is `[3, 2]`... actually last 3 = `[9, 3, 2]`, reversed = `[2, 3, 9]`. Verify from the golden vector.
- **Resume cursor test:** Forward scan limit=3, capture last key, forward scan from `Bound::Excluded(last_key)` limit=3, assert no overlap and correct continuation.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `RoRange::rev()` (does not exist) | `db.rev_range(rtxn, &range)` | heed 0.22.1 | Explicit reverse-range method; no confusion with Iterator::rev() |
| `DatabaseFlags::INTEGER_KEY` flag on open | `key_comparator::<IntegerComparator>()` on `database_options()` | heed 0.21 | `INTEGER_KEY` flag is deprecated since 0.21; use typed comparator |
| `zstd::Decoder::with_dictionary` (streaming) | `zstd::bulk::Decompressor::with_prepared_dictionary` (one-shot) | current | Bulk decompressor is simpler for small event payloads; streaming decoder is for large streams |

**Deprecated/outdated:**

- `DatabaseFlags::INTEGER_KEY` — deprecated since heed 0.21; still compiles but use `IntegerComparator` type parameter instead. [CITED: CLAUDE.md "DatabaseOpenOptions::key_comparator::(IntegerComparator)() — YES (preferred; DatabaseFlags::INTEGER_KEY deprecated since 0.21)"]
- `nostr` crate for event deserialization — explicitly out-of-scope per CLAUDE.md and D-04. Use `serde_json` + local `NostrEvent` struct.

---

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `zstd::bulk::Decompressor::decompress` error type implements `From<std::io::Error>` for `PayloadError` | Pattern 3, Pattern 4 | If the error type is `zstd_safe::ErrorCode` or a custom type, the `#[from]` annotation needs to change |
| A2 | `Event__*` default iteration mode is `move_between_keys` (not `move_through_duplicate_values`) when no method is called | Pitfall 3 | If default is already DUPSORT-aware, the explicit `.move_through_duplicate_values()` call is harmless but not required |
| A3 | The max compressed Nostr event size is well below 4 MiB (so the ceiling is safe) | Pattern 4 | If a legitimate event exceeds 4 MiB decompressed, it will be skipped with `DecompressedTooLarge` error |
| A4 | `zstd::dict::from_continuous` is usable in test code for generating a synthetic dictionary without the `experimental` feature | Validation Architecture | If it requires a feature flag, synthetic `0x01` test construction needs an alternate approach (e.g., use a pre-baked dict byte array) |
| A5 | `zstd::bulk::Compressor::with_dictionary` exists (for test synthesis of 0x01 payloads) | Validation Architecture | If the compressor dict API differs, the test synthesis approach changes; the decoder path itself is unaffected |
| A6 | `dictId` in `CompressionDictionary` sub-DB uses native-LE key (same as `levId` in `EventPayload`) | Pattern 5 | If dictId uses a different encoding, the GET by `dict_id.to_ne_bytes()` returns `None` for every lookup |

---

## Open Questions (RESOLVED)

1. **zstd capacity ceiling: fixed vs dynamic**
   - What we know: `Decompressor::decompress(data, capacity)` returns `Err` if decompressed output would exceed `capacity`. `zstd_safe::get_frame_content_size` can read the frame header's content size field, but requires `zstd-safe` sub-crate access.
   - What's unclear: Whether events in the live compacted DB ever exceed 64 KiB decompressed; whether `zstd_safe` is exposed directly via `zstd = "0.13.3"`.
   - Recommendation: Use a fixed 4 MiB ceiling for Phase 2. If `zstd_safe` is accessible, add a `get_frame_content_size`-based dynamic path in a follow-up.
   - **RESOLVED:** Fixed 4 MiB ceiling committed in Plan 02-02 Task 2 (`MAX_EVENT_DECOMPRESSED_SIZE`); dynamic `get_frame_content_size` path deferred to a follow-up.

2. **`Compressor::with_dictionary` API for test synthesis**
   - What we know: `Decompressor::with_prepared_dictionary` exists and is verified. The compressor API is needed only for test synthesis of `0x01` payloads.
   - What's unclear: Exact `zstd::bulk::Compressor::with_dictionary` signature (not verified).
   - Recommendation: Plan a task to verify the compressor API when setting up the test; fallback is a hard-coded pre-compressed byte array.
   - **RESOLVED:** Plan 02-02 Task 2 instructs the executor to verify the compressor API at test-setup time, with an explicit fallback to a pre-baked compressed byte array.

3. **`once_cell` vs `std::sync::OnceLock` for per-entry cache**
   - What we know: `once_cell` 0.46.0 is already in the transitive dependency graph (Cargo.lock). `std::sync::OnceLock` is stable since Rust 1.70 (the project targets current stable).
   - What's unclear: Whether per-dictId `OnceLock` or `RwLock<HashMap>` is preferred by the team.
   - Recommendation: `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>` — handles multiple dictIds, write-once after first miss, read-many thereafter. Claude's discretion.
   - **RESOLVED:** Plan 02-02 Task 1 adopts `RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>` (documented as Claude's discretion).

---

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `zstd` crate (Rust) | LMDB-08 decoder | Not yet (not in Cargo.toml) | — | Must add `zstd = "0.13.3"` |
| `cargo test` | All tests | Yes | Rust 1.89 (rustup) | — |
| Fixture `data.mdb` | LMDB-07/09 tests | Yes | tests/fixture/ | — |
| Compacted `data.mdb` with `0x01` payloads | LMDB-08 full integration test | No (only after `strfry compact`) | — | Synthetic unit test (see Validation Architecture) |

**Missing dependencies with no fallback:** `zstd = "0.13.3"` must be added to `Cargo.toml` before any Phase 2 code compiles.

**Missing dependencies with fallback:** Compacted `0x01` fixture — synthetic unit test covers the decode path; full integration deferred to Phase 5.

---

## Security Domain

> `security_enforcement: true`, `security_asvs_level: 1` in config.json.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | Library-only phase; no auth surface |
| V3 Session Management | No | No HTTP/session layer |
| V4 Access Control | No | Read-only primitives; no write surface |
| V5 Input Validation | Yes | Nostr event JSON decoded from LMDB — treated as untrusted per D-12 |
| V6 Cryptography | No | No crypto; sig verification explicitly excluded (D-04, D-12) |

### Known Threat Patterns for This Stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Malformed JSON in EventPayload | Tampering | `serde_json::from_slice` returns `Err` on invalid JSON; D-11 skip-and-continue |
| zstd decompression bomb (oversized payload) | DoS | Fixed capacity ceiling in `Decompressor::decompress(data, MAX_SIZE)` — returns `Err` not panic |
| Unknown `0x0x` type bytes beyond `0x00`/`0x01` | Tampering | `PayloadError::UnknownTypeTag` — logged and skipped; no undefined behavior |
| dictId references a non-existent dictionary | Tampering | `PayloadError::DictNotFound` — skip-and-continue; LMDB GET returns `None` cleanly |
| Borrowed LMDB bytes used after txn close | Memory safety | Compile-time lifetime enforcement by heed/Rust; pattern 6 (copy bytes before txn drop) |

**Key security note (D-12):** Decoded JSON is treated as untrusted. The lenient `serde_json::Deserialize` approach (no `deny_unknown_fields`) means unexpected fields are silently ignored — this is intentional for forward-compat but means Phase 3 MUST NOT assume the absence of unknown fields implies absence of malicious content in known fields. The `content` field in particular is unvalidated user-supplied text.

---

## Sources

### Primary (HIGH confidence)

- `docs.rs/heed/0.22.1/heed/struct.Database.html` — `rev_range` method signature confirmed; `RoRevRange` exists [VERIFIED]
- `docs.rs/heed/0.22.1/heed/struct.RoRevRange.html` — `move_through_duplicate_values()` confirmed for DUPSORT [VERIFIED]
- `docs.rs/zstd/0.13.3/zstd/bulk/struct.Decompressor.html` — `with_prepared_dictionary`, `with_dictionary`, `set_prepared_dictionary`, `decompress` signatures confirmed [VERIFIED]
- `docs.rs/zstd/0.13.3/zstd/dict/struct.DecoderDictionary.html` — `copy()` constructor (static lifetime), `Send + Sync` [VERIFIED]
- `spec.md §3.2` — `EventPayload` byte layout (`0x00`/`0x01`, dictId u32 native-LE, zstd frame) [CITED: spec.md in this repo]
- `src/events.cpp:196-215` — `decodeEventPayload` confirms 4-byte dictId at bytes[1..5] [CITED: spec.md cross-reference]
- `spam/CLAUDE.md` — pinned stack: `zstd = "0.13.3"`, `serde_json = "1.0.150"`, `heed = "0.22.1"` [CITED]
- `02-CONTEXT.md` — D-01 through D-12 (authoritative locked decisions) [CITED]
- Phase 1 source code — `src/lmdb/indexes.rs`, `meta.rs`, `env.rs`, `types.rs` — module layout and patterns to extend [VERIFIED: read in session]

### Secondary (MEDIUM confidence)

- `docs.rs/heed/0.22.1/heed/cookbook/` — confirmed `(Bound::Included(key), Bound::Unbounded)` pattern for Bytes-keyed range; Phase 1 indexes.rs already uses this [CITED]
- `Cargo.lock` — `once_cell` 0.46.0 is a transitive dep; `zstd` not present (must add) [VERIFIED: read in session]

### Tertiary (LOW confidence — training-only)

- `zstd` error type being `std::io::Error` [ASSUMED — A1]
- Default `RoRange`/`RoRevRange` iteration mode being `move_between_keys` [ASSUMED — A2, based on heed documentation text]

---

## Metadata

**Confidence breakdown:**

- Standard stack: HIGH — all key APIs verified at docs.rs
- Architecture / patterns: HIGH — heed patterns extend Phase 1 proven code; zstd API verified
- Pitfalls: HIGH for DUPSORT/rev (verified), MEDIUM for capacity ceiling (training knowledge)
- 0x01 fixture synthesis: MEDIUM — compressor API shape assumed, not verified

**Research date:** 2026-06-11
**Valid until:** 2026-07-11 (stable: heed 0.22.1 and zstd 0.13.3 are pinned versions; no fast-moving surface)
