# strfry-router-conversion

## What This Is

A focused, root-level DeepFry infrastructure project to replace the current
174-process `strfry stream` fan-out (`stream-relays.sh`) with a **single
`strfry router` process** wired through dockur's `ROUTER=` entrypoint. The
router must ingest from the same Web-of-Trust relay list, keep whitelist gating
intact, be verified on the live StrFry host, and let the old fan-out script be
retired. For the DeepFry operator running the canonical `strfry` +
`strfry-quarantine` deploy.

## Core Value

**One `strfry router` process replaces the 174-process fan-out — without
dropping whitelist gating and without crashing the canonical relay
(`MDB_BAD_RSLOT`).** If everything else fails, ingestion must stay gated and the
relay must stay up.

## Requirements

### Validated

<!-- Shipped and confirmed valuable. -->

- [x] Generate a streams-only router config from the WoT relay list, with a
      mandatory per-stream `pluginDown=/app/plugins/bloom` on every stream
      — *Validated in Phase 1 (CFG-01, ING-02 config-side): `gen-router-config.sh`
      emits the config; 22/22 bats pass; verifier 6/6; the CR-01 relay-URL
      injection / whitelist-bypass vector was found and closed. Live on-host proof
      of config load + gating is Phase 2/3.*

- [x] Wire the router through dockur's `ROUTER=` entrypoint env var in
      `docker-compose.strfry.yml`, and prove on the live arm64 host that the
      config actually loads
      — *Validated in Phase 2 (OPS-01, ING-01): `docker-compose.strfry.yml`
      sets `ROUTER: /app/strfry-router.config` + a `:ro` router-config bind-mount
      on the existing strfry service (no separate service, no `docker exec`). On
      the live deploy host `Loading router config file:` appeared (the line that
      NEVER appeared in `260701-l5s`), streams activated (`New stream group
      [ingest]` + 174 `Connecting to wss://`), exactly ONE `strfry router` process
      ran (vs the 174-stream fan-out), and the LMDB stayed at its host bind-mount
      path — captured in `02-VERIFICATION-EVIDENCE.md`. The `260701-l5s` blocker
      is broken.*

- [x] Verify on the live host that gating flows and the relay stays stable
      — *Validated in Phase 3 (verify-gating-stability-on-host): live on-host proof
      that bloom `decision=accept/reject` flows on router-down events, exactly one
      `strfry router` process, no `MDB_BAD_RSLOT`, and strfry stays up — captured in
      `03-VERIFICATION-EVIDENCE.md`.*

- [x] Retire `stream-relays.sh` once the router path is proven, keeping the WoT
      relay list as the single source of truth
      — *Validated in Phase 4 (OPS-03, retire-the-fan-out): `stream-relays.sh`
      deleted via clean `git rm` (no stub — D-01), README rewritten to the single
      `strfry router` (`ROUTER=`) + `gen-router-config.sh` ingestion path, and fresh
      read-only host proof captured (1 router / 0 stream procs, no `strfry-streams`
      session, WoT `relay_urls:` list of 174 unchanged and identical to the live
      `strfry-router.config`) in `04-VERIFICATION-EVIDENCE.md`.*

### Active

<!-- Current scope. Building toward these. -->

- Milestone v1.0 (strfry-router conversion) complete — all four phases shipped and
  verified on the live arm64 host. No active scope; project wraps per Out of Scope
  ("project wraps when the router conversion ships").

### Out of Scope

<!-- Explicit boundaries. Includes reasoning to prevent re-adding. -->

- Moving the StrFry LMDB / migrating to a Docker named volume — HARD RULE
  (CLAUDE.md): DB stays at its host bind-mount path for cross-host interop
- Forking or patching StrFry — extend only via config / plugins / entrypoint
- Upgrading off the pinned `dockurr/strfry 1.1.0` (commit `f31a1b9`) image —
  1.1.0 is the latest release and equals the pinned commit; an upgrade won't fix
  the router-config-load blocker and would be a separate decision
- The legacy `STREAMS=` per-relay fan-out entrypoint path — that is the mechanism
  being replaced, not reused
- Cross-project work in `web-of-trust`, `LMDB2GraphQL`, `whitelist-plugin`,
  `quarantine-rescuer`, etc. — this project governs the root StrFry deploy only
