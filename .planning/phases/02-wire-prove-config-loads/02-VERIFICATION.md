---
phase: 02-wire-prove-config-loads
verified: 2026-07-02T00:00:00Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
re_verification:
  previous_status: none
  note: "Initial goal-backward verification (02-VERIFICATION-EVIDENCE.md is the plan-03 executor's self-recorded host proof, not a prior verifier pass)"
review_findings_assessed:
  - id: CR-01
    disposition: "Portability follow-up, NOT a phase-goal failure. The compose `~` default is version/host-dependent fragility; on the live deploy host and on this verifier's Docker Compose it resolves to a real host bind-mount (type: bind, source /Users/g/deepfry/... locally; /Volumes/... on the deploy host per captured evidence). SC #1 (ROUTER= wiring present + parses) and SC #2-5 (config demonstrably loads on the live host) are both met. Recommend fixing before OPS-02/OPS-03 cutover (Phase 3/4)."
---

# Phase 2: Wire & Prove Config Loads — Verification Report

**Phase Goal:** The router starts with the stack via docker-compose.strfry.yml through dockur's ROUTER= entrypoint, and its config demonstrably loads on dockurr/strfry 1.1.0 — breaking the 260701-l5s blocker where `docker exec ... router <file>` never loaded the positional config.
**Verified:** 2026-07-02
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth (ROADMAP SC) | Status | Evidence |
| --- | --- | --- | --- |
| 1 | docker-compose.strfry.yml sets ROUTER= pointing at the bind-mounted router config (entrypoint path `./strfry router "$ROUTER"`, not raw docker exec) | ✓ VERIFIED | `docker-compose.strfry.yml:26` `ROUTER: /app/strfry-router.config` on the strfry service; matching `:ro` single-file bind-mount at line 33 (`${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro`); NO entrypoint/command override and NO separate router service on `strfry` (grep negative for `ROUTER: 1|yes` — none); the service keeps dockur's `strfry.sh`. `02-ENTRYPOINT-WIRING.md` source-cites `strfry.sh` v1.1.0 lines 52-54 `[ -f "$ROUTER" ]; then ./strfry router "$ROUTER" &`. `docker compose config` parses OK. |
| 2 | `Loading router config file:` appears in strfry logs (the line that NEVER appeared in 260701-l5s) | ✓ VERIFIED (host evidence) | `02-VERIFICATION-EVIDENCE.md`: captured `docker logs strfry` — `2026-07-02 06:55:21.545 ( 0.078s) [main thread]INFO\| Loading router config file: /app/strfry-router.config` (count = 1); `Failed to parse router config` count = 0. Arch-specific (arm64) runtime truth; correctly proven via captured host output, not local reproduction. |
| 3 | `New stream group [ingest]` and `Connecting to wss://...` follow (streams activated from generated config) | ✓ VERIFIED (host evidence) | `02-VERIFICATION-EVIDENCE.md`: sequential at boot `06:55:21.550 New stream group [ingest]` then `ingest: Connecting to wss://cfrelay.haorendashutest.workers.dev/...` (Connecting = 174, Connected = 162). Real log excerpt embedded, not paraphrased. |
| 4 | Exactly one `strfry router` process runs (not the 174-process strfry stream fan-out) | ✓ VERIFIED (host evidence) | `02-VERIFICATION-EVIDENCE.md`: `docker exec strfry pgrep -f 'strfry router' \| wc -l` = 1; `'strfry stream'` = 0 (baseline was 144). Full `ps` listing: PID 1 `bash /app/strfry.sh`, PID 7 `./strfry relay`, PID 29 `./strfry router /app/strfry-router.config` — nothing else. |
| 5 | The LMDB stays at its host bind-mount path — no named-volume migration, no DB relocation (HARD RULE) | ✓ VERIFIED (host evidence) | `02-VERIFICATION-EVIDENCE.md`: `docker inspect strfry` → `Type=bind Source=/Volumes/BACKUP/nostr/strfry_database Dest=/app/strfry-db` (unchanged from baseline); `docker volume ls` owns no strfry-db volume; relay `http_code=200`, `Health=healthy` on 7777 before and after recreate. Compose LMDB mount line (`docker-compose.strfry.yml:28`) untouched by the phase diff. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `.planning/phases/02-wire-prove-config-loads/02-ENTRYPOINT-WIRING.md` | Source-cited ROUTER= wiring confirmation (min 30 lines, contains ROUTER) | ✓ VERIFIED | 169 lines; cites strfry.sh (v1.1.0), Dockerfile, cmd_router.cpp (hoytech f31a1b9) by github.com ref + line numbers; confirms positional `router "$ROUTER"` from cwd /app; records D-05 fallback trigger. |
| `docker-compose.strfry.yml` | ROUTER env + router-config :ro bind-mount on strfry service (contains ROUTER) | ✓ VERIFIED | ROUTER at line 26, mount at line 33, both target `/app/strfry-router.config`; additive diff (+11/-0); quarantine service + LMDB mount untouched. |
| `.planning/phases/02-wire-prove-config-loads/02-VERIFICATION-EVIDENCE.md` | Recorded host proof w/ Observable Truths table (min 40 lines, contains "Loading router config file:") | ✓ VERIFIED | 147 lines; frontmatter + Observable Truths table binding all 5 SCs to captured log excerpts, process listing, and mount-source line. |
| `gen-router-config.sh` (Phase 1 dependency, run on host in plan 03) | Streams-only router config generator producing ~/deepfry/strfry-router.config | ✓ VERIFIED (present) | Exists at repo root (executable, 7339 bytes); referenced by plan-03 key_link; produced the 174-relay config the host proof loaded. Owned by Phase 1. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| docker-compose.strfry.yml | 02-ENTRYPOINT-WIRING.md | `ROUTER: /app/strfry-router.config` | ✓ WIRED | Env var name + container path in compose exactly match the confirmed invocation in the wiring artifact. |
| docker-compose.strfry.yml | config/strfry/strfry.conf | db lives only in /etc/strfry.conf, not the router mount | ✓ WIRED | Router mount is streams-only :ro; no `db` env/line added; `/etc/strfry.conf` mount (line 29) supplies db per golpe search order. |
| 02-VERIFICATION-EVIDENCE.md | docker-compose.strfry.yml | ROUTER= wiring produces `Loading router config file:` | ✓ WIRED | Captured host log line names the exact container path from compose line 26/33. |
| 02-VERIFICATION-EVIDENCE.md | gen-router-config.sh | generator run on host produces the bind-mounted config | ✓ WIRED | Evidence records `gen-router-config.sh` run → 174-relay streams-only config → loaded. |

