#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Files this script modifies
STRFRY_COMPOSE="$SCRIPT_DIR/docker-compose.strfry.yml"
EVTFWD_COMPOSE="$SCRIPT_DIR/docker-compose.evtfwd.yml"
WL_CLIENT_CONFIG="$SCRIPT_DIR/config/whitelist/whitelist.yaml"
WL_SERVER_CONFIG="$SCRIPT_DIR/config/whitelist/whitelist-server.yaml"
WL_ROUTER_CONFIG="$SCRIPT_DIR/config/whitelist/router.yaml"
WOT_CONFIG="$HOME/deepfry/web-of-trust.yaml"

# Backup directory
BACKUP_DIR="$SCRIPT_DIR/.switch-dgraph-backups"

usage() {
    cat <<EOF
Usage: $(basename "$0") <command>

Commands:
  remote    Point this machine's strfry at a remote Dgraph + whitelist-server
  local     Switch back to single-machine mode (everything on deepfry-net)
  status    Show current mode

When switching to remote, this script updates:
  - config/whitelist/whitelist.yaml        (plugin points at remote whitelist-server)
  - config/whitelist/whitelist-server.yaml (server points at remote Dgraph)
  - config/whitelist/router.yaml           (router plugin points at remote whitelist-server)
  - docker-compose.strfry.yml              (own network instead of external deepfry-net)
  - docker-compose.evtfwd.yml              (own network instead of external deepfry-net)
  - ~/deepfry/web-of-trust.yaml            (web-of-trust crawler's dgraph_addr)
EOF
    exit 1
}

backup_file() {
    local src="$1"
    local name
    name=$(basename "$src")
    if [[ -f "$src" ]] && [[ ! -f "$BACKUP_DIR/$name" ]]; then
        cp "$src" "$BACKUP_DIR/$name"
    fi
}

restore_file() {
    local dest="$1"
    local name
    name=$(basename "$dest")
    if [[ -f "$BACKUP_DIR/$name" ]]; then
        mv "$BACKUP_DIR/$name" "$dest"
        echo "  Restored $name"
        return 0
    fi
    return 1
}

switch_remote() {
    read -rp "Remote Dgraph/whitelist-server IP or hostname: " REMOTE_HOST

    if [[ -z "$REMOTE_HOST" ]]; then
        echo "Error: address cannot be empty."
        exit 1
    fi

    mkdir -p "$BACKUP_DIR"

    # --- Whitelist client config (strfry plugin → remote whitelist-server) ---

    backup_file "$WL_CLIENT_CONFIG"
    cat > "$WL_CLIENT_CONFIG" <<YAML
server_url: "http://${REMOTE_HOST}:8081"
check_timeout: 2s
YAML
    echo "  Updated config/whitelist/whitelist.yaml → http://${REMOTE_HOST}:8081"

    # --- Whitelist server config (whitelist-server → remote Dgraph) ---
    # Only needed if running whitelist-server locally against a remote Dgraph.
    # When the server is on the remote machine this file isn't used locally,
    # but we update it for consistency.

    backup_file "$WL_SERVER_CONFIG"
    cat > "$WL_SERVER_CONFIG" <<YAML
dgraph_graphql_url: "http://${REMOTE_HOST}:8080/graphql"
refresh_interval: 6h
refresh_retry_count: 3
idle_conn_timeout: 90s
http_timeout: 30s
query_timeout: 20m
server_listen_addr: ":8081"
YAML
    echo "  Updated config/whitelist/whitelist-server.yaml → http://${REMOTE_HOST}:8080/graphql"

    # --- Router plugin config (strfry router plugin → remote whitelist-server) ---
    # Defaults in whitelist-plugin/pkg/config/router_config.go cover quarantine
    # settings (ws://strfry-quarantine:7778 etc.) — only server_url needs
    # overriding per environment.

    backup_file "$WL_ROUTER_CONFIG"
    cat > "$WL_ROUTER_CONFIG" <<YAML
server_url: "http://${REMOTE_HOST}:8081"
YAML
    echo "  Updated config/whitelist/router.yaml → http://${REMOTE_HOST}:8081"

    # --- docker-compose.strfry.yml (own network, no external deepfry-net) ---
    #
    # Transform the existing compose in place rather than regenerating it, so
    # any services added to it (e.g. strfry-quarantine) carry over automatically.
    # Two changes:
    #   1. Rename deepfry-net → strfry-net everywhere (service refs + top-level).
    #   2. In the top-level networks block, replace `external: true` with
    #      `driver: bridge` so remote mode owns its network (the shared
    #      deepfry-net only exists when dgraph is on this machine).

    backup_file "$STRFRY_COMPOSE"
    awk '
      { gsub(/deepfry-net/, "strfry-net") }
      /^    external: true$/  { next }
      /^    name: strfry-net$/ { print; print "    driver: bridge"; next }
      { print }
    ' "$BACKUP_DIR/$(basename "$STRFRY_COMPOSE")" > "$STRFRY_COMPOSE"
    echo "  Updated docker-compose.strfry.yml → strfry-net (local network)"

    # --- docker-compose.evtfwd.yml (join strfry-net, point at local strfry) ---

    backup_file "$EVTFWD_COMPOSE"
    sed 's/deepfry-net/strfry-net/g' "$BACKUP_DIR/$(basename "$EVTFWD_COMPOSE")" \
        > "$EVTFWD_COMPOSE"
    echo "  Updated docker-compose.evtfwd.yml → strfry-net"

    # --- ~/deepfry/web-of-trust.yaml (web-of-trust crawler) ---

    if [[ -f "$WOT_CONFIG" ]]; then
        backup_file "$WOT_CONFIG"
        sed "s|^dgraph_addr:.*|dgraph_addr: ${REMOTE_HOST}:9080|" \
            "$BACKUP_DIR/$(basename "$WOT_CONFIG")" > "$WOT_CONFIG"
        echo "  Updated ~/deepfry/web-of-trust.yaml → ${REMOTE_HOST}:9080"
    else
        echo "  Warning: ~/deepfry/web-of-trust.yaml not found, skipping."
        echo "  Set dgraph_addr to ${REMOTE_HOST}:9080 manually when you create it."
    fi

    echo ""
    echo "Switched to remote Dgraph at ${REMOTE_HOST}."
    echo ""
    echo "  Whitelist server: http://${REMOTE_HOST}:8081"
    echo "  GraphQL endpoint: http://${REMOTE_HOST}:8080/graphql"
    echo "  gRPC endpoint:    ${REMOTE_HOST}:9080"
    echo ""
    echo "On this machine (strfry):"
    echo "  docker-compose -f docker-compose.strfry.yml up -d"
    echo "  docker-compose -f docker-compose.evtfwd.yml up -d"
    echo ""
    echo "On the remote machine (dgraph):"
    echo "  docker-compose -f docker-compose.dgraph.yml up -d"
}

switch_local() {
    if [[ ! -d "$BACKUP_DIR" ]]; then
        echo "Already in local mode (no backups found)."
        exit 0
    fi

    local switched=0

    restore_file "$WL_CLIENT_CONFIG" && switched=1
    restore_file "$WL_SERVER_CONFIG" && switched=1
    restore_file "$WL_ROUTER_CONFIG" && switched=1
    restore_file "$STRFRY_COMPOSE" && switched=1
    restore_file "$EVTFWD_COMPOSE" && switched=1
    restore_file "$WOT_CONFIG" && switched=1

    # Clean up backup dir if empty
    rmdir "$BACKUP_DIR" 2>/dev/null || true

    if [[ $switched -eq 0 ]]; then
        echo "Already in local mode (no backups found)."
        exit 0
    fi

    echo ""
    echo "Switched back to local mode (single machine)."
    echo ""
    echo "  docker-compose -f docker-compose.dgraph.yml up -d"
    echo "  docker-compose -f docker-compose.strfry.yml up -d"
    echo "  docker-compose -f docker-compose.evtfwd.yml up -d"
}

show_status() {
    if [[ -d "$BACKUP_DIR" ]] && [[ -n "$(ls -A "$BACKUP_DIR" 2>/dev/null)" ]]; then
        echo "Mode: remote"
        echo ""
        echo "  whitelist client:  $(grep 'server_url' "$WL_CLIENT_CONFIG" 2>/dev/null || echo 'n/a')"
        echo "  whitelist server:  $(grep 'dgraph_graphql_url' "$WL_SERVER_CONFIG" 2>/dev/null || echo 'n/a')"
        echo "  router plugin:     $(grep 'server_url' "$WL_ROUTER_CONFIG" 2>/dev/null || echo 'n/a')"
        echo "  wot crawler:       $(grep 'dgraph_addr' "$WOT_CONFIG" 2>/dev/null || echo 'n/a')"
        echo ""
        local network
        network=$(grep -o 'strfry-net\|deepfry-net' "$STRFRY_COMPOSE" | head -1)
        echo "  strfry network:    ${network:-unknown}"
    else
        echo "Mode: local (single machine, all services on deepfry-net)"
    fi
}

[[ $# -lt 1 ]] && usage

case "$1" in
    remote) switch_remote ;;
    local)  switch_local ;;
    status) show_status ;;
    *)      usage ;;
esac
