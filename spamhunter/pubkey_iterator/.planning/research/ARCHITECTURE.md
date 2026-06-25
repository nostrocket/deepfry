# Architecture Research

**Domain:** High-throughput Rust batch engine for pubkey-level Nostr spam detection — async fetch (tokio) → bounded channel → CPU analysis (rayon) → SQLite, with a separate corpus-wide aggregation phase and an offline logistic-regression weight-tuning loop.
**Researched:** 2026-06-25
**Confidence:** HIGH (component shape follows directly from the code-verified contract, the verified STACK concurrency model, and the literature-backed layer set in FEATURES; learned weights and spam prevalence are tuned-from-data, not assumed)

## Standard Architecture

This is a two-phase batch pipeline. **Phase A** is a streaming per-pubkey pass (bounded memory). **Phase B** is a corpus-wide aggregation that needs a distinct bounded-memory data structure because it crosses pubkey boundaries. A **tuner** runs entirely offline against the SQLite store.

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│  PHASE A — streaming per-pubkey pass  (memory bounded by channel cap)       │
├──────────────────────────────────────────────────────────────────────────┤
│   I/O stage (tokio)                  bridge            CPU stage (rayon)     │
│  ┌────────────┐   ┌──────────────┐  ┌──────────┐   ┌─────────────────────┐ │
│  │ Enumerator │──▶│   Fetcher    │─▶│ bounded  │──▶│  Per-pubkey Analyzer │ │
│  │ authors()  │   │ latestPer-   │  │  flume   │   │  runs L0,L1,L3,L4,L5 │ │
│  │ cursor page│   │ Author batch │  │ channel  │   │  L2,L8 → sub-scores  │ │
│  └─────┬──────┘   └──────┬───────┘  │ cap = N  │   └──────────┬──────────┘ │
│        │                 │          └──────────┘              │             │
│        │ paginate        │ retry 503 / INVALID_CURSOR         │ emit        │
│        ▼                 ▼                                    ▼             │
│  ┌──────────────────────────────────────┐         ┌────────────────────┐   │
│  │   LMDB2GraphQL  (read-only, :8080)    │         │  Combiner (L7)     │   │
│  │   authors · latestPerAuthor · stats   │         │  sigmoid(Σwᵢxᵢ+b)  │   │
│  └──────────────────────────────────────┘         └─────────┬──────────┘   │
│                                                              │              │
│   ┌──────────────┐                                  ┌────────▼─────────┐   │
│   │ Whitelist    │◀── L0 lookup (cached, batched)──▶│  Persistence     │   │
│   │ HTTP server  │                                  │  single SQLite   │   │
│   │ (Dgraph)     │                                  │  writer (WAL)    │   │
│   └──────────────┘                                  └────────┬─────────┘   │
│                          fingerprints (SimHash + content hash) spill ─┐    │
└───────────────────────────────────────────────────────────────────────┼───┘
                                                                          │
