//! Hand-written GraphQL query documents + their serde response structs (D-10).
//!
//! Mirrors `crate::store::writer`'s "SQL in named consts" convention, applied to
//! GraphQL: `AUTHORS_QUERY` / `STATS_QUERY` are the literal documents, and the
//! `*Page` / `*Data` / `*Result` structs deserialize the matching response.
//!
//! Field names are verified against contract §4/§6.4: async-graphql renames the
//! Rust `snake_case` to `camelCase` on the wire (`hasMore`, `endCursor`,
//! `maxLevId`), so the structs carry `#[serde(rename_all = "camelCase")]`.
//!
//! GraphQL nests a query's result under its field name, so the `data` object is
//! `{ "authors": { authors, hasMore, endCursor } }` — hence the two-layer
//! `AuthorsData { authors: AuthorsPage }` (deserialize `T = AuthorsData`, then
//! `.authors`). Same for `StatsData { stats: StatsResult }`.

use serde::Deserialize;

/// `authors(after, limit)` — paginated distinct-pubkey enumeration (contract §6.4).
/// Selects only the fields the walk needs: the pubkey list + pagination signals.
pub const AUTHORS_QUERY: &str =
    "query($after:String,$limit:Int){ authors(after:$after,limit:$limit){ authors hasMore endCursor } }";

/// `stats { maxLevId }` — the cheap corpus high-water-mark probe (contract §6.3).
/// Only `maxLevId` is selected; it is the D-09 drift probe the walk records.
pub const STATS_QUERY: &str = "query{ stats{ maxLevId } }";

/// One page of the `authors` enumeration (contract §5 `AuthorsPage`).
///
/// `end_cursor` is `None` at the end of the keyspace; `has_more` is the
/// equivalent termination signal. Both `endCursor`/`hasMore` map via camelCase.
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AuthorsPage {
    /// Distinct author pubkeys (64-char lowercase hex), byte-ascending.
    pub authors: Vec<String>,
    pub has_more: bool,
    /// Opaque cursor (the last pubkey hex); `None` ⇒ no next page.
    pub end_cursor: Option<String>,
}

/// The `data` object for the `authors` query — wraps the top-level field name
/// so `data.authors` is the [`AuthorsPage`].
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AuthorsData {
    pub authors: AuthorsPage,
}

/// The subset of `StatsResult` the walk reads (contract §6.3): the monotonic
/// insert counter used as the D-09 drift probe.
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct StatsResult {
    pub max_lev_id: i64,
}

/// The `data` object for the `stats` query — wraps the top-level field name so
/// `data.stats` is the [`StatsResult`].
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct StatsData {
    pub stats: StatsResult,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::graphql::envelope::GraphQlResponse;

    /// A real-shaped v1.2 `authors` response (contract §6.4) deserializes into
    /// `GraphQlResponse<AuthorsData>`, with camelCase `hasMore`/`endCursor`
    /// mapping to the snake_case struct fields and the double-nested
    /// `data.authors.authors` list landing correctly.
    #[test]
    fn authors_response_deserializes() {
        let body = r#"{"data":{"authors":{"authors":["aa..","bb.."],"hasMore":true,"endCursor":"bb.."}}}"#;
        let env: GraphQlResponse<AuthorsData> =
            serde_json::from_str(body).expect("parse authors envelope");
        let page = &env.data.expect("data present").authors;
        assert_eq!(page.authors.len(), 2);
        assert!(page.has_more);
        assert_eq!(page.end_cursor.as_deref(), Some("bb.."));
        assert!(env.errors.is_none(), "no errors on a clean 200");
    }

    /// A `stats` response (contract §6.3) deserializes into
    /// `GraphQlResponse<StatsData>` with `maxLevId` mapping to `max_lev_id`.
    #[test]
    fn stats_response_deserializes() {
        let body = r#"{"data":{"stats":{"maxLevId":12345}}}"#;
        let env: GraphQlResponse<StatsData> =
            serde_json::from_str(body).expect("parse stats envelope");
        assert_eq!(env.data.expect("data present").stats.max_lev_id, 12345);
    }

    /// The end of the keyspace: `endCursor` null + `hasMore` false parse cleanly
    /// (the canonical termination signal, contract §6.4).
    #[test]
    fn authors_terminal_page_deserializes() {
        let body = r#"{"data":{"authors":{"authors":["zz.."],"hasMore":false,"endCursor":null}}}"#;
        let env: GraphQlResponse<AuthorsData> =
            serde_json::from_str(body).expect("parse terminal page");
        let page = env.data.expect("data present").authors;
        assert!(!page.has_more);
        assert_eq!(page.end_cursor, None);
    }
}
