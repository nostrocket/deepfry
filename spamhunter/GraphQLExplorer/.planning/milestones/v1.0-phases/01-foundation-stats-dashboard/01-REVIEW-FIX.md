---
phase: 01-foundation-stats-dashboard
fixed_at: 2026-06-24T00:00:00Z
review_path: .planning/phases/01-foundation-stats-dashboard/01-REVIEW.md
iteration: 1
findings_in_scope: 4
fixed: 4
skipped: 0
status: all_fixed
---

# Phase 1: Code Review Fix Report

**Fixed at:** 2026-06-24
**Source review:** .planning/phases/01-foundation-stats-dashboard/01-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 4 (Critical + Warning; 0 critical, 4 warning)
- Fixed: 4
- Skipped: 0
- Info findings (IN-01..IN-05): out of scope, not fixed. (IN-02, duplicated
  connecting copy, was resolved incidentally by the WR-03 fix.)

## Fixed Issues

### WR-01: Empty-string VITE_GRAPHQL_URL bypasses the default and crashes new URL()

**Files modified:** `src/transport/config.ts`
**Commit:** c90e6da
**Applied fix:** Replaced `?? DEFAULT_GRAPHQL_URL` with an explicit blank check —
`const raw = import.meta.env.VITE_GRAPHQL_URL?.trim()` then
`raw && raw.length > 0 ? raw : DEFAULT_GRAPHQL_URL` — so an explicitly-empty or
whitespace-only env var falls back to the loopback default instead of producing
`''`. Wrapped `new URL(GRAPHQL_URL)` in try/catch to throw a clear, actionable
message on a genuinely malformed override rather than a bare `TypeError`. Default
remains `http://127.0.0.1:8080/graphql`; the single-source-of-truth invariant
(FND-02) is preserved (no inline literal introduced elsewhere).

### WR-02: VITE_GRAPHQL_URL is untyped — import.meta.env access is implicitly loose

**Files modified:** `src/vite-env.d.ts`
**Commit:** 6054bcf
**Applied fix:** Added an `ImportMetaEnv` interface declaring
`readonly VITE_GRAPHQL_URL?: string` plus an `ImportMeta { readonly env: ImportMetaEnv }`
augmentation, so the env key is typed once under `strict` and a typo at any call
site is caught at compile time.

### WR-03: Dual readiness/connecting gate — App and StatsDashboard both render "Connecting"

**Files modified:** `src/views/ConnectingShell.tsx` (new), `src/App.tsx`, `src/views/StatsDashboard.tsx`
**Commit:** 6a9e25d
**Applied fix:** Extracted the cold-start "Connecting to relay…" copy/markup into a
single shared `ConnectingShell` component. `App` (the readiness-gate owner) and the
`StatsDashboard` initial-load branch (`loading && !stats && !error`) both render it,
so the two can no longer drift. The state machine is unchanged: `App` still gates on
`/ready` and only mounts the dashboard once ready; the dashboard branch is retained
to cover the post-ready transient gap (a 200 on `/ready` does not strictly guarantee
the very next `POST /graphql` resolves before the first tick). All other UI-SPEC
states and verbatim copy are preserved. This also resolves IN-02 incidentally.

### WR-04: useStatsPoll does not handle client.query(...).toPromise() rejection

**Files modified:** `src/hooks/useStatsPoll.ts`
**Commit:** 37f0cd3
**Applied fix:** Attached `.catch(() => 'THREW' as const)` to the query promise
(preserving the typed result inference from `StatsDocument`) and branch on the
sentinel: on a throw, classify as `{ kind: 'NETWORK' }`, clear loading, and call
`schedule()` so the poll loop survives a rejection instead of dying permanently as
an unhandled rejection. The nudge-flag / no-auto-refetch / setTimeout-reschedule
semantics are unchanged. This is robustness-only behavior (an error-recovery path),
not a change to existing happy-path logic.

## Verification

- `npm run build` (tsc -b && vite build): PASS — clean, 47 modules, no type errors.
- `npm test` (vitest run): PASS — 16/16 tests across 2 files.

---

_Fixed: 2026-06-24_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
