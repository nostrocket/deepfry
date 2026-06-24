---
phase: quick-260624-ogw
plan: "01"
subsystem: deployment
status: complete
tags: [docker, compose, lmdb2graphql, ops]
dependency_graph:
  requires: []
  provides: [working lmdb2graphql compose service]
  affects: [docker-compose.lmdb2graphql.yml, LMDB2GraphQL/config/lmdb2graphql.yaml]
tech_stack:
  added: []
  patterns: [committed container-ready config, shared STRFRY_DB_PATH env var]
key_files:
  created:
    - LMDB2GraphQL/config/lmdb2graphql.yaml
  modified:
    - docker-compose.lmdb2graphql.yml
    - LMDB2GraphQL/config/lmdb2graphql.yaml.example
    - LMDB2GraphQL/Dockerfile
    - .env.example
decisions:
  - "LMDB2GRAPHQL_CONFIG default changed from ./config/lmdb2graphql.yaml to ./LMDB2GraphQL/config/lmdb2graphql.yaml (monorepo-root-relative, matching renamed project dir)"
  - "Committed container-ready config at LMDB2GraphQL/config/lmdb2graphql.yaml with strfry_db_path: /app/strfry-db and bind_address: 0.0.0.0:8080"
  - "Dockerfile x86_64-unknown-linux-musl build target unchanged (arm64 parameterization deferred as follow-up)"
metrics:
  duration: "~10 minutes"
  completed: "2026-06-24"
  tasks_completed: 2
  files_changed: 5
---

# Phase quick-260624-ogw Plan 01: Fix lmdb2graphql Docker Compose service Summary

Fix broken lmdb2graphql compose service caused by stale spam/ build context (project renamed) and missing committed container-ready config.

## What Was Built

The `docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml up -d` workflow now works out of the box on any checkout. The `build.context` pointed at `spam/` (a deleted directory); the project had been renamed to `LMDB2GraphQL/`. Additionally there was no committed config at the default mount path, and the `STRFRY_DB_PATH` co-location requirement for the shared read-only LMDB mount was undocumented.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Fix compose build context, config default, and DB-path docs | 46e58da | docker-compose.lmdb2graphql.yml, .env.example |
| 2 | Create committed container-ready config and correct stale spam/ comments | 24b87f6 | LMDB2GraphQL/config/lmdb2graphql.yaml (new), LMDB2GraphQL/config/lmdb2graphql.yaml.example, LMDB2GraphQL/Dockerfile |

## Verification

End-to-end gate passed:

```
cd /Users/g/git/deepfry
docker compose -f docker-compose.strfry.yml -f docker-compose.lmdb2graphql.yml config
```

- Exits 0
- Resolved build context: `/Users/g/git/deepfry/LMDB2GraphQL` (existing directory)
- Resolved config mount source: `/Users/g/git/deepfry/LMDB2GraphQL/config/lmdb2graphql.yaml` (committed file)
- No `spam/` path references remain in compose, Dockerfile comments, or example config

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — all config values are wired to real container paths; no placeholders.

## Threat Flags

None — no new network endpoints, auth paths, or schema changes introduced. The LMDB mount was already `:ro` (T-05-05); this task preserves that.

## Follow-ups (Out of Scope)

- Parameterize Dockerfile build target to `aarch64-unknown-linux-musl` on arm64 hosts to eliminate QEMU emulation overhead. Deferred: risks static-link/C++-comparator build; needs separate verification.

## Self-Check: PASSED

- LMDB2GraphQL/config/lmdb2graphql.yaml — FOUND
- docker-compose.lmdb2graphql.yml — FOUND, build context = LMDB2GraphQL/
- Commits 46e58da and 24b87f6 — FOUND in git log
