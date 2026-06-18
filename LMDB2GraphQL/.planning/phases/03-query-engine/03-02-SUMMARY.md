---
phase: 03-query-engine
plan: "02"
subsystem: query
tags: [query-engine, rust, index-selection, k-way-merge, router, merge, lmdb]

# Dependency graph
requires:
  - src/query/filter.rs (NostrFilter, TagFilter — from plan 03-01)
  - src/lmdb/scan.rs (scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE)
  - src/lmdb/indexes.rs (IndexError)
  - src/lmdb/types.rs (LevId)
provides:
  - src/query/router.rs (select_index D-02, build_start_keys D-03, created_at_from_key D-06)
  - src/query/merge.rs (MergeCandidate + Ord impl, merge_prefixes D-05/D-10)
affects:
  - src/query/mod.rs (pub mod merge; pub mod router; added)

# Tech stack
tech_stack:
  added:
    - inline hex decode (decode_hex + nibble) — no external hex crate needed; 32-byte pubkey/id decoding for key construction
  patterns:
    - BinaryHeap max-heap for k-way merge (stdlib std::collections::BinaryHeap)
    - Manual Ord impl on (created_at DESC, lev_id DESC) for newest-first emission
    - saturating_sub + try_into().unwrap_or for safe key byte extraction (T-03-HEX)
    - Per-prefix stream materialization via Vec<IntoIter<(Vec<u8>, LevId)>>
    - Inline hex decode to avoid external dependency footgun

# Key files
key_files:
  created:
    - spam/src/query/router.rs — select_index (D-02), build_start_keys (D-03), created_at_from_key (D-06); 496 lines; 6 unit tests
    - spam/src/query/merge.rs — MergeCandidate + Ord + PartialOrd, merge_prefixes (D-05/D-10); 340 lines; 5 unit tests
  modified:
    - spam/src/query/mod.rs — added pub mod merge; pub mod router; in alphabetical order; updated comment to track remaining plans

# Key decisions
decisions:
  - No external hex crate: implemented inline decode_hex + nibble functions in router.rs — avoids new dependency legitimacy concern; 32-byte pubkey/id decoding is simple enough inline
  - Per-prefix scan materialized before merge (v1): each scan_index_bounded result collected into Vec<IntoIter> before heap seeding — simple and correct; bounded by limit (T-03-DOS); lazy streaming is a future optimization
  - merge.rs holds NO transaction: delegates entirely to scan_index_bounded which owns its own short RoTxn per call (D-08)
  - Test assertion bug caught and fixed (Rule 1): test_merge_candidate_ord_newest_first had incorrect heap-pop assertion for same-ts tie-break order; fixed to correctly assert lev_id=10 before lev_id=8 before lev_id=5

# Metrics
metrics:
  duration: "~25 minutes"
  completed: "2026-06-11"
  tasks_completed: 3
  files_created: 2
  files_modified: 1
  commits: 2
---

# Phase 03 Plan 02: Routing + Merge Core Summary

**One-liner:** Index selection (D-02 fixed priority) + per-prefix composite key construction (D-03 time-bound pushdown) + k-way heap merge over per-prefix reverse scans on key bytes alone (D-05/D-06/D-10 — no hydration).

## What Was Built

The shared routing + merge machinery that both the flat `events()` stream and `latestPerAuthor` reuse. Defines the two modules that sit between the filter input and the hydration step.

### Task 1: Index selection + per-prefix start_key construction (router.rs, TDD)

Created `src/query/router.rs` with:

- **`SelectedIndex`** enum: `Single(&'static str)` (one scan spans the whole index) or `Multi(&'static str)` (one scan per value-prefix, fan-out required per D-05)
- **`select_index(filter: &NostrFilter) -> SelectedIndex`**: D-02 fixed priority chain exactly:
  - `ids.is_some()` → `Single("Event__id")`
  - `authors.is_some() && kinds.is_some()` → `Multi("Event__pubkeyKind")`
  - `authors.is_some()` → `Multi("Event__pubkey")`
  - `kinds.is_some()` → `Multi("Event__kind")`
  - `tags.is_some()` → `Multi("Event__tag")`
  - else → `Single("Event__created_at")` (D-04 default global feed)
- **`build_start_keys(filter, selected, direction) -> Vec<Vec<u8>>`**: produces one composite start_key per value-prefix with D-03 time-bound pushdown:
  - `Reverse` → `filter.until.unwrap_or(u64::MAX)` as trailing `created_at(8 LE)` bytes
  - `Forward` → `filter.since.unwrap_or(0)` as trailing bytes
  - Per-index layouts: Event__id = id(32)‖ts(8); Event__pubkey = pubkey(32)‖ts(8); Event__kind = kind(8)‖ts(8); Event__pubkeyKind = pubkey(32)‖kind(8)‖ts(8); Event__tag = tag_name(1)‖tag_value(var)‖ts(8); Event__created_at = ts(8 plain)
  - Malformed hex → `tracing::warn!` + skip (T-03-HEX); never panics
