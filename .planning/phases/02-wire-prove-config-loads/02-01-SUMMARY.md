---
phase: 02-wire-prove-config-loads
plan: 01
subsystem: infra
tags: [strfry, docker, router, entrypoint, dockur, nostr]

# Dependency graph
requires:
  - phase: 01-generate-router-config
    provides: standalone streams-only router config + container path /app/strfry-router.config (D-08/D-10)
provides:
  - Source-cited confirmation that ROUTER=<path> runs ./strfry router "$ROUTER" from cwd /app in the same container as ./strfry relay
  - Confirmed cwd (/app), quoting, positional-arg binding, and db resolution (/etc/strfry.conf, router file streams-only)
  - The [ -f "$ROUTER" ] file-existence guard as a load-bearing prerequisite for plan 03
  - The single D-05 fallback trigger ("Loading router config file:" never appears) + contingency-only ladder
affects: [02-02 compose wiring, 02-03 host proof, wire-prove-config-loads]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Confirm-on-paper de-risk: read the exact compiled source at the pinned ref before touching infra config"

key-files:
  created:
    - .planning/phases/02-wire-prove-config-loads/02-ENTRYPOINT-WIRING.md
  modified: []

key-decisions:
  - "Deployed dockurr/strfry:1.1.0 maps to dockur's v1.1.0 TAG (not master): that tag's Dockerfile carries strfry.sh + CMD [/app/strfry.sh]; master has reverted to ENTRYPOINT [/app/strfry]/CMD [relay] and is NOT representative"
  - "ROUTER=<path> confirmed to pass the value AS the quoted positional <routerConfigFile> to ./strfry router from cwd /app — plan 02 wiring (D-01/D-02) is well-founded, no contradiction"
  - "The [ -f \"$ROUTER\" ] guard means the router config bind-mount MUST exist at the container path before start, or the router silently never runs (no error) — plan 03 prerequisite"

patterns-established:
  - "Cite each source file by github.com ref + exact commit/tag + line numbers when confirming external-image behavior on paper"

requirements-completed: [OPS-01]

# Metrics
duration: 6min
completed: 2026-07-02
status: complete
---

# Phase 2 Plan 01: ROUTER= Entrypoint Wiring Confirmation Summary

**Source-verified (dockur v1.1.0 `strfry.sh` + hoytech f31a1b9 `cmd_router.cpp`) that `ROUTER=<path>` runs `./strfry router "$ROUTER"` from cwd `/app` in the same container as the relay, passing the value as the quoted positional `<routerConfigFile>` — plan 02 can wire it once with confidence.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-07-02T06:36:39Z (approx, phase execution start)
- **Completed:** 2026-07-02
- **Tasks:** 1
- **Files modified:** 1 (created)

## Accomplishments
- Read the ACTUAL entrypoint source (`strfry.sh` @ dockur `v1.1.0`) that the deployed `dockurr/strfry:1.1.0` image is built from, and confirmed on paper the exact `ROUTER=<path>` invocation: `cd /app` (line 32) → `./strfry relay &` (line 49) → `if [ -f "$ROUTER" ]; then ./strfry router "$ROUTER" &` (lines 52-54).
- Discovered and recorded a source-of-truth correction: the deployed image maps to dockur's **`v1.1.0` tag** (Dockerfile `COPY strfry.sh /app/strfry.sh` + `CMD ["/app/strfry.sh"]`), whereas `master` has reverted to `ENTRYPOINT ["/app/strfry"]` / `CMD ["relay"]` with no `strfry.sh` — so future readers must read the tag, not master.
- Cross-checked `cmd_router.cpp` @ hoytech `f31a1b9` (== tag 1.1.0, GitHub compare "identical"): `USAGE` `router <routerConfigFile>` (line 18), `Loading router config file:` (line 300), `loadRawTaoConfig(routerConfigFile)` (line 303), `routerConfig.at("streams")` (line 332), `::exit(1)` on first-load parse failure (line 351) — T-02-01 mitigation satisfied.
- Recorded db resolution (bare `./strfry router` → golpe search order → `/etc/strfry.conf` supplies `db`; router file stays streams-only per Phase 1 D-10), why `ROUTER=1`/`yes` is forbidden (D-03: loads baked-in sample `/etc/strfry-router.conf` `friends{}` with wrong relays/plugin), and why `STREAMS=` is the wrong legacy per-relay `strfry stream` fan-out.
- Recorded the single D-05 fallback trigger (`Loading router config file:` never appears on host) + the contingency-only ladder (a: explicit `command:` override `/app/strfry --config=/etc/strfry.conf router /app/strfry-router.config`; b: dedicated router-only service sharing the same LMDB + `/etc/strfry.conf`).

