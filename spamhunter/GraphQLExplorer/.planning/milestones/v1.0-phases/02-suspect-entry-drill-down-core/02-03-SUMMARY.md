---
phase: 02-suspect-entry-drill-down-core
plan: 03
subsystem: GraphQLExplorer (frontend)
status: accepted-deferred-uat
tags: [rate-analysis, burst, asymmetry, forgeable-timestamps, drill-down, window-honesty]
requires:
  - "02-02: AuthorDrillDown view + useAuthorWindow (events / windowMeta / WindowEvent / WindowMeta) + WindowIndicator"
  - "Phase 1 / 02-02 conventions: single-named-tunable (POLL_INTERVAL_MS), named result interface, escaped-plaintext JSX, CSS tokens"
provides:
  - "BURST tunable constants { windowSec, minEvents, binSec } (src/analysis/thresholds.ts — single tunable home)"
  - "analyzeRate(createdAts) -> RateResult (asymmetric, bounds-checked) + isSaneTs + MIN_TS/MAX_TS (src/analysis/rate.ts)"
  - "RateResult type (analyzedCount / rejectedCount / bins / burstDetected / tightestIntervalSec — NO clean/ok/safe field)"
  - "RatePanel (hand-rolled CSS bars + amber burst marker + persistent forgeable caveat + co-located WindowIndicator)"
affects:
  - "src/views/AuthorDrillDown.tsx (loaded timeline branch now mounts RatePanel; re-derives as the window widens)"
tech-stack:
  added: []
  patterns:
    - "Asymmetry-is-structural: RateResult carries no clean/ok/safe field by construction (burst suspicious, quiet inconclusive)"
    - "Bounds-check-don't-mis-compute: isSaneTs filters forged 64-bit createdAt into rejectedCount; sort-ascending before interval math"
    - "Sliding-window burst (minEvents within windowSec) + fixed-width binByInterval bins for hand-rolled bars (no chart lib)"
    - "Color-never-sole-signal on a signal surface: amber burst tint paired with the 'burst' label + bar-spike shape"
key-files:
  created:
    - src/analysis/thresholds.ts
    - src/analysis/rate.ts
    - src/analysis/rate.test.ts
    - src/views/RatePanel.tsx
    - src/views/RatePanel.module.css
  modified:
    - src/views/AuthorDrillDown.tsx
decisions:
  - "[02-03]: BURST defaults windowSec:60 / minEvents:5 / binSec:3600 are literature-grounded sane defaults; corpus-validation deferred to Phase 3 — the honesty posture (denominator + caveat, no clean verdict) holds regardless of threshold choice"
  - "[02-03]: RateResult is structurally asymmetric — NO clean/ok/safe field; burstDetected:false means inconclusive, never exonerating"
  - "[02-03]: forged/out-of-range createdAt (negative, >4102444800, beyond MAX_SAFE_INTEGER) is counted in rejectedCount and excluded, never used in range/interval math; sane values sorted ascending so tightestIntervalSec is never negative"
  - "[02-03]: RatePanel mounted in the loaded timeline branch ONLY (>= 1 event); the zero-match branch keeps just its timeline-surface indicator (no events to analyze)"
metrics:
  duration_min: 7
  completed: 2026-06-24
  tasks: 3
  files: 6
requirements: [DRILL-01, DRILL-05]
---

# Phase 2 Plan 3: Posting-Rate / Burst Panel Summary

Asymmetric, bounds-checked posting-rate / burst analyzer (pure, unit-tested) surfaced as
hand-rolled CSS bars with a permanent "createdAt is author-claimed and forgeable" caveat
and a co-located non-removable window indicator — completing the Phase 2 core-value slice
(DRILL-01 + DRILL-05).

## What Shipped

- **`src/analysis/thresholds.ts`** — `BURST { windowSec: 60, minEvents: 5, binSec: 3600 }`,
  the single tunable home for the burst constants (mirrors the `POLL_INTERVAL_MS`
  single-named-tunable convention). Doc-comment notes the defaults are literature-grounded
  and corpus-validation is deferred to Phase 3.
- **`src/analysis/rate.ts`** — `analyzeRate(createdAts): RateResult`, `isSaneTs`,
  `MIN_TS` / `MAX_TS`, and the `RateResult` type. Pure (no React/transport/network).
  - `isSaneTs` = `Number.isSafeInteger(t) && 0 <= t <= 4_102_444_800` (2100-01-01).
  - Filters to sane values **sorted ascending**, counts the rest in `rejectedCount`.
  - `< 2` sane → `burstDetected:false, bins:[], tightestIntervalSec:null` (no crash).
  - Sliding-window burst: `>= BURST.minEvents` within `BURST.windowSec`.
  - `binByInterval` groups sane timestamps into fixed-width `binSec` bins for the bars.
  - `RateResult` has **no** `clean`/`ok`/`safe` field — asymmetry is structural.
