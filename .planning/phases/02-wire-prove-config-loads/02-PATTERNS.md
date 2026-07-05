# Phase 02: Wire & Prove Config Loads - Pattern Map

**Mapped:** 2026-07-02
**Files analyzed:** 3 (1 modified compose file, 1 new proof/evidence artifact, 1 conditional fallback edit to the same compose file)
**Analogs found:** 3 / 3

## Scope note

This phase is a **docker-compose wiring + on-host config-load proof**, not a code
subsystem. There is exactly one primary file edit (`docker-compose.strfry.yml`),
one produced artifact (the recorded proof/evidence — D-07), and one *conditional*
edit to the same compose file (the D-05 fallback, only if `ROUTER=` fails). The
"analogs" here are therefore other blocks/entries *within the same compose file*
plus the existing host-run operator scripts — the planner copies their exact
shape rather than an abstract convention.

The proof runbook (SSH → apply compose → `docker compose up` → grep logs →
process count) is **operator commands run on the host** (D-06), not a committed
script. There is no evidence the phase produces a new `.sh` file; the runbook +
recorded evidence are the deliverables. Classified below as "runbook / evidence
artifact" with the host-run operator scripts as the command-convention analog.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `docker-compose.strfry.yml` — `strfry` service: add `ROUTER=` env + router bind-mount (D-01/D-02) | config | event-driven (long-running stream router) | `strfry` service block (existing env/volume shape) in same file + `strfry.conf:ro` single-file mount | exact (same file, same service) |
| `docker-compose.strfry.yml` — D-05 **fallback** (conditional): explicit `command:`/`entrypoint` override OR dedicated router-only service | config | event-driven | `strfry-quarantine` service (`entrypoint`+`command`+`healthcheck` override) in same file | exact (same file, adjacent service) |
| Proof runbook + recorded evidence artifact (D-06/D-07) | test / verification | request-response (SSH/docker-exec ops) | `stream-relays.sh` (docker/docker-exec/tmux host conventions) + Phase 1 `01-VERIFICATION.md` (evidence-table shape) | role-match |

## Pattern Assignments

### `docker-compose.strfry.yml` — `strfry` service: add `ROUTER=` + router bind-mount (config, event-driven)

**Analog:** the existing `strfry` service block in the SAME file
(`/Users/g/git/deepfry/docker-compose.strfry.yml`, lines 2–34). It has **no**
`entrypoint`/`command` override, so it already runs dockur's `strfry.sh` — this
is exactly what makes the `ROUTER=` env viable (D-01). The change is purely
additive: one `environment:` entry + one `volumes:` entry.

**Existing env pattern to extend** (lines 18–19):
```yaml
    environment:
      STRFRY_PRIVATE_KEY: ${STRFRY_PRIVATE_KEY:-}
```
Add the router env alongside it. Per RESEARCH.md "Using dockur's built-in
entrypoint" (lines 292–301) the entrypoint runs `./strfry relay &` then
`./strfry router "$ROUTER" &`, so `ROUTER` must point at the container path the
bind-mount lands on:
```yaml
    environment:
      STRFRY_PRIVATE_KEY: ${STRFRY_PRIVATE_KEY:-}
      ROUTER: /app/strfry-router.config
```
Note D-03: do **not** use `ROUTER=1`/`ROUTER=yes` — that loads dockur's baked-in
`/etc/strfry-router.conf` sample `friends{}` stream. Point at the explicit path.

**Existing single-file config bind-mount pattern to copy** (lines 22, 64) —
this is the shape the new router mount must match (host file → container file,
optionally `:ro`):
```yaml
      - ./config/strfry/strfry.conf:/etc/strfry.conf:ro
```
The router mount sources from the host runtime artifact (Phase 1 D-08:
`~/deepfry/strfry-router.config`), not from `./config/...` (the config is
generated on the host, not committed — Phase 1 D-07). Container target is the
Phase 1-locked `/app/strfry-router.config` (D-02), matching `ROUTER=` above:
```yaml
      - ${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro
```
Whether the mount is `:ro` is Claude's Discretion (CONTEXT lines 85–89). The
existing config mounts show both styles: `strfry.conf:ro` (line 22, read-only)
vs `whitelist.yaml`/`router.yaml` (lines 23–24, writable). The router config is
read-only at runtime (strfry only reads it; hot-reload is triggered by the host
rewriting the file), so `:ro` matches the `strfry.conf:ro` precedent. Note the
existing file already uses a `${VAR:-default}` bind-mount source convention
(lines 21, 30, 63) and env-var indirection — reuse it rather than hardcoding the
host path.

