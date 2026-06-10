# Architecture Research

**Domain:** Read-only LMDB-to-SQLite derived-index service with GraphQL API (Rust)
**Researched:** 2026-06-10
**Confidence:** HIGH

---

## Standard Architecture

### System Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│  External: strfry process (sole writer, LMDB env)                   │
│  strfry-db/data.mdb  (MDB_INTEGERKEY EventPayload, 10TiB sparse)    │
└────────────────────────────┬────────────────────────────────────────┘
                             │ read-only volume mount
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│  deepfry Docker container (this service, Rust)                      │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Startup Gate                                                │   │
│  │  • open env MDB_RDONLY, max_dbs≥20, map_size≥10TiB          │   │
│  │  • read Meta id=1 → dbVersion, endianness                   │   │
│  │  • hard abort if dbVersion != 3 or endian mismatch          │   │
│  └──────────────────────┬───────────────────────────────────────┘   │
│                         │ env handle (Arc<Env>)                     │
│                         ▼                                           │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  LMDB Access Layer  (src/lmdb/)                              │   │
│  │  • EnvHandle wraps heed Env (Arc, shared across tasks)       │   │
│  │  • open_read_txn() → short-lived RoTxn (drop to release)    │   │
│  │  • scan_window(from_lev_id, window) → Vec<(u64, Vec<u8>)>   │   │
│  │  • read_meta() → MetaRecord { db_version, endianness,        │   │
│  │                               negentropy_mod_counter }       │   │
│  │  • read_compression_dict(dict_id) → Vec<u8>                 │   │
│  └──────────────────────┬───────────────────────────────────────┘   │
│                         │ raw (levId, payload_bytes) pairs          │
│                         ▼                                           │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Payload Decoder  (src/decoder/)                             │   │
│  │  • decode(bytes, dict_cache) → NostrEvent (serde struct)     │   │
│  │  • type byte 0x00 → &bytes[1..] as UTF-8 JSON                │   │
│  │  • type byte 0x01 → dictId u32 native-endian, zstd decomp   │   │
│  │  • DictCache: HashMap<u32, DecoderDictionary> (Arc<Mutex>)  │   │
│  │  • detect 0x01 on startup; warn if zstd support absent       │   │
│  └──────────────────────┬───────────────────────────────────────┘   │
│                         │ decoded NostrEvent structs                │
│                         ▼                                           │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  Derived Store  (src/store/)                                 │   │
│  │  SQLite (WAL mode, synchronous=NORMAL)                       │   │
│  │  • events table + event_tags table (spec §4.3)              │   │
│  │  • upsert_event(NostrEvent, lev_id)                          │   │
│  │  • delete_by_lev_ids(ids)    — reconciler path               │   │
│  │  • list_all_lev_ids()        — reconciler diff input         │   │
│  │  • replaceable_collapse()    — keep max created_at per key   │   │
│  │  • expire_events(now)        — filter expiration!=0 <= now   │   │
│  │  • last_lev_id() → u64       — tailer watermark              │   │
│  │  deadpool-sqlite pool (async-aware, spawn_blocking bridge)   │   │
│  └───────┬──────────────────────┬───────────────────────────────┘   │
│          │                      │                                   │
│          │ read queries         │ write via sync engine             │
│          ▼                      ▼                                   │
│  ┌───────────────┐   ┌──────────────────────────────────────────┐   │
│  │  GraphQL API  │   │  Sync Engine  (src/sync/)                │   │
│  │  (src/api/)   │   │                                          │   │
│  │  axum +       │   │  ┌──────────────┐  ┌──────────────────┐  │   │
│  │  async-graphql│   │  │FullImporter  │  │ IncrementalTailer│  │   │
│  │               │   │  │ windowed     │  │ MDB_SET_RANGE    │  │   │
│  │  resolvers:   │   │  │ levId scan   │  │ lastLevId+1      │  │   │
│  │  • events()   │   │  │ batch upsert │  │ on interval      │  │   │
│  │  • latestPer  │   │  └──────┬───────┘  └────────┬─────────┘  │   │
│  │    Author()   │   │         │                   │            │   │
│  │  • stats()    │   │  ┌──────▼───────────────────▼──────────┐ │   │
│  │               │   │  │  Reconciler                         │ │   │
│  │  pagination   │   │  │  • diff derived lev_id set vs LMDB  │ │   │
│  │  (created_at, │   │  │  • delete stale rows                │ │   │
│  │   lev_id)     │   │  │  • replaceable_collapse + expire    │ │   │
│  │  hard limits  │   │  └──────────────────────────────────────┘ │   │
│  └───────────────┘   │                                          │   │
│       ▲              │  ┌──────────────────────────────────────┐ │   │
│       │              │  │  Change Probe                        │ │   │
│       │              │  │  • poll Meta.negentropyModCounter    │ │   │
│       │              │  │  • notify crate: watch data.mdb     │ │   │
│       │              │  │  • signal Reconciler when changed   │ │   │
│       │              │  └──────────────────────────────────────┘ │   │
│       │              └──────────────────────────────────────────┘   │
│       │                                                             │
│  HTTP :8080  (docker-compose expose)                                │
└─────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| Startup Gate | Version/endianness validation before any work begins | Inline in `main()`, hard `process::exit(1)` on fail |
| LMDB Access Layer | Open env read-only, issue short-lived read txns, windowed levId cursor scans | `heed` crate, `Arc<Env>`, `RoTxn` dropped immediately after batch |
| Payload Decoder | Decode `0x00` raw and `0x01` zstd-dict payloads to `NostrEvent` structs | `zstd` crate with dictionary API, `DictCache` keyed by `dictId` |
| Derived Store | Write-through SQLite index; owns schema, upsert, reconcile, collapse, expiry | `rusqlite` + `deadpool-sqlite`; WAL mode; `spawn_blocking` bridge |
| Sync Engine — Full Importer | One-shot windowed scan of all `EventPayload` on first run | Calls LMDB Access Layer in fixed-size windows (e.g. 1000 levIds/txn) |
| Sync Engine — Incremental Tailer | Periodic `MDB_SET_RANGE` from `lastLevId+1` to ingest new events | `tokio::time::interval`, reads from LMDB, upserts into Derived Store |
| Sync Engine — Reconciler | Full diff to detect deletions; runs on change signal or schedule | Reads all lev_ids from both LMDB and SQLite, deletes orphans |
| Change Probe | Detect when LMDB has changed without polling every record | `notify` crate watching `data.mdb` + periodic poll of `negentropyModificationCounter` |
| GraphQL API | Read-only query surface over Derived Store | `async-graphql` + `axum`; resolvers hit SQLite via pool |

