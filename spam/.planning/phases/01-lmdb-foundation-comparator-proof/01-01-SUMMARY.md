---
phase: 01-lmdb-foundation-comparator-proof
plan: "01"
subsystem: database
tags: [lmdb, heed, rust, golpe, comparator, ffi, strfry, nostr]

# Dependency graph
requires: []
provides:
  - Rust crate scaffold (lmdb2graphql) pinned to heed 0.22.1 with read-only LMDB env helpers
  - Golpe C++ comparators vendored with exception-safety fix and extern "C" wrappers compiled via build.rs
  - Three heed::Comparator impls (StringUint64Cmp, Uint64Uint64Cmp, StringUint64Uint64Cmp) bridging to golpe FFI
  - Comparator hook smoke proof: heed 0.22.1 registers golpe foreign comparator via mdb_set_compare and LMDB uses it for scan ordering
  - Go/no-go decision: Approach B PROCEED — highest project risk retired
affects:
  - 01-02-fixture-pin: inherits the crate and comparator module; adds fixture DB + pinned strfry version gate
  - 01-03-production-gate: inherits all of 01-01 and 01-02; adds startup self-check and config loading

# Tech tracking
tech-stack:
  added:
    - heed 0.22.1 (LMDB typed wrapper; Comparator trait; key_comparator; EnvFlags::READ_ONLY; .open() without MDB_CREATE)
    - lmdb-master-sys 0.2.6 (transitive; LMDB C library)
    - cc 1.2.63 (build dep; C++ compilation of vendored golpe comparators)
    - tracing 0.1 + tracing-subscriber 0.3 with env-filter and json features
    - thiserror 2 + anyhow 1 (error handling)
    - serde 1 + serde_json 1 (JSON deserialization groundwork)
    - serde_yaml 0.9 (DEVIATION — serde_yaml_ng 0.10 intended; see deviations)
    - dirs 5.0.1 (~/deepfry/ home resolution)
    - tempfile 3 (dev dep; test tempdir)
  patterns:
    - Read-only LMDB access via EnvFlags::READ_ONLY; no write_txn helper exposed
    - NO_LOCK only in fixture/CI context (open_fixture_env); never in production (open_read_only_env)
    - Zero-sized enum types as heed::Comparator implementations (heed convention)
    - C++ comparators compiled with -fno-exceptions -fno-rtti -std=c++17 via build.rs/cc crate
    - extern "C" safe wrappers taking (ptr, len) pairs — no lmdb-sys types crossing FFI boundary
    - throw replaced by std::abort() in vendored C++ — exception UB eliminated (RFC 2945)
    - compare() impls that cannot panic (no panic across extern "C")

key-files:
  created:
    - spam/Cargo.toml (crate manifest; heed 0.22.1 pinned)
    - spam/Cargo.lock (locked dependency tree)
    - spam/rust-toolchain.toml (channel = "stable")
    - spam/build.rs (cc-crate compile of golpe_comparators.cpp)
    - spam/src/main.rs (minimal tracing init + version print)
    - spam/src/lmdb/mod.rs (barrel: pub mod env; pub mod comparators)
    - spam/src/lmdb/env.rs (open_read_only_env, open_fixture_env)
    - spam/src/lmdb/comparators.rs (three heed::Comparator impls over golpe FFI)
    - spam/reference/golpe_comparators.cpp (vendored; exception-patched; extern "C" wrappers)
    - spam/reference/lmdbxx/lmdb++.h (vendored header-only lmdbxx)
    - spam/reference/golpe.yaml (schema; upstream provenance comment)
    - spam/tests/comparator_hook_smoke.rs (go/no-go kill-switch test; 2 tests)
  modified: []

key-decisions:
  - "Approach B: GO/PROCEED — heed 0.22.1 registers golpe foreign comparator via mdb_set_compare; LMDB uses it for range-scan ordering; adversarial smoke proof (golpe order != memcmp order) passed; plans 01-02 and 01-03 are unblocked"
  - "heed 0.22.1 pinned per plan spec (CLAUDE.md); prior executor deviation (0.20.5) resolved in Task 4 continuation"
  - "serde_yaml_ng deferred: serde_yaml 0.9 used instead of serde_yaml_ng 0.10; resolution gated by plan 01-02 Task 1 package legitimacy checkpoint"
  - "tracing-subscriber json feature restored: was unavailable in prior executor run; resolved when crates.io became reachable"

