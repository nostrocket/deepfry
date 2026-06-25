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

/// `latestPerAuthor(kind, perAuthor, authors)` — top-N kind-`kind` events per
/// author, grouped (contract §6.2). The author list is supplied as the GraphQL
/// VARIABLE `$authors` (never string-interpolated into the document — the
/// parameterization analog to SQL `?N`; T-03-02 Tampering mitigation).
///
/// The selection set deliberately EXCLUDES `raw` and `sig` (contract §9 best
/// practice #6; RESEARCH anti-pattern): both are large and unneeded by the
/// pipeline, so omitting them keeps the response — and the 256 KiB request/
/// response budget (T-03-03) — lean. Per D-03 / contract §6.2 it selects
/// `author events{ id pubkey kind createdAt content tags }`.
pub const LATEST_PER_AUTHOR_QUERY: &str = "query($kind:Int!,$perAuthor:Int!,$authors:[String!]!){ latestPerAuthor(kind:$kind,perAuthor:$perAuthor,authors:$authors){ author events{ id pubkey kind createdAt content tags } } }";

/// A single Nostr event as returned by `latestPerAuthor` (contract §5 `Event`,
/// minus `raw`/`sig` which the selection set omits).
///
/// `kind` and `created_at` are decoded as `i64` per contract §8 (the adapter maps
/// them to a 64-bit scalar, not GraphQL's 32-bit `Int`) — an `i32` here would
/// silently truncate adapter-claimed values (T-03-01 Tampering mitigation).
/// `content`/`tags` are untrusted text but this phase does not interpret them
/// (Phase 4 owns content validation).
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Event {
    /// 32-byte event id as 64-char lowercase hex.
    pub id: String,
    /// 32-byte author pubkey as 64-char lowercase hex.
    pub pubkey: String,
    /// Nostr event kind. 64-bit (contract §8) — `i64`, never `i32`.
    pub kind: i64,
    /// Author-claimed Unix timestamp in seconds. 64-bit (contract §8) — `i64`.
    pub created_at: i64,
    /// Raw event content; interpretation depends on `kind` (uninterpreted here).
    pub content: String,
    /// Nested tag array: `tags[i][0]` is the tag name, `tags[i][1..]` the values.
    pub tags: Vec<Vec<String>>,
}

/// One author's matching events (contract §5 `AuthorGroup`). Authors with zero
/// matches are OMITTED from the top-level list — never an empty group — so this
/// is matched back to the requested list by `author`, never zipped by index
/// (contract §5/§8; the INGEST-04 landmine `match_groups` defuses).
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AuthorGroup {
    /// The author pubkey (64-char lowercase hex) this group's events belong to.
    pub author: String,
    /// That author's matching events, newest-first, capped at `perAuthor`.
    pub events: Vec<Event>,
}

/// The `data` object for the `latestPerAuthor` query. UNLIKE `authors`/`stats`
/// (single objects), the top-level field is a LIST `[AuthorGroup!]!` (contract
/// §4/§6.2), so the wrapper field is a `Vec` — `data.latestPerAuthor` is the
/// `Vec<AuthorGroup>`.
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LatestPerAuthorData {
    pub latest_per_author: Vec<AuthorGroup>,
}

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

    /// A real-shaped `latestPerAuthor` response (contract §6.2) deserializes into
    /// `GraphQlResponse<LatestPerAuthorData>`: the top-level field is a LIST
    /// (`Vec<AuthorGroup>`), `createdAt` maps to `created_at`, and `kind`/
    /// `created_at` land as `i64` (no i32 truncation, T-03-01).
    #[test]
    fn latest_per_author_response_deserializes() {
        let body = r#"{"data":{"latestPerAuthor":[{"author":"aa00000000000000000000000000000000000000000000000000000000000001","events":[{"id":"e1","pubkey":"aa00000000000000000000000000000000000000000000000000000000000001","kind":1,"createdAt":1700000000,"content":"hi","tags":[["t","x"]]}]}]}}"#;
        let env: GraphQlResponse<LatestPerAuthorData> =
            serde_json::from_str(body).expect("parse latestPerAuthor envelope");
        let groups = env.data.expect("data present").latest_per_author;
        assert_eq!(groups.len(), 1);
        let g = &groups[0];
        assert_eq!(
            g.author,
            "aa00000000000000000000000000000000000000000000000000000000000001"
        );
        assert_eq!(g.events.len(), 1);
        let ev = &g.events[0];
        assert_eq!(ev.id, "e1");
        assert_eq!(ev.kind, 1i64, "kind decodes as i64");
        assert_eq!(ev.created_at, 1_700_000_000i64, "camelCase createdAt → i64");
        assert_eq!(ev.content, "hi");
        assert_eq!(ev.tags, vec![vec!["t".to_string(), "x".to_string()]]);
        assert!(env.errors.is_none(), "no errors on a clean 200");
    }

    /// A `created_at` beyond i32 range round-trips without truncation (T-03-01):
    /// proves the 64-bit decode is load-bearing, not incidental.
    #[test]
    fn latest_per_author_created_at_exceeds_i32() {
        // 2^33 — well beyond i32::MAX, a value i32 would silently corrupt.
        let body = r#"{"data":{"latestPerAuthor":[{"author":"aa","events":[{"id":"e","pubkey":"aa","kind":40000,"createdAt":8589934592,"content":"","tags":[]}]}]}}"#;
        let env: GraphQlResponse<LatestPerAuthorData> =
            serde_json::from_str(body).expect("parse large createdAt");
        let ev = &env.data.unwrap().latest_per_author[0].events[0];
        assert_eq!(ev.created_at, 8_589_934_592i64);
        assert_eq!(ev.kind, 40_000i64);
    }
}
