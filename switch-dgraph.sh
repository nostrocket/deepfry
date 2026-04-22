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

# Ports scanned by masscan and mapped to services
PORT_DGRAPH_HTTP=8080
PORT_DGRAPH_GRPC=9080
PORT_STRFRY=7777
PORT_WHITELIST=8081

ASSUME_YES=0
EXPLICIT_HOST=""

usage() {
    cat <<EOF
Usage: $(basename "$0") <command> [flags]

Commands:
  remote [--yes|-y] [--host <ip>]
            Point this machine's strfry at a remote Dgraph + whitelist-server.
            Auto-discovers hosts on the LAN via masscan (installed on demand).
            --yes     Skip confirmation prompts (still prompts on whitelist
                      version mismatch when alternative candidates exist).
            --host X  Skip discovery and use X for Dgraph, whitelist, and
                      strfry. Implies --yes.
  local     Switch back to single-machine mode (everything on deepfry-net).
  status    Show current mode.

When switching to remote, this script updates:
  - config/whitelist/whitelist.yaml        (plugin points at remote whitelist-server)
  - config/whitelist/whitelist-server.yaml (server points at remote Dgraph)
  - config/whitelist/router.yaml           (router plugin points at remote whitelist-server)
  - docker-compose.strfry.yml              (own network instead of external deepfry-net)
  - docker-compose.evtfwd.yml              (own network instead of external deepfry-net)
  - ~/deepfry/web-of-trust.yaml            (dgraph_addr + forward_relay_url)
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

# --- Auto-discovery helpers ------------------------------------------------

ensure_masscan() {
    if command -v masscan >/dev/null 2>&1; then
        return 0
    fi

    local os
    os=$(uname -s)
    echo "masscan not found in PATH."

    local install_cmd=""
    case "$os" in
        Darwin)
            if command -v brew >/dev/null 2>&1; then
                install_cmd="brew install masscan"
            else
                echo "Homebrew not installed. See https://brew.sh/ then re-run." >&2
                exit 1
            fi
            ;;
        Linux)
            if command -v apt-get >/dev/null 2>&1; then
                install_cmd="sudo apt-get update && sudo apt-get install -y masscan"
            elif command -v dnf >/dev/null 2>&1; then
                install_cmd="sudo dnf install -y masscan"
            elif command -v yum >/dev/null 2>&1; then
                install_cmd="sudo yum install -y masscan"
            elif command -v pacman >/dev/null 2>&1; then
                install_cmd="sudo pacman -S --noconfirm masscan"
            elif command -v apk >/dev/null 2>&1; then
                install_cmd="sudo apk add masscan"
            else
                echo "No supported package manager found. Install masscan manually." >&2
                exit 1
            fi
            ;;
        *)
            echo "Unsupported OS ($os). Install masscan manually." >&2
            exit 1
            ;;
    esac

    if [[ $ASSUME_YES -eq 0 ]]; then
        read -rp "Install masscan via '$install_cmd'? [Y/n] " reply
        case "$reply" in
            n|N) echo "Cannot continue without masscan." >&2; exit 1 ;;
        esac
    fi

    echo "Running: $install_cmd"
    eval "$install_cmd"

    if ! command -v masscan >/dev/null 2>&1; then
        echo "masscan still not found after install." >&2
        exit 1
    fi
}

# Echoes a single CIDR (a.b.c.0/24) for the default-route interface.
detect_subnet() {
    local os iface addr
    os=$(uname -s)

    if [[ "$os" == "Darwin" ]]; then
        iface=$(route -n get default 2>/dev/null | awk '/interface:/ {print $2}')
        [[ -z "$iface" ]] && { echo "Could not determine default interface." >&2; return 1; }
        addr=$(ifconfig "$iface" 2>/dev/null | awk '/inet / {print $2; exit}')
    else
        iface=$(ip -o -4 route show default 2>/dev/null | awk '{print $5; exit}')
        [[ -z "$iface" ]] && { echo "Could not determine default interface." >&2; return 1; }
        addr=$(ip -o -4 addr show "$iface" 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)
    fi

    [[ -z "$addr" ]] && { echo "Could not determine local IP on $iface." >&2; return 1; }
    echo "$(echo "$addr" | awk -F. '{print $1"."$2"."$3".0"}')/24"
}

