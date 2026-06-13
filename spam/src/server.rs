/// server.rs — axum HTTP router for the lmdb2graphql GraphQL API.
///
/// Provides:
///   - `build_router(schema)` — mounts `POST /graphql` (GraphQL service) and
///     `GET /graphql` (GraphiQL playground) on the same path (Pattern 5 / Pitfall 4).
///
/// ## Routing
///
/// ```text
///   POST /graphql  →  async-graphql Tower Service (GraphQL::new(schema))
///   GET  /graphql  →  graphiql handler → Html(GraphiQLSource)
/// ```
///
/// ## Pitfall 4 (RESEARCH.md)
///
/// `post_service` is a method on `MethodRouter`, NOT a free function in `axum::routing`.
/// Pattern: `get(handler).post_service(svc)` — chains on `MethodRouter` after `get(...)`.
/// Using `Router::new().route("/graphql", post_service(...))` would fail to compile in axum 0.8.
///
/// ## Read-only surface
///
/// The router has no write routes. No Mutation type in the schema (T-04-WRITE/API-06).
/// GraphiQL and introspection are enabled — this is a read-only adapter; no credentials are at risk.
use async_graphql::http::GraphiQLSource;
use async_graphql_axum::GraphQL;
use axum::{
    response::{Html, IntoResponse},
    routing::get,
    Router,
};

use crate::graphql::schema::AppSchema;

/// Build the axum router for the GraphQL API (Pattern 5 / RESEARCH.md).
///
/// Mounts two handlers on `/graphql`:
/// - `GET  /graphql` → GraphiQL playground (HTML, for browser use)
/// - `POST /graphql` → GraphQL Tower Service (for programmatic clients)
///
/// `post_service` is called on the `MethodRouter` returned by `get(graphiql)` (Pitfall 4 —
/// NOT a free `axum::routing::post_service` function).
pub fn build_router(schema: AppSchema) -> Router {
    Router::new().route(
        "/graphql",
        get(graphiql).post_service(GraphQL::new(schema)),
    )
}

/// GraphiQL playground handler — returns the GraphiQL v2 HTML page (GET /graphql).
///
/// GraphiQL and schema introspection are enabled for this read-only adapter.
/// No credentials or sensitive state are exposed (the adapter only reads strfry LMDB).
async fn graphiql() -> impl IntoResponse {
    Html(GraphiQLSource::build().endpoint("/graphql").finish())
}
