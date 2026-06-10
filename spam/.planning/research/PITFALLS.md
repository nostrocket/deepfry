# Pitfalls Research

**Domain:** Read-only Rust reader of strfry's LMDB; derived SQLite index; GraphQL query surface
**Researched:** 2026-06-10
**Confidence:** HIGH (spec §6 is authoritative, verified against LMDB internals and Rust ecosystem docs)

---

## Critical Pitfalls

### Pitfall 1: Opening Event__* index sub-DBs — silently wrong range scans (THE BIG ONE)

**What goes wrong:**
Any `MDB_SET_RANGE` or cursor traversal on `Event__id`, `Event__pubkey`, `Event__kind`, `Event__pubkeyKind`, `Event__tag`, or any `Event__*` dbi returns results ordered by `memcmp` byte order, not by strfry's golpe comparator order. The results are not an error — they are silently wrong subsets. You get *some* events back, just the wrong ones, with no indication anything is amiss.

**Why it happens:**
LMDB never persists custom comparators to disk. golpe registers `StringUint64`, `Uint64Uint64`, `StringUint64Uint64` comparators via `mdb_set_compare` at runtime inside strfry's process. A foreign reader that opens those dbis gets raw bytes ordered by LMDB's default `memcmp` — a completely different ordering. Range scans land at the wrong B-tree position. This is the core reason Approach A (derived store, read only `EventPayload`) exists.

**How to avoid:**
Never open any `Event__*` named sub-database. In the Rust LMDB wrapper (`heed` or `lmdb-rkv`), never call `open_database` or the equivalent with any name matching `Event__*`. Open only `EventPayload` (required), `Meta`, `Event` (optional, point lookups only by exact levId), and `CompressionDictionary`. Consider asserting at startup that deepfry does NOT open any dbi whose name starts with `Event__` — the unnamed root dbi can be scanned to list all dbi names, which is a useful safety check.

**Warning signs:**
Query results for range-based filters (by pubkey, kind, time window) return data that passes JSON validation but is systematically incomplete or has a suspicious distribution of pubkeys/kinds compared to what you expect from your dataset.

**Phase to address:**
Phase 1 (decoder library). This constraint must be encoded structurally — the LMDB-opening code must not provide a mechanism to open index dbis. It should not be possible to open them by accident.

---

### Pitfall 2: map_size smaller than strfry's configured mapsize — env open fails

**What goes wrong:**
If deepfry opens the LMDB env with a `map_size` smaller than strfry's configured mapsize (default: `10995116277760` bytes = 10 TiB), the open call returns `MDB_INVALID` or a related error. The 10 TiB is a *sparse* file reservation — it does not consume actual disk space — but LMDB refuses to open an env whose stated mapsize exceeds the configured ceiling of the opening process.

**Why it happens:**
Many Rust LMDB wrapper examples use small default or example map sizes (1 GiB, 10 GiB). The heed `EnvOpenOptions` builder requires an explicit `map_size()` call; omitting it or copying a small example value causes the open to fail against strfry's real environment. The 10 TiB sparse file approach is unusual enough that developers unfamiliar with strfry's configuration hit this immediately.

**How to avoid:**
Set `map_size` to at least `10_995_116_277_760` (10 TiB) in `EnvOpenOptions::map_size()`. This does not allocate memory — LMDB uses `mmap` and the reservation is virtual. Read `strfry.conf` for the actual configured value and expose it as a required deepfry config parameter so operators can keep them in sync. Fail loudly with a clear message if the open returns a map-size-related error.

**Warning signs:**
`mdb_env_open` returns `MDB_INVALID` or `-30798` immediately on startup before any database operations. The strfry-db directory is readable but the open fails.

**Phase to address:**
Phase 1 (decoder library). The env-open helper must set this correctly from the start.

---

### Pitfall 3: dbVersion mismatch — accepting wrong-format data silently

