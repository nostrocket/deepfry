---
phase: 02-suspect-entry-drill-down-core
plan: 02
subsystem: GraphQLExplorer (frontend)
status: awaiting-human-verify
tags: [routing, drill-down, pagination, window-honesty, identifier]
requires:
  - "02-01: parseIdentifier / isHexPubkey (src/identifier/identifier.ts)"
  - "Phase 1 transport: client, classify, paginate discipline, config (GRAPHQL_URL), ConnectingShell"
provides:
  - "EventsDocument (codegen-typed author-window query)"
  - "useHashRoute / Route (lowercase-64hex-only #/a/<hex> router)"
  - "useAuthorWindow / deriveWindowMeta / WindowEvent / WindowMeta (cursor-paginated author window)"
  - "WindowIndicator (non-removable DRILL-05 denominator)"
  - "SuspectEntryBar (shell paste bar — accent submit, ID-03 parse-failure note)"
  - "AuthorDrillDown (identity header + timeline + indicator + zero-match + Load more)"
  - "App shell + entry bar + hash router (first routing)"
affects:
  - "src/App.tsx (now a shell + router, was a single-view gate)"
tech-stack:
  added: []
  patterns:
    - "Discriminated Route union + hashchange listener (mirrors ApiError / useStatsPoll)"
    - "classify-before-data + THREW guard + opaque-cursor INVALID_CURSOR restart on a real query"
    - "Window denominator framing (count/range/hasMore), never a verdict"
key-files:
  created:
    - src/queries/events.graphql.ts
    - src/hooks/useAuthorWindow.ts
    - src/router/hashRouter.ts
    - src/views/WindowIndicator.tsx
    - src/views/WindowIndicator.module.css
    - src/views/SuspectEntryBar.tsx
    - src/views/SuspectEntryBar.module.css
    - src/views/AuthorDrillDown.tsx
    - src/views/AuthorDrillDown.module.css
    - src/App.module.css
  modified:
    - src/App.tsx
    - src/gql/gql.ts (codegen)
    - src/gql/graphql.ts (codegen)
decisions:
  - "loadMore = single page per click (UI-SPEC), gated on loading + an in-flight ref; NOT accumulatePages load-all"
  - "Display npub derived via parseIdentifier(hex).npub (single identifier module), not a second nip19 call site"
  - "Author run-token (useRef counter) invalidates a previous author's in-flight fetch on hex change/unmount — no stale writes"
  - "Shell entry bar mounted on EVERY route so a new suspect can be entered from the drill-down too"
metrics:
  tasks_completed: 3
  tasks_total: 4
  files_created: 10
  files_modified: 3
  duration: ~22m
  completed_date: 2026-06-24
---

# Phase 2 Plan 02: Suspect Entry + Drill-Down Core Summary

The thin end-to-end vertical slice: paste a suspect in the shell entry bar → route to a per-author drill-down → read a newest-first event timeline against a non-removable window-size denominator → widen the window one page per click. This is the app's first routing (a second view) and the first exercise of Phase 1's opaque-cursor discipline against a real `events` query, with the DRILL-05 window indicator baked in from the first signal.

## What Was Built

**Task 1 — EventsDocument + useAuthorWindow + hash router (`ecb7a2c`)**
- `src/queries/events.graphql.ts`: codegen-typed `Events($filter,$after,$limit)` selecting only `id pubkey kind createdAt content` (+ `endCursor hasMore`). The large canonical payload, signature, and tag fields are omitted (Phase 3 / payload size). `npm run codegen` makes `createdAt`/`kind` typed `number`.
- `src/hooks/useAuthorWindow.ts`: cursor-paginated author window reusing the Phase-1 transport boundary verbatim — `client.query` (POST-only), the mandatory `.toPromise().catch(() => 'THREW')` guard → NETWORK, `classify()` before reading `result.data`, constant filter across pages, explicit `PAGE_LIMIT = 100`. `INVALID_CURSOR` resets `after.current = null`, clears events, and restarts page 1 (never hand-builds a cursor). `loadMore()` fetches exactly one next page, gated on `loading` + an in-flight ref (no double-append). A `runId` ref invalidates a prior author's in-flight fetch on `hex` change/unmount. `deriveWindowMeta` returns `{count, hasMore, oldest, newest}` (oldest/newest null when empty).
- `src/router/hashRouter.ts`: `Route` union (`home | author | notfound`) + `useHashRoute()` with a `hashchange` listener (add/remove on cleanup). Lowercase-64hex-only matcher `/^#\/a\/([0-9a-f]{64})$/` — any other hash → `notfound`.

**Task 2 — WindowIndicator + SuspectEntryBar (`7f39d1b`)**
- `src/views/WindowIndicator.tsx` (+ CSS): presentational DRILL-05 denominator. No dismiss prop, no hidden branch — renders in every case incl. `N === 0`. Three verbatim UI-SPEC strings (N=0 / full window / partial window). The "more available — partial window" segment is amber (recoverable) + a dot so a partial window never reads as exoneration. `Intl.NumberFormat` for N; UTC trimmed to seconds + `Z`. No accent.
- `src/views/SuspectEntryBar.tsx` (+ CSS): controlled paste bar with the verbatim placeholder; `parseIdentifier(input)` on submit. `ok` → `window.location.hash = '#/a/' + hex` (clears error); `!ok` → inline amber note ("Not a valid npub / note / nprofile or 64-char hex.", `role="status" aria-live="polite"`) and STAYS on the page. EMPTY disables submit when blank; NOT_RECOGNIZED and REJECTED_NSEC both surface the same generic note (never reveals the input was a secret key). The "Inspect author" submit is the app's ONE accent action; the input stays neutral.

