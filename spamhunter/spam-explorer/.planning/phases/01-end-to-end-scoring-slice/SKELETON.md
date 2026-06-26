# Walking Skeleton — spam-explorer

**Phase:** 1
**Generated:** 2026-06-24

## Capability Proven End-to-End

Given a trusted seed pubkey, the CLI reads the web-of-trust Dgraph over gRPC, BFS-levels a small bounded reachable subgraph, counts each account's strictly-upstream valid followers, and writes a threshold-filtered JSONL file of suspected spam candidates — the complete metric proven correct from `--seed` in to `spam-candidates.jsonl` out.

## Architectural Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language / toolchain | Go 1.24.1+ (own `go.mod`, `module spam-explorer`) | Matches web-of-trust, which owns Dgraph access; reuse the same `dgo/v210` + `grpc` it already builds against (D-05) |
| Dependencies | `dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d`, `grpc v1.75.1` (pinned verbatim from web-of-trust) | Same Dgraph server (`dgraph/standalone:v25.3.0`), same protocol; lowest-risk provenance — reuse a sibling's locked, building deps |
| Data layer | web-of-trust Dgraph, read-only over gRPC `localhost:9080` (`--dgraph` flag) | ID-only follow graph already crawled; this tool only reads (data-separation rule) |
| Dgraph client | Hand-written minimal read-only client (`NewClient` + `ResolveSeed` + `ExpandFrontier`) with `MaxCallRecvMsgSize=256<<20` | D-06 — web-of-trust's `dgraph.go` is write-path heavy; copy only the dial idiom + raised gRPC cap, never the mutation path |
| Ingestion shape | Frontier (level-by-level) BFS expansion: one `uid(set){follows{uid,pubkey}}` query per level | D-01 — identical shape to the Phase 2 production path (Phase 2 paginates *inside* `ExpandFrontier`); no `@recurse`, no whole-graph fetch |
| Scoring | In-memory inversion of the materialized `follows` adjacency (no `~follows` query) | D-02/D-04 — a valid follower F of T has level(F) < level(T), so F→T was already recorded during BFS; the inversion is provably complete |
| Output | Sorted, k-shell + threshold filtered JSONL via `bufio` + `json.Encoder` | OUT-01/OUT-02; sort-by-pubkey makes output byte-stable for golden tests and pre-positions Phase 2's determinism goal |
| Directory layout | `cmd/spam-explorer/main.go` + `internal/{dgraph,bfs,score,output}`; pure packages I/O-free | `internal/` (leaf tool, one binary); I/O isolated in `internal/dgraph`, algorithm pure and offline-unit-testable (Architectural Responsibility Map) |
| CLI | std-lib `flag`; six flags incl. temporary `--max-level` cap | CLI-01 + D-03; matches web-of-trust's clusterscan flag idiom |

## Stack Touched in Phase 1

- [x] Project scaffold (`go.mod`, `Makefile` build/test/lint/fmt/vet/tidy, `cmd/` + `internal/` split) — Plan 01
- [x] One real Dgraph read (`ResolveSeed` eq(pubkey,...) + `ExpandFrontier` uid(set){follows}) — Plan 02
- [x] The full algorithm wired end-to-end (BFS leveling → valid-follower scoring → JSONL filter) — Plans 01 + 02
- [x] One interactive entry point (the `spam-explorer` CLI binary, six flags) wired to the read path — Plans 01 + 02
- [x] Documented local full-stack run command: `go run ./cmd/spam-explorer --seed <pubkey> --max-level 2 --out /tmp/spam-candidates.jsonl` against `docker-compose -f ../docker-compose.dgraph.yml up -d`

## Out of Scope (Deferred to Later Slices)

- **Paginated DQL streaming** for the full ~1.54M-node graph — Phase 2 (INGEST-02). The pagination drops *inside* `ExpandFrontier`; `bfs.go` does not change.
- **Determinism-at-scale verification** (re-run same seed/snapshot ⇒ identical levels/scores) — Phase 2 (LEVEL-02).
- **Input validation** (well-formed seed hex, N>0, k>=0, writable out path) — Phase 3 (CLI-02). Phase 1 has only a missing-seed guard.
- **Unreachable-node error reporting** (accounts in the graph but not reachable from the seed) — Phase 3 (OPS-01).
- **Secret-safe progress/summary logging** beyond the basic summary line — Phase 3 (OUT-03, OPS-02).
- **`--max-level` retention/removal review** — revisit at Phase 2 (D-03). It is a temporary Phase-1 bounding knob, not a locked v1 requirement.
- Multi-signal intersection, multi-seed runs, denylist artifact emission — v2.

## Subsequent Slice Plan

Each later phase adds one vertical slice on top of this skeleton without altering its architectural decisions:

- **Phase 2 — Production-Scale Streaming:** add pagination inside `ExpandFrontier` so the same pipeline runs against the full ~1.54M-node graph with bounded memory; verify deterministic first-reached leveling at scale.
- **Phase 3 — Operational Correctness:** add input validation + clear non-zero exits, unreachable-node detection/logging, and secret-safe progress + final summary logging to the output path.