**What goes wrong:**
If deepfry does not gate on `Meta.dbVersion == 3`, it may decode `EventPayload` values that follow a different on-disk layout from a newer (or older) strfry version. The result may be successful JSON parsing of garbage (if a future format happens to parse), or panics and decode errors that are confusing to diagnose, or — worst — silently wrong events in the derived store.

**Why it happens:**
The dbVersion check is easy to defer ("we'll add it later") or under-specify ("warn but don't abort"). strfry treats its format as an internal private API with no compatibility guarantee between releases. `CURR_DB_VERSION` is defined in `src/constants.h` and strfry's own startup code (`src/onAppStartup.cpp:45-72`) refuses to run on mismatch — deepfry must do the same.

**How to avoid:**
On startup, before opening any other dbi, read `Meta` (id=1), decode `dbVersion`, and `panic!` / `std::process::exit(1)` with a message like `"strfry dbVersion is X, deepfry requires 3 — update deepfry or pin strfry"` if the value is not exactly 3. This must not be configurable. In CI: pin a specific strfry commit hash, include a fixture `strfry-db` in the repo, and assert deepfry starts successfully against it.

**Warning signs:**
deepfry starts without error but decoded events have anomalous field values or JSON parse failures are more common than expected. Or deepfry was not updated when strfry was upgraded and nobody noticed.

**Phase to address:**
Phase 1 (decoder library). This is the first operation after env open — no other reads happen until the gate passes.

---

### Pitfall 4: Native-endian integers — wrong values on mismatched architecture

**What goes wrong:**
All integers in strfry's LMDB — `levId` (the `MDB_INTEGERKEY` key), `created_at`, `kind`, `expiration`, `dictId` — are stored in **host byte order** (`lmdb::to_sv`/`from_sv` do raw memory copies with no byte-swap). If deepfry runs on a different byte-order host than strfry, all integer reads are silently wrong: `levId` keys read as garbage values, `created_at` values are nonsense timestamps, `kind` values are wrong.

**Why it happens:**
The co-location assumption (deepfry and strfry on the same machine) makes this seem trivially safe. But x86-64 and arm64 are both little-endian, so cross-architecture deployments within the same endianness class might fool a developer into skipping the check. The real risk is that someone deploys deepfry on a big-endian host (unlikely but possible), or that the check is never written because "we're always co-located."

**How to avoid:**
Read `Meta.endianness` on startup (same transaction as the dbVersion check). Compare it to `cfg!(target_endian = "little")` / `cfg!(target_endian = "big")` at compile time, or use `u16::from_ne_bytes([1,0]) == 1` at runtime. If they differ, refuse to start with a clear error. Document in the operational runbook that deepfry must be co-located with strfry or on a same-endian host.

**Warning signs:**
`levId` cursor iteration returns `MDB_NOTFOUND` immediately (keys look like zero after byte-swap), or `created_at` values are in the year 2554 or similar.

**Phase to address:**
Phase 1 (decoder library). Same startup gate as dbVersion.

---

### Pitfall 5: Opening a write transaction — corrupts strfry's environment

**What goes wrong:**
LMDB enforces a single-writer constraint per environment. If deepfry opens a write transaction against strfry's env (even accidentally), and strfry is running concurrently, the result is environment corruption. This is not recoverable without restoring from backup.

**Why it happens:**
In `heed`, `env.write_txn()` compiles and links without any indication it is unsafe for this use case. A developer adding "just a quick metadata write" or accidentally calling the wrong method corrupts the live relay database. Rust's type system does not prevent this.

**How to avoid:**
Open the environment exclusively with `EnvOpenOptions::new().read_txn()` / `MDB_RDONLY` flag. In the Rust code, never call `env.write_txn()` — enforce this with a `#[deny]` lint comment in the module, and/or wrap the env in a newtype that only exposes `read_txn()`. Mount `strfry-db` as a read-only Docker volume bind mount (`ro` option) — the OS will then reject any write attempt at the filesystem level, providing a defense-in-depth backstop even if the Rust code misbehaves.

**Warning signs:**
Any code that calls `env.write_txn()` or `env.rw_txn()` in the deepfry codebase. Any `unsafe` block that touches LMDB write APIs.

