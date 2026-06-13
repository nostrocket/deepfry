---
status: resolved
trigger: "Phase 3 query engine: cursor pagination strands events and can non-terminate when a single created_at second holds more events than emit_limit (fat group). Two gap-closures (03-10, 03-11) relocated the bug instead of closing it; both shipped green tests."
created: 2026-06-13
updated: 2026-06-13
phase: 03-query-engine
requirement: QRY-05
fix_direction: correct-intra-group-resume
---

# Debug Session: cursor-fat-group-stranding

## Symptoms

- **Expected behavior:** `execute_query` cursor pagination returns ALL matching events across pages in (created_at DESC, lev_id DESC) order with no stranding, and always terminates (cursor eventually becomes None). This is must-have #5 / QRY-05.
- **Actual behavior:** When a single `created_at` second holds more than `emit_limit` (≥256) events ("fat group"), the lowest `F - emit_limit` lev_ids of a group of size F are NEVER returned. Pagination ends with a false `cursor: None` (silent data loss). Separately, a fat group at `created_at == 0` causes non-termination (identical cursor re-emitted forever).
- **Error messages:** NONE — silent wrong results. No panic, no error. This is the dangerous failure mode CLAUDE.md flags as the core correctness risk ("never silently-wrong data").
- **Timeline:** Introduced by gap-closure plan 03-10 (round-loop rewrite of `execute_query_internal`), persisted through 03-11 (the ts-advance override `deepest_scanned = (stalled_ts-1, u64::MAX)` skips the whole fat group). Never worked correctly for fat groups > emit_limit.
- **Reproduction:** Query where >256 events share one created_at (proven empirically by the code reviewer): FAT_COUNT=300, BELOW=5, TOTAL=305 → only 265 events collected, 40 stranded with cursor:None. The shipped regression test `test_execute_query_fat_timestamp_pagination_no_stall` is RIGGED — it pins `FAT_COUNT == emit_limit` exactly (engine.rs ~2010), so it passes vacuously.

## Known Root Cause (from 03-VERIFICATION.md + 03-REVIEW.md — confirm empirically, do not re-derive from scratch)

The page cursor is `(created_at, lev_id)`, but `lev_id` is the LMDB **DUPSORT value**, not part of the key. `build_start_keys` positions scans by `created_at` only, and `merge_windowed` is re-invoked fresh each page capped at `emit_limit`. So the engine has no way to resume *within* a dup group below a given lev_id — it can only restart at a timestamp boundary. A single created_at holding > emit_limit events therefore cannot be correctly paginated by the current key/cursor/merge design.

The comment at engine.rs ~370 claiming these events are "architecturally unreachable" is FACTUALLY WRONG — strfry's own resume model encodes lev_id into the resume position. The limit is this engine's timestamp-only `round_until`, not a property of LMDB.

## Chosen Fix Direction (USER DECISION — locked)

**Correct intra-group resume.** Implement lev_id-aware resume via LMDB DUPSORT cursor positioning (`MDB_GET_BOTH_RANGE` / heed equivalent) so a page can continue INSIDE a fat timestamp at `lev_id < floor` within the SAME created_at — no group skipped. This is the real QRY-05 fix and matches strfry's resume model. Expect changes to scan.rs (DUPSORT-aware resume bound), merge.rs (frontier resume), engine.rs (round-loop / cursor builder), and the cursor contract (D-11) as needed. Preserve (created_at DESC, lev_id DESC) ordering (D-10).

NOT the "fail loud / FatGroupTruncated" option — the user wants correct pagination, not a bounded v1.

## Current Focus

hypothesis: CONFIRMED. The stranding is caused by timestamp-only scan resumption (round_until = ts only, no lev_id). merge_windowed returns the same top-N entries at fat_ts every round; cursor exclusion drops them, no progress, events below the emit_limit window are permanently unreachable.

heed_dupsort_positioning: heed 0.22.1 does NOT expose MDB_GET_BOTH (cursor.rs has no get_both/move_on_key_and_value). DUPSORT value-level positioning cannot be done via standard heed range API.

fix_approach: Pass lev_id_floor: Option<(u64, LevId)> to merge_windowed. In refill_stream, after the prefix guard, filter out entries where (ts == floor_ts && lev_id >= floor_lev). Engine passes round_boundary as the floor. This achieves intra-group resume without DUPSORT cursor ops.

reasoning_checkpoint:
  hypothesis: "entries at fat_ts with lev_id < round_boundary.lev_id are unreachable because merge_windowed caps at emit_limit and always returns the same top-N lev_ids; cursor exclusion then drops all of them."
  confirming_evidence:
    - "Code trace: round_until = round_boundary.ts (no lev_id). build_start_keys uses only ts. merge_windowed returns entries in DESC order capped at emit_limit: always same top-N."
    - "FAT_COUNT=300, emit_limit=260: after 130 rounds all 260 reachable events emitted; 40 with lev_id < 41 at fat_ts are below the scan window, unreachable."
    - "no-progress break fires (last_merged == round_boundary) with deepest_scanned advanced to (fat_ts-1, MAX), skipping the remaining 40 events."
    - "03-REVIEW.md independently confirmed: 265/305 collected, 40 stranded."
  falsification_test: "A test with FAT_COUNT=300, BELOW=5 collecting all 305 events would fail under current code and pass after fix."
  fix_rationale: "By filtering entries at floor_ts with lev_id >= floor_lev inside merge_windowed, the merge skips already-seen entries and exposes entries with lower lev_ids at the same ts. No LMDB cursor positioning needed — pure value filtering in the loaded batch."
  blind_spots: "DUPSORT ORDER: lev_ids are MDB_INTEGERDUP — they're iterated in ascending order within a dup group. Reverse scan yields them in descending lev_id order, which is what we want (emit highest lev_id first, floor filters out the already-emitted ones)."

