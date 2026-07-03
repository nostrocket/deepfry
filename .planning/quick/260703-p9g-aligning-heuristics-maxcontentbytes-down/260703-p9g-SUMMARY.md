---
phase: quick-260703-p9g
plan: 01
subsystem: whitelist-plugin
status: complete
tags: [heuristics, quarantine, garbage-gate, reconnect-churn]
requires:
  - "quarantine relay maxEventSize = 65536 (config/strfry/strfry-quarantine.conf)"
provides:
  - "heuristics.MaxContentBytes = 65536 (64 KB content bound aligned to relay maxEventSize)"
affects:
  - "bloom + router plugins (both call heuristics.Filter before forwarding to quarantine)"
tech-stack:
  added: []
  patterns: []
key-files:
  created: []
  modified:
    - whitelist-plugin/pkg/heuristics/heuristics.go
decisions:
  - "Keep MaxContentBytes bounding only len(evt.Content), not total serialized size — the large-content offenders account for essentially all observed rejections; the near-boundary residual is negligible"
metrics:
  duration: 3min
  completed: "2026-07-03"
  tasks: 1
  files: 1
---

# Phase quick-260703-p9g Plan 01: Aligning heuristics MaxContentBytes down Summary

Lowered `heuristics.MaxContentBytes` from 256 KB to 64 KB (65536) so the pre-quarantine garbage gate drops oversized events before forwarding, aligning it to the quarantine relay's `maxEventSize` and eliminating the 1006-close reconnect churn that oversized (64 KB–256 KB content) events triggered in the fire-and-forget publisher.

## What Was Built

- Changed the `MaxContentBytes` constant from `256 * 1024` to `65536`.
- Rewrote its doc comment to cover the three required points: (a) the 64 KB / 65536-byte bound on `len(evt.Content)`; (b) alignment to the quarantine relay `maxEventSize` (`config/strfry/strfry-quarantine.conf`) so oversized events drop at the gate rather than being forwarded and rejected with a 1006 close that churns the publisher's reconnect loop; (c) the caveat that the relay bounds TOTAL serialized event size while this bounds only content length, so a near-limit-content event with large tags could still marginally exceed the relay limit — acceptable because it removes the large-content offenders behind essentially all observed rejections.

`heuristics.Filter`, the reason codes, `allowedKinds`, and all other code were left unchanged. The change correctly affects both the router and bloom plugins, which share `Filter` and forward to the same 64 KB relay.

## Verification

- `cd whitelist-plugin && go test ./pkg/heuristics/ -count=1` — PASS (boundary tests reference `MaxContentBytes`/`MaxContentBytes+1` symbolically, so they still meaningfully assert the new limit).
- `cd whitelist-plugin && go build ./...` — clean.
- `grep` confirms `65536` present and `256 * 1024` absent in `pkg/heuristics/heuristics.go`.
- Test file confirmed to contain no hardcoded `256*1024` / `262144` literal (uses symbolic references only) — no test edits needed.

## Deviations from Plan

None - plan executed exactly as written.

## Known Stubs

None.

## Self-Check: PASSED

- FOUND: whitelist-plugin/pkg/heuristics/heuristics.go (contains 65536, no `256 * 1024`)
- FOUND commit: 47891f9 (fix(quick-260703-p9g-01): lower heuristics MaxContentBytes to 64 KB)

## Commits

- 47891f9: fix(quick-260703-p9g-01): lower heuristics MaxContentBytes to 64 KB
