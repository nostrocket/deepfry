---
phase: 04-retire-the-fan-out
reviewed: 2026-07-02T00:00:00Z
depth: standard
files_reviewed: 1
files_reviewed_list:
  - README.md
findings:
  critical: 0
  warning: 1
  info: 1
  total: 2
status: issues_found
---

# Phase 04: Code Review Report

**Reviewed:** 2026-07-02
**Depth:** standard
**Files Reviewed:** 1
**Status:** issues_found

## Summary

This phase changed only `README.md` — the "Useful commands" section (lines 147-172) was
rewritten to replace the deleted `stream-relays.sh` tmux fan-out block with documentation
of the single `strfry router` ingestion path.

I verified every factual claim in the changed block against ground truth in the repo
(`docker-compose.strfry.yml`, `gen-router-config.sh`, `config/strfry/strfry.conf`). **The
changed block itself is accurate** — no broken commands, no references to deleted files,
no incorrect instructions:

- `strfry router` process started via `ROUTER` entrypoint env var pointing at
  `/app/strfry-router.config` — confirmed (`docker-compose.strfry.yml:20-26`).
- Auto-started with the stack, no separate start/attach/stop command — confirmed (no
  separate router entrypoint; router runs alongside `strfry relay`).
- `gen-router-config.sh` (host-run) rebuilds `~/deepfry/strfry-router.config` from the
  `relay_urls:` list in `~/deepfry/web-of-trust.yaml` — confirmed
  (`gen-router-config.sh:4-26`, `WOT_CONFIG`/`ROUTER_CONFIG` defaults).
- Regenerated config requires operator to reload/recreate strfry — confirmed
  (`docker-compose.strfry.yml:25`: "changing ROUTER requires a container recreate").
- Both `./gen-router-config.sh` and `./gen-router-config.sh generate` are valid — confirmed
  (`gen-router-config.sh:163-165`, `case "${1:-generate}"`).
- **Zero occurrences of the literal `stream-relays` in README.md** — confirmed (grep exit 1).
  The phase's stated hard requirement is met.

The findings below are both about **stale content elsewhere in the same file** that the new
router documentation now contradicts. The WARNING is a genuine, provable factual error in the
README; it is pre-existing (unchanged by this phase) but is elevated in importance because this
phase's changes now describe the router/ingestion path prominently, making the stale claim in
the Configuration Reference an active contradiction a reader will hit.

## Warnings

### WR-01: README claims `/app/plugins/router` is the active writePolicy plugin at `strfry.conf:117`, but the repo has `/app/plugins/bloom` active at line 140

**File:** `README.md:226`, `README.md:282`
**Issue:** The Configuration Reference states, twice, that the active writePolicy plugin is
the router plugin:

- Line 226: "Selected by `plugin = "/app/plugins/router"` at `config/strfry/strfry.conf:117`."
- Line 282: table row `writePolicy.plugin` = `/app/plugins/router` (line 117).

Ground truth in `config/strfry/strfry.conf`:
- The active line is `plugin = "/app/plugins/bloom"` at **line 140** (verified).
- Line 117 is a **comment** (`#     Same accept/reject decision as the whitelist plugin, and`),
  not the plugin selector. There is no `plugin = "/app/plugins/router"` assignment anywhere
  in the file.
- The header comment at line 123 explicitly annotates `/app/plugins/bloom  (active)`.

This is a direct contradiction with the repo and is made worse by this phase: the new
"Useful commands" block documents the `strfry router` ingestion path, whose generated config
(`gen-router-config.sh:138`) wires `pluginDown = "/app/plugins/bloom"`. So the ingestion path
the phase now documents actually depends on the **bloom** plugin, while the Configuration
Reference two sections down insists the **router** plugin is active at a line number that
doesn't hold the selector. A reader following the docs to flip or verify the plugin will edit
the wrong line and reach the wrong conclusion about which plugin gates writes.

**Fix:** Update both references to match the repo:
```markdown
| `writePolicy.plugin` | `/app/plugins/bloom` (line 140) | Selects the bloom plugin (locally-held bloom filter of whitelisted pubkeys; no per-event HTTP). Swap to `/app/plugins/whitelist` or `/app/plugins/router` by editing this line. |
```
And at line 226, either correct the selector/line number (`plugin = "/app/plugins/bloom"` at
`config/strfry/strfry.conf:140`) or reword the router-plugin section to describe it as an
available (not currently-active) option.

## Info

### IN-01: New router-plugin narrative and `strfry-router.config` mount default may drift from the `~/deepfry/` config convention documented elsewhere

**File:** `README.md:33` (compose reference), `docker-compose.strfry.yml:33`
**Issue:** The changed block correctly states the router config lives at
`~/deepfry/strfry-router.config`. The compose file's default host path
(`docker-compose.strfry.yml:33`) is `${STRFRY_ROUTER_CONFIG_PATH:-~/deepfry/strfry-router.config}`,
which agrees. However, the Configuration Reference section (lines 176-182) enumerates the three
config kinds (`.env`, `~/deepfry/*.yaml`, `config/**/*`) and does **not** mention
`~/deepfry/strfry-router.config` — a fourth, generated, non-`.yaml` file under `~/deepfry/`.
Not an error in the changed block, but the phase introduced prominent references to this file
without adding it to the config taxonomy, so a reader building a mental model of "what lives in
`~/deepfry/`" will find the router config undocumented in the reference.

**Fix:** Add a one-line note to the Configuration Reference (near line 181) that
`~/deepfry/strfry-router.config` is a generated (non-committed) streams-only StrFry router file
produced by `gen-router-config.sh`, so the config taxonomy is complete.

---

_Reviewed: 2026-07-02_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
