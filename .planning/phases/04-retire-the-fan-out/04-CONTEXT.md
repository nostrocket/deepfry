# Phase 4: Retire the Fan-Out - Context

**Gathered:** 2026-07-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Retire the legacy `stream-relays.sh` fan-out now that the single-`strfry router`
path is proven (Phases 1–3). This is the final cutover-cleanup phase:

1. `stream-relays.sh` is **retired** so it can no longer spawn the 174-process
   per-relay `strfry stream` fan-out.
2. **No orphaned `strfry stream` processes** remain on the live host — only the
   single `strfry router` (re-verified fresh, post-retirement).
3. The **Web-of-Trust relay list stays intact and unchanged** as the single
   source of truth driving the router config.

The router has been live and fan-out-free on the host since the Phase 2 recreate
(fan-out stopped, never restored — confirmed again in Phase 3). So the *host*
process retirement is effectively already done; Phase 4 makes the **repo** match
that reality (removes the resurrection path) and **re-confirms** it on the host.

**NOT in scope:**
- Any StrFry config / image / compose change — the router path is frozen as
  shipped in Phase 2/3. This phase does not touch `docker-compose.strfry.yml`,
  `strfry-router.config`, or the bloom plugin.
- Editing the WoT relay list or `gen-router-config.sh` logic (must stay intact).
- Hot-reload / live relay add-remove (deferred, out of scope).

</domain>

<decisions>
## Implementation Decisions

### Retirement approach — DELETE the script (SC #1, OPS-03)
- **D-01:** `git rm stream-relays.sh` — **delete it entirely**, not a no-op stub
  or redirect. Rationale: the router config is now the sole ingestion path, and
  the script's only non-trivial logic (the `relay_urls:` YAML parser +
  `resolve_relays` fallback) was **already ported verbatim into
  `gen-router-config.sh`** (see its header comment: "Ported verbatim from
  stream-relays.sh:37-63"). Nothing is lost by deleting; a stub would only invite
  someone to resurrect the fan-out. Clean removal is the strongest guarantee that
  the 174-`strfry stream` path cannot be respawned.
- **D-02:** Update **README.md** in the same change: remove the
  `./stream-relays.sh start|attach|stop` block from the "Useful commands"
  section and replace it with how ingestion actually runs now — the single
  `strfry router` via `ROUTER=` in `docker-compose.strfry.yml`, with
  `gen-router-config.sh` regenerating the config from the WoT relay list. No
  dangling references to `stream-relays.sh` may remain in tracked files (except
  historical `.planning/` artifacts, which are records and stay as-is).

### On-host re-verification — fresh proof after retirement (SC #2, SC #3, OPS-03)
- **D-03:** **Re-verify on the live host** rather than only citing Phase 3. After
  the retirement change, capture fresh evidence on `Gs-Mac-mini`:
  - **exactly ONE `strfry router` process** running, and
  - **ZERO `strfry stream` processes** (no orphans, no tmux `strfry-streams`
    session lingering), and
  - the **WoT relay list unchanged** — `~/deepfry/web-of-trust.yaml`'s
    `relay_urls:` still present and still the source the router config is built
    from (spot-check the live `strfry-router.config` still reflects that list).
- **D-04:** **Record evidence** into a phase artifact before "verified" — mirror
  the Phase 2/3 pattern (`02-VERIFICATION-EVIDENCE.md` /
  `03-VERIFICATION-EVIDENCE.md`): bind each Phase 4 ROADMAP success criterion to
  actual captured host output (process listing, tmux session check, relay-list
  diff/grep), not paraphrase.
- **D-05:** This phase makes **no StrFry service change** — the router keeps
  running untouched. Retirement is a repo edit (delete script + README) plus a
  read-only host verification. Do NOT restart, recreate, or reconfigure strfry as
  part of retirement; the live router state proven in Phase 3 must be preserved.

### Failure handling
- **D-06:** If the host re-check finds any orphaned `strfry stream` process or a
  lingering `strfry-streams` tmux session, **stop it as part of cutover** and
  record it; if the WoT relay list is found altered/missing, that is a **blocker**
  — surface it, do not "fix" it by regenerating from fallbacks.

### Claude's Discretion
- Exact `git rm` + commit sequencing; the precise README wording for the new
  router "useful commands" block; the exact `ps`/`pgrep`/`tmux ls`/`docker exec`
  commands and grep tallies used for the host check; the evidence artifact's
  filename and layout (follow the `03-VERIFICATION-EVIDENCE.md` shape); the
  SSH/deploy sequence to the host.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase 4 scope + requirements
- `.planning/ROADMAP.md` — Phase 4 section (the 3 success criteria this phase
  must bind to captured host evidence).
- `.planning/REQUIREMENTS.md` — **OPS-03** (`stream-relays.sh` retired after the
  router path is verified, no orphaned fan-out, WoT relay list intact as source
  of truth).

### The thing being retired + where its logic already lives
- `stream-relays.sh` — the script to delete (174-relay tmux fan-out of
  `docker exec strfry ./strfry stream <relay>`).
- `gen-router-config.sh` — **already contains the ported `relay_urls:` parser +
  fallback** (header: "Ported verbatim from stream-relays.sh:37-63"). This is why
  deletion loses nothing. Must stay intact — it is the WoT-list → router-config
  path (single source of truth).
- `README.md` — "Useful commands" section (~lines 148–156) carries the
  `./stream-relays.sh start|attach|stop` block to remove/replace.

### Prior-phase proof this phase builds on (read FIRST)
- `.planning/phases/03-verify-gating-stability-on-host/03-VERIFICATION-EVIDENCE.md`
  — SC#4 already recorded "1 `strfry router` / 0 `strfry stream` procs; fan-out
  not restored; LMDB unmoved." Phase 4 re-confirms this fresh, post-retirement.
- `.planning/phases/02-wire-prove-config-loads/02-VERIFICATION-EVIDENCE.md` — the
  evidence-artifact format to mirror (D-04); the live SSH ControlMaster access
  details to the deploy host.
- `.planning/phases/02-wire-prove-config-loads/02-CONTEXT.md` — the `git pull`
  bind-mount-truncation → recreate gotcha (only relevant if host-side files
  change; retirement itself changes no bind-mounted config).

### Project rules
- `CLAUDE.md` — DB-location HARD RULE (never move the LMDB / no named volume);
  StrFry stays stock; `~/deepfry/` config-dir protection (never delete/overwrite
  `~/deepfry/web-of-trust.yaml`); project-boundary rule.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `03-VERIFICATION-EVIDENCE.md` / `02-VERIFICATION-EVIDENCE.md` — the
  template/shape for Phase 4's evidence artifact (per-SC observable-truths table
  bound to captured host output).