**Phase to address:**
Phase 1 (decoder library). The Docker compose service definition must also enforce the `ro` mount from Phase 6 (hardening/deployment).

---

### Pitfall 6: Holding long read transactions — unbounded LMDB file growth

**What goes wrong:**
LMDB is multi-versioned (MVCC). An open read transaction pins all pages from its snapshot — strfry cannot reclaim pages freed by subsequent writes/deletes. A single long-lived read txn (e.g., one opened at the start of a full scan and held open for minutes) causes `data.mdb` to grow continuously as strfry writes new events, with no upper bound until the txn is closed. On a busy relay, this can exhaust disk in hours.

**Why it happens:**
The "simplest" approach is to open one read txn, scan everything, close it. This works in development with a small fixture DB. On a live relay with thousands of events/minute, it becomes a disk bomb.

**How to avoid:**
Scan `EventPayload` in bounded `levId` windows (e.g., 1000–10000 records per window). Open a read txn, scan the window, close the txn, yield, then open the next. Between windows, sleep briefly or yield to the async executor. For the incremental tailer, open a short read txn, read new events since the watermark, close it. Configure the window size as a tunable parameter. Monitor `data.mdb` size and alert on unexpected growth.

**Warning signs:**
`data.mdb` growing faster than the rate of new events warrants. `mdb_reader_list` shows a deepfry reader with an old transaction ID. Disk usage growing during a full reconcile.

**Phase to address:**
Phase 2 (full importer). The windowed scan pattern must be baked in from the first implementation, not retrofitted later.

---

### Pitfall 7: Incremental tailing misses deletions — stale events served forever

**What goes wrong:**
`levId` is monotonically increasing and is only assigned on insert. When strfry deletes an event (NIP-09, replaceable-event supersession, expiration sweep — `src/events.cpp:240`, `:274-350`), it calls `dbi_EventPayload.del(levId)` — no new `levId` is assigned. A deepfry tailer watching `lastLevId+1` sees nothing. The derived store retains the deleted event and serves it to GraphQL consumers indefinitely.

**Why it happens:**
Tailing by monotonic ID is the natural pattern for append-only logs. strfry's EventPayload *feels* append-only but is actually mutable (deletes happen). This is a correctness trap, not an operational one — the system appears to work correctly while serving data the relay has removed.

**How to avoid:**
Implement a periodic full reconciler (separate from the incremental tailer) that: (1) scans all `levId` keys in `EventPayload`, (2) diffs the set against `lev_id` values in the derived SQLite store, (3) deletes SQLite rows whose `lev_id` is no longer present in LMDB. Trigger reconciliation on: (a) elapsed time (configurable, e.g., 4 hours), (b) `Meta.negentropyModificationCounter` increasing since last reconcile, (c) filesystem watch on `data.mdb` inode change. Document the deletion-staleness window in the GraphQL API responses (e.g., a `stats { lastReconcileAt }` field).

**Warning signs:**
Queries for known-deleted events still return results. `negentropyModificationCounter` keeps incrementing but derived store event count never decreases. NIP-09 deletion tests pass on first run then fail after a day.

**Phase to address:**
Phase 3 (incremental tailer + reconciler). The reconciler must ship in the same phase as the tailer — never ship the tailer alone, as it creates a correctness illusion.

---

### Pitfall 8: Replaceable/parameterized-replaceable duplicates during tail window

**What goes wrong:**
Between two reconcile runs, the incremental tailer may observe both an old replaceable event (e.g., kind 0 profile) and its replacement. strfry physically deletes the old event (no new levId), so the next reconcile will remove it. But in the window between insert of the new version and the next reconcile, the derived store contains two versions of the same (pubkey, kind) pair. GraphQL queries return both — violating relay semantics.

**Why it happens:**
The derived store is not aware of NIP-01 replaceable-collapse rules at ingest time. Treating every `EventPayload` row as independent is correct for non-replaceable events but wrong for replaceable ones.

