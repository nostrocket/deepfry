# Phase 2: GraphQL Client + Author Enumeration - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning

<domain>
## Phase Boundary

Walk the entire `authors` keyspace through the LMDB2GraphQL adapter — visiting each
distinct pubkey exactly once via cursor pagination, terminating cleanly when
`hasMore` is false — resumably, while surviving the adapter's real failure modes
(`503`, `INVALID_CURSOR`, in-body GraphQL `errors[]`/`extensions.code`, snapshot
drift). This is the connectivity-proving vertical slice: it proves the contract
end-to-end against the live adapter **before any analysis exists**.

**In scope:** a reusable GraphQL client module, the `authors` enumeration walk,
per-batch resume-state persistence, adapter error handling, snapshot-drift probe,
and a minimal entry point to run/resume the walk.

**Out of scope (later phases):** `latestPerAuthor`/event fetching and the bounded
streaming pipeline (Phase 3), any detection layer or scoring (Phase 4), the full
`run`/`export` CLI surface (Phase 5). No analysis, no scoring, no writes to the
corpus (adapter is read-only by design).
</domain>

<decisions>
## Implementation Decisions

### Resume semantics
- **D-01:** `--resume` **auto-resumes the latest unfinished run** — the most
  recent `run` row whose `status` is not `done` (i.e. `running`/`aborted`),
  continuing from its stored `last_cursor`. No `--run-id` required.
- **D-02:** If no unfinished run exists, `--resume` starts a fresh run (do not
  error out). A normal (non-resume) invocation always starts a fresh run.
- **D-03:** Resume relies only on the existing Phase-1 `run` schema
  (`last_cursor`, `max_lev_id_start`, `max_lev_id_end`, `status`,
  `config_json`) — no new resume-tracking state is introduced.

### Phase output
- **D-04:** As the walk visits each distinct pubkey, **persist it** into the
  `pubkey` table via `INSERT OR IGNORE` (idempotent; safe on resume/overlap)
  **and** report running progress/count to stdout. Both: durable artifact +
  observability.
- **D-05:** Populating the `pubkey` table here is deliberate — `score`/`signal`
  tables already FK to `pubkey`, so it must be populated eventually; doing it
  during enumeration leaves a real, queryable system behind this phase.

### Resilience policy
- **D-06:** **Fail-fast** on transient adapter trouble: a small bounded retry
  ceiling per request (a few quick retries), then **abort the run** rather than
  retry indefinitely. Surfaces problems to the operator immediately.
- **D-07:** On abort, the run is marked `status='aborted'` with `last_cursor`
  preserved, so `--resume` (per D-01) continues exactly where it stopped. A
  failure **never advances the cursor**.
- **D-08:** `503` → backoff-and-retry within the bounded ceiling without
  advancing the cursor (locked by success criterion 3). `INVALID_CURSOR` →
  restart pagination from page 1 (locked by criterion 3). GraphQL
  `errors[]`/`extensions.code` in a `200` body are parsed and acted on, never
  ignored (locked by criterion 3).
- **D-09:** Snapshot drift = **record-and-continue** (locked by criterion 4):
  capture `stats.maxLevId` at run start and end into the `run` row as a
  drift probe; a corpus change mid-pagination does **not** abort the run.

### Client & entry-point structure
- **D-10:** GraphQL client is **hand-written query strings + serde structs** —
  no introspection codegen toolchain for this phase's single query.
- **D-11:** Build a **reusable `graphql` client module** that Phase 3 extends
  with `latestPerAuthor` (and `events` as needed) on the same client/transport.
  Design the client so adding a query later is additive, not a rewrite.
- **D-12:** Expose the walk via a **minimal binary/subcommand** now (enough to
  run it and pass `--resume`). The full clap CLI surface is Phase 5's `run`/
  `export` — do not build it out here.

### Claude's Discretion
- Exact retry ceiling count and backoff base/cap (small, fail-fast spirit per
  D-06) — researcher/planner to choose a sensible constant.
- `authors` page `limit` value (ceiling is 500; larger = fewer round-trips).
- HTTP client/runtime specifics (project stack is reqwest/tokio per the Cargo
  comment in Phase 1) and how the endpoint URL is supplied for this phase
  (config-file-driven config is OPS-03 / Phase 4 territory).
