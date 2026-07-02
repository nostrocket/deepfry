---
phase: 02-wire-prove-config-loads
plan: 03
verified: 2026-07-02T06:57:43Z
status: passed
score: 5/5 must-have truths verified
host: Gs-Mac-mini (Apple Silicon Mac mini, arm64) — the build/arch where 260701-l5s reproduces
host_repo_tip: 5a4d309 (docs(02-02): complete wire-router-into-stack plan; includes afea6b8 ROUTER= wiring)
d05_fallback_fired: false
behavior_unverified: 0
overrides_applied: 0
---

# Phase 2 Plan 03: Live Host Config-Load Proof — Verification Evidence

**Phase Goal:** Prove on the live arm64 deploy host (D-06) that the wired `ROUTER=`
path breaks the `260701-l5s` blocker — the router config demonstrably LOADS and
streams activate — as a log-line + process-count check (NOT an events-stored
check; that is Phase 3), recorded as a phase evidence artifact (D-07).

**Verified:** 2026-07-02T06:57:43Z on `Gs-Mac-mini` (arm64) via the operator's live
SSH ControlMaster (`/tmp/ssh-strfry.sock`, `g@192.168.149.21`).
**Status:** passed — all five ROADMAP success criteria bound to captured host output.
**D-05 fallback trigger:** did NOT fire — `Loading router config file:` appeared on
the first `ROUTER=` recreate; no command-override / dedicated-service rung was needed.

## Proof procedure (order actually run: D-08 → git pull → D-09 → recreate → capture)

1. **Baseline (before mutation):** host repo `~/git/deepfryupstream/deepfry` at
   `6f7f01c`; `strfry` container `Up 24 hours (healthy)`; **144 `strfry stream`
   procs** running in-container (the fan-out to eliminate); 0 router procs; relay
   `http_code=200` on 7777; LMDB bind mount `/Volumes/BACKUP/nostr/strfry_database
   → /app/strfry-db`; no `ROUTER`/`STREAMS` env baked.
2. **Delivery (git pull):** `git pull --ff-only origin main` advanced the host repo
   `6f7f01c → 5a4d309`, bringing `gen-router-config.sh` (plan 01) and the compose
   `ROUTER: /app/strfry-router.config` + router bind-mount (plan 02, commit `afea6b8`).
3. **D-08 prerequisite:** ran `gen-router-config.sh` on the host → wrote
   `~/deepfry/strfry-router.config` (174 relays, streams-only: 0 top-level `db`,
   0 top-level `relay`, 1 `streams{}`, exactly 1 `pluginDown = "/app/plugins/bloom"`).
4. **D-09 fan-out stop:** `tmux kill-session -t strfry-streams` (no active session —
   the fan-out procs were docker-exec children of the container, torn down by the
   recreate). `stream-relays.sh` was NOT modified (that is Phase 4).
5. **Recreate:** `docker compose -f docker-compose.strfry.yml up -d --force-recreate
   strfry` — bakes the `ROUTER=` env at create time and cures the single-file
   bind-mount truncation. `.env` `STRFRY_DB_PATH=/Volumes/BACKUP/nostr/strfry_database`
   (DB path unchanged).
6. **Capture:** grep `docker logs strfry` for the three required lines + failure
   signature; process count via `docker exec strfry pgrep -f`; LMDB mount via
   `docker inspect`; relay health via `curl http://localhost:7777/`.

## Observable Truths

