---
phase: 02-wire-prove-config-loads
reviewed: 2026-07-02T00:00:00Z
depth: standard
files_reviewed: 1
files_reviewed_list:
  - docker-compose.strfry.yml
findings:
  critical: 1
  warning: 2
  info: 1
  total: 4
status: issues_found
---

# Phase 02: Code Review Report

**Reviewed:** 2026-07-02
**Depth:** standard
**Files Reviewed:** 1
**Status:** issues_found

## Summary

Reviewed the delta on `docker-compose.strfry.yml` since `5caca18`: a new `ROUTER`
env var (line 26) and a new bind-mount for the streams-only router config (line 33)
on the `strfry` service.

Against the three stated phase constraints, two pass and one is broken:

- ROUTER is an explicit path (`/app/strfry-router.config`), not `1`/`yes`. PASS.
- The router config mount is `:ro`. PASS.
- The mount is meant to be a **host bind-mount** carrying the host-generated
  streams-only config. FAIL — the default source path uses `~`, which Docker
  Compose does not expand, so the mount does not behave as a host bind-mount when
  the env var is unset.

The DB bind-mount (line 28) is untouched and still a host bind-mount, so the LMDB
HARD RULE is not directly violated by the diff. However, the router-config default
introduces a Docker-managed-volume failure mode that is conceptually the same class
of problem the HARD RULE guards against (config must live on the host filesystem,
not inside the Docker VM).

## Critical Issues

### CR-01: Router config default path `~/deepfry/...` is not expanded by Docker Compose

**File:** `docker-compose.strfry.yml:33`
**Issue:**
```yaml
- ${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}:/app/strfry-router.config:ro
```
Docker Compose does **not** perform shell tilde (`~`) expansion in volume source
paths. Only the invoking shell expands `~`, and here it sits inside a
`${VAR:-default}` substitution that Compose evaluates itself — no shell touches it.
When `STRFRY_ROUTER_CONFIG_PATH` is unset (the default path this phase ships), the
source becomes the literal string `~/deepfry/strfry-router.config`.

Because that string is neither absolute (`/...`) nor relative-prefixed (`./...`),
Compose does not treat it as a host bind-mount. Depending on Compose version it
either (a) creates/attaches a **named/anonymous Docker-managed volume** keyed on
that literal string, or (b) errors on an invalid mount source. In case (a) the
`strfry router` process starts `:ro` against an empty Docker-VM volume — no
`streams{}` config — so the router silently does nothing, defeating the entire
"Wire & Prove Config Loads" phase goal. The host-side regeneration / hot-reload
described in the line-30 comment can never reach the container.

This is also the same failure class the LMDB HARD RULE warns against: config that
should live on the host filesystem instead ends up in a non-portable Docker-managed
volume inside the VM.

Note the contrast with every other volume default in the repo — lines 28, 41, 74,
and `docker-compose.dgraph.yml:19` all use `./data/...` (relative, correctly
resolved by Compose). Line 33 is the only default using `~`.

**Fix:** Use an absolute or `./`-relative default that Compose resolves as a real
host bind-mount. Prefer requiring the env var explicitly, or fall back to a
project-relative path:
```yaml
# Option A — project-relative default (resolves as a host bind-mount):
- ${STRFRY_ROUTER_CONFIG_PATH:-./data/strfry-router.config}:/app/strfry-router.config:ro

# Option B — require the var explicitly (fail loud if unset), documented in .env.example:
- ${STRFRY_ROUTER_CONFIG_PATH:?set STRFRY_ROUTER_CONFIG_PATH to the host router config path}:/app/strfry-router.config:ro
```
If the intended default really is the user's home dir, the caller's `.env` must
supply a fully-expanded absolute path (e.g. `STRFRY_ROUTER_CONFIG_PATH=/home/g/deepfry/strfry-router.config`);
`~` cannot be used as a default in the compose file.

## Warnings

### WR-01: Missing source file breaks container start / falls back to Docker volume

**File:** `docker-compose.strfry.yml:33`
**Issue:** The comment (lines 30–33) states the router config is a "Phase 1 runtime
artifact, generated host-side, not committed." If that file does not yet exist on the
host at the resolved path, Docker will create an **empty directory** at the source and
bind-mount a directory over the expected file target `/app/strfry-router.config`.
`strfry router /app/strfry-router.config` then fails or reads nothing, and there is no
guard/precondition ensuring the artifact exists before `up`. Combined with CR-01, an
unset env var produces a silently-empty config rather than a loud failure.

**Fix:** Add a pre-start check (compose profile, Makefile target, or entrypoint guard)
that verifies the host router-config file exists and is a regular file before starting
the `strfry` service — mirroring the `quarantine-db-guard.sh` pattern already used for
the quarantine DB (lines 56, 76). At minimum, document the required generation step so
`up` is not run against a missing artifact.

### WR-02: New `ROUTER` / `STRFRY_ROUTER_CONFIG_PATH` env vars undocumented in `.env.example`

**File:** `docker-compose.strfry.yml:26,33`
**Issue:** Neither `ROUTER` nor `STRFRY_ROUTER_CONFIG_PATH` is present in
`.env.example` (confirmed by grep — both absent). Operators have no discoverable way
to learn the required router-config path, the "must be an explicit path, not 1/yes"
constraint, or the CR-01 tilde caveat. This makes the misconfiguration in CR-01 far
more likely to bite in production.

**Fix:** Add both vars to `.env.example` with commented guidance, e.g.:
```bash
# Host path to the generated streams-only strfry router config (bind-mounted :ro).
# Must be an ABSOLUTE path — Docker Compose does NOT expand ~.
# STRFRY_ROUTER_CONFIG_PATH=/home/youruser/deepfry/strfry-router.config
```

## Info

### IN-01: Comment says "Container path matches ROUTER exactly" — worth a guard, not just a comment

**File:** `docker-compose.strfry.yml:26,31-33`
**Issue:** Correctness depends on the string `/app/strfry-router.config` being
identical in the `ROUTER` env value (line 26) and the mount target (line 33). This is
enforced only by a comment. A future edit to one and not the other would leave the
router reading a path with nothing mounted at it — silent no-op, same symptom class as
CR-01.

**Fix:** Optional — anchor the value once via an env var reused in both places, or add
a startup assertion that `$ROUTER` resolves to a readable file. Low priority given the
comment, but the coupling is invisible to tooling.

---

_Reviewed: 2026-07-02_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
