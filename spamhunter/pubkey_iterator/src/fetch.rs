//! The `latestPerAuthor` batched fetch policy feeding the Plan-02 pipeline.
//!
//! Two units sit on top of the Phase-2 GraphQL client:
//!
//! - [`match_groups`] — the INGEST-04 landmine defuser (D-04). The adapter OMITS
//!   authors with zero matching events from the `[AuthorGroup]` list (contract
//!   §5/§8), so the response is NOT positionally aligned with the requested list.
//!   Matching back by `author` (not by index) guarantees an omitted middle author
//!   never shifts a trailing author's events onto the wrong pubkey.
//!
//! - [`fetch_batch`] — the 413 shrink-and-retry policy (D-02). The normal path
//!   routes through `crate::enumerate::retry`, so transient `503`/transport/
//!   codeless-GraphQL errors get the SAME bounded backoff as the enumeration
//!   walk. A `413 PayloadTooLarge` is classified NON-retryable by
//!   `enumerate::is_retryable`, so it surfaces immediately rather than burning
//!   retries — `fetch_batch` then recursively halves the batch and retries each
//!   half, losing no authors (contract §7/§12). This recursion is the safety net
//!   DISTINCT from the 503 retry policy.
//!
//! `fetch_batch` is the production implementation over a real `&GraphQlClient`.
//! Plan 02's pipeline owns the injection seam: its watermark/bounded-memory test
//! substitutes a mock fetch closure at the pipeline boundary, while production
//! wiring calls `fetch_batch`.

use std::collections::HashMap;

use crate::graphql::queries::{AuthorGroup, Event};
use crate::graphql::{ClientError, GraphQlClient};

/// Attribute each response group back to the REQUESTED author list by `author`
/// (D-04 / contract §5/§8) — never by index.
///
/// Returns one entry per requested pubkey in request order: `(pubkey, events)`,
/// with an EMPTY `Vec` for any author the adapter omitted (zero matches). An
/// omitted middle author therefore yields an empty slot rather than shifting the
/// trailing authors' events — the INGEST-04 landmine, provably closed.
///
/// Authors appearing in `groups` but not in `requested` are dropped (the adapter
/// only returns what we asked for, but matching by key makes that safe regardless).
pub fn match_groups(requested: &[String], groups: Vec<AuthorGroup>) -> Vec<(&str, Vec<Event>)> {
    let mut by_author: HashMap<String, Vec<Event>> =
        groups.into_iter().map(|g| (g.author, g.events)).collect();
    requested
        .iter()
        .map(|pk| (pk.as_str(), by_author.remove(pk).unwrap_or_default()))
        .collect()
}

