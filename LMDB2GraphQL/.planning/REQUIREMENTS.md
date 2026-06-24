# Requirements: LMDB2GraphQL

**Defined:** 2026-06-10
**Core Value:** Serve correct, rich queries over strfry's events by reading strfry's live on-disk state directly — never copying event data or indexes out of strfry, never writing to strfry's database.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.
**Architecture:** Approach B — LMDB2GraphQL reads strfry's own `Event__*` indexes live (no derived store, no sync engine).

### LMDB Access & Comparators

- [x] **LMDB-01**: LMDB2GraphQL opens strfry's LMDB environment read-only (`MDB_RDONLY`), never opening a write transaction
- [x] **LMDB-02**: LMDB2GraphQL reads `Meta` and refuses to run (exits loudly) if `dbVersion != 3`
- [x] **LMDB-03**: LMDB2GraphQL reads `Meta.endianness`, compares to host, and refuses to run on mismatch
- [x] **LMDB-04**: LMDB2GraphQL sets LMDB `map_size` ≥ strfry's configured size so the environment opens successfully
- [x] **LMDB-05**: LMDB2GraphQL reimplements strfry/golpe's custom comparators (`StringUint64`, `Uint64Uint64`, `StringUint64Uint64`) and registers them via `mdb_set_compare` on each `Event__*` sub-DB it scans
- [x] **LMDB-06**: LMDB2GraphQL runs a comparator self-check against a pinned fixture DB at startup and **fails closed** if its scan order disagrees with strfry's known-correct order
- [x] **LMDB-07**: LMDB2GraphQL decodes `EventPayload` `0x00` (raw JSON) values to full event JSON
- [x] **LMDB-08**: LMDB2GraphQL decodes `EventPayload` `0x01` (zstd-dictionary-compressed) values using `CompressionDictionary[dictId]`
- [x] **LMDB-09**: LMDB2GraphQL keeps read transactions short (per-query, bounded by limit) so strfry can reclaim pages
- [x] **LMDB-10**: LMDB2GraphQL targets an **explicitly pinned strfry version/digest** — the version the parent DeepFry stack deploys (currently `dockurr/strfry`, unpinned `:latest` in `Dockerfile.strfry`) — recorded in config/docs as a shared contract; its fixture DB and comparators are generated against that exact version, and at startup it logs the configured target alongside the detected on-disk `dbVersion` (failing closed via LMDB-02 if the schema/migration version doesn't match the pin)

### Query Engine (over strfry's live indexes)

- [x] **QRY-01**: LMDB2GraphQL resolves `events()` filters (ids, authors, kinds, since/until) by scanning the most selective applicable strfry index (`Event__id` / `Event__pubkey` / `Event__pubkeyKind` / `Event__kind` / `Event__created_at`) to produce ordered `levId`s
- [x] **QRY-02**: LMDB2GraphQL resolves tag filters by scanning `Event__tag` (`tagName ‖ tagValue ‖ created_at`)
- [x] **QRY-03**: LMDB2GraphQL resolves `latestPerAuthor` via `Event__pubkeyKind` prefix scans (newest-first, N events per pubkey)
- [x] **QRY-04**: LMDB2GraphQL hydrates full event JSON by point-looking-up `EventPayload[levId]` for each matched result
- [x] **QRY-05**: LMDB2GraphQL filters out NIP-40 expired events (`expiration != 0 && expiration <= now`) at query time

### GraphQL API

- [x] **API-01**: A consumer can query `events()` filtered by ids, authors, kinds, since, until, and limit
- [x] **API-02**: A consumer can query `events()` filtered by a tag (name + values)
- [x] **API-03**: A consumer can query `latestPerAuthor(kind, perAuthor, authors)` to get the latest N events per pubkey
- [x] **API-04**: A consumer can query `stats` (event count via `mdb_stat` on `EventPayload`, max levId, dbVersion)
- [x] **API-05**: The API enforces a hard limit ceiling and cursor pagination on `(created_at, lev_id)` so no single query scans the whole DB
- [x] **API-06**: The API is read-only — it exposes no mutations

### Operations & Deployment

- [x] **OPS-01**: LMDB2GraphQL exposes `/health` and `/ready` endpoints (`/ready` gates on env open + passing the comparator self-check)
- [x] **OPS-02**: LMDB2GraphQL ships as a Docker subsystem with a `docker-compose` service, co-located with strfry, mounting `strfry-db` read-only (`:ro`)
- [x] **OPS-03**: CI pins the **same strfry version/digest the parent DeepFry stack deploys** (per LMDB-10), generates a fixture `strfry-db` from it, and asserts LMDB2GraphQL (a) decodes both `0x00` and `0x01` payloads and (b) reproduces strfry's index scan order via the reimplemented comparators
- [x] **OPS-04**: `stats` / startup output surfaces the expected (pinned) strfry version and the detected on-disk `dbVersion`, so operators can spot drift if the parent's `dockurr/strfry` image moves

## Milestone v1.1 Requirements (CORS Support)

Defined 2026-06-23. Enables a browser-based frontend served from a different host to query the GraphQL API cross-origin. Corpus is public Nostr relay data → wildcard origin, no credentials.

### CORS

- [x] **CORS-01**: A browser frontend served from a different origin can issue `POST /graphql` (and `GET /graphql` for GraphiQL) and read the response — the server sends `Access-Control-Allow-Origin: *`
- [x] **CORS-02**: The server correctly answers CORS preflight `OPTIONS` requests for `/graphql`, allowing the methods (`GET`, `POST`, `OPTIONS`) and request headers (e.g. `Content-Type`) a GraphQL-over-HTTP client sends, so non-simple POSTs succeed
- [x] **CORS-03**: The server does **not** send `Access-Control-Allow-Credentials` (wildcard origin, no cookies/auth) — consistent with the unauthenticated read-only surface and compatible with `Allow-Origin: *`
- [x] **CORS-04**: The `CorsLayer` is added without weakening existing protections — the body-limit layer, the `503`-until-ready schema gate, and the `bind_address` loopback default all continue to behave as before (CORS relaxes only the browser same-origin policy, not network exposure)

## Milestone v1.2 Requirements (Distinct Author Enumeration)

Defined 2026-06-24. Exposes the set of distinct pubkeys that have authored ≥1 event — a capability the existing API cannot serve (`latestPerAuthor` requires a caller-supplied author list; `events()` only yields pubkeys by paginating every event). Pubkeys-only output; counts are explicitly out of scope for this milestone (they would change the query from O(distinct authors) to O(total events) and reintroduce unbounded per-author fan-out).

### Distinct Authors

- [ ] **QRY-06**: LMDB2GraphQL enumerates the distinct pubkeys present in the `Event__pubkey` index in O(distinct authors) via a seek-skip scan — read one entry, take the 32-byte pubkey prefix, then re-seek with lower bound = `increment_be(prefix)` to jump to the next author — using short per-call read transactions and never opening a write txn
- [ ] **API-07**: A consumer can query `authors(after, limit)` and receive a paginated `AuthorsPage { authors: [String!]!, hasMore, endCursor }` of distinct hex pubkeys, with `limit` clamped to the same hard ceiling as `events()` and an opaque `after` cursor (the last pubkey returned) decoded fail-closed

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Observability

- **OBS-01**: Prometheus `/metrics` endpoint with detailed query/scan metrics

### API

- **API-V2-01**: Live subscriptions / push for newly matching events
- **API-V2-02**: REST/JSON facade over the query engine

### Portability

- **PORT-01**: Cross-architecture / big-endian support via byte-swapping (currently assumes co-located same-arch)

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Approach A (derived store / index replication) | Duplicates data and violates the "no event payloads outside StrFry" data-separation rule |
| A sync engine (full import / incremental tailer / reconciler) | Unnecessary under Approach B — LMDB2GraphQL reads live state, so no insert/delete sync and no staleness window |
| Replaceable / parameterized-replaceable collapse logic | strfry already enforces this at write time; its physical index set is already collapsed |
| Approach C (shell out to `strfry scan`/`export`) | Prototype-only, not a production query surface |
| Any write transaction against strfry's env | strfry is sole writer; a second writer corrupts the environment |
| Mutations / relay behavior | LMDB2GraphQL is a query surface, not a relay |
| Hot-path signature re-verification | strfry already validated on ingest; expensive with no correctness gain |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| LMDB-01 | Phase 1 | Complete (Plan 01-01) |
| LMDB-02 | Phase 1 | Complete |
| LMDB-03 | Phase 1 | Complete |
| LMDB-04 | Phase 1 | Complete (Plan 01-02) |
| LMDB-05 | Phase 1 | Complete (Plan 01-01) |
| LMDB-06 | Phase 1 | Complete |
| LMDB-10 | Phase 1 | Complete (Plan 01-02) |
| LMDB-07 | Phase 2 | Complete (Plan 02-01) |
| LMDB-08 | Phase 2 | Complete (Plan 02-02) |
| LMDB-09 | Phase 2 | Complete (Plan 02-03) |
| QRY-01 | Phase 3 | Complete |
| QRY-02 | Phase 3 | Complete |
| QRY-03 | Phase 3 | Complete |
| QRY-04 | Phase 3 | Complete |
| QRY-05 | Phase 3 | Complete |
| API-01 | Phase 4 | Complete |
| API-02 | Phase 4 | Complete |
| API-03 | Phase 4 | Complete |
| API-04 | Phase 4 | Complete |
| API-05 | Phase 4 | Complete |
| API-06 | Phase 4 | Complete |
| OPS-01 | Phase 5 | Complete |
| OPS-02 | Phase 5 | Complete |
| OPS-03 | Phase 5 | Complete |
| OPS-04 | Phase 5 | Complete |
| CORS-01 | Phase 6 | Complete |
| CORS-02 | Phase 6 | Complete |
| CORS-03 | Phase 6 | Complete |
| CORS-04 | Phase 6 | Complete |
| QRY-06 | Phase 7 | Pending |
| API-07 | Phase 7 | Pending |

**Coverage:**

- v1.0 requirements: 25 total — mapped to Phases 1–5, all complete
- v1.1 requirements: 4 total (CORS-01..04) — mapped to Phase 6 (roadmap created 2026-06-23)
- v1.2 requirements: 2 total (QRY-06, API-07) — mapped to Phase 7 (roadmap created 2026-06-24)
- Unmapped: 0

---
*Requirements defined: 2026-06-10 (v1.0); 2026-06-23 (v1.1 CORS); 2026-06-24 (v1.2 distinct authors)*
*Last updated: 2026-06-24 — v1.2 distinct-author requirements mapped to Phase 7*
