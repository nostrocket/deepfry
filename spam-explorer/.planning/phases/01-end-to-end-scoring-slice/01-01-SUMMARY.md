---
phase: 01-end-to-end-scoring-slice
plan: 01
subsystem: infra
tags: [go, golang, bfs, graph, jsonl, dgraph, spam-detection, sybil, web-of-trust]

# Dependency graph
requires: []
provides:
  - Independent spam-explorer Go module (go.mod pinned to dgo/v210 + grpc v1.75.1, Makefile, CLI flag skeleton)
  - internal/bfs.Level — pure FIFO frontier BFS leveling over an injected FrontierExpander (LEVEL-01)
  - internal/score.Score — pure in-memory follows-inversion valid-follower scoring (SCORE-01/02, D-02)
  - internal/output.Write — pure sorted, k-shell + threshold filtered JSONL writer (OUT-01/02)
affects: [Plan 02 wire tier (internal/dgraph), Phase 2 pagination, scoring calibration]

# Tech tracking
tech-stack:
  added:
    - "github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d (pinned, not yet imported)"
    - "google.golang.org/grpc v1.75.1 (pinned, not yet imported)"
    - "Go standard testing (table-driven + golden-file)"
  patterns:
    - "Injected FrontierExpander func value keeps internal/bfs pure (dependency points dgraph->bfs at call time, never bfs->dgraph at compile time)"
    - "In-memory follows-adjacency inversion for valid-follower counting (D-02) — no ~follows reverse-edge query"
    - "Sorted-by-pubkey JSONL output for byte-stable golden-file tests (Open Question 2)"
    - "registerFlags(fs *flag.FlagSet) seam makes CLI defaults unit-testable without os.Args"

key-files:
  created:
    - go.mod
    - go.sum
    - Makefile
    - .gitignore
    - cmd/spam-explorer/main.go
    - cmd/spam-explorer/main_test.go
    - internal/bfs/bfs.go
    - internal/bfs/bfs_test.go
    - internal/score/score.go
    - internal/score/score_test.go
    - internal/output/jsonl.go
    - internal/output/jsonl_test.go
  modified: []

key-decisions:
  - "Pinned dgo/v210 + grpc via `go get` (recorded as indirect in go.mod + hashed in go.sum) because no code imports them in Plan 01; they flip to direct deps when Plan 02 adds internal/dgraph"
  - "maxLevel <= 0 chosen as the 'no cap' sentinel for bfs.Level (documented)"
  - "Nodes AT the maxLevel cap are leveled but NOT expanded — only level-(M+1) discoveries are dropped, which is exactly what the D-04 correctness proof relies on"
  - "follower_count appears only in the score.go doc-comment proof (explaining why it is NOT used), never as code"

patterns-established:
  - "Pure algorithm packages (bfs/score/output) with zero Dgraph/grpc imports — fully offline unit-testable; I/O isolated to the future internal/dgraph"
  - "TDD RED->GREEN per pure package: failing test commit then implementation commit"

requirements-completed: [CLI-01, LEVEL-01, SCORE-01, SCORE-02, OUT-01, OUT-02]

# Metrics
duration: 15 min
completed: 2026-06-24
status: complete
---

# Phase 1 Plan 01: Pure Offline Scoring Core Summary

**Scaffolded the independent spam-explorer Go module and implemented the metric's pure heart — FIFO BFS leveling, in-memory follows-inversion valid-follower scoring, and sorted k-shell+threshold JSONL output — proven end-to-end on in-memory graphs with zero dependence on a live Dgraph.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-06-24T02:19:00Z
- **Completed:** 2026-06-24T02:34:22Z
- **Tasks:** 3
- **Files modified:** 12 (all created — greenfield module)

## Accomplishments
- Independent `spam-explorer` Go module: `go.mod` (module spam-explorer, go 1.24.1, dgo/v210 + grpc v1.75.1 pinned to the web-of-trust sibling versions), stack-standard `Makefile`, and a CLI skeleton wiring all six flags (`--seed`, `--threshold`, `--exclude-shells`, `--dgraph`, `--max-level`, `--out`) with the documented Phase-1 defaults.
- `internal/bfs.Level`: pure FIFO frontier BFS that assigns shortest-hop levels with first-reached-wins, terminates on cycles via a visited set, and honours the `--max-level` cap (D-03; `<=0` = no cap). Frontier expander is injected so it runs with no live Dgraph.
- `internal/score.Score`: pure in-memory adjacency inversion counting only strictly-upstream followers (`level(F) < level(T)`), skipping targets beyond the cap (D-04). Carries the verbatim D-02/D-04 correctness proof in its doc comment so no future editor reintroduces `~follows`.
- `internal/output.Write`: pure JSONL writer excluding seed + shells (`level > k`) and emitting only `vfc < threshold`, sorted by pubkey for byte-stable golden tests.
- Full offline test suite (10 bfs/score cases + 6 output golden-file cases + 2 CLI flag-default cases) passes; `go build`, `go vet` clean; purity invariant holds (no dgraph/grpc imports in the three pure packages).

## Task Commits

Each task was committed atomically (TDD tasks use test -> feat):

1. **Task 1: Module scaffold + CLI flag skeleton** - `e59ee30` (feat)
2. **Task 2: Pure BFS leveling + valid-follower scoring** - `127c8f7` (test, RED) -> `182a58c` (feat, GREEN)
3. **Task 3: Pure JSONL output writer** - `143fdd6` (test, RED) -> `9aebe80` (feat, GREEN)

