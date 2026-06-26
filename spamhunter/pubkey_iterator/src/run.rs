//! The end-to-end batch orchestration (`run_batch`, Phase 5 / the MD-02 fix).
//!
//! `run_batch` composes the FOUR existing subsystems onto ONE canonical run_id
//! (Plan 01's run-lifecycle unification made this clean):
//!
//! 1. **detect** — `seed_weights_if_empty` + `read_weights`: seed the combiner
//!    `weight` table from config on first run, then read the live weights.
//! 2. **enumerate** — `enumerate::run(store, client, resume, snapshot_json)`: the
//!    resumable `authors` walk that populates the durable `pubkey` table on the
//!    canonical run_id and records the start/end drift probe (it does NOT mark the
//!    run done — `run_batch` owns that, Pitfall 1/2).
//! 3. **pipeline** — `run_pipeline` + `production_fetch_with_whitelist`: the
//!    bounded tokio→flume→std::thread fetch+L0 stage feeding the CPU consumer.
//! 4. **store** — the single-writer actor: each scored pubkey is persisted via
//!    the lifted `stage.score(run_id, author, events, whitelisted)` →
//!    `store.persist(p)` consumer (the EXACT wiring that lived only in
//!    `pipeline.rs` `#[cfg(test)]` until this phase — the MD-02 fix).
//!
//! The τ + weight set are snapshotted into `run.config_json` BEFORE scoring
//! (D-04/D-06 reproducibility — the canonical run records the parameters it
//! scored with). Progress is a modulus-gated, count-only `eprintln!` (T-05-07: it
//! never logs event content) — no `indicatif` (RESEARCH dep discipline, D-01).
//!
//! Ownership (Pitfall 3): `run_batch` TAKES `Arc<Store>` so the consumer closure
//! (`Send + Sync + 'static`, it runs on the dedicated consumer thread) can clone
//! it; after `run_pipeline` returns — the consumer thread already joined inside —
//! `Arc::try_unwrap(store).ok().expect(...).close()` flushes+joins the writer.
//! `mark_run_done` fires after scoring on that same run_id, so a single run spans
//! enumerate→score→done (the `kind=1`, `per_author=100` fetch is hardcoded per
//! INGEST-02 / RESEARCH A3).

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use crate::config::Config;
use crate::detect::{
    read_weights, seed_weights_if_empty, ScoredInput, ScoringStage, BIAS_KEY, THRESHOLD_KEY,
};
use crate::enumerate::EnumerateError;
use crate::graphql::{ClientError, GraphQlClient};
use crate::pipeline::{
    production_fetch_with_whitelist, run_pipeline, DEFAULT_AUTHORS_PER_CALL, DEFAULT_CHANNEL_CAP,
};
use crate::store::queries::read_pubkeys;
use crate::store::Store;

/// The kind fetched for scoring (text notes, NIP-01 kind 1). Hardcoded per
/// INGEST-02 / RESEARCH A3 — not a config knob in Phase 5.
const FETCH_KIND: i64 = 1;
/// Events fetched per author for scoring (INGEST-02: "most-recent ~100 events").
/// Hardcoded per RESEARCH A3.
const FETCH_PER_AUTHOR: i64 = 100;

