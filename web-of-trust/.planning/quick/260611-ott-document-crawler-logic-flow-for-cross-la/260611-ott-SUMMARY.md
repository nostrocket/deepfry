---
phase: quick-260611-ott
plan: 01
subsystem: web-of-trust
tags: [documentation, cross-language, logic-flow, crawler, portability]
dependency_graph:
  requires: []
  provides: [DOC-CRAWLER-FLOW]
  affects: []
tech_stack:
  added: []
  patterns: [abstract-storage-operations, language-agnostic-specification]
key_files:
  created:
    - web-of-trust/fable_logic_flow.md
  modified: []
decisions:
  - "All storage interactions described as named abstract operations (EnsureSchema, GetStalePubkeys, MarkAttempted, AddFollowers, TouchLastDBUpdate, ValidatePubkey, CountPubkeys) with inputs/outputs/invariants — no Dgraph/DQL specifics in body"
  - "Two-phase GetStalePubkeys (frontier IS NULL first, then aged ASC) documented with the critical note on why ORDER BY last_attempt alone fails for null-value nodes"
  - "Filter-cap learning documented as two separate halving paths: NOTICE-based (CAS loop) and connection-drop-on-REQ attribution (500ms window, D-09/D-10)"
  - "MarkAttempted inline recover-or-purge documented as a behavioral invariant — prevents garbage pubkeys from re-entering frontier every cycle"
  - "staleRemaining metric in batch log is always 0 (totalStale := len(pubkeys) before loop, staleRemaining = totalStale - len(pubkeys) = 0) — documented as a known known, not a deviation from spec"
metrics:
  duration_seconds: 420
  completed_date: "2026-06-11"
  tasks_completed: 2
  files_created: 1
---

# Phase quick-260611-ott Plan 01: Crawler Logic Flow Document Summary

**One-liner:** Language- and database-agnostic logic flow specification of the web-of-trust crawler covering relay pool, chunked sub-REQ loop, two-path filter-cap learning, and all storage operations as abstract contracts.

## What Was Built

`web-of-trust/fable_logic_flow.md` — a 656-non-blank-line Markdown specification sufficient for a developer unfamiliar with Go, go-nostr, or Dgraph to reimplement the crawler in any language against any storage backend.

The document covers eleven sections:
1. Overview and Purpose
2. Domain Concepts (pubkey, follow edge, frontier/aged/stale, kind3CreatedAt, last_attempt, last_db_update)
3. Storage Operations — seven named abstract operations with Inputs/Outputs/Invariants tables
4. Configuration — all config keys with roles, the dual-purpose of relay_filter_batch_size
5. Main Crawl Loop — numbered steps, ASCII flowchart, seed bootstrap behavior
6. Relay Pool Management — per-relay state, state machine diagram, markRelayDead, ReconnectRelays
7. Subscription Handling — chunked sub-REQ loop, NOTICE-based halving (CAS loop), connection-drop-on-REQ attribution (D-09/D-10)
8. Event Handling — concurrent fan-in, dedup, signature verification, version check, p-tag parsing
9. Follow-List Chunking — documented as internal property of AddFollowers
10. Graceful Shutdown — signal handling, shutdown sequence, final report
11. Reimplementation Checklist — 11 behavioral invariants a port must preserve

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Extract crawler real behavior from source | (read-only analysis, no commit) | pkg/crawler/crawler.go, cmd/crawler/main.go, pkg/config/config.go, pkg/dgraph/dgraph.go, pkg/dgraph/validate.go |
| 2 | Write language- and database-agnostic logic flow document | 484c1c1 | web-of-trust/fable_logic_flow.md (created) |

## Deviations from Plan

### Code Discrepancies Found vs Plan Description

**1. [Accuracy] Forward relay reconnect does not reset failures counter**
- **Found during:** Task 1 source reading
- **Issue:** The plan action states reconnect resets "backoff to initial, reset failures to 0, RESET filterCap". The forward relay reconnect in `ReconnectRelays()` resets `backoff` and `alive` but NOT `failures` (forward relay has no failure counter in relayState — it uses backoff doubling without a threshold). The forward relay also has no filterCap.
- **Fix:** Document accurately: forward relay reconnect resets `backoff = initialBackoff` only (no failures counter, no filterCap).
- **Impact:** Section 6.5 accurately describes forward relay reconnect behavior.

**2. [Informational] staleRemaining metric is always 0**
- **Found during:** Task 1 source reading
- **Issue:** `totalStale := len(pubkeys)` (line 135 of cmd/crawler/main.go) and `staleRemaining := totalStale - len(pubkeys)` (line 161) — always 0. Pre-existing known bug (documented in STATE.md). Not a doc deviation; the spec does not describe the log metric formula.
- **Fix:** None needed — noted but not in scope of this document.

No other discrepancies found between the plan's behavioral description and the actual code.

## Known Stubs

None — documentation-only plan, no code stubs.

## Threat Flags

None — documentation-only plan, no new network endpoints, auth paths, or schema changes.

## Self-Check: PASSED

- [x] `web-of-trust/fable_logic_flow.md` exists: FOUND
- [x] Commit 484c1c1 exists in git log
- [x] 656 non-blank lines (>= 250 minimum)
- [x] Contains: GetStalePubkeys, AddFollowers, MarkAttempted, "filter", "floor"
- [x] No Go code fences (```go), no "nquad", no "dgraph.", no "nostr.Filter" in body
