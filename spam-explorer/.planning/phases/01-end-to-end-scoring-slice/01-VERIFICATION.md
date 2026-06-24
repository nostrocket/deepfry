---
phase: 01-end-to-end-scoring-slice
verified: 2026-06-24T10:48:00Z
status: passed
score: 5/5 must-haves verified
behavior_unverified: 0
overrides_applied: 0
---

# Phase 1: end-to-end-scoring-slice Verification Report

**Phase Goal:** Deliver a runnable CLI that, given a seed, levels a small reachable subgraph, counts strictly-upstream valid followers, and writes a threshold-filtered JSONL file — the complete metric proven correct end-to-end.

**Verified:** 2026-06-24T10:48:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Binary runs with `--seed`, `--threshold`, `--exclude-shells`, dgraph endpoint flag, `--out` and completes without crashing on a small reachable graph | ✓ VERIFIED | Live run `go run ./cmd/spam-explorer --seed d91191… --max-level 2 --threshold 2 --exclude-shells 1 --out <tmp>` exited 0, printed `done: leveled=46697 scored=46696 emitted=21880 deepest-level=2`. All six flags present in `-h` with documented defaults (threshold=2, exclude-shells=1, dgraph=localhost:9080, max-level=4, out=spam-candidates.jsonl). `cmd/spam-explorer/main.go:49-58` registers all flags; `main.go:60-112` wires the full spine. |
| 2 | Tool connects to Dgraph via dgo/v210 gRPC client and materializes enough follows/~follows adjacency to level + score every reachable account | ✓ VERIFIED | `internal/dgraph/client.go:32-46` dials via `grpc.NewClient` + `dgo.NewDgraphClient(api.NewDgraphClient(conn))` with `MaxCallRecvMsgSize(256<<20)`. `frontier.go:24-58` issues `frontier(func: uid(<csv>)){ uid pubkey follows{uid pubkey} }` per BFS level. Live run materialized 46,697 reachable accounts off an 851-follow seed at level 2. (Note: tool uses forward `follows` materialization + in-memory inversion, not a `~follows` query — this is the deliberate D-02 design; INGEST-03 wording allows `follows`/`~follows`.) |
| 3 | Each reachable account gets a level = shortest follow-hop distance from seed (seed=0, outward along follows) | ✓ VERIFIED | `internal/bfs/bfs.go:54-98` FIFO frontier: seed at level 0, first-discovery-wins (`if _, seen := levels[edge.UID]; !seen`), cycles terminate via the same visited check. Behavior-dependent invariant proven by `TestLevel_ShortestHop`, `TestLevel_DiamondFirstReachedWins`, `TestLevel_CycleTerminates` (all pass). Live run reports deepest-level=2 at `--max-level 2`. |
| 4 | Each scored account's valid_follower_count counts only followers strictly shallower (level(F) < level(T)); same-level + deeper excluded | ✓ VERIFIED | `internal/score/score.go:39-57` increments `vfc[target]` only on `lf < lt`. Behavior-dependent invariant proven by `TestScore_Upstream` (strict <), `TestScore_ExcludesSameLevelAndDeeper` (same-level and deeper not counted), `TestScore_SkipsTargetBeyondCap` (D-04), `TestScore_EveryNonSeedNodeHasParent` (vfc≥1 invariant) — all pass. |
| 5 | Output JSONL contains one {pubkey, valid_follower_count} per line for every account below N, seed + first k shells excluded | ✓ VERIFIED | `internal/output/jsonl.go:41-79`: survival = `level > k` AND `vfc < threshold` AND non-empty pubkey, sorted by pubkey. Live output: 21,880 lines, every line exactly `{pubkey,valid_follower_count}`, all pubkeys 64-hex, 0 empty/0x leaks, sorted=true, all vfc<2. Seed pubkey absent from output (grep=0); a level-1 direct followee absent (grep=0, shell excluded). |

**Score:** 5/5 truths verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `go.mod` | module spam-explorer, dgo/v210 + grpc v1.75.1 pinned | ✓ VERIFIED | `module spam-explorer`, go 1.24.1; both deps direct at the web-of-trust-matched versions |
| `Makefile` | build/test/lint/fmt/vet/tidy targets, `go test ./...` | ✓ VERIFIED | All targets present; version-injection LDFLAGS; `test: go test ./... -short -cover` |
| `internal/bfs/bfs.go` | Pure FIFO BFS leveling over injected expander (LEVEL-01) | ✓ VERIFIED | 98 lines; imports only `context`; injected FrontierExpander seam |
| `internal/score/score.go` | Pure in-memory follows-inversion scoring (SCORE-01/02, D-02) | ✓ VERIFIED | 57 lines; zero imports; carries D-02/D-04 correctness proof; strict-< |
| `internal/output/jsonl.go` | Sorted, k-shell+threshold filtered JSONL writer (OUT-01/02) | ✓ VERIFIED | 79 lines; only stdlib imports; sorted, filtered, empty-pubkey guard |
| `cmd/spam-explorer/main.go` | CLI entry: flag parse + orchestration | ✓ VERIFIED | `flag.Parse()`; full NewClient→ResolveSeed→bfs.Level→score.Score→output.Write spine |
| `internal/dgraph/client.go` | Read-only dgo/v210 client, MaxCallRecvMsgSize=256<<20 (INGEST-01) | ✓ VERIFIED | `256 << 20`; no Alter/Mutate/schema path |
| `internal/dgraph/resolve.go` | ResolveSeed eq(pubkey) with %q quoting + missing-seed guard | ✓ VERIFIED | `%q` interpolation, `NewReadOnlyTxn`, `seed pubkey %q not found` error |
| `internal/dgraph/frontier.go` | ExpandFrontier one BFS level per round-trip (INGEST-03, D-01) | ✓ VERIFIED | `func: uid(...)` + nested `follows{uid pubkey}`; returns `[]bfs.FrontierResult` (no adapter) |

### Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| internal/bfs/bfs.go | internal/score/score.go | levels+adjacency maps consumed unchanged | ✓ WIRED — `main.go:87-94` passes bfs output directly into `score.Score` |
| internal/score/score.go | internal/output/jsonl.go | vfc map filtered + emitted | ✓ WIRED — `main.go:94-97` `score.Score` → `output.Write` |
| cmd/main.go | internal/dgraph/frontier.go | `client.ExpandFrontier` injected as expander | ✓ WIRED — `main.go:87` injects into `bfs.Level` |
| internal/dgraph/frontier.go | Dgraph | `func: uid(...)` read-only DQL | ✓ WIRED — confirmed live (46,697 accounts returned) |

### Data-Flow Trace (Level 4)

Live run confirms real data flows seed → Dgraph read → levels → scores → JSONL: 46,697 leveled, 21,880 emitted candidates, every record a real 64-hex pubkey with vfc < threshold. No static/hardcoded data path.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Binary completes on live graph | `go run ./cmd/spam-explorer --seed d91191… --max-level 2 --out <tmp>` | exit 0, 21,880 lines | ✓ PASS |
| Output keys + pubkey format | node JSON scan over output | all `{pubkey,valid_follower_count}`, all 64-hex | ✓ PASS |
| Seed + level-1 shell excluded | grep seed/followee in output | both 0 | ✓ PASS |
| Sorted + threshold | node scan | sorted=true, all vfc<2 | ✓ PASS |
| Full test suite | `go test ./... -short -count=1` | all 5 packages ok | ✓ PASS |
| Purity invariant | `go list` imports | bfs=[context], score=[], output=[stdlib] | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| CLI-01 | 01-01, 01-02 | Flags --seed/--threshold/--exclude-shells/endpoint/--out | ✓ SATISFIED | All six flags wired with defaults; live run consumed them |
| INGEST-01 | 01-02 | Connect via dgo/v210 gRPC | ✓ SATISFIED | client.go dial idiom; live connection succeeded |
| INGEST-03 | 01-02 | Materialize follows/~follows adjacency to level + score | ✓ SATISFIED | ExpandFrontier materializes per-level; 46,697 accounts |
| LEVEL-01 | 01-01, 01-02 | Seed=0, BFS shortest-hop levels | ✓ SATISFIED | bfs.Level + tests + live deepest-level=2 |
| SCORE-01 | 01-01, 01-02 | Count followers level(F)<level(T) | ✓ SATISFIED | score.Score strict-< + TestScore_Upstream |
| SCORE-02 | 01-01, 01-02 | Same-level/deeper discarded | ✓ SATISFIED | TestScore_ExcludesSameLevelAndDeeper |
| OUT-01 | 01-01, 01-02 | Exclude seed + first k shells | ✓ SATISFIED | level>k filter; live seed+shell grep=0 |
| OUT-02 | 01-01, 01-02 | Emit JSONL {pubkey,vfc} for vfc<N | ✓ SATISFIED | jsonl.go + live output all vfc<2 |

All 8 PLAN-declared requirement IDs map to Phase 1 in REQUIREMENTS.md traceability and are satisfied. No orphaned Phase-1 requirements (LEVEL-02/INGEST-02 are Phase 2; CLI-02/OUT-03/OPS-01/OPS-02 are Phase 3). Minor doc note: REQUIREMENTS.md coverage tally says "Phase 1: 6" but the traceability table maps 8 IDs to Phase 1 — a stale count in REQUIREMENTS.md, not a coverage gap.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none) | — | No TBD/FIXME/XXX/HACK/PLACEHOLDER markers in any phase source file | ℹ️ Info | The Plan-01 main.go stub is fully replaced by Plan-02 orchestration (confirmed). The two `follower_count` grep hits are the JSON tag and a flag description — not query reads; no `~follows` query exists (D-02 satisfied). |

ℹ️ Minor (non-blocking): `output.Write` defers `w.Flush()` without capturing the flush error in the return value — a late buffered-write failure on the final flush could be silently dropped. Low risk for a local one-shot CLI writing pubkey+int records; the live run wrote 21,880 lines cleanly. Candidate for a Phase-3 hardening pass (OUT-03/OPS), not a Phase-1 goal blocker.

### Gaps Summary

None. The phase goal is fully achieved: a runnable CLI resolves a seed, BFS-levels the reachable subgraph one frontier at a time off live Dgraph, counts strictly-upstream valid followers via in-memory adjacency inversion, and writes a sorted, seed/shell- and threshold-filtered JSONL candidate file. All five success criteria are verified — the three behavior-dependent ones (state-transition leveling, strict-< scoring, end-to-end run) are confirmed both by dedicated passing tests and by a live end-to-end run against Dgraph v25.3.0 that reproduced the SUMMARY's reported numbers (46,697 leveled / 21,880 emitted) exactly.

---

_Verified: 2026-06-24T10:48:00Z_
_Verifier: Claude (gsd-verifier)_