┌──────────────────────────────────────────────────────────────────────▼───┐
│  PHASE B — corpus-wide aggregation  (bounded-memory cross-pubkey cluster)   │
├──────────────────────────────────────────────────────────────────────────┤
│  ┌──────────────────────────────────────────────────────────────────────┐ │
│  │  Cross-pubkey Aggregator (L6)                                          │ │
│  │   Stage 1 exact:  content_hash → bucket → count DISTINCT authors       │ │
│  │   Stage 2 near:   gaoya MinHash+LSH banding over retained fingerprints │ │
│  │   emits per-pubkey L6 sub-score back into `signal`                     │ │
│  └──────────────────────────────────────────────┬───────────────────────┘ │
│                                                  ▼                          │
│                                       re-run Combiner (L7) over full        │
│                                       sub-score set → final score           │
└──────────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────────┐
│  OFFLINE — tuner (separate invocation: `tune` subcommand)                   │
│   signal × label  →  feature matrix  →  linfa-logistic fit  →  weight table │
│   (next run reads `weight` for wᵢ, b, τ)                                    │
└──────────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility (boundary) | Typical Implementation |
|-----------|---------------------------|------------------------|
| **Enumerator** | Owns the pubkey universe. Pages `authors(after, limit≤500)` to a cursor, emits pubkeys downstream. Persists the latest cursor + `stats.maxLevId` for resume. Never touches event content. | tokio task; `reqwest` POST; `endCursor` loop until `hasMore=false` |
| **Fetcher** | Owns I/O for event retrieval. Chunks pubkeys into ≤1000-author `latestPerAuthor` batches, retries `503`/internal errors with backoff, surfaces `INVALID_CURSOR` to the Enumerator. Emits `(pubkey, Vec<Event>)` groups. | tokio + `Semaphore` (32–128 in-flight); `reqwest` connection pool |
| **Bounded channel** | The back-pressure seam. Caps in-flight pubkey work so fetchers cannot outrun analyzers and blow memory over 100M events. | `flume` bounded MPMC, async-send / blocking-recv |
| **Per-pubkey Analyzer** | Owns CPU analysis for ONE pubkey. Runs every per-pubkey layer (L0,L1,L2,L3,L4,L5,L8) over that pubkey's ≤100 events, emits each `xᵢ∈[0,1]`. Side-output: per-event content hash + SimHash fingerprint for Phase B. Pubkey-independent → embarrassingly parallel. | `rayon` `par_iter` / scoped pool |
| **Combiner (L7)** | Owns score fusion. `score = sigmoid(Σ wᵢ·xᵢ + b)`; reads `wᵢ,b,τ` from the `weight` table. Pure reduce, no I/O. | pure function over a sub-score map |
| **Persistence** | Owns the SQLite store. Single writer, batched WAL transactions. Idempotent upserts keyed by `(run_id, pubkey)`. | `rusqlite` bundled, one writer thread, prepared statements |
| **Cross-pubkey Aggregator (L6)** | Owns the DISTINCT corpus-wide phase. Buckets fingerprints across pubkeys (exact hash, then MinHash/LSH), emits per-pubkey cluster sub-score. Bounded memory via hash buckets + LSH, never an O(n²) in-memory join. | `gaoya` LSH index + `ahash` bucket map, fed from spilled fingerprints |
| **Tuner** | Owns the offline learning loop. Joins `signal × label`, fits `linfa-logistic`, writes `weight`. Never runs inline with a scoring run. | `tune` subcommand; `linfa-logistic` + `ndarray` |
| **Whitelist client (L0)** | Owns the one external trust input. Caches the whitelist set; absence → `x=1.0`, presence → `x=0.0`. A weighted signal, never a gate. | cached HTTP client against whitelist-plugin server |

## Recommended Project Structure

```
src/
├── main.rs                 # wires runtime, parses CLI, dispatches subcommand
├── cli.rs                  # clap derive: run | label | tune | export
├── config.rs               # figment: defaults → file → env → CLI; weights/thresholds
├── pipeline/               # orchestration of the two-stage pipeline
│   ├── mod.rs              # spawns tokio fetch side + rayon analyze side + writer
│   ├── enumerator.rs       # authors() pagination + cursor/resume state
│   ├── fetcher.rs          # latestPerAuthor batching, retry, back-pressure send
│   └── channel.rs          # bounded flume wiring, типed PubkeyBatch payload
├── graphql/                # the LMDB2GraphQL HTTP+JSON adapter (NOT a gql client)
│   ├── client.rs           # reqwest POST, /ready gate, 503/413 handling
│   ├── queries.rs          # const query strings (authors, latestPerAuthor, stats)
│   └── types.rs            # serde structs for data/errors envelope, Event
├── layers/                 # one module per detection layer; all impl `Layer`
│   ├── mod.rs              # Layer trait + LayerRegistry + SubScore contract
│   ├── whitelist.rs        # L0
│   ├── near_dup.rs         # L1 (also produces fingerprints for L6)
│   ├── cadence.rs          # L2
│   ├── entropy.rs          # L3
│   ├── links.rs            # L4
│   ├── fingerprint.rs      # L5 tag/kind
│   └── script_anomaly.rs   # L8
├── analyze.rs              # per-pubkey analyzer: run registry → Vec<SubScore>
├── combiner.rs             # L7 logistic fusion (reads weights)
├── aggregate/              # Phase B corpus-wide
│   └── cross_dup.rs        # L6 exact-hash + MinHash/LSH clustering
├── tune/                   # offline learning
│   └── logistic.rs         # signal×label → linfa-logistic → weight table
├── store/                  # SQLite
│   ├── schema.rs           # DDL + PRAGMAs + migrations (run once)
│   ├── writer.rs           # single-writer batched txn API
│   └── queries.rs          # feature-matrix read, idempotent upserts
└── model.rs                # Event, PubkeyBatch, SubScore, Verdict, RunState
```