# Writes "host port" lines to stdout (one per open port).
run_masscan() {
    local subnet="$1"
    local ports="${PORT_STRFRY},${PORT_DGRAPH_HTTP},${PORT_WHITELIST},${PORT_DGRAPH_GRPC}"
    local out
    out=$(sudo masscan -p "$ports" "$subnet" --rate=1000 -oL - 2>/dev/null || true)
    # -oL format: "open tcp <port> <ip> <timestamp>"
    echo "$out" | awk '$1=="open" {print $4, $3}'
}

# Prints probe results to stderr and echoes "host" on stdout for matches.
# Usage: verify_dgraph_http <host>
verify_dgraph_http() {
    local host="$1"
    curl -fsS -m 2 "http://${host}:${PORT_DGRAPH_HTTP}/health" >/dev/null 2>&1
}

verify_dgraph_grpc() {
    local host="$1"
    if command -v nc >/dev/null 2>&1; then
        nc -zw2 "$host" "$PORT_DGRAPH_GRPC" >/dev/null 2>&1
    else
        # Fallback: bash /dev/tcp
        (exec 3<>"/dev/tcp/${host}/${PORT_DGRAPH_GRPC}") 2>/dev/null && exec 3>&- 3<&-
    fi
}

verify_strfry() {
    local host="$1"
    # NIP-11: GET / with Accept: application/nostr+json returns the relay info doc.
    local body
    body=$(curl -fsS -m 2 -H 'Accept: application/nostr+json' "http://${host}:${PORT_STRFRY}/" 2>/dev/null || true)
    [[ -n "$body" ]] && echo "$body" | grep -q '"supported_nips"'
}