| # | Truth (ROADMAP SC) | Status | Evidence (captured host output) |
|---|--------------------|--------|---------------------------------|
| 1 | `Loading router config file:` appears — the exact line that NEVER appeared in 260701-l5s (SC #2; breaks the blocker) | ✓ VERIFIED | `docker logs strfry`: `2026-07-02 06:55:21.545 ( 0.078s) [main thread ]INFO\| Loading router config file: /app/strfry-router.config` (count = 1). No `Failed to parse router config` (count = 0). |
| 2 | `New stream group [ingest]` then `ingest: Connecting to wss://…` follow — router activated streams from the generated config (SC #3) | ✓ VERIFIED | Sequential at boot: `06:55:21.550 …New stream group [ingest]`, then `06:55:21.550 …ingest: Connecting to wss://cfrelay.haorendashutest.workers.dev/jade-zenith-prism` (Connecting count = 174; Connected count = 162). |
| 3 | Exactly ONE `strfry router` process runs — not the 144-proc `strfry stream` fan-out (SC #4, ING-01) | ✓ VERIFIED | `docker exec strfry pgrep -f 'strfry router' \| wc -l` = **1**; `…'strfry stream' \| wc -l` = **0** (baseline was 144). Process listing: `PID 1 bash /app/strfry.sh`, `PID 7 ./strfry relay`, `PID 29 ./strfry router /app/strfry-router.config` — nothing else. |
| 4 | LMDB stays at its host bind-mount path — no named-volume migration, no relocation (SC #5, HARD RULE) | ✓ VERIFIED | `docker inspect strfry`: `Type=bind Source=/Volumes/BACKUP/nostr/strfry_database Dest=/app/strfry-db RW=true` — identical to baseline. `docker volume ls` owns no `strfry-db` volume. Host path `drwxr-xr-x /Volumes/BACKUP/nostr/strfry_database` exists on disk. |
| 5 | The relay remains healthy on 7777 (SC #5); evidence recorded as captured host output before "proven" (D-07) | ✓ VERIFIED | `curl -sS http://localhost:7777/` → `http_code=200` (before AND after recreate); `docker inspect` → `Status=running Health=healthy`. This artifact embeds the actual captured excerpts below, not paraphrases. |

**Score:** 5/5 truths verified with captured runtime host evidence.

## Captured host log excerpt (ordered startup — `docker logs strfry`)

```
2026-07-02 06:55:19.507 (   0.028s) [main thread     ]INFO| CONFIG: Loading config from file: /etc/strfry.conf
2026-07-02 06:55:19.518 (   0.040s) [main thread     ]INFO| CONFIG: successfully installed
2026-07-02 06:55:21.545 (   0.078s) [main thread     ]INFO| Loading router config file: /app/strfry-router.config
2026-07-02 06:55:21.550 (   0.083s) [main thread     ]INFO| New stream group [ingest]
2026-07-02 06:55:21.550 (   0.083s) [main thread     ]INFO| ingest: Connecting to wss://cfrelay.haorendashutest.workers.dev/jade-zenith-prism
2026-07-02 06:55:21.559 (   0.092s) [main thread     ]INFO| ingest: Connecting to wss://test.nfrelay.app
2026-07-02 06:55:21.565 (   0.097s) [main thread     ]INFO| ingest: Connecting to wss://nostr-01.yakihonne.com
2026-07-02 06:55:21.865 (   0.397s) [main thread     ]INFO| ingest: Connecting to wss://haven.jonmartins.com/outbox/quebec-nexus-marble
```

Grep tallies over the full recreate logs:
- `Loading router config file:` → **1**
- `New stream group [ingest]` → **1**
- `ingest: Connecting to wss://` → **174**
- `ingest: Connected to wss://` → **162**
- `Failed to parse router config` (failure signature) → **0**

## Captured process listing (`docker exec strfry ps -o pid,args`)

```
PID   COMMAND
    1 bash /app/strfry.sh
    7 ./strfry relay
   29 ./strfry router /app/strfry-router.config
```

- `strfry router` proc count = **1** (single env-opener — the ING-01 fix vs. 144 `strfry stream` env-openers).
- `strfry stream` proc count = **0** (baseline 144 fan-out procs gone after recreate).

## Captured LMDB mount source (`docker inspect strfry`) — HARD RULE proof

```
Type=bind Source=/Volumes/BACKUP/nostr/strfry_database Dest=/app/strfry-db RW=true
```

- Mount `.Type` is `bind` (host filesystem path), **not** `volume` (docker named volume).
- `.Source` `/Volumes/BACKUP/nostr/strfry_database` is unchanged from the pre-recreate
  baseline and matches `.env` `STRFRY_DB_PATH`.
- `docker volume ls | grep -iE 'strfry.?db|strfry_database'` → no match (no named volume owns the DB).
- The LMDB was NOT moved and NOT migrated to a Docker named volume (CLAUDE.md HARD RULE).

## Bloom pluginDown gating (context; live gating decision proof is Phase 3 / ING-02)

The router-down subscriptions carry `pluginDown = "/app/plugins/bloom"`; the plugin is
actively processing ingested events on the host:

```
[bloom-plugin] 2026/07/02 06:55:33 decision=accept id=7a0a31699d3db02e… pubkey=37c10448
[bloom-plugin] 2026/07/02 06:55:33 decision=accept id=2f0569a4b366c447… pubkey=37c10448
[bloom-plugin] 2026/07/02 06:55:33 decision=accept id=6eea308623bcbf63… pubkey=37c10448
```

Gate decisions observed since recreate: **165,667**. This confirms the pluginDown gate is
wired and running on router-down events. Per the threat register (T-02-09), the LIVE
accept/reject *gating decision* proof (that the gate correctly rejects out-of-WoT events)
is Phase 3 (ING-02) — recorded here so config-load is not mistaken for gating-proven.

## D-05 fallback status

**Not triggered.** The D-05 ladder (a: explicit `command:` override
`/app/strfry --config=/etc/strfry.conf router /app/strfry-router.config`; b: dedicated
router-only service sharing the same LMDB bind-mount) applies ONLY if
`Loading router config file:` never appears. It appeared on the first `ROUTER=` recreate,
so no fallback rung was built or needed. The dockur `strfry.sh` entrypoint `ROUTER=` path
(confirmed on paper in 02-ENTRYPOINT-WIRING.md) is now confirmed live.

## Host state left after the proof

- `strfry` container: recreated, `Status=running Health=healthy`, one router process,
  174 ingest streams connecting (162 connected), relay serving 7777.
- The prior 144-proc `strfry stream` fan-out is stopped (torn down by the recreate; no
  active `strfry-streams` tmux session). Restarting/retiring the fan-out is a Phase 4
  concern — `stream-relays.sh` was left unmodified (D-09).
- Host repo `~/git/deepfryupstream/deepfry` at `5a4d309`; `~/deepfry/strfry-router.config`
  generated (174 relays). The DB remains at `/Volumes/BACKUP/nostr/strfry_database`.

---

_Verified: 2026-07-02T06:57:43Z on Gs-Mac-mini (arm64) via live SSH ControlMaster._
_Proof breaks the 260701-l5s blocker: OPS-01 (config loads on the deploy host), ING-01
(one router process replaces the 144-proc stream fan-out). ROADMAP SC #1–5 bound to
captured host evidence._
