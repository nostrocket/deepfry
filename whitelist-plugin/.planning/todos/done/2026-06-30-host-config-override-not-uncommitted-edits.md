---
created: 2026-06-30T07:07:25.808Z
resolved: 2026-06-30T08:30:00.000Z
status: done
title: Move strfry-host deploy config into override/.env instead of uncommitted edits
area: deploy
files:
  - docker-compose.strfry.yml
  - docker-compose.evtfwd.yml
  - docker-compose.lmdb2graphql.yml
  - config/whitelist/whitelist.yaml
  - config/whitelist/whitelist-server.yaml
  - config/whitelist/router.yaml
---

## Problem

The strfry host (`g@192.168.149.21`, checkout `/Users/g/git/deepfryupstream/deepfry`)
carries its deploy-specific configuration as **uncommitted working-tree edits** on top of
`origin/main` — its established pattern. These host-local deltas are:

- `config/whitelist/{whitelist,router}.yaml`: `server_url` → `http://192.168.149.170:8081`
  (the whitelist server runs on a different LAN host, reached by IP, not the docker
  service name `whitelist-server`).
- `config/whitelist/whitelist-server.yaml`: `dgraph_graphql_url` → `http://192.168.149.170:8080/graphql`; `debug` removed.
- `docker-compose.strfry.yml` / `docker-compose.evtfwd.yml`: `deepfry-net` → `strfry-net`
  (own bridge, not the shared external net).
- `docker-compose.strfry.yml`: `oom_score_adj: -300`; `docker-compose.lmdb2graphql.yml`:
  `oom_score_adj: 1000` (the latter is now committed upstream via c7687b6).

Because these live only as uncommitted edits, **every `git pull` on the host requires a
stash → pull → stash-pop reconcile** (done on 2026-06-30 for the bloom cutover). That
reconcile is fragile: it produced one duplicate-key merge artifact (`oom_score_adj`
appearing twice in `docker-compose.strfry.yml`) that had to be hand-fixed, and a future
upstream change to any of these lines would conflict again. A `git checkout .` or an
aggressive clean on the host would also silently wipe the deploy config.

## Solution

Move host-specific values out of the tracked files so the host checkout can stay clean and
`git pull` is conflict-free:

**Option A — `docker-compose.override.yml` (preferred for compose-level deltas).** Compose
auto-merges `docker-compose.override.yml` on top of the base file. Put the host's
`oom_score_adj`, the `strfry-net` network definition/refs, and any host-only volume/port
tweaks there. Keep it gitignored (or commit a `*.override.example.yml`). This removes the
need to edit `docker-compose.strfry.yml` / `evtfwd.yml` in place.

**Option B — `.env` + variable interpolation for endpoints.** Parameterise the server/dgraph
URLs and network name in the tracked configs via `${...}` and supply host values in a
gitignored `.env`:
- `server_url: "${WHITELIST_SERVER_URL:-http://whitelist-server:8081}"` in whitelist.yaml /
  router.yaml (note: viper reads these YAMLs, so confirm env-substitution or switch those
  keys to env-var overrides the loader already supports).
- `dgraph_graphql_url: "${DGRAPH_GRAPHQL_URL:-...}"` in whitelist-server.yaml.
- Network name via a compose `.env` var.
- `debug` via an env-driven default.

**Net effect:** the host checkout tracks `origin/main` with **zero** uncommitted edits; all
host-specific values live in gitignored `docker-compose.override.yml` + `.env`. Future
deploys are just `git pull && docker compose up -d --build` with no stash dance.

**Done when:** the strfry host runs the relay with `git status` clean (no tracked-file
edits), all current host-local values (server/dgraph IPs, `strfry-net`, OOM tuning,
`debug` off) sourced from override/.env, and a `git pull` applies with no stash/conflict.
Document the override/.env setup in the README or a `deploy/` note.

## Context

Surfaced during the 2026-06-30 bloom-gate cutover on the strfry host (milestone v1.1).
A backup of the current host-local edits was saved on the host at
`/tmp/host-local-deploy-*.patch`. Relates to [[2026-06-30-whitelist-server-stale-refresh]]
(both are deploy/ops hardening items for the same stack).

## Resolution (2026-06-30)

Chose the gitignore-templates approach (no Go/viper code change).

Repo (commit `8343c1f`, pushed):
- `config/whitelist/{whitelist,router,whitelist-server}.yaml` gitignored; tracked
  `*.yaml.example` templates + `config/whitelist/README.md` document per-host setup.
- `docker-compose.{strfry,evtfwd,lmdb2graphql}.yml` take the network name from
  `${DEEPFRY_NET_NAME:-deepfry-net}` (`external: true` unchanged); `.env.example`
  documents `DEEPFRY_NET_NAME`. `docker-compose.dgraph.yml` stays the `deepfry-net`
  creator (dev/single-host).

Host (`g@192.168.149.21`, `/Users/g/git/deepfryupstream/deepfry`):
- Backed up + restored the 3 real configs (untracked, host values: `192.168.149.170`,
  bloom keys, no `debug`); appended `DEEPFRY_NET_NAME=strfry-net` to the existing `.env`.
- `git status` is now CLEAN; a further `git pull` is a no-op (no stash-reconcile).
- Non-disruptive: no containers recreated; strfry/quarantine stayed up + healthy on
  the existing `strfry-net`.

Note: with `external: true`, the named network must pre-exist on a host
(`docker network create strfry-net`) — it already does on the strfry host; documented
in `.env.example`.
