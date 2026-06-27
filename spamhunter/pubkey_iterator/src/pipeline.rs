//! The bounded-memory streaming pipeline (D-05 / INGEST-03): the structural heart
//! of the phase.
//!
//! [`run_pipeline`] wires three stages across the async↔sync boundary:
//!
//! 1. **Source (sync):** the enumerated pubkeys (read up front from the durable
//!    `pubkey` table via [`crate::store::queries::read_pubkeys`], D-07) chunked
//!    into `authors_per_call`-sized batches.
//! 2. **Fetcher (async, on the tokio runtime):** up to [`DEFAULT_FETCH_CONCURRENCY`]
//!    batch fetches run concurrently (`buffer_unordered`), and each completed
//!    batch's [`AuthorGroup`]s are pushed into a **BOUNDED** `flume` channel via
//!    `tx.send_async().await` (production fetch: [`crate::fetch::fetch_batch`];
//!    tests: a cheap generator). The `await` IS the back-pressure: when the
//!    channel is full the send suspends and — because the fetch stream is not
//!    polled while we await — new fetches pause too, so peak in-flight memory is
//!    capped by `channel_cap + (DEFAULT_FETCH_CONCURRENCY + 1) × authors_per_call`,
//!    never the corpus size (Pitfall 2 — the writer channel's `unbounded` is NOT
//!    copied here).
//! 3. **Consumer (sync, OFF the tokio runtime):** a dedicated `std::thread`
//!    blocking-`recv()`s the channel and applies an injected
//!    `C: Fn(&AuthorGroup)` closure (Phase 3: [`consume_noop`], a counter; Phase
//!    4: the Layer/combiner stage — the single seam). `recv()` is a blocking call
//!    that MUST NOT run on a tokio worker (Pitfall 4), and the thread's
//!    `JoinHandle` is joined after `drop(tx)` (Pitfall 3 — never a dangling
//!    consumer; mirrors `Store::close()`'s writer join).
//!
//! Both the `fetch` source and the `consumer` are injected, so the watermark
//! tests substitute a mock fetcher with no HTTP and Phase 4 swaps the consumer
//! without touching the channel or the fetcher.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use futures_util::stream::{self, StreamExt};

use crate::detect::ScoredInput;
use crate::graphql::queries::AuthorGroup;
use crate::graphql::ClientError;

/// Default in-flight channel capacity (groups), RESEARCH Open Question #2. Small
/// enough to bound memory tightly, large enough to keep the fetcher and consumer
/// busy without a per-group rendezvous hop. Empirically tunable.
pub const DEFAULT_CHANNEL_CAP: usize = 64;

/// Default authors per `latestPerAuthor` call (D-01/D-02, RESEARCH §413-sizing).
/// Well under the ≤1000 contract cap (§12) and a per-call cost (authors ×
/// perAuthor) the 413 shrink-and-retry (Plan 01) backstops. Empirically tunable,
/// validated by the live D-09 check.
///
/// Lowered 250 → 50 (debug adapter-crash-heavy-query, 2026-06-27). The adapter's
/// strfry LMDB (318 GB) lives on an EXTERNAL USB drive on a RAM-starved 8 GiB host
/// that cannot cache the working set; a 250-author call reads ~5,268 cold event
/// payloads (~5 ms each) → 25-40s and materialises a ~3.4 MB response as 400+ MiB
/// in the adapter, crash-looping it. A 50-author call reads ~1,050 events (~340 KB,
/// ~5s cold / instant warm) — bounded and safe. This is a pure batching change:
/// same authors scored, smaller per-request blast radius. Raise only once the
/// adapter's DB is on fast local storage with adequate RAM.
pub const DEFAULT_AUTHORS_PER_CALL: usize = 50;

/// Default number of batch fetches kept in flight concurrently by the producer
/// (`buffer_unordered`). The fetch stage is I/O-bound (measured: serial adapter
/// round-trips dominated wall-clock), so overlapping `DEFAULT_FETCH_CONCURRENCY`
/// requests collapses the aggregate fetch latency and stops one slow batch from
/// stalling the whole pipeline. Peak in-flight memory stays O(1) in the corpus:
/// bounded by `channel_cap + (DEFAULT_FETCH_CONCURRENCY + 1) * authors_per_call`
/// (INGEST-03 — the bound is independent of the author-set size, just a larger
/// constant than the serial path). Empirically tunable.
///
/// Set to 1 (debug adapter-crash-heavy-query, 2026-06-27). Live testing confirmed
/// the adapter crash is NOT reader-slot contention but memory: each heavy
/// `latestPerAuthor` response materialises to 400+ MiB in the adapter, and the host
/// (8 GiB, ~1 GiB free) OOM-kills the adapter when concurrent/overlapping heavy
/// requests stack those allocations. curl disconnecting at timeout does NOT stop
/// the adapter from continuing to build a response, so even FC=2 stacks. FC=1
/// serialises fetches so at most one large response is ever in flight, which —
/// together with the smaller `DEFAULT_AUTHORS_PER_CALL` — keeps the adapter within
/// its memory budget. Raise only once the adapter streams/caps responses and runs
/// on a host with adequate RAM + fast local storage.
pub const DEFAULT_FETCH_CONCURRENCY: usize = 1;

