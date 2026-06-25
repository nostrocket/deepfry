# Phase 4: Detection Layers + Logistic Combiner - Research

**Researched:** 2026-06-25
**Domain:** Rust per-pubkey content spam detection — a shared `Layer` trait, four P1 detection layers (L0 whitelist, L1 near-duplicate, L3 entropy, L4 link/mention), a logistic combiner (L7), TOML config, and deterministic SQLite persistence through the existing single-writer store.
**Confidence:** HIGH (codebase + whitelist API code-verified; crate versions verified via `cargo info`; algorithm cutoffs are conservative FP-averse defaults tagged `[ASSUMED]` per D-08, which explicitly delegates these to research discretion and makes every one a config value).

## Summary

This phase turns the Phase-3 no-op pipeline consumer into the real scoring stage. Each enumerated pubkey's `AuthorGroup` (its ≤100 most-recent kind-1 events) flows through a fixed, ordered set of `Layer`s; each layer emits a sub-score `xᵢ∈[0,1]` plus a structured JSON evidence blob; the L7 logistic combiner fuses the sub-scores with `sigmoid(Σwᵢxᵢ + b)` using hand-set weights read from the `weight` table; the resulting `score` row + per-layer `signal` rows persist through the existing single-writer actor. The integration is almost entirely additive: the `score`/`signal`/`fingerprint`/`weight` tables, the `WriteMsg::Score(Persist)` write path, the `SubScore` model, and the `consumer: Fn(&AuthorGroup)` seam in `run_pipeline` all already exist from Phases 1–3. Phase 4 adds the layers, the combiner, the config loader, the `weight`-table seed/read, and wires `match_groups` so zero-event pubkeys are scored (D-15).

The dominant project risk is false positives (REQUIREMENTS core value; D-08; "no hard single-layer cutoffs" anti-feature). Every cutoff below is chosen conservatively and is a config value, and the combiner's starting weights/bias are tuned so that **no single layer can push a pubkey over τ** — flagging requires multi-signal agreement. Determinism (OPS-02) is achievable with zero RNG: every layer is a pure deterministic function of its events, the layer reduction order is fixed (a `Vec<Box<dyn Layer>>` iterated in declaration order), and persistence already UPSERTs on `(run_id, pubkey)`.

**Primary recommendation:** Object-safe `dyn Layer` trait with a fixed-order `Vec<Box<dyn Layer>>` registry built once from config. Hand-roll SimHash (L1) and Shannon entropy (L3) over `unicode-segmentation` tokens hashed with the project's already-owned `xxhash-rust` (xxh3) — this gives bit-for-bit determinism that the `simhash` crate's default SipHash hasher does not guarantee across the persisted `fingerprint.simhash` column. Add only `unicode-segmentation`, `url`, and `toml` as Phase-4-owned deps. Use the whitelist's real `GET /check/{pubkey}` → `{"whitelisted":bool}` endpoint (no batch endpoint exists) behind a small TTL+LRU cache and a reused `reqwest::Client`.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01:** One shared **`Layer` trait**: each layer takes a pubkey's fetched events and emits a normalized sub-score **xᵢ∈[0,1]** plus structured **evidence** (why it fired). Every layer is **independently enable/disable-able** and carries a **tunable weight + threshold** read from config — no recompile to retune (OPS-03).
- **D-02:** Names are self-explanatory: the trait, layer structs, and config keys read as plain English (e.g. `whitelist_absence`, `near_duplicate`, `content_entropy`, `link_mention`).
- **D-03 (L0 whitelist):** Query the live whitelist; **absence emits a weighted spam sub-score, presence clears ONLY this layer** — never a gate or exemption. A whitelisted pubkey still flows through every other layer. Whitelist at `http://127.0.0.1:8081` (docker); look up batched/cached.
- **D-04 (L1 near-duplicate):** Within-pubkey near-duplicate detection over a pubkey's own events via **SimHash + Hamming distance** threshold; emits a sub-score from the repeated-content ratio. (The existing `fingerprint` table is available for content hashes.)
- **D-05 (L3 content entropy):** Flag **low-entropy templated text** and **high-entropy gibberish**, plus URL/emoji/hashtag density; emit a sub-score.
- **D-06 (L4 link & mention):** Score URL ratio, repeated domains, mass `p`-tag mentions, and hashtag stuffing; emit a sub-score.
- **D-07 (L7):** Fuse sub-scores into one per-pubkey score via weighted logistic combination **`sigmoid(Σwᵢxᵢ + b)`**, using **hand-set conservative starting weights + bias read from the `weight` table** (seeded by config defaults on first run). Phase 6's tuner later re-fits these from labels.
- **D-08:** All layer thresholds, the L0 whitelist-absence sub-score magnitude, the logistic starting weights + bias, and the flag threshold **τ** are set to **conservative, false-positive-averse defaults** chosen from per-phase research. FPs are the dominant risk — when in doubt, bias toward NOT flagging. Every value is a **config-file value**.
- **D-09 (config):** Config is **TOML at `~/deepfry/pubkey_iterator_config.toml`** (explicit user write-grant for THIS file only). Carries: adapter URL (`http://192.168.149.21:8080/graphql`), whitelist URL (`http://127.0.0.1:8081`), per-layer enable + weight + threshold, combiner bias, and τ. A committed in-repo default/example documents the shape. **Config-loading tests use a temp dir**, never the real file. Other files under `~/deepfry/` are off-limits.
- **D-10:** For every scored pubkey, persist the **`score` row** (run_id, pubkey, score, suspected) and the **per-layer `signal` rows** (EAV: run_id, pubkey, layer, value, evidence-JSON) through the single-writer store. Per-layer explanation lives in `signal.evidence` (SCORE-05).
- **D-11 (OPS-02):** Scoring is **deterministic** — seeded RNG, **fixed layer-sum order**, UPSERT on `(run_id, pubkey)`. Re-running the same snapshot with the same weights → identical verdicts.
- **D-12 (SCORE-04):** Verdicts are **pubkey-level only**; per-event signals are inputs, never the deliverable. **No enforcement** — output is a score in SQLite.
- **D-13:** **SQLite only.** No flat-file output anywhere in this phase.
- **D-14:** Layer unit tests use **synthetic event fixtures** (deterministic). L0 has an integration test against the **live whitelist** (`127.0.0.1:8081`); the combiner determinism test re-runs a fixture twice and asserts identical scores. Live services reachable automatically; a transient outage degrades to a deferred manual check, never a block.
- **D-15:** Resolve the Phase-3 `match_groups` carry-forward: **every enumerated pubkey gets a verdict, including zero-event pubkeys.** Wire `match_groups` against the requested pubkey list so omitted (zero-event) authors become empty groups that reach the Layer/combiner stage. Add a test proving a zero-event pubkey still gets a `score` row. Default decision is **score every pubkey** (alternative: delete unused `match_groups`).

### Claude's Discretion

- All starting parameter magnitudes (SimHash bit-width + Hamming cutoff, entropy low/high cutoffs, density ratios, L0 absence magnitude, logistic weights + bias, τ) — chosen conservatively here, all config values (D-08).
- Layer dispatch mechanism (`dyn` vs enum), crate-vs-hand-roll for SimHash/entropy, config crate choice, cache strategy for L0.

### Deferred Ideas (OUT OF SCOPE)

