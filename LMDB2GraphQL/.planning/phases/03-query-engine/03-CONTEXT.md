# Phase 3: Query Engine - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning

<domain>
## Phase Boundary

Compose Phase 2's read primitives into full query semantics. This phase builds the
**internal query-engine library** that the Phase 4 GraphQL layer will call. It composes
three Phase-2 pieces:

1. **Bounded/reversible `Event__*` scan primitives** (`scan_index_bounded` /
   `scan_index_windowed`, `src/lmdb/scan.rs`) — yield `(composite key bytes, levId)` pairs.
2. **`EventPayload[levId]` hydration** (`decode_event_payload*`, `get_event_payload`,
   `src/lmdb/payload.rs`) — point-lookup + decode to a typed `NostrEvent` + retained raw JSON.
3. **The six comparator-correct index open-helpers** (`src/lmdb/indexes.rs`).

…into the query semantics required by QRY-01..QRY-05:
- **Filter routing / index selection** (QRY-01) — pick the most selective applicable index, scan, residual-filter.
- **Tag scans** via `Event__tag` (QRY-02).
- **`latestPerAuthor`** via `Event__pubkeyKind` prefix scans (QRY-03).
- **Hydration** of matched levIds via `EventPayload[levId]` (QRY-04).
- **NIP-40 expiration filtering** at query time (QRY-05).
- **Cursor pagination** on `(created_at, lev_id)`.

**Out of scope (later phases):**
- GraphQL schema, resolvers, HTTP surface, the hard limit *ceiling*, and the public
  cursor type — **Phase 4** (this phase exposes the engine API the resolvers call, and
  accepts a `limit`, but does not enforce a max ceiling or define the GraphQL types).
- `/health` `/ready`, Docker, CI fixture assertions — **Phase 5**.
- Deletion reconciliation / replaceable-supersession collapse — strfry enforces these at
  write time; LMDB2GraphQL inherits a collapsed live index set (PROJECT.md Out of Scope).

</domain>

<decisions>
## Implementation Decisions

### Filter routing & index selection (QRY-01)
- **D-01:** **One index + in-memory residual filter** (mirrors strfry's DBScan planner, matches
  QRY-01's "most selective applicable index" wording). Pick a single index, scan it, post-filter
  the remaining predicates on the result. **No multi-index intersection** in v1 — rejected as
  over-engineering and error-prone across DUPSORT streams.
- **D-02:** **Fixed selectivity priority order** (most selective first):
  `ids → Event__id`; `authors AND kinds → Event__pubkeyKind`; `authors only → Event__pubkey`;
  `kinds only → Event__kind`; `since/until only → Event__created_at`. A `tag` constraint routes to
  `Event__tag` (QRY-02). Hardcoded heuristic for v1 — **no cardinality estimation**; revisit only if
  a real query proves pathological.
- **D-03:** **`since`/`until` are pushed into the scan bounds**, not applied purely as a residual.
  All six `Event__*` keys carry trailing `created_at` (8 LE), so the time bound becomes part of the
  `start_key` (and an early-stop once past the bound). For prefix indexes (`Event__pubkey`,
  `Event__pubkeyKind`, …) the time bound applies *within* each prefix group's walk.
- **D-04:** A fully-empty / non-indexable filter defaults to a **reverse `Event__created_at` walk
  bounded by `limit`** ("latest N events overall" — the natural global feed). No hard rejection of
  empty filters at the engine boundary; the (Phase-4) limit ceiling is the safety bound.

### Multi-value fan-out & merge
- **D-05:** Multiple authors / kinds / authors×kinds map to multiple disjoint key-prefixes in ONE
  index. Resolve via **per-prefix reverse scan + k-way merge**: one bounded `Reverse` scan per prefix
  (each newest-first, ~`limit` entries), merged by `(created_at, levId)` descending via a heap,
  emitting until `limit`. Preserves exact global newest-first order at minimal hydration.
  (Rejected: concatenate-all-then-sort — over-scans when `limit` is small.)
- **D-06:** **Hydrate AFTER merge selection.** The merge runs on `(created_at, levId)` derived from
  the scan **key bytes alone** (no decode); only the finally-selected levIds are hydrated via
  `EventPayload[levId]` point-lookups. Honors Phase-2 D-05 (scan yields levIds; engine decides what to
  hydrate) and avoids over-decoding. NOTE: residual predicates that need decoded fields (NIP-40
  expiration, tag-value matches not expressible from the key) are evaluated **after** hydration — see D-07.

### NIP-40 expiration & limit accounting (QRY-05)
- **D-07:** **Over-fetch & backfill** to honor `limit`. Because expiration (and any decoded-field
  residual predicate) can only be evaluated after hydration, selecting exactly `limit` candidates then
  dropping expired ones would return short. Instead: keep pulling candidates from the merge, hydrate,
  drop expired / residual-failing, and continue until `limit` valid events are collected **or** the
  streams are exhausted. Returns a full N whenever N valid events exist. Bounded by the scan window so
  still LMDB-safe. (Rejected: filter-post-limit / return-short.)
- **D-08:** Expiration predicate = `expiration != 0 && expiration <= now`, read from the decoded
  `NostrEvent.tags` (`["expiration", "<unix>"]`). `tags` is already typed `Vec<Vec<String>>` (Phase-2
  D-03), so no extra parse machinery is needed.
- **D-09:** **`now` comes from direct system time** at the expiration check (user's explicit choice —
  not an injected clock). ⚠️ **Testing implication for the planner:** NIP-40 tests against the pinned
  fixed-timestamp fixture must use **future-dated** (or `0`/absent) `expiration` values to be
  deterministic, since `now` is not pinnable. Do NOT add a clock seam unless a later phase needs one.

### Result ordering & pagination cursor
- **D-10:** `events()` ordering contract = **`created_at` DESC, tie-broken by `levId` DESC** (a total
  order — `levId` is unique per event). Results are a **flat list**, newest-first.
- **D-11:** Pagination cursor = **opaque** encoding of the last `(created_at, levId)` pair (treat as a
  blob; suggested `base64(created_at(8) ‖ levId(8))`). Next page resumes by constructing the scan
  `start_key` from the decoded cursor and excluding it (`Bound::Excluded`), reusing Phase-2 D-06's
  `start_key`-as-resume-cursor mechanism. Consumers never inspect cursor internals. (Rejected:
  structured/visible cursor fields — couples consumers to internals.)
- **D-12:** `latestPerAuthor(kind, perAuthor, authors)` result is **grouped by author** — one bucket
  per pubkey, each bucket newest-first and ≤ `perAuthor`, via a per-author `Reverse`
  `Event__pubkeyKind[pubkey‖kind]` prefix scan capped at `perAuthor`. (Distinct from `events()` which is
  one flat merged stream — `latestPerAuthor` deliberately preserves the per-author grouping.)

### Claude's Discretion
- Module layout, `thiserror` error-type boundaries, and precise engine function signatures
  (within the established stack: heed 0.22.1, `tracing`, `serde_json`).
- The k-way merge data structure (binary heap vs. ordered merge of `limit`-bounded sub-iterators).
- Cursor byte layout / encoding details, provided D-11's opaqueness and `(created_at, levId)` content hold.
- How residual predicates are represented internally (closure vs. predicate struct) and the exact set
  evaluable from key bytes vs. requiring hydration.
- Internal over-fetch batch sizing for D-07's backfill loop (reuse `scan.rs` `DEFAULT_WINDOW_SIZE`
  pattern; a sensible default, optionally a knob).
