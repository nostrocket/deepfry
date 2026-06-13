# Phase 8: Frontier Prioritization, Timeout & Observability - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-13
**Phase:** 8-frontier-prioritization-timeout-observability
**Areas discussed:** Backoff schedule (PERF-02), Frontier ordering (PERF-01), EOSE-quorum & timeout (TIMEOUT-01/02), staleRemaining metric (METRIC-01)

---

## Backoff schedule (PERF-02)

The user paused the first question set to ask "what problem are we trying to solve?" — reframed around the 0.76% production hit rate (99% of batch capacity wasted on pubkeys that return no kind-3 event). PERF-02's job: stop wasting batch slots on chronic-miss stubs without abandoning them (core value = keep expanding the WoT). User then directed: "Exponential dropoff starting at 2 hours."

| Option | Description | Selected |
|--------|-------------|----------|
| miss_count predicate + advance last_attempt | Increment miss_count, stamp last_attempt forward | (initially leaned, then superseded) |
| Separate next_attempt predicate | next_attempt eligibility timestamp; aged query filters lt(next_attempt, now) | ✓ |

**User's choice (mechanism):** next_attempt + miss_count predicates. Surfaced because the chosen 2h base is below the existing 24h StalePubkeyThreshold, so "advance last_attempt forward" would require stamping into the past (field would lie). next_attempt governs eligibility directly; 24h becomes the hit-refresh cadence.

| Option | Description | Selected |
|--------|-------------|----------|
| Geometric ×4 | base × 4^miss | |
| Geometric ×2 (2h, 4h, 8h, 16h, 32h, ...) | gentle dropoff | ✓ |

**User's choice (ratio):** ×2 from a 2h base.

| Option | Description | Selected |
|--------|-------------|----------|
| ~30 days cap | monthly recheck | |
| ~7 days cap | weekly recheck of dormant stubs | ✓ |
| ~90 days cap | very long tail | |

**User's choice (cap):** 7 days. Backoff kicks in immediately on the first miss (implied by "starting at 2 hours" — first miss → ~2h retry; no grace period).

**Notes:** HIT → next_attempt = now + 24h, miss_count = 0. MISS → next_attempt = now + min(2h × 2^miss_count, 7d), increment. One-time backfill of existing attempted nodes (next_attempt = last_attempt + 24h). Params config-driven.

---

## Frontier ordering (PERF-01)

| Option | Description | Selected |
|--------|-------------|----------|
| Both frontier and aged | order both selection phases by count(~follows) DESC | ✓ |
| Frontier only | order only the never-attempted frontier | |

**User's choice (scope):** Both phases.

| Option | Description | Selected |
|--------|-------------|----------|
| orderdesc with explicit first: — verify safe | value-bearing sort; confirm 1000-row cap doesn't truncate | ✓ |
| Add stored follower_count predicate | maintain indexed count, sort on it | (fallback only) |

**User's choice (1000-cap):** orderdesc + explicit first:N, with a flagged verification item against live Dgraph. Stored follower_count predicate kept as documented fallback if verification fails.

**Notes:** The existing GetStalePubkeys docstring warns Dgraph caps unbounded sorted queries at 1000 rows — researcher/planner must confirm follower-count sort isn't truncated before applying.

---

## EOSE-quorum & timeout (TIMEOUT-01/02)

| Option | Description | Selected |
|--------|-------------|----------|
| Both config-driven | timeout default 30s→15s; new relay_eose_quorum key (0.70) | ✓ |
| Timeout config, quorum constant | hardcode 70% | |

**User's choice (tunable):** Both config-driven.

| Option | Description | Selected |
|--------|-------------|----------|
| atomic counter + cancel at threshold | shared atomic 'done', cancel() at quorum | ✓ |
| eoseChan alongside errorsChan | explicit channel plumbing | |

**User's choice (signal):** Atomic counter (reuses Phase 6/7 atomic.Int32 pattern); cancel relay query context at done >= ceil(0.70 × queried).

| Option | Description | Selected |
|--------|-------------|----------|
| 70% of alive relays at batch start | snapshot at launch | |
| 70% of relays actually queried | explicit | ✓ |

**User's choice (denominator):** Relays actually queried this batch. A relay dying mid-batch counts toward done.

---

## staleRemaining metric (METRIC-01)

| Option | Description | Selected |
|--------|-------------|----------|
| Match selection exactly (frontier + aged) | NOT has(last_attempt) + (next_attempt < now) | ✓ |
| Aged-eligible only | just next_attempt < now | |

**User's choice (count def):** Match selection semantics exactly so staleRemaining reflects real outstanding work.

| Option | Description | Selected |
|--------|-------------|----------|
| Run it every batch | indexed count() is cheap | ✓ |
| Refresh every N batches / cache | avoid per-batch cost | |

**User's choice (cost):** Run every batch; cache only if proven expensive on the production graph.

**Notes:** Bug confirmed — staleRemaining = totalStale - len(pubkeys) where totalStale = len(pubkeys), always 0. Fix uses a real CountStalePubkeys query minus batch size.

---

## Claude's Discretion

- Exact config key names / YAML structure for PERF-02 backoff params and quorum.
- Whether the hit/miss stamp split extends MarkAttempted's signature, adds a sibling, or passes a hit-set map.
- Exact DQL shape of ordered frontier/aged queries and CountStalePubkeys.
- How the atomic quorum counter + cancel() wiring sits inside the FetchAndUpdateFollows select loop.
- Test layout (unit backoff-math helpers; integration ordering/count/backfill).

## Deferred Ideas

- Stored follower_count predicate as primary sort key (only if D-09 verification fails).
- Caching / periodic refresh of CountStalePubkeys (only if per-batch count is expensive).
- Per-relay metrics endpoint / structured observability (OBS-01) — future milestone.
- Relay re-discovery (DISC-01) — future milestone.
