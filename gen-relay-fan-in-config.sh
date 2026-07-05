#!/usr/bin/env bash
set -euo pipefail

# gen-relay-fan-in-config.sh — generate a standalone streams-only `strfry router`
# config from the Web-of-Trust relay list.
#
# Reads the same `relay_urls:` list `stream-relays.sh` parses from
# ~/deepfry/web-of-trust.yaml and emits ~/deepfry/strfry-relay-fan-in.config: a
# taocpp/config++ file whose only top-level key is `streams`, with one `ingest`
# stream carrying every relay URL and a MANDATORY per-stream
# `pluginDown = "/app/plugins/bloom"` gate (the global writePolicy does NOT gate
# router-down events, so omitting it would silently bypass the whitelist).
#
# Host-run operator script (arm64 deploy host). Runs on demand; NOT a container
# entrypoint. The empty-list path runs WoT discovery then re-reads, and fails
# hard (no config emitted) if the list is still empty — there is NO hardcoded
# fallback relay set (the WoT list is the single source of truth).
#
# Env knobs (overridable — tests point these at a temp HOME):
#   WOT_CONFIG     default ${HOME}/deepfry/web-of-trust.yaml   (input, read-only)
#   FAN_IN_CONFIG  default ${HOME}/deepfry/strfry-relay-fan-in.config (ONLY file written)
#   WOT_DIR        default web-of-trust                         (make -C target dir)

WOT_CONFIG="${WOT_CONFIG:-${HOME}/deepfry/web-of-trust.yaml}"
FAN_IN_CONFIG="${FAN_IN_CONFIG:-${HOME}/deepfry/strfry-relay-fan-in.config}"
WOT_DIR="${WOT_DIR:-web-of-trust}"

# In-container path to the bloom whitelist plugin (Phase 2 bind-mount target).
BLOOM_PLUGIN="/app/plugins/bloom"

RELAYS=()

