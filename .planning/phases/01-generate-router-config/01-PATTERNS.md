# Phase 1: Generate Router Config - Pattern Map

**Mapped:** 2026-07-02
**Files analyzed:** 1 new file (`gen-router-config.sh`)
**Analogs found:** 1 structural analog (partial) + 2 supporting refs / 1 total new file

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `gen-router-config.sh` (new, repo root) | config-generator (host-run operator script) | transform (read YAML relay list → emit taocpp config file) + file-I/O | `stream-relays.sh` (repo root) | role-match / partial — analog *spawns processes*, generator *writes a config file* |

**Why partial:** No existing in-repo script generates a config file. `stream-relays.sh` is the closest structural + domain analog: same host-run operator context, same relay-list source, same YAML-parse logic to port — but it fans out one `docker exec ... strfry stream` per relay instead of emitting a file. The generator REUSES the parser, DROPS the process fan-out and the hardcoded fallback, and ADDS file emission + WoT-discovery-on-empty (D-05/D-06).

## Pattern Assignments

### `gen-router-config.sh` (config-generator, transform + file-I/O)

**Primary analog:** `/Users/g/git/deepfry/stream-relays.sh`
**Supporting refs:** `config/strfry/quarantine-db-guard.sh` (host-run bash idioms), `web-of-trust/Makefile` + `web-of-trust/cmd/discover-relays/main.go` (D-05 discovery invocation), RESEARCH.md "Minimal WORKING router config" + CORRECTION section (output shape).

---

#### 1. Shebang + strict-mode + config-path header (PORT from `stream-relays.sh` lines 1-8)

```bash
#!/usr/bin/env bash
set -euo pipefail

CONTAINER="strfry"

WOT_CONFIG="${WOT_CONFIG:-${HOME}/deepfry/web-of-trust.yaml}"
```

- Keep `set -euo pipefail` and the `WOT_CONFIG` env-overridable default — both are the established repo convention (also lets tests point at a temp `HOME`, per web-of-trust CLAUDE.md testing rule).
- DROP `SESSION`, `STRFRY` (no tmux, no in-container binary invocation for generation).
- ADD an output-path var, e.g. `ROUTER_CONFIG="${ROUTER_CONFIG:-${HOME}/deepfry/strfry-router.config}"` (D-08). This is the ONLY file the generator writes (D-09 — never touch other `~/deepfry/` files).

---

#### 2. Relay-list parser — PORT VERBATIM (`stream-relays.sh` lines 37-63)

This is the load-bearing excerpt to copy. It is a dependency-free bash parser for `relay_urls:` in `~/deepfry/web-of-trust.yaml` (D-02):

```bash
# Extract the `relay_urls:` YAML list from the web-of-trust config. Reads list
# items (indented `- value` lines) until the next top-level key. Strips inline
# quotes/comments. No external YAML dependency required.
load_relays_from_config() {
  [[ -f "$WOT_CONFIG" ]] || return 1
  local line in_list=0 relay
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" =~ ^relay_urls: ]]; then
      in_list=1
      continue
    fi
    if (( in_list )); then
      if [[ "$line" =~ ^[[:space:]]+-[[:space:]]+(.+)$ ]]; then
        relay="${BASH_REMATCH[1]}"
        relay="${relay%%#*}"                       # strip inline comment
        relay="${relay#[\"\']}"; relay="${relay%[\"\']}"  # strip quotes
        relay="${relay%"${relay##*[![:space:]]}"}" # rtrim
        [[ -n "$relay" ]] && RELAYS+=("$relay")
      elif [[ "$line" =~ ^[[:space:]]*$ ]]; then
        continue                                   # blank line within list
      else
        break                                      # next top-level key
      fi
    fi
  done < "$WOT_CONFIG"
  (( ${#RELAYS[@]} > 0 ))
}
```

Declare `RELAYS=()` before use (analog line 35). This function's return value (0 if ≥1 relay) drives the empty-list branch.

---

#### 3. `resolve_relays()` — REPLACE the fallback branch (`stream-relays.sh` lines 65-72 is what NOT to copy)

The analog falls back to `FALLBACK_RELAYS`:

