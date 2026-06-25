---
phase: 04-batch-triage
plan: 02
subsystem: ui
tags: [react, hash-router, css-modules, batch-triage, sortable-table, escaped-plaintext, accent-reservation, nostr]
status: accepted-deferred-uat

# Dependency graph
requires:
  - phase: 04-batch-triage
    plan: 01
    provides: useLatestPerAuthor + useAuthorEnumeration hooks, triageAuthor / mergeByAuthor / chunk pure modules, parseBatchInput tokenizer, TRIAGE thresholds, codegen-typed documents
  - phase: 02-author-drilldown
    provides: hashRouter (Route union + parseHash matcher), App shell + SuspectEntryBar, WindowIndicator amber-on-partial pattern, ConnectingShell, parseIdentifier
  - phase: 03-signals
    provides: RatePanel amber-on-signal chip CSS, tokens.css design system
provides:
  - "#/batch top-level route ({ name: 'batch' }) + exact matcher + extended hashRouter test"
  - BatchImport view (paste / file-upload / enumerate sources → one deduped set, import summary, large-set warning, single accent Triage submit)
  - TriageTable view (sortable per-signal table, amber-on-signal chips, explicit 0-events rows, two batch denominators, per-chunk error+retry, row drill-in to #/a/<hex>)
  - BatchTriage.module.css (token-only shared CSS, accent confined to .triageSubmit)
  - Neutral 'Batch triage' nav affordance in the App shell header
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single accent action discipline extended by exactly one (.triageSubmit) — verified accent confined to that class"
    - "Composition-only UI: two views wire 04-01 hooks + pure modules with zero new runtime deps"
    - "Pure local .sort() over computed rows for sortable columns (no refetch)"
    - "Escaped-plaintext rendering of all author-supplied tokens via JSX interpolation (no dangerouslySetInnerHTML)"
    - "Browser FileReader platform API for in-browser file read (never uploaded), size-bounded by TRIAGE.maxFileBytes"

key-files:
  created:
    - src/router/hashRouter.test.ts
    - src/views/BatchImport.tsx
    - src/views/TriageTable.tsx
    - src/views/BatchTriage.module.css
  modified:
    - src/router/hashRouter.ts
    - src/App.tsx
    - src/App.module.css

decisions:
  - "Collapsed both UI-SPEC CSS modules into one BatchTriage.module.css shared by BatchImport + TriageTable (RESEARCH-sanctioned option; one source of the accent/amber discipline)"
  - "BatchImport owns the collected deduped hex set + run flag and renders <TriageTable inputHexes={...}/> once Triage is submitted; a fresh array reference per submit re-triggers the table effect"
  - "Combined import summary sums paste + file counts and unions paste/file valid hexes with enumerated authors into the single collected set fed to triage"
  - "Default sort = event-count descending; picking a new column defaults to descending (most-signal/most-events first)"

metrics:
  duration: ~7m
  completed: 2026-06-25
  tasks_completed: 3
  files_created: 4
  files_modified: 3
  tests_passing: 113
---

# Phase 4 Plan 02: Batch Triage UI Surfaces Summary

The thin UI composition over the 04-01 core: a `#/batch` route, a BatchImport view (paste + file + enumerate → one deduped set with a transparent import summary and a single accent Triage submit), and a sortable TriageTable of transparent per-signal indicators that drills into the existing `#/a/<hex>` view — every Phase 1–3 honesty contract (accent reservation, amber-on-signal, escaped plaintext, non-removable denominators, "absence ≠ clean") carried forward at batch scale.

## What was built

- **Task 1 — `#/batch` route + shell wiring** (commit `d018b03`): extended the `hashRouter` `Route` union with `{ name: 'batch' }` and added an exact `#/batch` matcher (home/author/notfound untouched); created `hashRouter.test.ts` (batch / home / author / notfound + exact-match-only + uppercase/wrong-length-reject cases); mounted `<BatchImport />` at `route.name === 'batch'` in `App.tsx` and added a neutral "Batch triage" nav button to the shell header (accent stays on the two go-submits).
- **Task 2 — BatchImport view** (commit `d205cdc`): three import sources feeding one deduped lowercase-hex set — a neutral paste textarea (`parseBatchInput` live), a neutral `.txt/.csv` file picker + drop zone via the browser `FileReader` (`TRIAGE.maxFileBytes` enforced, amber inline error on oversize/unreadable, paste still works), and a neutral "Enumerate corpus authors" action wired to `useAuthorEnumeration` (live running count + Stop, complete/stopped/error states). Import summary "N valid · M duplicates removed · K unparseable" with the unparseable tokens listed as escaped plaintext; non-blocking large-set warning above `TRIAGE.largeSetWarn`; the single accent "Triage" submit (disabled until ≥1 valid).
- **Task 3 — TriageTable view** (commit `5ba00c9`): a hand-rolled sortable `<table>` (no grid/table library) with Author / Events / Burst / Near-dup / Tag fan-out columns, default event-count descending, pure local `.sort()` (no refetch). Per-signal amber chips (dot + verbatim label) when tripped, neutral "—" when absent; explicit muted "0 events" cells; no clean/score column, no green/teal. Non-removable "Triaged N of M authors" denominator (amber on partial) + the persistent first-pass-screen framing line; per-chunk recoverable error + Retry (413/503/INVALID) with partial rows retained and generic copy for hard errors (no server-internal leak); whole-row click drills into the existing `#/a/<hex>` route using the row's normalized hex.
- **`BatchTriage.module.css`** (shared by both views): token-only (consumes `tokens.css`, no new tokens), amber-on-signal chip mirroring `.burstBadge`, and the teal `--accent` confined to `.triageSubmit` and its `:hover` only.

## Verification

- `npx tsc -b` — exits 0.
- `npm run build` — green (tsc + vite bundle, 105 modules).
- `npx vitest run` — 113/113 tests pass (including the 6 new `hashRouter` cases).
- Accent confinement: the only `var(--accent)` usages in `BatchTriage.module.css` are inside the `.triageSubmit` selector + its `:hover` (lines 261/263/270); all other "green"/accent grep hits are prohibition comments. No green/success/teal in the table or indicators.
- `dangerouslySetInnerHTML`: no actual JSX attribute usage in either view (`grep -E 'dangerouslySetInnerHTML\s*='` → none); the matches the acceptance grep counts are security-rationale **comments** documenting the prohibition (the same convention used by `WindowIndicator`/`SuspectEntryBar`).
- TriageTable verdict/score guard: `0` non-comment occurrences of clean/spam-score/score-column.

## Deviations from Plan

### Auto-fixed / benign discrepancies

**1. [Rule 3 — benign] `dangerouslySetInnerHTML == 0` acceptance grep reads non-zero due to comments**
- **Found during:** Task 2 / Task 3 verification.
- **Issue:** The plan's acceptance criterion `grep -c "dangerouslySetInnerHTML" … == 0` is tripped by the security-rationale **comments** in both views (e.g. "NEVER dangerouslySetInnerHTML"). These follow the established codebase convention (`WindowIndicator.tsx`, `SuspectEntryBar.tsx` carry the same comment).
- **Resolution:** Verified the actual XSS surface is clean — there is **no** `dangerouslySetInnerHTML=` JSX attribute anywhere; all author-supplied tokens/hex render via JSX interpolation. The mitigation for threat **T-04-06** holds. No code change needed; documented here for the verifier.
- **Files:** `src/views/BatchImport.tsx`, `src/views/TriageTable.tsx`. **Commit:** n/a (no change).

**2. [Planned option chosen] One shared CSS module**
- The UI-SPEC/PATTERNS allowed collapsing `BatchTriage.module.css` + `TriageTable.module.css` into one module. Chose the single shared module (one home for the accent/amber discipline). Documented as a decision, not a deviation.

## Known Stubs

None. Both views are fully wired to the live 04-01 hooks (`useLatestPerAuthor`, `useAuthorEnumeration`) and pure modules (`triageAuthor`, `mergeByAuthor`, `parseBatchInput`, `chunk*`). No hardcoded empty data, no placeholder text, no unwired components.

## Threat surface

All Phase-4 threat-register mitigations for this plan are upheld: T-04-06 (escaped plaintext, no `dangerouslySetInnerHTML`), T-04-07 (`TRIAGE.maxFileBytes` enforced before read, amber note on oversize, no crash), T-04-08 (generic per-chunk hard-error copy, no server message echoed), T-04-09 (no clean/green/score column, explicit "0 events", persistent first-pass framing + non-removable "triaged N of M" denominator). No new security-relevant surface introduced beyond the register.

## Human verification — DEFERRED (not performed)

Task 4 is a `checkpoint:human-verify` (the full live `npm run dev` batch-triage walkthrough: nav → import → triage → sort → drill-in, accent-confined-to-Triage, no green/clean). Per the standing user decision (2026-06-25) to consolidate live UI walkthroughs into a single phase-end verification, this checkpoint was **NOT performed** and the manual UI checks are **not claimed as passed**. The slice is finalized as `accepted-deferred-uat`; the live walkthrough is deferred to phase-end verification. Automated gates (typecheck, build, full test suite, accent/XSS/verdict grep checks) all pass.

## Self-Check: PASSED

- Files created/modified — all 7 present on disk (verified).
- Commits `d018b03`, `d205cdc`, `5ba00c9` — all present in git log (verified).
