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

use std::time::Duration;

use crate::graphql::queries::AuthorsPage;
use crate::graphql::{ClientError, GraphQlClient};
use crate::store::Store;

/// Page size: the contract §6.4 ceiling. `authors` enumeration is O(distinct
/// authors) seek-skip, so round-trips dominate — 500 minimizes them (Discretion
/// A4); a 500×64-hex response (~35 KB) is far under the 256 KiB request limit.
const LIMIT: i64 = 500;

/// Bounded-retry ceiling: 1 initial attempt + 2 retries (fail-fast, D-06).
const MAX_ATTEMPTS: u32 = 3;

/// Backoff base; doubles per attempt, capped at `BACKOFF_CAP`.
const BACKOFF_BASE: Duration = Duration::from_millis(250);

/// Backoff cap (250ms → 500ms → 1s … never exceeds this).
const BACKOFF_CAP: Duration = Duration::from_secs(2);

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

/// Whether a client error is worth a bounded backoff-retry (D-06/D-08).
///
/// Retryable: `Unavailable` (HTTP 503 — adapter still booting), `Transport`
/// (connection refused / timeout — 503-equivalent), and a codeless `Graphql`
/// ("internal error" — contract §7 says retry with backoff).
///
/// NON-retryable: `PayloadTooLarge` (413 — a client bug for the tiny `authors`
/// body), and any coded `Graphql` error (`INVALID_CURSOR` is handled by the
/// restart branch before this is reached; `TOO_MANY_AUTHORS`/validation are bugs
/// retry won't fix).
fn is_retryable(e: &ClientError) -> bool {
    match e {
        ClientError::Unavailable => true,
        ClientError::Transport(_) => true,
        ClientError::Graphql { code: None, .. } => true,
        ClientError::PayloadTooLarge => false,
        ClientError::Graphql { code: Some(_), .. } => false,
    }
}

/// Run an async client operation with bounded exponential backoff (RESEARCH
/// §"Bounded Retry"). Returns the first success, or the terminal error after the
/// ceiling / on the first non-retryable error. A coded `Graphql` error
/// (`INVALID_CURSOR` etc.) is non-retryable and surfaces immediately so the
/// caller's restart/abort branch handles it — never burning a retry/sleep
/// (Pitfall 4).
///
/// Both the page fetches and the start/end `stats` drift probes route through
/// this single helper (D-06/D-08), so a transient `503`/transport blip on a
/// probe gets the same bounded backoff as a page fetch rather than aborting the
/// run on the first failure (MD-02).
pub(crate) async fn retry<T, F, Fut>(mut op: F) -> Result<T, ClientError>
where
    F: FnMut() -> Fut,
    Fut: std::future::Future<Output = Result<T, ClientError>>,
{
    let mut delay = BACKOFF_BASE;
    for attempt in 1..=MAX_ATTEMPTS {
        match op().await {
            Ok(v) => return Ok(v),
            Err(e) if is_retryable(&e) && attempt < MAX_ATTEMPTS => {
                tokio::time::sleep(delay).await;
                delay = (delay * 2).min(BACKOFF_CAP);
            }
            Err(e) => return Err(e), // non-retryable, or ceiling reached
        }
    }
    unreachable!("loop returns on the final attempt")
}

/// Fetch one `authors` page through the bounded-retry helper.
async fn fetch_page_with_retry(
    client: &GraphQlClient,
    after: Option<&str>,
) -> Result<AuthorsPage, ClientError> {
    retry(|| client.authors(after, LIMIT)).await
}