### Structure Rationale

- **`layers/` is the extensibility frontier.** Every layer is a `Layer` impl registered in `LayerRegistry`. Adding L6/L2/L5/L8 later means a new file + one registration line — no schema migration, because signals are stored EAV (one row per `(run, pubkey, layer)`).
- **`pipeline/` is kept disjoint from `analyze.rs`/`layers/`.** The async fetch side and the sync CPU side never share threads; the only contact is the bounded channel. This enforces the "async I/O front, rayon compute back" rule from STACK.
- **`aggregate/` is physically separate from per-pubkey `analyze.rs`** because L6 has a fundamentally different memory model (cross-pubkey, corpus-wide). Keeping it apart prevents accidentally pulling cross-pubkey state into the streaming hot path.
- **`graphql/` is HTTP+JSON, not a GraphQL client** — per STACK, three fixed query strings beat codegen here.

## Architectural Patterns

### Pattern 1: Bounded-channel back-pressure pipeline (the load-bearing decision)

**What:** tokio fetchers `send_async` onto a bounded `flume` channel; rayon analyzers `recv` from it. When analyzers fall behind, the channel fills and fetchers block — memory is capped at `channel_cap × max_events_per_pubkey`, independent of the 100M-event corpus.
**When to use:** any time a fast producer (network) feeds a slower or bursty consumer (CPU) over an unbounded data set.
**Trade-offs:** + memory bounded, + natural rate-matching; − must tune `cap` (too small starves CPU, too large wastes RAM); − a stalled writer can back-pressure all the way to fetch (desirable here).

**Example:**
```rust
// fetch side (tokio): block when analyzers are behind
let permit = semaphore.acquire().await?;
let groups = client.latest_per_author(kind, per_author, &chunk).await?; // ≤1000 authors
for g in regroup_by_author(groups) {
    tx.send_async(PubkeyBatch { pubkey: g.author, events: g.events }).await?; // back-pressure here
}

// analyze side (rayon): saturate cores, never touch the network
rx.iter().par_bridge().for_each(|batch| {
    let subs = registry.run_all(&batch);          // Vec<SubScore>, xᵢ∈[0,1]
    let verdict = combiner.fuse(&subs, &weights);  // sigmoid(Σwᵢxᵢ+b)
    writer_tx.send(Persist { batch_key: batch.pubkey, subs, verdict }).ok();
});
```

### Pattern 2: Two-phase streaming + bounded aggregation (per-pubkey vs corpus-wide)

**What:** Phase A streams per-pubkey and writes sub-scores + cheap fingerprints (a 64-bit SimHash and a content hash per event) to SQLite/spill. Phase B reads back only the fingerprints (tiny vs events) and clusters across pubkeys with exact-hash buckets then MinHash/LSH — never re-fetching or re-buffering events.
**When to use:** when one signal (L6 coordination) is inherently cross-entity but the corpus cannot be held in memory.
**Trade-offs:** + L6 memory is O(distinct fingerprints), not O(events); + L6 can re-run without re-fetching; − two passes over derived data; − final score for L6-affected pubkeys is written in Phase B, so the combiner runs twice (once provisional in A, once final in B).

**Example:**
```rust
// Phase B: bounded-memory cross-pubkey clustering
let mut exact: HashMap<u64, HashSet<Pubkey>, ahash::RandomState> = default(); // content_hash → authors
for fp in store.stream_fingerprints(run_id) {            // reads `fingerprint` table, not events
    exact.entry(fp.content_hash).or_default().insert(fp.pubkey);
}
// any bucket with many DISTINCT authors = coordinated blast (cleanest signal, per spike)
let mut lsh = gaoya::lsh::LshIndex::new(bands, rows);     // near-dup, O(n) candidates
for fp in store.stream_fingerprints(run_id) { lsh.insert(fp.pubkey, fp.minhash); }
```

### Pattern 3: Layer trait + EAV signal contract (extensible integration seam)

