# Phase 3: Query Engine - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-11
**Phase:** 3-Query Engine
**Areas discussed:** Index selection / routing, Multi-value fan-out & merge, NIP-40 expiration + limit, Ordering & pagination cursor

---

## Index selection / routing

### How to resolve a multi-constraint filter

| Option | Description | Selected |
|--------|-------------|----------|
| One index + residual filter | Pick the single most-selective index, scan, post-filter remaining predicates in memory (mirrors strfry's DBScan planner) | ✓ |
| Multi-index intersection | Scan several candidate indexes, intersect levId sets before hydrating | |
| You decide | Defer to research/planning | |

### Selectivity priority order

| Option | Description | Selected |
|--------|-------------|----------|
| ids > pubkeyKind > pubkey > kind > created_at | Fixed routing: ids→Event__id; authors+kinds→Event__pubkeyKind; authors→Event__pubkey; kinds→Event__kind; time-only→Event__created_at; tags→Event__tag | ✓ |
| Static order, refine later | Same order as a hardcoded heuristic; no cardinality estimation, revisit only if pathological | (folded into choice) |
| You decide | Defer to planning | |

### since/until handling

| Option | Description | Selected |
|--------|-------------|----------|
| Push into scan bounds | Encode since/until into start_key + early-stop; for prefix indexes applies within each prefix group | ✓ |
| Residual filter only | Scan broadly, drop out-of-range in memory | |
| You decide | Defer to planning | |

### Empty / non-indexable filter

| Option | Description | Selected |
|--------|-------------|----------|
| Scan Event__created_at newest-first | Reverse Event__created_at walk bounded by limit ("latest N overall") | ✓ |
| Require at least one constraint | Reject fully-empty filters at the engine boundary | |
| You decide | Defer to planning | |

**Notes:** No cardinality estimation in v1; the fixed order is the heuristic. Limit ceiling (Phase 4) is the safety bound for the empty-filter default feed.

---

## Multi-value fan-out & merge

### Producing a single ordered result from multiple prefixes

| Option | Description | Selected |
|--------|-------------|----------|
| Per-prefix reverse scan + k-way merge | One bounded Reverse scan per prefix (~limit each), merge by (created_at, levId) desc via heap, emit until limit | ✓ |
| Concatenate + sort | Scan each prefix, concatenate all, sort, truncate to limit | |
| You decide | Defer to planning | |

### When to hydrate

| Option | Description | Selected |
|--------|-------------|----------|
| Hydrate after merge selection | Merge on (created_at, levId) from key bytes; hydrate only the final selected levIds | ✓ |
| Hydrate then filter | Hydrate all candidates first, then merge/filter on decoded fields | |
| You decide | Defer to planning | |

**Notes:** Hydrate-after-selection surfaced the NIP-40/residual interaction (decoded-field predicates run after hydration → backfill), which led directly into Area 3.

---

## NIP-40 expiration + limit accounting

### Honoring limit when post-hydration filtering drops events

| Option | Description | Selected |
|--------|-------------|----------|
| Over-fetch & backfill | Keep pulling candidates from the merge, hydrate, drop expired/residual-fail, until limit valid events collected or streams dry | ✓ |
| Filter post-limit, return short | Select exactly limit candidates, drop expired, return survivors (may be < limit) | |
| You decide | Defer to planning | |

### Clock source for `now`

| Option | Description | Selected |
|--------|-------------|----------|
| Injected clock, per-query now | Capture now once via an injectable time source; deterministic against fixed fixtures | |
| Direct system time | Call system clock directly at the expiration check | ✓ |
| You decide | Defer to planning | |

**Notes:** User chose direct system time. Flagged testing implication: NIP-40 tests against the pinned fixed-timestamp fixture must use future-dated (or 0/absent) expiration values to stay deterministic, since `now` is not pinnable. No clock seam added.

---

## Ordering & pagination cursor

### events() ordering contract + cursor shape

| Option | Description | Selected |
|--------|-------------|----------|
| Newest-first, opaque (created_at,levId) cursor | created_at DESC, levId DESC tie-break; cursor = opaque encoding of last (created_at, levId), resume via Excluded | ✓ |
| Newest-first, structured cursor | Same ordering, but expose {created_at, levId} as visible fields | |
| You decide | Defer to planning | |

### latestPerAuthor result shape

| Option | Description | Selected |
|--------|-------------|----------|
| Flat list, global newest-first | Per-author scan capped at perAuthor, merged into one flat created_at DESC list | |
| Grouped by author | Per-pubkey buckets, each newest-first and ≤ perAuthor | ✓ |
| You decide | Defer to planning | |

**Notes:** events() is one flat merged stream; latestPerAuthor deliberately preserves per-author grouping. Both share the per-prefix-reverse-scan machinery — difference is only output shape.

---

## Claude's Discretion

- Module layout, `thiserror` error-type boundaries, precise engine function signatures.
- K-way merge data structure (binary heap vs. ordered merge of bounded sub-iterators).
- Cursor byte layout / encoding (opaqueness + (created_at, levId) content fixed).
- Internal representation of residual predicates and the key-byte-evaluable vs. hydration-required split.
- Over-fetch batch sizing for the backfill loop (reuse scan.rs window pattern).
- Whether engine functions own or borrow the RoTxn (recommend own), keeping the short-txn invariant.

## Deferred Ideas

- Hard limit ceiling + public GraphQL cursor/Connection types — Phase 4.
- Cardinality-based index selection — future optimization only if a query proves pathological.
- Injectable clock for NIP-40 — explicitly not added; revisit only if a later phase needs it.
- Deletion / replaceable-supersession handling — out of scope (strfry enforces at write time).
- Doc-sync of stale rusqlite/SQLite wording in CLAUDE.md + amended LMDB-05 — non-code cleanup, carried from Phases 1-2.