**Task 3 — AuthorDrillDown view + App shell/router wiring (`3a67751`)**
- `src/views/AuthorDrillDown.tsx` (+ CSS): `useAuthorWindow(hex)`. Identity header shows BOTH forms (npub + 64-char hex, mono, labeled, each with a "Copy pubkey" affordance) and a "Back to corpus stats" control. State gating mirrors StatsDashboard: connecting → full error shell (via `errorTreatment` over every ApiError kind; NETWORK echoes `GRAPHQL_URL`, INTERNAL generic, VALIDATION verbatim) → loaded. ID-03 zero-match renders the neutral calm empty state WITH `<WindowIndicator>` (N=0). Timeline (≥1 event) always renders `<WindowIndicator>` then the newest-first list (server `createdAt` DESC, never re-sorted); each row = kind (mono) / UTC + raw epoch (mono) / single-line truncated escaped content (sans). DRILL-06 Load more is one page per click (disabled "Loading…" while in flight); when `hasMore` is false the button is replaced by the muted "End of available events — this is the full window." No accent anywhere in the view.
- `src/App.tsx` (+ `App.module.css`): preserves the `waitForReady` gate + shared `ConnectingShell`. After ready, renders an app shell hosting `<SuspectEntryBar>` on every route, then switches on `useHashRoute()`: home → StatsDashboard, author → AuthorDrillDown(hex), notfound → a neutral not-found block with a back-to-home control.

## Verification

- `npm run codegen && npm run build` (tsc -b + vite build) passes — EventsDocument typed; all new modules compile.
- `npm run test` — 25/25 pass (no regressions; pre-existing identifier/errors/useStatsPoll suites green).
- Grep gates all pass:
  - events doc selects only the five fields; raw/sig/tags absent (incl. comments).
  - useAuthorWindow has the THREW guard + `classify` + `INVALID_CURSOR` restart + `PAGE_LIMIT = 100`.
  - router matches lowercase-64hex only.
  - no `dangerouslySetInnerHTML` in any of WindowIndicator / SuspectEntryBar / AuthorDrillDown (incl. comments).
  - accent present only in `SuspectEntryBar.module.css`; absent from WindowIndicator / AuthorDrillDown modules.
  - App wires `useHashRoute` + `SuspectEntryBar` + `AuthorDrillDown` and preserves `waitForReady`; `WindowIndicator` appears 3× in AuthorDrillDown (timeline + zero-match).

## Threat Mitigations Applied

- **T-02-04 (XSS):** event `content`, `createdAt`, hex, npub all rendered as escaped plaintext via JSX; content preview single-line truncated; no `dangerouslySetInnerHTML`.
- **T-02-05 (forgeable createdAt):** rendered as data (UTC + raw epoch), never asserted as truth; the window indicator frames N as a denominator. The asymmetric rate panel + caveat land in 02-03.
- **T-02-06 (non-normalized hash):** router accepts lowercase-64hex only; navigation sets the hash only after `parseIdentifier` normalizes; non-matching → notfound.
- **T-02-07 (DoS / aggressive paging):** button-driven single-page loads, explicit limit 100, loadMore gated on loading + in-flight ref.
- **T-02-08 (info disclosure):** reuses `classify()` — INTERNAL generic, VALIDATION verbatim, NETWORK echoes only `VITE_GRAPHQL_URL`.
- **T-02-09 (opaque cursor):** `endCursor` stored/passed back verbatim, never parsed; INVALID_CURSOR resets to page 1.

## Deviations from Plan

None — plan executed as written. Two plan-permitted decisions exercised: (1) `loadMore` is the recommended single-page-per-click variant (not accumulatePages); (2) the display npub is derived via `parseIdentifier(hex).npub` rather than a second nip19 call site, keeping the identifier module the single normalizer.

One implementation refinement within plan scope: an author `runId` ref was added so a previous author's in-flight fetch is dropped on `hex` change/unmount (prevents stale-author state writes alongside the Pitfall-4 double-click guard).

## Known Stubs

None. The rate/burst panel and the forgeable-createdAt caveat are explicitly the next slice (02-03, DRILL-01) per the plan's scope note — not stubs in this plan's surface.

## Remaining Work

**Task 4 — human-verify checkpoint (NOT auto-approved).** The end-to-end slice must be verified by a human against the live lens (`npm run dev`): paste → route → both identity forms → newest-first timeline → indicator (incl. amber partial) → single-page Load more (no double-append) → end caption → parse-failure stays home → valid-but-zero-match neutral empty state with N=0 indicator → accent only on "Inspect author". See 02-02-PLAN.md Task 4 for the full verification script.

## Self-Check: PASSED

- src/queries/events.graphql.ts — FOUND
- src/hooks/useAuthorWindow.ts — FOUND
- src/router/hashRouter.ts — FOUND
- src/views/WindowIndicator.tsx — FOUND
- src/views/WindowIndicator.module.css — FOUND
- src/views/SuspectEntryBar.tsx — FOUND
- src/views/SuspectEntryBar.module.css — FOUND
- src/views/AuthorDrillDown.tsx — FOUND
- src/views/AuthorDrillDown.module.css — FOUND
- src/App.module.css — FOUND
- src/App.tsx — FOUND (modified)
- Commit ecb7a2c — FOUND
- Commit 7f39d1b — FOUND
- Commit 3a67751 — FOUND
