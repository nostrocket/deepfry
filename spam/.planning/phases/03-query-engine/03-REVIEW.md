---
phase: 03-query-engine
reviewed: 2026-06-13T07:53:23Z
depth: deep
files_reviewed: 1
files_reviewed_list:
  - src/query/engine.rs
findings:
  critical: 2
  warning: 2
  info: 1
  total: 5
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-13T07:53:23Z
**Depth:** deep
**Files Reviewed:** 1
**Status:** issues_found

## Summary

Second gap-closure pass (03-11) on `execute_query_internal`, reviewed adversarially against the
two stranding BLOCKERs the first pass (03-10) introduced. Cross-file tracing covered
`merge.rs` (`merge_windowed`, `refill_stream`, `StreamState`) and `scan.rs`
(`collect_window`/`collect_bounded` reverse DUPSORT semantics) to verify the convergence and
stranding claims rather than trusting the green test suite (all 22 engine tests pass; the suite
does not prove correctness here).

**Verdict: the CR-02 fix did NOT close the data-stranding class — it relocated it.**

- **CR-01 (empty-valid fallback cursor): genuinely fixed.** `deepest_scanned` is updated from
  `last_merged` every round before the break decision (engine.rs:349-351), and the new
  `else if valid.is_empty() && !exhausted` branch (engine.rs:432-439) returns
  `Some(deepest_scanned)`. Empirically verified: an empty-valid budget-capped page returns a
  cursor and full pagination surfaces all matching events. No defect remains in this branch.

