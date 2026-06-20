---
phase: 14-frontier-read-path-throughput-follower-count
plan: 01
subsystem: database
tags: [dgraph, dql, follower_count, read-path, crawler, backfill, go]

# Dependency graph
requires:
  - phase: 08-*
    provides: "GetStalePubkeys frontier/aged val(count(~follows)) ordering, next_attempt/miss_count predicates, BackfillNextAttempt paginated backfill template"
  - phase: 12-*
    provides: "withWindowTimeout bounded per-window mutation pattern in AddFollowers"
provides:
  - "follower_count: int @index(int) stored predicate on Profile"
  - "GetStalePubkeys ordered by stored follower_count (no per-call count(~follows) aggregate)"
  - "AddFollowers delta maintenance (+1/-1, stubs init to 1) inside the existing all-or-nothing txn"
  - "BackfillFollowerCount paginated idempotent recompute method"
  - "cmd/backfill-follower-count operator CLI + Makefile target"
  - "followerCountDelta pure helper (unit-test seam)"
affects: [crawler-throughput, frontier-ordering, future-trust-scoring]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Stored derived predicate read on the hot path instead of recomputed aggregate"
    - "Read-current-then-write-clamped-absolute for counter maintenance (no native Dgraph increment)"
    - "uid-cursor pagination for idempotent overwrite backfill (vs offset:0 self-emptying filter)"

key-files:
  created:
    - cmd/backfill-follower-count/main.go
    - pkg/dgraph/dgraph_follower_count_test.go
    - pkg/dgraph/dgraph_follower_count_integration_test.go
  modified:
    - pkg/dgraph/dgraph.go
    - Makefile

key-decisions:
  - "follower_count is an ordering hint, not authoritative — cheap delta maintenance, no reconciliation machinery (D-context Area 1)"
  - "Decrement correctness: read current follower_count within the txn, write clamped max(0, current-1) absolute value — no blind decrement nquad (Dgraph SetNquads has no native decrement)"
  - "New followee stubs init follower_count=1 and are excluded from the +1 adjustment to avoid double-counting"
  - "BackfillFollowerCount pages by uid cursor (gt(uid, lastUID)) since unconditional overwrite never self-empties an offset:0 filter"
  - "Integration tests split into a separate //go:build integration file because a single Go file cannot mix tagged and untagged tests"

patterns-established:
  - "followerCountDelta pure helper as the unit-test seam (mirrors chunkSlice discipline)"
  - "Count-adjustment nquads chunked via chunkSlice(batchSize)+withWindowTimeout inside the single AddFollowers txn"

requirements-completed: [DSCALE-01, DSCALE-03, TEST-03]

# Metrics
duration: ~25min
completed: 2026-06-20
status: awaiting-checkpoint
---

# Phase 14 Plan 01: Frontier Read-Path Throughput (follower_count) Summary

**Replaced the per-call count(~follows) frontier aggregate in GetStalePubkeys with a stored, int-indexed follower_count predicate — maintained cheaply (+1/-1) in AddFollowers' existing transaction and backfilled by a new idempotent operator CLI.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 5 of 6 complete (Task 6 is an operator-run live-verification checkpoint — PENDING)
- **Files modified:** 2 modified, 3 created

## Accomplishments

