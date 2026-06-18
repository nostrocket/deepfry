---
phase: "02-payload-decoding-index-scan-primitives"
plan: "02"
subsystem: "lmdb"
tags: ["zstd", "dictionary-cache", "payload-decode", "0x01-path", "concurrency"]
dependency_graph:
  requires: ["02-01"]
  provides: ["LMDB-08", "DictCache", "0x01-decode-path"]
  affects: ["src/lmdb/payload.rs"]
tech_stack:
  added: []
  patterns:
    - "RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>> — read-lock fast path, write-lock only on cache miss"
    - "DecoderDictionary::copy — 'static owned copy built OUTSIDE read txn and OUTSIDE write lock"
    - "Decompressor::with_prepared_dictionary + decompress(frame, MAX_EVENT_DECOMPRESSED_SIZE)"
    - "TDD: test helper fn make_synthetic_0x01_payload using zstd::dict::from_continuous"
key_files:
  modified:
    - "src/lmdb/payload.rs"
decisions:
  - "decode_event_payload_with_cache takes &heed::Env for potential cache-miss LMDB lookup; env is never touched on cache hit (fast path)"
  - "decode_event_payload (cache-less, Plan 01) still returns UnknownTypeTag for 0x01 to preserve 0x00-only call-sites without modification"
  - "MAX_EVENT_DECOMPRESSED_SIZE = 4 MiB — permissive for all legitimate Nostr events; returns Err (not UB) on overflow"
  - "get_or_load_no_env + insert_for_test are #[cfg(test)]-only; production code always calls get_or_load(dict_id, env)"
  - "Synthetic 0x01 round-trip uses zstd::dict::from_continuous (available via default zstd features: zdict_builder) — no pre-baked bytes needed"
metrics:
  duration: "~7 minutes"
  completed_date: "2026-06-11"
  tasks_completed: 2
  files_modified: 1
  commits: 1
---

# Phase 02 Plan 02: DictCache + 0x01 zstd-dictionary decode path (LMDB-08) Summary

**One-liner:** Lazy concurrency-safe DictCache (RwLock/HashMap/Arc) + wired 0x01 zstd-dictionary decode path with 4 MiB decompression ceiling, proven by synthetic round-trip test.

## What Was Built

### Task 1: DictCache (D-09, D-10)

Added `pub struct DictCache` to `src/lmdb/payload.rs` with field type
`RwLock<HashMap<u32, Arc<DecoderDictionary<'static>>>>`.

Key implementation decisions:
- **Read-lock fast path**: acquires `inner.read()`, returns `Arc::clone` on hit without touching LMDB.
- **Slow path**: opens a short read txn, GETs raw dictionary bytes via `open_compression_dictionary_db`, `.to_vec()`s them out, drops the txn, then calls `DecoderDictionary::copy(&raw_bytes)` — all OUTSIDE the txn AND outside the write lock (anti-pattern from RESEARCH Pattern 5 avoided).
- **Write path**: acquires write lock, uses `entry(dict_id).or_insert_with(|| Arc::clone(&dd))` to handle a racing concurrent insert without double-initialising.
- `DecoderDictionary<'static>` is `Send + Sync` (verified by `assert_send_sync::<DictCache>()` structural test).
- `insert_for_test` and `get_or_load_no_env` are `#[cfg(test)]`-only helpers.

### Task 2: 0x01 decode path + size ceiling (LMDB-08)

Added two public functions:
- `decode_event_payload_with_cache(raw, dict_cache, env)` — main entrypoint for Phase 3 scan loops.
- `decode_event_payload_with_cache_and_limit(raw, dict_cache, env, max_decompressed)` — internal; exposes ceiling as a param for the over-ceiling test.

Added `pub const MAX_EVENT_DECOMPRESSED_SIZE: usize = 4 * 1024 * 1024`.

0x01 dispatch:
1. Guard `raw.len() >= 5` → `TruncatedZstdPayload { len }` (T-02-06).
2. `dict_id = u32::from_le_bytes(raw[1..5])` — 4 bytes, LE (LMDB-03 gate annotation).
3. `dict_cache.get_or_load(dict_id, env)?` — lazy; errors on miss.
4. `Decompressor::with_prepared_dictionary(&dd)?.decompress(frame, MAX_EVENT_DECOMPRESSED_SIZE)?`.
5. `serde_json::from_slice(&decompressed)` → `DecodedEvent { event, raw_json: decompressed }`.

The original `decode_event_payload` (Plan 01, 0x00-only) continues to return `UnknownTypeTag` for 0x01 — no breaking change to existing callers.

## Tests Added (14 total payload tests)

| Test | Coverage |
|------|----------|
| `test_dict_cache_send_sync` | D-10 structural: DictCache: Send + Sync compiles |
| `test_dict_cache_missing_dict_returns_dict_not_found` | DictNotFound / SubDbNotFound on fixture miss |
| `test_dict_cache_hit_returns_cached_entry` | Cache hit returns same Arc::ptr_eq (D-09) |
| `test_decode_0x01_truncated_returns_error` | TruncatedZstdPayload for len=4 (T-02-06) |
| `test_decode_0x01_unknown_dict_returns_dict_not_found` | DictNotFound with empty cache (T-02-07) |
| `test_decode_0x01_synthetic_round_trip` | LMDB-08: compress+decode; kind/created_at/content + raw_json == original |
| `test_decode_0x01_over_ceiling_returns_error` | Err on 1-byte ceiling (T-02-05, no panic) |

All 5 Plan 01 payload tests still pass. Full suite: 38 tests pass.

## Deviations from Plan

None — plan executed exactly as written.

The one structural choice exercised under "Claude's discretion": `decode_event_payload_with_cache` takes `(raw, dict_cache, env)` rather than splitting into a pure cache-lookup variant. This is the more direct mapping of Pattern 4 from the RESEARCH doc and avoids a proliferation of function signatures. Tests that pre-populate via `insert_for_test` pass a fixture `env` that is never actually used (fast path returns before opening any txn).

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced.
All threat model mitigations from `<threat_model>` in the plan are implemented and tested:

| Threat | Mitigation | Evidence |
|--------|-----------|---------|
| T-02-05 decompression bomb | `decompress(frame, MAX_EVENT_DECOMPRESSED_SIZE)` returns Err | `test_decode_0x01_over_ceiling_returns_error` passes |
| T-02-06 truncated payload | `raw.len() < 5` guard → TruncatedZstdPayload | `test_decode_0x01_truncated_returns_error` passes |
| T-02-07 unknown dictId | `get_or_load` → DictNotFound | `test_decode_0x01_unknown_dict_returns_dict_not_found` passes |
| T-02-09 read-only LMDB | No `.create()` calls (count=0); no `write_txn` (count=0) | `grep -c '.create(' = 0` |

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `src/lmdb/payload.rs` | FOUND |
| `02-02-SUMMARY.md` | FOUND |
| commit `6f172ba` | FOUND |
| 38 tests pass | PASS |
| `.create()` count = 0 | PASS |
| `write_txn` count = 0 | PASS |