---

## Recommended Project Structure

```
spam/
├── src/
│   ├── main.rs                  # startup gate, tokio::main, spawn tasks, bind axum
│   ├── config.rs                # ~/deepfry/spam.yaml via config crate
│   ├── lmdb/
│   │   ├── mod.rs               # EnvHandle, open_read_only_env()
│   │   ├── meta.rs              # read Meta record (dbVersion, endianness, modcounter)
│   │   ├── scan.rs              # scan_window(from, window_size) → short txn, drop
│   │   └── types.rs             # RawPayload(levId, bytes), MetaRecord
│   ├── decoder/
│   │   ├── mod.rs               # decode(raw) → Result<NostrEvent>
│   │   ├── dict_cache.rs        # Arc<RwLock<HashMap<u32, DecoderDictionary>>>
│   │   └── types.rs             # NostrEvent, TagEntry
│   ├── store/
│   │   ├── mod.rs               # StoreHandle, pool init, migrate()
│   │   ├── schema.sql           # events + event_tags DDL, indexes (spec §4.3)
│   │   ├── upsert.rs            # upsert_batch(events) within single SQLite txn
│   │   ├── reconcile.rs         # list_all_lev_ids, delete_stale, collapse, expire
│   │   └── query.rs             # events(), latest_per_author(), stats() — read path
│   ├── sync/
│   │   ├── mod.rs               # SyncEngine: coordinates importer/tailer/reconciler
│   │   ├── importer.rs          # full windowed scan on first run (no watermark)
│   │   ├── tailer.rs            # incremental tail from lastLevId watermark
│   │   ├── reconciler.rs        # diff + delete + collapse + expire
│   │   └── probe.rs             # change probe: notify watcher + modcounter poll
│   └── api/
│       ├── mod.rs               # axum Router, GraphQL handler mount
│       ├── schema.rs            # async-graphql Schema, Query root
│       └── resolvers/
│           ├── events.rs        # events() resolver with filters + pagination
│           ├── latest_per_author.rs  # ROW_NUMBER window query
│           └── stats.rs         # DbStats resolver
├── tests/
│   ├── fixture/                 # pinned strfry-db fixture (generated, committed)
│   └── decoder_test.rs          # decode 0x00 + 0x01 against fixture
├── Cargo.toml
├── Makefile
└── docker-compose.deepfry.yml
```

