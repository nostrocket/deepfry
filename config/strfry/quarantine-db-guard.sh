#!/bin/sh
# quarantine-db-guard.sh — refuse to start strfry if the configured db path
# could collide with the mainline strfry's db. See quarantine/SPEC.md §6.8.3.
#
# Exit codes:
#   0 — all checks passed, exec'd the wrapped command
#   1 — could not determine db path from config, or required env var missing
#   2 — configured path equals the forbidden mainline path (identity check)
#   3 — configured path does not equal the expected quarantine path (drift check)
#   4 — the forbidden mainline path is visible inside this container (mount leak)

set -eu

CONF="${STRFRY_CONF:-/etc/strfry.conf}"
EXPECTED="${QUARANTINE_EXPECTED_DB:?QUARANTINE_EXPECTED_DB is required}"
FORBIDDEN="${MAINLINE_DB_PATH:?MAINLINE_DB_PATH is required}"

# Extract the db = "..." value (first uncommented match).
CONFIGURED=$(
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*db[[:space:]]*=/ {
      match($0, /"[^"]*"/)
      if (RSTART > 0) {
        print substr($0, RSTART + 1, RLENGTH - 2)
        exit
      }
    }
  ' "$CONF"
)

if [ -z "$CONFIGURED" ]; then
  echo "FATAL: could not determine db path from $CONF" >&2
  exit 1
fi

# Normalize: add trailing slash, resolve symlinks / .. when the dir exists.
normalize() {
  p="$1"
  case "$p" in */) ;; *) p="$p/" ;; esac
  d=$(dirname "$p")
  b=$(basename "$p")
  if [ -d "$d" ]; then
    printf '%s/%s/\n' "$(cd "$d" && pwd -P)" "$b"
  else
    printf '%s\n' "$p"
  fi
}

CONFIGURED_N=$(normalize "$CONFIGURED")
EXPECTED_N=$(normalize "$EXPECTED")
FORBIDDEN_N=$(normalize "$FORBIDDEN")

if [ "$CONFIGURED_N" = "$FORBIDDEN_N" ]; then
  echo "FATAL: quarantine db path ($CONFIGURED_N) matches mainline db path ($FORBIDDEN_N). Refusing to start." >&2
  exit 2
fi

if [ "$CONFIGURED_N" != "$EXPECTED_N" ]; then
  echo "FATAL: quarantine db path ($CONFIGURED_N) does not match expected ($EXPECTED_N). Refusing to start." >&2
  exit 3
fi

# Defense in depth: the mainline path must not exist inside this container at all.
if [ -e "$FORBIDDEN_N" ]; then
  echo "FATAL: mainline db path ($FORBIDDEN_N) is visible inside the quarantine container. Refusing to start." >&2
  exit 4
fi

echo "quarantine-db-guard: ok (db=$CONFIGURED_N)"
exec "$@"