- **CLI `run`/`export`** — Phase 5.
- **Labeling / tuning / backtest** — Phase 6.
- **v2 layers:** L2 cadence/burst (DETECT-06), L5 tag/kind fingerprint (DETECT-07), L6 cross-pubkey clustering (DETECT-08, top v2 priority), L8 language/homoglyph (DETECT-09).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DETECT-01 | Whitelist layer: absence → weighted spam sub-score; presence clears only this layer | §L0 — real `GET /check/{pubkey}` API, conservative absence magnitude, TTL+LRU cache, reqwest reuse |
| DETECT-02 | Within-pubkey near-duplicate (SimHash + Hamming) → sub-score | §L1 — hand-rolled deterministic SimHash over xxh3 token shingles, Hamming clustering, repeated-content ratio → `[0,1]`, `fingerprint` table usage |
| DETECT-03 | Content entropy (low=templated, high=gibberish) + URL/emoji/hashtag density → sub-score | §L3 — Shannon entropy cutoffs, density ratios, normalization |
| DETECT-04 | Link & mention: URL ratio, repeated domains, mass p-tags, hashtag stuffing → sub-score | §L4 — `url` crate parsing, thresholds, normalization |
| DETECT-05 | Common `Layer` contract: `xᵢ∈[0,1]`, enable/disable, tunable weight/threshold | §Layer Trait Design — object-safe `dyn Layer`, config-driven registry |
| SCORE-01 | Combiner: `sigmoid(Σwᵢxᵢ + b)` from `weight` table | §L7 Combiner — fixed-order reduction, conservative weights/bias, multi-signal requirement |
| SCORE-04 | Pubkey-level only; no enforcement | §L7 / §Persistence — only `score`/`signal` rows written |
| SCORE-05 | Per-layer explanation (which fired, sub-score, evidence) persisted | §Layer Trait Design (evidence JSON) + §Persistence (`signal.evidence`) |
| OPS-02 | Deterministic: same snapshot+weights → identical verdicts | §Determinism — no RNG, fixed order, hand-rolled stable hashing, UPSERT |
| OPS-03 | Config without recompile | §Config TOML — `toml` + serde, `weight`-table seed/read |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Whitelist membership lookup | External service (whitelist-plugin HTTP at :8081) | This engine's L0 layer (reqwest client + cache) | The whitelist is owned by `whitelist-plugin`; this engine is a read-only HTTP consumer (D-03). It never decides membership, only consumes `{whitelisted:bool}`. |
| Content feature extraction (SimHash, entropy, ratios) | This engine — CPU analysis stage (rayon, per-pubkey) | — | Pure functions of an `AuthorGroup`'s events; embarrassingly parallel; STACK.md "rayon compute back". |
| Sub-score → fused score | This engine — L7 combiner (CPU stage) | `weight` table (SQLite) supplies weights/bias | Combination is pure arithmetic over the `Vec<SubScore>`; weights are persisted state read once at run start. |
| Persistence of score/signal/fingerprint | This engine — single-writer SQLite actor | — | Phase-1 invariant: all writes funnel through the one writer thread. |
| Config (URLs, weights, thresholds, τ) | Filesystem TOML at `~/deepfry/` | `weight` table (seeded from config on first run) | OPS-03: retune without recompile; weights live in SQLite so Phase 6's tuner can overwrite them (D-07). |

## Standard Stack

### Core (already in Cargo.toml — reused, NOT re-added)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `rusqlite` | 0.40 (bundled) | Persist score/signal/fingerprint; read `weight` | [VERIFIED: cargo info] existing store dep; single-writer batch fit. |
| `serde` + `serde_json` | 1.0 / 1.0.150 | Config structs; `signal.evidence` JSON; `run.config_json` | [VERIFIED: cargo info] evidence + config (de)serialization. |
| `reqwest` | 0.13 | L0 whitelist HTTP `GET /check/{pubkey}` | [VERIFIED: cargo info] already the GraphQL transport; reuse one `Client`. |
| `tokio` | 1 | L0 async lookups inside the fetch stage / a small async pass | [VERIFIED: cargo info] existing runtime. |
| `rayon` | 1.12 | Per-pubkey layer evaluation in the CPU consumer stage | [VERIFIED: cargo info] Phase-3-owned; the layer fan-out runs here. |
| `flume` | 0.12 | analyze → writer channel (existing) | [VERIFIED: cargo info] single-writer seam. |
| `thiserror` | 2 | Layer/combiner/config error taxonomy | [VERIFIED: cargo info] existing convention. |

### Phase-4-owned additions

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `unicode-segmentation` | 1.13.3 | Grapheme/word tokenization for SimHash shingles + entropy + emoji counting over multilingual Nostr content | [VERIFIED: crates.io] 7.5M weekly downloads, unicode-rs official; STACK.md pick. |
| `url` | 2.5.8 | Parse URLs out of content for L4 (host extraction → repeated-domain ratio) and L3 URL density | [VERIFIED: crates.io] 11.8M weekly downloads, servo official; WHATWG-correct host parsing beats a regex. |
| `toml` | 1.1.2 | Parse `pubkey_iterator_config.toml` into serde structs (OPS-03) | [VERIFIED: crates.io] 12.3M weekly downloads, toml-rs official. |

**Not added (deliberate):**

- **`simhash` crate (0.3)** — `[VERIFIED: cargo info]` legitimate (49.9k weekly downloads), BUT its default `hasher-sip` feature uses SipHash whose hash of a given string is stable within a build yet the crate's API hashes tokens with a hasher the persisted 64-bit value depends on. For OPS-02 we must store `fingerprint.simhash` and re-derive it bit-identically across runs/builds. **Recommendation: hand-roll a 64-bit SimHash** (≈30 lines) over xxh3-hashed shingles using the already-owned `xxhash-rust` xxh3 (portable, seed-fixed, stable across runs/platforms — STACK.md §xxhash rationale). This removes a dependency, guarantees determinism on the persisted column, and the algorithm is trivial. If the planner prefers the crate, it MUST pin `default-features=false, features=["hasher-fnv"]` (FNV is a fixed deterministic hash) and add a determinism test over the persisted value. `[ASSUMED]` (determinism property of the crate's SipHash path not exhaustively verified this session — hand-rolling sidesteps the question).
- **`gaoya` (MinHash+LSH)** — STACK.md reserves it for L6 cross-pubkey clustering (DETECT-08, v2). L1 is *within-pubkey* near-dup over ≤100 events — O(n²) Hamming over 64-bit SimHashes is ~5000 comparisons max, trivially fast and fully deterministic. Do NOT add gaoya in Phase 4.
- **`xxhash-rust`** — STACK.md lists it; it is **not yet in Cargo.toml**. If the hand-rolled SimHash uses xxh3, add `xxhash-rust = { version = "0.8.15", features = ["xxh3"] }` `[VERIFIED: crates.io]` (1.5M weekly downloads). Alternative with zero new deps: shingle-hash with `std::hash::Hasher` via a **fixed-seed** `ahash`? No — `ahash` randomizes by default. Cleanest deterministic zero-dep option: FNV-1a hand-rolled (8 lines). **Recommendation: hand-rolled FNV-1a for shingle hashing** to avoid even the xxhash dep, since shingle count per pubkey is tiny and FNV-1a is deterministic-by-construction. Planner's call; both are fine.
- **`emojis` (0.9)** — `[VERIFIED: cargo info]` exists, but emoji *density* (D-05) only needs counting, not lookup. `unicode-segmentation` graphemes + a Unicode emoji range check (or counting graphemes whose codepoints fall in emoji blocks) suffices. Avoid the dep.

**Installation:**
```toml
# Cargo.toml — Phase-4 additions only
unicode-segmentation = "1.13"
url                  = "2.5"
toml                 = "1.1"
# OPTIONAL (only if not hand-rolling shingle hashing):
# xxhash-rust = { version = "0.8", features = ["xxh3"] }
```

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hand-rolled SimHash | `simhash` 0.3 crate | Crate saves ~30 lines but introduces a hasher-determinism question on a *persisted* column; hand-roll is safer for OPS-02. |
| `toml` crate | `figment` 0.10 (STACK.md glue pick) | `figment` layers file+env+CLI — overkill for Phase 4 (one TOML file, no CLI yet). Plain `toml` + serde is leaner. Phase 5 (CLI) may upgrade to figment. `[ASSUMED]` figment deferral. |
| Hand-rolled FNV-1a shingle hash | `xxhash-rust` xxh3 | xxh3 is faster on large slices; shingles are ≤8 tokens so speed is irrelevant. Hand-roll = zero deps. |
| O(n²) Hamming within pubkey | `gaoya` LSH | LSH wins at corpus scale (L6/v2); within ≤100 events O(n²) is faster and deterministic with no index state. |

