---
phase: 09-phase-8-hardening-resilience-follow-ups
plan: "02"
subsystem: web-of-trust/pkg/crawler + web-of-trust/cmd/crawler
tags: [hardening, resilience, forward-publish-timeout, grpc-retry, backoff]
dependency_graph:
  requires: [09-01-SUMMARY.md]
  provides: [bounded-forwardEvent, transient-dgraph-retry, BackfillNextAttempt-cadence-wired]
  affects: [pkg/crawler/crawler.go, cmd/crawler/main.go]
tech_stack:
  added:
    - google.golang.org/grpc/codes (transient gRPC error classification)
    - google.golang.org/grpc/status (gRPC status extraction)
  patterns:
    - context.WithTimeout(ctx, c.timeout) bounded-publish child context (HARD-03)
    - isDgraphTransient + retry-with-backoff + labeled-break mainLoop (RESIL-01)
    - MissBackoff threaded through crawler.Config (HARD-01/IN-03)
key_files:
  created: []
  modified:
    - pkg/crawler/crawler.go
    - cmd/crawler/main.go
decisions:
  - "RESIL-01 retry params: dgraphRetryInitial=5s, dgraphRetryMax=2m, dgraphRetryAttempts=5 (consistent with relay initialBackoff/maxBackoff in crawler.go)"
  - "MarkAttempted wrapped in best-effort retry: persistent transient errors log WARN and continue (not exit) because PERF-02 stamping is non-loop-critical"
  - "MissBackoff threaded as config.MissBackoffParams field on crawler.Config (matching existing EjectionThresholds pattern) to avoid duplicating the struct definition"
  - "Worktree was forked before Phase 7/8/09-01 commits; prerequisite files (config.go, dgraph.go, backoff.go, test files) brought forward in Task 1 commit"
metrics:
  duration: "~30 minutes"
  completed: "2026-06-13"
  tasks_completed: 2
  tasks_total: 3
  files_modified: 2
  files_carried_forward: 5
---

# Phase 9 Plan 02: Crawler Drain Hardening + Main Loop Resilience (HARD-03, HARD-01, RESIL-01) Summary

Bounded the forward-publish to prevent drain-loop stalls, wired the correct hit-refresh cadence into BackfillNextAttempt, and made the main crawl loop survive transient Dgraph gRPC blips instead of exiting.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Bound forwardEvent publish + update BackfillNextAttempt caller (HARD-03, HARD-01) | 10c885c | pkg/crawler/crawler.go + prerequisite files |
| 2 | Transient-vs-fatal Dgraph error classification + retry in main loop (RESIL-01) | 37bf110 | cmd/crawler/main.go |

## Task 3: Live-Host Checkpoint (Pending)

Task 3 is a `checkpoint:human-verify` task requiring live-host verification against Dgraph + relays on the strfry host. The code is fully implemented and committed; the checkpoint is deferred.

**Steps to verify on the strfry host:**

1. Build: `cd web-of-trust && make build-crawler`. Confirm it compiles.
2. Run the crawler against live Dgraph for at least one batch; confirm it processes a batch and logs `Batch complete: ...` (HARD-03 did not break the drain — events still forward, MarkAttempted still runs).
3. RESIL-01 transient retry: while the crawler is mid-run, bounce the Dgraph container (`docker-compose -f docker-compose.dgraph.yml restart dgraph-alpha` or equivalent). Expected: the crawler logs a WARN like "Transient Dgraph error ... (attempt 1/5) ... retrying in 5s" and RESUMES once Dgraph is back — WITHOUT the process exiting.
4. Fatal path sanity: point the crawler at a bad Dgraph address (or stop Dgraph entirely past the retry budget) and confirm it eventually exits loudly with "Dgraph unavailable after 5 attempts" rather than hanging.
5. Confirm no "BackfillNextAttempt failed" WARN on startup against the already-migrated production graph (it should log "seeded 0" or a real count).

