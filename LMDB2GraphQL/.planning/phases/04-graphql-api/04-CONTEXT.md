# Phase 4: GraphQL API - Context

**Gathered:** 2026-06-13
**Status:** Ready for planning

<domain>
## Phase Boundary

Expose the completed Phase-3 query engine as a **read-only GraphQL endpoint**
(`async-graphql` 7.2.1 + `axum` 0.8.x). This phase builds the HTTP/schema layer
ONLY — it composes the engine's existing library functions; it does not
reimplement query logic.

The schema must satisfy API-01..API-06:
- **`events(filter, after, limit)`** — filter by ids, authors, kinds, since, until (API-01)
  and by tag name+values (API-02), newest-first, with cursor pagination.
- **`latestPerAuthor(kind, perAuthor, authors)`** — latest N events per pubkey (API-03).
- **`stats`** — event count, max levId, dbVersion (API-04).
- **Hard limit ceiling + cursor pagination** on `(created_at, lev_id)` (API-05).
- **No mutation surface** — query-only schema (API-06).

Engine surface this layer calls (already built, Phase 3):
- `query::engine::execute_query(env, &NostrFilter, &DictCache, Option<&PageCursor>)
  -> Result<(Vec<DecodedEvent>, Option<PageCursor>), QueryError>`
- `query::engine::latest_per_author(env, kind, per_author, &[String], &DictCache)
  -> Result<HashMap<String, Vec<DecodedEvent>>, QueryError>`
- `query::filter::{NostrFilter, TagFilter, PageCursor}` (cursor `encode`/`decode` exist).
- `lmdb::types::{DecodedEvent, NostrEvent, LevId}` — `DecodedEvent { event: NostrEvent, raw_json }`.

**Out of scope (later phases):**
- `/health`, `/ready`, Docker subsystem, docker-compose, CI fixture assertions,
  startup drift surfacing — **Phase 5** (OPS-01..OPS-04).
- Subscriptions / live push, REST facade, Prometheus `/metrics` — **v2**
  (API-V2-01/02, OBS-01).
- Any change to the query engine's semantics — locked in Phase 3.

</domain>

<decisions>
## Implementation Decisions

### Event representation (API-01/02)
- **D-01:** **Typed fields + a `raw` escape hatch.** Expose `id`, `pubkey`, `kind`,
  `createdAt`, `content`, `sig` as typed GraphQL fields (from `NostrEvent`), PLUS a
  `raw: String!` field carrying `DecodedEvent.raw_json` — the byte-exact JSON strfry
  stored (Phase-2 D-01). Clients get selectable structure AND a lossless passthrough
  for anything `NostrEvent` does not model.
- **D-02:** **`tags` is a native nested list `[[String!]!]`** — maps `NostrEvent.tags:
  Vec<Vec<String>>` directly (e.g. `[["e","<id>"],["p","<pk>"]]`). No custom JSON
  scalar; tags stay schema-typed and selectable. (Rejected: JSON-scalar tags, and
  defer-to-raw-only — both lose structural queryability.)

### Pagination contract (API-05)
- **D-03:** **Simple page object**, not a Relay Connection. `events(...)` returns
  `{ events: [Event!]!, endCursor: String, hasMore: Boolean! }`. Maps 1:1 to the
  engine's `(Vec<DecodedEvent>, Option<PageCursor>)`:
  - `endCursor` = `cursor.map(PageCursor::encode)` (opaque base64, D-11).
  - `hasMore` = `cursor.is_some()`.
  - Next page: consumer passes the opaque cursor back as an **`after: String`** arg;
    resolver `PageCursor::decode`s it (fail-closed `CursorDecode` on bad input, T-03-CUR)
    and passes `Some(&cursor)` to `execute_query`.
  (Rejected: full Relay edges/node/pageInfo — per-edge cursors are unused; the single
  opaque cursor suffices for this read-only adapter.)

### Limit ceiling & DoS guards (API-05)
- **D-04:** **Hard ceiling = 500 events/page; default = 100** when `limit` is omitted.
  The ceiling is enforced **at the API layer before calling the engine** (engine's own
  `DEFAULT_WINDOW_SIZE=256` / `limit=0` behavior is internal and unchanged).
- **D-05:** **Cap silently — never reject** (API-05 wording: "capped, not rejected").
  A request asking for `limit > 500` is clamped to 500 and served; no error.
- **D-06:** **No async-graphql depth/complexity limits** for v1. The limit ceiling plus
  the engine's `MAX_ROUNDS` bound is the DoS guard. The schema is shallow
  (`Event` / page object / author groups), so nested-query depth abuse is limited.
  (Revisit if the endpoint is exposed to untrusted public traffic — see Deferred.)

