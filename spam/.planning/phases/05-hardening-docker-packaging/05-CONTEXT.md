# Phase 5: Hardening & Docker Packaging - Context

**Gathered:** 2026-06-13
**Status:** Ready for planning
**Mode:** Auto-generated (infrastructure phase — discuss skipped per smart-discuss infrastructure detection)

<domain>
## Phase Boundary

Make LMDB2GraphQL operationally safe for DeepFry deployment. Scope:
1. **Liveness/readiness gates** — `/health` (process alive → 200) and `/ready` (200 only after the LMDB env opens AND the comparator self-check passes; 503 otherwise).
2. **Docker packaging** — a Dockerfile and a `docker-compose` service that runs co-located with strfry in the DeepFry stack, mounting `strfry-db` read-only (`:ro`).
3. **CI correctness gate** — CI generates a fixture `strfry-db` from the pinned strfry version/digest and asserts: (a) both `0x00` and `0x01` payload decoding succeed, and (b) LMDB2GraphQL's reimplemented comparator scan order matches strfry's byte-for-byte.
4. **Version drift surfacing** — startup output and the `stats` query expose the expected (pinned) strfry version alongside the detected on-disk `dbVersion`, so operators spot drift if the parent `dockurr/strfry` image moves.

Out of scope: end-user GraphQL features (delivered Phase 4), semantic search, and any write path.
</domain>

<decisions>
## Implementation Decisions

### Claude's Discretion
All implementation choices are at Claude's discretion — this is a pure infrastructure/packaging/hardening phase with prescriptive technical success criteria. Use the ROADMAP phase goal, success criteria (OPS-01..OPS-04), the existing codebase conventions, and the project's locked stack/constraints (CLAUDE.md) to guide decisions. Plan-phase research will resolve the specific approaches (health-endpoint wiring into the existing axum router, Alpine static-binary Docker build, docker-compose service co-located with strfry, CI fixture generation against the pinned strfry digest).

### Locked constraints (from PROJECT.md / CLAUDE.md — not re-decided here)
- Read-only LMDB access only (`MDB_RDONLY`); never open a write txn. Short per-query read txns.
- Hard version gate: refuse to run if `Meta.dbVersion != 3`; assert endianness matches host.
- Co-located deployment: mount strfry's `strfry-db` read-only.
- Packaged as its own Docker subsystem within the DeepFry stack with a docker-compose service.
- Pin a strfry version/digest; treat strfry's index key formats + comparator semantics as a private API.
- Comparator self-check must fail closed at startup (pinned-fixture self-check).
</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `src/main.rs` — already performs the synchronous startup gate chain: load config → open env (`MDB_RDONLY`) → read Meta → `assert_db_version` (LMDB-02) → `assert_endianness` (LMDB-03) → `run_comparator_self_check` (the pinned-fixture seek-gate self-check). It already logs `pinned_strfry_version` alongside detected `db_version` (OPS-04 startup basics). The `/ready` gate should reflect the SAME conditions (env open + comparator self-check pass).
- `src/server.rs` — axum router (`build_router(schema)`): `POST /graphql` (async-graphql Tower service) + GraphiQL on `GET`, with a `RequestBodyLimitLayer`. New `/health` and `/ready` routes hang off this router. Note the axum 0.8 `get(handler).post_service(svc)` chaining pattern already documented in the file.
- `src/config.rs` — holds `pinned_strfry_version` and `bind_address` (now defaulting to loopback `127.0.0.1:8080` after Phase-4 CR-01); extend here for any new ops config (e.g. health bind, readiness toggles).
- `src/lmdb/self_check.rs` — `GoldenVectors::load_committed()` + `run_comparator_self_check(&env, &golden)`; the readiness probe and CI comparator-order assertion both build on this.
- `src/lmdb/meta.rs` — `assert_db_version` / `assert_endianness`; `MetaRecord` exposes `db_version`. `stats` resolver already surfaces `dbVersion`; extend `stats` to also surface the pinned strfry version (OPS-04).
- Parent stack already has `../docker-compose.strfry.yml`, `../docker-compose.dgraph.yml`, `../docker-compose.evtfwd.yml` — model the new service compose file on these and co-locate with strfry's `strfry-db` volume.

### Established Patterns
- `tracing` structured logging (info/warn) at startup; fail-closed `anyhow` context on each gate.
- Alpine static-binary build pattern is referenced in CLAUDE.md (`RUSTFLAGS="-C target-feature=+crt-static"`, `rusqlite bundled`, static liblmdb). NOTE: this build machine's toolchain runs the `stable-x86_64-apple-darwin` toolchain under Rosetta; CI/Docker should pin its own Linux toolchain rather than copy the local dev setup.
- Tests run via `cargo test` against a committed fixture LMDB; CI should generate/refresh the fixture from the pinned strfry digest.

### Integration Points
- Health/readiness routes → `src/server.rs` router; readiness state must be shared with `main.rs`'s gate results (e.g. an `Arc`-shared readiness flag set after the self-check passes, or the router only mounts after gates pass — readiness should still report 503 during async startup before the env is confirmed open).
- Docker service → parent DeepFry `docker-compose` stack, mounting the strfry `strfry-db` volume `:ro`.
- CI → fixture generation from pinned `dockurr/strfry` digest (already pinned in the parent Dockerfile.strfry per STATE.md), then `cargo test` correctness assertions.
</code_context>

<specifics>
## Specific Ideas

No specific user-supplied requirements — infrastructure phase. Refer to ROADMAP success criteria (OPS-01 health/readiness, OPS-02 docker-compose co-location with `:ro` mount, OPS-03 CI fixture correctness gate, OPS-04 version-drift surfacing) and existing codebase patterns. The traceability warning surfaced at Phase-4 close (OBS-01, PORT-01 present in REQUIREMENTS body but missing from the table) should be reconciled during this phase's planning if those IDs belong to the hardening scope.
</specifics>

<deferred>
## Deferred Ideas

None — infrastructure phase; scope is bounded by the ROADMAP success criteria.
</deferred>
