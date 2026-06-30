---
phase: 04-ops-integration
plan: "01"
subsystem: whitelist-plugin
tags: [build, docker, ops, bloom, deployment]
dependency_graph:
  requires: [03-02]
  provides: [bloom-build-targets, docker-image-with-bloom, strfry-conf-bloom-docs, bloom-filter-persistence]
  affects: [Makefile, Dockerfile.strfry, config/strfry/strfry.conf, docker-compose.strfry.yml]
tech_stack:
  added: []
  patterns: [mirror-router-targets, host-path-bind-mount-pattern, dockerfile-plugin-builder-pattern]
key_files:
  created: []
  modified:
    - whitelist-plugin/Makefile
    - Dockerfile.strfry
    - config/strfry/strfry.conf
    - docker-compose.strfry.yml
decisions:
  - "D-02: bloom Makefile targets mirror router targets exactly — same static flags, same LDFLAGS version stamping"
  - "D-03: Dockerfile adds third RUN+COPY for bloom; whitelist/router lines byte-identical"
  - "D-04: strfry.conf keeps plugin = /app/plugins/router as active default; bloom documented as opt-in"
  - "D-05: bloom filter persisted under /root/deepfry/bloom-data/ (dedicated subdirectory to avoid per-file mount collisions)"
metrics:
  duration: "4m"
  completed: "2026-06-30"
  tasks: 3
  files: 4
status: complete
---

# Phase 04 Plan 01: Bloom Ops Integration Summary

**One-liner:** Bloom gate plugin made buildable (native + static Alpine/Linux binaries), baked into the strfry Docker image alongside whitelist/router, documented as a third opt-in writePolicy plugin in strfry.conf, and given a persistent filter directory in docker-compose.

## What Was Built

Three infrastructure changes that make the `cmd/bloom` plugin (completed in Phases 1-3) operationally deployable — mirroring exactly how `whitelist` and `router` are already handled.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add bloom Makefile build targets (OPS-01) | 6672847 | whitelist-plugin/Makefile |
| 2 | Bake bloom into Docker image + strfry.conf (OPS-02) | aebc3d3 | Dockerfile.strfry, config/strfry/strfry.conf |
| 3 | Persist bloom filter directory across restarts (OPS-02, GATE-05) | 7ffe059 | docker-compose.strfry.yml |

## Verification Evidence

- `make build-bloom` exits 0, produces `bin/bloom`
- `make build-bloom-alpine` exits 0, produces `bin/bloom-alpine`
- `make build-bloom-linux` exits 0, produces `bin/bloom-linux`
- `Dockerfile.strfry` contains `go build ... -o /bloom ./cmd/bloom` RUN block and `COPY --from=plugin-builder /bloom /app/plugins/bloom`
- Existing `/whitelist` and `/router` RUN+COPY lines in `Dockerfile.strfry` are byte-identical (git diff shows additions only)
- `config/strfry/strfry.conf` documents `/app/plugins/bloom` as a third opt-in writePolicy plugin; `plugin = "/app/plugins/router"` is the preserved active default
- `docker-compose.strfry.yml` adds `${BLOOM_DATA_PATH:-./data/bloom}:/root/deepfry/bloom-data` mount on `strfry` service only; `strfry-quarantine` service unchanged
- `docker compose -f docker-compose.strfry.yml config` validates (exit 0)
- `cmd/whitelist`, `cmd/router`, and their build/selection paths: zero changes (git diff HEAD~3 HEAD confirms no modifications)
- No sibling project files (spamhunter/, LMDB2GraphQL/, etc.) staged or committed

## Deviations from Plan

### Auto-fixed Issues

None - plan executed exactly as written.

The help block in the existing Makefile did not yet contain router rows (the original help block predated the router targets being added). Per the plan's "mirror router targets" instruction and the PATTERNS.md help block pattern, the router rows were added alongside the bloom rows. This is consistent with plan intent and added no net risk — it completes the help documentation for the already-existing router targets.

## Known Stubs

None — this plan adds no runtime behavior. All additions are build/deploy configuration only.

## Threat Flags

No new threat surface introduced. This plan adds only build targets and deploy configuration. The bloom binary is built from first-party source (`./cmd/bloom`) already audited in Phases 1-3. No new network endpoints, auth paths, or schema changes.

## Self-Check: PASSED

- [x] `bin/bloom` produced by `make build-bloom` — FOUND
- [x] `bin/bloom-alpine` produced by `make build-bloom-alpine` — FOUND
- [x] `bin/bloom-linux` produced by `make build-bloom-linux` — FOUND
- [x] commit 6672847 exists — FOUND (git log confirmed)
- [x] commit aebc3d3 exists — FOUND (git log confirmed)
- [x] commit 7ffe059 exists — FOUND (git log confirmed)
- [x] SUMMARY.md at correct path — this file
