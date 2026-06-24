---
phase: 01-end-to-end-scoring-slice
plan: 02
subsystem: infra
tags: [go, golang, dgraph, dgo, grpc, bfs, jsonl, spam-detection, sybil, web-of-trust, walking-skeleton]

# Dependency graph
requires:
  - "internal/bfs.Level (Plan 01) — pure FIFO frontier BFS; FrontierExpander/FrontierResult/FollowEdge seam"
  - "internal/score.Score (Plan 01) — pure in-memory follows-inversion valid-follower scoring"
  - "internal/output.Write (Plan 01) — pure sorted, k-shell + threshold filtered JSONL writer"
  - "go.mod pins dgo/v210 + grpc v1.75.1 (Plan 01, indirect until imported here)"
provides:
  - "internal/dgraph.NewClient — minimal read-only dgo/v210 gRPC client (MaxCallRecvMsgSize=256<<20, insecure creds, no mutation/Alter/schema) (INGEST-01, D-06)"
  - "internal/dgraph.Client.ResolveSeed — eq(pubkey,%q) UID lookup with DQL-injection-safe quoting + missing-seed guard (CLI-01 input path, Pitfall 4, T-01-02)"
  - "internal/dgraph.Client.ExpandFrontier — one BFS level per round-trip returning []bfs.FrontierResult; satisfies bfs.FrontierExpander directly; the Phase 2 pagination seam (INGEST-03, D-01)"
  - "cmd/spam-explorer/main.go — full resolve->BFS->score->output orchestration spine against live Dgraph"
affects: [Phase 2 pagination (inside ExpandFrontier), scoring calibration, Phase 3 input validation + secret-safe logging]

# Tech tracking
tech-stack:
  added:
    - "github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d (now a DIRECT dep — imported by internal/dgraph)"
    - "google.golang.org/grpc v1.75.1 (now a DIRECT dep — imported by internal/dgraph)"
    - "google.golang.org/grpc/credentials/insecure (local gRPC, no TLS by stack design)"
    - "github.com/dgraph-io/dgo/v210/protos/api (api.NewDgraphClient)"
  patterns:
    - "ExpandFrontier returns []bfs.FrontierResult so it satisfies bfs.FrontierExpander with NO adapter — dependency points dgraph->bfs (never bfs->dgraph)"
    - "Query strings extracted into pure helpers (frontierQuery/resolveSeedQuery) so query SHAPE is unit-testable offline (web-of-trust package-constant pattern)"
    - "Read-only txn + defer Discard for every query; %q (strconv.Quote) for the only untrusted interpolation (the --seed pubkey)"
    - "Empty-pubkey guard in output.Write: uncrawled stub follows-targets are leveled by BFS but never emitted"

key-files:
  created:
    - internal/dgraph/client.go
    - internal/dgraph/resolve.go
    - internal/dgraph/frontier.go
    - internal/dgraph/client_test.go
    - internal/dgraph/frontier_test.go
  modified:
    - cmd/spam-explorer/main.go
    - internal/output/jsonl.go
    - internal/output/jsonl_test.go
    - go.mod
    - go.sum

key-decisions:
  - "ExpandFrontier returns []bfs.FrontierResult directly (not a local dgraph.FrontierResult type) — bfs already defined the exact json-tagged shape in Plan 01, so client.ExpandFrontier satisfies bfs.FrontierExpander with zero adapter code; the dependency cleanly points dgraph->bfs"
  - "Query strings live in pure helper funcs (frontierQuery, resolveSeedQuery) — lets frontier_test assert the query shape (func: uid root, nested follows{uid pubkey}, %q-quoted seed) offline without a live DB"
  - "Empty-pubkey records are filtered in output.Write, not the wire layer — the wire faithfully returns whatever the graph holds (some follows-targets are uncrawled stubs with no pubkey predicate); the output filter is where 'usable candidate' is defined"

patterns-established:
  - "internal/dgraph is the SOLE wire tier; all Dgraph I/O isolated here, bfs/score/output stay pure (Architectural Responsibility Map)"
  - "Phase 2 pagination seam is the single ExpandFrontier function — bfs.go never changes when batching drops in"

requirements-completed: [CLI-01, INGEST-01, INGEST-03, LEVEL-01, SCORE-01, SCORE-02, OUT-01, OUT-02]

# Metrics
duration: 18 min
completed: 2026-06-24
status: complete
---

# Phase 1 Plan 02: Wire Tier + End-to-End Walking Skeleton Summary

**Built the minimal read-only `internal/dgraph` wire tier (dgo/v210 client with the load-bearing 256 MiB gRPC cap, DQL-injection-safe seed resolution, and one-round-trip frontier expansion) and wired the full seed -> live-Dgraph -> BFS -> score -> filtered-JSONL spine into main.go — confirmed end-to-end against the live web-of-trust graph (46,697 accounts leveled, 21,880 spam candidates emitted), proving RESEARCH Assumption A1.**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-06-24T02:34:xxZ (immediately after Plan 01)
- **Completed:** 2026-06-24T02:43:16Z
- **Tasks:** 2
- **Files:** 5 created + 5 modified

