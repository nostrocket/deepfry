---
status: testing
phase: 01-foundation-stats-dashboard
source: [01-VERIFICATION.md]
started: 2026-06-24T19:05:00Z
updated: 2026-06-24T19:05:00Z
---

## Current Test

number: 1
name: Stats polling — hidden-tab pause + "corpus changed" nudge (no auto-refetch)
expected: |
  Run `npm run dev` against the live lens (the gitignored .env points VITE_GRAPHQL_URL at
  http://192.168.149.21:8080/graphql). The live-poll dot is teal/"Live" while the tab is
  visible; switching to another tab for >5s dims it to "Paused (tab hidden)" and the network
  poll stops; returning resumes immediately. A maxLevId increase shows the dismissible teal
  "Corpus changed — refresh to update." nudge and does NOT auto-refetch the cards until
  "Refresh stats" is clicked.
awaiting: user response

## Tests

### 1. Stats polling — hidden-tab pause + corpus-changed nudge (no auto-refetch)
expected: Dot teal/"Live" while visible; dims to "Paused (tab hidden)" + poll stops when hidden; resumes on return; maxLevId increase shows dismissible teal nudge with NO auto-refetch (cards update only on "Refresh stats").
result: [pending]

### 2. Visual dashboard + distinct state treatments (incl. empty-corpus)
expected: 2×2 stat-card grid renders real formatted integers (mono data / sans labels); each error/empty/paused state is distinct and non-blank with its verbatim UI-SPEC copy paired with a label/shape (not color alone); empty-corpus ("No events in corpus yet") reads as a calm neutral fact, never an error. The empty-corpus branch is unreachable against the live ~27.1M-event corpus — confirm via a mocked `eventCount: 0` render or accept as environment-blocked. Optionally point VITE_GRAPHQL_URL at a down host to see the NETWORK red state.
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
