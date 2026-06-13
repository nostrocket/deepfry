# Web-of-Trust Crawler

## What This Is

The `web-of-trust` Go module is a Nostr crawler that subscribes to kind-3 (contact list) events from many relays and stores an ID-only pubkey follow-graph in Dgraph. It is one of several sidecar services around a stock StrFry Nostr relay in the DeepFry stack; it never stores event payloads (those live in StrFry's LMDB) — only pubkey relationships. The follow-graph feeds trust scoring and the whitelist plugin that decides which pubkeys may write events.

## Core Value

The crawler must continuously **expand** the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

## Current Milestone: v1.2 Crawler Reliability & Efficiency — COMPLETE (2026-06-13)

**Goal:** Fix three high-severity operational bugs found in a 40-batch production run and build automatic relay health management that ejects bad relays without manual intervention.

**Status:** All 16 requirements delivered across Phases 05–08 (VALID, FILTER, RELAY, LOG, PERF, TIMEOUT, METRIC). Phase 08 (frontier prioritization, 15s timeout, EOSE-quorum, honest staleRemaining) verified live on the production host.

**Target fixes:**
- **VALID**: `updateFollowsFromEvent` uses `nostr.GetPublicKey` as a validator (semantically wrong — it's a private-key→public-key derivation); 19 garbage pubkeys already in DB re-enter every batch permanently. Fix validator to hex regex; purge bad nodes; stamp invalid pubkeys in `MarkAttempted` via UID to age them out.
- **FILTER**: `batchSize` of 500 causes 40% of relays to reject or crash on every batch. Reduce to 100; parse NOTICE "filter item too large" to track per-relay caps; detect connection-drop-on-REQ pattern.
- **PERF**: `GetStalePubkeys` queries stubs uniformly, yielding 0.76% event hit rate (99% wasted cycles). Reorder frontier by `count(~follows) DESC`; exponentially back off long-miss stubs after N failed attempts.
- **RELAY-HEALTH**: Failure counter resets to 0 on reconnect, so flapping relays never graduate to "removed". Persist/decay failure count across reconnects; classify failure reasons; auto-eject relays exceeding configurable thresholds per failure class. Learned filter caps also reset on reconnect, re-running the halving cascade every batch — persist them.
- **LOGGING**: Production logs dominated by per-relay noise — ~100 reconnect lines per sweep, 6-line cap-halving cascades per relay per batch, duplicate dead/timeout pairs. Aggregate to one-line-per-state-change summaries.
- **TIMEOUT**: 44% of relays exceed the 30s EOSE timeout. Reduce to 15s; add EOSE-quorum early exit (cancel at ≥70% EOSE/errored).
- **METRIC**: `staleRemaining` is always 0 due to off-by-one in metric formula.

## Requirements

### Validated

- ✓ Subscribes to kind-3 events from many configured relays and writes a pubkey follow-graph to Dgraph — existing (`cmd/crawler`, `pkg/crawler`)
- ✓ Upsert-based pubkey nodes with `@unique @index(exact)`, follow-edge mutations, schema management — existing (`pkg/dgraph/dgraph.go`)
- ✓ Selects stale pubkeys to (re)crawl via `GetStalePubkeys` — existing (`pkg/dgraph`)
- ✓ Cluster/trust analysis and weak-bridge detection — existing (`pkg/dgraph/clusterscan.go`, `cmd/clusterscan`)
- ✓ Supporting CLIs: relay discovery, pubkey export, healthcheck — existing (`cmd/discover-relays`, `cmd/pubkeys`, `cmd/healthcheck`)
- ✓ **Crawler reaches the uncrawled frontier** — stub pubkeys selected frontier-first; coverage grows past seed neighbourhood — shipped v1.0 (Phase 01-02)
- ✓ **`last_attempt` predicate + attempt tracking** — never-attempted nodes selected first; every queried pubkey stamped via `MarkAttempted`; one-time backfill applied — shipped v1.0 (Phase 01-02)
- ✓ **Chunked writes persist all follows** — `>10k`-follow pubkeys fully written; per-chunk `kind3CreatedAt` version guard fixed; `defer cancel()` leak eliminated — shipped v1.1 (Phase 03)
- ✓ **Regression coverage** — unit + integration tests cover chunk/version-guard logic — shipped v1.1 (Phase 03)
- ✓ **VALID-01/02/03** — hex-regex pubkey validation; garbage purge; UID-stamped invalid nodes age out — shipped v1.2 (Phase 05)
- ✓ **FILTER-01/02** — batchSize 500→100; NOTICE "filter item too large" parsing + per-relay cap detection — shipped v1.2 (Phase 06)
- ✓ **RELAY-01/02/03** — persisted/decayed failure counters; per-class classification + configurable auto-ejection; learned filter caps persist across reconnects — shipped v1.2 (Phase 07)
- ✓ **LOG-01/02/03** — one-line reconnect-sweep / filter-cap / relay-death summaries — shipped v1.2 (Phase 07)
- ✓ **PERF-01/02** — frontier ordered by `count(~follows) DESC`; geometric backoff (`next_attempt`/`miss_count`) for chronic-miss stubs — shipped v1.2 (Phase 08)
- ✓ **TIMEOUT-01/02** — per-batch timeout 30s→15s; EOSE-quorum early exit at ≥70% — shipped v1.2 (Phase 08)
- ✓ **METRIC-01** — honest `staleRemaining` via `CountStalePubkeys` — shipped v1.2 (Phase 08)

### Active

_All v1.2 requirements delivered (Phases 05–08). Milestone v1.2 complete — no active requirements. Code-review hardening follow-ups (WR-02/03/04/05) and a transient-Dgraph-error retry are tracked in `.planning/todos/pending/` for a future cycle._

### Out of Scope

- Manual relay removal by name — automatic ejection (RELAY-01/02) replaces this.
- `RemoveFollower` injection hardening (SEC-01/02) — latent dead code; documented as a future idea.
- Whitelist-plugin problems, quarantine false-positives, caching — out of this module; deferred to dedicated milestones.
- Any change to StrFry itself — protocol rule: StrFry stays unmodified.

## Context

- Brownfield module; codebase mapped to `.planning/codebase/` (commit `9be572e`).
- v1.1 shipped Phase 3 (write-path correctness + regression coverage). Phase 4 (RemoveFollower injection hardening, SEC-01/02) was deferred — documented as a future idea, not part of any active milestone.
- v1.2 is motivated by a 40-batch production run (20,000 pubkeys queried): 172 relays, 38s avg batch time, 789 pubkeys/min, 0.76% event hit rate, 43 new nodes added. Full analysis in `.planning/research/` or conversation history.
- Key root causes: `nostr.GetPublicKey` misused as pubkey validator; 500-author filter exceeds most relay limits; stale frontier ordered by age not by graph significance (follower count).
- Tech: Go 1.24.1+, `go-nostr`, `dgo/v210` Dgraph gRPC client, `viper` for YAML config.

## Constraints

- **Tech stack**: Go module; Dgraph via gRPC (`localhost:9080`) and DQL over HTTP (`localhost:8080`). Must stay compatible with the existing `Profile` schema.
- **Config**: Live config lives at `~/deepfry/web-of-trust.yaml` — never edit it for testing; use a temp `HOME` (per `8pc_crawled.md` §6).
- **Data separation**: ID-only graph in Dgraph; no event payloads. StrFry unmodified.
- **Testing**: Integration tests gate on a live Dgraph via `//go:build integration` / `make test-integration`. No unit-test suite exists yet.
- **Verification**: requires running the crawler against live Dgraph + relays on the strfry host (manual step, per spec §6).

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Add `last_attempt` predicate distinct from `last_db_update` | Distinguishes "tried" from "successfully ingested" so un-fetchable pubkeys converge | ✓ Shipped v1.0 |
| v1.1 scoped to write-path integrity + hardening | Chunk data-drop is high-severity (silent loss undercutting core value); bundled with the adjacent leak and test-coverage gaps | ✓ Shipped v1.1 |
| Defer `RemoveFollower` injection hardening (SEC-01/02) | Latent dead code with no callers; low urgency; documented as future idea rather than blocking a milestone | ✓ Deferred v1.1 Phase 4 |
| v1.2 auto-ejection over manual relay removal | Hard-coded relay blacklists don't scale and require manual ops; classify failure reasons and auto-eject based on configurable thresholds | — Pending |
| Reduce `batchSize` 500 → 100 | 40% of relay pool rejects 500-author filters; 100 stays within all known relay limits including StrFry default | — Pending |
| Frontier ordered by follower count | High-follower stubs more likely to have kind-3 events; reduces wasted cycles from 99.24% | — Pending |
| Persist learned filter caps across reconnects (RELAY-03) | Reverses Phase 6's reset-on-reconnect: re-learning the cap every batch re-runs the halving cascade, re-kills floor-capped relays, and floods logs | — Pending |
| Logging noise (LOG-01/02/03) folded into Phase 7 | All three touch the relay state machine Phase 7 already rewrites; avoids touching the same code in two phases | — Pending |

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
*Last updated: 2026-06-13 — Phase 08 complete; milestone v1.2 fully delivered (PERF-01/02, TIMEOUT-01/02, METRIC-01 shipped, verified live)*