**db separation (do NOT duplicate):** `db` is supplied by the global
`--config` file only. From `/Users/g/git/deepfry/config/strfry/strfry.conf`:
```
db = "./strfry-db/"                    # line 6 (global config owns db)
...
writePolicy { plugin = "/app/plugins/bloom" }   # line 140 (client-path gate)
```
The router file must NOT contain `db`/`relay` (Phase 1 D-10 / RESEARCH gotcha 3),
and the client-path `writePolicy` does NOT gate router-down events — that is why
the generated router config carries its own `pluginDown = "/app/plugins/bloom"`
(RESEARCH lines 100–112). Phase 2 does not touch this; it is context so the
planner does not "helpfully" add a `db` line to the mount or env.

**Gotcha to encode in the plan** (CONTEXT lines 184–187): `git pull` on the
deploy host **truncates single-file config bind-mounts** — recreate the
container to fix. Since the `ROUTER=` env change requires a container recreate
anyway (env vars are baked at create time), the recreate that applies the new
env also cures any truncation and tears down the docker-exec'd `strfry stream`
procs (D-09). Sequence the plan so the compose change reaches the host, THEN
`docker compose up -d --force-recreate strfry` (or `up -d` after `down`).

---

### `docker-compose.strfry.yml` — D-05 fallback (conditional) (config, event-driven)

**Analog:** the `strfry-quarantine` service in the SAME file
(`/Users/g/git/deepfry/docker-compose.strfry.yml`, lines 39–71). Build the
fallback ONLY if the wired `ROUTER=` path still fails to load on the host
(`Loading router config file:` never appears — D-05). Do NOT build it
pre-emptively.

**`entrypoint` + `command` override pattern** (lines 45–48) — this is the exact
shape for D-05(a) "explicit `command:` override invoking `strfry --config=... router ...`":
```yaml
    entrypoint: ["/usr/local/bin/quarantine-db-guard.sh"]
    # Absolute path: the dockurr/strfry image keeps the binary at /app/strfry
    # and isn't in PATH, so the guard's `exec "$@"` would otherwise fail.
    command: ["/app/strfry", "--config=/etc/strfry.conf", "relay"]
```
For the D-05(a) fallback, mirror the `command:` array to the router invocation
(RESEARCH lines 282–290, "Exact command line"). The global `--config` is a
golpe flag BEFORE the subcommand; the router file is the positional arg AFTER
`router`:
```yaml
    command: ["/app/strfry", "--config=/etc/strfry.conf", "router", "/app/strfry-router.config"]
```
Note this only runs the router (not `relay`) — D-05(a) replaces the entrypoint,
so if the relay must also run, D-05(b) (a dedicated router-only service sharing
the same LMDB bind-mount + `/etc/strfry.conf`) is the alternative. Use the
`strfry-quarantine` `working_dir: /app` (line 58) so the router's cwd matches
the entrypoint's assumptions (D-04: confirm cwd against dockur `strfry.sh`).

**Healthcheck override pattern** (lines 49–57) — copy this shape if the fallback
is a dedicated service and the baked-in 7777 HTTP healthcheck is wrong for it:
```yaml
    healthcheck:
      test: ["CMD-SHELL", "curl -ILfSs http://localhost:7778/ > /dev/null || exit 1"]
      interval: 1m
      timeout: 5s
      retries: 2
```
A router-only fallback service has no HTTP relay port, so it would need the
healthcheck disabled or repointed — this block is the analog for how the repo
overrides the dockur base image's inherited healthcheck.

**LMDB-sharing constraint (D-05b):** if a dedicated router service is used, it
must bind-mount the SAME LMDB path as the `strfry` service (line 21:
`${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db`) and the SAME
`/etc/strfry.conf`. Per CLAUDE.md DB-location HARD RULE, never introduce a named
volume — reuse the existing bind-mount variable verbatim. The `strfry-quarantine`
service deliberately mounts a *different* DB (lines 60–66); the router fallback
does the OPPOSITE — it shares the mainline DB.

---

### Proof runbook + recorded evidence artifact (test / verification, request-response)

**Analog (host command conventions):** `/Users/g/git/deepfry/stream-relays.sh`.
This is the on-host operator-script convention the proof runbook mirrors:
container-name constant, `docker ps`/`docker exec` gating, and the tmux session
(`strfry-streams`) the proof must STOP (D-09).

**Container/exec conventions** (`stream-relays.sh` lines 4–6, 81–87, 104–105):
```bash
SESSION="strfry-streams"
CONTAINER="strfry"
STRFRY="/app/strfry"
...
if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
  echo "Error: container '${CONTAINER}' is not running."
...
docker exec ${CONTAINER} ${STRFRY} stream ${RELAYS[0]}
```
Key facts for the D-09 fan-out stop:
- The fan-out is the tmux session `strfry-streams`; stop it with
  `tmux kill-session -t strfry-streams` (the `cmd_stop` pattern, lines 118–125).