/// `run_batch`'s error taxonomy: the enumerate leg's error (which already wraps a
/// client/store error), a standalone client error from the fetch stage, a SQLite
/// store error from a read/run-state write, or a snapshot-serialization error.
#[derive(Debug, thiserror::Error)]
pub enum RunError {
    /// The enumerate leg failed (retry-exhausted/non-retryable client error, or a
    /// store error) — the enumeration aborted with the cursor preserved.
    #[error("enumerate error: {0}")]
    Enumerate(#[from] EnumerateError),
    /// A GraphQL client error surfaced from the fetch/score pipeline.
    #[error("graphql client error: {0}")]
    Client(#[from] ClientError),
    /// A SQLite store error from a read (`read_weights`/`read_pubkeys`) or a
    /// run-state write (`mark_run_done`).
    #[error("store error: {0}")]
    Store(#[from] rusqlite::Error),
    /// The τ + weight snapshot could not be serialized to JSON.
    #[error("snapshot serialization error: {0}")]
    Snapshot(#[from] serde_json::Error),
    /// The canonical `run` row vanished between enumerate and the done-mark
    /// (out-of-band delete or a refactor that changes `run_id` mid-batch). Marking
    /// would silently UPDATE zero rows, so `run_batch` fails loudly instead of
    /// reporting success for a run that was never marked `done` (MED-01).
    #[error("run row {0} vanished before it could be marked done")]
    MissingRunRow(i64),
}

/// Drive ONE full end-to-end batch — enumerate → fetch → score → persist — on a
/// single canonical run_id, returning that run_id.
///
/// Takes ownership of an `Arc<Store>` so the consumer closure can clone it across
/// the `'static` consumer-thread boundary (Pitfall 3); after the pipeline drains
/// it unwraps the sole reference and `close()`s the writer, then marks the run
/// `done`. `resume` threads straight to `enumerate::run` (Phase-2 semantics, D-03).
/// `limit` (operator `--limit`) caps how many pubkeys the enumerate leg walks, for
/// bounded end-to-end test runs; `None` walks the full keyspace.
///
/// Reproducibility (D-04/D-06): τ + the weight set are snapshotted into
/// `run.config_json` (via `enumerate::run`'s `config_json` arg) BEFORE any score
/// is written, so the canonical run records the parameters it scored with.
pub async fn run_batch(
    store: Arc<Store>,
    config: &Config,
    resume: bool,
    limit: Option<u64>,
) -> Result<i64, RunError> {
    // 1. Seed the weight table from config on first run (no-op once seeded), then
    //    read the live weights (the stored values stand across a Phase-6 retune).
    seed_weights_if_empty(&store, config)?;
    let weights = read_weights(&store.reader()?)?;

    // 2. Build the τ + weight reproducibility snapshot. τ from the seeded
    //    `_threshold` sentinel (threshold column) else config.tau; bias from the
    //    `_bias` sentinel else config.bias — so the snapshot reflects what scoring
    //    will actually use (which `ScoringStage::from_config` reads the same way).
    let tau = weights
        .iter()
        .find(|w| w.layer == THRESHOLD_KEY)
        .and_then(|w| w.threshold)
        .unwrap_or(config.tau);
    let bias = weights
        .iter()
        .find(|w| w.layer == BIAS_KEY)
        .map(|w| w.weight)
        .unwrap_or(config.bias);
    let snapshot = serde_json::json!({
        "tau": tau,
        "bias": bias,
        "weights": weights,
        "adapter_url": config.adapter_url,
        "channel_cap": DEFAULT_CHANNEL_CAP,
        "authors_per_call": DEFAULT_AUTHORS_PER_CALL,
    });
    let snapshot_json = serde_json::to_string(&snapshot)?;

    // 3. Enumerate leg on the canonical run_id (D-03 resume). The snapshot lands on
    //    the run via begin_run/set_run_config_json; enumerate does NOT mark done.
    let client = GraphQlClient::new(config.adapter_url.clone());
    let run_id = crate::enumerate::run(&store, &client, resume, &snapshot_json, limit).await?;

    // 4. Build the fixed-order stage from config + the seeded weights.
    let stage = Arc::new(ScoringStage::from_config(config, &weights));

    // 5. The durable enumeration source (D-07: read the `pubkey` table, decoupled
    //    from the live walk). Empty corpus → the pipeline is a clean no-op.
    let pubkeys = read_pubkeys(&store.reader()?)?;
    let total = pubkeys.len();
    eprintln!("run {run_id}: scoring {total} enumerated pubkeys (kind={FETCH_KIND}, per_author={FETCH_PER_AUTHOR})");

    // 6. The CPU consumer (lifted from pipeline.rs `#[cfg(test)]` — the MD-02 fix):
    //    score each ScoredInput on THIS run_id and persist through the single
    //    writer. Progress is a count-only eprintln (T-05-07 — never event content).
    let processed = Arc::new(AtomicU64::new(0));
    let store_c = Arc::clone(&store);
    let stage_c = Arc::clone(&stage);
    let counter = Arc::clone(&processed);
    let consumer = move |input: &ScoredInput| {
        let p = stage_c.score(
            run_id,
            &input.group.author,
            &input.group.events,
            input.whitelisted,
        );
        store_c.persist(p);
        let n = counter.fetch_add(1, Ordering::Relaxed) + 1;
        if n.is_multiple_of(1000) {
            eprintln!("run {run_id}: scored {n} pubkeys");
        }
    };

    // 7. The fetch stage: events + L0 whitelist resolution in the SAME tokio stage
    //    (Pitfall 5 — the CPU consumer never does HTTP). One pooled client +
    //    whitelist client cloned into the per-batch async closure.
    let client_f = Arc::new(client);
    let whitelist = Arc::new(crate::detect::whitelist::WhitelistClient::new(
        config.whitelist_url.clone(),
    ));
    let fetch = move |batch: Vec<String>| {
        let client_f = Arc::clone(&client_f);
        let wl_f = Arc::clone(&whitelist);
        async move {
            production_fetch_with_whitelist(&client_f, &wl_f, FETCH_KIND, FETCH_PER_AUTHOR, &batch)
                .await
        }
    };

    // 8. Drive the bounded pipeline (it joins the consumer thread before returning).
    run_pipeline(
        fetch,
        pubkeys,
        DEFAULT_CHANNEL_CAP,
        DEFAULT_AUTHORS_PER_CALL,
        consumer,
    )
    .await?;
    let scored = processed.load(Ordering::Relaxed);
    eprintln!("run {run_id}: scored {scored} pubkeys total");

    // 9. Flush + join the writer (Pitfall 3): the consumer thread is already joined
    //    inside run_pipeline, so the store Arc is the sole reference here. close()
    //    drains+commits every score/signal before the run is marked done.
    let store = Arc::try_unwrap(store)
        .ok()
        .expect("sole store ref after pipeline join (Pitfall 3)");

    // 10. Re-mark the canonical run done. enumerate recorded the end drift via
    //     set_run_max_lev_end; read it back so mark_run_done stamps the SAME
    //     max_lev_id_end (a single run_id spans enumerate→score→done, A5).
    //
    //     MED-01: distinguish None (the run row is GONE — should never happen, but
    //     an out-of-band delete or a future run_id-changing refactor could trigger
    //     it) from Some(0) (the row exists, drift probe genuinely unrecorded). A
    //     missing row is a hard error, NOT a silent unwrap_or(0) that would let
    //     mark_run_done UPDATE zero rows while run_batch still reports success.
    let max_lev_end = match read_max_lev_end(&store, run_id)? {
        Some(v) => v,
        None => return Err(RunError::MissingRunRow(run_id)),
    };
    // mark_run_done itself also asserts it matched exactly one row (defence in
    // depth — the run row could vanish between this read and the UPDATE).
    store.mark_run_done(run_id, max_lev_end)?;
    store.close()?;
    Ok(run_id)
}

/// Read back the run's recorded `max_lev_id_end` (set by enumerate's clean-end
/// drift probe) so `mark_run_done` re-stamps the same value (D-09 note).
fn read_max_lev_end(store: &Store, run_id: i64) -> rusqlite::Result<Option<i64>> {
    use rusqlite::OptionalExtension;
    let conn = store.reader()?;
    conn.query_row(
        "SELECT max_lev_id_end FROM run WHERE run_id = ?1",
        rusqlite::params![run_id],
        |r| r.get::<_, Option<i64>>(0),
    )
    .optional()
    .map(Option::flatten)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::Store;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use tempfile::TempDir;

    /// A current-thread tokio runtime to drive one batch per test (the client.rs /
    /// enumerate.rs / pipeline.rs harness idiom).
    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    /// A fresh temp-FILE store path (never `:memory:` — WAL sidecars need disk).
    fn temp_store() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// Parse the committed example config (the conservative D-08 defaults), then
    /// point its adapter/whitelist URLs at the supplied stub URLs.
    fn stub_config(adapter_url: String, whitelist_url: String) -> Config {
        let body = std::fs::read_to_string(concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/pubkey_iterator_config.example.toml"
        ))
        .expect("read example config");
        let mut cfg: Config = toml::from_str(&body).expect("parse example config");
        cfg.adapter_url = adapter_url;
        cfg.whitelist_url = whitelist_url;
        cfg
    }

    /// Minimal `variables.authors` extractor (mirror of pipeline.rs's helper).
    fn parse_authors_local(body: &str) -> Vec<String> {
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

    /// An adapter stub serving BOTH legs run_batch hits:
    /// - `authors` (the enumerate walk) → an EMPTY page (endCursor null) so the
    ///   enumerate leg is a clean no-op over the PRE-SEEDED pubkey table (the
    ///   isolation the plan recommends — scoring + snapshot + done without a full
    ///   dual-document stub).
    /// - `stats` (start/end drift probes) → a fixed maxLevId.
    /// - `latestPerAuthor` (the fetch stage) → one event per requested author,
    ///   OMITTING `omit` (a zero-event author, contract §5) — the omitting_stub
    ///   idiom, so the omitted pubkey still gets a score row (D-15).
    ///
    /// Routes by inspecting the request body's `query`/`variables`.
    fn adapter_stub(omit: String) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}/graphql");
        std::thread::spawn(move || {
            for conn in listener.incoming() {
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                let mut buf = vec![0u8; 16384];
                let n = sock.read(&mut buf).unwrap_or(0);
                let req = String::from_utf8_lossy(&buf[..n]);
                let body = req.split("\r\n\r\n").nth(1).unwrap_or("");

                let resp_body = if body.contains("latestPerAuthor") {
                    let authors = parse_authors_local(body);
                    let groups = authors
                        .iter()
                        .filter(|a| **a != omit)
                        .map(|a| {
                            format!(
                                r#"{{"author":"{a}","events":[{{"id":"{a}-e","pubkey":"{a}","kind":1,"createdAt":1700000000,"content":"hi","tags":[]}}]}}"#
                            )
                        })
                        .collect::<Vec<_>>()
                        .join(",");
                    format!(r#"{{"data":{{"latestPerAuthor":[{groups}]}}}}"#)
                } else if body.contains("authors") {
                    // Empty authors page: endCursor null → enumerate is a no-op
                    // (the corpus is pre-seeded into the pubkey table).
                    r#"{"data":{"authors":{"authors":[],"hasMore":false,"endCursor":null}}}"#
                        .to_string()
                } else {
                    // stats drift probe.
                    r#"{"data":{"stats":{"maxLevId":100}}}"#.to_string()
                };

                let bytes = resp_body.as_bytes();
                let head = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    bytes.len()
                );
                let _ = sock.write_all(head.as_bytes());
                let _ = sock.write_all(bytes);
                let _ = sock.flush();
            }
        });
        url
    }

    /// A loopback whitelist stub: always replies `{"whitelisted":body}`, serving up
    /// to `max_conns` connections (mirrors pipeline.rs's whitelist_stub).
    fn whitelist_stub(body_whitelisted: bool, max_conns: usize) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}");
        std::thread::spawn(move || {
            for (i, conn) in listener.incoming().enumerate() {
                if i >= max_conns {
                    break;
                }
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                let mut b = [0u8; 4096];
                let _ = sock.read(&mut b);
                let resp_body = format!(r#"{{"whitelisted":{body_whitelisted}}}"#);
                let bytes = resp_body.as_bytes();
                let head = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    bytes.len()
                );
                let _ = sock.write_all(head.as_bytes());
                let _ = sock.write_all(bytes);
                let _ = sock.flush();
            }
        });
        url
    }

