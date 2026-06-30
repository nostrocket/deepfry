---
created: 2026-06-30 23:40:17+0800
title: Bound read-txn concurrency / set maxreaders to avoid MDB_READERS_FULL
area: lmdb
files:
  - src/lmdb/env.rs:17
  - src/lmdb/env.rs:34
  - src/graphql/resolvers.rs:136
  - src/query/hydrate.rs:175
  - src/query/engine.rs:621
---

## Problem

lmdb2graphql opens strfry's LMDB environment **read-only** and shares it with the
strfry relay process (both mount the same `/Volumes/BACKUP/nostr/strfry_database`).
LMDB's reader-slot table lives in `lock.mdb` and is **shared across every process**
that opens the env. The table size is fixed when `lock.mdb` is first created.

On 2026-06-30 15:07 the strfry relay **crashed (SIGABRT)** with:

```
mdb_txn_begin: MDB_READERS_FULL: Environment maxreaders limit reached
```

strfry's `maxreaders` is 256 (now raised to 4096 in deepfry config commit 0d512f2),
but lmdb2graphql contributes readers to the same table. Each GraphQL request opens a
short-lived `RoTxn` inside a `tokio::task::spawn_blocking` closure (resolvers.rs,
payload.rs, hydrate.rs, engine.rs). LMDB allocates a reader slot **per thread** that
holds a read txn; tokio's blocking pool defaults to **512 threads**, so under load
lmdb2graphql alone can claim far more than 256 (even 4096) slots and exhaust the
shared table — taking down the canonical relay, not just this read lens.

Note: `EnvOpenOptions` in `src/lmdb/env.rs` does **not** currently call
`.max_readers(...)`, so the table is created at heed's default (126). If lmdb2graphql
opens the env before strfry, it creates `lock.mdb` at 126 — smaller than strfry's
configured ceiling and trivially exhausted.

## Solution

Two complementary changes (do both):

1. **Set an explicit `max_readers` on env open** (`src/lmdb/env.rs:17,34`, and the
   other `EnvOpenOptions::new()` sites) to match strfry's 4096, so whichever process
   creates `lock.mdb` sizes the table large enough. Plumb it from
   `lmdb2graphql.yaml` (new `max_readers` key, default 4096) alongside `map_size`.

2. **Bound concurrent read txns** so lmdb2graphql can never occupy more slots than
   the table allows: cap tokio's `max_blocking_threads`, and/or gate the
   spawn_blocking read path behind a `tokio::sync::Semaphore` (a read-concurrency
   ceiling). resolvers.rs:136 already has a "reject past the ceiling" note — extend
   that pattern to a global read-txn permit so excess requests get a clean client
   error instead of exhausting the shared LMDB reader table.

Verification: drive concurrent heavy `latestPerAuthor` queries (the pubkey_iterator
scoring workload) and confirm strfry no longer aborts with MDB_READERS_FULL. See
deepfry memory `strfry-host-deploy` and `lmdb2graphql-heavy-query-capacity-bound`,
and the debug doc `.planning/debug/adapter-crash-heavy-query.md`.
