---
phase: 02-suspect-entry-drill-down-core
verified: 2026-06-24T23:10:00Z
status: passed
score: 9/9 must-have truth-clusters statically verified (0 failed); 8 live-UAT behaviors confirmed by human walkthrough 2026-06-25 (see 02-UAT.md, all pass)
behavior_unverified: 0
human_verification_result: passed 2026-06-25 — all 8 items pass (live walkthrough against lens at VITE_GRAPHQL_URL)
overrides_applied: 0
human_verification:
  - test: "Paste a known author's npub (or 64-char hex) in the shell entry bar → click 'Inspect author' (npm run dev, against live lens at VITE_GRAPHQL_URL)"
    expected: "URL becomes #/a/<64 lowercase hex>; drill-down opens; identity header shows BOTH npub and 64-char hex (mono, labeled); newest-first timeline appears (ID-01/ID-02)"
    why_human: "End-to-end routing + live query + render is a runtime state transition across modules; grep proves the wiring is present but cannot exercise the actual navigation + fetch + render against the live corpus"
  - test: "On the open drill-down, observe the window indicator on the timeline surface"
    expected: "Reads 'Computed over N fetched events · …'; when more pages exist shows the amber 'more available — partial window' segment (DRILL-05)"
    why_human: "Partial-vs-full window text depends on live hasMore from the lens; the N=0 / full / partial branches are present in code but the live branch taken is runtime-data-dependent"
  - test: "Click 'Load more' once, then double-click / hold rapidly; continue until exhausted"
    expected: "Exactly one page appends per click, N increases, indicator re-derives live; rapid double-click does NOT duplicate rows (inFlight race guard); when exhausted the button is replaced by 'End of available events — this is the full window.' (DRILL-06)"
    why_human: "Single-page-append + double-click race guard + exhaustion transition are runtime state/ordering invariants; the guard (inFlight.current / hasMoreRef.current) is present and wired but no automated test exercises the React/network append path"
  - test: "Paste garbage (e.g. 'npub1zzz' or random text) in the entry bar and submit"
    expected: "Inline amber error 'Not a valid npub / note / nprofile or 64-char hex.' appears AND the app STAYS on the dashboard — no navigation, no empty timeline (ID-03 parse-failure branch)"
    why_human: "The two-state ID-03 distinction (parse failure vs valid-but-zero) is a runtime navigation invariant; parseIdentifier rejection is unit-proven but the stay-home-vs-navigate UI behavior needs a live browser"
  - test: "Paste / hash a syntactically valid 64-hex pubkey expected to have zero events in the corpus"
    expected: "Drill-down opens to the neutral calm 'No events for this author' empty state WITH the window indicator present (N=0); visually distinct from the parse error (not amber, not red) (ID-03 zero-match branch)"
    why_human: "The valid-but-zero-match path renders only when the live lens returns an empty page for a real pubkey; the branch is present in code but the empty-state transition is runtime-data-dependent"
  - test: "Inspect an author with many events; observe the 'Posting rate' panel beside/below the timeline"
    expected: "Hand-rolled CSS bars render; a tight burst is marked amber AND labeled 'burst'; a quiet author stays neutral and is NOT presented as 'clean'; NO green/teal anywhere in the panel; the forgeable caveat is always shown and non-dismissible; the rate-surface window indicator re-derives on Load more (DRILL-01/DRILL-05)"
    why_human: "analyzeRate asymmetry is unit-proven, but the live rendered bars, amber-burst marker, color-reservation, and live re-derivation on widening are visual/runtime properties needing a browser against real data"
  - test: "Confirm the only teal accent on screen is the 'Inspect author' submit"
    expected: "Load more, timeline rows, Back, Copy, the window indicator, and the rate panel are all neutral chrome — no accent leak (UI-SPEC accent reservation #3)"
    why_human: "Accent reservation is a visual property across rendered surfaces; CSS-module grep shows accent only in SuspectEntryBar.module.css, but the on-screen composite needs a human eye"
  - test: "Verify the WR-01 bounded INVALID_CURSOR recovery (code-review fix)"
    expected: "If the lens rejects even a null cursor / loops on INVALID_CURSOR, the UI surfaces a recoverable error ('Pagination expired — reloading from the top.') after exactly one cursor-drop retry — it does NOT spin forever on a permanent ConnectingShell"
    why_human: "The cursorRetry-bounded recovery is a state-handling change in a React/network-coupled hook with no node-env unit test (per 02-REVIEW-FIX.md); it was verified via tsc + full suite but the actual recovery transition needs a live/forced-error reproduction"