## Package Legitimacy Audit

Run via `gsd-tools query package-legitimacy check --ecosystem crates …` on 2026-06-25.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `unicode-segmentation` | crates.io | since 2015-04 | 7.57M/wk | github.com/unicode-rs/unicode-segmentation | OK | Approved |
| `url` | crates.io | since 2014-11 | 11.85M/wk | github.com/servo/rust-url | OK | Approved |
| `toml` | crates.io | since 2014-11 | 12.37M/wk | github.com/toml-rs/toml | OK | Approved |
| `xxhash-rust` (optional) | crates.io | since 2020-10 | 1.56M/wk | github.com/DoumanAsh/xxhash-rust | OK | Approved (optional) |
| `simhash` (rejected) | crates.io | since 2018-08 | 49.9k/wk | github.com/bartolsthoorn/simhash-rs | OK | NOT USED (determinism; hand-roll) |
| `gaoya` (deferred v2) | crates.io | since 2022-02 | 3.13k/wk | github.com/serega/gaoya | OK | NOT USED in P4 (L6/v2 only) |
| `ahash` | crates.io | since 2019-02 | 9.51M/wk | github.com/tkaitchuck/ahash | OK | NOT USED (randomized — breaks determinism) |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none. Crates with no postinstall concept (`postinstall: null`) — crates.io has no install-time script execution like npm, so the postinstall vector does not apply.

## Architecture Patterns

### System Architecture Diagram

```
 [pubkey table] ──read_pubkeys()──▶ Vec<String> (all enumerated pubkeys, incl. zero-event)
        │
        ▼ chunk(authors_per_call=250)
 ┌────────────────────────── tokio fetch stage ──────────────────────────┐
 │  for batch:                                                            │
 │    fetch_batch(client, kind=1, per_author, batch)  ──▶ Vec<AuthorGroup>│
 │    match_groups(batch, groups)  ◀── D-15 WIRE HERE                     │
 │      └▶ one (pubkey, Vec<Event>) per REQUESTED pubkey                  │
 │         (zero-event authors → empty Vec, never dropped)                │
 │    rebuild AuthorGroup{author, events} per requested pubkey            │
 │    tx.send_async(group).await   ── back-pressure on bounded channel ── │
 └────────────────────────────────────────────────────────────────────────┘
        │  bounded flume channel (cap=64)
        ▼
 ┌──────────────── consumer std::thread (OFF tokio) — the Phase-4 seam ───┐
 │  while recv(group):                                                     │
 │    ScoringStage.score(&group):                                          │
 │      ┌── fixed-order Vec<Box<dyn Layer>> ──┐                            │
 │      │  L0 whitelist_absence  ─▶ x0, ev0    │  (each layer: pure fn of  │
 │      │  L1 near_duplicate     ─▶ x1, ev1    │   the group's events,      │
 │      │  L3 content_entropy    ─▶ x3, ev3    │   xᵢ∈[0,1] + evidence JSON)│
 │      │  L4 link_mention       ─▶ x4, ev4    │                            │
 │      └────────────────────────────────────┘                            │
 │      L7 combiner: z = b + Σ wᵢ·xᵢ  (FIXED iteration order)              │
 │                   score = 1/(1+e^-z);  suspected = score > τ            │
 │      build Persist{ run_id, pubkey, score, whitelisted, suspected,      │
 │                     subscores: [SubScore{layer,value,evidence}, …] }    │
 │      store.persist(persist)   ──▶ WriteMsg::Score                       │
 │      (optional) store fingerprint rows for L1 simhashes                 │
 └─────────────────────────────────────────────────────────────────────────┘
        │  flume unbounded writer channel (existing)
        ▼
 [single writer actor] ── batched txn ── UPSERT score / signal / pubkey / fingerprint
```

The L0 whitelist lookup is I/O. Two valid placements (planner decides):
- **(A) In the fetch stage (recommended):** do the whitelist `GET` async alongside `fetch_batch` (both are tokio I/O), attach the boolean to the group before it crosses the channel. Keeps the rayon consumer pure-CPU per STACK.md's "never block rayon on network I/O".
- **(B) In a pre-pass:** resolve whitelist membership for the whole batch before scoring. Same effect; (A) overlaps it with event fetching.

### Recommended Project Structure
```
src/
├── detect/
│   ├── mod.rs            # Layer trait, LayerOutput, ScoringStage registry + combiner
│   ├── whitelist.rs      # L0 WhitelistAbsenceLayer + WhitelistClient (reqwest + cache)
│   ├── near_duplicate.rs # L1 — hand-rolled SimHash + Hamming + repeated-content ratio
│   ├── content_entropy.rs# L3 — Shannon entropy + URL/emoji/hashtag density
│   ├── link_mention.rs   # L4 — URL ratio, repeated domains, p-tag/hashtag stuffing
│   └── combiner.rs       # L7 sigmoid(Σwᵢxᵢ+b); reads weights from `weight` table
├── config.rs             # serde TOML config; ~/deepfry path resolution; weight-table seed
└── (existing: store/, graphql/, fetch.rs, pipeline.rs, enumerate.rs, model.rs)
```

### Pattern 1: Object-safe `Layer` trait + fixed-order registry (DETECT-05)
**What:** A single object-safe trait; layers held in a `Vec<Box<dyn Layer>>` built once from config in a fixed declaration order. `dyn` dispatch is chosen over an enum because the iteration is over a small fixed set evaluated once per pubkey (≤100 events) — virtual-call overhead is negligible vs. the entropy/SimHash compute, and `dyn` keeps each layer in its own file with a clean `enabled()`/`name()` contract (D-01/D-02). Enum dispatch would centralize a match arm per layer and couple the combiner to every layer's type — worse for the "new layer = new file, no migration" ethos.

**When to use:** Always for the layer set in this phase and all future layers.

**Example:**
```rust
// src/detect/mod.rs
/// One detection layer's output: a normalized sub-score plus a structured
/// explanation serialized into `signal.evidence` (SCORE-05).
pub struct LayerOutput {
    pub value: f64,                 // xᵢ ∈ [0,1] — caller debug_asserts the bound
    pub evidence: serde_json::Value // per-layer JSON; serialized to signal.evidence
}

/// The shared Layer contract (D-01/DETECT-05). Object-safe: no generics, no
/// associated types, &self only — so `Box<dyn Layer>` works.
pub trait Layer: Send + Sync {
    /// Stable EAV layer name persisted into `signal.layer` (D-02). NEVER renamed
    /// once shipped — it is the signal table's contract key.
    fn name(&self) -> &'static str;        // e.g. "L0_whitelist_absence"
    /// Pure, deterministic function of this pubkey's events. `whitelisted` is the
    /// pre-resolved L0 membership (so layers stay CPU-only; see diagram note A).
    fn score(&self, events: &[Event], whitelisted: bool) -> LayerOutput;
}
```

### Pattern 2: ScoringStage — deterministic reduction (SCORE-01 / OPS-02)
```rust
// src/detect/mod.rs
pub struct ScoringStage {
    layers: Vec<Box<dyn Layer>>,   // FIXED order = config/declaration order
    weights: Vec<f64>,             // weights[i] pairs with layers[i] (by name lookup)
    bias: f64,
    tau: f64,
}

impl ScoringStage {
    /// Returns the Persist payload for one pubkey. No RNG, no HashMap iteration in
    /// the hot path — `layers` is a Vec iterated in index order (OPS-02 fixed order).
    pub fn score(&self, run_id: i64, pubkey: &str, events: &[Event], whitelisted: bool) -> Persist {
        let mut z = self.bias;
        let mut subscores = Vec::with_capacity(self.layers.len());
        for (i, layer) in self.layers.iter().enumerate() {
            let out = layer.score(events, whitelisted);
            debug_assert!((0.0..=1.0).contains(&out.value));
            z += self.weights[i] * out.value;
            subscores.push(SubScore {
                layer: layer.name().to_string(),
                value: out.value,
                evidence: Some(out.evidence.to_string()),
            });
        }
        let score = 1.0 / (1.0 + (-z).exp());     // sigmoid
        Persist {
            run_id, pubkey: pubkey.to_string(), score,
            whitelisted, suspected: score > self.tau, subscores,
        }
    }
}
```

