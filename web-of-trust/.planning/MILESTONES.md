# Milestones

## v1.6 Crawl Throughput Optimization (Shipped: 2026-06-20)

**Phases completed:** 2 phases, 2 plans, 8 tasks

**Key accomplishments:**

- Dgraph frontier sizing, sampled count queries, exact batch accounting, and relay-cap safety coverage for crawler throughput tuning
- Replaced the per-call count(~follows) frontier aggregate in GetStalePubkeys with a stored, int-indexed follower_count predicate — maintained cheaply (+1/-1) in AddFollowers' existing transaction and backfilled by a new idempotent operator CLI.

**Phase breakdown:** 13 (Main-Loop Throughput Controls) · 14 (Frontier Read-Path Throughput — `follower_count`)

**Requirements:** 16/16 delivered (LOOP-01/02/03/04, COUNT-01/02/03, MEASURE-01/02/03, DSCALE-01/03, TEST-01/02/03)

**Live verification:** `GetStalePubkeys` ~119s → ~1.3s on the 1.38M-node production Dgraph (frontier 69s→0.01s via `eq(uncrawled,1)`; aged 50s→1.3s via `ge(follower_count,0)`); `follower_count` backfilled full-graph (~2.5 min, idempotent, exact). See `milestones/v1.6-phases/14-.../14-VERIFICATION.md`.

**Git range:** `ccbca03` (fix(13)) → `6d095b8` (docs(14) live-verified) — 28 commits, 27 files (+3954/-224).

**Known deferred (operational):** new crawler binary not yet redeployed to production. Read-path speedup is live-verified against the production Dgraph but not yet running in the deployed crawler. Cutover = redeploy binary + one-time `uncrawled=1` safety seed for never-attempted nodes (per 14-VERIFICATION.md runbook). Tracked in PROJECT.md → Current Focus.

---

## v1.5 Dgraph Follow-Update Timeout Resilience (Shipped: 2026-06-18)

**Phases completed:** 1 phases, 1 plans, 3 tasks

**Key accomplishments:**

- Dgraph follow updates now fail transiently per pubkey, preserve atomic kind-3 graph writes, and leave skipped pubkeys retry-eligible.

**Phase breakdown:** 12 (Dgraph Follow-Update Resilience)

**Requirements:** 6/6 delivered (DWRITE-01/02/03/04, OBS-02, TEST-06)

**Git range:** `0b646d5` (feat(12-01)) → `30bcb8e` (phase-12 close)

---

## v1.4 Crawler Hang Fix (Relay-Query Liveness) (Shipped: 2026-06-16)

**Phases completed:** 1 phases, 1 plans, 3 tasks

**Key accomplishments:**

- Eliminated 48-minute crawler hang by making FetchAndUpdateFollows return within a bounded multiple of c.timeout via child-goroutine-bounded Subscribe (HANG-02), independent dispatcher timeout exit (HANG-01), and conn-close+mark-dead for outstanding relays on timeout (HANG-03). Adversarial code review found+fixed 2 critical concurrency defects; the marker boolean was hardened to a per-batch generation token before close.

---

## v1.3 Unbounded Dgraph Retry Resilience (Shipped: 2026-06-15)

**Phases completed:** 1 phases, 1 plans, 0 tasks

**Key accomplishments:**

- Generic `retryDgraph[T]` helper with 1m→5m indefinite backoff, ctx-cancel-aware sleep, and per-call-type cumulative-average observability replaces four bounded 5-attempt retry blocks in the crawler main loop.

---

## v1.2 Crawler Reliability & Efficiency (Shipped: 2026-06-15)

**Phases completed:** 5 phases, 11 plans, 21 tasks

**Key accomplishments:**

- Replaced misused key-derivation validator (nostr.GetPublicKey) with hex-regex validator (dgraph.ValidatePubkey) at both crawler call sites, added inline recover-or-purge to MarkAttempted so garbage nodes self-clean, and added unit tests covering all live-DB garbage types.
- Added two integration tests gated behind `//go:build integration` proving MarkAttempted recovers uppercase nodes to lowercase (last_attempt unset) and purges unrecoverable garbage, and that AddFollowers writes zero garbage-pubkey nodes when given a garbage-laden follow-list.
- relay_filter_batch_size config field (default 100) wired through crawler with per-relay filterCap and NOTICE-based cap halving via WithNoticeHandler at both connect sites.
- queryRelay refactored with filterCap-sized chunked sub-REQ loop, 500ms connection-drop attribution, and drainSubscription helper; 6 unit tests cover all cap-halving behaviors.
- Three-class failure counters with halving decay, probe-up filter-cap recovery, and single-line-per-state-change logging replace the single-counter reset-on-reconnect state machine
- queryRelay now returns typed errors and never calls markRelayDead; the single-threaded FetchAndUpdateFollows dispatcher is the sole markRelayDead caller; filterRejectionError closes the WR-01 mis-classification; startup failures no longer eject relays; hoisted probe defer closes WR-03; real-seam tests close WR-05.
- Additive `next_attempt`/`miss_count` predicates, follower-count-ordered frontier selection (PERF-01), hit/miss exponential-backoff stamping with idempotent backfill (PERF-02), and the honest `CountStalePubkeys` metric (METRIC-01) — the data layer Wave 2's crawler wiring consumes.
- Wired Plan 01's data layer into the live crawler: 15s timeout (config-driven), EOSE-quorum early-exit at ≥70% queried-relay completion, hit-set-driven PERF-02 stamping, honest non-zero `staleRemaining` via `CountStalePubkeys`, and one-time startup backfill — verified live against production Dgraph + 148 relays.
- Closed the deferred Phase 8 code-review warnings (Phase 9): paginated `BackfillNextAttempt` (HARD-01), inline-discard `MarkAttempted` recovery-txn hygiene (HARD-02), bounded `forwardEvent` publish so a hung forward relay can't stall the drain loop (HARD-03), documented the live-verified large-frontier sort-cap guarantee (HARD-04), and made the main crawl loop classify transient Dgraph gRPC errors and retry with 5s→2m backoff instead of exiting (RESIL-01) — live-approved on the strfry host.

**Phase breakdown:** 5 (Pubkey Validation Hardening) · 6 (Filter Size & Per-Relay Cap Detection) · 7 (Relay Health Management) · 8 (Frontier Prioritization, Timeout & Observability) · 9 (Phase 8 Hardening & Resilience Follow-ups)

**Requirements:** 21/21 delivered (VALID-01/02/03, FILTER-01/02, RELAY-01/02/03, LOG-01/02/03, PERF-01/02, TIMEOUT-01/02, METRIC-01, HARD-01/02/03/04, RESIL-01)

**Git range:** `e9c0efa` (feat(05-01)) → `40d70d9` (phase-09 close)

---
