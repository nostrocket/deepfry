# Feature Research

**Domain:** Algorithmic (non-LLM) pubkey-level spam detection on a Nostr relay — a stack of independently-tunable, weighted content/behavioral layers scoring each pubkey from its ~100 recent events (`content`, `kind`, `tags`, `createdAt`).
**Researched:** 2026-06-25
**Confidence:** HIGH (algorithms are well-established and literature-backed; spam *prevalence* and final weights are MEDIUM — must be tuned from labeled data)

> **Framing note.** In this project a "feature" is a **detection layer**. Each layer reads one or more event fields, emits a normalized sub-score `xᵢ ∈ [0,1]`, and is independently tunable (weight `wᵢ` + internal threshold). The deliverable is a *per-pubkey* verdict — never a per-event verdict (see anti-features). Every layer below is pure-Rust, streaming, and LLM-free.

---

## Feature Landscape

### Table Stakes (Must-Have Layers)

These are the layers without which the engine is not credibly a content/behavioral spam detector. Each is cheap, high-signal, and standard in the literature.

| Layer | What signal it captures / Why expected | Complexity | Notes (algorithm · accuracy · speed · FP risk · tunable knob) |
|-------|----------------------------------------|------------|----------------------------------------------------------------|
| **L0 Whitelist signal** | Absence from the relay whitelist (Dgraph `Profile` / web-of-trust coverage) = "never seen in the wild" — a strong prior for spam. Binary trust input. | LOW | **Algo:** O(1) lookup against whitelist-plugin HTTP server; emit `x = 0.0` if whitelisted, `x = 1.0` if absent. **Accuracy:** high recall as a *prior*, NOT a verdict (many legit new accounts are absent). **Speed:** one cached HTTP/dictionary hit per pubkey (batchable). **FP risk:** HIGH if used alone → must be a *weighted* term, never an exemption or a hard gate. Presence clears only this layer; pubkey still flows through every later layer. **Knob:** weight `w₀` (start modest, e.g. 0.5–1.0 of one logit); tune down hard from labels. |
| **L1 Within-pubkey near-duplicate ratio** | Copy-paste blasting: one account posting the same/near-same text repeatedly. The single highest-precision content signal. | MEDIUM | **Algo:** SimHash (Charikar/Manku, 64-bit) per event over token shingles; cluster a pubkey's own events by Hamming ≤ 3; sub-score = fraction of events in a near-dup cluster. Add exact-hash (xxhash of normalized text) for the cheap exact-dup pass first. **Accuracy:** ~0.75 P/R at Hamming 3 on web-scale; higher for verbatim blasts. **Speed:** O(tokens) to build fingerprint; compare = XOR+popcount (ns). **FP risk:** LOW–MED (templated greetings, "gm", reposts). **Knob:** Hamming threshold (k=3 default), near-dup-ratio cutoff. |
| **L2 Posting cadence / burst analysis** | Machine-regular timing and volume spikes from `createdAt`. Bots fire on fixed timers; humans don't. | MEDIUM | **Algo:** sort `createdAt`, compute inter-arrival times (IATs); features = **IAT entropy** (low ⇒ periodic bot — validated by Gianvecchio USENIX'08, Nature Sci.Rep. 2022), **coefficient of variation** (≈0 ⇒ metronomic), and **max burst** (events per short window). **Accuracy:** strong *in combination*, weak alone. **Speed:** O(n log n) sort over ≤100 events — trivial. **FP risk:** HIGH alone — the deepfry spam-clusters spike proved raw timestamp clustering is dominated by FALSE POSITIVES (crawler refresh batching, popular-client default writes). `createdAt` is author-claimed (contract §8). **Knob:** entropy floor, CV floor, burst window+count; weight kept modest. |
| **L3 Content entropy / templated-text** | Low-entropy templated spam OR high-entropy gibberish (algorithmic handles, random padding). | LOW | **Algo:** Shannon entropy over chars + bigrams; dictionary-word ratio; compression-ratio proxy (zstd/gzip length ÷ raw length) — templated text compresses hard, gibberish doesn't. Aggregate per-pubkey mean/min. **Accuracy:** good as a contributing feature. **Speed:** O(len) per event, very fast. **FP risk:** MED — short posts, code, non-Latin scripts skew entropy → normalize by length and script. **Knob:** entropy floor/ceiling, dict-ratio threshold. |
| **L4 Link & mention spam ratios** | Promo blasting: URL-heavy events, one domain repeated across events, mass `p`-tag mentions, hashtag stuffing. | LOW | **Algo:** per-pubkey ratios — fraction of events with URLs, distinct-vs-repeated domain count, URL-shortener prevalence, mean `p`-tags/event, mean `t`-tags/event, repeated identical hashtag sets. **Accuracy:** high for promo/affiliate spam. **Speed:** O(events × tags), counters only — very fast. **FP risk:** MED — news/aggregator accounts post many links; popular hashtags are normal. **Knob:** per-ratio thresholds; weighted, not hard cutoffs. |
| **L5 Tag/kind fingerprinting** | Templated tag structures and abnormal kind distributions (scripted clients emit uniform tag shapes). | LOW | **Algo:** histogram of `kind` per pubkey (entropy / single-kind dominance); canonical tag-shape signature (sorted tag-name multiset) and its repetition rate across the pubkey's events; cross-pubkey collision of tag signatures (see L6). **Accuracy:** moderate; strong when tag shape repeats exactly. The spike's "exact out-degree collisions across distinct pubkeys" lesson applies: exact structural repetition is the cleanest coordination tell. **Speed:** O(events × tags), hashable signature. **FP risk:** MED — many legit clients are templated too. **Knob:** kind-entropy floor, tag-signature repeat ratio. |

