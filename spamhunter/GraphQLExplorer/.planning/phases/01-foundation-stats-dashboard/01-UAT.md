---
status: passed
phase: 01-foundation-stats-dashboard
source: [01-VERIFICATION.md]
started: 2026-06-24T19:05:00Z
updated: 2026-06-24T20:30:00Z
---

## Current Test

(none — all tests passed)

## Tests

### 1. Stats polling — hidden-tab pause + corpus-changed nudge (no auto-refetch)
expected: Dot teal/"Live" while visible; dims to "Paused (tab hidden)" + poll stops when hidden; resumes on return; maxLevId increase shows dismissible teal nudge with NO auto-refetch (cards update only on "Refresh stats").
result: passed — confirmed by user in-browser against the live lens.

### 2. Visual dashboard + distinct state treatments (incl. empty-corpus)
expected: 2×2 stat-card grid renders real formatted integers (mono data / sans labels); states distinct/non-blank with verbatim copy; empty-corpus is a calm neutral fact.
result: passed — confirmed by user in-browser. Live stat cards render real data.

## Summary

total: 2
passed: 2
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps

None. During UAT a real end-to-end transport bug was found and fixed (commit 63e389d):
urql defaulted queries to GET, which hit the lens's GraphiQL IDE (HTML on GET /graphql)
instead of the API (POST /graphql, contract §1), surfacing as a spurious NETWORK error.
Fixed via `preferGetMethod: false` on the urql Client; verified end-to-end (real stats
returned). The walking skeleton now genuinely works browser→lens.
