---
phase: 12-dgraph-follow-update-resilience
status: passed
verified: 2026-06-18
requirements: [DWRITE-01, DWRITE-02, DWRITE-03, DWRITE-04, OBS-02, TEST-06]
human_verification: []
gaps: []
---

# Phase 12 Verification

## Result

PASSED. Phase 12 delivers the Dgraph follow-update resilience goal: transient Dgraph follow-write failures no longer abort the crawler batch, failed pubkeys remain retry-eligible, fatal write errors still surface, and AddFollowers retains all-or-nothing graph replacement semantics with bounded internal windows and diagnostics.

## Requirement Checks

| Requirement | Status | Evidence |
|-------------|--------|----------|
| DWRITE-01 | PASS | `pkg/crawler/crawler.go` classifies transient `AddFollowers` errors through `dgraph.IsTransientError`, logs `retry_scheduled=true`, records `SkipAttempt`, and continues processing later events. Covered by `TestFetchAndUpdateFollows_TransientDgraphWriteDoesNotAbortBatch`. |
| DWRITE-02 | PASS | `pkg/dgraph/dgraph.go` adds child contexts around follower query, timestamp mutation, delete mutation, followee-resolution windows, missing-followee windows, edge windows, and commit. Source check found no `CommitNow: true` inside AddFollowers and one final `txn.Commit`. |
| DWRITE-03 | PASS | `cmd/crawler/main.go` uses `attemptableBatchKeys(pubkeys, result.SkipAttempt)` before `MarkAttempted`. Covered by `TestAttemptableBatchKeys_SkipsTransientWriteFailures`. |
| DWRITE-04 | PASS | `dgraph.IsTransientError` keeps `ResourceExhausted` and other fatal codes non-transient. Fatal AddFollowers errors return from `FetchAndUpdateFollows`. Covered by `TestFetchAndUpdateFollows_FatalDgraphWritePassthrough` and `TestIsDgraphTransient_ResourceExhaustedFatal`. |
| OBS-02 | PASS | `AddFollowers` emits `follow_update` logs with `pubkey`, `follows`, `chunk`, `elapsed`, `retry_count`, and `outcome`; transient/fatal outcomes are set through the shared classifier. |
| TEST-06 | PASS | Short tests cover transient/fatal classification, progress accounting, transient skip-and-continue, fatal passthrough, and MarkAttempted filtering. |

## Automated Verification

All commands exited 0:

```bash
make test
go test ./pkg/dgraph ./pkg/crawler ./cmd/crawler -count=1
go test -tags=integration ./pkg/dgraph -run TestAddFollowersLargeKind3 -count=1
```

## Source Checks

```bash
rg -n "func IsTransientError|type FollowUpdateError|type FetchResult|SkipAttempt|attemptableBatchKeys" \
  pkg/dgraph/dgraph.go pkg/crawler/crawler.go cmd/crawler/main.go
awk 'NR>=252 && NR<=585 && /CommitNow: true/{print NR ":" $0}' pkg/dgraph/dgraph.go
awk 'NR>=252 && NR<=585 && /txn\.Commit|CommitNow: false|CommitNow: true/{print NR ":" $0}' pkg/dgraph/dgraph.go
```

Findings:
- Required symbols exist.
- `AddFollowers` contains no `CommitNow: true`.
- `AddFollowers` has one final `txn.Commit(windowCtx)` path and internal mutations use `CommitNow: false`.

## Code Review

`12-REVIEW.md` status: clean. No blocking findings.

## Residual Risk

The integration-tag AddFollowers command depends on existing guarded fixture/live-Dgraph behavior. It exits 0 in this environment; deterministic short tests provide the primary verification for the new Phase 12 behavior.
