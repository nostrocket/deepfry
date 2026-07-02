# Phase 2: Wire & Prove Config Loads - Context

**Gathered:** 2026-07-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Wire the Phase-1-generated **streams-only router config** through dockur's
`ROUTER=` entrypoint env var in `docker-compose.strfry.yml`, and **prove on the
live arm64 host** that the config actually loads on `dockurr/strfry 1.1.0`
(commit `f31a1b9`) — breaking the `260701-l5s` blocker where the positional
router config never loaded.

**"Proof" for this phase = these log lines + process count on the host:**
`Loading router config file:` → `New stream group [ingest]` →
`ingest: Connecting to wss://...`, with **exactly one** `strfry router` process
and the LMDB unmoved.

This phase is **wiring + config-load proof only**. NOT in scope:
- Live bloom `decision=accept/reject` gating flow and `MDB_BAD_RSLOT` stability
  under sustained load → **Phase 3**.
- Retiring `stream-relays.sh` → **Phase 4** (this phase only *stops* the fan-out
  temporarily during the proof; the script file is left untouched).

</domain>

<decisions>
## Implementation Decisions

### Router placement (OPS-01, ING-01)
- **D-01:** The router runs in the **same container** as the canonical relay,
  via dockur's default `strfry.sh` entrypoint. Set `ROUTER=/app/strfry-router.config`
  on the existing `strfry` service; the entrypoint runs `./strfry relay &` +
  `./strfry router "$ROUTER" &` together. Do NOT add a separate router service.
  Matches the roadmap SC wording ("`ROUTER=` env var", "exactly one `strfry
  router` process") and the source-verified research recipe.
- **D-02:** Bind-mount the host-generated config into the container at the path
  `ROUTER=` points to: host `~/deepfry/strfry-router.config` →
  container `/app/strfry-router.config`. This is the container path locked in
  Phase 1 (D-08). Keep `db`/`relay` OUT of this file (Phase 1 D-10).
- **D-03:** Do NOT use `ROUTER=1`/`ROUTER=yes` (that loads dockur's baked-in
  `/etc/strfry-router.conf` sample `friends{}` stream). Point `ROUTER=` at our
  generated file explicitly.

### De-risk strategy — the ROUTER= path is a LEAD, not a confirmed fix
- **D-04:** **Read dockur's `strfry.sh` entrypoint + Dockerfile at the pinned
  commit FIRST**, before touching compose. Confirm exactly how `ROUTER=` invokes
  the router (cwd, quoting, whether it passes the value as the positional
  `<routerConfigFile>`, and whether the global `--config`/DB path resolves the
  same way). Get the wiring provably right on paper, then wire it once.
- **D-05:** Fallback candidates are considered **only if the wired `ROUTER=`
  path still fails to load on the host** (`Loading router config file:` never
  appears). Do not build fallbacks pre-emptively. Candidate ladder to enumerate
  in the plan for that contingency: (a) explicit `command:`/entrypoint override
  invoking `strfry --config=/etc/strfry.conf router /app/strfry-router.config`
  directly with correct cwd; (b) a dedicated router-only service sharing the
  same LMDB bind-mount + `/etc/strfry.conf`.

### Proof execution — on the live host (blocker is build-specific)
- **D-06:** The **executor SSHes to the deploy host** (Apple Silicon Mac mini,
  arm64, repo `~/git/deepfryupstream/deepfry`), applies the compose change,
  brings up the `strfry` service, and greps the logs for the required lines.
  Autonomous end-to-end proof — the blocker could only be reproduced on this
  build/arch, so a local/CI check cannot substitute.
- **D-07:** **Record the evidence** (startup log excerpt showing the three
  required lines, the single-router process listing, relay still healthy) into
  a phase artifact before considering the phase proven — the verify-on-host
  discipline that correctly blocked `260701-l5s`.

### Proof procedure — prerequisites & fan-out handling
- **D-08:** **Prerequisite:** run `gen-router-config.sh` on the host FIRST to
  produce `~/deepfry/strfry-router.config` from the current WoT relay list. The
  config is a runtime artifact (not committed, Phase 1 D-07); it must exist on
  the host before `docker compose up`.
- **D-09:** **Stop the `stream-relays.sh` fan-out during the proof.** The
  fan-out runs as the host tmux session `strfry-streams`, which `docker exec`s
  `strfry stream <relay>` procs INTO the `strfry` container. Kill that tmux
  session (and its in-container exec procs) so the proof can observe "exactly
  one `strfry router` process" cleanly and without reader-slot noise. The
  **script file `stream-relays.sh` is NOT modified or retired** here — that is
  Phase 4. Recreating the `strfry` container for the new `ROUTER=` env also
  tears down those docker-exec'd stream procs.

### Claude's Discretion
- Exact compose diff shape, whether the router mount is `:ro`, the log-grep /
  process-check commands, and the format/location of the recorded evidence
  artifact — planner/executor's call, consistent with the existing compose file
  and repo conventions.
- The precise SSH/deploy sequence (git pull vs direct edit on host) — but see
  the `git pull` bind-mount-truncation gotcha in Code Insights.

### Folded Todos
- **`2026-07-01-run-strfry-router-via-dockur-entrypoint.md`** (`resolves_phase: 2`,
  area `deploy`) — "Run strfry router via dockur `ROUTER=` entrypoint (replace
  174-stream fan-out)". Reviewed-but-deferred in Phase 1 because its scope is the
  entrypoint wiring, not config generation. It **is** this phase. Its solution
  steps (read dockur `strfry.sh`; standalone streams-only file with mandatory
  `pluginDown`; wire via `ROUTER=`; verify on host) are fully absorbed into
  D-01…D-09 above.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### The blocker + source-verified entrypoint/config mechanism (read FIRST)
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-RESEARCH.md`
  — **Read the bottom `CORRECTION` + "Using dockur's built-in entrypoint"
  subsections first.** Source-verified (`f31a1b9` + dockur/strfry + hoytech/golpe):
  the two-files separation, the `Loading router config file:` distinct log line,
  the exact healthy-startup log sequence to grep for, the `ROUTER=` env wiring
  (`./strfry relay &` + `./strfry router "$ROUTER" &`), and why `STREAMS=` is the
  wrong (legacy fan-out) path. Also the version analysis proving an image update
  cannot fix the blocker.
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-PLAN.md`
  — the ~9-variant host investigation and the config-load BLOCKER details; the
  "Recommended next steps" (read dockur `cmd_router.cpp`/`strfry.sh`; try the
  `ROUTER=` env). This is the failure this phase must break.
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-SUMMARY.md`
  — BLOCKED outcome; "state left clean" notes (maxreaders=4096 mitigation remains
  deployed).

### dockur image source (READ at execution — D-04)
- `github.com/dockur/strfry` `strfry.sh` (entrypoint: `ROUTER=`/`STREAMS=` env
  wiring) + `Dockerfile` (`COPY . .` fork build) + `src/apps/mesh/cmd_router.cpp`
  (positional `router <routerConfigFile>`, `reconcileConfig()` reads
  `routerConfig.at("streams")`) + shipped `strfry-router.conf` sample. This is
  the ACTUAL code compiled into `dockurr/strfry:1.1.0`.

### Phase 1 output & context
- `.planning/phases/01-generate-router-config/01-CONTEXT.md` — Phase 1 decisions,
  esp. D-08 (host `~/deepfry/strfry-router.config` → container
  `/app/strfry-router.config`), D-10 (standalone streams-only, no `db`/`relay`),
  D-13 (`pluginDown` mandatory).
- `gen-router-config.sh` — the Phase 1 generator run as the on-host prerequisite
  (D-08).

### Files this phase touches / references
- `docker-compose.strfry.yml` — the `strfry` service where `ROUTER=` env +
  router bind-mount are added (D-01/D-02). Currently uses the dockur default
  entrypoint (no override), which is what makes `ROUTER=` viable.
- `stream-relays.sh` — the fan-out to STOP (not retire) during the proof (D-09);
  tmux session `strfry-streams`, `docker exec strfry /app/strfry stream <relay>`.
- `config/strfry/strfry.conf` — the golpe global `--config` (supplies `db`);
  the router file must NOT duplicate `db`.

### Project rules
- `CLAUDE.md` — DB-location HARD RULE (never move the LMDB / no named volume);
  StrFry stays stock (extend via config/plugins/entrypoint only);
  `~/deepfry/` config-dir protection; project-boundary rule.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `gen-router-config.sh` (Phase 1): produces `~/deepfry/strfry-router.config`
  on the host from the WoT `relay_urls:` list. Run it as the proof prerequisite.
- `docker-compose.strfry.yml` `strfry` service: no custom `entrypoint`/`command`
  override, so it already runs dockur's `strfry.sh` — adding `ROUTER=` env +
  a router bind-mount is a minimal, additive change.

### Established Patterns
- Host-run operator scripts execute on the arm64 Mac mini deploy host with
  `docker`/`docker exec` access. The proof runs there via SSH (D-06).
- The `strfry-quarantine` service shows the pattern for overriding
  entrypoint/command + healthcheck if a fallback (D-05a) ever needs a custom
  invocation — mirror its `command: ["/app/strfry", "--config=/etc/strfry.conf", ...]`
  shape.
- Config bind-mounts are single-file mounts (e.g. `strfry.conf:ro`).

### Integration Points
- `ROUTER=/app/strfry-router.config` → dockur `strfry.sh` → `./strfry router
  "$ROUTER"` (same container as `./strfry relay`).
- Router bind-mount: host `~/deepfry/strfry-router.config` → container
  `/app/strfry-router.config`.
- Fan-out to stop: host tmux session `strfry-streams` → in-container
  `docker exec` `strfry stream` procs (D-09).

### Gotchas
- **`git pull` on the deploy host truncates single-file config bind-mounts** →
  recreate the container to fix (recorded from prior strfry-host deploys). Relevant
  when the compose/config change reaches the host via git pull.
- **Bloom `sourceType:"stream"`** (LOW-confidence flag from RESEARCH.md): the
  `/app/plugins/bloom` plugin receives router-down events with
  `sourceType:"stream"` (relay events are `"client"`). This only matters for the
  *gating* proof (Phase 3), NOT Phase 2's config-load proof — but note it so it
  isn't a surprise: if the plugin special-cases `sourceType`, streams would still
  connect (Phase 2 passes) yet store nothing.

</code_context>

<specifics>
## Specific Ideas

- The proof is deliberately a **log-line + process-count** check, not an
  events-stored check — matching the roadmap's Phase 2 success criteria. The
  goal is narrowly "the config demonstrably loads and streams activate," leaving
  live gating and stability to Phase 3.
- Treat this as the project's **highest-unknown de-risk**: read the actual
  entrypoint source before wiring (D-04) so the single host attempt is
  well-founded, rather than adding another failed variant to the `260701-l5s`
  pile.

</specifics>

<deferred>
## Deferred Ideas

- **Live bloom `decision=accept/reject` gating flow + `MDB_BAD_RSLOT`
  stability** under the single-router process → Phase 3.
- **Retiring / rewriting `stream-relays.sh`** and ensuring no orphaned
  `strfry stream` procs remain → Phase 4. (Phase 2 only stops the fan-out
  temporarily.)
- **Hot-reload-driven relay add/remove** (rewrite the config live, no restart)
  — an ops capability beyond the load proof.

### Reviewed Todos (not folded)
None — the single matched todo (`resolves_phase: 2`) was folded into scope above.

</deferred>

---

*Phase: 2-wire-prove-config-loads*
*Context gathered: 2026-07-02*
