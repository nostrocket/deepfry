# Feature Research

**Domain:** Read-only queryable index layer over a Nostr relay event store (strfry/LMDB → derived SQLite → GraphQL)
**Researched:** 2026-06-10
**Confidence:** HIGH (spec.md and PROJECT.md are the authoritative primary sources; ecosystem research corroborates)

---

## Feature Landscape

### Table Stakes (Users Expect These)

Features that must exist or the service is incorrect/useless. Each maps to a correctness or completeness requirement from spec.md.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| **Version/endianness hard gate** | Without it, the service silently decodes garbage on DB version bump or cross-arch deployment. Users expect the service to refuse loudly rather than return corrupt data. | LOW | Read `Meta.dbVersion` and `Meta.endianness` on startup; exit non-zero with a clear error if either mismatches. This is correctness-critical — spec §6.1, §6.3. Must happen before any scan begins. |
| **Full LMDB importer (initial load)** | Without a full import there is nothing to query. | MEDIUM | Windowed `levId` scan of `EventPayload` using short read transactions (one per window). Decode both `0x00` (raw) and `0x01` (zstd-dictionary) payloads. Upsert into SQLite derived store. |
| **Incremental tailer (levId watermark)** | Queries go stale immediately after initial load without continuous catch-up. | MEDIUM | `MDB_SET_RANGE` from `lastLevId+1` on each poll. Catches inserts only — deletions are handled by the reconciler. |
| **Dual-format payload decoder (`0x00` / `0x01`)** | Compacted databases store `0x01` zstd-dictionary payloads. Failing to handle this silently drops events on compacted deployments. | MEDIUM | Must load dictionary from `CompressionDictionary[dictId]` and decompress. Cache dictionaries by `dictId`. Detect `0x01` prefix on startup and warn if zstd support unavailable. Spec §3.2, §6.8. |
| **`events()` GraphQL query** | Core NIP-01-equivalent filter surface. Consumers expect `ids`, `authors`, `kinds`, `since`, `until`, `tag`, `limit`, `orderBy` — the filter vocabulary every Nostr tool speaks. | MEDIUM | Translates directly to SQL predicates on the derived store. Requires `event_tags` join table for tag filtering. NOT served from strfry's `Event__*` sub-DBs (forbidden). |
| **Hard limit ceiling on `events()`** | Without a ceiling a single query can full-scan the SQLite derived store, OOM the service, and block other queries. | LOW | Server-enforced max (e.g. 1000 rows). Client-supplied `limit` is capped at the ceiling; never ignored. Spec §5, §6.9. |
| **`stats` query** | Operators need to know whether the service is live, how fresh the index is, and what DB version it decoded from. | LOW | Returns: `eventCount`, `maxLevId` (watermark), `lastSyncAt`, `dbVersion`, `endianness`. |
| **NIP-40 expiration filtering** | Serving expired events is semantically incorrect. Relay already enforces this at ingest; the query layer must too. | LOW | Filter `expiration != 0 AND expiration <= now()` at query time in SQL. Also apply during reconcile to proactively remove expired rows. Spec §6.7. |
| **Replaceable-event collapse** | Serving superseded replaceable events (kinds 0, 3, 10000–19999) alongside their replacements violates NIP-01 relay semantics. Parameterized replaceable (kinds 30000–39999) collapse on `(pubkey, kind, d-tag)`. | MEDIUM | The `EventPayload` set after a full scan is already correct (strfry enforces this at write time). But incremental tailing can transiently expose both versions. The derived store must enforce: for replaceable kinds keep highest `created_at` per `(pubkey, kind)`; for parameterized keep highest `created_at` per `(pubkey, kind, d-tag)`. Spec §6.6. |
| **Periodic full reconciler (deletion detection)** | `levId` tailing catches inserts only. NIP-09 deletions, replaceable-event supersessions, and expiration sweeps delete `EventPayload` rows without emitting new `levId`s. Without reconciliation the derived store serves deleted events forever. | HIGH | Re-scan all `EventPayload` keys, diff against derived-store `lev_id` set, delete stale rows. Correctness-critical for query correctness. Spec §6.5. Cadence configurable; document staleness window. |
| **Change-detection probe** | Avoids redundant full reconcile scans when nothing has changed. Without it the service must either reconcile blindly (wasteful) or not reconcile at all (stale). | LOW | Read `Meta.negentropyModificationCounter` (increments on any add/remove) and/or `inotify`/`kqueue` watch on `data.mdb`. Trigger reconcile only when counter advances or file mtime changes. Spec §4.2 O7. |
| **Read-only LMDB access enforcement** | A second writer to an LMDB env corrupts it. Users expect co-located services to not destroy their relay DB. | LOW | Open with `MDB_RDONLY`. Never open a write transaction. This is a hard safety constraint, not optional polish. Spec §6.4. |
| **Short read-transaction discipline** | Long-held read transactions prevent LMDB from reclaiming pages; the `data.mdb` file grows unbounded while strfry writes. | LOW | Open one read txn per scan window; close before sleep/next window. Never hold a read txn across an idle interval. Spec §6.4, §6.10. |
| **No `Event__*` sub-DB access** | Scanning strfry's index sub-DBs with a foreign comparator silently returns wrong subsets. Correctness-critical. | LOW | Implementation must never open `Event__id`, `Event__pubkey`, `Event__kind`, `Event__pubkeyKind`, `Event__tag`, or any `Event__*` dbi for range traversal. Spec §6.2. |
| **Staleness window documentation** | Operators must understand the consistency model. A query layer that does not document when deleted events disappear is a correctness hazard. | LOW | API response for `stats` includes `lastFullReconcileAt`; docs describe the deletion staleness window explicitly. |