behavior_unverified_items:
  - truth: "Pasting a valid npub/hex routes to #/a/<hex> and opens the drill-down (ID-01)"
    test: "Paste a known npub/hex and click Inspect author against the live lens"
    expected: "Navigates to #/a/<lowercase-hex> and renders the drill-down with a live timeline"
    why_human: "Cross-module routing + live fetch + render transition; presence/wiring verified, runtime behavior not exercised by any test"
  - truth: "Load more fetches exactly ONE next page per click, appending and re-deriving windowMeta; double-click cannot double-append (DRILL-06)"
    test: "Click Load more once, then rapid double-click; observe append count and row uniqueness"
    expected: "One page per click; no duplicate rows; indicator re-derives; end caption on exhaustion"
    why_human: "Append/race/exhaustion is a runtime ordering+state invariant in a React/network hook with no automated coverage of the append path"
  - truth: "A parse failure stays on the dashboard with an inline amber note and never navigates (ID-03)"
    test: "Submit garbage in the entry bar"
    expected: "Inline amber note shown; URL unchanged; no empty timeline"
    why_human: "Stay-home-vs-navigate is a runtime UI state transition; parse rejection is unit-proven but the navigation suppression is not"
  - truth: "A valid pubkey with zero events shows the neutral 'No events for this author' empty state WITH the N=0 indicator (ID-03)"
    test: "Drill into a valid-but-unused 64-hex"
    expected: "Neutral calm empty state + WindowIndicator (N=0); distinct from the parse error"
    why_human: "Empty-state branch renders only on a live empty page; runtime-data-dependent"
  - truth: "The window indicator's partial-window (amber 'more available') vs full-window text reflects live hasMore (DRILL-05)"
    test: "Open an author with more pages; observe the indicator"
    expected: "Amber 'more available — partial window' when hasMore; 'full window' when exhausted"
    why_human: "Branch taken depends on live lens hasMore; all branches present in code"
  - truth: "A detected burst is marked amber AND labeled 'burst'; a quiet author is neutral, never 'clean' (DRILL-01)"
    test: "Inspect a bursty author and a quiet author"
    expected: "Bursty → amber tint + 'burst' label; quiet → neutral, no clean/green"
    why_human: "analyzeRate asymmetry is unit-proven; the rendered amber marker + color reservation are visual runtime properties"
  - truth: "Accent (teal) appears ONLY on the 'Inspect author' submit across all rendered surfaces"
    test: "Visually scan the dashboard and drill-down"
    expected: "No accent on Load more / rows / back / copy / indicator / rate panel"
    why_human: "Composite visual property; CSS-module grep confirms accent scoped to the entry bar, but on-screen verification needs a human"
  - truth: "WR-01 bounded INVALID_CURSOR recovery surfaces an error instead of spinning forever"
    test: "Force a repeated INVALID_CURSOR (looping lens / null-cursor rejection)"
    expected: "Recoverable error surfaced after one cursor-drop retry; no permanent spinner"
    why_human: "State-handling change in a React/network hook with no node-env unit test; recovery transition needs live/forced reproduction"
---

# Phase 2: Suspect Entry + Drill-Down Core Verification Report

**Phase Goal:** An analyst can paste a single suspect (npub or hex), land on that author's drill-down, and form a defensible first judgment from a newest-first timeline with an asymmetric burst signal — always reading their conclusion against an honest, non-removable window-size denominator.

**Verified:** 2026-06-24T23:10:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

The phase goal is **achieved in code**: every artifact exists, is substantive, is wired, and real data flows through the wiring. The pure-logic foundations (identifier normalization, asymmetric burst analysis, window-meta derivation, hash routing) are unit-proven (38/38 tests, tsc clean). All four code-review WARNING fixes (WR-01..WR-04) are present in the source.

The phase goal is **not yet `passed`** only because it asserts runtime behaviors — routing transitions, live pagination/append, the ID-03 two-state distinction, the rendered asymmetric burst marker, and the accent-reservation visual — that grep/file checks cannot exercise. Those live `npm run dev` walkthroughs were **deliberately deferred** to a single phase-end UAT (user decision, 2026-06-24; recorded as `accepted-deferred-uat` in both 02-02 and 02-03 SUMMARYs). Per that decision these are classified `human_needed`, not gaps — the code is present, wired, and statically verifiable; it simply needs a human in a browser against the live lens to confirm the runtime behavior.

