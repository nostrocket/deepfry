---
phase: 14
status: gaps_found
verified_by: live operator run on the Dgraph host (2026-06-20)
---

# Phase 14 Verification — Frontier Read-Path Throughput (`follower_count`)

**Result: GAPS FOUND.** Live verification on the production Dgraph (v25.3.0, 1,383,141
nodes) shows the phase code as written does NOT deliver the goal. Two blocking findings,
both proven with measurements, neither catchable by the unit/integration tests (4–5 node
graphs) or static review.

## Environment
- Dgraph host, live graph: `localhost:8080` (HTTP) / `:9080` (gRPC), v25.3.0, 1,383,141 nodes.
- `follower_count: int @index(int)` index built successfully via `EnsureSchema` (additive, safe).

## Measured read-path latency (`GetStalePubkeys`, first:100)

| Query | Wall time | Note |
|-------|-----------|------|
| OLD frontier (`var{fc as count(~follows)}` + `orderdesc: val(fc)`) | **69.0s** | baseline; returned 0 rows (frontier empty — all nodes attempted) |
| OLD aged (`count(~follows)` sort) | **49.9s** | steady-state hot path |
| NEW frontier as-implemented (`func: has(pubkey)` + `orderdesc: follower_count`) | **47.8s** | barely better |
| NEW aged as-implemented (`func: has(next_attempt)` + `orderdesc: follower_count`) | **23.5s** | ~2× only |
| `func: has(pubkey), first:100` **no ordering** | **0.028s** | enumeration is NOT the bottleneck |
| index-driven `func: gt(follower_count,100), orderdesc, first:100` | **0.064s** | index entry-point is ~1000× faster |
| CORRECTED frontier (`func: gt(follower_count,-1)` entry + filter) | **0.26s** | the fix |
| CORRECTED aged (`func: gt(follower_count,-1)` entry + filter) | **0.40s** | the fix |

## Finding 1 (BLOCKING) — read query does not exploit the index
The rewritten `GetStalePubkeys` orders by the stored `follower_count` but keeps
`func: has(pubkey)` / `func: has(next_attempt)` as the entry point. Dgraph therefore
materializes and sorts the ENTIRE matching set (24–48s) instead of walking the
`follower_count` int index. The stored predicate removed the `count(~follows)` recompute
but the dominant cost (full set sort) remains, so the win is only ~2×, not the order of
magnitude the phase targets.

**Fix (proven, 0.26–0.40s):** drive both blocks off the int index as the entry point —
`func: gt(follower_count, -1), first: N, orderdesc: follower_count @filter(...)` (or
`ge(follower_count, 0)`), keeping the existing `@filter(NOT has(last_attempt))` /
`@filter(lt(next_attempt, now))`. Re-measure at full backfill to confirm filter-skip depth
on the frontier phase stays bounded.

## Finding 2 (BLOCKING) — backfill is impractically slow at production scale
`backfill-follower-count` uses offset pagination (`func: has(pubkey), offset: M,
orderasc: pubkey`) recomputing `count(~follows)` per node. Measured ~100 nodes/min →
**days** for 1.38M nodes (killed at 1000 nodes). The offset/orderasc:pubkey idiom (mirrored
from `GetAllPubkeysPaginated`) is fine for small exports but not a full-graph seed.

**Fix options:** (a) single/few-batch upsert — `query { v as var(func: has(pubkey)) { fc as
count(~follows) } } mutation { set { uid(v) <follower_count> val(fc) . } }` — computes all
counts in one pass and writes `val(fc)`; (b) uid-cursor (`after: <lastUID>`) pagination
instead of offset, which is O(n) total instead of O(n²). Re-verify the chosen approach
completes over the full graph in acceptable time.

## State left on production (benign)
- `follower_count @index(int)` added (additive, unused by the live crawler — still on old binary).
- ~1000 nodes carry a backfilled `follower_count` (ordering hint; harmless).
- Live crawler binary NOT restarted → no behavior change in production.

## Next step
Revise Plan 14-01 (read-query entry point + backfill mechanism), re-execute those two tasks,
then re-run this live verification. Do NOT close Phase 14 / milestone v1.6 until both fixes
re-measure correctly on the full graph.
