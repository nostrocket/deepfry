---
phase: 02-suspect-entry-drill-down-core
fixed_at: 2026-06-24T00:00:00Z
review_path: .planning/phases/02-suspect-entry-drill-down-core/02-REVIEW.md
iteration: 1
findings_in_scope: 4
fixed: 4
skipped: 0
status: all_fixed
---

# Phase 2: Code Review Fix Report

**Fixed at:** 2026-06-24
**Source review:** 02-REVIEW.md
**Iteration:** 1
**Scope:** WARNING findings only (WR-01..WR-04). INFO findings (IN-01..IN-05) deliberately NOT fixed.

**Summary:**
- Findings in scope: 4
- Fixed: 4
- Skipped: 0

**Final verification (run from `spamhunter/GraphQLExplorer`):**
- `npm test` → 38 passed (36 baseline + 2 new WR-04 regression tests), 0 failed
- `npx tsc -b` → exit 0 (clean)

## Fixed Issues

### WR-04: `binByInterval` can iterate ~1M times on a single sane-but-distant gap

**Files modified:** `src/analysis/rate.ts`, `src/analysis/rate.test.ts`
**Commit:** `ec80e11`
**Applied fix:** Replaced the one-bin-at-a-time advance loop with an integer-division
jump (`currentStart = origin + Math.floor((t - origin) / binSec) * binSec`). Cost is
now bounded by the number of events, not the timestamp span — two sane timestamps up
to ~4.1e9s apart no longer drive ~1.14M empty-bin iterations on every RatePanel render.
Output is identical for valid inputs: per-bin `start` stays `origin + k*binSec` and empty
spans still emit no bin. Added two regression tests (single events across the full sane
range; multi-event-per-bin grouping across a large gap). All 13 rate tests green.

### WR-03: Default urql `cacheExchange` can serve a stale window for a revisited author

**Files modified:** `src/hooks/useAuthorWindow.ts`
**Commit:** `6e90726`
**Applied fix:** Set `requestPolicy: 'network-only'` on the events window query (third
arg to `client.query`). The shared client uses the default document `cacheExchange`,
which could replay a cached first page when an analyst re-drills into a
previously-visited author, silently understating the DRILL-05 honest denominator
against an actively-ingesting corpus. `network-only` forces every fresh visit to
re-derive the window from current corpus state. `cache-and-network` was rejected
because it would still flash the stale window first — the honesty contract requires
the fetched set to reflect current state, not a stale-then-fresh sequence. Scoped to
this query only; the shared client default is unchanged (no impact on `useStatsPoll`).
tsc confirms `requestPolicy` is a valid OperationContext option in `@urql/core` v6.

### WR-01: INVALID_CURSOR recovery can recurse without bound

**Files modified:** `src/hooks/useAuthorWindow.ts`
**Commit:** `80b2ae7`
**Status:** fixed: requires human verification (state-handling change; the hook is
React/network-coupled and has no node-env unit test, so the recovery logic was
verified via tsc + full suite but not exercised by an automated test).
**Applied fix:** Added a `cursorRetry` ref. On INVALID_CURSOR the handler now surfaces
the error (`setError` + `setLoading(false)`) instead of recursing when the cursor is
already null (page 1 itself rejected) or after one retry has already happened.
Otherwise it performs exactly one cursor-drop restart of page 1. The budget resets to
0 on every successful page fetch and on author change. This eliminates the unbounded
`fetchPage(null)` self-recursion that left the user on a permanent spinner with no
error surfaced.

### WR-02: `loadMore` reads a stale `loading` value from its closure

**Files modified:** `src/hooks/useAuthorWindow.ts`
**Commit:** `80b2ae7` (committed together with WR-01 — interleaved, interdependent
edits in the same file; `hasMoreRef` introduced for WR-02 is also synced inside WR-01's
INVALID_CURSOR branch)
**Applied fix:** Promoted `hasMore` to a `hasMoreRef` mirror kept in sync at every
`setHasMore` call site. `loadMore` now gates solely on always-current refs
(`inFlight.current` as the single in-flight source of truth, `hasMoreRef.current` for
exhaustion), dropping both the stale `loading` closure capture and the deps that
churned `loadMore`'s identity on every load/idle transition. The `hasMore`/`loading`
state copies still drive rendering.

## Notes

- INFO findings (IN-01 `as WindowEvent[]`; IN-02 dead defensive `parseIdentifier`;
  IN-03 empty App readiness catch; IN-04 `meta.oldest as number`; IN-05 `key={bin.start}`)
  were intentionally left as-is per the fix scope (WARNING only).
- WR-01 + WR-02 share one commit because their edits to `useAuthorWindow.ts` are
  interleaved and interdependent; splitting them would have produced an inconsistent
  intermediate tree.

---

_Fixed: 2026-06-24_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
