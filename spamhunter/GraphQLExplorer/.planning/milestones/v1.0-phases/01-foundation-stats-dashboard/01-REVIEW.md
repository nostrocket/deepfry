---
phase: 01-foundation-stats-dashboard
reviewed: 2026-06-24T00:00:00Z
depth: standard
files_reviewed: 16
files_reviewed_list:
  - src/App.tsx
  - src/main.tsx
  - src/queries/stats.graphql.ts
  - src/transport/config.ts
  - src/transport/client.ts
  - src/transport/errors.ts
  - src/transport/readiness.ts
  - src/transport/paginate.ts
  - src/hooks/useStatsPoll.ts
  - src/views/StatsDashboard.tsx
  - src/views/StatsDashboard.module.css
  - src/styles/tokens.css
  - src/vite-env.d.ts
  - codegen.ts
  - vite.config.ts
  - scripts/check-graphql-pin.cjs
findings:
  critical: 0
  warning: 4
  info: 5
  total: 9
status: issues_found
---

# Phase 1: Code Review Report

**Reviewed:** 2026-06-24
**Depth:** standard
**Files Reviewed:** 16
**Status:** issues_found

## Summary

Reviewed the Phase 1 foundation/walking-skeleton of the read-only GraphQL spam-investigation frontend (React 19 + Vite 7 + urql + GraphQL Codegen). The code is well-structured and the phase invariants are largely respected: the base URL is centralized in `transport/config.ts` with the correct loopback default, there is no `server.proxy` and no inline/relative `/graphql` literal, `classify()` is a single error boundary, `useStatsPoll` uses `setTimeout`-reschedule with hidden-tab pause and a nudge-only `maxLevId` diff, `pinnedStrfryVersion` is rendered via plain JSX interpolation (escaped, no `dangerouslySetInnerHTML`), and `graphql` is exact-pinned to `16.14.2` with a guard. `npx tsc -b` passes clean.

No Critical/Blocker issues found. The findings below are correctness/robustness gaps (Warnings) and quality items (Info). The most notable is an empty-string `VITE_GRAPHQL_URL` falling through `??` to itself (not the default), and the dual-gate where both `App` and `StatsDashboard` independently render a "Connecting" state and issue probes/queries.

## Warnings

### WR-01: Empty-string `VITE_GRAPHQL_URL` bypasses the default and crashes `new URL()`