# Echoes the remote short commit on success; empty string on failure.
verify_whitelist() {
    local host="$1"
    local body
    body=$(curl -fsS -m 2 "http://${host}:${PORT_WHITELIST}/version" 2>/dev/null || true)
    [[ -z "$body" ]] && return 1
    echo "$body" | sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

# Picks a host for a service, interactively if needed.
# Args: label, host1 host2 ...
# Sets: chosen to selected host (empty if user aborts that service).
pick_host() {
    local label="$1"
    shift
    local n=$#
    chosen=""

    if [[ $n -eq 0 ]]; then
        if [[ $ASSUME_YES -eq 1 ]]; then
            echo "  No $label candidates found on LAN." >&2
            return 1
        fi
        read -rp "No $label found on LAN. Enter host manually (or blank to skip): " chosen
        return 0
    fi

    if [[ $ASSUME_YES -eq 1 ]]; then
        chosen="$1"
        echo "  $label: ${chosen} (auto-selected)"
        return 0
    fi

    echo "  $label candidates:"
    local i=1
    for h in "$@"; do
        echo "    [$i] $h"
        i=$((i + 1))
    done
    local reply
    read -rp "  Use [1-${n}] or type a host (blank = skip): " reply
    if [[ -z "$reply" ]]; then
        chosen=""
        return 0
    fi
    if [[ "$reply" =~ ^[0-9]+$ ]] && [ "$reply" -ge 1 ] && [ "$reply" -le "$n" ]; then
        # Positional-args version of array index.
        shift $((reply - 1))
        chosen="$1"
    else
        chosen="$reply"
    fi
}

# Picks the whitelist host with version-match ranking.
# Args: "host|commit" entries as positional args.
# Sets: chosen, chosen_commit_matches (0|1), chosen_remote_commit.
pick_whitelist_host() {
    local local_commit
    local_commit=$(git -C "$SCRIPT_DIR" rev-parse --short HEAD 2>/dev/null || echo "")
    chosen=""
    chosen_commit_matches=0
    chosen_remote_commit=""

    local n=$#
    if [[ $n -eq 0 ]]; then
        if [[ $ASSUME_YES -eq 1 ]]; then
            echo "  No whitelist candidates found on LAN." >&2
            return 1
        fi
        read -rp "No whitelist server found on LAN. Enter host manually (or blank to skip): " chosen
        return 0
    fi

    # Partition into matched / mismatched tokens, preserving order.
    local matched="" mismatched=""
    local entry host commit n_match=0 n_mismatch=0
    for entry in "$@"; do
        host="${entry%%|*}"
        commit="${entry#*|}"
        [[ "$commit" == "$entry" ]] && commit=""
        if [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
            matched="$matched $entry"
            n_match=$((n_match + 1))
        else
            mismatched="$mismatched $entry"
            n_mismatch=$((n_mismatch + 1))
        fi
    done

    echo "  whitelist candidates (local HEAD: ${local_commit:-unknown}):"
    local i=1
    for entry in $matched $mismatched; do
        host="${entry%%|*}"
        commit="${entry#*|}"
        [[ "$commit" == "$entry" ]] && commit=""
        if [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
            echo "    [$i] $host  [version match: $commit]"
        else
            echo "    [$i] $host  [version mismatch: remote=${commit:-unknown} local=${local_commit:-unknown}]"
        fi
        i=$((i + 1))
    done

    local n_total=$((n_match + n_mismatch))
    local reply picked=""

    if [[ $ASSUME_YES -eq 1 ]]; then
        if [[ $n_match -gt 0 ]]; then
            picked=$(echo $matched | awk '{print $1}')
        elif [[ $n_total -eq 1 ]]; then
            picked=$(echo $mismatched | awk '{print $1}')
            echo "  Warning: only one whitelist candidate and it does not match local HEAD." >&2
        else
            echo "  Multiple whitelist candidates found, none matching local HEAD." >&2
            echo "  Re-running interactive prompt for safety."
            read -rp "  Use [1-${n_total}] or type a host (blank = skip): " reply
            if [[ -z "$reply" ]]; then
                chosen=""; return 0
            fi
            if [[ "$reply" =~ ^[0-9]+$ ]] && [ "$reply" -ge 1 ] && [ "$reply" -le "$n_total" ]; then
                picked=$(echo $matched $mismatched | awk -v idx="$reply" '{print $idx}')
            else
                chosen="$reply"
                return 0
            fi
        fi
    else
        read -rp "  Use [1-${n_total}] or type a host (blank = skip): " reply
        if [[ -z "$reply" ]]; then
            chosen=""; return 0
        fi
        if [[ "$reply" =~ ^[0-9]+$ ]] && [ "$reply" -ge 1 ] && [ "$reply" -le "$n_total" ]; then
            picked=$(echo $matched $mismatched | awk -v idx="$reply" '{print $idx}')
        else
            chosen="$reply"
            return 0
        fi
    fi

    host="${picked%%|*}"
    commit="${picked#*|}"
    [[ "$commit" == "$picked" ]] && commit=""
    chosen="$host"
    chosen_remote_commit="$commit"
    if [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
        chosen_commit_matches=1
    fi
    [[ $ASSUME_YES -eq 1 ]] && echo "  whitelist: $chosen (auto-selected)"
}

# --- Remote switch ---------------------------------------------------------

discover_and_pick() {
    # Sets DGRAPH_HOST, WHITELIST_HOST, STRFRY_HOST (may be empty if user skips).

    ensure_masscan

    local subnet
    if ! subnet=$(detect_subnet); then
        echo "Subnet detection failed; falling back to manual entry." >&2
        fallback_manual
        return
    fi

    echo "Scanning $subnet for Dgraph/StrFry/whitelist endpoints..."
    local scan
    scan=$(run_masscan "$subnet")

    # Always include localhost as a probe candidate (masscan usually skips it).
    scan=$(printf "%s\n127.0.0.1 %d\n127.0.0.1 %d\n127.0.0.1 %d\n127.0.0.1 %d\n" \
        "$scan" "$PORT_DGRAPH_HTTP" "$PORT_DGRAPH_GRPC" "$PORT_STRFRY" "$PORT_WHITELIST")

    local dgraph_http_cands=""
    local dgraph_grpc_cands=""
    local strfry_cands=""
    local whitelist_entries=""   # "host|commit" tokens, whitespace-separated

    local line host port
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        host=$(echo "$line" | awk '{print $1}')
        port=$(echo "$line" | awk '{print $2}')
        case "$port" in
            "$PORT_DGRAPH_HTTP")
                case " $dgraph_http_cands " in *" $host "*) continue ;; esac
                if verify_dgraph_http "$host"; then
                    dgraph_http_cands="$dgraph_http_cands $host"
                fi
                ;;
            "$PORT_DGRAPH_GRPC")
                case " $dgraph_grpc_cands " in *" $host "*) continue ;; esac
                if verify_dgraph_grpc "$host"; then
                    dgraph_grpc_cands="$dgraph_grpc_cands $host"
                fi
                ;;
            "$PORT_STRFRY")
                case " $strfry_cands " in *" $host "*) continue ;; esac
                if verify_strfry "$host"; then
                    strfry_cands="$strfry_cands $host"
                fi
                ;;
            "$PORT_WHITELIST")
                case " $whitelist_entries " in *" $host|"*) continue ;; esac
                local commit
                if commit=$(verify_whitelist "$host") && [[ -n "$commit" ]]; then
                    whitelist_entries="$whitelist_entries $host|$commit"
                fi
                ;;
        esac
    done <<< "$scan"

    # Only report the intersection of http+grpc (a real Dgraph has both).
    local dgraph_cands=""
    for host in $dgraph_http_cands; do
        case " $dgraph_grpc_cands " in
            *" $host "*) dgraph_cands="$dgraph_cands $host" ;;
        esac
    done

    if [[ -z "$dgraph_cands" && -z "$strfry_cands" && -z "$whitelist_entries" ]]; then
        echo "No services detected on $subnet."
        fallback_manual
        return
    fi

    pick_host "Dgraph" $dgraph_cands
    DGRAPH_HOST="$chosen"

    pick_whitelist_host $whitelist_entries
    WHITELIST_HOST="$chosen"
    WHITELIST_COMMIT_MATCHES="$chosen_commit_matches"
    WHITELIST_REMOTE_COMMIT="$chosen_remote_commit"

    pick_host "StrFry" $strfry_cands
    STRFRY_HOST="$chosen"
}

