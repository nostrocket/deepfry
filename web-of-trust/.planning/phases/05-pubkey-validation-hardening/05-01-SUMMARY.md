---
phase: 05-pubkey-validation-hardening
plan: "01"
subsystem: web-of-trust
tags: [validation, pubkey, crawler, dgraph, garbage-collection, testing]
dependency_graph:
  requires: []
  provides: [VALID-01, VALID-03]
  affects: [pkg/crawler/crawler.go, pkg/dgraph/dgraph.go, pkg/dgraph/validate_test.go]
tech_stack:
  added: []
  patterns: [hex-regex validation, recover-or-purge inline cleanup, table-driven tests]
key_files:
  created:
    - pkg/dgraph/validate_test.go
  modified:
    - pkg/crawler/crawler.go
    - pkg/dgraph/dgraph.go
decisions:
  - "Use dgraph.ValidatePubkey (hex-regex) instead of nostr.GetPublicKey (key-derivation) at both crawler validation sites"
  - "Inline recover-or-purge in MarkAttempted subsumes VALID-02 explicit-migration step — no separate startup purge needed"
  - "Recovered uppercase nodes get no last_attempt stamp so they re-enter fresh frontier with corrected pubkey"
metrics:
  duration: "~20 minutes"
  completed: "2026-06-10"
  tasks_completed: 3
  tasks_total: 3
  files_changed: 3
---

# Phase 05 Plan 01: Pubkey Validation Hardening Summary

**One-liner:** Replaced misused key-derivation validator (nostr.GetPublicKey) with hex-regex validator (dgraph.ValidatePubkey) at both crawler call sites, added inline recover-or-purge to MarkAttempted so garbage nodes self-clean, and added unit tests covering all live-DB garbage types.

## What Was Built

### Task 1: Swap nostr.GetPublicKey for dgraph.ValidatePubkey (VALID-01)

**Commit:** `2e67418`
**File:** `pkg/crawler/crawler.go`

Replaced the misused `nostr.GetPublicKey` validator at both crawler call sites:

- `FetchAndUpdateFollows` (~line 266): validates each pubkey from the stale batch before appending to `authors` for relay REQ filters
- `updateFollowsFromEvent` (~line 507): validates each p-tag pubkey before appending to `rawFollows` for Dgraph writes

Both sites now call `dgraph.ValidatePubkey(pubkey)` which enforces `^[0-9a-f]{64}$`. Invalid pubkeys are skipped with a debug-gated log line (`continue`). The `github.com/nbd-wtf/go-nostr` import was preserved — it is used 14+ times elsewhere in the file.

**Root cause fixed:** `nostr.GetPublicKey` is a private-key→public-key derivation function. When given arbitrary garbage (uppercase hex, relay-URL blobs, short hex), it silently succeeded or produced a non-sensical return without erroring. This allowed all 19 garbage nodes already in DB to pass validation and be written / re-queried.

### Task 2: Recover-or-purge logic in MarkAttempted (VALID-03, subsumes VALID-02)

**Commit:** `05322a3`
**File:** `pkg/dgraph/dgraph.go`

Extended the validation gate in `MarkAttempted` from a simple skip-and-log to a full recover-or-purge inline loop:

**RECOVERABLE path** (uppercase/mixed-case 64-char hex):
1. Resolve the garbage node's UID via `ResolvePubkeysToUIDs(ctx, []string{pk})`
2. If not found in Dgraph — silently skip (nothing to fix)
3. Check if the canonical lowercase form already exists as a separate node
   - If yes: purge the uppercase garbage node via `DeleteNodes`
   - If no: update the `pubkey` predicate in place with a UID-nquad mutation `<uid> <pubkey> "lowercase" .` — **no `last_attempt` stamp** so the corrected node re-enters the fresh frontier

**UNRECOVERABLE path** (short hex like "f1"/"cbdc"/"de", relay-URL blobs, other non-hex garbage):
1. Resolve UID via `ResolvePubkeysToUIDs(ctx, []string{pk})`
2. If found: delete via `DeleteNodes`
3. If not found: silently skip

Each action is logged at WARN/INFO so operators can observe cleanup happening. Valid pubkeys continue to the unchanged `ResolvePubkeysToUIDs` → `last_attempt` stamp path.

This satisfies VALID-03 and makes VALID-02's separate explicit migration step redundant (per CONTEXT.md decision to drop it).

### Task 3: Unit tests for ValidatePubkey and isValidHexPubkey (D-06)

**Commit:** `1b24543`
**File:** `pkg/dgraph/validate_test.go`

Created table-driven tests in `package dgraph` with no build tag (runs under `make test` / `go test -short`):

- `TestValidatePubkey`: 11 cases — valid lowercase 64-char hex, uppercase 64-char hex, mixed-case hex, short hex "f1"/"cbdc"/"de", relay-URL blob, empty string, 63-char hex (one short), 65-char hex (one over), all-zeros
- `TestIsValidHexPubkey`: 8 cases — same garbage types for the unexported bool fast-path

All 19 test cases pass. No Dgraph dependency required.

## Verification Results

```
go build ./...          # exit 0
go test -short ./...    # ok web-of-trust/pkg/dgraph
go vet ./...            # exit 0
grep nostr.GetPublicKey # 0 non-comment occurrences in pkg/crawler/crawler.go
grep dgraph.ValidatePubkey # 2 occurrences in pkg/crawler/crawler.go
```

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — all validation paths are wired to real logic. Integration behavior (MarkAttempted recover/purge end-to-end against live Dgraph) is verified in Plan 02 per the plan's verification section.

## Threat Flags

No new threat surface introduced. T-05-01 (p-tag intake Tampering) is now mitigated by Task 1. T-05-03 (stale-frontier re-queue DoS) is now mitigated by Task 2.

## Requirements Delivered

| Requirement | Status |
|-------------|--------|
| VALID-01 | Delivered — hex-regex validator at both crawler call sites |
| VALID-02 | Subsumed — inline recover/purge in MarkAttempted handles existing garbage |
| VALID-03 | Delivered — MarkAttempted recover-or-purge loop active |

## Self-Check: PASSED

- `pkg/crawler/crawler.go` modified: confirmed (commit 2e67418)
- `pkg/dgraph/dgraph.go` modified: confirmed (commit 05322a3)
- `pkg/dgraph/validate_test.go` created: confirmed (commit 1b24543)
- All commits exist in git log
- `go test -short ./pkg/dgraph/` passes (19 test cases green)
- `go build ./...` passes
- `go vet ./...` passes
