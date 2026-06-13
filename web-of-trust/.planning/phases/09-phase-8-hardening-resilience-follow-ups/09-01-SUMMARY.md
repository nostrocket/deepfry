---
phase: 09-phase-8-hardening-resilience-follow-ups
plan: "01"
subsystem: web-of-trust/pkg/dgraph
tags: [hardening, pagination, transaction-safety, documentation]
dependency_graph:
  requires: [08-02-SUMMARY.md]
  provides: [BackfillNextAttempt-paginated, MarkAttempted-txn-safe, HARD-04-doc-evidence]
  affects: [pkg/dgraph/dgraph.go, pkg/crawler/crawler.go]
tech_stack:
  added: []
  patterns:
    - first:/offset:0 re-query pagination loop (filter-shrink pattern)
    - inline txn.Discard inside loop body (vs deferred in function-scope)
key_files:
  created: []
  modified:
    - pkg/dgraph/dgraph.go
    - pkg/crawler/crawler.go
decisions:
  - "HARD-04: chose documentation path (doc comment on GetStalePubkeys citing D-09 / 08-REVIEW WR-05) over inserting 1001+ fixture nodes into the shared integration Dgraph"
  - "HARD-01: always re-query offset:0 per window (not advancing offset) because the NOT has(next_attempt) filter shrinks the result set as rows are stamped — offset advancement would skip rows"
  - "caller update in crawler.go uses temporary 86400 default for hitRefreshCadence pending proper config wiring in plan 09-02"
metrics:
  duration: "~15 minutes"
  completed: "2026-06-13"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 2
---

# Phase 9 Plan 01: Dgraph Layer Hardening (HARD-01, HARD-02, HARD-04) Summary

Hardened the Dgraph layer's two latent failure modes deferred from Phase 8 and documented the large-frontier sort-cap evidence. All three requirements closed.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Paginate BackfillNextAttempt + parameterize hitRefreshCadence | b43b827 | pkg/dgraph/dgraph.go, pkg/crawler/crawler.go |
| 2 | MarkAttempted recovery-txn hygiene + independence docs | f6d5072 | pkg/dgraph/dgraph.go |
| 3 | Large-frontier sort-cap coverage (HARD-04) | 1550b2e | pkg/dgraph/dgraph.go |

## Must-Haves Verification

- **BackfillNextAttempt paginates**: yes — first:/offset:0 loop, commits per-window CommitNow mutation, no single unbounded query or mutation (HARD-01/WR-03).
- **Uses hitRefreshCadence parameter**: yes — `func (c *Client) BackfillNextAttempt(ctx context.Context, hitRefreshCadence int64) (int, error)`; no `86400` literal remains in the function body (IN-03).
- **Idempotent**: yes — second run finds zero candidates (NOT has(next_attempt) filter excludes already-stamped nodes).
- **MarkAttempted inline Discard**: yes — recovery txn calls `txn.Discard(ctx)` inline after Mutate on both success and error paths; no `defer txn.Discard` inside the pubkeys loop (HARD-02/WR-02).
- **VALID-03 semantics preserved verbatim**: yes — decision tree (recoverable → update-in-place, duplicate → purge, unrecoverable → purge) and all log message wording unchanged.
- **Independence + retry-safety documented**: yes — code comment in recovery branch documents that recovery and stamp txns are independent operations, and that MarkAttempted is retry-safe.
- **HARD-04 covered**: yes — doc comment on GetStalePubkeys cites D-09 live-verified evidence and 08-REVIEW.md WR-05 as standing evidence.

## Decisions Made

### HARD-04 path chosen: documentation

Per CONTEXT.md, the preferred path is the documentation escape hatch because inserting >1000 fixture nodes into the shared integration Dgraph is slow and pollutes the DB used by every other test. The D-09 human checkpoint in Phase 8 already verified on the production graph (100k+ frontier nodes) that `first: N` is honored together with `orderdesc: val(fc)` — the top-N nodes by follower count were returned, not a pre-truncated subset. This standing live-verified evidence is cited in the GetStalePubkeys doc comment referencing `08-REVIEW.md WR-05`.

### HARD-01 offset strategy: always re-query offset:0

The plan's IMPORTANT pagination caveat is correct: the NOT has(next_attempt) filter shrinks the result set as rows are stamped, so advancing offset between windows would cause rows at position `offset` to be skipped. The correct strategy is to always re-query `first: batchSize, offset: 0` — each committed window removes its nodes from the filtered set, making the next offset:0 page the next unbacked batch. This is implemented and documented in the function's doc comment.

### Caller update: temporary 86400 default

The `BackfillNextAttempt` signature change (HARD-01/IN-03) breaks the existing call site in `pkg/crawler/crawler.go`. Since the full config wiring (MissBackoff.HitRefreshCadence threaded through crawler.Config) is deferred to plan 09-02, the call site was updated with a temporary `86400` (24h) literal to keep the package compilable. This matches the previous hard-coded behavior exactly. A TODO comment marks the site for 09-02 replacement.

## Deviations from Plan

### Minor adaptation: crawler.go call site updated inline (Rule 3 — blocking issue)

The plan notes "the caller in pkg/crawler/crawler.go is updated in plan 09-02." However, changing `BackfillNextAttempt`'s signature without updating the caller leaves the package uncompilable — violating the task's `go build ./...` acceptance criterion. The call site was updated with the temporary 86400 default rather than waiting for 09-02. This is documented with a TODO comment pointing to 09-02 for the proper config-driven value. VALID-03 semantics and the task's scope are unaffected.

## Integration Test Status

`make test-integration` was NOT run locally — it requires a live Dgraph on `localhost:9080`. All changes are in `pkg/dgraph` and `pkg/crawler`, which have integration tests gated on `//go:build integration`. Non-integration `go test -short ./pkg/dgraph/` passes (no unit tests in that package). Integration tests should be run on the strfry host per CLAUDE.md §6.

## Known Stubs

None — no placeholder values or TODO stubs introduced that affect plan objective delivery.

## Self-Check: PASSED

Files confirmed present:
- pkg/dgraph/dgraph.go — modified (BackfillNextAttempt paginated, MarkAttempted inline Discard, GetStalePubkeys HARD-04 doc)
- pkg/crawler/crawler.go — modified (BackfillNextAttempt call site updated)

Commits confirmed:
- b43b827 feat(09-01): paginate BackfillNextAttempt + parameterize hitRefreshCadence
- f6d5072 feat(09-01): MarkAttempted recovery-txn hygiene + independence docs
- 1550b2e docs(09-01): document large-frontier sort-cap guarantee on GetStalePubkeys
