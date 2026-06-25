---
phase: 01-foundation-stats-dashboard
plan: 03
subsystem: graphql-explorer-frontend
tags: [stats-dashboard, polling, page-visibility, react19, ui-spec, nudge, walking-skeleton]
requires:
  - "classify(result) -> ApiError | null + ApiError union (src/transport/errors.ts, 01-02)"
  - "urql client + StatsDocument (01-01)"
  - "waitForReady(signal?) readiness gate (src/transport/readiness.ts, 01-02)"
  - "src/styles/tokens.css design tokens (01-01)"
provides:
  - "useStatsPoll(intervalMs?) (src/hooks/useStatsPoll.ts) — setTimeout-reschedule poll + Page Visibility pause + maxLevId-diff nudge flag; returns { stats, error, loading, hasNewData, acknowledge, isPaused, refresh }"
  - "POLL_INTERVAL_MS = 5000 (tunable seconds-scale default)"
  - "shouldNudge(last, next) pure nudge predicate (unit-tested)"
  - "StatsDashboard (src/views/StatsDashboard.tsx) — four cards + complete distinct-state set + corpus-changed nudge + Refresh CTA"
  - "StatsDashboard.module.css — hand-rolled CSS consuming tokens.css"
affects:
  - "Phases 2-4 inherit the dashboard state-treatment set, the poll-and-diff discipline, and the tokens/CSS-module pattern"
tech-stack:
  added: []
  patterns:
    - "setTimeout-reschedule polling (NOT setInterval) — slow request never stacks; hidden tab cleanly skips"
    - "Page Visibility API (visibilityState / visibilitychange) pauses the network poll and surfaces an isPaused signal"
    - "maxLevId-diff flips a nudge flag ONLY — never auto-refetches any window (analyst decides)"
    - "Complete distinct, non-blank UI state set branched off the classify() ApiError union + loaded/empty/paused"
    - "Teal accent confined to exactly two elements: corpus-changed nudge + live-poll active dot"
    - "Color never the sole signal — every state pairs color with a label and/or dot/badge shape"
key-files:
  created:
    - src/hooks/useStatsPoll.ts
    - src/hooks/useStatsPoll.test.ts
    - src/views/StatsDashboard.tsx
    - src/views/StatsDashboard.module.css
  modified:
    - src/App.tsx
decisions:
  - "TDD scope for Task 1: the hook's effect touches document (Page Visibility) and the urql client, which the existing Node-environment vitest config cannot exercise without adding jsdom + RTL (package installs, excluded from auto-fix). The nudge DECISION LOGIC was extracted into a pure exported shouldNudge(last, next) helper and unit-tested (6 cases) — covering the contract-critical behavior (first observation never nudges; strict increase only) without a DOM dependency."
  - "useStatsPoll surfaces a classified error (via classify() from 01-02) so the dashboard branches on the SAME ApiError union the rest of the transport uses — the view never reads errors[] directly."
  - "Refresh CTA both re-pulls on demand AND acknowledges the nudge (the analyst chose to update); the nudge's own dismiss button reuses the same refresh path."
  - "Error rendering is two-tier: a full-shell error before any successful load, and a non-blocking inline note above the last-good cards once data exists — so a transient poll error never blanks a populated dashboard."
metrics:
  duration: ~12m
  completed: 2026-06-24
  tasks: 2
  files: 5
status: complete
---

# Phase 1 Plan 03: Stats Dashboard Summary

Replaced the raw slice-1 stats render with the polished, honest corpus-stats dashboard: a `useStatsPoll` hook that polls on a seconds-scale `setTimeout` reschedule, pauses on a hidden tab via the Page Visibility API, and flips a `maxLevId`-diff nudge flag without ever auto-refetching — and a `StatsDashboard` view rendering the four typed scalars plus the complete distinct-state set from the approved UI-SPEC with verbatim copy and disciplined color (STATS-01, STATS-02). The phase goal is now true: an analyst launches the tool and watches live corpus stats with an honest change-nudge.

## What Was Built

