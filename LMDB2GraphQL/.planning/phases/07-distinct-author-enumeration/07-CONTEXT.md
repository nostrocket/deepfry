# Phase 7: Distinct Author Enumeration - Context

**Gathered:** 2026-06-24
**Status:** Ready for planning
**Source:** Interactive decision capture (no discuss-phase; decisions locked with the user 2026-06-24)

<domain>
## Phase Boundary

Add a single new read-only GraphQL query, `authors(after, limit)`, that paginates the set of **distinct pubkeys present in the database** — every pubkey that has authored at least one event. This is a capability the existing API cannot serve today:

- `latestPerAuthor(kind, perAuthor, authors)` requires the caller to already supply the author list.
- `events()` only surfaces pubkeys by paginating through every event and deduping client-side (O(total events)).

The query reads strfry's live `Event__pubkey` index directly. **Pubkeys-only output** — no per-author counts (see locked decision below).

In scope: engine enumeration function + GraphQL resolver + types + pagination cursor + fixture tests + SDL/no-mutation regression.
Out of scope: per-author event counts, any new index or derived store, any write path, hydrating events.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Output shape — pubkeys only
- The query returns `AuthorsPage { authors: [String!]!, hasMore: Boolean!, endCursor: String }`.
- `authors` are 64-char lowercase hex pubkeys.
- **Counts are explicitly excluded.** Rationale: an event count per author requires enumerating *every* `Event__pubkey` entry for that author (all `created_at` keys AND all DUPSORT dups within each), which changes the query from O(distinct authors) to O(total events) and reintroduces the unbounded per-author fan-out the codebase deliberately bounds (`T-04-FANOUT` in `latest_per_author`). If counts are ever needed they are a separate, explicitly-bounded resolver — not a field here. (Deferred, not v1.2.)

### Scan strategy — seek-skip, O(distinct authors)
- Enumerate distinct pubkeys by seeking, reading one entry, taking the 32-byte pubkey prefix, then re-seeking with lower bound = `increment_be(prefix)` to jump past all of that author's other entries to the next distinct pubkey.
- `increment_be` adds 1 to the 32-byte big-endian pubkey; returns `None` on all-`0xFF` overflow (clean end-of-stream).
- This works because the pubkey portion of the `Event__pubkey` key sorts by plain byte order (memcmp) under `StringUint64Cmp`, so lexicographic increment of the 32-byte prefix yields a valid "first possible key of the next author" lower bound.
- Rejected alternative: scan-all + adjacent-dedup (O(total events)) — simpler but pays for every event of every author; only acceptable for tiny DBs.

### Resolver name — `authors`
- Public query name is `authors` (not `distinctAuthors`) — matches the existing `latestPerAuthor` "author" vocabulary.

### Pagination cursor
- The cursor is the **last pubkey returned** (a 32-byte value / its hex), NOT the `(created_at, lev_id)` `PageCursor` used by `events()`. Do **not** reuse `PageCursor` — different domain.
- `after` is decoded **fail-closed**: a malformed cursor yields a client-facing error that does NOT echo the offending bytes (mirror `T-04-CUR` / `T-04-HEX` handling in `resolvers.rs`).
- `endCursor` is `Some(last_pubkey)` when a full page was returned (more may remain), `None` at true end of stream.

### Limits / DoS
- `limit` clamped to the same hard ceiling as `events()` (≤ 500, default 100) at the API layer before calling the engine — reuse the existing D-04/D-05 clamp pattern. Each page is bounded to `limit` seeks.

### Read-only invariants (non-negotiable)
- Open `Event__pubkey` with the existing `open_index_string_uint64` helper (`StringUint64Cmp` comparator) — never `.create()`.
- Short per-call `RoTxn`, dropped at end of the enumeration call (D-08); never a write txn (T-03-RDONLY).
- Resolver wraps the synchronous engine call in `tokio::task::spawn_blocking` (clone `env` before the closure — `RoTxn` is `!Send`), like every existing resolver.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Index access + comparator (reuse, do not reinvent)
- `src/lmdb/indexes.rs` — `open_index_string_uint64` opens `Event__pubkey` with `StringUint64Cmp`; key layout `pubkey(32) ‖ created_at(8 LE)`, `MDB_DUPSORT`, value = 8-byte LE levId. This is the index to enumerate.
- `src/lmdb/comparators.rs` — `StringUint64Cmp` (pubkey portion = memcmp/byte order; basis for `increment_be` correctness).
- `src/lmdb/scan.rs` — `scan_index_bounded` / `seek_range_first_lev_id` show the `db.range((Included(&key[..]), Unbounded))` → `MDB_SET_RANGE` pattern and short-txn discipline to follow.

### GraphQL layer patterns to mirror
- `src/graphql/resolvers.rs` — `events()` resolver: limit clamp (D-04/D-05), `spawn_blocking` + clone-before-closure (Pitfall 1), `map_query_error` (T-04-LEAK), fail-closed cursor decode (T-04-CUR), `EventsPage { hasMore, endCursor }` shape to mirror for `AuthorsPage`.
- `src/graphql/types.rs` — `EventsPage` SimpleObject; add `AuthorsPage` alongside it.
- `src/graphql/schema.rs` — `Query` root + `build_schema` (depth/complexity limits); the no-`type Mutation` SDL regression test lives in resolvers.rs tests.
- `src/query/filter.rs` — `PageCursor` encode/decode + `QueryError` variants (the cursor model NOT to reuse, and the error type to extend/reuse).

### Project rules
- `LMDB2GraphQL/CLAUDE.md` — read-only LMDB constraints, short-txn rule, comparator correctness. NOTE: its "What NOT to Use" table says "Opening any `Event__*` sub-DB → silently wrong" — this is **stale**; the project already opens all six `Event__*` indexes with reimplemented comparators behind a startup self-check gate (Phase 1). Plan should include updating that stale table.
- `deepfry/CLAUDE.md` — data-separation rule (no event payloads outside StrFry — enumeration returns only pubkeys, which is fine).
</canonical_refs>

<specifics>
## Specific Ideas

- Engine entry point sketch (in `src/query/engine.rs` or a new `src/query/authors.rs`):
  `distinct_authors(env, after: Option<&[u8;32]>, limit: usize) -> Result<(Vec<[u8;32]>, Option<[u8;32]>), QueryError>` — opens one `RoTxn`, loops `limit` times doing range-seek + prefix-extract + `increment_be`, returns the pubkeys and the next-page cursor (last pubkey) when a full page was filled.
- DUPSORT note for the planner: the composite `pubkey‖created_at` is the KEY (dups are levIds), so the same pubkey appears under many keys; seek-skip (not just adjacent-dedup of one window) is required to jump across them.
- Fixture test must assert: the exact distinct-pubkey set from `tests/fixture` (it has PK1/PK2 — confirm the full set), correct ordering (byte-ascending pubkey), pagination with `limit` smaller than the author count covers every pubkey exactly once, and clean termination.
- Snapshot semantics: each page is a separate short txn, so a pubkey appearing mid-pagination could be missed/duplicated across pages — same eventual-consistency property `events()` already has; document, don't try to make it strongly consistent.
</specifics>

<deferred>
## Deferred Ideas

- Per-author event counts (`AuthorStat { pubkey, eventCount }`) — separate bounded resolver or v2; see Output-shape decision.
- `since`/`until` or kind filtering on author enumeration — not requested; `Event__pubkey` alone can't filter by kind (that's `Event__pubkeyKind`).
</deferred>

---

*Phase: 07-distinct-author-enumeration*
*Context captured: 2026-06-24 via interactive decision capture*
