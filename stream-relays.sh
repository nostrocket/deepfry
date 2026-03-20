#!/usr/bin/env bash
set -euo pipefail

SESSION="strfry-streams"
CONTAINER="strfry"
STRFRY="/app/strfry"

RELAYS=(
  wss://relay.damus.io
  wss://relay.nostr.band
  wss://nos.lol
  wss://relay.snort.social
  wss://relay.primal.net
  wss://nostr.wine
  wss://relay.nostr.bg
  wss://nostr.fmt.wiz.biz
  wss://relay.current.fyi
  wss://nostr.oxtr.dev
  wss://relay.nostr.info
  wss://offchain.pub
  wss://nostr-pub.wellorder.net
  wss://nostr.mom
  wss://relay.mostr.pub
  wss://nostr.land
  wss://relay.orangepill.dev
  wss://purplepag.es
  wss://eden.nostr.land
  wss://atlas.nostr.land
)

ensure_tmux() {
  if ! command -v tmux &>/dev/null; then
    echo "tmux not found, installing via brew..."
    brew install tmux
  fi
}

check_container() {
  if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
    echo "Error: container '${CONTAINER}' is not running."
    echo "Start it with: docker-compose up -d"
    exit 1
  fi
}

cmd_start() {
  ensure_tmux
  check_container

  if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Session '$SESSION' already exists. Use '$0 stop' first or '$0 attach' to monitor."
    exit 1
  fi

  echo "Starting strfry stream for ${#RELAYS[@]} relays..."

  # Create session with the first relay
  local name
  name=$(echo "${RELAYS[0]}" | sed 's|wss://||;s|[./]|-|g')
  tmux new-session -d -s "$SESSION" -n "$name" \
    "docker exec ${CONTAINER} ${STRFRY} stream ${RELAYS[0]}; echo '[exited] press enter to close'; read"

  # Create windows for remaining relays
  for relay in "${RELAYS[@]:1}"; do
    name=$(echo "$relay" | sed 's|wss://||;s|[./]|-|g')
    tmux new-window -t "$SESSION" -n "$name" \
      "docker exec ${CONTAINER} ${STRFRY} stream ${relay}; echo '[exited] press enter to close'; read"
  done

  echo "Started ${#RELAYS[@]} streams in tmux session '$SESSION'."
  echo "Run '$0 attach' to monitor."
}

cmd_stop() {
  if tmux has-session -t "$SESSION" 2>/dev/null; then
    tmux kill-session -t "$SESSION"
    echo "Stopped all streams (killed session '$SESSION')."
  else
    echo "No active session '$SESSION' found."
  fi
}

cmd_attach() {
  ensure_tmux
  if tmux has-session -t "$SESSION" 2>/dev/null; then
    tmux attach -t "$SESSION"
  else
    echo "No active session '$SESSION' found. Run '$0' to start."
    exit 1
  fi
}

case "${1:-start}" in
  start)  cmd_start ;;
  stop)   cmd_stop ;;
  attach) cmd_attach ;;
  *)
    echo "Usage: $0 [start|stop|attach]"
    exit 1
    ;;
esac