## Accomplishments

- **`internal/dgraph.NewClient`** (INGEST-01, D-06): hand-written minimal read-only client transcribing the web-of-trust dial idiom — `grpc.NewClient` (not deprecated `grpc.Dial`), insecure transport creds, and the **load-bearing `grpc.MaxCallRecvMsgSize(256<<20)`** that keeps a fan-out frontier response from failing with `ResourceExhausted`. Wraps `*dgo.Dgraph` + `*grpc.ClientConn`; `Close()` releases the conn. **No `Alter`, `Mutate`, `Commit`, or schema code path exists** (D-06, threat T-01-04).
- **`internal/dgraph.ResolveSeed`** (CLI-01 input path, Pitfall 4, T-01-02): `eq(pubkey, %q)` read-only lookup with `defer txn.Discard`. The `--seed` is the only untrusted interpolation and is quoted with `%q`/`strconv.Quote` (DQL-injection mitigation — never raw `%s`). Returns a clear `seed pubkey %q not found in graph` error so a missing seed exits with a signal instead of a silent empty file.
- **`internal/dgraph.ExpandFrontier`** (INGEST-03, D-01): one BFS level per round-trip via `frontier(func: uid(<csv>)) { uid pubkey follows { uid pubkey } }`, returning `[]bfs.FrontierResult` so it satisfies `bfs.FrontierExpander` with no adapter. Carries the **Phase 2 pagination-seam comment** marking it as the single place batching drops in.
- **`cmd/spam-explorer/main.go`**: replaced the Plan-01 stub with the full spine — `NewClient` -> `ResolveSeed` -> `bfs.Level(ctx, seedUID, client.ExpandFrontier, maxLevel)` -> `score.Score` -> `output.Write`, with `log.Fatalf` on every failure point, `defer client.Close()`, and a stderr summary line (`leveled / scored / emitted / deepest-level`).
- **Live end-to-end smoke run (A1 confirmed):** `go run ./cmd/spam-explorer --seed d91191… --max-level 2 --out /tmp/spam-candidates.jsonl` against the live Dgraph (gRPC `localhost:9080`) exited 0, leveled **46,697** accounts off an 851-follow seed, scored 46,696, and wrote **21,880** well-formed `{"pubkey":…,"valid_follower_count":…}` lines — every pubkey a 64-char hex, zero `0x` UIDs leaked.
- **Offline test coverage:** `internal/dgraph` query-shape tests (frontier `func: uid` + nested `follows { uid pubkey }`; resolve `%q` quoting incl. an adversarial injection-shaped seed) and an offline client construct/close test all pass; full `go build` / `go vet` / `go test ./... -short` green.

## Task Commits

1. **Task 1: internal/dgraph (client + resolve + frontier)** — `edd73bc` (test, RED) -> `a47d4ed` (feat, GREEN)
2. **Task 2: main.go pipeline wiring + live smoke run** — `ee6ceb4` (feat)
   - Deviation fix (surfaced by the smoke run): `14c478b` (fix — skip empty-pubkey stub nodes in output)

## Files Created/Modified

- `internal/dgraph/client.go` — `NewClient` (gRPC dial, 256 MiB cap, insecure creds) + `Close`; read-only only (D-06)
- `internal/dgraph/resolve.go` — `ResolveSeed` (eq(pubkey,%q) + missing-seed guard) + `resolveSeedQuery` pure helper
- `internal/dgraph/frontier.go` — `ExpandFrontier` (-> `[]bfs.FrontierResult`) + `frontierQuery` pure helper + Phase-2 seam comment
- `internal/dgraph/client_test.go` — offline construct + Close
- `internal/dgraph/frontier_test.go` — query-shape asserts (frontier + resolve, incl. adversarial-seed escaping)
- `cmd/spam-explorer/main.go` — full orchestration spine (was a Plan-01 stub)
- `internal/output/jsonl.go` — added empty-pubkey skip guard (deviation fix)
- `internal/output/jsonl_test.go` — `TestWrite_SkipsNodesWithoutPubkey`
- `go.mod` / `go.sum` — dgo/v210 + grpc flipped from `// indirect` to direct deps (now imported)

## Decisions Made