### latestPerAuthor shape (API-03)
- **D-07:** **List of author groups.** Surface the engine's
  `HashMap<String, Vec<DecodedEvent>>` as `[{ author: String!, events: [Event!]! }]`,
  one entry per requested author that has events — preserves the per-author grouping
  the engine deliberately keeps (Phase-3 D-12). (Rejected: flatten to one list — loses
  grouping, forces client re-grouping by pubkey.)
- **D-08:** **Bound `perAuthor` only.** Clamp `perAuthor` to the ceiling (≤500, silent
  clamp like D-05); **the number of `authors` is NOT capped** (user's explicit choice).
  Each author is one bounded reverse `Event__pubkeyKind` scan capped at `perAuthor`, so
  total work = `authors.len() × perAuthor`. The unbounded author-count fan-out is an
  accepted v1 risk — flag for observability (see Deferred).

### stats (API-04)
- **D-09:** `stats` returns `{ eventCount, maxLevId, dbVersion }`:
  - `eventCount` via `mdb_stat` on the `EventPayload` sub-DB (entry count).
  - `maxLevId` = the last (largest) key of `EventPayload` (`MDB_INTEGERKEY`).
  - `dbVersion` from the already-read `Meta` (the value the startup gate verified).
  Read-only, short txn — same per-query-txn invariant as the engine (Phase-2 D-08).

### Read-only surface (API-06)
- **D-10:** **Query-only schema** — no `Mutation` root type registered. The schema is
  structurally incapable of mutations. Consistent with the project-wide read-only,
  `MDB_RDONLY` posture.

### Claude's Discretion
- Module layout for the GraphQL/HTTP layer (e.g. `src/graphql/` schema + resolvers,
  `src/http.rs` or `src/server.rs` axum wiring), within the locked stack
  (`async-graphql` 7.2.1, `async-graphql-axum` 7.2.1, `axum` 0.8.x).
- `QueryError` → GraphQL error mapping (recommendation: `CursorDecode` and malformed
  hex inputs → client-facing GraphQL errors; `Lmdb`/`Payload` → opaque internal error +
  `tracing::error!`). Corrupt-payload skip-warn-count already handled inside the engine.
- HTTP endpoint path (suggest single `POST /graphql`), bind address/port (from config;
  follow the `~/deepfry/lmdb2graphql.yaml` convention), and the axum router shape.
- Whether `execute_query`/`latest_per_author` run inside `tokio::task::spawn_blocking`
  (heed/LMDB is synchronous — recommended pattern per CLAUDE.md).
- How the opened `heed::Env` + `DictCache` are shared into resolvers (async-graphql
  `Data`/context, an `Arc`-wrapped app state, etc.).
- Field nullability details for `Event` (e.g. whether `content`/`sig` are nullable —
  follow what `NostrEvent`'s typed fields guarantee).
- GraphiQL playground and schema introspection on/off (user did not constrain; default
  to enabling GraphiQL + introspection for a read-only service unless Phase-5 hardening
  says otherwise).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project decisions & requirements
- `.planning/REQUIREMENTS.md` — **API-01, API-02, API-03, API-04, API-05, API-06**
  (this phase's scope) + the v2 deferrals (subscriptions, REST facade) so they are not
  pulled forward.
- `.planning/ROADMAP.md` — Phase 4 goal + 6 success criteria.
- `.planning/PROJECT.md` — Approach B, Rust stack, read-only/no-mutations/no-subscriptions
  posture, the "latest 20 kind-1 per pubkey" motivating query (→ `latestPerAuthor`),
  and Key Decisions (Query API = GraphQL; request/response only for v1).
- `.planning/phases/03-query-engine/03-CONTEXT.md` — Phase-3 engine decisions D-01..D-12
  (flat newest-first `events()` ordering D-10; opaque `(created_at, lev_id)` cursor D-11;
  per-author grouped `latestPerAuthor` D-12; over-fetch/backfill honoring `limit` D-07).
- `.planning/phases/02-payload-decoding-index-scan-primitives/02-CONTEXT.md` — `DecodedEvent`
  `{ event, raw_json }` byte-exact passthrough (D-01), short per-query txns (D-08).

### On-disk encoding (authoritative — verified against strfry source)
- `spec.md` §3.1 / §3.4 — `EventPayload` sub-DB + `levId` semantics (for `stats`:
  `mdb_stat` count, max levId = last key). `levId` is monotonic, NOT chronological.
- `spec.md` §6.4 — read-only safety + short read txns (the per-query-txn invariant the
  resolvers must keep when calling the engine).
- ⚠️ **spec.md caveat (carried from Phases 1-3):** spec.md is a pre-pivot **Approach A**
  document. Use it only for verified on-disk encodings/caveats (§3.x, §6.x), NOT for
  architecture.

### Project stack reference
- `CLAUDE.md` (this project) — `async-graphql` 7.2.1 (`#[Object]`/`#[SimpleObject]` macros,
  query-only schema, built-in complexity/depth limits available but deferred per D-06),
  `async-graphql-axum` 7.2.1 (`GraphQLRequest`/`GraphQLResponse`, axum ^0.8.1 compat),
  `axum` 0.8.x, `serde_json` + local `NostrEvent` (NOT the `nostr` crate), `tokio`
  `spawn_blocking` for synchronous heed calls.
  ⚠️ Its `rusqlite`/SQLite "derived index/store" entries are **stale** and contradict
  Approach B — ignore.

### Existing code to compose (this repo)
- `src/query/engine.rs` — `execute_query`, `latest_per_author` (the two functions the
  resolvers call).
- `src/query/filter.rs` — `NostrFilter`, `TagFilter`, `PageCursor` (`encode`/`decode`),
  `QueryError` (error-mapping source). `NostrFilter` is *constructed by Phase-4 resolvers*.
- `src/lmdb/types.rs` — `DecodedEvent { event: NostrEvent, raw_json }`, `NostrEvent`
  (typed `id`, `pubkey`, `kind`, `created_at`, `content`, `sig`, `tags: Vec<Vec<String>>`),
  `LevId`.
- `src/lmdb/payload.rs` — `DictCache` (the resolver app-state must hold one; shared into
  `execute_query`/`latest_per_author`).
- `src/lmdb/env.rs`, `src/lmdb/meta.rs` — the opened `heed::Env` + `Meta` (`dbVersion` for
  `stats`); `src/main.rs` is the current startup gate the server wiring extends.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`src/query/engine.rs`** — both query entry points are complete and return decoded
  events + an `Option<PageCursor>`. The GraphQL layer is a thin adapter: build a
  `NostrFilter` from GraphQL args → call → map results to schema types.
- **`PageCursor::encode/decode`** (`src/query/filter.rs`) — the opaque-cursor codec is
  already implemented and fail-closed (T-03-CUR). The `after` arg uses it directly; no
  new cursor logic needed.
- **`DecodedEvent.raw_json`** — the exact-passthrough source for the `Event.raw` field (D-01).
- **`DictCache`** — must be created once at startup and shared into resolvers (it caches
  zstd dictionaries across queries).

### Established Patterns
- **Per-call short `read_txn()` inside engine primitives** (Phase-2 D-08) — resolvers do
  NOT hold a txn; the engine reopens per batch. Keep that invariant.
- **`.open()` never `.create()`**, `MDB_RDONLY` only — the server adds no write paths.
- **Startup gate sequence in `src/main.rs`** — open env → Meta gates → comparator
  self-check. The Phase-4 server boots *after* those gates pass, reusing the same opened
  `Env` (and the read `Meta` for `stats.dbVersion`).
- **`thiserror` typed errors in libs / `anyhow` in `main`** — extend for the
  `QueryError`→GraphQL boundary (resolvers translate; do not leak internals).

### Integration Points
- The opened `heed::Env` + `DictCache` (+ `Meta`) become axum/async-graphql app state,
  injected into resolvers via async-graphql `Data`/context.
- Synchronous heed/LMDB calls run under `tokio::task::spawn_blocking` to avoid blocking
  the async runtime (CLAUDE.md pattern).
- `main.rs` transitions from "run gates then exit" to "run gates → bind axum server".

</code_context>

<specifics>
## Specific Ideas

- The motivating query "latest 20 kind-1 events per pubkey" (PROJECT.md) is exactly
  `latestPerAuthor(kind: 1, perAuthor: 20, authors: [...])` → `[{ author, events }]`.
- `events(...)` page object keeps the consumer contract minimal: `{ events, endCursor,
  hasMore }` + an `after` arg — deliberately simpler than Relay because the engine's
  single opaque cursor already expresses everything pagination needs here.
- `Event.raw` is the lossless fallback: if a client needs a field `NostrEvent` does not
  model, it reads `raw` and parses — no schema change required.

</specifics>

<deferred>
## Deferred Ideas

- **async-graphql depth/complexity limits** — not enabled for v1 (D-06). Revisit if the
  endpoint is ever exposed to untrusted public traffic; the hooks exist in async-graphql.
- **Bounding `latestPerAuthor` author-count fan-out** — explicitly left uncapped (D-08).
  Flag for **observability** (Phase-5 / v2): a request with a very large `authors` list
  fans out to one scan per author. Consider a per-request author cap or metrics if real
  traffic proves pathological.
- **Relay Connection pagination** — rejected for v1 (D-03); a future API revision could
  add it if Relay/Apollo tooling compatibility becomes a requirement.
- **Subscriptions / live push, REST facade, Prometheus `/metrics`** — v2 scope
  (API-V2-01/02, OBS-01), not this phase.
- **Doc-sync (carried from Phases 1-3):** stale `rusqlite`/SQLite "derived store" wording
  in `CLAUDE.md` still wants a cleanup pass — not Phase 4 code work.

None of the discussion strayed outside the phase domain.

</deferred>

---

*Phase: 4-GraphQL API*
*Context gathered: 2026-06-13*
