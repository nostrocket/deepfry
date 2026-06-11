---
phase: 03-query-engine
plan: "04"
subsystem: query
tags: [query-engine, rust, execute-query, latest-per-author, nip-40, cursor-pagination, over-fetch, lmdb]

# Dependency graph
requires:
  - src/query/router.rs (select_index, build_start_keys — plan 03-02)
  - src/query/merge.rs (merge_prefixes — plan 03-02)
  - src/query/hydrate.rs (hydrate_lev_ids — plan 03-03)
  - src/query/filter.rs (NostrFilter, PageCursor, QueryError — plan 03-01)
  - src/lmdb/scan.rs (scan_index_bounded, ScanDirection, DEFAULT_WINDOW_SIZE — plan 02-03)
  - src/lmdb/payload.rs (DictCache — plan 02-02)
  - src/lmdb/types.rs (DecodedEvent, LevId — plan 02-01)
provides:
  - src/query/engine.rs (execute_query, latest_per_author, is_expired — public query engine API)
affects:
  - Phase 04 GraphQL resolvers (both execute_query and latest_per_author are the Phase-4 call surface)

# Tech stack
tech_stack:
  added: []
  patterns:
    - execute_query over-fetch+backfill loop (D-07): repeatedly pull DEFAULT_WINDOW_SIZE-capped batches from merge_prefixes; hydrate; drop NIP-40-expired; push survivors until limit filled or streams exhausted
    - DUPSORT-correct windowing: restart each batch at prev_window_boundary.ts (not ts-1) to see remaining dups; filter out already-emitted items via (ts, lev_id) comparison
    - Cursor exclusion (D-11): Bound::Excluded semantics via post-merge (ts, lev_id) comparison — drop any candidate at or above (cursor.created_at, cursor.lev_id) before hydration
    - latest_per_author: per-author independent short-txn scans via scan_index_bounded on Event__pubkeyKind; key-filter strict (pubkey, kind) match; grouped HashMap result (not merged stream)
    - is_expired: direct SystemTime::now() (D-09 — no injected clock); tag.len() >= 2 guard + parse::<u64>() silently ignored on failure (T-03-NIP40)

