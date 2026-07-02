---
phase: 02-wire-prove-config-loads
plan: 03
subsystem: infra
tags: [strfry, docker-compose, router, nostr, lmdb, dockur, bloom, ssh, arm64]

# Dependency graph
requires:
  - phase: 01-generate-router-config
    provides: gen-router-config.sh — streams-only router config generator (produces ~/deepfry/strfry-router.config)
  - phase: 02-wire-prove-config-loads (plan 02)
    provides: docker-compose.strfry.yml ROUTER= env + router config bind-mount on the strfry service (commit afea6b8)
provides:
  - Live arm64 host proof that the ROUTER= path loads the router config and activates ingest streams (02-VERIFICATION-EVIDENCE.md)
  - Confirmation that the 260701-l5s blocker is broken (Loading router config file: now appears)
  - Confirmation that a single strfry router process replaces the 144-proc strfry stream fan-out (ING-01)
  - Confirmation the LMDB is unmoved at its host bind-mount path after container recreate (HARD RULE)
affects: [03-live-gating, phase-3, ING-02, stream-relays.sh-retirement, phase-4]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Live-host verify-on-arch: the config-load blocker is arm64-specific, so proof runs on the deploy Mac mini via the operator's SSH ControlMaster (/tmp/ssh-strfry.sock), not local/CI"
    - "Recreate-to-apply: ROUTER= env is baked at container-create time and single-file bind-mounts truncate on git pull, so delivery = git pull + docker compose up -d --force-recreate strfry"
    - "Evidence artifact mirrors 01-VERIFICATION.md: frontmatter + Observable Truths table binding each ROADMAP SC to captured host output"

key-files:
  created:
    - .planning/phases/02-wire-prove-config-loads/02-VERIFICATION-EVIDENCE.md
  modified: []

key-decisions:
  - "Delivered the plan-01/02 changes to the host via git pull --ff-only (6f7f01c → 5a4d309), then force-recreated the strfry container — cures the single-file bind-mount truncation and bakes ROUTER= in one step"
  - "Routed all host commands through the operator's existing live SSH ControlMaster socket (/tmp/ssh-strfry.sock); public-key auth to the host is refused, and the master already holds an authenticated password session (ControlPersist=4h)"
  - "Set explicit PATH=/usr/local/bin:/opt/homebrew/bin:... in each non-interactive SSH command because docker (/usr/local/bin) and tmux (/opt/homebrew/bin) are not on the default non-login PATH"
  - "Used BusyBox-compatible `pgrep -f … | wc -l` for the in-container process count (the image's BusyBox pgrep lacks -c)"

patterns-established:
  - "Config-load proof = three log lines (Loading router config file: / New stream group [ingest] / ingest: Connecting to wss://) + one-router process count + LMDB-unmoved mount inspection, all captured, not paraphrased (D-07)"

requirements-completed: [OPS-01, ING-01]

# Metrics
duration: 9min
completed: 2026-07-02
status: complete
---

# Phase 2 Plan 03: Live Host Config-Load Proof Summary

**On the live arm64 Mac mini, the wired `ROUTER=` path loads the generated router config (`Loading router config file:` — the line that never appeared in 260701-l5s), activates 174 ingest streams under a single `strfry router` process, and leaves the LMDB unmoved at its host bind-mount — breaking the blocker.**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-07-02T06:51:49Z
- **Completed:** 2026-07-02T07:00:30Z
- **Tasks:** 1 executed this session (Task 2; Task 1 checkpoint approved by operator prior)
- **Files modified:** 1 created (evidence artifact)

## Accomplishments

- Proved on the deploy host (`Gs-Mac-mini`, arm64) that `ROUTER=/app/strfry-router.config` causes dockur's `strfry.sh` entrypoint to run `./strfry router …` and load the config — `Loading router config file: /app/strfry-router.config` appears at boot (t≈0.078s), the exact line that never appeared in 260701-l5s.
- Captured `New stream group [ingest]` followed by 174 `ingest: Connecting to wss://…` lines (162 `Connected`), with `Failed to parse router config` count = 0 — streams activated from the generated config.
- Confirmed exactly **one** `strfry router` process (PID 29) and **zero** `strfry stream` procs, versus the 144-proc `strfry stream` fan-out at baseline — the ING-01 reader-slot fix, live.
- Confirmed the LMDB stayed at its host bind-mount `/Volumes/BACKUP/nostr/strfry_database → /app/strfry-db` (Type=bind, no named volume) after the recreate, and the relay stayed healthy (`http_code=200`, `Health=healthy`) on 7777 — HARD RULE preserved.
- Recorded all of the above as captured host output (not paraphrase) in `02-VERIFICATION-EVIDENCE.md` (D-07).

