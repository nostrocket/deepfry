---
status: passed
phase: 03-remaining-spam-signals
source: [03-VERIFICATION.md]
started: 2026-06-25
updated: 2026-06-25
---

## Current Test

All tests complete — passed by human walkthrough against the live lens on 2026-06-25.

## Tests

### 1. Duplicate panel — clusters + "X of N fetched" framing (DRILL-02)
expected: Near-duplicate clusters shown with a count framed against the window denominator; amber badges paired with a label; never a bare "0 duplicates".
result: [pass]

### 2. Tags panel — fan-out / stuffing flags (DRILL-03)
expected: Top-N mentioned pubkeys + top-N hashtags with counts; amber "high tag count" / "high mention fan-out" / "hashtag stuffing" labels (label+shape, not color-only) when thresholds tripped; framed against window.
result: [pass]

### 3. Kinds panel — histogram + out-of-range flag (DRILL-04)
expected: Hand-rolled CSS/SVG bar histogram of event kinds (NIP/kind name where known + raw number); neutral bars; an amber "N out-of-range kind/timestamp" note when applicable.
result: [pass]

### 4. Raw-JSON inspector — lazy fetch + escaped render + Retry/Close (DRILL-04, WR-01)
expected: "View raw" lazily fetches one event's `raw` bytes on demand (never in the list query), renders escaped plaintext in a <pre> (pretty-printed if JSON); on a fetch error the error state offers working Retry + Close controls (no misleading auto-retry); never executes HTML/markdown.
result: [pass]

### 5. Per-panel window indicator + live re-derivation (DRILL-05 carried forward)
expected: Each new panel shows a non-removable WindowIndicator (incl. at N=0); clicking "Load more" widens the window and all panels re-derive live.
result: [pass]

## Summary

total: 5
passed: 5
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