patterns-established:
  - "Pattern: All LMDB opens use EnvFlags::READ_ONLY; write transactions never exposed outside test-local contexts"
  - "Pattern: golpe comparators bridged via extern 'C' (ptr,len) wrappers — safe Rust ↔ C++ boundary; zero LMDB types crossing the FFI"
  - "Pattern: heed zero-sized enum type implements heed::Comparator; compare() body calls unsafe FFI and returns result.cmp(&0)"
  - "Pattern: Comparator API smoke test uses adversarial keys (golpe LE uint64 order != memcmp byte order) to prove the hook is active"

requirements-completed: [LMDB-05, LMDB-01]

# Metrics
duration: 45min
completed: 2026-06-10
---

# Phase 01 Plan 01: LMDB Foundation and Comparator Proof Summary

**heed 0.22.1 registers golpe's foreign C++ comparator via mdb_set_compare on a read-only LMDB sub-DB; adversarial smoke test proves LMDB uses it for scan ordering — Approach-B go/no-go gate GREEN**

## Performance

- **Duration:** ~45 min (across two executor runs)
- **Started:** 2026-06-10T06:00:00Z
- **Completed:** 2026-06-10
- **Tasks:** 4 (Tasks 1-3 in prior run; Task 4 + heed 0.22.1 upgrade in continuation)
- **Files modified:** 12

## Accomplishments

- Proof that heed 0.22.1 can register a foreign C++ comparator on an existing LMDB sub-DB opened read-only, and LMDB uses it for range-scan ordering — the genuine Approach-B kill-switch per CONTEXT.md
- Three golpe comparators vendored, exception-patched (throw → std::abort()), and compiled exception-free via build.rs/cc with -fno-exceptions; exposed as three heed::Comparator impls
- Read-only env helpers (open_read_only_env, open_fixture_env) that cannot open a write transaction — LMDB-01 foundation
- heed 0.22.1 now pinned (upgraded from 0.20.5 deviation of the prior executor run); comparator proof re-verified on the pinned version

## Task Commits

Each task was committed atomically:

1. **Task 1: Scaffold the production crate and pin the Phase 1 stack** - `1d8f4ec` (feat)
2. **Task 2: Vendor golpe comparators with exception fix and FFI bridge** - `57fa33d` (feat)
3. **Task 3: Comparator-hook smoke proof — go/no-go kill-switch** - `40d4e2d` (test)
4. **Task 4 (checkpoint doc):** go/no-go checkpoint recorded - `b33face` (docs)
5. **Task 4 (continuation): heed 0.22.1 upgrade + proof re-verification** - `3a7af85` (chore)

## Files Created/Modified

- `spam/Cargo.toml` — crate manifest; heed 0.22.1 pinned; full Phase 1 dependency set
- `spam/Cargo.lock` — locked dep tree; heed 0.22.1, lmdb-master-sys 0.2.6
- `spam/rust-toolchain.toml` — channel = "stable"
- `spam/build.rs` — cc compile of golpe_comparators.cpp with -fno-exceptions -std=c++17
- `spam/src/main.rs` — minimal tracing init and crate version print
- `spam/src/lmdb/mod.rs` — module barrel (pub mod env; pub mod comparators)
- `spam/src/lmdb/env.rs` — open_read_only_env (production) and open_fixture_env (CI); EnvFlags::READ_ONLY enforced; no write_txn exposed
- `spam/src/lmdb/comparators.rs` — StringUint64Cmp, Uint64Uint64Cmp, StringUint64Uint64Cmp as heed::Comparator impls over golpe FFI
- `spam/reference/golpe_comparators.cpp` — vendored golpe comparators; all throw replaced with std::abort(); three extern "C" _safe wrapper functions
- `spam/reference/lmdbxx/lmdb++.h` — vendored header-only lmdbxx (upstream provenance comment)
- `spam/reference/golpe.yaml` — golpe schema (upstream pin placeholder; to be resolved in plan 01-02)
- `spam/tests/comparator_hook_smoke.rs` — adversarial smoke test proving golpe order != memcmp order on re-opened read-only sub-DB

