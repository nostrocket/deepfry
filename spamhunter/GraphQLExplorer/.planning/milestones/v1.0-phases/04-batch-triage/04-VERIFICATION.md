---
phase: 04-batch-triage
verified: 2026-06-25T13:20:00Z
status: passed
score: 7/7 must-haves statically verified (5 ROADMAP SC + load-bearing invariants); 5 require live-UI confirmation
behavior_unverified: 5
overrides_applied: 0
re_verification:
  previous_status: none
  previous_score: n/a
  note: "Initial verification. 04-REVIEW.md found 1 critical + 6 warnings; all fix commits (CR-01, WR-01..WR-06) confirmed present in source."
behavior_unverified_items:
  - truth: "Chunk loop fills the triage table progressively, retains partial work on a mid-split (413 halve) failure, and never silently truncates"
    test: "npm run dev; #/batch; Triage a large set against the live lens; if reproducible force a 413 (or a chunk error) and confirm partial rows are retained, the table keeps filling, and a Retry control re-issues only the still-missing authors (WR-01 mid-split partial-retention path)"
    expected: "Partial rows stay on screen; 'Triaged N of M authors — partial batch' shows the honest numerator; Retry re-fetches only uncovered authors; no server internals shown on a hard chunk error"
    why_human: "The async chunk loop + 413 mid-split partial retention (WR-01) and runId-gated retry race (WR-02) are driven by the live urql client; the pure decision helper chunkDegradeDecision is unit-tested but the loop integration is not exercised by a behavioral test"
  - truth: "Enumerate corpus pages the opaque cursor until hasMore is false with a live running count and a working Stop control"
    test: "Click 'Enumerate corpus authors'; watch the running count climb; press Stop mid-run; let a second run reach completion"
    expected: "Count climbs per page; Stop yields 'Snapshot — stopped early at N authors (incomplete)' (amber); completion yields 'N distinct authors as of this fetch'"
    why_human: "The enumeration loop runs against the live authors() endpoint; isStuckPage + maxEnumPages are unit-tested but the live paging/Stop is runtime-only"
  - truth: "The sortable triage table renders per-signal columns, default event-count descending, amber-on-signal chips, neutral dash on absence, explicit '0 events' rows"
    test: "Triage a mixed set; confirm columns Author/Events/Burst/Near-dup/Tag fan-out; default sort = event count descending; click headers to re-sort (no network refetch); confirm amber chips on tripped signals, '—' on absence, and an explicit '0 events' row for a zero-match author"
    expected: "Sortable hand-rolled table; tripped signals are amber chips with verbatim labels; no green/teal; no clean/score column; zero-match rows show '0 events'"
    why_human: "Visual rendering + interactive sort behavior cannot be confirmed by grep/test; requires the running app"
  - truth: "Clicking a triage row drills into the existing #/a/<hex> author view"
    test: "Click any triage row"
    expected: "URL becomes #/a/<64hex> and the full Phase 2/3 AuthorDrillDown renders for that author"
    why_human: "Hash navigation + route resolution + drill-down render is a runtime flow"
  - truth: "The teal accent appears ONLY on the 'Triage' submit; nav, enumerate, Stop, file picker, textarea, sort headers, rows are neutral chrome"
    test: "Visually scan the #/batch view and the shell nav"
    expected: "Only the 'Triage' submit is teal; 'Batch triage' nav link and every other control is neutral"
    why_human: "Accent confinement is statically confirmed in CSS (var(--accent) only inside .triageSubmit), but final visual confirmation across the rendered app is a human check (UI-SPEC pillar)"