### Differentiators (High-Value Layers)

Layers that materially raise precision/recall beyond a basic per-account filter. These are where this engine earns its keep over `spam-explorer`'s structural-only approach.

| Layer | Value proposition | Complexity | Notes (algorithm · accuracy · speed · FP risk · tunable knob) |
|-------|-------------------|------------|----------------------------------------------------------------|
| **L6 Cross-pubkey duplicate clustering** | Coordinated spam: many *distinct* pubkeys posting identical/near-identical text or identical tag shapes = botnet. Catches what single-account layers miss. | HIGH | **Algo:** corpus-wide. Stage 1 = exact: hash normalized content (xxhash) → bucket by hash; any bucket with many distinct authors is a coordinated blast (the spike confirmed exact-value collisions across pubkeys are the *cleanest* signal). Stage 2 = near: MinHash + LSH banding (tune (b,r) to a Jaccard threshold) or SimHash prefix-banding → O(n) candidate pairs instead of O(n²). Sub-score = size/strength of the cross-pubkey cluster a pubkey belongs to. **Accuracy:** very high precision for coordinated campaigns. **Speed:** O(n) with LSH; needs a corpus-wide pass (an aggregation phase, not pure per-pubkey streaming). **FP risk:** LOW–MED (viral reposts, client boilerplate). **Knob:** min cluster size, distinct-author count, Jaccard/Hamming threshold. **Dependency:** reuses L1 fingerprints. |
| **L7 Logistic-regression weight tuner** | Turns hand-set weights into a calibrated, correctable model: combined score = `sigmoid(Σ wᵢ·xᵢ + b)`. Re-fits `wᵢ,b` from human-labeled false positives ("backpropagation"). The core "correctability" promise. | MEDIUM | **Algo:** standardize sub-scores; fit logistic regression (gradient descent / IRLS) on labeled spam/ham pubkeys; class-weight or precision-oriented threshold for rare-spam imbalance. Start interpretable (hand weights), re-fit as labels accumulate. **Accuracy:** as good as the labels; calibrated probabilities. **Speed:** linear model — fitting and scoring both cheap; no LLM. **FP risk:** controlled *by* this layer (it minimizes labeled FPs). **Knob:** decision threshold, class weights, per-feature regularization. |
| **L8 Language/script anomaly & homoglyph abuse** | Charset abuse: mixed-script content, Unicode confusables/homoglyphs, zero-width/emoji stuffing — phishing & evasion tells. | MEDIUM | **Algo:** Unicode UTS#39 — `unicode-security`/`unicode_skeleton` for confusable skeleton + `mixed_script`; `unicode-script` for script detection; ratio of confusable/non-NFC/zero-width chars; emoji density; optional `whatlang` language consistency. **Accuracy:** moderate; strong for impersonation/phishing. **Speed:** O(len), table lookups. **FP risk:** HIGH alone — legit multilingual users mix scripts → **weak weighted feature only**, never a gate. **Knob:** confusable-ratio threshold, mixed-script flag weight. |

### Anti-Features (Tempting But Bad)