### Anti-Patterns to Avoid
- **HashMap iteration in the reduction.** Iterating a `HashMap<layer, weight>` gives non-deterministic order → non-deterministic floating-point sum. Use parallel `Vec`s indexed positionally (writer.rs already documents "iterate the Vec as-is → deterministic").
- **Any RNG.** D-11 says "seeded RNG" but no layer here needs randomness. The honest implementation uses **zero RNG**; the determinism guarantee is then trivially met. Do not introduce RNG to satisfy the wording.
- **Hard single-layer cutoff.** Forbidden anti-feature (REQUIREMENTS "Out of Scope"). Never let one layer's `value=1.0` alone exceed τ — verify via the weight-budget check in §L7.
- **Blocking the rayon/consumer thread on the whitelist HTTP call.** Resolve membership in the tokio stage (diagram note A), pass the bool in.
- **`format!`-ing values into SQL.** Existing T-01-01 mitigation — keep using `params![]` (the writer already does).
- **Reusing layer names with a different meaning.** `signal.layer` is a stable contract key; pick final names now (see §Layer naming).

### Layer naming (D-02, stable `signal.layer` keys)

| Layer | `name()` / config key | Note |
|-------|----------------------|------|
| L0 | `L0_whitelist_absence` | absence emits the sub-score |
| L1 | `L1_near_duplicate` | (existing test uses `L1_near_dup` — pick ONE and keep it; recommend `L1_near_duplicate` per D-02 plain-English) |
| L3 | `L3_content_entropy` | |
| L4 | `L4_link_mention` | |

> Note: the Phase-1 store tests use placeholder names `L1_near_dup` / `L3_velocity`. Those are *test fixtures*, not the contract — Phase 4 picks the real names. Keep config keys and `name()` identical.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| URL extraction / host parsing | Regex URL matcher | `url` crate `Url::parse` + `.host_str()` | WHATWG-correct; punycode/IDN, ports, userinfo handled; regex misses edge cases that matter for repeated-domain detection. |
| Grapheme/word tokenization | Byte/char splitting | `unicode-segmentation` `.unicode_words()` / `.graphemes(true)` | Multilingual + emoji-heavy Nostr content; naive `split_whitespace` breaks CJK and miscounts emoji graphemes. |
| TOML parse | Hand parser | `toml` + `serde` derive | Standard, typed, error messages. |
| Sigmoid / logistic | external ML crate | `1.0/(1.0+(-z).exp())` (std) | One line; `linfa-logistic` is Phase 6's *training* dep, not needed to *evaluate* a fixed linear model. |
| HTTP client + pooling | raw TCP | reuse the existing `reqwest::Client` | Connection pooling, timeouts already configured. |

**DO hand-roll (deliberately):** 64-bit SimHash and Shannon entropy. Both are ~10–30 lines, and hand-rolling guarantees the bit-for-bit determinism OPS-02 needs on the persisted `fingerprint.simhash` column — a property an external hasher does not advertise. This is the rare case where hand-rolling is the *more* defensible choice (determinism > convenience).

## Layer Trait Design (DETECT-05) — recommendation

- **Dispatch:** object-safe `dyn Layer` in a `Vec<Box<dyn Layer>>`. **Recommended over enum dispatch.** Per-pubkey cost is dominated by entropy + SimHash compute; vtable calls are noise. `dyn` gives one-file-per-layer isolation and a clean `enabled` gate.
- **Enable/disable + weight/threshold:** the `ScoringStage` is built from config. A disabled layer is simply **omitted from the `layers` Vec** at build time (so it contributes nothing to the sum and writes no `signal` row) — cleaner than evaluating-then-zeroing, and deterministic. Per-layer `threshold` is passed into each layer's constructor from config (each layer interprets its own threshold; the combiner only needs weights + bias + τ).
- **Composition order:** fixed by the order layers are pushed in `ScoringStage::from_config` (L0, L1, L3, L4). This order, plus positional `weights`, is the OPS-02 fixed-sum-order guarantee.
- **Evidence:** each layer returns `serde_json::Value`; `ScoringStage` serializes it to the `signal.evidence` TEXT column (SCORE-05). Keep evidence small and explanatory (the offending values, not the raw content).

## L0 Whitelist (DETECT-01) — verified API

**The whitelist-plugin HTTP API (code-verified in `whitelist-plugin/pkg/server/server.go` and `pkg/client/client.go`):**

- **Endpoint:** `GET /check/{pubkey}` where `{pubkey}` is the 64-char lowercase hex in the path. `[VERIFIED: whitelist-plugin/pkg/server/server.go:45]` (`mux.HandleFunc("GET /check/{pubkey}", s.handleCheck)`).
- **Request:** plain `GET`, no body, no auth. The client form is `GET {serverURL}/check/{pubkey}` `[VERIFIED: whitelist-plugin/pkg/client/client.go:67]`.
- **Response (200):** `{"whitelisted": true|false}` — `Content-Type: application/json`, single boolean field `whitelisted` `[VERIFIED: server.go:77-79,102]` (`type checkResponse struct { Whitelisted bool \`json:"whitelisted"\` }`).
- **Errors:** `400` "missing pubkey" if empty; `503` from `/health` while loading. `/check` itself returns 200 with `whitelisted:false` for unknown pubkeys (the in-memory map lookup never errors) `[VERIFIED: server.go:86-103]`.
- **There is NO batch endpoint.** The API is strictly one-pubkey-per-`GET`. Other routes: `GET /health` (`{"status":"ok"}` or 503 `{"status":"loading"}`), `GET /stats`, `GET /version` `[VERIFIED: server.go:43-50]`. **D-03's "batched/cached" must be read as client-side batching/caching, not a server batch call.**

**Recommended L0 implementation:**
- Reuse a single `reqwest::Client` (connection pooling). Issue `GET {whitelist_url}/check/{pubkey}` per pubkey; parse `{whitelisted:bool}` with serde.
- **Cache:** a per-run in-memory `HashMap<String, bool>` is sufficient and deterministic — within one run a pubkey is scored once, but a small cache guards against any re-lookup and lets a `/health` gate run once. The reference Go client uses an 8192-entry LRU with a 30s TTL `[VERIFIED: client.go:11-18]`; for a single deterministic batch run, a **plain `HashMap` keyed by pubkey (no TTL)** is preferable — TTL would make results time-dependent and threaten OPS-02 within a long run. `[ASSUMED]` (no-TTL choice; rationale: determinism).
- **Concurrency:** resolve membership in the tokio fetch stage with a bounded `Semaphore` (e.g. 32–64 in-flight) so L0 lookups overlap event fetching and never block the rayon consumer.
- **Failure handling (D-14):** if the whitelist is unreachable, the conservative + FP-averse choice is to treat the pubkey as **whitelisted = unknown → emit the absence sub-score? NO.** Treating an unreachable whitelist as "absent" would inflate spam scores for every pubkey during an outage (mass FP). **Recommendation: on whitelist transport failure, set the L0 sub-score to 0.0 (as if cleared) and record `evidence:{"whitelist":"unreachable"}`** — i.e. fail toward NOT flagging, matching the FP-averse mandate. A run executed during a whitelist outage is flagged in `evidence` for a deferred manual re-check (D-14 degrade-gracefully). `[ASSUMED]` — surface to user; this is a policy decision.

**L0 sub-score on absence (conservative default, D-08):**
- `whitelisted == true` → `x_L0 = 0.0` (clears only this layer).
- `whitelisted == false` → `x_L0 = 1.0` (the layer "fires"), but with a **small weight** so absence alone cannot flag (see §L7). Most pubkeys in the corpus are not whitelisted, so a non-whitelisted pubkey is the common case — the weight must reflect that absence is weak evidence. `evidence:{"whitelisted":false}`.

## L1 Within-pubkey near-duplicate (DETECT-02)

