/// schema.rs — GraphQL schema factory for lmdb2graphql (API-06, D-10).
///
/// Defines `AppState` (shared resolver state), the `AppSchema` type alias, and
/// `build_schema` — the factory that wires Query + EmptyMutation + EmptySubscription
/// into a query-only schema with no mutation surface (D-10/API-06).
///
/// Design decisions implemented here:
///   D-10: Query-only schema — EmptyMutation/EmptySubscription structurally prevent writes.
///   Pattern 1 (RESEARCH.md): Schema::build + .data(app_state) + .extension(Tracing).
///   Pattern 2 (RESEARCH.md): AppState carries heed::Env (Clone), Arc<DictCache>, MetaRecord.
use std::sync::Arc;

use async_graphql::{
    extensions::Tracing, EmptyMutation, EmptySubscription, Schema,
};

use crate::lmdb::payload::DictCache;
use crate::lmdb::types::MetaRecord;

use super::resolvers::Query;

/// Shared resolver state — injected once at schema build, retrieved per-resolver via context.
///
/// ## Ownership
///
/// - `env: heed::Env` — `Clone` is cheap (internal refcount); no Arc wrapper needed.
/// - `dict_cache: Arc<DictCache>` — `DictCache` contains `RwLock` and is not `Clone`;
///   must be Arc-wrapped to share across `spawn_blocking` closures (Pitfall 1 / Pattern 2).
/// - `meta: MetaRecord` — plain clone; carries `db_version` for the `stats` resolver (D-09).
#[derive(Clone)]
pub struct AppState {
    /// The opened strfry LMDB environment (read-only). Cheap to clone (internal refcount).
    pub env: heed::Env,

    /// Cached zstd dictionaries shared across all resolvers and queries.
    /// `DictCache` is not `Clone` (contains `RwLock`) — must be `Arc`-wrapped.
    pub dict_cache: Arc<DictCache>,

    /// Parsed Meta record from LMDB — carries `db_version` for the `stats` resolver (D-09).
    /// The startup gate has already verified `db_version == 3` before this is constructed.
    pub meta: MetaRecord,
}

/// Query-only schema type alias (API-06, D-10).
///
/// `EmptyMutation` and `EmptySubscription` make this schema structurally incapable of
/// mutations — no `mutation` type appears in the SDL (D-10/API-06).
pub type AppSchema = Schema<Query, EmptyMutation, EmptySubscription>;

/// Build the query-only GraphQL schema with shared `AppState` (Pattern 1 / RESEARCH.md).
///
/// The schema:
/// - registers `AppState` once via `.data(app_state)` — available per-resolver via
///   `ctx.data_unchecked::<AppState>()` (Pattern 3).
/// - enables the `Tracing` extension — free structured tracing of resolver execution
///   (requires the `tracing` feature on `async-graphql`).
/// - uses `EmptyMutation` + `EmptySubscription` — no mutation root in the SDL (D-10/API-06).
/// - does NOT add `.limit_depth()` or `.limit_complexity()` — the limit ceiling (500) plus
///   the engine's `MAX_ROUNDS` bound is the v1 DoS guard (D-06/T-04-DOS).
///
/// Called once from `main.rs` after all startup gates pass.
pub fn build_schema(app_state: AppState) -> AppSchema {
    Schema::build(Query, EmptyMutation, EmptySubscription)
        .data(app_state)
        .extension(Tracing)
        .finish()
}
