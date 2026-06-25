# Phase 2: GraphQL Client + Author Enumeration - Research

**Researched:** 2026-06-25
**Domain:** GraphQL-over-HTTP client (Rust/reqwest/tokio), cursor-paginated enumeration, two-layer error handling, resumable run state against an existing SQLite store
**Confidence:** HIGH (contract is code-verified; Phase-1 source read directly; crate versions confirmed via `cargo search`)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D-01:** `--resume` auto-resumes the latest unfinished run — the most recent `run` row whose `status` is not `done` (`running`/`aborted`), continuing from its stored `last_cursor`. No `--run-id`.
- **D-02:** If no unfinished run exists, `--resume` starts a fresh run (do not error). A normal (non-resume) invocation always starts a fresh run.
- **D-03:** Resume relies only on the existing Phase-1 `run` schema (`last_cursor`, `max_lev_id_start`, `max_lev_id_end`, `status`, `config_json`) — no new resume-tracking state.
- **D-04:** As the walk visits each distinct pubkey, persist it into the `pubkey` table via `INSERT OR IGNORE` (idempotent) **and** report running progress/count to stdout.
- **D-05:** Populating `pubkey` here is deliberate — `score`/`signal` FK to `pubkey`, so it must be populated eventually; doing it during enumeration leaves a queryable system behind this phase.
- **D-06:** Fail-fast on transient adapter trouble: a small bounded retry ceiling per request, then **abort the run** rather than retry indefinitely.
- **D-07:** On abort, mark `status='aborted'` with `last_cursor` preserved so `--resume` continues exactly where it stopped. A failure **never advances the cursor**.
- **D-08:** `503` → backoff-and-retry within the bounded ceiling without advancing the cursor. `INVALID_CURSOR` → restart pagination from page 1. GraphQL `errors[]`/`extensions.code` in a `200` body are parsed and acted on, never ignored.
- **D-09:** Snapshot drift = record-and-continue: capture `stats.maxLevId` at run start and end into the `run` row as a drift probe; a corpus change mid-pagination does **not** abort the run.
- **D-10:** GraphQL client is hand-written query strings + serde structs — no introspection codegen toolchain.
- **D-11:** Build a reusable `graphql` client module that Phase 3 extends with `latestPerAuthor` (and `events`) on the same client/transport. Adding a query later must be additive, not a rewrite.
- **D-12:** Expose the walk via a minimal binary/subcommand now (enough to run it and pass `--resume`). The full clap CLI is Phase 5 — do not build it out.

### Claude's Discretion
- Exact retry ceiling count and backoff base/cap (small, fail-fast spirit per D-06).
- `authors` page `limit` value (ceiling 500; larger = fewer round-trips).
- HTTP client/runtime specifics (project stack is reqwest/tokio) and how the endpoint URL is supplied (config-file-driven config is OPS-03 / Phase 4 territory).
- Internal module naming/layout within the reusable-client constraint (D-11).

### Deferred Ideas (OUT OF SCOPE)
- Live enumeration streaming directly into the fetch pipeline — Phase 3.
- Config-file-driven endpoint/parameter configuration (OPS-03) — Phase 4.
- Full `run`/`export` CLI — Phase 5.
- Direct `heed` LMDB reads to bypass the GraphQL hop (PERF-01) — v2.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| INGEST-01 | Enumerate all distinct pubkeys via `authors` cursor pagination, resumable, terminating cleanly at end of keyspace | §"Opaque-Cursor Pagination Loop", §"Resume Against the Real `run` Schema", contract §6.4 [CITED] |
| INGEST-04 | Handle adapter conditions gracefully — `503` (back off, no cursor advance), `INVALID_CURSOR` (restart), in-body `errors[]`, snapshot drift (record `maxLevId`, do not abort) | §"Two-Layer Error Handling", §"Bounded Retry + Backoff", contract §7/§8 [CITED] |
</phase_requirements>

## Summary

This phase wires a hand-written GraphQL-over-HTTP client (reqwest/tokio, D-10) to the existing synchronous Phase-1 SQLite store and walks the adapter's `authors` keyspace to completion, resumably. The contract pins the wire shape exactly: `POST /graphql` with `{query, variables}`, a `{data, errors}` envelope, opaque cursors passed back verbatim as `after`, terminate when `hasMore` is false, ceiling `authors.limit` 500, body limit 256 KiB, and a closed error taxonomy (`503`/`413` transport; `INVALID_CURSOR`/`TOO_MANY_AUTHORS`/internal/validation in-body). CONTEXT.md already locked every policy decision. **The real work the planner must specify is the glue the contract and CONTEXT.md do not: there is no `main.rs` and no `run`-table read/update API yet** — Phase 1 only built the `pubkey`/`score`/`signal` write path and `begin_run`. The planner must add a small set of `run`-state helpers (read latest non-done run, update `last_cursor`+`max_lev_id_*`, mark `aborted`/`done`) and a `pubkey`-only idempotent insert seam (the existing `Persist` path is coupled to score/signal, which do not exist for enumeration).

The async/sync boundary is the one genuine architectural decision. The store is strictly synchronous (`rusqlite` + a `flume` writer thread); reqwest is async-default. The cleanest pattern for a single-walker batch job is **`reqwest::blocking` on a synchronous `main`** — it sidesteps spinning a tokio runtime around a fundamentally sequential fetch→persist loop and matches the store's threading model with zero `block_on` bridging. Async reqwest is recommended only because D-11 says Phase 3 extends this transport, and Phase 3 (INGEST-03) explicitly needs `tokio → bounded channel → rayon`. The decision is therefore: **build the transport async now** (so Phase 3 inherits it without a rewrite) and drive the Phase-2 walk from a `#[tokio::main]` entry point, persisting through the sync store via ordinary blocking calls inside the async loop (the walk is sequential — no `spawn_blocking` needed for a single in-flight request, but persist calls should not be `.await`ed since they are sync). See the async/sync hazard note in Pitfalls.