**File:** `src/transport/config.ts:14-20`
**Issue:** `import.meta.env.VITE_GRAPHQL_URL ?? DEFAULT_GRAPHQL_URL` uses `??`, which only falls back on `null`/`undefined`. Vite injects an explicitly-set-but-empty env var (`VITE_GRAPHQL_URL=` in a `.env` file) as the empty string `''`, not `undefined`. An empty string is not nullish, so `GRAPHQL_URL` becomes `''`. Then `new URL(GRAPHQL_URL)` at line 18 throws `TypeError: Invalid URL` at module-load time, which crashes the entire app before any error boundary or readiness state can render — a blank white screen, not the intended "connecting"/"network" state. The config module's whole purpose is to be the safe single source of the base URL; a trivially-misconfigured `.env` defeats it loudly.
**Fix:** Treat blank/whitespace-only as unset, and fail with a clear message on a genuinely malformed URL:
```ts
const raw = import.meta.env.VITE_GRAPHQL_URL?.trim()
export const GRAPHQL_URL: string = raw && raw.length > 0 ? raw : DEFAULT_GRAPHQL_URL

let base: URL
try {
  base = new URL(GRAPHQL_URL)
} catch {
  throw new Error(
    `VITE_GRAPHQL_URL is not a valid absolute URL: "${GRAPHQL_URL}". ` +
      `Expected e.g. http://127.0.0.1:8080/graphql`,
  )
}
```

### WR-02: `VITE_GRAPHQL_URL` is untyped — `import.meta.env` access is implicitly loose

**File:** `src/vite-env.d.ts:1`, `src/transport/config.ts:15`
**Issue:** The project enables `strict` but `vite-env.d.ts` only references `vite/client`, which types `import.meta.env` keys generically. There is no `ImportMetaEnv` augmentation declaring `VITE_GRAPHQL_URL: string | undefined`, so the contract between the env and the single config module is unenforced. A typo (`VITE_GRAPHQL_UR`) at a future call site would not be caught, and the type of the value (`string | undefined` vs `string`) is not pinned, which is exactly what makes WR-01 silent. For a foundation module whose stated job is to be the only place a URL literal lives, the env contract should be typed.
**Fix:** Augment the env interface so the key is declared once and checked everywhere:
```ts
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_GRAPHQL_URL?: string
}
interface ImportMeta {
  readonly env: ImportMetaEnv
}
```

### WR-03: Dual readiness/connecting gate — `App` and `StatsDashboard` both probe and both render a "Connecting" state

**File:** `src/App.tsx:61-76`, `src/views/StatsDashboard.tsx:177-193`, `src/hooks/useStatsPoll.ts:139`
**Issue:** `App` blocks on `waitForReady()` (GET `/ready`) and only mounts `StatsDashboard` once ready returns 200. But `StatsDashboard` → `useStatsPoll` immediately fires its own `tick()` (`POST /graphql`) on mount and `StatsDashboard` *also* renders an identical "Connecting to relay…" shell (lines 181-193) for its own `loading && !stats && !error` state. The consequences:
- The `StatsDashboard` connecting-shell is effectively dead in the normal flow (we only mount it *after* `/ready` is 200), yet it duplicates the copy/markup of `ConnectingState` in `App.tsx` — two sources of truth for the same state that can drift.
- The readiness contract is "poll `/ready` until 200, then it is safe to `POST /graphql`." `/ready` returning 200 does not strictly guarantee the very next `POST /graphql` won't transiently 503; the second connecting-shell is the only thing covering that gap, so it is not purely redundant — but its existence alongside `App`'s gate means the two states are uncoordinated and the user can briefly see one "Connecting" screen replaced by a second identical one.
**Fix:** Pick one owner of the "connecting" state. Either (a) drop `App`'s `waitForReady` gate and let `useStatsPoll`/`classify` handle `NOT_READY` (503) with backoff inside the dashboard, or (b) keep `App`'s gate and remove the now-unreachable connecting branch from `StatsDashboard`, relying on `NOT_READY` inline/shell treatment for any post-ready transient 503. Centralize the connecting copy in one component so it cannot drift.

### WR-04: `useStatsPoll` does not handle `client.query(...).toPromise()` rejection

**File:** `src/hooks/useStatsPoll.ts:97-118`
**Issue:** `const result = await client.query(...).toPromise()` is not wrapped in try/catch. urql's `toPromise()` normally resolves with a `CombinedError` on the result (which `classify()` handles), so this is usually fine. But a defect or an unexpected throw in the exchange chain (or a future custom exchange added in Phases 2-4) would reject the promise. Because there is no `.catch`/`try`, the rejection becomes an unhandled promise rejection: the `schedule()` call at the end of `tick` never runs, so **polling silently stops permanently** and `loading` may stick. The function is the structural template Phases 2-4 inherit, so the missing safety net propagates.
**Fix:** Wrap the query and reschedule in `finally` so a throw can never kill the poll loop:
```ts
let result
try {
  result = await client.query(StatsDocument, {}).toPromise()
} catch {
  if (!cancelled) { setError({ kind: 'NETWORK' }); setLoading(false); schedule() }
  return
}
if (cancelled) return
// ...existing classify/data handling, with schedule() reachable on every path
```

## Info

### IN-01: `tickRef`/`tick` overlap is possible via `refresh()` and visibility resume

**File:** `src/hooks/useStatsPoll.ts:120-139`
**Issue:** `tickRef.current` and `onVisibility` both `clearTimeout(timer)` then `void tick()`. `clearTimeout` cancels the *scheduled* tick, but if a `tick()` is already mid-flight (awaiting the network), calling `refresh()` (or rapidly toggling tab visibility) starts a *second* concurrent `tick()`. Two in-flight `client.query` calls can resolve out of order; the older response can land last and overwrite `stats`/`lastLevId` with stale data, briefly mis-firing or mis-suppressing the nudge. Low impact at seconds-scale with idempotent stats, but worth an inflight guard before Phase 2 wires real pagination.
**Fix:** Track an `inFlight` ref and early-return from `tick()` if one is already running (or use a monotonic request id and ignore stale resolutions).

### IN-02: Duplicated connecting-state copy/markup across two files

**File:** `src/App.tsx:50-56`, `src/views/StatsDashboard.tsx:186-190`
**Issue:** The strings "Connecting to relay…" and "Waiting for the relay to report ready. This can take a moment on cold start." plus their shell markup are copy-pasted in `ConnectingState` (App) and `StatsDashboard`. The comment claims "VERBATIM UI-SPEC copy"; duplicating it in two components invites drift when the spec changes. (Related to WR-03.)
**Fix:** Extract a single `ConnectingShell` component (or a copy constant) imported by both call sites.

### IN-03: `READY_URL`/`HEALTH_URL` discard any base path in `VITE_GRAPHQL_URL`

**File:** `src/transport/config.ts:18-20`
**Issue:** `new URL('/ready', base)` uses an absolute path, so it resolves against the *origin only* and drops any path prefix on the configured URL. If someone sets `VITE_GRAPHQL_URL=http://host:8080/api/graphql`, the GraphQL client hits `/api/graphql` but readiness hits `http://host:8080/ready` (not `/api/ready`). The comment "same origin/base as /graphql" implies base-path awareness, but only the origin is preserved. For the v1 loopback default this is harmless; flag it so the assumption is explicit before any non-root deployment.
**Fix:** Either document that `VITE_GRAPHQL_URL` must be origin-root, or derive the sibling endpoints relative to the GraphQL path (e.g. `new URL('./ready', base)` / strip the trailing `graphql` segment) so a path prefix is honored consistently.

