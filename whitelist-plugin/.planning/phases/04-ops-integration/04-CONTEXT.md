# Phase 4: Ops & Integration - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Source:** Orchestrator-resolved decisions (plan-phase; discuss-phase skipped by user choice)

<domain>
## Phase Boundary

Make the existing `cmd/bloom` gate plugin buildable, deployable, and documented the
same way the existing `whitelist`/`router` plugins are. No new runtime behavior — this
phase is build targets (OPS-01), Docker + `strfry.conf` wiring (OPS-02), and docs
(OPS-03). The plugin code itself (Phases 1–3) is complete and stays unchanged.

</domain>

<decisions>
## Implementation Decisions

### Cross-project boundary (explicit user permission)
- **D-01**: OPS-02 may modify deepfry **monorepo-root** deploy files — `Dockerfile.strfry`,
  `docker-compose.strfry.yml`, and `config/strfry/strfry.conf` — even though they live
  outside the `whitelist-plugin/` project directory. The user granted explicit cross-boundary
  permission for these specific deploy files (they are the deployment surface for this
  plugin's binaries). Do NOT touch any other sibling project.

### Build targets (OPS-01)
- **D-02**: Add bloom Makefile targets mirroring the existing `router` targets exactly:
  `build-bloom` (native), `build-bloom-alpine` (static musl), `build-bloom-linux` (static
  generic). Use the same `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 ... -tags netgo` flags and
  the same `LDFLAGS` version-stamping. Add an `APP_BLOOM=bloom` variable, register the new
  targets in `.PHONY` and `all`, and add `help` lines — matching the router rows.

### Docker + strfry.conf (OPS-02)
- **D-03**: In `Dockerfile.strfry`, add a third `plugin-builder` build step for `./cmd/bloom`
  → `/bloom`, and a `COPY --from=plugin-builder /bloom /app/plugins/bloom` — mirroring the
  existing `/whitelist` and `/router` steps. Bloom ships **alongside** whitelist/router in
  the same image (all three baked; selection is config-time, not build-time).
- **D-04**: Bloom stays **opt-in**. `config/strfry/strfry.conf` selects the writePolicy plugin
  by a single-line change to point at `/app/plugins/bloom` (per roadmap SC2). `whitelist` and
  `router` binaries and their selection paths remain byte-identical / untouched.
- **D-05**: Bloom reads its `bloom_`-prefixed config from the shared `whitelist.yaml` already
  bind-mounted at `/root/deepfry/whitelist.yaml` (established in Phase 3 / GATE-07). The
  persisted filter is written under `~/deepfry/` (`/root/deepfry/` in-container). Plans must
  account for filter persistence surviving restarts (a writable `~/deepfry/` path/volume) so
  GATE-05 resilience holds in the Docker deployment, not just locally.

### Docs (OPS-03)
- **D-06**: `whitelist-plugin/README.md` documents the bloom plugin (config keys, accept/reject
  behavior, periodic fetch + persistence/resilience) and the server's `GET /bloom` endpoint
  (conditional GET / ETag), consistent in structure with the existing plugin/endpoint docs.

### Claude's Discretion
- Exact Dockerfile layer ordering and whether to factor the three plugin builds into a loop
  vs. three explicit steps (keep consistent with current explicit style).
- README section placement and heading structure.
- Whether to also wire bloom into `strfry-quarantine.conf` or leave quarantine on its current
  plugin (default: leave quarantine unchanged unless a plan shows a clear need).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Build conventions (mirror these)
- `whitelist-plugin/Makefile` — existing `build`/`build-router`/`build-server` + `-alpine`/`-linux`
  variants, `LDFLAGS` version stamping, `.PHONY`/`all`/`help` structure to extend
- `whitelist-plugin/cmd/bloom/` — the binary these targets build

### Docker / strfry deploy surface (deepfry monorepo ROOT — outside whitelist-plugin/)
- `/Users/g/git/deepfry/Dockerfile.strfry` — `plugin-builder` stage builds `/whitelist` + `/router`
  from `whitelist-plugin/cmd/*` → `/app/plugins/`; add the bloom equivalent here
- `/Users/g/git/deepfry/config/strfry/strfry.conf` — selects the writePolicy plugin (single-line change)
- `/Users/g/git/deepfry/config/strfry/strfry-quarantine.conf` — quarantine relay config (reference; leave unchanged unless needed)
- `/Users/g/git/deepfry/docker-compose.strfry.yml` — mounts `strfry.conf`, `whitelist.yaml`, `router.yaml`; bloom config + filter persistence path live here

### Docs
- `whitelist-plugin/README.md` — existing plugin/endpoint docs to extend consistently

</canonical_refs>

<specifics>
## Specific Ideas

- Mirror `router` (not `whitelist`) for Makefile targets — `router` is the closest analog
  (also a writePolicy plugin built from `./cmd/<name>`).
- The Dockerfile already uses a single `plugin-builder` stage building multiple binaries; the
  bloom build is a third `RUN go build ... -o /bloom ./cmd/bloom` + one more `COPY`.

</specifics>

<deferred>
## Deferred Ideas

- GATE-F1 (faster refresh) and GATE-F2 (metrics endpoint/counters) are v2 — out of scope.
- Changing the default writePolicy from `whitelist` to `bloom` is an operator decision, not a
  code change in this phase — bloom remains opt-in.

</deferred>

---

*Phase: 04-ops-integration*
*Context resolved: 2026-06-30 via plan-phase orchestrator (cross-boundary permission granted by user)*