## Decisions Made

- **GO/PROCEED on Approach B:** heed 0.22.1 correctly hooks golpe's comparator. The adversarial proof (keys whose LE uint64 byte order inverts numeric order) confirmed the hook changes scan results — plans 01-02 and 01-03 are unblocked.
- **heed 0.22.1 pinned:** Upgraded from the 0.20.5 deviation when crates.io became reachable. Comparator API unchanged between 0.20.5 and 0.22.1 — no source changes needed.
- **serde_yaml_ng deferred:** serde_yaml 0.9 (deprecated upstream) used because serde_yaml_ng provenance gating is handled by plan 01-02 Task 1 checkpoint. The package legitimacy audit for all crates (tagged [ASSUMED] in RESEARCH) is a single gate in plan 01-02.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] heed 0.20.5 used instead of 0.22.1 in prior executor run**
- **Found during:** Prior executor Task 1 (crates.io unreachable)
- **Issue:** crates.io was unreachable; heed 0.22.1 could not be fetched; prior executor used the locally-cached 0.20.5 and documented it as a deviation
- **Fix:** In this continuation run, crates.io was reachable; bumped Cargo.toml to heed = "0.22.1", ran cargo update, confirmed build and smoke test pass on 0.22.1
- **Files modified:** spam/Cargo.toml, spam/Cargo.lock, spam/tests/comparator_hook_smoke.rs (version string in docstring)
- **Verification:** `cargo test --test comparator_hook_smoke` — 2/2 tests pass on heed 0.22.1
- **Committed in:** `3a7af85`

**2. [Deviation - Deferred] serde_yaml 0.9 instead of serde_yaml_ng 0.10**
- **Status:** Outstanding — not resolved in this plan. Resolution gated by plan 01-02 Task 1 package legitimacy checkpoint.
- **Impact:** serde_yaml 0.9 is deprecated but functional for YAML config deserialization; no API difference for the Phase 1 use case.

**3. [Rule 3 - Resolved] tracing-subscriber "json" feature missing in prior run**
- **Found during:** Prior executor Task 1 (tracing-serde dep unavailable in local cache)
- **Fix:** In continuation run, crates.io reachable; cargo add restored the json feature
- **Files modified:** spam/Cargo.toml
- **Committed in:** `3a7af85`

---

**Total deviations:** 1 auto-fixed (blocking resolved in continuation), 1 deferred (serde_yaml_ng upgrade), 1 minor resolved (json feature)
**Impact on plan:** All auto-fixes necessary for spec compliance. No scope creep. The serde_yaml deviation is tracked and gated by plan 01-02.

## Known Stubs

None — plan 01-01 is a proof-of-concept foundation; no UI or data-serving paths exist yet. No stub values or placeholder data flows.

## Threat Flags

No new security-relevant surfaces beyond those in the plan's threat_model. T-01-01 (READ_ONLY), T-01-02 (no-exceptions / std::abort), and T-01-03 (control assertion proves hook active) are all mitigated as specified.

## Issues Encountered

- crates.io was unreachable during the prior executor run, causing the heed 0.20.5 fallback. Resolved in this continuation run.
- No other issues; all tests pass cleanly.

## User Setup Required

None — no external service configuration required for this plan. Plan 01-02 will require a strfry fixture DB and will generate a USER-SETUP.md if manual fixture preparation is needed.

## Next Phase Readiness

- Plan 01-02 (fixture-pin) is unblocked: the comparator module and env helpers are ready to accept a real strfry fixture DB
- Plan 01-03 (production-gate) is unblocked: the Comparator impls are ready for the startup self-check
- Outstanding follow-up before plan 01-02 finishes: serde_yaml_ng 0.10 swap (plan 01-02 Task 1 package legitimacy checkpoint covers this)
- heed 0.22.1 is now the active pinned version — no version drift outstanding

---
*Phase: 01-lmdb-foundation-comparator-proof*
*Completed: 2026-06-10*
