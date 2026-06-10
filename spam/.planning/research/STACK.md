# Stack Research

**Domain:** Read-only LMDB-to-GraphQL middleware (Rust) — strfry/deepfry
**Researched:** 2026-06-10
**Confidence:** HIGH (all versions verified against crates.io; API behaviour verified against docs.rs and official sources)

---

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `heed` | 0.22.1 | LMDB read-only access | Actively maintained by Meilisearch; typed wrapper with `READ_ONLY` env flag, `IntegerComparator` (maps to `MDB_INTEGERKEY`), named-DB discovery via unnamed root, `open_database` (no `MDB_CREATE`), `MDB_SET_RANGE`-style range iterators via `RoRange`. See critical notes below. |
| `zstd` | 0.13.3 | zstd decompression with custom dictionary | Official bindings to libzstd; exposes `Decoder::with_dictionary(&[u8])` — pass stored dictionary bytes directly. `DecoderDictionary` / `DDict` allow caching pre-digested dictionaries across calls. |
| `rusqlite` | 0.40.1 | Embedded SQLite derived index | Synchronous, ergonomic; feature `bundled` embeds a known-good SQLite; feature `window` enables `ROW_NUMBER() OVER` (the `latestPerAuthor` query); WAL is set via `PRAGMA journal_mode=WAL` at connection time — no special feature flag needed. JSON1 is part of bundled SQLite and usable via raw SQL. |
| `async-graphql` | 7.2.1 | GraphQL schema + resolvers | Most active Rust GraphQL library; procedural macros (`#[Object]`, `#[SimpleObject]`, `#[InputObject]`); built-in complexity/depth limiting; Relay cursor connections; query-only schemas (subscriptions are optional); direct axum integration via `async-graphql-axum`. |
| `async-graphql-axum` | 7.2.1 | GraphQL ↔ axum HTTP bridge | Official integration crate; provides `GraphQLRequest` extractor and `GraphQLResponse` responder; compatible with axum 0.8.x. |
| `axum` | 0.8.9 | HTTP server | Tokio-team maintained; idiomatic async, integrates cleanly with async-graphql-axum; best choice for new Rust HTTP services as of 2025-2026. |
| `tokio` | 1.52.3 | Async runtime | Standard async runtime for Rust; required by axum and async-graphql; use `features = ["full"]` for development, trim features in production. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `serde` | 1.0.228 | Derive `Serialize`/`Deserialize` | Required for any struct that maps to/from JSON |
| `serde_json` | 1.0.150 | JSON decode of Nostr event payloads | Use `serde_json::from_slice` on the raw bytes after stripping the `0x00` type byte. Define a local `NostrEvent` struct with `#[derive(Deserialize)]` — no need for the `nostr` crate (see below). |
| `notify` | 8.2.0 | Filesystem watch on `data.mdb` | Stable release; use `recommended_watcher()` with a `std::sync::mpsc::Sender`, then bridge to tokio via a spawned blocking task. 9.x is RC; use 8.2.0. |
| `notify-debouncer-mini` | 0.7.0 | Debounce rapid `data.mdb` inotify events | strfry does many small writes; raw notify fires many events per commit; debouncer collapses them into one signal before triggering reconcile. |
| `tracing` | 0.1.44 | Structured logging/instrumentation | Standard in the tokio ecosystem; use `tracing::instrument` on LMDB scan loops and GraphQL resolvers. |
| `tracing-subscriber` | 0.3.23 | Log output (JSON or pretty) | `EnvFilter` for runtime log-level control; `fmt::json()` for Docker-friendly output. |
| `thiserror` | (latest) | Error type derivation | Derive `#[derive(thiserror::Error)]` for the decoder library's error enum — clean boundary between LMDB, zstd, SQLite, and GraphQL error kinds. |
| `anyhow` | (latest) | Error propagation in binaries | Use in `main.rs` and integration tests; keep `thiserror` in library crates for typed errors. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `cargo` | Build, test, dependency management | Standard; use `cargo test -- --nocapture` for LMDB fixture tests |
| `cargo clippy` | Lint | Run with `-- -D warnings` in CI |
| `cargo fmt` | Format | `--check` in CI |
| `rust-toolchain.toml` | Pin Rust edition and version | Pin to stable; record `channel = "stable"` + minimum version |
| `Docker` (Alpine / `rust:alpine`) | Container build | Use `cargo build --release` with `RUSTFLAGS="-C target-feature=+crt-static"` for Alpine static binary; heed/lmdb links liblmdb which must also be static or available |

---

## The LMDB Crate Decision (Critical)

This project has hard constraints that differentiate the crates:

### Requirement matrix

