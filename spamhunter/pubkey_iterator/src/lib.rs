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

/// Hand-written GraphQL-over-HTTP client (`GraphQlClient`): the injectable-endpoint
/// transport, the `{data, errors}` envelope, the `authors`/`stats` query documents
/// + response structs, and the two-layer `ClientError` taxonomy (D-10/D-11).
pub mod graphql;

/// The `authors` opaque-cursor enumeration walk (`enumerate::run`): the bounded
/// retry + abort + drift-probe + flush-before-cursor pagination loop that
/// composes the store (plan 01) and the GraphQL client (plan 02) into the
/// connectivity-proving vertical slice (INGEST-01 / INGEST-04).
pub mod enumerate;
