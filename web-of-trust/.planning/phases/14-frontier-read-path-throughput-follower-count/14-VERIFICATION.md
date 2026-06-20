---
phase: 14
status: passed
verified_by: live operator run on the Dgraph host (2026-06-20)
---

# Phase 14 Verification — Frontier Read-Path Throughput (`follower_count`)

**Result: PASSED** (after two live-verification fix cycles). Measured on the production
Dgraph (v25.3.0, 1,383,141 nodes). `GetStalePubkeys` read-path dropped from ~119s to ~1.3s.

## Final read-path latency (`GetStalePubkeys`, first:100)

| Query | Old (count(~follows)) | Final (this phase) |
|-------|----------------------|--------------------|
| Frontier (`func: eq(uncrawled,1)`, orderdesc follower_count) | 69.0s | **0.01s** (empty) / **0.013s** (with nodes, correctly ordered) |
| Aged (`func: ge(follower_count,0)` + `lt(next_attempt)`) | 49.9s | **1.3s** |
| **Combined GetStalePubkeys** | **~119s** | **~1.3s** |

- Frontier ordering correctness confirmed live: 3 seeded uncrawled nodes (fc=50,5,1)
  returned in exact desc order, then deleted (production left clean).
- Accuracy (DSCALE-03): top node stored `follower_count=70547` == fresh `count(~follows)=70547`.

## What it took (the plan's first cut did NOT work at scale — live verification caught it)

Two fix cycles were required beyond the initial implementation; neither failure was
catchable by the unit/integration tests (4–5 node graphs) or static review:

1. **Read query didn't exploit the index.** `func: has(pubkey) ... orderdesc: follower_count`
   full-sorts the whole set (24–48s). Fixed: drive off the int index — aged uses
   `func: ge(follower_count, 0)` (1.3s).
2. **Frontier can't use follower_count as entry** (never-attempted nodes are absent-predicate
   + low-follower → bottom of a desc walk → ~25s scanning the whole index). Fixed: a positive
   indexed `uncrawled` marker — set on node creation, star-deleted in `MarkAttempted` — so the
   frontier enters via `func: eq(uncrawled, 1)` (0.01s). Invariant: `uncrawled=1` ⟺ no `last_attempt`.
3. **Backfill was impractical** (offset pagination + per-node count: ~100 nodes/min → days).
   A single bulk upsert hit Dgraph's "var has over million UIDs" cap. Fixed: uid-cursor
   (`after:`) bulk upsert, ~100k/batch — full 1.38M backfill in **2.5 min**, idempotent.

## Schema / data state on production (all additive, safe)
- `follower_count: int @index(int)` — added; all 1,383,141 nodes backfilled (incl 0 for no-follower).
- `uncrawled: int @index(int)` — added; currently 0 nodes carry it (frontier is empty — all attempted).
- Live crawler still running the OLD binary → no behavior change yet (see deploy runbook).

## Deploy runbook (the production cutover — NOT yet done)

The schema + follower_count backfill are live, but the running crawler is the OLD binary.
To activate the new read path + ongoing maintenance:

1. **Stop the old crawler.**
2. **Deploy the new crawler binary** (`make build`; the read-path queries + follower_count
   delta maintenance + uncrawled marker maintenance are all in it).
3. **One-time uncrawled safety seed** (handles any never-attempted nodes the old crawler may
   have created since the follower_count backfill — currently ~0, but do it to avoid stranding):
   set `uncrawled=1` on every `NOT has(last_attempt)` node before starting the new crawler.
   (Frontier was measured at 0, so this is a near-no-op now; run it anyway for safety.)
4. **Start the new crawler.** From here: new nodes get `uncrawled=1` on creation and
   `follower_count` maintained on every `AddFollowers`; `MarkAttempted` clears `uncrawled`.
5. Optional: re-run `backfill-follower-count` periodically — follower_count is an ordering
   hint and self-heals via maintenance, but a periodic re-seed corrects any drift.

## Known minor follow-up (non-blocking)
- `CountStalePubkeys`'s frontier-count block still uses `has(pubkey) @filter(NOT has(last_attempt))`
  (a metrics count, throttled by `count_sample_interval`). Correct by the invariant but could be
  switched to `eq(uncrawled,1)` for speed. Out of scope for this phase.

## Independent operator win (separate from this phase)
- Config tuning in `~/deepfry/web-of-trust.yaml`: `count_sample_interval` 1→~20,
  `frontier_batch_size` 100→~1000 (mechanisms shipped in Phase 13). Stacks on top of this.
