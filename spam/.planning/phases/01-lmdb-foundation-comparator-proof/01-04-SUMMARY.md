---
phase: 01-lmdb-foundation-comparator-proof
plan: 04
subsystem: lmdb2graphql/lmdb
tags: [rust, lmdb, heed, comparators, self-check, startup-gate, gap-closure, cr-01, tdd]
dependency_graph:
  requires: ["01-01", "01-02", "01-03"]
  provides: ["comparator-seek-gate", "cr-01-closure"]
  affects: ["phase-05-readiness"]
tech_stack:
  added: []
  patterns:
    - "MDB_SET_RANGE via heed db.range() with (Bound::Included, Bound::Unbounded) tuple"
    - "TDD RED/GREEN gate (test commit before implementation)"
    - "Adversarial seek pairs derived from committed golden vector ordering_groups"
    - "Non-vacuous comparator gate proof via memcmp control on golpe-built B-tree"
key_files:
  created: []
  modified:
    - spam/src/lmdb/indexes.rs
    - spam/src/lmdb/self_check.rs
    - spam/tests/self_check_test.rs
    - spam/.planning/phases/01-lmdb-foundation-comparator-proof/01-03-SUMMARY.md
decisions:
  - "seek_first_ge_lev_id uses (Bound::Included, Bound::Unbounded) tuple — the heed 0.22 Bytes-keyed range pattern from cookbook; not RangeFrom<&[u8]> (unsized, rejected by compiler)"
  - "Memcmp mismatch on golpe fixture lands on levId=4 (kind=1), not levId=3 (kind=255) as plan predicted — kind=1 LE [0x01...] > lower_bound [0x00 0x01...] at byte[0] under memcmp; kind=1 is the first golpe-order leaf entry satisfying memcmp >= condition"
  - "Non-vacuous test asserts landing_lev_id != 2 AND == 4 (pinned to fixture for reproducibility)"
  - "Phase 2 seek gate uses build_adversarial_seeks() with static pubkey bytes for 79be...98 (secp256k1 generator point, PROVENANCE.md)"
  - "ComparatorSeekEmpty added alongside ComparatorSeekMismatch for empty-range defensive case"
metrics:
  duration: "~35 minutes"
  completed: "2026-06-11"
  tasks_completed: 3
  files_changed: 4
  test_count: 19
---

# Phase 01 Plan 04: Gap Closure — CR-01 Comparator Seek Gate Summary

Closes CR-01: the startup comparator self-check was vacuous (forward `iter()` never invokes the registered comparator). Adds a comparator-exercising `seek_first_ge_lev_id()` helper and a two-phase `run_comparator_self_check` — Phase 1 forward scan for data integrity, Phase 2 `MDB_SET_RANGE` seeks on adversarial key pairs (Event__kind, Event__pubkeyKind) proving golpe comparators are actually consulted. A non-vacuous test proves the gate trips under memcmp. LMDB-06 correctness restored.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Add seek_first_ge_lev_id helper to indexes.rs | 82f5eaa | src/lmdb/indexes.rs |
| 2 (RED) | Failing tests for seek gate + ComparatorSeekMismatch | 678c43b | tests/self_check_test.rs |
| 2 (GREEN) | Wire seek gate into run_comparator_self_check | 8e9d7ea | src/lmdb/self_check.rs |
| 3 | Honesty fix — correct 01-03-SUMMARY decisions entry | 43410bc | .planning/.../01-03-SUMMARY.md |

## Verification

All tests pass:
- `cargo test --lib indexes` — 4 tests pass (3 existing + 1 new adversarial seek test)
- `cargo test --test self_check_test` — 4 tests pass (2 existing + 2 new)
- `cargo test --all-targets` — 19 tests pass total (was 16; 3 new tests added)
- `cargo build` — clean
- `cargo clippy --all-targets -- -D warnings` — fails on build script due to stale system
  `/usr/local/bin/clippy-driver` (1.71.1) shadowing rustup 1.89; no code defects (known
  environment issue, documented in STATE.md)

## TDD Gate Compliance

- RED gate: `test(01-04)` commit 678c43b — test fails to compile (`ComparatorSeekMismatch` not yet defined in `SelfCheckError`)
- GREEN gate: `feat(01-04)` commit 8e9d7ea — all 4 self_check_test tests pass

## Success Criteria Status

