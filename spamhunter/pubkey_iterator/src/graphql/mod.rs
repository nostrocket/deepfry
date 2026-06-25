//! The reusable hand-written GraphQL-over-HTTP client (D-10, D-11).
//!
//! This is the crate's transport seam: a `GraphQlClient` with an injectable
//! endpoint and a generic `query<T>` that surfaces in-body `errors[]` before
//! trusting `data` (D-08), plus the `authors`/`stats` typed wrappers. Phase 3
//! extends the *same* transport additively (a new query const + struct + wrapper
//! for `latestPerAuthor`) — no transport rewrite.
//!
//! Layout mirrors `crate::store`'s submodule + selective re-export idiom:
//! `envelope` (the `{data, errors}` shape), `queries` (the query documents +
//! response structs), `client` (the transport + two-layer error dispatch).

mod envelope;
pub mod queries;

pub use envelope::{Extensions, GraphQlError, GraphQlResponse};
pub use queries::{AuthorsPage, StatsResult, AUTHORS_QUERY, STATS_QUERY};