### Observable Truths

| #   | Truth | Status | Evidence |
| --- | ----- | ------ | -------- |
| 1   | parseIdentifier accepts hex/npub/nprofile (normalized lowercase hex + canonical npub), rejects nsec (REJECTED_NSEC, no secret leak), note + garbage (NOT_RECOGNIZED), empty (EMPTY); uppercase hex normalized (ID-01/02/03 pure logic) | ✓ VERIFIED | `src/identifier/identifier.ts:43-79` — per-type switch; nsec→REJECTED_NSEC (no hex), nprofile reads `.data.pubkey` (l.66), catch arm = parse failure; 9/9 tests pass |
| 2   | Pasting a valid npub/hex routes to #/a/<lowercase-hex> and opens the drill-down (ID-01) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | SuspectEntryBar `window.location.hash = '#/a/' + r.hex` (l.39) → hashRouter `^#\/a\/([0-9a-f]{64})$` (l.21) → App switch (l.56) → AuthorDrillDown. All wired; runtime transition deferred to UAT |
| 3   | The drill-down identity header shows BOTH forms — npub (mono) and 64-char hex (mono), each labeled (ID-02) | ✓ VERIFIED | `AuthorDrillDown.tsx:45-79` IdentityHeader renders `npub` + `hex` idRows, each labeled + mono via CSS module, escaped JSX |
| 4   | A parse failure shows an inline amber note and STAYS on the dashboard — never navigates, never empty timeline (ID-03) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `SuspectEntryBar.tsx:44` sets `parseError` and returns without navigating; verbatim string l.70. Stay-home transition deferred to UAT |
| 5   | A valid pubkey with zero events shows the neutral 'No events for this author' empty state WITH the N=0 window indicator (ID-03) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `AuthorDrillDown.tsx:186,197-208` isZeroMatch branch renders WindowIndicator + neutral empty caption. Branch present; live empty render deferred to UAT |
| 6   | Timeline lists events newest-first across kinds; each row shows kind, createdAt (human UTC + raw epoch), single-line escaped content preview | ✓ VERIFIED (structure) / ⚠️ live order via UAT | `AuthorDrillDown.tsx:147-157,221-223` TimelineRow renders kind/utc+epoch/content, server order never re-sorted; EventsDocument fixed createdAt-DESC ordering |
| 7   | The window-size indicator renders on the timeline surface, shows 'Computed over N … · full\|partial · range', renders even at N=0 (DRILL-05) | ✓ VERIFIED (all branches present) / ⚠️ live branch via UAT | `WindowIndicator.tsx:26-61` — N=0 / full / partial branches, all three verbatim strings; mounted timeline + zero-match + rate-panel surfaces (3× in drill-down, 1× in RatePanel) |
| 8   | Load more fetches exactly ONE next page per click, appends, re-derives windowMeta live; hasMore=false → 'End of available events' caption (DRILL-06) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `useAuthorWindow.ts:225-228` loadMore single-page, gated on `inFlight.current`/`hasMoreRef.current`; `AuthorDrillDown.tsx:227-238` button↔caption swap. Append/race/exhaustion is a runtime invariant — deferred to UAT |
| 9   | analyzeRate returns burstDetected:true on a tight cluster, false (inconclusive, never 'clean') on quiet; bounds-checks createdAt via isSaneTs into rejectedCount; <2 sane → no crash (DRILL-01) | ✓ VERIFIED | `src/analysis/rate.ts` — sliding-window burst (l.108-117), isSaneTs bounds [0, 4_102_444_800] (l.33-35), rejectedCount (l.87), <2 guard (l.91-99), no clean/safe field; 13/13 tests pass |
| 10  | RatePanel renders hand-rolled CSS bars, amber+labeled burst marker, persistent forgeable caveat, co-located WindowIndicator (DRILL-01/DRILL-05) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | `RatePanel.tsx:34-92` — bars scaled to max bin, burstBadge amber+"burst" label, verbatim caveat (l.87-90), WindowIndicator co-located (l.48). Rendered visual/asymmetry deferred to UAT |

