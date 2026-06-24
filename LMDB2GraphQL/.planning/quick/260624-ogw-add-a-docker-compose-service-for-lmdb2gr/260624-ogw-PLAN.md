---
phase: quick-260624-ogw
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - /Users/g/git/deepfry/docker-compose.lmdb2graphql.yml
  - /Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml
  - /Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml.example
  - /Users/g/git/deepfry/LMDB2GraphQL/Dockerfile
  - /Users/g/git/deepfry/.env.example
autonomous: true
requirements: [OPS-02]
must_haves:
  truths:
    - "docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config succeeds and resolves the lmdb2graphql build context to LMDB2GraphQL/ (an existing directory)"
    - "docker compose up -d works out of the box: a committed config file exists at the default LMDB2GRAPHQL_CONFIG path with container-ready values"
    - "operator can mount the live strfry LMDB read-only by setting STRFRY_DB_PATH (same var strfry uses), documented in .env.example"
    - "no stale spam/ path references remain in the lmdb2graphql deployment files"
  artifacts:
    - path: "/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml"
      provides: "committed container-ready config consumed by the compose default mount"
      contains: "strfry_db_path: /app/strfry-db"
    - path: "/Users/g/git/deepfry/docker-compose.lmdb2graphql.yml"
      provides: "working lmdb2graphql service definition with correct build context"
      contains: "context: LMDB2GraphQL/"
  key_links:
    - from: "/Users/g/git/deepfry/docker-compose.lmdb2graphql.yml"
      to: "/Users/g/git/deepfry/LMDB2GraphQL/"
      via: "build.context resolves to the renamed project directory"
      pattern: "context: LMDB2GraphQL/"
    - from: "/Users/g/git/deepfry/docker-compose.lmdb2graphql.yml"
      to: "/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml"
      via: "LMDB2GRAPHQL_CONFIG default mount target"
      pattern: "LMDB2GRAPHQL_CONFIG:-./LMDB2GraphQL/config/lmdb2graphql.yaml"
---

<objective>
Fix the broken `lmdb2graphql` docker-compose service so it runs in-container, co-located with strfry, mounting strfry's LMDB read-only — resolving the `MDB_BAD_RSLOT` failure caused by running the native macOS binary against a Linux/Docker-held `lock.mdb`.

Root cause (proven via host investigation): `lmdb2graphql` was run as a native macOS binary against strfry's live LMDB. strfry runs inside Docker (Linux); LMDB's `lock.mdb` reader table + process-shared mutexes are OS/runtime-specific and cannot be shared across a Linux↔macOS boundary → first read txn fails with `MDB_BAD_RSLOT: Invalid reuse of reader locktable slot`. The fix is to run lmdb2graphql in a container co-located with strfry (the deployment the project CLAUDE.md already specifies).

The compose service already exists but is broken: `build.context: spam/` points at a directory that no longer exists (project was renamed `spam/` → `LMDB2GraphQL/`), so `docker compose build` fails immediately. Stale `spam/` references also appear in compose/Dockerfile/example comments. There is no committed config at the default mount path, and the `STRFRY_DB_PATH` default is wrong for the live host.

Purpose: Make `docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml up -d` work out of the box, sharing the same Linux LMDB locking as strfry.
Output: Corrected compose service, a committed container-ready config, corrected stale path comments, and documented `STRFRY_DB_PATH` requirement.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@/Users/g/git/deepfry/CLAUDE.md
@/Users/g/git/deepfry/LMDB2GraphQL/CLAUDE.md
@/Users/g/git/deepfry/docker-compose.lmdb2graphql.yml
@/Users/g/git/deepfry/docker-compose.strfry.yml
@/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml.example
@/Users/g/git/deepfry/LMDB2GraphQL/Dockerfile
@/Users/g/git/deepfry/.env.example

