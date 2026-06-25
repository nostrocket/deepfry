# Project Research Summary

**Project:** Spamhunter — Pubkey Spam Classifier
**Domain:** High-throughput Rust batch engine for pubkey-level Nostr spam detection (content/behavioral layers, GraphQL-fed, SQLite output, label-driven logistic-regression weight re-tuning; no LLM)
**Researched:** 2026-06-25
**Confidence:** HIGH

## Executive Summary

Spamhunter is a re-runnable Rust batch engine that scores each Nostr pubkey for spam from its ~100 recent events, pulled read-only through the LMDB2GraphQL adapter. The expert-consensus way to build this is a **two-pool streaming pipeline**: an async I/O front (`tokio` + `reqwest`) paginates the pubkey universe and batch-fetches events, a bounded back-pressure channel (`flume`) bridges to a CPU-bound analysis back (`rayon`) that runs a stack of independently-weighted detection *layers*, and a single-writer `rusqlite`/WAL store persists per-layer sub-scores. Each layer emits a normalized sub-score `xi in [0,1]`; a logistic combiner fuses them (`score = sigmoid(Sum wi*xi + b)`), and an offline `linfa-logistic` tuner re-fits the weights from human-labeled false positives. There is no LLM anywhere — that is an explicit, speed-driven constraint, and the entire stack is algorithmic/statistical (SimHash/MinHash, Shannon entropy, n-grams, ratios) plus a single small linear model.

The recommended approach is **opinionated and dependency-honest**: build persistence -> connectivity -> back-pressure -> combiner+P1 layers -> tuning loop -> corpus-wide cross-pubkey clustering -> remaining layers. The combiner (L7) is deliberately built *early* because it is the integration seam every layer plugs into; cross-pubkey clustering (L6) is built *late* because it needs a proven streaming pipeline and a fundamentally different (corpus-wide, bounded-memory) data model. Use HTTP+JSON with hand-written query strings, not a GraphQL client; use sync `rusqlite` not async `sqlx`; never run CPU work on tokio threads or use unbounded channels. Confidence in the stack, architecture, and feature algorithms is HIGH — versions were verified live against crates.io, and the architecture follows directly from a code-verified `contract.md` and DeepFry's own spam-clusters spike.

The dominant risk is **false positives**, not throughput. DeepFry's own production spike proved that the most "obvious" spam signals (timestamp bursts) were overwhelmingly crawler/infrastructure artifacts, not bots. Every single layer has a real FP mode: timing fires on crawler batches, whitelist-absence penalizes new legit users, entropy misfires on CJK/emoji, near-dup flags "gm" and reposts, and volume+links+cadence describes legitimate news bots. Mitigation is structural: every layer is a *weighted* term (never a hard gate), high-confidence flags require a coordination co-signal, the output is a *reviewable list* (never auto-enforcement), and the tuning loop must inject randomly-sampled unflagged pubkeys to avoid a selection-bias feedback trap. Speed risks (round-trip overhead, allocator/hashing hot loops, SQLite contention) are well-understood and have prescriptive fixes.

## Key Findings

### Recommended Stack

The stack is a prescriptive pure-Rust set, all versions verified live against crates.io on 2026-06-25 (see STACK.md). The load-bearing decision is the concurrency model: **`tokio` for the network-bound fetch stage, `rayon` for the CPU-bound analysis stage, joined by a bounded `flume` channel** for back-pressure. HTTP is plain `reqwest` (rustls, no OpenSSL) + `serde_json` against ~3 fixed GraphQL query strings — a GraphQL client buys nothing. Output is `rusqlite` (bundled SQLite, WAL, batched single-writer transactions). Weight re-tuning is `linfa-logistic` — pure Rust, no Python, no ONNX, no LLM.