**Score:** 9/9 must-have truth-clusters statically verified (0 FAILED); 8 behavior items present + wired but deferred to live UAT.

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/identifier/identifier.ts` | parseIdentifier + ParseResult + isHexPubkey | ✓ VERIFIED | 79 lines; pure (nip19-only import); wired into SuspectEntryBar + AuthorDrillDown |
| `src/identifier/identifier.test.ts` | nip19 branches + EMPTY/NSEC/NOT_RECOGNIZED + case norm | ✓ VERIFIED | 9 tests pass; describe block present |
| `src/queries/events.graphql.ts` | EventsDocument selecting id/pubkey/kind/createdAt/content | ✓ VERIFIED | raw/sig/tags absent (grep=0); endCursor/hasMore selected; codegen-typed |
| `src/router/hashRouter.ts` | useHashRoute + Route + lowercase-64hex matcher | ✓ VERIFIED | `/^#\/a\/([0-9a-f]{64})$/`; hashchange add/remove cleanup |
| `src/hooks/useAuthorWindow.ts` | window hook + deriveWindowMeta + types | ✓ VERIFIED | THREW guard, classify-before-data, INVALID_CURSOR bounded restart (WR-01), network-only (WR-03), hasMoreRef (WR-02), runId stale-write guard |
| `src/views/WindowIndicator.tsx` | non-removable DRILL-05 denominator (renders at N=0) | ✓ VERIFIED | no dismiss prop/hidden branch; 3 verbatim strings; amber partial segment |
| `src/views/SuspectEntryBar.tsx` | paste bar — parseIdentifier on submit | ✓ VERIFIED | navigate on ok / inline note on fail; accent on submit only |
| `src/views/AuthorDrillDown.tsx` | identity header + timeline + indicator + zero-match + Load more | ✓ VERIFIED | all branches present; RatePanel mounted; no accent; no raw HTML |
| `src/analysis/thresholds.ts` | BURST tunable constants | ✓ VERIFIED | windowSec/minEvents/binSec; single tunable home |
| `src/analysis/rate.ts` | analyzeRate + isSaneTs + RateResult | ✓ VERIFIED | asymmetric (no clean field), bounds-checked, WR-04 integer-jump binning |
| `src/analysis/rate.test.ts` | burst/quiet/<2/forged/negative-interval coverage | ✓ VERIFIED | 13 tests pass (incl. 2 WR-04 regression) |
| `src/views/RatePanel.tsx` | bars + amber burst + caveat + co-located indicator | ✓ VERIFIED | verbatim caveat; "burst" label; no accent/green; analyzeRate driven |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| SuspectEntryBar.tsx | identifier.ts | parseIdentifier on submit → hash navigate / inline note | ✓ WIRED (l.23,35,39) |
| useAuthorWindow.ts | events.graphql.ts | client.query(EventsDocument, {filter:{authors:[hex]}, after, limit:100}) | ✓ WIRED (l.23,128-133) |
| useAuthorWindow.ts | transport/errors.ts | classify(result) before data; INVALID_CURSOR → reset+restart (bounded) | ✓ WIRED (l.22,151,155-176) |
| App.tsx | hashRouter.ts | useHashRoute() switch home/author/notfound | ✓ WIRED (l.3,33,55-57) |
| AuthorDrillDown.tsx | WindowIndicator.tsx | <WindowIndicator meta={windowMeta}/> on timeline + zero-match, always | ✓ WIRED (3× l.200,212 + RatePanel) |
| rate.ts | thresholds.ts | import { BURST } — windowSec/minEvents/binSec | ✓ WIRED (l.20,112,123) |
| RatePanel.tsx | rate.ts | analyzeRate(events.map(e=>e.createdAt)) | ✓ WIRED (l.20,31) |
| AuthorDrillDown.tsx | RatePanel.tsx | <RatePanel events windowMeta/> in loaded branch | ✓ WIRED (l.32,243) |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
| -------- | ------------- | ------ | ------------------ | ------ |
| AuthorDrillDown timeline | events | useAuthorWindow → client.query(EventsDocument) live lens, authors:[hex] | Yes (network-only, real `events` query) | ✓ FLOWING |
| WindowIndicator | meta (count/range/hasMore) | deriveWindowMeta(events, hasMore) from live page set | Yes (derived from fetched events) | ✓ FLOWING |
| RatePanel bars/burst | rate | analyzeRate(events.map createdAt) from live events | Yes (re-derives per render as window widens) | ✓ FLOWING |
| Identity header | npub/hex | route hex (router-matched) → parseIdentifier(hex).npub | Yes (real nip19 derivation) | ✓ FLOWING |