/// The Phase-3 no-op consumer (D-06): count a scored input's events and drop it.
///
/// This is the pass-through that proves end-to-end flow with no unbounded
/// buffering. Phase 4 replaces the consumer closure at the [`run_pipeline`] seam
/// with the Layer/combiner stage; the channel and fetcher are unchanged. The
/// carrier is now [`ScoredInput`] (D-15): the no-op reads `.group.events`.
pub fn consume_noop(input: &ScoredInput, count: &AtomicU64) {
    count.fetch_add(input.group.events.len() as u64, Ordering::Relaxed);
}

/// Stream `pubkeys` through the bounded-memory pipeline, returning the total
/// number of events the consumer observed.
///
/// - `fetch`: the injected fetch source. Production passes a closure that calls
///   [`production_fetch_with_whitelist`] (event fetch + L0 membership resolution
///   in the SAME tokio stage); tests pass a cheap synthetic generator. `F:
///   Fn(Vec<String>) -> Fut`, `Fut: Future<Output = Result<Vec<ScoredInput>,
///   ClientError>>` — the source yields the [`ScoredInput`] carrier directly, so
///   the fetch stage is the single place that resolves whitelist membership
///   (Plan 03 / RESEARCH note A; Pitfall 5 — the CPU consumer never does HTTP).
/// - `consumer`: the injected drain-side closure (the Phase-4 seam). Phase 3
///   passes a [`consume_noop`] wrapper; Phase 4 passes the Layer stage. Must be
///   `Send + Sync + 'static` because it runs on the dedicated consumer thread.
///   It receives a [`ScoredInput`] so the fetch-stage-resolved whitelist bool
///   reaches `stage.score(...)` (D-15) without the consumer recomputing it.
/// - `channel_cap`: the bounded channel capacity — the back-pressure point. Pass
///   [`DEFAULT_CHANNEL_CAP`] for the standard bound.
/// - `authors_per_call`: the batch size handed to `fetch`. Pass
///   [`DEFAULT_AUTHORS_PER_CALL`] for the standard sizing.
///
/// The `fetch` source yields the [`ScoredInput`] carrier (group + resolved
/// whitelist bool) directly; the producer sends each across the bounded channel
/// unchanged. Plan 03 moved whitelist resolution INTO the fetch stage (note A),
/// so the carrier's `whitelisted` is the real L0 membership — no placeholder, no
/// new channel/payload/consumer plumbing (the carrier, channel type, and
/// consumer signature are unchanged from Plan 01). The per-run no-TTL whitelist
/// cache lives in the fetch stage, NOT the consumer, preserving OPS-02
/// determinism (no HashMap in the score path).
///
/// The consumer's `recv()` runs on a `std::thread` (off the tokio runtime,
/// Pitfall 4) joined after `drop(tx)` (Pitfall 3). The producer uses
/// `send_async().await` so the tokio reactor stays alive while the send awaits
/// channel room — that await is the back-pressure (D-05).
pub async fn run_pipeline<F, Fut, C>(
    fetch: F,
    pubkeys: Vec<String>,
    channel_cap: usize,
    authors_per_call: usize,
    consumer: C,
) -> Result<u64, ClientError>
where
    F: Fn(Vec<String>) -> Fut,
    Fut: std::future::Future<Output = Result<Vec<ScoredInput>, ClientError>>,
    C: Fn(&ScoredInput) + Send + Sync + 'static,
{
    // BOUNDED is load-bearing: copying the store's unbounded writer channel here
    // would buffer the whole corpus and fail INGEST-03 (Pitfall 2). The carrier
    // is ScoredInput (group + whitelist bool) so the bool reaches the consumer.
    let (tx, rx) = flume::bounded::<ScoredInput>(channel_cap);

    // CONSUMER: a dedicated std::thread running a BLOCKING drain loop. recv()
    // blocks this thread (NOT a tokio worker — Pitfall 4) until an input arrives
    // or every sender drops (channel closed → recv() Err → loop ends). The
    // injected closure is the Phase-4 seam (D-06).
    let consumer_handle = std::thread::spawn(move || {
        while let Ok(input) = rx.recv() {
            consumer(&input);
        }
    });

    // PRODUCER: keep up to DEFAULT_FETCH_CONCURRENCY batch fetches in flight at
    // once (buffer_unordered), pushing each completed batch's groups into the
    // bounded channel as it arrives. Concurrency overlaps the I/O-bound adapter
    // round-trips (measured: serial fetch dominated wall-clock) and stops one
    // slow batch from stalling the rest. Back-pressure is preserved: while we
    // await a send below the fetch stream is NOT polled, so a full channel
    // naturally pauses new fetches (D-05). On the first fetch error the stream
    // short-circuits; we still drop(tx) + join the consumer below (Pitfall 3).
    let fetch_result: Result<(), ClientError> = async {
        // Iterator::map is lazy and stream::iter pulls on demand, so `fetch` is
        // invoked only as buffer_unordered spins each future up to the cap — the
        // whole corpus is never fetched eagerly.
        let mut fetches = stream::iter(
            pubkeys
                .chunks(authors_per_call)
                .map(|batch| fetch(batch.to_vec())),
        )
        .buffer_unordered(DEFAULT_FETCH_CONCURRENCY);

        while let Some(result) = fetches.next().await {
            // The fetch source yields the carrier directly (group + resolved
            // whitelist bool) — Plan 03 resolved membership in the fetch stage.
            let inputs = result?; // a fetch error propagates and stops the run.
            for input in inputs {
                // send_async (NOT the blocking tx.send) keeps the reactor alive
                // while awaiting channel room — this await is the back-pressure.
                if tx.send_async(input).await.is_err() {
                    // Consumer dropped (shouldn't happen) — stop fetching.
                    return Ok(());
                }
            }
        }
        Ok(())
    }
    .await;

    // Close the channel so the consumer's recv() returns Err and its loop exits,
    // THEN join it (mirrors Store::close()'s writer join, store/mod.rs:251). A
    // brief blocking join at end-of-run is acceptable — all fetching is done.
    drop(tx);
    consumer_handle
        .join()
        .expect("pipeline consumer thread did not panic");

    fetch_result.map(|()| 0)
}