**Core technologies:**
- `tokio` 1.52 + `reqwest` 0.13 (rustls/json/gzip, default-features off): network-bound fetch stage — the de-facto async runtime + mature HTTP client for POSTing GraphQL-over-HTTP.
- `rayon` 1.12: data-parallel per-pubkey analysis — embarrassingly parallel, work-stealing, kept strictly off tokio threads.
- `flume` 0.12: bounded MPMC channel bridging async-fetch and sync-analyze — the back-pressure seam that keeps memory bounded over a 100M-event corpus.
- `rusqlite` 0.40 (bundled, WAL): single-writer output store — SQLite's sweet spot; 100k+ inserts/sec with batched transactions.
- `gaoya` 0.2.2 (MinHash+LSH) + `simhash` 0.3: near-duplicate detection core — within-pubkey and corpus-wide clustering without O(n^2).
- `linfa-logistic` 0.8 (+ `ndarray` 0.16): logistic-regression weight tuner — the "backprop from labels" loop; keep the linfa family on the same minor.
- `clap` 4.6, `figment` 0.10, `tracing`, `indicatif`, `mimalloc`: CLI, layered config/thresholds, structured spans for profiling, progress, and the global allocator.

**Explicitly avoid:** any local LLM/embedding inference (forbidden), a heavyweight GraphQL client, `sqlx` for v1, running analysis on tokio threads, unbounded channels, and reading strfry LMDB directly in v1 (`heed` is a feature-gated v2 fast path only, justified only if profiling proves the HTTP hop dominates).

### Expected Features