- Those tmux windows run `docker exec strfry /app/strfry stream <relay>` procs
  INSIDE the `strfry` container. Recreating the `strfry` container for the new
  `ROUTER=` env also tears those exec procs down (CONTEXT lines 82–83).
- Do NOT modify or retire `stream-relays.sh` — that is Phase 4 (D-09). The proof
  only *stops* the running session.

**Proof grep targets** — from RESEARCH.md "Expected healthy startup log"
(lines 303–312), the three lines that constitute "proof" (CONTEXT lines 15–18):
```
Loading router config file: /app/strfry-router.config     <-- MUST see this
New stream group [ingest]                                 <-- MUST see this
ingest: Connecting to wss://...                           <-- MUST see this
```
Plus "exactly one `strfry router` process" — a process count inside the
container (e.g. `docker exec strfry pgrep -fc 'strfry router'` should be 1) and
the LMDB unmoved (relay still healthy on 7777). Absence of `Loading router
config file:` is the D-05 fallback trigger.

**Analog (evidence-artifact shape):** Phase 1's
`/Users/g/git/deepfry/.planning/phases/01-generate-router-config/01-VERIFICATION.md`
(lines 1–30). The recorded proof (D-07) should follow this repo's verification
convention — YAML frontmatter (`phase`, `verified`, `status`, `score`) + an
"Observable Truths" evidence table mapping each required log line / process
check to concrete captured output. Location/format is Claude's Discretion
(CONTEXT lines 85–89), but this frontmatter+truth-table shape is the established
pattern. The evidence must embed the actual captured host log excerpt + process
listing, not a summary (the verify-on-host discipline that correctly blocked
`260701-l5s`).

## Shared Patterns

### Bind-mount source indirection (`${VAR:-default}`)
**Source:** `/Users/g/git/deepfry/docker-compose.strfry.yml` lines 21, 30, 63, 79
**Apply to:** the new router bind-mount source.
```yaml
      - ${STRFRY_DB_PATH:-./data/strfry-db}:/app/strfry-db
      - ${BLOOM_DATA_PATH:-./data/bloom}:/root/deepfry/bloom-data
```
Every host-path mount and the network name use an overridable env var with a
default. The router mount should too (e.g.
`${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}`) rather than a
hardcoded `~/deepfry/...` — matches the file's own convention and lets a
split/relocated deploy override it.

### Two-files config separation (streams vs global)
**Source:** RESEARCH.md CORRECTION section (lines 218–290) + Phase 1 SUMMARY
(`.../01-generate-router-config/01-01-SUMMARY.md` lines 83–85)
**Apply to:** both the `ROUTER=` env and the D-05 fallback command.
The `streams{}` topology lives ONLY in the positional router file
(`/app/strfry-router.config`); `db`/`relay` live ONLY in the global
`--config` (`/etc/strfry.conf`). golpe silently discards unknown top-level keys,
so a misplaced `streams{}` "installs successfully" and does nothing — this is
the exact `260701-l5s` blocker this phase breaks. Never merge them; never add
`db` to the router mount or a `db` env.

### Dockur binary is at `/app/strfry`, not in PATH
**Source:** `/Users/g/git/deepfry/docker-compose.strfry.yml` lines 46–47 (comment)
**Apply to:** the D-05 fallback `command:` / any `docker exec` proof command.
```yaml
    # Absolute path: the dockurr/strfry image keeps the binary at /app/strfry
    # and isn't in PATH, so the guard's `exec "$@"` would otherwise fail.
```
Always use the absolute `/app/strfry` in exec/command arrays (stream-relays.sh
line 6 uses the same `STRFRY=/app/strfry` constant).

### Verify-on-host, record evidence
**Source:** Phase 1 `01-VERIFICATION.md` (frontmatter + Observable Truths table)
**Apply to:** the D-07 recorded proof artifact.
Frontmatter (`status`, `score`) + an evidence table binding each required truth
to captured output. The blocker is build/arch-specific (arm64 Mac mini), so the
proof MUST run on the deploy host (D-06); a local/CI check cannot substitute.

## No Analog Found

None. Every file touched has a direct in-repo analog (same compose file, adjacent
compose service, existing host operator script, Phase 1 verification artifact).

## Metadata

**Analog search scope:** `/Users/g/git/deepfry/docker-compose.strfry.yml`,
repo-root `*.sh` operator scripts (`stream-relays.sh`, `gen-router-config.sh`),
`config/strfry/` (`strfry.conf`, `strfry-quarantine.conf`),
`.planning/phases/01-generate-router-config/` (SUMMARY, VERIFICATION),
RESEARCH.md (`260701-l5s`).
**Files scanned:** 7
**Pattern extraction date:** 2026-07-02