human_verification:
  - test: "Full batch-triage slice walkthrough (04-02 Task 4, deferred): npm run dev → neutral 'Batch triage' nav → #/batch → paste mixed npub/hex/dupe/note/garbage → confirm 'N valid · M duplicates removed · K unparseable' with unparseable tokens listed (escaped) → upload .txt + oversized file → enumerate + Stop → Triage → sortable table → row drill-in"
    expected: "Every step behaves per 04-02 PLAN Task 4 how-to-verify; accent confined to Triage; no green/clean; escaped tokens; honesty denominators present"
    why_human: "checkpoint:human-verify deferred to phase-end per the 2026-06-25 user decision (recorded in 04-02-SUMMARY as accepted-deferred-uat); requires the running app against the live lens"
  - test: "WR-01 mid-split partial-retention: force or reproduce a 413 that halves, with one sub-half succeeding and a sibling failing"
    expected: "The successful sub-half's authors stay covered (counted in 'Triaged N of M'); Retry re-issues only the uncovered authors; no re-413 of already-resolved work"
    why_human: "The 413 mid-split path is the WR-01 fix benefiting from a live browser check; not exercised by an automated behavioral test"
---

# Phase 4: Batch Triage Verification Report

**Phase Goal:** An analyst can paste or upload a list of suspects and triage them together in one sortable table, then drill into any author — without silently truncating, misattributing, or overloading the backend.
**Verified:** 2026-06-25T13:20:00Z
**Status:** human_needed
**Re-verification:** No — initial verification (post code-review-fix)
**Mode:** mvp (goal is an outcome statement, not the strict User Story regex; verified against the 5 ROADMAP success criteria per routing guidance)

## Automated Gate Results

- `npx vitest run` — **128/128 tests pass** (14 files). Includes the 4 new pure-module suites (chunk 11, mergeByAuthor 4, triage 6, batchImport 7), the extended hashRouter suite (6), and 2 new hook decision-helper suites (useLatestPerAuthor chunkDegradeDecision 5, useAuthorEnumeration isStuckPage/maxEnumPages 6).
- `npm run build` (`tsc -b && vite build`) — **green** (106 modules, bundle emitted, no type errors).

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria + load-bearing invariants)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | SC1: Import by paste OR file; mixed npub/hex normalized (reuse Phase-2 identifier), deduped, counted | ✓ VERIFIED | `parseBatchInput` (batchImport.ts) splits on `/[\s,]+/`, routes every token through `parseIdentifier` (single nip19 site), dedupes via Set, counts valid/duplicate/unparseable. View imports the tested module (CR-01 fix). batchImport.test.ts (7) pins mixed npub+hex+dupe+note+garbage. BatchImport.tsx paste + FileReader file source both call it. |
| 2 | SC2: Chunked to respect ≤1000-author cap AND 256 KiB body, chunking on whichever binds first, small perAuthor (3–10) | ✓ VERIFIED | chunk.ts `chunkSize() = min(500, 1000, byteBudgetAuthors()≈3225)` = 500 (author cap binds first, documented); `TRIAGE.perAuthor=5`. WR-06: byte-budget pinned by chunk.test.ts. Runtime 413/TOO_MANY_AUTHORS degrade via `chunkDegradeDecision` (shrink/terminal), unit-tested. |
| 3 | SC3: Triage table matched by author key (never index-zip); zero-match authors shown as "0 events" | ✓ VERIFIED | mergeByAuthor.ts left-joins via `Map` keyed by `author`, iterates full input set → missing author = `events:[]`. mergeByAuthor.test.ts pins the case where response order differs AND omits authors (the anti-index-zip pin). TriageTable renders explicit "0 events" cell when `eventCount === 0`. |
| 4 | SC4: Clicking a triage row opens the existing Phase 2/3 drill-down | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | TriageTable `drillIn(author)` sets `window.location.hash = '#/a/' + author`; hashRouter resolves `#/a/<64hex>` → `{name:'author'}`; App renders `<AuthorDrillDown>`. Wiring present; runtime nav flow not exercised by a test → human. |
| 5 | SC5: Enumerate distinct authors via paginated authors() (opaque cursor, loop until !hasMore), feed same pipeline, live-snapshot count | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | useAuthorEnumeration pages `endCursor` verbatim, Stop control, runningCount per page, bounded INVALID_CURSOR restart, WR-03 stuck-page + page ceiling. authors merge into the same `collected` set fed to TriageTable. Pure helpers unit-tested; live loop is runtime-only → human. |
| 6 | INV: No silent truncation — partial chunk results retained, never kills the batch (WR-01/WR-02) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | useLatestPerAuthor retains left-half groups on mid-split sibling failure, marks only resolved authors covered, runId-gates retry writes. Decision logic unit-tested; async loop + mid-split path is runtime-only → human (WR-01 browser check flagged in routing). |
| 7 | INV: Honesty posture — no clean/score column, no green/teal, accent only on Triage, escaped plaintext | ✓ VERIFIED (static) / ⚠️ visual confirm pending | triage.ts has no clean/ok/safe/score field; TriageTable has no verdict column; `var(--accent)` appears only inside `.triageSubmit`; nav uses neutral `.navLink`; `dangerouslySetInnerHTML=` attribute count = 0 in both views; tokens rendered via JSX interpolation. Final visual scan → human. |

