---
phase: 02-backfill-live-verification
plan: 01
subsystem: database
tags: [dgraph, dql, migration, crawler, nostr, operational]

requires:
  - "last_attempt schema predicate + frontier-first GetStalePubkeys + MarkAttempted (Phase 1, 01-01)"
provides:
  - "MIG-01 backfill: last_attempt seeded from last_db_update on every existing crawled node in live Dgraph"
  - "Live confirmation that the frontier-first crawler converts stubs and expands the graph"
affects: [production-crawler, web-of-trust-growth]

tech-stack:
  added: []
  patterns:
    - "One-time DQL upsert migration (upsert { query { ... } mutation { set { uid(nodes) <pred> val(x) . } } }) to seed a new predicate from an existing one"
    - "Verify a predicate exists in the live schema before a value-copy upsert (the predicate was already present; no crawler-start-for-schema needed)"

key-files:
  created: []
  modified: []

key-decisions:
  - "SC2 re-adjudicated: the literal 'New pubkey added to graph (signer)' grep is 0 by design — that line only fires when a kind-3's SIGNER is a brand-new node, but frontier pubkeys already exist as stubs and take the existing-node branch. The real frontier-expansion signal is 'New pubkey added to graph (stub)' = 16,813 plus crawled climbing +2,992."
  - "SC3 re-adjudicated: stub count ROSE (+13,821), the opposite of the plan's prediction, because discovery (+16,813 new stubs from fetched contact lists) outpaces conversion (+2,992) in a 5-minute window. This is the crawler's core value (expand the web of trust) working as intended; a multi-hour soak converts the backlog faster than new discovery once the frontier saturates."
  - "Fix E inline HTTP upsert committed cleanly ('code':'Success'); no Ratel/startup fallback needed. The last_attempt predicate already existed in the live schema."

requirements-completed: [MIG-01]

duration: ~1h (incl. 5-min live crawl + ~4-min relay-connect)
completed: 2026-06-09
---

# Phase 2: Backfill + Live Verification Summary

**The Fix E backfill seeded `last_attempt` on all 15,229 already-crawled nodes, and a 5-minute live crawl on the strfry host proved the frontier-first crawler works: the graph grew +16,813 pubkeys and crawled +2,992 (both were frozen at ~15.2k before the fix) — confirming the 8% crawl fix unblocks web-of-trust expansion in production.**

## Performance

- **Duration:** ~1 h (dominated by the ~4-min sequential relay-connect + 5-min crawl window)
- **Tasks:** 2 automated + 1 blocking human-verify checkpoint
- **Repo files modified:** 0 (operational phase — live Dgraph state change only)

## Accomplishments

- **MIG-01 backfill committed (SC1, PASS literally):** Ran the Fix E DQL upsert (`8pc_crawled.md` §4) against live Dgraph; `"code":"Success"`. The verify query `{ q(func: has(last_db_update)) @filter(NOT has(last_attempt)) { count } }` returns `c:0` — every crawled node now carries `last_attempt` (was 3 before). The `last_attempt: int @index(int)` predicate already existed in the live schema, so no crawler-start-for-schema step was needed.
- **Live crawl executed safely:** Baseline snapshot recorded, debug enabled only via `/tmp/fakehome/deepfry/web-of-trust.yaml` (`HOME=/tmp/fakehome`), 5-minute run captured to `/tmp/crawler.log` (21 MB), crawler exited cleanly on SIGINT. The live `~/deepfry/web-of-trust.yaml` was never touched (`debug: false`, mtime unchanged) — threat mitigation T-02-02 held.
- **Fix proven to take effect (SC2/SC3, PASS in substance):** graph expansion is unambiguous over the 5-min window:
  - crawled (`kind3CreatedAt`): **15,229 → 18,221 (+2,992)** — was frozen before the fix
  - total pubkeys: **189,256 → 206,069 (+16,813)**
  - stubs: **174,027 → 187,848 (+13,821)**
  - `New pubkey added to graph (stub)` log lines: **16,813**
- **Diagnostic confirms frontier reach:** per-batch "had events" trended **up** (0 → 14 → 432 → 423 → 430 → 451 → 461) across 1,168 query batches (~430–461/500 hit rate) — the opposite of the pre-fix steady re-crawl of the same ~1,657 known accounts (§3.2).

## Task Commits

This phase produced no repository source changes (it is operational — a live Dgraph migration + verification runbook). No commits.

## Files Created/Modified

None in the repo. Live/host artifacts:
- **Live Dgraph:** `last_attempt` populated on every existing crawled node (permanent MIG-01 migration).
- `/tmp/crawler.log` (21 MB) — captured 5-min crawl stdout/stderr; source for the log-line asserts.
- `/tmp/wot-phase2-counts.txt` — baseline + post-run total/crawled/stub counts; source for the delta assert.
- `/tmp/fakehome/deepfry/web-of-trust.yaml` — throwaway debug config (`debug: true`); never the live config.

## Decisions Made

### SC2 re-adjudicated (approved by operator)
The literal SC2 metric — `grep -c 'New pubkey added to graph (signer)' /tmp/crawler.log` — is **0**, but this is **by design, not a failure**. Verified in source (`pkg/dgraph/dgraph.go:143-161`): the "(signer)" line only fires when a kind-3 event's *signer* is a brand-new node. Frontier pubkeys already exist as stub nodes, so their kind-3 takes the existing-node branch and never logs "(signer)". The correct frontier-expansion signal is `New pubkey added to graph (stub)` (`dgraph.go:260`) = **16,813**, plus the crawled count climbing. The fix unambiguously took effect; the literal SC2 metric simply cannot fire for already-present frontier nodes.

### SC3 re-adjudicated (approved by operator)
The plan predicted the stub count would *decrease*; it *increased* (+13,821). This is correct behaviour: each crawled contact list adds its followees as new stub nodes, so in a short window **discovery (+16,813) outpaces conversion (+2,992)** and net stubs rise. This is precisely the crawler's core value ("continuously expand the web of trust — discovering newly-seen pubkeys"). A multi-hour soak converts the backlog faster than new discovery once the frontier saturates (§6 step 6).

## Deviations from Plan

No execution deviations — both automated tasks ran as written and their verify gates passed. The only divergence was at the success-criteria adjudication: SC2's literal grep and SC3's stub-direction prediction did not match the observed (correct) behaviour, both reinterpreted and approved at the blocking checkpoint as above.

## Issues Encountered

- 5-minute window is a smoke signal, not full convergence (anticipated by the plan's timing note). Growth is clearly positive; full backlog convergence requires the multi-hour soak.

## User Setup Required

None. The backfill is committed and the fix is live. **Recommended follow-up (operator's discretion, not a phase gate):** let the production crawler soak for several hours and confirm `has(kind3CreatedAt)` climbs well past 18,221, per §6 step 6.

## Next Phase Readiness

This is the final phase of the milestone. All 9 v1 requirements are now delivered (SCHEMA-01, SEL-01/02/03, ATTEMPT-01/02, TEST-01/02 in Phase 1; MIG-01 here). The 8% crawl fix is complete and verified live.

---
*Phase: 02-backfill-live-verification*
*Completed: 2026-06-09*
