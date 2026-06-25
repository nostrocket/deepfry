---
phase: 03-remaining-spam-signals
reviewed: 2026-06-25T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - src/analysis/nearDup.ts
  - src/analysis/tags.ts
  - src/analysis/kinds.ts
  - src/analysis/thresholds.ts
  - src/views/DuplicatePanel.tsx
  - src/views/TagsPanel.tsx
  - src/views/KindsPanel.tsx
  - src/views/RawInspector.tsx
  - src/queries/rawEvent.graphql.ts
  - src/queries/events.graphql.ts
  - src/hooks/useAuthorWindow.ts
  - src/views/AuthorDrillDown.tsx
findings:
  critical: 0
  warning: 4
  info: 5
  total: 9
status: issues_found
---

# Phase 3: Code Review Report

**Reviewed:** 2026-06-25T00:00:00Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Phase 3 adds three pure spam-signal analyzers (`nearDup`, `analyzeTags`, `analyzeKinds`), their three panels, and a lazy raw-bytes inspector. The phase-specific concerns called out in the brief all hold up under adversarial reading:

- **nearDup (O(n²) bound, determinism, transitivity):** Stage-2 is correctly bounded — shingle Sets are precomputed once (lines 104-109) and the size-disparity short-circuit (line 139) is *sound* (it only skips pairs whose `min/max` size ratio already caps Jaccard below the cutoff, since `jaccard ≤ min/max`). Clustering is deterministic and transitive-correct via union-find. No `clean` field. PASS on the stated concerns.
- **tags defensive parsing:** `Array.isArray` + `typeof tag[0]==='string'` + `typeof tag[1]==='string'` guards are present; malformed rows are counted, never thrown. PASS.
- **kinds reuse of isSaneTs:** Confirmed imported from `./rate` (single source); kind bounded by `Number.isSafeInteger && >= 0`. PASS.
- **RawInspector XSS / lazy fetch:** Imperative `client.query` (not `useQuery`), `JSON.parse` in try/catch, rendered as escaped `<pre>` text node, no `dangerouslySetInnerHTML`. Confirmed via grep: zero dangerous sinks in `src/`. `raw` is selected ONLY in `rawEvent.graphql.ts`, NOT in `events.graphql.ts`. PASS.
- **Panels:** `nearDup` is wrapped in `useMemo` keyed on `events` identity; amber-on-signal only, no teal/green/clean. PASS.

The implementation is solid on its load-bearing contracts. The findings below are secondary correctness/robustness issues, the most significant being a dead-end error state in RawInspector and a memo key that does not actually bound the O(n²) recompute.

## Warnings

### WR-01: RawInspector error state is a permanent dead end — no retry, no close

**File:** `src/views/RawInspector.tsx:95-107`
**Issue:** The `error` phase renders a status `<div>` with **no button**. Once a fetch fails, the inspector is stuck in `error` forever — the analyst cannot retry and cannot return to `idle`. Worse, the recoverable copy actively lies: it reads "Couldn't load raw bytes — retrying." but nothing retries (there is no timer, no re-invocation of `fetchRaw`, no recovery path). Every other terminal phase (`zeroMatch`, `loaded`) provides a Close button back to `idle`; the error phase omits it. Compare to `useAuthorWindow`, where INVALID_CURSOR genuinely re-drives `fetchPage` — here the "retrying" claim has no mechanism behind it.
**Fix:** Add a retry/close affordance and align the copy with reality:
```tsx
if (state.phase === 'error') {
  const toneClass = state.tone === 'recoverable' ? styles.recoverable : styles.hardFail
  return (
    <div className={`${styles.note} ${toneClass}`} role="status" aria-live="polite">
      <span aria-hidden="true" className={styles.stateDot} />
      <span>
        {state.tone === 'recoverable'
          ? 'Couldn’t load raw bytes.'
          : 'Couldn’t load the raw bytes for this event.'}
      </span>
      <button type="button" className={styles.trigger} onClick={() => void fetchRaw()}>
        Retry
      </button>
      <button type="button" className={styles.trigger} onClick={() => setState({ phase: 'idle' })}>
        Close
      </button>
    </div>
  )
}
```
(If a literal auto-retry is intended by the spec, implement it — but do not leave copy that promises behavior the component does not perform.)

### WR-02: DuplicatePanel `useMemo` key does not bound the O(n²) recompute as documented

**File:** `src/views/DuplicatePanel.tsx:89`
**Issue:** The memo is keyed on `[events]`, but the argument passed to `nearDup` is `events.map((e) => ({ id: e.id, content: e.content }))` — a *new* array created on every render. The memo only skips recompute when the `events` **reference** is stable, which is the real guard; however the comment at lines 17-19/87-88 claims the memo prevents O(n²) recompute "on unrelated parent re-renders." That is true ONLY if the parent never passes a fresh `events` array on unrelated re-renders. `AuthorDrillDown` calls `useAuthorWindow` and `setEvents((prev) => ...)` returns a new array only on fetch, so today the reference is stable across unrelated re-renders — the guard works *by luck of current parent behavior*, not by construction. The `.map(...)` inside the `useMemo` factory is fine (it runs only when the memo recomputes), so this is not a correctness bug today, but the safety margin is thinner than the comments assert: any future parent change that re-creates `events` per render (e.g. deriving/filtering inline) silently re-arms the O(n²) self-DoS the comment claims is bounded.
**Fix:** Either document that the bound depends on the parent keeping `events` referentially stable, or make the panel robust by memoizing on a cheap content signature:
```tsx
const dup = useMemo(
  () => nearDup(events.map((e) => ({ id: e.id, content: e.content }))),
  [events],
)
// Defensive alternative if events identity ever becomes unstable:
// key the memo on events.length + a hash, not the array reference.
```
At minimum, soften the "bounded" claim to "bounded *while the parent keeps `events` referentially stable across unrelated re-renders*."