**Score:** 7/7 statically verified at the code level; 5 carry runtime/visual confirmation deferred to human UAT.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/analysis/chunk.ts` | dual-axis sizing + slicer | ✓ VERIFIED | chunkAuthors, chunkSize, byteBudgetAuthors, SAFE_BYTES_PER_AUTHOR, BODY_LIMIT_BYTES exported; pure; tested (11). |
| `src/analysis/mergeByAuthor.ts` | left-join keyed by author | ✓ VERIFIED | Map-by-author left join; reuses WindowEvent; tested (4) incl. anti-index-zip pin. |
| `src/analysis/triage.ts` | 4-analyzer fan-in, no verdict | ✓ VERIFIED | analyzeRate/nearDup/analyzeTags → {eventCount,burst,nearDup,tagFanOut}; no clean/score; tested (6). |
| `src/analysis/batchImport.ts` | tokenize/dedupe/count | ✓ VERIFIED | single parseIdentifier site; unparseable preserved; tested (7). |
| `src/queries/latestPerAuthor.graphql.ts` | 6 fields, raw EXCLUDED | ✓ VERIFIED | id pubkey kind createdAt content tags; `grep -c raw` = 0. |
| `src/queries/authors.graphql.ts` | after/limit → authors endCursor hasMore | ✓ VERIFIED | opaque cursor doc. |
| `src/hooks/useLatestPerAuthor.ts` | chunk loop, 413 degrade, partial-on-error, runId | ✓ VERIFIED (wired) | classify-before-data, network-only, WR-01/02/04/05 fixes present; decision helper tested. |
| `src/hooks/useAuthorEnumeration.ts` | enumeration loop, Stop, opaque cursor, bounded restart | ✓ VERIFIED (wired) | WR-03 stuck-page + ceiling; isStuckPage tested. |
| `src/router/hashRouter.ts` | + {name:'batch'} + #/batch matcher | ✓ VERIFIED | union variant + exact matcher; home/author/notfound intact; tested. |
| `src/views/BatchImport.tsx` | paste/file/enumerate + summary + Triage | ✓ VERIFIED | imports tested module (CR-01); FileReader; escaped unparseable list; single accent submit. |
| `src/views/TriageTable.tsx` | sortable table + chips + 0-events + drill-in + indicators | ✓ VERIFIED | hand-rolled <table>, local .sort(), amber chips, "0 events", "Triaged N of M", per-chunk Retry, #/a/ drill-in. |
| `src/views/BatchTriage.module.css` | token-only, accent on submit only | ✓ VERIFIED | var(--accent) only in .triageSubmit; no green/teal elsewhere. |

### Key Link Verification

| From | To | Via | Status |
|------|----|----|--------|
| App.tsx | BatchImport.tsx | `route.name === 'batch'` outlet + `#/batch` nav | ✓ WIRED |
| BatchImport.tsx | analysis/batchImport.ts | `parseBatchInput(text)` (single decode site, CR-01) | ✓ WIRED |
| BatchImport.tsx | useAuthorEnumeration.ts | enumerate corpus source | ✓ WIRED |
| TriageTable.tsx | useLatestPerAuthor.ts | rows + triagedCount/totalCount/chunkErrors/retryChunk | ✓ WIRED |
| TriageTable.tsx | analysis/triage.ts | `triageAuthor(row.events)` per row | ✓ WIRED |
| TriageTable.tsx | hashRouter (#/a/) | `window.location.hash = '#/a/' + row.author` | ✓ WIRED |
| useLatestPerAuthor.ts | chunk.ts + mergeByAuthor.ts | `chunkAuthors(_, chunkSize())` + incremental `mergeByAuthor` | ✓ WIRED |
| useLatestPerAuthor.ts | transport/errors.ts | `classify(result)` before reading data | ✓ WIRED |

### Code-Review Fix Verification (04-REVIEW.md → fix commits)

| Finding | Severity | Fix commit | Status in source |
|---------|----------|-----------|------------------|
| CR-01 duplicate tokenizer (tested module dead, shipped one untested) | Critical | 4afeae3 | ✓ FIXED — BatchImport.tsx imports `parseBatchInput` from analysis/batchImport; no private re-impl in views. |
| WR-01 mid-split 413 discards partial work | Warning | acc43bd | ✓ FIXED — fetchChunk retains left-half groups + covered set on sibling failure. |
| WR-02 retryChunk races global loading | Warning | acc43bd | ✓ FIXED — retry state writes gated on `myRun === runId.current`. |
| WR-04 TOO_MANY_AUTHORS futile Retry | Warning | acc43bd | ✓ FIXED — chunkDegradeDecision shrinks TOO_MANY_AUTHORS like 413; unit-tested. |
| WR-05 unsplittable 413 shown recoverable | Warning | acc43bd | ✓ FIXED — length≤1 → 'terminal' hard-fail, Retry suppressed; unit-tested. |
| WR-03 unbounded enumeration loop | Warning | 7a73aaf | ✓ FIXED — isStuckPage no-progress detector + TRIAGE.maxEnumPages ceiling; unit-tested. |
| WR-06 BODY_LIMIT_BYTES never enforced | Warning | 6bb4a37 | ✓ FIXED — serialized-body-size pin added to chunk.test.ts. |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| BATCH-01 | 04-01, 04-02 | Import batch (paste/file), mixed npub/hex normalized | ✓ SATISFIED | parseBatchInput + BatchImport sources (SC1). |
| BATCH-02 | 04-01, 04-02 | Chunked ≤1000 / 256 KiB, small perAuthor | ✓ SATISFIED | chunk.ts + useLatestPerAuthor degrade (SC2). |
| BATCH-03 | 04-01, 04-02 | Triage table matched by author, 0-events shown | ✓ SATISFIED | mergeByAuthor + TriageTable (SC3). |
| BATCH-04 | 04-01, 04-02 | Enumerate distinct authors, feed same pipeline | ⚠️ SATISFIED (wired) / needs-human | useAuthorEnumeration → collected set → TriageTable (SC5); live loop = human. |

No orphaned requirements: REQUIREMENTS.md maps exactly BATCH-01..04 to Phase 4; all claimed in both plans.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none) | — | No TBD/FIXME/XXX debt markers in phase-modified source | — | IN-02 (REVIEW) noted `error` state is exported-but-unused in useLatestPerAuthor — documented as intentional ("currently unused; chunk errors are per-chunk"); not a blocker. |

No `dangerouslySetInnerHTML=` JSX attributes; no clean/score/verdict column; no hardcoded empty data flowing to render (rows derive from the live hook; empty initial state is overwritten by `run()`).

### Human Verification Required

The phase is a frontend slice whose remaining truths are confirmable only by running the app against the live lens. Per routing guidance these are `human_needed` (manual UAT), NOT gaps — code is implemented, wired, and build/test-verified. The 04-02 Task 4 checkpoint was deferred to phase-end (accepted-deferred-uat). See `human_verification` and `behavior_unverified_items` frontmatter for the 5 runtime/visual items (full walkthrough, WR-01 mid-split partial retention, enumeration loop, sortable table render, drill-in, accent confinement visual scan).

### Gaps Summary

No gaps. All five ROADMAP success criteria and both load-bearing invariants (left-join by author; honest no-silent-truncation chunk degrade) are implemented and wired; the pure correctness cores are unit-tested (128/128 green) and the app type-checks and bundles. The one critical and six warning code-review findings are all fixed in source with the corresponding commits present. The outstanding work is live-UI confirmation only.

---

_Verified: 2026-06-25T13:20:00Z_
_Verifier: Claude (gsd-verifier)_