**How to avoid:**
In the derived SQLite store, apply replaceable-collapse at upsert time: for kinds 0, 3, and 10000–19999 (replaceable), keep only the row with the highest `created_at` per `(pubkey, kind)`. For kinds 30000–39999 (parameterized-replaceable), keep only the highest `created_at` per `(pubkey, kind, d-tag)`. Enforce this as a post-insert trigger or as an explicit DELETE before INSERT in the reconcile logic. The replaceable-collapse rules are in `src/events.cpp:274-350` — verify any edge cases (same `created_at` tiebreak) against strfry's actual logic.

**Warning signs:**
Two kind-0 events for the same pubkey appear in query results. Replaceable event tests pass on initial import but fail after incremental tail runs.

**Phase to address:**
Phase 4 (replaceable/expiration collapse). However, the derived-store schema should reserve space for the `d-tag` column from Phase 2 to avoid schema migrations.

---

### Pitfall 9: Serving NIP-40 expired events — stale data past expiration

**What goes wrong:**
Events with a non-zero NIP-40 `expiration` tag remain physically present in `EventPayload` until strfry's expiration sweep removes them. Between the expiration time and the sweep (or the next deepfry reconcile), deepfry serves expired events that the relay itself would reject via `REQ`.

**Why it happens:**
Expiration is easy to forget because most events don't use it, and `expiration == 0` (no expiration) is the common case. A query that doesn't filter on `expiration` appears correct on all test data and only fails in production when a client relies on expiration semantics.

**How to avoid:**
At query time in GraphQL resolvers, add a mandatory filter: `WHERE expiration = 0 OR expiration > unixepoch('now')`. Also apply this filter during reconcile to proactively drop expired events from the derived store rather than waiting for strfry's sweep. Parse the `expiration` tag from the raw JSON during import (the `tags` array, first entry where tag_name = 'expiration') and store it in the `expiration` column. Index it: `CREATE INDEX ix_expiration ON events(expiration) WHERE expiration != 0`.

**Warning signs:**
Clients receive events with `expiration` values in the past. Expiration integration tests pass only when run immediately after fixture creation.

**Phase to address:**
Phase 4 (replaceable/expiration collapse). The expiration column must be populated from Phase 2 import onward, even if query-time filtering is added later.

---

### Pitfall 10: Missing zstd 0x01 dictionary path — silent decode failure on compacted DBs

**What goes wrong:**
The default strfry write path always prepends `0x00` (raw JSON). The `0x01` (zstd dictionary compressed) form only appears after an operator runs strfry's offline compaction tooling. If deepfry handles only `0x00`, it silently fails to decode any event on a compacted database — returning no results, or panicking, or skipping events depending on error handling. The operator who ran compaction may not realize deepfry is broken.

**Why it happens:**
Development and CI fixture databases are always uncompressed. The `0x01` path is tested only when someone explicitly compacts, which is a manual operator action. It is easy to ship a decoder that "works in dev" but fails silently in production after the first compaction.

**How to avoid:**
Implement `0x01` decoding from day one, even if the test fixture only exercises `0x00`. The decode function must: (1) read `dictId` from bytes 1..4 as a native-endian u32, (2) fetch the zstd dictionary from `CompressionDictionary[dictId]`, (3) cache it (loading a zstd dictionary has non-trivial CPU cost — cache by `dictId` in a `HashMap<u32, DecoderDictionary>`), (4) decompress with the cached dictionary. Use the `zstd` crate (FFI to libzstd) rather than `ruzstd` (pure Rust) for dictionary support — verify `ruzstd`'s dictionary API maturity before using it. On startup, sample the first N payloads and warn if any are `0x01` and zstd support is not compiled in.

**Warning signs:**
After a compaction, deepfry returns 0 events. Logs show decode errors on byte `0x01`. `data.mdb` size dropped significantly (compaction happened) but derived store event count did not change.

**Phase to address:**
Phase 1 (decoder library). Both `0x00` and `0x01` paths must be implemented and unit-tested together. A fixture with a `0x01` payload must be included in the test suite.