- **DSCALE-01 (read path):** `GetStalePubkeys` now orders both the frontier (`NOT has(last_attempt)`) and aged (`lt(next_attempt, now)`) phases by the stored `orderdesc: follower_count`, with the `var{fc as count(~follows)}` / `var{ac as count(~follows)}` aggregate blocks removed entirely. `collectStale` and the caller signature are unchanged. The superseded D-09/D-10 doc rationale was rewritten to the perf rationale.
- **DSCALE-03 (write maintenance + backfill):** `AddFollowers` adjusts `follower_count` (+1 for newly-added followees, -1 for removed) for valid followees inside its existing all-or-nothing transaction. Decrements read the current value within the txn and write `max(0, current-1)` — never a blind decrement. New followee stubs initialize to 1. `BackfillFollowerCount` provides a paginated, uid-cursor, idempotent recompute (`follower_count = count(~follows)`).
- **Operator tooling:** `cmd/backfill-follower-count` CLI (`--dgraph-addr`, `--dry-run`) calls `EnsureSchema` first (triggering the int-index build over the live graph) then runs the backfill. Wired into the Makefile (`build`, `.PHONY`, `help`, `run-` target).
- **TEST-03 (automated coverage):** `TestFollowerCountDelta` (5 table cases) passes under `make test`; integration tests for predicate ordering and backfill idempotency compile behind `//go:build integration`.

## Task Commits

1. **Task 1: follower_count predicate + followerCountDelta helper** — `2bab80d` (feat)
2. **Task 2: GetStalePubkeys orders by stored follower_count** — `7aea2fc` (feat)
3. **Task 3: AddFollowers delta maintenance + BackfillFollowerCount** — `95f4ded` (feat)
4. **Task 4: backfill-follower-count CLI + Makefile** — `688eacd` (feat)
5. **Task 5: unit + integration tests** — `0172305` (test)

_Task 3 was a TDD task; the pure delta seam (`followerCountDelta`) was added in Task 1 and its test in Task 5. The remaining behaviors (in-txn delta, stub init, backfill idempotency) require a live Dgraph and are covered by the integration tests + the Task 6 live checkpoint._

## Files Created/Modified

- `pkg/dgraph/dgraph.go` — Added `follower_count: int @index(int)` to schema + Profile type; rewrote both `GetStalePubkeys` query blocks; added Step 4 follower_count delta maintenance in `AddFollowers`; added `followerCountDelta` pure helper and `BackfillFollowerCount` method.
- `cmd/backfill-follower-count/main.go` — New operator CLI: EnsureSchema then BackfillFollowerCount (or dry-run count).
- `Makefile` — `APP_BACKFILL_FC` var, `build-backfill-follower-count` + `run-backfill-follower-count` targets, registered in `build`/`.PHONY`/`help`.
- `pkg/dgraph/dgraph_follower_count_test.go` — `TestFollowerCountDelta` (untagged unit test).
- `pkg/dgraph/dgraph_follower_count_integration_test.go` — `//go:build integration` predicate-ordering + backfill-idempotency tests + `queryFollowerCount` helper.

## Decisions Made

- **Counter maintenance via read-then-write-absolute.** Dgraph `SetNquads` has no native increment/decrement, so a blind `±1` nquad is incorrect. The implementation reads each affected followee's current `follower_count` in one batched query within the same txn, then writes the adjusted absolute value (`current+1` / `max(0, current-1)`). Documented in-code as the chosen approach.
- **Stub init to 1, excluded from +1.** New followee stubs created during the write carry `follower_count "1"` (signer is the first observed follower) and are excluded from the `added` +1 set to avoid double-counting.
- **uid-cursor backfill pagination.** Because the backfill overwrites unconditionally (idempotent re-run), an `offset:0 @filter(NOT has(follower_count))` page would never self-empty; it pages by `gt(uid, lastUID)` ordered ascending instead.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Split integration tests into a separate build-tagged file**
- **Found during:** Task 5 (test authoring)
- **Issue:** The plan names a single test file `pkg/dgraph/dgraph_follower_count_test.go` containing both untagged unit tests and `//go:build integration` live tests. A Go source file carries one file-level build constraint; mixing tagged and untagged tests in one file is impossible.
- **Fix:** Kept the unit test (`TestFollowerCountDelta`) in `dgraph_follower_count_test.go` (untagged, runs under `make test`) and placed the live tests in a new `dgraph_follower_count_integration_test.go` carrying `//go:build integration`.
- **Files modified:** pkg/dgraph/dgraph_follower_count_test.go, pkg/dgraph/dgraph_follower_count_integration_test.go
- **Verification:** `go test ./pkg/dgraph/ -run TestFollowerCountDelta` passes; `go vet -tags=integration ./pkg/dgraph/` compiles the integration tests.
- **Committed in:** `0172305` (Task 5 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking — Go build-tag file constraint).
**Impact on plan:** No functional change; the test split is required by the Go toolchain. All planned test surfaces are present. No scope creep.