## Task Commits

1. **Task 1: Read dockur source and confirm the ROUTER= invocation on paper** - `1d528b9` (docs)

**Plan metadata:** committed with SUMMARY/STATE/ROADMAP update.

## Files Created/Modified
- `.planning/phases/02-wire-prove-config-loads/02-ENTRYPOINT-WIRING.md` (created, 169 lines) - Source-cited confirmation of the ROUTER= entrypoint wiring: confirmed invocation + cwd, config/db resolution, why ROUTER= not ROUTER=1/not STREAMS=, and the D-05 fallback trigger + ladder.

## Decisions Made
- **Read the `v1.1.0` tag, not `master`.** The plan directed reading dockur `strfry.sh`/`Dockerfile`; while fetching I found `master`'s Dockerfile no longer uses `strfry.sh` (it reverted to `ENTRYPOINT ["/app/strfry"]`/`CMD ["relay"]`). The deployed `dockurr/strfry:1.1.0` (published 2026-04-26) maps to dockur's `v1.1.0` tag, whose Dockerfile carries `COPY strfry.sh /app/strfry.sh` + `CMD ["/app/strfry.sh"]` + `COPY strfry-router.conf /etc/strfry-router.conf`. All confirmations were taken from the `v1.1.0` tag so the artifact reflects the ACTUAL deployed binary. This is documented prominently in the artifact's "Sources read" section.

## Deviations from Plan

None - plan executed exactly as written. (The tag-vs-master source selection above is a documentation-fidelity decision within the plan's Task 1 mandate, not a deviation from planned scope. No code was changed; `docker-compose.strfry.yml` was left untouched as required.)

## Issues Encountered
- The dockur `master` branch does not represent the deployed image (its Dockerfile omits `strfry.sh`). Resolved by fetching the `v1.1.0` tag, which is what Docker Hub `dockurr/strfry:1.1.0` (2026-04-26) is built from, and citing that tag throughout.

## User Setup Required
None - no external service configuration required. (This plan is on-paper source confirmation only; no host action taken. On-host proof is plan 03.)

## Next Phase Readiness
- **Plan 02 (compose wiring):** Well-founded. Wire `ROUTER=/app/strfry-router.config` on the existing `strfry` service + bind-mount host `~/deepfry/strfry-router.config` → container `/app/strfry-router.config`. The confirmed env var name (`ROUTER`) and container path are locked. Do NOT use `ROUTER=1`/`STREAMS=`.
- **Plan 03 (host proof):** Clear pass/fail. Success = `Loading router config file:` → `New stream group [ingest]` → `ingest: Connecting to wss://...` with exactly one `strfry router` process. Prerequisite recorded: the bind-mounted config file MUST exist at `/app/strfry-router.config` before start, or the `[ -f "$ROUTER" ]` guard silently skips the router (no error). Fallback trigger + D-05 ladder documented as contingency-only.

## Self-Check: PASSED

- `.planning/phases/02-wire-prove-config-loads/02-ENTRYPOINT-WIRING.md` — FOUND (169 lines)
- Commit `1d528b9` — FOUND
- Plan automated verification (all 6 grep checks) — PASSED

---
*Phase: 02-wire-prove-config-loads*
*Completed: 2026-07-02*