### Structure Rationale

- **lmdb/**: Isolated read-only layer. Owns all unsafe LMDB interaction. No decoded types leak out — only `RawPayload`. Other modules never touch `heed` directly.
- **decoder/**: Pure function layer. Input = raw bytes + dict cache. Output = `NostrEvent`. Zero I/O. Testable with fixture payloads in isolation.
- **store/**: All SQLite writes and reads go through here. The only place that owns rusqlite connections. Exposes an async-compatible interface via `spawn_blocking`.
- **sync/**: Orchestrates the three sync patterns and the change probe as independent tokio tasks. Has no HTTP/GraphQL concern.
- **api/**: Owns the HTTP surface. Resolvers call into `store/query.rs` only — never directly into `lmdb/` or `sync/`.

---

## Architectural Patterns

### Pattern 1: Windowed Read Transaction per Scan Batch

**What:** Open a short-lived LMDB read txn, iterate a bounded window of levIds (e.g. 1000 per batch), collect results into a `Vec`, drop the txn, then process (decode + upsert). Repeat until exhausted.

**When to use:** Full importer, reconcile key-scan, any cursor that may touch many pages.

**Trade-offs:** Each window pays one txn open/close roundtrip. The benefit is that no txn is held while SQLite writes are in progress, preventing page pinning from growing `data.mdb`.

```rust
// In src/lmdb/scan.rs
pub fn scan_window(
    env: &Env,
    db: &Database<OwnedType<u64>, ByteSlice>,
    from_lev_id: u64,
    window: usize,
) -> Result<Vec<(u64, Vec<u8>)>> {
    let rtxn = env.read_txn()?;                // open
    let mut cursor = db.iter_from(&rtxn, &from_lev_id)?;
    let mut batch = Vec::with_capacity(window);
    for _ in 0..window {
        match cursor.next().transpose()? {
            Some((k, v)) => batch.push((*k, v.to_vec())),  // copy out of mmap
            None => break,
        }
    }
    drop(rtxn);                                // release — pages can be reclaimed
    Ok(batch)
}
```

**Critical detail:** Copy bytes *out* of the mmap slice (`v.to_vec()`) before dropping the txn. The slice is only valid for the txn's lifetime; holding a reference after drop is undefined behavior with `heed`.

### Pattern 2: spawn_blocking Bridge for SQLite from Async Context

**What:** SQLite via rusqlite is synchronous. All SQLite operations go through `tokio::task::spawn_blocking`, ensuring async tasks are never blocked. `deadpool-sqlite` provides a pool that manages this automatically.

**When to use:** Every resolver read and every sync engine write.

**Trade-offs:** Each pool checkout may block briefly if all connections are busy; size the pool (`max_size`) to match expected concurrency (GraphQL resolvers + one sync writer).

```rust
// In src/store/upsert.rs
pub async fn upsert_batch(pool: &Pool, events: Vec<(u64, NostrEvent)>) -> Result<()> {
    let conn = pool.get().await?;
    conn.interact(|conn| {
        let tx = conn.transaction()?;
        for (lev_id, ev) in &events {
            // INSERT OR REPLACE into events ...
            // INSERT OR REPLACE into event_tags ...
        }
        tx.commit()
    }).await??;
    Ok(())
}
```

### Pattern 3: Change Probe → Reconcile Signal (Not Polling Every Row)

**What:** Use a two-tier probe: (1) filesystem watch (`notify` crate) on `data.mdb` for coarse "file modified" signal; (2) read `Meta.negentropyModificationCounter` to cheaply confirm an actual event add/remove. Only trigger a reconcile when the counter changes.

**When to use:** The reconciler is expensive (full lev_id set scan). Avoid running it on every tail tick.

**Trade-offs:** `notify` on Docker volume mounts may not deliver events (Docker bind mounts and some overlay2 configurations suppress inotify). Fall back to a periodic timer (every N minutes) as the guaranteed reconcile trigger.

```rust
// In src/sync/probe.rs
pub async fn run_probe(
    env: Arc<Env>,
    meta_db: MetaDb,
    reconcile_tx: mpsc::Sender<()>,
) {
    let mut last_counter = read_mod_counter(&env, &meta_db);
    let mut interval = tokio::time::interval(Duration::from_secs(300)); // 5 min fallback
    loop {
        interval.tick().await;
        let current = read_mod_counter(&env, &meta_db);
        if current != last_counter {
            last_counter = current;
            let _ = reconcile_tx.try_send(());
        }
    }
}
```

### Pattern 4: Separate Read/Write Paths Through Derived Store

**What:** The sync engine holds the single write connection (or uses a write-serializing mechanism). GraphQL resolvers use read-only connections from the pool. SQLite WAL mode allows concurrent readers while the sync engine writes.

**When to use:** Always — this prevents sync engine writes from blocking GraphQL queries.

**Trade-offs:** WAL mode requires all connections open the same file with WAL enabled. In `deadpool-sqlite`, set `PRAGMA journal_mode=WAL` on connection open.

### Pattern 5: Replaceable Collapse as Derived Store Concern

**What:** After upserting a replaceable event (kind 0, 3, 10000–19999) or parameterized replaceable (30000–39999), delete older rows from `events` where `(pubkey, kind)` matches and `created_at` is lower. This mirrors strfry's write-time semantics in the derived store.

**When to use:** On every upsert of a replaceable kind, and during full reconcile.

**Trade-offs:** Slightly more work per upsert. The alternative — deferring collapse to query time — makes `latestPerAuthor` queries slower and returns stale superseded events.

```sql
-- After upsert for replaceable kinds (0, 3, 10000-19999)
DELETE FROM events
WHERE pubkey = ?1 AND kind = ?2
  AND created_at < ?3
  AND lev_id != ?4;

-- For parameterized replaceable (30000-39999), also include d-tag
DELETE FROM events
WHERE pubkey = ?1 AND kind = ?2
  AND lev_id IN (
      SELECT e.lev_id FROM events e
      JOIN event_tags t ON t.lev_id = e.lev_id
      WHERE t.tag_name = 'd' AND t.tag_value = ?3
        AND e.created_at < ?4
  );
```

---

## Data Flow

### Startup Flow

```
main()
  │
  ├─ open LMDB env (MDB_RDONLY, max_dbs≥20, map_size=10TiB)
  ├─ read Meta id=1
  ├─ assert dbVersion == 3  →  FAIL: log + exit(1)
  ├─ assert endianness == host  →  FAIL: log + exit(1)
  ├─ sample first 100 EventPayload entries: warn if any 0x01 found
  ├─ open / migrate SQLite (schema.sql, WAL mode)
  ├─ spawn SyncEngine tasks (tokio::spawn)
  │     ├─ if last_lev_id(sqlite) == 0: FullImporter task
  │     ├─ IncrementalTailer task (interval)
  │     ├─ Reconciler task (triggered by probe channel)
  │     └─ ChangeProbe task (file watch + timer)
  └─ bind axum on :8080
```

### Full Import Flow (first run or after reset)

```
FullImporter
  │
  loop:
    ├─ scan_window(lev_id=cursor, window=1000)  [short read txn, dropped]
    ├─ for each (lev_id, bytes): decode(bytes, dict_cache) → NostrEvent
    ├─ upsert_batch(events) into SQLite  [spawn_blocking]
    ├─ replaceable_collapse() for replaceable kinds in batch
    ├─ cursor = max(lev_id) + 1
    └─ until window returns empty
```

### Incremental Tail Flow (ongoing)

```
IncrementalTailer (every N seconds, e.g. 10s)
  │
  ├─ last_lev = last_lev_id(sqlite)
  ├─ scan_window(lev_id=last_lev+1, window=1000)  [short read txn, dropped]
  ├─ decode + upsert_batch
  ├─ replaceable_collapse for new events
  └─ if window full: loop (don't wait for next tick)
```

### Reconcile Flow (on change probe signal or periodic)

```
Reconciler
  │
  ├─ full_lmdb_lev_ids = scan all EventPayload keys (windowed, short txns)
  ├─ full_sqlite_lev_ids = SELECT lev_id FROM events
  ├─ orphans = sqlite_ids - lmdb_ids
  ├─ delete_by_lev_ids(orphans) from events + event_tags
  ├─ expire_events(now)                    -- purge NIP-40 expired
  └─ replaceable_collapse() full pass      -- ensure consistency after deletions
```

### GraphQL Query Flow

```
HTTP POST /graphql
  │
  └─ axum handler → async-graphql executor
        │
        └─ resolver (e.g. events())
              │
              ├─ build SQL from filter args (kinds, authors, since, until, tag)
              ├─ enforce limit ceiling (hard cap, e.g. 1000)
              ├─ cursor pagination on (created_at DESC, lev_id DESC)
              ├─ pool.get().await → spawn_blocking → rusqlite query
              └─ return Vec<Event>
```

---

## Concurrency Model: One Process, Two Concerns

The service runs as a single Tokio process with two distinct task groups that must not block each other.

### Sync Tasks (CPU + I/O bound, non-async internals)

LMDB cursor scans and SQLite upserts are synchronous operations. They run via `tokio::task::spawn_blocking`. The sync engine coordinates them sequentially within a task:

```
tokio::spawn(async move {
    // This is the sync engine "supervisor" task.
    // It serializes Importer → Tailer → Reconciler runs.
    // Each step internally uses spawn_blocking for the blocking work.
    loop {
        tailer_tick().await;           // async sleep, then spawn_blocking scan
        if probe.changed() {
            reconciler_run().await;    // spawn_blocking LMDB scan + SQLite diff
        }
        tokio::time::sleep(interval).await;
    }
})
```

The importer runs to completion before the tailer loop starts (first-run gate via `last_lev_id == 0`).

### API Tasks (async I/O)

The axum server and GraphQL executor run as normal async tasks. They only touch SQLite via `deadpool-sqlite`'s `interact()` which internally uses `spawn_blocking`. They never touch the LMDB env directly.

### Shared State

| State | Type | Shared How |
|-------|------|------------|
| LMDB env | `Arc<heed::Env>` | Cloned into sync tasks; never into API tasks |
| SQLite pool | `Arc<deadpool_sqlite::Pool>` | Cloned into both sync tasks and API tasks |
| Dict cache | `Arc<RwLock<HashMap<u32, DecoderDictionary>>>` | Cloned into sync tasks only |
| Change probe channel | `tokio::sync::mpsc::Sender<()>` | Probe → Reconciler task |
| Staleness metadata | `Arc<RwLock<StalenessInfo>>` | Written by sync, read by `stats` resolver |

---

## SQLite Schema (spec §4.3 verbatim)

```sql
CREATE TABLE IF NOT EXISTS events (
    lev_id      INTEGER PRIMARY KEY,
    id          TEXT UNIQUE NOT NULL,
    pubkey      TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    kind        INTEGER NOT NULL,
    expiration  INTEGER,
    content     TEXT NOT NULL,
    sig         TEXT NOT NULL,
    tags        TEXT NOT NULL,   -- JSON array
    raw         TEXT NOT NULL,   -- full event JSON
    seen_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS event_tags (
    lev_id      INTEGER NOT NULL REFERENCES events(lev_id) ON DELETE CASCADE,
    tag_name    TEXT NOT NULL,
    tag_value   TEXT NOT NULL
);

-- deepfry-owned indexes
CREATE INDEX IF NOT EXISTS ix_pubkey_kind_created
    ON events(pubkey, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_kind_created
    ON events(kind, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_created
    ON events(created_at DESC);
CREATE INDEX IF NOT EXISTS ix_tag
    ON event_tags(tag_name, tag_value);
CREATE INDEX IF NOT EXISTS ix_event_tags_lev
    ON event_tags(lev_id);
```

`ON DELETE CASCADE` on `event_tags.lev_id` lets `delete_by_lev_ids()` on `events` automatically clean up tags — no separate tag delete step needed.

---

## Anti-Patterns

### Anti-Pattern 1: Holding a Read Txn Across SQLite Writes

**What people do:** Open one LMDB read txn at the start of the full import, scan everything, then close it at the end.

**Why it's wrong:** The txn pins all pages written since it opened. While SQLite upserts run (potentially seconds), strfry continues writing. The mmap cannot reclaim those pages. `data.mdb` grows without bound. On a 10 TiB sparse file, this causes filesystem-level waste.

**Do this instead:** The windowed scan pattern (Pattern 1). Open txn, scan 1000 rows, copy bytes out, drop txn, decode and upsert, repeat.

### Anti-Pattern 2: Touching Event__ Index DBIs

**What people do:** Try to scan `Event__pubkey` or `Event__kind` for a faster targeted import path.

**Why it's wrong:** golpe registers custom comparators (`StringUint64`, `Uint64Uint64`) at runtime via `mdb_set_compare`. These are never written to `data.mdb`. A foreign reader sees raw bytes sorted by `memcmp`, not the registered comparator. `MDB_SET_RANGE` lands in the wrong position silently. Data returned is undefined.

**Do this instead:** Only scan `EventPayload` (keyed by `levId` via `MDB_INTEGERKEY`, standard byte-order — safe). Build your own indexes in SQLite.

### Anti-Pattern 3: Opening a Write Transaction Against the LMDB Env

**What people do:** Attempt to open a write txn to e.g. update a progress marker in LMDB.

**Why it's wrong:** LMDB has a single-writer constraint enforced at the OS level. A second writer from a different process (or the same process if strfry is running) will either block indefinitely or error. If both somehow succeed, the env is corrupted.

**Do this instead:** Store all deepfry-owned state (watermarks, metadata) in the derived SQLite store.

### Anti-Pattern 4: Serving GraphQL Directly From LMDB

**What people do:** Skip the derived store and try to answer GraphQL queries by scanning LMDB on each request.

**Why it's wrong:** (1) Each scan must open and close a read txn — under concurrent GraphQL queries, you hold many simultaneous reader slots (LMDB has a maxreaders limit, default 126). (2) `levId` order is not chronological; sorting requires a full scan every time. (3) Tag filtering has no index — O(n) per query. The derived SQLite store exists precisely to make these queries fast and index-backed.

**Do this instead:** Sync to SQLite; serve all queries from SQLite.

### Anti-Pattern 5: Relying Only on the Incremental Tailer for Correctness

**What people do:** Ship only the tailer (levId watermark), skipping the reconciler.

**Why it's wrong:** strfry deletes events for replaceable supersession, NIP-09 deletions, and expiration sweeps. Deleted events leave no new levId. The tailer never sees them go away. The derived store serves deleted events indefinitely.

**Do this instead:** Run the reconciler on a schedule (hours) and on change-probe signals. Document the staleness window in the `stats` resolver.

---

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| strfry LMDB | `heed` read-only env open; docker read-only bind mount of `strfry-db/` | Never write; assert dbVersion==3 on startup; map_size must match or exceed strfry's |
| Docker compose | `docker-compose.deepfry.yml`; `volumes: - ./strfry-db:/app/strfry-db:ro` | `ro` flag is essential; co-located on same host → same endianness |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| lmdb/ ↔ decoder/ | `Vec<(u64, Vec<u8>)>` — owned bytes, no lifetimes crossing boundary | Bytes copied out of mmap before txn drop |
| decoder/ ↔ store/ | `Vec<(u64, NostrEvent)>` — deserialized struct | NostrEvent is `serde::Deserialize`; tags normalized at decode time |
| sync/ ↔ api/ | Shared `Arc<Pool>` (SQLite) only | Sync writes; API reads; WAL allows concurrent access |
| sync/probe ↔ sync/reconciler | `tokio::sync::mpsc::channel(1)` | Bounded 1; if reconciler is busy, probe signals are dropped (intentional) |
| api/ ↔ store/ | `store/query.rs` public async fns | Resolvers never construct SQL directly; all queries in store module |

---

## Build Order (maps to spec §7 phases)

The dependency graph drives phase ordering:

```
Phase 1: decoder library + lmdb access layer
  └─ no dependencies; purely testable against fixture DB
  └─ deliverable: decode 0x00 and 0x01, Meta gate, endianness check

Phase 2: derived store schema + full importer
  └─ depends on: Phase 1 (decoder output = importer input)
  └─ deliverable: windowed scan → SQLite populated; watermark stored

Phase 3: incremental tailer + reconciler + change probe
  └─ depends on: Phase 2 (store upsert/delete/lev_id APIs exist)
  └─ deliverable: ongoing sync; deletion correctness via periodic reconcile

Phase 4: replaceable collapse + expiration
  └─ depends on: Phase 2/3 (upsert and reconcile hooks exist)
  └─ deliverable: derived store matches relay semantics

Phase 5: GraphQL API
  └─ depends on: Phase 2 (store query APIs); Phase 4 (collapse = correct data)
  └─ deliverable: events(), latestPerAuthor(), stats(); pagination; hard limits

Phase 6: hardening
  └─ depends on: all above
  └─ deliverable: metrics, staleness reporting, version gate robustness, load tests, Docker packaging
```

Phases 1–2 can be developed and tested entirely with a fixture DB, without a live strfry instance. Phase 3 requires a running strfry to validate incremental behavior.

---

## Scaling Considerations

This is a single-node sidecar. The derived store is not expected to scale beyond the single strfry instance it mirrors.

| Concern | At current scale | If needed |
|---------|-----------------|-----------|
| SQLite read concurrency | WAL mode handles N concurrent GraphQL readers; pool size 5–10 is sufficient | Switch to Postgres if read contention appears |
| LMDB reader slots | Sync engine uses 1–2 simultaneous read txns; no contention with strfry | strfry's maxreaders (default 126) is not a concern |
| GraphQL query cost | Hard limit ceiling (e.g. 1000 rows) + index-backed queries | Add query complexity limit via async-graphql's built-in complexity extension |
| Sync lag | Tailer runs every 10s; reconciler every 5 min | Reduce intervals if lower latency needed; still eventually consistent |
| `data.mdb` growth from reader | Windowed txns (Pattern 1) prevent page pinning | Monitor `data.mdb` size in production |

---

## Sources

- strfry source: `src/events.cpp`, `src/PackedEvent.h`, `src/constants.h`, `src/Decompressor.h`, `golpe.yaml` (referenced in spec.md §9)
- spec.md §3, §4, §4.3, §6, §7 — on-disk encoding and required operations (source of truth for this document)
- `heed` crate docs: https://github.com/meilisearch/heed (HIGH confidence — verified via Context7)
- `async-graphql` cursor connection docs: https://github.com/async-graphql/async-graphql (HIGH confidence — verified via Context7)
- `rusqlite` WAL + pool pattern: https://github.com/rusqlite/rusqlite (HIGH confidence — verified via Context7)
- `tokio::task::spawn_blocking`: https://docs.rs/tokio/latest/tokio/task/fn.spawn_blocking.html (HIGH confidence — verified via Context7)
- `deadpool-sqlite`: https://lib.rs/crates/deadpool-sqlite (MEDIUM confidence — verified via WebSearch + crates.io)
- `notify` crate (file watch): https://github.com/notify-rs/notify (MEDIUM confidence — verified via WebSearch)
- `zstd` crate with dictionary support: https://docs.rs/zstd (MEDIUM confidence — verified via WebSearch + docs.rs)
- LMDB page-pinning behavior: https://deepwiki.com/LMDB/lmdb/3-transaction-management (MEDIUM confidence — corroborates spec §6.4)
- azoth-lmdb LMDB→SQLite projection pattern: https://lib.rs/crates/azoth-lmdb (LOW confidence — single source, used as corroboration only)

---
*Architecture research for: deepfry — read-only LMDB derived-index service (Rust)*
*Researched: 2026-06-10*