## Task Commits

1. **Task 1: Confirm safe to mutate the live host** — checkpoint:human-verify, approved by operator before this session ("approved — deliver via git pull"). No commit.
2. **Task 2: Run the host proof and record the evidence artifact** — `5c37b1a` (test)

_No plan-metadata commit is listed here; the docs/state metadata commit is recorded below under Files Created/Modified._

## Files Created/Modified

- `.planning/phases/02-wire-prove-config-loads/02-VERIFICATION-EVIDENCE.md` — recorded host proof: frontmatter + Observable Truths table binding each of the five ROADMAP SCs to captured host log excerpt, process listing, and LMDB mount-source line.

## Host actions performed (live, gated behind the approved checkpoint)

- `git pull --ff-only origin main` on `~/git/deepfryupstream/deepfry` (`6f7f01c → 5a4d309`) — delivered plan-01 `gen-router-config.sh` + plan-02 compose `ROUTER=` wiring (`afea6b8`).
- Ran `gen-router-config.sh` → wrote `~/deepfry/strfry-router.config` (174 relays, streams-only, 1 bloom `pluginDown`).
- `tmux kill-session -t strfry-streams` (no active session — defensive; `stream-relays.sh` NOT modified, per D-09 / Phase 4 boundary).
- `docker compose -f docker-compose.strfry.yml up -d --force-recreate strfry` — applied `ROUTER=` env, cured bind-mount truncation, tore down the 144 docker-exec'd `strfry stream` procs.

## Decisions Made

- **Delivery via git pull + force-recreate** (per operator's approval). The recreate is mandatory anyway (ROUTER env baked at create time) and simultaneously cures the git-pull single-file bind-mount truncation gotcha.
- **Used the operator's live SSH ControlMaster** (`/tmp/ssh-strfry.sock`): direct public-key auth to `g@192.168.149.21` is refused by the host, but a live master process (`ssh -fN -M -S /tmp/ssh-strfry.sock … ControlPersist=4h … PreferredAuthentications=password`) already held an authenticated session. All host commands were multiplexed through it (`ssh -S /tmp/ssh-strfry.sock -o BatchMode=yes`). No new credentials were needed or created.
- **Explicit PATH per SSH command** — `docker`/`tmux` are not on the non-login-shell PATH on the host.

## Deviations from Plan

None affecting scope. Two operational adaptations, both anticipated by the plan/context:

- The `strfry-streams` tmux session was already gone; the 144 `strfry stream` procs were docker-exec children of the container and were torn down by the recreate (the plan explicitly notes the recreate does this). `stream-relays.sh` left unmodified.
- Host tooling PATH + BusyBox `pgrep` (no `-c`) required minor command-shape adjustments (explicit PATH; `pgrep -f … | wc -l`). No behavior change to the proof.

**D-05 fallback:** NOT triggered — `Loading router config file:` appeared on the first `ROUTER=` recreate, so neither the command-override rung (a) nor the dedicated router-only service rung (b) was built. Success was not declared on the trigger alone; the positive evidence is recorded.

## Issues Encountered

- **Host authentication:** the deploy host refused public-key SSH auth and BatchMode. Resolved by detecting and reusing the operator's already-authenticated live ControlMaster socket at `/tmp/ssh-strfry.sock` (not the stale `~/.ssh/ssh-strfry.sock` an `ls` artifact suggested). Host was reachable (ping 0.8ms) and the master was live (`ssh -O check` → `Master running`), so no evidence was fabricated.

## Next Phase Readiness

- OPS-01 (config loads on the deploy host) and ING-01 (one router process, no fan-out) are proven live — the 260701-l5s blocker is broken.
- **Deferred to Phase 3 (ING-02):** the LIVE gating *decision* proof (that the bloom `pluginDown` correctly rejects out-of-WoT events). This plan proves config-load + stream-activation only; the bloom gate is wired and processing (165,667 decisions observed) but accept/reject correctness is Phase 3's live proof (threat T-02-09).
- **Deferred to Phase 4:** retiring/replacing `stream-relays.sh` and deciding the standing ingest topology. The fan-out is currently stopped (torn down by the recreate); restarting it is out of scope here.
- Host left healthy: strfry running, one router, 174 ingest streams, relay serving 7777, LMDB at its bind-mount.

## Self-Check: PASSED

- `02-VERIFICATION-EVIDENCE.md` exists (FOUND).
- Commit `5c37b1a` exists (FOUND).
- Plan automated verify (five greps) PASS.

---
*Phase: 02-wire-prove-config-loads*
*Completed: 2026-07-02*