**Primary recommendation:** async reqwest 0.13 + tokio 1 (full feature) for the reusable transport (D-11); hand-written `AuthorsPage`/`StatsResult` serde structs and a generic `GraphQlResponse<T> { data: Option<T>, errors: Option<Vec<GraphQlError>> }` envelope; hand-rolled bounded retry (3 attempts, exponential 250ms→2s cap) rather than a crate; add `run`-state SQLite helpers and a `pubkey`-only insert to the store; a minimal `clap`-free or single-subcommand binary for `--resume`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| HTTP transport + JSON envelope decode | GraphQL client module (`src/graphql/`) | — | D-11 seam; Phase 3 extends the same transport |
| Cursor loop / termination / drift probe | Enumerator (`src/enumerate.rs` or similar) | GraphQL client | Walk orchestration is application logic, not transport |
| Pubkey persistence (`INSERT OR IGNORE`) | Phase-1 store single-writer | — | D-04/D-05; respect single-writer invariant |
| Run-state read/update (cursor, maxLevId, status) | Phase-1 store (new helpers) | — | D-03 reuses `run` schema; helpers don't exist yet |
| Retry/backoff policy | GraphQL client (per-request) | Enumerator (abort decision) | Per-request retry is transport; abort-run is policy (D-06) |
| Entry point / `--resume` arg | Binary (`src/main.rs`) | — | D-12 minimal subcommand |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `reqwest` | `0.13` | HTTP client for `POST /graphql` | The de-facto Rust HTTP client; project stack already names it [CITED: CONTEXT D-Discretion / Cargo.toml comment] |
| `tokio` | `1` | Async runtime for reqwest + Phase-3 pipeline | Project stack; Phase 3 (INGEST-03) needs `tokio → channel → rayon` [CITED: REQUIREMENTS INGEST-03] |
| `serde` | `1.0` | Derive (de)serialize for envelope + page structs | **Already in Cargo.toml** (`features = ["derive"]`) [VERIFIED: Cargo.toml] |
| `serde_json` | `1.0` | Build request body / parse `{data,errors}` | **Already in Cargo.toml** [VERIFIED: Cargo.toml] |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `clap` | `4` | `--resume` flag parsing | Optional — D-12 says minimal; a single bool flag can be parsed from `std::env::args()` without clap. If clap is added, gate it to derive a one-flag struct only; full CLI is Phase 5. |
| `thiserror` | `2` | Typed client error enum | Optional but recommended for the `GraphQlError`/transport/retry-exhausted error taxonomy; keeps the two-layer mapping explicit. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| async reqwest 0.13 | `reqwest::blocking` | Blocking matches the sync store with zero runtime ceremony and is simpler for a sequential Phase-2 walk. **Rejected** because D-11 requires the transport be reusable by Phase 3, which needs async (INGEST-03 `tokio → bounded channel → rayon`). Building blocking now forces a Phase-3 rewrite — violates D-11. |
| `backoff` crate | hand-rolled retry | `backoff` is **v0.4.0 and unmaintained**; pulls `tokio`/`futures` features and a `rand` jitter dep for ~20 lines of logic. Fail-fast spirit (D-06) wants 3 attempts max — trivial to hand-roll. **Reject the crate** per "deps land in owning phase" discipline. |
| `tokio-retry` 0.3 | hand-rolled retry | Same conclusion — a generic retry stream is overkill for a 3-attempt ceiling; hand-roll keeps the abort-on-exhaustion decision (D-06/D-07) inline and readable. |
| `reqwest-middleware` + `reqwest-retry` | hand-rolled | Adds two deps and a middleware layer; retry policy here is bespoke (`503` retries but `INVALID_CURSOR` does NOT — it restarts from page 1). Middleware retry can't express "restart pagination," so the loop must own it anyway. |
| GraphQL codegen (cynic/graphql_client) | hand-written structs | Explicitly forbidden by **D-10**. The contract has introspection on, but a single query needs no toolchain. |

**Installation:**
```bash
cargo add reqwest --no-default-features --features json,charset,http2
cargo add tokio --features rt-multi-thread,macros
cargo add thiserror   # optional, recommended
# serde + serde_json already present
```

**TLS note:** the adapter default-binds `127.0.0.1:8080` and the DeepFry compose publishes `127.0.0.1:8080:8080` (plain HTTP, loopback) [CITED: contract §10]. **No TLS is required for the Phase-2 target**, so `reqwest` can drop the default `native-tls`/`rustls` features (`--no-default-features --features json`). If a future deployment puts the adapter behind HTTPS, add `rustls-tls`. Keep this out of scope (endpoint config is OPS-03 / Phase 4).

**Version verification:**
- `reqwest` latest `0.13.4` [VERIFIED: cargo search] — use `"0.13"`.
- `tokio` latest `1.52.3` [VERIFIED: cargo search] — use `"1"`.
- `serde`/`serde_json` already pinned `1.0` [VERIFIED: Cargo.toml].
- `backoff` latest `0.4.0` [VERIFIED: cargo search] — **rejected** (see alternatives).

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `reqwest` | crates.io | ~7 yrs | very high (top-20 crate) | github.com/seanmonstar/reqwest | OK | Approved |
| `tokio` | crates.io | ~7 yrs | very high (top crate) | github.com/tokio-rs/tokio | OK | Approved |
| `serde` / `serde_json` | crates.io | established | very high | github.com/serde-rs | OK | Already in tree |
| `thiserror` | crates.io | established | very high | github.com/dtolnay/thiserror | OK | Approved (optional) |
| `clap` | crates.io | established | very high | github.com/clap-rs/clap | OK | Optional (D-12 minimal) |
| `backoff` | crates.io | v0.4.0, stale | moderate | github.com/ihrwein/backoff | SUS (unmaintained) | REMOVED — hand-roll instead |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** `backoff` — not slopsquat, but unmaintained; removed by design choice, not a security gate. No `checkpoint:human-verify` needed since it is not being installed.

*Crate names here are from training knowledge cross-checked against `cargo search` output this session. reqwest/tokio/serde are the canonical Rust HTTP/async/serialization crates — HIGH confidence they are legitimate. Treat as `[VERIFIED: crates.io]` only after the planner confirms each `cargo add` resolves.*

## Architecture Patterns

### System Architecture Diagram

