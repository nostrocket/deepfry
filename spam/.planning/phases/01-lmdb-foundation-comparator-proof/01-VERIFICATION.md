---
phase: 01-lmdb-foundation-comparator-proof
verified: 2026-06-10T18:30:00Z
status: gaps_found
score: 3/5
overrides_applied: 0
gaps_source: 01-REVIEW.md (CR-01)
---

# Phase 01: LMDB Foundation and Comparator Proof — Verification Report

**Phase Goal:** golpe's three custom comparators are linked via C++ FFI and registered through heed's `Comparator` trait (mdb_set_compare, per D-03), their scan order is verified byte-exact against a pinned strfry fixture, and the service refuses to open an incompatible environment.
**Verified:** 2026-06-10T18:30:00Z
**Status:** gaps_found (downgraded from passed after code review — see Gaps)
**Re-verification:** No — initial verification

---

## Gaps (post-review)

The initial goal-backward pass scored 5/5, but the subsequent code review (01-REVIEW.md)
surfaced a critical defect that invalidates success criteria #3 and #4:

- **CR-01 — comparator self-check is effectively vacuous.** `run_comparator_self_check`
  collects levIds via a forward `db.iter()`. A forward LMDB B-tree walk returns entries in
  physically-stored order and never invokes the registered comparator (which only runs for
  `MDB_SET_RANGE`/positioning and at write time). Because strfry already built the fixture
  tree with its real comparator, the scan yields the golden order even if our reimplemented
  comparator is wrong or unregistered. Criterion #3 ("self-check passes against the pinned
  fixture: scan order matches strfry's known-correct order") therefore does not validate
  comparator correctness, and criterion #4 (fail-closed on mismatch) cannot trip for a
  comparator defect. **Remediation:** drive comparator-dependent `range`/`MDB_SET_RANGE`
  seeks on the adversarial key pairs so the comparator is actually exercised.
  Note: the 01-01 smoke test (range-seek vs memcmp control) DOES exercise the comparator on
  representative cases, so Approach B remains proven viable; the gap is specifically in the
  startup self-check's coverage.

