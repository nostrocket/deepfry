---
phase: 03-remaining-spam-signals
plan: 02
subsystem: spamhunter/GraphQLExplorer (frontend — author drill-down signal panels + raw inspector)
tags: [frontend, react, graphql-codegen, ui, spam-signals, drill-down, xss-safe]
requires:
  - 03-01 (pure analyzers nearDup.ts / tags.ts / kinds.ts / kindNames.ts + thresholds NEAR_DUP/TAGS)
  - 02 (RatePanel shell, WindowIndicator, useAuthorWindow window + imperative transport boundary, AuthorDrillDown host)
provides:
  - "EventsDocument now selects tags (raw stays out — lazy-only)"
  - "RawEventDocument — the single-event raw-bytes query (ids path, the only place raw is fetched)"
  - "WindowEvent gains tags: string[][]"
  - "DuplicatePanel / TagsPanel / KindsPanel — three stacked client-side signal panels"
  - "RawInspector — lazy per-event escaped <pre> canonical-bytes viewer"
  - "errorTreatment exported from AuthorDrillDown for reuse"
affects:
  - src/views/AuthorDrillDown.tsx (hosts the three panels + per-row View raw)
  - src/hooks/useAuthorWindow.ts (WindowEvent shape)
  - src/queries/events.graphql.ts (selection)
  - src/gql/* (regenerated)
tech-stack:
  added: []   # zero new runtime dependencies (per UI-SPEC / RESEARCH)
  patterns:
    - "pure-analyzer-consumer panel (RatePanel shell: --surface card, 13px/600 muted title, co-located WindowIndicator, persistent caveat, amber-on-signal never teal/green)"
    - "useMemo-bounded O(n^2) call (nearDup keyed on events array identity)"
    - "imperative classify-gated lazy fetch (client.query + throw-guard + classify-before-data, copied from useAuthorWindow)"
    - "escaped <pre> render (JSON.parse try/catch pretty/verbatim, never dangerouslySetInnerHTML)"
key-files:
  created:
    - src/queries/rawEvent.graphql.ts
    - src/views/DuplicatePanel.tsx
    - src/views/DuplicatePanel.module.css
    - src/views/TagsPanel.tsx
    - src/views/TagsPanel.module.css
    - src/views/KindsPanel.tsx
    - src/views/KindsPanel.module.css
    - src/views/RawInspector.tsx
    - src/views/RawInspector.module.css
  modified:
    - src/queries/events.graphql.ts
    - src/hooks/useAuthorWindow.ts
    - src/views/AuthorDrillDown.tsx
    - src/views/AuthorDrillDown.module.css
    - src/gql/gql.ts
    - src/gql/graphql.ts
decisions:
  - "raw kept OUT of EventsDocument — selected ONLY in rawEvent.graphql.ts (lazy, single-event, bounds the per-page payload — T-03-08)"
  - "row-inline raw expand (RESEARCH Open-Question 2) — no modal/focus-trap; RawInspector owns its idle->loaded state and fetches only on activation"
  - "TagsPanel renders each amber badge independently (high tag count / high mention fan-out / hashtag stuffing) so one outlier entry can carry multiple; highTagCount derived from entry.tagCount vs TAGS.highTagCount"
  - "timeline rows wrapped in .rowGroup so the divider/last-child logic survives the per-row inspector child"
metrics:
  duration: ~25m
  completed: 2026-06-25
  tasks: 3
  files: 15
status: complete
---

# Phase 3 Plan 02: Author Drill-Down Signal Panels + Lazy Raw Inspector Summary

Mounted the user-visible half of the slice — three stacked client-side signal panels (DuplicatePanel / TagsPanel / KindsPanel) consuming the slice-03-01 pure analyzers over the same `useAuthorWindow` window, plus a lazy per-event escaped raw-JSON inspector — on the existing `AuthorDrillDown` view, with `tags` wired onto the window query and the canonical `raw` bytes fetched lazily via a dedicated single-event document.

## What shipped

- **Task 1 (BLOCKING codegen):** Added `tags` to `EventsDocument` (six-field selection; `raw` stays out), created `RawEventDocument` (`events(filter:{ids},limit:1){events{id raw}}` — the only place `raw` is selected), extended `WindowEvent` with `tags: string[][]`, and ran `npm run codegen` so the new field/document are typed. `tsc -b` green against the regenerated types.
- **Task 2:** Three panels, each `{ events, windowMeta }`, re-deriving live on Load more, co-locating a non-removable `WindowIndicator`, amber-on-signal only (no teal/green/accent), escaped JSX text nodes only:
  - **DuplicatePanel (DRILL-02):** O(n²) `nearDup` wrapped in `useMemo` keyed on `events`; "X of N fetched" framing; amber `exact duplicate` / `near-duplicate` cluster badges (dot + label), expandable to escaped single-line member previews; neutral zero/N=0 facts; persistent asymmetry note.
  - **TagsPanel (DRILL-03):** top-N mentions (truncated mono pubkeys) + top-N hashtags (escaped) + `{count} event references`; per-event amber outlier badges driven by the analyzer's `massMention`/`stuffing` flags + derived `high tag count` — verbatim "high mention fan-out" / "hashtag stuffing" labels; malformed-rows counted note.
  - **KindsPanel (DRILL-04):** hand-rolled neutral CSS bar histogram (bars always `--text-muted`, never amber), NIP names via `KIND_NAMES` + raw kind number, unknown-kind label, amber out-of-range flagged note.
- **Task 3:** `RawInspector` — imperative classify-gated lazy fetch (`client.query(RawEventDocument, …, { requestPolicy: 'network-only' })` + `.catch(() => 'THREW')` + `classify()` before reading data, no `useQuery`); escaped `<pre>` pretty-printed via `JSON.parse`/`JSON.stringify` in try/catch (verbatim fallback); amber retryable / red hard / neutral zero-match states; `errorTreatment` lifted/exported from `AuthorDrillDown`. Mounted the three panels stacked after `RatePanel` in the loaded branch and a per-row "View raw" trigger (row-inline expand) in `TimelineRow`.

## Verification

- `npm run codegen` — clean, idempotent (no `src/gql` drift on re-run).
- `npm run build` (tsc -b + vite) — green.
- `npx vitest run` — 75/75 pass (7 files); no analyzer-suite regression (panels are pure consumers).
- Grep gates: `raw` count in `events.graphql.ts` = 0; `dangerouslySetInnerHTML` = 0 across all new files; no `teal|--accent|#3DD6C0|green|success|clean` in any panel/inspector CSS; `useQuery` = 0 in RawInspector; all verbatim copy strings present.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `.row:last-child` border logic broken by per-row inspector**
- **Found during:** Task 3
- **Issue:** Wrapping each timeline row in a `.rowGroup` (to host the row-inline `RawInspector`) made every `.row` the sole `.row` child of its group, so `.row:last-child` always matched and removed all row dividers.
- **Fix:** Moved the divider + `:last-child` reset onto `.rowGroup`; added `.rowGroup > :not(.row)` padding so the inspector aligns under the row band.
- **Files modified:** src/views/AuthorDrillDown.module.css
- **Commit:** 16966d4

### Comment-token rewordings (gate hygiene, not behavior)

Several plan grep gates were written to return 0 for tokens (`raw`, `dangerouslySetInnerHTML`, `useQuery`, `teal`/`clean`) that I had initially used inside explanatory comments describing the discipline being followed. The substantive prohibitions were always met (no `raw` GraphQL field, no injection sink, no declarative hook, no accent/exoneration color in code). I reworded the comments (e.g. "no raw-HTML injection sink", "the declarative urql hook", "never read as exonerating") so the literal gates also pass without weakening the explanation. No runtime behavior changed.

## Threat surface

All four mitigations from the plan's threat register are in place and grep-confirmed:
- **T-03-05** (XSS): every author-controlled value (content previews, hashtags, pubkeys, raw bytes) renders as an escaped JSX text node; zero `dangerouslySetInnerHTML`.
- **T-03-06** (DoS): `JSON.parse` in try/catch → verbatim fallback; `<pre>` `pre-wrap` + `break-all`.
- **T-03-07** (info disclosure): lazy-fetch errors mapped through the shared `errorTreatment`/`classify()` boundary — generic copy, no internals.
- **T-03-08** (payload inflation): `raw` selected only in `rawEvent.graphql.ts`, grep-confirmed absent from `EventsDocument`.

No new threat surface beyond the plan's `<threat_model>`.

## Self-Check: PASSED

- All 9 created files present on disk.
- All 3 task commits present: 5058a7e, ccccf95, 16966d4.
