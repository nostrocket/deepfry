# Phase 9: Phase 8 Hardening & Resilience Follow-ups - Context

**Gathered:** 2026-06-13
**Status:** Ready for planning
**Source:** Deferred Phase 8 code-review warnings (`08-REVIEW.md`) + 08-02 live-host verification finding, captured as `.planning/todos/pending/`

<domain>
## Phase Boundary

This phase closes the Phase 8 code-review warnings that were **deliberately deferred** to a dedicated pass (they touch live-verified runtime code and the sensitive VALID-03 block, so they were not edited inline during Phase 8), plus one resilience gap found during the 08-02 live-host run. All items are latent failure modes the live run happened to dodge — none is a regression of shipped behavior.

**In scope:** WR-02, WR-03, WR-04, WR-05 from `08-REVIEW.md`, and the transient-Dgraph-error retry (RESIL-01). Optionally the IN-01..04 nice-to-haves.

**Out of scope:** Any change to the verified Phase 8 behavior contracts (frontier ordering, 15s timeout, EOSE-quorum, honest staleRemaining). VALID-03 recover-or-purge *semantics* must be preserved verbatim — HARD-02 only adds transaction hygiene around them.
</domain>

<decisions>
## Implementation Decisions

### HARD-01 — Paginate BackfillNextAttempt (WR-03, highest value)
- `BackfillNextAttempt` (`pkg/dgraph/dgraph.go`) currently loads every `has(last_attempt) ∧ ¬has(next_attempt)` node in one query and writes one mutation. On a large legacy set this can exceed the gRPC message cap (same hazard `AddFollowers` already chunks at `batchSize=200`), fail non-fatally, and silently never backfill — defeating D-06.
- LOCKED: paginate the query with `first:`/`offset:` and commit stamps in `batchSize` windows, mirroring the existing `AddFollowers` chunking pattern.
- Live run dodged this (`seeded 0`, already-migrated), so it is latent not active.

### HARD-02 — MarkAttempted transaction safety (WR-02)
- `MarkAttempted` (`pkg/dgraph/dgraph.go`) runs recovery/purge/stamp as independent `CommitNow` transactions with no rollback; the in-place recovery txn lacks `defer txn.Discard(ctx)`.
- LOCKED: ensure each recovery txn is discarded promptly. CAUTION: this is inside the VALID-03 recover-or-purge block that Phase 5 Plan 01 preserved verbatim — a naive `defer` inside a loop would accumulate undiscarded txns until function return. Discard per-iteration, not deferred-to-return.
- LOCKED: document stamp-vs-recovery independence and retry-safety in code comments.

### HARD-03 — Bounded forward-publish (WR-04)
- `forwardEvent` (`pkg/crawler/crawler.go`) publishes on the long-lived `relayContext` with no per-publish timeout, so a hung forward relay can stall the single-threaded drain loop and delay `MarkAttempted` / the next batch.
- LOCKED: wrap the forward publish in a short bounded context (e.g. `c.timeout`).

### HARD-04 — Large-frontier sort-cap coverage (WR-05)
- The `orderdesc: val(fc)` frontier query was verified LIVE on the production graph via the D-09 human checkpoint (top-N honored, not pre-truncated at 1000), so this is mitigated in practice. Gap: no integration test exercises the >1000-row frontier sort-cap regime (`TestGetStalePubkeysOrder` sizes limit to `countFrontier()+100`).
- LOCKED: prefer an integration test that exercises a frontier larger than the sort cap; if infeasible against the test Dgraph, document the live-verified guarantee as standing evidence. Either path satisfies the requirement.

### RESIL-01 — Retry transient Dgraph errors in main loop
- The main loop in `cmd/crawler/main.go` breaks on a `CountStalePubkeys` error (mirroring the pre-existing break-on-error for `CountPubkeys`), so the crawler exits on a transient gRPC blip (`Unavailable` / `EOF`) and relies on its supervisor to restart. Observed live: `Error counting stale pubkeys: ... code = Unavailable desc = error reading from server: EOF`.
- LOCKED: distinguish transient (retry with backoff) from fatal (exit) via `google.golang.org/grpc/status` code inspection. Wrap the per-batch `CountStalePubkeys` / `CountPubkeys` calls (and consider `GetStalePubkeys` / `MarkAttempted`). Keep exit behavior for genuinely fatal/unrecoverable errors so a misconfigured endpoint still surfaces loudly.

### Verification posture
- Phase 8 required live-host verification. HARD-02/03 and RESIL-01 touch the live drain/main loop; HARD-01 touches Dgraph mutations. Plan for unit/integration coverage where it can run under the existing `//go:build integration` + temp-`HOME` harness, and flag any item that still needs a live-host re-verification checkpoint (per CLAUDE.md spec §6).

### Claude's Discretion
- Exact retry/backoff parameters for RESIL-01 (initial interval, max attempts, cap) — pick sensible defaults consistent with existing relay backoff constants; make config-driven only if it falls out naturally.
- Whether to fold IN-01..04 (TouchLastDBUpdate debug log, NewReadOnlyTxn for count queries, BackfillNextAttempt `86400`→`params.HitRefreshCadence`, integer-space quorum threshold) into this phase or leave as future nice-to-haves. Recommend including the cheap, low-risk ones (IN-03 especially, since it touches the same `BackfillNextAttempt` function as HARD-01).
- Plan/wave structure.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Source of scope
- `.planning/phases/08-frontier-prioritization-timeout-observability/08-REVIEW.md` — original code-review findings (CR-01, WR-01..06, IN-01..04); WR-02/03/04/05 are this phase's scope
- `.planning/todos/pending/phase08-code-review-hardening-followups.md` — deferred warnings with fix guidance
- `.planning/todos/pending/crawler-retry-transient-stale-count-errors.md` — RESIL-01 context and acceptance

### Code to modify
- `pkg/dgraph/dgraph.go` — `BackfillNextAttempt` (HARD-01), `MarkAttempted` recover/purge/stamp block (HARD-02), frontier sort-cap query + `TestGetStalePubkeysOrder` (HARD-04)
- `pkg/crawler/crawler.go` — `forwardEvent` (HARD-03)
- `cmd/crawler/main.go` — main crawl loop break-on-error sites (RESIL-01)

### Patterns to follow
- `AddFollowers` chunking at `batchSize=200` in `pkg/dgraph/dgraph.go` — the existing gRPC-cap-safe pagination pattern HARD-01 should mirror
- Phase 5 VALID-03 recover-or-purge block — HARD-02 must preserve its semantics verbatim
</canonical_refs>

<specifics>
## Specific Ideas

- HARD-01 is flagged "highest value" in the deferred-warnings todo — prioritize it.
- HARD-04 has a documentation escape hatch (live-verified D-09 evidence) if a >1000-row integration test is impractical against the test Dgraph.
</specifics>

<deferred>
## Deferred Ideas

- IN-01, IN-02, IN-04 nice-to-haves may be left for a future cycle at the planner's discretion (IN-03 recommended for inclusion since it co-locates with HARD-01).
- DISC-01/02, SEC-01/02, OBS-01, TUNE-01, TEST-05 from REQUIREMENTS.md "Future Requirements" remain out of scope.
</deferred>

---

*Phase: 09-phase-8-hardening-resilience-follow-ups*
*Context gathered: 2026-06-13 from deferred Phase 8 code-review warnings + live-host finding*
