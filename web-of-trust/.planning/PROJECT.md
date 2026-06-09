# Web-of-Trust Crawler

## What This Is

The `web-of-trust` Go module is a Nostr crawler that subscribes to kind-3 (contact list) events from many relays and stores an ID-only pubkey follow-graph in Dgraph. It is one of several sidecar services around a stock StrFry Nostr relay in the DeepFry stack; it never stores event payloads (those live in StrFry's LMDB) — only pubkey relationships. The follow-graph feeds trust scoring and the whitelist plugin that decides which pubkeys may write events.

## Core Value

The crawler must continuously **expand** the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

## Current Milestone: v1.1 Write Integrity & Hardening

**Goal:** Eliminate silent data loss in the crawler's Dgraph write path and harden it — so large follow-lists persist completely, the chunk loop doesn't leak resources, the remove path can't be injected, and tests would catch a regression.

**Target fixes:**
- Chunked follow-lists silently drop data: `processFollowsInChunks` reuses one `kind3CreatedAt` across chunks, so `AddFollowers`' version guard (`pkg/dgraph/dgraph.go:165`) skips every chunk after the first. Pubkeys with >10k follows persist only the first 200 follows — directly undercutting the "expand the web of trust" core value.
- `defer cancel()` accumulates inside the chunk `for` loop (`pkg/crawler/chunks.go:39-40`) instead of freeing per-iteration.
- `RemoveFollower` builds DQL via raw string concatenation of unvalidated input (`pkg/dgraph/dgraph.go:344-355`), unlike the `$`-Vars / `%q` paths used elsewhere. Currently dead code (no callers) — latent, fixed for hygiene/defense-in-depth.
- Thin test coverage: a single integration-gated test exists and nothing would have caught the chunk bug. Add unit tests (no live Dgraph) for the chunk/version-guard logic. (`make test-integration` already exists in the Makefile.)

## Requirements

### Validated

<!-- Inferred from existing code (codebase map 9be572e). These ship today. -->

- ✓ Subscribes to kind-3 events from many configured relays and writes a pubkey follow-graph to Dgraph — existing (`cmd/crawler`, `pkg/crawler`)
- ✓ Upsert-based pubkey nodes with `@unique @index(exact)`, follow-edge mutations, schema management — existing (`pkg/dgraph/dgraph.go`)
- ✓ Selects stale pubkeys to (re)crawl via `GetStalePubkeys` — existing (currently defective, see Active)
- ✓ Cluster/trust analysis and weak-bridge detection — existing (`pkg/dgraph/clusterscan.go`, `cmd/clusterscan`)
- ✓ Supporting CLIs: relay discovery, pubkey export, healthcheck — existing (`cmd/discover-relays`, `cmd/pubkeys`, `cmd/healthcheck`)
- ✓ **Crawler reaches the uncrawled frontier** — stub pubkeys selected via `GetStalePubkeys` frontier-first; coverage grows past seed neighbourhood — shipped v1.0 (Phase 01-02)
- ✓ **`last_attempt` predicate + attempt tracking** — never-attempted nodes selected first; every queried pubkey stamped via `MarkAttempted`; one-time backfill applied — shipped v1.0 (Phase 01-02)

### Active

<!-- Milestone v1.1: write-path integrity + hardening. -->

- [ ] **Chunked writes persist all follows**: a `>10k`-follow pubkey's full follow-list is written to Dgraph, not just the first 200-item chunk — the per-chunk `kind3CreatedAt` version guard no longer discards chunks 2…N.
- [ ] **Chunk loop frees resources per-iteration**: the `defer cancel()` in `processFollowsInChunks` no longer accumulates contexts/timers until function return.
- [ ] **`RemoveFollower` is injection-safe**: the remove path uses `$`-Vars / `%q` (and/or hex-pubkey validation) instead of raw string concatenation, consistent with the rest of `pkg/dgraph`.
- [ ] **Regression coverage exists**: unit tests (no live Dgraph) cover the chunk/version-guard logic and fail against the pre-fix behaviour.

### Out of Scope

- Open-socket / relay-connection-scaling issues — separate concern in CONCERNS.md; deferred to a later milestone to keep this fix isolated.
- Whitelist-plugin problems, quarantine false-positives, caching — out of this module's fix; deferred to dedicated milestones.
- Any change to StrFry itself — protocol rule: StrFry stays unmodified.

## Context

- Brownfield module; codebase mapped to `.planning/codebase/` (commit `9be572e`). `CONCERNS.md` already documents the 8% crawl bug as the #1 issue.
- Root-cause analysis and the full fix are specified in `8pc_crawled.md` at the module root: exact code for the schema change, the `GetStalePubkeys` rewrite (`collectStale` helper), `MarkAttempted`, the crawler-loop wiring, a DQL backfill upsert, and an integration regression test.
- `GetStalePubkeys`'s only caller is `cmd/crawler/main.go:109` (verified); `ResolvePubkeysToUIDs` already exists in `pkg/dgraph/clusterscan.go:45`.
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
| Milestone scoped to the 8% crawl fix only | Tightly-specified, isolated change; other CONCERNS.md issues get their own milestones | — Pending |
| YOLO mode | Fix is fully specified with exact code; low ambiguity | — Pending |
| Skip per-phase research | `8pc_crawled.md` already contains root-cause evidence and exact code | — Pending |
| Add `last_attempt` predicate distinct from `last_db_update` | Distinguishes "tried" from "successfully ingested" so un-fetchable pubkeys converge | ✓ Shipped v1.0 |
| v1.1 scoped to write-path integrity + hardening | Chunk data-drop is high-severity (silent loss undercutting core value); bundled with the adjacent leak, injection, and test-coverage gaps in the same write path | — Pending |
| Fix `RemoveFollower` despite no callers | Latent injection + inconsistent with the rest of `pkg/dgraph`; cheap defense-in-depth | — Pending |

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
*Last updated: 2026-06-09 — milestone v1.1 (Write Integrity & Hardening) started*
