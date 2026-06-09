---
gsd_state_version: 1.0
milestone: v1.1
milestone_name: Write Integrity & Hardening
status: planning
last_updated: "2026-06-09T08:30:10.736Z"
last_activity: 2026-06-09
progress:
  total_phases: 2
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State: Web-of-Trust Crawler — v1.1 Write Integrity & Hardening

**Last updated:** 2026-06-09

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** v1.1 roadmap created (Phases 3–4). Ready to plan Phase 3 (write-path correctness + regression coverage).

## Current Position

Phase: 3 — Write-Path Correctness + Regression Coverage (not started)
Plan: —
Status: Roadmap complete; awaiting `/gsd-plan-phase 3`
Last activity: 2026-06-09 — v1.1 roadmap created, 7/7 requirements mapped

## Performance Metrics

- Phases complete (v1.1): 0 / 2
- Requirements delivered (v1.1): 0 / 7
- Plans complete (v1.1): 0 / 0

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| Continue numbering from Phase 3 | DEFAULT numbering mode; v1.0 ended at Phase 2 |
| 2-phase coarse structure | Tightly-coupled, localized fixes in two files (`pkg/crawler/chunks.go`, `pkg/dgraph/dgraph.go`) plus tests; favour few cohesive phases per YOLO + COARSE config |
| Phase 3 = write-path correctness + all regression coverage | CHUNK-01/02 + LEAK-01 are one interlocking change in the write path; TEST-03 (integration) and TEST-04 (unit) prove it and ship together |
| Phase 4 = remove-path hardening | SEC-01/02 touch `RemoveFollower` only (dead code, no callers); isolated defense-in-depth, lowest risk, sequenced last |

### Important Facts

- CHUNK-01 root cause: `processFollowsInChunks` reuses one `kind3CreatedAt` across chunks, tripping the guard at `pkg/dgraph/dgraph.go:165` (`kind3createdAt <= existingKind3CreatedAt -> return nil`); chunks 2…N silently dropped for pubkeys with >500 follows.
- CHUNK-02: the fix must still short-circuit genuine duplicates (same/older event, already complete) — distinguish "subsequent chunk of same event" from "already fully ingested."
- LEAK-01: `defer cancel()` sits inside the chunk `for` loop at `chunks.go:39-40`, accumulating until function return.
- SEC-01/02: `RemoveFollower` at `dgraph.go:344-355` builds DQL via raw string concatenation; `RemovePubKeyIfNoFollowers` (same file, ~line 379) is the `$`-Vars reference pattern. No callers today.
- TEST-03 needs a live Dgraph (`//go:build integration`, `make test-integration` already exists). TEST-04 is unit-only, no live Dgraph (`make test` / `-short`).
- Live config at `~/deepfry/web-of-trust.yaml` must not be edited for testing; use a temp `HOME` per spec §6.

### Todos

- [ ] Plan Phase 3 (`/gsd-plan-phase 3`)
- [ ] Plan Phase 4 (`/gsd-plan-phase 4`)

### Blockers

None.

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md` for full context. v1.1 covers the write-path integrity + hardening fixes; the v1.0 8% crawl fix (Phases 1–2) is shipped and verified live.
