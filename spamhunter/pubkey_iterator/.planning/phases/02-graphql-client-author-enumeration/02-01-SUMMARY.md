---
phase: 02-graphql-client-author-enumeration
plan: 01
subsystem: spamhunter/pubkey_iterator — store
tags: [store, single-writer, rusqlite, resumability, tdd]
requires:
  - "Phase-1 store (Store::open, begin_run, persist, reader, close; WriteMsg-free Persist path)"
provides:
  - "WriteMsg enum (Score(Persist) | Pubkeys(Vec<String>)) — the D-11 single-writer extension point"
  - "Store::insert_pubkeys — idempotent pubkey-dimension inserts through the single writer"
  - "Store::latest_unfinished_run / set_run_cursor / set_run_max_lev_start / set_run_max_lev_end / mark_run_aborted / mark_run_done"
affects:
  - "Plan 02 (enumerator) persists pubkeys via insert_pubkeys and drives resume via the run-state helpers"
  - "Phase 3 fetch payload becomes an additive WriteMsg variant"
tech-stack:
  added: []
  patterns:
    - "Enum-message writer channel (flume::Sender<WriteMsg>) preserves single-writer ordering across pubkey + score writes"
    - "Short-lived write connection (run_write_conn) for run-row UPDATEs only — never races the actor on overlapping rows"
key-files:
  created: []
  modified:
    - src/model.rs
    - src/store/writer.rs
    - src/store/mod.rs
decisions:
  - "Chose the enum-message route (RESEARCH Open Q1 / A5) over short-lived-conn pubkey inserts: routes both pubkey inserts and (future) run-state through one ordered channel, avoiding the Pitfall-2 cursor-ahead-of-data race and keeping Phase-3 additive."
  - "run-state UPDATEs use the sanctioned begin_run short-lived-write template (they touch only the run row, never pubkey/score/signal), so they do not violate the single-writer invariant for the actor's tables."
metrics:
  duration: ~15m
  completed: 2026-06-25
status: complete
---

# Phase 02 Plan 01: Store run-state + pubkey-insert API Summary

Widened the Phase-1 single-writer store channel from a bare `Persist` to a `WriteMsg` enum and added the resume/abort/drift surface (`insert_pubkeys` + six `run`-state helpers) that the plan-02 enumerator persists through — reusing the existing `UPSERT_PUBKEY` const and `run` schema with no migration and no second write connection.

## What was built

- **`WriteMsg` enum (src/model.rs):** `Score(Persist)` wraps the unchanged Phase-1 payload; `Pubkeys(Vec<String>)` carries the D-04 enumeration write. Derives `Debug, Clone, PartialEq` per the model.rs convention. Documented as the D-11 additive extension point.
- **Writer actor (src/store/writer.rs):** `writer_loop` now receives `flume::Receiver<WriteMsg>` and `match`es each drained message. `Score` runs the existing `up_pubkey`/`up_score`/`up_signal` sequence verbatim; `Pubkeys` runs only `up_pubkey` (the `UPSERT_PUBKEY` INSERT OR IGNORE) per pk. Same `BATCH=8192` greedy-drain / `prepare_cached` / single-transaction machinery — mixed messages buffer into one `Vec<WriteMsg>` and commit in one transaction.
- **Store API (src/store/mod.rs):** `tx`/`open` channel widened to `WriteMsg`; `persist` wraps in `Score`; new `insert_pubkeys` sends `Pubkeys`. Six run-state helpers: `latest_unfinished_run` (reader + `query_row().optional()` over `status != 'done' ORDER BY run_id DESC LIMIT 1`, maps all 8 columns into `model::Run`), `set_run_cursor`, `set_run_max_lev_start`, `set_run_max_lev_end`, `mark_run_aborted` (preserves `last_cursor`), `mark_run_done` (records `max_lev_id_end`). Extracted `now_epoch_secs()` and `run_write_conn()` helpers; `begin_run` refactored to reuse `now_epoch_secs()`.

## Tests (TDD)

Four atomic commits across two RED→GREEN cycles. 12 store tests pass (6 pre-existing Phase-1 + 6 new):

- `insert_pubkeys_is_idempotent` — distinct pubkeys persist once; cross-message + intra-vec duplicates leave one row.
- `latest_unfinished_run_selection` — None / single / highest-id / all-done→None.
- `set_run_cursor_roundtrip`, `set_run_max_lev_roundtrip`.
- `mark_run_aborted_preserves_cursor` (D-07), `mark_run_done_sets_status_and_max_end`.

All use the temp-FILE convention (`tempfile::TempDir` + on-disk `.sqlite`), never `:memory:`.

## Verification

- `cargo test --lib store::` → 12 passed, 0 failed.
- `cargo build` → clean. `cargo clippy --lib` → no warnings.
- No `format!`-built SQL in changed files (only doc-comment matches). Every bind `params![]`/`?N` (T-02-01).
- No second write connection introduced; single-writer invariant for actor tables preserved (T-02-02).

## TDD Gate Compliance

Each task: `test(02-01)` RED commit (verified failing to compile) → `feat(02-01)` GREEN commit. Gate sequence present in git log for both tasks.

## Deviations from Plan

None functionally. Two minor consolidations within plan intent (DRY, not behavior change), tracked as Rule 1/quality:
- Extracted `now_epoch_secs()` so `begin_run` and the new `mark_run_*` helpers share one epoch-seconds source instead of duplicating the `SystemTime` block.
- Extracted `run_write_conn()` so the four run-state UPDATE helpers share the sanctioned PRAGMA+busy_timeout short-lived-write setup (the `begin_run` template) instead of repeating it five times.

## Known Stubs

None.

## Self-Check: PASSED

- src/model.rs, src/store/writer.rs, src/store/mod.rs — all present and modified.
- Commits b745273, e5bc2a7, 65100b3, b86d30f — all in git log.