```
                          --resume flag (D-12, std::env or clap)
                                     |
                                     v
  [stats query] --maxLevId_start--> [Enumerator loop] --persist--> [Phase-1 Store]
       |                              |   ^                          (single writer)
       |                              |   |                            |
       v                              |   | endCursor->after          +-- pubkey  INSERT OR IGNORE (D-04)
  GraphQlClient.query::<StatsResult>  |   | (verbatim, opaque)         +-- run     UPDATE last_cursor,
       |                              v   |                                         max_lev_id_start/end,
       +<--- POST /graphql ----- GraphQlClient.query::<AuthorsPage>                 status (D-03/D-07/D-09)
                |                     |
                |   200 {data,errors} |  hasMore==false -> terminate cleanly (INGEST-01)
                |   503 -> retry      |  errors[].code==INVALID_CURSOR -> after=null, restart pg1 (D-08)
                |   413 -> abort      |  retry ceiling exhausted -> mark run aborted, preserve cursor (D-06/D-07)
                v
        LMDB2GraphQL /graphql
```

Data flow: read `stats.maxLevId` once at start → store as `max_lev_id_start`. Loop: fetch a page of `authors`, parse envelope, persist each pubkey + advance `last_cursor` to the page's `endCursor`, repeat until `hasMore` is false. At clean termination, read `stats.maxLevId` again → store `max_lev_id_end`, mark `status='done'`.

### Recommended Project Structure
```
src/
├── main.rs            # D-12 minimal entry point: parse --resume, build client+store, run walk
├── lib.rs             # add `pub mod graphql; pub mod enumerate;`
├── model.rs           # existing — Run/Score/... (unchanged; maybe add Run read helpers nearby)
├── graphql/           # D-11 reusable client module
│   ├── mod.rs         #   GraphQlClient { http: reqwest::Client, endpoint: String }
│   ├── client.rs      #   query<T: DeserializeOwned>(query, variables) -> Result<T, ClientError>
│   ├── envelope.rs    #   GraphQlResponse<T>, GraphQlError, extensions.code parsing
│   └── queries.rs     #   AUTHORS_QUERY, STATS_QUERY consts + AuthorsPage/StatsResult structs
├── enumerate.rs       # the authors walk + resume/abort/drift orchestration
└── store/             # existing — ADD run-state + pubkey-only helpers (see below)
```

### Pattern 1: Generic GraphQL envelope (parse errors before trusting data)
**What:** A single generic response type so in-body `errors[]` can never be silently dropped (criterion 3 / INGEST-04).
**When to use:** Every query the client issues.
**Example:**
```rust
// envelope.rs — hand-written per D-10; mirrors contract §3 "{data, errors}" + §7 codes.
use serde::Deserialize;

#[derive(Deserialize)]
pub struct GraphQlResponse<T> {
    pub data: Option<T>,
    #[serde(default)]
    pub errors: Option<Vec<GraphQlError>>,
}

#[derive(Deserialize, Debug)]
pub struct GraphQlError {
    pub message: String,
    #[serde(default)]
    pub extensions: Option<Extensions>,
}

#[derive(Deserialize, Debug)]
pub struct Extensions {
    pub code: Option<String>,   // "INVALID_CURSOR" | "TOO_MANY_AUTHORS" | absent (internal/validation)
}
```

### Pattern 2: Two-layer dispatch in the client
**What:** HTTP status decides transport errors; body `errors[]` decides application errors; only then is `data` trusted.
**When to use:** Inside `GraphQlClient::query`.
**Example:**
```rust
// client.rs (async). Maps contract §7 to a typed error the loop can match on.
pub enum ClientError {
    Unavailable,          // HTTP 503 -> caller retries (D-08)
    PayloadTooLarge,      // HTTP 413 -> caller aborts (never retried; client bug)
    Transport(reqwest::Error),
    Graphql { code: Option<String>, message: String }, // in-body errors[] (D-08)
}

async fn query<T: serde::de::DeserializeOwned>(
    &self, query: &str, variables: serde_json::Value,
) -> Result<T, ClientError> {
    let resp = self.http.post(&self.endpoint)
        .json(&serde_json::json!({ "query": query, "variables": variables }))
        .send().await.map_err(ClientError::Transport)?;
    match resp.status().as_u16() {
        503 => return Err(ClientError::Unavailable),     // contract §7 transport
        413 => return Err(ClientError::PayloadTooLarge), // contract §7 transport
        _ => {}
    }
    let env: GraphQlResponse<T> = resp.json().await.map_err(ClientError::Transport)?;
    if let Some(errs) = env.errors {                     // contract §7 "always check errors even on 200"
        let e = &errs[0];
        return Err(ClientError::Graphql {
            code: e.extensions.as_ref().and_then(|x| x.code.clone()),
            message: e.message.clone(),
        });
    }
    env.data.ok_or(ClientError::Graphql { code: None, message: "null data".into() })
}
```

### Pattern 3: Reusable-client extensibility (D-11 seam)
**What:** `query<T>` is generic over the response type; adding `latestPerAuthor` in Phase 3 is a new const query string + a new struct + a new typed method — **no transport change**.
**When to use:** Design now so Phase 3 is additive.
```rust
impl GraphQlClient {
    pub async fn authors(&self, after: Option<&str>, limit: i64) -> Result<AuthorsPage, ClientError> {
        self.query(AUTHORS_QUERY, json!({ "after": after, "limit": limit })).await
    }
    pub async fn stats(&self) -> Result<StatsResult, ClientError> {
        self.query(STATS_QUERY, json!({})).await
    }
    // Phase 3 ADDS: pub async fn latest_per_author(...) -> Result<Vec<AuthorGroup>, _>
}
```

