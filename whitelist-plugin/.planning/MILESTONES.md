# Milestones

## v1.0 — Whitelist Server + Plugins (baseline)

**Status:** Shipped (production). Recorded retroactively when GSD planning was introduced — pre-dates `.planning/`.

Web-of-trust write enforcement for a stock StrFry relay:
- Whitelist server with Dgraph-backed, lock-free in-memory cache and HTTP API (`/check`, bulk `POST /check`, `/health`, `/stats`, `/version`)
- Async periodic refresh with atomic swap and failure resilience
- Thin `whitelist` client plugin (HTTP `/check` per event, fail-closed)
- `router` plugin variant (forwards rejected kind 0/1/3 events to a quarantine relay)
- StrFry JSONL plugin protocol, 10k events/sec handler path, RSS < 128 MiB at 1M keys

## v1.1 — Bloom Filter Gate Plugin (in progress)

**Status:** Planning — started 2026-06-29.

Standalone StrFry plugin that gates writes against a locally-held bloom filter of all whitelisted pubkeys (sole local gate, zero per-event HTTP), fed by a new `/bloom` endpoint on the whitelist server, with on-disk persistence for resilience when the server is unreachable.
