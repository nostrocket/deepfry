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

# Ports probed on each live host and mapped to services
PORT_DGRAPH_HTTP=8080
PORT_DGRAPH_GRPC=9080
PORT_STRFRY=7777
PORT_WHITELIST=8081

ASSUME_YES=0
EXPLICIT_HOST=""
EXPLICIT_SUBNET=""
VERBOSE=0

usage() {
    cat <<EOF
Usage: $(basename "$0") <command> [flags]

Commands:
  remote [--yes|-y] [--host <ip>] [--subnet <cidr>] [--verbose|-v]
            Point this machine's strfry at a remote Dgraph + whitelist-server.
            Discovery is a parallel ICMP ping-sweep across every attached /24
            followed by TCP connect probes (nc -z) on live hosts. No sudo,
            no install beyond nc (auto-installed if missing on Linux).
            --yes         Skip confirmation prompts (still prompts on whitelist
                          version mismatch when alternative candidates exist).
            --host X      Skip discovery and use X for Dgraph, whitelist, and
                          strfry. Implies --yes.
            --subnet CIDR Override the auto-detected subnets (comma/space
                          separated, e.g. 192.168.2.0/24,10.0.0.0/24).
                          CIDRs wider than /22 are rejected.
            --verbose     Print live-host list and per-(host,port) probe results.
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

ensure_nc() {
    if command -v nc >/dev/null 2>&1; then
        return 0
    fi

    local os
    os=$(uname -s)
    echo "nc (netcat) not found in PATH."

    local install_cmd=""
    case "$os" in
        Darwin)
            # nc ships with macOS; if it's missing something is very wrong.
            echo "macOS normally ships nc at /usr/bin/nc — check your PATH." >&2
            exit 1
            ;;
        Linux)
            if command -v apt-get >/dev/null 2>&1; then
                install_cmd="sudo apt-get update && sudo apt-get install -y netcat-openbsd"
            elif command -v dnf >/dev/null 2>&1; then
                install_cmd="sudo dnf install -y nmap-ncat"
            elif command -v yum >/dev/null 2>&1; then
                install_cmd="sudo yum install -y nmap-ncat"
            elif command -v pacman >/dev/null 2>&1; then
                install_cmd="sudo pacman -S --noconfirm openbsd-netcat"
            elif command -v apk >/dev/null 2>&1; then
                install_cmd="sudo apk add netcat-openbsd"
            else
                echo "No supported package manager found. Install nc manually." >&2
                exit 1
            fi
            ;;
        *)
            echo "Unsupported OS ($os). Install nc manually." >&2
            exit 1
            ;;
    esac

    if [[ $ASSUME_YES -eq 0 ]]; then
        read -rp "Install nc via '$install_cmd'? [Y/n] " reply
        case "$reply" in
            n|N) echo "Cannot continue without nc." >&2; exit 1 ;;
        esac
    fi

    echo "Running: $install_cmd"
    eval "$install_cmd"

    if ! command -v nc >/dev/null 2>&1; then
        echo "nc still not found after install." >&2
        exit 1
    fi
}

# OS-specific timeout flags for ping and nc.
# - ping: macOS uses `-t <sec>` (whole-command timeout), Linux uses `-W <sec>`
#   (wait per-reply).
# - nc: BSD (macOS) needs `-G <sec>` for connect timeout because its `-w` is
#   only the idle timeout — a firewall silently dropping SYN hangs for ~75s
#   without `-G`. Linux netcat-openbsd does respect `-w` for connects.
PING_TIMEOUT_FLAG=""
NC_TIMEOUT_FLAGS=""
_init_os_flags() {
    if [[ "$(uname -s)" == "Darwin" ]]; then
        PING_TIMEOUT_FLAG="-t 1"
        NC_TIMEOUT_FLAGS="-G 1 -w 1"
    else
        PING_TIMEOUT_FLAG="-W 1"
        NC_TIMEOUT_FLAGS="-w 1"
    fi
}
_init_os_flags

