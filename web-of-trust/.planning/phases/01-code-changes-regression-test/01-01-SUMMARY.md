---
phase: 01-code-changes-regression-test
plan: 01
subsystem: database
tags: [dgraph, dql, grpc, nostr, crawler, go]

requires: []
provides:
  - "last_attempt schema predicate (distinct from last_db_update) on the Profile type"
  - "frontier-first GetStalePubkeys (3-arg, batched) that selects never-attempted stubs"
  - "MarkAttempted to stamp last_attempt on every queried batch"
  - "crawler loop wired to the batched selection + attempt stamping"
  - "integration regression test asserting stubs are selected and fresh nodes are not"
affects: [phase-2-backfill, live-verification, migration]

tech-stack:
  added: []
  patterns:
    - "Two-phase Dgraph selection: explicit `NOT has(predicate)` frontier query with `first:` cap, then ordered top-up — never rely on orderasc to surface missing-value nodes or the default 1000-row cap"
    - "Raised gRPC MaxCallRecvMsgSize on the Dgraph client for large selection batches"

key-files:
  created:
    - pkg/dgraph/dgraph_stale_test.go
  modified:
    - pkg/dgraph/dgraph.go
    - cmd/crawler/main.go

key-decisions:
  - "Bumped gRPC MaxCallRecvMsgSize to 256MiB in NewClient — a large frontier `first:` over a 189k-stub graph exceeds the 4MB default (real production concern, not just the test)"
  - "Test sizes its frontier limit above the live never-attempted count (countFrontier helper) because Phase 1 selection is unordered, so a fixed 100k limit could non-deterministically drop the test stub against a populated DB"
  - "n-quads split one triple per line — Dgraph's parser rejects multiple triples on a single line"

patterns-established:
  - "Frontier-first stale selection: Phase 1 NOT has(last_attempt) + first:, Phase 2 orderasc: last_attempt + lt() top-up"
  - "Attempt tracking decoupled from success: MarkAttempted stamps last_attempt regardless of whether a kind-3 came back"

requirements-completed: [SCHEMA-01, SEL-01, SEL-02, SEL-03, ATTEMPT-01, ATTEMPT-02, TEST-01, TEST-02]

duration: ~20min
completed: 2026-06-09
---

# Phase 1: Code Changes + Regression Test Summary

**Frontier-first `GetStalePubkeys` plus a new `last_attempt` predicate and `MarkAttempted`, so the crawler can finally reach the ~92% stub frontier instead of re-crawling the same 15k accounts — verified green against a live Dgraph.**

## Performance

- **Duration:** ~20 min
- **Tasks:** 3
- **Files modified:** 3 (2 modified, 1 created)

## Accomplishments
- Added `last_attempt: int @index(int)` predicate + Profile field (Fix A) so "attempted" is distinct from "successfully ingested" (`last_db_update`).
- Rewrote `GetStalePubkeys` into a batched, two-phase frontier-first query (Fix B): Phase 1 selects never-attempted stubs via `NOT has(last_attempt)` + explicit `first:` (no `orderasc`, no 1000-cap reliance); Phase 2 tops up with aged-out attempted nodes. Added `collectStale` helper.
- Added `MarkAttempted` (Fix C) reusing the existing `ResolvePubkeysToUIDs`, stamping `last_attempt` on every queried batch so un-fetchable pubkeys age out of the frontier.
- Wired the crawler loop (Fix D): passes `const batchSize = 500`, deleted the manual 500-cap (`limitedPubkeys`) block, calls `MarkAttempted` after `FetchAndUpdateFollows` (non-fatal on error).
- Added `//go:build integration`-gated regression test asserting a pure stub IS selected and a freshly-attempted node is NOT — the exact property the old `orderasc`/1000-cap query violated.

## Task Commits

1. **Task 1: Dgraph schema + frontier-first selection + attempt tracking (Fixes A/B/C)** - `25217ac` (feat)
2. **Task 2: Crawler loop wiring (Fix D)** - `93c1436` (feat)
3. **Task 3: Integration regression test (TEST-01/02)** - `180645c` (test)

## Files Created/Modified
- `pkg/dgraph/dgraph.go` - `last_attempt` schema, frontier-first `GetStalePubkeys` + `collectStale`, `MarkAttempted`, raised gRPC recv-size in `NewClient`.
- `cmd/crawler/main.go` - batched selection, removed manual cap, `MarkAttempted` wiring.
- `pkg/dgraph/dgraph_stale_test.go` - integration regression test + `mustMutate`/`countFrontier` helpers.

## Decisions Made
- Followed `8pc_crawled.md` §4 Fixes A–D verbatim for the production code.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] gRPC max receive message size too small for large frontier batches**
- **Found during:** Task 3 (running the integration test against live Dgraph)
- **Issue:** A frontier query with a large `first:` over the live ~189k-stub graph returned a response > 4MB, failing with gRPC `ResourceExhausted`. This is a real production failure mode for `GetStalePubkeys`, not just the test.
- **Fix:** Added `grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(256<<20))` to `NewClient`.
- **Files modified:** pkg/dgraph/dgraph.go
- **Verification:** `make test-integration` green.
- **Committed in:** `180645c`

**2. [Rule 1 - Incorrect spec snippet] Test n-quads rejected by Dgraph parser**
- **Found during:** Task 3
- **Issue:** The §5 spec's RDF placed multiple triples on one line; Dgraph requires a newline after each `.` and rejected the mutation.
- **Fix:** Split the n-quads one triple per line in the test's `mustMutate` input.
- **Files modified:** pkg/dgraph/dgraph_stale_test.go
- **Verification:** mutation commits cleanly; test runs.
- **Committed in:** `180645c`

**3. [Rule 1 - Non-deterministic against populated DB] Fixed test limit could drop the stub**
- **Found during:** Task 3
- **Issue:** The spec's `GetStalePubkeys(ctx, now-3600, 100000)` assumed a small DB. Against the live graph (189,252 never-attempted nodes), Phase 1's unordered `first: 100000` need not include the freshly-inserted test stub, so the assertion flaked.
- **Fix:** Added a `countFrontier` test helper; the test sizes its limit to `frontier_count + 1000` so the stub is guaranteed selected regardless of graph state.
- **Files modified:** pkg/dgraph/dgraph_stale_test.go
- **Verification:** `make test-integration` green; stub selected, fresh node excluded.
- **Committed in:** `180645c`

---

**Total deviations:** 3 auto-fixed (1 blocking infra limit, 2 spec-snippet corrections).
**Impact on plan:** All necessary to make the regression test actually execute against the live Dgraph. The gRPC fix also hardens production `GetStalePubkeys`. No scope creep beyond Fixes A–D + §5 test.

## Issues Encountered
- The integration suite runs against a populated live Dgraph (localhost:9080, ~189k nodes), which surfaced the three deviations above (all resolved). Test now passes in ~3.2s.

## User Setup Required
None - no external service configuration required. (Phase 2 will run the Fix E backfill + live verification on the strfry host.)

## Next Phase Readiness
- Code, build, and automated regression test all green. `EnsureSchema` will add `last_attempt` to the live Dgraph on first run after deploy.
- Phase 2 (MIG-01) can proceed: run the Fix E DQL backfill (after `EnsureSchema` adds the predicate) and verify live frontier progress per spec §6.

---
*Phase: 01-code-changes-regression-test*
*Completed: 2026-06-09*
