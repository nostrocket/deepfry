# Phase 1: Generate Router Config - Context

**Gathered:** 2026-07-02
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver a **generator** that emits a standalone, streams-only `strfry router`
config (taocpp / config++ format) from the same Web-of-Trust relay list
`stream-relays.sh` reads, with `pluginDown = "/app/plugins/bloom"` baked into
every stream.

This phase is **config-authoring only**. Wiring the `ROUTER=` entrypoint and
proving the config loads is Phase 2; live on-host gating/stability verification
is Phase 3; retiring `stream-relays.sh` is Phase 4. None of those are in scope
here.

</domain>

<decisions>
## Implementation Decisions

### Generator form
- **D-01:** A **new standalone script** (e.g. `gen-router-config.sh`) produces
  the config. It does NOT live as a subcommand on `stream-relays.sh` — that
  script is retired in Phase 4, so the surviving relay-loading logic belongs in
  the generator, not in the doomed fan-out script.
- **D-02:** The generator **ports** `stream-relays.sh`'s relay-list parsing —
  `load_relays_from_config()` reads the `relay_urls:` YAML list from
  `~/deepfry/web-of-trust.yaml` with no external YAML dependency (pure bash
  line parsing). Reuse that approach; do NOT reuse its hardcoded-fallback
  behavior (see D-05).

### Invocation model
- **D-03:** Generation runs **on demand, when the operator calls the script** —
  it is NOT a container entrypoint step and NOT auto-run at deploy. It executes
  on the host (same context `stream-relays.sh` runs in today), so it can read
  `~/deepfry/` and invoke the WoT discovery tool.
- **D-04:** The generator does **NOT require Dgraph**. Confirmed: the WoT
  discovery tool (`discover-relays`) discovers via the nostr.watch API with a
  NIP-65 (kind-10002) fallback and writes back to `web-of-trust.yaml` — it has
  no Dgraph dependency (no `dgo`/dgraph import; see
  `web-of-trust/cmd/discover-relays/main.go`).

### Empty-list behavior
- **D-05:** If `relay_urls:` is empty/missing, the generator **runs WoT
  discovery** via `make -C web-of-trust run-discover-relays` (go run — builds
  fresh, needs the Go toolchain at call time), then **re-reads** the list.
- **D-06:** If the list is **still empty after discovery** (tool errors,
  missing, or returns nothing), the generator **fails hard**: exit non-zero and
  emit NO config. It never falls back to a hardcoded relay list — the WoT relay
  list stays the single source of truth (CFG-01). A stale/wrong ingest set is
  worse than no ingest set.

### Output location & bind-mount
- **D-07 [informational]:** The config is **generated fresh** each run and is
  **NOT committed to git**. It is a runtime artifact. Auto-satisfied by D-08:
  the output path `~/deepfry/strfry-router.config` lives outside the git repo,
  so it is physically un-committable — no executor action required.
- **D-08:** Host output path: **`~/deepfry/strfry-router.config`** — beside
  `web-of-trust.yaml` in the DeepFry config dir. Container path (for the
  Phase 2 `ROUTER=` bind-mount): **`/app/strfry-router.config`** (where the
  strfry binary and plugins live; matches the research example path).
- **D-09:** The generator only ever writes its **own** output file
  (`~/deepfry/strfry-router.config`). It must never delete or overwrite other
  files in `~/deepfry/` (CLAUDE.md config-dir rule). Note: the discovery
  fallback path (D-05) causes `discover-relays` to modify `web-of-trust.yaml` —
  that is that tool's own documented behavior, invoked intentionally, not the
  generator writing there directly.

### Config shape (locked by source-verified research — restated for the planner)
- **D-10:** Standalone `streams { ... }` file. Top-level key is `streams`. It
  must contain **NO** `db`/`relay` keys — golpe silently discards unknown
  top-level keys, so `streams{}` cannot live in the main `--config`; and passing
  a `db`-bearing file as the router positional arg breaks loading.
- **D-11:** **One** stream (e.g. named `ingest`) carrying all relay URLs, since
  every relay shares one policy: `dir = "down"`, `filter = {}` (match ALL —
  omitting/`{}` pulls everything, an implicit `limit:0` suppresses backfill),
  `pluginDown = "/app/plugins/bloom"`.
- **D-12:** `urls` is a **whitespace/newline-separated** array — **NO commas**
  (taocpp array syntax: `urls = [ "wss://a" "wss://b" ]`).
- **D-13:** `pluginDown` is **mandatory on the stream** — the global
  `writePolicy` does NOT gate router-down events; omitting `pluginDown` silently
  bypasses the whitelist (release blocker, ING-02 config-side).