| Requirement | `heed` 0.22 | `lmdb-rkv` 0.14 | raw `lmdb` 0.8 |
|---|---|---|---|
| `MDB_RDONLY` env flag | `EnvFlags::READ_ONLY` — YES | `EnvironmentFlags::READ_ONLY` — YES | `EnvironmentFlags::READ_ONLY` — YES |
| Open without `lock.mdb` write perm | `EnvFlags::NO_LOCK` (unsafe block) — YES | supported — YES | YES |
| `MDB_INTEGERKEY` on existing DB | `DatabaseOpenOptions::key_comparator::<IntegerComparator>()` — YES (preferred; `DatabaseFlags::INTEGER_KEY` deprecated since 0.21 but still works) | `DatabaseFlags::INTEGER_KEY` — YES | YES |
| Open named sub-DB without MDB_CREATE | `env.open_database(&rtxn, Some("name"))` returns `Option` — YES | `env.open_db(Some("name"))` — YES | YES |
| Discover sub-DB names via unnamed root | Open `open_database(&rtxn, None)` → `Database<Str, DecodeIgnore>` → `iter()` — documented in cookbook — YES | NOT documented; would need raw cursor on dbi 0 | NO |
| Raw byte cursor / `MDB_SET_RANGE` | `db.range(&rtxn, lower..)` on any typed or `Bytes`-typed DB — YES | `Cursor::get(MDB_SET_RANGE)` — YES | YES |
| Does NOT force a comparator on existing DB | Correct: `open_database` (not `create_database`) never sets a comparator; use unspecified types + `remap_types` — YES | Correct: `open_db` without `MDB_CREATE` — YES | YES |
| Maintenance activity | Active (Meilisearch, 2025-2026) | Dormant (Mozilla; last release 0.14.0, no 2025 activity) | Abandoned (last release 0.8.0, March 2018) |

**Verdict: use `heed` 0.22.1.**

`lmdb-rkv` works but is dormant. The unnamed-root iteration pattern (needed to assert which sub-DBs exist at startup) is explicitly documented in heed's cookbook and absent from lmdb-rkv. `heed`'s `IntegerComparator` maps cleanly to `MDB_INTEGERKEY` for `EventPayload`'s native-endian `uint64` keys.

### Critical: do NOT set a comparator when opening `Event__*` sub-DBs

The project spec prohibits opening `Event__*` sub-DBs at all. This constraint is architectural — deepfry must not touch those sub-DBs. `heed`'s typed API makes this natural: simply never call `open_database` with an `Event__*` name. The unnamed-root iteration is used only to assert/log which sub-DBs exist, not to scan them.

### Opening `EventPayload` correctly with heed

```rust
// Read-only env — no write lock needed
let env = unsafe {
    EnvOpenOptions::new()
        .max_dbs(20)
        .map_size(10 * 1024 * 1024 * 1024 * 1024) // match strfry's 10 TiB
        .flags(EnvFlags::READ_ONLY | EnvFlags::NO_LOCK)
        .open(db_path)?
};

// Open EventPayload: MDB_INTEGERKEY on levId (native-endian u64)
// Use heed's NativeEndian integer type for keys, raw Bytes for values
let rtxn = env.read_txn()?;
let payload_db: Database<U64<NativeEndian>, Bytes> = env
    .database_options()
    .types::<U64<NativeEndian>, Bytes>()
    .name("EventPayload")
    .key_comparator::<IntegerComparator>()
    .open(&rtxn)?
    .expect("EventPayload must exist");

// Range scan from lastLevId+1 (MDB_SET_RANGE semantics)
for result in payload_db.range(&rtxn, &(last_lev_id + 1)..)? {
    let (lev_id, raw_bytes) = result?;
    // decode raw_bytes: 0x00 prefix → plain JSON, 0x01 → zstd dict
}
rtxn.abort(); // CRITICAL: short-lived txn, do not hold open
```

---

## SQLite: `rusqlite` vs `sqlx`

**Use `rusqlite`.**

- `sqlx` with SQLite requires `sqlx::migrate!` async machinery and compile-time query checking that is awkward with a read-heavy schema built incrementally. It is async-first but SQLite I/O is not meaningfully faster async vs sync for this workload (single writer, batch imports).
- `rusqlite` is synchronous; run it in `tokio::task::spawn_blocking` for the importer/tailer background task. GraphQL resolvers that query SQLite also use `spawn_blocking`. This is the correct pattern — SQLite's C library is inherently synchronous.
- **Feature flags needed:**
  - `bundled` — embed SQLite 3.49.x, avoids OS version mismatch, required for Alpine Docker builds
  - `window` — enables `sqlite3_create_window_function`, unlocks `ROW_NUMBER() OVER (PARTITION BY ...)` for `latestPerAuthor`
  - `modern_sqlite` — enables SQLite 3.34.1+ bindings (implied by `bundled` at current versions)
