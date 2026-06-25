//! `pubkey_iterator` — the persistence foundation of the spam-classifier engine.
//!
//! Phase 1 builds the dependency root: a single SQLite store (WAL mode, single
//! writer, batched idempotent UPSERTs) that every later phase persists through.

/// Row-mapped model structs (`Run`, `Score`, `SubScore`, `Fingerprint`, `Label`,
/// `Weight`) plus the `Persist` writer-channel payload.
pub mod model;

/// SQLite store: schema DDL, PRAGMA-first `open`, the single-writer actor, and
/// the prepared-statement read helpers.
///
/// Tests use a temp FILE (`tempfile::TempDir`), never an in-memory `:memory:` DB,
/// because WAL sidecar (`-wal`/`-shm`) behavior differs in-memory.
pub mod store;