# Extract the `relay_urls:` YAML list from the web-of-trust config. Reads list
# items (indented `- value` lines) until the next top-level key. Strips inline
# quotes/comments. No external YAML dependency required. Returns 0 only when at
# least one relay was collected. Ported verbatim from stream-relays.sh:37-63.
load_relays_from_config() {
  [[ -f "$WOT_CONFIG" ]] || return 1
  local line in_list=0 relay saw_item=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" =~ ^relay_urls: ]]; then
      in_list=1
      continue
    fi
    if (( in_list )); then
      if [[ "$line" =~ ^[[:space:]]+-[[:space:]]+(.+)$ ]]; then
        saw_item=1
        relay="${BASH_REMATCH[1]}"
        # Strip a genuine trailing inline comment only when a whitespace
        # precedes the '#'. A bare '#' inside an unquoted URL (fragment
        # identifier, e.g. wss://relay.example/path#room) is preserved (WR-02).
        relay="${relay%%[[:space:]]#*}"
        relay="${relay#[\"\']}"; relay="${relay%[\"\']}"  # strip one boundary quote
        relay="${relay%"${relay##*[![:space:]]}"}" # rtrim
        if [[ -n "$relay" ]]; then
          # CR-01 (T-01-01): relay URLs originate from the WoT discovery black
          # box which derives from untrusted network-sourced lists. emit_config
          # wraps each value in double-quotes verbatim, so a value containing an
          # embedded '"', whitespace, '{', '}', '=' or backslash could break out
          # of the taocpp string and inject a rogue pluginDown/db/relay key —
          # the ING-02 whitelist bypass. VALIDATE and reject (fail hard per the
          # D-06 fail-hard philosophy) any URL that is not a clean ws://|wss://
          # URL. Never emit an unvalidated URL.
          if [[ "$relay" =~ ^wss?://[A-Za-z0-9._~:/?#@!$\&\'\(\)*+,\;=%-]+$ ]]; then
            RELAYS+=("$relay")
          else
            echo "FATAL: rejecting malformed/unsafe relay URL: ${relay}" >&2
            return 1
          fi
        fi
      elif [[ "$line" =~ ^[[:space:]]*$ ]]; then
        continue                                   # blank line within list
      else
        break                                      # next top-level key
      fi
    fi
  done < "$WOT_CONFIG"
  # WR-03: distinguish "relay_urls present but every entry was blank/invalid"
  # from "no list at all". A degenerate list (e.g. only `- ""` items) silently
  # parses like an absent list and triggers discovery + a config rewrite; emit a
  # diagnostic so an operator typo does not quietly kick off a network run.
  if (( in_list && saw_item && ${#RELAYS[@]} == 0 )); then
    echo "WARN: relay_urls present but all entries were empty/invalid" >&2
  fi
  (( ${#RELAYS[@]} > 0 ))
}

# Populate RELAYS. If the config already lists relays, use them. If empty, run
# WoT discovery (make -C "$WOT_DIR" run-discover-relays — a black box that reads
# and writes web-of-trust.yaml itself) and re-read. If STILL empty, fail hard:
# emit NO config and exit non-zero. NEVER fall back to a hardcoded list (D-06,
# CFG-01) — a stale/wrong ingest set is worse than none.
resolve_relays() {
  if load_relays_from_config; then
    echo "Loaded ${#RELAYS[@]} relays from ${WOT_CONFIG}."
    return 0
  fi
  echo "No relays in ${WOT_CONFIG}; running WoT discovery..."
  RELAYS=()                                        # reset before re-read
  make -C "$WOT_DIR" run-discover-relays
  RELAYS=()                                        # discard anything partial
  if load_relays_from_config; then
    echo "Discovery populated ${#RELAYS[@]} relays."
    return 0
  fi
  echo "FATAL: relay list still empty after discovery; emitting no config." >&2
  exit 1
}

# Emit the streams-only taocpp/config++ router file to $FAN_IN_CONFIG from
# RELAYS. The ONLY top-level key is `streams` — NO `db`/`relay` keys (strfry
# reads the LMDB path from the global --config; golpe silently discards unknown
# top-level keys, and a `db`-bearing positional file breaks router load). The
# single `ingest` stream sets dir="down", filter={} (match all; implicit limit:0
# suppresses backfill), and pluginDown to the bloom whitelist plugin — MANDATORY,
# because the global writePolicy does NOT gate router-down events. `urls` is a
# whitespace/newline-separated array with NO commas (taocpp array syntax).
#
# Writes ONLY $FAN_IN_CONFIG (D-09): builds a temp file beside it, then mv's it
# into place atomically — never touching any other ~/deepfry/ file.
emit_config() {
  local tmp url
  tmp="$(mktemp "${FAN_IN_CONFIG}.XXXXXX")"
  # WR-01 (D-09): under `set -e`, a mid-write failure (ENOSPC, permission
  # change, interrupt) would abort the script and orphan the temp file in the
  # protected ~/deepfry/ dir. Register a cleanup trap scoped to this function so
  # the temp is removed on any exit path; the successful `mv` below consumes it,
  # after which we clear the trap.
  trap 'rm -f "$tmp"' RETURN

  {
    echo "connectionTimeout = 20"
    echo "verbose = true"
    echo ""
    echo "streams {"
    echo "    ingest {"
    echo "        dir        = \"down\""
    echo "        pluginDown = \"${BLOOM_PLUGIN}\""
    echo "        filter     = {}"
    echo ""
    echo "        urls = ["
    for url in "${RELAYS[@]}"; do
      echo "            \"${url}\""
    done
    echo "        ]"
    echo "    }"
    echo "}"
  } > "$tmp"

  mv "$tmp" "$FAN_IN_CONFIG"
  trap - RETURN
  echo "Wrote ${#RELAYS[@]} relays to ${FAN_IN_CONFIG}."
}

cmd_generate() {
  resolve_relays
  emit_config
}

# Dispatcher — only runs when the script is executed directly, not when sourced
# (tests source it to exercise individual functions).
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-generate}" in
    generate) cmd_generate ;;
    *) echo "Usage: $0 [generate]" >&2; exit 1 ;;
  esac
fi