---

### Pitfall 11: Tokio async executor blocked by synchronous LMDB scans

**What goes wrong:**
LMDB operations are synchronous C FFI calls. A full-scan or large windowed scan called directly from a Tokio async task blocks the async executor thread for its entire duration. During a full reconcile (potentially scanning millions of events), all other async tasks — including in-flight GraphQL requests — are starved. Response latency spikes to seconds or indefinitely.

**Why it happens:**
The natural Rust code path is `async fn import() { for row in cursor { ... } }` — this compiles fine and works on small fixtures but blocks the executor on large data sets. Tokio's cooperative scheduling only works when tasks actually yield; synchronous blocking calls never yield.

**How to avoid:**
Wrap all LMDB scan operations in `tokio::task::spawn_blocking(|| { ... })`. The blocking thread pool is separate from the async executor pool. For windowed scans, each window can be a separate `spawn_blocking` call with an `.await` between them — this lets the executor run other tasks between windows. Never call LMDB cursor operations directly in an async context. Apply the same rule to SQLite writes during reconcile (use `spawn_blocking` or a dedicated SQLite thread via `tokio::sync::mpsc`).

**Warning signs:**
GraphQL requests time out during reconcile runs. Tokio task metrics show worker threads at 100% utilization with no async yield points. Response latency histogram has a bimodal distribution (normal + very long tail).

**Phase to address:**
Phase 2 (full importer) and Phase 3 (incremental tailer). The import loop structure must use `spawn_blocking` from the first implementation.

---

### Pitfall 12: SQLite write contention during reconcile while serving reads

**What goes wrong:**
SQLite in WAL mode allows concurrent readers and one writer. However, the full reconciler (which performs bulk INSERT/DELETE operations) holds the write lock for the duration of its transaction. In the default journal mode (not WAL), even reads are blocked. In WAL mode, reads proceed from the prior snapshot, but reads that start after the reconcile commits will see the updated data atomically. The real problem is that without WAL mode and a `busy_timeout`, `SQLITE_BUSY` errors terminate read queries during reconcile.

**Why it happens:**
New Rust SQLite integrations (`rusqlite`) default to journal mode DELETE, not WAL. A developer tests with a small DB where reconcile is instantaneous and never observes contention.

**How to avoid:**
Enable WAL mode on the derived SQLite database with `PRAGMA journal_mode=WAL` at database initialization. Set `PRAGMA busy_timeout=5000` so read connections wait up to 5 seconds for a write lock rather than returning `SQLITE_BUSY` immediately. Use `BEGIN IMMEDIATE` for write transactions during reconcile to take the write lock at transaction start, avoiding upgrade deadlocks. Keep reconcile write transactions bounded (batch deletes rather than one giant transaction) so the write lock is held for milliseconds, not seconds.

**Warning signs:**
`SQLITE_BUSY` or "database is locked" errors in GraphQL resolver logs. GraphQL requests fail sporadically during reconcile windows. Response latency spikes correlate exactly with reconcile start times.

**Phase to address:**
Phase 3 (reconciler). SQLite WAL mode must be set at schema creation time in Phase 2; it cannot be changed later without recreating the file.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Skip `0x01` zstd path initially | Simpler Phase 1 decoder | Silent failure after first compaction | Never — implement both paths in Phase 1 |
| Hold one read txn for full import | Simpler scan loop | Unbounded disk growth on live relay | Never on live relay; acceptable only for offline fixture testing |
| Open `EventPayload` by ordinal position instead of name | Avoids needing to know dbi name | Fragile if strfry adds dbis before EventPayload | Never — always open by name |
| Skip reconciler, only tail | Simpler Phase 3 | Deleted events served forever | Never — correctness is non-negotiable |
| SQLite in DELETE journal mode | Fewer setup steps | Reader/writer contention during reconcile | Never — WAL must be set at schema creation |
| Omit dbVersion gate | One fewer startup check | Silent decode errors after strfry upgrade | Never — this is a hard correctness gate |
| Skip `spawn_blocking` for small imports | Simpler async code | Executor starvation on production data volumes | Acceptable only for unit tests with fixture DBs |