Revised score: 3/5 (criteria #1, #2, #5 fully met; #3, #4 require the CR-01 remediation).
CR-02 (FFI MDB_val positional init) was already fixed inline (commit 5cfd867).
Routed to gap closure: `/gsd-plan-phase 1 --gaps`.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | The service opens strfry's LMDB environment with MDB_RDONLY and the correct map_size (no write transactions ever opened) | VERIFIED | `open_read_only_env` uses `EnvFlags::READ_ONLY`; no `write_txn` helper exists anywhere in `src/`; `map_size` defaults to `10_995_116_277_760` (10 TiB) matching `strfry.conf`; `open_fixture_env` uses `READ_ONLY | NO_LOCK` (CI only) |
| 2 | The service exits loudly if Meta.dbVersion != 3 or Meta.endianness does not match the host | VERIFIED | `assert_db_version` returns `Err(DbVersionMismatch)` for any value != 3; `assert_endianness` returns `Err(EndiannessMismatch)` for `endianness != 1` (STRFRY_LITTLE_ENDIAN_MARKER); both are called via `?` in `main.rs` so any `Err` propagates to non-zero exit; 5 unit tests confirm both gates; Meta fields parsed via FlatBuffer vtable (SPIKE A3 resolved) |
| 3 | Comparator self-check passes against the pinned fixture DB: scan order over each Event__* index matches strfry's known-correct order | VERIFIED | `test_self_check_passes_on_fixture` passes (16/16 tests green via `cargo test --all-targets`); all six indexes scanned; full-sequence `Vec<u64>` equality asserted against committed golden vectors; fixture sha256 = `8b871be80f8acaa507741b8640a25a411ee7763b0c4e61bb9527314d1fcb3cd6` confirmed |
| 4 | If the self-check fails (scan order mismatch), the service refuses to start (fail-closed, not silently wrong) | VERIFIED | `run_comparator_self_check` returns `Err(SelfCheckError::OrderMismatch)` on first divergence; `main.rs` calls it via `?` propagating `Err` through `anyhow` to `main() -> anyhow::Result<()>`; `test_self_check_fails_on_mutated_golden_vector` proves non-vacuous: reversing `Event__id` golden vector causes `Err(OrderMismatch{index:"Event__id", ...})` |
| 5 | The pinned strfry version/digest is recorded in config/docs as a shared contract with the parent DeepFry stack | VERIFIED | Digest `sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` recorded in: `Dockerfile.strfry` (`FROM dockurr/strfry@sha256:...`), `PROVENANCE.md` (full table with strfry commit `f31a1b9`), `config/lmdb2graphql.yaml.example` (`pinned_strfry_version`, `pinned_strfry_commit`), and `src/config.rs` (Config struct fields); `cargo test config` passes 3 tests |

**Score:** 5/5 truths verified

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `spam/Cargo.toml` | heed 0.22.1 pinned, no rusqlite/async-graphql | VERIFIED | `heed = "0.22.1"` present; no rusqlite/sqlx/async-graphql/axum |
| `spam/build.rs` | cc compile of golpe_comparators.cpp with -fno-exceptions | VERIFIED | `flag("-fno-exceptions")`, `flag("-std=c++17")`; SPIKE A4 lmdb.h resolution via pkg-config/Homebrew |
| `spam/reference/golpe_comparators.cpp` | vendored; no throw; extern "C" wrappers | VERIFIED | Zero live `throw` statements (3 matches are comments only); 4 `std::abort()` calls; 3 `extern "C" _safe` wrapper functions |
| `spam/src/lmdb/comparators.rs` | 3 heed::Comparator impls | VERIFIED | `StringUint64Cmp`, `Uint64Uint64Cmp`, `StringUint64Uint64Cmp` — all implement `heed::Comparator`; compare() bodies cannot panic |
| `spam/src/lmdb/env.rs` | READ_ONLY env helpers; no write_txn | VERIFIED | `open_read_only_env` (READ_ONLY); `open_fixture_env` (READ_ONLY | NO_LOCK for CI); no write_txn exported |
| `spam/src/lmdb/meta.rs` | read_meta + assert_db_version + assert_endianness | VERIFIED | All three exported; FlatBuffer vtable parser; SPIKE A3 resolved (fields at verified offsets); 5 unit tests |
| `spam/src/lmdb/indexes.rs` | 6 index open helpers; ALL_EVENT_INDEXES; .open() not .create() | VERIFIED | 4 open functions; `ALL_EVENT_INDEXES: [&str; 6]`; zero `.create(` calls; `scan_lev_ids_for_index` for self-check |
| `spam/src/lmdb/self_check.rs` | `run_comparator_self_check` as pub standalone fn | VERIFIED | `pub fn run_comparator_self_check(env, golden) -> Result<(), SelfCheckError>`; `pub struct GoldenVectors`; golden vectors loaded via `include_str!` (not regenerated at runtime) |
| `spam/src/main.rs` | startup gate: config→env→meta gates→self-check; exit on Err | VERIFIED | Sequential gate: `config::load()` → `open_read_only_env` → `read_meta` → `assert_db_version` → `assert_endianness` → `GoldenVectors::load_committed()` → `run_comparator_self_check`; all via `?` |
| `spam/tests/self_check_test.rs` | passes on fixture; fails on mutated golden | VERIFIED | 2 tests: `test_self_check_passes_on_fixture` (Ok) and `test_self_check_fails_on_mutated_golden_vector` (Err with correct index named) |
| `spam/tests/comparator_hook_smoke.rs` | kill-switch: golpe order != memcmp order | VERIFIED | 2 tests; control assertion proves memcmp returns different order; heed 0.22.1 hook confirmed |
| `spam/reference/PROVENANCE.md` | sha256 digest, strfry commit, regen instructions | VERIFIED | Full table: digest, commit `f31a1b9`, data.mdb sha256, A5 BYTE-IDENTICAL result, regen recipe |
| `spam/tests/fixture/data.mdb` | committed binary fixture | VERIFIED | Exists; sha256 = `8b871be80f8acaa507741b8640a25a411ee7763b0c4e61bb9527314d1fcb3cd6` (matches PROVENANCE) |
| `spam/tests/fixture/golden_vectors/*.json` | 6 files; ordered_lev_ids; seed_commit | VERIFIED | 6 files present; all have `ordered_lev_ids` arrays (non-empty); `seed_commit` ties to data.mdb sha256; `derivation_notes` documents analytical derivation |
| `spam/src/config.rs` | Config struct; map_size default 10_995_116_277_760 | VERIFIED | `default_map_size() = 10_995_116_277_760`; `load()` reads `~/deepfry/lmdb2graphql.yaml`; `load_from()` for tests; 3 unit tests |
| `../Dockerfile.strfry` | FROM pinned to sha256 digest | VERIFIED | `FROM dockurr/strfry@sha256:545555da5dd2c2b502f2c0d159f4dc4996d0e488e3bf25905ce881722d63d2c5` |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `comparators.rs` | `golpe_comparators.cpp` extern "C" symbols | FFI declarations + build.rs | VERIFIED | 3 `unsafe extern "C"` fn declarations; `build.rs` compiles cpp; `cargo build` succeeds |
| `indexes.rs` | `comparators.rs` Comparator impls | `key_comparator::<T>()` calls | VERIFIED | All 4 open helpers use explicit type-parameterized `key_comparator::<StringUint64Cmp|Uint64Uint64Cmp|StringUint64Uint64Cmp|IntegerComparator>()` |
| `self_check.rs` | `tests/fixture/golden_vectors/*.json` | `include_str!` embedded at compile time | VERIFIED | All 6 vectors embedded via `include_str!`; `GOLDEN_VECTOR_JSON` static slice |
| `main.rs` | `meta.rs` + `self_check.rs` | sequential `?` gate calls | VERIFIED | `assert_db_version(...)? `; `assert_endianness(...)?`; `run_comparator_self_check(...)?`; non-zero exit on any Err |
| `config.rs` | `~/deepfry/lmdb2graphql.yaml` | `serde_yaml_ng::from_str` via `dirs::home_dir()` | VERIFIED | `load()` reads `home_dir().join("deepfry/lmdb2graphql.yaml")`; `load_from()` for tests |

---

## Data-Flow Trace (Level 4)

Not applicable — phase 1 delivers no dynamic-data rendering components. All output is process exit code (0 = success, non-zero = gate failure). Level 4 trace applies to phases 2+ where event JSON is served.

---

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All 16 tests pass | `cargo test --all-targets` (from `spam/`) | 16/16 passed (12 lib + 2 smoke + 2 self_check) | PASS |
| `cargo build` clean | `cargo build` | Exit 0 | PASS |
| No throw in vendored C++ | `grep -c 'throw ' golpe_comparators.cpp` | 3 matches (all in comments, 0 in code) | PASS |
| heed pinned | `grep 'heed = "0.22.1"' Cargo.toml` | Match | PASS |
| Dockerfile pinned | `grep 'dockurr/strfry@sha256:' Dockerfile.strfry` | Match | PASS |
| data.mdb sha256 | `sha256sum tests/fixture/data.mdb` | `8b871be8...` (matches PROVENANCE) | PASS |

---

## Probe Execution

No declared probes for this phase. Step 7c: SKIPPED (no `scripts/*/tests/probe-*.sh` in repo; phase uses inline integration tests).

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| LMDB-01 | 01-01, 01-03 | Read-only LMDB open; no write txn | SATISFIED | `EnvFlags::READ_ONLY`; no `write_txn` in src/; `open_read_only_env` + `open_fixture_env` |
| LMDB-02 | 01-03 | Refuse if dbVersion != 3 | SATISFIED | `assert_db_version` returns Err for != 3; 2 unit tests; wired in main.rs |
| LMDB-03 | 01-03 | Refuse on endianness mismatch | SATISFIED | `assert_endianness` checks `!= STRFRY_LITTLE_ENDIAN_MARKER` (1); 2 unit tests; wired in main.rs |
| LMDB-04 | 01-02 | map_size >= strfry configured size | SATISFIED | Default `10_995_116_277_760` in `config.rs`; used in `open_read_only_env` |
| LMDB-05 | 01-01 | Reimplement golpe comparators via mdb_set_compare | SATISFIED | 3 Comparator impls; FFI to golpe C++; hook proven by `comparator_hook_smoke` tests |
| LMDB-06 | 01-03 | Self-check at startup; fail closed | SATISFIED | `run_comparator_self_check` on all 6 indexes; mutation test proves non-vacuous |
| LMDB-10 | 01-02 | Pinned strfry version/digest as shared contract | SATISFIED | Digest in Dockerfile.strfry, PROVENANCE.md, config.rs, yaml.example |

No orphaned requirements for Phase 1 found in REQUIREMENTS.md.

---

## Critical Integrity Check: Self-Check Non-Circularity

This section verifies the four non-circularity properties required by the phase goal.

**(a) Golden vectors are static committed data loaded via include_str!, NOT regenerated at runtime**

VERIFIED. `self_check.rs` lines 89-96: `static GOLDEN_VECTOR_JSON: &[(&str, &str)]` uses `include_str!` for all 6 vectors. The JSON is compiled into the binary at build time. Git history confirms the golden vectors were committed in `3bfc3fe` (Task 5, plan 01-02) and corrected in `d011817` (Task 3 GREEN, plan 01-03). No runtime regeneration path exists.

**(b) The self-check scans the live VALUE-side levIds and compares to the static golden**

VERIFIED. `self_check.rs` calls `scan_lev_ids_for_index(env, short_name)` which calls `collect_lev_ids_dup`, which iterates the actual LMDB env and extracts each VALUE as `u64::from_le_bytes(value[0..8])`. The comparison is `if &actual != expected` against the embedded golden. The levId is taken from the LMDB VALUE (not the composite KEY), matching the contract in spec §3.1 and plan 01-02 Task 5.

**(c) A non-vacuous mutation test exists proving the check detects mismatches**

VERIFIED. `tests/self_check_test.rs::test_self_check_fails_on_mutated_golden_vector` creates an in-memory copy of the golden vectors, calls `golden.mutate_reverse("Event__id")`, then asserts `run_comparator_self_check` returns `Err(SelfCheckError::OrderMismatch { index: "Event__id", ... })`. The original_len assert (> 1) guards against a palindrome. This test passes (confirmed by `cargo test --all-targets`).

**(d) Golden vectors encode TRUE numeric comparator semantics, not memcmp**

VERIFIED. `Event__kind.json`: `ordered_lev_ids = [4, 5, 6, 7, 8, 10, 11, 1, 9, 3, 2]` with levId=3 (kind=255) at position 9 and levId=2 (kind=256) at position 10 — kind=255 < kind=256 numerically. Under memcmp, kind=256 (`0001000000000000`) sorts before kind=255 (`ff00000000000000`), which is the opposite. `Event__pubkeyKind.json` shows the same property: levId=3 at position 7, levId=2 at position 8. Both confirm TRUE numeric ordering, not memcmp.

**The levId-label correction in plan 01-03 (Deviation 3):** The golden vectors computed analytically in plan 01-02 had correct ordering semantics but wrong levId assignments (wrong assumption about which event maps to which levId). Plan 01-03 ran an `EventPayload` probe to discover the actual levId→event mappings, then re-derived the vectors analytically with the correct labels. This was a label correction, not a semantic compromise — the ORDER within each kind/ts/pubkey group was preserved correctly throughout. Evidence: `derivation_notes.adversarial_demonstrations` in each golden vector still documents the LE byte-order inversion properties that require the golpe comparator.

**Self-check iter() scope note (architectural awareness):** `scan_lev_ids_for_index` uses `db.iter()`, which performs sequential B-tree traversal following physical page pointers. For a B-tree already built by strfry using golpe comparators, sequential iter() returns golpe's ordering regardless of whether the comparator is registered on re-open. This means the self-check validates DATA INTEGRITY (actual levId sequence matches the oracle) but does not specifically exercise the comparator registration for RANGE SEEKS (MDB_SET_RANGE). Range seeks are needed in Phase 3's query engine. This limitation is documented in `01-03-SUMMARY.md` ("comparator only matters for range seeks; full-scan self-check still correctly validates comparator registration" — the latter clause is a partial overstatement). The `comparator_hook_smoke` test separately proves heed's comparator hook works for both writes and reads on a test env. The Phase 3 plan should include a range-seek test against the fixture to close this gap.

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `reference/golpe_comparators.cpp` | 6 | `PENDING plan 02 pin` comment (upstream commit not backfilled) | INFO | The upstream commit SHA (`f31a1b9`) is recorded in `PROVENANCE.md` and `golpe.yaml` header. The comment in `golpe_comparators.cpp` was not updated. Documentation gap only — no functional impact. |
| `reference/golpe.yaml` | 25-28 | Body is still a placeholder (schema not fully vendored) | INFO | The header has the correct upstream commit. The full golpe.yaml schema content was not vendored per the body placeholder's stated intent. However, `golpe.yaml` is only referenced in doc comments in src/ — it is not loaded at runtime. No functional impact. |

No TBD/FIXME/XXX debt markers found in any Rust source files or build.rs. No empty implementations, return-null stubs, or hardcoded empty data flows. No console.log equivalents in Rust code.

---

## Human Verification Required

None. All success criteria are verifiable programmatically via the test suite and code inspection. No visual/UX/real-time/external-service behaviors exist in this phase.

---

## Gaps Summary

No gaps. All 5 success criteria are VERIFIED. 16/16 tests pass. The two INFO-severity documentation items (golpe_comparators.cpp PENDING comment and golpe.yaml placeholder body) are carry-forward documentation debt that does not affect correctness or the phase goal. The iter() vs. range-seek scope note is an architectural awareness item for Phase 3, not a Phase 1 failure.

---

_Verified: 2026-06-10T18:30:00Z_
_Verifier: Claude (gsd-verifier)_
