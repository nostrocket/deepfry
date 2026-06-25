# Phase 4: Detection Layers + Logistic Combiner - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Source:** Front-loaded decisions (autonomous run)
**Mode:** mvp

<domain>
## Phase Boundary

The pipeline produces a **real per-pubkey spam score**. Each P1 detection
layer emits a normalized sub-score xᵢ∈[0,1] through one shared **Layer
contract**; the **logistic combiner (L7)** fuses them via
`sigmoid(Σwᵢxᵢ + b)`; the score plus per-layer evidence is persisted. This is
the first end-to-end runnable verdict and the integration seam every future
layer plugs into.

**In scope:** the `Layer` trait (DETECT-05), the four P1 layers (L0/L1/L3/L4),
the L7 logistic combiner, persistence of `score` + per-layer `signal` rows
with evidence, the config file (OPS-03) carrying URLs + weights + thresholds,
and deterministic scoring (OPS-02). The Phase-3 no-op consumer is replaced by
the real Layer/combiner stage at the rayon seam.

**Out of scope (later phases):** the CLI `run`/`export` surface (Phase 5),
labeling/tuning/backtest (Phase 6), and all v2 layers (L2/L5/L6/L8).
</domain>

<decisions>
## Implementation Decisions

### Layer contract (DETECT-05)
- **D-01:** One shared **`Layer` trait**: each layer takes a pubkey's fetched
  events and emits a normalized sub-score **xᵢ∈[0,1]** plus structured
  **evidence** (why it fired). Every layer is **independently enable/disable-able**
  and carries a **tunable weight + threshold** read from config — no recompile
  to retune (OPS-03).
- **D-02:** Names are self-explanatory: the trait, layer structs, and
  config keys read as plain English (e.g. `whitelist_absence`,
  `near_duplicate`, `content_entropy`, `link_mention`).

### P1 layers (DETECT-01..04)
- **D-03 (L0 whitelist):** Query the live whitelist; **absence emits a
  weighted spam sub-score, presence clears ONLY this layer** — never a gate or
  exemption. A whitelisted pubkey still flows through every other layer. The
  whitelist is at `http://127.0.0.1:8081` (docker); look up batched/cached.
- **D-04 (L1 near-duplicate):** Within-pubkey near-duplicate detection over a
  pubkey's own events via **SimHash + Hamming distance** threshold; emits a
  sub-score from the repeated-content ratio. (The existing `fingerprint` table
  is available for content hashes.)
- **D-05 (L3 content entropy):** Flag **low-entropy templated text** and
  **high-entropy gibberish**, plus URL/emoji/hashtag density; emit a sub-score.
- **D-06 (L4 link & mention):** Score URL ratio, repeated domains, mass `p`-tag
  mentions, and hashtag stuffing; emit a sub-score.

### Combiner (SCORE-01)
- **D-07 (L7):** Fuse sub-scores into one per-pubkey score via weighted
  logistic combination **`sigmoid(Σwᵢxᵢ + b)`**, using **hand-set conservative
  starting weights + bias read from the `weight` table** (seeded by config
  defaults on first run). Phase 6's tuner later re-fits these from labels.

### Starting parameters (Claude's discretion — conservative, FP-averse)
- **D-08:** All layer thresholds (SimHash bits + Hamming cutoff, entropy
  low/high cutoffs, density ratios), the L0 whitelist-absence sub-score
  magnitude, the logistic **starting weights + bias**, and the flag threshold
  **τ** are set to **conservative, false-positive-averse defaults** chosen from
  per-phase research. False positives are the dominant project risk — when in
  doubt, bias toward NOT flagging. Every value is a **config-file value**
  (retunable; Phase 6 re-fits weights from labels).

### Config file (OPS-03)
- **D-09:** Config is **TOML at `~/deepfry/pubkey_iterator_config.toml`**
  (explicit user write-grant for THIS file only). It carries: the adapter URL
  (`http://192.168.149.21:8080/graphql`), the whitelist URL
  (`http://127.0.0.1:8081`), per-layer enable flags + weights + thresholds,
  the combiner bias, and τ. A committed in-repo default/example documents the
  shape. **Config-loading tests use a temp dir**, never the real file (repo
  rule). Other files under `~/deepfry/` are off-limits.

### Persistence & determinism
- **D-10:** For every scored pubkey, persist the **`score` row** (run_id,
  pubkey, score, suspected) and the **per-layer `signal` rows** (EAV: run_id,
  pubkey, layer, value, evidence-JSON) through the single-writer store. Each
  flagged pubkey's per-layer explanation (which layers fired, each sub-score,
  contributing evidence) lives in `signal.evidence` (SCORE-05).
- **D-11 (OPS-02):** Scoring is **deterministic** — seeded RNG, **fixed
  layer-sum order**, UPSERT on `(run_id, pubkey)`. Re-running the same snapshot
  with the same weights produces identical verdicts.
