---
phase: 04-retire-the-fan-out
verified: 2026-07-02T00:00:00Z
status: passed
score: 6/6 must-haves verified
behavior_unverified: 0
overrides_applied: 0
requirements:
  - id: OPS-03
    status: satisfied
---

# Phase 4: Retire the Fan-Out Verification Report

**Phase Goal:** The legacy `stream-relays.sh` fan-out is retired now that the router path is verified, leaving no orphaned per-relay `strfry stream` processes and the WoT relay list intact as the single source of truth.
**Verified:** 2026-07-02
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

The phase goal decomposes into 3 ROADMAP success criteria (SC #1 repo retirement, SC #2 no orphaned host procs, SC #3 WoT list intact). The repo-side truths (SC #1, key links, prohibitions) were verified directly against the working tree and git history. The live-host truths (SC #2, SC #3) are verified via the captured `04-VERIFICATION-EVIDENCE.md` artifact — the host is external and this verifier does not SSH; per the plan design, the artifact is the evidence sink. The artifact was checked for binding to *real captured output* (process listing with PIDs, `ls -l` with byte counts/dates, a 174-vs-174 set diff) rather than paraphrase, and its `StartedAt=06:55:19Z` was cross-checked against Phase 3's evidence and matches — corroborating the no-restart claim and that the number is a captured value, not fabricated.

### Observable Truths

| # | Truth | Status | Evidence |
| --- | ------- | ---------- | -------------- |
| 1 | `stream-relays.sh` deleted from tracking (SC #1, D-01) | ✓ VERIFIED | `git ls-files stream-relays.sh` → empty; `test -e stream-relays.sh` → NO. Commit `3d3c71c` removes 145 lines (whole file), not a stub. |
| 2 | README documents the router ingestion path, zero `stream-relays` literals (SC #1, D-02) | ✓ VERIFIED | `grep -c 'stream-relays' README.md` = **0**. README "Useful commands" (lines 147–172) documents single `strfry router` + `ROUTER=` env in `docker-compose.strfry.yml` + `./gen-router-config.sh`; sibling whitelist-API and quarantine-rescue blocks intact; no restart command added. |
| 3 | `gen-router-config.sh` unchanged, both provenance comments intact (CFG-01, D-01) | ✓ VERIFIED | `grep -c 'stream-relays.sh' gen-router-config.sh` = **2** (lines 7, 36). Not in this phase's diff (last touched commits `87932ff`/`a45aaed`/`5519680`, all Phase 1). |
| 4 | Dangling-reference gate = 0 (no tracked operational file can respawn the fan-out; prohibition) | ✓ VERIFIED | `git ls-files \| grep -vE '^\.planning/\|^gen-router-config\.sh$' \| xargs grep -l 'stream-relays\.sh' \| wc -l` = **0**. Remaining refs are only `.planning/` history (records) + `gen-router-config.sh` provenance (both allowed). |
| 5 | No orphaned `strfry stream` procs on host — only 1 `strfry router`, no `strfry-streams` tmux session (SC #2) | ✓ VERIFIED (evidence artifact) | `04-VERIFICATION-EVIDENCE.md`: `pgrep -f 'strfry router' \| wc -l` = **1**, `'strfry stream' \| wc -l` = **0**; `ps -o pid,args` shows PID 1 bash, 7 relay, 29 router, two bloom — no stream. tmux not installed → no `strfry-streams` session possible. |
| 6 | WoT relay list unchanged & still driving `strfry-router.config` as single source of truth (SC #3, CFG-01) | ✓ VERIFIED (evidence artifact) | `04-VERIFICATION-EVIDENCE.md`: `web-of-trust.yaml` (8649 bytes, dated 28 Jun — pre-Phase-2) `relay_urls:` = **174** items; live `strfry-router.config` = **174** `wss://` entries; sorted-set `diff` empty → IDENTICAL SET. Neither file modified/regenerated. |

**Score:** 6/6 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | ----------- | ------ | ------- |
| `.planning/phases/04-retire-the-fan-out/04-VERIFICATION-EVIDENCE.md` | Fresh post-retirement host proof; Observable Truths table binding 3 SCs | ✓ VERIFIED | Exists (created commit `baf87e8`, 204 lines). Contains "Observable Truths" table + raw captured host output (PIDs, byte sizes, 174/174 diff, tmux check, `docker inspect` StartedAt). |
| `README.md` | Router ingestion path documented, no `stream-relays` | ✓ VERIFIED | Modified (commit `a43581a`). |
| `stream-relays.sh` | Deleted (not stubbed) | ✓ VERIFIED | Gone from tracking + working tree (commit `3d3c71c`). |

### Key Link Verification

| From | To | Via | Status | Details |
| ---- | --- | --- | ------ | ------- |
| `gen-router-config.sh` | `~/deepfry/web-of-trust.yaml` `relay_urls:` | `load_relays_from_config` parses `relay_urls:` (the surviving WoT-list → router-config path) | ✓ WIRED | `load_relays_from_config()` at line 37; matches `^relay_urls:` at line 41; called at lines 94, 102. This parser is the reason deleting `stream-relays.sh` loses nothing. Evidence artifact confirms it produces the live 174-relay config (IDENTICAL SET). |

### Prohibitions (must-NOT checks)

| # | Prohibition | Status | Evidence |
| --- | ----------- | ------ | -------- |
| 1 | No tracked operational file references `stream-relays.sh` (excl. `.planning/` + provenance) | ✓ VERIFIED | Dangling-reference gate = 0. |
| 2 | `stream-relays.sh` NOT replaced by a stub/redirect/shim | ✓ VERIFIED | Commit `3d3c71c` = pure 145-line deletion; no new tracked file invokes `strfry stream`. |
| 3 | No strfry restart/recreate/reconfigure (D-05) | ✓ VERIFIED | `StartedAt=06:55:19Z` unchanged from Phase 3, `RestartCount=0`; `baf87e8` touches only the evidence file. |
| 4 | `~/deepfry/web-of-trust.yaml` NOT deleted/overwritten/regenerated | ✓ VERIFIED | Byte size 8649, date 28 Jun (pre-Phase-2) in evidence; read-only checks only. |
| 5 | `gen-router-config.sh` NOT modified; both provenance comments intact | ✓ VERIFIED | Not in phase diff; `grep -c` = 2. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ---------- | ----------- | ------ | -------- |
| OPS-03 | 04-01-PLAN | `stream-relays.sh` retired after router path verified, no orphaned fan-out, WoT list intact as source of truth | ✓ SATISFIED | Truths 1–6 all VERIFIED; REQUIREMENTS.md maps OPS-03 → Phase 4 and no other plan claims it (no orphaned requirements). |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| ---- | ---- | ------- | -------- | ------ |
| `gen-router-config.sh` | 123 | `XXX` inside `mktemp "${ROUTER_CONFIG}.XXXXXX"` | ℹ️ Info | False positive — `mktemp` template placeholder, not a debt marker. Also not modified by this phase. |

No blocker or warning anti-patterns. No unreferenced TBD/FIXME markers in phase-modified files.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Script deleted | `git ls-files stream-relays.sh` | empty | ✓ PASS |
| README literal-free | `grep -c 'stream-relays' README.md` | 0 | ✓ PASS |
| Provenance preserved | `grep -c 'stream-relays.sh' gen-router-config.sh` | 2 | ✓ PASS |
| Resurrection gate | dangling-reference gate | 0 | ✓ PASS |
| Commits exist | `git log --oneline 3d3c71c a43581a baf87e8` | all present | ✓ PASS |

Live-host process/config spot-checks (SC #2, SC #3) are covered by the captured evidence artifact — not re-run here (host is external, plan design routes these to the artifact).

### Gaps Summary

None. The phase goal is achieved. Repo-side retirement is complete and irreversible via tracked files (clean deletion + zero dangling references + README rewritten). The live-host state (1 router / 0 stream, no fan-out session, WoT list unchanged & driving the 174-relay config) is bound to real captured output in `04-VERIFICATION-EVIDENCE.md`, whose `StartedAt` timestamp cross-checks against Phase 3, corroborating that no restart occurred and the evidence is genuine capture. OPS-03 satisfied.

---

_Verified: 2026-07-02_
_Verifier: Claude (gsd-verifier)_
