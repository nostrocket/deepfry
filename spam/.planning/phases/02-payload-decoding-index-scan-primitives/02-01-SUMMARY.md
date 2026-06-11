---
phase: 02-payload-decoding-index-scan-primitives
plan: 01
subsystem: database
tags: [lmdb, heed, serde_json, zstd, nostr, eventpayload, decode]

# Dependency graph
requires:
  - phase: 01-lmdb-foundation-comparator-proof
    provides: "read-only env open (open_fixture_env), IntegerComparator open chain (meta.rs), committed strfry fixture + golden vectors, LevId type"
provides:
  - "NostrEvent (lenient serde Deserialize) + DecodedEvent { event, raw_json } types"
  - "EventPayload + CompressionDictionary read-only sub-DB open helpers (IntegerComparator)"
  - "get_event_payload(env, lev_id) — short read txn, owned txn-independent bytes (D-08)"
  - "decode_event_payload — 0x00 raw-JSON path returning typed struct + exact retained raw_json (D-01)"
  - "PayloadError enum (0x01/zstd variants present for Plan 02-02)"
  - "decode_payload_skip_on_error — skip+warn+count, never panics (D-11)"
affects: [02-02-zstd-path, 02-03-scan-primitives, 03-query-engine, 04-graphql-api]

# Tech tracking
tech-stack:
  added: ["zstd 0.13.3 (direct dep; consumed by Plan 02-02, not this plan)"]
  patterns:
    - "EventPayload decode mirrors meta.rs: IntegerComparator open, per-call short read txn, thiserror enum, byte-offset doc comments"
    - "Copy-out-before-txn-drop (.to_vec()) for txn-independent owned bytes (D-08)"
    - "Skip+warn+count wrapper (decode_payload_skip_on_error) for never-panic decode loops (D-11)"

key-files:
  created:
    - src/lmdb/payload.rs
    - tests/payload_test.rs
  modified:
    - src/lmdb/types.rs
    - src/lmdb/mod.rs
    - Cargo.toml
    - Cargo.lock

key-decisions:
  - "0x01 zstd tag returns UnknownTypeTag in this plan; Plan 02-02 replaces that arm with the real decompress path"
  - "PayloadError enum defined with all 0x01/zstd variants up front so the API is stable across 02-01 and 02-02"
  - "tags typed as Vec<Vec<String>> (D-03); no deny_unknown_fields (D-02 lenient/forward-compat)"
  - "Doc comments reworded to avoid the literal tokens .create( and deny_unknown_fields so the read-only / lenient acceptance greps return 0"

patterns-established:
  - "Pattern: EventPayload sub-DB opens take (&env, &rtxn) and never use the creating open variant (read-only invariant, T-02-04)"
  - "Pattern: integer-keyed GET uses lev_id.to_ne_bytes() (MDB_INTEGERKEY, LE on co-located host)"
  - "Pattern: DecodedEvent carries both typed struct AND exact raw JSON bytes — one decode, no double parse (D-01)"

requirements-completed: [LMDB-07]

# Metrics
duration: ~20min
completed: 2026-06-11
---

# Phase 02 Plan 01: EventPayload Decoding Foundation Summary

**0x00 raw-JSON EventPayload decoding over strfry's live LMDB — lenient `NostrEvent`/`DecodedEvent` serde types, read-only `IntegerComparator` sub-DB opens, and a `decode_event_payload` that yields both the typed event and the exact retained raw JSON (D-01), with never-panic skip+warn+count for malformed input (D-11).**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-06-11
- **Tasks:** 4 (Task 1 was the zstd legitimacy checkpoint — pre-approved by human before this run)
- **Files modified:** 6 (2 created, 4 modified)

## Accomplishments

- `NostrEvent` (7 required typed fields, lenient — ignores unknown top-level fields) and `DecodedEvent { event, raw_json: Vec<u8> }` added to `types.rs`.
- `EventPayload` and `CompressionDictionary` sub-DBs open read-only with `IntegerComparator`; missing sub-DB maps cleanly to `SubDbNotFound` (no panic on the 0x00-only fixture).
- `get_event_payload` opens a short read txn and copies bytes out before the txn drops (D-08) — returns owned, txn-independent `Vec<u8>`.
- `decode_event_payload` decodes the `0x00` path to a `DecodedEvent` whose `raw_json` is the exact JSON bytes after the type tag (no re-serialize, D-01); unknown tags and invalid JSON return `Err`, never panic (T-02-01/T-02-02).
- `decode_payload_skip_on_error` implements the D-11 skip+warn+count policy.
- LMDB-07 verified for 0x00: all 11 fixture levIds decode to correct `NostrEvent`s with exact retained raw JSON, validated against the `Event__id.json` golden vectors.

## Task Commits