### IN-04: `HEALTH_URL` is exported but unused in this phase

**File:** `src/transport/config.ts:20`
**Issue:** `HEALTH_URL` is declared/exported but nothing in the reviewed Phase 1 surface imports it (`READY_URL` is used by `readiness.ts`; `GRAPHQL_URL` by `client.ts`). `noUnusedLocals` does not catch exported members, so it slips through. It is presumably scaffold for a later phase (like `paginate.ts`), but unlike `paginate.ts` it carries no "scaffold-only / first used in Phase N" note.
**Fix:** Add a one-line "scaffold — first consumed in Phase N (health indicator)" comment mirroring the `paginate.ts` convention, or remove until needed.

### IN-05: `httpStatus` casts through an `any`-shaped inline type to read `error.response`

**File:** `src/transport/errors.ts:36-41`
**Issue:** `(error as { response?: { status?: unknown } }).response` reaches into urql's `CombinedError.response` (typed `any`) to find the HTTP status, because the status is on the sibling `response` property, not `networkError`. The code is defensive (checks `typeof status === 'number'`) and well-commented, but it depends on `@urql/core@6.0.3`'s internal `makeErrorResult` placement of the raw `Response`. A urql minor upgrade could move it, and nothing here would fail loudly — `NOT_READY`/`PAYLOAD_TOO_LARGE` would silently misclassify as `NETWORK`. This is the documented A2 resolution, so it is accepted risk, but worth a test asserting the `503`/`413` paths against the installed urql to catch drift.
**Fix:** Add a unit test that constructs the actual urql `CombinedError` shape for a 503 and 413 and asserts `classify()` returns `NOT_READY`/`PAYLOAD_TOO_LARGE`, so an upgrade that relocates `response.status` trips a red test instead of silently degrading to `NETWORK`.

---

_Reviewed: 2026-06-24_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
