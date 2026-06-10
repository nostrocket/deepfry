<!-- GSD:project-start source:PROJECT.md -->

## Project

**LMDB2GraphQL — Queryable GraphQL Adapter over strfry's LMDB**

LMDB2GraphQL is a read-only adapter that exposes a [strfry](https://github.com/hoytech/strfry) Nostr relay's LMDB database as a standard, directly queryable GraphQL endpoint. It lets consumers run rich queries the Nostr `REQ` protocol cannot express — e.g. *"latest 20 kind-1 events per pubkey"* — without going through the strfry relay process. It is a **query lens over strfry's live data, not a copy of it**: LMDB2GraphQL reads strfry's existing on-disk indexes directly and never replicates event data into a separate store. It ships as one component of the DeepFry stack.

**Core Value:** Serve correct, rich queries over strfry's events by reading strfry's **live** on-disk state directly — never copying event data or indexes out of strfry (honoring the DeepFry stack's data-separation rule: *no event payloads outside StrFry*), while never writing to strfry's database.

### Constraints

- **Tech stack**: Rust — needs LMDB read bindings with **custom comparator support** (`mdb_set_compare`), zstd (dictionary decompression for payload hydration), and a GraphQL server. No SQLite / no derived store. Diverges from the Go stack by deliberate choice.
- **Correctness (the crux)**: LMDB2GraphQL's reimplemented comparators must be byte-identical to strfry's, or range scans return silently-wrong data. Mitigate with a pinned-fixture self-check that fails closed at startup.
- **Coupling**: Approach B depends on strfry's internal index key formats AND comparator semantics (broader surface than the payload format alone). Pin a strfry version; treat as a private API.
- **Safety**: read-only LMDB access only (`MDB_RDONLY`); never open a write txn. Keep read txns short (per-query, bounded by limit) so strfry can reclaim pages and `data.mdb` doesn't grow unbounded.
- **Compatibility**: hard version gate — refuse to run if `Meta.dbVersion != 3`. Assert `Meta.endianness` matches host; refuse on mismatch (co-located assumption).
- **Deployment**: packaged as its own Docker subsystem within the DeepFry stack, with a docker-compose service, co-located with strfry, mounting `strfry-db` read-only.
- **Compression**: must handle `0x01` zstd-dictionary payloads (appears after strfry offline compaction) even though default deployments are `0x00` uncompressed.

<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->

## Technology Stack

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

## The LMDB Crate Decision (Critical)

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

### Critical: do NOT set a comparator when opening `Event__*` sub-DBs

### Opening `EventPayload` correctly with heed

## SQLite: `rusqlite` vs `sqlx`

- `sqlx` with SQLite requires `sqlx::migrate!` async machinery and compile-time query checking that is awkward with a read-heavy schema built incrementally. It is async-first but SQLite I/O is not meaningfully faster async vs sync for this workload (single writer, batch imports).
- `rusqlite` is synchronous; run it in `tokio::task::spawn_blocking` for the importer/tailer background task. GraphQL resolvers that query SQLite also use `spawn_blocking`. This is the correct pattern — SQLite's C library is inherently synchronous.
- **Feature flags needed:**
- **WAL mode:** `PRAGMA journal_mode=WAL` executed on connection open. No crate feature flag — just a raw SQL pragma.
- **JSON1:** Part of bundled SQLite. Use via raw SQL: `json_extract(tags, '$[0][1]')`. No special feature flag.

## GraphQL: `async-graphql` vs `juniper`

- `juniper` 0.17.1 exists but has lower community momentum, less axum integration documentation, and no built-in query complexity limiting.
- `async-graphql` has first-class `#[Object]` procedural macros, built-in depth/complexity limits (essential for protecting unbounded SQLite scans), Relay cursor pagination helpers, and `async-graphql-axum` integration crate at matching version.
- v8.0.0-rc.5 is available but RC — do not use for initial build; upgrade after GA.

## Nostr event JSON: `serde_json` only — do NOT add the `nostr` crate

#[derive(Debug, Deserialize)]

## zstd dictionary decompression API

## Filesystem watching: `notify` + debouncer

- `notify` 9.x is RC; use stable 8.2.0.
- For tokio integration: `recommended_watcher` uses a `std::sync::mpsc::Sender`; bridge to tokio by spawning a `tokio::task::spawn_blocking` loop that forwards events into a `tokio::sync::mpsc::channel` or sets a `tokio::sync::Notify`.
- Watch the `data.mdb` file itself (and its parent for create events); pair with polling `negentropyModificationCounter` from `Meta` to confirm actual event changes vs. checkpoint writes.

## Installation (Cargo.toml)

# LMDB

# zstd

# SQLite derived index

# GraphQL + HTTP

# JSON

# Filesystem watch

# Observability

# Error handling

## Alternatives Considered

| Recommended | Alternative | Why Not |
|-------------|-------------|---------|
| `heed` | `lmdb-rkv` | Dormant since ~2022; no named-root iteration docs; fewer maintainers |
| `heed` | raw `lmdb` 0.8 | Abandoned (2018); no safe wrappers; `lmdb-rkv` is its successor |
| `rusqlite` | `sqlx` (sqlite feature) | sqlx SQLite is async-first but requires more ceremony; spawn_blocking with rusqlite is simpler for a single-writer derived store |
| `async-graphql` | `juniper` | juniper has less momentum, no built-in complexity limiting, weaker axum story |
| `serde_json` (local struct) | `nostr` crate | nostr crate is a full protocol implementation; pulls secp256k1/bech32; we only need JSON deserialization, sig verification is out of scope |
| `notify` 8.2.0 | `notify` 9.x RC | 9.x is a release candidate; stick with stable for production |

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

## Stack Patterns by Variant

- zstd path is inactive but must still be present; detect `0x01` on startup and log a warning if no dictionary loaded
- Use `rusqlite` `bundled` feature (embeds SQLite; avoids OS library mismatch)
- Link heed/lmdb statically: `LMDB_STATIC=1` or ensure `lmdb-sys` builds from source via heed's default Cargo feature
- Do not use `EnvFlags::NO_LOCK` in tests if strfry is also running against the same env; use standard read locking (`EnvFlags::READ_ONLY` only)
- For CI with a fixture DB (no live strfry), `EnvFlags::NO_LOCK` is safe

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| `async-graphql` 7.2.1 | `async-graphql-axum` 7.2.1, `axum` ^0.8.1, `tokio` ^1 | Must keep async-graphql and async-graphql-axum at same minor version |
| `heed` 0.22.1 | `tokio` ^1 (for spawn_blocking bridge) | heed itself is sync; wrap LMDB calls in `spawn_blocking` for async context |
| `rusqlite` 0.40.1 | `bundled` embeds SQLite 3.49.x | `modern_sqlite` feature implied by `bundled`; enables 3.34+ window functions |
| `notify` 8.2.0 | Bridges to tokio via std mpsc → spawn_blocking | Not natively async; bridge required |

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

<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->

## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->

## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->

## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->

## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:

- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->

<!-- GSD:profile-start -->

## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