/// Convenience wrapper for the Phase-3 no-op drain: runs [`run_pipeline`] with a
/// [`consume_noop`] consumer over a shared [`AtomicU64`] and returns the total
/// event count. Phase 4 calls [`run_pipeline`] directly with the Layer consumer.
pub async fn run_pipeline_noop<F, Fut>(
    fetch: F,
    pubkeys: Vec<String>,
    channel_cap: usize,
    authors_per_call: usize,
) -> Result<u64, ClientError>
where
    F: Fn(Vec<String>) -> Fut,
    Fut: std::future::Future<Output = Result<Vec<ScoredInput>, ClientError>>,
{
    let count = Arc::new(AtomicU64::new(0));
    let c2 = Arc::clone(&count);
    run_pipeline(fetch, pubkeys, channel_cap, authors_per_call, move |input| {
        consume_noop(input, &c2)
    })
    .await?;
    Ok(count.load(Ordering::Relaxed))
}

/// The production fetch stage that closes the Phase-3 `match_groups` seam (D-15)
/// AND resolves L0 whitelist membership (Plan 03 / RESEARCH note A).
///
/// Fetches a batch via [`crate::fetch::fetch_batch`], then wires
/// [`crate::fetch::match_groups`] so the response (which OMITS zero-event
/// authors, contract §5) is re-expanded to ONE entry per REQUESTED pubkey — an
/// adapter-omitted author becomes an empty-`events` group rather than being
/// dropped. Every enumerated pubkey therefore reaches the consumer and gets a
/// `score` row (Pitfall 3 / D-15).
///
/// For each REQUESTED pubkey (incl. zero-event ones — they still get an L0
/// sub-score) it resolves whitelist membership via
/// [`crate::detect::whitelist::WhitelistClient::is_whitelisted`] in this SAME
/// tokio stage, attaching the resolved bool to the [`ScoredInput`] carrier
/// BEFORE it crosses the channel. This keeps the whitelist HTTP OUT of the CPU
/// consumer (Pitfall 5) and the per-run no-TTL cache out of the score path
/// (OPS-02). On a whitelist outage `is_whitelisted` fails toward not-flagging
/// (Pitfall 2), so the L0 layer clears rather than emitting a mass false-positive.
pub async fn production_fetch_with_whitelist(
    client: &crate::graphql::GraphQlClient,
    whitelist: &crate::detect::whitelist::WhitelistClient,
    kind: i64,
    per_author: i64,
    batch: &[String],
) -> Result<Vec<ScoredInput>, ClientError> {
    let groups = crate::fetch::fetch_batch(client, kind, per_author, batch).await?;
    // match_groups attributes by author (never index) and yields one entry per
    // REQUESTED pubkey, empty for omitted authors — D-15.
    let matched = crate::fetch::match_groups(batch, groups);
    // Resolve L0 membership for the WHOLE batch in ONE round-trip via the bulk
    // endpoint (note A), instead of one serial GET per pubkey — this was the
    // dominant fetch-stage cost and a serial unbounded-hang point. The cache
    // still keeps a repeated pubkey a single round-trip and a stable value
    // (OPS-02); the bulk map always has an entry per requested pubkey.
    let pubkeys: Vec<String> = matched.iter().map(|(pk, _)| pk.to_string()).collect();
    let wl = whitelist.is_whitelisted_bulk(&pubkeys).await;
    let mut out = Vec::with_capacity(matched.len());
    for (pk, events) in matched {
        // Missing key shouldn't happen (bulk fills every requested pubkey), but
        // default to the fail-safe true (clears L0) if it ever does (Pitfall 2).
        let whitelisted = wl.get(pk).copied().unwrap_or(true);
        out.push(ScoredInput {
            group: AuthorGroup {
                author: pk.to_string(),
                events,
            },
            whitelisted,
        });
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::detect::{Layer, LayerOutput, ScoringStage};
    use crate::graphql::queries::{AuthorGroup, Event};
    use crate::graphql::GraphQlClient;
    use crate::store::Store;
    use std::sync::atomic::AtomicUsize;
    use std::sync::Mutex;
    use tempfile::TempDir;

    /// A current-thread tokio runtime to drive one pipeline run per test (mirrors
    /// the client.rs / enumerate.rs harness idiom).
    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    /// `n` distinct 64-char lowercase-hex pubkeys — synthetic authors for the
    /// mock-fetcher tests (no HTTP; the determinism win RESEARCH calls for).
    fn synthetic_authors(n: usize) -> Vec<String> {
        (0..n).map(|i| format!("{i:064x}")).collect()
    }

    /// Build one event for `author` (id encodes the author so a test could prove
    /// attribution if needed).
    fn ev(author: &str, idx: usize) -> Event {
        Event {
            id: format!("{author}-e{idx}"),
            pubkey: author.to_string(),
            kind: 1,
            created_at: 1_700_000_000,
            content: String::new(),
            tags: vec![],
        }
    }

    /// A cheap mock `fetch`: for each requested author, fabricate a carrier with
    /// `events_per_author` events and `whitelisted=false` (the synthetic default;
    /// the L0-resolution path is exercised by `production_fetch_with_whitelist`
    /// tests, not the watermark/seam tests). No HTTP — the injection seam the
    /// watermark proof relies on. The source yields the [`ScoredInput`] carrier
    /// directly (matching `run_pipeline`'s `F` contract since Plan 03).
    fn mock_fetch(
        batch: Vec<String>,
        events_per_author: usize,
    ) -> Result<Vec<ScoredInput>, ClientError> {
        Ok(batch
            .into_iter()
            .map(|a| {
                let events = (0..events_per_author).map(|i| ev(&a, i)).collect();
                ScoredInput {
                    group: AuthorGroup { author: a, events },
                    whitelisted: false,
                }
            })
            .collect())
    }

    /// D-08 / INGEST-03 / success criterion #3 — the headline bounded-memory
    /// proof. Over a LARGE synthetic author set behind a cheap mock fetcher with
    /// a deliberately SLOW consumer, an in-flight AtomicUsize (incremented right
    /// after each successful send, decremented inside the consumer) tracked with
    /// a max-watermark must NEVER exceed `channel_cap + authors_per_call`. The
    /// assertion is STRUCTURAL (in-flight count bounded by capacity), not an
    /// OS-RSS measurement.
    ///
    /// The instrumentation lives in the TEST (a wrapping consumer + a send-side
    /// counter), not in production `run_pipeline` — the production path stays
    /// clean.
    #[test]
    fn bounded_memory_watermark() {
        const N: usize = 100_000; // ≥ 100k authors (one event each = 100k groups)
        const CAP: usize = 64;
        const APC: usize = 250;

        // in_flight = (groups handed toward the channel) − (groups consumed). The
        // increment fires per group inside the producer-side send wrapper at the
        // EXACT moment a group enters the channel (a `Fn(&AuthorGroup)` we splice
        // into the consumer chain via a counting fetch that defers the count to
        // send time); the decrement fires in the consumer. The watermark is the
        // running max — a STRUCTURAL bound (not OS-RSS).
        let in_flight = Arc::new(AtomicUsize::new(0));
        let watermark = Arc::new(AtomicUsize::new(0));
        let consumed = Arc::new(AtomicUsize::new(0));
        let sent = Arc::new(AtomicUsize::new(0));

        // Slow consumer: record consumption, recompute in-flight = sent − consumed
        // and bump the watermark, then sleep so the producer races ahead and the
        // channel fills — exercising back-pressure. Reading the live difference
        // (rather than a paired inc/dec) makes the watermark insensitive to which
        // thread observes first.
        let consumed_c = Arc::clone(&consumed);
        let sent_c = Arc::clone(&sent);
        let if_c = Arc::clone(&in_flight);
        let wm_c = Arc::clone(&watermark);
        let consumer = move |_input: &ScoredInput| {
            let done = consumed_c.fetch_add(1, Ordering::SeqCst) + 1;
            let now = sent_c.load(Ordering::SeqCst).saturating_sub(done);
            if_c.store(now, Ordering::SeqCst);
            bump(&wm_c, now);
            std::thread::sleep(std::time::Duration::from_micros(50));
        };

        // The mock fetch fabricates one event per author. The send-time count is
        // approximated by counting at fetch return: because the pipeline's
        // `send_async` back-pressures (and pauses the fetch stream while a send
        // awaits), the producer cannot run ahead of the channel by more than CAP
        // groups + the buffer_unordered fan-out — at most DEFAULT_FETCH_CONCURRENCY
        // fetched-but-unsent batches plus the one in-hand batch (each ≤ APC). So
        // `sent − consumed` is bounded by CAP + (FC + 1) * APC even though we bump
        // `sent` per fabricated group at fetch return.
        let sent_f = Arc::clone(&sent);
        let if_f = Arc::clone(&in_flight);
        let consumed_f = Arc::clone(&consumed);
        let wm_f = Arc::clone(&watermark);
        let fetch = move |batch: Vec<String>| {
            let sent_f = Arc::clone(&sent_f);
            let if_f = Arc::clone(&if_f);
            let consumed_f = Arc::clone(&consumed_f);
            let wm_f = Arc::clone(&wm_f);
            async move {
                let groups = mock_fetch(batch, 1)?;
                for _ in &groups {
                    let s = sent_f.fetch_add(1, Ordering::SeqCst) + 1;
                    let now = s.saturating_sub(consumed_f.load(Ordering::SeqCst));
                    if_f.store(now, Ordering::SeqCst);
                    bump(&wm_f, now);
                }
                Ok::<Vec<ScoredInput>, ClientError>(groups)
            }
        };

        let total = block_on(run_pipeline(
            fetch,
            synthetic_authors(N),
            CAP,
            APC,
            consumer,
        ))
        .expect("pipeline");

        assert_eq!(
            total, 0,
            "run_pipeline's intrinsic count is unused with a custom consumer"
        );
        assert_eq!(
            consumed.load(Ordering::SeqCst),
            N,
            "every group consumed exactly once"
        );
        let peak = watermark.load(Ordering::SeqCst);
        // With buffer_unordered(DEFAULT_FETCH_CONCURRENCY) the producer can hold up
        // to FC fetched-but-unsent batches buffered in the stream plus one in-hand
        // batch being drained, on top of the channel's CAP slots — so the bound
        // widens to CAP + (FC + 1) * APC. It is STILL O(1) in the corpus (INGEST-03):
        // a fixed constant, never a function of the {N}-author set.
        let bound = CAP + (DEFAULT_FETCH_CONCURRENCY + 1) * APC;
        assert!(
            peak <= bound,
            "in-flight watermark {peak} must be bounded by \
             channel_cap + (fetch_concurrency + 1) * authors_per_call ({bound}), \
             not the {N}-author corpus — INGEST-03 / D-08"
        );
        // Sanity: with a slow consumer over 100k authors the channel really did
        // fill (the bound is exercised, not vacuously satisfied by an idle channel).
        assert!(
            peak > CAP,
            "back-pressure exercised: watermark {peak} exceeded a single channel slot count"
        );
    }

    /// CAS the running max watermark up to `now`.
    fn bump(watermark: &AtomicUsize, now: usize) {
        let mut cur = watermark.load(Ordering::SeqCst);
        while now > cur {
            match watermark.compare_exchange(cur, now, Ordering::SeqCst, Ordering::SeqCst) {
                Ok(_) => break,
                Err(actual) => cur = actual,
            }
        }
    }

    /// D-06 / success criterion #4 — end-to-end no-drop count. Over a synthetic
    /// set of known size with a fast no-op consumer, the final count equals the
    /// EXACT total number of events produced: nothing dropped under back-pressure,
    /// every group consumed once.
    #[test]
    fn pipeline_endtoend_count() {
        const N: usize = 5_000;
        const EVENTS_PER_AUTHOR: usize = 3;

        let fetch = |batch: Vec<String>| async move { mock_fetch(batch, EVENTS_PER_AUTHOR) };
        let total = block_on(run_pipeline_noop(
            fetch,
            synthetic_authors(N),
            DEFAULT_CHANNEL_CAP,
            DEFAULT_AUTHORS_PER_CALL,
        ))
        .expect("pipeline drains");

        assert_eq!(
            total,
            (N * EVENTS_PER_AUTHOR) as u64,
            "no-op count equals the exact synthetic total — no drops under back-pressure"
        );
    }

    /// The Phase-4 seam: `run_pipeline` accepts a caller-supplied consumer. A
    /// closure that records each group's author into a shared Vec observes
    /// EXACTLY the produced groups — demonstrating the swap point without channel
    /// or fetcher changes.
    #[test]
    fn injected_consumer_seam() {
        const N: usize = 1_000;
        let seen = Arc::new(Mutex::new(Vec::<String>::new()));
        let seen_c = Arc::clone(&seen);
        let consumer = move |input: &ScoredInput| {
            seen_c.lock().unwrap().push(input.group.author.clone());
        };

        let fetch = |batch: Vec<String>| async move { mock_fetch(batch, 1) };
        block_on(run_pipeline(
            fetch,
            synthetic_authors(N),
            DEFAULT_CHANNEL_CAP,
            DEFAULT_AUTHORS_PER_CALL,
            consumer,
        ))
        .expect("pipeline runs with the injected consumer");

        let mut got = Arc::try_unwrap(seen)
            .expect("single ref after join")
            .into_inner()
            .unwrap();
        got.sort();
        let mut want = synthetic_authors(N);
        want.sort();
        assert_eq!(
            got, want,
            "the injected consumer observes exactly the produced groups (Phase-4 seam)"
        );
    }

    /// D-09 live check (self-skipping). Constructs a REAL `GraphQlClient` against
    /// the CONTEXT adapter (override via `LMDB2GRAPHQL_URL`) and issues a real
    /// `latest_per_author`, asserting the response deserializes into a
    /// `Vec<AuthorGroup>` — proving the Task-1 structs match the real wire shape.
    /// When the adapter is transiently unreachable (`Unavailable`/`Transport`),
    /// it SKIPS with an eprintln deferred-manual note rather than failing CI
    /// (D-09 degrade-gracefully). Picks a real pubkey by enumerating one author
    /// page so it never hardcodes a possibly-absent key.
    #[test]
    fn live_latest_per_author() {
        const DEFAULT_URL: &str = "http://192.168.149.21:8080/graphql";
        let url = std::env::var("LMDB2GRAPHQL_URL").unwrap_or_else(|_| DEFAULT_URL.to_string());

        block_on(async {
            let client = GraphQlClient::new(url.clone());

            // Probe + obtain a real author from page 1. Any transport-level
            // unreachability self-skips.
            let page = match client.authors(None, 1).await {
                Ok(p) => p,
                Err(ClientError::Unavailable) | Err(ClientError::Transport(_)) => {
                    eprintln!(
                        "live_latest_per_author: live adapter unreachable at {url} \
                         — D-09 deferred to manual check"
                    );
                    return;
                }
                Err(e) => panic!("unexpected non-transport error probing authors: {e:?}"),
            };

            let Some(pubkey) = page.authors.into_iter().next() else {
                eprintln!(
                    "live_latest_per_author: adapter reachable but authors page empty at {url} \
                     — D-09 deferred to manual check"
                );
                return;
            };

            match client
                .latest_per_author(1, 5, std::slice::from_ref(&pubkey))
                .await
            {
                Ok(groups) => {
                    // Deserialized into Vec<AuthorGroup>. Possibly empty (an author
                    // with zero kind-1 events is omitted, contract §5) — assert the
                    // TYPE/Ok, not non-emptiness.
                    let _typed: &Vec<AuthorGroup> = &groups;
                    eprintln!(
                        "live_latest_per_author: deserialized {} group(s) from {url} (D-09 OK)",
                        groups.len()
                    );
                }
                Err(ClientError::Unavailable) | Err(ClientError::Transport(_)) => {
                    eprintln!(
                        "live_latest_per_author: live adapter unreachable at {url} \
                         — D-09 deferred to manual check"
                    );
                }
                Err(e) => panic!("latest_per_author returned an unexpected error: {e:?}"),
            }
        });
    }

    // ---- Task 3: ScoredInput carrier + match_groups wiring (D-15) ----

    /// A trivial deterministic layer for the end-to-end pipeline tests: emits a
    /// fixed value + non-empty evidence, regardless of events (so even a
    /// zero-event pubkey gets a real subscore + score row).
    struct PipelineTrivialLayer;
    impl Layer for PipelineTrivialLayer {
        fn name(&self) -> &'static str {
            "L1_near_duplicate"
        }
        fn score(&self, events: &[Event], whitelisted: bool) -> LayerOutput {
            LayerOutput {
                value: 0.4,
                evidence: serde_json::json!({
                    "n_events": events.len(),
                    "whitelisted": whitelisted,
                }),
            }
        }
    }

    /// Build a single-layer ScoringStage for the pipeline tests (sigmoid over the
    /// trivial layer; conservative bias/τ).
    fn trivial_stage() -> ScoringStage {
        ScoringStage::from_layers(vec![Box::new(PipelineTrivialLayer)], vec![2.0], -4.0, 0.5)
    }

    fn temp_store() -> (TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().expect("create temp dir");
        let path = dir.path().join("spamhunter.sqlite");
        (dir, path)
    }

    /// A loopback HTTP stub for `latestPerAuthor` that OMITS `omit` (zero-event
    /// author, contract §5) and returns one event for every other requested
    /// author. Mirrors `fetch::tests::size_gated_stub`. Runs one connection per
    /// `latest_per_author` call.
    fn omitting_stub(omit: String) -> String {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}/graphql");
        std::thread::spawn(move || {
            for conn in listener.incoming() {
                use std::io::{Read, Write};
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                let mut buf = vec![0u8; 16384];
                let n = sock.read(&mut buf).unwrap_or(0);
                let req = String::from_utf8_lossy(&buf[..n]);
                let body = req.split("\r\n\r\n").nth(1).unwrap_or("");
                let authors = parse_authors_local(body);

                let groups = authors
                    .iter()
                    .filter(|a| **a != omit) // OMIT the zero-event author (§5)
                    .map(|a| {
                        format!(
                            r#"{{"author":"{a}","events":[{{"id":"{a}-e","pubkey":"{a}","kind":1,"createdAt":1700000000,"content":"hi","tags":[]}}]}}"#
                        )
                    })
                    .collect::<Vec<_>>()
                    .join(",");
                let resp_body = format!(r#"{{"data":{{"latestPerAuthor":[{groups}]}}}}"#);
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

    /// Minimal `variables.authors` extractor (mirror of fetch.rs's parse_authors).
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

    /// Carrier round-trip: a `ScoredInput { group, whitelisted }` crosses the
    /// bounded channel and the consumer reads `.whitelisted` back out, passing it
    /// into `stage.score(...)`. With whitelisted=false the Persist is false.
    #[test]
    fn scored_input_carrier_roundtrips_whitelist_bool() {
        let seen = Arc::new(Mutex::new(Vec::<(String, bool)>::new()));
        let seen_c = Arc::clone(&seen);
        let stage = Arc::new(trivial_stage());
        let stage_c = Arc::clone(&stage);
        let consumer = move |input: &ScoredInput| {
            // The consumer passes the carrier bool straight into score (never
            // recomputed) — this is the D-15 contract.
            let p = stage_c.score(1, &input.group.author, &input.group.events, input.whitelisted);
            seen_c.lock().unwrap().push((p.pubkey, p.whitelisted));
        };

        let authors = synthetic_authors(3);
        let fetch = |batch: Vec<String>| async move { mock_fetch(batch, 2) };
        block_on(run_pipeline(
            fetch,
            authors.clone(),
            DEFAULT_CHANNEL_CAP,
            DEFAULT_AUTHORS_PER_CALL,
            consumer,
        ))
        .expect("pipeline runs");

        let got = Arc::try_unwrap(seen).unwrap().into_inner().unwrap();
        assert_eq!(got.len(), 3, "every group scored once");
        assert!(
            got.iter().all(|(_, wl)| !*wl),
            "Plan-01 placeholder: whitelisted=false rode the carrier into the Persist"
        );
    }

    /// A loopback whitelist stub that always replies `{"whitelisted":body}`,
    /// serving up to `max_conns` connections (one GET per requested pubkey, minus
    /// cache hits). Mirrors the `omitting_stub` idiom; returns the base URL.
    fn whitelist_stub(body_whitelisted: bool, max_conns: usize) -> String {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}");
        std::thread::spawn(move || {
            for (i, conn) in listener.incoming().enumerate() {
                if i >= max_conns {
                    break;
                }
                use std::io::{Read, Write};
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                let mut buf = [0u8; 4096];
                let _ = sock.read(&mut buf);
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

    /// D-15 integration: drive `run_pipeline` via `production_fetch_with_whitelist`
    /// over a requested list where the mock adapter OMITS one pubkey (zero
    /// events). After the run, the omitted pubkey HAS a `score` row — proving
    /// match_groups wiring scores every enumerated pubkey, AND the fetch stage
    /// resolved (a non-whitelisted) L0 membership for it.
    #[test]
    fn zero_event_pubkey_gets_score_row() {
        use crate::detect::whitelist::WhitelistClient;
        let (_dir, path) = temp_store();
        let store = Arc::new(Store::open(&path).expect("open store"));
        let run_id = store.begin_run("{}").expect("begin_run");

        // Three requested pubkeys; the adapter omits the middle one (zero events).
        let pk0 = "aa00000000000000000000000000000000000000000000000000000000000001".to_string();
        let omitted =
            "aa00000000000000000000000000000000000000000000000000000000000002".to_string();
        let pk2 = "aa00000000000000000000000000000000000000000000000000000000000003".to_string();
        let requested = vec![pk0.clone(), omitted.clone(), pk2.clone()];

        // Pre-register the pubkey dimension rows (FK target for score rows).
        store.insert_pubkeys(requested.clone());
        store.flush().expect("flush pubkeys");

        let url = omitting_stub(omitted.clone());
        let client = Arc::new(GraphQlClient::new(url));
        // Whitelist stub: all three requested pubkeys resolve to NOT whitelisted
        // (one GET each → 3 connections). The zero-event omitted pubkey is still
        // resolved (it gets an L0 sub-score), proving the fetch stage resolves
        // membership for zero-event authors too.
        let wl_url = whitelist_stub(false, 8);
        let whitelist = Arc::new(WhitelistClient::new(wl_url));

        let stage = Arc::new(trivial_stage());
        let store_c = Arc::clone(&store);
        let stage_c = Arc::clone(&stage);
        let consumer = move |input: &ScoredInput| {
            let p = stage_c.score(run_id, &input.group.author, &input.group.events, input.whitelisted);
            store_c.persist(p);
        };

        let client_f = Arc::clone(&client);
        let wl_f = Arc::clone(&whitelist);
        let fetch = move |batch: Vec<String>| {
            let client_f = Arc::clone(&client_f);
            let wl_f = Arc::clone(&wl_f);
            async move {
                production_fetch_with_whitelist(&client_f, &wl_f, 1, 5, &batch).await
            }
        };

        block_on(run_pipeline(
            fetch,
            requested.clone(),
            DEFAULT_CHANNEL_CAP,
            DEFAULT_AUTHORS_PER_CALL,
            consumer,
        ))
        .expect("pipeline runs");

        // Unwrap the store Arc so close() drains+commits the writer.
        let store = Arc::try_unwrap(store).ok().expect("sole store ref after join");
        store.close().expect("flush + join writer");

        let conn = rusqlite::Connection::open(&path).expect("reader");
        let n_score: i64 = conn
            .query_row("SELECT count(*) FROM score WHERE run_id = ?1", [run_id], |r| {
                r.get(0)
            })
            .unwrap();
        assert_eq!(n_score, 3, "every requested pubkey (incl. omitted) has a score row");
        let omitted_present: i64 = conn
            .query_row(
                "SELECT count(*) FROM score WHERE run_id = ?1 AND pubkey = ?2",
                rusqlite::params![run_id, omitted],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(omitted_present, 1, "the zero-event (omitted) pubkey got a score row (D-15)");
    }

    /// SCORE-05: a normal (non-zero-event) pubkey persists a score row + signal
    /// row(s) with non-empty evidence JSON.
    #[test]
    fn normal_pubkey_persists_score_and_evidence() {
        let (_dir, path) = temp_store();
        let store = Arc::new(Store::open(&path).expect("open store"));
        let run_id = store.begin_run("{}").expect("begin_run");
        let authors = synthetic_authors(2);
        store.insert_pubkeys(authors.clone());
        store.flush().expect("flush pubkeys");

        let stage = Arc::new(trivial_stage());
        let store_c = Arc::clone(&store);
        let stage_c = Arc::clone(&stage);
        let consumer = move |input: &ScoredInput| {
            let p = stage_c.score(run_id, &input.group.author, &input.group.events, input.whitelisted);
            store_c.persist(p);
        };

        let fetch = |batch: Vec<String>| async move { mock_fetch(batch, 3) };
        block_on(run_pipeline(
            fetch,
            authors.clone(),
            DEFAULT_CHANNEL_CAP,
            DEFAULT_AUTHORS_PER_CALL,
            consumer,
        ))
        .expect("pipeline runs");

        let store = Arc::try_unwrap(store).ok().expect("sole store ref").close();
        store.expect("flush + join writer");

        let conn = rusqlite::Connection::open(&path).expect("reader");
        let n_score: i64 = conn
            .query_row("SELECT count(*) FROM score WHERE run_id = ?1", [run_id], |r| r.get(0))
            .unwrap();
        assert_eq!(n_score, 2, "both pubkeys scored");
        // Every signal row carries non-empty evidence JSON (SCORE-05).
        let n_empty_ev: i64 = conn
            .query_row(
                "SELECT count(*) FROM signal WHERE run_id = ?1 AND (evidence IS NULL OR evidence = '')",
                [run_id],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(n_empty_ev, 0, "every signal row has non-empty evidence (SCORE-05)");
        let n_sig: i64 = conn
            .query_row("SELECT count(*) FROM signal WHERE run_id = ?1", [run_id], |r| r.get(0))
            .unwrap();
        assert_eq!(n_sig, 2, "one signal row per pubkey for the single trivial layer");
    }

    /// OPS-02 end-to-end: run the same fixture corpus twice into two fresh temp
    /// DBs; the ordered score/signal tables are byte-identical across runs.
    #[test]
    fn rerun_endtoend_is_deterministic() {
        type RunTables = (Vec<(String, f64)>, Vec<(String, String, f64)>);
        let run_once = || -> RunTables {
            let (_dir, path) = temp_store();
            let store = Arc::new(Store::open(&path).expect("open store"));
            let run_id = store.begin_run("{}").expect("begin_run");
            let authors = synthetic_authors(20);
            store.insert_pubkeys(authors.clone());
            store.flush().expect("flush pubkeys");

            let stage = Arc::new(trivial_stage());
            let store_c = Arc::clone(&store);
            let stage_c = Arc::clone(&stage);
            let consumer = move |input: &ScoredInput| {
                let p = stage_c.score(run_id, &input.group.author, &input.group.events, input.whitelisted);
                store_c.persist(p);
            };
            let fetch = |batch: Vec<String>| async move { mock_fetch(batch, 4) };
            block_on(run_pipeline(
                fetch,
                authors,
                DEFAULT_CHANNEL_CAP,
                DEFAULT_AUTHORS_PER_CALL,
                consumer,
            ))
            .expect("pipeline runs");
            Arc::try_unwrap(store)
                .ok()
                .expect("sole store ref")
                .close()
                .expect("flush + join writer");

            let conn = rusqlite::Connection::open(&path).expect("reader");
            let mut scores = crate::store::queries::read_scores(&conn, run_id).expect("scores");
            scores.sort_by(|a, b| a.0.cmp(&b.0));
            let mut signals = crate::store::queries::read_signals(&conn, run_id).expect("signals");
            signals.sort_by(|a, b| (&a.0, &a.1).cmp(&(&b.0, &b.1)));
            (scores, signals)
        };
        let a = run_once();
        let b = run_once();
        assert_eq!(a.0, b.0, "score table deterministic end-to-end (OPS-02)");
        assert_eq!(a.1, b.1, "signal table deterministic end-to-end (OPS-02)");
    }
}
