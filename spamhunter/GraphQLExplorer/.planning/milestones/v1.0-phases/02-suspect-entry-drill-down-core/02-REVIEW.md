---
phase: 02-suspect-entry-drill-down-core
reviewed: 2026-06-24T00:00:00Z
depth: standard
files_reviewed: 13
files_reviewed_list:
  - src/identifier/identifier.ts
  - src/queries/events.graphql.ts
  - src/router/hashRouter.ts
  - src/hooks/useAuthorWindow.ts
  - src/views/WindowIndicator.tsx
  - src/views/SuspectEntryBar.tsx
  - src/views/AuthorDrillDown.tsx
  - src/App.tsx
  - src/analysis/thresholds.ts
  - src/analysis/rate.ts
  - src/views/RatePanel.tsx
  - src/identifier/identifier.test.ts
  - src/analysis/rate.test.ts
findings:
  critical: 0
  warning: 4
  info: 5
  total: 9
status: issues_found
---

# Phase 2: Code Review Report

**Reviewed:** 2026-06-24
**Depth:** standard
**Files Reviewed:** 13
**Status:** issues_found

## Summary

Phase 2 (suspect entry + author drill-down core) was reviewed at standard depth across 11 source files and 2 test files. The four phase-critical concerns called out in the brief hold up well under adversarial scrutiny:

1. **`identifier.ts` (nsec/note/secret handling)** — Correct. `nsec` is rejected via a distinct `REJECTED_NSEC` arm that carries no decoded material; `note` falls through `default` to `NOT_RECOGNIZED`; the `catch` arm is the genuine parse-failure branch, structurally separate from any valid-but-zero-match outcome. The secret is never normalized, echoed, navigated to, or stored. Verified against the test suite, which asserts the failure arm carries no `hex`/`npub`.
2. **`rate.ts` (forgeable timestamps + asymmetry)** — Correct. `isSaneTs` bounds-checks via `Number.isSafeInteger` + `[MIN_TS, MAX_TS]`; out-of-range values are counted in `rejectedCount` and excluded from math; values are sorted ascending before interval math so gaps are never negative; `RateResult` carries no clean/ok/safe field. The asymmetry is structural.
3. **View layer (XSS)** — Clean. Grep confirms zero `dangerouslySetInnerHTML` / `innerHTML` / `eval` sinks. All author-controlled `content`, `createdAt`, `hex`, `npub` render via JSX interpolation (React default escaping).
4. **`useAuthorWindow.ts` (cursor / loadMore / errors)** — Mostly correct. Opaque cursor is stored verbatim; `INVALID_CURSOR` drops the cursor and restarts page 1; the `.toPromise().catch(() => 'THREW')` guard prevents unhandled rejection; `inFlight` ref + `runId` token guard against double-append and stale writes. One unbounded-recovery edge case is flagged below (WR-01).

No BLOCKER-class defects (no injection, no secret leakage, no data-loss path) were found. Findings are concentrated in robustness/edge-case territory (WARNING) and minor quality (INFO).

## Warnings

### WR-01: INVALID_CURSOR recovery can recurse without bound

**File:** `src/hooks/useAuthorWindow.ts:131-139`
**Issue:** On an `INVALID_CURSOR` classification the handler clears the cursor and re-invokes `void fetchPage(null, myRun)` to restart page 1. If the page-1 fetch *also* returns `INVALID_CURSOR` (a misbehaving/looping lens, or a server that rejects even a null cursor), the function calls itself again with `cursor === null` indefinitely — a tight async self-recursion with no attempt counter and no terminal state. The in-flight guard does not stop it because the handler resets `inFlight.current = false` immediately before the recursive call. The user sees a spinning ConnectingShell (events were cleared, `loading` stays implicitly true) with no error surfaced.
**Fix:** Add a bounded retry guard so a second consecutive INVALID_CURSOR on a null cursor surfaces the error instead of recursing:
```ts
const cursorRetry = useRef(0)
// ...inside the INVALID_CURSOR branch:
if (apiError.kind === 'INVALID_CURSOR') {
  if (cursor === null || cursorRetry.current >= 1) {
    // page-1 itself was rejected, or we already retried once — surface it
    setError(apiError)
    setLoading(false)
    inFlight.current = false
    return
  }
  cursorRetry.current += 1
  after.current = null
  setEvents([])
  setHasMore(false)
  inFlight.current = false
  void fetchPage(null, myRun)
  return
}
// reset cursorRetry.current = 0 on a successful page fetch
```

### WR-02: `loadMore` reads a stale `loading` value from its closure

**File:** `src/hooks/useAuthorWindow.ts:181-184`
**Issue:** `loadMore` is memoized on `[loading, hasMore, fetchPage]` and guards on `loading`. Because `loading` is captured in the closure, a `loadMore` reference held by React between renders can read a stale `loading`. The genuine in-flight guard is `inFlight.current` (a ref, always current), so the duplicate-append risk is in practice covered — but the `loading` term in the guard is redundant-yet-stale, and `loading` in the dep array forces an unnecessary identity churn of `loadMore` on every load/idle transition. The mixed ref+state guard makes the gating harder to reason about and is the kind of inconsistency that breeds a real double-fetch when the code is later edited.
**Fix:** Make the guard rely solely on the refs (which are always current) and drop `loading` from the deps:
```ts
const loadMore = useCallback(() => {
  if (inFlight.current || !hasMoreRef.current) return
  void fetchPage(after.current, runId.current)
}, [fetchPage])
```
(promote `hasMore` to a ref mirror, or keep `hasMore` in deps but remove the stale `loading` term).

