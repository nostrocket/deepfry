---
phase: 03-query-engine
plan: "07"
subsystem: query
tags: [gap-closure, cr-06, cr-07, wr-04, in-02, tag-filter, hex-decode, and-semantics]
dependency_graph:
  requires:
    - 03-06-SUMMARY.md  # execute_query_internal rewrite with StreamState, since stop-bound
    - 03-05-SUMMARY.md  # lev_id-keyed HashMap join in engine
  provides:
    - 64-char lowercase hex only tag value decode (strfry's exact 32-byte-id rule)
    - single-char tag name validation with warn-and-skip (WR-04)
    - NIP-01 AND-across-fields tag residual in execute_query_internal (CR-06)
    - shared decode_hex/nibble pub(crate) helpers (IN-02)
  affects:
    - src/query/router.rs (tag decode rule, tag name validation, pub(crate) helpers)
    - src/query/engine.rs (tag residual AND semantics, hex helper consolidation)
tech_stack:
  added: []
  patterns:
    - 64-char-lowercase-hex-only gate for Event__tag value decode
    - tag.name.len() == 1 guard with tracing::warn! skip for invalid tag names
    - tags_filter.iter().all(...) for NIP-01 AND across distinct tag fields
    - shared pub(crate) decode_hex from router.rs reused in engine.rs (IN-02)
key_files:
  created: []
  modified:
    - src/query/router.rs
    - src/query/engine.rs
decisions:
  - "CR-07 fix: decode Event__tag values ONLY when exactly 64 lowercase hex chars; all other values use raw UTF-8"
  - "WR-04 fix: tag.name.len() != 1 emits tracing::warn and produces zero start keys"
  - "CR-06 fix: tags_filter.iter().all(...) for AND across distinct TagFilter fields; values within one field stay ORed"
  - "IN-02 consolidation: decode_hex/nibble made pub(crate) in router.rs; engine.rs decode_hex_32 delegates to shared implementation; local nibble removed"
metrics:
  duration: ~15 minutes
  completed: "2026-06-12"
  tasks: 2
  files_modified: 2
requirements: [QRY-02]
---

# Phase 03 Plan 07: Tag Filter Semantics Gap Closure (CR-06/CR-07/WR-04/IN-02) Summary

**One-liner:** Fixed three tag filter correctness bugs — 64-char lowercase hex only decode (CR-07), single-char tag name validation with warn-skip (WR-04), NIP-01 AND-across-fields tag residual (CR-06) — and consolidated duplicate hex helpers (IN-02).

## What Was Built

### Task 1: 64-char lowercase hex decode + single-char tag name validation (src/query/router.rs)

**CR-07 fix — Event__tag value decode rule:**

The old code applied `decode_hex` to ANY even-length hex value, meaning a literal topic
tag like "beef" (4-char even-length hex) was decoded to binary `[0xBE, 0xEF]` instead of
stored as raw UTF-8. strfry's actual rule is: decode to 32 raw bytes ONLY when the value
is exactly 64 lowercase hex characters (0-9, a-f). All other values (short hex, uppercase
hex, non-hex) use raw UTF-8 bytes unchanged.

The fix replaces the fallback-decode approach with an explicit gate:
```rust
if value.len() == 64 && value.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f')) {
    decode_hex(value).unwrap_or_else(|_| value.as_bytes().to_vec())
} else {
    value.as_bytes().to_vec()
}
```

**WR-04 fix — tag name validation:**

The old code silently truncated multi-char or empty tag names to their first byte, producing
scans of the wrong prefix (e.g. "emoji" would scan the 'e' prefix, returning unrelated events).
The fix requires `tag.name.len() == 1` and emits `tracing::warn!` + produces zero start keys
for the invalid tag.

**IN-02 consolidation:**

Made `decode_hex` and `nibble` `pub(crate)` so engine.rs can reuse them.

**5 new regression tests added:**
- `test_build_start_keys_tag_literal_hex_not_decoded` — "beef" → raw UTF-8 bytes (1+4+8=13 byte key, not 11)
- `test_build_start_keys_tag_uppercase_hex_not_decoded` — 64-char uppercase hex → raw UTF-8 (73 bytes, not 41)
- `test_build_start_keys_tag_64char_lowercase_hex_decoded` — 64-char lowercase hex → 32 binary bytes (41 bytes)
- `test_build_start_keys_tag_multichar_name_skipped` — "emoji" tag name → zero keys
- `test_build_start_keys_tag_empty_name_skipped` — "" tag name → zero keys

### Task 2: NIP-01 AND-across-fields tag residual + IN-02 consolidation (src/query/engine.rs)

**CR-06 fix — tag residual AND semantics:**

The old code used a `'outer` labeled loop that `break 'outer`'d on the FIRST matching
TagFilter. This is OR semantics: a filter `{#e:[X], #p:[Y]}` would return events with #e=X
OR #p=Y. NIP-01 requires AND: both #e=X AND #p=Y must be present on the same event.

The fix replaces the `'outer` loop with `tags_filter.iter().all(...)`:
```rust
let passes = tags_filter.iter().all(|tf| {
    decoded.event.tags.iter().any(|ev_tag| {
        ev_tag.len() >= 2
            && ev_tag[0] == tf.name
            && tf.values.iter().any(|v| v == &ev_tag[1])
    })
});
```
Every TagFilter (distinct field) must match (AND). Values within a single TagFilter remain
ORed (the inner `.any`).

**IN-02 consolidation:**

Removed the local `decode_hex_32`/`nibble` functions and replaced with a delegation to the
`pub(crate) decode_hex` from router.rs. The new `decode_hex_32` is a thin wrapper:
```rust
fn decode_hex_32(s: &str) -> Option<[u8; 32]> {
    if s.len() != 64 { return None; }
    match decode_hex(s) {
        Ok(bytes) => bytes.try_into().ok(),
        Err(_) => None,
    }
}
```

**3 new regression tests added:**
- `test_execute_query_multi_tag_and_semantics` — {#e:[64a], #p:[any]} returns 0 events (AND semantics)
- `test_execute_query_tag_values_or_within_field` — {#e:[64a, other]} returns 3 events (within-field OR)
- `test_execute_query_single_tag_still_matches` — {#e:[64a]} still returns 3 events (baseline guard)

## Test Results

- `cargo test --lib query::router`: **12 passed** (7 existing + 5 new CR-07/WR-04 regression tests)
- `cargo test --lib query::engine`: **16 passed** (13 existing + 3 new CR-06 regression tests)
- `cargo test --all-targets`: **81 lib tests + 16 integration tests = all passed, zero failures**

## Deviations from Plan

**1. [Rule 2 - Auto-add] Added a 5th test `test_build_start_keys_tag_64char_lowercase_hex_decoded`**

The plan specified 4 regression tests in the behavior section. An additional positive test
was added to prove that the 64-char lowercase hex case still correctly hex-decodes (the
existing fixture tag scenario). This ensures the gate doesn't accidentally reject valid
32-byte-id tags. No plan deviation in functionality.

**2. [Rule 1 - Bug] decode_hex_32 in engine.rs now accepts both upper- and lower-case hex**

The plan said "keep behavior byte-identical" for the consolidation. The old local `nibble`
in engine.rs accepted both upper- and lower-case hex (matching router.rs's `nibble`).
The consolidated `decode_hex` also accepts both cases, so the behavior is byte-identical.
The lowercase-only gate is specifically in the Event__tag value path in `build_start_keys`,
not in the `decode_hex_32` function used by `latest_per_author` for pubkey decoding.

No other deviations. All plan correctness requirements (CR-06, CR-07, WR-04, IN-02) satisfied.

## Decisions Made

| Decision | Rationale |
|----------|-----------|
| 64-char lowercase hex only for Event__tag value decode | Matches strfry's exact rule for 32-byte event/pubkey IDs; prevents "beef" → 0xBEEF silent miss |
| tag.name.len() != 1 → warn + zero keys | Safe default: better to return no results with a logged warning than scan the wrong prefix silently |
| tags_filter.iter().all(...) with inner .any(...) | Directly expresses NIP-01 semantics: AND across fields (iter().all), OR within a field (inner .any) |
| decode_hex_32 delegates to pub(crate) decode_hex | DRY: one implementation to update if the hex format ever changes; behavior preserved |

## Known Stubs

None — all production code paths are fully implemented.

## Threat Flags

No new security-relevant surface introduced. All changes are read-only (no write_txn, no .create()).

The following threat register mitigations from the plan are satisfied:
- T-03-CR07-I: 64-char lowercase hex only gate in Event__tag value decode path
- T-03-CR06-I: AND-across-fields residual in execute_query_internal
- T-03-WR04-I: single-char tag name validation with warn-skip

## Self-Check: PASSED

- FOUND: src/query/router.rs (64-char hex gate, tag.name.len() != 1 guard, pub(crate) helpers)
- FOUND: src/query/engine.rs (tags_filter.iter().all, decode_hex consolidation)
- FOUND: commit f75eead (Task 1 — 64-char lowercase hex decode + single-char tag name validation)
- FOUND: commit 42dd092 (Task 2 — NIP-01 AND tag residual + IN-02 hex helper consolidation)
- All 81 lib tests + 16 integration tests pass (cargo test --all-targets)