- **D-12 (SCORE-04):** Verdicts are **pubkey-level only**; per-event signals
  are inputs, never the deliverable. **No enforcement** action — output is a
  score in SQLite, nothing else.

### Output medium
- **D-13:** **SQLite only.** No flat-file output anywhere in this phase.

### Carry-forward from Phase 3 (resolve here)
- **D-15:** Phase 3 built `fetch::match_groups` (zero-event authors → empty
  groups, no index-shift) but did **not** wire it into `fetch_batch`/the
  pipeline (Phase-3 review MD-01). Resolve it here: **every enumerated pubkey
  gets a verdict, including zero-event pubkeys.** A pubkey with zero kind-1
  events still flows through the layers — L0 (whitelist absence) and the
  content layers simply emit their zero/absent sub-scores. So `fetch_batch`
  (or the pipeline's fetch stage) MUST use `match_groups` against the requested
  pubkey list so omitted (zero-event) authors become empty groups that reach
  the Layer/combiner stage, rather than silently vanishing. Add a test proving
  a zero-event pubkey still gets a `score` row. If, in planning, scoring
  zero-event pubkeys proves undesirable, the alternative is to delete the unused
  `match_groups` — but the default decision is **score every pubkey**.

### Verification posture
- **D-14:** Layer unit tests use **synthetic event fixtures** (deterministic).
  L0 has an integration test against the **live whitelist**
  (`127.0.0.1:8081`); the combiner determinism test re-runs a fixture twice and
  asserts identical scores. Live services are reachable automatically; a
  transient outage degrades to a deferred manual check, never a block.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Live services (reachable, no human intervention)
- **Whitelist:** `http://127.0.0.1:8081` (docker). **The whitelist project's
  API is in the sibling `whitelist-plugin/` directory — the user has granted
  explicit permission to READ that project's API surface for this integration
  only** (no other cross-project work). Find the request/response shape there.
- **Adapter:** `http://192.168.149.21:8080/graphql` (for the full-run path).

### Phase 1 schema (the persistence targets)
- `src/store/schema.rs` — `score` (run_id, pubkey, score, suspected),
  `signal` EAV (run_id, pubkey, layer, value, evidence JSON), `fingerprint`
  (content hashes for L1), `weight` (layer, weight, threshold, bias/`_bias`,
  `_threshold`). The EAV `signal` table means a new layer adds rows, never a
  migration.
- `src/store/{mod,writer,queries}.rs` — single-writer API to persist through.

### Pipeline seam
- `src/enumerate.rs` / the Phase-3 pipeline — the rayon stage where the
  no-op consumer is replaced by the Layer/combiner stage.

### Project planning
- `.planning/ROADMAP.md` Phase 4 — goal + 5 success criteria.
- `.planning/REQUIREMENTS.md` — DETECT-01..05, SCORE-01/04/05, OPS-02/03.
</canonical_refs>

<code_context>
## Existing Code Insights

- `weight` table already models per-layer weight + threshold + `_bias` /
  `_threshold` sentinel rows + provenance — the combiner reads it; config
  seeds it on first run.
- `signal.evidence` is a TEXT/JSON column purpose-built for SCORE-05 per-layer
  explanations.
- `fingerprint` table (run_id, pubkey, content_hash, ...) supports L1 SimHash
  storage.
- Single-writer store invariant: all `score`/`signal` writes route through the
  one writer actor (extend the Phase-2 `WriteMsg` enum additively).
- `rayon` (Phase 3) carries the CPU analysis; layers run inside the rayon
  stage. Determinism requires a fixed reduction order over layers.
</code_context>

<specifics>
## Specific Ideas

- L0 is the subtle one: absence-from-whitelist is a *weighted term*, never a
  hard gate. A non-whitelisted pubkey is not automatically spam; it just
  carries that one sub-score into the logistic sum.
- Hand-set starting weights must be conservative: the combiner should require
  *multi-signal agreement* to flag, matching the project's "no hard
  single-layer cutoffs" anti-feature stance.
- Determinism is testable cheaply: score a fixture corpus twice, assert byte-
  identical `score`/`signal` rows.
</specifics>

<deferred>
## Deferred Ideas

- **CLI `run`/`export`** — Phase 5.
- **Labeling / tuning / backtest** — Phase 6.
- **v2 layers:** L2 cadence/burst (DETECT-06), L5 tag/kind fingerprint
  (DETECT-07), L6 cross-pubkey clustering (DETECT-08, top v2 priority), L8
  language/homoglyph (DETECT-09).
</deferred>

---

*Phase: 4-Detection Layers + Logistic Combiner*
*Context front-loaded: 2026-06-25 (autonomous run)*
