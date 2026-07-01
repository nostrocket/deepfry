---
created: 2026-07-01
title: Run strfry router via dockur ROUTER= entrypoint (replace 174-stream fan-out)
area: deploy
files:
  - stream-relays.sh
  - config/strfry/strfry.conf
  - docker-compose.strfry.yml
  - .planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/
---

## Problem

`stream-relays.sh` spawns ~174 `strfry stream <relay>` processes (one
`docker exec strfry /app/strfry stream <relay>` per relay in the WoT config).
That many processes opening the same LMDB env exhausts / corrupts the shared
reader-slot table → `MDB_BAD_RSLOT` crash of the canonical relay (surfaced after
the bloom writePolicy plugin was fixed to actually run). Converting to a SINGLE
`strfry router` process is the intended fix.

Blocked in quick task **260701-l5s**: calling the binary directly —
`docker exec strfry /app/strfry router <streams-file>` — does NOT load the
positional router config on the pinned `dockurr/strfry 1.1.0` build (commit
`f31a1b9`). Verified extensively (process alive, line-buffered): the
`Loading router config file:` line never appears, 0 streams activate, 0 events,
no error. Updating the image won't help — 1.1.0 is the latest release and equals
the pinned commit.

Lead: the dockur image ships an entrypoint (`strfry.sh`) that drives the router
via a `ROUTER=` env var (runs `./strfry router "$ROUTER"`). The env-var path may
work where the raw `docker exec` doesn't — that's the route to pursue.

## Solution

1. Read dockur's `strfry.sh` entrypoint + Dockerfile (github.com/dockur/strfry)
   to see exactly how `ROUTER=` is wired vs the legacy `STREAMS=` path (STREAMS=
   is the per-relay `strfry stream` fan-out we are REPLACING — do not use it).
2. Set the router config as a standalone streams-only file:
   `streams { ingest { dir="down"  pluginDown="/app/plugins/bloom"  urls=[...] } }`.
   `pluginDown` is MANDATORY — the global `writePolicy` does NOT gate router-down
   events, so omitting it silently bypasses the whitelist (gating regression).
3. Wire it via the compose service (e.g. `ROUTER` env / mount the router config)
   or a dedicated one-shot; generate the streams file from the same WoT relay list
   `stream-relays.sh` already loads.
4. VERIFY ON HOST before shipping: `Loading router config file:` appears, streams
   connect, bloom `decision=accept/reject` lines flow, strfry stays up (no
   MDB_BAD_RSLOT), and it's a SINGLE process (not 174).

**HARD CONSTRAINT (CLAUDE.md):** do NOT move the LMDB or migrate to a named
volume — the DB stays at its host bind-mount path for cross-host interop.

See `.planning/quick/260701-l5s-.../260701-l5s-RESEARCH.md` (full source-verified
router schema + version analysis) and `260701-l5s-PLAN.md` (blocker + next steps).
