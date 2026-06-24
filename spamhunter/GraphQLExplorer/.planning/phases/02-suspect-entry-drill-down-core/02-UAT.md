---
status: testing
phase: 02-suspect-entry-drill-down-core
source: [02-VERIFICATION.md]
started: 2026-06-24
updated: 2026-06-24
---

## Current Test

number: 1
name: End-to-end paste → route → both identity forms → live timeline
expected: |
  Paste a known author's npub (or 64-char hex) and click "Inspect author". URL becomes
  `#/a/<64 lowercase hex>`, the identity header shows BOTH npub and hex (mono, labeled),
  and a newest-first timeline appears (kind / UTC+epoch / content preview).
awaiting: user response

## Tests

### 1. End-to-end paste → route → both identity forms → live timeline (ID-01, ID-02)
expected: URL becomes `#/a/<64 lowercase hex>`; identity header shows both npub and hex; newest-first timeline renders across kinds.
result: [pending]

### 2. Window indicator partial vs full against live hasMore (DRILL-05)
expected: Indicator reads "Computed over N fetched events · …"; when more pages exist it shows the amber "more available — partial window" framing; non-removable.
result: [pending]

### 3. Load more — one page per click, no double-append, end caption (DRILL-06)
expected: One click appends exactly one page; N increases; rapid double-click adds no duplicate rows; when exhausted the button is replaced by "End of available events — this is the full window."
result: [pending]

### 4. Garbage paste stays on the dashboard with inline amber error (ID-03)
expected: Pasting `npub1zzz`/garbage shows an inline amber parse error AND stays on the dashboard (no navigation, no empty timeline).
result: [pending]

### 5. Valid-but-zero-match neutral empty state with N=0 indicator (ID-03)
expected: A valid but unused 64-hex shows a neutral "No events for this author" empty state WITH the window indicator (N=0), visually distinct from the parse error.
result: [pending]

### 6. Asymmetric burst panel — amber+labeled burst, neutral-never-clean, persistent forgeable caveat (DRILL-01, DRILL-05)
expected: Rate panel renders hand-rolled bars; burst marker is amber and labeled; no green/teal/"clean"/"safe" state; persistent "createdAt is author-claimed and forgeable" caveat beside the chart; co-located window indicator.
result: [pending]

### 7. Accent reservation — teal only on "Inspect author" (UI-SPEC)
expected: The only `--accent` (teal) element on screen is the "Inspect author" submit; no accent leaks into drill-down/rate surfaces.
result: [pending]

### 8. WR-01 bounded INVALID_CURSOR recovery (code-review fix)
expected: When the lens returns INVALID_CURSOR, the window restarts from page 1 once; a repeated/null-cursor rejection surfaces an error state rather than an infinite spinner.
result: [pending]

## Summary

total: 8
passed: 0
issues: 0
pending: 8
skipped: 0
blocked: 0

## Gaps
