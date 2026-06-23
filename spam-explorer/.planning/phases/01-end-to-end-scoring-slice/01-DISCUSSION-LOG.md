# Phase 1: End-to-End Scoring Slice - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-23
**Phase:** 1-End-to-End Scoring Slice
**Areas discussed:** Subgraph ingestion, Valid-follower source, Bounding the slice, Layout & client reuse

---

## Subgraph Ingestion

| Option | Description | Selected |
|--------|-------------|----------|
| Frontier expansion | BFS level-by-level: query follows for current frontier UIDs, advance. Phase 2 adds pagination inside each batch — minimal rework. | ✓ |
| Single subgraph fetch | One DQL query (@recurse from seed) pulls whole reachable subgraph; simplest but throwaway. | |
| @recurse bounded depth | One @recurse capped at fixed depth; compact but doesn't resemble Phase 2 streaming path. | |

**User's choice:** Frontier expansion
**Notes:** Chosen so ingestion has the same shape as the Phase 2 production path; pagination drops into the frontier-batch seam later.

---

## Valid-Follower Source

| Option | Description | Selected |
|--------|-------------|----------|
| In-memory inversion | Invert the follows adjacency already built during BFS; no ~follows query. Valid followers are provably reachable and already materialized. | ✓ |
| Explicit ~follows query | Query reverse ~follows per node; more round-trips, mirrors schema 1:1. | |

**User's choice:** In-memory inversion
**Notes:** Correctness argument captured in CONTEXT.md D-02 — every valid follower F has level(F) < level(T), so F is reachable and edge F→T was recorded when F was expanded.

---

## Bounding the Slice

| Option | Description | Selected |
|--------|-------------|----------|
| Small test seed | Run against a seed with a naturally small reachable neighborhood; same code path as production. | |
| Temporary --max-level cap | Depth-cap flag to bound traversal; useful debug knob but not a v1 requirement. | ✓ |
| No cap at all | Rely on operator's seed choice; risk a "small" seed blowing up. | |

**User's choice:** Temporary --max-level cap
**Notes:** Captured as a Phase-1 bounding/debug flag (CONTEXT.md D-03), flagged for removal/retention review at Phase 2. Interaction with in-memory inversion confirmed safe (D-04).

---

## Layout & Client Reuse

| Option | Description | Selected |
|--------|-------------|----------|
| cmd/ + minimal client | cmd/spam-explorer/main.go + internal/ mirroring web-of-trust; hand-write minimal read-only dgo client. | ✓ |
| Copy wot dgraph.go | Copy web-of-trust's dgraph.go wholesale; drags in unused write/schema code. | |
| Flat single package | Everything in one main package; diverges from stack conventions. | |

**User's choice:** cmd/ + minimal client
**Notes:** Reference web-of-trust's gRPC dial idiom (raised MaxCallRecvMsgSize) and ResolvePubkeysToUIDs read pattern; do not import the separate module.

---

## Claude's Discretion

- Frontier batch size, JSONL write buffering/flush cadence, output line ordering, internal adjacency/level data structures, and summary-line wording — none constrained during discussion.

## Deferred Ideas

- Paginated DQL streaming for the full graph → Phase 2.
- Determinism-at-scale verification → Phase 2.
- Input validation, unreachable-node error reporting, secret-safe logging → Phase 3.
- `--max-level` flag retention/removal review → Phase 2.
- Multi-signal intersection, multi-seed, denylist artifact → v2.