### Claude's Discretion
- Exact generator script name/location in the repo, internal structure, and
  logging/UX — planner/executor's call, consistent with existing repo scripts.
- Whether the single stream is named `ingest` or another clear name (no
  functional effect — the section name is the internal group name).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Router config schema & gating (source-verified — read FIRST)
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-RESEARCH.md`
  — source-verified (`f31a1b9`) router config schema (`streams`, `dir`, `urls`,
  `filter`, `pluginDown`, `pluginUp`), the two-files separation
  (global `--config` vs positional router config), the `writePolicy`≠`pluginDown`
  gating fact, and a full example config. **Read the bottom `CORRECTION` section
  first.** This is the authoritative spec for Phase 1's output shape.
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-PLAN.md`
  — ~9-variant host investigation + the config-load BLOCKER (primarily Phase 2,
  but explains why the file shape/separation matters).
- `.planning/quick/260701-l5s-convert-stream-relays-sh-to-a-single-str/260701-l5s-SUMMARY.md`
  — BLOCKED outcome; state-left-clean notes.

### Relay-list source & parsing to port
- `stream-relays.sh` — `load_relays_from_config()` / `resolve_relays()`: the
  `relay_urls:` YAML parser to port (reuse the parse, drop the fallback).
- `~/deepfry/web-of-trust.yaml` — the WoT relay list (single source of truth;
  `relay_urls:` at line 43). Host file, not in the repo.

### WoT discovery tool (black-box dependency — do NOT modify)
- `web-of-trust/cmd/discover-relays/main.go` — flags (`--count`, `--replace`,
  `--dry-run`), nostr.watch-API-then-NIP-65 discovery, writes fastest relays
  into `relay_urls:`. Requires the config file to already exist.
- `web-of-trust/Makefile` — `run-discover-relays` target (the invocation D-05
  uses).

### Global strfry config (context — the router file is separate from this)
- `config/strfry/strfry.conf` — the golpe global config that supplies the LMDB
  `db` path. The router config must NOT duplicate `db`; strfry reads it from
  here via `--config`.

### Project rules
- `CLAUDE.md` — DB-location HARD RULE (never move the LMDB); `~/deepfry/`
  config-dir protection; project-boundary rule.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `stream-relays.sh::load_relays_from_config()` — dependency-free bash parser
  for the `relay_urls:` list in `~/deepfry/web-of-trust.yaml`. Port this into
  the new generator.
- `web-of-trust` `discover-relays` binary / `make run-discover-relays` — the
  discovery tool invoked when the relay list is empty (D-05). Called as a
  black box; not modified by this project.

### Established Patterns
- Host-run operator scripts (`stream-relays.sh`, `config/strfry/*.sh`) execute
  on the arm64 Mac mini deploy host with `docker`/`docker exec` access — the
  generator follows the same host-run pattern.
- DeepFry runtime config lives in `~/deepfry/` (e.g. `web-of-trust.yaml`); the
  generated router config joins it there.

### Integration Points
- Output `~/deepfry/strfry-router.config` → bind-mounted to
  `/app/strfry-router.config` in the strfry container; consumed by the Phase 2
  `ROUTER=` entrypoint wiring in `docker-compose.strfry.yml`.
- `pluginDown = "/app/plugins/bloom"` — the in-container bloom whitelist plugin
  path the generated stream references.

</code_context>

<specifics>
## Specific Ideas

- Empty-list recovery is intentionally an active refresh (run discovery) rather
  than a passive fallback — the operator's explicit preference: keep the WoT
  list as the one true source, never silently substitute a hardcoded set.
- Config is deliberately ephemeral (regenerated, not committed) so it always
  reflects the current WoT list; leverages the router's hot-reload-on-file-change
  behavior noted in RESEARCH.md.

</specifics>

<deferred>
## Deferred Ideas

- **Wiring the `ROUTER=` entrypoint** (`docker-compose.strfry.yml`) and proving
  `Loading router config file:` appears — Phase 2.
- **Hot-reload-driven relay add/remove** (rewrite the config file live, no
  restart) — an ops capability that builds on this generator; belongs to
  Phase 2/3 verification or later, not config authoring.

### Reviewed Todos (not folded)
- `2026-07-01-run-strfry-router-via-dockur-entrypoint.md` — "Run strfry router
  via dockur `ROUTER=` entrypoint (replace 174-stream fan-out)". Matched on
  keywords (score 0.6) but its actual scope is the `ROUTER=` entrypoint wiring,
  which is **Phase 2**, not config generation. Deferred to Phase 2.

</deferred>

---

*Phase: 1-generate-router-config*
*Context gathered: 2026-07-02*