### Anti-Patterns to Avoid
- **Parsing or constructing the cursor.** It is opaque (base64 of internal keys for `events`; "happens to be last pubkey hex" for `authors` — but **do not rely on that**) [CITED: contract §6.4]. Always pass `endCursor` back verbatim as `after`. If you ever see `INVALID_CURSOR`, it is a *client bug*, not adapter trouble — D-08 says restart from page 1.
- **Trusting `data` before checking `errors`.** The adapter returns HTTP 200 for any query that reaches the resolver; application errors live only in `errors[]` [CITED: contract §7].
- **Reusing a cursor across queries.** `events` and `authors` cursors are not interchangeable [CITED: contract §4/§6.4]. Irrelevant in Phase 2 (only `authors`), but the reusable client (D-11) must not share cursor state across query types.
- **Advancing the cursor on failure.** D-07: a failed/aborted page must leave `last_cursor` at the *previous successful* page's `endCursor`. Persist the cursor only after a page is fully persisted.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HTTP + connection pooling + JSON | Custom hyper/socket code | `reqwest` 0.13 `.json()` | Decades of edge cases (keep-alive, chunked, gzip) |
| GraphQL envelope decode | A bespoke JSON walker | `serde` generic `GraphQlResponse<T>` | Type-safe, already a dependency |
| SQLite write path / batching / WAL | A second writer connection | The Phase-1 single-writer store | **Single-writer invariant is load-bearing** [VERIFIED: store/mod.rs] — never open a second write connection |
| Schema migration for resume state | New tables / ALTER | Existing `run` columns | D-03 — the `run` table already has `last_cursor`, `max_lev_id_start/end`, `status`, `config_json` [VERIFIED: store/schema.rs] |

**Key insight:** Almost everything for *persistence* already exists. The only genuinely new code is (a) the HTTP/GraphQL transport and (b) a handful of `run`-state SQL helpers that Phase 1 didn't need. Do not reinvent the store; extend it.

## Runtime State Inventory

Not a rename/refactor/migration phase — **section omitted by trigger rule** (this is a greenfield feature addition). The one migration-adjacent fact: **no SQLite schema migration is needed** (D-03), so there is no stored-data rename to track.

## Resume Against the Real Phase-1 `run` Schema

This is the section the planner most needs, because **the required `run`-state API does not exist yet.** Verified against `src/store/mod.rs`, `schema.rs`, `writer.rs`, `queries.rs`, `model.rs`:

**What exists today [VERIFIED: store source]:**
- `Store::open(&Path) -> rusqlite::Result<Store>` — opens WAL, spawns the single writer thread.
- `Store::begin_run(&self, config_json: &str) -> rusqlite::Result<i64>` — inserts a `run` row `status='running'`, returns `run_id`. Uses a short-lived write connection (not the actor).
- `Store::persist(Persist)` — enqueues to the writer actor. **Coupled to score+signal**: `writer_loop` writes `pubkey` *and* `score` *and* `signal` for every `Persist` [VERIFIED: writer.rs lines 60-76]. There is no score/signal for enumeration, so **`Persist` is the wrong vehicle for pubkey-only inserts.**
- `Store::reader() -> Connection`, `Store::close()`.
- `model::Run` struct mirrors the row (all fields present, including `last_cursor: Option<String>`, `max_lev_id_start/end: Option<i64>`, `status: String`).
- `UPSERT_PUBKEY` const = `"INSERT INTO pubkey (pubkey) VALUES (?1) ON CONFLICT(pubkey) DO NOTHING"` [VERIFIED: writer.rs line 35] — exactly the D-04 semantics, but currently only reachable through the `Persist` path.

**What the planner must ADD (does not exist):**
1. **Pubkey-only idempotent insert.** Two viable approaches:
   - *Recommended:* a writer-actor message variant or a dedicated `Store::insert_pubkeys(&[String])` that batches through the single writer using the existing `UPSERT_PUBKEY` const. This honors the single-writer invariant and reuses the batching/WAL machinery. **Do not add a second write connection.** Implementation requires extending the channel payload (currently `flume::Sender<Persist>`) — either widen `Persist` semantics or introduce an enum message. The enum-message refactor is the cleaner D-11-aligned choice and keeps Phase-3's fetch payload additive too.
   - *Simpler but allowed:* since `begin_run` already opens a short-lived write connection for one INSERT, a `Store::update_run_*` / `insert_pubkey_batch` can follow the same short-lived-connection pattern **as long as it never runs concurrently with the actor on overlapping rows** — for Phase 2 the walk is sequential and pubkey/run rows don't overlap score/signal, so this is safe. Prefer the actor route for consistency; document the choice.
2. **`Store::latest_unfinished_run(&self) -> rusqlite::Result<Option<Run>>`** (D-01). SQL:
   ```sql
   SELECT run_id, started_at, finished_at, max_lev_id_start, max_lev_id_end,
          last_cursor, config_json, status
   FROM run WHERE status != 'done' ORDER BY run_id DESC LIMIT 1
   ```
   Open via `reader()`. Returns `None` → `--resume` starts fresh (D-02).
3. **`Store::set_run_cursor(run_id, cursor: &str)`** — persist `last_cursor` after each fully-persisted page (D-07). `UPDATE run SET last_cursor=?2 WHERE run_id=?1`.
4. **`Store::set_run_max_lev_start(run_id, v)` / `set_run_max_lev_end(run_id, v)`** (D-09). Set start right after `begin_run`/at resume start; set end at clean termination.
5. **`Store::mark_run_aborted(run_id)`** (D-07): `UPDATE run SET status='aborted', finished_at=?2 WHERE run_id=?1` — **does not touch `last_cursor`** (cursor already points at last good page).
6. **`Store::mark_run_done(run_id)`**: `UPDATE run SET status='done', finished_at=?2, max_lev_id_end=?3 WHERE run_id=?1`.

**Resume flow (D-01/D-02/D-03):**
- `--resume` present → `latest_unfinished_run()`. If `Some(run)`: reuse `run.run_id`, set `after = run.last_cursor` (note: a `None`/empty cursor means resume from page 1 — equivalent to fresh start of the same run id), re-record `max_lev_id_start` (or keep the original; recording original at first start is more faithful to D-09 — keep the existing value if present). If `None`: `begin_run` fresh.
- No `--resume` (or fresh): always `begin_run`.
- `config_json` for Phase 2: a minimal JSON capturing the page `limit` and endpoint is sufficient (weights snapshot is Phase 4+). The column is `NOT NULL` so pass at least `"{}"` — matching Phase-1 tests which use `"{}"`.

## Opaque-Cursor Pagination Loop

Precise loop structure (criterion 1 / INGEST-01, criterion 4 / D-09):

