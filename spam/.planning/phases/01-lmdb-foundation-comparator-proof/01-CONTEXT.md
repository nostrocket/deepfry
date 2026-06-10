# Phase 1: LMDB Foundation & Comparator Proof - Context

**Gathered:** 2026-06-10
**Status:** Ready for planning

<domain>
## Phase Boundary

De-risk the entire Approach-B bet and lay the production foundation for reading strfry's LMDB. This phase:

- Opens strfry's LMDB environment **read-only** (`MDB_RDONLY`, `map_size ≥ 10995116277760`), never opening a write transaction.
- Gates on `Meta.dbVersion == 3` and asserts `Meta.endianness` matches the host — exits loudly on mismatch.
- Makes strfry/golpe's three custom comparators (`StringUint64`, `Uint64Uint64`, `StringUint64Uint64`) available to LMDB via `mdb_set_compare`, registered through heed's `Comparator` trait, on each `Event__*` index it scans.
- Proves scan order is **byte-exact** against a pinned strfry fixture across all six `Event__*` indexes, via a fail-closed startup self-check.
- Records the pinned strfry version/digest as a shared contract with the parent DeepFry stack.

**Out of scope (later phases):** payload decoding (`0x00`/`0x01` → JSON) is Phase 2; query semantics/filter routing/`latestPerAuthor`/NIP-40 is Phase 3; GraphQL API is Phase 4; health/ready endpoints + docker-compose + CI hardening is Phase 5. This phase may build the *reusable* self-check gate that Phase 5's `/ready` later calls, but does not build the HTTP surface.

</domain>

<decisions>
## Implementation Decisions

### Comparator technique (CHANGES a previously-locked decision)
- **D-01:** golpe's **C++ comparator ships in production** via a thin FFI call inside heed's `Comparator::compare`. There is **no Rust reimplementation** of the comparator logic. Both the original "Rust reimpl" plan and a "C++-in-production" plan register through the *same* heed `Comparator` trait — the difference is only where the comparison logic lives — so shipping golpe's actual code costs no extra registration machinery and eliminates reimplementation-correctness risk entirely.
- **D-02:** `build.rs` compiles golpe's vendored comparator source against vendored `lmdb++.h` (lmdbxx, header-only) via the `cc`/`cxx` crate. We accept including lmdbxx/golpe headers wholesale rather than surgically isolating the comparator function. The Alpine **static release binary therefore carries a C++/golpe build** — the musl/static-libstdc++ link story is a Phase-1 build-system task (see Research item #2).
- **⚠️ D-03 (requirement amendment):** This **amends LMDB-05**, which currently reads "reimplements strfry/golpe's custom comparators ... *in Rust*". New intent: "links golpe's custom comparators via FFI and registers them through heed's `Comparator` trait (`mdb_set_compare`)." PROJECT.md Key Decisions and the project CLAUDE.md ("Reimplement ... in Rust") carry the same stale wording and need updating. The spike's central risk shifts accordingly — from "did I reimplement the math correctly?" to "can heed register a foreign comparator and does scan order match?" (which we agreed is the real go/no-go gate regardless).

### Self-check & proof
- **D-04:** **Keep** the fail-closed startup self-check, **reframed** as a wiring/registration/drift gate (not a reimplementation-correctness proof). It guards the silent failure modes that survive even with golpe's real comparator: (a) heed registration silently not taking effect → LMDB falls back to `memcmp` → wrong subsets with no error; (b) the wrong comparator bound to the wrong index; (c) build/host/endianness/dependency drift. Preserves LMDB-06 and success criteria #3/#4.
- **D-05:** Oracle = **committed golden vectors**. For each index, compute the expected ordered `levId` sequence analytically from the adversarial seed + the documented/vendored comparator rules, **hand-audit once**, and commit. This is independent, human-verified ground truth — deliberately NOT a dump from a golpe-linked tool (which would use the same comparator we ship → circular, proves consistency not correctness).
- **D-06:** Self-check assertion granularity = **full ordered-sequence equality per index** (scan end-to-end, assert the full `levId` sequence equals the golden reference). On any mismatch, fail closed (refuse to start).
- **D-07:** Prove **all six** `Event__*` indexes: `Event__id`, `Event__pubkey`, `Event__created_at`, `Event__kind`, `Event__pubkeyKind`, `Event__tag`. The adversarial seed already must exercise every comparator, so the marginal cost is mostly more golden vectors — acceptable for a spike the whole project rests on.

