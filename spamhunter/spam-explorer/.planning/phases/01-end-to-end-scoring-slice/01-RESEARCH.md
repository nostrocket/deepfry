# Phase 1: End-to-End Scoring Slice - Research

**Researched:** 2026-06-23
**Domain:** Go CLI + Dgraph (`dgo/v210` gRPC) read-only graph traversal — BFS leveling + valid-follower scoring + JSONL output
**Confidence:** HIGH (every mechanic verified against sibling production code in this monorepo; nothing fetched blind from a registry)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01:** Materialize the reachable subgraph via **frontier (level-by-level) BFS expansion** — query `follows` for the current frontier's UIDs, advance to the next level, repeat. Chosen over a single whole-subgraph fetch or a bounded `@recurse` so the ingestion path has the **same shape as the Phase 2 production path** (Phase 2 just adds pagination *inside* each frontier batch — minimal rework, no rewrite).
- **D-02:** Compute `valid_follower_count` by **inverting the in-memory `follows` adjacency already materialized during BFS** — do **not** issue `~follows` queries. A valid follower `F` of `T` satisfies `level(F) < level(T)`, so `F` is provably reachable and the edge `F→T` was recorded when `F` was expanded during BFS. The materialized `follows` adjacency therefore contains **every** edge that could ever count as valid.
- **D-03:** Bound Phase-1 traversal with a **temporary `--max-level <M>` cap flag**. Phase-1 bounding/safety + debug knob, **not** a locked v1 requirement — flag for explicit removal/retention review at Phase 2.
- **D-04 (correctness note — D-02 + D-03 interaction):** In-memory inversion remains **correct under the `--max-level M` cap**. Every scored node `T` sits at level ≤ M; all its valid followers have `level(F) < level(T) ≤ M`, so every such `F` was expanded and its `F→T` edge recorded before the cap took effect. Do **NOT** add `~follows` queries to "compensate" for the cap.
- **D-05:** Layout mirrors web-of-trust conventions: `cmd/spam-explorer/main.go` entry point + `internal/` packages, with its own `go.mod` (independent module). Go 1.24.1+, Makefile following the stack's common targets.
- **D-06:** Hand-write a **minimal read-only `dgo/v210` client** (connect + read-only txn query only). Do **not** copy web-of-trust's `dgraph.go` wholesale — that file is write-path heavy. Reference web-of-trust's connection setup for the gRPC dial pattern (notably the raised `MaxCallRecvMsgSize`).

### Claude's Discretion

- Frontier batch size, JSONL write buffering/flush cadence, output line ordering, internal data structures for the adjacency/level maps, and the exact summary-line wording. None were constrained during discussion.

### Deferred Ideas (OUT OF SCOPE)

- **Paginated DQL streaming** for the full ~1.54M-node graph — Phase 2 (the frontier-expansion shape from D-01 is the seam where pagination drops in).
- **Determinism-at-scale verification** — Phase 2.
- **Input validation, unreachable-node error reporting, secret-safe logging** — Phase 3.
- **`--max-level` flag retention/removal review** — Phase 2 (D-03).
- Multi-signal intersection, multi-seed runs, denylist artifact emission — v2.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| CLI-01 | User supplies `--seed`, `--threshold <N>`, `--exclude-shells <k>`, a Dgraph endpoint flag, `--out <path>` as flags | Std-lib `flag` package; mirrors clusterscan `flag.Int`/`flag.String` idiom (Code Examples §CLI). Add `--max-level` per D-03. |
| INGEST-01 | Connect to web-of-trust Dgraph via `dgo/v210` gRPC client | `NewClient` dial idiom with `MaxCallRecvMsgSize=256<<20` + insecure creds — extracted verbatim below (§Standard Stack, Code Examples §Client). |
| INGEST-03 | Materialize enough `follows`/`~follows` adjacency to BFS-level and score every reachable account | Frontier BFS `uid(...) { follows {uid} }` query (D-01); `~follows` satisfied by **in-memory inversion** (D-02), no reverse query. |
| LEVEL-01 | Seed = level 0; BFS outward along `follows`; each node level = shortest follow-hop distance | Standard FIFO BFS over the materialized adjacency; first-reached wins (BFS invariant). |
| SCORE-01 | For each `T`, count followers `F` with `level(F) < level(T)` as `valid_follower_count` | In-memory inversion of `follows` adjacency + level comparison (Code Examples §Scoring). |
| SCORE-02 | Followers at same level or deeper discarded | Same inversion pass — strict `<` comparison only. |
| OUT-01 | Exclude seed + first `k` shells (levels `1..k`) from scoring output | Filter `level(T) > k` before emit (and exclude seed at level 0). |
| OUT-02 | Emit JSONL `{pubkey, valid_follower_count}` per line for every scored account with `vfc < N` | Buffered `bufio.Writer` + `encoding/json.Encoder` streaming (Code Examples §Output). |
</phase_requirements>

## Summary

Phase 1 is a Walking Skeleton: a single-binary Go CLI that connects once to the web-of-trust Dgraph over gRPC, BFS-levels a bounded reachable subgraph from a seed, scores each node by strictly-upstream follower count, and streams a threshold-filtered JSONL file. **Every mechanic this phase needs already exists, verified, in the sibling `web-of-trust` module in this same monorepo** — the connection dial, the read-only transaction pattern, the `eq(pubkey, [...])` resolution query, and the `uid(...)` + `count(follows)` query shapes are all directly transcribable. There is no novel library research and no registry-fetch risk: `dgo/v210` and `grpc` are already pinned and building in a production sibling.