- **`ExpandFrontier` returns `[]bfs.FrontierResult` (no local result type, no adapter):** Plan 01 already defined `bfs.FrontierResult`/`FollowEdge` with json tags (`uid`/`pubkey`/`follows`) matching the planned query exactly. Returning that type makes `client.ExpandFrontier` satisfy `bfs.FrontierExpander` at the call site directly — the dependency points dgraph->bfs (correct direction), and main injects it with no glue. The plan explicitly permitted an adapter; none was needed.
- **Query strings extracted to pure helpers (`frontierQuery`, `resolveSeedQuery`):** enables the offline query-shape assertions the plan and RESEARCH Validation Architecture call for (web-of-trust package-constant pattern) without a live DB.
- **Empty-pubkey filtering belongs in `output.Write`, not the wire:** the wire layer should faithfully return whatever the graph holds; "is this a usable candidate" is an output-policy decision.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Empty-pubkey stub nodes were emitted as `{"pubkey":"",...}`**
- **Found during:** Task 2 (live smoke run output validation)
- **Issue:** The web-of-trust graph contains `follows`-edges pointing to UIDs that have **no `pubkey` predicate** (uncrawled stub nodes — a follow references a pubkey that was never itself crawled into a profile). BFS legitimately levels these (they are discovered as followees), so they flowed through scoring and `output.Write` emitted them as `{"pubkey":"","valid_follower_count":N}`. The first smoke run produced 8 such unidentifiable rows out of 21,888. An empty pubkey is not an actionable spam candidate and pollutes the JSONL.
- **Fix:** Added a guard in `output.Write` to skip any node whose resolved `pubkeys[uid]` is empty. Documented in the doc comment as the third survival condition. Added regression test `TestWrite_SkipsNodesWithoutPubkey`.
- **Files modified:** `internal/output/jsonl.go`, `internal/output/jsonl_test.go`
- **Verification:** Re-ran the live smoke run — emitted dropped 21,888 -> 21,880 (the 8 empty rows removed); `grep -c '"pubkey":""'` now returns 0; all 21,880 records validate as 64-char hex pubkeys with exactly two keys.
- **Committed in:** `14c478b`

---

**Total deviations:** 1 auto-fixed (1 bug). **Impact:** Correctness improvement to the output contract surfaced only by real data (the in-memory unit graphs in Plan 01 never had pubkey-less nodes). No scope creep — the BFS/score/wire logic matches the plan and RESEARCH spec exactly; the fix is a one-line output filter plus a test.

## Issues Encountered

**Working-directory deletion mid-execution (recovered, non-fatal):** Between Task 1's GREEN commit and Task 2's first edit, an external filesystem event deleted the entire `spam-explorer/` working tree (the Bash tool reported "Working directory was deleted; shell cwd recovered to /Users/g"). All committed work was intact in git (`HEAD` at `a47d4ed`); the deletions showed only as unstaged working-tree removals. Recovered fully with `git checkout -- spam-explorer/`, re-verified `go build` + `go test ./...` green, then continued. No commits were lost and no destructive git operation (clean/reset --hard) was used — the restore was a plain checkout of tracked files from HEAD.

The plan's purity-check greps (`~follows`, `follower_count`) produce expected false-positive matches inside doc comments (score.go's D-02/D-04 proof, main.go's wiring comment, the `valid_follower_count` metric name/JSON tag). Verified precisely: no `~follows` reverse-edge query and no stored-`follower_count`-predicate read exists in any code path (D-02/D-04 satisfied).

## Known Stubs

None. The Plan-01 main.go orchestration stub is now fully implemented; the resolve->BFS->score->output spine runs end-to-end against live Dgraph.

## Threat Flags

None. The two trust boundaries in the plan's threat model are both mitigated as designed: the `--seed` -> DQL interpolation uses `%q` quoting (T-01-02), and queries select only `uid`/`pubkey`/`follows` — never event content (T-01-03, data-separation rule). The client is read-only with no mutation path (T-01-04). No new security surface was introduced.

## Next Phase Readiness

- The Walking Skeleton is complete: a real seed resolves, the reachable subgraph materializes one frontier at a time, the pure Plan-01 packages level/score/filter it, and a well-formed JSONL candidate file lands — all against the live `dgraph/standalone:v25.3.0` (A1 confirmed empirically).
- **Phase 2 seam is in place:** pagination drops INSIDE `internal/dgraph.ExpandFrontier` (split `uids` into batches, merge results); `bfs.go` is untouched. The 256 MiB cap is headroom until then.
- **Calibration is a runtime concern (RESEARCH Open Question 1):** the smoke run at `--threshold 2 --exclude-shells 1` emitted 21,880 candidates from a 46,697-node level-2 slice — defaults are uncalibrated placeholders, not a correctness blocker. Tuning + `--max-level` retention review are Phase 2; input validation + secret-safe logging are Phase 3.

## Self-Check: PASSED

- All 5 created files verified present on disk (`internal/dgraph/{client,resolve,frontier,client_test,frontier_test}.go`).
- All 4 plan commits present in `git log` (`edd73bc`, `a47d4ed`, `14c478b`, `ee6ceb4`).
- `go build ./...`, `go vet ./...`, `go test ./... -short -count=1` all green.
- gRPC cap present (`grep -c '256 << 20' internal/dgraph/client.go` == 1).
- No `~follows` query / no stored `follower_count` read in any non-comment code path.
- Live smoke run exits 0 and produces a well-formed JSONL candidate file (0 empty-pubkey rows, 0 `0x` UID leaks).

---
*Phase: 01-end-to-end-scoring-slice*
*Completed: 2026-06-24*
