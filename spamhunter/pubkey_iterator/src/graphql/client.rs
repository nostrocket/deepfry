//! The async GraphQL-over-HTTP transport with two-layer error dispatch (D-08, D-11).
//!
//! [`GraphQlClient`] POSTs `{query, variables}` to an *injectable* endpoint and
//! decodes the response through the generic [`GraphQlResponse<T>`] envelope. The
//! dispatch order is load-bearing (contract §7):
//!
//! 1. HTTP transport status first — `503 → Unavailable` (retryable), `413 →
//!    PayloadTooLarge` (non-retryable abort).
//! 2. Then the body's `errors[]` — checked BEFORE `data`, so an in-body error
//!    (e.g. `INVALID_CURSOR`) is surfaced as [`ClientError::Graphql`] and never
//!    silently treated as success (T-02-06 mitigation, criterion 3).
//! 3. Only then is `data` trusted; a `200` with `data:null` and no `errors` is
//!    itself a `Graphql` error ("null data").
//!
//! `authors`/`stats` are thin typed wrappers over the generic `query<T>` — the
//! D-11 additive seam: Phase 3 adds `latest_per_author` as another wrapper +
//! const + struct with no transport change.

use serde::de::DeserializeOwned;
use serde_json::json;

use super::envelope::GraphQlResponse;
use super::queries::{
    AuthorGroup, AuthorsData, AuthorsPage, LatestPerAuthorData, StatsData, StatsResult,
    AUTHORS_QUERY, LATEST_PER_AUTHOR_QUERY, STATS_QUERY,
};

/// The two-layer error taxonomy (contract §7 → typed errors the walk branches on).
///
/// Carries only the HTTP status discriminant + the first error `message` string,
/// never the whole response body (T-02-04: do not log large/full bodies).
#[derive(Debug, thiserror::Error)]
pub enum ClientError {
    /// HTTP `503` — startup gates not yet passed. Caller retries with backoff
    /// without advancing the cursor (D-08).
    #[error("adapter unavailable (HTTP 503)")]
    Unavailable,
    /// HTTP `413` — request body exceeded the adapter's 256 KiB limit. A client
    /// bug for `authors` (tiny body); never retried (D-08).
    #[error("payload too large (HTTP 413)")]
    PayloadTooLarge,
    /// A reqwest transport error (connection refused, timeout, decode failure).
    /// Retryable — the adapter may still be booting (503-equivalent).
    #[error("transport error: {0}")]
    Transport(#[from] reqwest::Error),
    /// An in-body GraphQL error surfaced from `errors[]`. `code` is
    /// `extensions.code` when present (`INVALID_CURSOR` / `TOO_MANY_AUTHORS`),
    /// `None` for internal/validation errors. Surfaced BEFORE `data` is trusted.
    #[error("graphql error{}: {message}", match code { Some(c) => format!(" [{c}]"), None => String::new() })]
    Graphql {
        code: Option<String>,
        message: String,
    },
}

/// A reusable GraphQL-over-HTTP client (D-11).
///
/// The `endpoint` is a struct FIELD, never hardcoded inside any method — so
/// tests point it at a loopback stub and a future config-driven URL (OPS-03 /
/// Phase 4) sets it without touching the transport.
pub struct GraphQlClient {
    http: reqwest::Client,
    endpoint: String,
}

impl GraphQlClient {
    /// Build a client targeting `endpoint` with a default `reqwest::Client`
    /// (connection pooling, keep-alive). No TLS — loopback HTTP (contract §10).
    pub fn new(endpoint: impl Into<String>) -> Self {
        Self {
            http: reqwest::Client::new(),
            endpoint: endpoint.into(),
        }
    }

    /// The endpoint this client targets (operator-supplied; not user input).
    pub fn endpoint(&self) -> &str {
        &self.endpoint
    }

    /// Execute a GraphQL `query` document with `variables`, returning the typed
    /// `data` payload `T`.
    ///
    /// Two-layer dispatch in the order described in the module docs: HTTP status
    /// (503/413) → body `errors[]` (before `data`) → `data`.
    pub async fn query<T: DeserializeOwned>(
        &self,
        query: &str,
        variables: serde_json::Value,
    ) -> Result<T, ClientError> {
        let resp = self
            .http
            .post(&self.endpoint)
            .json(&json!({ "query": query, "variables": variables }))
            .send()
            .await?; // reqwest::Error → ClientError::Transport (retryable)

        // Layer 1: transport status. A non-200/503/413 still falls through to
        // body parsing — GraphQL returns 200 for anything that reaches the
        // resolver, so 503/413 are the only statuses we special-case (contract §7).
        match resp.status().as_u16() {
            503 => return Err(ClientError::Unavailable),
            413 => return Err(ClientError::PayloadTooLarge),
            _ => {}
        }

        // Layer 2: decode the envelope, then check errors BEFORE data.
        let env: GraphQlResponse<T> = resp.json().await?;
        if let Some(errs) = env.errors {
            if let Some(first) = errs.into_iter().next() {
                return Err(ClientError::Graphql {
                    code: first.extensions.and_then(|x| x.code),
                    message: first.message,
                });
            }
        }
        // Layer 3: trust data only after errors is clear. A 200 with null data
        // and no errors is itself an error.
        env.data.ok_or(ClientError::Graphql {
            code: None,
            message: "response carried null data with no errors".into(),
        })
    }