- Live SSH ControlMaster to the deploy host (`g@192.168.149.21`, `Gs-Mac-mini`
  arm64) — the same access Phases 2/3 used for autonomous on-host proof.

### Established Patterns
- Host-run operator verification over SSH: `docker exec strfry pgrep/ps`,
  `ps -ax | grep`, `tmux ls`, grep tallies.
- Repo hygiene: retirement is a tracked-file change (delete + README), separate
  from the untouched live StrFry service.

### Integration Points
- `stream-relays.sh` → `docker exec strfry ./strfry stream <relay>` in a tmux
  session `strfry-streams` (the fan-out being retired).
- `gen-router-config.sh` → `~/deepfry/strfry-router.config` built from
  `~/deepfry/web-of-trust.yaml` `relay_urls:` — the surviving single source of
  truth.

### Gotchas
- **Do NOT delete or regenerate `~/deepfry/web-of-trust.yaml`** — it is the
  protected host config that IS the source of truth; the phase confirms it
  unchanged, never rewrites it.
- **No strfry restart/recreate** for retirement — preserve the live router state
  proven in Phase 3 (D-05).
- Historical `.planning/` references to `stream-relays.sh` are records — leave
  them; only remove references from operational tracked files (README, scripts).

</code_context>

<specifics>
## Specific Ideas

- The host is already fan-out-free (fan-out stopped in Phase 2, confirmed in
  Phase 3). Phase 4's host task is a **re-confirmation**, expected to pass
  immediately — the real deliverable is closing the resurrection path in the repo
  (delete the script) and recording fresh evidence.
- `gen-router-config.sh:36` comment is the proof that deletion is safe — cite it
  in the plan's rationale.

</specifics>

<deferred>
## Deferred Ideas

- **Hot-reload-driven relay add/remove** (rewrite the router config live, no
  restart) — an ops capability beyond this conversion; out of scope.

### Reviewed Todos (not folded)
None.

</deferred>

---

*Phase: 4-retire-the-fan-out*
*Context gathered: 2026-07-02*