```rust
// enumerate.rs (async; persist calls are sync into the store)
let mut after: Option<String> = run.last_cursor.clone(); // resume point or None for page 1
let max_start = client.stats().await?.max_lev_id;        // D-09 drift probe (start)
store.set_run_max_lev_start(run_id, max_start)?;
let mut count: u64 = 0;
loop {
    let page = match client.authors(after.as_deref(), LIMIT).await {
        Ok(p) => p,
        Err(ClientError::Graphql { code: Some(c), .. }) if c == "INVALID_CURSOR" => {
            after = None;                  // D-08: restart pagination from page 1
            continue;                      // do NOT advance, do NOT abort
        }
        Err(e) => return handle_retry_or_abort(e, &store, run_id, &after), // D-06/D-07/D-08
    };
    for pk in &page.authors { /* validated 64-char lowercase hex */ }
    store.insert_pubkeys(&page.authors)?;  // D-04 INSERT OR IGNORE, single writer
    count += page.authors.len() as u64;
    eprintln!("enumerated {count} distinct pubkeys");      // D-04 stdout progress
    match page.end_cursor {
        Some(c) => { store.set_run_cursor(run_id, &c)?; after = Some(c); } // advance only after persist (D-07)
        None => break,                     // hasMore==false -> clean termination (INGEST-01)
    }
    if !page.has_more { break; }           // belt-and-suspenders; endCursor==null is the canonical signal
}
let max_end = client.stats().await?.max_lev_id;          // D-09 drift probe (end)
store.mark_run_done(run_id, max_end)?;
```