- **WAL mode:** `PRAGMA journal_mode=WAL` executed on connection open. No crate feature flag — just a raw SQL pragma.
- **JSON1:** Part of bundled SQLite. Use via raw SQL: `json_extract(tags, '$[0][1]')`. No special feature flag.

```toml
rusqlite = { version = "0.40.1", features = ["bundled", "window"] }
```

---

## GraphQL: `async-graphql` vs `juniper`

**Use `async-graphql` 7.2.1.**

- `juniper` 0.17.1 exists but has lower community momentum, less axum integration documentation, and no built-in query complexity limiting.
- `async-graphql` has first-class `#[Object]` procedural macros, built-in depth/complexity limits (essential for protecting unbounded SQLite scans), Relay cursor pagination helpers, and `async-graphql-axum` integration crate at matching version.
- v8.0.0-rc.5 is available but RC — do not use for initial build; upgrade after GA.

```toml
async-graphql = { version = "7.2.1", features = ["tracing"] }
async-graphql-axum = "7.2.1"
```

---

## Nostr event JSON: `serde_json` only — do NOT add the `nostr` crate

The `nostr` crate (0.44.3) is a full Nostr protocol implementation including key management, event signing, relay client, and NIP implementations. deepfry needs none of that — it only deserialises already-validated event JSON.

**Use plain `serde_json` with a local struct:**

```rust
#[derive(Debug, Deserialize)]
pub struct NostrEvent {
    pub id: String,
    pub pubkey: String,
    pub created_at: i64,
    pub kind: u16,
    pub tags: Vec<Vec<String>>,
    pub content: String,
    pub sig: String,
}
```

The `nostr` crate would pull in secp256k1, bech32, and other heavy dependencies with no benefit since signature verification is explicitly out of scope (strfry validated on ingest).

---

## zstd dictionary decompression API

```rust
use std::io::{Cursor, Read};
use zstd::stream::read::Decoder;

// dict_bytes: raw bytes from CompressionDictionary[dictId]
// compressed: &[u8] after stripping the 0x01 type byte and 4-byte dictId
fn decompress_with_dict(compressed: &[u8], dict_bytes: &[u8]) -> Result<Vec<u8>> {
    let cursor = Cursor::new(compressed);
    let mut decoder = Decoder::with_dictionary(cursor, dict_bytes)?;
    let mut out = Vec::new();
    decoder.read_to_end(&mut out)?;
    Ok(out)
}
```

Cache `dict_bytes` by `dictId` (HashMap in the importer state); a fresh call to `Decoder::with_dictionary` on each compressed event is acceptable since dictionary lookup is cheap once cached as raw bytes. For higher throughput, `zstd::dict::DecoderDictionary` / `DDict` can be pre-digested once per `dictId`.

---

## Filesystem watching: `notify` + debouncer

```toml
notify = "8.2.0"
notify-debouncer-mini = "0.7.0"
```

- `notify` 9.x is RC; use stable 8.2.0.
- For tokio integration: `recommended_watcher` uses a `std::sync::mpsc::Sender`; bridge to tokio by spawning a `tokio::task::spawn_blocking` loop that forwards events into a `tokio::sync::mpsc::channel` or sets a `tokio::sync::Notify`.
- Watch the `data.mdb` file itself (and its parent for create events); pair with polling `negentropyModificationCounter` from `Meta` to confirm actual event changes vs. checkpoint writes.

---

## Installation (Cargo.toml)

```toml
[dependencies]
# LMDB
heed = "0.22.1"

# zstd
zstd = "0.13.3"

# SQLite derived index
rusqlite = { version = "0.40.1", features = ["bundled", "window"] }

# GraphQL + HTTP
async-graphql = { version = "7.2.1", features = ["tracing"] }
async-graphql-axum = "7.2.1"
axum = "0.8.9"
tokio = { version = "1.52.3", features = ["full"] }

# JSON
serde = { version = "1.0.228", features = ["derive"] }
serde_json = "1.0.150"

# Filesystem watch
notify = "8.2.0"
notify-debouncer-mini = "0.7.0"

# Observability
tracing = "0.1.44"
tracing-subscriber = { version = "0.3.23", features = ["env-filter", "json"] }

# Error handling
thiserror = "2"
anyhow = "1"
```

---

## Alternatives Considered

| Recommended | Alternative | Why Not |
|-------------|-------------|---------|
| `heed` | `lmdb-rkv` | Dormant since ~2022; no named-root iteration docs; fewer maintainers |
| `heed` | raw `lmdb` 0.8 | Abandoned (2018); no safe wrappers; `lmdb-rkv` is its successor |
| `rusqlite` | `sqlx` (sqlite feature) | sqlx SQLite is async-first but requires more ceremony; spawn_blocking with rusqlite is simpler for a single-writer derived store |
| `async-graphql` | `juniper` | juniper has less momentum, no built-in complexity limiting, weaker axum story |
| `serde_json` (local struct) | `nostr` crate | nostr crate is a full protocol implementation; pulls secp256k1/bech32; we only need JSON deserialization, sig verification is out of scope |
| `notify` 8.2.0 | `notify` 9.x RC | 9.x is a release candidate; stick with stable for production |

