# Web-of-Trust Crawler

## What This Is

The `web-of-trust` Go module is a Nostr crawler that subscribes to kind-3 (contact list) events from many relays and stores an ID-only pubkey follow-graph in Dgraph. It is one of several sidecar services around a stock StrFry Nostr relay in the DeepFry stack; it never stores event payloads (those live in StrFry's LMDB) — only pubkey relationships. The follow-graph feeds trust scoring and the whitelist plugin that decides which pubkeys may write events.

## Core Value

The crawler must continuously **expand** the web of trust — discovering and fetching contact lists for newly-seen pubkeys — not just re-refresh the accounts it already knows.

## Current State: v1.5 Dgraph Follow-Update Timeout Resilience — PLANNED (2026-06-18)

**Goal:** A slow or oversized Dgraph follow-update must not abort the crawler batch or stop crawl progress.

**Status:** New milestone opened from the 2026-06-18 production failure where batch 1 queried 1000 pubkeys, found 567 events, spent ~47s outside relay fetch, then aborted while updating follows for pubkey `314072c16fa9433e1374f62e5b02c8163946ed298a9cde3b1541513c29d19fff`: `failed to add follows: query follower failed: rpc error: code = DeadlineExceeded desc = context deadline exceeded`. The failure points at the follow-write path (`AddFollowers` / follower lookup / mutation), not relay-query liveness. Phase 12 will make Dgraph follow updates bounded, retryable where safe, observable, and non-fatal to the overall crawl loop.

**Target fixes:**
- **DWRITE**: Bound and harden `AddFollowers` / follow-edge update paths so one large pubkey or slow follower lookup cannot abort the run.
- **RETRY**: Treat transient Dgraph write-path `DeadlineExceeded` as recoverable at the pubkey/update level while preserving fatal handling for non-transient errors.
- **CORRECTNESS**: Preserve graph correctness under partial progress; no duplicate edge corruption, no event payload storage, no StrFry changes.
- **OBSERVABILITY**: Log enough operation/pubkey/follow-count/timing context to diagnose slow follow updates in production without raw secrets or event payloads.
- **TESTING**: Add deterministic regression coverage for timeout/retry/partial-progress behavior without requiring live Dgraph for unit tests.

## Previous State: v1.4 Crawler Hang Fix (Relay-Query Liveness) — SHIPPED (2026-06-16)

**Goal:** A single stuck or half-open relay must never wedge the crawler — `FetchAndUpdateFollows` must always return on its own relay-query timeout, without leaking goroutines unboundedly.

**Status:** All 4 requirements delivered in a single phase (Phase 11), closing the ~48-minute production hang diagnosed via SIGQUIT goroutine dump (root cause in `web-of-trust/HANG-FINDINGS.md`). The dispatcher now exits independently of `wg.Wait()` / `eventsChan` close (HANG-01); `relay.Subscribe` runs in a child goroutine with a ctx-select so `queryRelay` returns on timeout even when go-nostr's `Fire()` ignores the context (HANG-02); and outstanding relays are closed + marked-dead on the timeout path / closed without penalty on the quorum-cancel path, reaping leaked goroutines (HANG-03). go-nostr exposes no write-deadline API, so the "or equivalent keepalive" clause was satisfied by connection-close rather than a literal write deadline. An adversarial code-review loop caught and fixed 2 critical concurrency defects (range-during-compaction; quorum-path subscription leak) before the milestone closed; the `completedThisBatch` boolean was hardened to a per-batch generation token. Verified: `make test` green, three liveness tests pass under `-race`. Full archive: `milestones/v1.4-ROADMAP.md`, `milestones/v1.4-REQUIREMENTS.md`, `milestones/v1.4-MILESTONE-AUDIT.md`.

**Next milestone:** v1.5 Dgraph Follow-Update Timeout Resilience — see Current State above. Deferred candidates: TUNE-01 (config-driven retry/timeout backoff via `web-of-trust.yaml`), the v1.2 nice-to-haves (IN-01/02/04), and the Future Requirements backlog (DISC, SEC, TEST-05).

## Previous State: v1.3 Unbounded Dgraph Retry Resilience — SHIPPED (2026-06-15)

**Goal:** The crawler must survive any-length Dgraph outage without exiting — retrying transient gRPC errors indefinitely with exponential backoff instead of giving up after 5 attempts.

**Status:** All 8 requirements delivered in a single phase (Phase 10). A generic `retryDgraph[T]` helper replaced the four bounded 5-attempt retry blocks: indefinite transient-error retry, 1m→2m→4m→5m capped backoff, ctx-cancel-aware sleep (clean SIGINT/SIGTERM shutdown mid-backoff), and per-call-type cumulative-average duration logging. `ResourceExhausted` was reclassified fatal during code review to prevent indefinite-retry livelock on the ~4MB gRPC message-size limit. Full archive: `milestones/v1.3-ROADMAP.md`, `milestones/v1.3-REQUIREMENTS.md`, `milestones/v1.3-MILESTONE-AUDIT.md`.

**Next milestone:** v1.4 Crawler Hang Fix (Relay-Query Liveness) — see Current Milestone above. Deferred candidates remain: TUNE-01 (config-driven retry backoff via `web-of-trust.yaml`), the v1.2 nice-to-haves (IN-01/02/04), and the Future Requirements backlog (DISC, SEC, TEST-05).

## Previous State: v1.2 Crawler Reliability & Efficiency — SHIPPED (2026-06-15)

**Goal:** Fix three high-severity operational bugs found in a 40-batch production run and build automatic relay health management that ejects bad relays without manual intervention.

**Status:** All 21 requirements delivered across Phases 05–09 (VALID, FILTER, RELAY, LOG, PERF, TIMEOUT, METRIC, HARD, RESIL). Phases 08 and 09 (frontier prioritization, 15s timeout, EOSE-quorum, honest staleRemaining; then the deferred hardening follow-ups + transient-error resilience) were verified live on the production host. Full archive: `milestones/v1.2-ROADMAP.md` and `milestones/v1.2-REQUIREMENTS.md`.

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
- ✓ **HARD-01/02/03/04** — paginated `BackfillNextAttempt`; inline-discard `MarkAttempted` recovery-txn hygiene; bounded `forwardEvent` publish; documented large-frontier sort-cap guarantee — shipped v1.2 (Phase 09)
- ✓ **RESIL-01** — main crawl loop classifies transient Dgraph gRPC errors and retries with backoff instead of exiting — shipped v1.2 (Phase 09)
- ✓ **RETRY-01/02/03** — indefinite transient-retry via generic `retryDgraph[T]` helper; read calls break `mainLoop` on fatal, `MarkAttempted` warns-and-continues — shipped v1.3 (Phase 10)
- ✓ **BACKOFF-01/02** — backoff 1m→2m→4m→5m (capped), applied to all four main-loop Dgraph calls — shipped v1.3 (Phase 10)
- ✓ **SHUTDOWN-01** — mid-backoff `select` on `ctx.Done()` interrupts immediately; fatal non-transient errors still exit loudly — shipped v1.3 (Phase 10)
- ✓ **OBS-01** — per-call-type cumulative-average duration logged each batch via `callMetrics` — shipped v1.3 (Phase 10)
- ✓ **TEST-01** — deterministic `package main` unit tests (backoff sequence, ctx-cancel, fatal passthrough, transient+success) via injected sleep — shipped v1.3 (Phase 10)
- ✓ **HANG-01** — `FetchAndUpdateFollows` returns on its own relay-query timeout via independent dispatcher exit + non-blocking drain (no `wg.Wait()`/`eventsChan` gating) — shipped v1.4 (Phase 11)
- ✓ **HANG-02** — `queryRelay` bounds `relay.Subscribe` in a child goroutine + ctx-select, returning on timeout even when go-nostr `Fire()` ignores the context — shipped v1.4 (Phase 11)
- ✓ **HANG-03** — outstanding relays closed + marked-dead on timeout (closed without penalty on quorum-cancel), reaping leaked goroutines; per-batch generation token replaces the marker boolean — shipped v1.4 (Phase 11)
- ✓ **TEST-02** — regression + partial-progress + close-on-timeout + quorum-close tests; `make test` green, all pass under `-race` — shipped v1.4 (Phase 11)

_v1.2 requirements all delivered (Phases 05–09); v1.3 (Phase 10) and v1.4 (Phase 11) all delivered. v1.5 is active for Dgraph follow-update timeout resilience. Remaining nice-to-haves (IN-01/02/04) and the "Future Requirements" backlog (DISC, SEC, TUNE-01, TEST-05) remain deferred to a later milestone._

### Active

- [ ] **DWRITE-01**: A transient Dgraph `DeadlineExceeded` during follow updates does not abort the crawler process or discard the rest of the batch.
- [ ] **DWRITE-02**: Follow-edge writes for large contact lists are bounded into safe units with per-unit context deadlines and partial-progress accounting.
- [ ] **DWRITE-03**: Failed follow updates leave the pubkey eligible for a later retry without corrupting existing follow edges or permanently aging it out as a clean miss.
- [ ] **DWRITE-04**: Fatal Dgraph write errors still fail loudly rather than being hidden by the transient-retry path.
- [ ] **OBS-02**: Production logs identify slow follow-update operations by pubkey, follow-count/chunk, elapsed time, retry count, and final outcome.
- [ ] **TEST-06**: Unit or integration-style tests cover timeout classification, chunk/partial-progress behavior, retry scheduling, and fatal-error passthrough.

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
- v1.5 production signal: with `RelayFilterBatchSize` effectively at 1000 for the measured run, relay fetch was ~13.8s but total batch time was ~61.2s; Dgraph overhead dominated before the follow-update path hit `DeadlineExceeded`.
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
| v1.2 auto-ejection over manual relay removal | Hard-coded relay blacklists don't scale and require manual ops; classify failure reasons and auto-eject based on configurable thresholds | ✓ Shipped v1.2 (Phase 07) |
| Reduce `batchSize` 500 → 100 | 40% of relay pool rejects 500-author filters; 100 stays within all known relay limits including StrFry default | ✓ Shipped v1.2 (Phase 06) |
| Frontier ordered by follower count | High-follower stubs more likely to have kind-3 events; reduces wasted cycles from 99.24% | ✓ Shipped v1.2 (Phase 08) |
| Persist learned filter caps across reconnects (RELAY-03) | Reverses Phase 6's reset-on-reconnect: re-learning the cap every batch re-runs the halving cascade, re-kills floor-capped relays, and floods logs | ✓ Shipped v1.2 (Phase 07) |
| Logging noise (LOG-01/02/03) folded into Phase 7 | All three touch the relay state machine Phase 7 already rewrites; avoids touching the same code in two phases | ✓ Shipped v1.2 (Phase 07) |
| Open Phase 9 follow-up rather than carry Phase 8 review warnings as tech debt | At the v1.2 close gate, the deferred WR-02/03/04/05 + transient-retry todos were resolved in a dedicated phase (Phase 08 was already verified, so a follow-up phase beat a `--force` replan) | ✓ Shipped v1.2 (Phase 09) |
| v1.3 retries transient Dgraph errors forever (not bounded) | RESIL-01's 5-attempt cap (~2.5min) still exits the crawler on longer Dgraph outages; an unattended crawler should recover whenever Dgraph returns rather than dying. Operator SIGINT/SIGTERM remains the only stop. | Planned v1.3 |
| v1.3 backoff 1min start, 5min cap (was 5s → 2min) | A down Dgraph won't recover in seconds; starting at 1min avoids log spam and pointless rapid retries, 5min cap keeps recovery prompt once it returns | ✓ Shipped v1.3 (Phase 10) |
| v1.3 single generic `retryDgraph[T]` helper over four near-identical blocks | Collapses four bounded retry blocks into one indefinite-retry helper; injected sleep fn enables deterministic unit tests without a live Dgraph | ✓ Shipped v1.3 (Phase 10) |
| v1.4 bound the hang at the crawler (dispatcher exit + conn-close) rather than fork go-nostr | go-nostr v0.52's `Fire()` ignores the per-call ctx and exposes no write-deadline API; closing the connection cancels the relay's connectionContext to reap the parked write loop + leaked goroutine — satisfies HANG-03's "or equivalent keepalive" without forking | ✓ Shipped v1.4 (Phase 11) |
| v1.4 per-batch generation token instead of a reset-to-false marker boolean | A monotonic generation has no reset to race, eliminating the cross-batch marker race a leftover goroutine could otherwise cause | ✓ Shipped v1.4 (Phase 11) |
| v1.5 scoped to Dgraph follow-update timeout resilience before broader throughput tuning | The latest production failure is a correctness/liveness break in the write path; the broader crawl-speed backlog remains useful but should not hide the immediate abort condition | — Pending |
| v1.5 continues phase numbering with Phase 12 | v1.4 shipped Phase 11 and config uses sequential phase naming | — Pending |

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
*Last updated: 2026-06-18 after v1.5 milestone start — Dgraph Follow-Update Timeout Resilience planned from production `DeadlineExceeded` failure.*
