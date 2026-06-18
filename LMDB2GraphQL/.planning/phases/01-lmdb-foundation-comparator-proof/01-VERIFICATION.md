---
phase: 01-lmdb-foundation-comparator-proof
verified: 2026-06-11T06:00:00Z
status: passed
score: 5/5
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 3/5
  gaps_closed:
    - "CR-01: comparator self-check was vacuous (forward iter() never invokes the registered comparator) — now closed by MDB_SET_RANGE seek gate on adversarial pairs in run_comparator_self_check Phase 2"
    - "Overstated documentation in self_check.rs and 01-03-SUMMARY.md corrected to honestly describe forward scan as physical-order integrity check and range seeks as the comparator gate"
  gaps_remaining: []
  regressions: []
---

# Phase 01: LMDB Foundation and Comparator Proof — Verification Report (Re-verification)

**Phase Goal:** golpe's three custom comparators are linked via C++ FFI and registered through heed's `Comparator` trait (mdb_set_compare, per D-03), their scan order is verified byte-exact against a pinned strfry fixture, and the service refuses to open an incompatible environment.
**Verified:** 2026-06-11T06:00:00Z
**Status:** passed
**Re-verification:** Yes — after CR-01 gap closure (plan 01-04)

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | The service opens strfry's LMDB environment with MDB_RDONLY and the correct map_size (no write transactions ever opened) | VERIFIED | `open_read_only_env` uses `EnvFlags::READ_ONLY`; no `write_txn` helper exists anywhere in `src/`; `map_size` defaults to `10_995_116_277_760` matching `strfry.conf`; confirmed by live test run |
| 2 | The service exits loudly if Meta.dbVersion != 3 or Meta.endianness does not match the host | VERIFIED | `assert_db_version` returns `Err(DbVersionMismatch)` for any value != 3; `assert_endianness` returns `Err(EndiannessMismatch)` for `endianness != STRFRY_LITTLE_ENDIAN_MARKER (1)`; both called via `?` in `main.rs`; 5 unit tests confirm both gates; Meta fields parsed via FlatBuffer vtable |
| 3 | Comparator self-check passes against the pinned fixture DB: scan order over each Event__* index matches strfry's known-correct order — AND the registered golpe comparator is ACTUALLY exercised (not merely a physical-order walk) | VERIFIED | `run_comparator_self_check` now performs two phases: (1) forward scan for data integrity, (2) `db.range()` MDB_SET_RANGE seeks on adversarial pairs (Event__kind kind=256 lower-bound → lands on levId=2; Event__pubkeyKind pubkey=79be+kind=256 lower-bound → lands on levId=2); both phases pass on the fixture; `test_self_check_with_seek_gate_passes_on_fixture` confirms Ok(()) |
| 4 | If the self-check fails (scan order mismatch or wrong comparator), the service refuses to start (fail-closed, not silently wrong) | VERIFIED | `run_comparator_self_check` returns `Err(SelfCheckError::ComparatorSeekMismatch)` when a seek lands on wrong levId; `main.rs:64` calls via `?` propagating through anyhow to non-zero exit; `test_seek_gate_detects_memcmp_comparator_on_fixture` proves gate is non-vacuous: memcmp seek lands on levId=4 (kind=1), not levId=2 — assertion `!= 2` and pinned `== 4` both pass |
| 5 | The pinned strfry version/digest is recorded in config/docs as a shared contract with the parent DeepFry stack | VERIFIED | Digest `sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` recorded in: `Dockerfile.strfry` (`FROM dockurr/strfry@sha256:...`), `reference/PROVENANCE.md`, `config/lmdb2graphql.yaml.example` (`pinned_strfry_version`, `pinned_strfry_commit`), `src/config.rs` (Config struct fields); 3 config unit tests pass |

**Score:** 5/5 truths verified

---

## CR-01 Closure Verification (the specific gap this re-verification addresses)

### What was wrong (prior verification)

`run_comparator_self_check` validated scan order using a forward `db.iter()` walk. A forward B-tree walk follows physical page pointers in the order strfry wrote them — and never invokes the registered comparator (which only runs for `MDB_SET_RANGE` positioning). Since strfry built the fixture with its real golpe comparator, the forward scan yielded the golden order regardless of whether our reimplemented comparator was wrong, unregistered, or fell back to memcmp. This meant criteria #3 and #4 could not actually detect a comparator defect.

### What plan 01-04 added

**indexes.rs — `seek_first_ge_lev_id`:** A new public helper that opens the named Event__* sub-DB with its correct golpe comparator, then calls `db.range(rtxn, &(Bound::Included(lower_bound_key), Bound::Unbounded))` — the heed 0.22 `(Bound, Bound)` pattern for `MDB_SET_RANGE`. This is the LMDB operation that traverses B-tree branch nodes using the registered comparator. It returns the VALUE-side levId of the first entry with key >= lower_bound. `grep -c '\.range(' src/lmdb/indexes.rs` = 2 (both in `seek_range_first_lev_id`). No write txn, no `.create()` calls anywhere in the new code.