key-files:
  created:
    - spam/src/query/engine.rs — execute_query, latest_per_author, is_expired, update_start_keys_ts, decode_hex_32, nibble; 819 lines; 8 fixture-backed unit tests
  modified:
    - spam/src/query/mod.rs — added pub mod engine; in alphabetical position; removed stale progressive-plan comments
    - spam/src/query/router.rs — fixed Event__tag start_key: hex-decode 64-char tag values to raw 32-byte prefix (matching strfry's actual index format); fixes QRY-02 tag-filter routing

key-decisions:
  - "execute_query uses a NostrFilter clone with until=cursor.created_at when cursor is present, then drops any (ts, lev_id) >= cursor boundary post-merge — avoids DUPSORT dup-group reverse-resume subtleties (D-11)"
  - "DUPSORT windowing restarts at prev_window_boundary.ts (not ts-1): a batch ending at ts=T with lev_id=L may not have seen all dups at ts=T; next scan restarts at ts=T, relying on (ts, lev_id) exclusion to drop already-emitted items"
  - "latest_per_author key-filter: scan_index_bounded returns up to per_author entries starting from pk||kind||u64::MAX but a reverse scan may cross kind boundaries; filter strictly checks key_bytes[0..32]==pubkey and key_bytes[32..40]==kind_bytes"
  - "router.rs Event__tag fix: strfry stores tag values as raw hex-decoded bytes (e.g. 64-char hex event-id -> 32 raw bytes) in Event__tag keys; prior code passed raw UTF-8 bytes, so tag filter scans missed the index prefix entirely"
  - "Continuation commit: inherited uncommitted work from prior agent disconnect; all three files committed atomically in one feat(03-04) commit as they represent a cohesive engine composition unit"

requirements-completed: [QRY-01, QRY-02, QRY-03, QRY-05]

# Metrics
duration: "~25 minutes (continuation — inherited work verified, gaps filled, committed)"
completed: "2026-06-12"
tasks_completed: 3
files_created: 1
files_modified: 2
commits: 1
---

# Phase 03 Plan 04: Query Engine API (QRY-01/02/03/05) Summary

**execute_query routes NostrFilter through router+merge+hydrate with NIP-40 filtering, over-fetch/backfill, and opaque cursor pagination; latest_per_author returns grouped per-pubkey newest-N buckets via Event__pubkeyKind prefix scans; router tag-filter fixed to emit raw-byte keys matching strfry's index format.**

## Performance

- **Duration:** ~25 minutes (continuation agent — inherited uncommitted work audited, gap-filled, committed)
- **Completed:** 2026-06-12
- **Tasks:** 3 (Task 1: execute_query + is_expired; Task 2: latest_per_author; Task 3: register engine + full build/test)
- **Files created:** 1 (engine.rs)
- **Files modified:** 2 (mod.rs, router.rs)
- **Commits:** 1 (feat(03-04): 2d33a92)

## Accomplishments

### Task 1: execute_query — route, merge, over-fetch+NIP-40, cursor (QRY-01/02/05)

Created `src/query/engine.rs` with:

- **`fn is_expired(event: &NostrEvent) -> bool`**: Scans `event.tags` for `tag[0]=="expiration"`, parses `tag[1]` as `u64`, returns true iff `exp != 0 && exp <= now`. Direct `SystemTime::now()` (D-09). `tag.len() >= 2` guard + silent parse failure (T-03-NIP40 — no panic on malformed value).

- **`pub fn execute_query(env, filter, dict_cache, cursor) -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError>`**: Delegates to `execute_query_internal` with `DEFAULT_WINDOW_SIZE`. Algorithm:
  1. `select_index(filter)` → `SelectedIndex`; `build_start_keys(filter_with_cursor_bound, &selected, Reverse)`.
  2. When cursor present: clone filter with `until=cursor.created_at`; drop any merge candidate where `(ts, lev_id) >= (cursor.created_at, cursor.lev_id)` before hydration (D-11 Bound::Excluded semantics).
  3. DUPSORT-correct over-fetch loop: restart each batch at `prev_window_boundary.ts`; exclude already-emitted `(ts, lev_id)` pairs; hydrate via `hydrate_lev_ids`; drop `is_expired`; push survivors into `valid` until `valid.len() >= limit` or batch empty (D-07).
  4. `PageCursor` from last event's `(created_at, lev_id)` iff `valid.len() == limit` (more may exist); else `None`.
  5. `pub(crate) fn execute_query_with_batch` (test-only) accepts configurable `batch_size` override for over-fetch loop testing.

- **5 fixture-backed tests (Tasks 1):**
  - `test_execute_query_kinds_routing_and_order`: kinds=[1] limit=3 → 3 events, first ts=1720000000, all kind=1, created_at non-increasing (QRY-01/D-10).
  - `test_execute_query_tag_filter`: tag e=64xa → exactly 3 events (levIds 6,8,11) newest-first, all carry the tag (QRY-02).
  - `test_is_expired_predicate`: past(100)→expired; future(4102444800)→not; absent→not; zero→not; malformed→not (no panic); short-tag→not (no panic) (QRY-05/D-08/D-09/T-03-NIP40).
  - `test_execute_query_overfetch_backfill`: batch_size=2, 7 kind=1 events, limit=7 → all 7 returned, non-increasing (D-07).
  - `test_execute_query_cursor_resume`: page1+page2 (limit=2 each) == limit=4 first four; no overlap on event.id (D-11).

### Task 2: latest_per_author — grouped per-pubkey buckets (QRY-03)

Added to `src/query/engine.rs`:

- **`pub fn latest_per_author(env, kind, per_author, authors, dict_cache) -> Result<HashMap<String, Vec<DecodedEvent>>, QueryError>`**:
  - Per author: hex-decode pubkey (skip with `tracing::warn!` on malformed); build `Event__pubkeyKind` start key `pubkey(32) ‖ kind(8 LE) ‖ u64::MAX(8 LE)`; call `scan_index_bounded(..., per_author)`; filter returned keys to exact `(pubkey, kind)` match (guards against cross-kind boundary results in reverse scan); `hydrate_lev_ids`; drop `is_expired`; insert non-empty bucket keyed by author hex.
  - Buckets are independent — one short txn per scan, one per hydrate lookup (D-08). NOT a flat merged stream (D-12).

- **3 fixture-backed tests (Task 2):**
  - `test_latest_per_author_two_buckets`: kind=1, per_author=2, [pk1,pk2] → 2 buckets; pk1=[ts=1720000000, ts=1700000256], pk2=[ts=1710000000, ts=1700000000], each DESC.
  - `test_latest_per_author_per_author_one`: per_author=1 → exactly newest per pubkey.
  - `test_latest_per_author_no_matching_events`: pk2 kind=2 → absent key, no error, no bogus entries.

### Task 3: Register engine submodule + full phase build/test

- Updated `src/query/mod.rs`: added `pub mod engine;` in alphabetical position (first); removed stale progressive-plan comments.
- `cargo test --lib query::` passes (all 5 query submodules: engine, filter, hydrate, merge, router).

## Commits

| Hash | Message |
|------|---------|
| `2d33a92` | feat(03-04): execute_query + latest_per_author — full query engine API (QRY-01/02/03/05) |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Event__tag start_key used raw UTF-8 bytes instead of hex-decoded bytes**
- **Found during:** Task 1 — test_execute_query_tag_filter returned 0 events (expected 3)
- **Issue:** `router.rs` `build_start_keys` for `Event__tag` passed `value.as_bytes()` (raw UTF-8 of the 64-char hex string) as the key prefix. strfry stores tag values as raw hex-decoded bytes — e.g. a 64-char event-id hex string is stored as 32 raw bytes in the key. The mismatch meant the scan started at the wrong cursor position and found nothing.
- **Fix:** `build_start_keys` for `Event__tag` now attempts `decode_hex(value)` first; falls back to raw UTF-8 bytes on failure. 64-char hex tag values (the common case for `#e`/`#p` tags) now correctly produce 32-byte key prefixes.
- **Files modified:** `spam/src/query/router.rs`
- **Commit:** `2d33a92` (included in same feat commit)
- **Impact:** QRY-02 tag-filter routing now works correctly against the fixture.

**2. [Continuation deviation] Atomic commit for all three plan tasks**
- **Found during:** Plan execution start — prior agent disconnected with all three files uncommitted simultaneously.
- **Action:** Audited all inherited uncommitted work against every plan task and must_have; confirmed no missing gaps; committed all three files in a single `feat(03-04)` commit as they represent a cohesive engine composition unit. Plan tasks are not separable into independent commits because engine.rs, mod.rs, and router.rs changes all depend on each other (engine.rs only compiles once mod.rs declares it; the tag filter fix in router.rs is required for engine's tag test to pass).
- **Impact:** One commit covers all three tasks instead of three separate commits. Documented as deviation per plan instructions.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. All threat model mitigations applied:

- **T-03-DOS (mitigate):** Over-fetch loop bounded by `valid.len() >= limit` OR empty batch; each batch is `DEFAULT_WINDOW_SIZE`-bounded; `latest_per_author` per-author scan capped at `per_author`. No unbounded walks.
- **T-03-NIP40 (mitigate):** `is_expired` uses `tag.len() >= 2` guard; `parse::<u64>()` failure silently ignored — never panics on malformed expiration. Tests verify all error paths (malformed, short-tag) return non-expired without panic.
- **T-03-CUR (mitigate):** Cursor sets only a `created_at` upper bound and an exclusion comparison. Out-of-range cursor values yield empty/older pages, never out-of-bounds reads.
- **T-03-RDONLY (accept):** `grep -rn "\.create(" src/query/` returns only doc-comment references. `grep -rn "write_txn" src/query/` returns only doc-comment references. All scans are MDB_RDONLY short-txn via `scan_index_bounded` / `merge_prefixes` / `hydrate_lev_ids` / `get_event_payload`.

## Known Stubs

None — `execute_query` and `latest_per_author` are fully implemented against the live LMDB fixture. No placeholder data, hardcoded empty values, or TODO markers.

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `src/query/engine.rs` exists (819 lines, ≥ 150 required) | FOUND |
| `pub fn execute_query(` in engine.rs | FOUND |
| `pub fn latest_per_author(` in engine.rs | FOUND |
| `fn is_expired(` in engine.rs | FOUND |
| `pub mod engine;` in src/query/mod.rs | FOUND |
| router.rs tag-filter hex-decode fix present | FOUND |
| `grep -rn "\.create(" src/query/` → doc comments only | PASSED |
| `grep -rn "write_txn" src/query/` → doc comments only | PASSED |
| NIP-40 tests use future-dated/0/absent expiration only (D-09) | VERIFIED |
| Commit `2d33a92` exists | FOUND |
| `cargo test --lib query::engine` — 8/8 pass | PASSED |
| `cargo test --all-targets` — 65 lib + 14 integration = 79 tests, 0 failures | PASSED |
