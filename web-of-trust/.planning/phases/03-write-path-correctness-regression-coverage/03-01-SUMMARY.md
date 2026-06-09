---
phase: 03-write-path-correctness-regression-coverage
plan: 01
subsystem: database
tags: [dgraph, grpc, batching, validation, nostr, kind3, web-of-trust]

# Dependency graph
requires:
  - phase: 02-backfill-live-verification
    provides: live AddFollowers write path and crawler chunking baseline
provides:
  - "Unified AddFollowers: full follow set written in one all-or-nothing transaction with internal 200-item batching"
  - "Shared hex-pubkey validator (dgraph.ValidatePubkey + isValidHexPubkey) gating every Dgraph pubkey-add site"
  - "Pure chunkSlice([]string,int) helper as the unit-test seam for batching"
  - "Single crawler write path: >10000 chunk branch removed, chunks.go deleted"
affects: [03-02, phase-04-security, web-of-trust-crawler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Internal batching inside the Dgraph client (query string + mutations windowed at 200) rather than caller-side chunking"
    - "Size-scaled timeout: deadline = baseTimeout + batches*perBatchTimeout, one live context per write"
    - "Single-source-of-truth pubkey validator reused across packages"

key-files:
  created:
    - pkg/dgraph/validate.go
  modified:
    - pkg/dgraph/dgraph.go
    - pkg/crawler/crawler.go
    - cmd/healthcheck/main.go
  deleted:
    - pkg/crawler/chunks.go

key-decisions:
  - "Batch BOTH the followee-resolution query string and the mutations in 200-item windows: at ~10k followees the query string alone exceeds the ~4MB gRPC cap, not just the nquads"
  - "Edge nquads accumulated across windows, then re-chunked at batchSize for the edge mutation, so a huge follow list never produces one oversized SetNquads"
  - "Invalid signer returns an error (nothing written); invalid followee / MarkAttempted input is skipped+logged so the valid remainder still persists (D-09)"
  - "Version guard left byte-for-byte unchanged so same/older createdAt still short-circuits (CHUNK-02 preserved)"

patterns-established:
  - "Pubkey validation gate: dgraph.ValidatePubkey (exported) + isValidHexPubkey (hot-loop bool), backed by one validHexPubkeyRe"
  - "chunkSlice: pure, no Dgraph dependency, never returns an empty trailing chunk"

requirements-completed: [CHUNK-01, CHUNK-02, LEAK-01]

# Metrics
duration: 3 min
completed: 2026-06-09
---

# Phase 3 Plan 01: Write-Path Correctness Summary

**Unified AddFollowers into a single all-or-nothing transaction with internal 200-item batching of both the followee-resolution query and the edge mutations, deleted the crawler chunking path, and added a shared hex-pubkey validator gating every Dgraph pubkey-add site.**

## Performance

- **Duration:** 3 min
- **Started:** 2026-06-09T09:21:41Z
- **Completed:** 2026-06-09T09:24:42Z
- **Tasks:** 3
- **Files modified:** 4 (1 created, 3 modified, 1 deleted)

## Accomplishments
- Eliminated the chunked-write data-drop bug at its root: chunks 2…N no longer re-enter AddFollowers carrying the same createdAt and trip the version guard, so a pubkey with >500 follows now persists its entire follow set in one crawl (CHUNK-01).
- AddFollowers is now the single write path for the full follow set in one transaction, with internal batching (batchSize=200) of the resolution query string, stub-create, and edge mutations so neither exceeds the ~4MB gRPC cap at ~10k followees (D-06), plus a size-scaled timeout context with one defer cancel (D-07, LEAK-01).
- Added pkg/dgraph/validate.go as the single source of truth for pubkey validation; all three pubkey-add sites (signer write, followee stub, MarkAttempted input) now gate via isValidHexPubkey (D-08/D-09).
- Deleted pkg/crawler/chunks.go (removing its per-iteration `defer cancel()` leak, LEAK-01) and removed the `>10000` size-branch in crawler.go.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add shared hex-pubkey validator and dedupe healthcheck** - `ccfa8b1` (feat)
2. **Task 2: Rewrite AddFollowers + gate MarkAttempted** - `c982480` (fix)
3. **Task 3: Delete chunks.go and remove crawler size-branch** - `fe0b032` (refactor)

## Files Created/Modified
- `pkg/dgraph/validate.go` (created) - validHexPubkeyRe + exported ValidatePubkey + internal isValidHexPubkey
- `pkg/dgraph/dgraph.go` (modified) - rewritten AddFollowers (single batched write path, size-scaled timeout, validation gate), new chunkSlice helper + tuning consts, gated MarkAttempted input
- `pkg/crawler/crawler.go` (modified) - removed `>10000` chunk branch; unconditional AddFollowers call
- `cmd/healthcheck/main.go` (modified) - dropped local regexp + var validPubkey, delegates to dgraph.ValidatePubkey
- `pkg/crawler/chunks.go` (deleted) - removed processFollowsInChunks and its context leak

## Decisions Made
- Batch the followee-resolution query string (not only the nquads): at ~10k followees the single query string alone exceeds 4MB, so it is windowed at 200 like the mutations.
- Edge nquads are accumulated across windows then re-chunked at batchSize for the SetNquads mutation, keeping each edge mutation under the cap on very large lists.
- Named consts `baseTimeout`, `perBatchTimeout`, `batchSize` documented as the single tuning point.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None. `go test -short ./pkg/dgraph/` reports "no test files" because the existing `dgraph_stale_test.go` is integration-tagged; the unit-test seam (chunkSlice) is covered by Plan 03-02. `go vet -tags=integration ./pkg/dgraph/` confirms the integration build still compiles after the rewrite.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- chunkSlice is exported within the package and ready for the TEST-04 unit test in Plan 03-02.
- AddFollowers full-set write path is ready for the TEST-03 integration assertion in Plan 03-02.
- Phase 4 SEC-02 RemoveFollower should reuse dgraph.ValidatePubkey (documented in the validator doc-comment).

## Self-Check: PASSED

- `pkg/dgraph/validate.go` exists on disk with ValidatePubkey + isValidHexPubkey. PASS
- `go build ./...`, `go vet ./pkg/dgraph/ ./pkg/crawler/ ./cmd/healthcheck/`, and `go vet -tags=integration ./pkg/dgraph/` all clean. PASS
- `grep -rc processFollowsInChunks pkg/ cmd/` == 0; `test ! -f pkg/crawler/chunks.go`. PASS
- Version guard `if kind3createdAt <= existingKind3CreatedAt` present exactly once and still `return nil`s. PASS
- All three D-08 sites reference isValidHexPubkey (signer gate, followee skip, MarkAttempted gate). PASS
- 3 task commits present (`ccfa8b1`, `c982480`, `fe0b032`). PASS

---
*Phase: 03-write-path-correctness-regression-coverage*
*Completed: 2026-06-09*