- **Task 1 — useStatsPoll** (`19d904f`, TDD): `src/hooks/useStatsPoll.ts` exports `useStatsPoll(intervalMs = POLL_INTERVAL_MS)`, the tunable `POLL_INTERVAL_MS = 5000` constant, and the pure `shouldNudge(last, next)` predicate. The hook drives `client.query(StatsDocument)` on a `setTimeout` **reschedule** (never `setInterval`, so a slow request can't stack overlapping polls); when `document.visibilityState === 'hidden'` the tick skips the network call, sets `isPaused`, and reschedules; a `visibilitychange` listener resumes immediately on re-show. A strict `maxLevId` increase flips `hasNewData` (nudge flag) — it **never** auto-refetches. Returns `{ stats, error, loading, hasNewData, acknowledge, isPaused, refresh }`; the timer and listener are cleaned up on unmount. `src/hooks/useStatsPoll.test.ts` (6 cases) covers the nudge predicate (first observation never nudges; strict increase only; unchanged/decreasing never nudge) and the seconds-scale default.
- **Task 2 — StatsDashboard + App wiring** (`30d52fd`): `src/views/StatsDashboard.tsx` consumes `useStatsPoll` and renders the four cards (Event count, Max levId, DB version, Pinned strfry version) in a 2×2 grid at ≥ md width (single column below) — sans 13/600 muted labels, mono 28/600 primary values, large integers via `Intl.NumberFormat`, `pinnedStrfryVersion` as escaped plaintext. It implements the COMPLETE UI-SPEC state set branched off the `classify()` `ApiError` union plus loaded/empty/paused: connecting shell, loaded/live (teal live-poll dot), empty-corpus (`eventCount === 0` → neutral calm caption, not an error), poll-paused (dimmed neutral dot + "Paused (tab hidden)"), corpus-changed (teal dismissible nudge), `INVALID_CURSOR`/`TOO_MANY_AUTHORS`/`NOT_READY`/`PAYLOAD_TOO_LARGE`/`VALIDATION` (amber), `INTERNAL` (red, generic — no internals), and `NETWORK` (red, direct-connection wording). `src/views/StatsDashboard.module.css` is hand-rolled, consuming `tokens.css`; the teal accent appears in exactly two places (the nudge and the live-poll dot). `src/App.tsx` now mounts `<StatsDashboard />` after `waitForReady` resolves, replacing the raw slice-1 render.

## Verification Results

- `npm run build` (`tsc -b && vite build`) exits 0, 46 modules, emits `dist/`.
- `npm test` → 16/16 green (10 existing classifier tests + 6 new `useStatsPoll` nudge/interval tests).
- Task 1 gate: `setTimeout` present, **no** `setInterval`, `visibilitychange`/`visibilityState` present → `POLL_OK`.
- Task 2 gate: `dist/` exists, **no** `dangerouslySetInnerHTML` anywhere in `src/`, `Intl.NumberFormat` present, "Corpus changed" present → `DASH_OK`.
- Verbatim copy audit: all 12 UI-SPEC strings (Corpus stats, Refresh stats, Connecting to relay…, No events in corpus yet, Paused (tab hidden), Corpus changed — refresh to update., and the five error messages + INTERNAL/NETWORK) match verbatim.
- Accent audit: `var(--accent)` appears only in the CSS module, scoped to `.dotLive` (live-poll dot) and `.nudge`/`.nudgeDismiss` (the corpus-changed nudge) — nowhere else.
- Prohibition audit: no inline base URL / `192.168` / `server.proxy` / `credentials` in the new view or hook. (The `127.0.0.1:8080` literal in the NETWORK error message is the UI-SPEC copy string, a user-facing message — not a client base URL.)

## Manual verification (advisory — autonomous plan, no blocking checkpoint)

Task 2's `<human-check>` (live `npm run dev` against the lens: 2×2 cards with real values, teal live-poll dot animating while visible / dimming on tab switch, the teal nudge appearing on a `maxLevId` bump without auto-refetch until Refresh stats is clicked, and `eventCount=0` reading as a calm "No events in corpus yet") is left for the operator. The live corpus is non-empty (eventCount ~27.1M) so the empty-corpus state won't trigger live — it is implemented and unit-reachable via a mocked `eventCount: 0` (the view branches on `stats.eventCount === 0`), per the environment note.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Reworded comments that tripped the automated prohibition greps**
- **Found during:** Task 1 and Task 2 verification
- **Issue:** The plan's automated gates use `! grep -q "setInterval"` and `! grep -rq "dangerouslySetInnerHTML"`. JSDoc comments that *named* the forbidden APIs ("never setInterval", "never dangerouslySetInnerHTML") matched the greps and failed the gates even though neither API is used.
- **Fix:** Reworded the comments to describe the discipline without the literal forbidden tokens ("setTimeout RESCHEDULE (NOT a fixed repeating timer)"; "raw-HTML injection is never used"). Behavior unchanged; gates now pass cleanly (`POLL_OK`, `DASH_OK`).
- **Files modified:** src/hooks/useStatsPoll.ts, src/views/StatsDashboard.tsx
- **Commits:** 19d904f, 30d52fd

## TDD Gate Compliance

Task 1 was `tdd="true"`. RED was confirmed (the test failed with "Cannot find module './useStatsPoll'" before the hook existed); GREEN followed (16/16 green after writing the hook). Per the decision above, the RED/GREEN cycle targeted the extracted pure `shouldNudge` predicate + `POLL_INTERVAL_MS` (the contract-critical nudge semantics) rather than the DOM/network effect, because the existing Node-environment vitest config has no jsdom and adding RTL/jsdom is a package install (excluded from auto-fix). The test and implementation were committed together in `19d904f` (single feat commit on the main working tree, sequential mode).

## Threat Surface

No new surface beyond the plan's `<threat_model>`. Mitigations applied:
- **T-01-07** (Tampering/XSS): `pinnedStrfryVersion` and all scalars render via JSX interpolation (React escapes by default); no raw-HTML injection API is used anywhere in `src/`.
- **T-01-08** (Information Disclosure): the `INTERNAL` state shows the generic UI-SPEC copy "Something went wrong reading the corpus. Retrying shortly." — the raw server message is never reachable (the classifier from 01-02 already drops it; the view has no message field to render for `INTERNAL`). `VALIDATION` messages (user-safe per contract §7) are shown verbatim.
- **T-01-09** (self-inflicted DoS): `useStatsPoll` is seconds-scale (5000ms), pauses on a hidden tab, and is nudge-flag-only (no auto-refetch).

## Known Stubs

None. Every state treatment is implemented and non-blank. The empty-corpus branch is implemented but won't trigger against the live (non-empty) corpus — that is an environment fact, not a stub; the view genuinely branches on `stats.eventCount === 0`.

## Self-Check: PASSED

- FOUND: src/hooks/useStatsPoll.ts, src/hooks/useStatsPoll.test.ts, src/views/StatsDashboard.tsx, src/views/StatsDashboard.module.css
- FOUND commits: 19d904f, 30d52fd