/// `true` when `pk` is the contract-guaranteed shape: 64 lowercase-hex chars
/// (defense-in-depth before the DB write, T-02-08 / V5). The contract guarantees
/// this (§5/§6.4); we validate at the trust boundary anyway.
fn is_valid_pubkey(pk: &str) -> bool {
    pk.len() == 64 && pk.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// Run the `authors` opaque-cursor walk against `client`, persisting through
/// `store`. When `resume` is true, continue the latest unfinished run from its
/// stored `last_cursor` (D-01); otherwise (or when none exists) start fresh
/// (D-02).
///
/// Loop invariants (load-bearing):
/// - flush-before-cursor (Pitfall 2 / D-07): `insert_pubkeys` precedes
///   `set_run_cursor` in every iteration, so the cursor never advances past
///   unwritten pubkeys.
/// - `INVALID_CURSOR` resets `after=None` and restarts page 1 — no abort, no
///   retry (Pitfall 4, distinct branch before the retry helper's policy).
/// - retry exhaustion / non-retryable → `mark_run_aborted` (cursor untouched)
///   then return the error (D-06/D-07).
/// - clean termination on `endCursor==null` records the end drift probe and
///   marks the run done (INGEST-01 / D-09).
///
/// The store is the sync boundary (Pitfall 1): store calls are plain synchronous
/// calls inside this async fn; never wrap the store in a `tokio::sync::Mutex`.
pub async fn run(
    store: &Store,
    client: &GraphQlClient,
    resume: bool,
) -> Result<(), EnumerateError> {
    // Resume selection (D-01/D-02/D-03).
    let (run_id, mut after, existing_start) = if resume {
        match store.latest_unfinished_run()? {
            Some(run) => (run.run_id, run.last_cursor.clone(), run.max_lev_id_start),
            None => (store.begin_run("{}")?, None, None),
        }
    } else {
        (store.begin_run("{}")?, None, None)
    };

    // Drift probe start (D-09). On resume keep the original `max_lev_id_start`
    // when already set (RESEARCH Open Q2); only record it when null. Routed
    // through `retry` (MD-02) so a transient `503` on the start probe — the
    // "adapter still booting" condition the retry policy exists for — gets the
    // same bounded backoff as a page fetch instead of aborting before page 1.
    let max_start = retry(|| client.stats()).await?.max_lev_id;
    if existing_start.is_none() {
        store.set_run_max_lev_start(run_id, max_start)?;
    }

    let mut count: u64 = 0;
    loop {
        let page = match fetch_page_with_retry(client, after.as_deref()).await {
            // INVALID_CURSOR (D-08, Pitfall 4): drop the cursor, restart page 1.
            // Distinct branch — NO abort, NO retry (the retry helper already
            // treats a coded Graphql error as non-retryable, so it surfaces here).
            //
            // Guarded by `after.is_some()` (MD-01): only a non-null cursor can be
            // invalid, so a restart only makes sense when one was actually in
            // play. An `INVALID_CURSOR` while `after` is already `None` (page 1)
            // would otherwise reset `after = None` (no change) and re-issue the
            // identical request forever — an un-sleeping, unbounded hot loop.
            // Letting it fall through to the generic `Err(e)` arm turns that into
            // a loud, bounded abort (cursor preserved) instead.
            Err(ClientError::Graphql { code: Some(ref c), .. })
                if c == "INVALID_CURSOR" && after.is_some() =>
            {
                after = None;
                continue;
            }
            // Retry exhausted / non-retryable: abort with the cursor preserved
            // (D-06/D-07), report the error, return it.
            Err(e) => {
                store.mark_run_aborted(run_id)?;
                eprintln!("enumerate: aborting run {run_id} after error: {e}");
                return Err(EnumerateError::Client(e));
            }
            Ok(page) => page,
        };

        // V5 / T-02-08 defense-in-depth: validate shape before the DB write.
        // The contract guarantees 64-char lowercase hex; drop anything else
        // rather than persist a malformed pubkey.
        let valid: Vec<String> = page
            .authors
            .iter()
            .filter(|pk| is_valid_pubkey(pk))
            .cloned()
            .collect();

        // Flush-before-cursor (Pitfall 2 / D-07): enqueue the page's pubkeys
        // through the single writer FIRST.
        store.insert_pubkeys(valid.clone());
        // `count` is rows-seen-across-pages, NOT a distinct count (LW-02): an
        // overlapping resume re-fetches a page and counts a pubkey twice even
        // though `INSERT OR IGNORE` collapses it to one row. Word the log to
        // match that — a true distinct count would need `SELECT count(*) FROM
        // pubkey`.
        count += valid.len() as u64;
        eprintln!("enumerate: run {run_id} fetched {count} pubkeys (pre-dedup)");

        // ONLY after the pubkeys are DURABLE, advance the cursor. The flush
        // barrier (D-07 / Pitfall 2) blocks until the writer has committed the
        // batch just enqueued, so `set_run_cursor` (a separate short-lived
        // connection) can never make the cursor durable past un-committed
        // pubkeys — closing the async-writer-vs-cursor-connection race.
        match page.end_cursor {
            Some(c) => {
                store.flush()?;
                store.set_run_cursor(run_id, &c)?;
                after = Some(c);
            }
            // endCursor==null ⇒ end of the keyspace (canonical termination signal).
            None => break,
        }
    }

    // Terminal page's pubkeys must be durable BEFORE the run is marked done
    // (BL-01). The loop's `None` arm `break`s with the terminal page's
    // `insert_pubkeys` still only enqueued — no per-iteration flush fires for
    // it. `mark_run_done` commits `status='done'` on a separate short-lived
    // connection synchronously, and a `done` run is never re-enumerated by
    // `--resume` (`latest_unfinished_run` filters `status != 'done'`). Without
    // this barrier a crash between the `done` commit and the writer actor's
    // final batch would silently lose the terminal page. Flushing here (after
    // the loop, before `mark_run_done`) restores flush-before-cursor for the
    // last page regardless of how the loop exited with a pending enqueue.
    store.flush()?;

    // Clean termination (INGEST-01): end drift probe + mark done (D-09). The
    // end probe is routed through `retry` (MD-02) so a transient `503`/transport
    // blip does not discard the `done` mark for a run whose entire keyspace was
    // already enumerated and cursor-advanced.
    let max_end = retry(|| client.stats()).await?.max_lev_id;
    store.mark_run_done(run_id, max_end)?;
    Ok(())
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
        // For a 503 the `body` is irrelevant: the client short-circuits on the
        // status (client.rs) before parsing any body, so `{}` fixtures below
        // are placeholders, not meaningful payloads (LW-03 test-fidelity note).
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

    /// terminal_page_flushed_before_done (BL-01 regression): a `done` run must
    /// have ALL pages' pubkeys — including the terminal page — durably committed
    /// BEFORE the run is marked `done`. Asserted WITHOUT `close()`: the run-row
    /// `done` mark commits on a separate connection, so if the terminal page's
    /// pubkeys are visible to a fresh reader the instant `run()` returns (before
    /// any join), the flush barrier provably preceded `mark_run_done`. A
    /// regression of the missing terminal flush would leave PK3/PK4 sitting in
    /// the writer channel — visible only after `close()` joins the writer —
    /// while the run already reads `done`, the silent data-loss window.
    #[test]
    fn terminal_page_flushed_before_done() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                            // start drift probe
            Resp::ok(authors_body(&[PK1, PK2], true, Some(PK2))), // page 1 (non-terminal)
            Resp::ok(authors_body(&[PK3, PK4], false, None)),     // page 2 (terminal, endCursor=null)
            Resp::ok(stats_body(100)),                            // end drift probe
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        block_on(run(&store, &client, false)).expect("walk completes");

        // Read on a SEPARATE connection BEFORE close(): mark_run_done committed
        // `done` on its own connection, so the terminal page's pubkeys must
        // already be durable here — the flush barrier ran before the done mark.
        let run_id = latest_run_id(&path);
        let (status, _c, _s, _e) = read_run(&path, run_id);
        assert_eq!(status, "done", "clean termination marks run done");
        assert_eq!(
            read_pubkeys(&path),
            vec![PK1, PK2, PK3, PK4],
            "terminal page's pubkeys are durable BEFORE the run is marked done \
             (flush precedes mark_run_done — no reliance on close())"
        );

        store.close().expect("flush + join");
        drop(dir);
    }

    /// invalid_cursor_on_page1_aborts (MD-01 regression): an `INVALID_CURSOR`
    /// while `after` is still `None` (page 1) must NOT spin — the `after.is_some()`
    /// guard makes it fall through to a bounded abort instead of an un-sleeping
    /// hot loop. Asserted via the run ending `aborted` with no extra requests.
    #[test]
    fn invalid_cursor_on_page1_aborts() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                                              // 1: start probe
            Resp::ok(graphql_error_body("invalid cursor", Some("INVALID_CURSOR"))), // 2: page 1, after=None
        ]);
        let store = Store::open(&path).expect("open store");
        let client = GraphQlClient::new(server.url());
        let result = block_on(run(&store, &client, false));
        assert!(result.is_err(), "INVALID_CURSOR on page 1 aborts, not spins");
        store.close().expect("flush + join");

        let run_id = latest_run_id(&path);
        let (status, _c, _s, _e) = read_run(&path, run_id);
        assert_eq!(status, "aborted", "page-1 INVALID_CURSOR is a bounded abort");
        assert_eq!(
            server.request_count(),
            2,
            "no restart loop — exactly start-probe + the single invalid page-1 request"
        );
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

    /// invalid_cursor_restarts: an in-body INVALID_CURSOR on a page whose `after`
    /// is non-null (page 2, after a valid page-1 cursor was in play) → after
    /// resets to None, pagination restarts from page 1, no abort, no retry
    /// (asserted via the request count: exactly start-probe + page1 + invalid
    /// page2 + restarted-page1 + end-probe = 5, with no retry sleeps inflating
    /// the count). The MD-01 `after.is_some()` guard means the restart only fires
    /// for a non-null cursor — see `invalid_cursor_on_page1_aborts` for the
    /// page-1 (after=None) abort path.
    #[test]
    fn invalid_cursor_restarts() {
        let (dir, path) = temp_store();
        let server = StubServer::start(vec![
            Resp::ok(stats_body(100)),                                       // 1: start probe
            Resp::ok(authors_body(&[PK1, PK2], true, Some(PK2))),            // 2: page 1 → cursor=PK2
            Resp::ok(graphql_error_body("invalid cursor", Some("INVALID_CURSOR"))), // 3: page 2 (after=PK2) invalid → restart
            Resp::ok(authors_body(&[PK1, PK2], false, None)),                // 4: page 1 from scratch (terminal)
            Resp::ok(stats_body(100)),                                       // 5: end probe
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
            5,
            "no retry/sleep for INVALID_CURSOR — exactly start+page1+invalid+restarted-page1+end requests"
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
        let full = [PK1, PK2, PK3, PK4, PK5, PK6];

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
