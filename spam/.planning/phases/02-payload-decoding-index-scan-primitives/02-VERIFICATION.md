---
phase: 02-payload-decoding-index-scan-primitives
verified: 2026-06-11T00:00:00Z
status: passed
score: 9/9 must-haves verified
overrides_applied: 0
---

# Phase 02: Payload Decoding & Index Scan Primitives Verification Report

**Phase Goal:** Full event JSON can be hydrated from both 0x00 and 0x01 EventPayload formats, and bounded cursor scans over each Event__* index are tested in isolation
**Verified:** 2026-06-11
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | A 0x00 (raw JSON) EventPayload is decoded to full event JSON (SC-1, LMDB-07) | VERIFIED | `decode_event_payload` in `src/lmdb/payload.rs:400`; all 11 fixture levIds pass `tests/payload_test.rs::test_all_fixture_levids_decode` |
| 2 | A 0x01 (zstd-dictionary-compressed) EventPayload is decoded using CompressionDictionary[dictId] (SC-2, LMDB-08) | VERIFIED | `decode_event_payload_with_cache` in `src/lmdb/payload.rs:239`; `test_decode_0x01_synthetic_round_trip` passes; `test_decode_0x01_truncated_returns_error` and `test_decode_0x01_over_ceiling_returns_error` prove safe error handling |
| 3 | Read transactions are opened and closed per-query, not held open across queries (SC-3, LMDB-09) | VERIFIED | `scan_index_bounded` takes `&heed::Env` not `&RoTxn` (structural guarantee, line 104); `scan_index_windowed` drops `rtxn` before each batch boundary (line 224); `test_windowed_with_small_window_no_gaps_no_dupes` proves multi-batch operation |
| 4 | 0x00 decode returns exact retained raw JSON bytes — no re-serialize (D-01, LMDB-07) | VERIFIED | `raw_json: json_bytes.to_vec()` at `payload.rs:413`; `test_decode_0x00_round_trip_against_golden_vectors` asserts `serde_json::from_slice::<NostrEvent>(&decoded.raw_json).id == decoded.event.id` |
| 5 | NostrEvent deserialization is lenient — unknown top-level fields ignored, 7 fields required (D-02, D-03) | VERIFIED | No `deny_unknown_fields` (`grep -c 'deny_unknown_fields' src/lmdb/types.rs` = 0); `test_nostr_event_ignores_unknown_fields` and `test_nostr_event_missing_required_field_errors` both pass |
| 6 | Malformed/undecodable payload is skipped+warned+counted, never panics (D-11) | VERIFIED | `decode_payload_skip_on_error` at `payload.rs:430`; `test_decode_skip_on_error_counts` confirms skip_count increments on error and not on success; empty slice, unknown tag, and bad JSON all return `Err` |
| 7 | Bounded forward and reverse scans return correct golden-vector sequences for Event__kind (LMDB-09) | VERIFIED | `test_forward_bounded_event_kind_limit3_golden_prefix` asserts `[4,5,6]`; `test_reverse_bounded_event_kind_limit3_golden_suffix` asserts `[2,3,9]` |
| 8 | Every Event__* scan iterates duplicate VALUEs (MDB_DUPSORT) — no levId silently skipped (T-02-14) | VERIFIED | `move_through_duplicate_values()` called 8 times in `scan.rs` (`grep -c` = 8); `test_dupsort_duplicate_lev_ids_not_skipped` asserts levIds 5,6 and 7,8 both present |
| 9 | Reverse windowed (limit=0) scan does not silently drop levIds at a DUPSORT group boundary (CR-01) | VERIFIED | Key-granular windowing (`collect_window` drains boundary dup group, resumes with `Bound::Excluded`); `test_reverse_window_smaller_than_dup_group_no_drop` and `test_reverse_window_straddle_non_first_group_no_drop` both pass; `test_old_code_reverse_drops_levid_nonvacuity` proves the regression suite is non-vacuous |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|---------|---------|--------|---------|
| `src/lmdb/types.rs` | `NostrEvent` (lenient serde) + `DecodedEvent { event, raw_json: Vec<u8> }` | VERIFIED | `pub struct NostrEvent` at line 88, `pub struct DecodedEvent` at line 125; `#[derive(serde::Deserialize)]` present; no `deny_unknown_fields`; `tags: Vec<Vec<String>>` |
| `src/lmdb/payload.rs` | sub-DB open helpers, `decode_event_payload`, `DictCache`, `0x00` + `0x01` paths, `PayloadError` | VERIFIED | 718 lines; all public API present; `pub fn decode_event_payload` line 400; `pub struct DictCache` line 125; `pub fn decode_event_payload_with_cache` line 239; `pub enum PayloadError` line 58 |
| `src/lmdb/scan.rs` | `ScanDirection`, `scan_index_bounded`, windowed scan, DUPSORT-aware | VERIFIED | 669 lines; `pub enum ScanDirection` line 63; `pub fn scan_index_bounded` line 104; `pub fn scan_index_windowed` line 182; `DEFAULT_WINDOW_SIZE = 256` |
| `src/lmdb/mod.rs` | `pub mod payload;` and `pub mod scan;` | VERIFIED | Both present at lines 5 and 6 respectively (alphabetical order) |
| `tests/payload_test.rs` | 0x00 fixture round-trip + all-11-levId smoke | VERIFIED | 2 integration tests pass; `test_decode_0x00_round_trip_against_golden_vectors` and `test_all_fixture_levids_decode` |
| `tests/scan_test.rs` | resume-cursor, DUPSORT coverage, per-index smoke, windowed integration | VERIFIED | 4 integration tests pass |
| `tests/dupsort_resume_test.rs` | CR-01 regression: dup ordering proof + reverse window non-vacuity | VERIFIED | 4 tests pass including empirical dup-order proof and non-vacuity test |
| `Cargo.toml` | `zstd = "0.13.3"` direct dependency | VERIFIED | Line 32; Cargo.lock confirms `version = "0.13.3"`, `source = "registry+https://github.com/rust-lang/crates.io-index"` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `src/lmdb/payload.rs` | `rasgueadb_defaultDb__EventPayload` | `env.database_options().key_comparator::<IntegerComparator>().name(...).open(&rtxn)` | WIRED | `open_event_payload_db` at line 318; `.open()` never `.create()` |
| `src/lmdb/payload.rs` | `src/lmdb/types.rs::DecodedEvent` | `serde_json::from_slice into NostrEvent + retained raw bytes` | WIRED | `payload.rs:265` (0x00 path), `payload.rs:297` (0x01 path) |
| `src/lmdb/mod.rs` | `src/lmdb/payload.rs` | `pub mod payload;` | WIRED | `mod.rs:5` |
| `src/lmdb/mod.rs` | `src/lmdb/scan.rs` | `pub mod scan;` | WIRED | `mod.rs:6` |
| `payload.rs decode path (0x01)` | `DictCache::get_or_load` | `dict_id = u32::from_le_bytes(raw[1..5]); cache.get_or_load(dict_id, env)` | WIRED | `payload.rs:279-285`; `raw[1..5]` confirmed (not `[1..4]`) |
| `DictCache::get_or_load (miss)` | `rasgueadb_defaultDb__CompressionDictionary` | `LMDB GET dict_id.to_ne_bytes() then DecoderDictionary::copy` | WIRED | `payload.rs:171-182`; txn dropped before `DecoderDictionary::copy` at line 186 |
| `src/lmdb/payload.rs` | `zstd::bulk::Decompressor` | `Decompressor::with_prepared_dictionary(&dd).decompress(frame, MAX_EVENT_DECOMPRESSED_SIZE)` | WIRED | `payload.rs:290-294` |
| `src/lmdb/scan.rs` | `indexes.rs open helpers` | `open_index_string_uint64 / open_index_uint64_uint64 / open_index_string_uint64_uint64 / open_index_created_at` | WIRED | `scan.rs:37-40` imports; dispatch at lines 119-138 (bounded) and 200-219 (windowed) |
| `scan.rs reverse scan` | `heed RoRevRange` | `db.rev_range(rtxn, &range).move_through_duplicate_values()` | WIRED | `scan.rs:292` (collect_bounded), `scan.rs:388` (collect_window); no `.rev()` call present |
| `scan.rs (all scans)` | `MDB_DUPSORT duplicate values` | `.move_through_duplicate_values()` on every range/rev_range iterator | WIRED | `grep -c 'move_through_duplicate_values' scan.rs` = 8 |