**What:** every layer implements one trait and emits a normalized sub-score. The combiner consumes a `&[SubScore]` map by layer name. New layers append rows to the tall `signal` table — no DDL change, and the tuner automatically picks them up via the join.
**When to use:** when the set of features will grow and must stay tunable independently.
**Trade-offs:** + zero-migration extensibility, + uniform combiner/tuner; − dynamic dispatch (negligible vs analysis cost); − layer names become a stable contract (renaming a layer orphans its history).

**Example:**
```rust
pub struct SubScore { pub layer: &'static str, pub value: f64 } // value ∈ [0,1]

pub trait Layer: Send + Sync {
    fn name(&self) -> &'static str;
    fn score(&self, events: &[Event], ctx: &LayerCtx) -> SubScore; // ctx carries whitelist cache etc.
}
// registry.run_all → Vec<SubScore>; combiner: sigmoid(Σ w[layer]·value + b)
```

## Data Flow

### Request (run) Flow

```
authors(after) ─cursor─▶ pubkey pages
      ↓
chunk ≤1000 ─▶ latestPerAuthor(kind, perAuthor) ─▶ AuthorGroup[]
      ↓ (flume bounded)
per-pubkey events ─▶ Analyzer runs L0..L8(per-pubkey) ─▶ SubScore[]
      ↓                                                      ↓ fingerprints
   Combiner L7 (provisional) ─▶ Verdict          ┌──────────┘
      ↓                                           ▼
   SQLite: run, score, signal, fingerprint   Phase B: cross-dup cluster (L6)
                                                   ↓
                                            update signal(L6) ─▶ re-fuse L7 ─▶ final score
```

### The fetch-strategy decision (tied to contract limits)

**Recommendation: default to `latestPerAuthor(kind:1, perAuthor:100)` batched at 1000 authors, with a small fixed set of secondary kind passes (0, 3, 7), NOT per-author `events`.**

Rationale, anchored to `contract.md`:

| Strategy | Cost | Kinds | Calls for N pubkeys | Body-limit / clamp interaction | Verdict |
|----------|------|-------|---------------------|--------------------------------|---------|
| `latestPerAuthor` | ≈ `authors × perAuthor` index scans, batched ≤1000 authors/call | one kind per call | `⌈N/1000⌉` per kind | `perAuthor` clamps to ≤500 (we want ~100, fine); request body = 1000 × 64-hex ≈ 64 KB, well under 256 KiB | **chosen** |
| per-author `events` filter | one author/call, all kinds, `limit≤500` | all kinds | `N` calls (100M-pubkey-order → far too many round-trips) | tiny body, but N× the HTTP overhead | rejected for the bulk pass |

The corpus is dominated by kind-1 (text notes) — the content layers (L1/L3/L4) operate almost entirely on kind-1. So the **primary pass** is `latestPerAuthor(kind:1, perAuthor:100)`, which gets the ~100 recent events that matter for content scoring in `⌈N/1000⌉` calls. Kind/tag fingerprinting (L5) wants kind diversity; satisfy it with a few **additional batched `latestPerAuthor` passes** over a small whitelist of kinds (0 profile, 3 contacts, 7 reactions) at low `perAuthor` (e.g. 3–5), merged per author. This stays O(authors) in call count and keeps each body small.

Reserve per-author `events` (`filter:{authors:[pk]}`) only for a **targeted re-fetch** of specific flagged pubkeys during human review/labeling, where cross-kind completeness matters for one account at a time. Do not use it for the corpus pass.

> Why not one giant `latestPerAuthor(kind:1, perAuthor:500)`? `perAuthor:500 × 1000 authors` is the heaviest legal request (contract §6.2 "1000×500 is still heavy"). We only need ~100 recent events; `perAuthor:100` is 5× cheaper per call with no accuracy loss for pubkey-level scoring.

### Idempotency, resumability, and contract-error handling