- A broader StrFry deploy/orchestration charter — deliberately narrow;
  project wraps when the router conversion ships

## Context

- **Blocked predecessor:** quick-task `260701-l5s`
  (`.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/`)
  produced source-verified research (RESEARCH.md), a documented ~9-variant host
  investigation (PLAN.md), and a BLOCKED outcome (SUMMARY.md). No code shipped;
  `stream-relays.sh` is unchanged and the host was left clean.
- **Root cause of the current crash:** ~174 `strfry stream` processes each open
  the same LMDB env, exhausting/corrupting the shared reader-slot table →
  `MDB_BAD_RSLOT` on the canonical relay (surfaced after the bloom writePolicy
  plugin was fixed to actually run). A prior task raised `maxreaders` to 4096
  (deployed) as mitigation; the router conversion is the intended real fix.
- **The blocker to break:** on the deployed `dockurr/strfry 1.1.0`, calling the
  binary directly — `docker exec strfry /app/strfry router <file>` — never loads
  the positional router config (`Loading router config file:` never logs, 0
  streams activate, no error). Lead: dockur's entrypoint (`strfry.sh`) drives the
  router via a `ROUTER=` env var (`./strfry router "$ROUTER"`); the env-var path
  may work where raw `docker exec` doesn't.
- **Router config schema (source-verified):** top-level key is
  `streams { <name> { dir, urls, filter, pluginDown, ... } }`. golpe silently
  ignores unknown top-level keys, so `streams{}` cannot live in the main
  `--config` file — it must be a standalone router config file.
- **Gating gotcha:** the global `writePolicy` does NOT gate router-down events,
  so `pluginDown` is mandatory per stream or the whitelist is silently bypassed.
- **Deploy host:** Apple Silicon Mac mini (arm64), repo at
  `~/git/deepfryupstream/deepfry`, docker at `/usr/local/bin`. strfry +
  strfry-quarantine + lmdb2graphql currently healthy.
- **Files owned by this project:** `stream-relays.sh`,
  `docker-compose.strfry.yml`, `config/strfry/` (`strfry.conf`,
  `strfry-quarantine.conf`, guard scripts).

## Constraints

- **StrFry integrity**: Stock/unmodified StrFry only, extended via
  config/plugins/entrypoint — never fork. Stay on pinned `dockurr/strfry 1.1.0`
  (`f31a1b9`). — Core DeepFry principle; the plugin/entrypoint interface is the
  only supported extension surface.
- **Whitelist gating**: Router-down ingestion MUST stay gated by the bloom
  whitelist plugin (`pluginDown` per stream). An ungated path is a release
  blocker. — The global `writePolicy` does not cover router-down events.
- **Data location**: NEVER move the LMDB or migrate to a named volume; it stays
  at the host bind-mount path. — Cross-host interoperability (CLAUDE.md HARD RULE).
- **Verify-on-host**: No config rewrite ships unverified on the live host;
  withhold rather than risk broken or ungated ingestion. — The discipline that
  correctly blocked `260701-l5s`.

## Key Decisions

<!-- Decisions that constrain future work. Add throughout project lifecycle. -->

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Single `strfry router` process replaces the per-relay `strfry stream` fan-out | 174 env-openers exhaust the LMDB reader-slot table → `MDB_BAD_RSLOT`; one process fixes it at the source | — Pending |
| Drive the router via dockur's `ROUTER=` entrypoint env var, not raw `docker exec ... router <file>` | Raw positional-config path silently fails to load on 1.1.0 (`f31a1b9`); the entrypoint env-var path is the untried lead | — Pending |
| Router config is a standalone streams-only file, not merged into the main `--config` | golpe silently ignores unknown top-level keys, so `streams{}` won't load from the main config | — Pending |
| `pluginDown=/app/plugins/bloom` mandatory on every stream | Global `writePolicy` doesn't gate router-down events; omitting it silently bypasses the whitelist | — Pending |
| Narrow project scope (router conversion only), not a broad deploy-layer charter | Keep the effort bounded; project completes when the conversion ships | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-07-02 — Phase 4 complete; milestone v1.0 shipped (`stream-relays.sh` fan-out retired, single `strfry router` path proven end-to-end on the live arm64 host, WoT relay list intact as single source of truth)*
