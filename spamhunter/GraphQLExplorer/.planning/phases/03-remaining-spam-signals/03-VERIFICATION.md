---
phase: 03-remaining-spam-signals
verified: 2026-06-25T00:00:00Z
status: human_needed
score: 9/13 must-haves verified
behavior_unverified: 0
overrides_applied: 0
human_verification:
  - test: "Paste a suspect with repeated/near-duplicate posts, open the drill-down, and confirm the Duplicate content panel renders below the timeline showing 'X of N fetched are near-duplicates across K cluster(s)' with amber 'exact duplicate' / 'near-duplicate' cluster badges (dot + label), each expandable to escaped single-line member previews; confirm a clean author reads as a neutral muted fact, never green/clean."
    expected: "Panel visible below timeline; denominator framing present; amber badge dot+label (no color-only); zero-cluster case is a neutral fact; persistent asymmetry caveat shown."
    why_human: "Visual panel rendering and amber-vs-neutral color/shape treatment can only be confirmed in a browser against the live lens."
  - test: "On the Tags & mentions panel, confirm top-N mentions (truncated mono pubkeys), top-N hashtags, the '{count} event references' line, and amber per-event badges — 'high tag count', 'high mention fan-out', 'hashtag stuffing' — each paired with a dot shape, render correctly against a real author. Confirm the malformed-rows note appears when applicable."
    expected: "Two top-N lists + e-reference count render; amber outlier badges show with label+shape (never color-only); no teal/green; malformed-rows note when malformedTagRows>0."
    why_human: "Amber flag rendering and label+shape (no color-only) treatment are visual; depend on real tag fan-out data in the window."
  - test: "On the Event kinds panel, confirm the hand-rolled CSS bar histogram renders with NIP/kind labels (KIND_NAMES) + the raw kind number, unknown kinds show '(unknown kind)', bars stay neutral (never amber), and an amber out-of-range note appears when outOfRangeCount>0."
    expected: "Neutral bar histogram with kind labels; unknown-kind label present; amber out-of-range flagged note when applicable."
    why_human: "Hand-rolled bar layout and neutral-vs-amber rendering are visual; depend on a real kind distribution."
  - test: "On any timeline row, click 'View raw'. Confirm it lazily fetches that single event's raw bytes (network request fires only on click, not on mount) and renders escaped <pre> (pretty-printed if JSON, verbatim if not) with the correct caption. Then exercise the WR-01 fix: force/observe a failed fetch and confirm both the Retry button (re-fetches) and Close button (returns to idle) work — the error state is not a dead end. Confirm Close from loaded/zero-match also works."
    expected: "Raw fetched lazily on click; escaped <pre> pretty/verbatim with correct caption; Retry re-invokes the fetch; Close returns to idle; error state never stuck."
    why_human: "Lazy fetch-on-activation, the <pre> escaping/pretty-print UX, and the Retry/Close click-through (WR-01 fix) require interactive browser testing against the live lens."
  - test: "Confirm each of the three signal panels carries a co-located, non-removable WindowIndicator ('computed over N fetched events' with hasMore awareness), visible even at N=0, and that clicking 'Load more' widens the analysis live (panels re-derive over the accumulated window)."
    expected: "Per-panel WindowIndicator visible (incl. N=0); Load more re-derives all three panels over the wider window."
    why_human: "Per-panel window indicator rendering and live re-derivation on Load more are visual/interactive against the live lens."
---

# Phase 3: Remaining Spam Signals Verification Report

**Phase Goal:** An analyst sees the full forensic picture for an author — repeated/near-duplicate content, mass-mention and hashtag-stuffing patterns, and kind distribution — and can drop into the canonical bytes of any single event without bloating the list query.
**Verified:** 2026-06-25T00:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

Phase 3 splits cleanly into two layers, and the goal-backward verification follows that seam:

1. **The analyzer + transport core (statically verifiable)** — three pure analyzers, the lazy raw query, the additive `tags` selection, codegen. Every truth here is exercised by passing unit tests, grep gates, codegen idempotency, `tsc -b`, and `vite build`. **VERIFIED.**
2. **The user-visible panel layer (browser-only)** — whether an analyst *sees* the forensic picture rendered (panel layout, amber flag shapes, per-panel window indicators, lazy raw-inspector UX incl. the WR-01 Retry/Close fix). The code is implemented and build/test-verified, but the rendered behavior can only be confirmed by a human in a browser against the live lens. **Routed to human_needed — NOT gaps** (the routing context is explicit that frontend visual/interactive criteria are manual UAT, not failures, when the code is present and statically clean).