### Differentiators (Features REQ Cannot Express)

Features that provide value beyond what the Nostr `REQ` protocol can express, or operational polish that a naive implementation omits.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **`latestPerAuthor(kind, perAuthor, authors)` query** | The primary motivating query: "latest N kind-K events per author." `REQ` has no `GROUP BY` equivalent. Without this query, the entire service reduces to a thin SQL wrapper over what a relay already provides via `REQ`. | MEDIUM | Implemented as `ROW_NUMBER() OVER (PARTITION BY pubkey ORDER BY created_at DESC)` filtered to `kind` and `rn <= perAuthor`. Requires the `ix_pubkey_kind_created` covering index. This is the single highest-value differentiator. |
| **Cursor-based pagination on `events()`** | Allows stable page traversal over large result sets without offset drift (events added/deleted between pages). | MEDIUM | Composite cursor on `(created_at, lev_id)` encodes position without offset. `after` / `before` cursor args on `events()`. Required for large-dataset consumers. |
| **`orderBy` enum (`CREATED_AT_DESC` / `CREATED_AT_ASC`)** | Ascending order (chronological feed reconstruction) is not efficiently expressible in `REQ` without `since`/`until` boundary walking. | LOW | Single enum on `events()`; maps to `ORDER BY created_at ASC/DESC`. Trivial given the covering index. |
| **Tag filter on `events()` (`TagFilter { name, values }`)** | `REQ` supports `#e`, `#p`, etc. but the derived store can combine tag filters with other predicates (kinds + authors + tag) more efficiently. | MEDIUM | Requires `event_tags(lev_id, tag_name, tag_value)` join table with index on `(tag_name, tag_value)`. |
| **Prometheus `/metrics` endpoint** | Operators running the DeepFry stack already scrape metrics. A service without metrics is invisible in production dashboards. | LOW | Expose: `deepfry_events_total`, `deepfry_last_sync_timestamp`, `deepfry_last_reconcile_timestamp`, `deepfry_sqlite_query_duration_seconds` (histogram), `deepfry_lmdb_scan_duration_seconds` (histogram). |
| **`/health` and `/ready` HTTP endpoints** | Docker healthcheck and Kubernetes liveness/readiness probes require standard endpoints. Without them, container orchestration cannot determine service health. | LOW | `/health` returns 200 always (process alive). `/ready` returns 200 once initial import is complete and the service can serve queries; 503 during initial import. |
| **Staleness reporting in `stats`** | Operators need to know the worst-case age of stale deleted events. Exposes `lastFullReconcileAt` and `deletionStalenessWindowSeconds` in `stats` response. | LOW | `stats` already returns `lastSyncAt`; add `lastFullReconcileAt`. Derived from SQLite metadata row updated after each reconcile run. |
| **Post-compaction full reconcile trigger** | strfry compaction rewrites `EventPayload` (introducing `0x01`) and can relocate dictionaries. A naive tailer will miss compaction-induced changes. | LOW | Detect via `negentropyModificationCounter` drop or large jump, or operator-triggered endpoint `POST /reconcile`. Spec §6.11. |
| **Fixture-pinned integration test against known strfry DB** | Protects against silent breakage when strfry upgrades. No other relay-adjacent query layer pins a specific relay version for regression testing. | MEDIUM | CI: pin a strfry git commit, generate a fixture `strfry-db`, assert deepfry decodes all events correctly, assert version gate fires on wrong `dbVersion`. Spec §6.11. |
| **`db-version` and `endianness` in `stats` response** | Makes the gating decision observable. Operators can confirm which DB version deepfry decoded without inspecting logs. | LOW | Already read from `Meta`; surface in `stats` response. |