### Data-Flow Trace (Level 4)

Not applicable — these are library primitives (no HTTP layer, no UI rendering). Data flow is verified via integration tests against the committed LMDB fixture.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|---------|---------|--------|--------|
| All 55 unit + integration tests pass | `cargo test --all-targets` | 39 lib + 2 comparator + 4 dupsort + 2 payload + 4 scan + 4 self_check = 55 passed, 0 failed | PASS |
| `decode_event_payload` reads all 11 fixture events | `cargo test --test payload_test` | 2 tests pass | PASS |
| Forward/reverse golden vector assertions | `cargo test lmdb::scan` | 9 scan unit tests pass including exact `[4,5,6]` and `[2,3,9]` | PASS |
| CR-01 regression: reverse windowed scan complete | `cargo test --test dupsort_resume_test` | 4 tests pass; non-vacuity test confirms old code drops levId=6 | PASS |

### Probe Execution

No probe scripts declared for this phase. Step 7c: SKIPPED (no declared probes; behavioral spot-checks cover the runnable code).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| LMDB-07 | 02-01-PLAN.md | Decode 0x00 (raw JSON) EventPayload values to full event JSON | SATISFIED | `decode_event_payload` 0x00 path; all 11 fixture events decode; `tests/payload_test.rs` passes |
| LMDB-08 | 02-02-PLAN.md | Decode 0x01 (zstd-dictionary-compressed) values using CompressionDictionary[dictId] | SATISFIED | `decode_event_payload_with_cache`; `DictCache` with `RwLock<HashMap<u32,Arc<DecoderDictionary<'static>>>>` (Send+Sync); synthetic round-trip test passes; truncated/oversized/missing-dict all return `Err` |
| LMDB-09 | 02-03-PLAN.md | Keep read transactions short (per-query, bounded by limit) so strfry can reclaim pages | SATISFIED | `scan_index_bounded` signature takes `&heed::Env` (structural guarantee); windowed scan drops txn before each batch; `test_windowed_with_small_window_no_gaps_no_dupes` proves multi-txn behavior |

