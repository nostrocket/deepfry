# Requirements: Web-of-Trust Crawler — Crawl Coverage Fix

**Defined:** 2026-06-09
**Core Value:** The crawler must continuously expand the web of trust — fetching contact lists for newly-seen pubkeys — not just re-refresh accounts it already knows.

## v1 Requirements

Requirements for this milestone (implementing `8pc_crawled.md`). Each maps to roadmap phases.

### Schema

- [ ] **SCHEMA-01**: The Dgraph schema includes an indexed `last_attempt: int @index(int)` predicate, and `last_attempt` is listed on the `Profile` type. (`EnsureSchema`, `pkg/dgraph/dgraph.go`)

### Selection

- [ ] **SEL-01**: `GetStalePubkeys` selects never-attempted nodes (the uncrawled frontier) via an explicit `NOT has(last_attempt)` query with an explicit `first:` limit — never via `orderasc` on a missing-value predicate, and never relying on Dgraph's default 1000-row cap.
- [ ] **SEL-02**: `GetStalePubkeys` accepts a `limit int` parameter and, after filling from the frontier, tops up the batch with previously-attempted nodes whose `last_attempt` is older than the threshold (ordered by `last_attempt`, bounded by an explicit `first:`).
- [ ] **SEL-03**: A `collectStale` helper runs a named-block stale query and merges `{pubkey -> kind3CreatedAt}` rows into the result map.

### Attempt Tracking

- [ ] **ATTEMPT-01**: A `MarkAttempted(ctx, pubkeys, ts)` method stamps `last_attempt = ts` on every given pubkey (resolving UIDs via the existing `ResolvePubkeysToUIDs`), so un-fetchable pubkeys age out of the frontier instead of being re-selected every cycle.
- [ ] **ATTEMPT-02**: The crawler loop passes a `batchSize` limit to `GetStalePubkeys` (removing the manual 500-cap block) and calls `MarkAttempted` for the whole queried batch immediately after `FetchAndUpdateFollows`. (`cmd/crawler/main.go`)

### Migration

- [ ] **MIG-01**: A one-time backfill seeds `last_attempt` from `last_db_update` for existing crawled nodes (run after the schema change), so already-crawled accounts are not re-prioritised as frontier.

### Verification

- [ ] **TEST-01**: An integration regression test (`//go:build integration`, run via `make test-integration`) asserts that a pure stub (no `last_attempt`) IS returned by `GetStalePubkeys` and that a freshly-attempted node is NOT returned as stale.
- [ ] **TEST-02**: The build passes (`make build-crawler`) and the single caller at `cmd/crawler/main.go:109` is updated for the new `GetStalePubkeys` signature.

## v2 Requirements

Deferred to future milestones (out of this fix; tracked in `.planning/codebase/CONCERNS.md`).

### Reliability

- **REL-01**: Address "too many open sockets" / relay-connection scaling.
- **REL-02**: Fix whitelist-plugin issues.
- **REL-03**: Reduce quarantine false-positives (legit events sent to quarantine).
- **REL-04**: Add caching layer.

### Tuning

- **TUNE-01**: Raise `stale_pubkey_threshold` toward the `86400` code default to spend more budget expanding the graph vs re-refreshing known accounts (optional; config at `~/deepfry/web-of-trust.yaml`).

## Out of Scope

| Feature | Reason |
|---------|--------|
| Modifying StrFry | Protocol rule: StrFry stays unmodified — extend only via plugins/external services |
| Editing `~/deepfry/web-of-trust.yaml` for testing | Live config must not change; use a temp `HOME` per spec §6 |
| Storing event payloads in Dgraph | Data-separation rule: Dgraph holds the ID-only graph; payloads live in StrFry LMDB |
| Open-socket / whitelist / quarantine / cache fixes | Separate concerns; their own future milestones (see v2) |

## Traceability

Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| SCHEMA-01 | TBD | Pending |
| SEL-01 | TBD | Pending |
| SEL-02 | TBD | Pending |
| SEL-03 | TBD | Pending |
| ATTEMPT-01 | TBD | Pending |
| ATTEMPT-02 | TBD | Pending |
| MIG-01 | TBD | Pending |
| TEST-01 | TBD | Pending |
| TEST-02 | TBD | Pending |

**Coverage:**
- v1 requirements: 9 total
- Mapped to phases: 0 (set during roadmap)
- Unmapped: 9 ⚠️ (resolved by roadmapper)

---
*Requirements defined: 2026-06-09*
*Last updated: 2026-06-09 after initial definition*
