---
phase: 01-lmdb-foundation-comparator-proof
reviewed: 2026-06-11T00:00:00Z
depth: standard
files_reviewed: 5
files_reviewed_list:
  - src/lmdb/indexes.rs
  - src/lmdb/self_check.rs
  - src/main.rs
  - src/lmdb/comparators.rs
  - tests/self_check_test.rs
findings:
  critical: 0
  warning: 3
  info: 3
  total: 6
status: issues_found
---

# Phase 01: Code Review Report

**Reviewed:** 2026-06-11
**Depth:** standard
**Files Reviewed:** 5
**Status:** issues_found

## Summary

Adversarial review of the post-gap-closure state of phase 01 (commits 82f5eaa, 678c43b,
8e9d7ea on base 741102a). Focus: the CR-01 comparator-seek gate added to the startup
self-check.

**CR-01 verdict: genuinely closed.** The two-phase self-check now exercises the registered
golpe comparator. Phase 2 drives `db.range()` (`MDB_SET_RANGE`) on adversarial lower-bound
keys whose golpe numeric ordering disagrees with memcmp. I verified empirically:

- `Event__kind` seek `(kind=256, ts=0)` → golpe lands on levId=2; memcmp lands on levId=4.
  The non-vacuous test `test_seek_gate_detects_memcmp_comparator_on_fixture` proves this
  divergence by re-opening the fixture with NO comparator and asserting `landing != 2`
  (and pins `== 4`). The gate is genuinely non-vacuous for this index.
- `Event__pubkeyKind` seek `(pk=79be…, kind=256, ts=0)` → golpe lands on levId=2; I traced
  that memcmp would land on levId=5. The gate trips for this index too — but no regression
  test covers it (WR-01).

**Fail-closed propagation: confirmed correct.** `run_comparator_self_check(&env, &golden)?`
in `main.rs:64` propagates any `SelfCheckError` through `anyhow::Context` to
`main() -> anyhow::Result<()>`, exiting non-zero. Both new error variants
(`ComparatorSeekMismatch`, `ComparatorSeekEmpty`) flow through this path.

**Read-only invariants: upheld.** No `.create()` / `MDB_CREATE` anywhere; all opens use
`.open()`. The new `seek_first_ge_lev_id` opens a per-call `read_txn()` dropped at function
end (short-lived, bounded to one `MDB_SET_RANGE` + first-entry read). No write txn helper
exists in the module. Production env uses `READ_ONLY` only; `NO_LOCK` is test-only.

**Panic safety: upheld.** Both `value[0..8].try_into().unwrap()` sites are guarded by an
explicit `value.len() < 8` check first, so no panic on short/disk-controlled values. The
FFI comparators abort (not panic) on malformed keys, and all keys reaching them are
fixed-length by construction in this phase.

The remaining findings are robustness and test-coverage gaps, not correctness breaks on the
committed fixture. No blockers.

## Warnings

### WR-01: `Event__pubkeyKind` seek gate has no non-vacuous regression test

**File:** `tests/self_check_test.rs:180-243`
**Issue:** The non-vacuous proof test (`test_seek_gate_detects_memcmp_comparator_on_fixture`)
covers ONLY `Event__kind`. `build_adversarial_seeks()` (`self_check.rs:252-263`) ships two
seeks — `Event__kind` AND `Event__pubkeyKind` — but only the former has a test proving that a
wrong/absent comparator produces a divergent landing. I independently traced the pubkeyKind
seek: under golpe it lands on levId=2, under memcmp on levId=5, so the gate IS non-vacuous
today. But there is no committed assertion locking that in. A future change to the
`pubkey_kind_lower_bound` bytes, the pubkey constant, or the fixture could silently make the
pubkeyKind seek land on levId=2 under both comparators (vacuous pass) with no test failing —
re-opening the CR-01 gap for that index.
**Fix:** Add a sibling to test (d) that opens `Event__pubkeyKind` without a comparator
(memcmp fallback) and asserts the landing levId `!= 2` (and pins the expected memcmp landing,
which my trace puts at levId=5), mirroring the existing `Event__kind` proof.

### WR-02: `seek_first_ge_lev_id` silently returns the lowest dup value for multi-dup keys