---

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| strfry LMDB env open | Using a small default map_size | Set map_size >= 10 TiB (match strfry.conf), read value from config |
| strfry LMDB env open | Using `MDB_NOLOCK` without understanding the tradeoff | Use normal read locking unless write perms on lock.mdb are unavailable; if `MDB_NOLOCK`, the reader table is bypassed and stale readers cannot be detected |
| heed / lmdb-rkv `open_database` | Opening an `Event__*` dbi by name | Whitelist the only acceptable dbi names and panic on any other attempt |
| zstd dictionary | Constructing a new `DecoderDictionary` per event | Load the dictionary once per `dictId`, cache in a `HashMap`; dictionary loading is expensive |
| SQLite WAL | Setting `journal_mode=WAL` after schema creation | Must set at first connection before any schema migrations |
| Docker mount | Mounting `strfry-db` as read-write | Always mount as `ro` (read-only) in docker-compose to enforce OS-level write prevention |
| `PackedEvent` slice | Slicing bytes without checking length | Always assert `len >= 88` before parsing; malformed entries should be logged and skipped, not panicked |

---

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Full-DB read txn during import | `data.mdb` grows continuously during import | Windowed scan, close txn between windows | Immediately on any live relay with active writes |
| Synchronous LMDB in async task | GraphQL latency spikes during reconcile | `spawn_blocking` for all LMDB operations | Any data set > ~10k events |
| Un-batched SQLite inserts (no transaction) | Import takes hours instead of minutes | Wrap window inserts in a single SQLite transaction | Any import > ~1k rows |
| No index on `expiration` | Full table scan for expiration filtering | `CREATE INDEX ix_expiration ON events(expiration) WHERE expiration != 0` | Negligible now; noticeable at 100k+ events |
| No `dictId` cache for zstd dicts | Dictionary reload per event on compacted DB | `HashMap<u32, DecoderDictionary>` in the decoder state | Immediately on any compacted DB |
| Unbounded GraphQL response | Single query scans entire derived store | Hard `limit` ceiling in resolver (e.g., max 1000) | Any production data set |

---

## "Looks Done But Isn't" Checklist

- [ ] **Decoder:** Handles `0x01` zstd-dictionary payloads, not only `0x00` — test with a fixture containing a `0x01` payload
- [ ] **Startup gate:** Refuses to run (process exits non-zero) on `dbVersion != 3`, not just warns
- [ ] **Startup gate:** Refuses to run on endianness mismatch, not just warns
- [ ] **Scan loop:** Uses bounded `levId` windows and closes read txn between windows — verify with a large fixture DB that `data.mdb` does not grow during import
- [ ] **Reconciler:** Ships alongside the incremental tailer, not as a future TODO — verify deleted events are removed from derived store
- [ ] **Expiration:** Query-time filter `WHERE expiration = 0 OR expiration > now` is applied in ALL resolvers, not just `events()`
- [ ] **Replaceable collapse:** Kind 0 and kind 3 deduplication applied at ingest; verify with two kind-0 events for the same pubkey
- [ ] **SQLite WAL:** `PRAGMA journal_mode=WAL` confirmed in schema initialization; verified by checking `data.db-wal` file exists at runtime
- [ ] **Docker mount:** `strfry-db` mounted `:ro` in docker-compose service definition
- [ ] **map_size:** Configured from `strfry.conf` value, not hardcoded or defaulted to a small value
- [ ] **`spawn_blocking`:** All LMDB cursor operations wrapped; verified with Tokio tracing that no blocking occurs on async threads during reconcile

