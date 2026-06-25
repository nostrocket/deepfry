//! `pubkey_iterator` — the persistence foundation of the spam-classifier engine.
//!
//! Phase 1 builds the dependency root: a single SQLite store (WAL mode, single
//! writer, batched idempotent UPSERTs) that every later phase persists through.

/// Row-mapped model structs (`Run`, `Score`, `SubScore`, `Fingerprint`, `Label`,
/// `Weight`) plus the `Persist` writer-channel payload.
pub mod model;

/// TOML configuration (OPS-03 / D-09): the path-argument `config::load` parses
/// `pubkey_iterator_config.toml` into typed serde structs (adapter/whitelist
/// URLs, combiner τ + bias, and the four per-layer enable/weight/threshold
/// entries). Config-loading tests use a temp dir, never the real `~/deepfry`
/// file. The committed `pubkey_iterator_config.example.toml` documents the shape.
pub mod config;

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

/// The `latestPerAuthor` batched fetch policy (`fetch::fetch_batch` +
/// `fetch::match_groups`): match response groups back to the requested authors
/// by `author` (D-04, the INGEST-04 landmine defuser) and recursively shrink a
/// batch on a `413` (D-02), reusing `enumerate::retry` for `503`/transient
/// backoff. This is the fetch path the Plan-02 bounded-channel pipeline consumes.
pub mod fetch;

/// The bounded-memory streaming pipeline (`pipeline::run_pipeline` +
/// `pipeline::consume_noop`): the async↔sync concurrency heart of Phase 3
/// (INGEST-03 / D-05). A tokio fetcher chunks the enumerated pubkeys and calls
/// the Plan-01 `fetch_batch`, pushing `AuthorGroup`s into a **bounded** flume
/// channel; a `std::thread`/rayon drain (CPU stage, OFF the tokio runtime)
/// applies an injected consumer closure — Phase 3's no-op counter (D-06), the
/// Phase-4 Layer/combiner seam. The bounded channel is the back-pressure point,
/// so peak in-flight memory is capped by channel capacity, never the corpus size.
pub mod pipeline;

/// The detection-layer integration seam (Phase 4): the shared `Layer` trait, the
/// fixed-order `ScoringStage` registry + logistic `sigmoid(Σwᵢxᵢ+b)` combiner
/// (SCORE-01), the `weight`-table seed/read helpers (seeded from config on first
/// run, SCORE-04), and the `ScoredInput { group, whitelisted }` channel carrier
/// (D-15). One trivial layer proves the slice end-to-end in Plan 01; the four
/// real layers (L0/L1/L3/L4) plug into this same registry in Plans 02–03.
/// Deterministic: positional `Vec` sum, zero RNG (OPS-02).
pub mod detect;
