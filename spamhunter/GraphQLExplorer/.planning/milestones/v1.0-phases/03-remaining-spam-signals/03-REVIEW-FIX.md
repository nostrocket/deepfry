---
phase: 03-remaining-spam-signals
fixed_at: 2026-06-25T00:00:00Z
review_path: .planning/phases/03-remaining-spam-signals/03-REVIEW.md
iteration: 1
findings_in_scope: 4
fixed: 4
skipped: 0
status: all_fixed
---

# Phase 3: Code Review Fix Report

**Fixed at:** 2026-06-25
**Source review:** 03-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 4 (all WARNING)
- Fixed: 4
- Skipped: 0
- INFO findings (IN-01..IN-05): intentionally NOT addressed (out of scope per task).

## Fixed Issues

### WR-04: defensive asymmetry — malformed window rows reach nearDup/analyzeKinds untyped

**Files modified:** `src/analysis/nearDup.ts`, `src/analysis/kinds.ts`, `src/analysis/nearDup.test.ts`, `src/analysis/kinds.test.ts`
**Commit:** 1a428ce
**Applied fix:** `normalizeContent` now coerces a non-string `content` to `''` (guard
`if (typeof s !== 'string') return ''`) instead of calling `.normalize` on `null`/`undefined`
— a coerced-empty row keys on `''` (empty shingle Set), so it only exact-buckets with other
empties and never near-matches a substantive post. `analyzeKinds` was already runtime-robust
(`isSaneKind`/`isSaneTs` use `Number.isSafeInteger`, which is `false` for null/undefined/NaN/
non-numbers → flagged into `outOfRangeCount`); this is now documented as a deliberate parity
with `tags.ts` rather than incidental. Added regression cases for null/undefined/non-string
`content` and null/undefined/non-number `kind`/`createdAt` to both suites (no-throw +
correct out-of-range accounting). This was the most important finding for robustness — it
closes the one place the cast (`page.events as WindowEvent[]`) bypassed the otherwise-thorough
defensive posture.

### WR-01: RawInspector error state is a permanent dead end

**Files modified:** `src/views/RawInspector.tsx`
**Commit:** fcc191e
**Applied fix:** Added a Retry button (re-invokes the lazy `fetchRaw()`) and a Close button
(returns to `idle`) to the `error` phase, matching the affordances every other terminal phase
(`zeroMatch`, `loaded`) already provides. The verbatim UI-SPEC Copywriting-Contract strings
were KEPT ("Couldn't load raw bytes — retrying." / "Couldn't load the raw bytes for this
event.") — the retryable copy is now honest because Retry is the real mechanism behind it,
resolving the "promises behavior the component does not perform" defect without breaking the
mandated verbatim copy.
**Note:** UI-only change; no component test harness exists in this project (tests cover
analysis/transport/hooks). Verified via `tsc -b` + `vite build`. Recommend a manual click-
through of the error → Retry / Close paths during UAT.

### WR-03: unhandled promise rejection on clipboard write

**Files modified:** `src/views/AuthorDrillDown.tsx`
**Commit:** bc0d6ac
**Applied fix:** Added a silent `.catch(() => {})` to both copy buttons
(`navigator.clipboard?.writeText(npub)` line 65 and `...writeText(hex)` line 77) so a rejected
write (denied permission, non-secure context, unfocused document) no longer surfaces as an
Unhandled Promise Rejection. Copy is best-effort convenience, so a silent catch is appropriate.

### WR-02: DuplicatePanel useMemo key does not bound the O(n²) recompute

**Files modified:** `src/views/DuplicatePanel.tsx`
**Commit:** ac57ce3
**Applied fix:** Replaced the `[events]` (array reference) memo key with a cheap O(n) content
signature (`id:contentLength` joined per row, itself memoized on `[events]`). The expensive
`nearDup` memo now keys on `[sig]` — a primitive string — so when a future parent hands a
fresh `events` array whose content is unchanged, `sig` is the same string value and the O(n²)
pass is skipped. The bound now holds by construction rather than by luck of current parent
behavior. Comments updated to reflect the real guarantee. (No ESLint in this project, so no
exhaustive-deps directive was added.)

## Verification

- `npx tsc -b`: clean (exit 0) after every fix.
- `npx vitest run`: 83 passed (was 75; +8 new WR-04 regression tests).
- `npm run build` (`tsc -b && vite build`): clean, 95 modules transformed, built in ~1.9s.

---

_Fixed: 2026-06-25_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
