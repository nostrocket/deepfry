---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
last_updated: "2026-06-09T05:08:31.923Z"
progress:
  total_phases: 2
  completed_phases: 1
  total_plans: 1
  completed_plans: 1
  percent: 50
---

# Project State: Web-of-Trust Crawler — Crawl Coverage Fix

**Last updated:** 2026-06-09

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 01 — code-changes-regression-test

## Current Position

Phase: 01 (code-changes-regression-test) — COMPLETE
Plan: 1 of 1 (complete)
**Phase:** 1 — Code Changes + Regression Test
**Plan:** 01-01 complete
**Status:** Phase 1 complete — ready for Phase 2 (backfill + live verification)
**Progress:** 1/2 phases complete

```
[=====     ] 50%
```

## Performance Metrics

- Phases complete: 1 / 2
- Requirements delivered: 8 / 9
- Plans complete: 1 / 1 (Phase 1)

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| 2-phase coarse structure | Code changes + test ship together (interdependent); backfill + live verification are operational steps that depend on the binary being deployed |
| SCHEMA-01 through ATTEMPT-02 + TEST-01/02 all in Phase 1 | The schema predicate, GetStalePubkeys rewrite, MarkAttempted, and crawler-loop wiring are tightly coupled; splitting them would leave the binary in a broken intermediate state |
| MIG-01 isolated in Phase 2 | The DQL backfill requires the `last_attempt` predicate to already exist in Dgraph (applied by EnsureSchema on first run after Phase 1 deploy) |

### Important Facts

- The only caller of `GetStalePubkeys` is `cmd/crawler/main.go:109` (verified in spec §7)
- `ResolvePubkeysToUIDs` already exists at `pkg/dgraph/clusterscan.go:45` — `MarkAttempted` reuses it directly
- Live config at `~/deepfry/web-of-trust.yaml` must not be edited; use `HOME=/tmp/fakehome` for test runs per spec §6
- Fix E backfill must run AFTER `EnsureSchema` adds `last_attempt`; do not run it against a schema that lacks the predicate
- Integration tests gate on `//go:build integration` and require a live Dgraph at `localhost:9080`

### Todos

- [x] Apply Fix A (schema): add `last_attempt: int @index(int)` to `EnsureSchema` in `pkg/dgraph/dgraph.go` (`25217ac`)
- [x] Apply Fix B (selection): replace `GetStalePubkeys` with frontier-first version; add `collectStale` helper (`25217ac`)
- [x] Apply Fix C (attempt tracking): add `MarkAttempted` to `pkg/dgraph/dgraph.go` (`25217ac`)
- [x] Apply Fix D (crawler loop): update `cmd/crawler/main.go` — pass `batchSize`, delete manual 500-cap block, call `MarkAttempted` (`93c1436`)
- [x] Add regression test to `pkg/dgraph/dgraph_stale_test.go` (`180645c`)
- [x] Run `make build-crawler` to confirm build (clean)
- [x] Run `make test-integration` to confirm regression test passes (green vs live Dgraph)
- [ ] Run Fix E backfill on strfry host after deploy (Phase 2)
- [ ] Verify live crawler per spec §6 (Phase 2)

### Blockers

None at start.

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md` for full context. The fix specification with exact code is in `8pc_crawled.md` at the module root.