The load-bearing design insight (D-02/D-04) is that **valid-follower counting needs no `~follows` query at all**. Because a valid follower of `T` is by definition strictly upstream of `T`, it was necessarily expanded earlier in the BFS, so the `F→T` edge is already in the in-memory `follows` adjacency. Inverting that adjacency in memory yields every count that could ever be valid. This keeps the entire scoring pass query-free after ingestion and removes any temptation to issue per-node reverse-edge queries.

The two real landmines are mechanical, not algorithmic: (1) the gRPC default 4 MB receive cap — solved by replicating web-of-trust's `MaxCallRecvMsgSize = 256<<20`; and (2) the UID-vs-pubkey distinction — Dgraph traverses on internal UIDs, so BFS frontiers must carry UIDs while output carries pubkeys, requiring both fields in every selection block.

**Primary recommendation:** Scaffold an independent Go 1.24.1 module mirroring `quarantine-rescuer` (one-shot CLI analog) and `web-of-trust` (Dgraph access). Hand-write a ~60-line read-only client (`NewClient` + one `Query` method), keep BFS/scoring/output in `internal/` packages with pure-function seams, and pin `dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d` + `grpc v1.75.1` to match the sibling exactly.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| CLI flag parsing / orchestration | `cmd/spam-explorer/main.go` | — | Entry point; std-lib `flag`; wires the pipeline, owns `os.Exit` codes |
| Dgraph connection + read-only query | `internal/dgraph` (minimal client) | — | Owns gRPC dial, `MaxCallRecvMsgSize`, read-only txn; the ONLY tier that talks to Dgraph |
| Seed pubkey → UID resolution | `internal/dgraph` | — | `eq(pubkey, [...])` query; UID is the BFS traversal key |
| Frontier expansion (one level) | `internal/dgraph` (query) + `internal/bfs` (orchestration) | — | Query returns one level's `follows{uid,pubkey}`; bfs decides next frontier |
| BFS leveling | `internal/bfs` (pure) | — | FIFO over materialized adjacency; first-reached wins; no I/O |
| Valid-follower scoring | `internal/score` (pure) | — | In-memory inversion of `follows` adjacency + level compare; no I/O (D-02) |
| Threshold + k-shell filter | `internal/score` or `internal/output` (pure) | — | `level > k` AND `vfc < N`; pure predicate |
| JSONL streaming write | `internal/output` | — | `bufio.Writer` + `json.Encoder`; owns file lifecycle |