**Algorithm (hand-rolled, deterministic):**
1. For each event, normalize content (trim, lowercase, collapse internal whitespace) — normalization must be fixed and documented for determinism.
2. Tokenize into word **shingles** (e.g. 3-word shingles via `unicode-segmentation::unicode_words`). For very short posts (<3 words) use the whole post as one shingle.
3. Hash each shingle with a **deterministic** hash (hand-rolled FNV-1a, or xxh3 if `xxhash-rust` added) → fold into a 64-bit SimHash: for each of 64 bit positions, +1 if the shingle-hash bit is set else −1; final bit = sign of the accumulator. Store per-event `simhash` (i64 bit-reinterpret) + `content_hash` (exact-dup xxh3/FNV of normalized content) in the `fingerprint` table.
4. Cluster the event SimHashes by **Hamming distance ≤ H**. Repeated-content ratio = `(events_in_near_dup_clusters) / total_events`, where a "near-dup cluster" is any group of ≥2 events within Hamming H of each other (union-find or O(n²) pairwise — ≤100 events).
5. Sub-score = repeated-content ratio, clamped to `[0,1]`. (Optionally weight exact-duplicate clusters higher than near-dup; keep v1 simple = ratio.)

**Conservative defaults (D-08, all config values) — `[ASSUMED]`:**

| Param | Default | Rationale |
|-------|---------|-----------|
| SimHash bit-width | 64 | Standard; matches `fingerprint.simhash` INTEGER column. |
| Shingle size | 3 words | Balances sensitivity; 1-word over-matches common phrases. |
| Hamming threshold `H` | **3** of 64 bits | Conservative — requires very high similarity (~95%+ token overlap) to count as near-dup, minimizing FP on coincidentally-similar short posts. |
| Min cluster size | 2 events | A pubkey must repeat itself at least once. |
| Min events to score | **5** | A pubkey with <5 events has too little signal; below this, `x_L1 = 0.0` (FP-averse). |

**`[0,1]` normalization:** sub-score = repeated-content ratio directly (already in `[0,1]`). A pubkey posting 100 identical events → ratio ≈ 1.0; a pubkey with all-distinct events → 0.0.

**`fingerprint` table usage:** insert one row per event `(run_id, pubkey, content_hash, simhash)` via a new additive `WriteMsg` variant (see §Persistence). The Phase-1 schema's `idx_fp_chash` index on `(run_id, content_hash)` supports the later L6 cross-pubkey pass (v2) — Phase 4 only writes, does not query cross-pubkey.

## L3 Content entropy (DETECT-03)

**Two-sided entropy + density. Sub-score = max of the component flags (FP-averse: take the strongest single signal but each component is itself bounded and conservative).**