| Anti-feature | Why requested | Why problematic | Alternative |
|--------------|---------------|-----------------|-------------|
| **Any LLM / on-device model inference** | "An LLM would judge spam better." | Explicitly forbidden (PROJECT.md): too slow for 100M+ events; not the speed×accuracy point this engine occupies. | Algorithmic/statistical layers above + logistic combiner; defer semantic judgment to a separate future service if ever needed. |
| **Per-event spam verdicts as the deliverable** | "Tell me which posts are spam." | Out of scope — deliverable is a *pubkey* list. Per-event verdicts invite live-enforcement scope creep and a different data model. | Per-event signals are *inputs only*, aggregated to a pubkey score (this is the whole design). |
| **Whitelist as a hard exemption/gate** | "Whitelisted pubkeys are trusted, skip them." | Whitelist = "seen by the crawler," not "is good." Compromised/legit-then-spammy accounts exist; gating creates blind spots. | Whitelist is a *weighted* layer (L0); presence clears only that term, pubkey still runs all later layers. |
| **Hard per-layer cutoffs (auto-ban on one signal)** | "If IAT entropy < X, it's a bot." | Every single layer has real false positives (crawler timing artifacts, multilingual users, link-aggregators). Single-signal bans = false-positive disaster proven by the spam-clusters spike. | Weighted sum + tuned threshold (L7); require multi-signal agreement, mirroring the spike's "two-of-three intersection" precision lesson. |
| **Re-implementing structural follow-graph detection** | "The graph signals work well." | That's `spam-explorer`'s job; duplicating it wastes the disjoint-signal advantage and couples to Dgraph internals. | Consume content/behavior only; the whitelist (L0) is the *only* graph-derived input, and only as a binary prior. |
| **Buffering the whole corpus in memory** | "Easier to compute cross-pubkey stats." | 100M+ events won't fit; violates the streaming constraint. | Stream per-pubkey for L0–L5/L8; for L6 use streaming hash buckets / LSH (bounded memory), not an all-pairs in-memory join. |
| **Real-time / incremental scoring in v1** | "Score as events arrive." | v1 is a re-runnable batch (PROJECT.md); incremental adds complexity before the layer set is even validated. | Idempotent batch now; incremental service is a later milestone. |

---

## Feature Dependencies

```
L0 Whitelist ────────────────┐
L1 Near-dup (SimHash) ──┬─────┤
        └──reuses fingerprints──> L6 Cross-pubkey clustering
L2 Cadence ─────────────┤      │
L3 Entropy ─────────────┤      ├──feeds sub-scores──> L7 Logistic combiner/tuner ──> per-pubkey verdict
L4 Link/mention ────────┤      │                                  ^
L5 Tag/kind fingerprint ┘      │                                  │
L6 Cross-pubkey clustering ────┘                       human-labeled FPs (SQLite)
L8 Lang/script anomaly ──────────────────────────────────────────┘

L7 ──requires──> all layers emitting normalized [0,1] sub-scores
L6 ──requires──> L1 fingerprints + a corpus-wide aggregation pass
```

### Dependency Notes

- **L7 (combiner) requires every other layer** to emit a normalized `xᵢ ∈ [0,1]`. Define the sub-score contract first; it is the integration seam.
- **L6 (cross-pubkey) reuses L1 fingerprints** and needs a corpus-wide aggregation phase (hash/LSH buckets), so it is ordered *after* the per-pubkey streaming pass — it is not pure per-pubkey streaming.
- **L0 (whitelist) is independent** and the cheapest filter; compute it first to short-circuit nothing (never a gate) but to populate a prior early and cheaply.
- **L2 (cadence) enhances but must never stand alone** — combine with content layers, per the spam-clusters false-positive lesson.
- **L8 (lang/script) conflicts with naive language filtering** — must be weak/weighted to avoid penalizing legitimate multilingual users.

### Cheap-first filtering / layer ordering

Run cheapest, highest-signal layers first so expensive ones see a smaller stream:

1. **L0 whitelist** (O(1) lookup) and **L3 entropy / L4 link-mention / L5 tag-kind** (O(len)/O(tags), pure counters) — compute on the per-pubkey streaming pass, no extra fetch.
2. **L1 near-dup** (build SimHash once; cheap to compare) — also on the streaming pass; fingerprints are retained for L6.
3. **L2 cadence** (O(n log n) over ≤100 events) — streaming pass.
4. **L8 lang/script** (table lookups) — streaming pass, low weight.
5. **L6 cross-pubkey clustering** — separate corpus-wide aggregation phase after streaming (uses retained fingerprints + hash buckets).
6. **L7 logistic combiner** — final reduce over collected sub-scores; tuner runs offline against the SQLite labeled set.

> **Combining into a verdict.** `score = sigmoid(Σ wᵢ·xᵢ + b)`; flag pubkey if `score > τ`. Weights start hand-set (interpretable, conservative on L0/L2/L8), then L7 re-fits `wᵢ,b` by logistic regression on human-labeled spam/false-positive pubkeys stored in SQLite. Standardize features first; handle spam-rarity with class weighting or a precision-oriented `τ`. This keeps each layer independently tunable while giving a principled, correctable combiner — the "backpropagation from labels" requirement.

---

## MVP Definition

### Launch With (v1)

