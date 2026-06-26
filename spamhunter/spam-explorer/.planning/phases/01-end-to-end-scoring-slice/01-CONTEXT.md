# Phase 1: End-to-End Scoring Slice - Context

**Gathered:** 2026-06-23
**Status:** Ready for planning

<domain>
## Phase Boundary

Deliver a runnable Go CLI that, given a seed pubkey, runs the **complete scoring pipeline end-to-end on a small reachable subgraph**: CLI flags → connect to Dgraph (`dgo/v210` gRPC) → BFS-level outward along `follows` → count strictly-upstream valid followers → write threshold-filtered JSONL. The goal is to prove the metric **correct end-to-end**, not yet to scale it.

**Locked algorithm semantics (NOT up for discussion — see PROJECT.md "The Algorithm"):**
- Seed = level 0; BFS traverses outward along `follows` (seed → followees = level 1 → …); each node's level = shortest follow-hop distance from seed.
- Valid follower of `T`: a follower `F` with `level(F) < level(T)` (strictly upstream). Same-level and deeper followers are discarded.
- Output: JSONL `{pubkey, valid_follower_count}`, one per line, for every scored account with `valid_follower_count < N`, excluding the seed and its first `k` shells (levels `1..k`).
- Single seed per run.

**Deferred to later phases (explicitly OUT of Phase 1 scope):**
- Paginated DQL streaming for the full ~1.54M-node graph → **Phase 2** (INGEST-02, LEVEL-02 determinism-at-scale).
- Input validation, unreachable-node error reporting, secret-safe progress/summary logging → **Phase 3** (CLI-02, OUT-03, OPS-01, OPS-02).

**Phase 1 requirements:** CLI-01, INGEST-01, INGEST-03, LEVEL-01, SCORE-01, SCORE-02, OUT-01, OUT-02.
</domain>

<decisions>
## Implementation Decisions

### Subgraph Ingestion
- **D-01:** Materialize the reachable subgraph via **frontier (level-by-level) BFS expansion** — query `follows` for the current frontier's UIDs, advance to the next level, repeat. Chosen over a single whole-subgraph fetch or a bounded `@recurse` so the ingestion path has the **same shape as the Phase 2 production path** (Phase 2 just adds pagination *inside* each frontier batch — minimal rework, no rewrite).

### Valid-Follower Computation
- **D-02:** Compute `valid_follower_count` by **inverting the in-memory `follows` adjacency already materialized during BFS** — do **not** issue `~follows` queries. Justification (must be preserved for the planner): a valid follower `F` of `T` satisfies `level(F) < level(T)`, so `F` is provably reachable and the edge `F→T` was recorded when `F` was expanded during BFS. The materialized `follows` adjacency therefore contains **every** edge that could ever count as valid. This avoids redundant Dgraph round-trips and re-fetching data already in memory.

### Bounding the Slice
- **D-03:** Bound Phase-1 traversal with a **temporary `--max-level <M>` cap flag**. This is a Phase-1 bounding/safety + debug knob, **not a locked v1 requirement** — flag it for explicit removal/retention review when Phase 2 runs the full graph.
- **D-04 (correctness note — interaction of D-02 + D-03):** In-memory inversion remains **correct under the `--max-level M` cap**. Every scored node `T` sits at level ≤ M; all its valid followers have `level(F) < level(T) ≤ M`, so every such `F` was expanded and its `F→T` edge recorded before the cap took effect. The cap only drops level-`M+1` discoveries, which are never scored. The planner should NOT add `~follows` queries to "compensate" for the cap — it isn't needed.

### Project Layout & Dgraph Client
- **D-05:** Layout mirrors web-of-trust conventions: `cmd/spam-explorer/main.go` entry point + `internal/` packages, with its own `go.mod` (independent module — spam-explorer is a standalone monorepo subdirectory). Go 1.24.1+, Makefile following the stack's common targets.
- **D-06:** Hand-write a **minimal read-only `dgo/v210` client** (connect + read-only txn query only). Do **not** copy web-of-trust's `dgraph.go` wholesale — that file is write-path heavy (schema `Alter`, upserts, `EnsureSchema`, stale-pubkey selection) and this tool only reads. Reference web-of-trust's connection setup for the gRPC dial pattern (notably the raised `MaxCallRecvMsgSize` — large selection responses exceed the default 4MB gRPC cap).

