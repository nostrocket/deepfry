---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Completed 05-03 code-review fix (CR-01/CR-02/WR-01-04/IN-01)
last_updated: "2026-06-15T09:19:59.524Z"
last_activity: 2026-06-15
progress:
  total_phases: 5
  completed_phases: 5
  total_plans: 23
  completed_plans: 23
  percent: 100
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-06-10)

**Core value:** Serve correct, rich queries over strfry's events by reading strfry's live on-disk state directly — never copying event data or indexes out of strfry, never writing to strfry's database.
**Current focus:** Phase 05 — hardening-docker-packaging

## Current Position

Phase: 05
Plan: Not started
Status: Ready to execute
Last activity: 2026-06-15

Progress: [████████░░] 90% (Phases 1-4 complete; Phase 05 both plans executed, 1 verification gap open)

## Performance Metrics

**Velocity:**

- Total plans completed: 25
- Average duration: ~40 min
- Total execution time: ~2.4 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-lmdb-foundation-comparator-proof | 4/4 | ~115 min | ~29 min |
| 02-payload-decoding-index-scan-primitives | 3/3 | ~33 min | ~11 min |
| 02 | 3 | - | - |
| 03 | 11 | - | - |
| 04 | 2 | - | - |
| 05 | 3 | - | - |

**Recent Trend:**

- Last 5 plans: Plan 01-03 (~15 min, 14 files, 4 commits), Plan 01-04 (~35 min, 4 files, 4 commits), Plan 02-01 (~20 min, 6 files, 3 commits), Plan 02-02 (~7 min, 1 file, 1 commit)
- Trend: Consistent ~7-35 min/plan

*Updated after each plan completion*
| Phase 03-query-engine P02 | 25 minutes | 3 tasks | 3 files |
| Phase 03-query-engine P03 | 8 minutes | 2 tasks | 2 files |
| Phase 03-query-engine P04 | 25 minutes | 3 tasks | 3 files |
| Phase 03-query-engine P05 | 20 | 2 tasks | 2 files |
| Phase 03-query-engine P06 | 31 | 2 tasks | 5 files |
| Phase 03-query-engine P07 | 15 | 2 tasks | 2 files |
| Phase 03-query-engine P08 | 5min | 2 tasks | 1 files |
| Phase 03-query-engine P09 | 6min | 2 tasks | 2 files |
| Phase 03-query-engine P10 | 20min | 2 tasks | 3 files |
| Phase 03-query-engine P11 | 30min | 2 tasks | 1 file |
| Phase 04-graphql-api P01 | 9 | 3 tasks | 5 files |
| Phase 05-hardening P01 | 6 min | 2 tasks | 7 files |
| Phase 05 P03 | 1227 | 2 tasks | 4 files |

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
- Plan 02-02 (2026-06-11): DictCache + 0x01 zstd-dictionary decode path — DictCache{RwLock<HashMap<u32,Arc<DecoderDictionary<'static>>>>}; get_or_load: read-lock fast path, short-txn miss path, DecoderDictionary::copy OUTSIDE txn+write-lock; decode_event_payload_with_cache(raw,cache,env); MAX_EVENT_DECOMPRESSED_SIZE=4MiB; TruncatedZstdPayload/DictNotFound/ZstdError guards all tested; synthetic round-trip via from_continuous+Compressor::with_dictionary; .create()=0, write_txn=0; LMDB-08 satisfied; 38 tests pass
- Plan 02-03 (2026-06-11): scan_index_bounded/windowed — ScanDirection, bounded forward/reverse with move_through_duplicate_values; limit=0 windowed via Included+levId-skip (DUPSORT-correct); scan_index_windowed exposed for test-only small-window override; index-specific start key lengths to avoid golpe C comparator SIGABRT; all six indexes dispatched via indexes.rs open helpers; 53 tests pass; LMDB-09 satisfied
- [Phase ?]: No external hex crate: inline decode_hex in router.rs avoids new dep legitimacy concern
- Plan 03-03 (2026-06-12): LevIdNotFound propagated as hard error (not skip) — a levId from a real index scan missing in EventPayload is structural corruption; decode failures use skip-warn-count (D-11); 57 tests pass
- Plan 03-04 (2026-06-12): execute_query over-fetch+backfill+cursor + latest_per_author grouped buckets + is_expired NIP-40 predicate — QRY-01/02/03/05 satisfied; router.rs tag-filter fixed to hex-decode tag values to raw bytes matching strfry index format; 65 lib tests pass
- [Phase ?]: CR-05 fix: Vec<(LevId, DecodedEvent)> return from hydrate_lev_ids; callers join on lev_id via HashMap, never positional zip
- [Phase ?]: scan_index_one_window in scan.rs reuses collect_window for key-granular exclusive-resume without modifying merge_prefixes signature
- [Phase ?]: CR-03 fix: per-stream StreamState with exclusive-resume replaces Included-restart windowing in execute_query_internal; stuck-window advance branch removed entirely
- [Phase ?]: Decode Event__tag values to 32 raw bytes ONLY when exactly 64 lowercase hex chars; all other values use raw UTF-8
- [Phase ?]: tags_filter.iter().all() for AND across distinct TagFilter fields; inner .any() for OR within one field
- [Phase ?]: decode_hex/nibble pub(crate) in router.rs; engine.rs decode_hex_32 delegates to shared helper; local nibble removed
- [Phase 03-query-engine]: CR-01 closed: Reverse scan Bound::Excluded(ts+1) for finite start keys — heed 0.22.1 rev_range Included positions at smallest dup then steps away; Excluded(ts+1) lands on largest dup of the boundary timestamp
- [Phase 03-query-engine P10]: Bounded round-loop with MAX_ROUNDS=8; partial-result cursor returned when budget-capped with reachable events; VERIFICATION truth #5 VERIFIED; 106 tests pass (90 lib + 16 integration)
- [Phase 03-query-engine P11]: CR-01 closed: deepest_scanned fallback cursor returns Some() when valid empty + !exhausted. CR-02 closed: no-progress break with ts-advance override (deepest_scanned = stalled_ts-1 / u64::MAX) ensures events below fat timestamp reachable. 108 tests pass.
- [Phase 05 P01]: OPS-01 — /health (200 always) + /ready (503 before gates, 200 after) via Arc<AtomicBool> set after run_comparator_self_check. OPS-04 — pinnedStrfryVersion in AppState+StatsResult+stats resolver. 125 tests pass.
- [Phase ?]: build_probe_router: probe-only startup surface (/health + /ready, no /graphql) served before gate chain so 503→200 transition is observable to real orchestrators (OPS-01 gap-closure)
- [Phase ?]: NetListener alias for post-gate re-bind: preserves source-order awk acceptance criterion unambiguity (single TcpListener::bind before self-check)
- [Phase 05-03 CR-01/CR-02 fix]: bind-once gated-router design — single TcpListener::bind before gate chain; one axum::serve for entire process lifetime; POST /graphql gated behind Arc<OnceCell<AppSchema>> (503 while empty, execute when populated); eliminates connection-refused gap (CR-01) and ephemeral-port re-bind bug (CR-02) from probe-shutdown approach; AppRouterState replaces separate schema+ready parameters; build_probe_router and Notify/re-bind removed

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

Last session: 2026-06-15T06:00:00.000Z
Stopped at: Completed 05-03 code-review fix (CR-01/CR-02/WR-01-04/IN-01)
Resume: None — phase 05 fully complete
Resume file: None