1. **Task 1: Add zstd 0.13.3 + crate-legitimacy gate** - `74f7783` (chore) — completed pre-run; human approved zstd 0.13.3 / zstd-safe 7.2.4 / zstd-sys 2.0.16 (all crates.io, no overrides).
2. **Task 2: NostrEvent + DecodedEvent types** - `5c8ec42` (feat)
3. **Task 3: payload.rs sub-DB opens + 0x00 decode + PayloadError + skip-count** - `8d8170c` (feat)
4. **Task 4: wire module + 0x00 fixture round-trip integration test** - `7ad535d` (test)

_Note: `pub mod payload;` (nominally Task 4) was committed with Task 3 so the introducing commit builds and its tests run; the Task 4 commit adds only the integration test._

## Files Created/Modified

- `src/lmdb/payload.rs` (created) - sub-DB open helpers, `get_event_payload`, `decode_event_payload`, `PayloadError`, `decode_payload_skip_on_error`, 7 unit tests.
- `tests/payload_test.rs` (created) - 0x00 fixture round-trip + all-11-levId smoke integration tests.
- `src/lmdb/types.rs` (modified) - added `NostrEvent` + `DecodedEvent` + 3 unit tests.
- `src/lmdb/mod.rs` (modified) - added `pub mod payload;` (alphabetical).
- `Cargo.toml` / `Cargo.lock` (modified, Task 1) - `zstd = "0.13.3"` direct dependency.

## Decisions Made

- 0x01 (zstd) tag intentionally returns `UnknownTypeTag` in this plan; Plan 02-02 replaces that match arm with the real decompress-then-`serde_json` path. The full set of 0x01-related `PayloadError` variants (`TruncatedZstdPayload`, `DictNotFound`, `ZstdError`, `DecompressedTooLarge`) is defined now for a stable enum.
- Reworded doc-comment mentions of `.create(` and `deny_unknown_fields` so the acceptance greps (`grep -c '\.create(' == 0`, `grep -c 'deny_unknown_fields' == 0`) reflect actual code, not prose.

## Deviations from Plan

None - plan executed exactly as written. (The doc-comment rewording above is a wording adjustment to satisfy the plan's own acceptance greps, not a behavioral change.)

## TDD Gate Compliance

Tasks 2 and 3 are marked `tdd="true"`. In Rust, the `NostrEvent`/`DecodedEvent` structs and the `payload.rs` API must exist for their tests to compile, so a separate non-compiling RED-only commit was not produced; instead each feature and its tests were committed together as a single `feat` commit with the behavior tests included and passing (Task 2 `5c8ec42`, Task 3 `8d8170c`). The Task 4 integration test was committed separately as a `test` commit (`7ad535d`). All behavior assertions from the `<behavior>` blocks are present and green.

## Issues Encountered

- `cargo clippy --all-targets` fails in this environment with `the -Z unstable-options flag must also be passed to enable check-cfg` in the `build.rs`/`cc` build script. This is the pre-existing toolchain-pin / PATH issue already recorded in STATE.md "Pending Todos" (stale system `clippy-driver`/`rustdoc` shadow the rustup toolchain). It is unrelated to this plan's code; the established workaround `cargo test --all-targets` passes cleanly (25 tests, 0 failures). Not fixed here — out of scope.

## Verification

- `cargo build` — compiles with zstd added.
- `cargo test --all-targets` — 25 tests pass, 0 failures (23 lib incl. 3 types + 7 payload unit tests; 2 payload integration tests).
- `grep -c '\.create(' src/lmdb/payload.rs` == 0 (read-only invariant, T-02-04).
- `grep -c 'deny_unknown_fields' src/lmdb/types.rs` == 0 (D-02 lenient).
- `grep -q 'to_ne_bytes' src/lmdb/payload.rs` and `grep -q 'tracing::warn!' src/lmdb/payload.rs` — both present.

## Next Phase Readiness

- Plan 02-02 can wire the `0x01` zstd-dictionary path into the existing `decode_event_payload` 0x01 arm and `open_compression_dictionary_db` helper; `PayloadError` already carries the needed variants.
- Plan 02-03 (scan primitives) can hydrate scanned levIds through `get_event_payload` + `decode_event_payload`.
- No blockers introduced.

## Self-Check: PASSED

- FOUND: src/lmdb/payload.rs
- FOUND: tests/payload_test.rs
- FOUND: .planning/phases/02-payload-decoding-index-scan-primitives/02-01-SUMMARY.md
- FOUND commit: 5c8ec42 (Task 2)
- FOUND commit: 8d8170c (Task 3)
- FOUND commit: 7ad535d (Task 4)

---
*Phase: 02-payload-decoding-index-scan-primitives*
*Completed: 2026-06-11*