---

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Event__* dbi opened and wrong data imported | HIGH | Drop and rebuild derived SQLite store from scratch; fix the dbi open code; re-import |
| map_size set too small (open failed) | LOW | Update config, restart; no data loss |
| dbVersion mismatch accepted, wrong data imported | HIGH | Drop and rebuild derived store; pin strfry version; re-import |
| Long read txn caused disk growth | MEDIUM | Close deepfry, strfry's next write cycle will reclaim pages; monitor `data.mdb` size stabilization |
| Deleted events in derived store (missed reconcile) | LOW | Run a forced full reconcile; no persistent corruption |
| SQLite in DELETE mode causing BUSY errors | LOW | `PRAGMA journal_mode=WAL` can be set on existing DB; restart deepfry |
| Write txn opened against strfry env | CRITICAL | Stop everything immediately; restore strfry-db from backup; do not restart strfry against a corrupted env |

---

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Event__* range scan (comparator trap) | Phase 1 — decoder library | Whitelist test: assert no `Event__*` dbi is ever opened; code review gate |
| map_size too small | Phase 1 — env open helper | Integration test: open against a real strfry-db fixture; confirm open succeeds |
| dbVersion not gated | Phase 1 — decoder library | Unit test: fixture with dbVersion=99 must cause process exit |
| Native-endian integers unchecked | Phase 1 — decoder library | Unit test: fixture with endianness mismatch must cause process exit |
| Write txn opened | Phase 1 — decoder library + Phase 6 deployment | Static: `grep -r 'write_txn\|rw_txn'` returns no hits; Docker `:ro` mount in compose |
| Long read txns | Phase 2 — full importer | Integration test: monitor `data.mdb` size growth during full import with concurrent writes |
| Incremental tail misses deletions | Phase 3 — reconciler | Integration test: insert event, delete it in strfry, run reconcile, assert event absent from GraphQL |
| Replaceable duplicates | Phase 4 — collapse logic | Unit test: import two kind-0 events for same pubkey; assert only newer is returned |
| Expired events served | Phase 4 — collapse logic | Unit test: import event with expiration in the past; assert it is excluded from all query results |
| zstd 0x01 path missing | Phase 1 — decoder library | Unit test: fixture payload with `0x01` prefix + dictionary bytes; assert correct JSON decoded |
| Tokio blocking in async | Phase 2 — importer + Phase 3 tailer | Tokio tracing: assert no sync blocking on executor threads during reconcile |
| SQLite write contention | Phase 3 — reconciler | Test: concurrent GraphQL reads during reconcile; assert 0 SQLITE_BUSY errors with WAL + busy_timeout |

---

## Sources

- `spec.md §6` — authoritative caveats and gotchas (primary source, HIGH confidence)
- `spec.md §3` — on-disk encoding details including `EventPayload` type-byte prefix, native-endian integers
- `spec.md §4` — required database operations and forbidden operations list
- strfry source refs: `src/constants.h` (dbVersion), `src/events.cpp:196-215` (decodeEventPayload), `src/events.cpp:240,274-350` (deletion/replace logic), `src/onAppStartup.cpp:45-72` (version gate), `src/PackedEvent.h` (packed struct)
- [LMDB documentation — Reader Lock Table](http://www.lmdb.tech/doc/group__readers.html) — reader table and MDB_NOLOCK tradeoffs
- [LMDB internals — long read transaction page pinning](http://www.lmdb.tech/doc/group__internal.html) — DB file growth from long read txns
- [heed — Fully typed LMDB wrapper](https://github.com/meilisearch/heed) — Rust LMDB API surface
- [SQLite WAL documentation](https://sqlite.org/wal.html) — concurrent reader/writer semantics
- [Understanding SQLITE_BUSY](http://activesphere.com/blog/2018/12/24/understanding-sqlite-busy) — contention patterns and `BEGIN IMMEDIATE`
- [tokio::task::spawn_blocking](https://docs.rs/tokio/latest/tokio/task/fn.spawn_blocking.html) — blocking thread pool for synchronous operations
- [zstd dict — DecoderDictionary](https://rustdocs.bsx.fi/zstd/dict/struct.DecoderDictionary.html) — dictionary handle reuse in the zstd Rust crate

---
*Pitfalls research for: read-only Rust LMDB reader of strfry with derived SQLite index and GraphQL*
*Researched: 2026-06-10*