status: fixing
next_action: Apply fix to merge.rs (add lev_id_floor param to merge_windowed and refill_stream), update engine.rs to pass round_boundary as floor, de-rig the existing test to FAT_COUNT=300, add ts=0 non-termination test.

## Evidence

- timestamp: 2026-06-13 — 03-REVIEW.md (deep): probe test FAT_COUNT=300 collected 265/305, 40 stranded with false cursor:None. Rigged regression test pins FAT_COUNT==emit_limit.
- timestamp: 2026-06-13 — 03-VERIFICATION.md: independent code trace confirmed stranding + ts=0 non-termination; identified timestamp-only round_until as root cause; noted strfry encodes lev_id in resume.

## Eliminated

- hypothesis: pure no-progress break (last_merged == round_boundary → break) suffices — ELIMINATED in 03-11: creates infinite cross-page stall when fat_count ≥ emit_limit.
- hypothesis: ts-advance override (deepest_scanned = (stalled_ts-1, u64::MAX)) suffices — ELIMINATED: skips the whole fat group, stranding its lowest F-emit_limit lev_ids; plus ts=0 non-termination.

## Resolution

root_cause: >
  The engine's round_until passed only the created_at timestamp to build_start_keys / merge_windowed,
  with no lev_id component. For a fat created_at group (F > emit_limit events sharing one timestamp),
  merge_windowed always returned the same top-N (emit_limit) lev_ids regardless of which ones
  cursor-exclusion had already rejected. After emit_limit rounds, the cursor floor reached the
  lowest reachable lev_id, cursor-exclusion dropped all merge output, last_merged == round_boundary
  (no-progress break fired), deepest_scanned advanced to (ts-1, MAX) skipping the remaining
  F - emit_limit events. For the ts=0 variant the deepest_scanned guard left it unchanged,
  causing the same cursor to re-emit forever.

fix: >
  Added lev_id_floor: Option<(u64, LevId)> parameter to merge_windowed and refill_stream (merge.rs).
  When set to round_boundary, the floor filter drops entries with (ts == floor_ts && lev_id >= floor_lev)
  from each batch, exposing lower lev_ids within the same fat dup group. Engine.rs passes round_boundary
  as lev_id_floor on every merge_windowed call. When the floor drops an entire window (all fat_ts dups
  exhausted), refill_stream loops once more (up to 2 attempts) issuing a second window past the floor
  key, reaching entries at ts < floor_ts. The no-progress break is retained as dead-code safety net
  but no longer fires for fat groups. heed 0.22.1 does NOT expose MDB_GET_BOTH, so the pure-filter
  approach was necessary.

verification: >
  - test_execute_query_fat_timestamp_pagination_exceeds_emit_limit: FAT_COUNT=300 > emit_limit=260,
    BELOW=5. Collects 305/305, no duplicates. (Old code: 265/305.) PASSES.
  - test_execute_query_fat_timestamp_at_ts_zero_terminates: 10 events at ts=0. Terminates, collects
    all 10. (Old code: infinite loop.) PASSES.
  - test_execute_query_fat_timestamp_pagination_at_limit_boundary: FAT_COUNT=260=emit_limit, BELOW=5.
    (Previously rigged; now legitimate regression guard.) PASSES (265/265).
  - Full suite: 94/94 lib tests GREEN + 4 integration tests + 1 doc test. cargo test passes.

files_changed:
  - src/query/merge.rs
  - src/query/engine.rs

orchestrator_independent_verification: >
  Confirmed the fix is robust beyond the shipped tests (not just rubber-stamped — the two prior
  "green" fixes were actually broken). Wrote a temporary probe with FAT_COUNT=800 (>> the 256
  production window) paginated via the PRODUCTION path (execute_query → DEFAULT_WINDOW_SIZE=256),
  not the batch_size=2 harness paginate_all uses: collected 805/805, no duplicates, terminated.
  Probe reverted; full suite re-confirmed green (110 tests). The resume_key mechanism descends
  correctly through an arbitrarily fat dup group across pages even under the production window.

coverage_gap_noted: >
  All shipped fat-group regression tests paginate with batch_size=2 (paginate_all hardcodes
  execute_query_with_batch(..., 2)), NOT the production DEFAULT_WINDOW_SIZE=256. The fix is
  empirically correct under production batch size (probe above), but a PERMANENT regression test
  exercising the production window would harden against future window/floor-interaction regressions.
  Recommend adding one when convenient (non-blocking — correctness is verified).