fallback_manual() {
    read -rp "Remote Dgraph/whitelist-server IP or hostname: " manual_host
    if [[ -z "$manual_host" ]]; then
        echo "Error: address cannot be empty." >&2
        exit 1
    fi
    DGRAPH_HOST="$manual_host"
    WHITELIST_HOST="$manual_host"
    STRFRY_HOST="$manual_host"
    WHITELIST_COMMIT_MATCHES=0
    WHITELIST_REMOTE_COMMIT=""
}

apply_remote_configs() {
    mkdir -p "$BACKUP_DIR"

    # --- Whitelist client config (strfry plugin → remote whitelist-server) ---
    backup_file "$WL_CLIENT_CONFIG"
    cat > "$WL_CLIENT_CONFIG" <<YAML
server_url: "http://${WHITELIST_HOST}:${PORT_WHITELIST}"
check_timeout: 2s
YAML
    echo "  Updated config/whitelist/whitelist.yaml → http://${WHITELIST_HOST}:${PORT_WHITELIST}"

    # --- Whitelist server config (whitelist-server → remote Dgraph) ---
    backup_file "$WL_SERVER_CONFIG"
    cat > "$WL_SERVER_CONFIG" <<YAML
dgraph_graphql_url: "http://${DGRAPH_HOST}:${PORT_DGRAPH_HTTP}/graphql"
refresh_interval: 6h
refresh_retry_count: 3
idle_conn_timeout: 90s
http_timeout: 30s
query_timeout: 20m
server_listen_addr: ":${PORT_WHITELIST}"
YAML
    echo "  Updated config/whitelist/whitelist-server.yaml → http://${DGRAPH_HOST}:${PORT_DGRAPH_HTTP}/graphql"

    # --- Router plugin config ---
    backup_file "$WL_ROUTER_CONFIG"
    cat > "$WL_ROUTER_CONFIG" <<YAML
server_url: "http://${WHITELIST_HOST}:${PORT_WHITELIST}"
YAML
    echo "  Updated config/whitelist/router.yaml → http://${WHITELIST_HOST}:${PORT_WHITELIST}"

    # --- docker-compose.strfry.yml (own network, no external deepfry-net) ---
    backup_file "$STRFRY_COMPOSE"
    awk '
      { gsub(/deepfry-net/, "strfry-net") }
      /^    external: true$/  { next }
      /^    name: strfry-net$/ { print; print "    driver: bridge"; next }
      { print }
    ' "$BACKUP_DIR/$(basename "$STRFRY_COMPOSE")" > "$STRFRY_COMPOSE"
    echo "  Updated docker-compose.strfry.yml → strfry-net (local network)"

    # --- docker-compose.evtfwd.yml ---
    backup_file "$EVTFWD_COMPOSE"
    sed 's/deepfry-net/strfry-net/g' "$BACKUP_DIR/$(basename "$EVTFWD_COMPOSE")" \
        > "$EVTFWD_COMPOSE"
    echo "  Updated docker-compose.evtfwd.yml → strfry-net"

    # --- ~/deepfry/web-of-trust.yaml ---
    if [[ -f "$WOT_CONFIG" ]]; then
        backup_file "$WOT_CONFIG"
        sed -e "s|^dgraph_addr:.*|dgraph_addr: ${DGRAPH_HOST}:${PORT_DGRAPH_GRPC}|" \
            -e "s|^forward_relay_url:.*|forward_relay_url: \"ws://${STRFRY_HOST}:${PORT_STRFRY}\"|" \
            "$BACKUP_DIR/$(basename "$WOT_CONFIG")" > "$WOT_CONFIG"
        if ! grep -q '^forward_relay_url:' "$WOT_CONFIG"; then
            echo "forward_relay_url: \"ws://${STRFRY_HOST}:${PORT_STRFRY}\"" >> "$WOT_CONFIG"
        fi
        echo "  Updated ~/deepfry/web-of-trust.yaml → dgraph_addr ${DGRAPH_HOST}:${PORT_DGRAPH_GRPC}, forward_relay_url ws://${STRFRY_HOST}:${PORT_STRFRY}"
    else
        echo "  Warning: ~/deepfry/web-of-trust.yaml not found, skipping."
        echo "  Set dgraph_addr to ${DGRAPH_HOST}:${PORT_DGRAPH_GRPC} and forward_relay_url to ws://${STRFRY_HOST}:${PORT_STRFRY} manually when you create it."
    fi
}