### Claude's Discretion
- Frontier batch size, JSONL write buffering/flush cadence, output line ordering, internal data structures for the adjacency/level maps, and the exact summary-line wording are left to research/planning — none were constrained during discussion.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Algorithm & Requirements (this project)
- `.planning/PROJECT.md` §"The Algorithm (locked semantics)" — the exact BFS leveling + valid-follower rule + output filter this phase implements. Authoritative.
- `.planning/REQUIREMENTS.md` — Phase 1 requirements: CLI-01, INGEST-01, INGEST-03, LEVEL-01, SCORE-01, SCORE-02, OUT-01, OUT-02.
- `.planning/ROADMAP.md` §"Phase 1: End-to-End Scoring Slice" — 5 success criteria; phase boundary vs Phase 2/3.

### Dgraph access pattern (reference only — DO NOT modify web-of-trust)
- `../web-of-trust/pkg/dgraph/dgraph.go` — gRPC connection setup (`NewClient`, `grpc.NewClient` with `MaxCallRecvMsgSize = 256<<20`, `insecure` creds), `Profile` schema definition (`pubkey @index(exact) @upsert @unique`, `follows: [uid] @reverse`, `follower_count: int @index(int)`). Copy the *connection* idiom, not the write path.
- `../web-of-trust/pkg/dgraph/clusterscan.go` — read-only DQL idioms: `NewReadOnlyTxn()`, `ResolvePubkeysToUIDs` (seed pubkey → UID via `eq(pubkey, [...])`), UID-set query construction. Closest existing analog to this tool's read queries.

### Prior art (background — informs the metric, not the implementation)
- `../web-of-trust/.planning/spikes/spam-clusters/` — the 2026-06-23 spam-clusters spike (probe 04 "weakly-bridged pods"); the ad-hoc intuition this tool formalizes. Note the carried caveat: pure weak-bridge signal pollutes with legitimate newcomers (mitigated by `k`-shell exclusion + threshold `N`).

**Note:** `follower_count` floor in this graph is 1 (~49% of nodes at fc==1) — do NOT reuse `fc <= 1` as any kind of discriminator (per PROJECT.md Context).
</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- web-of-trust gRPC dial idiom (`pkg/dgraph/dgraph.go:NewClient`): raised `MaxCallRecvMsgSize` is load-bearing for large read responses — replicate it in the minimal client.
- web-of-trust `ResolvePubkeysToUIDs` (`pkg/dgraph/clusterscan.go`): exact pattern for turning the `--seed` pubkey into a Dgraph UID to start BFS; uses `eq(pubkey, [...])` + read-only txn.

### Established Patterns
- Stack convention: each subsystem is an independent Go module with `cmd/` + `internal/` (or `pkg/`) split and a Makefile exposing `build`/`test`/`lint`/`fmt`/`vet`/`tidy`. `quarantine-rescuer` is the closest analog — a one-shot CLI in the same stack.
- Read-only Dgraph access uses `c.dg.NewReadOnlyTxn()` with `defer txn.Discard(ctx)`.

### Integration Points
- Input: live web-of-trust Dgraph (gRPC `localhost:9080` default; HTTP `localhost:8080`), `Profile` graph with `follows` / `~follows` (`@reverse`) edges. ID-only — never pull event payloads (data-separation rule).
- Output: a JSONL file at the `--out` path. No write-back to Dgraph/StrFry/whitelist (out of scope).
</code_context>

<specifics>
## Specific Ideas

- The valid-follower-via-inversion argument (D-02/D-04) is the load-bearing design insight for this phase — the user wants the simpler, query-light path, with the correctness proof captured so the planner doesn't reintroduce `~follows` queries.
</specifics>

<deferred>
## Deferred Ideas

- **Paginated DQL streaming** for the full ~1.54M-node graph — Phase 2 (frontier-expansion shape from D-01 is the seam where pagination drops in).
- **Determinism-at-scale verification** (re-run same seed/snapshot ⇒ identical levels/scores) — Phase 2.
- **Input validation, unreachable-node error reporting, secret-safe logging** — Phase 3.
- **`--max-level` flag retention/removal review** — revisit at Phase 2 (D-03).
- Multi-signal intersection, multi-seed runs, denylist artifact emission — v2 (per REQUIREMENTS.md, out of milestone scope).
</deferred>

---

*Phase: 1-End-to-End Scoring Slice*
*Context gathered: 2026-06-23*
