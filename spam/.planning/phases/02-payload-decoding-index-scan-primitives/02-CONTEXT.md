# Phase 2: Payload Decoding & Index Scan Primitives - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning

<domain>
## Phase Boundary

Build the two read primitives the query engine (Phase 3) will compose:

1. **EventPayload decoding** — hydrate full Nostr event JSON from the `EventPayload`
   sub-DB in both on-disk formats:
   - `0x00` raw JSON (the default write path — LMDB-07)
   - `0x01` zstd-dictionary-compressed, using `CompressionDictionary[dictId]` (LMDB-08)
2. **Bounded `Event__*` index scan primitives** — generalize Phase 1's proof-of-concept
   scans into reusable, bounded, resumable cursor scans over each `Event__*` index,
   **tested in isolation**, with per-query short read transactions (LMDB-09).

This phase opens the `EventPayload` and `CompressionDictionary` sub-DBs for the first
time (Phase 1 only opened `Meta` + the six `Event__*` indexes). It produces library-level
primitives only — no query composition, no GraphQL.

**Out of scope (later phases):**
- Filter routing / index selection, `latestPerAuthor`, NIP-40 expiration filtering,
  cursor pagination semantics — **Phase 3** (this phase only returns the raw `(key, levId)`
  pairs and decoded events those compose from).
- GraphQL schema / HTTP surface — **Phase 4**.
- `/health` `/ready`, Docker, CI fixture assertions — **Phase 5**.
- Deletion reconciliation / staleness window (spec §6.5/§6.6) — a query-engine/derived-view
  concern; not in scope for raw read primitives.

</domain>

<decisions>
## Implementation Decisions

### EventPayload decoder output shape (LMDB-07, LMDB-08)
- **D-01:** Decoder returns a **typed `NostrEvent` struct AND the retained decoded JSON
  bytes**. The struct gives Phase 3 typed field access (filter routing, latestPerAuthor,
  NIP-40); the retained raw bytes give Phase 4 an exact-passthrough field without
  re-serializing. One decode produces both — no double parse.
- **D-02:** `NostrEvent` deserialization is **lenient**: the 7 known fields (`id`, `pubkey`,
  `created_at`, `kind`, `tags`, `content`, `sig`) are required and typed; unknown top-level
  fields are **ignored** (serde default, NOT `deny_unknown_fields`) for forward-compat with
  events strfry accepted that carry extra fields.
- **D-03:** `tags` is typed as **`Vec<Vec<String>>`** (standard Nostr) so Phase 3 tag scans
  and NIP-40 expiration extraction can use it directly — not kept as a raw `serde_json::Value`.
- **D-04:** Per CLAUDE.md / spec §6.9 — use `serde_json` + the local `NostrEvent` struct,
  **NOT the `nostr` crate**. Do **not** re-verify signatures on the decode path (strfry already
  validated on ingest).

### Bounded Event__* scan primitive contract (LMDB-09)
- **D-05:** A scan primitive yields **`(composite key bytes, levId)` pairs** — it does **not**
  hydrate. Phase 3 decides which `levId`s to hydrate (via a separate `EventPayload` lookup) and
  uses the raw key bytes to construct `(created_at, lev_id)` pagination cursors. Keeps scan and
  decode as separate, isolation-testable primitives (the phase goal) and avoids over-hydrating
  events a later filter discards.
- **D-06:** Scan is parameterized by **`limit` + `start-key` + `direction` (forward / reverse)**.
  Reverse iteration (heed `RoRange::rev()` / `MDB_PREV`) is required so "latest N" maps to a
  `created_at`-descending tail walk rather than read-all-then-sort. `start-key` positions via
  `MDB_SET_RANGE` (the same path the Phase 1 CR-01 gate proved exercises the golpe comparator)
  and doubles as the pagination resume cursor.
- **D-07:** **`limit = 0` means unbounded** (scan to the end of the index). To stay
  LMDB-09-compliant, `limit = 0` is implemented via **internal windowing**: the primitive walks
  a bounded internal batch, **closes the read txn**, reopens and resumes from the last key, and
  repeats — presenting one continuous unbounded stream to the caller while **no single read txn
  is ever long-lived**. A bounded (`limit > 0`) scan that fits a window uses a single short txn.
- **D-08:** Read transactions are **per-query and short** (LMDB-09): opened inside the primitive,
  bounded by `limit` (or the internal window for `limit = 0`), dropped/`abort()`ed before return.
  No read txn is held across primitive calls. (Carries forward Phase 1's per-call `read_txn()`
  pattern in `indexes.rs`.)

### zstd dictionary loading & cache (LMDB-08)
- **D-09:** Dictionaries are **lazily loaded and cached by `dictId`**: on the first `0x01` payload
  carrying a given `dictId`, GET `CompressionDictionary[dictId]`, build a zstd `DecoderDictionary`
  (DDict), and cache it for the process lifetime. Default `0x00`-only deployments pay nothing;
  compacted DBs digest each dict once. (spec §6.8.)