1. **Shannon entropy** over the character distribution of concatenated normalized content: `H = -Σ p(c) log2 p(c)` bits/char. Hand-rolled (~10 lines over a `HashMap<char,u64>` count — but read out deterministically; the entropy value is order-independent so the HashMap is safe *here* as long as we don't iterate it for the score, only sum counts).
   - **Low-entropy (templated):** `H < 2.0` bits/char over content with ≥N chars → templated/repeated. Default low cutoff **2.0**, only applied when total content length ≥ **200 chars** (short posts are naturally low-entropy — don't penalize "gm"). `[ASSUMED]`
   - **High-entropy (gibberish):** `H > 5.5` bits/char (random base64/hash-like dumps). English text ≈ 4.0–4.5 bits/char; >5.5 indicates near-random. Default high cutoff **5.5**. `[ASSUMED]`
   - Entropy component sub-score: `0.0` inside `[2.0, 5.5]`; ramps to `1.0` as it exits the band (linear, clamped). Both edges conservative.
2. **Density features (per D-05):** over tokenized content —
   - URL density = URLs / words; emoji density = emoji-graphemes / graphemes; hashtag density = `#tags` / words.
   - Each maps to `[0,1]` via a ratio with a conservative knee (e.g. emoji density >0.5 → strong; URL density >0.5 → strong). Defaults below.

**Conservative cutoffs (config, `[ASSUMED]`):**

| Feature | Knee (→ contributes 1.0) | Below knee |
|---------|--------------------------|------------|
| Low entropy | H ≤ 2.0 (len ≥ 200) | linear ramp to 0 at H=3.0 |
| High entropy | H ≥ 5.5 | linear ramp to 0 at H=5.0 |
| Emoji density | ≥ 0.50 | linear to 0 |
| Hashtag density | ≥ 0.30 | linear to 0 |
| URL density | covered primarily by L4; L3 may include a light term or defer to L4 |

**Sub-score = max(entropy_component, emoji_density_component, hashtag_density_component)**, clamped `[0,1]`. `evidence:{"entropy_bits":4.2,"emoji_density":0.1,"hashtag_density":0.05}`. Use `max` (not sum) so L3 stays ≤1 and a single mild feature doesn't stack into a flag.

## L4 Link & mention (DETECT-04)

**Features (use `url` crate for parsing; tags come from `Event.tags`, where `tags[i][0]` is the tag name):**
1. **URL ratio** = events-containing-≥1-URL / total events.
2. **Repeated-domain concentration** = (max events sharing one host) / events-with-URLs. Parse host via `Url::parse(token).ok()?.host_str()`. A pubkey spamming the same domain repeatedly is the signal.
3. **Mass p-tag mentions** = mean `p`-tag count per event (mention-spam / tag-listing). Knee at a high per-event count.
4. **Hashtag stuffing** = mean `t`-tag count per event (Nostr hashtags are `["t", "..."]` tags) and/or inline `#` density (overlaps L3 — keep tag-based here).

**Conservative cutoffs (config, `[ASSUMED]`):**

| Feature | Knee (→ 1.0) | Note |
|---------|--------------|------|
| URL ratio | ≥ 0.80 | most posts are links |
| Repeated-domain concentration | ≥ 0.70 | same host dominates |
| Mean p-tags/event | ≥ 10 | mass-mention |
| Mean t-tags/event | ≥ 8 | hashtag stuffing |
| Min events to score | 5 | else 0.0 (FP-averse) |

**`[0,1]` normalization:** each feature → component in `[0,1]` via `min(value/knee, 1.0)`; **sub-score = max of components** (same FP-averse rationale as L3). `evidence:{"url_ratio":0.9,"top_domain":"x.com","top_domain_share":0.8,"mean_p_tags":12.0}`.

## L7 Logistic combiner (SCORE-01) — conservative weights requiring multi-signal agreement

**`z = b + Σ wᵢ·xᵢ`, `score = sigmoid(z)`, `suspected = score > τ`.** Read `wᵢ` per layer + `_bias` + `_threshold(τ)` from the `weight` table; seed from config on first run.

**Design constraint (multi-signal agreement, the anti-feature guard):** choose weights + bias + τ so that **no single layer firing at `x=1.0` (all others 0) produces `score > τ`**, but **two or more strong layers do**. Concretely, with the conservative starting set below:

| Layer | Starting weight `wᵢ` | Single-layer score @ x=1 (others 0) |
|-------|----------------------|--------------------------------------|
| L0 whitelist_absence | **0.8** | sigmoid(−4.0 + 0.8) = sigmoid(−3.2) ≈ 0.039 |
| L1 near_duplicate | **2.0** | sigmoid(−4.0 + 2.0) = sigmoid(−2.0) ≈ 0.119 |
| L3 content_entropy | **1.5** | sigmoid(−2.5) ≈ 0.076 |
| L4 link_mention | **1.5** | sigmoid(−2.5) ≈ 0.076 |
| bias `b` | **−4.0** | baseline sigmoid(−4.0) ≈ 0.018 |
| τ (`_threshold`) | **0.5** | flag cutoff |

- **Single strongest layer (L1 @ 1.0)** → 0.119 < τ=0.5 → **not flagged**. ✓ no single-layer cutoff.
- **L0 absent + L1 dup (both 1.0)** → sigmoid(−4.0+0.8+2.0)=sigmoid(−1.2)≈0.231 → still not flagged (absence is weak). ✓
- **L1 + L3 (both 1.0)** → sigmoid(−4.0+2.0+1.5)=sigmoid(−0.5)≈0.378 → not flagged (conservative). 
- **L1 + L3 + L4 (all 1.0)** → sigmoid(−4.0+2.0+1.5+1.5)=sigmoid(1.0)≈0.731 → **flagged**. ✓ multi-signal agreement.
- **L1 + L4 + L0-absent** → sigmoid(−4.0+2.0+1.5+0.8)=sigmoid(0.3)≈0.574 → flagged.

This realizes D-08 / "no hard single-layer cutoffs": strong content evidence from ≥2 independent layers is required; whitelist-absence only nudges. **All six values are `weight`-table rows, retunable, and overwritten by Phase 6's tuner.** `[ASSUMED]` magnitudes — these are FP-averse starting points, not fitted; they satisfy the structural multi-signal property which is the load-bearing requirement.

**`weight` table mapping:** rows keyed by layer name (`L0_whitelist_absence`→0.8, …), plus sentinel rows `_bias`→(weight=−4.0) and `_threshold`→(weight=0.5 or use the `threshold` column). The schema comment says `layer` PK is "layer name, or `_bias`/`_threshold`" — follow that. On first run (table empty for these keys) seed from config; thereafter read what's stored (so a Phase-6 retune persists).

**Determinism (OPS-02):** sigmoid is a pure function; the sum iterates the fixed-order `layers`/`weights` Vecs; UPSERT on `(run_id,pubkey)` (existing `UPSERT_SCORE`). No RNG. Re-running the same `AuthorGroup`s with the same `weight` rows yields byte-identical `score`/`signal` rows.

## Config TOML (OPS-03)

**Crate:** `toml` 1.1 + `serde` derive. Path: `~/deepfry/pubkey_iterator_config.toml` (resolve `$HOME` via `std::env::var("HOME")` or the `dirs` pattern — but D-09 grants write only to this one file; tests use a `tempfile::TempDir` path passed in). **Config loading must take a path argument** so tests never touch the real file (repo rule + D-09).

**Recommended shape:**
```toml
adapter_url   = "http://192.168.149.21:8080/graphql"
whitelist_url = "http://127.0.0.1:8081"
tau           = 0.5
bias          = -4.0

[layers.L0_whitelist_absence]
enabled   = true
weight    = 0.8
absence_subscore = 1.0      # x emitted when NOT whitelisted

[layers.L1_near_duplicate]
enabled        = true
weight         = 2.0
hamming_threshold = 3
shingle_size   = 3
min_events     = 5

[layers.L3_content_entropy]
enabled            = true
weight             = 1.5
entropy_low        = 2.0
entropy_high       = 5.5
min_len_for_low    = 200
emoji_density_knee = 0.5
hashtag_density_knee = 0.3

[layers.L4_link_mention]
enabled              = true
weight               = 1.5
url_ratio_knee       = 0.8
domain_concentration_knee = 0.7
mean_ptags_knee      = 10.0
mean_ttags_knee      = 8.0
min_events           = 5
```
Commit a `pubkey_iterator_config.example.toml` (or `config.example.toml`) in-repo documenting this shape (D-09). The `run.config_json` column already stores the weight snapshot per run (existing `begin_run(config_json)`), so serialize the effective config there for reproducibility (TUNE-03 forward-compat).

## Runtime State Inventory

> Phase 4 is greenfield code (new layers/combiner/config), not a rename/refactor. No stored-state migration. The one carry-forward is the `match_groups` wiring (D-15), which is a *code* change, not a data migration:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | `weight` table: empty on existing DBs → seed from config on first Phase-4 run. `fingerprint` table: unused until L1 writes. | Code: seed `weight`; write `fingerprint` rows. No migration. |
| Live service config | Whitelist server at :8081 (external, read-only consumer). | None — consume its existing `/check` API. |
| OS-registered state | None. | None. |
| Secrets/env vars | `LMDB2GRAPHQL_URL` (existing override). Config adds `whitelist_url` (loopback, no secret). | None. |
| Build artifacts | New deps (`unicode-segmentation`, `url`, `toml`) → `Cargo.lock` updates. | `cargo build` regenerates lock. |
| Phase-3 carry-forward (D-15) | `fetch::match_groups` exists but is NOT wired into `fetch_batch`/pipeline. | **Code:** wire `match_groups(requested, groups)` into the fetch stage so every requested pubkey (incl. zero-event) reaches the consumer as an `AuthorGroup` (possibly empty). Add the zero-event-gets-a-score test. |

## Common Pitfalls

### Pitfall 1: Non-deterministic SimHash from a randomized hasher
**What goes wrong:** Using `ahash` (randomized per-process seed) or relying on the `simhash` crate's SipHash without pinning a fixed hasher produces a different `fingerprint.simhash` value each run → OPS-02 determinism test fails, and persisted fingerprints are not comparable across runs (breaks the future L6 cross-pubkey pass).
**Why:** ahash seeds from a random source at startup; SipHash keys can vary.
**How to avoid:** Hand-roll SimHash over FNV-1a (or xxh3, both deterministic-by-construction). Add a determinism unit test that hashes the same content twice and asserts equal i64.
**Warning signs:** determinism test flaky; `fingerprint.simhash` differs across re-runs.

### Pitfall 2: Whitelist "unreachable == absent" inflates spam scores
**What goes wrong:** If a whitelist transport error is mapped to "not whitelisted", an outage makes *every* pubkey emit the absence sub-score → mass false positives.
**Why:** conflating "lookup failed" with "confirmed absent".
**How to avoid:** On whitelist error, emit `x_L0 = 0.0` (fail toward not-flagging, FP-averse) and record `evidence:{"whitelist":"unreachable"}` for deferred manual review (D-14). Distinguish the two states explicitly.
**Warning signs:** spike in `suspected=1` correlated with a whitelist outage window.

### Pitfall 3: Zero-event pubkeys silently dropped (D-15)
**What goes wrong:** The adapter omits zero-match authors (contract §5), so without `match_groups` a zero-event pubkey never reaches the consumer and gets no `score` row — violating "every enumerated pubkey gets a verdict".
**Why:** the fetch stage currently passes `fetch_batch` results straight through; `match_groups` is built but unwired.
**How to avoid:** wire `match_groups(batch, groups)` in the fetch stage; rebuild an `AuthorGroup{author, events:[]}` per requested pubkey. Zero-event pubkeys then flow through layers (L0 fires on absence; L1/L3/L4 emit 0.0 since min_events gates them) and get a (low) score. Add the explicit test.
**Warning signs:** `count(score) < count(pubkey)` for a completed run.

### Pitfall 4: Floating-point sum order drift
**What goes wrong:** Summing `wᵢ·xᵢ` by iterating a HashMap gives implementation-defined order → different rounding → occasional 1-ULP score differences → determinism test fails intermittently.
**Why:** HashMap iteration order is not stable.
**How to avoid:** positional `Vec` iteration in fixed layer order (the writer already iterates `subscores` Vec as-is for the same reason).

### Pitfall 5: Blocking the rayon consumer on HTTP
**What goes wrong:** Doing the whitelist `GET` inside the rayon/consumer stage blocks a CPU thread on network I/O, starving analysis throughput (STACK.md rule).
**How to avoid:** resolve whitelist membership in the tokio fetch stage; pass the bool into `Layer::score`.

## Code Examples

### Deterministic 64-bit SimHash (hand-rolled)
```rust
// FNV-1a — deterministic by construction, zero deps.
fn fnv1a64(bytes: &[u8]) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325;
    for &b in bytes { h ^= b as u64; h = h.wrapping_mul(0x100000001b3); }
    h
}

/// 64-bit SimHash over word-shingles of `content` (deterministic).
fn simhash64(shingles: &[String]) -> u64 {
    let mut acc = [0i32; 64];
    for s in shingles {
        let h = fnv1a64(s.as_bytes());
        for i in 0..64 {
            if (h >> i) & 1 == 1 { acc[i] += 1 } else { acc[i] -= 1 }
        }
    }
    let mut out = 0u64;
    for i in 0..64 { if acc[i] > 0 { out |= 1 << i } } // tie (==0) → 0, fixed
    out
}

#[inline] fn hamming64(a: u64, b: u64) -> u32 { (a ^ b).count_ones() }
```

### Shannon entropy (deterministic, order-independent)
```rust
fn shannon_bits_per_char(s: &str) -> f64 {
    use std::collections::HashMap;
    let mut counts: HashMap<char, u64> = HashMap::new();
    let mut n = 0u64;
    for c in s.chars() { *counts.entry(c).or_insert(0) += 1; n += 1; }
    if n == 0 { return 0.0; }
    // Summation is order-independent (addition is commutative); HashMap iteration
    // order does not affect the result for this reduction.
    counts.values().map(|&c| {
        let p = c as f64 / n as f64;
        -p * p.log2()
    }).sum()
}
```

### Whitelist client call (matches verified API)
```rust
#[derive(serde::Deserialize)]
struct CheckResponse { whitelisted: bool }

async fn is_whitelisted(client: &reqwest::Client, base: &str, pubkey: &str)
    -> Result<bool, reqwest::Error>
{
    // GET {base}/check/{pubkey} -> {"whitelisted": bool}  (server.go:45,102)
    let url = format!("{base}/check/{pubkey}");
    let resp = client.get(&url).send().await?.error_for_status()?;
    Ok(resp.json::<CheckResponse>().await?.whitelisted)
}
```

### Wiring match_groups in the fetch stage (D-15)
```rust
// production fetch closure for run_pipeline: every REQUESTED pubkey returns a group.
let groups = fetch_batch(&client, 1, per_author, &batch).await?;
let matched = crate::fetch::match_groups(&batch, groups); // Vec<(&str, Vec<Event>)>
let rebuilt: Vec<AuthorGroup> = matched.into_iter()
    .map(|(pk, events)| AuthorGroup { author: pk.to_string(), events })
    .collect();
// rebuilt now has one entry per requested pubkey, empty Vec for zero-event authors.
```

## Persistence — extend the single-writer additively

- **Score + signals:** **no new code path needed** — `Persist` + `WriteMsg::Score` + `UPSERT_SCORE`/`UPSERT_SIGNAL` already do exactly this (writer.rs iterates `subscores` in fixed order). The combiner builds a `Persist` and calls `store.persist(persist)`.
- **Fingerprints (L1):** add a new additive variant `WriteMsg::Fingerprints(Vec<Fingerprint>)` (the `Fingerprint` model + table already exist) with a `UPSERT_FINGERPRINT` const keyed on `(run_id, pubkey, content_hash)`. Mirror the existing additive-extension pattern (model.rs documents WriteMsg as the "sanctioned single-writer extension point"). Optional for v1 if L1 computes ratios in-memory — but persisting fingerprints is cheap and enables L6 (v2), so recommended.
- **Weights:** add `store` read/seed helpers: `read_weights(run_id?) -> Vec<Weight>` (read the `weight` table) and a seed-on-empty path. Reads use a `reader()` connection; the seed write can go through `run_write_conn()` (touches only `weight`, like the `run`-row updates) or a new `WriteMsg` variant — `run_write_conn` style is simplest and doesn't race the actor's tables.
- **UPSERT determinism (OPS-02):** unchanged — `ON CONFLICT(run_id,pubkey) DO UPDATE` (score) and `ON CONFLICT(run_id,pubkey,layer)` (signal) make re-runs idempotent.

## Validation Architecture

> nyquist_validation is enabled (config.json `workflow.nyquist_validation: true`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Rust built-in `#[test]` + `cargo test` (no external test crate) |
| Config file | none — Cargo convention; `tempfile` 3.27 dev-dep for temp DB/config dirs |
| Quick run command | `cargo test --lib detect:: -- --nocapture` (layer + combiner unit tests) |
| Full suite command | `cargo test` (includes existing store/pipeline/fetch tests) |

### Phase Requirements → Test Map (5 ROADMAP success criteria)
| Req / Criterion | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SC#1 / DETECT-01..05 | Each layer emits `xᵢ∈[0,1]` on synthetic fixtures; bound asserted | unit | `cargo test --lib detect::` | ❌ Wave 0 |
| SC#1 / DETECT-01 | L0 absence→sub-score, presence→0; integration vs live :8081 (self-skip on outage, D-14) | integration | `cargo test --lib detect::whitelist::live_check` | ❌ Wave 0 |
| SC#1 / DETECT-02 | L1 SimHash determinism + repeated-content ratio on duplicate-heavy fixture | unit | `cargo test --lib detect::near_duplicate` | ❌ Wave 0 |
| SC#1 / DETECT-03/04 | L3 entropy band + density; L4 url/domain/tag ratios on fixtures | unit | `cargo test --lib detect::content_entropy detect::link_mention` | ❌ Wave 0 |
| SC#2 / SCORE-01 | combiner: single strong layer < τ, multi-signal ≥ τ (multi-signal-agreement test) | unit | `cargo test --lib detect::combiner::multi_signal` | ❌ Wave 0 |
| SC#3 / SCORE-05 | scored pubkey → `score` row + per-layer `signal` rows with non-empty evidence JSON | integration | `cargo test --lib detect::persists_score_and_evidence` | ❌ Wave 0 (reuses temp_db harness) |
| SC#4 / SCORE-04/OPS-03 | enable/disable a layer via config omits its signal row; no enforcement side-effects | unit | `cargo test --lib config:: detect::disabled_layer_omitted` | ❌ Wave 0 |
| SC#5 / OPS-02 | re-run same fixture corpus twice → byte-identical score/signal rows | integration | `cargo test --lib detect::rerun_deterministic` | ❌ Wave 0 (mirror existing `rerun_is_deterministic`) |
| D-15 | a zero-event pubkey still gets a `score` row | integration | `cargo test --lib fetch::zero_event_gets_score` or pipeline test | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `cargo test --lib detect::` (fast, no network).
- **Per wave merge:** `cargo test` (full suite; live L0 test self-skips if :8081 down).
- **Phase gate:** full suite green before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `src/detect/mod.rs` + per-layer modules with unit tests over synthetic `Event` fixtures (reuse the `ev(author, idx)` helper pattern from pipeline.rs/fetch.rs tests).
- [ ] `src/config.rs` with a path-arg loader and temp-dir tests (D-09).
- [ ] L0 live integration test with self-skip on transport error (mirror `live_latest_per_author` in pipeline.rs:392).
- [ ] Determinism test mirroring `store::tests::rerun_is_deterministic`.
- [ ] `WriteMsg::Fingerprints` + `UPSERT_FINGERPRINT` (if persisting fingerprints) with an idempotency test.
- No new framework install needed — `#[test]` + `tempfile` already present.

## Security Domain

> security_enforcement enabled, ASVS L1 (config.json).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | No auth surface; whitelist :8081 is loopback, no creds. |
| V3 Session Management | no | Stateless batch tool. |
| V4 Access Control | no | Read-only consumer; no enforcement (SCORE-04). |
| V5 Input Validation | **yes** | Event `content`/`tags` are UNTRUSTED. Layers treat them as opaque text; `url::parse` is fail-safe (`.ok()`); never `format!` content into SQL (use `params![]`, existing T-01-01). Pubkey in the `/check/{pubkey}` URL path is 64-hex from the adapter — but still URL-path-segment it safely (it is already validated hex from the enumerate stage). |
| V6 Cryptography | no | SimHash/FNV/xxh3 are NOT security hashes — they are similarity/feature hashes. No secrets, no crypto decisions. Do not represent them as cryptographic. |
| V7 Error Handling/Logging | yes | Whitelist outage degrades gracefully (D-14); never log raw secrets (none here). |

### Known Threat Patterns for this stack
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Malicious event content (huge/adversarial strings) crashing a layer | DoS | events are already capped by `per_author` (≤100) + 256 KiB body (contract); entropy/SimHash are O(content len) and bounded; clamp shingle loops. |
| SQL injection via pubkey/content/layer | Tampering | `params![]` everywhere (existing invariant); never interpolate. |
| Whitelist-unreachable → mass false-positive | Tampering (of verdicts) / availability | fail toward not-flagging (Pitfall 2); record `evidence` for manual re-check. |
| Non-determinism leaking via hasher seed | Repudiation (irreproducible verdicts) | deterministic hashing; OPS-02 re-run test. |
| URL parsing as an SSRF/fetch vector | — | NOT applicable — L4 only *parses* URLs for host strings; it never fetches them. |

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `fxhash` 0.2 | `rustc-hash` 2.x / `foldhash` | superseded | irrelevant here — we use deterministic FNV/xxh3, not a HashMap-speed hasher for fingerprints. |
| `simhash` crate default SipHash | hand-rolled fixed hasher for persisted fingerprints | n/a | determinism on the stored column. |

**Deprecated/outdated:** none affecting this phase.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Hamming threshold H=3/64 for L1 near-dup | §L1 | Too low → misses near-dups (FN); too high → FP. Config-tunable; Phase 6 refits. Low FP risk (conservative). |
| A2 | Entropy cutoffs 2.0 (low, len≥200) / 5.5 (high) bits/char | §L3 | Mis-tuned cutoff → mild FP/FN. Conservative + config + multi-signal combiner blunts impact. |
| A3 | Density knees (emoji 0.5, hashtag 0.3, url 0.8, p-tags 10, t-tags 8) | §L3/§L4 | Same — config values, blunted by combiner. |
| A4 | Logistic weights (L0=0.8,L1=2.0,L3=1.5,L4=1.5), bias=−4.0, τ=0.5 | §L7 | These set the multi-signal threshold. The *structural* property (no single-layer flag; ≥2 strong layers flag) is the requirement and is satisfied; exact magnitudes are starting points Phase 6 retunes. |
| A5 | L0 absence sub-score = 1.0 with small weight; whitelisted = 0.0 | §L0 | If absence should be stronger evidence, raise weight — but FP-averse mandate says weak. |
| A6 | On whitelist transport failure, emit x_L0=0.0 (fail toward not-flagging) | §L0/Pitfall 2 | Policy choice — surface to user. Alternative (treat as absent) is rejected as FP-prone. |
| A7 | No-TTL per-run HashMap cache for L0 (vs the Go client's 30s TTL) | §L0 | TTL would threaten OPS-02 within a long run; no-TTL is the determinism-safe choice. |
| A8 | Hand-roll SimHash (FNV-1a) instead of the `simhash` crate | §Standard Stack | If planner prefers the crate, pin `features=["hasher-fnv"]` + add determinism test. |
| A9 | min_events=5 gate for L1/L4 (else sub-score 0) | §L1/§L4 | Too-sparse pubkeys under-scored — acceptable (FP-averse; they likely also lack whitelist + trigger L0 only, which alone can't flag). |
| A10 | `toml` crate (not `figment`) for Phase 4 | §Config | figment is STACK.md's eventual glue; deferring to Phase 5 CLI is fine. |
| A11 | Persisting `fingerprint` rows via a new `WriteMsg::Fingerprints` is recommended (not strictly required for L1's in-memory ratio) | §Persistence | If skipped, L6 (v2) loses pre-computed fingerprints — low risk, re-derivable. |

**These are exactly the values D-08 delegates to research discretion; every one is a config value and the discuss/plan step may adjust them. The structural guarantees (xᵢ∈[0,1], multi-signal-only flagging, determinism) are NOT assumptions — they are verified design properties.**

## Open Questions

1. **L0 lookup placement (fetch stage vs pre-pass).**
   - What we know: must not block rayon; reqwest client is reused.
   - What's unclear: whether to overlap with `fetch_batch` (note A) or do a separate batched async pass.
   - Recommendation: overlap in the fetch stage (A); simplest and keeps the consumer pure-CPU.
2. **Persist fingerprints in Phase 4 or defer to L6/v2?**
   - Recommendation: persist now (cheap, additive `WriteMsg`, enables v2) but acceptable to skip if planning wants minimal surface — L1 only needs in-memory simhashes for the within-pubkey ratio.
3. **Exact L1 ratio definition** (events-in-clusters/total vs distinct-clusters penalty).
   - Recommendation: events-in-near-dup-clusters / total events — simplest, monotone, in `[0,1]`.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| cargo / rustc | build + test | ✓ | cargo 1.96.0 | — |
| Whitelist server :8081 | L0 live integration test (D-14) | depends on docker host (loopback) | — | self-skip test, deferred manual check (D-14) |
| LMDB2GraphQL adapter | full-run path only (not unit tests) | per network | — | unit tests use synthetic fixtures; no adapter needed |
| crates.io (unicode-segmentation/url/toml) | build | ✓ (resolvable via cargo index) | see table | — |

**Missing with no fallback:** none — all unit tests run with synthetic fixtures, no live services.
**Missing with fallback:** live whitelist (self-skip), live adapter (unit tests don't need it).

## Sources

### Primary (HIGH confidence)
- `whitelist-plugin/pkg/server/server.go` (code-verified) — `GET /check/{pubkey}` → `{"whitelisted":bool}`, no batch endpoint, `/health` 503-while-loading.
- `whitelist-plugin/pkg/client/client.go` (code-verified) — reference client: per-pubkey GET, 8192-entry LRU, 30s TTL, fail-closed on transport error.
- `src/store/{schema,mod,writer,queries}.rs`, `src/model.rs` (code-verified) — `score`/`signal`/`fingerprint`/`weight` tables, `Persist`/`SubScore`/`WriteMsg` enum, `UPSERT_SCORE`/`UPSERT_SIGNAL`, single-writer fixed-order iteration, additive-extension pattern.
- `src/pipeline.rs`, `src/fetch.rs`, `src/graphql/queries.rs` (code-verified) — `run_pipeline(consumer: Fn(&AuthorGroup))` seam, `match_groups` (built, unwired — D-15), `Event{content,tags,kind,created_at}` shape, live-test self-skip idiom.
- `cargo info` (local crates.io index, 2026-06-25) — `unicode-segmentation` 1.13.3, `url` 2.5.8, `toml` 1.1.2, `xxhash-rust` 0.8.15, `simhash` 0.3.0, `gaoya` 0.2.2, `ahash` 0.8.12, `linfa(-logistic)` 0.8.1.
- `gsd-tools query package-legitimacy check --ecosystem crates` (2026-06-25) — all candidate crates verdict OK with downloads/repo/age.
- `.planning/research/STACK.md` (project, HIGH) — stack discipline, gaoya-for-L6, deterministic-hash rationale, no-LLM.

### Secondary (MEDIUM confidence)
- `.planning/ROADMAP.md` Phase 4 — 5 success criteria (mapped in Validation Architecture).
- `.planning/REQUIREMENTS.md` — DETECT/SCORE/OPS IDs.

### Tertiary (LOW confidence)
- Algorithm cutoff magnitudes (entropy/Hamming/density/weights) — `[ASSUMED]` conservative defaults per D-08; not empirically fitted (Phase 6's job). Tracked in Assumptions Log.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — versions `cargo info`-verified + legitimacy-checked; reuse vs add cleanly separated.
- Whitelist API: HIGH — read directly from `whitelist-plugin` source (path, request, response, no-batch fact all code-verified).
- Architecture / trait design / persistence: HIGH — grounded in existing code (`WriteMsg`, `run_pipeline` seam, `weight`/`fingerprint` tables, `match_groups`).
- Algorithm cutoffs / weights: MEDIUM-LOW (intentionally) — conservative FP-averse `[ASSUMED]` defaults that D-08 delegates to research and makes config values; the *structural* properties are verified.

**Research date:** 2026-06-25
**Valid until:** 2026-07-25 (stable Rust ecosystem; whitelist API is in-repo and changes only if that project changes — re-verify `/check` shape if `whitelist-plugin` is updated).
