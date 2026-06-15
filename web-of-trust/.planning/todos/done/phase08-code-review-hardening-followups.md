---
title: Phase 8 code-review hardening follow-ups (WR-02/03/04/05 + info)
area: crawler
status: resolved
created: 2026-06-13T12:44:51.805Z
resolved: 2026-06-15
resolution: Closed in Phase 9 — WR-03→HARD-01 (paginated BackfillNextAttempt), WR-02→HARD-02 (MarkAttempted recovery-txn hygiene), WR-04→HARD-03 (bounded forwardEvent publish), WR-05→HARD-04 (large-frontier sort-cap doc), plus IN-03 (HitRefreshCadence param). IN-01/02/04 left as future nice-to-haves. Live-approved 2026-06-15.
source: 08-REVIEW.md (Phase 8 code review)
---

# Phase 8 code-review hardening follow-ups

The Phase 8 code review (`08-REVIEW.md`) found 1 blocker and 6 warnings. Resolved
immediately during execution: **CR-01** (BackoffInterval premature cap),
**WR-01** (staleRemaining negative clamp), **WR-06** (lossy-drain comment).

The remaining warnings are real but touch live-verified runtime code (and the
sensitive VALID-03 block), so they were deferred to a dedicated gap-closure pass
with live re-verification rather than unsupervised inline edits. Suggested route:
`/gsd-plan-phase 08 --gaps` → `/gsd-execute-phase 08 --gaps-only`.

## Deferred warnings

- **WR-03 (highest value) — `BackfillNextAttempt` unbounded query + mutation**
  (`pkg/dgraph/dgraph.go`). Loads every `has(last_attempt) ∧ ¬has(next_attempt)`
  node and writes one mutation. On a large legacy set this can exceed the gRPC
  message cap (the same hazard `AddFollowers` chunks at batchSize=200), fail as
  non-fatal, and silently never backfill — defeating D-06. Live run dodged it
  (`seeded 0`, already-migrated), so it is latent, not active. Fix: paginate the
  query (`first:`/`offset:`) and commit stamps in batchSize windows.

- **WR-02 — `MarkAttempted` non-atomic multi-`CommitNow` + missing `defer Discard`**
  (`pkg/dgraph/dgraph.go`). Recovery/purge/stamp run as independent `CommitNow`
  transactions with no rollback; the in-place recovery txn lacks `defer
  txn.Discard(ctx)`. Note: this is inside the VALID-03 recover-or-purge block
  that Plan 01 preserved verbatim — change carefully (a naive `defer` inside the
  loop would accumulate undiscarded txns until function return). Fix: ensure each
  recovery txn is discarded promptly; document stamp-vs-recovery independence and
  retry-safety.

- **WR-04 — unbounded forward publish can stall the drain loop**
  (`pkg/crawler/crawler.go`). `forwardEvent` publishes on the long-lived
  `relayContext` with no per-publish timeout, so a hung forward relay can stall
  the single-threaded drain and delay `MarkAttempted`/next batch. Fix: wrap the
  forward publish in a short bounded context (e.g. `c.timeout`).

- **WR-05 — large-frontier sort-cap not covered by tests**
  (`pkg/dgraph/dgraph.go`). The `orderdesc: val(fc)` frontier query was
  **verified live** on the production graph via the D-09 human checkpoint
  (top-N honored, not pre-truncated at 1000), so this is mitigated in practice.
  Gap: no integration test exercises the >1000-row frontier sort-cap regime
  (`TestGetStalePubkeysOrder` sizes limit to countFrontier()+100). Fix: add a
  large-frontier integration test, or document the live-verified guarantee.

## Info (nice-to-have)

- IN-01: `TouchLastDBUpdate` return value ignored (`crawler.go`) — log under debug.
- IN-02: use `NewReadOnlyTxn()` for `CountPubkeys` / `GetPubkeysWithMinFollowers`.
- IN-03: `BackfillNextAttempt` hardcodes `86400` instead of `params.HitRefreshCadence`.
- IN-04: `quorumReached` float comparison — fine for 0.70 default; integer-space
  threshold would be more robust for arbitrary fractions.