| Criterion | Status | Evidence |
|-----------|--------|----------|
| seek_first_ge_lev_id added to indexes.rs with .range() | PASS | grep .range( → 2 matches; unit test asserts levId=2 |
| Unit test: Event__kind adversarial seek → levId=2 | PASS | test_seek_first_ge_lev_id_event_kind_adversarial_pair passes |
| run_comparator_self_check drives Phase 2 seeks | PASS | seek_first_ge_lev_id called for Event__kind + Event__pubkeyKind |
| ComparatorSeekMismatch error variant added | PASS | self_check.rs SelfCheckError enum; test compiles with pattern match |
| Non-vacuous trip test | PASS | test_seek_gate_detects_memcmp_comparator_on_fixture asserts levId=4 != 2 |
| Self-check still passes on fixture | PASS | test_self_check_with_seek_gate_passes_on_fixture passes |
| main.rs still wired, fail-closed | PASS | grep run_comparator_self_check src/main.rs matches |
| 01-03-SUMMARY honesty fix | PASS | 'full-scan self-check still correctly validates comparator registration' removed |
| No write_txn, no .create() (new code) | PASS | seek helpers use env.read_txn() only; no write path |
| All 19 tests pass | PASS | cargo test --all-targets: 19/19 ok |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] heed range API requires (Bound, Bound) tuple, not RangeFrom<&[u8]>**

- **Found during:** Task 1 (first compilation attempt)
- **Issue:** Plan specified `db.range(rtxn, &(lower_bound_key..))` which fails because `lower_bound_key` is `&[u8]` (unsized) and `RangeFrom<&[u8]>` does not implement `RangeBounds<[u8]>`.
- **Fix:** Used `(Bound::Included(lower_bound_key), Bound::Unbounded)` tuple — the correct pattern documented in heed 0.22 cookbook "Use Bytes as Cursor Ranges".
- **Files modified:** spam/src/lmdb/indexes.rs
- **Commit:** 82f5eaa

**2. [Rule 1 - Bug] Memcmp mismatch on fixture lands on levId=4, not levId=3**

- **Found during:** Task 2 RED (first test run)
- **Issue:** Plan predicted the non-vacuous test would assert memcmp landing = levId=3 (kind=255). Actual fixture result is levId=4 (kind=1 entry). Under memcmp on the golpe-built leaf page, kind=1 LE bytes `[0x01, 0x00, ...]` > lower_bound `[0x00, 0x01, ...]` at byte[0], so kind=1 is the first golpe-order entry satisfying `>= lower_bound` under memcmp.
- **Fix:** Updated non-vacuous test to assert `!= 2` (semantic correctness) AND `== 4` (pinned fixture reproducibility). The gate is just as non-vacuous — memcmp lands on a wrong entry (4 != 2), proving run_comparator_self_check would return Err.
- **Files modified:** spam/tests/self_check_test.rs
- **Commit:** 678c43b → 8e9d7ea

## Phase 1 Success Criteria (Restored)

| Criterion | Was | Now |
|-----------|-----|-----|
| #3: self-check passes by exercising golpe comparator | FAILED (iter() vacuous) | PASS (seek gate exercises comparator) |
| #4: fail-closed on comparator defect | FAILED (vacuous — can't trip) | PASS (ComparatorSeekMismatch on wrong landing) |

CR-01 closed. LMDB-06 / LMDB-05 / D-03 / D-04 correctness restored.

## Threat Surface Scan

No new network endpoints, auth paths, or external data paths introduced. The only change is a startup read path on the co-located strfry LMDB (already within the existing threat model). T-04-01 through T-04-04 mitigations all satisfied:
- T-04-01: ComparatorSeekMismatch returned on wrong landing → non-zero exit
- T-04-02: Non-vacuous test proves gate trips under memcmp (levId=4 != 2)
- T-04-03: Only read_txn() used; no write_txn; no .create()
- T-04-04: Per-call read_txn dropped after single seek; no long-lived txn

## Self-Check: PASSED

Created/modified files verified:
- spam/src/lmdb/indexes.rs: exists, contains .range(, no write_txn
- spam/src/lmdb/self_check.rs: exists, contains seek_first_ge_lev_id
- spam/tests/self_check_test.rs: exists, 4 tests
- spam/.planning/phases/01-lmdb-foundation-comparator-proof/01-03-SUMMARY.md: honesty fix verified

Commits verified to exist:
- 82f5eaa (Task 1): exists
- 678c43b (Task 2 RED): exists
- 8e9d7ea (Task 2 GREEN): exists
- 43410bc (Task 3): exists
