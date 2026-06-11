---
phase: 03-query-engine
plan: "01"
subsystem: query
tags: [query-engine, rust, nostr-filter, pagination-cursor, thiserror, base64]

# Dependency graph
requires:
  - lmdb/indexes.rs (IndexError — #[from] in QueryError::Lmdb)
  - lmdb/payload.rs (PayloadError — #[from] in QueryError::Payload)
provides:
  - src/query/filter.rs (NostrFilter, TagFilter, PageCursor, QueryError)
  - src/query/ module wired into crate root
affects:
  - src/lib.rs (pub mod query added)
  - Cargo.toml (base64 = "0.22" added)

# Tech stack
tech_stack:
  added:
    - base64 = "0.22" — cursor encode/decode for PageCursor (D-11 opaque pagination cursor)
  patterns:
    - thiserror #[from] error composition (mirrors IndexError house style)
    - base64::engine::general_purpose::STANDARD + Engine as _ idiom
    - fail-closed decode: length guard + base64 error → QueryError::CursorDecode

# Key files
key_files:
  created:
    - spam/src/query/mod.rs — module declarations (pub mod filter; engine/hydrate/merge/router in later plans)
    - spam/src/query/filter.rs — NostrFilter, TagFilter, PageCursor, QueryError + four unit tests
  modified:
    - spam/src/lib.rs — added pub mod query;
    - spam/Cargo.toml — added base64 = "0.22"

# Key decisions
decisions:
  - base64 0.22 chosen for PageCursor encode/decode (D-11); long-established crate, no legitimacy checkpoint required per plan threat model (T-03-SC)
  - Only pub mod filter declared in mod.rs; engine/hydrate/merge/router added as later plans land (per plan task 1 instructions)
  - QueryError wraps IndexError and PayloadError via #[from] for clean propagation; CursorDecode is the standalone fail-closed boundary

# Metrics
metrics:
  duration: "~7 minutes"
  completed: "2026-06-11"
  tasks_completed: 2
  files_created: 2
  files_modified: 2
  commits: 2
---

# Phase 03 Plan 01: Query Module + Contract Types Summary

**One-liner:** Query module skeleton with NostrFilter/TagFilter/PageCursor/QueryError types; base64 cursor encode/decode via STANDARD codec, fail-closed on malformed input (T-03-CUR).

## What Was Built

Phase 3's contract-first foundation: the `src/query/` module wired into the crate root, plus all engine-facing types that subsequent plans (router, merge, hydrate, engine) build against.

### Task 1: Wire query module + add base64 dependency

- Added `pub mod query;` to `src/lib.rs` alongside existing `lmdb` and `config` modules
- Created `src/query/mod.rs` with `pub mod filter;` and a comment listing the remaining submodules (engine/hydrate/merge/router) added in plans 03-02..03-04
- Added `base64 = "0.22"` to `Cargo.toml` for the `PageCursor` encode/decode implementation (D-11)

### Task 2: Define filter/cursor/error contract types in filter.rs (TDD)

Created `src/query/filter.rs` with:

- **`NostrFilter`** (`#[derive(Debug, Clone, Default)]`): all-optional fields `ids/authors/kinds/tags/since/until/limit`, with D-02 routing doc-comments. `since`/`until` are scan-bounds (D-03); `ids/authors/kinds/tags` become residual predicates or index-selection drivers (D-01/D-02).
- **`TagFilter`** (`#[derive(Debug, Clone)]`): `name: String` + `values: Vec<String>` for `#<tag_name>` predicates matching `tags[i][0]`/`tags[i][1]` (QRY-02).
- **`PageCursor`** (`#[derive(Debug, Clone)]`): `created_at: u64` + `lev_id: u64`; opaque D-11 pagination cursor.
  - `encode(&self) -> String`: `base64::STANDARD.encode(created_at.to_le_bytes() ‖ lev_id.to_le_bytes())` — 16 raw bytes → 24-char base64 string.
  - `decode(s: &str) -> Result<Self, QueryError>`: base64 decode → length check (must be 16) → two LE u64 reads. Returns `QueryError::CursorDecode` on any malformed input; never panics (T-03-CUR).
- **`QueryError`** (`#[derive(Debug, thiserror::Error)]`): `Lmdb(#[from] IndexError)`, `Payload(#[from] PayloadError)`, `CursorDecode { reason: String }`. Mirrors `IndexError` thiserror house style from `indexes.rs`.
- **Four unit tests** in `#[cfg(test)] mod tests`: `NostrFilter::default()` all-None, `PageCursor` round-trip, malformed base64 → CursorDecode, wrong-length base64 → CursorDecode.

## Commits

| Hash | Message |
|------|---------|
| `e66469f` | chore(03-01): wire query module and add base64 dependency |
| `7de3faf` | feat(03-01): define NostrFilter/TagFilter/PageCursor/QueryError contract types |

## Deviations from Plan

### Execution Environment Deviation

**1. [Rule 3 - Environment] `cargo` not available in execution PATH**
- **Found during:** Task 2 verification (after implementation)
- **Issue:** `cargo` binary is not installed or not on PATH in the current agent execution environment. Exhaustive search of filesystem found no cargo binary. Previous plans ran cargo successfully; this appears to be an environment difference at execution time.
- **Fix:** Code was reviewed manually against:
  - The `base64 0.22` API (`Engine::encode`/`Engine::decode` via `base64::engine::general_purpose::STANDARD`)
  - The `thiserror` patterns in existing codebase (`#[from]` composition, named struct variants)
  - The four test behaviors specified in the plan: all pass by logical code review
  - The `filter.rs` artifact criteria: `>80 lines`, required structs/enums/methods all present
- **Impact:** `cargo test --lib query::filter` could not be run to verify the four behavior tests pass at runtime. Tests are present and logically correct; they must be verified on first `cargo test` run.
- **To validate:** `cargo test --lib query::filter` — expected: 4 tests pass (no fixture env needed; pure-function tests).
- **Files not modified:** No workaround code was added.

## Threat Surface Scan

No new network endpoints, auth paths, file access patterns, or schema changes introduced. `PageCursor::decode` is the only new trust boundary: untrusted base64 string from a Phase-4 caller → fail-closed via `QueryError::CursorDecode` (T-03-CUR mitigated as specified).

## Known Stubs

None — all types are fully defined and functional. The `src/query/mod.rs` `// engine/hydrate/merge/router added in plans 03-02..03-04` comment is intentional scaffolding, not a stub.

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `src/query/filter.rs` exists | FOUND |
| `src/query/mod.rs` exists | FOUND |
| Commit `e66469f` (Task 1) exists | FOUND |
| Commit `7de3faf` (Task 2) exists | FOUND |
| `03-01-SUMMARY.md` exists | FOUND |