- [ ] **L0 Whitelist signal** — cheapest, required prior; defines the "weighted not gate" design.
- [ ] **L1 Within-pubkey near-duplicate ratio** — highest-precision single content signal; copy-paste blasting.
- [ ] **L3 Content entropy / templated-text** — cheap, broad coverage.
- [ ] **L4 Link & mention spam ratios** — cheap, catches promo/affiliate spam.
- [ ] **L7 Logistic combiner (hand-set weights to start)** — needed to produce a single verdict and to consume labels.

### Add After Validation (v1.x)

- [ ] **L2 Posting cadence / burst** — add once content layers anchor precision; trigger: enough labels to weight it without re-introducing crawler-artifact FPs.
- [ ] **L5 Tag/kind fingerprinting** — add when templated-client patterns show up in review.
- [ ] **L7 full re-fit from labels** — turn on weight re-optimization once a labeled FP set exists.

### Future Consideration (v2+)

- [ ] **L6 Cross-pubkey duplicate clustering** — highest value but needs the corpus-wide aggregation phase and bounded-memory LSH; defer until streaming pipeline is solid.
- [ ] **L8 Language/script anomaly & homoglyph abuse** — defer; weak signal, FP-prone, lowest marginal value first.

## Feature Prioritization Matrix

| Layer | User Value | Implementation Cost | Priority |
|-------|------------|---------------------|----------|
| L0 Whitelist | MEDIUM | LOW | P1 |
| L1 Near-duplicate (SimHash) | HIGH | MEDIUM | P1 |
| L3 Entropy / templated | MEDIUM | LOW | P1 |
| L4 Link/mention ratios | HIGH | LOW | P1 |
| L7 Logistic combiner | HIGH | MEDIUM | P1 |
| L2 Cadence / burst | MEDIUM | MEDIUM | P2 |
| L5 Tag/kind fingerprint | MEDIUM | LOW | P2 |
| L6 Cross-pubkey clustering | HIGH | HIGH | P3 |
| L8 Lang/script / homoglyph | LOW | MEDIUM | P3 |

**Priority key:** P1 must-have for launch · P2 add after validation · P3 future consideration.

## Competitor / Prior-Art Feature Analysis

| Signal family | `spam-explorer` (structural, Go) | spam-clusters spike (structural, Dgraph) | This engine (content/behavioral) |
|---------------|----------------------------------|------------------------------------------|----------------------------------|
| Trust/whitelist | seed-relative valid-follower count over follow graph | follower-count floors, sybil hubs | binary whitelist presence as a *weighted prior* (L0) |
| Coordination | weak-bridge pods, mutual-follow rings | **exact out-degree collisions = cleanest tell** | exact/near content + tag-shape collisions across pubkeys (L6) — same "exact repetition" principle, content domain |
| Timing | — | burst-created cohorts (mostly crawler FPs) | IAT entropy/CV/burst (L2) — *combined only*, heeding the FP lesson |
| Content | none (ID-only graph) | none (no content) | L1/L3/L4/L5/L8 — the disjoint half this engine uniquely owns |

## Sources

- Manku, Jain, Das Sarma — *Detecting Near-Duplicates for Web Crawling* (Google, WWW 2007): 64-bit SimHash, Hamming k=3. https://research.google.com/pubs/archive/33026.pdf
- Charikar — *Similarity Estimation Techniques from Rounding Algorithms* (SimHash, 2002).
- MinHash + LSH banding for document dedup: https://mattilyra.github.io/2017/05/23/document-deduplication-with-lsh.html · https://milvus.io/docs/minhash-lsh.md
- Gianvecchio et al. — *Measurement and Classification of Humans and Bots in Internet Chat* (USENIX Security 2008): IAT entropy. https://www.usenix.org/legacy/event/sec08/tech/full_papers/gianvecchio/gianvecchio_html/
- *DNA-influenced automated behavior detection via relative entropy* (Nature Scientific Reports 2022). https://www.nature.com/articles/s41598-022-11854-w
- Unicode UTS#39 Security Mechanisms (confusables, skeleton, mixed-script). https://www.unicode.org/reports/tr39/ · Rust `unicode_skeleton`: https://docs.rs/unicode_skeleton
- DeepFry prior art: `web-of-trust/.planning/spikes/spam-clusters/` (exact-collision coordination signal; timestamp-clustering false-positive lesson) and `spam-explorer/` (structural-only, disjoint signal source).
- LMDB2GraphQL contract v1.2 (`contract.md`): available fields (`content`, `kind`, `createdAt`, `tags`, `pubkey`), `authors`/`latestPerAuthor` enumeration, `createdAt` is author-claimed.

---
*Feature research for: algorithmic pubkey-level Nostr spam detection (content/behavioral layers)*
*Researched: 2026-06-25*