## Issues Encountered

- `grep -c 'orderdesc: follower_count'` initially returned 3 because the rewritten doc comment contained the literal phrase. Reworded the comment to "orderdesc on follower_count" / "the stored follower_count predicate" so the exact-count gate (exactly 2 query occurrences) holds without weakening the documentation.

## Build / Test Gate Results

- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go vet -tags=integration ./pkg/dgraph/` — PASS (integration tests compile)
- `make test` (`-short`) — PASS (all packages ok)
- `go test ./pkg/dgraph/ -run TestFollowerCountDelta` — PASS (5/5 table cases)
- `make build-backfill-follower-count` — PASS (`bin/backfill-follower-count` produced)
- `grep -c 'orderdesc: follower_count'` → 2 (frontier + aged); no `count(~follows)` in GetStalePubkeys.

## User Setup Required

Task 6 is an operator-run live verification on the **strfry host** (TEST-03) — it CANNOT run in this environment (requires the live ~1.38M-node production Dgraph). It is **PENDING operator action**.

### Task 6 — operator procedure (run on the strfry host)

1. **Build:** `cd web-of-trust && make build-backfill-follower-count` (and `make build` to confirm the new target is in the aggregate).
2. **Schema + index build:** `./bin/backfill-follower-count --dry-run --dgraph-addr localhost:9080`. This calls `EnsureSchema` first — confirm the `follower_count` int index builds over the live graph (Alter returns cleanly; Ratel at http://localhost:8000 shows `follower_count: int @index(int)`). The dry run prints the node count that would be backfilled.
3. **BEFORE evidence:** time a `GetStalePubkeys` batch (or capture `batch_ms`/`overhead_ms` from `~/deepfry/crawler-metrics.jsonl`) on the current frontier — record the baseline read-path latency (~39s/batch expected).
4. **Run the backfill:** `./bin/backfill-follower-count`. Confirm it prints `Backfilled follower_count on N nodes.` with N ≈ total node count, and that a SECOND run is safe (idempotent — completes without error, values unchanged on a spot-check).
5. **AFTER evidence:** time a `GetStalePubkeys` batch again — confirm read-path latency dropped substantially from the ≈39s baseline (DSCALE-01).
6. **Accuracy spot-check (DSCALE-03):** on a sample of nodes, compare stored `follower_count` against a freshly recomputed `count(~follows)`; confirm they match within ordering-hint tolerance (exact for static nodes; small drift acceptable for actively-changing ones).
7. **Top-N guarantee (WR-05 re-verify):** confirm `orderdesc: follower_count` + explicit `first: N` returns the true top-N-by-count frontier on the >1000-row live frontier, not a pre-truncated set.
8. **Record before/after latency numbers** in the phase verification notes / this SUMMARY (TEST-03 evidence).

**Resume signal:** Operator types "approved" with before/after latency numbers, or describes issues.

## Next Phase Readiness

- Code is complete, builds clean, and passes all automatable gates. The phase cannot be marked fully complete until the operator runs the Task 6 live verification on the strfry host and records the before/after read-path latency evidence (TEST-03).
- No blockers for the operator step beyond access to the live Dgraph + crawler on the strfry host.

## Self-Check: PASSED

- All created files present on disk (cmd/backfill-follower-count/main.go, both test files, this SUMMARY).
- All 5 task commits present in git history (2bab80d, 7aea2fc, 95f4ded, 688eacd, 0172305).

---
*Phase: 14-frontier-read-path-throughput-follower-count*
*Completed (code, tasks 1–5): 2026-06-20*