Investigation facts (authoritative — do NOT re-derive):
- `spam/` no longer exists; project is at `LMDB2GraphQL/`.
- Live strfry DB on the deploy host (Mac mini, arm64): `/Volumes/BACKUP/nostr/strfry_database`, mounted into the strfry container as `/app/strfry-db` rw. The strfry compose uses `${STRFRY_DB_PATH:-./data/strfry-db}` for the same value — keep the env-var pattern, do NOT hardcode the `/Volumes/...` path.
- Container runs as root (HOME=/root); `dirs::home_dir()` → `/root/deepfry/lmdb2graphql.yaml`, so the existing mount target `/root/deepfry/lmdb2graphql.yaml:ro` is correct.
- Dockerfile hardcodes `x86_64-unknown-linux-musl` (runs via QEMU on the arm64 host; functional, both targets little-endian so LMDB correctness is unaffected). Do NOT change the build target in this task — it risks the static-link/C++-comparator build. Note as a follow-up only.
</context>

<tasks>

<task type="auto">
  <name>Task 1: Fix compose service build context, config default, and DB-path docs</name>
  <files>/Users/g/git/deepfry/docker-compose.lmdb2graphql.yml, /Users/g/git/deepfry/.env.example</files>
  <action>In docker-compose.lmdb2graphql.yml: (1) change `build.context` from `spam/` to `LMDB2GraphQL/` (the renamed project dir — the primary blocker). (2) Change the `LMDB2GRAPHQL_CONFIG` default in the config volume from `./config/lmdb2graphql.yaml` to `./LMDB2GraphQL/config/lmdb2graphql.yaml` (path is relative to the monorepo root where the compose file lives) so the committed config created in Task 2 is mounted by default — per the locked decision. (3) Update the stale header comment that points operators at `spam/config/lmdb2graphql.yaml.example` to reference `LMDB2GraphQL/config/lmdb2graphql.yaml.example`, and update the prerequisite note to point at the now-committed `./LMDB2GraphQL/config/lmdb2graphql.yaml`. (4) In the header comments, add a note that the operator MUST set `STRFRY_DB_PATH` to the same value strfry uses (on this host: `/Volumes/BACKUP/nostr/strfry_database`); do NOT hardcode that path into the service — keep the `${STRFRY_DB_PATH:-./data/strfry-db}` var and the `:ro` read-only mount unchanged (defense-in-depth against accidental write txns, T-05-05). Do NOT change the healthcheck, ports, networks, or restart policy.

  In .env.example: the MACHINE-SPECIFIC PATHS block already has a commented `# STRFRY_DB_PATH=/mnt/ssd/strfry-db`. Augment that block with a comment explaining that lmdb2graphql mounts this same path read-only and must match the strfry service, and note the value used on the co-located Mac mini host. Keep STRFRY_DB_PATH commented (operator sets it per-host); do not introduce real secrets.</action>
  <verify>
    <automated>cd /Users/g/git/deepfry &amp;&amp; docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config &gt;/dev/null &amp;&amp; docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config | grep -A2 'lmdb2graphql' | grep -q 'LMDB2GraphQL' &amp;&amp; test -d /Users/g/git/deepfry/LMDB2GraphQL &amp;&amp; ! grep -nE '(context:[[:space:]]*spam/|spam/config/)' /Users/g/git/deepfry/docker-compose.lmdb2graphql.yml</automated>
  </verify>
  <done>`docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config` exits 0; the resolved lmdb2graphql build context points at the existing `LMDB2GraphQL/` directory; the config volume default is `./LMDB2GraphQL/config/lmdb2graphql.yaml`; no `spam/` path references remain in the compose file; `.env.example` documents the shared `STRFRY_DB_PATH` requirement for the read-only mount.</done>
</task>