    const PK0: &str = "aa00000000000000000000000000000000000000000000000000000000000001";
    const OMITTED: &str = "aa00000000000000000000000000000000000000000000000000000000000002";
    const PK2: &str = "aa00000000000000000000000000000000000000000000000000000000000003";

    /// OPS-01 / SC(run e2e): over a small pre-seeded corpus (3 pubkeys, the
    /// adapter omitting one as zero-event), `run_batch` returns a run_id;
    /// afterwards EVERY requested pubkey has a `score` row, each has ≥1 `signal`
    /// row, and `run.status == "done"`. The production analogue of the pipeline.rs
    /// `zero_event_pubkey_gets_score_row` test, but through the `run_batch` entry.
    #[test]
    fn run_batch_endtoend_mocked() {
        let (_dir, path) = temp_store();
        let store = Arc::new(Store::open(&path).expect("open store"));

        // Pre-seed the durable pubkey table; the adapter's authors page is empty so
        // the enumerate leg is a no-op and scoring runs over these three.
        let requested = vec![PK0.to_string(), OMITTED.to_string(), PK2.to_string()];
        store.insert_pubkeys(requested.clone());
        store.flush().expect("flush pubkeys");

        let adapter = adapter_stub(OMITTED.to_string());
        let wl = whitelist_stub(false, 16);
        let config = stub_config(adapter, wl);

        // run_batch TAKES the sole Arc<Store> and consumes it (Pitfall 3:
        // Arc::try_unwrap requires no surviving clone — the caller must hand over
        // ownership, exactly as main.rs's Run arm does). A fresh reader on the path
        // sees the durable rows after close() ran inside run_batch.
        let run_id = block_on(run_batch(store, &config, false, None)).expect("run_batch");

        let conn = rusqlite::Connection::open(&path).expect("reader");

        let n_score: i64 = conn
            .query_row(
                "SELECT count(*) FROM score WHERE run_id = ?1",
                [run_id],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(
            n_score, 3,
            "every requested pubkey (incl. the omitted zero-event one) has a score row"
        );

        // The omitted (zero-event) pubkey got a score row too (D-15).
        let omitted_present: i64 = conn
            .query_row(
                "SELECT count(*) FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, OMITTED],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(omitted_present, 1, "the zero-event pubkey was scored (D-15)");

        // Each pubkey has ≥1 signal row.
        let min_sigs: i64 = conn
            .query_row(
                "SELECT min(c) FROM (SELECT pubkey, count(*) AS c FROM signal \
                 WHERE run_id = ?1 GROUP BY pubkey)",
                [run_id],
                |r| r.get(0),
            )
            .unwrap();
        assert!(min_sigs >= 1, "every scored pubkey has ≥1 signal row");

        // The run is marked done on the SAME run_id (no second run row).
        let (status, n_runs): (String, i64) = {
            let status: String = conn
                .query_row(
                    "SELECT status FROM run WHERE run_id = ?1",
                    [run_id],
                    |r| r.get(0),
                )
                .unwrap();
            let n_runs: i64 = conn
                .query_row("SELECT count(*) FROM run", [], |r| r.get(0))
                .unwrap();
            (status, n_runs)
        };
        assert_eq!(status, "done", "the canonical run is marked done");
        assert_eq!(n_runs, 1, "scoring used the SAME run_id (no second run row)");
    }

    /// OPS-01 (reproducibility, D-04/D-06): after `run_batch`, `run.config_json`
    /// for the returned run_id parses as JSON with a `tau` field == config.tau and
    /// a `weights` array covering the seeded layer weights (+ _bias/_threshold).
    #[test]
    fn snapshot_records_tau_and_weights() {
        let (_dir, path) = temp_store();
        let store = Arc::new(Store::open(&path).expect("open store"));
        store.insert_pubkeys(vec![PK0.to_string()]);
        store.flush().expect("flush pubkeys");

        let adapter = adapter_stub("none".to_string()); // omit nothing
        let wl = whitelist_stub(false, 8);
        let config = stub_config(adapter, wl);

        // Hand the sole Arc<Store> to run_batch (Pitfall 3); read back via the path.
        let run_id = block_on(run_batch(store, &config, false, None)).expect("run_batch");

        let conn = rusqlite::Connection::open(&path).expect("reader");
        let config_json: String = conn
            .query_row(
                "SELECT config_json FROM run WHERE run_id = ?1",
                [run_id],
                |r| r.get(0),
            )
            .unwrap();
        let snapshot: serde_json::Value =
            serde_json::from_str(&config_json).expect("config_json parses as JSON");

        // τ == config τ (the example config's 0.5 — seeded into _threshold).
        let tau = snapshot.get("tau").and_then(|t| t.as_f64()).expect("tau");
        assert!(
            (tau - config.tau).abs() < 1e-12,
            "snapshot τ ({tau}) == config τ ({})",
            config.tau
        );

        // weights is an array covering the seeded combiner rows (4 layers + _bias
        // + _threshold = 6).
        let weights = snapshot
            .get("weights")
            .and_then(|w| w.as_array())
            .expect("weights array");
        assert_eq!(weights.len(), 6, "weights array covers the six combiner rows");
        let layer_names: Vec<&str> = weights
            .iter()
            .filter_map(|w| w.get("layer").and_then(|l| l.as_str()))
            .collect();
        for expected in [
            "L0_whitelist_absence",
            "L1_near_duplicate",
            "L3_content_entropy",
            "L4_link_mention",
            BIAS_KEY,
            THRESHOLD_KEY,
        ] {
            assert!(
                layer_names.contains(&expected),
                "snapshot weights must include {expected} (got {layer_names:?})"
            );
        }
    }

    /// TUNE-03 / D-04 (RESEARCH §TUNE-03: "already satisfied … Phase 6 adds a
    /// confirming test"): a RETUNED weight set propagates into the NEXT run's
    /// `config_json` snapshot, proving every past verdict is traceable to the exact
    /// weights that produced it. `run_batch` reads the live `weight` rows at startup
    /// (`read_weights`) and snapshots them BEFORE scoring; `seed_weights_if_empty`
    /// declines to overwrite stored rows, so an adopted retune survives.
    ///
    /// Construction: run a first `run_batch` so the six combiner rows exist, then
    /// simulate a Plan-02 ADOPTED retune by UPDATEing L1_near_duplicate's `weight`
    /// to a distinct sentinel (7.5) with provenance on `weight_write_conn` — exactly
    /// the write `tune::run_tune` performs on a backtest PASS. Run a SECOND
    /// `run_batch`, parse that run's `config_json`, and assert the snapshot's L1
    /// weight entry now carries 7.5 (the retuned value the second run consumed).
    /// A glue/wiring confirmation of existing behaviour — no production change.
    #[test]
    fn retuned_weights_appear_in_next_run_snapshot() {
        let (_dir, path) = temp_store();

        // First run: seeds the weight table (conservative config defaults) and
        // snapshots them. A fresh stub per run (each serves a bounded conn count).
        let run1 = {
            let store = Arc::new(Store::open(&path).expect("open store"));
            store.insert_pubkeys(vec![PK0.to_string()]);
            store.flush().expect("flush pubkeys");
            let adapter = adapter_stub("none".to_string());
            let wl = whitelist_stub(false, 8);
            let config = stub_config(adapter, wl);
            block_on(run_batch(store, &config, false, None)).expect("run_batch 1")
        };

        // Simulate an adopted retune: UPDATE L1_near_duplicate's weight to a
        // distinct sentinel on the weight_write_conn (the exact write run_tune
        // performs on a PASS — params-bound, weight-table only, T-06-07).
        const SENTINEL: f64 = 7.5;
        {
            let store = Store::open(&path).expect("reopen store for retune");
            let now = 1_700_000_000_i64;
            let conn = store.weight_write_conn().expect("weight write conn");
            let n = conn
                .execute(
                    "UPDATE weight SET weight = ?2, tuned_at = ?3, tuned_from_run = ?4 \
                     WHERE layer = ?1",
                    rusqlite::params!["L1_near_duplicate", SENTINEL, now, run1],
                )
                .expect("update L1 weight");
            assert_eq!(n, 1, "the L1 weight row was retuned");
            store.close().expect("close after retune");
        }

        // Second run: reads the live (now-retuned) weights at startup and snapshots
        // them. seed_weights_if_empty is a no-op (rows already exist), so the
        // sentinel survives into this run's reproducibility snapshot.
        let run2 = {
            let store = Arc::new(Store::open(&path).expect("reopen store for run 2"));
            let adapter = adapter_stub("none".to_string());
            let wl = whitelist_stub(false, 8);
            let config = stub_config(adapter, wl);
            block_on(run_batch(store, &config, false, None)).expect("run_batch 2")
        };
        assert_ne!(run1, run2, "the second run is a distinct run_id");

        // The second run's config_json snapshot carries the RETUNED L1 weight.
        let conn = rusqlite::Connection::open(&path).expect("reader");
        let config_json: String = conn
            .query_row(
                "SELECT config_json FROM run WHERE run_id = ?1",
                [run2],
                |r| r.get(0),
            )
            .unwrap();
        let snapshot: serde_json::Value =
            serde_json::from_str(&config_json).expect("config_json parses as JSON");
        let weights = snapshot
            .get("weights")
            .and_then(|w| w.as_array())
            .expect("weights array");
        let l1 = weights
            .iter()
            .find(|w| w.get("layer").and_then(|l| l.as_str()) == Some("L1_near_duplicate"))
            .expect("L1 weight entry in snapshot");
        let l1_weight = l1.get("weight").and_then(|w| w.as_f64()).expect("L1 weight");
        assert!(
            (l1_weight - SENTINEL).abs() < 1e-12,
            "the next run's snapshot carries the RETUNED L1 weight ({SENTINEL}), got {l1_weight} \
             — proving run-to-weight traceability (TUNE-03/D-04)"
        );
    }

    /// OPS-01 (live, must_have) / D-07 — the live end-to-end `run` proof, SELF-
    /// SKIPPING. Builds a `Config` pointing `adapter_url` at the live LMDB2GraphQL
    /// adapter (`LMDB2GRAPHQL_URL`, default the CONTEXT host) and `whitelist_url` at
    /// the live whitelist (`WHITELIST_URL`, default loopback :8081), with the four
    /// layers at the example-config defaults.
    ///
    /// It first PROBES the adapter (`client.authors(None, 1)`, mirroring
    /// `pipeline::tests::live_latest_per_author`); on `Unavailable`/`Transport` it
    /// prints a deferred-manual note and `return`s — NEVER failing CI on a transient
    /// outage (D-07). When reachable it drives the FULL `run_batch` (enumerate the
    /// live keyspace → fetch ~100 events/pubkey → score → persist) and asserts ≥1
    /// `score` row plus a `done` run.
    ///
    /// `#[ignore]` by default: a full live walk is unbounded (the whole corpus), so
    /// `cargo test` stays hermetic and fast; the live proof runs via
    /// `cargo test --lib run::tests::live_run_self_skipping -- --ignored`. Even under
    /// `--ignored` it self-skips on outage, so it is never a flaky CI failure.
    #[test]
    #[ignore = "live integration: requires the LMDB2GraphQL adapter + whitelist (run with --ignored); self-skips on outage"]
    fn live_run_self_skipping() {
        const DEFAULT_ADAPTER: &str = "http://192.168.149.21:8080/graphql";
        const DEFAULT_WHITELIST: &str = "http://127.0.0.1:8081";
        let adapter_url =
            std::env::var("LMDB2GRAPHQL_URL").unwrap_or_else(|_| DEFAULT_ADAPTER.to_string());
        let whitelist_url =
            std::env::var("WHITELIST_URL").unwrap_or_else(|_| DEFAULT_WHITELIST.to_string());

        let config = stub_config(adapter_url.clone(), whitelist_url);
        let (_dir, path) = temp_store();

        block_on(async {
            // Probe the adapter first: any transport-level unreachability self-skips
            // (D-07) rather than failing CI.
            let client = GraphQlClient::new(adapter_url.clone());
            match client.authors(None, 1).await {
                Ok(_) => {}
                Err(ClientError::Unavailable) | Err(ClientError::Transport(_)) => {
                    eprintln!(
                        "live_run_self_skipping: live adapter unreachable at {adapter_url} \
                         — D-07 deferred to manual check"
                    );
                    return;
                }
                Err(e) => panic!("unexpected non-transport error probing adapter: {e:?}"),
            }

            // Reachable: drive the full end-to-end batch. run_batch enumerates the
            // live keyspace, fetches kind-1 ~100 events/pubkey, scores, and persists.
            let store = Arc::new(Store::open(&path).expect("open store"));
            let run_id = match run_batch(store, &config, false, None).await {
                Ok(rid) => rid,
                // A mid-run transport blip on the live adapter degrades to a deferred
                // manual check (D-07) — never a CI failure.
                Err(RunError::Client(ClientError::Unavailable))
                | Err(RunError::Client(ClientError::Transport(_)))
                | Err(RunError::Enumerate(EnumerateError::Client(ClientError::Unavailable)))
                | Err(RunError::Enumerate(EnumerateError::Client(ClientError::Transport(_)))) => {
                    eprintln!(
                        "live_run_self_skipping: live adapter blipped mid-run at {adapter_url} \
                         — D-07 deferred to manual check"
                    );
                    return;
                }
                Err(e) => panic!("run_batch returned an unexpected error: {e:?}"),
            };

            // The live run scored ≥1 pubkey and is marked done.
            let conn = rusqlite::Connection::open(&path).expect("reader");
            let n_score: i64 = conn
                .query_row(
                    "SELECT count(*) FROM score WHERE run_id = ?1",
                    [run_id],
                    |r| r.get(0),
                )
                .unwrap();
            assert!(
                n_score >= 1,
                "a reachable live run must score ≥1 pubkey (got {n_score})"
            );
            let status: String = conn
                .query_row(
                    "SELECT status FROM run WHERE run_id = ?1",
                    [run_id],
                    |r| r.get(0),
                )
                .unwrap();
            assert_eq!(status, "done", "the live run is marked done");
            eprintln!(
                "live_run_self_skipping: live run {run_id} scored {n_score} pubkeys (D-07 OK)"
            );
        });
    }
}
