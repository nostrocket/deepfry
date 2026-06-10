# Spec: `deepfry` — Queryable Endpoint over strfry's LMDB

**Status:** Draft
**Date:** 2026-06-09
**Target strfry DB version:** 3 (`src/constants.h: CURR_DB_VERSION = 3`)

---

## 1. Goal

Build a middleware service ("deepfry") that exposes a [strfry](https://github.com/hoytech/strfry)
LMDB database as a **standard, directly queryable endpoint** (GraphQL proposed).
Consumers should be able to run rich queries — e.g. *"latest 20 kind-1 events per
pubkey"* — that the Nostr `REQ` protocol cannot express, without running queries
through the strfry relay process.

This document specifies **every database operation required**, the **exact on-disk
encodings**, and **all caveats and gotchas** needed to implement it safely against
a live, concurrently-written database.

---

## 2. Decision summary (read this first)

> **strfry's secondary index sub-databases (`Event__*`) use golpe-registered custom
> LMDB comparators. LMDB stores comparators only in process memory, never in the
> file. A foreign reader that opens those sub-DBs and does range scans / `MDB_SET_RANGE`
> lookups will get WRONG or UNDEFINED results — silently.**

This single fact drives the architecture. There are three viable approaches:

| Approach | Reads strfry index dbis? | Correctness | Coupling | Recommendation |
|---|---|---|---|---|
| **A. Derived store** | No — reads only `EventPayload` (well-defined `MDB_INTEGERKEY`) | High | Low | ✅ **Recommended** |
| **B. Embed strfry/golpe** | Yes, via strfry's own code with its comparators | Highest | Very high (version-locked) | Fallback if you need strfry's exact replaceable/deletion semantics live |
| **C. Shell out to `strfry scan`/`export`** | N/A (uses relay binary) | Highest | Low | Prototype / low-volume only |

**This spec specifies Approach A.** deepfry performs a read-only scan of strfry's
`EventPayload` sub-database, decodes each event to JSON, and **maintains its own
queryable index** in a standard database (SQLite or Postgres) with its own schema
and its own comparators. GraphQL is served from that derived store, never from
strfry's B-tree directly.

Approach A never depends on strfry's custom comparators, never writes to strfry's
DB, and survives strfry restarts and compactions.

---

## 3. Background: strfry's storage model (verified against source)

strfry is built on the [golpe](https://github.com/hoytech/golpe) framework over
LMDB. The schema lives in `golpe.yaml`. The DB is an LMDB **environment** (a
directory containing `data.mdb` + `lock.mdb`), default path `./strfry-db/`
(`strfry.conf:6`).

### 3.1 Named sub-databases (dbis)

golpe generates one LMDB named sub-DB per table/index. Names observed from
`golpe.yaml` and `src/`:

| dbi name | Purpose | Key | Value |
|---|---|---|---|
| `Meta` | Single row (id=1): `dbVersion`, `endianness`, `negentropyModificationCounter` | record id | golpe struct |
| `Event` | Indexable summary of each event (`PackedEvent`) | `levId` (uint64, auto-increment) | `PackedEvent` binary blob |
| `EventPayload` | **Raw nostr event JSON** (possibly compressed) | `levId` (uint64, `MDB_INTEGERKEY`) | type-byte-prefixed payload |
| `CompressionDictionary` | zstd dictionaries | `dictId` (uint32) | dictionary bytes |
| `NegentropyFilter` | negentropy subscription filters | — | — |
| `Event__id` | index | `id(32) ‖ created_at` (custom comparator `StringUint64`) | `levId` (uint64) |
| `Event__pubkey` | index | `pubkey(32) ‖ created_at` (`StringUint64`) | `levId` |
| `Event__created_at` | index | `created_at` (integer) | `levId` |
| `Event__kind` | index | `kind ‖ created_at` (`Uint64Uint64`) | `levId` |
| `Event__pubkeyKind` | index | `pubkey(32) ‖ kind ‖ created_at` (`StringUint64Uint64`) | `levId` |
| `Event__tag` | multi-index | `tagName(1) ‖ tagValue ‖ created_at` (`StringUint64`) | `levId` |
| `Event__deletion` | multi | NIP-09 bookkeeping | — |
| `Event__expiration` | multi (integer) | expiration timestamp | — |
| `Event__replace` | multi | `pubkey ‖ d-tag` → kind | — |
| `Event__replaceDeletion` | multi | hash(a-tag) ‖ created_at | — |
| `negentropy` | negentropy BTree storage (`src/onAppStartup.cpp:114`) | — | — |

> **deepfry (Approach A) opens only `EventPayload` (required) and optionally
> `Meta` and `Event` (read-only). It MUST NOT open or range-scan any `Event__*`
> sub-DB.**

To discover dbi names at runtime: open the **unnamed root** dbi and iterate its
keys — in LMDB, each named sub-DB appears as a key in the main DB.

### 3.2 `EventPayload` value encoding — the one format you must decode

Source: `src/events.cpp:196-215` (`decodeEventPayload`), `golpe.yaml:104-113`.

```
byte[0] = type tag
  0x00 -> bytes[1..]  is the raw nostr event JSON (UTF-8). DONE.
  0x01 -> bytes[1..4] is dictId   (uint32, NATIVE endianness)
          bytes[5..]  is zstd-compressed JSON, decompress with
                      CompressionDictionary[dictId] as the zstd dictionary.
```

**Default deployments store `0x00` (uncompressed).** The normal write path always
prepends `0x00` (`src/events.cpp:371-372`: `tmpBuf += '\x00'; tmpBuf += ev.jsonStr`).
The `0x01` form only appears if an operator ran strfry's offline
dictionary-compaction tooling. deepfry **must still handle `0x01`** to be correct
on compacted databases.

The decoded value is the complete signed Nostr event JSON object
(`id`, `pubkey`, `created_at`, `kind`, `tags`, `content`, `sig`).

### 3.3 `PackedEvent` value encoding (only if you read the `Event` dbi)

Source: `src/PackedEvent.h`. Fixed header + variable tags, all integers **native
endianness**:

```
offset 0   : id          (32 bytes)
offset 32  : pubkey      (32 bytes)
offset 64  : created_at  (uint64, native)
offset 72  : kind        (uint64, native)
offset 80  : expiration  (uint64, native; 0 = none)
offset 88+ : tags[]  — each: tagChar(1) ‖ len(1, uint8) ‖ value(len bytes)
```

`PackedEvent` contains **no `content` and no `sig`** — it is a filter/index
summary only. Full events come from `EventPayload`. deepfry generally does not
need `PackedEvent`; `EventPayload` JSON is the source of truth.

### 3.4 `levId`

`levId` ("Local EVent ID") is an auto-incrementing uint64 primary key. It is:
- **Monotonic** — newer events get larger `levId`s. Useful as an incremental cursor.
- **NOT chronological** — `levId` order ≠ `created_at` order (events arrive out of
  order). Sort by `created_at` for time queries, not `levId`.
- The key of both `Event` and `EventPayload` (as `MDB_INTEGERKEY` → native-endian
  uint64 byte layout).

---

## 4. Required database operations (Approach A)

All LMDB access is **read-only**. deepfry never opens a write transaction against
strfry's environment.

### 4.1 Environment open

- Open the env pointing at the **directory** containing `data.mdb`.
- Flags: `MDB_RDONLY`, `MDB_NOLOCK` is **NOT** recommended unless coordinated;
  prefer normal read locking. In Python `lmdb`: `readonly=True, lock=False,
  subdir=True`. (`lock=False` lets you read without write access to `lock.mdb`;
  acceptable for read-only consumers, see §6.4.)
- `max_dbs` ≥ number of named sub-DBs you open (set ≥ 20 to be safe).
- `map_size` ≥ strfry's configured `dbParams.mapsize` (`strfry.conf:13`,
  default `10995116277760` = 10 TiB). A reader may set an equal-or-larger map
  size; it does not allocate, it only bounds.
- Disable readahead if scanning large DBs on spinning disk is not a concern;
  match strfry's `noReadAhead` setting where relevant.

### 4.2 Operation set

| # | Operation | dbi | LMDB primitive | Notes |
|---|---|---|---|---|
| O1 | Read DB version & endianness | `Meta` | `GET` id=1 | Abort if `dbVersion != 3` (see §6.1). Compare host endianness to stored. |
| O2 | Full scan of all events | `EventPayload` | `cursor` first→last | Decode each value (§3.2). This is the bulk import. |
| O3 | Incremental tail | `EventPayload` | `cursor` `MDB_SET_RANGE` from `lastLevId+1` | `EventPayload` is `MDB_INTEGERKEY`, so integer range is well-defined. Catches **inserts** only (see §6.5). |
| O4 | Point lookup | `EventPayload` | `GET` by `levId` | Native-endian uint64 key. |
| O5 | (optional) Read packed summary | `Event` | `GET`/cursor by `levId` | Only if you want indexable fields without parsing JSON. |
| O6 | (optional) Read zstd dict | `CompressionDictionary` | `GET` by `dictId` | Required only when a payload begins with `0x01`. |
| O7 | Change detection | `Meta` | `GET` `negentropyModificationCounter` | Bumps on event add/remove; cheap "did anything change?" probe. Plus filesystem watch on `data.mdb`. |

> **Forbidden operations:** any range scan, `MDB_SET_RANGE`, or ordered cursor
> traversal on `Event__id`, `Event__pubkey`, `Event__kind`, `Event__pubkeyKind`,
> `Event__tag`, or any `Event__*` dbi. Their key order depends on golpe's custom
> comparator, which is not present in a foreign process. (See §6.2.)

### 4.3 Building the derived index

After O2/O3 decode each event JSON, upsert into the derived store (SQLite/Postgres):

```
events(
  lev_id        INTEGER PRIMARY KEY,   -- strfry levId, the sync cursor
  id            TEXT UNIQUE,           -- 32-byte hex event id
  pubkey        TEXT,
  created_at    INTEGER,
  kind          INTEGER,
  expiration    INTEGER NULL,
  content       TEXT,
  sig           TEXT,
  tags          JSON,                  -- original tags array
  raw           JSON,                  -- full event, for re-serialization
  seen_at       INTEGER                -- deepfry ingest time
)
-- deepfry-owned indexes (standard, NOT strfry's):
CREATE INDEX ix_pubkey_kind_created ON events(pubkey, kind, created_at DESC);
CREATE INDEX ix_kind_created        ON events(kind, created_at DESC);
CREATE INDEX ix_created             ON events(created_at DESC);
-- tags: separate table for tag queries
event_tags(lev_id, tag_name, tag_value)  -- index (tag_name, tag_value)
```

The motivating query *"latest 20 kind-1 per pubkey"* becomes a standard windowed
query against `ix_pubkey_kind_created` — trivial and fast in the derived store.

---

## 5. GraphQL interface (proposed)

Serve from the derived store. Suggested schema:

```graphql
type Event {
  id: ID!
  pubkey: String!
  createdAt: Int!
  kind: Int!
  content: String!
  sig: String!
  tags: [[String!]!]!
  expiration: Int
}

type Query {
  events(
    ids: [String!]
    authors: [String!]
    kinds: [Int!]
    since: Int
    until: Int
    tag: TagFilter
    limit: Int = 100
    orderBy: EventOrder = CREATED_AT_DESC
  ): [Event!]!

  # The query Nostr REQ cannot express:
  latestPerAuthor(kind: Int!, perAuthor: Int! = 20, authors: [String!]): [Event!]!

  stats: DbStats!   # event count, max levId, last sync time, dbVersion
}

input TagFilter { name: String!, values: [String!]! }
enum EventOrder { CREATED_AT_DESC CREATED_AT_ASC }
```

Implementation notes:
- `latestPerAuthor` → `ROW_NUMBER() OVER (PARTITION BY pubkey ORDER BY created_at DESC)`
  filtered to `kind` and `rn <= perAuthor`.
- Enforce a hard `limit` ceiling and pagination (cursor on `(created_at, lev_id)`)
  to avoid unbounded responses.
- Read-only API. No mutations — deepfry is a query surface, not a relay.

---

## 6. Caveats & gotchas (MUST address)

### 6.1 DB version coupling — **hard gate**
The on-disk format is strfry-internal and tied to `Meta.dbVersion`
(`CURR_DB_VERSION = 3`, `src/constants.h`). It can change between strfry releases
with no compatibility guarantee. **deepfry MUST read `Meta` on startup and refuse
to run (loudly) if `dbVersion != 3`.** Pin and test against a specific strfry
version. Treat the format as a private API.

### 6.2 Custom comparators — **the big one**
`Event__*` index dbis are declared with `comparator: StringUint64` /
`Uint64Uint64` / `StringUint64Uint64` (`golpe.yaml:30-52`). golpe registers these
via `mdb_set_compare` at runtime. **LMDB never persists comparators.** A foreign
reader sees raw bytes ordered by strfry's comparator, but its own cursor will
assume `memcmp` order → range queries silently return wrong subsets, and
`MDB_SET_RANGE` lands in the wrong place. **Do not traverse these dbis.** Build
your own index (Approach A). If you must (Approach B), you have to link golpe and
register byte-identical comparators — i.e. embed strfry's code.

### 6.3 Native-endian integers
All integers (`levId`, `created_at`, `kind`, `expiration`, `dictId`, `MDB_INTEGERKEY`
keys) are stored in **host byte order** (`lmdb::to_sv`/`from_sv` do raw copies),
not a portable encoding. `Meta.endianness` records the writer's endianness.
deepfry MUST: (a) read `Meta.endianness`, (b) compare to its own host, (c) refuse
or byte-swap if they differ. In practice run deepfry on the **same machine /
same architecture** as strfry (x86-64 and arm64 are both little-endian, so
cross-host is usually fine — but verify, don't assume).

### 6.4 Concurrency & read-only safety
- **Never open a write transaction.** A second writer to an LMDB env corrupts it.
  Open `MDB_RDONLY`. Treat strfry as the sole writer.
- LMDB readers see a **consistent snapshot** for the life of a read txn (MVCC).
  **Do not hold a read txn open for long** — LMDB cannot reclaim pages newer than
  the oldest open reader, so a long-lived reader makes the DB file grow without
  bound while strfry writes/deletes. Open short read txns: one per scan batch,
  then close. For a long full-import, scan in bounded `levId` windows, closing
  the txn between windows.
- `lock=False` (no `lock.mdb` write): acceptable for pure readers and avoids
  needing write perms on the lock file, but you lose the shared reader table.
  If you keep read txns short (above), this is safe. If unsure, use normal
  locking with read access to `lock.mdb`.
- File permissions: deepfry's user needs read access to `data.mdb` (and
  `lock.mdb` if locking).

### 6.5 Incremental sync misses deletions — **correctness trap**
`levId` is monotonic, so tailing `EventPayload` from `lastLevId+1` (O3) catches
all **new inserts**. It does **NOT** catch **deletions**: when strfry deletes an
event (NIP-09, replaceable-event supersession, or expiration sweep — see
`src/events.cpp:240` `dbi_EventPayload.del`, and `:274-350` deletion/replace
logic), no new `levId` appears, so a levId-tailing reader keeps serving a deleted
event forever. Mitigations:
- **Periodic full reconcile:** re-scan all `EventPayload` keys, diff against
  derived-store `lev_id` set, delete rows whose `levId` no longer exists.
- **Change probe:** `Meta.negentropyModificationCounter` and/or a filesystem
  watch on `data.mdb` (strfry itself uses a file-change monitor,
  `src/apps/mesh/cmd_stream.cpp:124-134`) to know *when* to reconcile.
- Document the staleness window for deletions in the API.

### 6.6 Replaceable & parameterized-replaceable events
strfry enforces NIP-01 replaceable (kinds 0, 3, 10000–19999), parameterized
replaceable (30000–39999), and NIP-09 deletions at write time
(`src/events.cpp:274-350`). The physically-present `EventPayload` set already
reflects these rules — so a fresh full scan is correct. But incremental tailing
can transiently show both an old and a new version of a replaceable event until
the next reconcile (the old one's payload is deleted without a new levId signal).
deepfry should, in its derived store, also apply replaceable-collapse (keep
highest `created_at` per `(pubkey, kind)` for replaceable kinds; per
`(pubkey, kind, d-tag)` for parameterized) so queries match relay semantics.

### 6.7 Expired & ephemeral events
- `expiration` (NIP-40) events remain physically present until strfry's expiration
  sweep removes them. A naive reader may serve an event past its expiration.
  deepfry should filter `expiration != 0 && expiration <= now` at query time and/or
  during reconcile.
- Ephemeral events (kinds 20000–29999) are generally not persisted by relays, so
  expect them absent.

### 6.8 Compression path
If any payload starts with `0x01`, you need the zstd dictionary from
`CompressionDictionary[dictId]` (O6) and a zstd decoder. Cache decoded
dictionaries by `dictId`. If you skip this, compacted DBs will fail to decode.
Detect early: sample payloads on startup and warn if `0x01` is present but zstd
support is unavailable.

### 6.9 Malformed / oversized data defensiveness
- `PackedEventView` requires ≥ 88 bytes (`src/PackedEvent.h:25`); guard before
  slicing if you read `Event`.
- Treat decoded JSON as untrusted: it originated from the network. Validate before
  re-serving. Do **not** re-verify sigs on the hot path (expensive) unless your
  threat model requires it — strfry already validated on ingest.
- Enforce response size/row limits in GraphQL to prevent a single query from
  scanning the whole DB.

### 6.10 Map size growth & disk
strfry's `mapsize` is 10 TiB sparse; the file grows as needed. A long-lived
reader (§6.4) can pin old pages and accelerate growth. Keep read txns short and
monitor `data.mdb` size.

### 6.11 Operational coupling
- deepfry and strfry must run with compatible strfry versions. CI: pin a strfry
  commit, generate a fixture DB, assert deepfry decodes it.
- A strfry **compaction** can rewrite `EventPayload` (introducing `0x01`) and
  reset/relocate dictionaries. Reconcile fully after a known compaction.
- A strfry **upgrade** that bumps `dbVersion` must block deepfry (§6.1) until
  deepfry is updated.

---

## 7. Implementation phases (suggested)

1. **Read-only decoder library** — open env, decode `EventPayload` (0x00 + 0x01),
   `Meta` gate, endianness check. Unit-tested against a fixture `strfry-db`.
2. **Full importer** — windowed scan → derived store (SQLite first).
3. **Incremental tailer + reconciler** — levId watermark + periodic full diff for
   deletions; change probe via `negentropyModificationCounter` + file watch.
4. **Replaceable/expiration collapse** in the derived store.
5. **GraphQL API** — read-only resolvers, `latestPerAuthor`, pagination, limits.
6. **Hardening** — version gate, metrics, staleness reporting, load tests.

---

## 8. Open questions

- Target derived store: SQLite (single-node, simplest) vs Postgres (concurrent
  readers, richer indexing)? Default assumption: **SQLite** for v1.
- Acceptable deletion-staleness window (drives reconcile frequency)?
- Will deepfry and strfry always be co-located (settles §6.3 endianness)?
- Is GraphQL required, or would a SQL/REST endpoint over the derived store suffice?
  The derived-store design is endpoint-agnostic; GraphQL is one facade.
- Do we need live subscriptions (push) or is request/response enough for v1?

---

## 9. References (source of truth)

- `golpe.yaml` — schema, indices, comparators, `EventPayload` tablesRaw definition
- `src/PackedEvent.h` — packed binary layout
- `src/events.cpp:196-215` — `decodeEventPayload` (the 0x00/0x01 rule)
- `src/events.cpp:368-374` — write path (uncompressed `0x00` default)
- `src/events.cpp:240`, `:274-350` — deletion / replaceable logic
- `src/Decompressor.h` — zstd dictionary decompression
- `src/constants.h` — `CURR_DB_VERSION = 3`
- `src/onAppStartup.cpp:45-72` — version gate behavior
- `src/DBQuery.h` — strfry's own scan logic (reference for Approach B / semantics)
- `strfry.conf:6,8-16` — db path, `mapsize`, `maxreaders`, `noReadAhead`