### Anti-Features (Deliberately NOT Building)

Features that seem natural but must be explicitly excluded because they are out of scope per PROJECT.md, correctness hazards, or architectural violations.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **Mutations / write relay behavior** | Consumers may want a single endpoint for reads and writes. | deepfry is a query surface, not a relay. Adding mutations would require write access to strfry's LMDB (corrupts the env if opened with a second writer) or a relay protocol — both violate the core architecture. | Route writes to strfry directly at `ws://localhost:7777`. |
| **Live subscriptions (WebSocket push) in v1** | Nostr clients expect `REQ`/`EVENT` push semantics. | Adds stateful connection management, backpressure, and event routing complexity. The derived store is eventually-consistent; push semantics amplify correctness hazards around deletion staleness. | Request/response only for v1. Subscriptions deferred to v2 when the consistency model is proven. |
| **Signature re-verification on the hot path** | Some consumers want end-to-end trust. | strfry already verifies sigs on ingest. Re-verifying per-query is expensive (Schnorr verification is ~100µs per event; a 1000-row response would add 100ms). Adds no correctness value for events that passed strfry's validator. | If sig integrity is required, use strfry's NIP-42 authenticated relay directly. |
| **Reading / range-scanning any `Event__*` index sub-DB** | Direct use of strfry's indexes would avoid building a derived store. | LMDB does not persist custom comparators. A foreign reader range-scanning `Event__pubkey`, `Event__kind`, etc. will silently receive wrong subsets. This is a silent correctness failure, not a performance tradeoff. | Read only `EventPayload` (Approach A). Build own derived index with standard SQL indexes. |
| **Writing to strfry's LMDB environment** | Could enable cache warming or cleanup from deepfry. | LMDB supports exactly one writer. A second write transaction corrupts the environment. strfry is the sole writer; deepfry is `MDB_RDONLY` always. | Never; route any mutations through strfry's plugin interface. |
| **Cross-architecture / big-endian support in v1** | Portability. | deepfry and strfry are co-located (same machine). x86-64 and arm64 are both little-endian in practice. Adding byte-swap logic introduces complexity and a new test surface. The endianness gate already handles detection. | Assert endianness match; refuse loudly on mismatch. Defer byte-swap support to a future version. |
| **Approach B (embedding strfry/golpe code)** | Would give exact live replaceable/deletion semantics without a reconcile step. | Version-locks deepfry to specific strfry internals (C++ ABI, golpe struct layout). Any strfry update that changes `PackedEvent` or comparator registration silently breaks deepfry. Maintenance cost is prohibitive. | Approach A (derived store with periodic reconcile). Accept the staleness window. |
| **Approach C (shelling out to `strfry scan`/`export`)** | Simple to prototype; no LMDB knowledge required. | Process-spawn overhead, stdout parsing fragility, no incremental tailing, no cursor. Not viable for production latency or volume. | Approach A only in production. |
| **Offset-based pagination** | Familiar to SQL consumers. | Offsets drift when events are inserted/deleted between pages. In an eventually-consistent derived store, offset pagination produces duplicates and gaps. | Cursor-based pagination on `(created_at, lev_id)`. |
| **Full-text / semantic search** | Nostr search (NIP-50) is a common relay feature. | Out of scope for this service per the broader DeepFry stack design (semantic-search is a separate planned subsystem). Adding FTS to deepfry duplicates responsibility. | Separate `semantic-search` subsystem in the DeepFry stack. |

---

## Feature Dependencies