**self_check.rs — Phase 2 seek gate:** `run_comparator_self_check` now calls `build_adversarial_seeks()` which constructs two adversarial seek pairs:
- `Event__kind` lower_bound = (kind=256, ts=0): under golpe numeric ordering, kind=255 < kind=256, so the cursor must skip kind=255 and land on levId=2. Under memcmp, kind=255 LE bytes `[0xFF, 0x00,...]` > kind=256 LE bytes `[0x00, 0x01,...]`, so a memcmp cursor lands on a different entry.
- `Event__pubkeyKind` lower_bound = (pubkey=79be..., kind=256, ts=0): same kind inversion logic within the pubkey prefix.
Both expected levIds are taken from the committed golden vectors' `ordering_groups`, not regenerated at runtime (static oracle). On any mismatch the function returns `Err(SelfCheckError::ComparatorSeekMismatch{index, expected_lev_id, actual_lev_id})`. The documentation was corrected to honestly describe Phase 1 as physical-order integrity only and Phase 2 as the comparator gate.

**Non-vacuous test — `test_seek_gate_detects_memcmp_comparator_on_fixture`:** Opens the fixture WITHOUT any custom comparator (default memcmp). Performs the identical seek for (kind=256, ts=0). Asserts `landing_lev_id != 2` (non-vacuity: memcmp does NOT land on the golpe-correct answer) AND `landing_lev_id == 4` (pinned: kind=1 LE `[0x01,...]` > lower_bound `[0x00, 0x01,...]` under memcmp — kind=1 is the first golpe-order leaf entry qualifying under memcmp). This test passes and proves the seek gate would return `Err(ComparatorSeekMismatch)` on any path that doesn't exercise the golpe comparator.