| Concern | Mechanism |
|---------|-----------|
| **Idempotent re-runs** | Every run gets a `run_id`. `score`/`signal` are keyed `(run_id, pubkey[, layer])`; persistence uses `INSERT … ON CONFLICT … DO UPDATE` (upsert), so re-processing a pubkey in the same run overwrites rather than duplicates. A full re-run is a new `run_id`; nothing mutates prior runs. `label` and `weight` are run-independent and survive re-runs. |
| **Resumability across pagination** | Persist the Enumerator's last `authors` `endCursor` + `stats.maxLevId` into the `run` row each batch. On restart with `--resume`, reopen the same `run_id` and continue from the stored cursor; already-written `(run_id, pubkey)` rows are skipped or harmlessly re-upserted. |
| **`503` (not ready)** | Gate the first query on `GET /ready`; treat any `503` from `/graphql` as transient → retry with exponential backoff + jitter. Do not advance the cursor on a 503. |
| **`INVALID_CURSOR`** | Per contract, the cursor is corrupt/cross-used → **drop it and restart pagination from page 1** for that query. With upserts keyed by `(run_id, pubkey)`, re-walking already-seen pubkeys is safe (idempotent). Never hand-build or reuse a cursor across `authors`/`events`. |
| **`413` body too large** | Should not occur with 1000-author batches (~64 KB), but if a future change inflates the body, split the author chunk and retry. |
| **`TOO_MANY_AUTHORS`** | Enforce the ≤1000 chunk size before sending; this is a guardrail, not an expected runtime error. |
| **Snapshot drift mid-pagination** | The corpus can change between calls (contract §8). Cursors anchor to sort keys, not offsets, so pagination tolerates inserts/deletes — a new author may or may not appear depending on sort position. Record `maxLevId` at start and end of a run as a drift indicator; treat the run as a best-effort snapshot of "the corpus during the run window," not an instantaneous one. Do not abort on drift; it is expected for a long batch. |

## SQLite Schema (concrete, idempotent, tuning-ready)

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;     -- safe under WAL for a re-runnable batch
PRAGMA temp_store   = MEMORY;
PRAGMA foreign_keys = ON;

-- One row per batch invocation. Holds resume state + drift probes + config snapshot.
CREATE TABLE IF NOT EXISTS run (
  run_id        INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at    INTEGER NOT NULL,
  finished_at   INTEGER,                 -- NULL while in progress
  max_lev_id_start INTEGER,              -- stats.maxLevId at start (drift probe)
  max_lev_id_end   INTEGER,              -- stats.maxLevId at end
  last_cursor   TEXT,                    -- opaque authors endCursor for --resume
  config_json   TEXT NOT NULL,           -- weights/thresholds snapshot for reproducibility
  status        TEXT NOT NULL DEFAULT 'running'  -- running | done | aborted
);

-- Pubkey dimension (run-independent identity).
CREATE TABLE IF NOT EXISTS pubkey (
  pubkey TEXT PRIMARY KEY               -- 64-char lowercase hex
);

-- Final per-pubkey verdict for a run. Idempotent upsert target.
CREATE TABLE IF NOT EXISTS score (
  run_id      INTEGER NOT NULL REFERENCES run(run_id),
  pubkey      TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score       REAL    NOT NULL,          -- sigmoid(Σwᵢxᵢ+b) ∈ [0,1]
  whitelisted INTEGER NOT NULL,          -- 0/1, denormalized L0 for fast filtering
  suspected   INTEGER NOT NULL,          -- score > τ at run time
  PRIMARY KEY (run_id, pubkey)
);

-- EAV per-layer sub-scores: tall so NEW LAYERS NEED NO MIGRATION.
CREATE TABLE IF NOT EXISTS signal (
  run_id INTEGER NOT NULL REFERENCES run(run_id),
  pubkey TEXT    NOT NULL REFERENCES pubkey(pubkey),
  layer  TEXT    NOT NULL,               -- e.g. 'L1_near_dup'  (stable contract)
  value  REAL    NOT NULL,               -- xᵢ ∈ [0,1]
  PRIMARY KEY (run_id, pubkey, layer)
);
CREATE INDEX IF NOT EXISTS idx_signal_layer ON signal(layer);