```
[Version/endianness hard gate]
    └──must-precede──> [Full LMDB importer]
                           └──must-precede──> [Incremental tailer]
                           └──must-precede──> [Periodic full reconciler]
                           └──must-precede──> [GraphQL API: events(), stats]

[Dual-format payload decoder]
    └──required-by──> [Full LMDB importer]
    └──required-by──> [Periodic full reconciler]

[Full LMDB importer]
    └──populates──> [Derived SQLite store]
                        └──required-by──> [events() query]
                        └──required-by──> [latestPerAuthor() query]
                        └──required-by──> [stats query]

[event_tags join table]
    └──required-by──> [events() tag filter]

[ix_pubkey_kind_created index]
    └──required-by──> [latestPerAuthor() window query]

[Replaceable-event collapse]
    └──required-by──> [events() correctness for kinds 0,3,10000-19999,30000-39999]
    └──depends-on──> [Full LMDB importer] (collapse is applied during upsert)
    └──enhanced-by──> [Periodic full reconciler] (cleans up incremental-tail transients)

[NIP-40 expiration filtering]
    └──applied-at──> [events() query time] (SQL predicate)
    └──also-applied-at──> [Periodic full reconciler] (proactive deletion)

[Change-detection probe]
    └──triggers──> [Periodic full reconciler]
    └──triggers──> [Incremental tailer]

[/ready health endpoint]
    └──depends-on──> [Full LMDB importer complete]

[Cursor-based pagination]
    └──depends-on──> [Composite index on (created_at, lev_id)]
    └──conflicts-with──> [Offset pagination] (do not implement both)
```

### Dependency Notes

- **Version/endianness gate must precede everything:** if the DB version is wrong, all decoded integers are potentially garbage. Fail fast at process startup before any scan begins.
- **Dual-format decoder required by importer AND reconciler:** both paths read `EventPayload` values; both must handle `0x00` and `0x01`. Do not implement decoding in the importer and forget it in the reconciler.
- **Replaceable collapse depends on full importer:** collapse logic is applied at upsert time during import and incremental tail. The full reconciler also enforces it when diffing the derived store.
- **`latestPerAuthor()` requires covering index:** the window function query `ROW_NUMBER() OVER (PARTITION BY pubkey ORDER BY created_at DESC)` is efficient only with `ix_pubkey_kind_created(pubkey, kind, created_at DESC)`. Without this index, the query degrades to a full-table scan partitioned in application memory.
- **Reconciler depends on full scan capability:** the diff is `SELECT lev_id FROM derived` vs all keys in `EventPayload`. This is an O(n) scan of both stores; it must use short LMDB read transactions across multiple windows to avoid pinning pages (spec §6.4).
- **`/ready` depends on import completion:** serving queries before the initial import is complete would return incorrect counts and miss events. The `/ready` endpoint must gate on import status.

---

## MVP Definition

### Launch With (v1)

Minimum set to validate the concept and serve correct queries.

- [ ] **Version/endianness hard gate** — correctness-critical; gates everything else
- [ ] **Full LMDB importer with dual-format decoder** — without this there is nothing to query
- [ ] **Incremental tailer** — keeps the index fresh without a service restart
- [ ] **Periodic full reconciler + change-detection probe** — without this the index serves deleted events; this is a correctness guarantee, not a nice-to-have
- [ ] **Replaceable-event collapse** — NIP-01 compliance; queries must match relay semantics
- [ ] **NIP-40 expiration filter at query time** — correctness; expired events must not be served
- [ ] **`events()` GraphQL query** — table stakes filter surface
- [ ] **`latestPerAuthor()` GraphQL query** — the entire raison d'etre of the service
- [ ] **`stats` query** — operator visibility; includes `lastFullReconcileAt`, `dbVersion`, watermark
- [ ] **Hard limit ceiling on `events()`** — safety; prevents unbounded scans
- [ ] **`/health` and `/ready` HTTP endpoints** — Docker healthcheck; required for compose integration
- [ ] **Docker subsystem + docker-compose integration** — matches DeepFry stack conventions

### Add After Validation (v1.x)

Add once the core is working and in production use.

- [ ] **Cursor-based pagination on `events()`** — needed once consumers start paginating large result sets; not required on day one if result sets are small
- [ ] **Prometheus `/metrics` endpoint** — add when the service is in active production monitoring; not blocking initial validation
- [ ] **Staleness reporting (`deletionStalenessWindowSeconds` in `stats`)** — enhance `stats` once reconcile cadence is tunable
- [ ] **`POST /reconcile` operator trigger** — useful after known strfry compactions; add once compaction is observed in production

### Future Consideration (v2+)