switch_remote() {
    DGRAPH_HOST=""
    WHITELIST_HOST=""
    STRFRY_HOST=""
    WHITELIST_COMMIT_MATCHES=0
    WHITELIST_REMOTE_COMMIT=""

    if [[ -n "$EXPLICIT_HOST" ]]; then
        DGRAPH_HOST="$EXPLICIT_HOST"
        WHITELIST_HOST="$EXPLICIT_HOST"
        STRFRY_HOST="$EXPLICIT_HOST"
        local local_commit commit
        local_commit=$(git -C "$SCRIPT_DIR" rev-parse --short HEAD 2>/dev/null || echo "")
        if commit=$(verify_whitelist "$EXPLICIT_HOST") && [[ -n "$commit" ]]; then
            WHITELIST_REMOTE_COMMIT="$commit"
            if [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
                WHITELIST_COMMIT_MATCHES=1
            fi
        fi
    else
        discover_and_pick
    fi

    if [[ -z "$DGRAPH_HOST" || -z "$WHITELIST_HOST" || -z "$STRFRY_HOST" ]]; then
        echo "Error: missing host for one or more services (dgraph=${DGRAPH_HOST:-none}, whitelist=${WHITELIST_HOST:-none}, strfry=${STRFRY_HOST:-none})." >&2
        exit 1
    fi

    apply_remote_configs

    echo ""
    echo "Switched to remote Dgraph."
    echo ""
    echo "  Whitelist server: http://${WHITELIST_HOST}:${PORT_WHITELIST}"
    if [[ -n "$WHITELIST_REMOTE_COMMIT" ]]; then
        if [[ "$WHITELIST_COMMIT_MATCHES" == "1" ]]; then
            echo "                    [version match: $WHITELIST_REMOTE_COMMIT]"
        else
            local local_commit
            local_commit=$(git -C "$SCRIPT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")
            echo "                    [version mismatch: remote=$WHITELIST_REMOTE_COMMIT local=$local_commit]"
        fi
    fi
    echo "  GraphQL endpoint: http://${DGRAPH_HOST}:${PORT_DGRAPH_HTTP}/graphql"
    echo "  gRPC endpoint:    ${DGRAPH_HOST}:${PORT_DGRAPH_GRPC}"
    echo "  StrFry forward:   ws://${STRFRY_HOST}:${PORT_STRFRY}"
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
        echo "  wot forward:       $(grep 'forward_relay_url' "$WOT_CONFIG" 2>/dev/null || echo 'n/a')"
        echo ""
        local network
        network=$(grep -o 'strfry-net\|deepfry-net' "$STRFRY_COMPOSE" | head -1)
        echo "  strfry network:    ${network:-unknown}"
    else
        echo "Mode: local (single machine, all services on deepfry-net)"
    fi
}

parse_remote_flags() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -y|--yes) ASSUME_YES=1; shift ;;
            --host)
                [[ $# -lt 2 ]] && { echo "--host requires a value" >&2; usage; }
                EXPLICIT_HOST="$2"
                ASSUME_YES=1
                shift 2
                ;;
            --host=*)
                EXPLICIT_HOST="${1#--host=}"
                ASSUME_YES=1
                shift
                ;;
            *) echo "Unknown flag: $1" >&2; usage ;;
        esac
    done
}

[[ $# -lt 1 ]] && usage

cmd="$1"
shift

case "$cmd" in
    remote)
        parse_remote_flags "$@"
        switch_remote
        ;;
    local)  switch_local ;;
    status) show_status ;;
    *)      usage ;;
esac