-- Derived fingerprints for Phase B (cross-pubkey L6). Cheap vs events.
CREATE TABLE IF NOT EXISTS fingerprint (
  run_id       INTEGER NOT NULL REFERENCES run(run_id),
  pubkey       TEXT    NOT NULL REFERENCES pubkey(pubkey),
  content_hash INTEGER NOT NULL,         -- xxh3 of normalized content (exact-dup bucket key)
  simhash      INTEGER NOT NULL,         -- 64-bit SimHash (near-dup)
  minhash      BLOB,                     -- packed MinHash signature for gaoya LSH (optional)
  PRIMARY KEY (run_id, pubkey, content_hash)
);
CREATE INDEX IF NOT EXISTS idx_fp_chash ON fingerprint(run_id, content_hash);

-- Human-labeled feedback. RUN-INDEPENDENT — a verdict outlives any run.
CREATE TABLE IF NOT EXISTS label (
  pubkey     TEXT PRIMARY KEY REFERENCES pubkey(pubkey),
  is_spam    INTEGER NOT NULL,           -- 1 spam, 0 ham (false positive)
  labeled_at INTEGER NOT NULL,
  note       TEXT
);

-- Learned/active weights consumed by every run. One row per layer + a bias row.
CREATE TABLE IF NOT EXISTS weight (
  layer      TEXT PRIMARY KEY,           -- layer name, or '_bias' for b, '_threshold' for τ
  weight     REAL NOT NULL,
  threshold  REAL,                       -- per-layer internal threshold (NULL for bias/τ rows)
  tuned_at   INTEGER,                    -- NULL = hand-set default; set when fitted
  tuned_from_run INTEGER                 -- provenance: which label set produced it
);
```

**Why this supports the tuning loop:** the tuner runs a single indexed query —
`SELECT s.pubkey, s.layer, s.value, l.is_spam FROM signal s JOIN label l USING (pubkey) WHERE s.run_id = ?` —
pivots it into an `ndarray` feature matrix (rows = labeled pubkeys, columns = layers in a fixed order), fits `linfa-logistic`, and writes the coefficients back to `weight` (one row per layer + `_bias` + `_threshold`). The next run reads `weight` to populate `wᵢ, b, τ`. Because `signal` is EAV, adding a layer just adds a column to the pivot automatically — no schema change, no tuner code change beyond the column ordering being read from the layer registry.

## The Tuning / "Backpropagation" Loop

```
              (offline `tune` subcommand — never inline with a run)
  label (human FPs/spam)  ┐
                          ├─ JOIN on pubkey ─▶ feature matrix X (pubkey × layer)
  signal (xᵢ from a run)  ┘                    target y (is_spam)
                                                   │
                          linfa-logistic.fit(X, y) with class-weighting for spam rarity
                                                   │  precision-oriented τ (low FP)
                                                   ▼
                          weight table (wᵢ, b, τ)  ──read by──▶  next run's Combiner (L7)