/// Fetch the latest `per_author` kind-`kind` events for `batch`, transparently
/// shrinking on a `413` (D-02).
///
/// - Normal path: `crate::enumerate::retry(|| client.latest_per_author(...))` —
///   so `503`/transport/codeless-GraphQL get the existing bounded exponential
///   backoff (D-06/D-08), identical to the enumeration walk.
/// - `413 PayloadTooLarge` with `batch.len() > 1`: split at the midpoint,
///   recursively fetch both halves, and return their UNION. `is_retryable`
///   classifies `413` as non-retryable, so it surfaces from `retry` immediately
///   (no wasted retries) and lands here — the shrink-and-retry safety net
///   (contract §7).
/// - `413` with `batch.len() <= 1`: a single author already overflows the 256 KiB
///   limit — irrecoverable by splitting, so the error surfaces.
/// - Any other error: surfaced unchanged (the caller's policy owns it).
///
/// Recursion uses `Box::pin` because an `async fn` that calls itself produces an
/// infinitely-sized future otherwise.
pub async fn fetch_batch(
    client: &GraphQlClient,
    kind: i64,
    per_author: i64,
    batch: &[String],
) -> Result<Vec<AuthorGroup>, ClientError> {
    match crate::enumerate::retry(|| client.latest_per_author(kind, per_author, batch)).await {
        Ok(groups) => Ok(groups),
        // 413 on a splittable batch: halve and retry each half (D-02). Distinct
        // from the 503 retry policy — 413 is non-retryable, so retry() already
        // surfaced it here without sleeping.
        Err(ClientError::PayloadTooLarge) if batch.len() > 1 => {
            let mid = batch.len() / 2;
            let (left_keys, right_keys) = batch.split_at(mid);
            let mut left = Box::pin(fetch_batch(client, kind, per_author, left_keys)).await?;
            let right = Box::pin(fetch_batch(client, kind, per_author, right_keys)).await?;
            left.extend(right);
            Ok(left)
        }
        // A single author that still 413s cannot be split further; or any other
        // error — surface it.
        Err(e) => Err(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::graphql::queries::{AuthorGroup, Event};
    use crate::graphql::GraphQlClient;
    use crate::store::Store;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;
    use tempfile::TempDir;

    const PK1: &str = "aa00000000000000000000000000000000000000000000000000000000000001";
    const PK2: &str = "aa00000000000000000000000000000000000000000000000000000000000002";
    const PK3: &str = "aa00000000000000000000000000000000000000000000000000000000000003";
    const PK4: &str = "aa00000000000000000000000000000000000000000000000000000000000004";

    /// A current-thread tokio runtime to drive one async call per test.
    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    /// Build a synthetic event whose `id` encodes its author, so a test can prove
    /// which author's events landed where.
    fn ev(author: &str, id: &str) -> Event {
        Event {
            id: id.to_string(),
            pubkey: author.to_string(),
            kind: 1,
            created_at: 1_700_000_000,
            content: String::new(),
            tags: vec![],
        }
    }

    fn group(author: &str, ids: &[&str]) -> AuthorGroup {
        AuthorGroup {
            author: author.to_string(),
            events: ids.iter().map(|i| ev(author, i)).collect(),
        }
    }

    /// INGEST-04 / D-04: requested = [PK1, PK2, PK3] but the response OMITS PK2
    /// (groups for PK1 and PK3 only). `match_groups` must attribute PK1→its
    /// events, PK2→EMPTY, PK3→its events — never PK3's events to PK2 (no shift).
    #[test]
    fn match_groups_no_shift() {
        let requested = vec![PK1.to_string(), PK2.to_string(), PK3.to_string()];
        // Response omits PK2 entirely (zero matches), PK1 and PK3 present.
        let groups = vec![group(PK1, &["pk1-e1"]), group(PK3, &["pk3-e1", "pk3-e2"])];

        let matched = match_groups(&requested, groups);

        assert_eq!(matched.len(), 3, "one entry per requested author");
        assert_eq!(matched[0].0, PK1);
        assert_eq!(
            matched[0]
                .1
                .iter()
                .map(|e| e.id.as_str())
                .collect::<Vec<_>>(),
            vec!["pk1-e1"]
        );
        assert_eq!(matched[1].0, PK2);
        assert!(
            matched[1].1.is_empty(),
            "omitted PK2 yields an EMPTY vec, not PK3's events"
        );
        assert_eq!(matched[2].0, PK3);
        assert_eq!(
            matched[2]
                .1
                .iter()
                .map(|e| e.id.as_str())
                .collect::<Vec<_>>(),
            vec!["pk3-e1", "pk3-e2"],
            "PK3's events stay attributed to PK3 — no positional shift"
        );
    }

    /// D-02: a mock fetcher that returns `PayloadTooLarge` for any batch larger
    /// than `K` and `Ok(groups)` otherwise. `fetch_batch` over an over-large batch
    /// must recursively halve and return the UNION of all sub-batch groups with no
    /// author lost. Uses a loopback stub keyed on REQUEST SIZE: the stub inspects
    /// the JSON body's `authors` array length and returns 413 if it exceeds K, else
    /// one group per requested author.
    #[test]
    fn fetch_413_split() {
        const K: usize = 1; // batches > 1 author 413; singletons succeed.
        let batch = vec![
            PK1.to_string(),
            PK2.to_string(),
            PK3.to_string(),
            PK4.to_string(),
        ];

        let url = size_gated_stub(K);
        let client = GraphQlClient::new(url);
        let groups = block_on(fetch_batch(&client, 1, 5, &batch)).expect("split succeeds");

        // Every author must appear exactly once in the union.
        let mut got: Vec<String> = groups.into_iter().map(|g| g.author).collect();
        got.sort();
        let mut want = batch.clone();
        want.sort();
        assert_eq!(
            got, want,
            "recursive split returns the union with no author lost"
        );
    }

    /// Happy path: a single batch within the limit returns its groups directly in
    /// one round trip (the retry helper passes through; no split).
    #[test]
    fn fetch_batch_happy() {
        let batch = vec![PK1.to_string(), PK2.to_string()];
        let url = size_gated_stub(1000); // never 413s for our tiny batch
        let client = GraphQlClient::new(url);
        let groups = block_on(fetch_batch(&client, 1, 5, &batch)).expect("happy fetch");
        let mut got: Vec<String> = groups.into_iter().map(|g| g.author).collect();
        got.sort();
        assert_eq!(got, batch, "single round trip returns both authors' groups");
    }

    /// read_pubkeys round-trips every inserted pubkey in ORDER BY pubkey order.
    #[test]
    fn read_pubkeys_roundtrip() {
        let (dir, path) = temp_store();
        let store = Store::open(&path).expect("open store");
        // Insert out of sorted order to prove ORDER BY is doing the sorting.
        store.insert_pubkeys(vec![PK3.into(), PK1.into(), PK4.into(), PK2.into()]);
        store.close().expect("flush + join");

        let conn = rusqlite::Connection::open(&path).expect("reader");
        let pks = crate::store::queries::read_pubkeys(&conn).expect("read_pubkeys");
        assert_eq!(
            pks,
            vec![PK1, PK2, PK3, PK4],
            "all pubkeys in ORDER BY pubkey order"
        );
        drop(dir);
    }

    // ---- test helpers ----

    fn temp_store() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// A loopback HTTP stub that parses each request body's `variables.authors`
    /// array and returns `413` when its length exceeds `max_authors`, otherwise a
    /// `latestPerAuthor` response with one group per requested author. Runs in a
    /// loop (one connection per `latest_per_author` call — the recursive split
    /// issues several). Hand-rolled std::net (no wiremock dep), mirroring the
    /// client.rs / enumerate.rs stub idiom.
    fn size_gated_stub(max_authors: usize) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}/graphql");
        thread::spawn(move || {
            for conn in listener.incoming() {
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                // Read the full request (headers + body). The client sends
                // Content-Length; for our small test bodies a single read suffices.
                let mut buf = vec![0u8; 16384];
                let n = sock.read(&mut buf).unwrap_or(0);
                let req = String::from_utf8_lossy(&buf[..n]);
                let body = req.split("\r\n\r\n").nth(1).unwrap_or("");
                let authors = parse_authors(body);

                let (status_line, resp_body) = if authors.len() > max_authors {
                    ("413 Payload Too Large", "{}".to_string())
                } else {
                    let groups = authors
                        .iter()
                        .map(|a| {
                            format!(
                                r#"{{"author":"{a}","events":[{{"id":"{a}-e","pubkey":"{a}","kind":1,"createdAt":1700000000,"content":"","tags":[]}}]}}"#
                            )
                        })
                        .collect::<Vec<_>>()
                        .join(",");
                    (
                        "200 OK",
                        format!(r#"{{"data":{{"latestPerAuthor":[{groups}]}}}}"#),
                    )
                };
                let bytes = resp_body.as_bytes();
                let head = format!(
                    "HTTP/1.1 {status_line}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    bytes.len()
                );
                let _ = sock.write_all(head.as_bytes());
                let _ = sock.write_all(bytes);
                let _ = sock.flush();
            }
        });
        url
    }

    /// Extract the `variables.authors` hex strings from a GraphQL request body.
    /// A deliberately minimal parser: finds `"authors":[ ... ]` and pulls the
    /// quoted 64-hex tokens out — enough to count the batch size for the stub.
    fn parse_authors(body: &str) -> Vec<String> {
        let v: serde_json::Value = match serde_json::from_str(body) {
            Ok(v) => v,
            Err(_) => return vec![],
        };
        v.get("variables")
            .and_then(|vars| vars.get("authors"))
            .and_then(|a| a.as_array())
            .map(|arr| {
                arr.iter()
                    .filter_map(|x| x.as_str().map(str::to_string))
                    .collect()
            })
            .unwrap_or_default()
    }
}
