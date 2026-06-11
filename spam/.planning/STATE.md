---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Phase 2 context gathered
last_updated: "2026-06-11T10:05:00.000Z"
last_activity: 2026-06-11 -- Plan 02-01 complete (EventPayload 0x00 decode, LMDB-07)
progress:
  total_phases: 5
  completed_phases: 1
  total_plans: 7
  completed_plans: 5
  percent: 27
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-10)

**Core value:** Serve correct, rich queries over strfry's events by reading strfry's live on-disk state directly — never copying event data or indexes out of strfry, never writing to strfry's database.
**Current focus:** Phase 02 — payload-decoding-index-scan-primitives

## Current Position

Phase: 02 (payload-decoding-index-scan-primitives) — EXECUTING
Plan: 2 of 3 (Plan 02-01 complete)
Status: Executing Phase 02 — Plan 02-01 done, 02-02 next
Last activity: 2026-06-11 -- Plan 02-01 complete (EventPayload 0x00 decode, LMDB-07)

Progress: [███░░░░░░░] 27% (Phase 1 complete; Plan 02-01 done; 02-02/02-03 + 3 phases remaining)

## Performance Metrics

**Velocity:**

- Total plans completed: 6
- Average duration: ~40 min
- Total execution time: ~2.4 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-lmdb-foundation-comparator-proof | 4/4 | ~115 min | ~29 min |
| 02-payload-decoding-index-scan-primitives | 1/3 | ~20 min | ~20 min |

**Recent Trend:**

- Last 5 plans: Plan 01-02 (~35 min, 15 files, 3 commits), Plan 01-03 (~15 min, 14 files, 4 commits), Plan 01-04 (~35 min, 4 files, 4 commits), Plan 02-01 (~20 min, 6 files, 3 commits)
- Trend: Consistent ~20-40 min/plan

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Init: Approach B chosen (query strfry live indexes; zero replication)
- Init: Rust stack — `heed` 0.22.1 (custom comparator crux), `async-graphql` 7.2.1, `axum` 0.8.x, `zstd` 0.13.x
- Init: Phase 1 is a de-risking spike — if `heed` cannot register custom comparators, approach must be revisited before any further build
- Plan 01-01 Task 3: Go/no-go gate GREEN — heed registers golpe foreign comparator (proven by adversarial smoke test; golpe order ≠ memcmp order)
- Plan 01-01 Task 4 (GO/PROCEED): Approach B decision confirmed; heed 0.22.1 upgrade completed; comparator proof re-verified on pinned version; plans 01-02 and 01-03 unblocked
- Plan 01-01: serde_yaml_ng 0.10 deferred; serde_yaml 0.9 used; gated by plan 01-02 Task 1 package legitimacy checkpoint
- Plan 01-02 Task 1 (APPROVED 2026-06-10): crate-legitimacy human-verify gate — all 10 deps confirmed canonical; Cargo.lock resolves 100% to crates.io registry with no git/path/patch overrides
- Plan 01-02 Task 1 follow-on (APPROVED): serde_yaml 0.9 → serde_yaml_ng 0.10 swap authorized; APPLIED in Task 3 (commit 2f8e2e8)
- Plan 01-02 COMPLETE (2026-06-10): strfry 1.1.0 pinned by digest sha256:545555da...; A5 BYTE-IDENTICAL; fixture sha256:8b871be8...; 6 golden vectors committed; config loader tested
- Plan 01-02 key finding: kind=3 is replaceable (Nostr NIP-01) — seed uses kind=2 to keep all 11 events in fixture
- Plan 01-03 (2026-06-10): Meta FlatBuffer vtable decode required (not raw C struct); dbVersion at abs byte 40, endianness at abs byte 32; STRFRY_LITTLE_ENDIAN_MARKER=1 (not 0); golden vectors corrected from actual fixture scan — levId=1..4 at ts=1700000000, levIds 6,8,11 have tags (not 9,10,11); all 6 self-check tests pass; Phase 1 success criteria met (LMDB-01/02/03/05/06)
- Plan 02-01 (2026-06-11): EventPayload 0x00 decode foundation — NostrEvent (lenient, 7 typed fields, no deny_unknown_fields D-02; tags Vec<Vec<String>> D-03) + DecodedEvent{event, raw_json} (D-01 exact retained bytes); EventPayload/CompressionDictionary read-only IntegerComparator opens; get_event_payload short txn + copy-out (D-08); decode_payload_skip_on_error skip+warn+count (D-11); 0x01 zstd arm returns UnknownTypeTag (Plan 02-02 wires it); all PayloadError zstd variants defined now for stable API; LMDB-07 verified — all 11 fixture levIds decode against Event__id.json golden vectors; 25 tests pass
- Plan 01-04 (2026-06-11): CR-01 gap closed — seek_first_ge_lev_id added to indexes.rs (MDB_SET_RANGE via heed db.range()); run_comparator_self_check upgraded to two-phase: Phase 1 physical-order integrity scan + Phase 2 comparator seek gate; ComparatorSeekMismatch error variant; non-vacuous test proves memcmp landing=levId=4 (kind=1) != golpe-correct levId=2 (kind=256); 01-03-SUMMARY honesty fixed; 19 tests pass; LMDB-06/LMDB-05/D-03/D-04 correctness restored

### Pending Todos

- Environment (non-blocking, fix before CI): `rust-toolchain.toml` pins `stable-x86_64-apple-darwin` on arm64; stale system `rustdoc 1.71.1` + `/usr/local/bin/clippy-driver` shadow the rustup 1.89 toolchain → bare `cargo test`/`cargo clippy` fail on the doctest/build-script step. Workaround `cargo test --all-targets`. Real fix: correct the toolchain pin / PATH.
- Code review WR-01 through WR-06 (warnings, see 01-REVIEW.md) — deliberately deferred; address in a future maintenance phase.

### Blockers/Concerns

- RESOLVED: CR-01 vacuous comparator self-check — closed in plan 01-04 (commit 8e9d7ea). Seek gate added; LMDB-06 correctness restored.
- RESOLVED: CR-02 FFI MDB_val positional init — fixed via named-member init + build.rs locate-or-warn (commit 5cfd867)
- RESOLVED: `heed` custom-comparator API confirmed — smoke test PASSED (Plan 01-01 Task 3)
- RESOLVED: heed 0.22.1 upgrade — completed in Plan 01-01 Task 4 continuation
- RESOLVED: Parent DeepFry stack Dockerfile.strfry pinned to digest in Plan 01-02 Task 3 (commit 2f8e2e8)
- RESOLVED: Docker/Colima no-egress issue — orchestrator pre-pulled dockurr/strfry:1.1.0 image; import ran successfully offline
- RESOLVED: Phase 1 spike A3 (Meta struct field offsets) — FlatBuffer vtable walker implemented; dbVersion at abs byte 40, endianness at abs byte 32; confirmed from onAppStartup.cpp

## Deferred Items

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

Last session: 2026-06-11T10:05:00.000Z
Stopped at: Completed 02-01-PLAN.md
Resume: execute Plan 02-02 (0x01 zstd-dictionary decode path) next
Resume file: .planning/phases/02-payload-decoding-index-scan-primitives/02-02-PLAN.md
