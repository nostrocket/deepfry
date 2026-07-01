---
quick_id: 260701-l5s
title: Convert stream-relays.sh to a single strfry router
status: incomplete
date: 2026-07-01
outcome: blocked
---

## Outcome: BLOCKED — no code shipped

The conversion was **not** applied. `stream-relays.sh` is unchanged. Shipping an
unverified `strfry router` rewrite risked either broken ingestion or, worse,
**ungated** ingestion (bypassing the whitelist bloom plugin), so it was withheld.

## What was accomplished

- **Root-cause research (source-verified).** Determined via strfry/golpe/dockur
  source at pinned commit `f31a1b9`:
  - Router config top-level key is `streams { <name> { dir, urls, filter, pluginDown, ... } }`.
  - The global `writePolicy` does NOT gate router-down events → `pluginDown =
    "/app/plugins/bloom"` is mandatory per stream to preserve whitelist gating.
  - golpe silently ignores unknown top-level keys, so `streams{}` cannot live in
    the main `--config` file.
  Full detail in `260701-l5s-RESEARCH.md`.

- **Extensive host verification (~9 invocation/config variants).** Documented in
  `260701-l5s-PLAN.md`. Every variant failed identically: strfry accepts
  `router <file>` but never loads the positional router config (`Loading router
  config file:` never logged), activates 0 streams, spawns no `pluginDown`
  process, pulls 0 events, logs no error. Build-specific; unresolved.

## Blocker

`strfry router` on the deployed `dockurr/strfry 1.1.0` (commit `f31a1b9`) does
not consume the positional router config file. See PLAN.md "BLOCKER" + "Recommended
next steps" (read dockur's `mesh/cmd_router.cpp` docopt wiring; try the `ROUTER=`
entrypoint env var; or move the LMDB to a named volume as the durable alternative
to the reader-slot contention).

## State left clean

- `stream-relays.sh`: unchanged.
- strfry host: all router test artifacts removed; strfry + strfry-quarantine +
  lmdb2graphql all healthy. maxreaders=4096 fix from the prior task remains deployed.