**File:** `src/lmdb/indexes.rs:258-285`
**Issue:** Event__* indexes are `MDB_DUPSORT + MDB_INTEGERDUP`; a single key can map to
several levId values. `seek_range_first_lev_id` takes `iter.next()` — the first
dup-sorted (smallest) levId for the landed key — and returns it as "the" levId. This happens
to be correct for the current adversarial pairs (kind=256 / pk+kind=256 each have exactly one
event, levId=2). But the function's contract and name suggest "the levId at the first key
>= bound" without documenting that multi-dup keys collapse to the lowest levId. If this helper
is reused (it is `pub`) with an adversarial key that lands on a multi-dup key whose
golpe-correct answer is not the lowest dup, the assertion would compare against the wrong
value and could either spuriously trip or vacuously pass.
**Fix:** Document the multi-dup contract on `seek_first_ge_lev_id` ("returns the lowest
dup-sorted levId of the first key >= bound"). If callers ever need a specific dup, expose the
full key+value or the dup set rather than only `next()`.

### WR-03: malformed VALUE handled inconsistently between Phase 1 and Phase 2 (scan skips, seek maps to empty)

**File:** `src/lmdb/indexes.rs:274-280` (seek) vs `src/lmdb/indexes.rs:305-313` (scan)
**Issue:** On a VALUE shorter than 8 bytes, `collect_lev_ids_dup` logs and `continue`s
(drops the entry, shortening the sequence), while `seek_range_first_lev_id` logs and returns
`Ok(None)`. In Phase 2 that `None` is reported as `ComparatorSeekEmpty` ("seek returned no
entry") — a misleading diagnostic for what is actually a malformed-value condition, not an
empty range. Both paths still fail closed (Phase 1 trips `OrderMismatch` on the changed
length; Phase 2 trips `ComparatorSeekEmpty`), so this is not a correctness break, but the
divergent handling and the misattributed error class will mislead an operator triaging a
real corruption.
**Fix:** Use one shared levId-decode helper that returns a distinct malformed-value error
(e.g. `IndexError::MalformedLevId { len }`), and surface it as such in both phases instead of
silently skipping (Phase 1) or masquerading as an empty range (Phase 2).

## Info

### IN-01: `seek_first_ge_lev_id` reports a valid index as `SubDbNotFound`

**File:** `src/lmdb/indexes.rs:242-246`
**Issue:** The `_ =>` arm returns `IndexError::SubDbNotFound { name: short_name }` for any
unhandled short name. `Event__created_at` is a real, existing sub-DB that this function simply
does not route (it has no `IntegerComparator` arm). If a future adversarial seek targets
`Event__created_at`, the error "Sub-DB not found — is this the right LMDB directory?" is wrong
and would send an operator on a false hunt for a missing database.
**Fix:** Either add an `Event__created_at` arm using `open_index_created_at`, or return a
distinct error like `IndexError::SeekUnsupportedIndex { name }` for indexes the seek path
intentionally does not handle.

### IN-02: in-source doc comments duplicate the adversarial-pair rationale across three files

**File:** `src/lmdb/indexes.rs:204-216`, `src/lmdb/self_check.rs:26-32`,
`tests/self_check_test.rs:156-178`
**Issue:** The kind=255-vs-256 memcmp-divergence explanation is reproduced verbatim-ish in
three places. The numbers (levId=2/3/4) are load-bearing and could drift between copies during
edits, leaving contradictory documentation about what the gate proves.
**Fix:** Keep the canonical derivation in the golden vector JSON `derivation_notes` (already
present) and have the code comments reference it rather than restate the byte-level argument.

### IN-03: doctest / clippy fail in this environment due to `--check-cfg` flag mismatch

**File:** `src/lmdb/self_check.rs:294-301` (the `rust,no_run` doc example)
**Issue:** `cargo test --doc` and `cargo clippy` fail with "the `-Z unstable-options` flag must
also be passed to enable the flag `check-cfg`". This originates from the build harness /
toolchain (a clippy-driver at `/usr/local/bin` vs the rustup stable toolchain) and fires even
though the doc example is `no_run`. `cargo build` and `cargo test` (lib + integration) all
pass cleanly. This is environment noise, not a defect introduced by 01-04, but it will break
a CI step that runs `cargo test` (which includes doctests) or `cargo clippy -- -D warnings`.
**Fix:** Out of scope for the code change; ensure CI pins a consistent toolchain (the
`rust-toolchain.toml` channel) so the `--check-cfg` flag is accepted, or mark the doc example
`ignore` if it is not meant to be compiled.

---

_Reviewed: 2026-06-11_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