### WR-03: Default urql `cacheExchange` can serve a stale window for a revisited author

**File:** `src/transport/client.ts:14-16` (consumed by `src/hooks/useAuthorWindow.ts:108-110`)
**Issue:** The client uses the default document `cacheExchange`. `EventsDocument` is queried per author with cursor pagination. When an analyst drills into author A, paginates several pages, navigates home, then returns to author A, the document cache may replay a cached first-page result rather than re-fetching live corpus state. For a spam-investigation tool where the corpus is actively ingesting, a stale window silently understates the denominator the whole UI is built to keep honest (DRILL-05). This is a correctness concern, not a perf one: the window-honesty contract assumes the fetched set reflects current corpus state.
**Fix:** Set the events query to network-first / cache-and-network, or scope a `requestPolicy`:
```ts
await client.query(EventsDocument, vars, { requestPolicy: 'cache-and-network' }).toPromise()
```
or, if staleness must never appear, `'network-only'`. Confirm against the contract whether replayed windows are acceptable.

### WR-04: `binByInterval` can iterate ~1M times on a single sane-but-distant gap

**File:** `src/analysis/rate.ts:54-71`
**Issue:** The bin-advance loop steps `currentStart += binSec` one bin at a time until `t` falls inside the window. Two *sane* timestamps (both pass `isSaneTs`) can legitimately be ~4.1e9 seconds apart (MIN_TS=0 .. MAX_TS=4.1e9). With `binSec = 3600` that is ~1.14M empty-bin advances for one gap, and `analyzeRate` re-runs on every RatePanel render (`src/views/RatePanel.tsx:31`) as the window widens. This is primarily a performance smell (out of strict v1 scope), but it borders on a UI-freeze correctness issue because an attacker can craft two real, in-range, far-apart timestamps to make every drill-down render do a million-iteration loop. Empty bins are also discarded (`if (count > 0)`), so the work produces nothing.
**Fix:** Skip empty spans arithmetically instead of looping:
```ts
for (const t of saneAscending) {
  if (t >= currentStart + binSec) {
    if (count > 0) bins.push({ start: currentStart, count })
    // jump straight to the bin containing t
    currentStart = origin + Math.floor((t - origin) / binSec) * binSec
    count = 0
  }
  count++
}
```

## Info

### IN-01: `as WindowEvent[]` assertion trusts server shape unchecked

**File:** `src/hooks/useAuthorWindow.ts:148`
**Issue:** `const rows = page.events as WindowEvent[]` type-asserts the server payload into `WindowEvent[]` with no runtime validation. If the lens returns a row with a missing/null `createdAt` or `id`, `deriveWindowMeta` and `analyzeRate` consume `undefined` as a number (NaN math) and React `key={e.id}` collides. The generated GraphQL types reduce the risk, but the assertion bypasses them.
**Fix:** Rely on the generated typed result instead of `as`, or filter rows lacking the five required fields before appending.

### IN-02: Defensive `parseIdentifier(hex)` fallback in the header is effectively dead

**File:** `src/views/AuthorDrillDown.tsx:165-166`
**Issue:** `hex` always arrives from the router's `/^#\/a\/([0-9a-f]{64})$/` matcher, so `parseIdentifier(hex)` cannot fail and `parsed.ok ? parsed.npub : hex` never takes the fallback branch. The comment acknowledges this. It is harmless defensiveness but is dead code that re-runs `npubEncode` on every render.
**Fix:** Acceptable as-is for defensiveness; if trimming, memoize the npub derivation or accept npub as a prop from the route layer.

### IN-03: Empty `catch` in App readiness swallows all failures, not just aborts

**File:** `src/App.tsx:39-42`
**Issue:** `.catch(() => { /* aborted ... ignore */ })` silently discards every rejection, including a genuine readiness failure that is *not* an abort. The app then sits on `ConnectingShell` forever with no diagnostic. The comment claims it is only the abort path, but the handler does not check `controller.signal.aborted`.
**Fix:** Distinguish abort from real failure:
```ts
.catch((e) => {
  if (controller.signal.aborted) return
  // surface or log a non-abort readiness failure
})
```

### IN-04: `meta.oldest as number` / `meta.newest as number` assertions

**File:** `src/views/WindowIndicator.tsx:38`
**Issue:** The non-null assertions are guarded by the `meta.count === 0` early return, so they are sound today, but they couple correctness to the ordering of the guard rather than the type. A future edit that moves the guard would silently pass `null` into `utc()` → `new Date(null*1000)` → epoch 0 rendered as a real date.
**Fix:** Narrow on the values directly (`if (meta.oldest == null || meta.newest == null)`) rather than on `count`.

### IN-05: `key={bin.start}` can collide if two bins share a start

**File:** `src/views/RatePanel.tsx:66`
**Issue:** Bins are keyed by `bin.start`. With the current `binByInterval` (monotonic `currentStart`), starts are unique, so this is safe today. It is a latent React-key fragility tied to the binning implementation rather than an independent invariant.
**Fix:** Safe as-is; if binning logic changes, key by index or `start-count`.

---

_Reviewed: 2026-06-24_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