**Why this matters:** keeping Dgraph I/O isolated in `internal/dgraph` and BFS/scoring/filter as pure functions means Phase 2 (pagination) changes only the query/frontier loop, and the scoring + output tiers are unit-testable without a live Dgraph — directly enabling the Wave 0 test plan below.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/dgraph-io/dgo/v210` | `v210.0.0-20230328113526-b66f8ae53a2d` | Dgraph gRPC client + DQL query API | [VERIFIED: web-of-trust go.mod + go list -m] Already pinned and building in the sibling module that owns Dgraph access; the project constraint mandates reuse |
| `google.golang.org/grpc` | `v1.75.1` | Transport for dgo; `MaxCallRecvMsgSize` dial option | [VERIFIED: web-of-trust go.mod] Direct dep of dgo dial path; pin to match sibling |
| `google.golang.org/grpc/credentials/insecure` | (part of grpc) | Insecure transport creds for local Dgraph | [VERIFIED: dgraph.go:16,45] Local-only gRPC at `localhost:9080`; no TLS in this stack |
| `github.com/dgraph-io/dgo/v210/protos/api` | (part of dgo) | `api.NewDgraphClient`, `api.Operation` | [VERIFIED: dgraph.go:13,52] Required to construct the dgo client from a grpc conn |

### Supporting (Go standard library — no external deps)

| Package | Purpose | When to Use |
|---------|---------|-------------|
| `flag` | CLI flag parsing | CLI-01; mirrors clusterscan `flag.Int`/`flag.String` |
| `encoding/json` | JSONL marshaling + unmarshaling Dgraph responses | OUT-02 emit + parse query responses |
| `bufio` | Buffered output writer | OUT-02 streaming write (Claude's discretion on flush cadence) |
| `os` | File creation, exit codes, stderr logging | `--out` file, exit codes |
| `context` | Per-query context (no timeout needed Phase 1; add in Phase 3) | Passed to every `txn.Query` |
| `log` | Progress + summary to stderr | Summary line (OUT-03 is Phase 3, but a basic summary is fine) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-written minimal client (D-06) | Copy web-of-trust `dgraph.go` wholesale | REJECTED by D-06 — that file is ~1885 lines of write-path machinery (Alter, upserts, backfill); this tool only reads. Copy the dial idiom, not the file. |
| `flag` (std lib) | `spf13/pflag` / `cobra` | web-of-trust uses std `flag` in its cmds; single-command tool needs nothing heavier. Std `flag` matches stack convention. |
| In-memory inversion (D-02) | `~follows` reverse-edge DQL queries | REJECTED by D-02/D-04 — redundant round-trips re-fetching data already in memory; correctness proof below shows inversion is complete. |
| Frontier BFS (D-01) | `@recurse(depth, loop:false)` single query | REJECTED by D-01 — `@recurse` (used by web-of-trust `ClusterBeneath`) returns a nested tree in one shot but has no pagination seam; frontier-by-frontier matches the Phase 2 production path. |

**Installation:**
```bash
# In the new spam-explorer/ module root:
go mod init spam-explorer
go get github.com/dgraph-io/dgo/v210@v210.0.0-20230328113526-b66f8ae53a2d
go get google.golang.org/grpc@v1.75.1
go mod tidy
```

**Version verification:** [VERIFIED: `go list -m` in /Users/g/git/deepfry/web-of-trust] —
`github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d` and
`google.golang.org/grpc v1.75.1` are the exact versions resolved and building in the sibling production module today. Pinning to these guarantees protocol compatibility with the same Dgraph server.

**Dgraph server:** [VERIFIED: docker-compose.dgraph.yml] `dgraph/standalone:v25.3.0`. The `dgo/v210` client speaks to it over gRPC on `localhost:9080` (HTTP/GraphQL on `8080`).

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `github.com/dgraph-io/dgo/v210` | Go module (pkg.go.dev) | established (v210 line, 2023 pin) | n/a (Go) | github.com/dgraph-io/dgo | OK | Approved — already in sibling production build |
| `google.golang.org/grpc` | Go module | established | n/a (Go) | github.com/grpc/grpc-go | OK | Approved — already in sibling production build |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none.

> Both packages are **already pinned, downloaded, and building** in the `web-of-trust` sibling module within this same monorepo. They are not being introduced from a blind registry lookup — they are reused via `go.sum` hashes that already exist in the stack. This is the lowest-risk possible provenance: reuse of a working sibling's locked dependency. No `checkpoint:human-verify` gate is required.

## Architecture Patterns

### System Architecture Diagram

```
  --seed <pubkey> ──┐
  --threshold N ─────┤
  --exclude-shells k ┤   [cmd/spam-explorer/main.go]
  --dgraph <addr> ───┤        flag parse + orchestrate
  --max-level M ─────┤
  --out <path> ──────┘
          │
          ▼
  ┌─────────────────────┐    eq(pubkey,[seed])     ┌──────────────────┐
  │ internal/dgraph     │ ───────────────────────▶ │  Dgraph          │
  │  NewClient (gRPC,   │ ◀─────────────────────── │  standalone      │
  │  MaxCallRecvMsgSize)│    seedUID                │  v25.3.0         │
  │  ResolveSeed()      │                          │  (localhost:9080)│
  │  ExpandFrontier()   │ ──uid(frontier){follows  │  Profile graph   │
  └─────────────────────┘    {uid,pubkey}}──────▶  │  follows @reverse│
          │              ◀── next level's edges ─── └──────────────────┘
          │  (per level, until frontier empty OR level == M)
          ▼
  ┌─────────────────────┐
  │ internal/bfs (pure) │   level[uid] = shortest hop; first-reached wins
  │  Level(seed, adj)   │   builds: levels map + follows adjacency (uid→[]uid)
  └─────────────────────┘
          │  levels{}, adjacency{}, uid→pubkey{}
          ▼
  ┌─────────────────────┐
  │ internal/score(pure)│   invert adjacency in memory (D-02):
  │  Score(levels, adj) │   for edge F→T: if level(F) < level(T) ⇒ vfc(T)++
  └─────────────────────┘   (NO ~follows query)
          │  vfc{} per scored node
          ▼
  ┌─────────────────────┐
  │ internal/output     │   filter: level(T) > k  AND  vfc(T) < N
  │  Write(filtered)    │   emit {pubkey, valid_follower_count} JSONL
  └─────────────────────┘ ──▶  --out file  +  stderr summary
```

A reader can trace the primary use case: a seed enters as a flag → resolved to a UID → BFS expands level by level pulling `follows` edges → adjacency + levels accumulate in memory → scoring inverts the adjacency and counts strictly-upstream followers → filter + JSONL write.

### Recommended Project Structure
```
spam-explorer/
├── go.mod                       # module spam-explorer, go 1.24.1
├── go.sum
├── Makefile                     # build/run/test/fmt/vet/tidy/clean/lint/lint-fix (+ build-alpine/linux)
├── cmd/
│   └── spam-explorer/
│       └── main.go              # flag parse, wire pipeline, exit codes, summary
└── internal/
    ├── dgraph/
    │   ├── client.go            # NewClient (dial + MaxCallRecvMsgSize), Close
    │   ├── resolve.go           # ResolveSeed: eq(pubkey,[...]) → UID
    │   └── frontier.go          # ExpandFrontier: uid(set){follows{uid,pubkey}}
    ├── bfs/
    │   └── bfs.go               # Level(): pure FIFO BFS over driver-fed frontiers
    ├── score/
    │   └── score.go             # Score(): pure in-memory adjacency inversion (D-02)
    └── output/
        └── jsonl.go             # Write(): bufio + json.Encoder streaming
