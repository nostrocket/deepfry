# Stack Research

**Domain:** High-throughput Rust batch engine for pubkey-level Nostr spam detection (content/behavioral layers, GraphQL-fed, SQLite output, label-driven weight re-tuning). Speed is the top priority. No local LLM / on-device model inference.
**Researched:** 2026-06-25
**Confidence:** HIGH

All versions below were verified live against crates.io on 2026-06-25 (not recalled from training). Where a crate is the clear, opinionated pick it is named; alternatives and explicit "do not use" entries follow.

---

## TL;DR — the prescriptive stack

- **HTTP+JSON, not a GraphQL client:** `reqwest` (rustls) + `serde`/`serde_json`. The contract is one POST to `/graphql` with a hand-written query string. A GraphQL client buys nothing.
- **Concurrency = both, split by stage:** `tokio` for the I/O-bound fetch stage, `rayon` for the CPU-bound analysis stage, joined by a bounded `flume` channel (back-pressured pipeline). Do not run CPU analysis on tokio worker threads.
- **Similarity:** `gaoya` for MinHash + LSH banding index (the near-dup clustering core), `simhash` for a cheap 64-bit per-pubkey content fingerprint, `ahash`/`foldhash` as the hasher everywhere a `HashMap`/feature hash is hot.
- **Text features:** `unicode-segmentation` for grapheme/word tokenization, char/word **n-grams computed by hand** (trivial), Shannon entropy computed by hand over byte/char/token distributions, `whatlang` only if a language signal is wanted. Language-agnostic by default.
- **SQLite:** `rusqlite` with `bundled` SQLite, WAL mode, batched transactions + prepared statements. Not `sqlx` (async + compile-time checking is the wrong tradeoff for a single-writer local batch tool).
- **Model:** `linfa-logistic` (pure-Rust logistic regression) to re-tune layer weights from labels. Not an LLM, not Python, not ONNX.
- **Glue:** `clap` (derive) for CLI, `figment` for layered config/threshold loading, `serde` for everything, `anyhow` + `thiserror` for errors, `tracing` for structured logs, `indicatif` for a corpus-scale progress bar.
- **Allocator:** `mimalloc` as the global allocator (measurable win on highly-concurrent, allocation-heavy hashing/tokenization workloads).

