---
phase: 02-wire-prove-config-loads
plan: 02
subsystem: infra
tags: [strfry, docker, router, compose, bind-mount, nostr]

# Dependency graph
requires:
  - phase: 01-generate-router-config
    provides: standalone streams-only router config + locked container path /app/strfry-router.config (D-08/D-10)
  - phase: 02-wire-prove-config-loads
    plan: 01
    provides: source-cited confirmation that ROUTER=<path> runs ./strfry router "$ROUTER" from cwd /app in the same container as ./strfry relay (D-04)
provides:
  - docker-compose.strfry.yml strfry service now sets ROUTER=/app/strfry-router.config (explicit path, not 1/yes) driving dockur's default strfry.sh entrypoint
  - A matching single-file :ro bind-mount ${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config} -> /app/strfry-router.config
  - The committed compose change the plan-03 on-host config-load proof will exercise
affects: [02-03 host proof, wire-prove-config-loads]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Additive compose wiring: extend the existing service (env + bind-mount) rather than adding an entrypoint/command override or a new service"
    - "Bind-mount source indirection via ${VAR:-default} for host paths (reuse the file's own convention)"

key-files:
  created:
    - .planning/phases/02-wire-prove-config-loads/02-02-SUMMARY.md
  modified:
    - docker-compose.strfry.yml

key-decisions:
  - "Wired ROUTER via env on the EXISTING strfry service (D-01): no entrypoint/command override, no separate router service — the service keeps dockur's strfry.sh, which is exactly what makes ROUTER= viable"
  - "ROUTER set to the explicit path /app/strfry-router.config, never 1/yes (D-03) — 1/yes would load the image's baked-in demo /etc/strfry-router.conf sample"
  - "Router bind-mount sourced from the host runtime artifact ~/deepfry/strfry-router.config (D-02, Phase 1 D-08), not a committed ./config file, via ${STRFRY_ROUTER_CONFIG_PATH:-...} indirection"
  - "Mount is :ro — strfry only reads the router config; host-side regeneration drives hot reload (matches the strfry.conf:ro precedent, T-02-06 mitigation)"
  - "db/relay kept out of the router file/mount (Phase 1 D-10 / T-02-03); db resolves solely from /etc/strfry.conf. LMDB bind-mount untouched (HARD RULE / T-02-05)"

patterns-established:
  - "Model a new single-file config mount on the existing ./config/strfry/strfry.conf:/etc/strfry.conf:ro entry; keep the container target identical to the driving env var value"

requirements-completed: [OPS-01]

# Metrics
duration: 4min
completed: 2026-07-02
status: complete
---

# Phase 2 Plan 02: Wire Router into the Stack Summary

Wired the router into `docker-compose.strfry.yml` by adding `ROUTER=/app/strfry-router.config` and a matching `:ro` single-file bind-mount to the existing `strfry` service — turning plan 01's on-paper confirmation into the one committed compose change plan 03's host proof exercises.

## What Was Built

Two additive changes to the `strfry` service only (D-01: no entrypoint/command override, no separate router service — the service keeps dockur's `strfry.sh`, which is what makes the `ROUTER=` path viable):

1. **`environment:`** — added `ROUTER: /app/strfry-router.config` alongside the existing `STRFRY_PRIVATE_KEY` entry. The explicit path (D-03: not `1`/`yes`) makes dockur's `strfry.sh` fire its `[ -f "$ROUTER" ]` path branch (`./strfry router "$ROUTER" &`) against OUR generated config, never the baked-in `/etc/strfry-router.conf` demo sample.
2. **`volumes:`** — added `${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro`, modeled on the existing `strfry.conf:ro` single-file mount. Sources from the host runtime artifact (D-02/Phase 1 D-08), uses the file's own `${VAR:-default}` indirection convention, and the container target matches the `ROUTER` value exactly. `:ro` because strfry only reads the config; host-side regeneration drives hot reload.

Both additions carry a YAML comment explaining the ROUTER→`strfry.sh` behavior, the db/relay separation (db lives only in `/etc/strfry.conf`), and that changing `ROUTER` requires a container recreate (env baked at create time).

## Verification

All plan automated checks pass:

| Check | Result |
|-------|--------|
| `ROUTER: /app/strfry-router.config` present | PASS |
| `:/app/strfry-router.config:ro` mount present | PASS |
| `STRFRY_ROUTER_CONFIG_PATH` indirection used | PASS |
| ROUTER not set to `1`/`yes` | PASS |
| LMDB bind-mount `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db` untouched (literal grep) | PASS |
| No `db` line/env added to the router mount | PASS |
| `docker compose -f docker-compose.strfry.yml config` parses | PASS |
| `strfry-quarantine` service unchanged (diff scope = strfry service only, +11/-0) | PASS |

## Threat Mitigations Applied

- **T-02-03 (Tampering — router mount duplicating db):** router mount is streams-only; no db env/line added — verified negative.
- **T-02-05 (DoS — relocating canonical LMDB):** LMDB bind-mount not touched; no named volume introduced (HARD RULE). Diff confirms `+11/-0` with the db mount line unchanged.
- **T-02-06 (Info Disclosure — writable router mount):** mount is `:ro`; strfry never writes the config.
- **T-02-04 (EoP — router-down events bypassing whitelist):** config-side mitigation lives in the Phase 1 config the mount points at (per-stream `pluginDown=/app/plugins/bloom`); live gating proof is Phase 3, not this wiring.

## Deviations from Plan

None — plan executed exactly as written. All decisions (D-01/D-02/D-03, `:ro` discretion, db separation) followed the plan and PATTERNS guidance.

## Notes for plan 03 (host proof)

- The `[ -f "$ROUTER" ]` guard (plan 01 finding) means the bind-mount must resolve to an existing file inside the container before start, or the router silently never runs. The proof must verify `/app/strfry-router.config` exists in-container.
- `ROUTER` env is baked at create time → the host proof needs `docker compose up -d --force-recreate strfry` (or `down` then `up -d`) for the change to take effect; the recreate also cures single-file bind-mount truncation from `git pull` and tears down the docker-exec'd `strfry stream` procs (D-09).
- The D-05 fallback ladder was NOT built (contingency-only; single trigger is `Loading router config file:` never appearing in logs).

## Self-Check: PASSED
- FOUND: docker-compose.strfry.yml (modified, +11/-0)
- FOUND: .planning/phases/02-wire-prove-config-loads/02-02-SUMMARY.md
- FOUND: commit afea6b8