### Observable Truths

| #   | Truth (source) | Status | Evidence |
| --- | -------------- | ------ | -------- |
| 1 | `nearDup` two-stage exact-bucket → shingle-Jaccard ≥0.8 → union-find clusters (DRILL-02) | ✓ VERIFIED | `src/analysis/nearDup.ts:103-177`; behavioral tests pass incl. transitive union-find (`nearDup.test.ts:124`), exact bucketing (`:86`), near pair (`:110`), no-singleton (`:140`), `NEAR_DUP.jaccard`-driven cutoff (`:186`) |
| 2 | `analyzeTags` p/e/t aggregation, top-N, e-ref count, high-tag outliers, per-event + aggregate massMention/stuffing flags, malformed-row counting no-throw (DRILL-03) | ✓ VERIFIED | `src/analysis/tags.ts:57-122`; tests pass for massMention (`tags.test.ts:96`), stuffing (`:107`), high-tag outlier (`:83`), malformed-row counting (`:59`), never-throws (`:73`) |
| 3 | `analyzeKinds` histogram + out-of-range count, reusing `isSaneTs` + `Number.isSafeInteger(kind)&&kind>=0` (DRILL-04) | ✓ VERIFIED | `src/analysis/kinds.ts:44-64`; imports `isSaneTs` from `./rate` (`:23`); tests pass for forged kind (`kinds.test.ts:64`), forged createdAt (`:76`), null/non-number coercion to out-of-range (`:101,116,132`) |
| 4 | Each analyzer returns inconclusive empty results for degenerate (0/1) input without crash/NaN | ✓ VERIFIED | `nearDup.ts:107-109` (<2 guard); `tags`/`kinds` 0-event tests pass (`tags.test.ts:128`, `kinds.test.ts:89`, `nearDup.test.ts:174,179`) |
| 5 | Every analyzer result exposes no clean/ok/safe key (structural asymmetry) | ✓ VERIFIED | grep code-only count = 0 for each of nearDup/tags/kinds; mandatory asymmetry test present in all three suites (`*.test.ts`) |
| 6 | `EventsDocument` selects `tags` (not `raw`); `WindowEvent` gains `tags: string[][]`; codegen run so the field is typed (DRILL-03) | ✓ VERIFIED | `events.graphql.ts:26` selects `tags`, selection block (`:20-30`) has no `raw` field; `useAuthorWindow.ts:40` `tags: string[][]`; `npm run codegen` is idempotent (no `src/gql` drift); `tsc -b` green |
| 7 | `RawEventDocument` selects `raw` via the single-event ids path — the ONLY place raw is fetched (DRILL-04) | ✓ VERIFIED | `rawEvent.graphql.ts:16-23` selects `id raw` via `events(filter,limit)`; `raw` absent from `EventsDocument` selection (grep) |
| 8 | DuplicatePanel wraps the O(n²) `nearDup` call in `useMemo` keyed on a stable content signature (WR-02 fix) | ✓ VERIFIED | `DuplicatePanel.tsx:96-104` — memoized `sig` (id+content-length), `nearDup` memo keyed on `[sig]` not `[events]`; build green |
| 9 | All analyzer suites + existing suites pass; no regression | ✓ VERIFIED | `npx vitest run` → 83 passed (7 files): nearDup 25, tags 10, kinds 10, rate 13, errors 10, identifier 9, useStatsPoll 6 |
| 10 | Three stacked signal panels render below the timeline, each re-deriving live on Load more (DRILL-02/03/04) | ? human_needed | Code present + wired: `AuthorDrillDown.tsx:267-269` mounts all three after RatePanel; each panel re-derives per render. Visual rendering + live Load-more re-derivation need a browser. |
| 11 | Duplicate panel frames "X of N fetched", never bare "0 duplicates"; zero = neutral fact (DRILL-02) | ? human_needed | Code present: `DuplicatePanel.tsx:116-133` (N=0 fact, zero-cluster neutral fact, "X of N fetched" summary). Rendered framing/neutral-not-green needs browser confirmation. |
| 12 | Tags panel surfaces top-N + e-ref + amber high-tag / mass-mention / hashtag-stuffing labels (label+shape, no color-only) (DRILL-03) | ? human_needed | Code present: `TagsPanel.tsx:116-118` renders all three verbatim amber labels driven by analyzer flags. Amber rendering + label+shape need browser. |
| 13 | Kinds panel hand-rolled CSS bar histogram with NIP/kind labels + amber out-of-range note (DRILL-04) | ? human_needed | Code present: `KindsPanel.tsx:35,58,88` (analyzeKinds, KIND_NAMES labels, out-of-range note). Bar layout + neutral/amber rendering need browser. |
| — | Each panel co-locates a non-removable WindowIndicator even at N=0 (DRILL-05 carried forward) | ? human_needed | Code present: `<WindowIndicator>` in all three panels (`DuplicatePanel.tsx:114`, `TagsPanel`, `KindsPanel.tsx:46`). Visible-at-N=0 rendering needs browser. |
| — | Per-row "View raw" lazily fetches one event's raw bytes, renders escaped `<pre>` pretty/verbatim (DRILL-04) | ? human_needed | Code present + wired: `RawInspector.tsx` imperative classify-gated lazy fetch, escaped `<pre>`, Retry/Close (WR-01). Lazy fetch-on-click + escaping UX + Retry/Close click-through need browser. |

