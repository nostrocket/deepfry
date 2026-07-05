#!/bin/sh
# router-liveness.sh — ingestion-liveness healthcheck for the strfry-router
# container. Exit 0 iff the `strfry router` process is alive; exit 1 otherwise.
#
# Why process-liveness (not a port probe, not a freshness probe):
#   - The strfry-router container exposes NO server port (it is a WS client to
#     upstream relays + a local LMDB writer), so a port probe is impossible.
#   - A "newest LMDB event younger than N min" freshness probe would go false-
#     unhealthy during legitimate quiet windows (all authors non-whitelisted by
#     the bloom gate → zero writes). Process-presence has no such false-positive.
#   - This directly detects the incident's failure mode: the router SIGKILLed by
#     the OOM killer. Dead router → this check fails → container unhealthy →
#     restart:unless-stopped auto-recovers it.
#
# Dependency-free: busybox `ps` + `grep` are always present in the dockur/strfry
# alpine image. No curl/jq/awk-of-config/file reads.
#
# Exit codes:
#   0 — the strfry router process is running
#   1 — the strfry router process is NOT running

set -eu

# Match the strfry router invocation in the process table. The container's
# command is `/app/strfry --config=/etc/strfry.conf router /app/...`, so we
# match a line containing both the strfry binary and the `router` subcommand.
# The bracket-class trick on the first char ([s]trfry) ensures this grep pattern
# never matches the grep process's own argv, so we do not self-detect.
if ps -ef 2>/dev/null | grep -q '[s]trfry.* router'; then
  echo "router-liveness: ok (strfry router process alive)"
  exit 0
fi

echo "router-liveness: FAIL (strfry router process not found)" >&2
exit 1
