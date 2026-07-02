---
phase: 01
plan: 01
subsystem: strfry-router-conversion
status: complete
tags: [config-generator, strfry-router, taocpp, bloom-gating, wot-relays]
dependency_graph:
  requires: []
  provides:
    - "gen-router-config.sh (host-run generator: WoT relay list -> streams-only router config)"
    - "~/deepfry/strfry-router.config (runtime artifact; Phase 2 ROUTER= bind-mount target)"
    - "load_relays_from_config() / resolve_relays() / emit_config() bash functions"
  affects:
    - "Phase 2 (ROUTER= entrypoint wiring in docker-compose.strfry.yml)"
tech_stack:
  added: [bats-core (test harness, dev-only)]
  patterns: [host-run operator bash script, source-guarded dispatcher, atomic mktemp+mv write]
key_files:
  created:
    - gen-router-config.sh
    - test/gen-router-config.bats
  modified: []
decisions:
  - "Emit streams-only taocpp file (no top-level db/relay) — global --config owns db (D-10)"
  - "pluginDown = /app/plugins/bloom mandatory on the ingest stream (D-13, ING-02 blocker)"
  - "urls array whitespace/newline separated, NO commas (D-12)"
  - "No hardcoded fallback relay set; empty list -> WoT discovery -> re-read -> fail hard (D-06)"
  - "Atomic write via mktemp+mv; script only ever writes $ROUTER_CONFIG (D-09)"
metrics:
  duration_min: 3
  tasks_completed: 2
  files_changed: 2
  tests: "13/13 bats cases pass"
  completed: 2026-07-02
---

# Phase 1 Plan 01: Generate Router Config Summary

Standalone host-run `gen-router-config.sh` that parses the Web-of-Trust `relay_urls:` list and emits a streams-only `strfry router` config (`~/deepfry/strfry-router.config`) with a mandatory per-stream `pluginDown = "/app/plugins/bloom"` bloom gate, fail-hard-on-empty WoT discovery, and no hardcoded fallback.

## What Was Built

- **`gen-router-config.sh`** (repo root, executable, 134 lines):
  - `load_relays_from_config()` — dependency-free bash parser ported verbatim from `stream-relays.sh:37-63`. Reads the `relay_urls:` YAML list from `$WOT_CONFIG`, strips inline comments and quotes, rtrims, stops at the next top-level key, returns 0 only when ≥1 relay collected.
  - `resolve_relays()` — replaces the analog's hardcoded-fallback branch: use the WoT list if populated; else run `make -C "$WOT_DIR" run-discover-relays` (black-box discovery tool that manages `web-of-trust.yaml` itself) and re-read; if still empty, write `FATAL:` to stderr and `exit 1` emitting NO config. No `FALLBACK_RELAYS` array anywhere.
  - `emit_config()` — templates the taocpp/config++ file to `$ROUTER_CONFIG`: only top-level key is `streams`, one `ingest` stream with `dir = "down"`, `pluginDown = "/app/plugins/bloom"`, `filter = {}`, and a whitespace/newline-separated comma-free `urls` array of every relay. Harmless top-level scalars `connectionTimeout`/`verbose` included per the source-verified template. Writes to a `mktemp` file then `mv`s into place — only ever targets `$ROUTER_CONFIG`.
  - Env knobs `WOT_CONFIG` / `ROUTER_CONFIG` / `WOT_DIR` (all `${VAR:-default}`); source-guarded `case` dispatcher (`generate`) so tests can source the file to exercise individual functions.

- **`test/gen-router-config.bats`** (187 lines, 13 cases): parser (3-relay parse with quote/comment strip, stop-at-top-level-key, empty/missing fail), resolve/discover/fail-hard (no discovery when populated, discover+re-read when empty via a stub `make`, fail-hard when still empty), and emission shape (streams-only with no storage/relay top-level key, exactly one pluginDown bloom line, dir=down + empty filter, comma-free urls of all input relays, writes only `$ROUTER_CONFIG` leaving siblings untouched, fail-hard writes no config). All cases run against a temp `HOME` — the real `~/deepfry/` is never touched.