In this project a "feature" is a **detection layer**: it reads event fields, emits a normalized sub-score, and is independently tunable (weight + internal threshold). The deliverable is always a *per-pubkey* verdict, never per-event. Algorithms are literature-backed (SimHash WWW'07, IAT entropy USENIX'08, UTS#39 confusables) — HIGH confidence on the algorithms; spam prevalence and final weights are MEDIUM and must be tuned from data.

**Must have (table stakes — v1):**
- **L0 Whitelist signal** — absence from the WoT whitelist as a *weighted prior* (never a gate); cheapest, required to anchor the "weighted-not-gate" design.
- **L1 Within-pubkey near-duplicate ratio** — SimHash/Hamming clustering of an account's own posts; the highest-precision single content signal (copy-paste blasting).
- **L3 Content entropy / templated-text** — Shannon entropy + dict-ratio + compression proxy; cheap, broad coverage.
- **L4 Link & mention spam ratios** — URL/domain/p-tag/hashtag ratios; cheap, catches promo/affiliate spam.
- **L7 Logistic combiner** — fuses sub-scores into one verdict and consumes labels; hand-set weights to start.

**Should have (competitive differentiators — v1.x):**
- **L2 Posting cadence / burst** — IAT entropy/CV/burst; add *after* content layers anchor precision, always co-signal-gated.
- **L5 Tag/kind fingerprinting** — templated tag-shape / kind-distribution signatures.
- **L7 full re-fit from labels** — turn on weight re-optimization once a labeled FP set exists.

**Defer (v2+):**
- **L6 Cross-pubkey duplicate clustering** — highest value (coordinated botnets) but needs the corpus-wide aggregation phase and bounded-memory LSH; defer until the streaming pipeline is solid.
- **L8 Language/script anomaly & homoglyph abuse** — weak, FP-prone signal; lowest marginal value first.

**Anti-features (rejected):** any LLM/on-device model, per-event verdicts as the deliverable, whitelist-as-hard-exemption, hard per-layer auto-ban cutoffs, re-implementing structural follow-graph detection (that is `spam-explorer`'s job), buffering the whole corpus, and real-time/incremental scoring in v1.

### Architecture Approach

A **two-phase batch pipeline plus an offline tuner**. Phase A is a streaming per-pubkey pass (memory bounded by the channel cap): Enumerator pages `authors` -> Fetcher batches `latestPerAuthor(kind:1, perAuthor:100)` at <=1000 authors -> bounded `flume` channel -> rayon Analyzer runs the per-pubkey layers -> Combiner fuses -> single SQLite writer. Phase B is a *separate* corpus-wide aggregation (L6) that reads back only cheap fingerprints (SimHash + content hash) from SQLite — never re-fetching events — and clusters across pubkeys via exact-hash buckets then MinHash/LSH, re-fusing the combiner. The Tuner runs entirely offline (`tune` subcommand): joins `signal x label`, fits `linfa-logistic`, writes the `weight` table the next run reads. Signals are stored EAV (one row per run/pubkey/layer) so adding a layer needs no migration.

**Major components:**
1. **Enumerator + Fetcher (tokio)** — own the pubkey universe and event I/O; cursor/resume state, retry `503`/`INVALID_CURSOR`, match results by `author` field not index.
2. **Bounded channel + Per-pubkey Analyzer (flume -> rayon)** — the back-pressure seam and the embarrassingly-parallel layer execution; each layer implements a `Layer` trait emitting one `SubScore`.
3. **Combiner (L7) + single-writer Persistence (rusqlite/WAL)** — pure logistic fusion reading the `weight` table; idempotent UPSERTs keyed `(run_id, pubkey)`.
4. **Cross-pubkey Aggregator (L6, Phase B)** — distinct corpus-wide, bounded-memory clustering over spilled fingerprints.
5. **Tuner (offline)** — `signal x label` -> `linfa-logistic` -> `weight`, with provenance tracking.

### Critical Pitfalls

1. **Timing/cadence FPs dominated by infrastructure artifacts** — DeepFry's spike proved top timestamp bursts were crawler/refresh writes (0% low-follower), not bots. Never let a timing layer stand alone; gate it behind a content/low-trust co-signal; weight it low and make tuning earn it up.
2. **Whitelist "absence = spam" false-positives new legit users** — the whitelist is crawler *coverage*, not curated trust. Keep L0 low-weight, never an exemption; counterweight absence with corpus-age (long varied history => coverage gap, not spam).
3. **LMDB2GraphQL contract violations (silent failures)** — page limits clamp silently to 500, GraphQL errors arrive in a `200` body's `errors[]`, and `latestPerAuthor` *omits* authors with zero matching events. Always parse `errors[]`/`extensions.code`, assert returned counts, match results by `author` field (never by index), and chunk <=1000 authors + <=256 KiB body.
4. **Selection-bias feedback loop in tuning** — training only on flagged-then-reviewed pubkeys entrenches blind spots (the model never sees its false negatives). Always inject a random/stratified sample of *unflagged* pubkeys into the review queue; track recall, not just FP rate.
5. **Buffering the corpus instead of streaming** — copying the contract's `push`-into-`all` recipe OOMs at 100M events. Stream page -> score -> write -> drop; bound the channel hard; never hold the full author/event set.
6. **Determinism / idempotency violations** — HashMap iteration randomness, rayon float-sum order, unseeded RNG, and non-atomic checkpointing break the "re-runnable idempotent batch" requirement. Seed all RNG, sum in fixed layer order, UPSERT on `(run_id, pubkey)`, checkpoint cursor transactionally.
7. **Ethics / blast-radius** — auto-wiring the suspected-spammer list into enforcement de-platforms real users. Hold the spec's boundary: v1 produces a *reviewable list* with per-layer evidence, never an automatic action; default to high-precision thresholds.

## Implications for Roadmap

Based on research, the suggested phase structure follows ARCHITECTURE.md's dependency-honest build order (**persistence -> connectivity -> back-pressure -> combiner+P1 layers -> tuning loop -> corpus-wide L6 -> remaining layers**), grouped into shippable increments.

### Phase 1: Persistence Foundation (model + SQLite store)
**Rationale:** Dependency root — everything persists here and idempotency lives here. Cannot test connectivity or scoring without it.
**Delivers:** `Event`/`PubkeyBatch`/`SubScore`/`RunState` types; full schema (`run`, `pubkey`, `score`, `signal` EAV, `fingerprint`, `label`, `weight`); single-writer batched-WAL UPSERT API.
**Uses (STACK):** `rusqlite` (bundled, WAL, `synchronous=NORMAL`), `serde`.
**Avoids:** Pitfall 12 (determinism/UPSERT on `(run_id, pubkey)`), Pitfall 15 (single-writer batched transactions).

### Phase 2: GraphQL Client + Enumerator
**Rationale:** Proves connectivity, pagination, and resumability against the real contract before any analysis exists.
**Delivers:** `reqwest` HTTP+JSON client with `/ready` gating, const query strings, full `errors[]`/`extensions.code` handling; `authors` pagination to a persisted cursor with resume state.
**Uses (STACK):** `reqwest` (rustls), `serde_json`, `figment` config, `tracing`.
**Avoids:** Pitfall 6 (silent clamp, body/author limits), Pitfall 8 (snapshot drift via `maxLevId` start/end), Pitfall 20 (`503`/readiness retry-with-backoff).

### Phase 3: Fetcher + Bounded Pipeline
**Rationale:** Establishes the back-pressure seam; validate memory stays bounded with a synthetic large author set *before* adding any layer (a streaming-design mistake here is a structural rewrite).
**Delivers:** `latestPerAuthor(kind:1, perAuthor:100)` batched <=1000 with retry; bounded `flume` channel; rayon consumer skeleton; single-writer funnel.
**Uses (STACK):** `tokio` + `Semaphore`, `flume`, `rayon`, `mimalloc`.
**Avoids:** Pitfall 7 (match by `author` field, empty-group omission), Pitfall 13 (streaming not buffering), Pitfall 14 (batched/pipelined, minimal fields — skip `raw`), Pitfall 17 (disjoint tokio/rayon pools).

### Phase 4: Layer Trait + Combiner (L7) + P1 Layers (L0, L1, L3, L4)
**Rationale:** First end-to-end runnable verdict. The combiner is the integration seam every layer plugs into, so it must exist before more layers; the P1 layers are the table-stakes content/trust signals.
**Delivers:** `Layer` trait + `LayerRegistry` + `SubScore` contract; L0 whitelist, L1 near-dup (+ fingerprint side-output for L6), L3 entropy, L4 link/mention; hand-set weights; `sigmoid` fusion writing `score`/`signal`.
**Uses (STACK):** `simhash`, `unicode-segmentation`, `ahash`/`foldhash`/`xxhash`, cached whitelist HTTP client.
**Avoids:** Pitfall 2 (whitelist low-weight + corpus-age counterweight), Pitfall 3 (grapheme-based entropy, NFC, not bytes), Pitfall 4 (near-dup length floor + kind-awareness), Pitfall 16 (single-pass tokenization shared across layers).

### Phase 5: CLI — `run` + `export`
**Rationale:** First shippable deliverable — produces the reviewable suspected-spammer list with per-layer evidence.
**Delivers:** `clap` subcommands; suspected-spammer export with layer decomposition and sample evidence per pubkey.
**Avoids:** Pitfall 18 (list-only boundary, per-pubkey evidence trail), Pitfall 19 (record threshold with the model version).

### Phase 6: `label` Subcommand + Tuner (`tune`)
**Rationale:** Closes the correctability loop; requires signals (Phase 4) and labels to exist. This is the project's core "backprop from labels" promise.
**Delivers:** human-label capture; `signal x label` join -> `linfa-logistic` fit (standardized, class-weighted, L2-regularized, holdout) -> `weight` table with provenance.
**Uses (STACK):** `linfa-logistic` + `ndarray`.
**Avoids:** Pitfall 10 (inject random unflagged samples; track recall), Pitfall 11 (class imbalance, leakage audit, min-label gate, holdout), Pitfall 19 (calibrate threshold to target precision per round).

### Phase 7: Phase B Aggregator (L6) + fingerprint clustering
**Rationale:** Highest value (coordinated botnets), highest cost; deliberately late because it needs a proven streaming pipeline (Phase 3) and L1 fingerprints (Phase 4), and has a distinct corpus-wide memory model.
**Delivers:** corpus-wide exact-hash bucketing (DISTINCT-author counting) + `gaoya` MinHash/LSH over spilled fingerprints; re-fuse L7 with the L6 sub-score.
**Uses (STACK):** `gaoya`, `ahash` bucket maps.
**Avoids:** Pitfall 5 (coordination co-signal for high confidence), the O(n^2)/in-memory-join anti-pattern (bounded-memory buckets reading fingerprints from SQLite).

### Phase 8: Remaining Layers (L2, L5, L8)
**Rationale:** Pure additions thanks to EAV `signal` (no schema change); add only once labels exist to weight them safely per the false-positive lesson.
**Delivers:** L2 cadence (co-signal-gated), L5 tag/kind fingerprint, L8 lang/script anomaly — all as registry entries.
**Uses (STACK):** `whatlang`, `unicode_skeleton`/UTS#39.
**Avoids:** Pitfall 1 (cadence co-signal-gated, low prior), Pitfall 9 (sanitize author-claimed `createdAt`).

### Phase Ordering Rationale

- **Dependency order over priority order.** Persistence is the root (idempotency + storage); connectivity proves the contract; the back-pressure seam must be validated before layers; the combiner precedes additional layers because it is the integration seam; the tuner needs signals+labels; L6 needs the streaming pipeline + L1 fingerprints.
- **Architecture grouping.** Phases map to ARCHITECTURE.md's component boundaries (`store/` -> `graphql/`+`pipeline/enumerator` -> `pipeline/` -> `layers/`+`combiner` -> `cli` -> `tune/` -> `aggregate/` -> remaining `layers/`).
- **Pitfall avoidance baked into ordering.** Streaming (Pitfall 13) and the threading split (Pitfall 17) are locked in at Phase 3 *before* any layer, because they are structural rewrites if wrong. The tuning safeguards (Pitfalls 10/11/19) land together in Phase 6. The enforcement boundary (Pitfall 18) is held from Phase 5 onward.

### Research Flags

Phases likely needing deeper research during planning (`/gsd-plan-phase --research-phase <N>`):
- **Phase 6 (Tuner):** statistical design is the highest-risk area — class-imbalance handling, leakage audit, holdout/calibration, and the selection-bias negative-sampling strategy need careful design beyond "fit a logistic regression." Confidence on final weights is MEDIUM.
- **Phase 7 (L6 cross-pubkey):** `gaoya` LSH band/row tuning to a target Jaccard threshold and the bounded-memory spill/read-back design are non-trivial; verify `gaoya` API shape against current 0.2.x.

Phases with standard patterns (skip research-phase):
- **Phase 1 (Persistence):** well-documented `rusqlite`/WAL single-writer pattern; schema already specified in ARCHITECTURE.md.
- **Phase 2 (GraphQL client):** HTTP+JSON against a code-verified contract; query shapes and error codes are fully enumerated.
- **Phase 3 (Pipeline):** the tokio->bounded-channel->rayon pattern is the documented STACK decision with worked examples.
- **Phases 4/5/8 (P1 + remaining layers, CLI):** algorithms are literature-backed and specified; FP mitigations are enumerated in PITFALLS.md.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Every crate version verified live against crates.io on 2026-06-25 (not recalled); concurrency model is the standard Rust async-front/rayon-back pattern. |
| Features | HIGH (algorithms) / MEDIUM (weights & prevalence) | Algorithms are literature-backed (SimHash WWW'07, IAT entropy USENIX'08, UTS#39); spam base rate and final layer weights are unknown and must be tuned from labeled data. |
| Architecture | HIGH | Component shape follows directly from the code-verified `contract.md`, the verified STACK concurrency model, and the literature-backed layer set; build order is dependency-honest. |
| Pitfalls | HIGH | Grounded in this project's `contract.md` v1.2, `PROJECT.md`, and the completed `web-of-trust` spam-clusters production-data spike — not generic advice. |

**Overall confidence:** HIGH

### Gaps to Address

- **Spam base rate is unknown.** Cannot estimate recall or set a principled threshold until labels exist. Handle in Phase 6: track recall proxies via random unflagged sampling; set a minimum-label gate before auto-retuning; default to high-precision thresholds meanwhile.
- **Final layer weights are unvalidated.** Hand-set conservative priors (modest on L0/L2/L8) ship first; `tune` replaces them once a labeled FP set accumulates. Version every model (weights + label set + snapshot) for rollback.
- **GraphQL/HTTP throughput ceiling is unproven.** Whether the network hop or the analysis CPU dominates is unknown until profiled. Handle by putting the fetch layer behind a trait (Phase 3) so the feature-gated `heed` direct-LMDB v2 path is possible *if* `tracing` spans prove the HTTP hop dominates — do not pre-optimize.
- **`linfa` <-> `ndarray` version pairing.** linfa 0.8.x pins ndarray 0.16; verify at integration time (Phase 6) and do not bump `ndarray` independently.
- **`gaoya` API surface** for the LSH banding index should be verified against 0.2.x at Phase 7 planning time.
- **Allowlist of known-legit automated publishers** (news bots, relays) is needed to seed the tuner against Pitfall 5; this is a data/curation task to surface during Phase 6 planning.

## Sources

### Primary (HIGH confidence)
- `contract.md` (LMDB2GraphQL v1.2, code-verified 2026-06-24) — endpoints, `authors`/`latestPerAuthor` semantics and limits (<=1000 authors, clamp-to-500, 256 KiB body), error codes (`503`/`INVALID_CURSOR`/`TOO_MANY_AUTHORS`/`413`), empty-group omission, opaque cursors, snapshot/drift, author-claimed `createdAt`, `/ready` gating.
- crates.io REST API — live version + maintenance check on 2026-06-25 for every crate (e.g. `gaoya` 0.2.2 updated 2026-06-15, `rusqlite` 0.40.1, `reqwest` 0.13.4, `tokio` 1.52.3, `rayon` 1.12.0, `linfa`/`linfa-logistic` 0.8.1).
- `.planning/PROJECT.md` — no-LLM/speed constraint, streaming/bounded-memory requirement, read-only upstream, idempotent re-runnable batch, whitelist-as-signal, list-only (no-enforcement) scope, label-driven re-tuning, `heed`-as-v2 note.
- `web-of-trust/.planning/spikes/spam-clusters/` (production-data spike) — empirical finding that top timestamp bursts were crawler-refresh artifacts (0% low-follower, fc-capped at 249), that timing signals are only meaningful intersected with low-trust co-signals, that exact-value collisions across pubkeys are the cleanest coordination tell, and the new-legit-account over-inclusion FP lesson.
- Repo MEMORY — Rust toolchain PATH/`RUSTUP_TOOLCHAIN` override gotcha for `spam/`; `MDB_BAD_RSLOT` in-container LMDB requirement (relevant to deferred `heed` v2); GSD subagents anchor to git root (use absolute paths).

### Secondary (MEDIUM confidence)
- Manku/Jain/Das Sarma (Google, WWW 2007) — 64-bit SimHash, Hamming k=3 for near-dup. https://research.google.com/pubs/archive/33026.pdf
- Gianvecchio et al. (USENIX Security 2008) — IAT entropy for human/bot classification. https://www.usenix.org/legacy/event/sec08/tech/full_papers/gianvecchio/
- Nature Sci. Reports 2022 — relative-entropy automated-behavior detection. https://www.nature.com/articles/s41598-022-11854-w
- Unicode UTS#39 Security Mechanisms (confusables, skeleton, mixed-script). https://www.unicode.org/reports/tr39/
- MinHash + LSH banding for document dedup — https://mattilyra.github.io/2017/05/23/document-deduplication-with-lsh.html
- `spam-explorer/` (DeepFry, structural-only Go) — disjoint signal source; defines what NOT to re-implement (follow-graph detection).

### Tertiary (LOW confidence)
- Spam *prevalence* and *final layer weights* — inferred/assumed; must be measured and tuned from labeled data, not taken from any source.

---
*Research completed: 2026-06-25*
*Ready for roadmap: yes*
