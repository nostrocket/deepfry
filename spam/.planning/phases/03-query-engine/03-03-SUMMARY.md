---
phase: 03-query-engine
plan: "03"
subsystem: query
tags: [query-engine, rust, hydration, lmdb, nostr-event, skip-warn-count]

# Dependency graph
requires:
  - src/lmdb/payload.rs (get_event_payload, decode_event_payload_with_cache, DictCache)
  - src/lmdb/types.rs (DecodedEvent, LevId)
  - src/query/filter.rs (QueryError — from plan 03-01)
provides:
  - src/query/hydrate.rs (hydrate_lev_ids)
affects:
  - src/query/mod.rs (pub mod hydrate; added)

# Tech stack
tech_stack:
  added: []
  patterns:
    - get_event_payload short-txn per levId (D-08 — no txn held across levIds)
    - decode_event_payload_with_cache + skip-warn-count (D-11 — corrupt payloads skipped, not fatal)
    - QueryError::Payload propagation for structural LevIdNotFound (hard error, not a skip)
    - Input-order preservation (D-10 — ordering is the merge's responsibility)

# Key files
key_files:
  created:
    - spam/src/query/hydrate.rs — hydrate_lev_ids with fixture-backed tests; 173 lines; 3 unit tests
  modified:
    - spam/src/query/mod.rs — added pub mod hydrate; in alphabetical position

# Key decisions
decisions:
  - LevIdNotFound propagated as QueryError::Payload (hard error): a levId from a real index scan missing in EventPayload is structural corruption — silently skipping it would hide data loss, so it aborts the batch with Err
  - Decode failures (corrupt payload bytes) use skip-warn-count (D-11): tracing::warn! with lev_id + reason structured fields, *skip_count += 1, continue — consistent with decode_payload_skip_on_error in payload.rs
  - No txn held across levIds: each get_event_payload call owns its own short RoTxn (D-08 invariant); hydrate_lev_ids never touches heed directly
  - Rule 1 fix: initial test assertions used wrong levId→event-id mapping (seed file insertion order != LMDB levId assignment order); corrected from Event__id.json golden vectors

# Metrics
metrics:
  duration: "~8 minutes"
  completed: "2026-06-12"
  tasks_completed: 2
  files_created: 1
  files_modified: 1
  commits: 1
---

# Phase 03 Plan 03: Hydration Step (QRY-04) Summary

**One-liner:** hydrate_lev_ids point-looks-up EventPayload[levId] for each selected levId via get_event_payload + decode_event_payload_with_cache; skips corrupt payloads with warn+count, propagates LevIdNotFound as hard error; preserves input order in short per-lookup txns (D-06/D-08/D-11).

## What Was Built

The hydration step that turns merge-selected levIds into DecodedEvents. Sits between the merge layer (03-02) and the engine over-fetch loop (03-04). Decodes ONLY the finally-selected levIds (D-06).

### Task 1: hydrate_lev_ids — point-lookup + decode + skip-warn-count (TDD)

Created `src/query/hydrate.rs` with:

- **`pub fn hydrate_lev_ids(env: &heed::Env, lev_ids: &[LevId], dict_cache: &DictCache, skip_count: &mut usize) -> Result<Vec<DecodedEvent>, QueryError>`**:
  - For each `&lev_id` in `lev_ids`: calls `get_event_payload(env, lev_id)?` — a short read txn is opened, raw bytes are copied out, txn drops (D-08). LevIdNotFound → `QueryError::Payload(..)` propagated immediately (not a skip — structural error).
  - `decode_event_payload_with_cache(&raw, dict_cache, env)`: on `Ok(decoded)` pushes to results; on `Err(e)` logs `tracing::warn!(lev_id, reason = %e, "skipping undecodable EventPayload in hydrate batch")`, increments `*skip_count`, continues (D-11).
  - Returns DecodedEvents in input order (D-10 — ordering is the merge's responsibility).
  - Hydrates only the provided levIds, no extra reads (D-06).
- **Three fixture-backed unit tests:**
  - `test_hydrate_three_lev_ids_in_order_zero_skips`: levIds [4,5,6] → 3 DecodedEvents with correct event.id values from Event__id.json golden vectors; skip_count=0.
  - `test_nonexistent_lev_id_propagates_as_payload_error`: levId=9999 → `Err(QueryError::Payload(..))` returned; skip_count stays 0 (not a skip).
  - `test_hydrate_preserves_input_order`: levIds [6,4] → events in that exact order (6 before 4); verifies no reordering.

### Task 2: Register hydrate submodule

Updated `src/query/mod.rs`:
- Added `pub mod hydrate;` in alphabetical order between `filter` and `merge`
- Updated comment from "plan 03-03 (TODO)" to indicate completion

## Commits

| Hash | Message |
|------|---------|
| `0996db8` | feat(03-03): hydrate_lev_ids — point-lookup + decode + skip-warn-count (QRY-04) |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Wrong levId→event-id mapping in initial test assertions**
- **Found during:** Task 1 — TDD RED phase (cargo test --lib query::hydrate)
- **Issue:** Initial test assertions mapped levId=4 → `2c4ca2cb...` and levId=6 → `b06bbf08...`, assuming LMDB assigns levIds in seed file insertion order. The fixture actually assigns levIds in a different order — `Event__id.json`'s `event_ids_in_order` is the authoritative mapping.
- **Actual mapping:** levId=4 → `ee5e90b9...` (kind=1, pk2), levId=5 → `4d401c51...` (kind=1, pk1), levId=6 → `ae9bd395...` (kind=1, pk1).
- **Fix:** Corrected all three test assertions (`test_hydrate_three_lev_ids_in_order_zero_skips`, `test_hydrate_preserves_input_order`) to use values from `tests/fixture/golden_vectors/Event__id.json`.
- **Files modified:** `src/query/hydrate.rs` (test assertion values corrected)
- **Impact:** Tests now reflect actual fixture data and correctly validate hydration behavior.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. All threat model mitigations applied:
- **T-03-PAY (mitigate):** Corrupt/undecodable payload caught at `decode_event_payload_with_cache` return, logged with `tracing::warn!(lev_id, reason = %e, ...)`, counted in `skip_count`, skipped — batch continues (D-11). Verified by code review (no panic path on malformed input — Phase-2 guarantee).
- **T-03-DOS (accept):** hydrate_lev_ids hydrates exactly the levIds handed to it; the merge/engine layer bounds that count by `limit` (upstream in 03-02 / 03-04). Verified: no extra reads.
- **T-03-RDONLY (accept):** Hydration delegates entirely to `get_event_payload` which opens MDB_RDONLY short txns and never `.create()`s; no write path introduced. Verified: hydrate.rs never calls env.write_txn() or database_options().create().

## Known Stubs

None — hydrate_lev_ids is fully implemented. The plan 03-04 comment in mod.rs (`// engine: plan 03-04 (TODO)`) is intentional scaffolding.

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `src/query/hydrate.rs` exists | FOUND |
| `pub fn hydrate_lev_ids(` in hydrate.rs | FOUND |
| `get_event_payload` called in hydrate.rs | FOUND |
| `decode_event_payload_with_cache` called in hydrate.rs | FOUND |
| `tracing::warn!` with lev_id + reason fields on decode error | FOUND |
| `pub mod hydrate;` in src/query/mod.rs | FOUND |
| hydrate.rs ≥ 40 lines (173) | FOUND |
| Commit `0996db8` exists | FOUND |
| `cargo test --lib query::hydrate` — 3/3 pass | PASSED |
| `cargo build --lib` succeeds | PASSED |
| `cargo test --lib` — 57/57 pass | PASSED |
