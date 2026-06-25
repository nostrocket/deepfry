# Phase 3: Fetcher + Bounded Streaming Pipeline - Research

**Researched:** 2026-06-25
**Domain:** Rust async/sync concurrency boundary — tokio I/O → bounded `flume` channel → `rayon` CPU stage; additive GraphQL query on the Phase-2 client.
**Confidence:** HIGH (everything is verifiable against in-repo code + the code-verified `contract.md` + registry-confirmed crate versions; the one open knob is the empirically-tunable authors-per-call number, treated as `[ASSUMED]` starting value).

## Summary

This phase adds **one new query** (`latestPerAuthor`) to the existing Phase-2 `GraphQlClient` (additive: a query const + serde structs + a thin wrapper, no transport rewrite — D-03), then wires a **bounded-memory streaming pipeline**: a tokio task fetches author batches via `latestPerAuthor`, pushes `AuthorGroup`s into a **bounded `flume` channel**, and a `rayon` thread-pool stage drains the channel and runs a no-op pass-through consumer (D-05/D-06). The whole point is back-pressure: when the rayon stage is slow, the bounded channel fills, `send`/`send_async` blocks the fetcher, and peak in-flight memory is capped by **channel capacity × batch size**, never the corpus size (INGEST-03, success criterion #3).

The two non-obvious landmines, both already flagged in CONTEXT and the contract: (1) `latestPerAuthor` **omits authors with zero matching events** (contract §5, §6.2, §8) — results MUST be matched back by the `author` field into a map keyed on the requested pubkey, never zipped by index (D-04, INGEST-04); and (2) `1000 authors × perAuthor:100 kind:1` events can blow past the **256 KiB body limit** (contract §12) — but 256 KiB is the *request* body cap, and a `413` is a *transport-layer* response (contract §7). The request body for `latestPerAuthor` is tiny (a query string + ~1000 × 64-char hex authors ≈ 65 KiB of authors), so a real `413` on this query is unlikely; the genuine risk is an oversized *response* and per-call cost (`authors × perAuthor` index scans, contract §6.2 "Cost awareness"). Recommend starting at **250 authors per call** and treating a `413` as the Phase-2 `ClientError::PayloadTooLarge` → shrink-batch-and-retry signal.

**Primary recommendation:** Add `rayon = "1.12"` (Phase-3-owned dep). Extend `src/graphql/queries.rs` + `client.rs` with `latestPerAuthor` exactly mirroring the existing `authors`/`stats` wrapper idiom. Build the pipeline as a `tokio::spawn` fetcher → `flume::bounded(N)` channel → a dedicated rayon `ThreadPool` whose worker drains via blocking `Receiver::recv()` (the sync side) while the fetcher pushes via `Sender::send_async().await` (the async side). The async↔sync boundary is the channel itself — no `spawn_blocking`, no `tokio::sync::Mutex` over the store.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| `latestPerAuthor` fetch (HTTP I/O) | tokio async task | reqwest client pool | I/O-bound; reuses the Phase-2 async reqwest transport (D-03). |
| author-batching + 413 shrink/retry | tokio async task | Phase-2 `retry` helper | batching is request-shaping; retry is the existing bounded-backoff policy. |
| author→pubkey matching | the fetch stage (post-decode) | — | match by `author` into a map *before* handing groups downstream (D-04). |
| back-pressure / bounded buffering | `flume::bounded` channel | — | the channel IS the back-pressure point (D-05). |
| no-op CPU analysis | rayon thread pool | — | CPU work off the tokio runtime (D-05/D-06); Phase-4 Layer stage plugs in here. |
| pubkey source (enumeration) | sync SQLite read (`Store::reader`) | Phase-2 `pubkey` table | read the persisted table; decouples fetch from the walk (D-07). |
| persistence | none this phase | — | read-only phase; no-op consumer persists nothing (CONTEXT in-scope). |

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D-01:** Fetch each pubkey's most-recent ~100 events via `latestPerAuthor(kind:1, perAuthor:100)`, batched at **≤1000 authors per call** (contract hard limit). `kind:1` (text notes) is the v1 target; the kind is a config value, not hardcoded magic.
- **D-02:** The authors-per-call batch size must keep the **response body ≤256 KiB** (contract §12). 1000 authors × 100 events may exceed 256 KiB — the planner/research picks a safe authors-per-call (Claude's discretion, e.g. start smaller and tune); a `413` is treated as "shrink the batch and retry" per the Phase-2 error taxonomy, never a hard failure.
- **D-03:** Reuse the **Phase-2 reusable GraphQL client** (D-11 from Phase 2): add `latestPerAuthor` as an additive query on the same client/transport. Same async reqwest/tokio transport, same two-layer error dispatch (HTTP status vs in-body `errors[]`/`extensions.code`).
- **D-04:** Fetched author groups are matched back to requested pubkeys **by the `author` field, never zipped by index**. Authors omitted from the response because they have zero matching kind-1 events are handled as "empty group" without misaligning the remaining results.
- **D-05:** Pipeline = **tokio fetch (I/O) → bounded channel → rayon analysis (CPU)**. The bounded channel (use the existing `flume` dep) is the back-pressure point: fetch blocks when the channel is full, so memory is bounded by **channel capacity, not corpus size**. CPU analysis runs off the tokio threads (rayon pool), never blocking the async runtime.
- **D-06:** The Phase-3 consumer is a **no-op / pass-through** rayon stage (counts groups, drops them). It proves end-to-end flow from enumeration → fetch → rayon with no unbounded buffering. Phase 4 swaps in the real Layer/combiner stage at this seam — design the stage boundary so that is additive.
- **D-07:** Enumeration source: read pubkeys from the **Phase-2 enumeration** (the persisted `pubkey` table is the recommended default — whether it re-walks live or reads the table is the planner's call; reading the persisted table decouples fetch from the walk).
- **D-08:** **Bounded-memory proof** is the headline test: run the pipeline over a **large synthetic author set** (mocked fetcher) and assert peak in-flight memory is bounded by channel capacity, not the author count. Unit/integration tests use a mocked adapter for determinism and speed.
- **D-09:** A **live integration check** fetches a real sample from the live adapter (`http://192.168.149.21:8080/graphql`) to prove `latestPerAuthor` deserializes the real response. Live services are reachable and need no human intervention; if transiently unreachable it degrades to a deferred manual check (does not block).

### Claude's Discretion
- Channel capacity, rayon thread-pool size, exact authors-per-call within the ≤1000 + 256 KiB envelope, and the in-flight buffering strategy — chosen by research/planner in the bounded-memory + fail-fast spirit.
- Whether the no-op consumer counts, hashes, or simply drops groups.

### Deferred Ideas (OUT OF SCOPE)
- **Real detection layers + scoring** — Phase 4 (this phase's no-op consumer is the plug-in seam).
- **Direct `heed` LMDB reads to bypass the GraphQL hop (PERF-01)** — v2, profiling-gated.
- **Incremental/service mode (SVC-01)** — v2.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| INGEST-02 | Fetch each pubkey's most-recent ~100 events via batched `latestPerAuthor` (≤1000 authors/call), respecting the 256 KiB body limit and the ≤500 page clamp. | §"latestPerAuthor Query Shape", §"Authors-per-call sizing & 413 handling". `perAuthor:100` is well under the silent `[1,500]` clamp (contract §12). |
| INGEST-03 | Fetch (I/O) and analysis (CPU) run as a bounded-memory streaming pipeline (tokio → bounded channel → rayon) that never buffers the full corpus. | §"The tokio → bounded channel → rayon pipeline", §"Bounded-memory proof test". |

> INGEST-04 (empty-group / match-by-author) is mapped to Phase 2 in REQUIREMENTS.md but the *consumer* of that data lives here; D-04 restates the match-by-author rule for the `latestPerAuthor` path. Covered in §"Author→pubkey matching".
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `rayon` | `1.12` | CPU analysis thread pool (the consumer stage). | The de-facto Rust data-parallelism crate; work-stealing pool, `ThreadPoolBuilder`, `install`/`spawn`. `[VERIFIED: crates.io]` (7.0M weekly downloads, repo `rayon-rs/rayon`). |
| `flume` | `0.12` (present) | The bounded back-pressure channel. **Both** sync `Receiver::recv()` and async `Sender::send_async()`/`Receiver::recv_async()` — the one crate that spans the async↔sync boundary cleanly. | Already a project dep (CONTEXT/`Cargo.toml`); `[VERIFIED: crates.io]` 3.1M weekly downloads, repo `zesterer/flume`. MPMC + async + sync in one. |
| `tokio` | `1.52` (present) | async runtime for the reqwest fetcher; `tokio::spawn` for the fetch task. | Phase-2 transport runtime; `rt-multi-thread,macros,time` features already enabled (`Cargo.toml`). |
| `reqwest` | `0.13` (present) | GraphQL-over-HTTP transport for `latestPerAuthor`. | Phase-2 dep; no change. |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `serde` / `serde_json` | `1.0` (present) | `latestPerAuthor` response structs (`AuthorGroup`, `Event`) + the `{authors,kind,perAuthor}` variables `json!`. | Mirrors the existing `AuthorsData`/`AuthorsPage` derive idiom in `queries.rs`. |
| `thiserror` | `2` (present) | No new error needed — reuse `ClientError`. | The 413/503/Graphql taxonomy already covers everything (see §413 handling). |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `flume::bounded` | `tokio::sync::mpsc` (bounded) | tokio mpsc is async-only; the rayon worker would need `blocking_recv()` (tokio) which couples the consumer to a tokio handle. `flume` gives a clean `recv()` on the sync side with no runtime handle — cleaner async↔sync seam. Also CONTEXT D-05 mandates the existing `flume` dep. |
| `flume::bounded` | `crossbeam-channel` (bounded) | crossbeam is sync-only — the *fetcher* side then needs `spawn_blocking` to send without blocking the tokio reactor. `flume`'s `send_async` avoids that. New dep, rejected. |
| dedicated rayon `ThreadPool` | `rayon::spawn` on the global pool | The global pool is fine and simpler; a dedicated `ThreadPoolBuilder::new().num_threads(n)` pool only matters if you want to size/isolate it (Claude's discretion). Recommend the **global pool + a single blocking drain loop** for the no-op stage (see pattern), keeping `rayon::spawn` available for per-group parallelism in Phase 4. |
| `latestPerAuthor` per batch | `events(filter:{authors,kinds})` paginated | `latestPerAuthor` is the *one* query that expresses "top-N per author" in a single call (contract §1, §6.2); `events` would need per-author pagination. D-01 locks `latestPerAuthor`. |

**Installation:**
```bash
# Add to [dependencies] in Cargo.toml (Phase-3-owned dep):
# rayon = "1.12"
cargo add rayon@1.12
```

**Version verification (performed this session):**
```
cargo search rayon  → rayon = "1.12.0"   # [VERIFIED: crates.io]
flume = "0.12.0"  (Cargo.lock, present)  # [VERIFIED: Cargo.lock]
tokio = "1.52.3"  (Cargo.lock, present)  # [VERIFIED: Cargo.lock]
reqwest = "0.13.4"(Cargo.lock, present)  # [VERIFIED: Cargo.lock]
```

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `rayon` | crates.io | since 2015-12 | 7.0M/wk | github.com/rayon-rs/rayon | OK | Approved (Phase-3-owned add) |
| `flume` | crates.io | since 2019-07 | 3.1M/wk | github.com/zesterer/flume | OK | Approved (already present) |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none
(Verdicts from `gsd-tools query package-legitimacy check --ecosystem crates rayon flume`; no postinstall scripts — N/A for crates.)

## Architecture Patterns

### System Architecture Diagram

```
                 ┌─────────────────────────────────────────────────────────────┐
                 │  tokio runtime (rt-multi-thread)                             │
   SQLite        │                                                              │
  pubkey  ──read─┼─► pubkey source (sync read via Store::reader())              │
  table          │        │ Vec<String> hex pubkeys                            │
                 │        ▼                                                     │
                 │   chunk into batches of N (≤1000, default 250)              │
                 │        │                                                     │
                 │        ▼                                                     │
                 │   tokio::spawn  fetcher task                                 │
                 │        │  client.latest_per_author(kind, perAuthor, &batch) │
                 │        │      ── HTTP POST /graphql (reqwest, async) ──►  LMDB2GraphQL
                 │        │      ◄── [AuthorGroup{author,events}] (omits empties)
                 │        │  match groups → map keyed on requested pubkey (D-04)│
                 │        ▼                                                     │
                 │   flume::Sender<AuthorGroup>.send_async(g).await  ───────────┼──┐
                 │        ▲  BLOCKS when channel full  (back-pressure point)    │  │
                 └────────┼─────────────────────────────────────────────────────┘  │
                          │                                                          │ bounded
                          │                          flume::bounded(CAP)             │ channel
                          │                                                          │ (CAP groups
   ┌──────────────────────┼──────────────────────────────────────────────────────┐ │  in flight)
   │  rayon thread pool    │                                                       │ │
   │   drain loop: while let Ok(group) = rx.recv() { ◄────────────────────────────┼─┘
   │       consume(group)  // Phase 3: no-op (count/drop)  ── Phase-4 seam ──►     │
   │   }                                                                           │
   └───────────────────────────────────────────────────────────────────────────────┘

  Peak in-flight memory  ≈  CAP (channel) × (perAuthor events × avg event size)
                            + at most one in-flight fetch batch
                         —  INDEPENDENT of total pubkey count.
```

### Recommended Project Structure
```
src/
├── graphql/
│   ├── queries.rs   # ADD: LATEST_PER_AUTHOR_QUERY const + Event/AuthorGroup/
│   │                #      LatestPerAuthorData structs (additive — D-03)
│   └── client.rs    # ADD: latest_per_author(kind, per_author, authors) wrapper
├── fetch.rs         # NEW: the fetcher (batching + 413 shrink/retry + match-by-author)
├── pipeline.rs      # NEW: spawn fetcher → flume::bounded → rayon drain; no-op consumer
└── enumerate.rs     # (unchanged) — the pubkey source the pipeline reads
```
(Module names are the planner's call; `fetch`/`pipeline` split keeps the fetch policy testable apart from the channel wiring.)

### Pattern 1: Additive `latestPerAuthor` query (D-03)
**What:** Mirror the exact `AUTHORS_QUERY` + `AuthorsData`/`AuthorsPage` idiom in `src/graphql/queries.rs`, and add a typed wrapper on `GraphQlClient` exactly like `authors()`.
**When to use:** the only fetch query this phase adds.
**Example:**
```rust
// src/graphql/queries.rs  — Source: existing AUTHORS_QUERY idiom + contract §6.2/§4
// Select ONLY the fields the no-op (and Phase-4) consumer needs. `raw` is large
// (contract §9 best-practice #6) — omit it; add content/tags here.
pub const LATEST_PER_AUTHOR_QUERY: &str = "query($kind:Int!,$perAuthor:Int!,$authors:[String!]!){ \
  latestPerAuthor(kind:$kind,perAuthor:$perAuthor,authors:$authors){ \
    author events{ id pubkey kind createdAt content tags } } }";

#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]   // createdAt → created_at, etc. (contract §4 naming note)
pub struct Event {
    pub id: String,
    pub pubkey: String,
    pub kind: i64,          // 64-bit per contract §8 (NOT i32)
    pub created_at: i64,    // 64-bit Unix seconds, author-claimed (contract §8)
    pub content: String,
    pub tags: Vec<Vec<String>>,   // [[String!]!]! — tags[i][0]=name (contract §5)
}

#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AuthorGroup {
    pub author: String,           // the requested pubkey (64-char lc hex) — D-04 match key
    pub events: Vec<Event>,       // newest-first, ≤ perAuthor (contract §5)
}

// latestPerAuthor returns a TOP-LEVEL LIST, so `data.latestPerAuthor` is `Vec<AuthorGroup>`.
#[derive(Deserialize, Debug, Clone, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LatestPerAuthorData {
    pub latest_per_author: Vec<AuthorGroup>,
}
```
```rust
// src/graphql/client.rs  — Source: existing authors()/stats() wrapper pattern
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
```
The existing two-layer dispatch in `query<T>` already surfaces `413 → PayloadTooLarge`, `503 → Unavailable`, and in-body `errors[]` (incl. `TOO_MANY_AUTHORS`) **before** trusting `data` — no transport change needed (`client.rs:83-120`). `[VERIFIED: src/graphql/client.rs, queries.rs]` for the idiom; field names `[CITED: contract.md §4, §5, §6.2]`.

### Pattern 2: Author→pubkey matching by `author` (D-04 / INGEST-04)
**What:** Build a `HashMap<&str, &AuthorGroup>` (or fold groups into a per-requested-pubkey result) keyed on `group.author`. Authors with zero matching events are simply **absent keys** — represent them as an empty group, never as a positional shift.
**When to use:** immediately after decoding each batch's `Vec<AuthorGroup>`.
**Example:**
```rust
// Source: contract §5 "Do not assume result.length === authors.length", §8 "match by author"
use std::collections::HashMap;

/// For a requested batch, yield one (pubkey, events) per REQUESTED author —
/// empty Vec for the omitted (zero-match) ones. Never zip by index.
fn match_groups<'a>(
    requested: &'a [String],
    groups: Vec<AuthorGroup>,
) -> Vec<(&'a str, Vec<Event>)> {
    let mut by_author: HashMap<String, Vec<Event>> =
        groups.into_iter().map(|g| (g.author, g.events)).collect();
    requested
        .iter()
        .map(|pk| (pk.as_str(), by_author.remove(pk).unwrap_or_default()))
        .collect()
}
```
If the pipeline only forwards *non-empty* groups to the rayon stage, you can skip the empty-fill and forward `groups` directly — but the **map must still be keyed on `author`** so a Phase-4 consumer that needs the requested-pubkey alignment is not built on a zip. Recommend keying-by-author unconditionally.

### Pattern 3: The tokio → bounded channel → rayon pipeline (D-05, INGEST-03)
**What:** A `flume::bounded::<AuthorGroup>(CAP)` channel. The fetcher (tokio task) sends with `send_async().await`; the rayon stage drains with the blocking `recv()`. The channel is the only synchronization — no `Mutex`, no `spawn_blocking`.
**When to use:** the pipeline core.
**Example:**
```rust
// Source: flume 0.12 docs (bounded/send_async/recv) + rayon 1.12 (spawn) + Phase-2 client
use flume::bounded;

pub async fn run_pipeline(
    client: &GraphQlClient,
    pubkeys: Vec<String>,        // read from the `pubkey` table (D-07), see Pattern 4
    kind: i64,                   // config (D-01) — 1 for v1
    per_author: i64,             // 100 (D-01); well under the [1,500] clamp (contract §12)
    channel_cap: usize,          // e.g. 64 groups in flight (Claude's discretion)
    authors_per_call: usize,     // e.g. 250 (see §413 sizing)
) -> Result<u64, ClientError> {
    let (tx, rx) = bounded::<AuthorGroup>(channel_cap);

    // CONSUMER: a single rayon task running a BLOCKING drain loop. recv() blocks
    // the rayon worker (not a tokio thread) until a group arrives or all senders
    // drop (channel closed → recv() Err → loop ends). This is the Phase-4 seam.
    let count = std::sync::Arc::new(std::sync::atomic::AtomicU64::new(0));
    let c2 = count.clone();
    rayon::spawn(move || {
        while let Ok(group) = rx.recv() {            // sync side of the boundary
            consume_noop(&group, &c2);               // Phase 3: count/drop (D-06)
        }
    });

    // PRODUCER: fetch batches on the tokio runtime, push into the bounded channel.
    // send_async blocks (awaits) when the channel is full → back-pressure (D-05).
    for batch in pubkeys.chunks(authors_per_call) {
        let groups = fetch_batch(client, kind, per_author, batch).await?; // §413 retry
        for g in groups {
            if tx.send_async(g).await.is_err() {
                break; // consumer dropped (shouldn't happen) — stop fetching
            }
        }
    }
    drop(tx);  // closes the channel → consumer's recv() returns Err → loop exits

    // Wait for the consumer to finish draining. Either: spawn it on a oneshot/
    // flume ack channel and await it, or move the drain into a dedicated
    // std::thread + JoinHandle the async fn joins (recommended — see Pitfall 3).
    Ok(count.load(std::sync::atomic::Ordering::Relaxed))
}

fn consume_noop(group: &AuthorGroup, count: &std::sync::atomic::AtomicU64) {
    // D-06: no-op. Count groups (or events) and drop. Phase 4 swaps in Layer::score.
    count.fetch_add(group.events.len() as u64, std::sync::atomic::Ordering::Relaxed);
}
```

**The async↔sync boundary (the heart of the phase):**
- **Producer side is async:** `tx.send_async(g).await` lets the tokio reactor keep the reqwest fetch alive while the send waits for channel room. Do **not** use the blocking `tx.send(g)` inside the async fn — it would block a tokio worker thread.
- **Consumer side is sync:** `rx.recv()` is a plain blocking call. It MUST NOT run on a tokio worker thread. Run it on a **rayon worker** (`rayon::spawn`) or a dedicated `std::thread`. CPU work stays entirely off the tokio runtime (D-05). `[VERIFIED: flume 0.12 docs — send_async/recv]`.
- **Back-pressure is automatic:** with `bounded(CAP)`, once `CAP` groups sit unconsumed, `send_async` suspends the fetcher future. A slow consumer therefore throttles the fetcher; the queue never grows past `CAP` (INGEST-03). `bounded(0)` is a rendezvous channel (send blocks until a receiver takes the value) — viable for the strictest bound but pays a sync hop per group; prefer a small `CAP` (e.g. 32–64) for throughput. `[VERIFIED: flume 0.12 fn.bounded docs]`.

**Joining the consumer (avoid the dangling-task trap):** `rayon::spawn`'d closures have no join handle. Recommended pattern: run the drain on a `std::thread::spawn(move || { rayon_pool.install(|| drain) })` or, simplest, a dedicated `std::thread` whose `JoinHandle` the async fn joins **after** `drop(tx)`. Joining a `std::thread` inside an async fn is a brief blocking call after all fetching is done — acceptable at end-of-run (mirrors `Store::close()` joining the writer thread, `store/mod.rs:251`). If parallel per-group analysis is wanted in Phase 4, the drain loop bodies call `rayon::spawn`/`par_iter` internally.

### Pattern 4: Pubkey source — read the persisted table (D-07)
**What:** Add a read helper on `Store` (mirrors `store/queries.rs::read_scores`) that selects all pubkeys ordered deterministically. Phase 3 reads them up front into a `Vec<String>` (or streams them — for v1, the count is the distinct-author set, which fits memory as hex strings; a streaming `query_map` iterator is the cleaner choice and avoids materializing the whole set).
**Example:**
```rust
// src/store/queries.rs (additive) — Source: existing read_scores idiom
pub fn read_pubkeys(conn: &Connection) -> rusqlite::Result<Vec<String>> {
    let mut stmt = conn.prepare("SELECT pubkey FROM pubkey ORDER BY pubkey")?;
    let rows = stmt.query_map([], |r| r.get::<_, String>(0))?;
    rows.collect()
}
// Open the read connection via the existing Store::reader() (store/mod.rs:264).
```
This decouples fetch from the live walk (D-07): Phase 2 already persisted every distinct pubkey into the `pubkey` table (`UPSERT_PUBKEY`, `writer.rs:35`). No re-walk needed.

### Anti-Patterns to Avoid
- **Zipping `groups[i]` to `authors[i]`** — breaks on the first zero-match author (contract §5/§8). Always key on `author`.
- **`flume::unbounded` for the analysis channel** — defeats the entire phase (corpus would buffer; INGEST-03 fails). The *writer* channel in `store/mod.rs:82` is `unbounded` by design (small messages, single writer) — do NOT copy that for the group channel. Use `bounded`.
- **Blocking `tx.send()` inside the async fetcher** — blocks a tokio worker; use `send_async().await`.
- **`rx.recv()` on a tokio task** — blocks the reactor. Drain on rayon/std::thread only.
- **Wrapping `Store` in `tokio::sync::Mutex`** — the codebase invariant (Pitfall 1, `enumerate.rs:130`): store calls are plain sync calls; this phase persists nothing anyway.
- **Selecting `raw` in the query** — `raw` is byte-exact JSON and large (contract §9 #6). Select only `content`/`tags`/`createdAt`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Back-pressured queue between async and sync | a custom `Mutex<VecDeque>` + condvar | `flume::bounded` (`send_async`/`recv`) | flume is MPMC, lock-light, spans async+sync, and `bounded` gives exact back-pressure semantics for free. |
| CPU thread pool / work-stealing | manual `std::thread` pool + work queue | `rayon` global pool / `ThreadPoolBuilder` | work-stealing, `par_iter` for Phase 4; battle-tested. |
| Bounded retry / backoff on 503/transient | new retry loop | the existing `enumerate::retry` helper (`enumerate.rs:80`) | already bounded-exponential (250ms base, ×2, cap 2s, 3 attempts) and classifies retryable vs not (`is_retryable`). Reuse for `latest_per_author`. |
| GraphQL error dispatch | new status/`errors[]` parsing | `GraphQlClient::query<T>` (`client.rs:83`) | two-layer dispatch (413/503 → body errors → data) is done; just deserialize into the new structs. |
| Idempotent pubkey read | new SQL | `store/queries.rs` read idiom + `Store::reader()` | parameterized, ordered, matches the existing convention. |

**Key insight:** Phase 3 is almost entirely *composition* of existing primitives — the only genuinely new code is the `latestPerAuthor` structs, the match-by-author map, and the `bounded`-channel wiring. The fetch error handling, retry, GraphQL dispatch, and pubkey persistence already exist.

## Common Pitfalls

### Pitfall 1: Zip-by-index misalignment (the INGEST-04 landmine)
**What goes wrong:** `latestPerAuthor` omits zero-match authors, so `groups.len() < authors.len()`; pairing `authors[i]` with `groups[i]` mislabels every subsequent author.
**Why it happens:** the naive mental model is "I asked for N, I get N back in order." Contract §5/§8 explicitly warn against it.
**How to avoid:** key results on `group.author` (Pattern 2); fill omitted authors as empty groups or simply forward by-author.
**Warning signs:** a test where the last author in a batch has events but they show up attributed to an earlier pubkey.

### Pitfall 2: Unbounded buffering hidden in the wrong channel choice
**What goes wrong:** using `flume::unbounded` (copying the writer-channel pattern) makes the fetcher race ahead and buffer the whole corpus in the channel — memory grows with author count, failing INGEST-03 / success criterion #3.
**Why it happens:** `store/mod.rs` uses `unbounded` for the writer; easy to cargo-cult.
**How to avoid:** `flume::bounded(CAP)` for the group channel; assert peak in-flight in the bounded-memory test (D-08).
**Warning signs:** memory grows linearly with the synthetic author-set size in the proof test.

### Pitfall 3: Dangling/never-joined rayon consumer
**What goes wrong:** `rayon::spawn` has no join handle; the async fn returns before the consumer finishes draining, dropping in-flight groups or reporting a wrong count.
**Why it happens:** fire-and-forget spawn with no synchronization back to the producer.
**How to avoid:** after `drop(tx)`, join the consumer — either a dedicated `std::thread` with a `JoinHandle` (recommended, mirrors `Store::close()` join at `store/mod.rs:251-260`), or a `flume`/oneshot ack the async fn awaits.
**Warning signs:** flaky group counts; the no-op count is less than expected.

### Pitfall 4: Blocking the tokio reactor at the boundary
**What goes wrong:** calling blocking `tx.send()` (producer) or `rx.recv()` (consumer) on a tokio worker thread stalls the reactor, starving other futures and breaking back-pressure timing.
**Why it happens:** flume exposes both sync and async methods; picking the wrong one per side.
**How to avoid:** producer = `send_async().await`; consumer = `recv()` on rayon/std::thread only. Never cross them.
**Warning signs:** the fetcher appears to serialize, or `tokio` task stalls under load.

### Pitfall 5: Mis-sizing the batch and treating 413 as fatal
**What goes wrong:** a `413` aborts the run instead of shrinking the batch.
**Why it happens:** Phase-2's `is_retryable` marks `PayloadTooLarge` as **non-retryable** (`enumerate.rs:64`) — correct for the tiny `authors` query, wrong as a *terminal* response for `latestPerAuthor`, which should shrink-and-retry (D-02).
**How to avoid:** in `fetch_batch`, catch `ClientError::PayloadTooLarge` specifically, halve `authors_per_call`, and retry the sub-batches — distinct from the `retry` helper's 503/transient policy (see §413 handling). Do NOT route 413 through `retry` (it would surface immediately as non-retryable).
**Warning signs:** a run aborts with `PayloadTooLarge` on a large batch.

## latestPerAuthor Query Shape (focus #1)

- **Signature** `[CITED: contract §4, §6.2]`: `latestPerAuthor(kind: Int!, perAuthor: Int!, authors: [String!]!): [AuthorGroup!]!`.
- **Returns a top-level list** (`[AuthorGroup!]!`), so the `data` object is `{ "latestPerAuthor": [ {author, events:[...]}, ... ] }` → struct `LatestPerAuthorData { latest_per_author: Vec<AuthorGroup> }` (camelCase rename). This differs from `authors`/`stats`, whose payloads are single objects.
- **`perAuthor:100`** is well within the silent `[1,500]` clamp (contract §12) — no clamp risk.
- **`authors` ≤ 1000** else in-body `TOO_MANY_AUTHORS` (contract §6.2/§7) — the batcher must chunk at ≤1000 (we recommend 250).
- **Field selection:** `author`, `events{ id pubkey kind createdAt content tags }`. Omit `raw`/`sig` (large/unneeded). Add fields in Phase 4 if a layer needs them.
- **`kind`/`createdAt` are 64-bit** (contract §8) → `i64` in the structs, not `i32`.
- Concrete structs in Pattern 1. `[VERIFIED: src/graphql/queries.rs idiom]` + `[CITED: contract §4/§5/§6.2]`.

## Authors-per-call sizing & 413 handling (focus #2)

**The 256 KiB limit is on the REQUEST body, returned as transport `413`** (contract §7, §12). The request body for `latestPerAuthor` ≈ the query string (~150 bytes) + the JSON array of authors (each 64-char hex + quotes/comma ≈ 68 bytes). So:
- 1000 authors ≈ **~68 KiB** request body — comfortably under 256 KiB. A `413` from the *request* is therefore unlikely even at the 1000 cap.
- The real constraints are (a) **per-call cost** ≈ `authors × perAuthor` index scans (contract §6.2 "Cost awareness" — a 1000×100 call is heavy), and (b) **response** size / latency. The contract caps the *request* body, not the response.

**Recommended starting `authors_per_call = 250`** `[ASSUMED — empirically tunable]`. Rationale: 250 × 100 = 25,000 events per response keeps a single in-flight response bounded and latency reasonable, while staying well under the 1000 author cap and the 68 KiB request body. The number is a config/tuning knob, not a contract limit — the planner should make it a constant the live integration check (D-09) can sanity-check against real response sizes/timing, and tune downward if responses are slow or upward if throughput-bound.

**413 handling — shrink-and-retry, distinct from the 503/transient retry:**
```rust
// Source: contract §7 (413 → shrink), Phase-2 ClientError taxonomy (client.rs:36)
async fn fetch_batch(
    client: &GraphQlClient, kind: i64, per_author: i64, batch: &[String],
) -> Result<Vec<AuthorGroup>, ClientError> {
    // 503/transient/codeless-Graphql → bounded backoff via the EXISTING retry helper.
    match retry(|| client.latest_per_author(kind, per_author, batch)).await {
        Ok(groups) => Ok(groups),
        // 413 is NON-retryable in is_retryable() — handle it HERE by splitting.
        Err(ClientError::PayloadTooLarge) if batch.len() > 1 => {
            let mid = batch.len() / 2;
            let mut left  = Box::pin(fetch_batch(client, kind, per_author, &batch[..mid])).await?;
            let     right = Box::pin(fetch_batch(client, kind, per_author, &batch[mid..])).await?;
            left.extend(right);
            Ok(left)
        }
        Err(e) => Err(e),
    }
}
```
Note: `retry` (`enumerate.rs:80`) classifies `PayloadTooLarge` as non-retryable (`is_retryable`, `enumerate.rs:64`), so it surfaces immediately — the recursive split is the D-02 "shrink the batch and retry" behavior. `TOO_MANY_AUTHORS` (coded Graphql, also non-retryable) should not occur if the batcher respects ≤1000, but if it does it surfaces as `ClientError::Graphql{code:Some("TOO_MANY_AUTHORS")}` and the same split logic could catch it (defensive). `[VERIFIED: enumerate.rs is_retryable/retry; client.rs ClientError]`, `[CITED: contract §7]`.

## Author→pubkey matching (focus #4)

See Pattern 2. Data structure: `HashMap<String, Vec<Event>>` (or `HashMap<&str, &AuthorGroup>`) keyed on the response `author` field. Omitted (zero-match) authors are absent keys → represented as an empty `Vec<Event>` when iterating the *requested* list, never as a positional shift. The contract's exact words: *"Authors with zero matching events are omitted… Do not assume `result.length === authors.length`"* (§5) and *"match results back to your requested list by `author`, don't zip by index"* (§8). `[CITED: contract §5, §8]`.

## Pipeline seam for Phase 4 (focus #6)

The `consume_noop(group, count)` call site in the rayon drain loop is the **single seam**. Phase 4 replaces it with the Layer/combiner stage *without touching the channel or fetcher*:
- Define the consumer as a trait object or generic `Fn(&AuthorGroup)` parameter, e.g. `fn run_pipeline<C: Fn(AuthorGroup) + Send + Sync>(consumer: C, ...)`. Phase 3 passes a counting closure; Phase 4 passes the Layer-combiner closure that emits `Persist` to the store writer.
- Keep the group payload (`AuthorGroup { author, Vec<Event> }`) as the stage's input contract — Phase 4's layers consume `events` per pubkey and emit a `Persist` (`model.rs:96`) through the existing `Store` writer (`store.persist`, `store/mod.rs:203`).
- The drain loop body is where Phase 4 may fan out with `rayon::par_iter`/`rayon::spawn` for per-group parallel scoring — the structural decision (CPU off the tokio runtime) is locked here so Phase 4 inherits it.
Design the consumer as an injected closure/trait now → Phase 4 is purely additive (swap the closure, add the `WriteMsg::Score` path that already exists in `model.rs:112`). `[VERIFIED: model.rs WriteMsg/Persist; store/mod.rs persist]`.

## Bounded-memory proof test (focus #5)

**The headline test (D-08, success criterion #3).** What's unit-testable vs live:

**Unit/mock (deterministic, fast):**
- **Mocked fetcher:** instead of hitting HTTP, inject a fetch function (closure/trait) that *generates* `AuthorGroup`s for a large synthetic author set (e.g. 1,000,000 synthetic pubkeys, each with `perAuthor` cheap events). This avoids the HTTP layer entirely and makes the test deterministic. The pipeline's `fetch_batch` should be behind a trait/`Fn` so the test substitutes a generator.
- **Bounded-memory assertion strategy:** the channel is `bounded(CAP)`, so the *channel* can hold at most `CAP` groups by construction. The proof is to show the **producer is forced to wait** when the consumer is slow:
  - Use a **slow consumer** (e.g. a small `thread::sleep` or a gate the test releases) and assert that an instrumented counter of "groups sent but not yet consumed" never exceeds `CAP + (one in-flight batch)`. Track via an `AtomicUsize` incremented on `send`, decremented on `consume`, with a max-watermark `AtomicUsize` (`fetch_max`). Assert `watermark <= CAP + authors_per_call`.
  - Independently assert **total throughput**: all `N` synthetic groups are consumed exactly once (the no-op count equals the synthetic event total) — proving nothing is dropped under back-pressure.
  - This is a *structural* proof (watermark bounded by capacity) rather than an OS-RSS measurement, which is non-deterministic across platforms. RSS-based assertions are flaky and NOT recommended for the unit test.
- **Match-by-author test:** feed a synthetic batch where a middle author is omitted; assert the remaining authors are not shifted (Pattern 2). Pure function, no async.
- **413 shrink test:** a mocked fetcher returns `PayloadTooLarge` for batches > K and `Ok` for ≤ K; assert `fetch_batch` recursively splits and returns the union with no loss.

**Live integration (D-09, `http://192.168.149.21:8080/graphql`):**
- A `#[tokio::test]` (or `#[ignore]`d-by-default test gated on an env var / reachability probe) that issues a real `latest_per_author(1, 5, &[<one real pubkey>])` and asserts the response **deserializes** into `Vec<AuthorGroup>` with `author`/`events` populated. This proves the structs match the *real* wire shape (the contract is code-verified but a live check catches drift, D-09).
- Degrade gracefully: probe `GET /ready` (or catch `ClientError::Unavailable`/`Transport`) → if unreachable, skip (not fail) and emit a deferred-manual note. Live services are reachable per CONTEXT, so this normally runs.

## Validation Architecture

> nyquist_validation = true (`.planning/config.json`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | `cargo test` (built-in) + `#[tokio::test]` for async paths (tokio `macros` feature present) |
| Config file | none — Cargo's built-in test harness |
| Quick run command | `cargo test --lib` |
| Full suite command | `cargo test` (lib + the live integration test; live test self-skips when adapter unreachable) |

The existing code uses a hand-rolled loopback `stub_server` (TcpListener) + a current-thread tokio runtime `block_on` helper (`client.rs:158-189`, `enumerate.rs` test harness) instead of `wiremock`/`#[tokio::test]` macro — **reuse that idiom** for fetch tests to stay consistent with the owning-phase / no-extra-dep discipline. For the pipeline test, a mocked fetcher closure is cleaner than a stub server.

### Phase Requirements → Test Map
| Req / Criterion | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| Criterion 1 / INGEST-02 | `latest_per_author` builds the right query + deserializes a real-shaped `[AuthorGroup]` response | unit (stub_server, like `authors_happy_path`) | `cargo test --lib latest_per_author` | ❌ Wave 0 |
| Criterion 1 / D-02 | 413 → recursive batch split, no loss | unit (mock fetcher) | `cargo test --lib fetch_413_split` | ❌ Wave 0 |
| Criterion 2 / D-04 | omitted (zero-match) author does not shift others; match by `author` | unit (pure fn) | `cargo test --lib match_groups_no_shift` | ❌ Wave 0 |
| Criterion 3 / INGEST-03 / D-08 | bounded watermark ≤ CAP + batch over a large synthetic set; all groups consumed once | unit (mock fetcher + slow consumer + atomic watermark) | `cargo test --lib bounded_memory_watermark` | ❌ Wave 0 |
| Criterion 4 / D-06 | no-op consumer drains end-to-end; count == synthetic total | unit (mock fetcher) | `cargo test --lib pipeline_endtoend_count` | ❌ Wave 0 |
| D-09 | real adapter response deserializes into `[AuthorGroup]` | live integration (`#[tokio::test]`, self-skip if unreachable) | `cargo test --lib live_latest_per_author -- --ignored` (or env-gated) | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `cargo test --lib` (mocked tests only — fast, deterministic).
- **Per wave merge:** `cargo test` (includes the live check when adapter reachable).
- **Phase gate:** full suite green + a manual confirmation that the live `latestPerAuthor` deserialized against `http://192.168.149.21:8080/graphql` before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `src/fetch.rs` (or in-module `#[cfg(test)]`) — `latest_per_author` stub test, `match_groups` test, `fetch_413_split` test (covers Criteria 1, 2).
- [ ] `src/pipeline.rs` `#[cfg(test)]` — `bounded_memory_watermark`, `pipeline_endtoend_count` (covers Criteria 3, 4 / D-08).
- [ ] live integration test (env-gated or `#[ignore]`) — D-09.
- [ ] shared test fixture: a synthetic-author generator (`fn synthetic_authors(n) -> Vec<String>`) and a mock-fetcher closure type.
- Framework install: none — `cargo test` + tokio macros already present.

## Security Domain

> security_enforcement = true, ASVS level 1 (`.planning/config.json`).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Adapter is unauthenticated by design (contract §1, network-placement-gated). |
| V3 Session Management | no | Stateless GraphQL-over-HTTP. |
| V4 Access Control | no | Read-only adapter; this phase persists nothing. |
| V5 Input Validation | yes | Pubkeys read from our own `pubkey` table (already validated at the Phase-2 trust boundary, `enumerate.rs::is_valid_pubkey`). Adapter responses: trust `kind`/`createdAt` as `i64` (contract §8 64-bit) — do NOT use `i32` (truncation). `content` is untrusted text but Phase 3 does not interpret it (no-op consumer); Phase 4 owns content-handling validation. |
| V6 Cryptography | no | No crypto; `sig` already verified by strfry on ingest (contract §5). |

### Known Threat Patterns for Rust async fetch pipeline
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Unbounded memory growth (resource exhaustion via fast fetch / slow consume) | Denial of Service | `flume::bounded` back-pressure — the phase's core control (INGEST-03). |
| Integer truncation of `kind`/`createdAt` | Tampering (silent data corruption) | `i64` structs per contract §8; never `i32`. |
| Logging full response bodies (info leak / log bloat) | Information Disclosure | Reuse Phase-2 `ClientError` discipline — carries only status + first message, never full body (`client.rs:26-28`, T-02-04). |
| Endpoint injection / SSRF via attacker-set URL | Tampering | Endpoint is operator-supplied (`GraphQlClient::new`, `main.rs` `LMDB2GRAPHQL_URL`), not user input — same posture as Phase 2 (T-02-10). |
| GraphQL variable injection | Tampering | Authors passed as a **GraphQL variable** (`json!({"authors": authors})`), never string-interpolated into the query document — parameterization analog to the SQL `?N` discipline. |

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `std::sync::mpsc` (sync-only, no async, SPMC limits) | `flume` MPMC w/ both sync `recv` and async `send_async`/`recv_async` | flume mature since ~2020 | one channel crate spans the async↔sync boundary — exactly the Phase-3 seam. |
| `spawn_blocking` to run CPU work off the reactor | dedicated rayon pool / `rayon::spawn`, fed by a bounded channel | rayon long-standard | CPU work isolated on a work-stealing pool; tokio reactor stays I/O-only; back-pressure via the channel, not a blocking-thread-pool queue. |

**Deprecated/outdated:** none relevant. `rayon::scope::breadth_first()` is deprecated (use `scope_fifo`) but not needed for the no-op stage.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `authors_per_call = 250` is a safe/efficient starting batch size. | §413 sizing | Low — it's a tuning knob, not a contract limit; the live check (D-09) validates real response size/latency and the planner tunes it. A too-large value surfaces as slow responses, not data loss; the 413-split path is the safety net regardless. |
| A2 | A dedicated `std::thread` join (vs `rayon::spawn`) is the cleanest consumer-completion sync. | Pattern 3 / Pitfall 3 | Low — both work; the std::thread join mirrors the proven `Store::close()` pattern. Planner may choose a flume-ack instead. |

**If this table looks short:** every other claim is verified against in-repo source, the code-verified `contract.md`, or registry-confirmed crate versions.

## Open Questions

1. **Stream the pubkey table or load it all?**
   - What we know: Phase 2 persisted every distinct pubkey to the `pubkey` table; a `read_pubkeys → Vec<String>` is simplest.
   - What's unclear: at full corpus scale the distinct-author count could be large enough that materializing all hex strings in a `Vec` is wasteful (though strings are ~64 bytes each — millions are still only ~100s of MB).
   - Recommendation: prefer a **streaming `query_map` iterator** chunked into batches (feeds the fetcher lazily) over a full `Vec`; both satisfy D-07. Planner's call; streaming is the cleaner bounded-memory story end-to-end.

2. **Channel capacity `CAP` value.**
   - What we know: any small bound (e.g. 32–64 groups) gives back-pressure; `bounded(0)` is the strictest (rendezvous).
   - Recommendation: start `CAP = 64`; it's Claude's discretion and the bounded-memory test is capacity-agnostic (asserts watermark ≤ CAP regardless of value).

## Environment Availability
| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `cargo`/Rust toolchain | build + test | ✓ | (workspace toolchain) | — |
| `rayon` crate | CPU stage | ✓ (registry) | 1.12 | — |
| `flume` crate | bounded channel | ✓ (present in Cargo.lock) | 0.12.0 | — |
| LMDB2GraphQL adapter | live check D-09 only | ✓ (per CONTEXT) | v1.2 | self-skip → deferred manual check (D-09) |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** the live adapter for D-09 only — degrades to a deferred manual check (does not block, per CONTEXT D-09).

## Sources

### Primary (HIGH confidence)
- In-repo source (`[VERIFIED]`): `src/graphql/{client.rs,queries.rs,envelope.rs,mod.rs}`, `src/enumerate.rs` (`retry`/`is_retryable`/`run`), `src/store/{mod.rs,writer.rs,queries.rs,schema.rs}`, `src/model.rs`, `src/main.rs`, `Cargo.toml`, `Cargo.lock`.
- `contract.md` (code-verified v1.2) — §4 schema, §5 types (`AuthorGroup`, `Event`), §6.2 `latestPerAuthor`, §7 errors (413/503/`TOO_MANY_AUTHORS`), §8 gotchas (empty-group omission, 64-bit ints), §9 best practices, §12 limits.
- `.planning/phases/03-…/03-CONTEXT.md`, `.planning/REQUIREMENTS.md` (INGEST-02/03/04), `.planning/ROADMAP.md` Phase 3 success criteria.
- Crate versions `[VERIFIED: crates.io / Cargo.lock]`: `rayon 1.12.0` (cargo search), `flume 0.12.0`, `tokio 1.52.3`, `reqwest 0.13.4`. Legitimacy: `gsd-tools package-legitimacy check` → both OK.

### Secondary (MEDIUM confidence)
- docs.rs `flume 0.12` — `fn bounded` (`bounded(cap)`, `bounded(0)` = rendezvous), `Receiver::recv_async/stream/into_stream`. `[CITED: docs.rs/flume/0.12.0]`.
- docs.rs `rayon 1.12` — `ThreadPoolBuilder::new().num_threads(n).build()`, `install`, `spawn`. `[CITED: docs.rs/rayon/1.12.0]`.

### Tertiary (LOW confidence)
- The `authors_per_call = 250` starting value `[ASSUMED]` — empirically tunable, validated by D-09.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — every crate is present or registry-verified with legitimacy check.
- Architecture (async↔sync boundary, query shape, match-by-author): HIGH — verified against in-repo idioms + code-verified contract.
- Pitfalls: HIGH — drawn from contract warnings + existing code invariants.
- Batch sizing: MEDIUM/LOW — the 250 starting value is `[ASSUMED]`, tuned via the live check.

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (stable; crate APIs and the code-verified contract are slow-moving).