### Fixture & version pin
- **D-08:** Pin strfry by **resolving `dockurr/strfry:latest` now** and recording its **sha256 digest + the strfry git commit it was built from**. Matches what the parent actually deploys today. Confirm the resolved build is `dbVersion == 3` (Research item #3).
- **D-09:** **Also pin the parent `Dockerfile.strfry`** to the same digest — a cross-repo change into `/Users/gareth/git/nostr/deepfry/Dockerfile.strfry` (currently `FROM dockurr/strfry:latest`). User explicitly approved touching parent-stack infra in this phase to make both projects share one concrete pin.
- **D-10:** Fixture strategy = **both**: commit a binary fixture (`data.mdb` + `lock.mdb`) for fast local/CI tests with no Docker, AND have CI regenerate the fixture from the pinned image and assert it matches the committed one. Document the committed blob's provenance; regenerate on version bump.
- **D-11:** Seed is **adversarial**, hand-designed to force the exact orderings naive `memcmp` gets wrong: same-pubkey/different-`created_at`, same-`kind`/different-`created_at`, shared tag-value prefixes, and `created_at` values whose little-endian byte order inverts numeric order. Guarantees the self-check fails if a comparator is mis-wired or not registered.
- **D-12:** **Vendor** `golpe.yaml` + golpe's generated comparator source (the `StringUint64` / `Uint64Uint64` / `StringUint64Uint64` definitions) into the repo under e.g. `reference/`, tagged with the upstream commit SHA. Downstream reads vendored pinned truth, not a moving upstream.

### Deliverable shape & config
- **D-13:** Phase 1 builds the **real foundational `lmdb`-access module of the production crate** — not a throwaway harness. Includes env open (`MDB_RDONLY`), `Meta` version gate, endianness assert, comparator registration via heed, and the self-check wired as a **reusable startup gate** (the same code Phase 5's `/ready` will call). The spike risk is retired by building the real thing minimally.
- **D-14:** Introduce a **minimal `~/deepfry/lmdb2graphql.yaml`** in Phase 1 holding only what this phase needs: the `strfry-db` path + the pinned strfry version/digest contract. Matches DeepFry's `~/deepfry/*.yaml` convention; gives the version pin (LMDB-10) a natural home from day one. Config grows in later phases.

### Claude's Discretion
- Exact crate/module layout, error type design (`thiserror`), and logging/diagnostics format for gate failures — within the established stack (heed 0.22.1, `tracing`).
- How the pinned-vs-detected strfry version drift is surfaced at startup (this becomes a fuller OPS-04 concern in Phase 5; a basic startup log line here is sufficient).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### On-disk format & strfry storage model (authoritative)
- `spec.md` §3 — strfry's storage model, named sub-DBs (dbi table), `levId` semantics. **REFERENCE ONLY.**
- `spec.md` §3.2 — `EventPayload` value encoding (`0x00` raw / `0x01` zstd-dict). (Decoding itself is Phase 2, but informs which sub-DBs exist.)
- `spec.md` §6.1 — DB version coupling / hard gate (`dbVersion == 3`).
- `spec.md` §6.2 — **Custom comparators: the core risk.** golpe registers `StringUint64`/`Uint64Uint64`/`StringUint64Uint64` via `mdb_set_compare`; LMDB never persists comparators; a foreign reader gets silently-wrong order without them.
- `spec.md` §6.3 — Native-endian integers; `Meta.endianness` check.
- ⚠️ **spec.md caveat:** spec.md is a pre-pivot **Approach A** document — its architecture *recommendation* (derived store / SQLite) is **superseded** by Approach B (PROJECT.md). Use it only for the verified on-disk encodings and caveats (§3, §6.1–6.3), NOT for architecture.

### Project decisions & requirements
- `.planning/PROJECT.md` — Approach B, Rust stack, Key Decisions table (NOTE: "reimplement in Rust" decision is amended by D-01/D-03).
- `.planning/REQUIREMENTS.md` — LMDB-01..06, LMDB-10 (Phase 1 scope). NOTE: **LMDB-05 wording is amended** by D-03.
- `.planning/ROADMAP.md` — Phase 1 goal + success criteria.

### Parent DeepFry stack (strfry deployment surface)
- `../config/strfry/strfry.conf` — db path `./strfry-db/`, `mapsize = 10995116277760` (10 TiB), `maxreaders = 256`, `noReadAhead = false`.
- `../Dockerfile.strfry` — `FROM dockurr/strfry:latest` (unpinned → to be pinned to a digest per D-09).
- `../docker-compose.strfry.yml` — how strfry is brought up (relevant for fixture-gen + Phase 5 co-location).
- `../CLAUDE.md` — DeepFry data-separation rule ("no event payloads outside StrFry"), "never fork strfry" principle.

### Project stack reference
- `CLAUDE.md` (this project) — Rust stack: `heed` 0.22.1 (custom comparator support), `zstd` 0.13.3, `async-graphql` 7.2.1, `axum` 0.8.x, the LMDB-crate decision matrix, and "do NOT set a comparator when opening Event__* sub-DBs" guidance. ⚠️ Its tech-stack table still lists `rusqlite`/SQLite "derived index" — **stale**, contradicts Approach B ("no SQLite/no derived store"); ignore for Phase 1, flag for cleanup.

### To be vendored during Phase 1 (per D-12)
- `reference/golpe.yaml` (upstream commit SHA) — schema, index key formats, comparator declarations (`golpe.yaml:30-52` per spec).
- `reference/<golpe comparator source>` — the actual `StringUint64`/`Uint64Uint64`/`StringUint64Uint64` C++ implementations to compile + ship.
- Vendored `lmdb++.h` (lmdbxx, header-only) — needed to compile golpe's comparator.

### External (pinned by commit/digest)
- strfry repo (`golpe.yaml`, `src/constants.h: CURR_DB_VERSION = 3`, comparator usage) at the pinned commit.
- golpe repo (comparator generation) at the pinned commit.
- heed 0.22.1 docs — `Comparator` trait, `DatabaseOpenOptions::key_comparator`, `EnvFlags::READ_ONLY`/`NO_LOCK` (Research item #1).

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **None in this repo** — greenfield Rust project (no `Cargo.toml`, no `.rs` files yet). Phase 1 establishes the crate.
- **Parent repo infra** — `../config/strfry/strfry.conf`, `../Dockerfile.strfry`, `../docker-compose.strfry.yml` provide the strfry deployment to pin against and to generate fixtures from.

### Established Patterns
- **DeepFry config convention:** per-component YAML under `~/deepfry/` (e.g. `~/deepfry/whitelist.yaml`, `~/deepfry/web-of-trust.yaml`). D-14 follows this with `~/deepfry/lmdb2graphql.yaml`. **Do not delete/overwrite files in `~/deepfry/`** (per CLAUDE.md) — use a temp dir when testing config loading.
- **Alpine static binary builds** are the DeepFry deployment norm (Go subsystems use `CGO_ENABLED=0` static Alpine builds); LMDB2GraphQL's equivalent is a static Rust+C++ musl build (D-02).

### Integration Points
- The self-check startup gate (D-04, D-13) is the integration point Phase 5's `/ready` endpoint will call — build it as a callable function, not inline in `main`.
- Pinned-version contract (`~/deepfry/lmdb2graphql.yaml`) is the shared coupling point with the parent stack (D-08/D-09).

</code_context>

<specifics>
## Specific Ideas

- **Prove heed's comparator hook FIRST, smallest possible** (Research item #1 / go-no-go): open one `Event__` index read-only, register one `Comparator`, scan, verify order — before building env-gate/config/fixture plumbing. This is the genuine kill-switch; if it fails, Approach B must be revisited (per STATE.md).
- The C++ artifact carries **option-value as a fallback escape hatch**: if heed's `Comparator` hook ever proves unusable, the documented fallback is dropping to a lower-level lmdb binding and calling `mdb_set_compare` directly with golpe's C function pointer.
- `Event__created_at` is a plain integer-key index (no custom comparator); the three custom comparators map to: `StringUint64` → `Event__id`/`Event__pubkey`/`Event__tag`; `Uint64Uint64` → `Event__kind`; `StringUint64Uint64` → `Event__pubkeyKind`.

</specifics>

<deferred>
## Deferred Ideas

- **Doc-sync follow-up (not code):** update LMDB-05 wording in REQUIREMENTS.md, the PROJECT.md Key Decisions row, and project CLAUDE.md to reflect "FFI to golpe's comparator" instead of "reimplement in Rust" (D-03). Best handled as a doc-update pass; planner should at minimum not be confused by the stale wording.
- **CLAUDE.md cleanup:** remove/correct the stale `rusqlite`/SQLite "derived index" entries that contradict Approach B. Not Phase 1's job.
- **OPS-04 (Phase 5):** richer pinned-vs-detected version drift surfacing in `stats`/startup output — only a basic startup log line is in Phase 1 scope.
- **Cross-arch / big-endian (v2, PORT-01):** explicitly out of scope; co-located little-endian assumed and asserted.

### Research questions for gsd-phase-researcher
- **R1 (go/no-go):** Confirm heed 0.22.1's `Comparator` trait + `DatabaseOpenOptions::key_comparator` (a) works on an *existing* sub-DB opened *without* `MDB_CREATE`, (b) passes the comparator raw key bytes (`MDB_val` → `&[u8]`), (c) supports a *distinct* comparator per dbi within one read txn, (d) interacts correctly with `MDB_RDONLY` (+ `NO_LOCK` for CI fixtures).
- **R2:** Confirm golpe's generated comparator compiles against vendored `lmdb++.h` with minimal/no golpe-runtime dependencies; identify the exact generated file + source lines; resolve the musl/Alpine static-link story (`cc` crate, static libstdc++).
- **R3:** How to resolve `dockurr/strfry:latest` → sha256 digest + strfry git commit; confirm that build is `dbVersion == 3`.
- **R4:** Deterministic mechanism to generate the fixture `data.mdb` from the pinned image by ingesting the adversarial seed events through strfry.

</deferred>

---

*Phase: 1-LMDB Foundation & Comparator Proof*
*Context gathered: 2026-06-10*