- **`created_at_from_key(key: &[u8]) -> u64`**: trailing 8 bytes as LE u64; `saturating_sub(8)` + `try_into().unwrap_or([0u8;8])` — returns 0 on short keys, never panics (T-03-HEX)
- **`decode_hex` + `nibble`**: inline hex decoding — no external crate
- **6 unit tests**: all six D-02 arms, Event__kind 16-byte layout with until bound, Event__pubkeyKind 48-byte layout, short-key safety, fixture smoke test (pk1 scan with limit=5 returns 5 events in non-increasing ts order)

### Task 2: K-way merge over per-prefix reverse scans (merge.rs, TDD)

Created `src/query/merge.rs` with:

- **`MergeCandidate`**: `{created_at: u64, lev_id: LevId, key_bytes: Vec<u8>, stream_idx: usize}` with manual `Ord` impl: `created_at.cmp().then(lev_id.cmp())` — max-heap pops newest first (D-10)
- **`merge_prefixes(env, short_name, start_keys, limit) -> Result<Vec<(u64, LevId, Vec<u8>)>, IndexError>`**:
  - Per prefix: `scan_index_bounded(env, short_name, Reverse, key, scan_limit)` → materialized `IntoIter`
  - Heap seeded with first candidate per stream
  - Loop: pop max → emit → pull next from that stream → stop at limit
  - No txn held — each `scan_index_bounded` owns its short `RoTxn`
  - `created_at` extracted via `created_at_from_key` on key bytes — no `EventPayload` read (D-06)
- **5 unit tests**: MergeCandidate Ord direct assertions, heap ordering correctness, single-prefix merge == direct reverse scan, multi-prefix non-increasing created_at with length ≤ limit, compile-time proof that no payload imports exist

### Task 3: Register router + merge submodules

Updated `src/query/mod.rs`:
- Added `pub mod merge;` and `pub mod router;` in alphabetical order
- Updated comment to track remaining plans (hydrate=03-03, engine=03-04)
- Note: mod.rs was updated in Task 1 commit (both submodules added together)

## Commits

| Hash | Message |
|------|---------|
| `d7b2e35` | feat(03-02): implement index selection + per-prefix start_key construction (router.rs) |
| `0d22974` | feat(03-02): implement k-way merge over per-prefix reverse scans (merge.rs) |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `hex` crate not in Cargo.toml**
- **Found during:** Task 1 implementation
- **Issue:** Plan references `hex::decode()` in the key construction code but the `hex` crate was not in Cargo.toml. Adding a new package requires legitimacy verification (deviation rule 3 exclusion).
- **Fix:** Implemented inline `decode_hex` + `nibble` functions in router.rs. The implementation is straightforward (two nibbles per byte) and avoids adding a new dependency entirely.
- **Files modified:** `src/query/router.rs` (inline decode_hex function added)
- **Impact:** No behavior change; same security guarantees (T-03-HEX: malformed hex → warn + skip)

**2. [Rule 1 - Bug] Incorrect heap-pop assertion in test_merge_candidate_ord_newest_first**
- **Found during:** Task 2 test execution
- **Issue:** The test had `assert_eq!(popped2.lev_id, 8)` but the remaining candidates after the first pop (ts=1720000000) were `b=(1700000000,10), c=(1700000000,8), d=(1700000000,5)`. At equal ts=1700000000, lev_id descends as 10→8→5, so the second pop is lev_id=10, not lev_id=8.
- **Fix:** Corrected assertion to `assert_eq!(popped2.lev_id, 10)` with corrected sequence 10→8→5. Added a clearer comment explaining the expected ordering.
- **Files modified:** `src/query/merge.rs` (test assertion corrected)

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. All mitigations in the plan's threat register are applied:
- **T-03-DOS**: Each per-prefix scan bounded by `limit` (or `DEFAULT_WINDOW_SIZE` when limit==0); heap emits at most `limit` results — verified by Test 3 in merge (length ≤ limit)
- **T-03-HEX**: Malformed hex → `tracing::warn!` + skip (not panic); `created_at_from_key` uses `saturating_sub` + `try_into().unwrap_or` — verified by router Test 4 (short-key test)
- **T-03-RDONLY**: No `.create()`, no write_txn — merge delegates entirely to `scan_index_bounded`; router builds byte slices only (no LMDB access); verified by grep

## Known Stubs

None — all functions are fully implemented. The trailing comments `// hydrate: plan 03-03 (TODO)` and `// engine: plan 03-04 (TODO)` in `mod.rs` are intentional scaffolding for future plans.

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `src/query/router.rs` exists | FOUND |
| `src/query/merge.rs` exists | FOUND |
| `fn select_index(` in router.rs | FOUND |
| `fn build_start_keys(` in router.rs | FOUND |
| `fn created_at_from_key(` in router.rs | FOUND |
| `struct MergeCandidate` in merge.rs | FOUND |
| `pub fn merge_prefixes(` in merge.rs | FOUND |
| router.rs ≥ 120 lines (496) | FOUND |
| merge.rs ≥ 80 lines (340) | FOUND |
| Commit `d7b2e35` (Task 1) exists | FOUND |
| Commit `0d22974` (Task 2) exists | FOUND |
| `cargo test --lib` 54/54 pass | PASSED |
| No `.create()` in merge.rs | VERIFIED |
| No write_txn in merge.rs | VERIFIED |
| No `payload` import in merge.rs | VERIFIED |
