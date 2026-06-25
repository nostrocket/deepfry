---
status: testing
phase: 03-remaining-spam-signals
source: [03-VERIFICATION.md]
started: 2026-06-25
updated: 2026-06-25
---

## Current Test

number: 1
name: Duplicate panel rendering + window framing
expected: |
  On an author drill-down with fetched events, the Duplicate panel renders cluster(s) of
  near-duplicate posts framed as "X of N fetched" (never bare "0 duplicates"), with amber
  cluster badges (label+shape, no green/teal).
awaiting: user response

## Tests

### 1. Duplicate panel — clusters + "X of N fetched" framing (DRILL-02)
expected: Near-duplicate clusters shown with a count framed against the window denominator; amber badges paired with a label; never a bare "0 duplicates".
result: [pending]

### 2. Tags panel — fan-out / stuffing flags (DRILL-03)
expected: Top-N mentioned pubkeys + top-N hashtags with counts; amber "high tag count" / "high mention fan-out" / "hashtag stuffing" labels (label+shape, not color-only) when thresholds tripped; framed against window.
result: [pending]

### 3. Kinds panel — histogram + out-of-range flag (DRILL-04)
expected: Hand-rolled CSS/SVG bar histogram of event kinds (NIP/kind name where known + raw number); neutral bars; an amber "N out-of-range kind/timestamp" note when applicable.
result: [pending]

### 4. Raw-JSON inspector — lazy fetch + escaped render + Retry/Close (DRILL-04, WR-01)
expected: "View raw" lazily fetches one event's `raw` bytes on demand (never in the list query), renders escaped plaintext in a <pre> (pretty-printed if JSON); on a fetch error the error state offers working Retry + Close controls (no misleading auto-retry); never executes HTML/markdown.
result: [pending]

### 5. Per-panel window indicator + live re-derivation (DRILL-05 carried forward)
expected: Each new panel shows a non-removable WindowIndicator (incl. at N=0); clicking "Load more" widens the window and all panels re-derive live.
result: [pending]

## Summary

total: 5
passed: 0
issues: 0
pending: 5
skipped: 0
blocked: 0

## Gaps