**Resume signal:** Type "approved" if the crawler survives a Dgraph bounce, exits loudly on a persistent/fatal failure, and drains batches normally; otherwise describe the observed behavior.

## Must-Haves Verification (automated)

- **HARD-03 bounded forwardEvent**: yes — `context.WithTimeout(ctx, c.timeout)` with `defer cancel()` wraps `c.forwardRelay.conn.Publish`; failure bookkeeping unchanged (pkg/crawler/crawler.go:242-246).
- **HARD-01 caller finalized**: yes — `cadenceSec := int64(cfg.MissBackoff.HitRefreshCadence.Seconds())` passed to `dgClient.BackfillNextAttempt(ctx, cadenceSec)` (pkg/crawler/crawler.go:142-143); no `86400` literal remains.
- **MissBackoff threaded**: yes — `crawler.Config` has new `MissBackoff config.MissBackoffParams` field; wired from `cfg.MissBackoff` in cmd/crawler/main.go.
- **RESIL-01 isDgraphTransient**: yes — `status.FromError(err)` with switch on `codes.Unavailable/DeadlineExceeded/ResourceExhausted`; non-gRPC and fatal codes return false (cmd/crawler/main.go:31-50).
- **RESIL-01 retry constants**: `dgraphRetryInitial=5s`, `dgraphRetryMax=2m`, `dgraphRetryAttempts=5` (consistent with crawler.go relay initialBackoff=30s/maxBackoff=5m).
- **RESIL-01 ctx cancellation honored**: yes — retry `select` includes `case <-ctx.Done(): break mainLoop`.
- **MarkAttempted best-effort**: yes — persistent transient errors log WARN and continue batch loop; does NOT exit.
- **Build + vet + tests**: `go build ./...` clean; `go vet ./pkg/crawler/ ./cmd/crawler/` clean; `go test -short ./...` passes (pkg/config, pkg/crawler, pkg/dgraph all ok).

## Deviations from Plan

### Prerequisite files carried forward (Rule 3 — blocking issue)

**Found during:** Task 1 setup

**Issue:** The worktree was forked from commit 2e9c528 (Phase 6 state), which predates the Phase 7, 8, and 09-01 changes. Files needed for compilation — `pkg/config/config.go` (EjectionThresholds/MissBackoffParams types), `pkg/dgraph/dgraph.go` (BackfillNextAttempt, CountStalePubkeys, updated MarkAttempted), `pkg/dgraph/backoff.go` (BackoffParams/BackoffInterval), and test files — were missing from the worktree.

**Fix:** Carried the main-repo versions of these files forward into the worktree in the Task 1 commit (10c885c). This brings the worktree to the expected 43c48ae state before applying the 09-02 changes. The content of the carried-forward files is byte-for-byte identical to the 43c48ae main-repo state — no substantive changes beyond what Phase 7/8/09-01 already introduced.

**Files modified:** pkg/config/config.go, pkg/dgraph/dgraph.go, pkg/dgraph/backoff.go, pkg/dgraph/backoff_test.go, pkg/config/config_test.go, pkg/crawler/crawler_quorum_test.go

**Commit:** 10c885c (included in Task 1 commit)

## Known Stubs

None — all three features are fully implemented. The only deferred item is the live-host verification (Task 3 checkpoint).

## Threat Flags

None — no new network endpoints, auth paths, or schema changes introduced. The changes are internal to error handling and context lifetime management.

## Self-Check: PASSED

Files confirmed present:
- pkg/crawler/crawler.go — modified (forwardEvent bounded context, BackfillNextAttempt cadence)
- cmd/crawler/main.go — modified (isDgraphTransient, retry loops)

Commits confirmed:
- 10c885c feat(09-02): bound forwardEvent publish + update BackfillNextAttempt caller (HARD-03, HARD-01)
- 37bf110 feat(09-02): transient-vs-fatal Dgraph error classification + retry in main loop (RESIL-01)