```

> `internal/` (not `pkg/`) because nothing here is meant for external import — this is a leaf tool. web-of-trust uses `pkg/` because its packages are shared across multiple `cmd/` binaries; spam-explorer has one binary, so `internal/` is the more honest choice and still matches the cmd/+package split convention (quarantine-rescuer also uses `internal/`). [VERIFIED: ls quarantine-rescuer/]

### Pattern 1: Minimal read-only Dgraph client (D-06)
**What:** A ~60-line client wrapping a single `*grpc.ClientConn` + `*dgo.Dgraph`, exposing only `NewClient`, `Close`, and read-query helpers. No `Alter`, no mutations, no schema.
**When to use:** Always for this tool — it only reads.
**Example:** see Code Examples §Client (extracted verbatim from web-of-trust dgraph.go:37-60).

### Pattern 2: Frontier (level-by-level) BFS (D-01)
**What:** Maintain a `current` frontier of UIDs at level L. Issue ONE query returning `follows{uid,pubkey}` for the whole frontier. Any followee UID not yet leveled gets level L+1 and joins the `next` frontier. Repeat until `next` is empty or `L+1 > M`. Record every `follows` edge into the in-memory adjacency as you go.
**When to use:** The whole ingestion path.
**Phase 2 seam:** The single `ExpandFrontier(uids)` call is exactly where pagination drops in — Phase 2 splits a large `uids` set into batches (`first:`/`after:` or chunked `uid(...)` blocks) **inside** this function. Nothing in `bfs.go` changes. This is the explicit reason D-01 chose frontier-expansion over `@recurse`.
**Example:** see Code Examples §Frontier.

### Pattern 3: In-memory valid-follower inversion (D-02)
**What:** After BFS, walk every recorded `follows` edge `F→T`. If `level(F) < level(T)`, increment `vfc[T]`. No reverse query.
**When to use:** The entire scoring pass.
**Example:** see Code Examples §Scoring.

### Anti-Patterns to Avoid
- **Issuing `~follows` queries to count followers:** Forbidden by D-02. It re-fetches edges already in memory and adds N round-trips. The inversion is provably complete (see §Don't Hand-Roll correctness proof).
- **Adding `~follows` to "compensate" for the `--max-level` cap:** Forbidden by D-04. The cap only drops level-(M+1) *discoveries*, which are never scored; every scored node's valid followers are at level < its own level ≤ M, hence already materialized.
- **Using `@recurse` for ingestion:** Forbidden by D-01 — no pagination seam for Phase 2.
- **Keying BFS on pubkey instead of UID:** Dgraph traverses on internal UIDs. The frontier MUST be UIDs; carry pubkey only for output. (See Landmine §UID-vs-pubkey.)
- **Reusing `fc <= 1` as a discriminator:** The graph's `follower_count` floor is 1 (~49% of nodes). This tool's whole point is that `valid_follower_count` (the new metric) ≠ `follower_count` (the stored predicate). Do not conflate them or fall back to `fc`.
- **Loading the whole graph in one query:** Phase 1 is bounded by `--max-level M` on a *small* reachable subgraph; the full 1.54M-node streaming is Phase 2. But even Phase 1 must not write a single unbounded `func: has(pubkey)` fetch.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| gRPC connection + DQL transport | Custom HTTP/JSON to `:8080` | `dgo/v210` + `grpc` (sibling-pinned) | Protocol, retries, message framing already handled; HTTP path loses the gRPC `MaxCallRecvMsgSize` control |
| Seed pubkey → UID lookup | New query design | Transcribe `ResolvePubkeysToUIDs` (clusterscan.go:45-88) | Exact `eq(pubkey, [...])` idiom proven against this graph |
| Reverse-follower enumeration | `~follows` query per node | In-memory inversion of materialized `follows` (D-02) | Correctness proof below; zero extra round-trips |

**Key insight (the D-02/D-04 correctness proof — preserve verbatim for the planner):**
A valid follower `F` of target `T` is defined as a follower with `level(F) < level(T)`. By the BFS leveling rule, every node at level `L` was discovered by expanding the `follows` edges of nodes at level `L-1` (or shallower). Therefore, for any edge `F → T` where `level(F) < level(T)`, the node `F` was expanded **before** `T`'s level was complete, and at that moment its `follows` set — which includes `T` — was read and recorded into the in-memory adjacency. Consequently the materialized `follows` adjacency contains **every** edge that could ever satisfy the valid-follower predicate. Inverting it in memory (`F→T` becomes a tally on `T` whenever `level(F) < level(T)`) yields the exact `valid_follower_count`. Edges from same-level or deeper followers are simply not counted (strict `<`), satisfying SCORE-02. The `--max-level M` cap (D-03) cannot break this: a scored node `T` has `level(T) ≤ M`, so all its valid followers have `level(F) < level(T) ≤ M` and were expanded before the cap; only level-(M+1) discoveries are dropped and those are never scored. **No `~follows` query is needed under any Phase-1 configuration.**

## Runtime State Inventory

> Greenfield phase — `spam-explorer/` currently contains only `.claude/` and `.planning/`. No prior code, no stored state, no OS registrations, no secrets owned by this tool. This is a new module, not a rename/refactor. **Section omitted as not applicable (no runtime state to migrate).**

## Common Pitfalls

### Pitfall 1: gRPC default 4 MB receive cap (ResourceExhausted)
**What goes wrong:** A frontier query that returns many `follows` edges exceeds gRPC's default 4 MB receive limit and fails with `ResourceExhausted`.
**Why it happens:** Even a modestly fan-out frontier (a few thousand accounts each following hundreds) blows past 4 MB. web-of-trust documents this exact failure (dgraph.go:39-42).
**How to avoid:** Replicate the dial option verbatim: `grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(256 << 20))`. [VERIFIED: dgraph.go:42-47]
**Warning signs:** `rpc error: code = ResourceExhausted desc = grpc: received message larger than max`.

### Pitfall 2: Confusing UID with pubkey
**What goes wrong:** BFS traverses on pubkeys, or output emits UIDs.
**Why it happens:** Dgraph's native node identity is the internal UID (`0x...`); `pubkey` is an indexed string predicate. `follows` edges point to UIDs.
**How to avoid:** Frontier sets and the `levels`/`adjacency` maps are keyed by **UID**. Every selection block fetches **both** `uid` and `pubkey` (`follows { uid pubkey }`), so a `uid→pubkey` map can be built for output. Resolve `--seed` pubkey → seed UID first (the `eq(pubkey,[...])` query). Emit `pubkey` in JSONL, never UID.
**Warning signs:** JSONL lines containing `0x...` hex UIDs instead of 64-char pubkeys; empty BFS because a pubkey was passed to `uid(...)`.

### Pitfall 3: Frontier never terminates / re-visits nodes
**What goes wrong:** Cycles in the follow graph (A follows B follows A) cause infinite expansion, or a node is re-leveled to a deeper level.
**Why it happens:** Naive BFS without a visited set, or assigning level on dequeue instead of on first discovery.
**How to avoid:** A node enters `levels` exactly once, at first discovery (shallowest = BFS guarantee, LEVEL-01). When expanding a frontier, only followees **absent** from `levels` get level L+1 and join `next`. Termination: loop while `next` is non-empty **and** (`L+1 <= M` when `--max-level` set). An empty `next` always terminates (seed network exhausted within the cap).
**Warning signs:** Process hangs; a node's level changes across iterations.

### Pitfall 4: Seed not present in the graph
**What goes wrong:** `ResolveSeed` returns no UID; BFS starts from nothing and emits an empty file with no signal.
**Why it happens:** Typo in `--seed`, or the seed hasn't been crawled.
**How to avoid:** After `eq(pubkey,[seed])`, check the result is non-empty; if empty, `log.Fatalf` with a clear message and non-zero exit (mirrors clusterscan.go:71-73 `if len(seedUIDs) == 0`). Full input validation is Phase 3 (CLI-02), but a missing-seed guard is cheap and prevents a silent empty run.
**Warning signs:** Zero-line output, no error.

### Pitfall 5: Treating `follower_count` predicate as the metric
**What goes wrong:** Reaching for the stored `follower_count` predicate (or `fc <= 1`) as a shortcut.
**Why it happens:** It's right there in the schema and the spike used it.
**How to avoid:** This tool computes a **new** metric (`valid_follower_count`) entirely from BFS levels + in-memory inversion. The stored `follower_count` predicate is irrelevant to the score and has a floor of 1 (~49% of nodes) so it cannot discriminate. Do not query it for scoring.
**Warning signs:** Any reference to `follower_count` in the scoring path; `fc` comparisons.

## Code Examples

Verified patterns transcribed from web-of-trust sibling source.

### §Client — minimal read-only client (D-06)
```go
// Source: web-of-trust/pkg/dgraph/dgraph.go:37-60 (connection idiom only; write path dropped)
package dgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	dg   *dgo.Dgraph
	conn *grpc.ClientConn
}

