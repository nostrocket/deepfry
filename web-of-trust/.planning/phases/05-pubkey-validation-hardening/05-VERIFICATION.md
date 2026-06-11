---
phase: 05-pubkey-validation-hardening
verified: 2026-06-10T12:55:04Z
human_verified: 2026-06-11T12:32:00Z
status: complete
score: 4/4 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Run `make test-integration` (or `go test -tags=integration ./pkg/dgraph/ -run TestMarkAttemptedRecoverOrPurge`) against a live Dgraph at localhost:9080"
    expected: "TestMarkAttemptedRecoverOrPurge passes: uppercase node recovered to lowercase (last_attempt unset), short-hex node deleted, relay-blob node deleted"
    result: pass
    run: 2026-06-11T12:30:12Z
  - test: "Run `go test -tags=integration ./pkg/dgraph/ -run TestWritePathRejectsGarbage` against a live Dgraph at localhost:9080"
    expected: "TestWritePathRejectsGarbage passes: zero garbage-pubkey nodes written to Dgraph when AddFollowers is called with a mixed follow-list (valid + uppercase hex + short hex + relay blob)"
    result: pass
    run: 2026-06-11T12:31:48Z
---

# Phase 5: Pubkey Validation Hardening Verification Report

**Phase Goal:** Invalid pubkeys never enter Dgraph and existing garbage nodes age out of the stale frontier without manual intervention
**Verified:** 2026-06-10T12:55:04Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A p-tag with uppercase hex, a relay-URL blob, or a truncated value is rejected at updateFollowsFromEvent and nothing is written to Dgraph for that value | VERIFIED | `pkg/crawler/crawler.go:507` calls `dgraph.ValidatePubkey(pubkey)` — enforces `^[0-9a-f]{64}$`; skips on error with debug log; 0 non-comment occurrences of `nostr.GetPublicKey` remain |
| 2 | FetchAndUpdateFollows no longer calls nostr.GetPublicKey to validate batch pubkeys; it uses the hex regex validator | VERIFIED | `pkg/crawler/crawler.go:266` calls `dgraph.ValidatePubkey(pubkey)`; grep confirms 0 non-comment occurrences of `nostr.GetPublicKey` in the file; go-nostr import preserved for other usages |
| 3 | MarkAttempted recovers 64-char uppercase-hex nodes to lowercase and purges unrecoverable garbage nodes (short hex, relay blobs) so they age out of the stale frontier | VERIFIED | `pkg/dgraph/dgraph.go:574-651` — full recover-or-purge loop in MarkAttempted: RECOVERABLE branch (lines 587-632) resolves UID, either deletes duplicate or updates pubkey in-place with no last_attempt stamp; UNRECOVERABLE branch (lines 633-651) resolves UID and calls DeleteNodes; valid pubkeys flow to existing last_attempt stamp path |
| 4 | ValidatePubkey and isValidHexPubkey have unit tests covering uppercase hex, short hex, relay-URL blob, and valid lowercase hex | VERIFIED | `pkg/dgraph/validate_test.go` exists, no build tag, package dgraph; `TestValidatePubkey` (11 cases) and `TestIsValidHexPubkey` (8 cases) pass under `go test -short` — confirmed by running the tests |

**Score:** 4/4 truths verified

### Note on ROADMAP Success Criterion 3 vs. Implementation

ROADMAP SC3 states "last_attempt is updated via a UID-based mutation." The actual implementation uses UID-based mutations for either in-place pubkey correction (uppercase→lowercase, no last_attempt stamp) or deletion. This divergence from SC3 literal text is explicitly pre-authorized by CONTEXT.md §canonical_refs line 57: "treat criterion 1 and 3 as the active gates" and §decisions D-03 which specifies the recover-or-purge approach. The ROADMAP SC2 text itself embeds the override note. The functional outcome — garbage nodes no longer re-enter the stale frontier indefinitely — is achieved.

VALID-02 is intentionally subsumed: CONTEXT.md §domain explicitly states the explicit startup migration / healthcheck-purge step was dropped. ROADMAP SC2 text acknowledges this inline. This is not a gap.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/crawler/crawler.go` | Hex-regex validation at both follow-list call sites | VERIFIED | Lines 266 and 507; both call `dgraph.ValidatePubkey`; skip-and-log preserved; go-nostr import intact |
| `pkg/dgraph/dgraph.go` | MarkAttempted recover-or-purge logic | VERIFIED | Lines 574-677; RECOVERABLE and UNRECOVERABLE branches present; ResolvePubkeysToUIDs and DeleteNodes called in both; valid path unchanged |
| `pkg/dgraph/validate_test.go` | Unit tests for ValidatePubkey and isValidHexPubkey | VERIFIED | File exists; package dgraph; no build tag; contains TestValidatePubkey and TestIsValidHexPubkey; all 19 test cases pass |
| `pkg/dgraph/dgraph_validation_test.go` | Integration tests gated behind `//go:build integration` | VERIFIED (structure) | First line `//go:build integration`; package dgraph; contains TestMarkAttemptedRecoverOrPurge and TestWritePathRejectsGarbage; `go vet -tags=integration` passes; live Dgraph run is human step |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/crawler/crawler.go` | `pkg/dgraph/validate.go` | `dgraph.ValidatePubkey` at lines 266 and 507 | WIRED | 2 occurrences confirmed; both in active code paths |
| `pkg/dgraph/dgraph.go MarkAttempted` | `pkg/dgraph/clusterscan.go ResolvePubkeysToUIDs` + `DeleteNodes` | UID resolution then in-place pubkey update or node delete | WIRED | ResolvePubkeysToUIDs called at lines 592, 604, 636, 657; DeleteNodes called at lines 612, 646 |
| `pkg/dgraph/dgraph_validation_test.go` | `pkg/dgraph MarkAttempted + ResolvePubkeysToUIDs` | White-box same-package test calling production methods | WIRED | Test calls c.MarkAttempted, c.ResolvePubkeysToUIDs, c.AddFollowers directly |

