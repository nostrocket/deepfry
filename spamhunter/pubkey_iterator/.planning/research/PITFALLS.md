# Pitfalls Research

**Domain:** Rust batch pubkey-level spam classifier over a Nostr corpus, fed by the read-only LMDB2GraphQL adapter; weighted tunable layers → per-pubkey score → SQLite → logistic-regression re-tuning from human labels. No LLM. Speed-paramount, re-runnable over 100M+ events.
**Researched:** 2026-06-25
**Confidence:** HIGH (grounded in this project's `contract.md` v1.2, `PROJECT.md`, and the completed `web-of-trust/.planning/spikes/spam-clusters/` spike — not generic advice)

## Critical Pitfalls

### Pitfall 1: Timing/cadence signals dominated by infrastructure artifacts, not bots

**What goes wrong:**
A "posting cadence / burst" layer fires hard on same-second / same-minute clusters and ranks them as the top spam candidates — but the clusters are crawler refresh batches, client default-list writes, mass migrations, or relay-side normalization, not coordinated bots. The biggest, most "obvious" temporal spikes are the worst false positives.

**Why it happens:**
`createdAt` is **author-claimed** (contract §8) and can legitimately repeat. Worse, in DeepFry's own ecosystem the spam-clusters spike (probe 03) found the largest timestamp spikes (Oct–Nov 2025, ~50/sec) were **0% low-follower, follower-capped at 249** — pure crawler/refresh write-time artifacts. Temporal clustering alone correlated with our own infrastructure, not adversaries. This engine reads through LMDB2GraphQL which exposes only `createdAt` (no relay-receive time), so the same trap is structurally present.

**How to avoid:**
- Never let a timing layer stand alone — gate it behind a content or low-trust co-signal (the spike's explicit lesson: only meaningful when intersected with low `follower_count`).
- Treat `createdAt` as adversary-controllable input, not ground truth (see Pitfall 9). Do not derive "burst" purely from `createdAt` proximity.
- Prefer signals on *what was published* (near-dup content, link ratio, templated text) over *when it claims to be published*.
- If a cadence layer is kept, weight it low by default and require the tuning loop to earn its weight up from labels, not down from a high prior.

**Warning signs:**
- The top-N flagged pubkeys cluster on a handful of exact timestamps.
- Flagged cohorts have *high* follower counts / are well-connected (real bots are usually follower-less).
- Flagged windows line up with known crawler runs (`~/deepfry/crawler-metrics.jsonl`).

**Phase to address:** Layer-design phase (define cadence layer with a mandatory co-signal); reinforced in the tuning/validation phase.

---

### Pitfall 2: Whitelist "absence = spam" false-positives new legit pubkeys

**What goes wrong:**
The whitelist layer treats *not being whitelisted* as a spam signal. But the whitelist reflects **web-of-trust crawler COVERAGE** ("seen in the wild" as a `Profile` node in Dgraph), **not curated trust**. A brand-new legitimate pubkey, or anyone the crawler simply hasn't reached yet, is absent — so the engine penalizes new real users exactly as if they were spam. This is a systematic, time-correlated FP mode, not random noise.

**Why it happens:**
`PROJECT.md` correctly states "absence is a spam signal," but the underlying semantic is coverage, not endorsement. Crawl coverage lags reality: new accounts, accounts on relays the crawler doesn't reach, and accounts crawled-but-not-yet-refreshed all read as "absent." Developers conflate "whitelisted" with "trusted."

**How to avoid:**
- Make the whitelist a *low-weight, single-layer* signal exactly as the spec says — never an exemption and never a dominant term. Absence must be insufficient on its own to flag.
- Combine absence with corpus-age evidence: a pubkey absent from the whitelist **but** with a long, varied posting history is almost certainly a coverage gap, not spam. Use the pubkey's own event span (earliest fetched `createdAt`) as a counterweight.
- Record the whitelist signal as its own column so FPs trace cleanly back to it during labeling.
- Consider a "coverage confidence" notion: if crawler coverage is known-incomplete, down-weight absence globally.

**Warning signs:**
- Newly-active pubkeys with clean, diverse content score high purely on the whitelist layer.
- Removing the whitelist layer drops a large block of flags (indicates over-reliance).
- Human labelers keep marking "absent-from-whitelist" pubkeys as false positives.

**Phase to address:** Whitelist-layer phase (weight/mitigation design); verified in tuning phase (the layer's learned weight should be modest).

---

### Pitfall 3: Charset/entropy layers misfire on non-English, CJK, and emoji-heavy content

**What goes wrong:**
An "entropy / templated-text / charset" layer flags legitimate CJK, RTL (Arabic/Hebrew), Cyrillic, or emoji-dense posts as anomalous because they trip byte-level entropy thresholds, low-ASCII ratios, or "looks templated" tests calibrated on English. Whole language communities get systematically flagged.

**Why it happens:**
Entropy/charset heuristics are usually tuned on Latin-script English. CJK has high per-character information but compresses differently; emoji and combining marks inflate byte counts vs. grapheme counts; short legitimate non-English posts look "low-diversity" under naive n-gram entropy. Rust string handling makes it easy to accidentally measure bytes (`len()`) instead of Unicode scalar values or graphemes.

**How to avoid:**
- Compute features on **Unicode scalar values / graphemes**, not raw bytes. Normalize (NFC) before any char-distribution analysis.
- Make charset features script-relative (e.g. "fraction of repeated graphemes" is more language-neutral than "fraction of non-ASCII").
- Do not flag "non-Latin" as a signal in itself — it is a demographic, not a behavior.
- Hold out a multilingual sample during tuning and inspect per-script false-positive rate explicitly.

**Warning signs:**
- Flagged set skews toward a particular language/script.
- Emoji-heavy or CJK posts cluster at high entropy-layer sub-scores.
- Grapheme count and byte count diverge wildly for flagged items.

**Phase to address:** Content-layer phase (entropy/charset design with grapheme-based features); validated in tuning phase with per-script FP audit.

---

### Pitfall 4: Near-dup layer flags legit short messages, reposts, and threaded replies

**What goes wrong:**
A SimHash/MinHash near-duplicate layer flags a pubkey because its own events repeat — but the repetition is legitimate: "gm", "+", reaction-style notes, boilerplate replies, reposts/quote-reposts (kind 6/16), or threaded replies that share quoted context. Active community members and news/relay bots get caught.

**Why it happens:**
Short strings collapse to near-identical hashes regardless of intent ("gm" ≈ "gm" ≈ "GM"). Reposts intentionally duplicate content. Threaded replies share quoted text and `e`/`p` tag scaffolding. The layer measures "repeats itself" without distinguishing *spam repetition* (same promo blast to many) from *normal repetition* (short social tokens, structured replies).

**How to avoid:**
- Apply a minimum content-length floor before near-dup scoring; exclude trivially short notes.
- Distinguish kinds: reposts (6/16), reactions (7), and replies (kind 1 with an `e` tag) have different baselines than original kind-1 posts. Score near-dup primarily on *original* content.
- Strip quoted/threaded scaffolding (leading quotes, mention prefixes) before hashing replies.
- Consider *cross-pubkey* duplication (same content from many pubkeys = coordinated blast) as a stronger signal than *within-pubkey* duplication.

**Warning signs:**
- High near-dup scores concentrated on accounts whose events are mostly very short.
- Reposters and reply-heavy accounts dominate the flagged set.
- Per-event content length histogram of flagged set skews tiny.

**Phase to address:** Near-dup-layer phase; FP audit in tuning phase.

---

### Pitfall 5: Legit high-volume accounts (news bots, relays, clients, aggregators) flagged as spam

**What goes wrong:**
High-throughput legitimate publishers — news/RSS bridges, relay system accounts, bot accounts that auto-post, NIP-aggregators — trip volume, cadence, link-ratio, and near-dup layers simultaneously and land at the top of the spam list. These are often the *most visible* accounts, so FPs here are maximally embarrassing.

**Why it happens:**
"Posts a lot, with links, on a regular cadence, with similar structure" describes both a spam blaster and a legitimate news bot. Multiple weak layers all fire in the same direction, and weighted aggregation amplifies the agreement into a confident FP.

**How to avoid:**
- Build an explicit allowlist concept for known-legit automated publishers (separate from the WoT whitelist) and feed it as labels into the tuning loop early.
- Lean on signals that separate spam from automation: *audience overlap* (spam blasts identical content to disjoint strangers; news bots post to followers/topics), link *destination diversity* (one promo domain vs. varied news sources), and reply/mention patterns.
- Never let "volume + links + cadence" alone clear a confidence threshold without a coordination signal.

**Warning signs:**
- Recognizable infrastructure/news accounts appear in top flags.
- Flagged accounts have many followers (real spam is usually follower-poor).
- The same FP accounts recur across runs.

**Phase to address:** Aggregation/scoring phase (require coordination co-signal for high confidence); seeded with allowlist labels in tuning phase.

---

### Pitfall 6: LMDB2GraphQL request-shape violations (256 KiB body, ≤1000 authors, ≤500 page)

**What goes wrong:**
The enumerator/fetcher batches naively and hits hard limits: a `latestPerAuthor` call with >1000 authors returns `TOO_MANY_AUTHORS`; a large `authors`/`ids` array blows the **256 KiB body cap** → `413`; a `limit`/`perAuthor` above 500 is **silently clamped** (so you think you asked for 1000 and silently get 500, under-fetching events and corrupting per-pubkey analysis); a stale/hand-built cursor returns `INVALID_CURSOR`.

**Why it happens:**
The contract (§5, §6, §7, §12) enforces these, but several fail **silently or as `200`-with-errors**: page clamp is silent, GraphQL errors arrive in a `200` body's `errors[]` array (not HTTP status). Code that only checks HTTP status misses `INVALID_CURSOR`/`TOO_MANY_AUTHORS` entirely. 64-char hex authors are ~64 bytes each in JSON; 1000 authors ≈ 64 KB before query overhead, so body-size and author-count limits interact.

**How to avoid:**
- Chunk `latestPerAuthor` author lists at **≤1000**, and additionally size-check the serialized body against 256 KiB (the smaller of the two binds first). Pick a conservative batch (e.g. 500 authors) so body never approaches the cap.
- Treat `perAuthor`/`limit` as **≤500 hard**; if you need ~100 events/author you're fine, but never assume >500 is honored — assert the returned count and detect silent clamping.
- **Always parse `errors[]` on every `200` response** and branch on `extensions.code` (`INVALID_CURSOR`, `TOO_MANY_AUTHORS`).
- Treat cursors as opaque; never parse, construct, persist-and-reuse-across-snapshots, or cross-use `events` vs `authors` cursors.

**Warning signs:**
- Per-pubkey event counts cap suspiciously at 500.
- Intermittent `413`s on the fattest batches.
- `errors[]` present but ignored because HTTP was `200`.

**Phase to address:** GraphQL-client/enumeration phase (batching + error-code handling as first-class).

---

### Pitfall 7: Empty-group omission and index-zipping corruption

**What goes wrong:**
Code requests `latestPerAuthor` for N authors and assumes the result array has N groups in the same order, zipping results back by index. But the contract (§5, §8) **omits authors with zero matching events** — so `result.length < authors.length`, indices shift, and every author after the first gap gets the wrong events attributed to them. Spam scores are then computed on mismatched data.

**Why it happens:**
The omission is easy to miss; small test batches where every author has events hide it. A pubkey with events of one kind but none of the requested kind silently drops out.

**How to avoid:**
- Always match results back by the `author` field, never by position. Build a `HashMap<pubkey, group>`.
- Authors absent from the response = "no events of this kind" — handle as a real (possibly meaningful) state, not as an error or a skip.
- Be explicit about which kinds you fetch: `latestPerAuthor` is single-kind; a pubkey that only posts kind-1 won't appear in a kind-0 query.

**Warning signs:**
- Off-by-one or shifted attributions in spot checks.
- `assert(result.len() == authors.len())` would fail (it will).
- Pubkeys with content scored as if empty (or vice versa).

**Phase to address:** GraphQL-client/fetcher phase.

---

### Pitfall 8: Snapshot drift across pagination mistaken for data, breaking idempotency

**What goes wrong:**
The corpus changes mid-enumeration (new events, deletions, NIP-40 expirations, replaceable-event churn). The contract (§6.4, §8) is explicit: each query is a query-time snapshot, and a new author added mid-pagination "may or may not appear depending on where it sorts relative to your cursor." Treating a multi-page enumeration as one consistent snapshot produces non-reproducible runs and inconsistent scores, and re-runs diverge even with identical code.

**Why it happens:**
`authors` pagination spans many requests over a long batch (100M+ events ⇒ long wall-clock). The DB is live. Developers assume "I enumerated the corpus" = a fixed set, but it's a moving target across pages.

**How to avoid:**
- Record `stats.maxLevId` (and `eventCount`) at run start and end; if it moved materially, the run spanned corpus changes — record this in run metadata so score diffs are interpretable.
- Make the run idempotent *given its inputs*: stamp each pubkey's score with the data it was actually computed from (event ids/levIds, fetch timestamp), so a re-run over the same snapshot is reproducible and a re-run over a changed corpus is explainably different.
- Don't treat "appeared/disappeared between pages" as a signal — it's drift.
- For resumable runs, persist progress by **opaque cursor + pubkey watermark**, accepting that resumption after a long gap reads a newer snapshot (Pitfall 12).

**Warning signs:**
- Two back-to-back runs produce different pubkey sets or scores with no code change.
- `maxLevId` at end ≫ at start on long runs.
- Pubkeys near the cursor frontier flicker in/out.

**Phase to address:** Enumeration phase (snapshot/drift handling) and idempotency phase (run-metadata stamping).

---

### Pitfall 9: Trusting author-claimed `createdAt` for time-dependent features

**What goes wrong:**
Features like "posts per hour," "account age," "recency," or "burst" use `createdAt` as if it were true time. But `createdAt` is **author-asserted** (contract §8) — a spammer can set it to the future, the distant past, or all-identical. Features built on it are adversary-controllable: a spammer trivially spoofs a "natural" cadence or hides recency.

**Why it happens:**
`createdAt` is the only time field LMDB2GraphQL exposes (no relay-receive/ingest time available through this lens). It's tempting to treat the only timestamp as authoritative. The spike already saw `createdAt` clusters that were artifacts; adversarial spoofing is the active-attacker version of the same hazard.

**How to avoid:**
- Treat `createdAt` as untrusted input. Clamp/sanitize: future timestamps (> now) and absurd-past values should be flagged or normalized, not used raw.
- Prefer order-stable, content-derived features over absolute-time features where possible.
- If using cadence, use it only as a weak, co-signal-gated layer (ties back to Pitfall 1), and consider *intra-batch ordering* (`levId` via fetched events) rather than claimed wall-clock.
- Never compute "account age" as a strong trust signal from `createdAt` alone — see also Pitfall 2's coverage counterweight.

**Warning signs:**
- Pubkeys with future-dated or epoch-zero events score anomalously (high or low).
- A spammer cohort shows implausibly uniform or implausibly old timestamps.

**Phase to address:** Feature/layer-design phase (sanitize timestamps); reinforced in cadence-layer design.

---

### Pitfall 10: Selection-bias feedback loop in the label-driven tuning

**What goes wrong:**
The logistic-regression re-tuning learns only from human-labeled false positives — and humans only ever review pubkeys **the model already flagged**. The model never sees its own false *negatives* (spam it missed). Over re-tuning cycles it optimizes to please reviewers on its current flagged set, narrows, and entrenches blind spots: spam patterns it doesn't currently catch are never labeled, so weights never learn to catch them. The classifier converges to "agrees with itself."

**Why it happens:**
"Backpropagation from labels" with a review queue sourced only from positives is a classic closed-loop selection-bias trap. Labeling negatives (huge unflagged majority) is expensive, so it's skipped. Spam base rate is unknown, so the model can't even estimate how much it's missing.

**How to avoid:**
- Always inject a **random / stratified sample of unflagged pubkeys** into the human review queue, not only flagged ones — so false negatives can be discovered and labeled.
- Track precision *and* recall proxies over re-tuning rounds; if the flagged set shrinks monotonically while "labeled FP rate" improves, suspect overfitting to the loop.
- Version every model: store weights, the label set used, and the run/snapshot, so you can compare rounds and roll back.

**Warning signs:**
- Flagged-set size shrinks each round while reviewer-agreement rises (looks great, is selection bias).
- New spam campaigns aren't caught until manually surfaced.
- Review queue contains zero "model said clean, human says spam" cases (because none are ever shown).

**Phase to address:** Tuning/feedback phase (review-queue sampling design + metrics).

---

### Pitfall 11: Class imbalance, label leakage, and overfitting a tiny label set

**What goes wrong:**
The spam base rate is unknown and likely low; a logistic regression trained on a tiny, imbalanced, hand-labeled set (a) collapses to predicting the majority class, (b) overfits idiosyncrasies of the few labeled examples, and/or (c) leaks the label through a feature (e.g. the whitelist layer correlates with how labels were sourced). Re-tuned weights look better on the labeled set but generalize worse.

**Why it happens:**
Early label sets are tiny and non-representative (sourced from the flagged top-N — see Pitfall 10). Imbalance isn't handled (no class weighting/resampling). Features and labels share provenance (whitelist-derived labels + whitelist feature → leakage). With few labels and many layer weights, the fit is underdetermined.

**How to avoid:**
- Apply class weighting or balanced resampling; report metrics suited to imbalance (precision/recall/PR-AUC), never raw accuracy.
- Regularize aggressively (L2) given few labels and few features (one weight per layer) — keep the model small and interpretable, which suits "tunable layer weights" anyway.
- Audit for leakage: if a label was assigned *because* of a layer's signal, that layer can't be trained on it without circularity. Keep label provenance in SQLite.
- Hold out a labeled test split; refuse to ship weights that only improve train-set fit.
- Set a minimum label count before auto-retuning; below it, keep manual priors.

**Warning signs:**
- Train fit improves, held-out fit doesn't.
- One layer's weight dominates and matches label provenance.
- Weights swing wildly between rounds (underdetermined fit).

**Phase to address:** Tuning phase (model design: regularization, class weighting, holdout, leakage audit).

---

### Pitfall 12: Non-deterministic scores and broken partial-run resumption

**What goes wrong:**
Re-runs produce different scores for the same pubkey even on unchanged data — because of nondeterministic iteration order (HashMap), float-accumulation order differences across rayon threads, RNG without a fixed seed (in sampling/MinHash), or floating-point non-associativity in weighted aggregation. Separately, a crashed/resumed run double-counts or skips pubkeys because progress wasn't checkpointed atomically. The project explicitly requires "re-runnable idempotent batch."

**Why it happens:**
Rust `HashMap` randomizes iteration; `rayon` parallel reduce is non-deterministic in float-sum order; MinHash/SimHash and any sampling need seeded RNG; SQLite writes interleaved with enumeration can leave partial state if not transactional. "Idempotent" is asserted in the spec but easy to violate silently.

**How to avoid:**
- Seed all RNG deterministically (record the seed in run metadata). Use deterministic hashers where hashing order affects output.
- Make weighted aggregation order-independent (sum in a fixed layer order, or use a numerically stable reduction) so parallel execution doesn't change scores.
- Checkpoint enumeration progress (opaque cursor + last-completed pubkey) transactionally in SQLite; on resume, skip already-scored pubkeys for the same run id.
- Use UPSERT keyed by `(pubkey, run_id)` so a re-run/resume overwrites rather than duplicates.

**Warning signs:**
- Diffing two runs on identical data shows score jitter in low bits or reordered flag lists.
- Resumed runs have duplicate or missing pubkey rows.
- Score depends on thread count.

**Phase to address:** Idempotency/persistence phase (determinism + resumable checkpointing).

---

### Pitfall 13: Buffering the corpus instead of streaming (memory blow-up at scale)

**What goes wrong:**
Code collects all distinct authors (potentially millions) into a `Vec`, or accumulates all fetched events, before processing — exhausting memory and stalling at 100M-event scale. The contract's pagination examples literally `push` into an `all` array; copying that pattern at corpus scale is fatal.

**Why it happens:**
The contract's JS recipes (§6.1, §6.4) buffer into `all` for illustration. Prototypes work on tiny corpora and the buffering pattern silently scales into OOM. `PROJECT.md` constraint: "hot paths must stream, not buffer the whole corpus."

**How to avoid:**
- Stream: enumerate one `authors` page → immediately fetch+score that page's pubkeys in chunks → write results to SQLite → drop the page. Never hold the full author set or full event set in memory.
- Bound concurrency with explicit backpressure (a fixed-size work channel), so fetch/score/write stages overlap without unbounded queues.
- Score per-pubkey from its ~100 events and discard those events before moving on.

**Warning signs:**
- RSS grows linearly with corpus size.
- A single `Vec` holds all authors/events.
- OOM only on the production corpus, never in tests.

**Phase to address:** Architecture/pipeline phase (streaming design from day one).

---

### Pitfall 14: GraphQL round-trip overhead as the throughput ceiling at 100M events

**What goes wrong:**
At 100M+ events the bottleneck is network/serialization round-trips to LMDB2GraphQL, not the Rust scoring. Per-pubkey or tiny-batch requests, over-selecting fields (especially the large `raw` field), and serial (non-pipelined) requests make a "fast Rust engine" wall-clock-bound on HTTP. Cost is `authors × perAuthor` index scans server-side too.

**Why it happens:**
It's natural to fetch one pubkey at a time. `raw` is byte-exact JSON and can be large (§5, §9). The adapter is unauthenticated/no-rate-limit but still single-process; flooding it serially wastes the pipeline.

**How to avoid:**
- Batch with `latestPerAuthor` (up to the ≤1000/256 KiB limit, Pitfall 6) to amortize round-trips.
- Select **only fields you score on** — skip `raw` unless you need canonical bytes; skip `tags`/`sig` if a layer doesn't use them (§9 best practice 6).
- Pipeline requests with bounded concurrency (overlap in-flight requests) instead of strict serial paging.
- Keep `perAuthor` to the real need (~100), since server cost ≈ `authors × perAuthor`.
- Note the documented escape hatch: `PROJECT.md` says the engine could later read strfry LMDB directly to skip the GraphQL hop — design the fetch layer behind a trait so that swap is possible.

**Warning signs:**
- CPU underutilized while wall-clock is high (I/O bound).
- Payloads dominated by `raw`.
- One in-flight request at a time.

**Phase to address:** GraphQL-client phase (field selection + pipelining); architecture phase (fetch-layer abstraction for future direct-LMDB path).

---

### Pitfall 15: SQLite write contention / per-row commit overhead under parallel scoring

**What goes wrong:**
Parallel scoring workers each write per-pubkey rows to SQLite, hitting `SQLITE_BUSY`/lock contention or paying a full fsync per row, throttling the whole pipeline. With millions of pubkeys, naive per-row autocommit makes SQLite the bottleneck.

**Why it happens:**
SQLite is single-writer. Spawning N rayon/tokio workers that each `INSERT` independently serializes on the write lock; default journal mode + per-statement commit fsyncs constantly.

**How to avoid:**
- Funnel all writes through a **single writer task/thread**; scoring workers send results over a channel.
- Use **WAL mode**, batch inserts in transactions (e.g. a few thousand rows per commit), and tune `synchronous`/`busy_timeout` deliberately.
- Use UPSERT keyed on `(pubkey, run_id)` (also serves idempotency, Pitfall 12).

**Warning signs:**
- `SQLITE_BUSY` errors or long lock waits.
- Throughput drops as worker count rises (negative scaling).
- fsync dominates a profile.

**Phase to address:** Persistence phase.

---

### Pitfall 16: Allocator/hashing hot-loop overhead drowning the speed goal

**What goes wrong:**
Per-event allocations (new `String`s, regex recompilation, per-call hasher construction), UTF-8 re-validation, and re-tokenizing the same content for every layer turn a "fast" engine slow. Speed is paramount per `PROJECT.md`, but death-by-a-thous-allocations in the inner loop silently erodes it.

**Why it happens:**
Each layer independently parses/tokenizes the same event; SimHash/MinHash and n-gram entropy allocate freely if not written carefully; regexes compiled inside loops; default hasher re-seeded per use.

**How to avoid:**
- Tokenize/normalize each event **once**, share the parsed form across all layers (compute features in one pass).
- Reuse buffers; precompile regexes (`once_cell`/`lazy_static`); pick fast deterministic hashers for content fingerprinting.
- Profile the hot loop on a representative slice before scaling; budget per-event CPU.
- Avoid cloning event content per layer — pass references.

**Warning signs:**
- Profiler shows allocation/`memcpy`/regex-compile dominating.
- Throughput far below CPU-bound expectation.
- Adding a layer multiplies runtime instead of adding a small increment.

**Phase to address:** Layer-architecture phase (single-pass feature extraction); validated by a perf baseline.

---

### Pitfall 17: rayon/tokio thread starvation (blocking I/O on async runtime, or mixing the two)

**What goes wrong:**
Blocking GraphQL/HTTP or SQLite calls run on a tokio async runtime (or CPU-heavy scoring runs on async worker threads), starving the runtime; or rayon CPU pools and tokio I/O pools fight for cores. The pipeline deadlocks or under-utilizes the machine.

**Why it happens:**
Rust mixes a CPU pool (rayon) and an async I/O runtime (tokio for HTTP). Calling blocking SQLite from an async task, or doing SimHash on tokio workers, blocks the executor; oversubscribing both pools to all cores causes contention.

**How to avoid:**
- Keep a clean split: async runtime for GraphQL I/O (bounded concurrency), a separate CPU pool (rayon or dedicated threads) for scoring, a single thread for SQLite writes. Communicate via bounded channels.
- Use `spawn_blocking` for unavoidable blocking calls on async tasks.
- Size pools deliberately; don't let rayon + tokio each claim all cores.

**Warning signs:**
- Latency spikes / apparent deadlocks under load.
- Cores idle while a queue backs up.
- Tail-latency tied to runtime thread count.

**Phase to address:** Architecture/pipeline phase (explicit stage/threading model).

---

### Pitfall 18: Ethics / blast-radius — false-flagging real users with no human gate

**What goes wrong:**
The list of "suspected spammers" is treated as authoritative and wired into downstream enforcement (whitelist removal, quarantine, denylist) without human review. Given the FP modes above (new legit users via whitelist coverage, non-English content, news bots, short messages), real users get silently de-platformed. The blast radius of a content-based denylist is censorship.

**Why it happens:**
The output is a clean SQLite list; it's tempting to feed it straight into the whitelist/quarantine pipeline. `PROJECT.md` scopes enforcement *out* of v1 ("the deliverable is a list, not a relay gate"), but that boundary erodes under pressure.

**How to avoid:**
- Hold the enforcement boundary the spec drew: v1 produces a **reviewable list**, never an automatic action. Any downstream enforcement must pass human review first.
- Surface, per flagged pubkey, the per-layer signals and sample evidence so a human can adjudicate — this also powers the labeling loop (Pitfall 10).
- Default to high-precision thresholds (favor missing spam over flagging real users); make the FP cost explicit in tuning.
- Keep the whitelist/quarantine integration explicitly out of v1 scope and gated behind review when it lands.

**Warning signs:**
- Any code path turns the SQLite list into an action without a human in the loop.
- Thresholds tuned for recall over precision.
- No per-pubkey evidence trail for reviewers.

**Phase to address:** Scope/contract phase (enforce list-only boundary); review-tooling phase (evidence surfacing).

---

### Pitfall 19: Threshold drift across re-tuning rounds

**What goes wrong:**
Each re-tuning round shifts the score distribution, so a fixed "flag if score > 0.7" threshold means something different every round — yesterday's flagged set and today's aren't comparable, and the effective FP rate silently changes even when weights "improved."

**Why it happens:**
Logistic-regression weight changes rescale scores; a hardcoded threshold isn't recalibrated. Without versioning, nobody notices the operating point moved.

**How to avoid:**
- Calibrate the decision threshold to a target precision on the held-out labeled set each round, and record it alongside the model version.
- Store score distributions per run; compare operating points across rounds, not raw thresholds.
- Pin (model weights, threshold, label set, snapshot id) together so a flag is fully reproducible.

**Warning signs:**
- Flagged-set size jumps between rounds with no campaign change.
- Same threshold, very different FP rate.
- No record of which threshold produced a historical list.

**Phase to address:** Tuning phase (calibrated, versioned thresholds).

---

### Pitfall 20: 503-during-startup and adapter availability not handled in the batch driver

**What goes wrong:**
The long-running batch issues queries while LMDB2GraphQL is still gating startup (returns `503` until LMDB opens, `dbVersion==3` asserted, comparator self-check passes) — or the adapter restarts mid-run — and the driver treats `503` as a fatal error or, worse, as "no data," producing a truncated/empty run.

**Why it happens:**
Contract §2: `POST /graphql` returns `503` until ready; `GET /ready` is the gate. A batch kicked off at deploy time races the adapter. Transient `503`/`internal error` need retry-with-backoff, not abort.

**How to avoid:**
- Gate run start on `GET /ready == 200`; poll, don't blast.
- Treat `503` and uncoded `"internal error"` as transient → retry with backoff; only abort on persistent failure.
- Distinguish "empty result" (legitimate) from "service not ready" (retry) — never conflate.

**Warning signs:**
- Runs started right after deploy come back empty/short.
- Sporadic mid-run failures abort the whole batch.

**Phase to address:** GraphQL-client phase (readiness gating + retry/backoff).

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Buffer all authors/events into a `Vec` (copy contract's `all` pattern) | Trivial to write; works on small corpora | OOM at 100M events; total rewrite of the pipeline | Only in a throwaway spike on a tiny corpus — never in the real engine |
| Per-row SQLite autocommit from each worker | Simplest write code | Lock contention, fsync-bound throughput | Never at scale; single-writer + batched txns required |
| Hardcoded score threshold | Fast to ship a "flagged" list | Threshold drift makes runs incomparable after re-tuning | MVP before any re-tuning exists; must become calibrated once tuning lands |
| Cadence/timing layer with a high prior weight | Catches obvious bursts immediately | Dominated by crawler/client artifacts → top FPs (proven in spike) | Never as a standalone high-weight layer; only co-signal-gated |
| Treat whitelist absence as a strong/dominant signal | Simple, catches uncrawled bots | Systematically FPs new legit users (coverage ≠ trust) | Never dominant; low-weight single layer only |
| Skip injecting unflagged samples into the review queue | Less labeling effort | Selection-bias feedback loop entrenches blind spots | Never — random/stratified negatives are required for valid tuning |
| Byte-level entropy/charset features | Easy in Rust (`str::len`, byte iter) | Systematic FPs on CJK/emoji/RTL communities | Never for the shipped layer; prototype only, must move to grapheme-based |
| Fetch `raw` (and all fields) always | One query shape | Payload bloat → I/O-bound at scale | When a layer genuinely needs canonical bytes; otherwise select minimal fields |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| LMDB2GraphQL `latestPerAuthor` | Sending >1000 authors; assuming result order/length matches request | Chunk ≤1000 (and ≤256 KiB body); match results by `author` field, never by index; absent author = no events of that kind |
| LMDB2GraphQL pagination | Parsing/constructing/persisting cursors; cross-using `events` vs `authors` cursors; assuming one consistent snapshot across pages | Treat cursors as opaque, pass back verbatim; expect `INVALID_CURSOR` on stale cursors; record `maxLevId` start/end to detect drift |
| LMDB2GraphQL errors | Checking only HTTP status; missing GraphQL `errors[]` in a `200` body | Always parse `errors[]`; branch on `extensions.code` (`INVALID_CURSOR`, `TOO_MANY_AUTHORS`); retry transient `503`/`internal error` |
| LMDB2GraphQL limits | Assuming `limit`/`perAuthor` >500 is honored | They silently clamp to 500 — assert returned counts; design for ≤500 |
| LMDB2GraphQL readiness | Querying during startup race | Gate on `GET /ready`; treat `503` as retryable, not fatal/empty |
| Whitelist (Dgraph `Profile`) | Reading presence as curated trust / absence as proof of spam | It's crawler coverage ("seen in the wild"); absence is a weak signal, mitigate with corpus-age counterweight |
| SQLite | Multi-writer parallel inserts | Single writer task + WAL + batched transactions + UPSERT on `(pubkey, run_id)` |
| `createdAt` | Using author-claimed time as ground truth for cadence/age | Sanitize (future/absurd-past), treat as untrusted, prefer content-derived features |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Buffering corpus instead of streaming | RSS grows with corpus; OOM | Page → score → write → drop; bounded channels | At full corpus (millions of authors / 100M events) |
| Per-pubkey / serial GraphQL requests | CPU idle, wall-clock high (I/O bound) | Batch via `latestPerAuthor`; pipeline with bounded concurrency | Anywhere past a few thousand authors |
| Over-selecting fields (`raw`, `tags`, `sig`) | Payloads dominated by unused data | Select only scored fields | At scale, payload bytes dominate |
| Per-row SQLite commits | `SQLITE_BUSY`, negative scaling with workers | Single writer, WAL, batched txns | Millions of pubkey rows |
| Allocation/regex-compile in inner loop | Profiler shows alloc/compile dominating | Single-pass tokenization shared across layers; precompiled regex; buffer reuse | Every event × every layer |
| rayon + tokio oversubscription / blocking on async | Idle cores + backed-up queue; tail latency tied to thread count | Separate I/O and CPU pools; `spawn_blocking`; sized pools | Under sustained throughput |
| Server-side `authors × perAuthor` cost | Adapter slow / heavy under big batches | Keep `perAuthor` to real need (~100); don't max both dimensions | 1000×500 requests |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Auto-wiring the suspected-spammer list into enforcement | Censorship / de-platforming real users (new accounts, non-English, news bots) | Keep v1 list-only; human review gate before any enforcement |
| Trusting `createdAt`/event fields as ground truth | Adversary spoofs cadence/age to evade or to poison features | Treat all event fields as untrusted input; sanitize timestamps |
| Exposing LMDB2GraphQL beyond loopback | Unauthenticated, no rate limit, full introspection — anyone reachable can query everything | Keep adapter on loopback / inside `deepfry-net`; the engine connects internally, never re-exposes it |
| Label provenance not tracked | Leakage / circular training (whitelist-sourced labels + whitelist feature) | Store label source in SQLite; audit feature↔label provenance before training |
| Treating signed events as needing re-verification, or re-deriving `raw` | Wasted CPU / corrupted canonical bytes (key order/whitespace differ) | strfry already verified sigs; use `raw` for canonical bytes, never reconstruct it |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Flagged list with no per-layer evidence | Reviewers can't adjudicate; labels are guesses; feedback loop degrades | Persist per-layer sub-scores + sample events per pubkey; surface them for review |
| Review queue shows only flagged pubkeys | Reviewers never see false negatives; selection bias entrenches | Include random/stratified unflagged samples in the queue |
| Opaque single spam score | Can't tell *why* a pubkey was flagged; can't tune the right layer | Show the layer decomposition; the spec's "independently tunable layers" depends on this visibility |
| Non-reproducible run identity | Can't compare runs / explain why a list changed | Stamp each run with snapshot id, model version, threshold, seed |

## "Looks Done But Isn't" Checklist

- [ ] **Author enumeration:** Often missing snapshot-drift handling — verify `maxLevId` is recorded start/end and the run knows if the corpus moved.
- [ ] **`latestPerAuthor` fetch:** Often missing empty-group handling — verify results matched by `author` field, not index, and absent authors handled.
- [ ] **GraphQL error handling:** Often missing `errors[]`/`extensions.code` parsing on `200` — verify `INVALID_CURSOR`/`TOO_MANY_AUTHORS`/`503` all handled, not just HTTP status.
- [ ] **Page limits:** Often missing silent-clamp detection — verify code never assumes `limit`/`perAuthor` >500 is honored.
- [ ] **Entropy/charset layer:** Often missing Unicode-correctness — verify features computed on graphemes/scalars (not bytes) and tested on CJK/emoji/RTL.
- [ ] **Near-dup layer:** Often missing length floor and kind-awareness — verify short notes / reposts / replies aren't auto-flagged.
- [ ] **Whitelist layer:** Often missing the coverage caveat — verify absence is low-weight and counterweighted by corpus age.
- [ ] **Tuning loop:** Often missing negative sampling — verify unflagged pubkeys enter the review queue and recall is tracked, not just FP rate.
- [ ] **Tuning model:** Often missing class-imbalance handling, regularization, holdout, and leakage audit — verify before auto-retuning weights.
- [ ] **Idempotency:** Often missing determinism — verify seeded RNG, order-independent aggregation, and that two runs on the same snapshot match bit-for-bit.
- [ ] **Resumption:** Often missing atomic checkpointing — verify resume skips done pubkeys and never double-counts (UPSERT on `(pubkey, run_id)`).
- [ ] **Streaming:** Often "works" on a tiny corpus only — verify constant memory on a large slice, not just unit tests.
- [ ] **Threshold:** Often hardcoded — verify it's calibrated per round and versioned with the model.
- [ ] **Enforcement boundary:** Often quietly crossed — verify no code path actions the list without human review.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Timing-layer FPs (Pitfall 1) | LOW | Drop the layer's weight to ~0 in tuning; re-gate behind a co-signal; re-run |
| Whitelist over-reliance (Pitfall 2) | LOW–MEDIUM | Re-weight via tuning + add corpus-age counterweight; re-label affected FPs |
| Charset/CJK FPs (Pitfall 3) | MEDIUM | Switch features to grapheme-based; re-run scoring (no schema change) |
| Near-dup FPs (Pitfall 4) | LOW–MEDIUM | Add length floor + kind-awareness; re-score |
| GraphQL limit/error mishandling (Pitfall 6,7,20) | MEDIUM | Fix client batching/error handling; **re-run affected pubkeys** (data may have been silently truncated) |
| Snapshot-drift / non-determinism (Pitfall 8,12) | MEDIUM–HIGH | Add run-metadata stamping + seeded determinism; historical runs without it can't be trusted/compared — re-run |
| Selection-bias loop (Pitfall 10) | HIGH | Add negative sampling; discard or re-weight models trained purely on flagged-set labels; rebuild label set |
| Memory blow-up (Pitfall 13) | HIGH | Re-architect to streaming pipeline — usually a structural rewrite, hence prevent early |
| Auto-enforcement of bad list (Pitfall 18) | HIGH | Reverse downstream actions (un-quarantine/re-whitelist); reputational damage may be irreversible — prevent, don't recover |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 1. Timing FPs | Layer-design / cadence-layer | Cadence layer requires a co-signal; standalone weight ≈ 0 after tuning |
| 2. Whitelist coverage FPs | Whitelist-layer | Absence is low-weight; corpus-age counterweight present; tuning gives modest weight |
| 3. Charset/CJK FPs | Content-layer | Features grapheme-based; per-script FP audit on multilingual holdout |
| 4. Near-dup FPs | Near-dup-layer | Length floor + kind-awareness; short/repost/reply not auto-flagged |
| 5. High-volume legit FPs | Aggregation/scoring | Coordination co-signal required for high confidence; allowlist labels seeded |
| 6. GraphQL limits | GraphQL-client / enumeration | Batch ≤1000 & ≤256 KiB; clamp detection; `errors[]`+codes handled |
| 7. Empty-group zip | GraphQL-client / fetcher | Results matched by `author`; `len` mismatch handled |
| 8. Snapshot drift | Enumeration + idempotency | `maxLevId` start/end recorded; run stamped with snapshot |
| 9. `createdAt` trust | Feature/layer-design | Timestamps sanitized; time features co-signal-gated |
| 10. Selection-bias loop | Tuning/feedback | Review queue includes random unflagged sample; recall tracked |
| 11. Imbalance/leakage/overfit | Tuning | Class weighting, L2, holdout, leakage audit, min-label gate |
| 12. Non-determinism/resume | Idempotency/persistence | Seeded RNG; order-independent agg; UPSERT `(pubkey,run_id)`; two-run bit match |
| 13. Buffering vs streaming | Architecture/pipeline | Constant memory on large slice |
| 14. Round-trip overhead | GraphQL-client + architecture | Batched, pipelined, minimal fields; fetch layer behind a trait |
| 15. SQLite contention | Persistence | Single writer + WAL + batched txns |
| 16. Allocator/hashing hot loop | Layer-architecture | Single-pass feature extraction; perf baseline met |
| 17. Thread starvation | Architecture/pipeline | Separate I/O vs CPU pools; `spawn_blocking`; no oversubscription |
| 18. Ethics/blast-radius | Scope/contract + review-tooling | No code path actions the list without human review |
| 19. Threshold drift | Tuning | Calibrated, versioned threshold per round |
| 20. 503/availability | GraphQL-client | `/ready` gating; transient retry-with-backoff |

## Sources

- Project `contract.md` (LMDB2GraphQL v1.2) §2–§12 — limits, error codes (`INVALID_CURSOR`, `TOO_MANY_AUTHORS`, `413`, `503`), empty-group omission, opaque cursors, snapshot semantics, author-claimed `createdAt`, `authors × perAuthor` cost. [HIGH — code-verified contract]
- Project `PROJECT.md` — whitelist-as-coverage model, list-only/no-enforcement scope, streaming constraint, no-LLM/speed constraint, Rust + SQLite + tuning-loop decisions. [HIGH]
- `web-of-trust/.planning/spikes/spam-clusters/03-burst-created-cohorts.md` and `00-INDEX.md` — empirical finding that top timestamp spikes were crawler-refresh artifacts (0% low-follower, fc-capped at 249), and that timing signals are only meaningful intersected with low-trust co-signals. Generalized here to all timing/`createdAt`-derived layers. [HIGH — production-data spike]
- `web-of-trust/.planning/spikes/spam-clusters/00-INDEX.md` — false-positive over-inclusion of new legit accounts lacking trusted followers (probe 04), generalized to the whitelist-coverage caveat. [HIGH]

---
*Pitfalls research for: Rust pubkey-level Nostr spam classifier over LMDB2GraphQL*
*Researched: 2026-06-25*
