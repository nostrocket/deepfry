# Roadmap: spam-explorer

## Overview

A one-shot Go CLI that scores every pubkey in the web-of-trust Dgraph follow graph by its seed-relative valid-follower count and emits a JSONL list of suspected spam/sybil candidates. The journey runs as a vertical pipeline: first prove the full path end-to-end on a small reachable subgraph (seed → BFS levels → valid-follower scoring → filtered JSONL), then harden that same path for the real ~1.54M-node graph via paginated streaming, then make the run operationally trustworthy with input validation, unreachable-node error reporting, and secret-safe progress/summary logging.

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: End-to-End Scoring Slice** - Run the whole pipeline (CLI → Dgraph → BFS levels → valid-follower scoring → filtered JSONL) on a small reachable subgraph
- [ ] **Phase 2: Production-Scale Streaming** - Read the follow graph via paginated DQL so leveling and scoring run deterministically against the full ~1.54M-node graph
- [ ] **Phase 3: Operational Correctness** - Input validation, unreachable-node error reporting, and secret-safe progress/summary logging

## Phase Details

### Phase 1: End-to-End Scoring Slice

**Goal**: Deliver a runnable CLI that, given a seed, levels a small reachable subgraph, counts strictly-upstream valid followers, and writes a threshold-filtered JSONL file — the complete metric proven correct end-to-end.
**Mode:** mvp
**Depends on**: Nothing (first phase)
**Requirements**: CLI-01, INGEST-01, INGEST-03, LEVEL-01, SCORE-01, SCORE-02, OUT-01, OUT-02
**Success Criteria** (what must be TRUE):

  1. User can run the binary with `--seed`, `--threshold`, `--exclude-shells`, a Dgraph endpoint flag, and `--out`, and it completes without crashing on a small reachable graph
  2. The tool connects to Dgraph via the `dgo/v210` gRPC client and materializes enough `follows`/`~follows` adjacency to level and score every reachable account
  3. Each reachable account receives a level equal to its shortest follow-hop distance from the seed (seed = level 0, outward along `follows`)
  4. Each scored account's `valid_follower_count` counts only followers strictly shallower than it (`level(F) < level(T)`); same-level and deeper followers are excluded
  5. The output JSONL contains one `{pubkey, valid_follower_count}` object per line for every account scoring below `N`, with the seed and its first `k` shells excluded

**Plans**: 1/2 plans executed
**Wave 1**

- [x] 01-01-PLAN.md — Scaffold the module + pure pipeline core (BFS leveling, valid-follower scoring, JSONL output) with offline tests

**Wave 2** *(blocked on Wave 1 completion)*

- [ ] 01-02-PLAN.md — Read-only Dgraph wire tier (client, resolve, frontier) + main.go orchestration; live end-to-end smoke run

### Phase 2: Production-Scale Streaming

**Goal**: Make the same pipeline run against the full production graph by reading it through paginated DQL queries, with deterministic, first-reached level assignment preserved at scale.
**Mode:** mvp
**Depends on**: Phase 1
**Requirements**: INGEST-02, LEVEL-02
**Success Criteria** (what must be TRUE):

  1. The tool reads the follow graph through paginated DQL queries rather than a single whole-graph query, and completes a run against the full ~1.54M-node graph
  2. Memory usage stays bounded during a full-graph run (the graph is not loaded in one query)
  3. Re-running with the same seed against the same graph snapshot produces identical levels and scores (first-reached/shallowest wins, as BFS guarantees)

**Plans**: TBD

### Phase 3: Operational Correctness

**Goal**: Make a run trustworthy and diagnosable — reject bad input clearly, surface graph accounts unreachable from the seed as errors, and report progress and a final summary without leaking secrets.
**Mode:** mvp
**Depends on**: Phase 2
**Requirements**: CLI-02, OUT-03, OPS-01, OPS-02
**Success Criteria** (what must be TRUE):

  1. The tool validates inputs (well-formed seed pubkey, `N > 0`, `k >= 0`, writable output path) and exits non-zero with a clear message on bad input
  2. Accounts that exist in the graph but are unreachable from the seed are detected and logged as errors rather than silently skipped
  3. The run logs progress and a final summary (accounts leveled, scored, and emitted) to the user-specified output path, with no secrets or event content in any log line

**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. End-to-End Scoring Slice | 1/2 | In Progress|  |
| 2. Production-Scale Streaming | 0/TBD | Not started | - |
| 3. Operational Correctness | 0/TBD | Not started | - |
