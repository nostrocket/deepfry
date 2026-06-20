# Phase 14: Frontier Read-Path Throughput (`follower_count`) - Context

**Gathered:** 2026-06-20
**Status:** Ready for planning

<domain>
## Phase Boundary

Eliminate the dominant per-batch read-path overhead in `GetStalePubkeys`. Today both
its frontier and aged phases run a `var(func: ...) { fc as count(~follows) }` block
that recomputes the follower count for the *entire* matching set (~1.3M never-attempted
nodes) on every call, purely to order the frontier by `orderdesc: val(fc)`. Production
metrics measure this at ≈39s/batch — the single largest cost in an ~80s batch.

This phase introduces a stored, indexed `follower_count` predicate on `Profile`,
maintained as follow edges change, so frontier ordering reads a value instead of
recomputing the aggregate. Scope is the **read path** (`GetStalePubkeys`) plus the
minimal write-path bookkeeping (`AddFollowers`) and a one-time backfill needed to keep
the stored value usable.

**Out of scope:** Dgraph write-path *throughput* optimization (`AddFollowers`
parallelism / transaction restructuring). Production metrics confirm the write path
(`MarkAttempted` ≈ 0.07s/batch) does not dominate — DWRITE-01 is closed as
"not dominant". The count-query and frontier-batch wins from the same analysis are pure
operator config (`count_sample_interval`, `frontier_batch_size`) already shipped in
Phase 13; no code change here.
</domain>

<decisions>
## Implementation Decisions

### follower_count predicate & read-path query (Area 1)
- **Accuracy contract: ordering hint, not authoritative.** `follower_count` only ranks
  the frontier, so small drift is acceptable. This permits cheap delta maintenance and
  tolerates eventual-consistency from backfill — exactness is explicitly NOT required.
- **Schema:** add `follower_count: int @index(int) .` to the schema and to the `Profile`
  type in `EnsureSchema`. The int index is required for an efficient `orderdesc`.
- **Read-path change:** in `GetStalePubkeys`, remove the `var { fc as count(~follows) }`
  block and order directly by the stored predicate (`orderdesc: follower_count`) in
  **both** the frontier phase (`NOT has(last_attempt)`) and the aged phase
  (`lt(next_attempt, now)`), keeping the explicit `first: N` limit.
- **Missing values:** the one-time backfill sets every existing node; newly-created
  nodes default to `0` (or `1` when created as a followee, see Area 2) and converge via
  maintenance/backfill. Nodes without the predicate sort last — acceptable for a hint.

### Maintenance on write — AddFollowers (Area 2)
- **Delta-update strategy.** When a signer's kind-3 follow set is replaced, compute the
  delta against `existingFollows`: `added = new − existing`, `removed = existing − new`.
  Increment each added followee's `follower_count` by +1 and decrement each removed
  followee's by −1. Unchanged followees are untouched.
- **Transaction scope: inside the existing all-or-nothing `AddFollowers` transaction.**
  It already resolves follower + existing/new followee UIDs, so the delta is computed
  from data in hand. Keeping it in the same txn preserves kind-3 all-or-nothing
  replacement semantics (DWRITE-02 spirit) and keeps the stored count consistent with
  the edges that produced it.
- **Large lists:** reuse the existing `batchSize` chunking for the count-update nquads
  so a >10k follow list does not exceed the ~4MB gRPC message limit.
- **New followee nodes** created during the write initialize `follower_count = 1`
  (the signer is their first observed follower).

### Backfill & verification (Area 3)
- **Backfill delivery:** an idempotent, paginated **operator-run CLI** under `cmd/`,
  mirroring the existing `BackfillNextAttempt` / `cmd/healthcheck` patterns. Run once
  over the existing ~1.38M nodes. Not auto-run on crawler startup.
- **Backfill computation:** per node, `follower_count = count(~follows)`, paginated to
  stay under gRPC message-size limits; safe to re-run (idempotent overwrite).
- **Live verification (TEST-03):** before/after `GetStalePubkeys` latency on the strfry
  host, plus a stored-vs-recomputed spot-check on a sample of nodes to confirm the
  maintained value tracks the true count within hint tolerance.
- **Unit coverage:** table-driven delta math (added / removed / unchanged sets),
  backfill pagination, and a check that the stale query orders by the predicate. Live
  Dgraph checks stay behind the existing `//go:build integration` tag.

### Claude's Discretion
- Exact CLI name/flags for the backfill command, pagination page size, and nquad
  batching details are at Claude's discretion, following existing `cmd/` and
  `pkg/dgraph` conventions.
</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `EnsureSchema` (`pkg/dgraph/dgraph.go:63`) — single place that declares predicates +
  the `Profile` type; `follower_count` is added here.
- `GetStalePubkeys` (`pkg/dgraph/dgraph.go:728`) — the two `var{count(~follows)}` +
  `orderdesc: val(...)` blocks to replace (frontier ~L737, aged ~L755).
- `AddFollowers` (`pkg/dgraph/dgraph.go:252`) — already queries the signer's
  `existingFollows` (pubkey→uid map, L350) and resolves/creates followees with
  `batchSize` chunking; the delta sets are computable from data it already loads.
- `BackfillNextAttempt` (`pkg/dgraph/dgraph.go:1042`) — paginated idempotent backfill
  template for the new follower_count backfill.
- `chunkSlice` / `batchSize` / `withWindowTimeout` — existing chunking + bounded-window
  helpers reused for the count-update mutations.

### Established Patterns
- Schema changes via `c.dg.Alter`; additive predicates are safe (Dgraph schema is
  additive). `@index(int)` already used for `kind3CreatedAt`/`last_attempt`/`next_attempt`.
- Mutations as nquads inside a `NewTxn()` with `CommitNow:false`, committed once;
  bounded per-window contexts (`withWindowTimeout`) per Phase 12.
- Read-only analysis queries via `NewReadOnlyTxn()` (`collectStale`).
- Backfill/maintenance CLIs live under `cmd/` (healthcheck, pubkeys) using
  `dgraph.NewClient(addr)`.

### Integration Points
- Read path: `cmd/crawler` → `GetStalePubkeys` (ordering only; no caller signature change).
- Write path: `pkg/crawler.FetchAndUpdateFollows` → `AddFollowers` (delta bookkeeping
  added internally; no signature change).
- New `cmd/` backfill binary added to the Makefile build targets.
</code_context>

<specifics>
## Specific Ideas

- The historical large-frontier sort-cap concern (08-REVIEW WR-05, dgraph.go:707) was
  about an *unbounded sorted query capping at 1000 with missing-value nodes sorting
  last*. After backfill every frontier node has `follower_count`, so `orderdesc:
  follower_count` + explicit `first: N` is the intended efficient pattern; re-verify the
  top-N-by-count guarantee holds against the live graph during TEST-03.
- The `D-10` note at dgraph.go:704 ("stored follower_count predicate is NOT needed")
  was a correctness judgment, not a performance one. This phase supersedes it; update
  that comment to reflect the perf rationale.
</specifics>

<deferred>
## Deferred Ideas

- DWRITE-02/03 write-path throughput optimization (AddFollowers parallelism /
  transaction restructuring) — investigation closed as "not dominant"; revisit only if
  a future milestone changes the write path.
- DSCALE-02 (Dgraph write parallelism evaluation) — remains a Future Requirement.
</deferred>
