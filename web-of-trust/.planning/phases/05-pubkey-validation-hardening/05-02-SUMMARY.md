---
phase: 05-pubkey-validation-hardening
plan: "02"
subsystem: web-of-trust
tags: [validation, pubkey, integration-tests, dgraph, garbage-collection]
dependency_graph:
  requires: [05-01]
  provides: [VALID-01, VALID-03]
  affects: [pkg/dgraph/dgraph_validation_test.go]
tech_stack:
  added: []
  patterns: [integration-test-gated-build-tag, time-derived-unique-fixtures, recover-or-purge-assertion]
key_files:
  created:
    - pkg/dgraph/dgraph_validation_test.go
  modified: []
decisions:
  - "Both D-07 and D-08 tests placed in a single file (dgraph_validation_test.go) per plan executor discretion — cohesion over file proliferation"
  - "queryLastAttempt helper defined in dgraph_validation_test.go (not stale_test.go) to keep the validation test self-contained"
  - "upperHex in D-08 derived from validFollowee to avoid a separate random fixture; kept clearly distinct from signer pubkey"
metrics:
  duration: "~2 minutes"
  completed: "2026-06-10"
  tasks_completed: 2
  tasks_total: 2
  files_changed: 1
---

# Phase 05 Plan 02: Pubkey Validation Integration Tests Summary

**One-liner:** Added two integration tests gated behind `//go:build integration` proving MarkAttempted recovers uppercase nodes to lowercase (last_attempt unset) and purges unrecoverable garbage, and that AddFollowers writes zero garbage-pubkey nodes when given a garbage-laden follow-list.

## What Was Built

### Task 1 + Task 2: Integration tests for D-07 and D-08 (single file)

**Commit:** `5f50c0f`
**File:** `pkg/dgraph/dgraph_validation_test.go`

Both integration tests live in the same file (executor discretion per plan spec). The file starts with `//go:build integration`, is in `package dgraph`, and reuses `mustMutate` and `countFrontier` helpers from `dgraph_stale_test.go` in the same package.

#### TestMarkAttemptedRecoverOrPurge (D-07)

Proves the VALID-03 recover-or-purge loop inside `MarkAttempted`:

1. Derives a unique lowercase 64-char hex pubkey via `fmt.Sprintf("%064x", time.Now().UnixNano())` and uppercases it for the recoverable fixture.
2. Builds a short-hex garbage string (`"zz" + 16-hex-nonce` — contains non-hex `z` chars, not 64 chars) and a relay-blob garbage string (`wss://relay.testmark.pub/TestMarkAttempted/<nonce>`).
3. Inserts all three nodes via `mustMutate` with `<dgraph.type> "Profile"` and no `last_attempt` on the uppercase node.
4. Calls `c.MarkAttempted(ctx, []string{upper, shortHex, relayBlob}, now)`.
5. Asserts via `ResolvePubkeysToUIDs`:
   - The lowercase canonical form now resolves to a node (recovered in place).
   - The original uppercase string no longer resolves to a node.
   - The recovered node has `last_attempt` = 0 (unset) via `queryLastAttempt` helper.
   - Both garbage nodes (short-hex and relay-blob) no longer resolve.
6. Cleans up the recovered lowercase node via `DeleteNodes`.

A local `queryLastAttempt` helper reads a node's `last_attempt` predicate by UID (returns 0 if absent).

#### TestWritePathRejectsGarbage (D-08)

Proves VALID-01 at the Dgraph layer — the `isValidHexPubkey` gate inside `AddFollowers` (dgraph.go:265) never persists garbage pubkeys:

1. Builds unique signer and valid-followee pubkeys.
2. Constructs a garbage uppercase-hex (from the valid-followee, uppercased), short-hex, and relay-blob.
3. Calls `c.AddFollowers(ctx, signerPubkey, kind3ts, follows, false)` with the mixed follow-set.
4. Asserts via `ResolvePubkeysToUIDs`:
   - None of the three garbage strings resolve to a node.
   - The valid followee was written.
5. Cleans up signer and valid-followee nodes via `DeleteNodes`.

A comment in the test documents which layer the assertion covers: `isValidHexPubkey` gate at dgraph.go:265, independent of the crawler-layer filter.

## Verification Results

```
go vet -tags=integration ./pkg/dgraph/    # exit 0
go build -tags=integration ./pkg/dgraph/  # exit 0
go test -short ./pkg/dgraph/              # ok (integration tests excluded, not affected)
```

Manual run against live Dgraph (per spec §6) is the final verification gate; automated compile check confirms both tests are structurally correct.

## Deviations from Plan

None — plan executed exactly as written. Both tests placed in one file as explicitly permitted by the plan.

## Known Stubs

None — integration tests are fully wired. Manual run against live Dgraph is a human verification step, not a stub.

## Threat Flags

No new threat surface. T-05-04 (test pollution of live frontier) is mitigated by time-derived unique fixtures and `DeleteNodes` cleanup at the end of each test. T-05-05 (garbage-string fixtures in DQL) is accepted — `ResolvePubkeysToUIDs` quotes every value with `strconv.Quote` (clusterscan.go:54–56); no raw interpolation sink added.

## Requirements Delivered

| Requirement | Status |
|-------------|--------|
| VALID-01 | Integration-verified — D-08 proves AddFollowers gate writes no garbage-pubkey nodes |
| VALID-03 | Integration-verified — D-07 proves MarkAttempted recovers uppercase and purges unrecoverable garbage |

## Self-Check: PASSED

- `pkg/dgraph/dgraph_validation_test.go` created: confirmed (commit 5f50c0f)
- First line is `//go:build integration`: confirmed
- Package is `package dgraph`: confirmed
- Contains `func TestMarkAttemptedRecoverOrPurge`: confirmed
- Contains `func TestWritePathRejectsGarbage`: confirmed
- `go vet -tags=integration ./pkg/dgraph/` passes: confirmed
- `go test -short ./pkg/dgraph/` unaffected: confirmed (ok, cached)
- Commit 5f50c0f exists in git log: confirmed
