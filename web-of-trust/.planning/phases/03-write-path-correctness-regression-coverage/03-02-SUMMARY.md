---
phase: 03-write-path-correctness-regression-coverage
plan: 02
subsystem: testing
tags: [go-test, integration-test, dgraph, regression, chunkslice, nostr, kind3]

# Dependency graph
requires:
  - phase: 03-write-path-correctness-regression-coverage
    provides: "chunkSlice helper and unified full-set AddFollowers from Plan 03-01"
provides:
  - "TestChunkSlice: no-infrastructure unit test pinning chunk count + membership at boundary cases (TEST-04)"
  - "TestAddFollowersLargeKind3: //go:build integration test asserting full follow-set persistence, skips cleanly without a fixture (TEST-03)"
affects: [phase-04-security, web-of-trust-crawler, ci]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "White-box package dgraph tests, plain if/t.Fatalf, no testify (mirrors dgraph_stale_test.go)"
    - "Fixture-glob + t.Skip-when-absent pattern for integration tests that need harvested live data"

key-files:
  created:
    - pkg/dgraph/dgraph_chunks_test.go
    - pkg/dgraph/dgraph_writepath_test.go
  modified: []

key-decisions:
  - "Unit test asserts BOTH chunk count and membership union (no dropped/duplicated items), plus no empty/oversized chunk"
  - "Integration test deletes the time-seeded unique signer in t.Cleanup; followee stubs are left untouched since they may overlap real graph nodes (deleting the signer removes all edges this test added)"
  - "Fixture absence is a clean t.Skip, not a failure — harvesting a largest-kind3 fixture needs a manual live crawl (D-11)"

patterns-established:
  - "makeStrings(n) test helper for generating unique input slices"
  - "selectLargestFixture/fixtureCount: pick the highest-count testdata/largest-kind3-*.json"

requirements-completed: [TEST-03, TEST-04]

# Metrics
duration: 2 min
completed: 2026-06-09
---

# Phase 3 Plan 02: Regression Coverage Summary

**Added a no-infrastructure unit test pinning chunkSlice's count + membership at the 0/200/201/500/501/10000 boundaries, and a //go:build integration test that asserts a >chunk-size follow-list persists its full follow set (skipping cleanly until a fixture is harvested).**

## Performance

- **Duration:** 2 min
- **Started:** 2026-06-09T09:26:05Z
- **Completed:** 2026-06-09T09:27:38Z
- **Tasks:** 2
- **Files modified:** 2 (both created)

## Accomplishments
- `TestChunkSlice` (TEST-04, D-10): table-driven, no build tag, runs under `make test` / `-short`, no Dgraph. Asserts chunk count and membership union at every boundary, plus no empty or oversized chunk. All 6 subtests pass.
- `TestAddFollowersLargeKind3` (TEST-03, D-11): `//go:build integration`, selects the highest-count `testdata/largest-kind3-*.json` fixture, writes its followees via one `AddFollowers` call, and asserts the persisted edge count equals the full follow set — the exact regression the data-drop bug caused. Skips cleanly (before any Dgraph connect) when no fixture is present, and tears down via `t.Cleanup` + `DeleteNodes`.
- Both files mirror `dgraph_stale_test.go` conventions (white-box `package dgraph`, plain `if`/`t.Fatalf`, no testify) and reuse its `mustMutate` helper without redeclaring it.

## Task Commits

Each task was committed atomically:

1. **Task 1: Unit test for chunkSlice boundary logic** - `17c74a2` (test)
2. **Task 2: Integration test for full follow-set persistence** - `1d444e7` (test)

## Files Created/Modified
- `pkg/dgraph/dgraph_chunks_test.go` (created) - TestChunkSlice + makeStrings helper, no build tag
- `pkg/dgraph/dgraph_writepath_test.go` (created) - TestAddFollowersLargeKind3 + selectLargestFixture/fixtureCount/resolveUID/countFollows helpers, //go:build integration

## Decisions Made
- Integration test guards with `len(follows) <= batchSize` so a too-small fixture fails loudly rather than silently passing without exercising batching.
- Cleanup deletes only the unique signer node (removes all test-added edges); followee stubs are deliberately not deleted because they can overlap legitimate live nodes.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None. The integration test skips before connecting to Dgraph when no fixture exists, so `go test -tags=integration` passes even without a live Dgraph (the compile + skip-on-missing-fixture path is the gate for this phase, per D-11).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Regression coverage is in place: TEST-04 runs in CI under `-short`; TEST-03 is ready to run against a harvested fixture + live Dgraph.
- To activate TEST-03 fully, harvest a `testdata/largest-kind3-<count>.json` fixture via a manual live crawl, then run `make test-integration`.

## Self-Check: PASSED

- `pkg/dgraph/dgraph_chunks_test.go` exists, `package dgraph`, no `//go:build` tag; `go test -short -run TestChunkSlice` passes all 6 subtests. PASS
- `pkg/dgraph/dgraph_writepath_test.go` line 1 is `//go:build integration`; `go vet -tags=integration ./pkg/dgraph/` compiles (no mustMutate redeclaration); the test skips cleanly with the harvest message. PASS
- Assertion compares persisted edge count to `len(follows)`; `t.Cleanup` + `DeleteNodes` teardown present. PASS
- No testify / external test deps. PASS
- 2 task commits present (`17c74a2`, `1d444e7`). PASS

---
*Phase: 03-write-path-correctness-regression-coverage*
*Completed: 2026-06-09*
