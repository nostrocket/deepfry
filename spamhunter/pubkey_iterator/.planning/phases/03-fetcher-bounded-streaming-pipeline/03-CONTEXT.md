# Phase 3: Fetcher + Bounded Streaming Pipeline - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Source:** Front-loaded decisions (autonomous run — gathered up front so the 2-6 run does not pause)
**Mode:** mvp

<domain>
## Phase Boundary

Enumerated pubkeys flow through a **bounded-memory streaming pipeline** —
tokio fetch → bounded channel → rayon analysis — that fetches each pubkey's
most-recent ~100 events via batched `latestPerAuthor` without ever buffering
the corpus. This locks in the structural concurrency decision (INGEST-03)
before any detection layer depends on it. The end of the pipeline is a no-op
pass-through consumer that proves end-to-end flow; the real layers arrive in
Phase 4.

**In scope:** the `latestPerAuthor` fetch query on the existing reusable
GraphQL client, author-group→pubkey matching by `author` field, the
tokio→bounded-channel→rayon pipeline with proven bounded memory and
back-pressure, and a no-op consumer proving flow.

**Out of scope (later phases):** the Layer trait and any detection logic
(Phase 4), scoring/persistence of scores (Phase 4), the CLI `run`/`export`
surface (Phase 5). This phase persists nothing new to the corpus (read-only).
</domain>

<decisions>
## Implementation Decisions

### Fetch query & batching
- **D-01:** Fetch each pubkey's most-recent ~100 events via
  `latestPerAuthor(kind:1, perAuthor:100)`, batched at **≤1000 authors per
  call** (contract hard limit). `kind:1` (text notes) is the v1 target; the
  kind is a config value, not hardcoded magic.
- **D-02:** The authors-per-call batch size must keep the **response body
  ≤256 KiB** (contract §12). 1000 authors × 100 events may exceed 256 KiB —
  the planner/research picks a safe authors-per-call (Claude's discretion,
  e.g. start smaller and tune); a `413` is treated as "shrink the batch and
  retry" per the Phase-2 error taxonomy, never a hard failure.
- **D-03:** Reuse the **Phase-2 reusable GraphQL client** (D-11 from Phase 2):
  add `latestPerAuthor` as an additive query on the same client/transport.
  Same async reqwest/tokio transport, same two-layer error dispatch (HTTP
  status vs in-body `errors[]`/`extensions.code`).

### Author-group matching (INGEST-04)
- **D-04:** Fetched author groups are matched back to requested pubkeys **by
  the `author` field, never zipped by index**. Authors omitted from the
  response because they have zero matching kind-1 events are handled as
  "empty group" without misaligning the remaining results.

### Pipeline structure (INGEST-03)
- **D-05:** Pipeline = **tokio fetch (I/O) → bounded channel → rayon analysis
  (CPU)**. The bounded channel (use the existing `flume` dep, already present)
  is the back-pressure point: fetch blocks when the channel is full, so memory
  is bounded by **channel capacity, not corpus size**. CPU analysis runs off
  the tokio threads (rayon pool), never blocking the async runtime.
- **D-06:** The Phase-3 consumer is a **no-op / pass-through** rayon stage
  (e.g. counts groups, drops them). It proves end-to-end flow from enumeration
  → fetch → rayon with no unbounded buffering. Phase 4 swaps in the real
  Layer/combiner stage at this seam — design the stage boundary so that is
  additive.
- **D-07:** Enumeration source: the pipeline reads pubkeys from the **Phase-2
  enumeration** (the persisted `pubkey` table is the simplest source; whether
  it re-walks live or reads the table is the planner's call — reading the
  persisted table is the recommended default, decoupling fetch from the walk).

### Verification posture
- **D-08:** **Bounded-memory proof** is the headline test: run the pipeline
  over a **large synthetic author set** (mocked fetcher) and assert peak
  in-flight memory is bounded by channel capacity, not the author count.
  Unit/integration tests use a mocked adapter for determinism and speed.
- **D-09:** A **live integration check** fetches a real sample from the live
  adapter (`http://192.168.149.21:8080/graphql`) to prove `latestPerAuthor`
  deserializes the real response. Live services are reachable and need no
  human intervention, so this can run automatically; if transiently
  unreachable it degrades to a deferred manual check (does not block).

### Claude's Discretion
- Channel capacity, rayon thread-pool size, exact authors-per-call within the
  ≤1000 + 256 KiB envelope, and the in-flight buffering strategy — chosen by
  research/planner in the bounded-memory + fail-fast spirit.
- Whether the no-op consumer counts, hashes, or simply drops groups.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Adapter contract
- `contract.md` — §6.2 `latestPerAuthor` (kind, perAuthor, authors[≤1000],
  matching by `author`, empty-group omission), §7 Errors (`413` body-too-large
  → shrink batch; `503`; in-body errors), §12 limits (body 256 KiB,
  `latestPerAuthor` authors ≤1000, page clamp ≤500).

### Live services (reachable, no human intervention)
- **LMDB2GraphQL adapter:** `http://192.168.149.21:8080/graphql` — POST
  `/graphql` is the only data endpoint; GET `/graphql` = GraphiQL; GET
  `/health`; GET `/ready` returns `503` until startup gates pass (treat a
  `503` from readiness as transient backoff, not failure).

### Phase 1 + Phase 2 foundations to build on
- `src/graphql/` (Phase 2) — the reusable async client + envelope + two-layer
  error dispatch. Extend with `latestPerAuthor` (additive).
- `src/enumerate.rs` + `src/store/` (Phases 1-2) — the `pubkey` table is the
  enumeration source; the single-writer store invariant still holds.
- `flume` dependency (already present) — the bounded channel.

### Project planning
- `.planning/ROADMAP.md` Phase 3 — goal + 4 success criteria.
- `.planning/REQUIREMENTS.md` — INGEST-02, INGEST-03.
</canonical_refs>

<code_context>
## Existing Code Insights

- The Phase-2 GraphQL client is the seam: add `latestPerAuthor` as a second
  hand-written query + serde structs on the same transport (no rewrite).
- `flume` is already a dependency (Phase 1 added it) — use it for the bounded
  channel; do not add a new channel crate.
- `rayon` is the Phase-3-owned dependency (deps land in their owning phase) —
  add it here for the CPU analysis stage.
- The single-writer store invariant from Phase 1/2 still governs any future
  persistence; Phase 3 persists nothing new (no-op consumer).
</code_context>

<specifics>
## Specific Ideas

- Back-pressure is the whole point: a slow rayon stage must slow the tokio
  fetcher via the bounded channel, never grow an unbounded queue.
- The author→pubkey matching is the classic INGEST-04 landmine: an omitted
  empty-group author must NOT shift every subsequent result by one. Match on
  the `author` field in the response, keyed into a map of requested pubkeys.
</specifics>

<deferred>
## Deferred Ideas

- **Real detection layers + scoring** — Phase 4 (this phase's no-op consumer
  is the plug-in seam).
- **Direct `heed` LMDB reads to bypass the GraphQL hop (PERF-01)** — v2,
  profiling-gated.
- **Incremental/service mode (SVC-01)** — v2.
</deferred>

---

*Phase: 3-Fetcher + Bounded Streaming Pipeline*
*Context front-loaded: 2026-06-25 (autonomous run)*