## Verification Evidence

- `bash -n gen-router-config.sh` passes; `grep` confirms `load_relays_from_config` and `run-discover-relays` present, `FALLBACK_RELAYS` absent → `PARSER_OK`.
- `bats test/gen-router-config.bats` → **13/13 pass**.
- Emitted config against a 2-relay fixture inspected directly: matches the source-verified RESEARCH.md template byte-shape (streams-only, one `ingest`, `dir="down"`, one `pluginDown` bloom line, `filter={}`, whitespace-separated comma-free `urls`). No top-level `db`/`relay` key present.
- Artifacts exceed `min_lines` (script 134 ≥ 60, test 187 ≥ 30) and the `pluginDown` / `run-discover-relays` / `pluginDown.*bloom` `contains`/`key_links` patterns are present.

## Threat Mitigations Applied

| Threat ID | Mitigation | Evidence |
|-----------|-----------|----------|
| T-01-01 (EoP: ungated stream) | `pluginDown = "/app/plugins/bloom"` mandatory on the stream | Test 9 asserts exactly one bloom pluginDown line |
| T-01-02 (Tampering: empty/poisoned list) | Empty list → WoT discovery → re-read → fail hard, NO hardcoded fallback | Tests 7 & 13 assert non-zero exit and no config written |
| T-01-03 (DoS: streams in global config) | Standalone streams-only file, NO top-level db/relay | Test 8 asserts streams present, storage/relay keys absent |
| T-01-04 (Tampering: protected config dir) | Writes only `$ROUTER_CONFIG` (atomic mktemp+mv); never seeds web-of-trust.yaml | Test 12 asserts sibling file untouched |

## Deviations from Plan

None — plan executed as written. One in-test assertion was corrected during Task 1 GREEN (a bats negative-array-index `${lines[-2]}` that errors under `set -u`-style array bounds was replaced with a substring match on `$output`); this fixed the test harness, not the generator behavior, and is not a plan deviation.

## Environment Notes

- `bats` was NOT preinstalled on this macOS host; installed `bats-core` 1.13.0 via Homebrew and executed the suite (13/13 pass). Per the plan's environment note, `bats` is a dev-only test-harness tool and does not violate threat T-01-SC (which concerns the *generated script's own* runtime dependencies — the script has none beyond bash + `make`/Go at discovery time).
- All test runs used temp `HOME`/temp `WOT_CONFIG`/`ROUTER_CONFIG` paths; the real `~/deepfry/` directory was never read or written (D-09, CLAUDE.md config-dir rule).

## Requirements Satisfied

- **CFG-01** — WoT `relay_urls:` list is the single source of truth; no second hand-maintained list (no `FALLBACK_RELAYS`).
- **ING-02 (config-side)** — every ingestion path carries `pluginDown = "/app/plugins/bloom"`; router-down events cannot bypass the whitelist.

## For Downstream Phases

- Phase 2 bind-mounts `~/deepfry/strfry-router.config` → container `/app/strfry-router.config` and wires it via dockur's `ROUTER=` env var. This plan only authors the config; it does NOT wire the entrypoint or prove the config loads (Phase 2) or verify live gating/stability (Phase 3).
- The `db` path is intentionally absent from the router config — strfry reads it from the global `config/strfry/strfry.conf` via `--config`. Do NOT add `db`/`relay` keys to the generated file.

## Commits

- `9cc0bc9` test(01-01): add failing tests for relay parser and resolve/discover/fail-hard (RED)
- `5519680` feat(01-01): implement relay parser and resolve/discover/fail-hard flow (GREEN)
- `b42810f` test(01-01): add failing tests for streams-only taocpp emission (RED)
- `a45aaed` feat(01-01): emit streams-only taocpp router config with bloom gating (GREEN)

## Self-Check: PASSED