- **D-10:** The dictionary cache must be **concurrency-safe** — Phase 4 serves GraphQL queries on
  tokio, so multiple resolvers can hit a `0x01` decode concurrently. (Exact concurrency primitive
  is Claude's discretion — see below.)

### Malformed / undecodable payload policy (spec §6.9)
- **D-11:** A single payload that fails to decode (unknown type byte, truncated, zstd error,
  **missing/absent `dictId`**, invalid JSON, missing required field) is **skipped**, logged at
  `warn` with `levId` + reason, and counted. The query/scan returns **all decodable events** —
  one corrupt payload never sinks an otherwise-valid query over a live DB we don't control.
  This skipped-count is exposed so the caller (eventually Phase 4) can surface it.
- **D-12:** Decoded JSON is treated as **untrusted** (it originated from the network). Validate
  structure before use; do not re-serve unvalidated. No signature re-verification on the hot path.

### Claude's Discretion
- Internal **window batch size** for `limit = 0` scans (a sensible default, optionally a config knob).
- Exact **concurrency primitive** for the dictionary cache (e.g. `RwLock<HashMap<u32, Arc<DecoderDictionary>>>`,
  `OnceCell`-per-dict, or a small concurrent map) — within the established stack.
- **Decode error-type design** (`thiserror` enum boundary between LMDB / zstd / serde error kinds),
  module layout, and the precise scan-primitive function signatures.
- Whether the scan primitive borrows a caller-supplied `RoTxn` or owns its own — provided D-08's
  short-txn invariant holds. (Recommendation: owns its own, consistent with Phase 1's `indexes.rs`.)
- Whether to do a startup `0x01` sampling/log line now or defer to Phase 5 (spec §6.8 detect-early).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### On-disk encoding (authoritative — verified against strfry source)
- `spec.md` §3.2 — **`EventPayload` value encoding** (the core of LMDB-07/08): `byte[0]` type tag;
  `0x00` → raw JSON in `bytes[1..]`; `0x01` → `dictId` (uint32 native LE) in `bytes[1..5]`, zstd
  payload in `bytes[5..]` decompressed with `CompressionDictionary[dictId]`. Source lines cited:
  `src/events.cpp:196-215` (`decodeEventPayload`), `golpe.yaml:104-113`, write path `events.cpp:371-372`.
- `spec.md` §3.1 — named sub-DB table: confirms `EventPayload` (key = `levId`, `MDB_INTEGERKEY`),
  `CompressionDictionary` (key = `dictId` uint32), and the six `Event__*` index key formats + comparators.
- `spec.md` §3.4 — `levId` semantics (monotonic, NOT chronological; uint64 `MDB_INTEGERKEY` key of
  both `Event` and `EventPayload`). Relevant to hydration-by-levId and cursors.
- `spec.md` §6.3 — native-endian integers (`dictId` and `levId` are native/LE on the co-located host).
- `spec.md` §6.4 — **read-only safety + short read txns** (the LMDB-09 rationale): MVCC snapshot,
  long readers pin pages and grow `data.mdb`; open short txns, scan in bounded `levId` windows.
- `spec.md` §6.8 — **compression path**: cache decoded dictionaries by `dictId`; detect `0x01` early.
- `spec.md` §6.9 — malformed/oversized defensiveness: treat decoded JSON as untrusted; do NOT
  re-verify sigs on the hot path; enforce limits.
- `spec.md` §6.10 — map-size/disk growth (reinforces short-txn requirement).
- ⚠️ **spec.md caveat (from Phase 1):** spec.md is a pre-pivot **Approach A** document. Its
  architecture (derived SQLite store, §4.3 importer) and its §4.2 **"forbidden to range-scan
  `Event__*`"** rule are **superseded by Approach B** — Phase 1 registered golpe's comparators and
  proved byte-exact order, so bounded `Event__*` scans are exactly what Phase 2 builds. Use spec.md
  only for the verified on-disk encodings + caveats (§3.x, §6.x), NOT for architecture.

### Project decisions & requirements
- `.planning/REQUIREMENTS.md` — **LMDB-07, LMDB-08, LMDB-09** (this phase's scope).
- `.planning/ROADMAP.md` — Phase 2 goal + 3 success criteria.
- `.planning/PROJECT.md` — Approach B, Rust stack, Key Decisions.
- `.planning/phases/01-lmdb-foundation-comparator-proof/01-CONTEXT.md` — Phase 1 decisions
  (D-01..D-14): comparator-via-FFI, the `Event__*` index→comparator mapping, short-txn pattern.

### Project stack reference
- `CLAUDE.md` (this project) — Rust stack: `zstd` 0.13.3 (`Decoder::with_dictionary`,
  `DecoderDictionary`/`DDict` caching), `serde_json` 1.0.150 + **local `NostrEvent` struct, NOT
  the `nostr` crate**, `heed` 0.22.1 range iterators. ⚠️ Its `rusqlite`/SQLite "derived index"
  entries are **stale** (contradict Approach B) — ignore.

### Existing code to extend (this repo)
- `src/lmdb/indexes.rs` — Phase 1 scans (`scan_lev_ids_for_index`, `seek_first_ge_lev_id`),
  the six index open-helpers with comparators, the per-call `read_txn()` pattern, and the
  `(key,value)`/levId-from-VALUE extraction contract. **Phase 2 generalizes these into bounded scans.**
- `src/lmdb/env.rs` — `open_read_only_env` / `open_fixture_env` (`MDB_RDONLY`, `NO_LOCK` fixture-only).
- `src/lmdb/types.rs` — `LevId`, `MetaRecord`; add `NostrEvent` here or a sibling module.
- `tests/fixture/` — committed adversarial `data.mdb` (strfry 1.1.0, `dbVersion==3`).

### External (pinned)
- strfry 1.1.0 @ `sha256:545555da…` — `src/events.cpp` (`decodeEventPayload`), `golpe.yaml`.
- `zstd` 0.13.3 docs — `zstd::dict` (`DecoderDictionary`, `Decoder::with_dictionary`).
- `heed` 0.22.1 docs — `RoRange` / `db.range()` + `.rev()`, `Bytes`-keyed cursor ranges.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`src/lmdb/indexes.rs`** — directly generalized this phase. `seek_range_first_lev_id` already
  uses `db.range(rtxn, &(Bound::Included(start)..))` (= `MDB_SET_RANGE`); the bounded scan extends
  this with a `limit` cap, a `direction` (add `.rev()`), and the internal-windowing loop for `limit=0`.
  `collect_lev_ids_dup` shows the `MDB_DUPSORT`/levId-from-8-byte-LE-VALUE extraction to reuse.
- **`src/lmdb/env.rs`** — env open is done; Phase 2 only adds opening `EventPayload` +
  `CompressionDictionary` named sub-DBs (via `open_database`, **never** `create_database`).
- **`src/lmdb/types.rs` / `meta.rs`** — established `thiserror` + module layout and the
  FlatBuffer/byte-offset documentation style to mirror for the payload decoder.

### Established Patterns
- **Per-call short `read_txn()`** opened inside each primitive and dropped before return
  (`indexes.rs`) — Phase 2's D-08 invariant continues this; D-07 windowing reopens between batches.
- **`.open()` never `.create()`** for every sub-DB (read-only safety — Phase 1 IndexError pattern).
- **`Event__*` indexes are `MDB_DUPSORT`** (multiple levId VALUEs per KEY) — the bounded scan must
  iterate key+duplicate-value pairs, same as `collect_lev_ids_dup`.
- **`tracing::warn!` for skip-and-continue** on short/malformed VALUEs already used in `indexes.rs`
  — D-11's skip+warn+count policy extends this consistently.

### Integration Points
- Scan primitive `(key, levId)` output + the `EventPayload`-by-levId hydration + the decoder are the
  three pieces Phase 3 composes into filter routing / latestPerAuthor / pagination.
- The decoder's retained-raw-JSON (D-01) is the source for Phase 4's exact event passthrough field.

</code_context>

<specifics>
## Specific Ideas

- `EventPayload` and `CompressionDictionary` are keyed by integer (`levId` uint64 / `dictId` uint32,
  `MDB_INTEGERKEY`) — open with `IntegerComparator` (like `Event__created_at` in `indexes.rs`), NOT
  a golpe custom comparator.
- "latest N kind-1 per pubkey" (the PROJECT.md motivating query) is the concrete target the reverse
  scan + `Event__pubkeyKind` prefix walk must serve cheaply — keep that path in mind when shaping the
  scan signature (Phase 3 composes it, but the primitive must make it possible without full reads).
- `0x01` is rare in practice (only after offline compaction) but MUST be correct — the lazy cache
  (D-09) means the common `0x00` path never touches zstd.

</specifics>

<deferred>
## Deferred Ideas

- **Deletion reconciliation / staleness window (spec §6.5/§6.6):** levId-tailing misses deletions and
  replaceable-event supersession. A query-engine/derived-view concern (Phase 3+), not raw read primitives.
- **NIP-40 expiration filtering (spec §6.7):** extracting/filtering on `expiration` is Phase 3
  (this phase only ensures `tags` is typed so Phase 3 can read it).
- **Startup `0x01` sampling + richer drift surfacing:** a basic detect-early log is optional now;
  fuller operational surfacing is OPS-territory (Phase 5).
- **Doc-sync (carried from Phase 1):** stale `rusqlite`/SQLite "derived index" wording in CLAUDE.md
  and the amended LMDB-05 wording still want a cleanup pass — not Phase 2 code work.

None of the discussion strayed outside the phase domain.

</deferred>

---

*Phase: 2-Payload Decoding & Index Scan Primitives*
*Context gathered: 2026-06-11*