- **CR-02 (no-progress break + ts-advance override): introduces a THIRD stranding bug
  (finding CR-01 below).** The DEVIATION from the plan (plan specified a *pure* break; executor
  added `deepest_scanned = (stalled_ts - 1, u64::MAX)`) is the source. The override silently
  abandons every event in a fat timestamp group **beyond `emit_limit`** by jumping the cursor
  past the whole group. The passing regression test is rigged — it pins `FAT_COUNT == emit_limit`
  exactly so the merge floor coincides with lev_id 1, masking the loss. With
  `FAT_COUNT > emit_limit` (the comment's own stated trigger, "a fat timestamp holds >=
  emit_limit events"), events are permanently stranded. Proven below with a probe test.

- A secondary non-termination bug exists at `stalled_ts == 0` (finding CR-02 below).

DESC (created_at, lev_id) ordering (D-10) and opaque-cursor semantics (D-11) are otherwise
preserved.

## Critical Issues

### CR-01: CR-02 ts-advance override strands fat-group events beyond `emit_limit` (silently-wrong results)

**File:** `src/query/engine.rs:379-392` (the `if last_merged == round_boundary` block; override at 384-390)
**Issue:**
When a single `created_at` second holds **more than `emit_limit`** events, the merge can only
ever return the top `emit_limit` dups of that timestamp. `merge_windowed` is re-invoked fresh on
every page from `round_until = stalled_ts` (timestamp-only start key, engine.rs:225-227), and a
reverse DUPSORT scan always positions at the **largest** dup and walks down
(`collect_bounded` / `reverse_upper_bound`, scan.rs:443-468). The within-round windowed refill
(`refill_stream`, merge.rs:118-192) walks deeper into the same dup group but is hard-capped at
`emit_limit` total (merge.rs:287-289). Therefore the lowest `F - emit_limit` lev_ids of a fat
group of size `F` are **never returned in any round of any page**.

Once the cursor descends to the merge floor `(stalled_ts, F - emit_limit + 1)`, every page
returns zero survivors and `last_merged == round_boundary` fires the no-progress break. The
override then sets `deepest_scanned = (stalled_ts - 1, u64::MAX)`, so the next page jumps
**past the entire fat timestamp** to `stalled_ts - 1` — abandoning lev_ids
`[1 .. F - emit_limit]` at `stalled_ts`. Pagination terminates and reports `cursor: None`
(false end-of-stream for those events), exactly the silently-wrong-data failure the project
CLAUDE.md names as "the crux."

**Proof (temporary probe, now reverted):** cloned the rigged regression test but set
`FAT_COUNT = 300` (> `emit_limit = 260` for `limit = 2`), `BELOW_COUNT = 5`, `TOTAL = 305`.
Full cursor pagination to `None` collected **265 unique** events, not 305 — exactly
`emit_limit (260) + below (5)`; **40 fat-group events stranded**. The existing test
`test_execute_query_fat_timestamp_pagination_no_stall` passes only because it pins
`FAT_COUNT = 260 = emit_limit` (engine.rs:2010), placing the floor at lev_id 1 so nothing sits
below it. The test comment admits the pin is deliberate.

The code comment at engine.rs:370-372 rationalizes this as "architecturally unreachable
... per-value positioning within a dup group is not possible via key-range-only scans." Two
problems: (1) strfry's own resume model carries the lev_id in the resume position, so a
lev_id-aware resume start key *can* descend within a dup group — the limitation is this
implementation's, not architectural; (2) even if one accepts a hard limit, returning a
silently-truncated result set with `cursor: None` (claiming completeness) violates the
fail-closed correctness requirement. A truncation must surface as continued pagination or an
explicit diagnostic — never a false EOF.

**Fix:**
Resume *within* the fat group instead of jumping past it. Keep `deepest_scanned` at the floor
`(stalled_ts, floor_lev)` and make `build_start_keys` encode the lev_id into the reverse resume
position so the next page's scan starts strictly below `floor_lev` within the dup group
(mirroring strfry's resume key layout; pair with an `Excluded` dup-resume in scan.rs). Advance
past `stalled_ts` only when the group is genuinely drained (`exhausted` already covers that):

```rust
if last_merged == round_boundary {
    // Do NOT skip the whole timestamp. Leave deepest_scanned at (stalled_ts, floor_lev)
    // so the next page resumes WITHIN the dup group below floor_lev. Requires a
    // lev_id-aware reverse start key in router::build_start_keys + Excluded dup-resume.
    break;
}
```

If a genuine cap is unavoidable for v1, the function MUST NOT return `cursor: None` after
truncating a fat group: return the floor cursor `Some((stalled_ts, floor_lev))` (documenting that
pages repeat until the caller observes a non-advancing cursor) OR propagate a `QueryError`
indicating an over-budget dup group. Silent truncation is unacceptable. Add a regression test
with `FAT_COUNT = emit_limit + 50` asserting all events are reachable — the current
`FAT_COUNT == emit_limit` test does not exercise this path.

---

### CR-02: Infinite (non-terminating) pagination when the stalled fat timestamp is `created_at == 0`

**File:** `src/query/engine.rs:384-390` (the `if stalled_ts > 0` guard and its no-op `else`)
**Issue:**
The underflow guard correctly prevents `stalled_ts - 1` from panicking when `stalled_ts == 0`.
But the `else` path (engine.rs:388-389) intentionally leaves `deepest_scanned` unchanged at
`(0, floor_lev)`. For a fat group at `created_at == 0` whose size exceeds `emit_limit`, the
no-progress break fires with `deepest_scanned = (0, floor_lev)`, so the page returns
`cursor: Some((0, floor_lev))`. The caller pages again: `round_boundary = (0, floor_lev)`, the
merge re-returns the same top-`emit_limit` dups, cursor-exclusion drops all of them
(`lev_id >= floor_lev`), `last_merged == round_boundary` fires the break again, the override is
again skipped (`stalled_ts == 0`), and the function returns the **identical**
`cursor: Some((0, floor_lev))`. The cursor never advances and never becomes `None` — a caller
paginating until `cursor == None` loops forever, each page returning an empty result set plus the
same cursor.

`created_at == 0` is not a realistic Nostr timestamp, but the function accepts arbitrary `u64`
timestamps from the on-disk index, and a corrupt/crafted index entry at ts=0 (or a crafted
cursor) turns this into an unbounded client-side pagination loop (availability concern). Within a
single `execute_query` call `MAX_ROUNDS` still holds, so this is not a single-call hang — it is a
cross-call divergence that the cursor-convergence invariant (D-11) is supposed to forbid.

**Fix:**
At `stalled_ts == 0` there is no lower timestamp; signal EOF deterministically rather than
re-emitting the same cursor:

```rust
if let Some((stalled_ts, _)) = last_merged {
    if stalled_ts > 0 {
        deepest_scanned = Some((stalled_ts - 1, u64::MAX));
    } else {
        // ts == 0 fat group: no lower key-range position exists. Force EOF instead of
        // re-emitting an identical, non-advancing cursor.
        deepest_scanned = None;
    }
}
```

(This still strands the sub-`emit_limit` tail per CR-01; the durable fix is intra-group resume,
after which this branch advances normally.) Add a regression test: a fat group at
`created_at = 0` larger than `emit_limit`, asserting pagination terminates within a bounded page
count.

## Warnings

### WR-01: Fat-timestamp regression test is constructed to pass rather than to expose the bug

**File:** `src/query/engine.rs:2003-2010` (`test_execute_query_fat_timestamp_pagination_no_stall`)
**Issue:**
`FAT_COUNT` is pinned to `260`, with the comment "= emit_limit; triggers the stall after all
fat-ts events emitted." Setting the fat group size *exactly equal* to `emit_limit` places the
merge floor at lev_id 1, so no events sit below the reachable window and the stranding in CR-01
cannot manifest. A regression test for a stall when "a fat timestamp holds **>=** emit_limit
events" must include the `>` case, which is precisely where the loss occurs. As written, the test
provides false assurance and is the reason this defect survived the gate.

**Fix:** Change `FAT_COUNT` to `emit_limit + N` (the test will then FAIL until CR-01 is fixed —
the correct behavior for a regression guard), or add a second case covering
`FAT_COUNT > emit_limit` with an explicit all-events-reachable assertion.

### WR-02: `exhausted` reflects only the final round, conflating budget caps with true EOF

**File:** `src/query/engine.rs:248`
**Issue:**
`exhausted = merge_batch.len() < emit_limit` is recomputed each round; only the last round's
value reaches the cursor builder. In the stall scenario the final round returns a *full*
`emit_limit` batch (all cursor-excluded), so `exhausted = false` — correct for entering the
CR-01/CR-02 branches today. But this couples two distinct conditions ("index region below the
boundary is empty" vs. "we hit the per-round cap") into one boolean read at one point in time.
A future change to round ordering or break placement could make `exhausted` stale (reflecting a
non-final round) and silently flip cursor emission. The invariant "exhausted means true EOF" is
load-bearing for D-11 and is currently protected only by statement ordering.

**Fix:** Rename to `last_round_under_filled` and derive the true-EOF decision explicitly (e.g.
`last_round_under_filled && deepest_scanned.is_none()`, or a dedicated `saw_floor` flag) so the
cursor builder's EOF condition does not depend on which round last wrote the variable. At
minimum, add an assertion/comment binding the variable's meaning to the final round.

## Info

### IN-01: Code comment asserts a false "architecturally unreachable" claim

**File:** `src/query/engine.rs:370-372`
**Issue:**
"Un-emitted events AT ts_L beyond the emit_limit budget are architecturally unreachable: lev_id
is a DUPSORT value, not part of the key, so per-value positioning within a dup group is not
possible via key-range-only scans." This is inaccurate: strfry's own resume mechanism encodes
the lev_id into the resume position and descends within dup groups; the limitation is a property
of this engine's timestamp-only `round_until` start key, not of LMDB DUPSORT or of strfry's
index format. The comment launders a fixable implementation limitation into an architectural
inevitability — which is how the stranding (CR-01) escaped review.

**Fix:** Replace with an accurate statement of the *current* limitation plus a TODO referencing
the intra-group resume work, so the constraint is not mistaken for a hard invariant.

---

_Reviewed: 2026-06-13T07:53:23Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: deep_