func NewClient(addr string) (*Client, error) {
	// LOAD-BEARING: a frontier query's follows-edge response can exceed gRPC's
	// default 4MB receive cap. Raise it (matches web-of-trust). 256 MiB.
	const maxRecvMsgSize = 256 << 20
	conn, err := grpc.NewClient(
		addr, // "localhost:9080"
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil {
		return nil, err
	}
	return &Client{dg: dgo.NewDgraphClient(api.NewDgraphClient(conn)), conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }
```

### §Resolve — seed pubkey → UID
```go
// Source: web-of-trust/pkg/dgraph/clusterscan.go:45-88 (ResolvePubkeysToUIDs), single-seed specialization
func (c *Client) ResolveSeed(ctx context.Context, seed string) (string, error) {
	query := fmt.Sprintf(`{ node(func: eq(pubkey, %q), first: 1) { uid pubkey } }`, seed)
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, query)
	if err != nil {
		return "", fmt.Errorf("resolve seed failed: %w", err)
	}
	var result struct {
		Node []struct {
			UID    string `json:"uid"`
			Pubkey string `json:"pubkey"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return "", fmt.Errorf("unmarshal seed failed: %w", err)
	}
	if len(result.Node) == 0 {
		return "", fmt.Errorf("seed pubkey %q not found in graph", seed)
	}
	return result.Node[0].UID, nil
}
```

### §Frontier — expand one BFS level in one round-trip (D-01)
```go
// Returns, for every UID in the frontier, its follows edges (uid + pubkey).
// Phase 2 SEAM: split `uids` into batches inside THIS function; bfs.go is untouched.
// Query shape verified against the uid(...) + count(follows) idioms in
// clusterscan.go (DegreesForUIDs:200-206, ExpandTrustedSet uid(...) blocks).

type FrontierResult struct {
	UID     string `json:"uid"`
	Pubkey  string `json:"pubkey"`
	Follows []struct {
		UID    string `json:"uid"`
		Pubkey string `json:"pubkey"`
	} `json:"follows"`
}

func (c *Client) ExpandFrontier(ctx context.Context, uids []string) ([]FrontierResult, error) {
	// uid(...) takes a comma-separated UID set; one block returns the whole frontier.
	query := fmt.Sprintf(`
	{
		frontier(func: uid(%s)) {
			uid
			pubkey
			follows {
				uid
				pubkey
			}
		}
	}`, strings.Join(uids, ", "))

	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("expand frontier failed: %w", err)
	}
	var result struct {
		Frontier []FrontierResult `json:"frontier"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal frontier failed: %w", err)
	}
	return result.Frontier, nil
}
```
> The `uid(<csv>)` root func and the nested `follows { uid pubkey }` block are the same primitives web-of-trust uses (`func: uid(%s)` in DegreesForUIDs/ExpandTrustedSet, and `follows { uid pubkey }` in the AddFollowers follower query, dgraph.go:361-365). One query returns the entire current frontier's outgoing edges — exactly D-01's "in one round-trip."

### §Scoring — in-memory adjacency inversion (D-02), pure
```go
// levels: uid -> BFS level (0 = seed). adjacency: F-uid -> []T-uid (follows edges, as materialized).
// Returns: uid -> valid_follower_count. No Dgraph access.
func Score(levels map[string]int, adjacency map[string][]string) map[string]int {
	vfc := make(map[string]int, len(levels))
	for follower, followees := range adjacency {
		lf, ok := levels[follower]
		if !ok {
			continue // follower never leveled (shouldn't happen for materialized edges)
		}
		for _, target := range followees {
			lt, ok := levels[target]
			if !ok {
				continue // target beyond the cap; never scored (D-04)
			}
			if lf < lt { // strictly upstream — SCORE-01; same/deeper discarded — SCORE-02
				vfc[target]++
			}
		}
	}
	return vfc
}
```

### §Output — threshold + k-shell filter, streaming JSONL (OUT-01/OUT-02)
```go
type Record struct {
	Pubkey             string `json:"pubkey"`
	ValidFollowerCount int    `json:"valid_follower_count"`
}

// scored: uid -> vfc; levels: uid -> level; pubkeys: uid -> pubkey.
func Write(path string, scored, levels map[string]int, pubkeys map[string]string, threshold, k int) (emitted int, err error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create output: %w", err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	enc := json.NewEncoder(w) // Encode writes one object + newline = JSONL
	for uid, vfc := range scored {
		lvl := levels[uid]
		if lvl <= k {        // OUT-01: exclude seed (level 0) and shells 1..k
			continue
		}
		if vfc >= threshold { // OUT-02: emit only vfc < N
			continue
		}
		if err := enc.Encode(Record{Pubkey: pubkeys[uid], ValidFollowerCount: vfc}); err != nil {
			return emitted, fmt.Errorf("encode record: %w", err)
		}
		emitted++
	}
	return emitted, nil
}
```
> `json.Encoder.Encode` appends a `\n` after each object — that is exactly JSONL. Output line ordering is Claude's discretion (map iteration is unordered; sort by pubkey or vfc if a deterministic file is wanted for tests).

### §CLI — flag parsing (CLI-01 + D-03 --max-level)
```go
// Source idiom: web-of-trust/cmd/clusterscan/main.go:46-54
seed := flag.String("seed", "", "trusted seed pubkey (64-char hex) to anchor BFS leveling")
threshold := flag.Int("threshold", 2, "emit accounts with valid_follower_count < N")
excludeShells := flag.Int("exclude-shells", 1, "exclude the seed and its first k shells (levels 1..k)")
dgraphAddr := flag.String("dgraph", "localhost:9080", "Dgraph gRPC endpoint")
maxLevel := flag.Int("max-level", 4, "Phase-1 bounding cap: stop BFS past this level (D-03; review at Phase 2)")
out := flag.String("out", "spam-candidates.jsonl", "output JSONL path")
flag.Parse()
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `grpc.Dial` (deprecated) | `grpc.NewClient` | grpc-go ~v1.63+ | web-of-trust already uses `grpc.NewClient` (dgraph.go:43); transcribe that, not `Dial` |
| Per-node `count(~follows)` for follower counts | Stored `follower_count` predicate (web-of-trust) / **in-memory inversion (this tool)** | web-of-trust Phase 14 | This tool sidesteps both — it computes a *different* metric from BFS levels |

**Deprecated/outdated:**
- `grpc.Dial` / `grpc.DialContext`: use `grpc.NewClient`. [VERIFIED: dgraph.go:43 uses NewClient]
- Do not introduce `dgo` v200 or a non-`/v210` import path — the stack is pinned to `/v210`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `dgraph/standalone:v25.3.0` accepts `dgo/v210` gRPC queries with the `uid(...) { follows { uid pubkey } }` shape | Frontier | LOW — web-of-trust runs identical query primitives against the same server today; if a frontier query were rejected the sibling crawler would already be broken |
| A2 | A single `uid(<csv>)` frontier query stays under 256 MiB for a Phase-1 `--max-level`-bounded subgraph | Frontier / Landmine 1 | LOW — Phase 1 is explicitly a *small* bounded slice; the 256 MiB cap is the same headroom web-of-trust uses for far larger selection batches. Phase 2 adds intra-frontier pagination regardless |

**Note:** No claim here is `[ASSUMED]` from training data alone — A1/A2 are inferences from verified sibling behavior, flagged only because they haven't been executed against a live server *in this session*. The planner may add a single smoke-run task to confirm A1 end-to-end (it doubles as the phase's acceptance check).

## Open Questions

1. **Default flag values (`--threshold`, `--exclude-shells`, `--max-level`).**
   - What we know: CLI-01 requires the flags exist; the spike used trust threshold T=200/500 but that's a *different* metric (`follower_count`, not `valid_follower_count`).
   - What's unclear: sensible defaults for the new metric haven't been calibrated (calibration is implied future work).
   - Recommendation: ship conservative placeholders (`threshold=2`, `exclude-shells=1`, `max-level=4`) and treat tuning as a runtime concern, not a Phase-1 blocker. The metric guarantees every scored node has ≥1 valid follower (BFS-tree parent), so `< 2` cleanly isolates "reached only through a single weak bridge."

2. **Output ordering for reproducible test fixtures.**
   - What we know: ordering is Claude's discretion; map iteration is non-deterministic.
   - What's unclear: whether the planner wants byte-stable output for golden-file tests.
   - Recommendation: sort emitted records by pubkey before writing — cheap, makes Wave 0 golden-file tests trivial, and pre-positions LEVEL-02's determinism goal (Phase 2).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | build/test | ✓ | go1.26.1 (≥1.24.1 required) | — |
| Dgraph server (gRPC :9080) | INGEST-01 live run | runtime | dgraph/standalone:v25.3.0 (per compose) | Unit tests run without it (pure bfs/score/output); only the smoke run needs it |
| `dgo/v210` + `grpc` modules | INGEST-01 | ✓ (resolvable; pinned in sibling) | dgo v210.0.0-2023…, grpc v1.75.1 | — |
| golangci-lint | `make lint` | optional | — | Makefile `lint` target warns-and-continues (stack convention) |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** A live Dgraph is needed only for the end-to-end smoke run; all `internal/bfs`, `internal/score`, `internal/output` logic is pure and unit-testable offline. `internal/dgraph` query *shapes* are unit-testable as format strings (web-of-trust does this — see `frontierStaleQueryFmt` extracted as a package constant for assertion without a live server).

## Validation Architecture

> `.planning/config.json` was not present in this module's `.planning/`; per the default rule (key absent ⇒ enabled), this section is included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` (`go test`) |
| Config file | none (Go convention — `_test.go` files) |
| Quick run command | `go test ./internal/... -short` |
| Full suite command | `make test` (`go test ./... -short -cover`) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CLI-01 | All flags parse; defaults applied | unit | `go test ./cmd/... -run TestFlags -x` | ❌ Wave 0 |
| INGEST-01 | Client dials with MaxCallRecvMsgSize; Close works | unit (constructor) + manual smoke | `go test ./internal/dgraph -run TestNewClient` | ❌ Wave 0 |
| INGEST-03 | Frontier query string shape correct (uid set + follows{uid,pubkey}) | unit (format-string assert, no live DB) | `go test ./internal/dgraph -run TestFrontierQueryShape` | ❌ Wave 0 |
| LEVEL-01 | Seed=0; shortest-hop levels; first-reached wins; cycles terminate | unit (pure bfs) | `go test ./internal/bfs -run TestLevel` | ❌ Wave 0 |
| SCORE-01 | vfc counts followers with level(F)<level(T) | unit (pure score) | `go test ./internal/score -run TestScoreUpstream` | ❌ Wave 0 |
| SCORE-02 | same-level/deeper followers excluded | unit (pure score) | `go test ./internal/score -run TestScoreExcludesSameDeeper` | ❌ Wave 0 |
| OUT-01 | seed + levels 1..k excluded | unit (output) | `go test ./internal/output -run TestExcludeShells` | ❌ Wave 0 |
| OUT-02 | only vfc<N emitted; one JSON object per line | unit (output, golden file) | `go test ./internal/output -run TestJSONL` | ❌ Wave 0 |
| (end-to-end) | seed → JSONL against live Dgraph | manual smoke | `go run ./cmd/spam-explorer --seed <known> --max-level 2 --out /tmp/x.jsonl` | ❌ Wave 0 — confirms A1 |

### Sampling Rate
- **Per task commit:** `go test ./internal/... -short`
- **Per wave merge:** `make test`
- **Phase gate:** `make test` green + one manual smoke run against live Dgraph before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `internal/bfs/bfs_test.go` — covers LEVEL-01 (incl. cycle termination, `--max-level` cap)
- [ ] `internal/score/score_test.go` — covers SCORE-01, SCORE-02 (table-driven on small hand-built level+adjacency maps)
- [ ] `internal/output/jsonl_test.go` — covers OUT-01, OUT-02 (golden JSONL file)
- [ ] `internal/dgraph/frontier_test.go` — asserts query-string shape without a live DB (mirror web-of-trust's package-constant pattern)
- [ ] `cmd/spam-explorer/main_test.go` — flag defaults (CLI-01)
- Framework install: none — Go `testing` is built in.

## Security Domain

> `security_enforcement` config not located in this module; treated as enabled. This is a read-only, offline, local-only analysis CLI on an ID-only graph — most categories are N/A, captured explicitly below.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Local gRPC, insecure creds by stack design (no auth on local Dgraph) |
| V3 Session Management | no | One-shot CLI, no sessions |
| V4 Access Control | no | Read-only; no mutation path exists in this tool |
| V5 Input Validation | partial (Phase 3) | Seed/N/k validation is CLI-02 (Phase 3). Phase 1 adds only a missing-seed guard. **Mitigation note:** `--seed` is interpolated into a DQL query via `%q` (`fmt.Sprintf` with quoting), matching web-of-trust; this quotes/escapes the string. Do not build the query with raw `%s`. |
| V6 Cryptography | no | No crypto; pubkeys are opaque identifiers here, never verified or signed |
| V7 Error/Logging | partial (Phase 3) | Secret-safe logging is OPS-02 (Phase 3). Phase 1 logs pubkeys + counts only (no event content, no secrets — the graph is ID-only by the data-separation rule). |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| DQL injection via `--seed` | Tampering | Use `%q` / `strconv.Quote` when interpolating the pubkey (web-of-trust idiom); never raw `%s`. Phase 3 adds full hex-format validation (CLI-02). |
| Reading event payloads (data-separation breach) | Information disclosure | Query only `uid`, `pubkey`, `follows` — never event content; enforced by query design |
| Writing back to the graph | Tampering | This tool has NO mutation code path — read-only client only (D-06) |

## Project Constraints (from CLAUDE.md)

- **Project boundary:** spam-explorer is an independent monorepo subdirectory. **Read** web-of-trust for reference; **do not modify** web-of-trust (or any sibling) without explicit permission. All Phase-1 work stays inside `spam-explorer/`.
- **Data separation:** structural inference only on the ID-only Dgraph graph. Never pull event payloads. Query only `uid`/`pubkey`/`follows`.
- **Stock StrFry / extend-don't-fork:** N/A to this tool (it touches Dgraph, not StrFry), but reinforces read-only posture.
- **Go module independence:** own `go.mod`, own Makefile, `cmd/` + `internal/` split (matches the monorepo's per-subsystem convention).
- **Config files in `~/deepfry/`:** never delete/overwrite; this tool takes its inputs via CLI flags (no config file in Phase 1), so it does not touch `~/deepfry/`.
- **GSD workflow enforcement:** file edits go through a GSD command; this RESEARCH.md is produced under the plan-phase research step.

## Sources

### Primary (HIGH confidence — verified sibling source in this monorepo)
- `web-of-trust/pkg/dgraph/dgraph.go:37-60` — `NewClient` gRPC dial idiom, `MaxCallRecvMsgSize = 256<<20`, insecure creds, `dgo.NewDgraphClient(api.NewDgraphClient(conn))`
- `web-of-trust/pkg/dgraph/dgraph.go:1048-1067` — `NewReadOnlyTxn()` + `defer txn.Discard(ctx)` read pattern
- `web-of-trust/pkg/dgraph/dgraph.go:356-366` — `follows { uid pubkey }` nested selection shape
- `web-of-trust/pkg/dgraph/clusterscan.go:45-88` — `ResolvePubkeysToUIDs`: `eq(pubkey, [...])` UID resolution
- `web-of-trust/pkg/dgraph/clusterscan.go:196-223` — `DegreesForUIDs`: `func: uid(%s)` + `count(follows)` UID-set query
- `web-of-trust/cmd/clusterscan/main.go:46-99` — std `flag` CLI idiom + seed-resolve-then-loop orchestration
- `web-of-trust/go.mod` + `go list -m` — pinned `dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d`, `grpc v1.75.1`, `go 1.24.1`
- `web-of-trust/Makefile`, `quarantine-rescuer/Makefile` — common targets (build/run/test/fmt/vet/tidy/clean/lint/lint-fix, build-alpine/linux)
- `docker-compose.dgraph.yml` — `dgraph/standalone:v25.3.0`, gRPC `:9080` / HTTP `:8080`

### Secondary (context)
- `web-of-trust/.planning/spikes/spam-clusters/04-weakly-bridged-pods.md` — prior-art intuition this tool formalizes; the `fc==1` floor caveat and newcomer-pollution caveat
- `spam-explorer/.planning/PROJECT.md`, `REQUIREMENTS.md`, `01-CONTEXT.md` — locked algorithm semantics + requirements

### Tertiary (LOW confidence)
- none — no claim relies on web-search or training-only knowledge

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions read from a building sibling module's resolved go.mod
- Architecture / query shapes: HIGH — every primitive transcribed from working sibling source against the same server
- Pitfalls: HIGH — gRPC cap and UID/pubkey distinction documented in sibling code comments
- Defaults / tuning: MEDIUM — placeholders, deferred to runtime calibration (Open Question 1)

**Research date:** 2026-06-23
**Valid until:** 2026-07-23 (stable — pinned deps, sibling code unlikely to churn; re-check if web-of-trust bumps dgo/grpc or the Dgraph server image)
