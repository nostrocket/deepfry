# Whitelist Plugin

## What This Is

A system that enforces web-of-trust based write access on a stock (unmodified) StrFry Nostr relay. A central **whitelist server** owns the Dgraph connection and holds an in-memory, lock-free cache of all whitelisted pubkeys; thin **plugin binaries** on each StrFry instance make the accept/reject decision for every incoming event. Part of the DeepFry stack for Humble Horse.

## Core Value

Every event written to the relay comes from a pubkey in the web of trust — enforced cheaply, reliably, and without forking StrFry.

## Requirements

### Validated

<!-- Shipped and confirmed valuable (the production v1.0 baseline, inferred from the codebase). -->

- ✓ **Whitelist server** — single instance owns the Dgraph connection, maintains an in-memory whitelist cache, exposes an HTTP API (`/check/{pubkey}`, bulk `POST /check`, `/health`, `/stats`, `/version`) — v1.0
- ✓ **Lock-free cache** — `atomic.Pointer[map[[32]byte]struct{}]` for O(1) contention-free lookups; RSS < 128 MiB at 1M keys — v1.0
- ✓ **Async refresh** — background goroutine re-fetches the whitelist from Dgraph on a configurable interval (default 6h) with retry/backoff and atomic swap; last snapshot preserved on failure — v1.0
- ✓ **Whitelist client plugin** (`cmd/whitelist`) — thin StrFry writePolicy plugin; translates JSONL stdin/stdout ⇄ server HTTP `GET /check`; fail-closed — v1.0
- ✓ **Router plugin** (`cmd/router`) — drop-in alternative that additionally forwards rejected kind 0/1/3 events to a quarantine relay (fire-and-forget, never affects the mainline decision) — v1.0
- ✓ **StrFry plugin protocol** — JSONL stdin/stdout, malformed-input tolerant, handles 10k events/sec in the handler path — v1.0
- ✓ **Checker / KeyRepository / Handler / IOAdapter interfaces** — decouple decision logic from whitelist source and wire protocol — v1.0

### Active

<!-- Current scope: milestone v1.1. -->

- [ ] Server builds a bloom filter from the in-memory whitelist on each refresh and exposes it via a new `/bloom` endpoint
- [ ] New standalone bloom-gate plugin checks events against a locally-held filter with zero per-event HTTP
- [ ] Plugin persists the filter to the config directory and falls back to it when the server is unreachable
- [ ] Shared bloom package used by both the server (build/serialize) and the plugin (load/query)

### Out of Scope

<!-- Explicit boundaries. -->

- Modifying the existing `whitelist` or `router` plugins — the bloom gate is a separate, opt-in fourth binary
- Per-event HTTP fallback in the bloom plugin's steady state — the bloom filter is the sole local gate (maybe-in-set → accept)
- Eliminating false-positive accepts — a ~1e-6 leak rate is deliberately tolerated in exchange for zero per-event network cost
- Forking StrFry — all integration stays within the stdin/stdout JSON plugin protocol

## Current Milestone: v1.1 Bloom Filter Gate Plugin

**Goal:** Add a standalone StrFry plugin that gates writes against a locally-held bloom filter of all whitelisted pubkeys — eliminating per-event HTTP entirely — fed by a new `/bloom` endpoint on the existing whitelist server, with on-disk persistence for resilience.

**Target features:**
- Server-side bloom build (rebuilt alongside each whitelist refresh/atomic swap) + `GET /bloom` endpoint with ETag/conditional GET
- New `cmd/bloom` plugin: sole local gate (not-in-set → reject, maybe-in-set → accept), zero per-event HTTP
- Filter sized for a 0.0001% (1-in-1,000,000) false-positive rate
- Plugin persists the fetched filter to `~/deepfry/` and serves from it when the server is unreachable
- Cold start: block only when there is neither a reachable server nor a persisted filter
- Periodic refresh (~6h, conditional GET) tracking the server's own Dgraph refresh
- Shared `pkg/bloom` package + build target + Docker/`strfry.conf` integration + docs

## Context

- Part of the DeepFry monorepo (`/Users/g/git/deepfry`). StrFry must stay unmodified; extend only via the JSON plugin protocol.
- Data separation rule: event payloads live only in StrFry's LMDB. The whitelist is ID-only (pubkeys), sourced from Dgraph's `Profile` graph.
- The whitelist server refreshes from Dgraph every ~6h by default; the system already tolerates that staleness, and `quarantine-rescuer` backfills events for newly-whitelisted pubkeys.
- Motivation for the bloom gate: reduce central-server load, lower per-event latency by removing the HTTP round-trip from the hot path, and keep the relay making correct reject decisions when the server is briefly unreachable.
- DeepFry config files live in `~/deepfry/` and must never be deleted/overwritten by tooling.

## Constraints

- **Tech stack**: Go 1.24.1+, single self-contained module; reuse existing `Checker`/`Handler`/`IOAdapter` abstractions.
- **Protocol**: StrFry stdin/stdout JSONL writePolicy plugin protocol only — no StrFry fork.
- **Correctness**: bloom filter must have no false negatives (legit rejects always exact); false-positive accepts bounded to the configured rate (~1e-6).
- **Resilience**: relay must keep gating writes when the whitelist server is unreachable, using the last persisted filter.
- **Compatibility**: existing `whitelist` and `router` plugins and the server's existing endpoints remain unchanged.
- **Config**: per-process YAML under `~/deepfry/`; persisted filter also lives under `~/deepfry/`.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Sole local gate (no per-event HTTP, maybe→accept) | Removes the server from the hot path entirely; meets load/latency/resilience goals at once | — Pending |
| 0.0001% (1e-6) false-positive target | Strongest practical spam suppression; filter still only a few MB at 1M keys | — Pending |
| Separate `cmd/bloom` binary (not a flag on existing plugins) | Keeps the proven `whitelist`/`router` plugins byte-identical; opt-in adoption | — Pending |
| Persist filter to `~/deepfry/` + serve on server-unreachable | Resilience: relay survives server downtime, incl. at cold start | — Pending |
| New `GET /bloom` on existing server + conditional GET | Reuses the server that already owns the canonical whitelist; cheap polling | — Pending |
| Refresh ~6h tracking server's Dgraph refresh | Matches existing staleness model; quarantine-rescuer backfills the gap | — Pending |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-06-29 after bootstrapping GSD planning + starting milestone v1.1*
