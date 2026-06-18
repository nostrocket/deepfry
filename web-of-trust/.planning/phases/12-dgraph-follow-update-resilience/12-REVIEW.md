---
phase: 12-dgraph-follow-update-resilience
status: clean
review_depth: standard
reviewed: 2026-06-18
scope:
  - pkg/dgraph/dgraph.go
  - pkg/dgraph/dgraph_chunks_test.go
  - pkg/dgraph/dgraph_stale_test.go
  - pkg/crawler/crawler.go
  - pkg/crawler/crawler_dgraph_write_test.go
  - cmd/crawler/main.go
  - cmd/crawler/main_test.go
---

# Phase 12 Code Review

## Findings

No blocking bugs, security issues, or behavioral regressions found in the Phase 12 source changes.

## Checks

- Verified `dgraph.IsTransientError` is shared by Dgraph and main-loop retry paths, with `ResourceExhausted` remaining fatal.
- Verified `AddFollowers` still uses one final transaction commit for the full kind-3 replacement and did not introduce per-chunk durable commits.
- Verified transient AddFollowers failures are converted into `FetchResult.SkipAttempt`, do not enter `FetchResult.Hits`, and are excluded from `MarkAttempted`.
- Verified fatal AddFollowers failures still return through `FetchAndUpdateFollows`.
- Verified regression coverage exists for classification, progress accounting, transient skip-and-continue, fatal passthrough, and attempt filtering.

## Residual Risk

- The integration-tag AddFollowers command only validates against live Dgraph when a harvested large-kind3 fixture exists; otherwise the guarded test exits successfully without exercising live persistence. Short tests cover the new logic without live Dgraph.

## Verification Reviewed

- `make test`
- `go test ./pkg/dgraph ./pkg/crawler ./cmd/crawler -count=1`
- `go test -tags=integration ./pkg/dgraph -run TestAddFollowersLargeKind3 -count=1`
