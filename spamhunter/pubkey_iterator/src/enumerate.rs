//! The `authors` opaque-cursor enumeration walk (INGEST-01 / INGEST-04).
//!
//! `run` drives a strictly-sequential pagination loop over `client.authors(after,
//! limit)`: persist each page's pubkeys through the store's single writer, THEN
//! advance the cursor (flush-before-cursor, Pitfall 2 / D-07), and terminate
//! cleanly when `endCursor` is null (INGEST-01). It records the `stats.maxLevId`
//! drift probe at start and clean end (D-09), retries `503`/transport/codeless
//! errors with bounded backoff without advancing the cursor (D-06/D-07/D-08),
//! aborts with the cursor preserved on exhaustion, and restarts from page 1 on an
//! in-body `INVALID_CURSOR` (D-08, Pitfall 4).
//!
//! The store is the sync boundary (Pitfall 1): `insert_pubkeys`/`set_run_cursor`
//! are plain synchronous calls inside the async fn; the walk is never wrapped in
//! a `tokio::sync::Mutex` nor spawned across tasks.

use crate::graphql::{ClientError, GraphQlClient};
use crate::store::Store;

// RED stub — Task 1 GREEN fills this in.
#[allow(dead_code)]
const RED_PLACEHOLDER: () = ();

/// The walk's error taxonomy: a transport/in-body client error or a store error.
#[derive(Debug, thiserror::Error)]
pub enum EnumerateError {
    /// A non-retryable / retry-exhausted GraphQL client error (the run is aborted
    /// with the cursor preserved before this is returned).
    #[error("graphql client error: {0}")]
    Client(#[from] ClientError),
    /// A SQLite store error from a run-state write (`begin_run`, cursor/drift
    /// updates, abort/done marks).
    #[error("store error: {0}")]
    Store(#[from] rusqlite::Error),
}

/// Run the `authors` opaque-cursor walk against `client`, persisting through
/// `store`. When `resume` is true, continue the latest unfinished run from its
/// stored `last_cursor` (D-01); otherwise (or when none exists) start fresh
/// (D-02). RED stub — unimplemented until Task 1 GREEN.
pub async fn run(
    _store: &Store,
    _client: &GraphQlClient,
    _resume: bool,
) -> Result<(), EnumerateError> {
    unimplemented!("Task 1 GREEN")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::Store;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::{Arc, Mutex};
    use std::thread;
    use tempfile::TempDir;

    /// A fresh temp-FILE store (never `:memory:` — WAL sidecars need an on-disk DB).
    fn temp_store() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// One scripted HTTP response: a status line + a JSON body.
    #[derive(Clone)]
    struct Resp {
        status_line: &'static str,
        body: String,
    }

    impl Resp {
        fn ok(body: impl Into<String>) -> Self {
            Resp { status_line: "200 OK", body: body.into() }
        }
        fn status(status_line: &'static str, body: impl Into<String>) -> Self {
            Resp { status_line, body: body.into() }
        }
    }

    /// Build the `data.authors` body for a page.
    fn authors_body(pubkeys: &[&str], has_more: bool, end_cursor: Option<&str>) -> String {
        let list = pubkeys
            .iter()
            .map(|p| format!("\"{p}\""))
            .collect::<Vec<_>>()
            .join(",");
        let cursor = match end_cursor {
            Some(c) => format!("\"{c}\""),
            None => "null".to_string(),
        };
        format!(
            r#"{{"data":{{"authors":{{"authors":[{list}],"hasMore":{has_more},"endCursor":{cursor}}}}}}}"#
        )
    }

    /// Build the `data.stats` body for a maxLevId.
    fn stats_body(max_lev_id: i64) -> String {
        format!(r#"{{"data":{{"stats":{{"maxLevId":{max_lev_id}}}}}}}"#)
    }

    /// An in-body GraphQL error body with an optional `extensions.code`.
    fn graphql_error_body(message: &str, code: Option<&str>) -> String {
        let ext = match code {
            Some(c) => format!(r#","extensions":{{"code":"{c}"}}"#),
            None => String::new(),
        };
        format!(r#"{{"data":null,"errors":[{{"message":"{message}"{ext}}}]}}"#)
    }

    /// A scripted multi-request loopback HTTP stub. It accepts connections in a
    /// loop and serves the queued responses in FIFO order, recording how many
    /// requests it answered (so tests assert call counts — e.g. no-retry on
    /// INVALID_CURSOR). Each `GraphQlClient` call is one request → one queued
    /// response. The server keeps running until the test's temp dir drops; extra
    /// connections after the script is exhausted get a `500` (never reached in
    /// the asserted paths).
    struct StubServer {
        url: String,
        requests: Arc<Mutex<u32>>,
    }

    impl StubServer {
        fn start(script: Vec<Resp>) -> Self {
            let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
            let addr = listener.local_addr().expect("local addr");
            let url = format!("http://{addr}/graphql");
            let requests = Arc::new(Mutex::new(0u32));
            let req_clone = Arc::clone(&requests);
            thread::spawn(move || {
                let mut queue = script.into_iter();
                for conn in listener.incoming() {
                    let mut sock = match conn {
                        Ok(s) => s,
                        Err(_) => break,
                    };
                    // Drain the request (headers + small body fit one read for tests).
                    let mut buf = [0u8; 8192];
                    let _ = sock.read(&mut buf);
                    let resp = queue.next().unwrap_or(Resp::status(
                        "500 Internal Server Error",
                        r#"{"data":null,"errors":[{"message":"stub exhausted"}]}"#,
                    ));
                    *req_clone.lock().unwrap() += 1;
                    let body = resp.body.as_bytes();
                    let head = format!(
                        "HTTP/1.1 {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                        resp.status_line,
                        body.len()
                    );
                    let _ = sock.write_all(head.as_bytes());
                    let _ = sock.write_all(body);
                    let _ = sock.flush();
                }
            });
            StubServer { url, requests }
        }

        fn url(&self) -> &str {
            &self.url
        }

        fn request_count(&self) -> u32 {
            *self.requests.lock().unwrap()
        }
    }

    /// Drive the async walk to completion on a current-thread runtime (the walk is
    /// strictly sequential — one in-flight request).
    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    /// Read all pubkeys from the durable `pubkey` table.
    fn read_pubkeys(path: &std::path::Path) -> Vec<String> {
        let conn = rusqlite::Connection::open(path).expect("reader");
        let mut stmt = conn
            .prepare("SELECT pubkey FROM pubkey ORDER BY pubkey")
            .unwrap();
        let v: Vec<String> = stmt
            .query_map([], |r| r.get::<_, String>(0))
            .unwrap()
            .map(|r| r.unwrap())
            .collect();
        v
    }

    /// Read a `run` row back as `(status, last_cursor, max_lev_id_start, max_lev_id_end)`.
    fn read_run(
        path: &std::path::Path,
        run_id: i64,
    ) -> (String, Option<String>, Option<i64>, Option<i64>) {
        let conn = rusqlite::Connection::open(path).expect("reader");
        conn.query_row(
            "SELECT status, last_cursor, max_lev_id_start, max_lev_id_end FROM run WHERE run_id = ?1",
            rusqlite::params![run_id],
            |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
        )
        .expect("read run row")
    }

    /// The most-recent run_id (the walk creates one via begin_run).
    fn latest_run_id(path: &std::path::Path) -> i64 {
        let conn = rusqlite::Connection::open(path).expect("reader");
        conn.query_row("SELECT max(run_id) FROM run", [], |r| r.get(0))
            .expect("max run_id")
    }

    const PK1: &str = "aa00000000000000000000000000000000000000000000000000000000000001";
    const PK2: &str = "aa00000000000000000000000000000000000000000000000000000000000002";
    const PK3: &str = "aa00000000000000000000000000000000000000000000000000000000000003";
    const PK4: &str = "aa00000000000000000000000000000000000000000000000000000000000004";
    const PK5: &str = "aa00000000000000000000000000000000000000000000000000000000000005";
    const PK6: &str = "aa00000000000000000000000000000000000000000000000000000000000006";

    /// terminates_on_null_cursor: 3 pages then endCursor=null → loop visits each
    /// page once, persists all pubkeys, terminates, marks the run done.
    #[test]
    fn terminates_on_null_cursor() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                                  // start drift probe
            Resp::ok(authors_body(&[PK1, PK2], true, Some(PK2))),       // page 1
            Resp::ok(authors_body(&[PK3, PK4], true, Some(PK4))),       // page 2
            Resp::ok(authors_body(&[PK5, PK6], false, None)),           // page 3 (terminal)
            Resp::ok(stats_body(100)),                                  // end drift probe
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, false)).expect("walk completes");
        store.close().expect("flush + join");

        let pks = read_pubkeys(&path);
        assert_eq!(pks, vec![PK1, PK2, PK3, PK4, PK5, PK6], "all pubkeys persisted once");
        let run_id = latest_run_id(&path);
        let (status, _cursor, start, end) = read_run(&path, run_id);
        assert_eq!(status, "done", "clean termination marks run done");
        assert_eq!(start, Some(100));
        assert_eq!(end, Some(100));
        drop(dir);
    }

    /// resume_from_last_cursor: a pre-seeded unfinished run with a stored cursor →
    /// the first `authors` call uses that cursor as `after` (no --run-id).
    #[test]
    fn resume_from_last_cursor() {
        let (dir, path) = temp_store();
        // Seed a run with a stored cursor, then close so it is durable + unfinished.
        let seed_run = {
            let store = Store::open(&path).expect("open store");
            let rid = store.begin_run("{}").expect("begin_run");
            store.set_run_max_lev_start(rid, 100).expect("seed start");
            store.set_run_cursor(rid, PK2).expect("seed cursor");
            store.insert_pubkeys(vec![PK1.into(), PK2.into()]);
            store.close().expect("flush");
            rid
        };

        // The walk should resume from PK2: serve only the remaining tail.
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                          // start probe (resume keeps existing start)
            Resp::ok(authors_body(&[PK3, PK4], false, None)),   // continuation page (terminal)
            Resp::ok(stats_body(100)),                          // end probe
        ]);
        let store = Store::open(&path).expect("reopen store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, true)).expect("resume walk completes");
        store.close().expect("flush + join");

        // Same run continued (no new run row), all pubkeys present, done.
        assert_eq!(latest_run_id(&path), seed_run, "resume reuses the existing run id");
        let pks = read_pubkeys(&path);
        assert_eq!(pks, vec![PK1, PK2, PK3, PK4]);
        let (status, _c, start, _e) = read_run(&path, seed_run);
        assert_eq!(status, "done");
        assert_eq!(start, Some(100), "resume keeps the original max_lev_id_start (D-09 Open Q2)");
        drop(dir);
    }

    /// pubkeys_idempotent_on_resume: an overlapping resume (re-fetch a page already
    /// persisted) leaves exactly one row per pubkey (INSERT OR IGNORE).
    #[test]
    fn pubkeys_idempotent_on_resume() {
        let (dir, path) = temp_store();
        // Seed: run already persisted PK1,PK2 with cursor at PK2.
        let seed_run = {
            let store = Store::open(&path).expect("open store");
            let rid = store.begin_run("{}").expect("begin_run");
            store.set_run_max_lev_start(rid, 100).expect("seed start");
            store.set_run_cursor(rid, PK2).expect("seed cursor");
            store.insert_pubkeys(vec![PK1.into(), PK2.into()]);
            store.close().expect("flush");
            rid
        };
        // Resume re-fetches an overlapping page (PK2 again) then a new page.
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),
            Resp::ok(authors_body(&[PK2, PK3], true, Some(PK3))), // overlaps PK2
            Resp::ok(authors_body(&[PK4], false, None)),
            Resp::ok(stats_body(100)),
        ]);
        let store = Store::open(&path).expect("reopen store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, true)).expect("resume walk completes");
        store.close().expect("flush + join");

        let _ = seed_run;
        let pks = read_pubkeys(&path);
        assert_eq!(pks, vec![PK1, PK2, PK3, PK4], "overlap leaves one row per pubkey");
        drop(dir);
    }

    /// retry_503_no_cursor_advance (success branch): a 503 then success → bounded
    /// retry succeeds and the cursor advances normally; the walk completes.
    #[test]
    fn retry_503_no_cursor_advance() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),
            Resp::status("503 Service Unavailable", r#"{}"#), // transient
            Resp::ok(authors_body(&[PK1, PK2], false, None)), // retry succeeds
            Resp::ok(stats_body(100)),
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, false)).expect("retry then success completes");
        store.close().expect("flush + join");

        let pks = read_pubkeys(&path);
        assert_eq!(pks, vec![PK1, PK2], "page persisted after the retry");
        let run_id = latest_run_id(&path);
        let (status, _c, _s, _e) = read_run(&path, run_id);
        assert_eq!(status, "done");
        drop(dir);
    }

    /// retry_503 exhaustion → abort with cursor unchanged from the prior page.
    /// Also abort_preserves_cursor: status='aborted', last_cursor == last persisted
    /// page's endCursor.
    #[test]
    fn abort_preserves_cursor() {
        let (dir, path) = temp_store();
        // page 1 succeeds (cursor → PK2), then 503 to exhaustion (3 attempts).
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),
            Resp::ok(authors_body(&[PK1, PK2], true, Some(PK2))), // page 1 persisted, cursor=PK2
            Resp::status("503 Service Unavailable", r#"{}"#),     // attempt 1
            Resp::status("503 Service Unavailable", r#"{}"#),     // attempt 2
            Resp::status("503 Service Unavailable", r#"{}"#),     // attempt 3 (exhaust)
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        let result = block_on(run(&store, &client, false));
        assert!(result.is_err(), "exhausted retries → walk returns an error");
        store.close().expect("flush + join");

        let run_id = latest_run_id(&path);
        let (status, cursor, _s, _e) = read_run(&path, run_id);
        assert_eq!(status, "aborted", "exhaustion marks the run aborted");
        assert_eq!(
            cursor.as_deref(),
            Some(PK2),
            "abort preserves the last fully-persisted page's endCursor (D-07)"
        );
        // page 1's pubkeys are durable; the failed page advanced nothing.
        assert_eq!(read_pubkeys(&path), vec![PK1, PK2]);
        drop(dir);
    }

    /// invalid_cursor_restarts: an in-body INVALID_CURSOR once → after resets to
    /// None, pagination restarts from page 1, no abort, no retry (asserted via the
    /// request count: exactly start-probe + invalid + page1 + end-probe = 4, with
    /// no retry sleeps inflating the count).
    #[test]
    fn invalid_cursor_restarts() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                                       // 1: start probe
            Resp::ok(graphql_error_body("invalid cursor", Some("INVALID_CURSOR"))), // 2: invalid → restart
            Resp::ok(authors_body(&[PK1, PK2], false, None)),                // 3: page 1 from scratch
            Resp::ok(stats_body(100)),                                       // 4: end probe
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, false)).expect("invalid_cursor restart completes");
        store.close().expect("flush + join");

        assert_eq!(read_pubkeys(&path), vec![PK1, PK2]);
        let run_id = latest_run_id(&path);
        let (status, _c, _s, _e) = read_run(&path, run_id);
        assert_eq!(status, "done", "INVALID_CURSOR restarts, never aborts");
        assert_eq!(
            server.request_count(),
            4,
            "no retry/sleep for INVALID_CURSOR — exactly start+invalid+page1+end requests"
        );
        drop(dir);
    }

    /// records_drift_does_not_abort: stats returns 100 at start, 150 at end →
    /// both recorded, run completes done (drift does NOT abort).
    #[test]
    fn records_drift_does_not_abort() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                        // start: 100
            Resp::ok(authors_body(&[PK1, PK2], false, None)),
            Resp::ok(stats_body(150)),                        // end: 150 (drift)
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, false)).expect("drift run completes");
        store.close().expect("flush + join");

        let run_id = latest_run_id(&path);
        let (status, _c, start, end) = read_run(&path, run_id);
        assert_eq!(status, "done", "drift does not abort the run (D-09)");
        assert_eq!(start, Some(100), "max_lev_id_start recorded");
        assert_eq!(end, Some(150), "max_lev_id_end recorded (differs from start)");
        drop(dir);
    }

    /// resume_boundary_union_complete (property): a deterministic full set split
    /// across arbitrary page boundaries, with a resume cut at any page boundary,
    /// yields a persisted union equal to the full mocked set. Implemented as a
    /// parameterized check across several (page-split, cut) combinations.
    #[test]
    fn resume_boundary_union_complete() {
        // Full ordered corpus.
        let full = vec![PK1, PK2, PK3, PK4, PK5, PK6];

        // (page sizes, cut after this many pages on the first leg)
        let cases: &[(&[usize], usize)] = &[
            (&[2, 2, 2], 1),
            (&[2, 2, 2], 2),
            (&[1, 2, 3], 1),
            (&[3, 3], 1),
            (&[1, 1, 1, 1, 1, 1], 3),
        ];

        for (sizes, cut_pages) in cases {
            let (dir, path) = temp_store();

            // Build the page list: each page is (slice, has_more, end_cursor).
            let mut pages: Vec<(Vec<&str>, bool, Option<&str>)> = Vec::new();
            let mut idx = 0usize;
            for (i, &sz) in sizes.iter().enumerate() {
                let slice: Vec<&str> = full[idx..idx + sz].to_vec();
                idx += sz;
                let is_last = i == sizes.len() - 1;
                let end_cursor = if is_last { None } else { Some(*slice.last().unwrap()) };
                pages.push((slice, !is_last, end_cursor));
            }

            // Leg 1: serve pages[0..cut_pages], then simulate a crash (no terminal
            // page → never marked done). We drive only the first cut_pages pages by
            // scripting just those, then a 503-to-exhaustion so the run aborts with
            // the cursor at the last served page.
            {
                let mut script = vec![Resp::ok(stats_body(100))];
                for (slice, has_more, ec) in pages.iter().take(*cut_pages) {
                    script.push(Resp::ok(authors_body(slice, *has_more, *ec)));
                }
                // Force an abort after the cut so leg 1 stops cleanly mid-walk.
                script.push(Resp::status("503 Service Unavailable", r#"{}"#));
                script.push(Resp::status("503 Service Unavailable", r#"{}"#));
                script.push(Resp::status("503 Service Unavailable", r#"{}"#));
                let server = StubServer::start(script);
                let store = Store::open(&path).expect("open store");
                let client = GraphQlClient::new(server.url());
                let _ = block_on(run(&store, &client, false)); // aborts; cursor preserved
                store.close().expect("flush");
            }

            // Leg 2: resume → serve the remaining pages from the cut onward.
            {
                let mut script = vec![Resp::ok(stats_body(100))];
                for (slice, has_more, ec) in pages.iter().skip(*cut_pages) {
                    script.push(Resp::ok(authors_body(slice, *has_more, *ec)));
                }
                script.push(Resp::ok(stats_body(100)));
                let server = StubServer::start(script);
                let store = Store::open(&path).expect("reopen store");
                let client = GraphQlClient::new(server.url());
                block_on(run(&store, &client, true)).expect("resume completes");
                store.close().expect("flush");
            }

            let mut got = read_pubkeys(&path);
            got.sort();
            let mut want: Vec<String> = full.iter().map(|s| s.to_string()).collect();
            want.sort();
            assert_eq!(
                got, want,
                "union of persisted pubkeys must equal the full set (sizes={sizes:?}, cut={cut_pages})"
            );
            drop(dir);
        }
    }
}