**Cursor/termination rules from the contract [CITED: contract §6.4]:** `endCursor` is opaque, passed verbatim; `null endCursor ⇒ done`; within one snapshot every distinct pubkey appears exactly once and the walk terminates cleanly at the end of the keyspace. `hasMore` and `endCursor==null` are equivalent termination signals — use `endCursor==null` as canonical (it is what the contract's own JS loop uses).

**Page `limit` recommendation (Discretion):** use `limit: 500` (the ceiling). `authors` enumeration is O(distinct authors) seek-skip [CITED: contract §6.4], so the bottleneck is round-trips, not per-page work; 500 minimizes round-trips. **Body-size check:** a page of 500 × 64-char hex pubkeys ≈ 500 × ~70 bytes (quoted + comma) ≈ 35 KB response — far under the 256 KiB *request* limit (which constrains `latestPerAuthor`/`ids` *input* arrays, not `authors` responses). The `authors` request body is tiny (just `after` + `limit`). **256 KiB is a non-issue for Phase 2** (see Pitfalls for the Phase-3 caveat).

## Two-Layer Error Handling

Mapping each contract condition to the locked policy (criterion 3 / INGEST-04 / D-08):

| Source | Condition | `ClientError` | Loop policy | Decision |
|--------|-----------|---------------|-------------|----------|
| Transport | HTTP `503` | `Unavailable` | bounded backoff-retry; on exhaustion abort (no cursor advance) | D-06/D-07/D-08 |
| Transport | HTTP `413` | `PayloadTooLarge` | **abort immediately** (client bug — should never happen for `authors`; never retried) | D-08 spirit (parse-and-act) |
| In-body | `extensions.code == "INVALID_CURSOR"` | `Graphql{code}` | drop cursor, `after=null`, restart page 1 (no abort, no retry) | D-08 |
| In-body | `extensions.code == "TOO_MANY_AUTHORS"` | `Graphql{code}` | cannot occur in Phase 2 (`authors` takes no author list); if seen, abort as a logic bug | contract §7 |
| In-body | no code, `"internal error"` | `Graphql{code:None}` | treated as transient → bounded backoff-retry, then abort | contract §7 "retry with backoff" + D-06 |
| In-body | no code, validation msg | `Graphql{code:None}` | abort immediately (bad input, retry won't help) | contract §7 "fix the input" |
| Transport | connection refused / timeout | `Transport` | bounded backoff-retry then abort (adapter may be booting → 503-equivalent) | D-06 |

The generic envelope (Pattern 1) guarantees in-body `errors[]` are surfaced as `ClientError::Graphql` before `data` is ever read — satisfying "GraphQL errors are parsed and acted on, never ignored" (D-08, criterion 3).

## Bounded Retry + Backoff (Discretion)

Hand-rolled, fail-fast (D-06). Recommended concrete defaults:
- **Ceiling:** 3 attempts total (1 initial + 2 retries). Small per the fail-fast spirit; surfaces persistent trouble to the operator fast (D-06).
- **Backoff:** exponential base 250 ms, ×2 per attempt, cap 2 s → waits of ~250 ms, ~500 ms before the final failure. Optional ±20% jitter (a single `fastrand`-free `(t/5)` deterministic skew or none — jitter is unnecessary for a single client; skip it to avoid a `rand` dep).
- **Retryable:** `Unavailable` (503), `Transport` (timeout/refused), and codeless `"internal error"`. **Non-retryable:** `PayloadTooLarge`, `INVALID_CURSOR` (handled separately by restart), validation errors, `TOO_MANY_AUTHORS`.
- **On exhaustion:** `mark_run_aborted(run_id)` (cursor untouched), print the error to stderr, exit non-zero (D-07). `--resume` later continues from the preserved `last_cursor`.

```rust
async fn fetch_page_with_retry(client: &GraphQlClient, after: Option<&str>) -> Result<AuthorsPage, ClientError> {
    const MAX_ATTEMPTS: u32 = 3;
    let mut delay = std::time::Duration::from_millis(250);
    for attempt in 1..=MAX_ATTEMPTS {
        match client.authors(after, 500).await {
            Ok(p) => return Ok(p),
            Err(e) if is_retryable(&e) && attempt < MAX_ATTEMPTS => {
                tokio::time::sleep(delay).await;
                delay = (delay * 2).min(std::time::Duration::from_secs(2));
            }
            Err(e) => return Err(e),  // non-retryable OR ceiling reached
        }
    }
    unreachable!()
}
```
**Sleep dependency:** `tokio::time::sleep` (needs tokio `time` feature) — no extra crate. If the entry point were blocking, `std::thread::sleep` instead.

## Common Pitfalls

### Pitfall 1: Async transport vs sync store boundary
**What goes wrong:** Calling the sync `store.insert_pubkeys()` from an async loop is fine (it's a blocking call on the current task), but if Phase 3's concurrent fetchers ever `.await` while holding store state, or if someone wraps the store in an async mutex, throughput collapses or deadlocks.
**Why it happens:** Mixing `reqwest` async with `rusqlite` sync invites a half-async design.
**How to avoid:** Phase 2's walk is strictly sequential — one in-flight request, persist, repeat. Make persist calls plain synchronous calls inside the async fn (they're short — a `flume::send` or a batched UPSERT). Do NOT spawn the walk across tasks. Leave Phase 3 to introduce `tokio → bounded channel → rayon` with the store writer thread as the sync sink (the flume seam already exists for exactly this — `model::Persist` over `flume::Sender`). **Document that the store is the sync boundary; never wrap it in `tokio::sync::Mutex`.**

### Pitfall 2: Advancing the cursor before the page is durable
**What goes wrong:** Persisting `last_cursor = endCursor` before the page's pubkeys are committed means a crash mid-batch resumes *past* unwritten pubkeys → silent gaps.
**Why it happens:** Natural to update everything at once.
**How to avoid:** Order is load-bearing: `insert_pubkeys(page)` → (writer commits) → `set_run_cursor(endCursor)`. D-07: "a failure never advances the cursor." Because pubkey insert is `INSERT OR IGNORE`, re-fetching the same page on resume is harmless (idempotent) — so the only correctness requirement is *never advance past unwritten data*, which the ordering guarantees. Note the writer is batched (BATCH=8192) — a `set_run_cursor` via a short-lived connection could commit before the actor flushes the pubkey batch; **either route the cursor update through the same writer actor (preserves ordering) or call `store.close()`/a flush before each cursor write.** Simplest correct design: route both pubkey inserts and run-state updates through the single writer actor as ordered messages.

### Pitfall 3: Snapshot drift misread as an error
**What goes wrong:** A pubkey added mid-walk may or may not appear depending on cursor position; treating "count changed vs stats" as a failure aborts a healthy run.
**Why it happens:** The "distinct-once" guarantee is **per-snapshot**, and the corpus is live [CITED: contract §6.4/§8].
**How to avoid:** D-09 record-and-continue: capture `max_lev_id_start` and `max_lev_id_end`; if they differ, the corpus changed mid-walk — record it (it's the drift probe), do **not** abort. Do not compare enumerated count to `eventCount` (authors ≠ events; `authors` has no count anyway, by design [CITED: contract §6.4]).

### Pitfall 4: Treating `INVALID_CURSOR` as transient
**What goes wrong:** Retrying an `INVALID_CURSOR` re-sends the same bad cursor → same error → wasted attempts → abort.
**Why it happens:** Lumping all in-body errors into the retry bucket.
**How to avoid:** `INVALID_CURSOR` is a *client bug* (we never construct cursors, so it should never happen, but the contract says fail-closed) → restart from page 1 (`after=null`), do not retry, do not abort. Distinct branch before the retry helper (see loop in §"Opaque-Cursor Pagination Loop").

### Pitfall 5: `serde(default)` on `errors`/`data` and the empty-data case
**What goes wrong:** A `200` with `errors` and `data: null` (internal error) could panic if you unwrap `data` first.
**How to avoid:** Check `errors` before `data` (Pattern 2). Use `Option<T>` for `data` and `Option<Vec<_>>` (with `#[serde(default)]`) for errors so either-or-both shapes parse.

## Code Examples

### Authors query string + page struct (hand-written, D-10)
```rust
// queries.rs — verified field names against contract §4/§6.4 (camelCase: hasMore, endCursor, maxLevId)
pub const AUTHORS_QUERY: &str =
  "query($after:String,$limit:Int){ authors(after:$after,limit:$limit){ authors hasMore endCursor } }";

pub const STATS_QUERY: &str = "query{ stats{ maxLevId } }";

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AuthorsPage {
    pub authors: Vec<String>,     // 64-char lowercase hex, byte-ascending
    pub has_more: bool,
    pub end_cursor: Option<String>,
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AuthorsData { pub authors: AuthorsPage }   // wraps the top-level field name

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StatsData { pub stats: StatsResult }

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StatsResult { pub max_lev_id: i64 }
```
Note: GraphQL responses nest under the query field name (`data.authors.authors`), so `query::<AuthorsData>` then `.authors` — or alias. The generic `query<T>` returns the `data` object; pick `T = AuthorsData`.

### Minimal entry point (D-12)
```rust
// main.rs — single flag, no full clap surface (D-12). clap optional.
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let resume = std::env::args().any(|a| a == "--resume");
    let endpoint = std::env::var("LMDB2GRAPHQL_URL")     // Phase-4 config is OPS-03; env is fine for now
        .unwrap_or_else(|_| "http://127.0.0.1:8080/graphql".into());
    let store = pubkey_iterator::store::Store::open(std::path::Path::new("spamhunter.sqlite"))?;
    let client = pubkey_iterator::graphql::GraphQlClient::new(endpoint);
    pubkey_iterator::enumerate::run(&store, &client, resume).await?;
    store.close()?;
    Ok(())
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `reqwest` 0.11/0.12 | `reqwest` 0.13 | 0.13 line current | API stable for `.json()`; confirm feature flags (`json`) |
| GraphQL codegen mandatory | Hand-written for single queries fine | n/a | Matches D-10; codegen is overkill here |
| `backoff` crate | hand-rolled / `tokio::time` | `backoff` stale at 0.4.0 | Avoid the dep |

**Deprecated/outdated:** `backoff` 0.4.0 (unmaintained); GraphQL-codegen toolchains for a one-query client (D-10 forbids).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Async reqwest (not blocking) is the right default because Phase 3 needs async | Summary / Standard Stack | LOW — if Phase 3 ends up reading the persisted `pubkey` table instead of re-walking live (CONTEXT defers that), a blocking client would have been simpler. Either works for Phase 2; async preserves D-11 optionality. |
| A2 | Endpoint supplied via env var `LMDB2GRAPHQL_URL` for Phase 2 | Code Examples / entry point | LOW — endpoint config is explicitly OPS-03/Phase 4; env is a stopgap. Planner may hardcode the default loopback URL instead. |
| A3 | Retry ceiling 3, backoff 250ms→2s | Bounded Retry | LOW — Discretion-area; any small bounded value satisfies D-06. Tune if the adapter's `503` startup window is longer. |
| A4 | Page `limit` 500 | Pagination Loop | LOW — Discretion; 500 is the ceiling and minimizes round-trips; smaller is also valid. |
| A5 | Routing pubkey inserts + run-state updates through the single writer actor (vs short-lived connections) | Resume / Pitfall 2 | MEDIUM — affects cursor-durability ordering. If the planner uses short-lived write connections instead, it MUST add an explicit flush before each `set_run_cursor` so the cursor never advances past an un-flushed pubkey batch. Both designs are correct; the choice must be deliberate. |
| A6 | Plain HTTP (no TLS) is sufficient | Standard Stack / TLS note | LOW — contract §10 says default loopback HTTP; a TLS-fronted deployment would need `rustls-tls` (Phase 4 concern). |

**No `[ASSUMED]` package legitimacy risks remain unflagged** — all crates are mainstream and `cargo search`-confirmed; the planner should still confirm each `cargo add` resolves before locking.

## Open Questions

1. **Writer-actor payload extension vs short-lived connection for pubkey/run writes.**
   - What we know: the existing channel is `flume::Sender<Persist>` and `Persist` carries score/signal (not present for enumeration); `begin_run` already uses a short-lived write connection for one INSERT.
   - What's unclear: whether to widen the actor message into an enum (`Persist::Score | Persist::Pubkeys | Persist::RunUpdate`) — cleaner for D-11/Phase-3 — or use short-lived connections for the Phase-2-only run/pubkey writes.
   - Recommendation: prefer the enum-message refactor (preserves single-writer ordering, makes Phase 3 additive). If time-boxed, short-lived connections are acceptable **with** an explicit flush-before-cursor-advance (see A5). Planner decides; both are correct.

2. **Whether to record `max_lev_id_start` fresh on resume or keep the original run's value.**
   - What we know: D-09 wants start/end as a drift probe across the *whole* walk; a resumed run already has a `max_lev_id_start`.
   - Recommendation: on resume, keep the existing `max_lev_id_start` if non-null (faithful to "start of this run's walk"); only set it when null. Set `max_lev_id_end` at clean termination.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Rust/cargo toolchain | build | ✓ (Phase 1 built) | edition 2021 | — |
| LMDB2GraphQL adapter (`/graphql`) | live enumeration test only | ✗ (not verified running this session) | v1.2 contract | Unit tests use a **mocked HTTP adapter** (`wiremock` or a tiny `tokio` server); a live run requires the adapter on `127.0.0.1:8080` |
| `crates.io` access for `cargo add` | reqwest/tokio install | ✓ (cargo search succeeded) | reqwest 0.13.4, tokio 1.52.3 | vendor/offline cache |

**Missing dependencies with no fallback:** none blocking — the build and unit tests do not need a live adapter.
**Missing dependencies with fallback:** the live adapter; the full vertical-slice "connectivity proof" (criterion-level) needs it, but every loop/error/resume behavior is unit-testable against a mock (see Validation Architecture). Recommend `wiremock` (`0.6`) as a dev-dependency for HTTP mocking, or a hand-rolled `tokio` oneshot server to avoid the dep — planner's call (dev-dep, fail-fast spirit doesn't apply to test infra).

## Validation Architecture

> nyquist_validation is enabled (config.json `workflow.nyquist_validation: true`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Rust built-in `#[test]` + `#[tokio::test]` (async tests) |
| Config file | none — `cargo test` (matches Phase 1 convention) |
| Quick run command | `cargo test --lib` |
| Full suite command | `cargo test` |

### Phase Requirements → Test Map (the 4 success criteria)
| Criterion / Req | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| C1 / INGEST-01 — clean termination | Loop terminates when `endCursor==null`, visits each page once | unit (mocked adapter returns 3 pages then null) | `cargo test enumerate::tests::terminates_on_null_cursor` | ❌ Wave 0 |
| C1 / INGEST-01 — resumability | Resume continues from stored `last_cursor`; no `--run-id` | unit (seed `run` row w/ cursor, assert walk starts at `after`) | `cargo test enumerate::tests::resume_from_last_cursor` | ❌ Wave 0 |
| C1 — pubkey persistence | Each enumerated pubkey lands in `pubkey` via INSERT OR IGNORE; overlap on resume is idempotent | unit (count `pubkey` rows after overlapping resume) | `cargo test enumerate::tests::pubkeys_idempotent_on_resume` | ❌ Wave 0 |
| C3 / INGEST-04 — `503` retry, no advance | `503` triggers bounded retry; cursor unchanged on abort | unit (mock returns 503×N then success / then exhaust→abort) | `cargo test enumerate::tests::retry_503_no_cursor_advance` | ❌ Wave 0 |
| C3 / INGEST-04 — `INVALID_CURSOR` restart | in-body `INVALID_CURSOR` resets `after=null`, restarts page 1 | unit (mock returns INVALID_CURSOR once) | `cargo test enumerate::tests::invalid_cursor_restarts` | ❌ Wave 0 |
| C3 / INGEST-04 — in-body errors not dropped | `200` body with `errors[]` is surfaced, never silently treated as success | unit (envelope parse test) | `cargo test graphql::tests::inbody_errors_surface` | ❌ Wave 0 |
| C3 — abort marks `aborted`, preserves cursor | On retry exhaustion: `status='aborted'`, `last_cursor` unchanged | unit (assert run row after forced abort) | `cargo test enumerate::tests::abort_preserves_cursor` | ❌ Wave 0 |
| C4 / D-09 — drift probe | `max_lev_id_start`/`_end` recorded; differing values do NOT abort | unit (mock `stats` returns different maxLevId start vs end) | `cargo test enumerate::tests::records_drift_does_not_abort` | ❌ Wave 0 |
| Connectivity proof (vertical slice) | Real walk against live adapter completes | manual / integration (gated on live adapter) | `LMDB2GRAPHQL_URL=... cargo run -- ` (manual) | ❌ requires live adapter |

### What is unit-testable vs needs the live adapter
- **Unit-testable with a mocked adapter (most of it):** cursor loop, termination, resume, retry/backoff branching, error-taxonomy mapping, envelope parsing, drift recording, abort/cursor-preservation, pubkey idempotency. Use `wiremock` (dev-dep) or a tiny `tokio::net` stub serving canned JSON. The `GraphQlClient` should take the endpoint as a field so tests point it at the mock — **design for injectability** (no hardcoded URL inside the client).
- **Property/held-out:** "every distinct pubkey exactly once per snapshot" is the adapter's guarantee, not ours — we only need to assert *we don't drop or duplicate* across a resume boundary (overlap is harmless via INSERT OR IGNORE). A property test: arbitrary page splits + a resume cut at any page boundary ⇒ the union of persisted pubkeys equals the full mocked set.
- **Genuinely needs the live adapter:** the connectivity proof itself — that the hand-written query string + structs deserialize the *real* v1.2 response and the walk completes against real data. This is the phase's raison d'être ("prove the contract end-to-end against the live adapter"). Make it a documented manual/integration step gated on the adapter being reachable; it cannot run in CI without the adapter. Mark it a manual `must_have`.

### Sampling Rate
- **Per task commit:** `cargo test --lib`
- **Per wave merge:** `cargo test`
- **Phase gate:** full suite green + one manual live-adapter run completing a full walk (connectivity proof).

### Wave 0 Gaps
- [ ] `tests`/`src/enumerate.rs` `#[cfg(test)]` — all 8 unit behaviors above
- [ ] `src/graphql/` envelope + query parse tests (fixture JSON from contract §7/§6.4 examples)
- [ ] HTTP mock harness: add `wiremock = "0.6"` dev-dep OR a hand-rolled tokio stub (decide; prefer hand-rolled to avoid dep if a single canned-response server suffices)
- [ ] `run`-state store helper tests (latest_unfinished_run, set_run_cursor, mark_run_aborted/done) — extend `store/mod.rs` `#[cfg(test)]`

## Security Domain

> security_enforcement is enabled (config.json), ASVS level 1.

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Adapter is unauthenticated by design [CITED: contract §1/§10]; nothing to authenticate. |
| V3 Session Management | no | Stateless HTTP, no sessions. |
| V4 Access Control | no | No auth surface introduced; this is a read-only client. |
| V5 Input Validation | yes | Validate the endpoint URL is the expected loopback/configured host; validate returned pubkeys are 64-char lowercase hex before persisting (contract guarantees it, but defense-in-depth before a DB write). |
| V6 Cryptography | no | Plain loopback HTTP per contract §10; no crypto in scope. TLS deferred to deployment (Phase 4). |
| V7 Error Handling/Logging | yes | Do not log full response bodies that could be large; log status + first error message. Never `format!`-interpolate pubkeys into SQL (Phase-1 already uses `?N` params — keep that for new helpers, T-01-01). |

### Known Threat Patterns for this stack
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via pubkey/cursor into new store helpers | Tampering | Parameterized `?N` binding only (Phase-1 discipline, writer.rs/queries.rs already do this) [VERIFIED: store source] |
| SSRF via attacker-controlled endpoint URL | Tampering/Info disclosure | Endpoint is operator-supplied (env/default loopback), not user-input; document that a future config-driven URL (OPS-03) must validate scheme/host. |
| Unbounded response → memory blowup | DoS | Page `limit` ≤500 (contract clamp) bounds each response; the walk holds one page at a time — no full-corpus buffer (aligns with Phase-3 INGEST-03 spirit). |
| Cursor as opaque token — no injection risk | — | Cursor is passed verbatim, never parsed/constructed; treated as an opaque string on both wire and DB. |

## Project Constraints (from CLAUDE.md)

- Rust subproject of the deepfry monorepo; **commit to main, no feature branches** [from MEMORY + CLAUDE.md].
- **Never delete/overwrite/`rm` files in `~/deepfry/`**; use temp dirs for config testing. (Phase 2 has no config file yet — OPS-03 is Phase 4 — so this is a forward constraint.)
- **Do not cross between sibling projects** (web-of-trust, spam-explorer, etc.) without explicit permission — scope strictly to `spamhunter/pubkey_iterator`.
- Stack discipline: **dependencies land in their owning phase** — Phase 2 adds reqwest/tokio (+ optional thiserror/clap); `rayon` and the fetch-pipeline deps stay for Phase 3.
- Single-writer store invariant is load-bearing — no second write connection [VERIFIED: store/mod.rs comment].
- All SQL binding parameterized (`?N`), never `format!` — T-01-01 [VERIFIED: writer.rs/queries.rs].

## Sources

### Primary (HIGH confidence)
- `contract.md` (LMDB2GraphQL v1.2, code-verified) — §3 request format, §4 schema, §5 types, §6.3 `stats`, §6.4 `authors`, §7 errors, §8 data semantics, §10 deployment, §12 limits cheat-sheet.
- Phase-1 source read directly: `src/store/{mod,schema,writer,queries}.rs`, `src/model.rs`, `src/lib.rs`, `Cargo.toml` — confirmed existing API, single-writer invariant, `UPSERT_PUBKEY` const, `run` columns.
- `cargo search` this session — reqwest 0.13.4, tokio 1.52.3, serde/serde_json 1.0, backoff 0.4.0, thiserror, clap, reqwest-middleware versions.

### Secondary (MEDIUM confidence)
- Training knowledge of reqwest 0.13 `.json()` API and tokio feature flags — standard, stable; confirm feature names on `cargo add`.

### Tertiary (LOW confidence)
- `wiremock` 0.6 as the suggested HTTP mock dev-dep — version from training; verify with `cargo search wiremock` if adopted (or skip in favor of a hand-rolled stub).

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions `cargo search`-verified; reqwest/tokio are project-named.
- Architecture: HIGH — contract is code-verified and pins the wire shape; Phase-1 API read directly.
- Resume/store wiring: HIGH on what exists (read the source), MEDIUM on the recommended new-helper design (two valid approaches — A5/Open Q1).
- Pitfalls: HIGH — derived from contract semantics + verified store threading model.

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (contract is v1.2 stable; re-verify if the adapter upgrades — contract footer says re-verify limits/types/CORS on upgrade).
