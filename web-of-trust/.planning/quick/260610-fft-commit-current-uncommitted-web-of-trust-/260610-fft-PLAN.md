---
quick_id: 260610-fft
description: commit current uncommitted web-of-trust changes
status: ready
mode: quick
must_haves:
  truths:
    - All pre-existing uncommitted changes in web-of-trust/ are committed in logical, atomic commits.
    - The repo builds (`go build ./...`) and vets clean after each code commit.
    - No file is left uncommitted except this quick task's own artifacts (handled by orchestrator).
  artifacts:
    - Four atomic commits grouping clusterscan, crawler refactor, gofmt, and planning housekeeping.
  key_links:
    - cmd/clusterscan/main.go
    - pkg/dgraph/clusterscan.go
    - pkg/crawler/crawler.go
---

# Quick Task 260610-fft: Commit uncommitted web-of-trust changes

## Context

The working tree contains a completed-but-uncommitted body of work. Changes already
exist in the **main working tree** (NOT to be regenerated). The job is purely to stage
and commit them in clean atomic groups. Run on the current branch
(`chore/commit-wot-clusterscan-changes`) — worktree isolation is disabled because the
changes pre-exist in the main tree.

All paths below are relative to the `web-of-trust/` module root.

`go build ./...` and `go vet ./...` already pass on the current tree (verified).

## Tasks

### Task 1 — feat(clusterscan): spam-cluster detection tool
- **files:** `cmd/clusterscan/main.go`, `pkg/dgraph/clusterscan.go`, `pkg/config/config.go`, `Makefile`, `go.mod`
- **action:** Stage exactly these five paths and commit. This is the new read-only
  clusterscan CLI plus its config fields, Makefile targets, and the `yaml.v3`
  direct-dependency promotion (legitimately imported by `cmd/discover-relays`).
- **verify:** `git status --short` shows these five no longer pending; `go build ./...` passes.
- **done:** Commit created with message:
  `feat(clusterscan): add read-only spam-cluster detection CLI`

### Task 2 — refactor(crawler): unify follow-list write path
- **files:** `pkg/crawler/crawler.go`
- **action:** Stage and commit. Removes the >10k size-branch so all follow-lists go
  through `AddFollowers` (which batches internally). Completes the `chunks.go` removal
  from commit fe0b032.
- **verify:** `go build ./...` passes.
- **done:** Commit created with message:
  `refactor(crawler): route all follow-lists through AddFollowers`

### Task 3 — style(dgraph): gofmt alignment in stale test
- **files:** `pkg/dgraph/dgraph_stale_test.go`
- **action:** Stage and commit the single gofmt whitespace fix.
- **verify:** `gofmt -l pkg/dgraph/dgraph_stale_test.go` prints nothing.
- **done:** Commit created with message:
  `style(dgraph): gofmt comment alignment in stale test`

### Task 4 — chore(planning): phase housekeeping
- **files (modified/deleted):** `.planning/config.json`,
  `.planning/phases/01-code-changes-regression-test/01-01-PLAN.md` (deleted),
  `.planning/phases/01-code-changes-regression-test/01-01-SUMMARY.md` (deleted),
  `.planning/phases/02-backfill-live-verification/02-01-PLAN.md` (deleted),
  `.planning/phases/02-backfill-live-verification/02-01-SUMMARY.md` (deleted)
- **files (new/untracked):**
  `.planning/phases/03-write-path-correctness-regression-coverage/03-PATTERNS.md`,
  `8pc_crawled.md`, `GSD-BUG-plan-phase-false-negative-agent-check.md`
- **action:** Stage all of the above (use `git add -A` scoped to these paths, including
  deletions) and commit. Do NOT stage this quick task's own directory
  (`.planning/quick/260610-fft-*`) — the orchestrator commits that in Step 8.
- **verify:** `git status --short` shows only the quick-task artifacts remaining.
- **done:** Commit created with message:
  `chore(planning): housekeeping for phases 01-03`

## Notes
- Do NOT run `go mod tidy` or reformat anything — commit the tree as-is.
- Do NOT commit `SUMMARY.md`, `PLAN.md`, `STATE.md`, or anything under
  `.planning/quick/260610-fft-*` — the orchestrator handles those.
