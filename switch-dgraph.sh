#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
COMPOSE_BACKUP="$SCRIPT_DIR/docker-compose.yml.local-backup"
WOT_CONFIG="$HOME/deepfry/config.yaml"
WOT_BACKUP="$HOME/deepfry/config.yaml.local-backup"

usage() {
    cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  remote    Use a remote Dgraph instance (prompts for IP/hostname)
  local     Switch back to the local Dgraph container
  status    Show current mode

Updates:
  - docker-compose.yml  (whitelist plugin's DGRAPH_GRAPHQL_URL, removes dgraph services)
  - ~/deepfry/config.yaml  (web-of-trust crawler's dgraph_addr)
EOF
    exit 1
}

switch_remote() {
    read -rp "Dgraph host IP or hostname: " DGRAPH_HOST

    if [[ -z "$DGRAPH_HOST" ]]; then
        echo "Error: address cannot be empty."
        exit 1
    fi

    # --- docker-compose.yml (whitelist plugin) ---

    # Use the backup as source if it exists (allows re-running with a new IP),
    # otherwise back up the current file first.
    local COMPOSE_SOURCE="$COMPOSE_FILE"
    if [[ -f "$COMPOSE_BACKUP" ]]; then
        COMPOSE_SOURCE="$COMPOSE_BACKUP"
    else
        cp "$COMPOSE_FILE" "$COMPOSE_BACKUP"
    fi

    # 1. Point DGRAPH_GRAPHQL_URL at the remote host
    # 2. Strip the dgraph and dgraph-ratel service blocks
    sed "s|http://dgraph:8080/graphql|http://${DGRAPH_HOST}:8080/graphql|" "$COMPOSE_SOURCE" |
    awk '
    BEGIN { skip = 0 }
    /^  dgraph:$/ { skip = 1; next }
    /^  dgraph-ratel:$/ { skip = 1; next }
    {
        if (skip) {
            if (/^  [a-zA-Z]/ || /^[a-zA-Z]/) {
                skip = 0
                print
            }
        } else {
            print
        }
    }
    ' > "${COMPOSE_FILE}.tmp" && mv "${COMPOSE_FILE}.tmp" "$COMPOSE_FILE"

    echo "  Updated docker-compose.yml"

    # --- ~/deepfry/config.yaml (web-of-trust crawler) ---

    if [[ -f "$WOT_CONFIG" ]]; then
        if [[ ! -f "$WOT_BACKUP" ]]; then
            cp "$WOT_CONFIG" "$WOT_BACKUP"
        fi

        sed "s|^dgraph_addr:.*|dgraph_addr: ${DGRAPH_HOST}:9080|" "$WOT_BACKUP" \
            > "${WOT_CONFIG}.tmp" && mv "${WOT_CONFIG}.tmp" "$WOT_CONFIG"

        echo "  Updated ~/deepfry/config.yaml"
    else
        echo "  Warning: ~/deepfry/config.yaml not found, skipping web-of-trust config."
        echo "  Set dgraph_addr to ${DGRAPH_HOST}:9080 manually when you create it."
    fi

    echo ""
    echo "Switched to remote Dgraph at ${DGRAPH_HOST}."
    echo ""
    echo "  GraphQL endpoint: http://${DGRAPH_HOST}:8080/graphql"
    echo "  gRPC endpoint:    ${DGRAPH_HOST}:9080"
    echo ""
    echo "Run 'docker-compose up -d' to apply."
}

switch_local() {
    local switched=0

    if [[ -f "$COMPOSE_BACKUP" ]]; then
        mv "$COMPOSE_BACKUP" "$COMPOSE_FILE"
        echo "  Restored docker-compose.yml"
        switched=1
    fi

    if [[ -f "$WOT_BACKUP" ]]; then
        mv "$WOT_BACKUP" "$WOT_CONFIG"
        echo "  Restored ~/deepfry/config.yaml"
        switched=1
    fi

    if [[ $switched -eq 0 ]]; then
        echo "Already in local mode (no backups found)."
        exit 0
    fi

    echo ""
    echo "Switched back to local Dgraph."
    echo "Run 'docker-compose up -d' to apply."
}

show_status() {
    if [[ -f "$COMPOSE_BACKUP" ]]; then
        local url
        url=$(grep -o 'DGRAPH_GRAPHQL_URL:.*' "$COMPOSE_FILE" | head -1)
        local addr
        addr=$(grep -o 'dgraph_addr:.*' "$WOT_CONFIG" 2>/dev/null || echo "n/a")
        echo "Mode: remote"
        echo "  whitelist plugin: ${url}"
        echo "  wot crawler:      ${addr}"
    else
        echo "Mode: local (Dgraph running in Docker)"
    fi
}

[[ $# -lt 1 ]] && usage

case "$1" in
    remote) switch_remote ;;
    local)  switch_local ;;
    status) show_status ;;
    *)      usage ;;
esac