```

- **Offline/batch only.** Tuning is a separate invocation against the existing SQLite file; a scoring run never trains. This keeps the hot path free of model-fit overhead and makes weight changes auditable (`weight.tuned_from_run` records provenance).
- **Start hand-set, then re-fit.** First runs use conservative hand weights (per FEATURES: modest on L0/L2/L8). Once a labeled FP set accumulates, `tune` replaces them. Standardize sub-scores before fitting; use class weights because spam is rare; choose τ for precision (low false positives is the project's headline correctness goal).
- **A run consumes the latest weights** by reading the `weight` table at startup and snapshotting it into `run.config_json` for reproducibility — you can always tell which weights produced a given score.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| Small corpus (≤1M events) | Single machine, default channel cap, in-memory L6 buckets are fine. The whole thing runs in minutes. |
| Medium (1M–10M events) | Tune `flume` cap and rayon chunk size; confirm `mimalloc` active; L6 exact buckets still fit RAM; persist incrementally per batch. |
| Target (100M+ events) | **Never collect pubkeys/events into a `Vec` first** — stream `authors` pages straight into fetch→analyze. L6 must read fingerprints back from SQLite (or an on-disk spill), not hold events. Bound channel capacity hard. Persist per-batch so a crash loses only the last batch (resume via `run.last_cursor`/`max_lev_id`). |

### Scaling Priorities

1. **First bottleneck — analysis CPU (most likely).** Profile with `samply`/flamegraph, micro-bench hot loops with `criterion`, swap `ahash`↔`foldhash`↔`xxh3` on the token-count loop, ensure SimHash/MinHash are vectorizable. rayon already saturates cores.
2. **Second bottleneck — the GraphQL/HTTP hop.** If JSON encode/decode + network dominates (prove it with `tracing` span timings), the documented escape hatch is the v2 `heed` direct-LMDB read (feature-gated, in-container per MEMORY's `MDB_BAD_RSLOT` note). Keep GraphQL as the reference path.
3. **Third — SQLite write throughput.** Batched WAL transactions (~10k rows) on a single writer hit 100k+ inserts/sec; only revisit if `tracing` shows the writer back-pressuring the pipeline.

## Anti-Patterns

### Anti-Pattern 1: Running CPU analysis on tokio worker threads

**What people do:** call SimHash/entropy/MinHash directly inside the async fetch tasks (or `block_in_place` everywhere).
**Why it's wrong:** CPU-bound work starves the async reactor and tanks fetch throughput — the two resource profiles fight.
**Do this instead:** keep the tokio and rayon pools disjoint, connected only by the bounded channel (Pattern 1).

### Anti-Pattern 2: Unbounded channel between fetch and analyze

**What people do:** use an unbounded queue "so fetchers never block."
**Why it's wrong:** a fast fetcher buffers the whole 100M-event corpus in RAM and OOMs — violates the streaming constraint.
**Do this instead:** bounded `flume` channel; let back-pressure rate-match producer to consumer.

### Anti-Pattern 3: Doing L6 (cross-pubkey) as an in-memory all-pairs join

**What people do:** load all events/fingerprints and compare every pair, or hold all events to "make cross-pubkey stats easy."
**Why it's wrong:** O(n²) comparison and O(events) memory — both fatal at corpus scale.
**Do this instead:** Phase B with exact-hash buckets (count DISTINCT authors per bucket) then MinHash+LSH banding for O(n) candidate pairs over *fingerprints* read back from SQLite — bounded memory.

### Anti-Pattern 4: Treating the whitelist as a hard gate

**What people do:** skip analysis for whitelisted pubkeys.
**Why it's wrong:** whitelist = "seen by the crawler," not "is good"; compromised/legit-then-spammy accounts create blind spots.
**Do this instead:** L0 is a weighted signal; presence clears only that term, the pubkey still flows through every later layer.

### Anti-Pattern 5: Per-author `events` for the bulk pass

**What people do:** loop one `events(filter:{authors:[pk]})` call per pubkey to get all kinds.
**Why it's wrong:** N HTTP round-trips over a 100M-pubkey-order corpus; the round-trip overhead dwarfs the work.
**Do this instead:** batched `latestPerAuthor` (≤1000 authors/call) for the bulk pass; reserve per-author `events` for targeted re-fetch during labeling.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| **LMDB2GraphQL** (`:8080`, read-only) | `reqwest` POST `/graphql` with const query strings; gate on `GET /ready` | Limits: `authors`/`latestPerAuthor`/`events` clamp to ≤500; `latestPerAuthor` ≤1000 authors; body ≤256 KiB. Errors: `503`, `INVALID_CURSOR`, `TOO_MANY_AUTHORS`, `413`. Loopback default; Docker reaches by container name on `deepfry-net`. |
| **Whitelist-plugin HTTP server** (Dgraph-backed) | Cached lookup; L0 reads presence | Absence = spam signal, not exemption. 6h refresh on the server; cache the set per run. |
| **strfry LMDB** (v2 only, deferred) | `heed 0.22` direct read behind `--source=lmdb` | Must replicate the adapter's startup gates (dbVersion==3, endianness, comparator). In-container only (MEMORY: `MDB_BAD_RSLOT` on native macOS). |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| Enumerator → Fetcher | in-process channel of pubkey pages | Enumerator owns cursor/resume; Fetcher owns retry |
| Fetcher → Analyzer | **bounded `flume`** (`PubkeyBatch`) | the back-pressure seam; async-send / blocking-recv |
| Analyzer → Persistence | `mpsc` to single writer (`Persist`) | one SQLite writer, batched WAL txns |
| Analyzer → Aggregator (L6) | via `fingerprint` table in SQLite | decouples phases; L6 re-runnable without re-fetch |
| Tuner ↔ Store | `signal × label` read, `weight` write | offline; provenance via `tuned_from_run` |
| Layers ↔ Combiner | `Layer` trait → `SubScore` map | EAV signal contract; new layers need no migration |

## Suggested Build Order (with dependencies)

Each step is independently testable and unblocks the next. This is the dependency-honest order, not just the layer priority order from FEATURES.

1. **Model + SQLite schema + store writer.** Define `Event`, `PubkeyBatch`, `SubScore`, `RunState`; create the schema (`run`, `pubkey`, `score`, `signal`, `fingerprint`, `label`, `weight`); implement the single-writer batched upsert. *Dependency root — everything persists here. Idempotency lives here.*
2. **GraphQL client + Enumerator.** `reqwest` POST, `/ready` gate, const queries, `503`/`INVALID_CURSOR` handling; page `authors` to a cursor and persist resume state. *Proves connectivity, pagination, resumability against the real contract.*
3. **Fetcher + bounded pipeline.** `latestPerAuthor(kind:1, perAuthor:100)` batched ≤1000, retry, push onto `flume`; rayon consumer skeleton. *Establishes the back-pressure seam — validate memory stays bounded with a synthetic large `authors` set BEFORE adding any layer.*
4. **Layer trait + Combiner (L7) + first layers (L0, L1, L3, L4).** Define the `SubScore` contract and `LayerRegistry`; implement the P1 layers; hand-set weights; `sigmoid` fusion → write `score`/`signal`. *This is the first end-to-end runnable verdict. L7 must exist as the integration seam before more layers.*
5. **CLI: `run` + `export`.** Wire `clap` subcommands; produce the suspected-spammer list. *First shippable deliverable.*
6. **`label` subcommand + Tuner (`tune`).** Capture human labels; join `signal × label`; fit `linfa-logistic`; write `weight`. *Closes the correctability loop — requires steps 1+4 (signals + labels exist).*
7. **Phase B Aggregator (L6) + fingerprint side-output.** Emit fingerprints in step-4 analysis; add the corpus-wide exact-hash + MinHash/LSH pass; re-fuse L7. *Highest value, highest cost; depends on a solid streaming pipeline (step 3) and L1 fingerprints (step 4).*
8. **Remaining layers L2, L5, L8.** Add as registry entries once labels exist to weight them safely (per FEATURES' false-positive lesson). *No schema change — pure additions thanks to EAV `signal`.*

> Build-order rule of thumb: **persistence → connectivity → back-pressure → combiner+P1 layers → tuning loop → corpus-wide L6 → remaining layers.** The combiner (L7) is deliberately early because it is the integration seam every layer plugs into; L6 is deliberately late because it needs the streaming pipeline and fingerprints proven first.

## Sources

- `contract.md` (LMDB2GraphQL v1.2, code-verified 2026-06-24) — endpoints, query shapes, `authors`/`latestPerAuthor` semantics and limits (≤1000 authors, clamp-to-500, 256 KiB body), error codes (`503`/`INVALID_CURSOR`/`TOO_MANY_AUTHORS`/`413`), snapshot/drift semantics, `/ready` gating. HIGH confidence (verified against implementation).
- `.planning/research/STACK.md` (2026-06-25) — verified crate stack, the tokio→bounded-channel→rayon concurrency model, single-writer SQLite WAL pattern, EAV signal table, `linfa-logistic` tuner, `heed` v2 fast path. HIGH confidence (versions verified live against crates.io).
- `.planning/research/FEATURES.md` (2026-06-25) — layer set L0–L8, the `xᵢ∈[0,1]` sub-score contract, L6 corpus-wide aggregation requirement, L7 logistic combiner, false-positive / "weighted-not-gate" design lessons. HIGH confidence (literature-backed algorithms; weights tuned from data).
- `.planning/PROJECT.md` — no-LLM constraint, streaming/bounded-memory requirement, read-only upstream, idempotent re-runnable batch, whitelist-as-signal, label-driven re-tuning. HIGH confidence (project source of truth).
- Repo MEMORY — `MDB_BAD_RSLOT` in-container LMDB requirement (relevant to the deferred `heed` v2 path); GSD subagents anchor to git root (handled by using absolute paths here). HIGH confidence (recorded operational facts).

---
*Architecture research for: high-throughput Rust Nostr pubkey spam-detection batch engine*
*Researched: 2026-06-25*