### WR-03: Unhandled promise rejection on clipboard write

**File:** `src/views/AuthorDrillDown.tsx:65, 77`
**Issue:** `onClick={() => void navigator.clipboard?.writeText(npub)}`. `writeText` returns a Promise that rejects in real conditions (permissions denied, non-secure context, document not focused). The `void` discards the Promise but does NOT handle rejection — this surfaces as an `Unhandled Promise Rejection` in the console (and is reported to error-tracking in production builds). The optional-chain `?.` guards only the *absence* of `clipboard`, not a rejected write.
**Fix:**
```tsx
onClick={() => void navigator.clipboard?.writeText(npub).catch(() => {})}
```
Apply to both the npub (line 65) and hex (line 77) copy buttons. (Silent catch is acceptable here — copy is a best-effort convenience.)

### WR-04: `page.events as WindowEvent[]` trusts server shape; malformed `tags` can reach the analyzer untyped

**File:** `src/hooks/useAuthorWindow.ts:190`
**Issue:** `const rows = page.events as WindowEvent[]` is an unchecked type assertion. `WindowEvent.tags` is typed `string[][]`, but the cast asserts the server returned that shape without verifying it. The brief explicitly notes tags are author-supplied and hostile (schema `[[String!]!]` notwithstanding). `analyzeTags` is defensively coded to survive `null`/non-array rows, so this does not crash — but `nearDup` and `analyzeKinds` consume `content`/`kind`/`createdAt` via the same unchecked rows. If the server ever returns `content: null` (e.g. a partial-error payload), `nearDup`'s `normalizeContent(events[i].content)` calls `.normalize` on `null` and throws — a path the cast hides from the type checker. This is the one place the otherwise-thorough defensive posture is bypassed.
**Fix:** Either narrow at the boundary (coerce `content` to `String(content ?? '')` and `kind`/`createdAt` to numbers when building `WindowEvent`s), or assert non-null at the cast and rely on the analyzers' guards — but document that `nearDup`/`analyzeKinds` assume non-null `content`/`kind`/`createdAt` and add the matching guard the way `analyzeTags` already guards `tags`. The asymmetry (tags is hardened, content is not) is the defect.

## Info

### IN-01: `byId` Map rebuilt on every ClusterGroup render

**File:** `src/views/DuplicatePanel.tsx:47`
**Issue:** `const byId = new Map(events.map((e) => [e.id, e]))` runs inside `ClusterGroup` on every render (including every open/close toggle of *any* sibling cluster's `open` state is isolated, but each group still rebuilds the full window Map on its own re-render). For large windows this is wasted work per cluster. Not a correctness issue and performance is out of v1 scope, but it is avoidable duplication.
**Fix:** Build the `byId` Map once in `DuplicatePanel` (or memoize it) and pass it down, or look members up from the parent.

### IN-02: Summary copy labels exact duplicates as "near-duplicates"

**File:** `src/views/DuplicatePanel.tsx:108`
**Issue:** When every cluster is `kind: 'exact'`, the summary still reads "{X} of {N} fetched are **near-duplicates** across {k} cluster(s)". An exact duplicate is not a near-duplicate; the per-cluster badge correctly says "exact duplicate" but the rollup is imprecise. If this exact string is mandated verbatim by the Copywriting Contract, ignore; otherwise it slightly misrepresents the strongest signal (exact repost) as the weaker one.
**Fix:** Use "duplicate or near-duplicate" in the rollup, or split counts.

### IN-03: `duplicateCount` framing assumes the panel header always says "near-duplicates"

**File:** `src/analysis/nearDup.ts:86-87`
**Issue:** Same root as IN-02 — `duplicateCount` sums exact + near members, but the only consumer phrases it as "near-duplicates." Consider exposing `exactCount` / `nearCount` so the panel can frame each honestly. Doc-only; behavior is correct.
**Fix:** Optional: add per-kind counts to `NearDupResult` for accurate UI framing.

### IN-04: Cluster output order is unspecified / not sorted

**File:** `src/analysis/nearDup.ts:154-166`
**Issue:** `clusters` is emitted in `groups` Map iteration order, which follows DSU root-id order — deterministic for a fixed input, but not sorted by significance (e.g. largest cluster first). The panel keys on `${cluster.kind}-${i}`, so list reordering across window growth could remount rows. Deterministic enough to not be a bug, but a "biggest cluster first" sort would be more useful to an analyst and more stable as the window grows.
**Fix:** Optional: `clusters.sort((a, b) => b.count - a.count)` before returning, and key the list on a stable cluster signature (e.g. sorted first memberId) rather than array index.

### IN-05: `truncPubkey` reused to truncate event ids in TagsPanel

**File:** `src/views/TagsPanel.tsx:109`
**Issue:** `truncPubkey(o.id)` applies a function named for pubkeys to an event id. Both are 64-char hex so the output is correct, but the name misleads — a reader expects an id-specific helper. Pure naming clarity.
**Fix:** Rename to `truncHex` (it is hex-agnostic) or add an `truncId` alias.

---

_Reviewed: 2026-06-25T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