**Score:** 9/13 truths verified statically (0 present, behavior-unverified). The remaining 4 + the 3 carried-forward UI truths are routed to human UAT (browser-only visual/interactive rendering) — not gaps.

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/analysis/nearDup.ts` | Two-stage near-dup detector + helpers | ✓ VERIFIED | 178 lines; exports `nearDup`, `NearDupResult`, `normalizeContent`, `shingles`, `jaccard`; pure (no React/transport); wired by DuplicatePanel |
| `src/analysis/tags.ts` | p/e/t aggregator with signal flags | ✓ VERIFIED | 123 lines; exports `analyzeTags`, `TagsResult`; defensive (never throws); wired by TagsPanel |
| `src/analysis/kinds.ts` | Kind histogram + bounds flag | ✓ VERIFIED | 65 lines; exports `analyzeKinds`, `KindsResult`; reuses `isSaneTs`; wired by KindsPanel |
| `src/analysis/kindNames.ts` | NIP kind→name lookup | ✓ VERIFIED | exports `KIND_NAMES`; consumed by KindsPanel |
| `src/analysis/thresholds.ts` | NEAR_DUP + TAGS alongside BURST | ✓ VERIFIED | `NEAR_DUP` (`:25`) + `TAGS` (`:35`) additive; `BURST` (`:13`) unchanged |
| `src/queries/events.graphql.ts` | tags added, raw out | ✓ VERIFIED | `tags` selected (`:26`); no `raw` field in selection |
| `src/queries/rawEvent.graphql.ts` | RawEventDocument selecting raw via ids | ✓ VERIFIED | exports `RawEventDocument`; selects `id raw` |
| `src/hooks/useAuthorWindow.ts` | WindowEvent + `tags: string[][]` | ✓ VERIFIED | `:40` |
| `src/views/DuplicatePanel.tsx` | Near-dup cluster panel | ✓ VERIFIED | wired to nearDup, memoized, WindowIndicator |
| `src/views/TagsPanel.tsx` | Tag fan-out panel | ✓ VERIFIED | wired to analyzeTags, amber labels |
| `src/views/KindsPanel.tsx` | Kind histogram panel | ✓ VERIFIED | wired to analyzeKinds + KIND_NAMES |
| `src/views/RawInspector.tsx` | Lazy escaped raw inspector | ✓ VERIFIED | imperative classify-gated fetch; Retry/Close (WR-01) |
| `src/views/AuthorDrillDown.tsx` | Hosts three panels + View raw | ✓ VERIFIED | mounts all three (`:267-269`) + per-row RawInspector (`:173`) |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| DuplicatePanel.tsx | analysis/nearDup.ts | `nearDup(` in useMemo | ✓ WIRED (`:29,102`) |
| TagsPanel.tsx | analysis/tags.ts | `analyzeTags(` | ✓ WIRED (`:48`) |
| KindsPanel.tsx | analysis/kinds.ts | `analyzeKinds(` | ✓ WIRED (`:35`) |
| RawInspector.tsx | queries/rawEvent.graphql.ts | `client.query(RawEventDocument, …, network-only)` classify-gated | ✓ WIRED (`:22,42`); `classify` before data (`:53`); no `useQuery` (count 0) |
| AuthorDrillDown.tsx | DuplicatePanel/TagsPanel/KindsPanel | mounts three panels stacked after RatePanel | ✓ WIRED (`:267-269`) |
| RawInspector.tsx | AuthorDrillDown.errorTreatment | imports/maps ApiError→tone (WR-01) | ✓ WIRED (`:23,47,55`) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Full test suite passes (no regression) | `npx vitest run` | 83 passed (7 files) | ✓ PASS |
| Production build type-checks + bundles | `npm run build` (tsc -b && vite build) | 95 modules transformed, exit 0 | ✓ PASS |
| Codegen idempotent (field actually typed, no stale drift) | `npm run codegen` + `git status --porcelain src/gql` | empty (no drift) | ✓ PASS |
| nearDup transitive union-find invariant | named test `unions a transitive chain (A≈B, B≈C) into ONE cluster` | pass | ✓ PASS |
| tags massMention/stuffing flag transitions | named tests `:96` / `:107` | pass | ✓ PASS |
| kinds forged kind/createdAt → outOfRangeCount | named tests `:64` / `:76` / `:101` | pass | ✓ PASS |

### Probe Execution

No conventional `scripts/*/tests/probe-*.sh` probes; no PLAN/SUMMARY probe declarations. Frontend phase — verification is `vitest` + `tsc -b` + `vite build` (all run above). Step 7c: not applicable.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| DRILL-02 | 03-01, 03-02 | Near-duplicate / repeated text via client-side detection (exact-hash then shingle/Jaccard ≈0.8) | ✓ SATISFIED (code+tests); ? rendering needs UAT | nearDup.ts + tests; DuplicatePanel wired |
| DRILL-03 | 03-01, 03-02 | Tag/mention aggregation (p/e/t) surfaces mass-mention + hashtag-stuffing | ✓ SATISFIED (code+tests); ? rendering needs UAT | tags.ts + tests; TagsPanel amber labels |
| DRILL-04 | 03-01, 03-02 | Kind-distribution breakdown + lazy raw-JSON inspector | ✓ SATISFIED (code+tests); ? rendering + lazy UX need UAT | kinds.ts + tests; KindsPanel + RawInspector lazy classify-gated fetch |

No orphaned requirements: REQUIREMENTS.md maps exactly DRILL-02/03/04 to Phase 3, all three claimed by both plans.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| ---- | ---- | ------- | -------- | ------ |
| (all phase-03 source) | — | TBD/FIXME/XXX | — | None found |
| (all phase-03 source) | — | TODO/HACK/PLACEHOLDER | — | None found |
| (all panel/inspector CSS) | — | teal/green/accent/clean token | — | None found (amber-only discipline holds) |
| (all src) | — | dangerouslySetInnerHTML | — | None found (XSS sink absent — T-03-05 mitigated) |

All four code-review findings (WR-01 dead-end error state → Retry/Close; WR-02 memo key → content signature; WR-03 clipboard rejection → silent catch; WR-04 hostile-input parity → coerce in nearDup/kinds) are present in the codebase and confirmed by +8 regression tests (75→83).

### Human Verification Required

See the `human_verification` frontmatter list — 5 browser-only checks covering: (1) Duplicate panel rendering + denominator framing + amber badges; (2) Tags panel amber flag rendering (label+shape, no color-only); (3) Kinds histogram + neutral bars + amber out-of-range note; (4) lazy View-raw fetch + escaped `<pre>` + the WR-01 Retry/Close click-through; (5) per-panel non-removable WindowIndicator at N=0 + live Load-more re-derivation. These are the visual/interactive success criteria the routing context flags as manual UAT.

### Gaps Summary

No gaps. Every statically verifiable must-have passes: all three pure analyzers are substantive, behaviorally tested (83/83), asymmetric (no clean field), pure (no React/transport import), and wired into their panels; the lazy raw query is the sole `raw` selection point; `tags` rides the window query with idempotent codegen; `tsc -b` and `vite build` are clean; no XSS sink, no accent/green in panel CSS, zero new dependencies. All four code-review warnings are fixed in code with regression coverage.

The four browser-only success criteria (rendered panels, amber flag shapes, lazy raw-inspector UX with Retry/Close, per-panel window indicators + live Load-more) are implemented and build/test-verified but require a human in a browser against the live lens to confirm the rendered/interactive behavior — classified `human_needed` (manual UAT) per the phase routing, not gaps.

---

_Verified: 2026-06-25T00:00:00Z_
_Verifier: Claude (gsd-verifier)_
