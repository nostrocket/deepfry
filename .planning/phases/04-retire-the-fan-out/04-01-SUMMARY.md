---
phase: 04
plan: 01
subsystem: strfry-router-conversion
tags: [ops, cutover, retirement, verification, strfry-router]
status: complete
requires:
  - Phase 3 gating+stability gate PASSED (router path production-safe on the live host)
provides:
  - "stream-relays.sh retired (deleted) — the 174-proc strfry stream fan-out resurrection path is closed in the repo"
  - "README documents the single strfry router (ROUTER= + gen-router-config.sh) ingestion path"
  - "04-VERIFICATION-EVIDENCE.md — fresh post-retirement host proof (1 router / 0 stream, no strfry-streams session, WoT list unchanged & driving the router config)"
affects:
  - README.md
  - stream-relays.sh (deleted)
tech-stack:
  added: []
  patterns:
    - "Read-only on-host re-verification over the live SSH ControlMaster (pgrep/ps/diff/curl), evidence bound per-SC to captured output"
    - "Clean git rm (no stub/redirect) to eliminate an operational resurrection path"
key-files:
  created:
    - .planning/phases/04-retire-the-fan-out/04-VERIFICATION-EVIDENCE.md
  modified:
    - README.md
  deleted:
    - stream-relays.sh
decisions:
  - "D-01: deleted stream-relays.sh entirely (git rm), NOT a stub/redirect — its relay_urls: parser already lives verbatim in gen-router-config.sh, so nothing is lost and no resurrection path remains"
  - "D-02: README Useful commands now documents the single strfry router (ROUTER= in docker-compose.strfry.yml) + gen-router-config.sh regeneration; zero literal stream-relays references"
  - "D-05: no strfry restart/recreate/reconfigure — retirement is a repo edit + read-only host check; StartedAt (06:55:19Z) unchanged"
metrics:
  duration_min: 2
  completed: 2026-07-02
  tasks: 3
  files: 3
requirements: [OPS-03]
---

# Phase 4 Plan 01: Retire the Fan-Out Summary

Retired the legacy `stream-relays.sh` fan-out with a clean `git rm` (no stub), rewrote the README "Useful commands" block to document the single `strfry router` ingestion path, and re-verified fresh on the live arm64 host `Gs-Mac-mini` that exactly one `strfry router` runs, zero `strfry stream` processes remain, no `strfry-streams` tmux session lingers, and the 174-relay Web-of-Trust list is unchanged and still driving `strfry-router.config` (identical URL set) — all bound to captured host evidence in `04-VERIFICATION-EVIDENCE.md`.

## What Was Built

- **Task 1 — deleted `stream-relays.sh`** (`git rm`, commit `3d3c71c`). The 174-relay tmux fan-out script is gone from tracking and the working tree. `gen-router-config.sh` left untouched — both its `stream-relays.sh` provenance comments (lines 7 and 36) preserved (the citation that proves the deletion is safe: the `relay_urls:` parser + `resolve_relays` were already ported verbatim into it).
- **Task 2 — updated `README.md`** (commit `a43581a`). Removed the `./stream-relays.sh start|attach|stop` block and replaced it with the current ingestion story: a single `strfry router` started by dockur's `ROUTER=` entrypoint env in `docker-compose.strfry.yml`, plus `gen-router-config.sh` regenerating `~/deepfry/strfry-router.config` from the WoT `relay_urls:` list. Zero `stream-relays` literals remain; no restart command was added (D-05); the whitelist HTTP API and quarantine-rescue sibling blocks are intact.
- **Task 3 — live-host re-verification + `04-VERIFICATION-EVIDENCE.md`** (commit `baf87e8`). Read-only checks over the live SSH ControlMaster (`/tmp/ssh-deepfry-host.sock`, `g@192.168.149.21`) captured fresh proof and bound each of the 3 Phase 4 ROADMAP success criteria to actual host output.

## Verification Results

Phase-level checks (all pass):

- `git ls-files stream-relays.sh` → empty (deleted); working-tree file absent.
- Dangling-reference gate over tracked operational files (excl. `.planning/` + `gen-router-config.sh` provenance) → **0**.
- `gen-router-config.sh` not in the plan diff; `grep -c 'stream-relays.sh'` = **2** (both provenance comments intact).
- `grep -c 'stream-relays' README.md` → **0**; README still contains `gen-router-config.sh`, `ROUTER`, `docker-compose.strfry.yml`.
- `04-VERIFICATION-EVIDENCE.md` exists with an Observable Truths table binding the 3 SCs.

Live-host evidence (Gs-Mac-mini, arm64):

- `docker exec strfry pgrep -f 'strfry router' | wc -l` = **1**; `'strfry stream' | wc -l` = **0**. Process listing shows only `bash /app/strfry.sh`, `./strfry relay`, `./strfry router …`, and two `/app/plugins/bloom` gate procs.
- tmux **not installed** on the host → the `strfry-streams` fan-out session cannot exist (equivalent proof no session lingers).
- `~/deepfry/web-of-trust.yaml` present (unchanged, dated 28 Jun), `relay_urls:` = **174** items; `~/deepfry/strfry-router.config` = **174** `wss://` entries; sorted-set `diff` = **empty (IDENTICAL SET)**.
- Container `StartedAt` = 06:55:19Z (unchanged since the Phase 2 recreate), `RestartCount=0`, healthy, `curl 7777` = 200 — no restart performed (D-05).

## Deviations from Plan

None — plan executed exactly as written. No orphan `strfry stream` process and no `strfry-streams` tmux session were found (D-06 orphan-stop not needed); the WoT relay list was intact (no blocker). No package-manager installs. No StrFry service change; no `~/deepfry/` file modified.

## Known Stubs

None. `stream-relays.sh` was deleted (not stubbed) by design (D-01).

## Threat Flags

None. No new network endpoint, auth path, file-access pattern, or schema change was introduced — this plan deletes a script, edits README prose, and performs read-only host verification. The threat register's `mitigate` dispositions were honored: T-04-01 (WoT config read-only, confirmed unchanged), T-04-02 (no strfry restart; no orphan-stop needed), T-04-03 (clean `git rm` + dangling-reference gate = 0), T-04-04 (evidence bound to captured raw host output).

## Commits

- `3d3c71c` — feat(04-01): git rm stream-relays.sh (retire the fan-out script)
- `a43581a` — docs(04-01): document the router ingestion path in README (D-02)
- `baf87e8` — docs(04-01): capture fresh post-retirement host verification evidence

## Self-Check: PASSED

- `04-VERIFICATION-EVIDENCE.md` exists (FOUND).
- `README.md` modified and tracked (FOUND).
- `stream-relays.sh` deleted (git ls-files empty, working tree absent — confirmed).
- Commits `3d3c71c`, `a43581a`, `baf87e8` exist in git log (confirmed).
