---
gsd_state_version: 1.0
milestone: v1.2
milestone_name: milestone
status: verifying
last_updated: "2026-06-11T08:29:26.520Z"
last_activity: 2026-06-11 -- Phase 06 Plan 01 complete
progress:
  total_phases: 4
  completed_phases: 2
  total_plans: 4
  completed_plans: 4
  percent: 50
---

# Project State: Web-of-Trust Crawler — v1.2 Crawler Reliability & Efficiency

**Last updated:** 2026-06-10

## Project Reference

**Core value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

**Current focus:** Phase 06 — filter-size-per-relay-cap-detection

## Current Position

Phase: 06 (filter-size-per-relay-cap-detection) — EXECUTING
Plan: 2 of 2
Status: Phase complete — ready for verification
Last activity: 2026-06-11 -- Phase 06 Plan 01 complete

## Performance Metrics

- Phases complete (v1.2): 0 / 4
- Requirements delivered (v1.2): 0 / 12 (FILTER-01 and FILTER-02 foundation in P01; full delivery on P02)
- Plans complete (v1.2): 1 / 0 (06-01 complete)

## Accumulated Context

### Key Decisions

| Decision | Rationale |
|----------|-----------|
| Continue numbering from Phase 5 | v1.1 ended at Phase 4; sequential numbering per config |
| 4-phase coarse structure | Natural coupling clusters: VALID (3), FILTER (2), RELAY (2), PERF+TIMEOUT+METRIC (5); coarse granularity compresses into minimum viable boundaries |
| Phase 5 = pubkey validation hardening | VALID-01/02/03 are tightly coupled — fixing the validator (01), purging existing garbage (02), and stamping invalid nodes via UID (03) must ship together or the bug re-enters on every batch |
| Phase 6 = filter size + per-relay cap | FILTER-01 reduces the default; FILTER-02 adds intelligence to detect per-relay limits; must ship together since FILTER-02's filter-rejection class informs Phase 7 |
| Phase 7 = relay health management | Depends on Phase 6 because FILTER-02 introduces the filter-rejection failure class that RELAY-02 needs to classify; RELAY-01/02 are coupled (persistence + classification) |
| Phase 8 = frontier + timeout + observability | PERF-01/02 touch GetStalePubkeys/MarkAttempted; TIMEOUT-01/02 and METRIC-01 all touch cmd/crawler or pkg/crawler event loop; all independent of relay health, can run in parallel with Phase 7 but grouped for coarse granularity; depends on Phase 5 for valid MarkAttempted UID stamps |
| Phase 05-pubkey-validation-hardening P01 | 1200 | 3 tasks | 3 files |
| Phase 06-filter-size-per-relay-cap-detection P01 | 117 | 2 tasks | 3 files |
| Phase 06 P02 | 89 | 2 tasks | 2 files |

### Important Facts

- v1.2 motivated by 40-batch production run: 172 relays, 38s avg batch time, 789 pubkeys/min, 0.76% event hit rate, 43 new nodes added.
- VALID root cause: `nostr.GetPublicKey` is a private-key→public-key derivation function, not a validator; used as a validator it silently accepts garbage pubkeys (uppercase hex, relay URL blobs, truncated values). 19 garbage nodes already in DB re-enter every batch.
- FILTER root cause: batchSize 500 exceeds relay limits; 40% of relays reject or crash on every batch REQ.
- PERF root cause: stale frontier ordered by age only, yielding 99.24% wasted cycles (stubs with 0 followers queried as eagerly as high-follower accounts).
- RELAY root cause: failure counter reset to 0 on reconnect; flapping relays never reach ejection threshold.
- TIMEOUT: 44% of relays exceed 30s EOSE timeout; EOSE-quorum early exit can reclaim that time.
- METRIC: staleRemaining always 0 due to off-by-one in cmd/crawler/main.go metric formula.
- Live config at `~/deepfry/web-of-trust.yaml` must not be edited for testing; use a temp `HOME`.
- Integration tests gate on live Dgraph via `//go:build integration` / `make test-integration`.

### Todos

- [ ] Plan Phase 5 (`/gsd-plan-phase 5`)
- [ ] Plan Phase 6 (`/gsd-plan-phase 6`)
- [ ] Plan Phase 7 (`/gsd-plan-phase 7`)
- [ ] Plan Phase 8 (`/gsd-plan-phase 8`)

### Blockers

None.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260610-fft | commit current uncommitted web-of-trust changes | 2026-06-10 | c62c2c5 | [260610-fft-commit-current-uncommitted-web-of-trust-](./quick/260610-fft-commit-current-uncommitted-web-of-trust-/) |

## Session Continuity

**To resume:** Load `ROADMAP.md` and `REQUIREMENTS.md` for full context. v1.2 roadmap defines Phases 5–8 covering 12 requirements. Phase 5 (pubkey validation) is the natural first target — it unblocks Phase 8's MarkAttempted UID stamping and cleans the DB before filter and relay health work begins.

## Decisions

- [Phase ?]: Replace nostr.GetPublicKey (key-derivation) with dgraph.ValidatePubkey (hex-regex) at both crawler call sites
- [Phase ?]: Inline recover-or-purge in MarkAttempted subsumes VALID-02 — no separate startup purge step needed; garbage nodes self-clean on first encounter
- [Phase 06-01]: Floor for filterCap halving fixed at 10 (D-05); minCap=10 hardcoded in WithNoticeHandler closures; not config-driven
- [Phase 06-01]: rs created before nostr.RelayConnect in New() to allow safe closure capture over pointer (avoids loop-variable bug)
- [Phase ?]: rs.conn and rs.url used inside function; single call site updated
- [Phase ?]: Caller manages sub.Unsub() per chunk — drainSubscription does not defer it, keeping per-chunk lifecycle explicit
- [Phase ?]: Uses time.Since(subscribeStart) on Subscribe error return; no goroutine needed