## Files Created/Modified
- `go.mod` / `go.sum` - module spam-explorer @ go 1.24.1; pins dgo/v210 + grpc v1.75.1 with hashes reused from the web-of-trust sibling
- `Makefile` - build/run/test/fmt/vet/tidy/clean/lint/lint-fix + build-alpine/build-linux; version-injection LDFLAGS
- `.gitignore` - ignores `/bin/`, the root `spam-explorer` binary, and `spam-candidates.jsonl`
- `cmd/spam-explorer/main.go` - six-flag CLI entry; `registerFlags` seam; resolve->BFS->score->output spine stubbed for Plan 02
- `cmd/spam-explorer/main_test.go` - asserts all six flag defaults + registration (CLI-01)
- `internal/bfs/bfs.go` + `bfs_test.go` - pure frontier BFS leveling (LEVEL-01) + injected-fake-expander tests
- `internal/score/score.go` + `score_test.go` - pure valid-follower inversion (SCORE-01/02, D-02) + table-driven tests
- `internal/output/jsonl.go` + `jsonl_test.go` - pure sorted/filtered JSONL writer (OUT-01/02) + golden-file tests

## Decisions Made
- **dgo/grpc pinned as indirect deps:** `go mod tidy` prunes the `require` block when no code imports a dependency. Since Plan 01 has no `internal/dgraph` yet, the two mandated versions are pinned via `go get` and recorded as `// indirect` in `go.mod` with their hashes in `go.sum`. They satisfy the acceptance criterion ("go.mod pins the two sibling-matched versions") and flip to direct deps the moment Plan 02 imports them. Lowest-risk provenance per threat T-01-SC — hashes reused from the building sibling.
- **`maxLevel <= 0` = "no cap"** chosen as the sentinel and documented in `bfs.Level`'s doc comment and proven by `TestLevel_NoCapWhenMaxLevelNonPositive`.
- **Cap semantics:** nodes at the cap level are leveled but never expanded; only level-(M+1) discoveries are dropped — this is precisely what the D-04 proof depends on, and `TestLevel_MaxLevelCapDropsDeeper` asserts the capped node's edges are not materialized.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pinned dgo/v210 + grpc after `go mod tidy` pruned them**
- **Found during:** Task 1 (module scaffold)
- **Issue:** `go mod tidy` removed the `require` block for dgo/v210 + grpc because no Plan-01 source file imports them yet, leaving go.mod with only `module`/`go` lines — failing the acceptance criterion that go.mod pin the two sibling-matched versions.
- **Fix:** Ran `go get github.com/dgraph-io/dgo/v210@v210.0.0-20230328113526-b66f8ae53a2d` and `go get google.golang.org/grpc@v1.75.1`, recording both at the exact web-of-trust versions (as `// indirect` until Plan 02 imports them) with hashes in go.sum.
- **Files modified:** go.mod, go.sum
- **Verification:** `cat go.mod` shows both versions pinned; `go build ./...` passes.
- **Committed in:** `e59ee30` (Task 1 commit)

**2. [Rule 1 - Bug] Removed stray root `spam-explorer` binary and gitignored it**
- **Found during:** Task 1 (post-commit untracked-file check)
- **Issue:** A compiled `spam-explorer` Mach-O binary appeared at the repo root (default `go build` output) and showed as untracked — would pollute the repo if committed.
- **Fix:** Deleted the binary and added `/spam-explorer` (plus `/bin/` and `spam-candidates.jsonl`) to `.gitignore`.
- **Files modified:** .gitignore
- **Verification:** `git status --short` shows no stray binary; `.gitignore` covers build outputs.
- **Committed in:** `e59ee30` (amended into Task 1 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking, 1 bug)
**Impact on plan:** Both necessary for a correct, clean module scaffold. No scope creep — the algorithm and tests match the plan and RESEARCH spec exactly.

## Issues Encountered
None. The purity-check greps in the plan's `<verification>` produce false-positive matches on the word "Dgraph" / "follower_count" inside doc comments; verified precisely via `go list -f '{{.Imports}}'` that no pure package actually imports dgraph/grpc, and that `follower_count` appears only in the score.go correctness-proof comment (never as code).

## Known Stubs
- `cmd/spam-explorer/main.go` orchestration body is an intentional Phase-1 stub: it parses flags and logs the config, then returns. The resolve -> BFS -> score -> output spine is wired in **Plan 02** (which adds `internal/dgraph`). Documented in the plan's Task 1 action ("orchestration body may be a minimal stub").

## Threat Flags
None — all three packages are offline, in-memory, no untrusted input crosses a boundary (matches the plan's threat model; the Dgraph endpoint / DQL-injection surface is introduced in Plan 02).

## Next Phase Readiness
- The pure pipeline core is proven correct on in-memory graphs under offline unit + golden-file tests. Plan 02 only needs to add `internal/dgraph` (client + ResolveSeed + ExpandFrontier) and feed real edges into `bfs.Level` -> `score.Score` -> `output.Write`.
- `bfs.FrontierExpander` is the exact seam `internal/dgraph.Client.ExpandFrontier` must satisfy; `bfs.FrontierResult`/`FollowEdge` carry the json tags matching the planned `frontier(func: uid(...)) { uid pubkey follows { uid pubkey } }` query.
- Flag defaults (`threshold=2`, `exclude-shells=1`, `max-level=4`) are uncalibrated placeholders (RESEARCH Open Question 1) — tuning is a runtime concern, not a blocker.

## Self-Check: PASSED

---
*Phase: 01-end-to-end-scoring-slice*
*Completed: 2026-06-24*