<task type="auto">
  <name>Task 2: Create committed container-ready config and correct stale spam/ comments</name>
  <files>/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml, /Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml.example, /Users/g/git/deepfry/LMDB2GraphQL/Dockerfile</files>
  <action>Create the committed config file `/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml` with the container-ready values from the existing example (per the locked decision): `strfry_db_path: /app/strfry-db`, `bind_address: "0.0.0.0:8080"`, `map_size: 10995116277760`, and the two pinned strfry fields copied verbatim from the example — `pinned_strfry_version` and `pinned_strfry_commit`. Include a short header comment stating this is the committed in-container config mounted read-only at `/root/deepfry/lmdb2graphql.yaml` by docker-compose.lmdb2graphql.yml, and that it contains no secrets. Note that bare-metal/dev users should instead copy the `.example` to `~/deepfry/lmdb2graphql.yaml` and use `127.0.0.1:8080`. Do NOT touch `~/deepfry/` (CLAUDE.md rule) — this file lives in the repo only.

  In lmdb2graphql.yaml.example: correct the stale `spam/config/lmdb2graphql.yaml.example` reference on line 4 to `LMDB2GraphQL/config/lmdb2graphql.yaml.example`. Leave all config values and other comments unchanged.

  In Dockerfile: correct the two stale `spam/` references in the header comment block (the `docker build --target builder ... spam/` example commands) to use `LMDB2GraphQL/`. Do NOT change any build stage, the `x86_64-unknown-linux-musl` target, RUN/COPY directives, or any FROM line — comment text only.</action>
  <verify>
    <automated>cd /Users/g/git/deepfry &amp;&amp; test -f LMDB2GraphQL/config/lmdb2graphql.yaml &amp;&amp; grep -q 'strfry_db_path: /app/strfry-db' LMDB2GraphQL/config/lmdb2graphql.yaml &amp;&amp; grep -q 'bind_address: "0.0.0.0:8080"' LMDB2GraphQL/config/lmdb2graphql.yaml &amp;&amp; grep -q 'pinned_strfry_commit' LMDB2GraphQL/config/lmdb2graphql.yaml &amp;&amp; ! grep -rn 'spam/config' LMDB2GraphQL/config/lmdb2graphql.yaml.example &amp;&amp; ! grep -nE 'spam/' LMDB2GraphQL/Dockerfile</automated>
  </verify>
  <done>A committed `LMDB2GraphQL/config/lmdb2graphql.yaml` exists with `strfry_db_path: /app/strfry-db`, `bind_address: "0.0.0.0:8080"`, `map_size: 10995116277760`, and both pinned strfry fields; the example file no longer references `spam/config`; the Dockerfile header comments no longer reference `spam/`; no build stages or the build target were modified.</done>
</task>

</tasks>

<verification>
Full end-to-end gate (the canonical check from the constraints):

```
cd /Users/g/git/deepfry
docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config
```

Must exit 0 and resolve the lmdb2graphql build context to the existing `LMDB2GraphQL/` directory. A full image build is optional (slow under QEMU emulation on the arm64 host) — `config` validation is the gating check.

Sanity: with `LMDB2GRAPHQL_CONFIG` unset, the resolved config mount source must be `./LMDB2GraphQL/config/lmdb2graphql.yaml`, and that file must exist in the repo.
</verification>

<success_criteria>
- `docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config` succeeds with the build context resolving to the existing `LMDB2GraphQL/` directory.
- A committed container-ready config exists at `LMDB2GraphQL/config/lmdb2graphql.yaml` and is the default mount source.
- The operator path to mount the live strfry LMDB read-only via `STRFRY_DB_PATH` is documented in `.env.example` and the compose header.
- No stale `spam/` references remain in the compose file, the Dockerfile comments, or the example config.
- The Dockerfile build target and build stages are unchanged.
</success_criteria>

<followups>
RECOMMENDED (not in scope — do NOT implement here): parameterize the Dockerfile build target to `aarch64-unknown-linux-musl` on arm64 hosts to avoid QEMU emulation overhead for the large live-read workload. Both targets are little-endian so LMDB correctness is unaffected; deferred because it risks the static-link / C++-comparator build and needs its own verification.
</followups>

<output>
Create `.planning/quick/260624-ogw-add-a-docker-compose-service-for-lmdb2gr/260624-ogw-SUMMARY.md` when done.
</output>