**01-03-SUMMARY.md honesty fix:** The overstated clause "full-scan self-check still correctly validates comparator registration" is gone. Replacement in `decisions:` array: "iter() on LMDB B-tree returns physical page order (golpe order, as written by strfry) regardless of registered comparator — forward full-scan validates PHYSICAL-ORDER DATA INTEGRITY only (levId sequence matches the oracle), but does NOT exercise the registered comparator. Comparator correctness is validated by MDB_SET_RANGE seek gate added in plan 01-04 (CR-01 closure)." Confirmed by `grep` — the old clause is absent.

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `spam/Cargo.toml` | heed 0.22.1 pinned, no rusqlite/async-graphql | VERIFIED | `heed = "0.22.1"` present; no rusqlite/sqlx/async-graphql/axum |
| `spam/build.rs` | cc compile with -fno-exceptions | VERIFIED | `-fno-exceptions`, `-std=c++17` present |
| `spam/reference/golpe_comparators.cpp` | no throw; extern "C" wrappers | VERIFIED | Zero live `throw` statements (matches in comments only); `std::abort()` present; 3 `extern "C"` `_safe` wrappers |
| `spam/src/lmdb/comparators.rs` | 3 heed::Comparator impls | VERIFIED | `StringUint64Cmp`, `Uint64Uint64Cmp`, `StringUint64Uint64Cmp` all implement `heed::Comparator` |
| `spam/src/lmdb/env.rs` | READ_ONLY env helpers; no write_txn | VERIFIED | `open_read_only_env` (READ_ONLY); `open_fixture_env` (READ_ONLY | NO_LOCK for CI); no write_txn exported |
| `spam/src/lmdb/meta.rs` | read_meta + assert_db_version + assert_endianness | VERIFIED | All three exported; FlatBuffer vtable parser; SPIKE A3 resolved; 5 unit tests |
| `spam/src/lmdb/indexes.rs` | 6 index open helpers; ALL_EVENT_INDEXES; seek_first_ge_lev_id; .open() not .create() | VERIFIED | 4 open functions; `ALL_EVENT_INDEXES: [&str; 6]`; `seek_first_ge_lev_id` present with `db.range()` call; single `.create(` is in a comment only |
| `spam/src/lmdb/self_check.rs` | `run_comparator_self_check` with Phase 2 seek gate; ComparatorSeekMismatch variant; honest docs | VERIFIED | Phase 1 forward scan + Phase 2 `build_adversarial_seeks()` → `seek_first_ge_lev_id`; `ComparatorSeekMismatch` and `ComparatorSeekEmpty` error variants present; doc accurately describes physical-order vs comparator-gate distinction |
| `spam/src/main.rs` | startup gate: config→env→meta gates→self-check; exit on Err | VERIFIED | Sequential gate via `?` all the way through; `run_comparator_self_check` called at line 64 |
| `spam/tests/self_check_test.rs` | passes on fixture (seek gate included); fails on mutated golden; non-vacuous trip test | VERIFIED | 4 tests: passes-on-fixture, fails-on-mutation, passes-with-seek-gate, non-vacuous-memcmp-detection; all pass |
| `spam/tests/comparator_hook_smoke.rs` | kill-switch: golpe order != memcmp order | VERIFIED | 2 tests pass; heed 0.22.1 comparator hook confirmed |
| `spam/reference/PROVENANCE.md` | sha256 digest, strfry commit, regen instructions | VERIFIED | Full table recorded |
| `spam/tests/fixture/data.mdb` | committed binary fixture | VERIFIED | Exists; sha256 matches PROVENANCE |
| `spam/tests/fixture/golden_vectors/*.json` | 6 files; ordered_lev_ids; seed_commit; adversarial ordering | VERIFIED | 6 files; all have `ordered_lev_ids` (11 entries each), `seed_commit`, `derivation_notes.adversarial_demonstrations`; Event__kind shows levId=3 (kind=255) before levId=2 (kind=256) confirming golpe numeric order |
| `spam/src/config.rs` | Config struct; map_size default 10_995_116_277_760 | VERIFIED | `default_map_size() = 10_995_116_277_760`; `load()` reads `~/deepfry/lmdb2graphql.yaml`; 3 unit tests |
| `../Dockerfile.strfry` | FROM pinned to sha256 digest | VERIFIED | `FROM dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `comparators.rs` | `golpe_comparators.cpp` extern "C" symbols | FFI declarations + build.rs | VERIFIED | 3 `unsafe extern "C"` fn declarations; build.rs compiles cpp; cargo build succeeds |
| `indexes.rs` | `comparators.rs` Comparator impls | `key_comparator::<T>()` calls | VERIFIED | All 4 open helpers use explicit type-parameterized `key_comparator::<StringUint64Cmp|Uint64Uint64Cmp|StringUint64Uint64Cmp|IntegerComparator>()` |
| `indexes.rs` `seek_first_ge_lev_id` | `db.range()` MDB_SET_RANGE invocation | `(Bound::Included, Bound::Unbounded)` heed pattern | VERIFIED | `seek_range_first_lev_id` in indexes.rs uses `db.range(rtxn, &range)?` where range is the Bound tuple; this is the MDB_SET_RANGE path that invokes the registered comparator |
| `self_check.rs` Phase 2 | `indexes.rs` `seek_first_ge_lev_id` → comparators.rs golpe impls | `build_adversarial_seeks()` → `seek_first_ge_lev_id(env, seek.index, &seek.lower_bound)` | VERIFIED | Lines 354-358 of self_check.rs; both Event__kind and Event__pubkeyKind adversarial seeks wired |
| `self_check.rs` | `tests/fixture/golden_vectors/*.json` | `include_str!` embedded at compile time; `GOLDEN_VECTOR_JSON` static | VERIFIED | All 6 vectors embedded; expected_lev_id for seek gate also derived from golden vector `ordering_groups` |
| `main.rs` | `meta.rs` + `self_check.rs` | sequential `?` gate calls | VERIFIED | `assert_db_version(...)?`; `assert_endianness(...)?`; `run_comparator_self_check(...)?`; non-zero exit on any Err |
| `config.rs` | `~/deepfry/lmdb2graphql.yaml` | `serde_yaml_ng::from_str` via `dirs::home_dir()` | VERIFIED | `load()` reads `home_dir().join("deepfry/lmdb2graphql.yaml")`; `load_from()` for tests |

---

## Data-Flow Trace (Level 4)

Not applicable — Phase 1 delivers no dynamic-data rendering components. All output is process exit code (0 = success, non-zero = gate failure).

---

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All 19 tests pass (including 3 new from plan 01-04) | `cargo test --all-targets` | 19/19 passed (13 lib + 2 smoke + 4 self_check) | PASS |
| `cargo build` clean | `cargo build` | Exit 0 | PASS |
| seek_first_ge_lev_id uses `db.range()` | `grep -c '.range(' src/lmdb/indexes.rs` | 2 matches | PASS |
| self_check.rs invokes seek gate | `grep -c '.range\|seek_first_ge_lev_id' src/lmdb/self_check.rs` | 4 matches | PASS |
| No write_txn in indexes.rs (new code) | `grep -c 'write_txn' src/lmdb/indexes.rs` | 0 | PASS |
| No live `.create(` in indexes.rs | `grep -n '.create(' src/lmdb/indexes.rs` | Line 19 (comment only) | PASS |
| Overstated claim removed from 01-03-SUMMARY | `grep 'full-scan self-check still correctly validates comparator registration' 01-03-SUMMARY.md` | NOT FOUND | PASS |
| Honesty fix present in 01-03-SUMMARY | `grep 'physical page order.*01-04' 01-03-SUMMARY.md` | FOUND — correct replacement text | PASS |
| main.rs wired to run_comparator_self_check | `grep 'run_comparator_self_check' src/main.rs` | Match at line 64 | PASS |

---

## Probe Execution

No declared probes for this phase. Step 7c: SKIPPED (no `scripts/*/tests/probe-*.sh` in repo; phase uses inline integration tests).

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| LMDB-01 | 01-01, 01-03 | Read-only LMDB open; no write txn | SATISFIED | `EnvFlags::READ_ONLY`; no `write_txn` in src/; `open_read_only_env` + `open_fixture_env`; seek helper uses per-call `read_txn()` only |
| LMDB-02 | 01-03 | Refuse if dbVersion != 3 | SATISFIED | `assert_db_version` returns Err for != 3; 2 unit tests; wired in main.rs |
| LMDB-03 | 01-03 | Refuse on endianness mismatch | SATISFIED | `assert_endianness` checks `!= STRFRY_LITTLE_ENDIAN_MARKER (1)`; 2 unit tests; wired in main.rs |
| LMDB-04 | 01-02 | map_size >= strfry configured size | SATISFIED | Default `10_995_116_277_760` in `config.rs`; used in `open_read_only_env` |
| LMDB-05 | 01-01, 01-04 | Reimplement golpe comparators via mdb_set_compare | SATISFIED | 3 Comparator impls; FFI to golpe C++; hook proven by `comparator_hook_smoke` tests; comparator-dependent seeks in Phase 2 startup gate confirm registration on the live fixture path |
| LMDB-06 | 01-03, 01-04 | Self-check at startup; fail closed | SATISFIED | `run_comparator_self_check` now drives MDB_SET_RANGE seeks on adversarial pairs; non-vacuous test proves gate trips under memcmp; CR-01 closed |
| LMDB-10 | 01-02 | Pinned strfry version/digest as shared contract | SATISFIED | Digest in Dockerfile.strfry, PROVENANCE.md, config.rs, yaml.example |

No orphaned requirements for Phase 1 found in REQUIREMENTS.md.

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `reference/golpe_comparators.cpp` | 6 | `PENDING plan 02 pin` comment (upstream commit not backfilled in this file) | INFO | The upstream commit SHA (`f31a1b9`) is recorded in `PROVENANCE.md` and `golpe.yaml` header. Documentation gap only — no functional impact. |
| `reference/golpe.yaml` | body | Body is a placeholder (schema not fully vendored) | INFO | Header has the correct upstream commit. Not loaded at runtime. No functional impact. |

No TBD/FIXME/XXX debt markers in any Rust source or build.rs. No empty implementations. No hardcoded empty data flows. Read-only invariant upheld throughout new code.

**WR-01 (from code review):** `Event__pubkeyKind` seek gate has no non-vacuous regression test proving memcmp divergence for that index specifically. The code reviewer traced that under memcmp it lands on levId=5 (not levId=2), so the gate IS non-vacuous today. However, there is no pinned assertion locking this in — a future change to the `pubkey_kind_lower_bound` bytes or the fixture could silently make the pubkeyKind seek vacuous with no test failing. This is a WARNING-level robustness gap, not a correctness blocker on the current fixture. The phase goal is met; this is future hardening.

---

## Human Verification Required

None. All success criteria are verifiable programmatically via the test suite and code inspection. No visual/UX/real-time/external-service behaviors exist in this phase.

---

## Gaps Summary

No gaps. All 5 success criteria are VERIFIED. 19/19 tests pass.

CR-01 is genuinely closed:
- `seek_first_ge_lev_id` in indexes.rs uses `db.range()` (MDB_SET_RANGE), which forces LMDB to consult the registered comparator for cursor positioning — unlike `db.iter()` which follows physical page order.
- `run_comparator_self_check` Phase 2 drives seeks on the Event__kind and Event__pubkeyKind adversarial pairs, asserting the cursor lands on the golpe-correct levId=2 (not the memcmp-fallback landing).
- The non-vacuous test `test_seek_gate_detects_memcmp_comparator_on_fixture` opens the fixture without a comparator and proves the memcmp seek lands on levId=4 (kind=1), not levId=2 — demonstrating that `run_comparator_self_check` would return `Err(ComparatorSeekMismatch)` for any wrong or absent comparator registration.
- Documentation in `self_check.rs` and `01-03-SUMMARY.md` has been corrected to honestly describe the two-phase nature.
- Read-only invariants are preserved in all new code.

The code-review warning WR-01 (no non-vacuous trip test for the Event__pubkeyKind arm specifically) is the only open robustness item, and it does not affect phase goal achievement.

---

_Verified: 2026-06-11T06:00:00Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification: Yes — after plan 01-04 gap closure (CR-01)_
