# Phase 2: Wire & Prove Config Loads - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-02
**Phase:** 2-wire-prove-config-loads
**Areas discussed:** Router placement, Fallback plan, Proof execution, Old fan-out handling

---

## Router placement

| Option | Description | Selected |
|--------|-------------|----------|
| Same container (ROUTER= env) | dockur default entrypoint on existing `strfry` service; `ROUTER=/app/strfry-router.config`; strfry.sh runs relay & router together. | ✓ |
| Separate strfry-router service | Dedicated compose service running only the router, sharing LMDB bind-mount + strfry.conf. | |

**User's choice:** Same container (ROUTER= env)
**Notes:** Matches roadmap SC wording ("`ROUTER=` env var", "exactly one `strfry router` process") and the source-verified research recipe. Separate service kept as a fallback candidate only (D-05).

---

## Fallback plan (ROUTER= is an untested lead)

| Option | Description | Selected |
|--------|-------------|----------|
| Read strfry.sh first, then wire | Read dockur's strfry.sh + Dockerfile @ pinned commit to confirm invocation before touching compose; fallbacks only if the wired path fails. | ✓ |
| Wire ROUTER=, fallbacks staged | Wire and try; if it fails, fall through a pre-enumerated ladder (command override → dedicated service). | |
| Both: read source, then staged ladder | Read strfry.sh AND pre-enumerate the fallback ladder. | |

**User's choice:** Read strfry.sh first, then wire
**Notes:** De-risk on paper first (get the single host attempt right). Fallback ladder is enumerated in CONTEXT (D-05) but only invoked if the wired `ROUTER=` path still fails to load on the host.

---

## Proof execution (must be on the live arm64 host)

| Option | Description | Selected |
|--------|-------------|----------|
| Executor SSHes to host | Executor SSHes to the arm64 Mac mini, applies compose change, brings up strfry, greps logs for the required lines. Autonomous. | ✓ |
| Operator runs, pastes logs | Executor lands the change; operator runs up + log capture on the host and pastes back. | |
| Decide at plan time | Leave the mechanism to planner/executor based on available access. | |

**User's choice:** Executor SSHes to host
**Notes:** Blocker was build/arch-specific (dockurr/strfry 1.1.0 arm64), so a local/CI check cannot substitute — proof must run on the deploy host. Evidence recorded into a phase artifact (D-07).

---

## Old fan-out during the proof

| Option | Description | Selected |
|--------|-------------|----------|
| Stop fan-out during proof | Stop the running `strfry stream` fan-out so the proof observes "exactly one router process" cleanly; script file left untouched (Phase 4 retires it). | ✓ |
| Leave fan-out running | Prove the router alongside the live fan-out; realistic but muddies the single-process SC and risks reader-slot contention. | |

**User's choice:** Stop fan-out during proof
**Notes:** The fan-out is the host tmux session `strfry-streams` spawning in-container `docker exec ... strfry stream` procs. Kill the session for the proof; do NOT modify/retire `stream-relays.sh` (Phase 4).

---

## Claude's Discretion

- Exact compose diff shape, router mount `:ro` flag, log-grep / process-check commands, and the evidence-artifact format/location.
- The precise SSH/deploy sequence (git pull vs direct host edit) — subject to the noted `git pull` bind-mount-truncation gotcha.

## Deferred Ideas

- Live bloom `decision=accept/reject` gating flow + `MDB_BAD_RSLOT` stability → Phase 3.
- Retiring/rewriting `stream-relays.sh` and orphaned-process cleanup → Phase 4.
- Hot-reload-driven relay add/remove (live config rewrite, no restart).
