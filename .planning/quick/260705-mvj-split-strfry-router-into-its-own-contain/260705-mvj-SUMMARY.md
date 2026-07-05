---
phase: 260705-mvj
plan: 01
subsystem: strfry
tags: [docker-compose, ingestion, router, healthcheck, oom, lmdb, ops]
requires:
  - Dockerfile.strfry (stock dockur/strfry image, bloom plugin baked at /app/plugins/bloom)
  - config/strfry/strfry.conf (shared db path + dbParams.maxreaders=4096)
  - ~/deepfry/strfry-router.config (host-generated streams config, pluginDown=bloom)
provides:
  - Dedicated relay/router container split (strfry relay-only + strfry-router router-only)
  - Ingestion-liveness process healthcheck (config/strfry/router-liveness.sh)
  - Deployment & verification runbook (README.md)
affects:
  - docker-compose.strfry.yml
  - config/strfry/router-liveness.sh
  - README.md
tech-stack:
  added: []
  patterns:
    - Explicit command: override to run router-only (mirrors strfry-quarantine pattern, avoids strfry.sh's forced relay launch)
    - Process-liveness healthcheck (busybox ps+grep, dependency-free) over port/freshness probes
    - Bounded container mem_limit + default oom_score_adj for deterministic OOM attribution
key-files:
  created:
    - config/strfry/router-liveness.sh
  modified:
    - docker-compose.strfry.yml
    - README.md
decisions:
  - "Router runs via explicit command: not strfry.sh — strfry.sh always launches a relay first, which would double-bind :7777"
  - "Healthcheck is process-liveness (ps for `strfry ... router`) not freshness — freshness false-fails during legitimate bloom-gate quiet windows"
  - "strfry-router gets default oom_score_adj (0); relay keeps -300 so the disposable router is the acceptable OOM victim while canonical data is the last sacrifice"
  - "mem_limit default 512m — headroom for ~174 upstream WS conns + LMDB page tables while still bounding a runaway; operator-tunable via STRFRY_ROUTER_MEM_LIMIT"
  - "Both containers bind-mount identical db dir + strfry.conf (maxreaders=4096) for safe multi-process LMDB; host bind-mount, no named volume (CLAUDE.md)"
metrics:
  duration: ~15m
  completed: 2026-07-05
status: complete
---

# Phase 260705-mvj Plan 01: Split StrFry Router Into Its Own Container Summary

Isolated the `strfry router` ingestion process into a dedicated, bounded, auto-restarting `strfry-router` container with a process-liveness healthcheck, closing the three gaps that caused a ~5h silent zero-ingestion OOM outage (unattributed OOM, no auto-restart, relay-port healthcheck hiding a dead router).

## What Was Built

- **`config/strfry/router-liveness.sh`** (new, executable): a POSIX `sh`, dependency-free healthcheck that exits 0 iff the `strfry router` process is alive (`ps -ef | grep '[s]trfry.* router'`, bracket-trick avoids self-match) and 1 otherwise, printing an ok/fail diagnostic. No curl/jq/config reads. Mirrors the `quarantine-db-guard.sh` `:ro` mount pattern.
- **`docker-compose.strfry.yml`** (split):
  - `strfry` is now relay-only — removed the `ROUTER` env (strfry.sh no longer launches the router) and removed the router-only `strfry-router.config` + `router.yaml` mounts; kept the db, `strfry.conf`, `whitelist.yaml`, `bloom-data` mounts, `oom_score_adj: -300`, `restart: unless-stopped`, port `7777`, and the inherited relay healthcheck.
  - New `strfry-router` service — same stock image, explicit `command: ["/app/strfry","--config=/etc/strfry.conf","router","/app/strfry-router.config"]` (router only, never strfry.sh), `restart: unless-stopped` (SPLIT-01), `mem_limit: ${STRFRY_ROUTER_MEM_LIMIT:-512m}` (SPLIT-02), no `oom_score_adj`, no `ports:`. Mounts the identical db dir + `strfry.conf` as the relay (safe multi-process LMDB) plus `strfry-router.config`, `router.yaml`, `whitelist.yaml`, writable `bloom-data` (pluginDown gating), and the `router-liveness.sh` script. SPLIT-03 healthcheck runs the liveness script (interval 1m / timeout 5s / retries 2).
  - Added an in-file INCIDENT RATIONALE comment documenting the outage.
- **`README.md`**: added `strfry-router` to the services table; updated the ingestion note + Configuration Reference to reflect the dedicated container; added `STRFRY_ROUTER_MEM_LIMIT` to the `.env` table; added a Deployment & verification runbook (config regen → `--force-recreate strfry strfry-router` → dual-container health confirmation → `docker kill` auto-restart proof (OOMKilled/ExitCode/RestartCount) → event-count-delta ingestion check).

## Verification

- `test -x config/strfry/router-liveness.sh && sh -n ...` → PASS; functional grep check confirms it matches the router argv, rejects the relay-only line, and does not self-match.
- `docker compose -f docker-compose.strfry.yml config` → PASS (validated locally; docker present). Resolved `strfry-router`: `command` = router subcommand, `mem_limit` = 536870912 (512 MiB), healthcheck → liveness script, router.config/router.yaml/liveness mounts resolve.
- Relay integrity confirmed via resolved config: `oom_score_adj: -300`, port 7777, db + strfry.conf + whitelist.yaml + bloom-data mounts present; router-only mounts moved off it; no `ROUTER` env.
- README checks: `strfry-router`, `runbook`, `force-recreate` all present.

## Requirements

- SPLIT-01 (auto-restart on router OOM): `restart: unless-stopped` on strfry-router.
- SPLIT-02 (bounded, attributed OOM): explicit `mem_limit` + default oom_score_adj.
- SPLIT-03 (ingestion-liveness healthcheck): `router-liveness.sh` + healthcheck wiring.

## Threat Mitigations

- **T-260705-01** (untrusted upstream bypassing whitelist): `pluginDown=/app/plugins/bloom` preserved — bloom plugin baked in image + writable `bloom-data` mount on strfry-router.
- **T-260705-02** (shared LMDB reader-table corruption): identical db dir + identical `strfry.conf` (maxreaders=4096) on both containers; host bind-mount, no named volume.
- **T-260705-03** (router runaway → VM-wide OOM killing the relay): `mem_limit` on router + relay's `-300` + router's default oom_score_adj.
- **T-260705-04** (silent router death): own container + `restart: unless-stopped` + process-liveness healthcheck.
- **T-260705-SC** (supply chain): accepted — no new packages; stock image + vendored plugins reused unchanged.

## Deviations from Plan

None — plan executed exactly as written. The relay service inherits its healthcheck from the base image (no explicit `healthcheck:` block in the file); the plan directed leaving the relay healthcheck as-is, so it was left untouched.

## Scope Boundary

Repo change only, per the task constraints. No SSH/deploy to any host; no `docker compose up`/`build` run. "verify" used `docker compose config` (local, available) for structural validation. The deployment + live verification (config regen, force-recreate, kill/restart proof, ingestion-delta) are captured as the operator-facing README runbook.

## Known Stubs

None.

## Self-Check: PASSED

- FOUND: config/strfry/router-liveness.sh (executable, `sh -n` clean)
- FOUND: docker-compose.strfry.yml (strfry-router service resolves, ROUTER removed)
- FOUND: README.md (services table + runbook)
- FOUND commits: 402c5c6 (T1), 1d5b5de (T2), d1089b4 (T3)
