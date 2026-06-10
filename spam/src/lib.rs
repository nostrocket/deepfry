/// lmdb2graphql library crate
/// Exposes the lmdb module for integration tests and future GraphQL layer.
pub mod lmdb;

/// Config loader — reads ~/deepfry/lmdb2graphql.yaml.
/// Tests must use tempfile::tempdir() and call config::load_from() (CLAUDE.md).
pub mod config;
