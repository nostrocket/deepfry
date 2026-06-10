# Roadmap: Web-of-Trust Crawler — v1.2 Crawler Reliability & Efficiency

**Milestone:** v1.2 — Fix three high-severity operational bugs found in a 40-batch production run and build automatic relay health management
**Created:** 2026-06-10
**Granularity:** Coarse
**Coverage:** 12/12 v1.2 requirements mapped
**Numbering:** Continues from v1.1 (last phase was Phase 4) — this milestone starts at Phase 5

## Phases

- [ ] **Phase 5: Pubkey Validation Hardening** - Fix the validator bug, purge existing garbage pubkeys from Dgraph, and ensure MarkAttempted ages invalid nodes out of the frontier
- [ ] **Phase 6: Filter Size & Per-Relay Cap Detection** - Reduce batch size to 100 and detect per-relay filter caps from NOTICE messages and connection-drop-on-REQ patterns
- [ ] **Phase 7: Relay Health Management** - Persist and decay failure counters across reconnects, classify failure reasons into buckets, and auto-eject relays that exceed configurable per-class thresholds
- [ ] **Phase 8: Frontier Prioritization, Timeout & Observability** - Order the stale frontier by follower count, apply exponential backoff to long-miss stubs, cut relay timeout to 15s, add EOSE-quorum early exit, and fix the staleRemaining metric

## Phase Details

### Phase 5: Pubkey Validation Hardening
**Goal**: Invalid pubkeys never enter Dgraph and existing garbage nodes age out of the stale frontier without manual intervention
**Depends on**: Phase 4 (v1.1 — shipped)
**Requirements**: VALID-01, VALID-02, VALID-03
**Success Criteria** (what must be TRUE):
  1. A p-tag containing an uppercase hex string, a relay URL blob, or a truncated value is rejected at `updateFollowsFromEvent` — the hex regex `^[0-9a-f]{64}$` rejects it, it is logged, and nothing is written to Dgraph for that value.
  2. After the startup migration or `healthcheck -purge` run, a DQL query for pubkeys not matching `^[0-9a-f]{64}$` returns zero results — the 19 known garbage nodes and any others are gone. (Per phase CONTEXT.md, the explicit migration step VALID-02 is dropped; VALID-03's inline recover/purge in `MarkAttempted` removes/corrects garbage nodes the first time they surface from the frontier.)
  3. When `MarkAttempted` is called for a pubkey that fails the hex validator, the node's `last_attempt` is updated via a UID-based mutation — the node no longer re-enters the stale frontier on every batch.
**Plans**: 2 plans
Plans:
- [ ] 05-01-PLAN.md — Validator swap (VALID-01), MarkAttempted recover-or-purge (VALID-03/02), validator unit tests
- [ ] 05-02-PLAN.md — Integration tests: MarkAttempted recover/purge (D-07) and end-to-end no-garbage write (D-08)

### Phase 6: Filter Size & Per-Relay Cap Detection
**Goal**: No relay rejects or drops a connection due to an oversized filter REQ, and relays with small caps are automatically queried at a safe size going forward
**Depends on**: Phase 5
**Requirements**: FILTER-01, FILTER-02
**Success Criteria** (what must be TRUE):
  1. The crawler issues REQ filters with at most 100 authors per request — verified by inspecting outbound messages to any relay; no relay receives a filter with more than 100 `authors` entries by default.
  2. When a relay sends a NOTICE containing "filter item too large" (or equivalent), `queryRelay` records the per-relay cap and subsequent REQs to that relay use chunked sub-queries at the detected limit — the relay is not discarded, just throttled.
  3. A relay that responds to a REQ by closing the connection (connection-drop-on-REQ pattern) is classified as having a small filter cap, and future batches to that relay are sized accordingly.
**Plans**: TBD

### Phase 7: Relay Health Management
**Goal**: Relays that repeatedly fail are automatically removed from the config without manual intervention, and failure tracking survives reconnects
**Depends on**: Phase 6
**Requirements**: RELAY-01, RELAY-02
**Success Criteria** (what must be TRUE):
  1. A relay that connects and immediately drops (flapping) accumulates failure counts across reconnect cycles — the counter is decayed on reconnect (e.g. halved) rather than reset to zero, so repeated flapping eventually pushes the count past `maxConsecutiveFailures`.
  2. Failure events are classified into at least three buckets — transport error, filter rejection (NOTICE or connection-drop on REQ), and subscription flap — and per-class ejection thresholds are readable from config.
  3. When a relay's failure count for any class exceeds its configured threshold, it is removed from the config file and a log line records which relay was ejected and why.
**Plans**: TBD

### Phase 8: Frontier Prioritization, Timeout & Observability
**Goal**: The crawler spends batch capacity on pubkeys most likely to return kind-3 events, exits batches early when enough relays have responded, and reports accurate progress metrics
**Depends on**: Phase 5
**Requirements**: PERF-01, PERF-02, TIMEOUT-01, TIMEOUT-02, METRIC-01
**Success Criteria** (what must be TRUE):
  1. `GetStalePubkeys` returns results ordered by incoming follower count descending — a pubkey with 1,000 followers appears before a stub with 0 followers when both are equally stale.
  2. A pubkey that returns no kind-3 event after N consecutive crawl attempts has its `last_attempt` advanced by an increasing interval (e.g. 1h → 4h → 16h) — it is not queried on every cycle but is never permanently abandoned.
  3. The per-batch relay query timeout fires at 15s — relays that have not sent EOSE within 15s are cancelled and the batch moves on.
  4. A batch whose alive relays reach ≥70% EOSE or error exits early without waiting for the remaining relays or the full 15s timeout.
  5. The `staleRemaining` value printed in the crawler's progress log reflects the actual remaining stale count (a `CountStalePubkeys` query result minus the current batch size) — not zero.
**Plans**: TBD

## Progress Table

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 5. Pubkey Validation Hardening | 0/2 | Planned | - |
| 6. Filter Size & Per-Relay Cap Detection | 0/0 | Not started | - |
| 7. Relay Health Management | 0/0 | Not started | - |
| 8. Frontier Prioritization, Timeout & Observability | 0/0 | Not started | - |