No hollow props, no static-empty returns, no disconnected wiring found.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| ID-01 | 02-01, 02-02 | Paste npub/64-hex → open author drill-down | ✓ SATISFIED (code) / live UAT pending | parseIdentifier accept arms unit-proven; entry→route→drill-down wired |
| ID-02 | 02-01, 02-02 | Normalize npub/note bech32 ↔ hex, query lowercase hex, display both | ✓ SATISFIED (code) / live UAT pending | nip19 normalization unit-proven; identity header shows both forms |
| ID-03 | 02-01, 02-02 | Distinguish parse failure from valid-but-zero-match | ✓ SATISFIED (code) / live UAT pending | parse failure = explicit fail arm (unit); stay-home vs zero-match-empty-state branches present |
| DRILL-01 | 02-03 | Timeline newest-first + asymmetric burst (burst suspicious, absence ≠ clean) | ✓ SATISFIED (code) / live UAT pending | analyzeRate asymmetry unit-proven; RatePanel amber+label; persistent forgeable caveat |
| DRILL-05 | 02-02, 02-03 | Non-removable window-size honesty indicator, hasMore-aware | ✓ SATISFIED (code) / live UAT pending | WindowIndicator no-dismiss, renders at N=0, on all 3 signal surfaces |
| DRILL-06 | 02-02 | Load more (cursor pagination, constant filter) to widen window | ✓ SATISFIED (code) / live UAT pending | single-page loadMore, constant filter, opaque-cursor restart; append behavior deferred to UAT |

All 6 declared phase requirement IDs (ID-01, ID-02, ID-03, DRILL-01, DRILL-05, DRILL-06) are accounted for and map cleanly to REQUIREMENTS.md (which already marks all 6 Complete for Phase 2). No orphaned requirements: REQUIREMENTS.md maps exactly these 6 to Phase 2, and every plan's `requirements:` frontmatter is a subset of them.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Full unit suite | `npm test` (vitest run) | 4 files, 38 passed (38) | ✓ PASS |
| Type build | `npx tsc -b` | exit 0, no errors | ✓ PASS |
| nostr-tools exact pin | node package.json check | `2.23.8`, exact-pin true | ✓ PASS |
| Live UI walkthrough | `npm run dev` browser walkthrough | Not run (no server start; deferred per user decision) | ? SKIP → human_needed |

### Probe Execution

Not applicable — Phase 2 is a frontend feature phase with no `scripts/*/tests/probe-*.sh` declared in any PLAN/SUMMARY. Skipped.

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
| ---- | ------- | -------- | ------ |
| (all phase-modified src) | TBD/FIXME/XXX | — | None found |
| (all phase-modified src) | TODO/HACK/PLACEHOLDER | — | None found |
| (all phase-modified src) | dangerouslySetInnerHTML / innerHTML / eval | — | None found (XSS sink-free) |
| src/analysis/rate.ts | clean/safe verdict field | — | 0 in non-comment lines (asymmetry structural) |
| src/queries/events.graphql.ts | raw/sig/tags | — | 0 (payload trimmed per contract) |

No blocker or warning anti-patterns. The only `accent` matches outside SuspectEntryBar.module.css are: StatsDashboard.module.css (Phase 1, out of scope) and a comment in App.module.css explicitly noting the entry-bar owns the sole accent — neither is an accent-color leak into a Phase-2 drill-down surface.

### Human Verification Required

8 items (see frontmatter `human_verification`) — the deferred live `npm run dev` UAT walkthroughs from 02-02 Task 4 and 02-03 Task 4, plus the WR-01 bounded-recovery manual check. All are runtime/visual behaviors over code that is present, wired, and statically verified. None is a gap: each has a concrete code artifact behind it that passed structural verification. Run the walkthrough scripts in 02-02-PLAN.md Task 4 and 02-03-PLAN.md Task 4 against the live lens, plus a forced-INVALID_CURSOR reproduction for WR-01.

### Gaps Summary

**No gaps.** Zero must-haves FAILED. Zero artifacts missing or stub. Zero key links unwired. All prohibitions hold (no XSS sink, no accent leak into drill-down surfaces, no clean/safe verdict field, opaque cursor passed verbatim with bounded INVALID_CURSOR restart, explicit limit 100, events doc trimmed to 5 fields, window indicator non-removable on every signal surface). The four code-review WARNINGs (WR-01..WR-04) are all fixed in source and confirmed by tsc + the 38-test suite. The phase goal is fully realized in code; what remains is the deliberately-deferred live UAT to confirm runtime/visual behavior, which routes this verification to `human_needed` rather than `passed`.

---

_Verified: 2026-06-24T23:10:00Z_
_Verifier: Claude (gsd-verifier)_
