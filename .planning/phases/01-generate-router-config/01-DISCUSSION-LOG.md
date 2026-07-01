# Phase 1: Generate Router Config - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-02
**Phase:** 1-generate-router-config
**Areas discussed:** Generator form, Empty-list behavior, Output path & bind-mount, Static vs regenerated, Discovery invocation, Where generation runs

---

## Generator form

| Option | Description | Selected |
|--------|-------------|----------|
| New standalone script | New gen script reusing the WoT-YAML relay parser; relay-loader survives Phase 4's retirement of stream-relays.sh | ✓ |
| Subcommand on stream-relays.sh | Add `generate` subcommand reusing load_relays_from_config in place; would need to move later | |

**User's choice:** New standalone script
**Notes:** Aligns with Phase 4 retiring stream-relays.sh — the surviving logic belongs in the generator.

---

## Empty-list behavior

| Option | Description | Selected |
|--------|-------------|----------|
| Fail-hard (no fallback) | Emit no config, exit non-zero; strict single-source-of-truth | |
| Keep hardcoded fallback | Fall back to the 20 hardcoded relays | |
| (free text) Run WoT discovery, then retry | Invoke the WoT relay discovery tool to repopulate the list, then re-read | ✓ |

**User's choice:** "Run the WoT relay discovery tool to find relays and then try again"
**Notes:** Refined into: run discovery → re-read → fail-hard if still empty (D-05/D-06). Never silently substitutes a hardcoded set; keeps the WoT list as the one source of truth.

---

## Discovery invocation (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Prebuilt binary, then fail-hard | Call web-of-trust/bin/discover-relays, re-read, fail-hard | |
| go run via make, then fail-hard | `make -C web-of-trust run-discover-relays` (fresh build), re-read, fail-hard | ✓ |
| Discover, else hardcoded fallback | Fall back to hardcoded relays if discovery fails | |

**User's choice:** go run via make, then fail-hard
**Notes:** Fresh build via Go toolchain at call time; no stale binary.

---

## Where generation runs (follow-up)

| Option | Description | Selected |
|--------|-------------|----------|
| Host-side pre-deploy step | Runs on host before/at docker-compose up | (partial) |
| Container entrypoint step | Baked into strfry container startup | |
| (free text) On-demand when the script is called | Operator runs the script explicitly; Dgraph NOT required | ✓ |

**User's choice:** "It runs when we call the script. Dgraph is NOT required."
**Notes:** Verified — discover-relays uses the nostr.watch API + NIP-65 fallback, no Dgraph dependency. Generation is an explicit operator-invoked host step, not auto-at-deploy and not a container entrypoint.

---

## Output path & bind-mount

| Option | Description | Selected |
|--------|-------------|----------|
| config/strfry/strfry-router.config → /app/strfry-router.config | In-repo, committed | |
| Repo root → /app/strfry-router.config | At repo root | |
| (free text) Same location as the web-of-trust config | ~/deepfry/ (beside web-of-trust.yaml) → /app/strfry-router.config | ✓ |

**User's choice:** "Same location as the web of trust config" → `~/deepfry/strfry-router.config`, bind-mounted to `/app/strfry-router.config`
**Notes:** Consistent with "generated fresh at deploy" — a runtime artifact in the DeepFry config dir, not the repo.

---

## Static vs regenerated

| Option | Description | Selected |
|--------|-------------|----------|
| Committed artifact + regen script | File committed to git, bind-mounted | |
| Generated fresh at deploy | Generated at runtime, not committed | ✓ |

**User's choice:** Generated fresh at deploy
**Notes:** Config is ephemeral, always reflects the current WoT list; leverages the router's hot-reload-on-file-change.

---

## Claude's Discretion

- Exact generator script name/location in the repo and internal structure.
- The single stream's section name (`ingest` or similar) — no functional effect.

## Deferred Ideas

- `ROUTER=` entrypoint wiring + proving config load — Phase 2 (also the reviewed-not-folded todo `2026-07-01-run-strfry-router-via-dockur-entrypoint.md`).
- Hot-reload-driven live relay add/remove — Phase 2/3 or later ops capability.