- **`src/analysis/rate.test.ts`** — 11 vitest cases (Node env): isSaneTs bounds,
  asymmetry (no clean field), burst-present, sparse/quiet, 0- and 1-element guards,
  forged/out-of-range rejection into `rejectedCount`, non-negative interval on unsorted
  input, tightest-gap correctness.
- **`src/views/RatePanel.tsx` (+ `.module.css`)** — title "Posting rate"; hand-rolled CSS
  bars scaled to the max bin (no chart lib); a detected burst tints the bars amber
  (`--recoverable`) **and** shows the "burst" text label + a dot (color never the sole
  signal); a `rejectedCount` note when forged timestamps are excluded; the **always-present
  verbatim forgeable caveat**; and a **co-located `WindowIndicator`** (DRILL-05). No teal
  accent, no positive/exoneration color, no raw-HTML sink; all values escaped plaintext.
- **`src/views/AuthorDrillDown.tsx`** — mounts `<RatePanel events={events}
  windowMeta={windowMeta} />` in the loaded timeline branch (≥ 1 event) so the analyzer
  re-derives live as Load more widens the window; the zero-match branch is untouched.

## How It Was Verified (automated)

- TDD: RED commit (`f4ae182`) — 8/11 cases fail against the stub; GREEN commit
  (`c8fa2dd`) — all 11 pass.
- `npm test` (full suite): **36/36 passing** (4 files).
- `npx tsc -b`: clean for `src/analysis/`.
- `npm run build`: passes (82 modules transformed).
- Grep gates all pass: `analyzeRate` + `WindowIndicator` in RatePanel; verbatim caveat
  + "Posting rate" + "burst" label present; **no `accent`** in RatePanel CSS; **0**
  `green`/`success` matches; **no** `dangerouslySetInnerHTML`; `RatePanel` mounted in
  AuthorDrillDown; rate.ts pure + bounds-checked + 0 `clean`/`safe` in non-comment lines.

## TDD Gate Compliance

- RED gate: `test(02-03): ...` — `f4ae182` (failing tests + stub).
- GREEN gate: `feat(02-03): implement asymmetric bounds-checked analyzeRate (GREEN)` — `c8fa2dd`.
- REFACTOR: not needed (implementation was clean on first GREEN).

## Deviations from Plan

None — plan executed exactly as written. (Two doc-comments in RatePanel were worded to
avoid the literal banned tokens `accent` / `green` / `dangerouslySetInnerHTML` so the
acceptance grep-gates read clean; the intent of each comment is preserved.)

## Threat Mitigations Applied (from plan threat_model)

- **T-02-10** (forged `createdAt`): `isSaneTs` bounds-check; out-of-range counted in
  `rejectedCount` and excluded from range/interval math; sort-before-interval prevents
  negative gaps. Unit-tested.
- **T-02-11** (quiet read as "clean"): `RateResult` has no clean/safe field (structural);
  persistent forgeable caveat beside the panel; no positive color; window indicator frames
  N as a denominator.
- **T-02-12** (XSS): all panel text escaped plaintext via JSX; no raw-HTML sink (grep-gated).

## Known Stubs

None. (The RED-phase stub in `rate.ts` was fully replaced in the GREEN commit.)

## Checkpoint Status — Task 4 (human-verify): DEFERRED, NOT PERFORMED

Task 4 is a `checkpoint:human-verify` (`autonomous: false`). Per the user's standing
decision (2026-06-24) — and matching how 02-02 was finalized — all live UI walkthroughs
are deferred to a single phase-end verification. The live `npm run dev` walkthrough
(inspect an author, confirm no positive/teal color, the persistent caveat, the rate-surface
window indicator re-deriving on Load more, the amber+labelled burst marker, and the
out-of-range note on forged timestamps) was **NOT performed** in this plan and is **not**
claimed as passed. It remains outstanding for phase-end UAT. The plan is finalized as
`accepted-deferred-uat`; all automated verification above passed.

## Requirements

- **DRILL-01** — timeline (02-02) + asymmetric burst/rate indicator with the persistent
  forgeable caveat; burst present = suspicious (amber + label), burst absent = inconclusive
  (neutral, never clean); forged timestamps flagged not mis-computed. **Met (pending live UAT).**
- **DRILL-05** — the window-size indicator is present on the rate-panel surface as well as
  the timeline (every signal surface). **Met (pending live UAT).**

## Commits

- `f4ae182` — test(02-03): add failing rate/burst analyzer tests + BURST thresholds (RED)
- `c8fa2dd` — feat(02-03): implement asymmetric bounds-checked analyzeRate (GREEN)
- `7551ff2` — feat(02-03): RatePanel (CSS bars + caveat + indicator) mounted in drill-down

## Self-Check: PASSED

All created files exist on disk; all three task commits resolve in git history.