- Internal module naming/layout within the reusable-client constraint (D-11).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Adapter contract (the thing this phase is built against)
- `contract.md` — Full code-verified LMDB2GraphQL interface contract. Critical
  sections for this phase: §6.4 `authors` (distinct-pubkey enumeration, cursor
  rules, clean termination), §6.3 `stats` (`maxLevId` drift probe), §7 Errors
  (`INVALID_CURSOR`, `503`, `413`, in-body `errors[]`/`extensions.code`), §8
  Data semantics (opaque cursors, live snapshot, corpus may change between
  calls), §12 limits cheat-sheet (`authors.limit` ceiling 500, body 256 KiB).
  *(git-root path: `spamhunter/pubkey_iterator/contract.md`)*

### Project planning
- `.planning/ROADMAP.md` — Phase 2 goal + the 4 locked success criteria.
- `.planning/REQUIREMENTS.md` — INGEST-01 (resumable cursor enumeration),
  INGEST-04 (graceful adapter error handling).
- `.planning/PROJECT.md` — overall engine intent, read-only constraint,
  ecosystem context.

### Phase 1 foundation to build on
- `src/store/schema.rs` — `run` table (`last_cursor`, `max_lev_id_start`,
  `max_lev_id_end`, `status`, `config_json`) used for resume state per D-01/D-03;
  `pubkey` table targeted by `INSERT OR IGNORE` per D-04.
- `src/store/mod.rs`, `src/store/writer.rs`, `src/store/queries.rs` — existing
  single-writer store API the enumerator persists through.
</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- Phase 1 SQLite store (`src/store/`): single-writer, WAL, idempotent UPSERT
  API. The enumerator persists pubkeys and run/resume state through this — no
  new persistence layer needed.
- `run` table already carries every column resume needs (cursor, maxLevId
  start/end, status, config_json). No schema migration for Phase 2.
- `flume` channel dependency is already present (Phase 1 added it for the
  analyze→writer seam) — available if the walk wants to decouple fetch from the
  single writer, though not required by this phase.

### Established Patterns
- Single-writer persistence: all DB mutations go through one writer with batched
  transactions. Pubkey inserts and per-batch run updates must respect this.
- EAV/idempotent-write discipline from Phase 1 (UPSERT / `INSERT OR IGNORE`)
  carries forward — re-processing within a run must not duplicate.
- Stack discipline: dependencies land in their owning phase. Phase 2 adds the
  HTTP/GraphQL client deps (reqwest/tokio/serde already partly present); fetch
  pipeline deps (rayon) stay for Phase 3.

### Integration Points
- New `graphql` client module → calls the LMDB2GraphQL `/graphql` endpoint.
- Enumerator → persists pubkeys + run resume-state via the Phase-1 store writer.
- Reusable client (D-11) is the seam Phase 3's fetcher extends.
</code_context>

<specifics>
## Specific Ideas

- The walk must honor the contract's "opaque cursor" rule: pass `endCursor` back
  verbatim as `after`; never parse or construct it (`INVALID_CURSOR` is a client
  bug if it happens → restart from page 1 per D-08).
- Distinct-pubkey-exactly-once is a per-snapshot guarantee; combined with
  record-and-continue drift handling (D-09), a pubkey added mid-walk may or may
  not appear depending on cursor position — that is acceptable, not an error.
</specifics>

<deferred>
## Deferred Ideas

- **Live enumeration streaming directly into the fetch pipeline** — Phase 3
  (bounded tokio→channel→rayon pipeline) decides whether it re-walks live or
  reads the persisted `pubkey` table; not decided here.
- **Config-file-driven endpoint/parameter configuration (OPS-03)** — Phase 4.
- **Full `run`/`export` CLI** — Phase 5 owns the CLI surface (D-12).
- **Direct `heed` LMDB reads to bypass the GraphQL hop (PERF-01)** — v2,
  profiling-gated.

</deferred>

---

*Phase: 2-GraphQL Client + Author Enumeration*
*Context gathered: 2026-06-25*
</content>
</invoke>
