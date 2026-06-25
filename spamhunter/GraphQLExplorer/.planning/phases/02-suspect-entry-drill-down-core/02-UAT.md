---
status: passed
phase: 02-suspect-entry-drill-down-core
source: [02-VERIFICATION.md]
started: 2026-06-24
updated: 2026-06-25
---

## Current Test

All tests complete — passed by human walkthrough against the live lens on 2026-06-25.

## Tests

### 1. End-to-end paste → route → both identity forms → live timeline (ID-01, ID-02)
expected: URL becomes `#/a/<64 lowercase hex>`; identity header shows both npub and hex; newest-first timeline renders across kinds.
result: [pass]

### 2. Window indicator partial vs full against live hasMore (DRILL-05)
expected: Indicator reads "Computed over N fetched events · …"; when more pages exist it shows the amber "more available — partial window" framing; non-removable.
result: [pass]

### 3. Load more — one page per click, no double-append, end caption (DRILL-06)
expected: One click appends exactly one page; N increases; rapid double-click adds no duplicate rows; when exhausted the button is replaced by "End of available events — this is the full window."
result: [pass]

### 4. Garbage paste stays on the dashboard with inline amber error (ID-03)
expected: Pasting `npub1zzz`/garbage shows an inline amber parse error AND stays on the dashboard (no navigation, no empty timeline).
result: [pass]

### 5. Valid-but-zero-match neutral empty state with N=0 indicator (ID-03)
expected: A valid but unused 64-hex shows a neutral "No events for this author" empty state WITH the window indicator (N=0), visually distinct from the parse error.
result: [pass]

### 6. Asymmetric burst panel — amber+labeled burst, neutral-never-clean, persistent forgeable caveat (DRILL-01, DRILL-05)
expected: Rate panel renders hand-rolled bars; burst marker is amber and labeled; no green/teal/"clean"/"safe" state; persistent "createdAt is author-claimed and forgeable" caveat beside the chart; co-located window indicator.
result: [pass]

### 7. Accent reservation — teal only on "Inspect author" (UI-SPEC)
expected: The only `--accent` (teal) element on screen is the "Inspect author" submit; no accent leaks into drill-down/rate surfaces.
result: [pass]

### 8. WR-01 bounded INVALID_CURSOR recovery (code-review fix)
expected: When the lens returns INVALID_CURSOR, the window restarts from page 1 once; a repeated/null-cursor rejection surfaces an error state rather than an infinite spinner.
result: [pass]

## Summary

total: 8
passed: 8
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