### Data-Flow Trace (Level 4)

Not applicable — this phase produces infrastructure config + a proof artifact, not a dynamic-data-rendering component. The "data flow" (WoT relay list → generated config → loaded streams) is proven behaviorally by the captured host log showing 174 real `Connecting to wss://` lines from the generated file.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Compose parses with ROUTER wiring | `docker compose -f docker-compose.strfry.yml config` | Parses OK; router mount resolves to `type: bind source: /Users/g/deepfry/strfry-router.config` | ✓ PASS |
| ROUTER not set to 1/yes | `grep -nE 'ROUTER: *(1\|yes...)'` | No match | ✓ PASS |
| LMDB mount untouched | `grep STRFRY_DB_PATH docker-compose.strfry.yml` | `${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db` present, unchanged | ✓ PASS |
| Live config-load (SC #2-5) | arm64 host recreate + `docker logs` | Proven on Gs-Mac-mini — captured in evidence artifact | ? SKIP (arch-specific; verified via captured host evidence, cannot reproduce locally) |

### Probe Execution

No conventional `scripts/*/tests/probe-*.sh` probes declared for this phase. The phase's verification mechanism is the live-host log-line + process-count proof, recorded in `02-VERIFICATION-EVIDENCE.md`. N/A.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| OPS-01 | 02-01, 02-02, 02-03 | Router starts via docker-compose ROUTER= entrypoint; config loads on dockurr/strfry 1.1.0 (`Loading router config file:` appears) | ✓ SATISFIED | ROUTER= wiring present (SC #1); `Loading router config file:` captured on host (SC #2). REQUIREMENTS.md marks [x] Complete. |
| ING-01 | 02-03 | Single `strfry router` process replaces the 174-process fan-out | ✓ SATISFIED | Exactly one router process, zero stream procs, captured (SC #4). REQUIREMENTS.md marks [x] Complete. |

No orphaned requirements — REQUIREMENTS.md maps only OPS-01 and ING-01 to Phase 2, both claimed by plans and both verified.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| gen-router-config.sh | 123 | `mktemp "${ROUTER_CONFIG}.XXXXXX"` | ℹ️ Info | False positive — `XXXXXX` is the mktemp template placeholder, not a debt marker. Not a blocker. |
| docker-compose.strfry.yml | 33 | `~` in `${...:-~/deepfry/...}` default (CR-01) | ⚠️ Warning | Portability fragility for fresh environments where the env var is unset AND the running Compose does not expand `~`. On the deploy host and this verifier's Compose it resolves to a real bind-mount, so the phase goal (config demonstrably loads) is met. Recommend fixing before Phase 3/4 cutover. Does not undermine any verified SC. |

No BLOCKER anti-patterns. No unreferenced TODO/FIXME/TBD/XXX debt markers in phase-modified files.

### Human Verification Required

None. The one runtime-behavior class (SC #2-5, arm64-specific config load) is already backed by captured host evidence embedded verbatim in `02-VERIFICATION-EVIDENCE.md` (real timestamped log excerpts, `docker inspect` output, `ps` listing). This satisfies the behavioral-evidence requirement for the state-transition/activation truths without a further human round.

### CR-01 Assessment (per verification brief)

The code review's CR-01 flags that the compose volume default `${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}` uses a `~` that Docker Compose may not expand when the env var is unset.

**Impact on the PHASE GOAL: none.** The goal is "config demonstrably loads on the live host." That is proven:
- On the live deploy host, `STRFRY_ROUTER_CONFIG_PATH` resolves the mount to the real host file, and `Loading router config file:` appeared (captured SC #2).
- This verifier's `docker compose config` resolves the *bare default* to `type: bind, source: /Users/g/deepfry/strfry-router.config` — a real host bind-mount, not a Docker-managed volume.

The review itself hedges ("Depending on Compose version it either (a) creates a named volume, or (b) errors"). It is a real portability follow-up for fresh/other-version environments and should be fixed (Option A `./data/...` default or Option B `:?` required var) plus documented in `.env.example` (WR-02) before the OPS-02/OPS-03 cutover in Phases 3-4. But it is an advisory code-review finding that the captured evidence has already superseded for the phase goal. Per the brief, it is NOT treated as a phase-goal failure.

### Gaps Summary

No gaps blocking goal achievement. All five ROADMAP success criteria are met: SC #1 is verified directly in the committed compose file (ROUTER= entrypoint wiring, not raw docker exec), and SC #2-5 are verified via captured live-arm64-host evidence (config loads, streams activate, single router process, LMDB unmoved). Both phase requirements (OPS-01, ING-01) are satisfied. The 260701-l5s blocker is broken — `Loading router config file:` now appears where it never did before.

The one open item (CR-01, tilde default portability + WR-02 undocumented env vars) is a pre-cutover hardening follow-up for Phase 3/4, not a Phase 2 goal failure — the live host and this verifier's Compose both resolve the mount to a real host bind-mount.

---

_Verified: 2026-07-02_
_Verifier: Claude (gsd-verifier)_