Defer until the consistency model is proven and additional use cases emerge.

- [ ] **Live subscriptions (WebSocket push)** — adds stateful complexity; defer until request/response semantics are proven insufficient
- [ ] **Additional `orderBy` variants** — `CREATED_AT_ASC` covers the main use case; other orderings (by `kind`, by `pubkey`) are speculative
- [ ] **Cross-architecture / big-endian support** — not needed while strfry and deepfry are co-located on x86-64 / arm64
- [ ] **`latestPerAuthor()` with tag filters** — current spec scopes to `kind` + optional `authors`; tag filtering on window queries adds SQL complexity

---

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Version/endianness hard gate | HIGH (correctness) | LOW | P1 |
| Full LMDB importer (dual-format) | HIGH | MEDIUM | P1 |
| Incremental tailer | HIGH | MEDIUM | P1 |
| Periodic full reconciler | HIGH (correctness) | HIGH | P1 |
| Change-detection probe | HIGH | LOW | P1 |
| Replaceable-event collapse | HIGH (correctness) | MEDIUM | P1 |
| NIP-40 expiration filter | HIGH (correctness) | LOW | P1 |
| `events()` GraphQL query | HIGH | MEDIUM | P1 |
| `latestPerAuthor()` GraphQL query | HIGH (differentiator) | MEDIUM | P1 |
| `stats` GraphQL query | HIGH | LOW | P1 |
| Hard limit ceiling | HIGH (safety) | LOW | P1 |
| `/health` + `/ready` endpoints | HIGH | LOW | P1 |
| Docker subsystem | HIGH | LOW | P1 |
| Cursor-based pagination | MEDIUM | MEDIUM | P2 |
| Prometheus `/metrics` | MEDIUM | LOW | P2 |
| Staleness reporting in `stats` | MEDIUM | LOW | P2 |
| `POST /reconcile` trigger | MEDIUM | LOW | P2 |
| Live subscriptions | LOW (v1 deferred) | HIGH | P3 |
| Big-endian / cross-arch support | LOW | MEDIUM | P3 |
| Tag filters on `latestPerAuthor()` | LOW (speculative) | MEDIUM | P3 |

**Priority key:**
- P1: Must have for launch — correctness, core value, or operational necessity
- P2: Should have — add in first production sprint after initial validation
- P3: Deferred — future version or not yet validated as needed

---

## Correctness-Critical Feature Flags

These features are not "nice to have" — omitting or mis-implementing them produces **silently wrong results**:

| Feature | Failure Mode If Missing |
|---------|------------------------|
| Version/endianness hard gate | Decodes integers from wrong-format DB; all `created_at`, `kind`, `levId` values are garbage |
| No `Event__*` sub-DB access | Range scans silently return wrong event subsets due to missing custom comparators |
| No write transactions | Second writer corrupts strfry's LMDB environment (data loss) |
| Short read-transaction discipline | `data.mdb` grows unbounded; strfry cannot reclaim pages |
| Periodic full reconciler | Serves NIP-09 deleted, replaced, and expired events indefinitely after incremental tail |
| Replaceable-event collapse | Serves superseded kind-0/3/replaceable events alongside current versions |
| NIP-40 expiration filter | Serves events past their declared expiration timestamp |
| Dual-format payload decoder | Events are silently absent on compacted databases (decodes fail on `0x01` prefix) |

---

## Sources

- spec.md (source of truth for on-disk format, operation set, caveats §6.1–§6.11, GraphQL schema §5)
- PROJECT.md (active requirements, out-of-scope, constraints)
- NIP-01: https://nips.nostr.com/1 (replaceable/parameterized-replaceable event semantics)
- NIP-09: https://nostr-nips.com/nip-09 (deletion event semantics)
- NIP-40: https://nips.nostr.com/40 (expiration timestamp semantics)
- GraphQL Cursor Connections Specification: https://relay.dev/graphql/connections.htm (pagination patterns)
- nostrdb: https://github.com/damus-io/nostrdb (relay-adjacent embedded DB, no GraphQL/REST layer)
- nostr-sqlite: https://github.com/vertex-lab/nostr-sqlite (SQLite event store patterns, tag indexing)
- Nostrify SQL adapter: https://nostrify.dev/store/sql (tag indexing, NIP-50 patterns)

---

*Feature research for: deepfry — queryable index layer over strfry's LMDB*
*Researched: 2026-06-10*
