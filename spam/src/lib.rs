/// lmdb2graphql library crate
/// Exposes the lmdb module for integration tests and future GraphQL layer.
pub mod lmdb;

/// Config loader — reads ~/deepfry/lmdb2graphql.yaml.
/// Tests must use tempfile::tempdir() and call config::load_from() (CLAUDE.md).
pub mod config;

/// Query engine — filter/cursor/error contract types (Phase 3).
/// Exposes the engine-facing types the GraphQL layer (Phase 4) calls.
pub mod query;

/// GraphQL API layer — schema types, resolvers, server wiring (Phase 4).
///
/// Plan 04-01: types (output + input types, DecodedEvent → Event mapping).
/// Plan 04-02: schema (AppSchema, build_schema), resolvers (Query root), server wiring.
pub mod graphql;

/// HTTP server — axum router mounting POST/GET /graphql (Phase 4 Plan 04-02).
/// `build_router(schema)` returns an axum Router ready for `axum::serve`.
pub mod server;
