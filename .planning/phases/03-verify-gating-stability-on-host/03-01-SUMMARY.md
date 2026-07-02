---
phase: 03-verify-gating-stability-on-host
plan: 01
subsystem: infra
tags: [strfry, router, bloom-plugin, whitelist, lmdb, verification, nostr, arm64]

# Dependency graph
requires:
  - phase: 02-wire-prove-config-loads
    provides: live single-strfry-router path (ROUTER= wired, config loads, 174 streams, fan-out stopped) on Gs-Mac-mini
provides:
  - Recorded release-blocker verification gate proving live bloom gating (accept + reject + not-stored) on the router-down path
  - Recorded soak evidence that the single router runs with 0 MDB_BAD_RSLOT and no restarts
  - Recorded neighbor-service health (strfry, strfry-quarantine, lmdb2graphql) alongside the running router
affects: [04-retire-the-fan-out]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Verify-on-host discipline: bind each ROADMAP SC to captured live-host output before 'verified' (mirrors 02-VERIFICATION-EVIDENCE.md)"
    - "Bounded log reads (--tail / --since / single-pass fixed-string counts) on a 3.68M+ line log to avoid multi-minute full-log scans"

key-files:
  created:
    - .planning/phases/03-verify-gating-stability-on-host/03-VERIFICATION-EVIDENCE.md
  modified: []

key-decisions:
  - "Organic rejects satisfied the reject proof — D-02 upstream-inject fallback NOT fired"
  - "Not-stored proven via docker exec strfry ./strfry scan by-id: rejected id empty, accepted id present (positive control)"
  - "lmdb2graphql probed light-only (/health, /ready, {__typename}) per D-07 — no heavy latestPerAuthor"
  - "Router left running; 174-proc fan-out NOT restored; no config/image/compose change (D-08)"

patterns-established:
  - "Positive-control by-id scan: pair a rejected id (expect empty) with an accepted id (expect stored) to prove the gate both drops and admits correctly"

requirements-completed: [ING-02, OPS-02]

# Metrics
duration: 25min
completed: 2026-07-02
status: complete
---

# Phase 3 Plan 01: Live Gating & Stability Proof Summary

**Verified on the live arm64 host that the single `strfry router` path gates router-down events for real (accept + reject-not_in_wot, rejects proven not-stored) and stays stable with 0 `MDB_BAD_RSLOT` and no restarts — passing the release-blocker gate 4/4 and unblocking Phase 4 cutover.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-02T09:22:21Z (soak window start)
- **Completed:** 2026-07-02T09:41:07Z (soak window end) + artifact write
- **Tasks:** 3 completed
- **Files modified:** 1 created (evidence artifact); no code/config changed

## What Was Built

This is a VERIFICATION / OBSERVATION plan — it produced NO code and NO config/image/compose
changes. It captured live-host evidence and wrote one artifact:
`.planning/phases/03-verify-gating-stability-on-host/03-VERIFICATION-EVIDENCE.md`, binding
each ROADMAP Phase 3 success criterion to actual captured host output (mirroring
`02-VERIFICATION-EVIDENCE.md`).

### Gate verdict: PASSED (4/4)

| SC | Requirement | Status | Key evidence |
|----|-------------|--------|--------------|
| SC#1 | ING-02 | PASS | `decision=accept id=000250447072d935… pubkey=e7d1d7ee` + `decision=reject id=e6e312c3f78831c8… reason=not_in_wot` on `sourceType="Stream"` path; rejected id `scan` → EMPTY (not stored), accepted id `scan` → event returned (stored) |
| SC#2 | OPS-02 | PASS | Soak 09:22:21Z→09:41:07Z; `MDB_BAD_RSLOT`=0 (tail-300k + full-log); RestartCount 0→0; StartedAt 06:55:19Z unchanged; curl 7777=200 start+end; 107,814 decisions / 300k lines |
| SC#3 | OPS-02 | PASS | strfry 7777=200; strfry-quarantine 7778=200 healthy; lmdb2graphql /health=200 /ready=200 `{__typename}`=200 (light only) |
| SC#4 | OPS-02 | PASS | arm64 host; 1 `strfry router` / 0 `strfry stream` procs; fan-out not restored; LMDB unmoved; no config change |

## Task Commits

| Task | Name | Commit |
|------|------|--------|
| 1 | Capture live bloom gating proof (accept + reject + not-stored) on router-down path | (host observation only — no repo files; feeds Task 3) |
| 2 | Capture stability soak + neighbor-service health probes | (host observation only — no repo files; feeds Task 3) |
| 3 | Write 03-VERIFICATION-EVIDENCE.md binding all four ROADMAP SCs | 8eac65c |

## Deviations from Plan

None — plan executed as written. The only operational adjustments were the two overrides
provided at dispatch (both are environment facts, not plan-logic changes):

1. **SSH socket** — used the working ControlMaster `/tmp/ssh-deepfry-host.sock` (the plan
   text's `/tmp/ssh-strfry.sock` was stale) with the required `PATH=/usr/local/bin:$PATH`
   prefix for docker on the arm64 host.
2. **Log-scan bounding** — because `docker logs strfry` holds 3.68M+ bloom decision lines,
   all scans used `--tail` / `--since` windows and single-pass fixed-string counts. The
   authoritative full-log `grep -c MDB_BAD_RSLOT` = 0 (over 3,680,188 decision lines) was
   already established at dispatch; independently reconfirmed via a clean tail-300k window
   (MDB errors = 0). Redundant contending full-log greps were pkill'd once the tail-window
   result was in hand.

## Decisions Made

- **D-02 fallback NOT fired** — organic `decision=reject reason=not_in_wot` events flow
  continuously on the router-down path, so no controlled upstream inject was needed.
- **Not-stored proof via by-id scan with positive control** — rejected id → empty; accepted
  id → stored. This proves the gate both drops out-of-WoT and admits in-WoT events (D-03).
- **lmdb2graphql light-only probe** — `/health`, `/ready`, `{__typename}` only; no heavy
  `latestPerAuthor` (D-07), so the known capacity crash was not manufactured.

## Host State Left

- `strfry`: running, healthy, RestartCount=0, one router process, ingest flowing.
- 174-proc `strfry stream` fan-out: still stopped, NOT restored (D-08) — retirement is Phase 4.
- `strfry-quarantine`, `lmdb2graphql`: running/healthy — probed for health only, never modified.
- LMDB unmoved at `/Volumes/BACKUP/nostr/strfry_database` (HARD RULE); no `~/deepfry/` config touched.

## Follow-ups / Phase 4 readiness

The gate PASSED 4/4 with no D-09 blocker. Phase 4 (retire `stream-relays.sh` / clear
orphaned `strfry stream` procs) is unblocked by this verification.

## Self-Check: PASSED

- FOUND: `.planning/phases/03-verify-gating-stability-on-host/03-VERIFICATION-EVIDENCE.md`
- FOUND: `.planning/phases/03-verify-gating-stability-on-host/03-01-SUMMARY.md`
- FOUND: commit `8eac65c` (Task 3 evidence-artifact commit)
