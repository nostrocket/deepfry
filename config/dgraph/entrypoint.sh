#!/bin/bash
#
# Custom entrypoint for the standalone Dgraph image.
#
# Two deliberate differences from the stock image entrypoint:
#
#   1. Dgraph runs as PID 1 (via `exec /run.sh`), so a crash propagates to
#      Docker and `restart: unless-stopped` can actually restart it. The
#      previous version ran Dgraph in the background and held the container
#      open with `tail -f /dev/null`, which masked every Dgraph crash — the
#      container stayed "Up" while the database was dead and the restart
#      policy never fired.
#
#   2. The GraphQL schema is only (re)loaded when it has actually changed.
#      Re-POSTing an unchanged schema makes Dgraph drop and rebuild the
#      `follows` index on every boot (an expensive, availability-affecting
#      reindex). We fingerprint the schema file and skip the load when it
#      matches the last successfully applied version.

set -uo pipefail

SCHEMA_FILE="/dgraph-seed/schema.graphql"
# Marker lives on the persistent /dgraph volume so it survives restarts.
SCHEMA_MARKER="/dgraph/.applied-schema.sha256"
ALPHA_HEALTH_URL="http://localhost:8080/health"
ADMIN_SCHEMA_URL="http://localhost:8080/admin/schema"

# Background task: wait for Alpha to be ready, then load the schema only if it
# changed since the last successful load. Runs as a child of this script; once
# `exec /run.sh` replaces the shell, it is reparented to PID 1 and exits on its
# own after doing (or skipping) the load.
load_schema_if_changed() {
  echo "⏳ Waiting for Dgraph Alpha to be ready..."
  until curl -sf "$ALPHA_HEALTH_URL" 2>/dev/null | grep -q '"status":"healthy"'; do
    sleep 1
  done
  echo "✅ Dgraph Alpha is healthy!"

  if [ ! -f "$SCHEMA_FILE" ]; then
    echo "⚠️  No schema file at $SCHEMA_FILE — skipping schema load."
    return
  fi

  local desired_hash applied_hash response
  desired_hash="$(sha256sum "$SCHEMA_FILE" | awk '{print $1}')"
  applied_hash=""
  [ -f "$SCHEMA_MARKER" ] && applied_hash="$(cat "$SCHEMA_MARKER" 2>/dev/null)"

  if [ "$desired_hash" = "$applied_hash" ]; then
    echo "✅ Schema unchanged (sha256 ${desired_hash:0:12}…) — skipping reload, preserving indexes."
    return
  fi

  echo "🔄 Schema changed — loading into Dgraph..."
  # A healthy /health does not guarantee the GraphQL admin API is ready — it
  # can briefly return "Server not ready". Retry the POST until it succeeds or
  # we run out of attempts, instead of dropping the load after one try.
  local attempt
  for attempt in $(seq 1 30); do
    response="$(curl -s -X POST "$ADMIN_SCHEMA_URL" --data-binary "@$SCHEMA_FILE")"
    if echo "$response" | grep -q '"Success"'; then
      echo "$desired_hash" > "$SCHEMA_MARKER"
      echo "✅ Schema loaded; fingerprint recorded (${desired_hash:0:12}…)."
      return
    fi
    echo "… admin API not ready (attempt ${attempt}/30): $response"
    sleep 2
  done
  # Leave the marker untouched so the next start retries the load.
  echo "❌ Schema load failed after 30 attempts (will retry on next start): $response"
}

load_schema_if_changed &

# Hand off to the stock startup script as PID 1 so Dgraph crashes are visible
# to Docker and `restart: unless-stopped` can recover them.
exec /run.sh
