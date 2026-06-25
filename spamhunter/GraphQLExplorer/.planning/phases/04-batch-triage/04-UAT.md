---
status: passed
phase: 04-batch-triage
source: [04-VERIFICATION.md]
started: 2026-06-25
updated: 2026-06-25
---

## Current Test

All tests complete — passed by human walkthrough against the live lens on 2026-06-25 (Triage button + chunked fetch confirmed working; `latestPerAuthor` returns live data).

## Tests

### 1. Batch import sources + summary (BATCH-01)
expected: Paste, file upload, and "enumerate corpus" all feed one pipeline; import summary shows valid/duplicate/unparseable counts; unparseable tokens listed (not silently dropped); note/nsec rejected.
result: [pass]

### 2. Chunked triage respects caps + pacing (BATCH-02)
expected: Triaging a large set issues sequential chunks (≤1000-author cap; 256 KiB body) with a progress indicator; a failed chunk keeps partial results and offers a retry that actually shrinks (413/TOO_MANY_AUTHORS) or shows an honest terminal hard-fail at size 1; backend not flooded.
result: [pass]

### 3. Triage table — match by author, "0 events", transparent signals (BATCH-03)
expected: Rows matched by author key (zero-match authors shown explicitly as "0 events", never misattributed); transparent per-signal columns (event count, burst, near-dup, fan-out), amber-on-signal only, NO "clean"/score column; "triaged N of M authors" denominator; persistent first-pass-screen framing.
result: [pass]

### 4. Corpus enumeration loop + Stop + snapshot honesty (BATCH-04)
expected: "enumerate corpus" paginates the authors query with a running count and a working Stop; terminates cleanly (no infinite spin on a stuck/empty page); the enumerated set is shown as a live snapshot with its count, then feeds the same chunked triage pipeline.
result: [pass]

### 5. Sorting + drill-in (BATCH-03)
expected: Triage table columns are sortable (default event count desc, source array not mutated); clicking a row opens that author's Phase 2/3 drill-down at #/a/<hex>. Accent (teal) appears only on the "Triage" submit.
result: [pass]

## Summary

total: 5
passed: 5
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
