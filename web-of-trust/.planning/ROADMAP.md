# Roadmap: Web-of-Trust Crawler — Crawl Coverage Fix

**Milestone:** Implement the 8% crawl fix specified in `8pc_crawled.md`
**Created:** 2026-06-09
**Granularity:** Coarse
**Coverage:** 9/9 v1 requirements mapped

## Phases

- [x] **Phase 1: Code Changes + Regression Test** - Add `last_attempt` schema predicate, rewrite `GetStalePubkeys` with frontier-first selection, add `MarkAttempted`, wire the crawler loop, confirm build, and add the integration regression test
- [x] **Phase 2: Backfill + Live Verification** - Run the one-time DQL backfill to seed `last_attempt` from `last_db_update` on existing crawled nodes, then verify the crawler reaches the uncrawled frontier in a live run on the strfry host

## Phase Details

### Phase 1: Code Changes + Regression Test
**Goal**: The crawler codebase correctly selects stub pubkeys, tracks attempt state, and the binary builds cleanly with a passing regression test
**Depends on**: Nothing (first phase)
**Requirements**: SCHEMA-01, SEL-01, SEL-02, SEL-03, ATTEMPT-01, ATTEMPT-02, TEST-01, TEST-02
**Success Criteria** (what must be TRUE):
  1. `make build-crawler` completes with no errors and the updated `GetStalePubkeys` signature is the only definition (single caller at `cmd/crawler/main.go:109` updated)
  2. A pure stub node (no `last_attempt`) IS returned by `GetStalePubkeys`, and a freshly-attempted node is NOT returned as stale, as asserted by the integration regression test (`make test-integration`)
  3. The Dgraph schema exposed by `EnsureSchema` includes `last_attempt: int @index(int)` listed on the `Profile` type
  4. Every pubkey batch queried by the crawler loop is immediately stamped with `last_attempt` via `MarkAttempted`, whether or not a kind-3 event came back
**Plans**: 1 plan
- [x] 01-01-PLAN.md — Fixes A-D + regression test: last_attempt schema, frontier-first GetStalePubkeys, MarkAttempted, crawler wiring, build + integration test

### Phase 2: Backfill + Live Verification
**Goal**: The running crawler on the strfry host begins converting stub nodes to crawled nodes, demonstrating that the fix unblocks graph growth
**Depends on**: Phase 1
**Requirements**: MIG-01
**Success Criteria** (what must be TRUE):
  1. The Fix E DQL upsert backfill completes without error: all nodes with `last_db_update` now also have `last_attempt` set, so already-crawled accounts are not re-prioritised as frontier
  2. `grep -c 'New pubkey added to graph (signer)' /tmp/crawler.log` is greater than 0 in a 5-minute live run (was 0 before the fix per spec §3.2)
  3. The stub count (nodes with no `kind3CreatedAt`) decreases materially compared to the baseline snapshot taken before the crawl run
**Plans**: 1 plan
- [x] 02-01-PLAN.md — Fix E backfill (seed `last_attempt` from `last_db_update`) + live verification runbook on the strfry host (baseline snapshot, 5-min crawl, §6 progress asserts)
**Outcome**: SC1 PASS (backfill committed, 0 nodes missing `last_attempt`). SC2/SC3 PASS in substance (operator-approved re-adjudication): graph grew +16,813 pubkeys and crawled +2,992 in 5 min (both frozen before the fix); the literal `(signer)` grep is 0 by design and stubs rose because discovery outpaces conversion in a short window. See `02-01-SUMMARY.md`.

## Progress Table

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Code Changes + Regression Test | 1/1 | Complete | 2026-06-09 |
| 2. Backfill + Live Verification | 1/1 | Complete | 2026-06-09 |