    /// Fetch one page of distinct authors (D-11 thin wrapper over `query`).
    /// `after` is the opaque cursor (`None` for page 1); `limit` is clamped
    /// server-side to `[1, 500]` (contract §6.4).
    pub async fn authors(
        &self,
        after: Option<&str>,
        limit: i64,
    ) -> Result<AuthorsPage, ClientError> {
        self.query::<AuthorsData>(AUTHORS_QUERY, json!({ "after": after, "limit": limit }))
            .await
            .map(|d| d.authors)
    }

    /// Fetch the corpus stats — `maxLevId` is the D-09 drift probe.
    pub async fn stats(&self) -> Result<StatsResult, ClientError> {
        self.query::<StatsData>(STATS_QUERY, json!({}))
            .await
            .map(|d| d.stats)
    }

    /// Fetch the latest `per_author` kind-`kind` events for each of `authors`,
    /// grouped (contract §6.2). A D-11 thin wrapper over the generic `query<T>`,
    /// exactly like `authors()`/`stats()` — no transport change.
    ///
    /// `authors` is passed as the GraphQL VARIABLE `$authors` (never interpolated
    /// into the query document — parameterization analog to SQL `?N`; T-03-02).
    /// An oversized author batch overflows the adapter's 256 KiB request limit and
    /// surfaces as [`ClientError::PayloadTooLarge`] via the existing transport
    /// dispatch — `fetch_batch` (Phase-3 fetch) catches that to shrink-and-retry.
    ///
    /// Returns the top-level `[AuthorGroup!]!` list; authors with zero matches are
    /// OMITTED (contract §5) — callers match back by `author`, never by index.
    pub async fn latest_per_author(
        &self,
        kind: i64,
        per_author: i64,
        authors: &[String],
    ) -> Result<Vec<AuthorGroup>, ClientError> {
        self.query::<LatestPerAuthorData>(
            LATEST_PER_AUTHOR_QUERY,
            json!({ "kind": kind, "perAuthor": per_author, "authors": authors }),
        )
        .await
        .map(|d| d.latest_per_author)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    /// A one-shot loopback HTTP stub: binds an ephemeral port, accepts one
    /// connection, reads the request (drained, not inspected — the client owns
    /// the body shape), and writes back a canned response with `status` and
    /// `json_body`. Returns the `http://127.0.0.1:PORT/graphql` URL the client
    /// is constructed with — proving endpoint injectability with no hardcoded URL.
    ///
    /// Hand-rolled std::net (no wiremock dep) per the fail-fast / owning-phase
    /// dep discipline — a single canned response per test is all we need.
    fn stub_server(status_line: &'static str, json_body: &'static str) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}/graphql");
        thread::spawn(move || {
            let (mut sock, _) = listener.accept().expect("accept one connection");
            // Drain the request headers+body enough to not RST the client. We
            // read once; the client's small POST fits in a single read for tests.
            let mut buf = [0u8; 4096];
            let _ = sock.read(&mut buf);
            let body = json_body.as_bytes();
            let resp = format!(
                "HTTP/1.1 {status_line}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            let _ = sock.write_all(resp.as_bytes());
            let _ = sock.write_all(body);
            let _ = sock.flush();
        });
        url
    }

    /// A current-thread tokio runtime to drive one async client call per test
    /// (avoids requiring the `#[tokio::test]` macro plumbing; the walk is
    /// sequential — one in-flight request).
    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    /// Criterion 3 / T-02-06: a 200 body carrying `errors[]` with
    /// `extensions.code == INVALID_CURSOR` surfaces as `ClientError::Graphql`
    /// with the parsed code — NEVER `Ok`. `data` is null but is never reached.
    #[test]
    fn inbody_errors_surface() {
        let url = stub_server(
            "200 OK",
            r#"{"data":null,"errors":[{"message":"invalid cursor: expected 16 bytes, got 3","extensions":{"code":"INVALID_CURSOR"}}]}"#,
        );
        let client = GraphQlClient::new(url);
        let result = block_on(client.query::<AuthorsData>(AUTHORS_QUERY, json!({})));
        match result {
            Err(ClientError::Graphql { code, message }) => {
                assert_eq!(code.as_deref(), Some("INVALID_CURSOR"));
                assert!(message.contains("invalid cursor"));
            }
            other => panic!("expected Graphql INVALID_CURSOR error, got {other:?}"),
        }
    }