### Data-Flow Trace (Level 4)

Not applicable to this phase — all artifacts are processing/validation logic, not components that render dynamic data from a data source. The write path (AddFollowers / MarkAttempted) is the data sink, not a renderer.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build passes | `go build ./...` | exit 0 | PASS |
| go vet passes | `go vet ./...` | exit 0 | PASS |
| go vet with integration tag | `go vet -tags=integration ./pkg/dgraph/` | exit 0 | PASS |
| nostr.GetPublicKey absent from crawler | `grep -v '^[[:space:]]*//' crawler.go \| grep -c 'nostr.GetPublicKey'` | 0 | PASS |
| dgraph.ValidatePubkey present at 2 sites | `grep -c 'dgraph.ValidatePubkey' crawler.go` | 2 | PASS |
| Unit tests pass | `go test -short ./pkg/dgraph/ -run 'TestValidatePubkey\|TestIsValidHexPubkey' -v` | PASS (19 sub-tests) | PASS |
| MarkAttempted has strings.ToLower | `grep -q 'strings.ToLower' dgraph.go` | found | PASS |
| MarkAttempted has DeleteNodes | `grep -q 'DeleteNodes' dgraph.go` | found | PASS |
| MarkAttempted has ResolvePubkeysToUIDs | `grep -q 'ResolvePubkeysToUIDs' dgraph.go` | found (5 lines) | PASS |
| Integration test file has correct build tag | `head -1 dgraph_validation_test.go` | `//go:build integration` | PASS |
| Commits referenced in SUMMARY exist | `git log --oneline` | 2e67418, 05322a3, 1b24543, 5f50c0f all present | PASS |

### Probe Execution

No probes declared in PLAN files. Step 7c: SKIPPED (no probe scripts for this phase).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| VALID-01 | 05-01-PLAN.md | updateFollowsFromEvent validates p-tag pubkeys against `^[0-9a-f]{64}$`; nostr.GetPublicKey no longer used | SATISFIED | crawler.go:507 uses dgraph.ValidatePubkey; 0 non-comment GetPublicKey occurrences |
| VALID-02 | 05-01-PLAN.md | Existing garbage pubkeys in Dgraph purged so stale frontier is clean | SATISFIED (subsumed) | Explicitly dropped per CONTEXT.md §domain and ROADMAP SC2 inline note; inline recover/purge in MarkAttempted handles existing garbage the first time they surface |
| VALID-03 | 05-01-PLAN.md | MarkAttempted encounters invalid pubkey → UID-based mutation so node ages out | SATISFIED | dgraph.go:574-651 — recover-or-purge loop; uppercase nodes corrected in-place; unrecoverable nodes deleted; neither re-enters frontier indefinitely |

No orphaned requirements: REQUIREMENTS.md Traceability table maps VALID-01/02/03 to Phase 5; all are accounted for. FILTER/PERF/RELAY/TIMEOUT/METRIC requirements are mapped to Phases 6-8 and are not Phase 5 deliverables.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none) | - | No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER markers found in any modified file | - | - |

### Human Verification Required

#### 1. TestMarkAttemptedRecoverOrPurge — Live Dgraph Integration

**Test:** Run `go test -tags=integration ./pkg/dgraph/ -run TestMarkAttemptedRecoverOrPurge -v` from `web-of-trust/`
**Expected:** Test passes; log shows uppercase node recovered to lowercase (last_attempt=0 confirmed by queryLastAttempt), short-hex garbage node deleted, relay-blob garbage node deleted; cleanup of recovered node succeeds
**Why human:** Requires a live Dgraph instance at localhost:9080. Cannot be verified without the service running. This is the gating integration proof for VALID-03.

#### 2. TestWritePathRejectsGarbage — Live Dgraph Integration

**Test:** Run `go test -tags=integration ./pkg/dgraph/ -run TestWritePathRejectsGarbage -v` from `web-of-trust/`
**Expected:** Test passes; uppercase hex, short hex, and relay-blob strings resolve to zero nodes after AddFollowers call; valid followee was written; cleanup succeeds
**Why human:** Requires a live Dgraph instance at localhost:9080. Cannot be verified without the service running. This is the gating integration proof for VALID-01 at the Dgraph layer.

### Gaps Summary

No gaps. All automated must-haves are verified. The only open items are two integration tests that require a live Dgraph instance — these are correctly gated behind `//go:build integration` and are structural prerequisites per Plan 02's verification section ("manual run against live Dgraph at localhost:9080 — this is the manual verification step per spec §6"). The code structure, logic, compilation, and unit-test coverage are all confirmed.

---

_Verified: 2026-06-10T12:55:04Z_
_Verifier: Claude (gsd-verifier)_