---

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `tokio` | 1.52 | Async runtime for the fetch stage | The de-facto Rust async runtime. The fetch stage is purely network-bound (paginating `authors`, then `latestPerAuthor` batches of ≤1000 authors). `tokio` lets you keep dozens–hundreds of in-flight HTTP requests with one thread pool. Use multi-thread runtime. |
| `rayon` | 1.12 | Data-parallel CPU analysis stage | Per-pubkey analysis (n-grams, entropy, SimHash, MinHash, ratios) is embarrassingly parallel and CPU-bound. `rayon`'s work-stealing `par_iter` saturates all cores with zero manual thread management. Keep it off the tokio threads. |
| `reqwest` | 0.13 | HTTP client (POST GraphQL-over-HTTP) | Mature, ergonomic async client. Build it with `rustls-tls`, `json`, `gzip` features and **disable `default-features`** to drop OpenSSL. The contract is plain POST `application/json`; `reqwest` + `serde_json` covers it completely. |
| `rusqlite` | 0.40 | SQLite output store | Synchronous, zero-overhead bindings to SQLite. The output store has a **single writer** (the batch) and ad-hoc readers — exactly SQLite's sweet spot. Use the `bundled` feature so SQLite is compiled in (no system dep, reproducible). WAL + batched transactions hit 100k+ inserts/sec. |
| `serde` + `serde_json` | 1.0.228 / 1.0.150 | (De)serialize GraphQL responses, config, signals | Universal. GraphQL responses are JSON; parse the `data`/`errors` envelope into typed structs. Also backs config and any cached state. |
| `linfa-logistic` | 0.8 | Logistic-regression weight re-tuning ("backprop") | Pure-Rust logistic regression from the `linfa` ecosystem. Trains a linear model over per-layer signal vectors → P(spam), giving exactly the "re-tune layer weights from human labels" loop the project wants. Dependency-light, fast, no Python, **no LLM**. |
| `clap` | 4.6 | CLI | Standard Rust CLI crate; derive macros give subcommands (`run`, `label`, `tune`, `export`) and flags (`--limit`, `--endpoint`, `--db`, `--dry-run`) with near-zero boilerplate. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `gaoya` | 0.2.2 | MinHash + LSH banding index | The near-duplicate **clustering** core. Provides `MinHasher` and an `LSHIndex` (banded buckets) so you can group a pubkey's events (or pubkeys across the corpus) by Jaccard similarity without O(n²) comparison. Actively maintained (last release 2026-06-15). Use for the repeated-content / templated-text layer. |
| `simhash` | 0.3 | 64-bit SimHash fingerprint + Hamming distance | Cheaper than MinHash for a single 64-bit per-document/per-pubkey fingerprint. Use it for fast coarse "are these two posts near-identical" checks and as a compact per-pubkey content signature stored in SQLite. Complements (does not replace) `gaoya`. |
| `ahash` | 0.8 | Fast non-cryptographic hashing | Default hasher for `HashMap`/`HashSet` in hot paths (feature counting, token sets, dedup). 2–5× faster than SipHash for short keys. |
| `foldhash` | 0.2 | Even-faster hasher (alternative to ahash) | Newer, extremely fast, used by hashbrown upstream. Drop-in for `HashMap` default-hasher swaps where you don't need ahash's DoS resistance (you don't — this is a batch tool). Pick one of ahash/foldhash; benchmark both on your token-counting hot loop. |
| `xxhash-rust` | 0.8 | xxh3 hashing for n-gram → bucket | When hashing n-grams into fixed-width feature buckets / MinHash inputs, `xxh3` is faster than ahash for medium byte slices and is stable/portable across runs (important if hashes get persisted). |
| `blake3` | 1.8 | Cryptographic content hashing / exact-dup keys | Use only where you need a stable, collision-resistant content hash (exact-duplicate detection across runs, content-addressed dedup keys in SQLite). Do **not** use blake3 as a `HashMap` hasher — it is overkill and slower for short keys. |
| `unicode-segmentation` | 1.13 | Grapheme/word/sentence tokenization | Language-agnostic, Unicode-correct word and grapheme iteration. Foundation for tokenization and word n-grams over arbitrary scripts (Nostr content is multilingual + emoji-heavy). |
| `whatlang` | 0.18 | Fast language detection (optional signal) | Pure-Rust, no model download, detects language from short text. Use only if "language mismatch / unexpected script" becomes a useful spam layer. Not required for v1. |
| `flume` | 0.12 | Bounded MPMC channel (fetch → analysis bridge) | The pipeline seam. Bounded `flume` channel applies back-pressure so the fast tokio fetchers can't outrun the rayon analyzers and blow memory on a 100M-event corpus. Works cleanly across async (tokio) and sync (rayon) sides. |
| `figment` | 0.10 | Layered config + threshold loading | Merge defaults → config file (TOML/YAML) → env → CLI overrides into typed structs. Per-layer weights and thresholds live here so tuning output can be written back as a file the next run reads. |
| `anyhow` | 1.0 | Error handling in the binary | Ergonomic error propagation with context at the application boundary (fetch retries, DB errors, parse failures). |
| `thiserror` | 2.0 | Typed errors in library modules | Define precise error enums per layer/module; convert to `anyhow` at the top. |
| `tracing` + `tracing-subscriber` | 0.1 / 0.3 | Structured logging + spans | Per-stage spans (`fetch`, `analyze`, `persist`) with timing; essential to profile where the corpus-scale time goes. |
| `indicatif` | 0.18 | Progress bar / throughput meter | Long batch over all pubkeys; show pubkeys/sec, ETA, page cursor. |
| `mimalloc` | 0.1 | Global allocator | Replace the system allocator. Allocation-heavy, multi-threaded hashing/tokenization workloads commonly see 5–20% throughput gains. One line in `main.rs`. |
| `governor` | 0.10 | Client-side rate limiting (optional) | LMDB2GraphQL has **no app-layer rate limiting** (per contract §10). If you ever point this at a shared/remote adapter, `governor` caps concurrent request rate to avoid hammering it. Not needed against loopback. |
| `heed` | 0.22 | Direct LMDB reads (v2, deferred) | Same version LMDB2GraphQL itself uses (0.22). The documented escape hatch to skip the GraphQL hop and read strfry's LMDB directly. **Out of scope for v1** — see "Stack Patterns by Variant". |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `cargo` (1.24.1+ toolchain) | Build/test | Per CLAUDE.md / MEMORY, `spam/` Rust needs PATH + `RUSTUP_TOOLCHAIN` overrides; worktree agents fork a stale base. Set toolchain explicitly. |
| `criterion` | Microbenchmarks | Benchmark the analysis hot loops (entropy, n-gram, SimHash, MinHash insert) — speed is the headline requirement; measure, don't guess. |
| `cargo flamegraph` / `samply` | CPU profiling | Find the real hot path in the analysis stage before optimizing. |
| `clippy` + `rustfmt` | Lint/format | Standard. |
| `sqlite3` CLI / DB Browser | Inspect output DB | Query scores, signals, labeled feedback by hand during tuning. |

## Installation

```toml
# Cargo.toml — [dependencies]
tokio                = { version = "1.52", features = ["rt-multi-thread", "macros", "sync", "time"] }
rayon                = "1.12"
reqwest              = { version = "0.13", default-features = false, features = ["rustls-tls", "json", "gzip"] }
flume                = "0.12"
rusqlite             = { version = "0.40", features = ["bundled"] }
serde                = { version = "1.0", features = ["derive"] }
serde_json           = "1.0"
gaoya                = "0.2"
simhash              = "0.3"
ahash                = "0.8"
foldhash             = "0.2"        # pick one of ahash/foldhash after benchmarking
xxhash-rust          = { version = "0.8", features = ["xxh3"] }
blake3               = "1.8"
unicode-segmentation = "1.13"
whatlang             = "0.18"       # optional language-signal layer
linfa                = "0.8"
linfa-logistic       = "0.8"
ndarray              = "0.16"       # linfa's array type for feature matrices
clap                 = { version = "4.6", features = ["derive"] }
figment              = { version = "0.10", features = ["toml", "env"] }
anyhow               = "1.0"
thiserror            = "2.0"
tracing              = "0.1"
tracing-subscriber   = { version = "0.3", features = ["env-filter"] }
indicatif            = "0.18"
mimalloc             = "0.1"
governor             = "0.10"       # optional, remote-adapter only

# v2 only (direct LMDB), gated behind a feature flag — do NOT add in v1:
# heed = "0.22"
```

> Verify `linfa` ↔ `ndarray` version pairing at integration time: linfa 0.8.x is built against ndarray 0.16. Do not independently bump `ndarray` ahead of what `linfa` pins.

## Concurrency model (the load-bearing decision)

Two stages with opposite resource profiles, bridged by one bounded channel:

```
                 (I/O-bound)                bounded flume          (CPU-bound)
  authors() ──▶ tokio: fetch latestPerAuthor ──▶ channel(cap=N) ──▶ rayon: analyze pubkey ──▶ mpsc ──▶ single SQLite writer
  pagination     (≤1000 authors/req, many                          (n-grams, entropy,         (batched WAL txn)
                  requests in flight)                               SimHash, MinHash/LSH,
                                                                    ratios → signals → score)
```

- **Fetch stage — `tokio`.** Network-bound. One multi-thread runtime, a `Semaphore` capping in-flight requests (e.g. 32–128), `reqwest` connection pool reused. Page `authors` for the pubkey universe, chunk into ≤1000-author `latestPerAuthor` calls (contract cap), retry `503`/`INVALID_CURSOR` per contract §7. Push fetched author-event groups into the channel.
- **Bridge — bounded `flume` channel.** The whole point is **back-pressure**: a 100M-event corpus must stream, not buffer. A bounded channel blocks fetchers when analyzers fall behind, capping memory regardless of corpus size.
- **Analysis stage — `rayon`.** CPU-bound and per-pubkey independent → `rayon` work-stealing across all cores. Each task turns one pubkey's events into per-layer signals + an aggregate score.
- **Persist stage — single SQLite writer.** Funnel results to one writer thread doing batched transactions (WAL). SQLite is single-writer; do not write from many threads.

**Rule:** never run CPU-heavy analysis directly on tokio worker threads (it starves the reactor), and never block rayon threads on network I/O. Keep the two pools disjoint, connected only by the channel. This is the standard "async I/O front, rayon compute back" Rust pipeline.

## SQLite schema shape (scores + per-layer signals + labels)

WAL + a normalized signals table so layers can be added without migrations:

```sql
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;   -- safe under WAL for a re-runnable batch
PRAGMA temp_store=MEMORY;

CREATE TABLE run        (run_id INTEGER PRIMARY KEY, started_at INTEGER, max_lev_id INTEGER, config_json TEXT);
CREATE TABLE pubkey     (pubkey TEXT PRIMARY KEY);                 -- 64-char hex
CREATE TABLE score      (run_id INTEGER, pubkey TEXT, score REAL, whitelisted INTEGER,
                         PRIMARY KEY(run_id, pubkey));
CREATE TABLE signal     (run_id INTEGER, pubkey TEXT, layer TEXT, value REAL,   -- one row per layer per pubkey
                         PRIMARY KEY(run_id, pubkey, layer));
CREATE TABLE label      (pubkey TEXT PRIMARY KEY, is_spam INTEGER,              -- human feedback, run-independent
                         labeled_at INTEGER, note TEXT);
CREATE TABLE weight     (layer TEXT PRIMARY KEY, weight REAL, threshold REAL);  -- tuned set, read each run
```

- `signal` is tall/EAV so a new detection layer is just new rows, never a schema change.
- `label` is independent of `run_id` (a verdict outlives a run); the tuner joins `signal` × `label` to fit `linfa-logistic`, writes results back to `weight`.
- Batch inserts in transactions of ~10k rows; reuse one prepared statement. Build the feature matrix for the tuner with a single indexed query.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| `reqwest` + raw query strings | `cynic` / `graphql-client` (typed GraphQL clients) | If the GraphQL surface were large/evolving and you wanted compile-time-checked queries from the introspected schema. Here there are ~3 queries with fixed shapes — the codegen overhead isn't worth it. |
| `rusqlite` (sync) | `sqlx` 0.9 (async) | If the store later becomes a concurrently-written service backend, or you migrate to Postgres. For a single-writer local batch, sqlx's async + compile-time query checking add overhead and a DB-at-build-time requirement for no benefit. |
| `flume` channel | `crossbeam-channel` 0.5 / `tokio::sync::mpsc` | `crossbeam-channel` if the whole pipeline were sync (no tokio); `tokio::mpsc` if it were all async. `flume` is chosen precisely because it bridges the async fetch side and the sync rayon side cleanly. |
| `gaoya` (MinHash+LSH) | `probminhash` 0.1 | `probminhash` for **weighted** Jaccard / ProbMinHash when token frequencies must be weighted. `gaoya` is preferred for v1 because it bundles the LSH banding index (clustering), not just the sketch. |
| `linfa-logistic` | `smartcore` 0.5 | `smartcore` if you later want a broader classical-ML toolbox (trees, SVM, ensembles) under one API. For just logistic regression on a small signal matrix, `linfa-logistic` is leaner and the ecosystem standard. |
| `ahash` | `foldhash` 0.2 / `rustc-hash` 2.1 | `foldhash` for raw speed where DoS resistance is irrelevant (this batch tool); `rustc-hash` (FxHash) for tiny integer keys. Benchmark on the actual token-count loop. |
| `figment` | `config` 0.15 | `config` if you prefer its provider model; `figment` chosen for tighter serde integration and ergonomic layering of file+env+CLI overrides. |
| `mimalloc` | `tikv-jemallocator` 0.7 | `jemalloc` is a fine alternative allocator; `mimalloc` tends to edge it on Windows/macOS and is simpler to wire. Benchmark both if allocation shows up hot. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Any local LLM / on-device model inference (`candle`, `llama.cpp` bindings, `ort`/ONNX runtime, embedding models) | **Explicitly forbidden by the project** — too slow for the corpus-scale speed goal. Even "small" embedding models cost ms/event × 100M events. | Algorithmic/statistical detectors (SimHash/MinHash, entropy, n-grams, ratios) + `linfa-logistic` for weighting. |
| A heavyweight GraphQL **client** (`graphql-client`, `cynic`, Apollo-style) | The contract is one POST with a fixed JSON body and ~3 query shapes. A full client adds codegen, schema sync, and dependency weight for zero benefit. | `reqwest` + a couple of `const` query strings + `serde_json`. |
| `sqlx` for the v1 store | Async + compile-time-checked queries require a live DB at build time and an async runtime around every query; wrong tradeoff for a single-writer local batch where `rusqlite` is faster and simpler. | `rusqlite` with `bundled` + WAL + batched transactions. |
| Running analysis on `tokio` worker threads (or `block_in_place` everywhere) | CPU-bound work starves the async reactor and tanks fetch throughput. | Dedicated `rayon` pool fed by a bounded channel. |
| `blake3` (or any crypto hash) as a `HashMap` hasher | Cryptographic strength is wasted and slower than ahash/foldhash for short keys. | `ahash`/`foldhash` for maps; reserve `blake3` for stable content-dedup keys. |
| `fxhash` 0.2 (the old crate) | Unmaintained; superseded. | `rustc-hash` 2.x (the maintained FxHash) or `foldhash`. |
| Unbounded channels between stages | A fast fetcher will buffer the whole corpus in RAM and OOM. Violates the "stream, not buffer" constraint. | **Bounded** `flume` channel for back-pressure. |
| `reqwest` with default features (OpenSSL) | Pulls a C OpenSSL dependency, complicates static/Alpine builds (this monorepo ships Alpine static binaries elsewhere). | `default-features = false` + `rustls-tls`. |
| Reading strfry LMDB directly in v1 (`heed`) | Couples the engine to strfry's on-disk schema/comparators and bypasses the stable, code-verified contract; the adapter does endianness + dbVersion gating for you. | Stay on LMDB2GraphQL for v1; keep `heed` as a feature-gated v2 fast path. |

## Stack Patterns by Variant

**If v1 (default — GraphQL adapter):**
- `reqwest` → LMDB2GraphQL `authors` + `latestPerAuthor`. No `heed`. Respect contract limits: `authors` ≤1000/page (clamped to 500), `latestPerAuthor` ≤1000 authors, body ≤256 KiB, gate on `/ready`, handle `INVALID_CURSOR`/`TOO_MANY_AUTHORS`/`503`.
- This is the supported, version-stable interface and decouples the engine from strfry internals.

**If v2 (direct LMDB fast path, later milestone):**
- Add `heed = "0.22"` behind a `--source=lmdb` feature flag, mirroring LMDB2GraphQL's own heed 0.22 usage. Open strfry's LMDB **read-only**, replicate the adapter's startup gates (assert `dbVersion == 3`, endianness check, comparator self-check) before trusting reads.
- Justified only if profiling proves the GraphQL/HTTP hop (JSON encode/decode + network) dominates total runtime. Keep the GraphQL path as the reference/fallback.

**If the analysis stage is the bottleneck (likely, given speed priority):**
- Profile with `samply`/flamegraph, micro-bench with `criterion`, swap `ahash`↔`foldhash`↔`xxh3` on the hot token loop, confirm `mimalloc` is active, ensure SimHash/MinHash work is vectorizable. Consider `simsimd` 6.x if you add dense-vector distance math (SIMD-accelerated) — not needed for hashing-based layers.

**If the corpus is genuinely 100M+ events:**
- Tune the bounded-channel capacity and rayon chunk size; never collect all pubkeys/events into a `Vec` first. Stream `authors` pages straight into the fetch→analyze pipeline. Persist incrementally per batch so a crash mid-run loses only the last batch (resume via `run.max_lev_id` / last cursor).

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| `linfa` 0.8 | `linfa-logistic` 0.8, `ndarray` 0.16 | Keep the linfa family on the same minor; don't bump `ndarray` independently. |
| `reqwest` 0.13 | `tokio` 1.x, `rustls` | Use `default-features=false` + `rustls-tls` for static/Alpine builds. |
| `rusqlite` 0.40 | `bundled` SQLite | `bundled` compiles SQLite in — no system libsqlite needed; reproducible across hosts. |
| `flume` 0.12 | `tokio` 1.x + `rayon` 1.x | Async-aware send/recv on the tokio side, blocking recv on the rayon side. |
| `heed` 0.22 (v2 only) | strfry LMDB `dbVersion == 3` | Matches LMDB2GraphQL's own heed 0.22; per MEMORY, LMDB access must run in-container, not native macOS (MDB_BAD_RSLOT). |
| `thiserror` 2.0 / `anyhow` 1.0 | each other | thiserror for typed module errors, anyhow at the binary boundary. |

## Sources

- crates.io REST API (`/api/v1/crates/<name>`) — live version + maintenance check on 2026-06-25 for every crate listed (HIGH confidence; these are the current published max-stable versions, not recalled). Notable: `gaoya` 0.2.2 last updated 2026-06-15 (actively maintained), `simhash` 0.3.0 (2026-03), `rusqlite` 0.40.1, `sqlx` 0.9.0, `reqwest` 0.13.4, `tokio` 1.52.3, `rayon` 1.12.0, `linfa`/`linfa-logistic` 0.8.1, `heed` 0.22.1.
- `contract.md` (LMDB2GraphQL v1.2, code-verified 2026-06-24) — endpoints, query shapes, limits (`authors`/`latestPerAuthor` caps, 256 KiB body, clamp-to-500, `/ready` gating, error codes). HIGH confidence (verified against implementation).
- `.planning/PROJECT.md` — no-LLM constraint, speed priority, SQLite output, label-driven tuning, read-only upstream, heed-as-v2 note. HIGH confidence (project source of truth).
- Repo MEMORY — Rust toolchain PATH/RUSTUP override gotcha for `spam/`; LMDB-in-container requirement (MDB_BAD_RSLOT on native macOS). HIGH confidence (recorded operational facts).

---
*Stack research for: high-throughput Rust Nostr pubkey spam-detection batch engine*
*Researched: 2026-06-25*