    /// An HTTP 503 maps to the retryable `Unavailable` error (transport layer,
    /// checked before any body parse).
    #[test]
    fn http_503_maps_unavailable() {
        let url = stub_server("503 Service Unavailable", r#"{}"#);
        let client = GraphQlClient::new(url);
        let result = block_on(client.query::<AuthorsData>(AUTHORS_QUERY, json!({})));
        assert!(
            matches!(result, Err(ClientError::Unavailable)),
            "503 must map to Unavailable, got {result:?}"
        );
    }

    /// An HTTP 413 maps to the non-retryable `PayloadTooLarge` error.
    #[test]
    fn http_413_maps_payload_too_large() {
        let url = stub_server("413 Payload Too Large", r#"{}"#);
        let client = GraphQlClient::new(url);
        let result = block_on(client.query::<AuthorsData>(AUTHORS_QUERY, json!({})));
        assert!(
            matches!(result, Err(ClientError::PayloadTooLarge)),
            "413 must map to PayloadTooLarge, got {result:?}"
        );
    }

    /// Happy path: a well-formed `authors` response over the stub deserializes
    /// into an `AuthorsPage` with the expected fields — exercised through the
    /// `authors()` typed wrapper (proves the D-11 wrapper unwraps `data.authors`).
    #[test]
    fn authors_happy_path() {
        let url = stub_server(
            "200 OK",
            r#"{"data":{"authors":{"authors":["aa00000000000000000000000000000000000000000000000000000000000001","bb00000000000000000000000000000000000000000000000000000000000002"],"hasMore":true,"endCursor":"bb00000000000000000000000000000000000000000000000000000000000002"}}}"#,
        );
        let client = GraphQlClient::new(url);
        let page = block_on(client.authors(None, 500)).expect("authors page");
        assert_eq!(page.authors.len(), 2);
        assert!(page.has_more);
        assert_eq!(
            page.end_cursor.as_deref(),
            Some("bb00000000000000000000000000000000000000000000000000000000000002")
        );
    }

    /// The `stats` typed wrapper unwraps `data.stats.maxLevId` (D-09 drift probe).
    #[test]
    fn stats_happy_path() {
        let url = stub_server("200 OK", r#"{"data":{"stats":{"maxLevId":98765}}}"#);
        let client = GraphQlClient::new(url);
        let stats = block_on(client.stats()).expect("stats");
        assert_eq!(stats.max_lev_id, 98765);
    }

    /// Happy path: a well-formed `latestPerAuthor` response over the stub
    /// deserializes — through the `latest_per_author()` wrapper — into a
    /// `Vec<AuthorGroup>` with `author` + `events` populated (proves the D-11
    /// wrapper unwraps the top-level LIST `data.latestPerAuthor`, contract §6.2).
    #[test]
    fn latest_per_author_happy_path() {
        let url = stub_server(
            "200 OK",
            r#"{"data":{"latestPerAuthor":[{"author":"aa00000000000000000000000000000000000000000000000000000000000001","events":[{"id":"e1","pubkey":"aa00000000000000000000000000000000000000000000000000000000000001","kind":1,"createdAt":1700000000,"content":"hi","tags":[["t","x"]]}]}]}}"#,
        );
        let client = GraphQlClient::new(url);
        let pk = "aa00000000000000000000000000000000000000000000000000000000000001".to_string();
        let groups =
            block_on(client.latest_per_author(1, 5, std::slice::from_ref(&pk))).expect("groups");
        assert_eq!(groups.len(), 1);
        assert_eq!(groups[0].author, pk);
        assert_eq!(groups[0].events.len(), 1);
        assert_eq!(groups[0].events[0].id, "e1");
        assert_eq!(groups[0].events[0].created_at, 1_700_000_000i64);
    }

    /// An oversized author batch trips the adapter's 256 KiB limit → HTTP 413,
    /// which surfaces through `latest_per_author` as `ClientError::PayloadTooLarge`
    /// via the EXISTING transport dispatch (no new handling in the wrapper). This
    /// is the error `fetch_batch` (Task 2) catches to shrink-and-retry (D-02).
    #[test]
    fn latest_per_author_413_surfaces_payload_too_large() {
        let url = stub_server("413 Payload Too Large", r#"{}"#);
        let client = GraphQlClient::new(url);
        let pk = "aa00000000000000000000000000000000000000000000000000000000000001".to_string();
        let result = block_on(client.latest_per_author(1, 5, &[pk]));
        assert!(
            matches!(result, Err(ClientError::PayloadTooLarge)),
            "413 must map to PayloadTooLarge through latest_per_author, got {result:?}"
        );
    }

    /// A 200 with null data and no errors is itself a `Graphql` error — never a
    /// silent `Ok` on absent data.
    #[test]
    fn null_data_no_errors_is_error() {
        let url = stub_server("200 OK", r#"{"data":null}"#);
        let client = GraphQlClient::new(url);
        let result = block_on(client.query::<AuthorsData>(AUTHORS_QUERY, json!({})));
        assert!(
            matches!(result, Err(ClientError::Graphql { code: None, .. })),
            "null data with no errors must be a Graphql error, got {result:?}"
        );
    }
}