# Echoes every non-loopback /24 the host is attached to, space-separated.
detect_subnets() {
    local os subnets
    os=$(uname -s)

    if [[ "$os" == "Darwin" ]]; then
        # macOS: walk every inet entry, coerce each to a /24.
        subnets=$(ifconfig 2>/dev/null | awk '
            /inet / && $2 !~ /^127\./ {
                split($2, o, ".")
                print o[1]"."o[2]"."o[3]".0/24"
            }' | sort -u | tr '\n' ' ')
    else
        # Linux: ip prints CIDRs directly; coerce each to its /24 base.
        subnets=$(ip -o -4 addr show 2>/dev/null | awk '{print $4}' | \
            awk -F/ '$1 !~ /^127\./ {split($1, o, "."); print o[1]"."o[2]"."o[3]".0/24"}' | \
            sort -u | tr '\n' ' ')
    fi

    [[ -z "$subnets" ]] && { echo "No non-loopback subnets found." >&2; return 1; }
    echo "$subnets"
}

# Guard against ping-sweeping /16 or wider — too many hosts for a bash loop.
# Accepts /22 (1024 hosts) through /32.
_check_cidr_size() {
    local cidr="$1"
    local prefix="${cidr##*/}"
    # If no /prefix was given, assume /32 single host.
    [[ "$prefix" == "$cidr" ]] && return 0
    if ! [[ "$prefix" =~ ^[0-9]+$ ]]; then
        echo "Bad CIDR prefix in $cidr" >&2
        return 1
    fi
    if (( prefix < 22 )); then
        echo "Refusing to ping-sweep $cidr — prefix /$prefix is too broad (pass a narrower --subnet)." >&2
        return 1
    fi
    return 0
}

# Writes live host IPs (one per line) to stdout.
# $1 is a space-separated list of /24 CIDRs.
ping_sweep() {
    local subnets="$1"
    local tmp
    tmp=$(mktemp -t pingsweep.XXXXXX)
    local cidr base octet launched=0

    for cidr in $subnets; do
        _check_cidr_size "$cidr" || { rm -f "$tmp"; return 1; }
        base=$(echo "${cidr%/*}" | awk -F. '{print $1"."$2"."$3}')
        for octet in $(seq 1 254); do
            # shellcheck disable=SC2086
            ( ping -c 1 $PING_TIMEOUT_FLAG "${base}.${octet}" >/dev/null 2>&1 && \
                echo "${base}.${octet}" >> "$tmp" ) &
            launched=$((launched + 1))
            # Throttle to avoid process-table churn on large sweeps.
            if (( launched % 128 == 0 )); then wait; fi
        done
    done
    wait
    sort -u "$tmp"
    rm -f "$tmp"
}

# Writes "host port" lines to stdout for each TCP port that connects.
# $1: space-separated hosts. $2: space-separated ports.
tcp_sweep() {
    local hosts="$1"
    local ports="$2"
    local tmp
    tmp=$(mktemp -t tcpsweep.XXXXXX)
    local host port launched=0

    for host in $hosts; do
        for port in $ports; do
            # shellcheck disable=SC2086
            ( nc -z $NC_TIMEOUT_FLAGS "$host" "$port" >/dev/null 2>&1 && \
                echo "$host $port" >> "$tmp" ) &
            launched=$((launched + 1))
            if (( launched % 128 == 0 )); then wait; fi
        done
    done
    wait
    sort -u "$tmp"
    rm -f "$tmp"
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
        # shellcheck disable=SC2086
        nc -z $NC_TIMEOUT_FLAGS "$host" "$PORT_DGRAPH_GRPC" >/dev/null 2>&1
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

# Echoes one of:
#   <short-sha>  — /version responded with a real commit
#   unavailable  — /version missing/placeholder but /health answered
# Returns non-zero if neither endpoint responded.
verify_whitelist() {
    local host="$1"
    local body commit
    body=$(curl -fsS -m 2 "http://${host}:${PORT_WHITELIST}/version" 2>/dev/null || true)
    if [[ -n "$body" ]]; then
        commit=$(echo "$body" | sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
        case "$commit" in
            ""|none|unknown) ;;       # fall through to /health probe
            *) echo "$commit"; return 0 ;;
        esac
    fi
    # /health returns 200 when ready or 503 while loading — both mean "server is there".
    local code
    code=$(curl -s -o /dev/null -m 2 -w "%{http_code}" "http://${host}:${PORT_WHITELIST}/health" 2>/dev/null || echo "000")
    case "$code" in
        200|503) echo "unavailable"; return 0 ;;
    esac
    return 1
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

    # Partition into match / mismatch / unavailable, preserving order.
    local matched="" mismatched="" unavailable=""
    local entry host commit n_match=0 n_mismatch=0 n_unavailable=0
    for entry in "$@"; do
        host="${entry%%|*}"
        commit="${entry#*|}"
        [[ "$commit" == "$entry" ]] && commit=""
        if [[ "$commit" == "unavailable" ]]; then
            unavailable="$unavailable $entry"
            n_unavailable=$((n_unavailable + 1))
        elif [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
            matched="$matched $entry"
            n_match=$((n_match + 1))
        else
            mismatched="$mismatched $entry"
            n_mismatch=$((n_mismatch + 1))
        fi
    done

    echo "  whitelist candidates (local HEAD: ${local_commit:-unknown}):"
    local i=1
    for entry in $matched $mismatched $unavailable; do
        host="${entry%%|*}"
        commit="${entry#*|}"
        [[ "$commit" == "$entry" ]] && commit=""
        if [[ "$commit" == "unavailable" ]]; then
            echo "    [$i] $host  [version unavailable: server has no /version or no commit embedded]"
        elif [[ -n "$local_commit" && "$commit" == "$local_commit" ]]; then
            echo "    [$i] $host  [version match: $commit]"
        else
            echo "    [$i] $host  [version mismatch: remote=${commit:-unknown} local=${local_commit:-unknown}]"
        fi
        i=$((i + 1))
    done

    local n_total=$((n_match + n_mismatch + n_unavailable))
    local reply picked=""

    if [[ $ASSUME_YES -eq 1 ]]; then
        if [[ $n_match -gt 0 ]]; then
            picked=$(echo $matched | awk '{print $1}')
        elif [[ $n_total -eq 1 ]]; then
            picked=$(echo $mismatched $unavailable | awk '{print $1}')
            echo "  Warning: only one whitelist candidate and it does not match local HEAD." >&2
        else
            echo "  Multiple whitelist candidates found, none matching local HEAD." >&2
            echo "  Re-running interactive prompt for safety."
            read -rp "  Use [1-${n_total}] or type a host (blank = skip): " reply
            if [[ -z "$reply" ]]; then
                chosen=""; return 0
            fi
            if [[ "$reply" =~ ^[0-9]+$ ]] && [ "$reply" -ge 1 ] && [ "$reply" -le "$n_total" ]; then
                picked=$(echo $matched $mismatched $unavailable | awk -v idx="$reply" '{print $idx}')
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
            picked=$(echo $matched $mismatched $unavailable | awk -v idx="$reply" '{print $idx}')
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

    ensure_nc

    local subnets
    if [[ -n "$EXPLICIT_SUBNET" ]]; then
        # Accept comma- or space-separated list.
        subnets=$(echo "$EXPLICIT_SUBNET" | tr ',' ' ')
    elif ! subnets=$(detect_subnets); then
        echo "Subnet detection failed; falling back to manual entry." >&2
        fallback_manual
        return
    fi

    echo "Pinging subnets for live hosts: $subnets"
    local live
    if ! live=$(ping_sweep "$subnets"); then
        echo "Ping sweep failed; falling back to manual entry." >&2
        fallback_manual
        return
    fi

    # Always include localhost so a fully-local stack is discoverable.
    live=$(printf "%s\n127.0.0.1\n" "$live" | sort -u)

    local live_count
    live_count=$(echo "$live" | grep -c . || true)
    echo "  Found $live_count live host(s); probing ports..."
    if [[ $VERBOSE -eq 1 ]]; then
        echo "--- live hosts ---" >&2
        echo "$live" >&2
        echo "--- end live hosts ---" >&2
    fi

    local ports="${PORT_STRFRY} ${PORT_DGRAPH_HTTP} ${PORT_WHITELIST} ${PORT_DGRAPH_GRPC}"
    local scan
    scan=$(tcp_sweep "$live" "$ports")
    local hit_count
    hit_count=$(echo "$scan" | grep -c . || true)
    echo "  TCP probe found $hit_count open port(s)."
    if [[ $VERBOSE -eq 1 ]]; then
        echo "--- open host:port pairs ---" >&2
        echo "$scan" >&2
        echo "--- end open ---" >&2
    fi

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
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] dgraph-http ${host}:${port} OK" >&2
                else
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] dgraph-http ${host}:${port} FAIL (/health not 200)" >&2
                fi
                ;;
            "$PORT_DGRAPH_GRPC")
                case " $dgraph_grpc_cands " in *" $host "*) continue ;; esac
                if verify_dgraph_grpc "$host"; then
                    dgraph_grpc_cands="$dgraph_grpc_cands $host"
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] dgraph-grpc ${host}:${port} OK" >&2
                else
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] dgraph-grpc ${host}:${port} FAIL (TCP closed)" >&2
                fi
                ;;
            "$PORT_STRFRY")
                case " $strfry_cands " in *" $host "*) continue ;; esac
                if verify_strfry "$host"; then
                    strfry_cands="$strfry_cands $host"
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] strfry ${host}:${port} OK" >&2
                else
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] strfry ${host}:${port} FAIL (no NIP-11 doc)" >&2
                fi
                ;;
            "$PORT_WHITELIST")
                case " $whitelist_entries " in *" $host|"*) continue ;; esac
                local commit
                if commit=$(verify_whitelist "$host") && [[ -n "$commit" ]]; then
                    whitelist_entries="$whitelist_entries $host|$commit"
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] whitelist ${host}:${port} OK (commit=$commit)" >&2
                else
                    [[ $VERBOSE -eq 1 ]] && echo "  [probe] whitelist ${host}:${port} FAIL (/version not available)" >&2
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
        echo "No services detected on: $subnets"
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

    # Dgraph is the only hard requirement (it's what this script primarily switches).
    # Whitelist and strfry commonly live alongside dgraph on a shared remote box —
    # if discovery couldn't resolve them independently, default to the dgraph host
    # so the common "everything on one remote" case doesn't silently fail.
    if [[ -z "$DGRAPH_HOST" ]]; then
        echo "" >&2
        echo "ERROR: no Dgraph host resolved. Nothing written." >&2
        echo "  dgraph=${DGRAPH_HOST:-none} whitelist=${WHITELIST_HOST:-none} strfry=${STRFRY_HOST:-none}" >&2
        echo "" >&2
        echo "  Re-run with --host <ip> to skip discovery, or --verbose to see why probes failed." >&2
        exit 1
    fi
    if [[ -z "$WHITELIST_HOST" ]]; then
        echo "  (no whitelist-server discovered — defaulting to dgraph host: $DGRAPH_HOST)"
        WHITELIST_HOST="$DGRAPH_HOST"
    fi
    if [[ -z "$STRFRY_HOST" ]]; then
        echo "  (no strfry discovered — defaulting to dgraph host: $DGRAPH_HOST)"
        STRFRY_HOST="$DGRAPH_HOST"
    fi

    echo ""
    echo "Resolved endpoints — about to write configs:"
    echo "  Dgraph HTTP :   http://${DGRAPH_HOST}:${PORT_DGRAPH_HTTP}"
    echo "  Dgraph gRPC :   ${DGRAPH_HOST}:${PORT_DGRAPH_GRPC}"
    echo "  Whitelist   :   http://${WHITELIST_HOST}:${PORT_WHITELIST}"
    echo "  StrFry      :   ws://${STRFRY_HOST}:${PORT_STRFRY}"
    echo ""
    if [[ $ASSUME_YES -eq 0 ]]; then
        local reply
        read -rp "Apply these settings? [Y/n] " reply
        case "$reply" in
            n|N) echo "Aborted — nothing written."; exit 0 ;;
        esac
    fi

    apply_remote_configs

    echo ""
    echo "Switched to remote Dgraph."
    echo ""
    echo "  Whitelist server: http://${WHITELIST_HOST}:${PORT_WHITELIST}"
    if [[ -n "$WHITELIST_REMOTE_COMMIT" ]]; then
        if [[ "$WHITELIST_COMMIT_MATCHES" == "1" ]]; then
            echo "                    [version match: $WHITELIST_REMOTE_COMMIT]"
        elif [[ "$WHITELIST_REMOTE_COMMIT" == "unavailable" ]]; then
            echo "                    [version unavailable: rebuild whitelist-server to embed commit]"
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
    echo "Mode: remote — verify with \`$(basename "$0") status\`"
    echo ""
    echo "Restart strfry on whichever machine(s) run it (the writePolicy plugin"
    echo "only reads whitelist.yaml / router.yaml at strfry startup):"
    echo "  docker compose -f docker-compose.strfry.yml restart"
    echo "  docker compose -f docker-compose.evtfwd.yml restart"
    echo ""
    echo "If strfry runs on a different machine than this one, copy the updated"
    echo "config/whitelist/*.yaml over to that machine first (or run this script"
    echo "there too)."
    echo ""
    echo "On the remote dgraph machine (if not already running):"
    echo "  docker compose -f docker-compose.dgraph.yml up -d"
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
            -v|--verbose) VERBOSE=1; shift ;;
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
            --subnet)
                [[ $# -lt 2 ]] && { echo "--subnet requires a value" >&2; usage; }
                EXPLICIT_SUBNET="$2"
                shift 2
                ;;
            --subnet=*)
                EXPLICIT_SUBNET="${1#--subnet=}"
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