- Whether engine functions own their own `RoTxn` or borrow per-call (recommendation: own, consistent with
  `scan.rs` / `indexes.rs`), provided Phase-2 D-08's short-txn invariant holds.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project decisions & requirements
- `.planning/REQUIREMENTS.md` — **QRY-01, QRY-02, QRY-03, QRY-04, QRY-05** (this phase's scope);
  also the Phase-4 API-01..API-06 entries that consume this engine (read for downstream awareness).
- `.planning/ROADMAP.md` — Phase 3 goal + 4 success criteria.
- `.planning/PROJECT.md` — Approach B (query live indexes, zero replication), Rust stack, Key Decisions,
  and the "latest N kind-1 per pubkey" motivating query; Out-of-Scope (no deletion reconciliation,
  no replaceable-collapse — inherited from strfry).
- `.planning/phases/02-payload-decoding-index-scan-primitives/02-CONTEXT.md` — Phase 2 decisions
  D-01..D-12 (scan yields `(key, levId)`; reverse + start-key cursor; per-call short txns; lenient
  `NostrEvent` with typed `Vec<Vec<String>>` tags; skip-warn-count on malformed payloads).
- `.planning/phases/01-lmdb-foundation-comparator-proof/01-CONTEXT.md` — Phase 1 index→comparator map
  and the `StringUint64` / `Uint64Uint64` / `StringUint64Uint64` key formats.

### On-disk encoding (authoritative — verified against strfry source)
- `spec.md` §3.1 — named sub-DB table + the six `Event__*` index key formats + comparators
  (the composite-key byte layouts the router and the cursor encoder depend on).
- `spec.md` §3.4 — `levId` semantics (monotonic, NOT chronological; uint64 `MDB_INTEGERKEY`).
  Relevant to the `(created_at, levId)` ordering tie-break and cursor.
- `spec.md` §6.4 — read-only safety + short read txns (the per-query-txn invariant the engine must keep).
- `spec.md` §6.7 — NIP-40 expiration: expired-but-unswept events linger physically; must filter at
  query time (QRY-05 / D-07/D-08).
- `spec.md` §6.5 / §6.6 — deletion / replaceable-supersession caveats: **out of scope** here
  (strfry enforces at write time), but read so the planner does NOT re-add collapse logic.
- ⚠️ **spec.md caveat (carried from Phases 1-2):** spec.md is a pre-pivot **Approach A** document. Its
  §4.2 "forbidden to range-scan `Event__*`" rule and its derived-SQLite architecture are **superseded by
  Approach B** — Phase 1 proved byte-exact `Event__*` scan order. Use spec.md only for verified on-disk
  encodings + caveats (§3.x, §6.x), NOT for architecture.

### Project stack reference
- `CLAUDE.md` (this project) — Rust stack: `async-graphql` 7.2.1 / `axum` 0.8.x (Phase 4, but informs the
  engine API shape), `serde_json` + local `NostrEvent` (NOT the `nostr` crate), `heed` 0.22.1 range
  iterators. ⚠️ Its `rusqlite`/SQLite "derived index/store" entries are **stale** — they contradict
  Approach B ("no SQLite / no derived store"); ignore.

### Existing code to compose (this repo)
- `src/lmdb/scan.rs` — `scan_index_bounded` / `scan_index_windowed`, `ScanDirection`,
  `DEFAULT_WINDOW_SIZE`; the `(Vec<u8>, LevId)` output the engine merges and routes.
- `src/lmdb/indexes.rs` — the six comparator-correct open helpers + key-format table; `full_db_name`.
- `src/lmdb/payload.rs` — `get_event_payload`, `decode_event_payload`,
  `decode_event_payload_with_cache`, `DictCache`, `decode_payload_skip_on_error` (skip-warn-count).
- `src/lmdb/types.rs` — `NostrEvent` (typed `tags: Vec<Vec<String>>`), `DecodedEvent` (event + raw JSON), `LevId`.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`src/lmdb/scan.rs`** — the engine's primary building block. `scan_index_bounded(env, short_name,
  direction, start_key, limit)` already provides bounded `Reverse` scans positioned by `start_key`
  (MDB_SET_RANGE) — exactly what D-05's per-prefix scans and D-11's cursor-resume need. `limit=0`
  windowing exists for the empty-filter / unbounded default-feed case (D-04).
- **`src/lmdb/payload.rs`** — `get_event_payload` + `decode_event_payload*` are the hydration step
  (D-06). `decode_payload_skip_on_error` already implements the skip-warn-count policy (Phase-2 D-11)
  the over-fetch loop (D-07) should reuse so a single corrupt payload never sinks a page.
- **`src/lmdb/indexes.rs`** — key-format table is the spec the router (D-02) and the cursor encoder
  (D-11) build composite `start_key` bytes from.

### Established Patterns
- **Per-call short `read_txn()` inside each primitive, dropped before return** (Phase-2 D-08) — the
  engine MUST NOT hold a txn across scan/hydrate calls; the over-fetch loop (D-07) reopens per batch.
- **`.open()` never `.create()`** for every sub-DB (read-only safety, Phase-1 pattern).
- **`tracing::warn!` skip-and-continue** on malformed VALUEs/payloads — extend uniformly to the
  engine's hydrate step.
- **Trailing `created_at` (8 LE) in every `Event__*` key** — the basis for D-03 (push time bound into
  bounds) and D-10/D-11 (extract `created_at` for ordering + cursor from key bytes, no decode).

### Integration Points
- The engine API (filter → ordered, hydrated, paginated events; `latestPerAuthor` grouped buckets) is
  the surface the Phase-4 GraphQL resolvers call. Shape it as callable library functions returning
  `DecodedEvent`s + an opaque cursor, NOT inline in any HTTP handler.
- `DecodedEvent.raw_json` (Phase-2 D-01) remains the exact-passthrough source for Phase 4's event field.

</code_context>

<specifics>
## Specific Ideas

- The motivating query "latest 20 kind-1 events per pubkey" (PROJECT.md) is `latestPerAuthor` (D-12):
  per-author `Reverse` `Event__pubkeyKind[pubkey‖kind]` prefix scan capped at `perAuthor`, grouped by author.
- The k-way merge (D-05) and `latestPerAuthor` (D-12) share the same per-prefix-reverse-scan machinery;
  the difference is only the output shape (flat merged stream vs. per-author buckets) — factor accordingly.
- Merge/order on `(created_at, levId)` from key bytes, decode last (D-06) — the engine should be able to
  page a feed while hydrating only the events it actually returns.

</specifics>

<deferred>
## Deferred Ideas

- **Hard limit ceiling + GraphQL cursor type** — Phase 4 (the engine accepts a `limit` and emits an
  opaque cursor; enforcing a max and defining the public Connection/cursor types is the API layer's job).
- **Cardinality-based index selection** — D-02 is a fixed heuristic; cost-based estimation is a future
  optimization, only if a real query proves pathological.
- **Injectable clock for NIP-40** — explicitly NOT added (D-09 uses direct system time); revisit only if
  a later phase needs deterministic time control beyond future-dated fixture values.
- **Deletion / replaceable-supersession handling** — out of scope (strfry enforces at write time);
  flagged so the planner does not re-introduce collapse logic from spec.md's Approach-A sections.
- **Doc-sync (carried from Phases 1-2):** stale `rusqlite`/SQLite "derived store" wording in CLAUDE.md
  and the amended LMDB-05 wording still want a cleanup pass — not Phase 3 code work.

None of the discussion strayed outside the phase domain.

</deferred>

---

*Phase: 3-Query Engine*
*Context gathered: 2026-06-11*