```bash
# stream-relays.sh lines 65-72 — DO NOT PORT THIS FALLBACK
resolve_relays() {
  if load_relays_from_config; then
    echo "Loaded ${#RELAYS[@]} relays from ${WOT_CONFIG}."
  else
    RELAYS=("${FALLBACK_RELAYS[@]}")   # <-- REMOVE: violates D-06 / CFG-01
    echo "No relays in ${WOT_CONFIG}; using ${#RELAYS[@]} hardcoded fallback relays."
  fi
}
```

Per D-05/D-06, replace the `else` branch with: run WoT discovery, RE-READ, then fail hard if still empty. Also DELETE the `FALLBACK_RELAYS=(...)` array (analog lines 12-33). New shape:

```bash
resolve_relays() {
  if load_relays_from_config; then
    echo "Loaded ${#RELAYS[@]} relays from ${WOT_CONFIG}."
    return
  fi
  echo "No relays in ${WOT_CONFIG}; running WoT discovery..."
  RELAYS=()                                   # reset before re-read
  make -C "${WOT_DIR:-web-of-trust}" run-discover-relays   # D-05 (see #4)
  if load_relays_from_config; then
    echo "Discovery populated ${#RELAYS[@]} relays."
  else
    echo "FATAL: relay list still empty after discovery; emitting no config." >&2
    exit 1                                     # D-06 fail-hard, NO fallback
  fi
}
```

---

#### 4. WoT discovery invocation (D-05) — INVOKE AS BLACK BOX

**Makefile target** (`web-of-trust/Makefile` lines 63-65) — do NOT modify:

```make
## Run discover-relays application
run-discover-relays:
	go run $(BUILD_FLAGS) ./cmd/$(APP_DISCOVER)
```

Invocation contract (verified from `web-of-trust/cmd/discover-relays/main.go`):
- `make -C web-of-trust run-discover-relays` runs `go run ./cmd/discover-relays` → **needs the Go toolchain at call time** (builds fresh, not a prebuilt binary).
- The tool reads/writes `~/deepfry/web-of-trust.yaml` via viper (`main.go` lines 174-176: `SetConfigName("web-of-trust")`, `AddConfigPath(filepath.Join(homeDir, "deepfry"))`).
- **It requires the config file to already exist** — `main.go` lines 178-181 `log.Fatal`s with "No config file found" if missing. If the generator can run before the crawler ever created the file, either the generator's own "still empty → exit 1" (D-06) will catch it, or the operator must have `web-of-trust.yaml` present. Do NOT have the generator create/seed that file (D-09).
- Default flags are fine (`--count 50`, merge not `--replace`); D-05 says just run the target. The generator does not pass flags.
- **No Dgraph dependency** (D-04) — confirmed: `discover-relays/main.go` imports only `go-nostr`, `nip11`, `viper`, `yaml` (lines 21-24); no `dgo`/dgraph import. Discovery is nostr.watch-API + NIP-65 fallback.

---

#### 5. Config emission — the NEW behavior (no in-repo analog; shape from RESEARCH.md)

The generator's novel part. Template the taocpp config to `$ROUTER_CONFIG` from `RELAYS[@]`. Authoritative shape is RESEARCH.md "Minimal WORKING router config" (lines 130-147) + the CORRECTION section. Locked details from CONTEXT decisions:

- **Top-level key `streams` only** — NO `db`/`relay` (D-10; golpe silently drops unknown top-level keys, so `db` here would be ignored anyway and putting `streams` in the global file is the documented failure).
- **ONE stream** (name `ingest` or similar, operator discretion) carrying all URLs (D-11).
- `dir = "down"`, `filter = {}`, `pluginDown = "/app/plugins/bloom"` (D-11, D-13 — `pluginDown` is MANDATORY; omitting it silently bypasses the whitelist, release blocker ING-02).
- `urls` array **whitespace/newline-separated, NO commas** (D-12). (RESEARCH note: taocpp accepts commas too and dockur's sample uses commas — but CONTEXT D-12 locks whitespace-separated; follow CONTEXT.)

Target output (from RESEARCH.md lines 130-147):

```
connectionTimeout = 20
verbose = true

streams {
    ingest {
        dir        = "down"
        pluginDown = "/app/plugins/bloom"
        filter     = {}

        urls = [
            "wss://relay.example-1.com"
            "wss://relay.example-2.com"
            # ... every relay from RELAYS[@], whitespace/newline separated, NO commas ...
        ]
    }
}
```

Emission technique: build the `urls` block by iterating `RELAYS[@]` (each as `"    \"$relay\""` on its own line), then write the whole file with the **Write tool at authoring time is N/A** — at runtime the SCRIPT emits it; use a bash heredoc or `printf` **inside the generated script** (this is runtime script output, not agent file authoring, so heredoc is acceptable there). Write to a temp file then `mv` into place, or write directly — but the script must only ever target `$ROUTER_CONFIG` (D-09).

---

#### 6. CLI dispatch + usage (idiom from `stream-relays.sh` lines 137-145)

The analog uses a `case "${1:-...}"` dispatcher. The generator can be single-purpose (no subcommands per Claude's Discretion), but if a dispatcher is kept, follow this idiom:

```bash
case "${1:-generate}" in
  generate) cmd_generate ;;
  *) echo "Usage: $0 [generate]"; exit 1 ;;
esac
```

Logging/UX: match the analog's plain `echo "..."` info lines and `echo "..." >&2` + `exit N` for errors (also see `quarantine-db-guard.sh` lines 32-35, 54-56 for the `FATAL: ... >&2; exit N` convention used by `config/strfry/*.sh`).

---

## Shared Patterns

### Host-run operator script conventions
**Sources:** `stream-relays.sh` lines 1-8, 137-145; `config/strfry/quarantine-db-guard.sh` lines 12-16, 32-35, 54-56
**Apply to:** `gen-router-config.sh`
- `#!/usr/bin/env bash` + `set -euo pipefail` (bash) — `quarantine-db-guard.sh` uses `#!/bin/sh` + `set -eu` because it's a POSIX container entrypoint; the generator is a host bash script, so follow `stream-relays.sh`'s bash form.
- Env-overridable path vars with `${VAR:-default}` (`WOT_CONFIG`, add `ROUTER_CONFIG`).
- Errors to stderr with a `FATAL:`/clear prefix and a distinct non-zero exit code; info to stdout via `echo`.
- Runs on the arm64 Mac mini deploy host (has `docker`, Go toolchain, `~/deepfry/`).

### Config-dir protection (CLAUDE.md HARD RULE)
**Source:** CLAUDE.md "Config Files" section
**Apply to:** `gen-router-config.sh`
- Never delete/overwrite/`rm` files in `~/deepfry/` except the generator's own `strfry-router.config` (D-09). The discovery step modifying `web-of-trust.yaml` is that TOOL's own documented behavior, invoked intentionally — the generator itself must not write there.

### Global-vs-router config separation (RESEARCH.md hard fact)
**Source:** RESEARCH.md CORRECTION section lines 218-330; `config/strfry/strfry.conf` lines 1-6
**Apply to:** the emitted file
- `config/strfry/strfry.conf` line 6 defines `db = "./strfry-db/"` in the GLOBAL golpe config. The router config the generator emits must NOT duplicate `db` (D-10) — strfry reads it from the global `--config`. Mixing them is the exact documented zero-streams failure.

## No Analog Found

No in-repo analog exists for the config-emission behavior itself:

| Aspect | Role | Data Flow | Reason |
|--------|------|-----------|--------|
| taocpp/config++ file emission (the `streams{}` template) | config-generator | file-I/O / transform | No existing script writes a strfry router / taocpp config. Use RESEARCH.md's "Minimal WORKING router config" (lines 130-147) + CORRECTION (lines 257-290) as the authoritative template instead. |
| WoT-discovery-then-re-read-then-fail-hard flow | control-flow | event-driven | `stream-relays.sh` only had a passive hardcoded fallback (the OPPOSITE of D-06). This active-refresh-else-fail flow is new; spec is D-05/D-06. |

## Metadata

**Analog search scope:** repo root (`stream-relays.sh`), `config/strfry/*.sh`, `web-of-trust/cmd/discover-relays/`, `web-of-trust/Makefile`, `config/strfry/strfry.conf`
**Files scanned:** 6 (stream-relays.sh, quarantine-db-guard.sh, strfry.conf, discover-relays/main.go, web-of-trust/Makefile, plus CONTEXT.md + RESEARCH.md upstream)
**Pattern extraction date:** 2026-07-02