---

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| `lmdb` 0.8 | Abandoned since 2018; no maintenance | `heed` |
| `rkv` crate | Higher-level wrapper on `lmdb-rkv`; abstracts away the low-level cursor access needed for `MDB_SET_RANGE` on integer keys; dormant | `heed` directly |
| `sqlx` with SQLite feature | Async SQLite overhead without benefit; `MIN_RUST_VERSION = 1.94.0` is high; compile-time query checking requires a live DB at build time | `rusqlite` with `spawn_blocking` |
| Setting `MDB_CREATE` when opening sub-DBs | Creates new sub-DBs in strfry's env — catastrophic if env opened without `READ_ONLY` | `open_database` (not `create_database`) in heed |
| Opening any `Event__*` sub-DB | Custom golpe comparators not present in deepfry process; range scans silently wrong | Only open `EventPayload`, `Meta`, `CompressionDictionary`, `Event` |
| Long-lived `RoTxn` | Pins LMDB pages, prevents strfry reclaim, `data.mdb` grows unbounded | Short per-window transactions; `rtxn.abort()` after each batch |
| `nostr` crate | Full protocol stack with crypto; no benefit since sigs already verified by strfry | `serde_json` + local struct |
| `notify` 9.x RC | Pre-release; API may change | `notify` 8.2.0 stable |

---

## Stack Patterns by Variant

**If the database has never been compacted (0x00 payloads only):**
- zstd path is inactive but must still be present; detect `0x01` on startup and log a warning if no dictionary loaded

**If running in Docker on Alpine:**
- Use `rusqlite` `bundled` feature (embeds SQLite; avoids OS library mismatch)
- Link heed/lmdb statically: `LMDB_STATIC=1` or ensure `lmdb-sys` builds from source via heed's default Cargo feature

**If running integration tests against a fixture strfry-db:**
- Do not use `EnvFlags::NO_LOCK` in tests if strfry is also running against the same env; use standard read locking (`EnvFlags::READ_ONLY` only)
- For CI with a fixture DB (no live strfry), `EnvFlags::NO_LOCK` is safe

---

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| `async-graphql` 7.2.1 | `async-graphql-axum` 7.2.1, `axum` ^0.8.1, `tokio` ^1 | Must keep async-graphql and async-graphql-axum at same minor version |
| `heed` 0.22.1 | `tokio` ^1 (for spawn_blocking bridge) | heed itself is sync; wrap LMDB calls in `spawn_blocking` for async context |
| `rusqlite` 0.40.1 | `bundled` embeds SQLite 3.49.x | `modern_sqlite` feature implied by `bundled`; enables 3.34+ window functions |
| `notify` 8.2.0 | Bridges to tokio via std mpsc → spawn_blocking | Not natively async; bridge required |

---

## Sources

- `heed` 0.22.1 — https://docs.rs/heed/0.22.1/heed/ (EnvFlags, DatabaseOpenOptions, IntegerComparator, cookbook)
- `heed` cookbook — https://docs.rs/heed/latest/heed/cookbook/index.html (unnamed root iteration pattern — HIGH confidence)
- `lmdb-rkv` — https://docs.rs/lmdb-rkv/latest/lmdb/struct.Environment.html (dormancy confirmed by crates.io version history)
- `zstd` 0.13.3 — https://docs.rs/zstd/0.13.3/zstd/dict/index.html (DecoderDictionary, with_dictionary API)
- `rusqlite` 0.40.1 — https://github.com/rusqlite/rusqlite/blob/master/Cargo.toml (bundled, window feature flags)
- `async-graphql` 7.2.1 — https://docs.rs/async-graphql/latest/async_graphql/ (macros, complexity limits)
- `async-graphql-axum` 7.2.1 — https://docs.rs/async-graphql-axum/7.2.1/async_graphql_axum/ (axum ^0.8.1 compat)
- `axum` 0.8.9 — https://crates.io/crates/axum (release date April 2026)
- `tokio` 1.52.3 — https://crates.io/crates/tokio
- `notify` 8.2.0 — https://docs.rs/notify/8.2.0/notify/ (stable; 9.x RC)
- `serde_json` 1.0.150 — https://crates.io/crates/serde_json
- crates.io version verification: all crate versions confirmed current as of research date

---

*Stack research for: deepfry — Rust LMDB-to-GraphQL middleware over strfry*
*Researched: 2026-06-10*