All three phase-owned requirements are satisfied. No orphaned requirements identified.

### Anti-Patterns Found

No blockers. Deferred warnings (documented below) were explicitly reviewed and accepted.

| File | Finding | Severity | Disposition |
|------|---------|---------|-------------|
| `src/lmdb/payload.rs:162,190,203,212` | `RwLock` accessed via `.expect("DictCache RwLock poisoned")` — panics on lock poison in tokio context (WR-05 from code review) | Warning | Deferred — no tokio context exists yet in Phase 2; Phase 3/4 will have an axum integration context where this matters. Not a current-phase blocker. |
| `src/lmdb/payload.rs:257-260` | Empty `raw` slice returns `UnknownTypeTag { tag: 0 }`, ambiguous with a real 0x00 tag (WR-03) | Warning | Deferred — affects diagnostics only; does not affect correctness. Actionable at Phase 3 observability pass. |
| `src/lmdb/payload.rs` | 0x00 path has no size ceiling (WR-04); only the 0x01 path is bounded by `MAX_EVENT_DECOMPRESSED_SIZE` | Warning | Deferred — strfry enforces `maxEventSize=65536` on ingest; historical events are bounded by that. Actionable at Phase 5 hardening. |
| `tests/scan_test.rs:45` | `kind_reverse_high_key` function unused (dead_code warning, WR-01 variant) | Info | Pre-existing compile warning; no correctness impact. |
| `src/lmdb/payload.rs:87-103,292-294` | `DecompressedTooLarge` variant defined but never constructed; over-ceiling error maps to `ZstdError` (IN-04) | Info | Variant is present for API stability; the test only asserts `is_err()`. Low priority cleanup. |

Debt markers (TBD, FIXME, XXX): none found in any Phase 2 modified file.

### Human Verification Required

None. All must-have truths are verifiable programmatically via the test suite. No UI, external services, or user flows are involved in this phase.

### Gaps Summary

No gaps. All 9 must-have truths are VERIFIED. All artifacts exist and are substantive, wired, and data-flowing (where applicable). The CR-01 BLOCKER from the code review was fully resolved with:

1. A corrected `collect_window` implementation using key-granular windowing (drain boundary dup group, resume with `Bound::Excluded`).
2. An empirical proof test (`test_proven_dup_iteration_order_range_and_rev_range`) establishing the actual heed 0.22.1 DUPSORT ordering.
3. Three regression tests (`test_reverse_window_smaller_than_dup_group_no_drop`, `test_reverse_window_straddle_non_first_group_no_drop`, `test_old_code_reverse_drops_levid_nonvacuity`) that are non-vacuous (the non-vacuity test demonstrates the old code drops levId=6).

The deferred warnings (WR-01/03/04/05, IN-04) are non-blocking for Phase 2's goal. They are documented above and appropriate for a Phase 3/5 maintenance pass.

---

_Verified: 2026-06-11_
_Verifier: Claude (gsd-verifier)_
