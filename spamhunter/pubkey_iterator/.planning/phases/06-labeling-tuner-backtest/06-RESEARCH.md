# Phase 6: Labeling + Logistic Tuner + Backtest Gate - Research

**Researched:** 2026-06-26
**Domain:** offline supervised re-fit of a logistic combiner (linfa-logistic), SQLite schema rename, backtest adoption gate, negative-sampling review queue
**Confidence:** HIGH

## Summary

Phase 6 closes the correctability loop. A human writes ground-truth labels straight into a renamed
`backpropagation` table; a new `tune` subcommand fits a binary logistic-regression over the
`signal × backpropagation` join with **linfa-logistic 0.8.1** (deterministic L-BFGS, no RNG), extracts
per-layer coefficients + intercept, and writes them to the existing `weight` table **only if** a
backtest against the full labeled set shows no new false negatives and no new false positives. A
`review_queue` view surfaces a seeded random sample of *unflagged* pubkeys to counter selection bias.

The codebase is unusually well-prepared for this: the `weight` table already carries `tuned_at` /
`tuned_from_run` provenance columns; `ScoringStage::from_config` already reads weights positionally and
computes `sigmoid(Σwᵢxᵢ+b)`; `seed_weights_if_empty` already declines to overwrite stored weights so a
retune survives; and `run_batch` already snapshots τ + the full weight set into `run.config_json` before
scoring (TUNE-03 is effectively **already satisfied** — Phase 6 only adds a confirming test, see §TUNE-03).
The `label` table exists, is unused, and matches the human-insert contract — so D-01 is a free
`CREATE TABLE` rename, not a data migration.

The one genuine design freedom is the adoption mechanism (D-06): the recommended pattern is **fit →
re-score in-memory using the SAME combiner math → gate → write the `weight` table only on pass**. The new
weights are never persisted to `weight` until they clear the backtest, so a blocked adoption is a pure
no-op on live state (old weights stay in force).

**Primary recommendation:** Add `linfa = "0.8"`, `linfa-logistic = "0.8"`, `ndarray = "0.16"` (default
features, pure-Rust, no BLAS). Build the feature matrix in **fixed layer order** (the same order
`ScoringStage` uses), fit with `LogisticRegression::default().alpha(L2).max_iterations(N)`, map
`fitted.params()[i]` → layer weight and `fitted.intercept()` → `_bias`, re-score with a standalone
`combine(weights, bias, xs)` helper extracted from `ScoringStage::score`, and write through
`Store::weight_write_conn` inside one transaction guarded by the backtest.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Label capture (TUNE-01) | Database / Storage | — | Humans `INSERT` directly into `backpropagation`; engine only reads it (D-02). No app tier. |
| Logistic fit (TUNE-02) | Offline batch (CLI `tune`) | linfa/ndarray lib | Pure CPU math over a SQLite read; no I/O, no network. |
| Weight loading + snapshot (TUNE-03) | Offline batch (`run_batch`) | Database / Storage | Already implemented in Phase 5: read latest `weight` rows → snapshot into `run.config_json`. |
| Negative-sampling queue (TUNE-04) | Database / Storage | — | A SQLite VIEW (or materialized table) over `score`; deterministic seed lives in SQL. |
| Backtest gate (TUNE-05) | Offline batch (CLI `tune`) | — | In-memory re-score using the combiner; pure decision, blocks the `weight` write. |

**Why this matters:** every capability in Phase 6 is either a SQLite read/write or in-process CPU math.
There is **no network, no async, no new HTTP** — `tune` must be a synchronous subcommand (like `export`),
NOT inside the tokio `Run` arm. Putting the fit on the tokio runtime, or routing the weight write through
the async single-writer actor, would both be tier misassignments.

## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01:** Rename the Phase-1 `label` table → `backpropagation`. Keep the shape: `pubkey` (TEXT),
  `is_spam` (INTEGER 1/0), `labeled_at` (INTEGER unix ts); retain nullable `source` + `note` for
  leakage-audit / provenance. Update `src/store/schema.rs` and **every reference**. No real data exists →
  `CREATE TABLE` rename, not a data migration.
- **D-02:** Humans `INSERT` labels directly into `backpropagation` with any SQLite client. **There is NO
  `label` CLI subcommand** — deliberate, recorded deviation from OPS-01's literal wording. The engine only
  **reads** `backpropagation`.
- **D-03:** A `tune` subcommand fits a logistic model with `linfa-logistic` over the `signal ×
  backpropagation` join (per-layer sub-scores as features, `is_spam` as label) and writes new layer
  weights + bias + threshold to the `weight` table with provenance (`tuned_at`, `tuned_from_run`).
  `linfa-logistic` (+ `linfa`, `ndarray`) are the Phase-6-owned deps.
- **D-04:** Each run reads the latest weights at startup and snapshots them into run metadata so any past
  score is traceable to the exact weights that produced it.
- **D-05:** The review/labeling queue includes a random sample of *unflagged* pubkeys to counter selection
  bias — materialized as a SQLite table/view (e.g. `review_queue`). Sample size is Claude's discretion
  (sensible default, config-able).
- **D-06:** Before adopting new weights, backtest against the full `backpropagation` set: confirmed-spam
  must remain flagged (no new FN) AND confirmed-non-spam must remain unflagged (no new FP). A regression
  is surfaced and **BLOCKS adoption**; old weights stay in force; the regression detail is reported.
- **D-07:** SQLite only. Labels in `backpropagation`, weights in `weight`, review queue + backtest results
  in SQLite. No flat files.
- **D-08:** Tuner + backtest verified with synthetic labeled fixtures. No real human labels for the
  autonomous run. The logistic fit is seeded/deterministic — same fixture → same weights.

### Claude's Discretion

- Negative-sample size, `linfa-logistic` hyperparameters (regularization `alpha`, `max_iterations`), the
  exact `review_queue` columns, and how "adoption" is gated mechanically (e.g. a staging weight set
  promoted only on a passing backtest).

### Deferred Ideas (OUT OF SCOPE)

- Live enforcement / feeding the whitelist or quarantine — out of scope (high blast radius).
- Incremental service mode (SVC-01) and direct LMDB reads (PERF-01) — v2.
- A `label` CLI subcommand — intentionally dropped (D-02); labels are direct SQLite inserts.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| TUNE-01 | Humans record confirmed FPs/TPs as run-independent labels in SQLite | §`label`→`backpropagation` Rename — the renamed table keyed on `pubkey` (run-independent), human `INSERT` contract; §Validation maps the seed-and-read test |
| TUNE-02 | Offline `tune` step fits logistic (`linfa-logistic`) over signals × labels, writes new weights | §Standard Stack (linfa 0.8.1 set), §Pattern 1 (feature matrix + fit + extract), §Pattern 2 (`tune` arm + provenance write) |
| TUNE-03 | Each run reads latest weights + snapshots them into run metadata | §TUNE-03 — already implemented in `run.rs`; Phase 6 adds a confirming test only |
| TUNE-04 | Review queue includes randomly-sampled unflagged pubkeys (negative sampling) | §Pattern 3 (`review_queue` deterministic-seed VIEW over `score`) |
| TUNE-05 | Weight/algorithm change backtested against full labeled set before adoption; regressions block | §Pattern 4 (fit→re-score→gate→write-on-pass), §Common Pitfalls (the leakage + tie pitfalls) |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `linfa` | 0.8.1 | ML framework: `Dataset` / `DatasetBase`, the `Fit` trait | The de-facto Rust ML toolkit (rust-ml org); `linfa-logistic` is built on it; CONTEXT D-03 names it. [VERIFIED: crates.io] |
| `linfa-logistic` | 0.8.1 | Binary `LogisticRegression` (L-BFGS via argmin), `FittedLogisticRegression` | Exactly the model the combiner uses (`sigmoid(Σwᵢxᵢ+b)`); deterministic, pure-Rust. CONTEXT D-03. [VERIFIED: crates.io] |
| `ndarray` | 0.16 | `Array1` / `Array2<f64>` for the feature matrix + target vector | linfa 0.8.1 pins `ndarray = "0.16"`; **must match to share types** (see Pitfall 1). [VERIFIED: crates.io] |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `argmin` | 0.11.0 (transitive) | The L-BFGS + More–Thuente line-search optimizer inside linfa-logistic | Pulled in automatically; **do not add directly**. Listed only to document the deterministic optimizer. [VERIFIED: cargo resolve] |
| `rusqlite` | 0.40 (existing) | Read the join, write `weight` rows | Already a dep; the join query + weight write go through it. |
| `serde_json` | 1.0 (existing) | Surface the backtest report (e.g. regression detail) | Already a dep; no new add. |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `linfa-logistic` | Hand-rolled gradient descent | Rejected — CONTEXT locks linfa-logistic; hand-rolling re-implements L-BFGS + line search (don't-hand-roll). |
| `ndarray` 0.16 | `ndarray` 0.17.2 (current latest) | **Rejected** — linfa 0.8.1 pins `^0.16`; 0.17 would resolve a *second* ndarray and the `Array2` you build would be a different type than `Dataset::new` expects (cryptic trait-bound error). Pin 0.16. |
| linfa `default-features` | `blas` / `openblas-system` feature | Rejected — pure-Rust default needs no system BLAS (reproducible across hosts, matching the `rusqlite` `bundled` rationale). The dataset (≤ a few thousand labeled rows × ~4 features) is tiny; BLAS is irrelevant. |

**Installation:**
```bash
cargo add linfa@0.8 linfa-logistic@0.8 ndarray@0.16
```
This resolves (verified via a throwaway `cargo add`): `linfa 0.8.1`, `linfa-logistic 0.8.1`,
`ndarray 0.16.1`, `argmin 0.11.0` — clean, no version conflicts. `[VERIFIED: cargo resolve]`

**Version verification (run during planning to re-confirm):**
```bash
cargo info linfa@0.8.1          # version: 0.8.1, repo rust-ml/linfa
cargo info linfa-logistic@0.8.1 # version: 0.8.1, repo rust-ml/linfa
cargo info ndarray@0.16         # confirm a 0.16.x exists
```
linfa-logistic 0.8.1 published 2025-12-23; 0.8.0 2025-09-30. `[VERIFIED: crates.io]`

**Cargo.toml placement (stack discipline):** the manifest header says "the remainder (gaoya/linfa) stays
for its owning phase … those are Phase 6." Add a Phase-6 block:
```toml
# linfa + linfa-logistic + ndarray (Phase-6-owned, D-03): the offline logistic
# tuner. Default features = pure Rust (no BLAS) — linfa pins ndarray 0.16, so
# ndarray MUST be 0.16 to share Array2/Dataset types. Deterministic L-BFGS fit
# (argmin), no RNG — matches OPS-02. Used ONLY by the `tune` subcommand.
linfa = "0.8"
linfa-logistic = "0.8"
ndarray = "0.16"
```

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `linfa` | crates.io | since 2018-04 | ~22.7k/wk | github.com/rust-ml/linfa | OK | Approved |
| `linfa-logistic` | crates.io | since 2020-11 | ~2.0k/wk | github.com/rust-ml/linfa | OK | Approved |
| `ndarray` | crates.io | since 2015-12 | ~1.55M/wk | github.com/rust-ndarray/ndarray | OK | Approved |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none.

All three returned `verdict: OK` from `gsd-tools query package-legitimacy check --ecosystem crates`, each
with a real source repo under the well-known `rust-ml` / `rust-ndarray` orgs, no postinstall, not
deprecated. CONTEXT D-03 names them explicitly. `[VERIFIED: crates.io + package-legitimacy seam]`

## Architecture Patterns

### System Architecture Diagram

```
  HUMAN (any SQLite client)
        │  INSERT INTO backpropagation (pubkey, is_spam, labeled_at[, source, note])
        ▼
  ┌─────────────────────────────────────────────────────────────────┐
  │                       SQLite store (WAL)                          │
  │   backpropagation ──┐                                             │
  │   signal (EAV) ─────┤  JOIN on pubkey, fixed layer order          │
  │   score ────────────┤  (latest run's signals)                    │
  │   weight ◄──────┐    │                                            │
  └─────────────────┼────┼────────────────────────────────────────────┘
                    │    │
   `tune` subcommand│    ▼
   (sync, no tokio) │  (1) BUILD feature matrix  Array2<f64> [rows=labeled pubkeys,
                    │      cols=fixed layers]  +  target Array<i32/bool> = is_spam
                    │           │
                    │           ▼
                    │  (2) FIT  LogisticRegression::default()
                    │           .alpha(α).max_iterations(N).fit(&dataset)   [L-BFGS, deterministic]
                    │           │  → FittedLogisticRegression
                    │           ▼
                    │  (3) EXTRACT  params()[i] → staging weight[layer_i]
                    │               intercept() → staging _bias
                    │           │
                    │           ▼
                    │  (4) BACKTEST  re-score EVERY labeled pubkey with STAGING weights
                    │               using combine() = sigmoid(Σ wᵢ·xᵢ + b) ; flag = score > τ
                    │               compare vs is_spam:
                    │                 • any confirmed-spam now UNflagged  → new FN  → BLOCK
                    │                 • any confirmed-ham  now flagged    → new FP  → BLOCK
                    │           │
                    │     ┌─────┴─────┐
                    │   PASS         FAIL
                    │     │            │
                    └─────┘            └──► report regression detail; WRITE NOTHING
   (5) on PASS: UPDATE weight rows (layer weights + _bias + _threshold) with
       tuned_at = now, tuned_from_run = <run scored from>   (one transaction)
```

The combiner math (`combine`) is shared between the live `ScoringStage::score` and the backtest
re-score — re-using it guarantees the backtest measures the SAME function the next `run` will apply.

### Recommended Project Structure
```
src/
├── store/
│   └── schema.rs          # RENAME label → backpropagation (D-01)
├── model.rs               # rename `Label` struct doc-comment refs; (struct name optional)
├── detect/
│   └── mod.rs             # extract a free `combine(weights,bias,xs)->f64`; update the
│                          #   `count(&conn,"label")` test refs → "backpropagation"
├── tune.rs                # NEW: the tuner — join read, feature matrix, fit, extract,
│                          #   backtest gate, weight write (pure SQLite + linfa)
└── main.rs                # add the `Tune` clap arm (sync, no tokio runtime)
```
`tune.rs` is a new sibling module declared in `lib.rs`, mirroring how `export.rs` was added in Phase 5.

### Pattern 1: Build the feature matrix, fit, and extract parameters
**What:** Turn the `signal × backpropagation` join into an `ndarray` `Dataset`, fit, read coefficients.
**When to use:** the core of `tune` (TUNE-02).
**Key shape rule:** columns are the **enabled layers in the SAME fixed order** `ScoringStage` uses
(`L0_whitelist_absence, L1_near_duplicate, L3_content_entropy, L4_link_mention`). `params()[i]` then maps
positionally back to `layer_i` — this is exactly the positional pairing `from_config` already relies on.
```rust
// Source: docs.rs/linfa-logistic/0.8.1 + rust-ml/linfa master src/lib.rs [CITED]
use linfa::traits::Fit;
use linfa::Dataset;
use linfa_logistic::LogisticRegression;
use ndarray::{Array1, Array2};

// LAYERS is the fixed feature order (must match ScoringStage's declared order).
const LAYERS: [&str; 4] = [
    "L0_whitelist_absence",
    "L1_near_duplicate",
    "L3_content_entropy",
    "L4_link_mention",
];

// rows: one per labeled pubkey that has signals for the chosen run.
// For each row, xs[j] = signal.value for LAYERS[j] (0.0 if a layer wrote no row).
// y = 1 (spam) / 0 (ham) from backpropagation.is_spam.
let n = rows.len();
let mut x = Array2::<f64>::zeros((n, LAYERS.len()));
let mut y = Array1::<usize>::zeros(n); // linfa binary targets: integer class labels
for (i, row) in rows.iter().enumerate() {
    for (j, v) in row.features.iter().enumerate() {
        x[[i, j]] = *v;
    }
    y[i] = row.is_spam as usize;
}
let dataset = Dataset::new(x, y);

// Deterministic L-BFGS (no RNG). alpha = L2 regularization; with_intercept fits the bias.
let model = LogisticRegression::default()
    .alpha(1.0)               // L2 strength — Claude's discretion; 1.0 is a safe default
    .max_iterations(100)      // convergence cap — discretion
    .with_intercept(true)
    .fit(&dataset)?;          // FittedLogisticRegression

let coeffs = model.params();   // &Array1<f64>, one per feature column, in LAYERS order
let bias   = model.intercept();// f64 → the _bias sentinel
// coeffs[j] is the tuned weight for LAYERS[j].
```
**Determinism note (OPS-02 / D-08):** linfa-logistic optimizes with argmin's `LBFGS` + `MoreThuenteLineSearch`
— **no RNG, no random seed**. Given the same dataset and the default initial params (all-zeros), the fit is
bit-reproducible. So "seeded fit" is satisfied by *fixing the input order + hyperparameters*; there is no
seed knob to set. The fixture test asserts the same rows → the same weights twice. `[VERIFIED: rust-ml/linfa src]`

### Pattern 2: The `tune` subcommand + provenance write
**What:** the clap arm; sync (no tokio); reads the join, fits, gates, writes weights with provenance.
**When to use:** `main.rs` dispatch + `tune::run_tune`.
```rust
// main.rs — mirror the Export arm (pure SQLite, no tokio runtime).
#[derive(Subcommand, Debug)]
enum Commands {
    Run { #[arg(long)] resume: bool },
    Export { #[arg(long)] run_id: Option<i64> },
    /// Re-fit layer weights from human labels; adopt only if the backtest passes.
    Tune {
        /// Which run's signals to train from (default: the latest `done` run).
        #[arg(long)] run_id: Option<i64>,
    },
}
// ...
Commands::Tune { run_id } => {
    let config = pubkey_iterator::config::load(&config_path)?;
    let store = pubkey_iterator::store::Store::open(&cli.db)?;
    let report = pubkey_iterator::tune::run_tune(&store, &config, run_id)?;
    eprintln!("{}", report.summary());     // adopted, or BLOCKED with regression detail
    store.close()?;
}
```
**Which run to train from:** train from the latest `done` run's `signal` rows (the `score`/`signal`
features are run-scoped, so a fit needs ONE run's signals joined to the run-independent labels). Record
that run_id as `tuned_from_run` (the provenance column already exists). `export::latest_done_run(&conn)`
is the existing helper to default it.

**The weight write (on backtest PASS):** UPDATE the existing `weight` rows in one transaction on
`Store::weight_write_conn` (the sanctioned short-lived weight-only connection — it touches ONLY `weight`,
never the actor's tables, so the single-writer invariant holds). Set `tuned_at = now`,
`tuned_from_run = <run_id>`:
```rust
// Source: src/detect/mod.rs seed_weights_if_empty pattern [CITED: existing code]
let conn = store.weight_write_conn()?;
let tx = conn.unchecked_transaction()?; // or conn.transaction() on a &mut conn
for (layer, w) in fitted_layer_weights {           // params![] bound — never format! (T-05)
    tx.execute(
        "UPDATE weight SET weight=?2, tuned_at=?3, tuned_from_run=?4 WHERE layer=?1",
        params![layer, w, now, run_id])?;
}
tx.execute("UPDATE weight SET weight=?1, tuned_at=?2, tuned_from_run=?3 WHERE layer='_bias'",
    params![bias, now, run_id])?;
// _threshold (τ) is NOT re-fit by logistic regression — keep the existing τ unless a
// separate τ-tuning decision is made (out of scope; leave _threshold untouched).
tx.commit()?;
```
**Important:** the logistic fit produces layer **weights + bias** only. τ (`_threshold`) is a *decision
threshold*, not a fitted logistic parameter. Leave `_threshold` as-is (the backtest uses the stored τ).
The `tuned_at`/`tuned_from_run` provenance applies to the rows the tuner actually rewrites.

### Pattern 3: The `review_queue` negative-sampling view (TUNE-04 / D-05)
**What:** a deterministic random sample of *unflagged* pubkeys from the latest run, for human review.
**When to use:** the human reads it before entering labels, so labels aren't drawn only from flagged
pubkeys (selection bias).
**Deterministic sampling without an RNG:** order unflagged pubkeys by a hash of `(pubkey || run_id)` and
take the first K. A stable hash of a fixed string is reproducible across runs (no `rand` dep needed) —
SQLite has no built-in hash, so either (a) compute the order in Rust and INSERT into a `review_queue`
TABLE, or (b) use a deterministic SQL expression. Recommended: a **materialized TABLE** populated by `tune`
(or a small `review` step folded into `tune`), so the sample is a stable, inspectable artifact:
```sql
CREATE TABLE IF NOT EXISTS review_queue (
  run_id     INTEGER NOT NULL REFERENCES run(run_id),
  pubkey     TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score      REAL    NOT NULL,   -- the unflagged pubkey's score (≤ τ)
  sampled_at INTEGER NOT NULL,
  PRIMARY KEY (run_id, pubkey)
);
```
Populate in Rust with a deterministic selection (e.g. sort unflagged `score` rows by a fixed
`xxhash(pubkey)` — the `gxhash`/`xxhash` already used for fingerprints, or a simple FNV — and take the
first `sample_size`). **Deterministic** = same run + same sample_size → same rows (D-08/OPS-02 spirit).
**Default sample size:** 100 (config-able, e.g. `tune.review_sample_size` in the TOML; Claude's discretion
per D-05). Sample the complement of the suspected set: `score.suspected = 0` for the chosen run, plus
optionally pubkeys with no score row at all ("unscored").

### Pattern 4: The backtest adoption gate (TUNE-05 / D-06)
**What:** fit into a STAGING (in-memory) weight set, re-score the full labeled set, promote only on pass.
**When to use:** the safety interlock — the whole point of Phase 6.
```rust
// Re-use the SAME combiner math the live run uses (extracted from ScoringStage::score).
fn combine(weights: &[f64], bias: f64, xs: &[f64]) -> f64 {
    let z = bias + weights.iter().zip(xs).map(|(w, x)| w * x).sum::<f64>();
    1.0 / (1.0 + (-z).exp())  // sigmoid
}

struct Regression { new_false_negatives: Vec<String>, new_false_positives: Vec<String> }

fn backtest(labeled: &[(String, bool, Vec<f64>)], staging_w: &[f64], staging_b: f64, tau: f64)
    -> Result<(), Regression>
{
    let mut fn_ = vec![]; let mut fp = vec![];
    for (pk, is_spam, xs) in labeled {
        let flagged = combine(staging_w, staging_b, xs) > tau;
        if *is_spam && !flagged { fn_.push(pk.clone()); }   // confirmed-spam now unflagged
        if !*is_spam && flagged { fp.push(pk.clone()); }    // confirmed-ham now flagged
    }
    if fn_.is_empty() && fp.is_empty() { Ok(()) }
    else { Err(Regression { new_false_negatives: fn_, new_false_positives: fp }) }
}
```
On `Ok` → write the staging weights to the `weight` table (Pattern 2). On `Err` → write nothing, return
the regression detail in the report (which pubkeys regressed, FN vs FP). **Blocked adoption is a no-op on
live state** — the old `weight` rows are never touched, so the next `run` keeps scoring with them. This is
the FP-averse, conservative default D-06 mandates.

**Definition of "remain flagged/unflagged":** the gate compares the STAGING-weight verdict (`score > τ`)
against the human label `is_spam`. The strict reading of D-06 is *absolute*: zero new FN and zero new FP.
That is the conservative default and what the synthetic-regression test asserts.

### Anti-Patterns to Avoid
- **Writing weights then backtesting:** never persist the fitted weights before the gate passes. Fit into
  a staging Vec; the `weight` table is touched only on PASS. (A "promote a staging weight set" mechanism
  is exactly D-06's suggested approach.)
- **Re-implementing sigmoid in the backtest differently from the live combiner:** extract ONE `combine`
  helper and call it from both `ScoringStage::score` and `backtest`, or the gate measures a different
  function than production applies.
- **HashMap-ordered feature columns:** the feature matrix columns and `weights` Vec must be a fixed
  positional `Vec` in the SAME order (OPS-02). A HashMap iteration would scramble `params()[i]` → layer.
- **Adding a `label` subcommand:** explicitly forbidden (D-02). Labels are direct SQL inserts.
- **Bumping ndarray to 0.17:** breaks the shared-type bound with linfa 0.8 (Pitfall 1).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Logistic regression fit | Gradient descent + line search | `linfa-logistic` `LogisticRegression` | L-BFGS + More–Thuente line search, convergence handling, L2 regularization — all solved, deterministic. |
| Matrix/vector storage | `Vec<Vec<f64>>` | `ndarray::Array2`/`Array1` | linfa's `Dataset` takes ndarray; rolling your own forces conversions and breaks the trait bounds. |
| The sigmoid combiner | A second copy in `tune.rs` | A shared `combine()` extracted from `ScoringStage::score` | The backtest MUST measure the exact function the next run applies. |
| Weight provenance | A new table | The existing `weight.tuned_at` / `tuned_from_run` columns | Purpose-built in Phase 1 for exactly this. |
| Run→weight traceability (TUNE-03) | New snapshot code | The existing `run.config_json` snapshot in `run_batch` | Already implemented (see §TUNE-03). |

**Key insight:** Phase 6 is mostly *wiring already-built pieces together*. The schema columns, the
combiner, the weight read/seed, and the run snapshot all exist. The genuinely new code is: the rename, the
~80-line `tune.rs` (join → matrix → fit → gate → write), the `review_queue` table, and the `Tune` clap arm.

## Runtime State Inventory

> Rename phase (D-01: `label` → `backpropagation`). All five categories checked.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | The `label` table has **no rows** — it is created by `CREATE TABLE IF NOT EXISTS` but nothing inserts into it (verified: no `INSERT INTO label` anywhere in `src/`; `seed_scored_pubkey` and the writer touch only score/signal/pubkey/fingerprint/weight/suspected_spammer). No SQLite data to migrate. | None — pure DDL rename. |
| Live service config | None — `label` is an internal SQLite table, not referenced by any external service, n8n workflow, Dgraph schema, or whitelist plugin. This engine is read-only toward strfry/Dgraph. | None. |
| OS-registered state | None — no scheduled task, pm2 process, or systemd unit references `label`. | None. |
| Secrets/env vars | None — no env var or secret named `label`. | None. |
| Build artifacts | The compiled `pubkey_iterator` binary embeds `SCHEMA_DDL` as a string const; rebuilding after the rename regenerates it. No egg-info / stale package. An **existing on-disk `spamhunter.sqlite`** from a prior `run` will already have a `label` table; since `CREATE TABLE IF NOT EXISTS backpropagation` won't rename it, a pre-existing dev DB would end up with BOTH tables (harmless — `backpropagation` is empty either way). | Rebuild after edit. For a pre-existing dev DB, drop it or `ALTER TABLE label RENAME TO backpropagation` manually (dev convenience only; not a production migration since no production data exists). |

**Exact references to update** (grep `\blabel\b` across `src/`):
1. `src/store/schema.rs:66` — `CREATE TABLE IF NOT EXISTS label (...)` → `backpropagation` (keep all
   columns: `pubkey`, `is_spam`, `labeled_at`, `source`, `note`).
2. `src/store/mod.rs:433` (test `open_creates_wal_and_schema`) — the table-list assertion array contains
   `"label"` → change to `"backpropagation"`.
3. `src/detect/mod.rs:833,851-853` (test `no_enforcement_side_effect`) — `count(&conn0, "label")`,
   `count(&conn, "label")`, and the assertion message "scoring must NOT write the label table" → all
   `backpropagation`.
4. `src/model.rs:68-77` — the `Label` struct doc-comment says "mirrors the `label` table"; update the
   comment to `backpropagation`. The **struct name `Label`** itself is internal Rust and *may* be renamed
   to `Backpropagation` for consistency, but is not load-bearing (no SQL depends on it) — Claude's
   discretion. `model.rs:73` (`pub labeled_at: i64`) is a field name, keep it (matches the column).

**Nothing found that depends on the `label` name outside these four files** — verified by
`grep -rn "label" src/ --include="*.rs"` (the only hits are the four above plus the `labeled_at` field).

## Common Pitfalls

### Pitfall 1: ndarray version skew with linfa
**What goes wrong:** adding `ndarray = "0.17"` (the current latest) while linfa pins `^0.16` resolves two
ndarray crates; the `Array2<f64>` you build is a *different type* than `Dataset::new` accepts → a cryptic
`expected ndarray::ArrayBase<...0.16...>, found ...0.17...` trait error.
**Why it happens:** `cargo info ndarray` shows 0.17.2 as latest, which is the tempting choice.
**How to avoid:** pin `ndarray = "0.16"` to match linfa 0.8.1's dependency. Verified: `cargo add
linfa@0.8 linfa-logistic@0.8 ndarray@0.16` resolves cleanly to 0.16.1.
**Warning signs:** build error mentioning two ndarray versions or `ArrayBase` trait-bound mismatch.

### Pitfall 2: Label leakage / selection bias making the fit look perfect
**What goes wrong:** if labels come only from flagged pubkeys, the logistic fit learns "everything is
spam" and the backtest trivially passes — but live FPs explode.
**Why it happens:** labels are entered after reviewing the `suspected_spammer` export (all flagged).
**How to avoid:** TUNE-04's `review_queue` deliberately injects unflagged pubkeys for the human to label,
so the training set has real negatives. The `source` column on `backpropagation` lets a later audit see
whether a label came from the flagged export or the review queue (leakage audit — D-01 retained it).
**Warning signs:** the fit assigns near-zero or negative weight to every layer; the backtest passes with
zero labeled negatives present.

### Pitfall 3: A degenerate fit (all-one-class labels) crashes or returns garbage
**What goes wrong:** if every labeled pubkey is `is_spam = 1` (or all 0), logistic regression has no
decision boundary; linfa-logistic may error or produce an unstable fit.
**Why it happens:** early labeling, or a synthetic fixture that forgot to seed both classes.
**How to avoid:** before fitting, assert the labeled set has ≥1 spam AND ≥1 ham row; if not, return a
clear "need both classes to tune" report and write nothing (a blocked-by-precondition, not a regression).
The synthetic fixtures MUST seed both classes (the determinism + pass-case fixtures) — see §Validation.
**Warning signs:** `fit()` returns `Err`, or all coefficients are huge/NaN.

### Pitfall 4: A pubkey with no signal row for a layer
**What goes wrong:** the `signal × backpropagation` join is sparse — a disabled layer (or a zero-event
pubkey) writes no `signal` row, so a naive INNER JOIN per layer drops the pubkey or leaves a feature
column missing.
**Why it happens:** EAV signals are one row *per fired layer*, not a dense row per pubkey.
**How to avoid:** build the feature matrix by pivoting: for each labeled pubkey, look up each LAYERS[j]'s
`signal.value` and default to **0.0** when absent (a missing layer contributed nothing to the live score —
0.0 is the correct neutral feature, matching `ScoringStage` omitting a disabled layer). Do NOT inner-join
per layer; left-join the labeled pubkey set against its signals and fill gaps with 0.0.
**Warning signs:** fewer feature rows than labeled pubkeys; ragged feature vectors.

### Pitfall 5: Backtesting against the wrong run's signals
**What goes wrong:** re-scoring uses signals from a different run than the labels were reviewed against,
producing meaningless FN/FP counts.
**Why it happens:** `signal` is keyed `(run_id, pubkey, layer)`; the join must fix one run_id.
**How to avoid:** `tune` picks ONE run (default latest `done` via `latest_done_run`), trains on that run's
signals, AND backtests on that same run's signals — record it as `tuned_from_run`. The backtest re-scores
the SAME (pubkey, feature-vector) pairs the fit used.
**Warning signs:** backtest FN/FP counts that don't match a manual spot-check.

## Code Examples

### Reading the `signal × backpropagation` join into per-pubkey feature vectors
```rust
// Source: derived from src/store/queries.rs read_signals pattern [CITED: existing code]
// Pivot the EAV signals for one run into dense feature vectors aligned to LAYERS,
// joined to the run-independent labels. Missing (pubkey,layer) → 0.0 (Pitfall 4).
pub fn labeled_features(
    conn: &rusqlite::Connection,
    run_id: i64,
    layers: &[&str],
) -> rusqlite::Result<Vec<(String, bool, Vec<f64>)>> {
    use std::collections::BTreeMap;
    // 1. labels (run-independent)
    let mut labels: BTreeMap<String, bool> = BTreeMap::new();
    let mut s = conn.prepare("SELECT pubkey, is_spam FROM backpropagation")?;
    for row in s.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)? != 0)))? {
        let (pk, is_spam) = row?; labels.insert(pk, is_spam);
    }
    // 2. signals for this run, only for labeled pubkeys
    let mut feats: BTreeMap<String, Vec<f64>> =
        labels.keys().map(|pk| (pk.clone(), vec![0.0; layers.len()])).collect();
    let mut s = conn.prepare(
        "SELECT pubkey, layer, value FROM signal WHERE run_id = ?1 ORDER BY pubkey, layer")?;
    for row in s.query_map([run_id], |r|
        Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?, r.get::<_, f64>(2)?)))?
    {
        let (pk, layer, value) = row?;
        if let Some(v) = feats.get_mut(&pk) {
            if let Some(j) = layers.iter().position(|l| *l == layer) { v[j] = value; }
        }
    }
    Ok(labels.into_iter()
        .map(|(pk, is_spam)| { let f = feats.remove(&pk).unwrap(); (pk, is_spam, f) })
        .collect())
}
```

### Extracting the standalone `combine` from `ScoringStage::score`
```rust
// Source: src/detect/mod.rs ScoringStage::score lines 199-217 [CITED: existing code]
// Extract the sum+sigmoid so tune's backtest and the live run share ONE function.
pub fn combine(weights: &[f64], bias: f64, xs: &[f64]) -> f64 {
    debug_assert_eq!(weights.len(), xs.len());
    let z = bias + weights.iter().zip(xs).map(|(w, x)| w * x).sum::<f64>();
    1.0 / (1.0 + (-z).exp())
}
// ScoringStage::score then calls combine(&self.weights, self.bias, &collected_xs)
// instead of inlining the loop — a pure refactor with the existing tests guarding it.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| linfa 0.7.x + ndarray 0.15 | linfa 0.8.x + ndarray 0.16 | linfa 0.8.0 (2025-09-30) | Use 0.8.1; ndarray must be 0.16 (not 0.15, not 0.17). |
| ndarray latest 0.16 | ndarray latest 0.17.2 | ndarray 0.17 (2025-2026) | Do NOT adopt 0.17 — linfa 0.8 pins 0.16 (Pitfall 1). |

**Deprecated/outdated:**
- linfa-logistic 0.3.0 docs (top of some search results) — far behind; use 0.8.1 API (`params()`,
  `intercept()`, `Dataset::new`, `LogisticRegression::default().alpha(..).max_iterations(..)`).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Default `alpha = 1.0`, `max_iterations = 100` are sensible logistic hyperparameters | Pattern 1 | LOW — tunable; D-05 hands hyperparameters to Claude's discretion. Synthetic fixtures will reveal a too-strong/too-weak L2. |
| A2 | Default review-queue `sample_size = 100` | Pattern 3 | LOW — config-able per D-05; only affects how much a human reviews. |
| A3 | The strict D-06 gate = **zero** new FN and **zero** new FP (absolute, not a ratio) | Pattern 4 | MEDIUM — if the user intended a tolerance band, the gate would block too aggressively. D-06's wording ("must remain flagged … must remain unflagged") supports the strict reading; confirm in discuss/plan. |
| A4 | τ (`_threshold`) is NOT re-fit by the logistic step; left untouched | Pattern 2 | MEDIUM — logistic regression fits weights+bias, not the decision threshold. If the user wants τ tuned too, that is a separate (out-of-scope) step. |
| A5 | `tune` trains+backtests from the latest `done` run's signals | Pattern 2 / Pitfall 5 | LOW — `--run-id` overrides; matches the `export` default-run convention. |
| A6 | The `label` table is empty in all real DBs (no migration) | Runtime State Inventory | LOW — verified no `INSERT INTO label` in `src/`; a stale dev DB is a manual dev convenience only. |

## Open Questions

1. **Should τ (`_threshold`) ever be re-tuned, or only the layer weights + bias?**
   - What we know: logistic regression fits weights + intercept; τ is a separate decision threshold.
   - What's unclear: whether D-06's "adopt" includes a τ sweep.
   - Recommendation: keep τ fixed in Phase 6 (re-fit weights + `_bias` only). A τ-tuning step is a clean
     v2 follow-up. Confirm in planning (A4).

2. **Strict vs tolerant backtest gate.**
   - What we know: D-06 says confirmed-spam must remain flagged and confirmed-ham must remain unflagged.
   - What's unclear: zero-tolerance vs "no worse than the current weights."
   - Recommendation: implement zero-new-FN-and-zero-new-FP (strict, FP-averse). The report can also show
     net change vs current weights for context, but the *gate* is strict (A3).

## Environment Availability

> `tune` is a pure-SQLite + in-process-math subcommand. No network, no external services.

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Rust toolchain | build | ✓ | rustc/cargo 1.96.0 | — |
| `linfa`/`linfa-logistic`/`ndarray` | the fit | ✓ (crates.io) | 0.8.1/0.8.1/0.16.1 | — |
| C compiler / system BLAS | — | not needed | — | pure-Rust default (no BLAS) |
| SQLite | store | ✓ (rusqlite `bundled`) | bundled | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** none. linfa pulls argmin (pure Rust); no system libs required.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Rust built-in `#[test]` + `tempfile` dev-dep (existing convention) |
| Config file | none — `cargo test`; tests live in `#[cfg(test)] mod tests` per module |
| Quick run command | `cargo test --lib tune` |
| Full suite command | `cargo test` |

All tests use a temp-FILE SQLite DB (`tempfile::TempDir`, never `:memory:` — WAL sidecars), matching the
established `store::tests::temp_db` / `export::tests::temp_db` idiom. Fixtures are synthetic (D-08): seed
`signal` + `backpropagation` rows directly on a write connection (mirroring `export::tests::seed_scored_pubkey`),
no real labels, no network.

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| TUNE-01 | `backpropagation` table created with the right shape; a direct INSERT round-trips | unit | `cargo test --lib store::tests::open_creates_wal_and_schema` (update array) + a new `backpropagation_insert_roundtrip` | ❌ Wave 0 (rename + new test) |
| TUNE-02 | seed signals+labels (both classes), run `tune`, assert `weight` rows updated with `tuned_at`/`tuned_from_run` | unit | `cargo test --lib tune::tests::tune_writes_weights_with_provenance` | ❌ Wave 0 |
| TUNE-02 | same fixture twice → identical weights (deterministic fit, D-08/OPS-02) | unit | `cargo test --lib tune::tests::fit_is_deterministic` | ❌ Wave 0 |
| TUNE-03 | `run_batch` snapshots latest weights into `run.config_json` (already passing) | unit | `cargo test --lib run::tests::snapshot_records_tau_and_weights` | ✅ exists — add a confirming assertion that a *retuned* weight set appears in the next run's snapshot |
| TUNE-04 | `review_queue` contains a deterministic sample of unflagged pubkeys; re-run → same sample | unit | `cargo test --lib tune::tests::review_queue_samples_unflagged_deterministically` | ❌ Wave 0 |
| TUNE-05 | seed a fixture where the new fit would unflag a confirmed-spam (or flag a confirmed-ham); assert the gate BLOCKS and the `weight` table is UNCHANGED | unit | `cargo test --lib tune::tests::backtest_blocks_regression_and_leaves_weights` | ❌ Wave 0 |
| TUNE-05 | clean fixture (separable classes) → backtest PASSES, weights adopted | unit | `cargo test --lib tune::tests::backtest_passes_and_adopts` | ❌ Wave 0 |
| (guard) | degenerate single-class labels → clear precondition error, no write (Pitfall 3) | unit | `cargo test --lib tune::tests::single_class_labels_blocks_with_message` | ❌ Wave 0 |
| (guard) | missing signal for a layer → feature defaults to 0.0 (Pitfall 4) | unit | `cargo test --lib tune::tests::sparse_signals_default_to_zero` | ❌ Wave 0 |

**How to construct the "known regression" fixture (TUNE-05):** seed labels + signals such that the
*current* weights correctly classify all labeled pubkeys, but the freshly-fit weights (e.g. with strong L2
shrinking a discriminative layer's weight toward 0, or a deliberately adversarial label set) flip at least
one confirmed-spam below τ or one confirmed-ham above τ. Simplest deterministic construction: seed a
labeled-ham pubkey whose signals are high on a layer the new fit would up-weight, so the staging score
crosses τ → a new FP → gate blocks. Assert post-`tune` that `weight` rows still carry the *pre-tune*
values (and `tuned_at` is still NULL / unchanged).

### Sampling Rate
- **Per task commit:** `cargo test --lib tune` (the new module's tests, < 5s — the fit over a handful of
  synthetic rows is instant).
- **Per wave merge:** `cargo test` (full suite — guards the `combine` refactor + schema-rename test edits).
- **Phase gate:** full suite green before `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `src/tune.rs` — the new module (join read, feature matrix, fit, backtest gate, weight write,
  review_queue population) and its `#[cfg(test)] mod tests`.
- [ ] `src/store/schema.rs` — rename `label`→`backpropagation`; add `review_queue` DDL.
- [ ] `src/store/mod.rs` + `src/detect/mod.rs` — update the `"label"` references in existing tests
  (table-list assertion + the `no_enforcement_side_effect` counts/message).
- [ ] `src/lib.rs` — declare `pub mod tune;`.
- [ ] `Cargo.toml` — add the Phase-6 dep block (linfa/linfa-logistic/ndarray).
- [ ] `src/detect/mod.rs` — extract `pub fn combine(...)` and call it from `ScoringStage::score` (guarded
  by existing `single_layer_sigmoid_and_subscore` / `score_is_deterministic` tests).
- [ ] Optional: `tune.review_sample_size` + hyperparameter fields in `config.rs` (Claude's discretion).

## Security Domain

> ASVS L1 (local single-operator CLI; trusted operator input — consistent with the Phase 5 posture
> "operator-supplied `--config`/`--db` paths are trusted, T-05-04 / V5").

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | Local CLI, no auth surface. |
| V3 Session Management | no | No sessions. |
| V4 Access Control | no | Single local operator; OS file permissions guard the SQLite file. |
| V5 Input Validation | yes | The `tune` SQL reads labels/signals; **every value bound with `params![]`/`?N`, never `format!`-interpolated** (the project's T-05 rule). Human labels are integers/hex pubkeys; `is_spam` is read as `i64 != 0` (any non-0/1 is treated as spam — benign, operator-trusted). |
| V6 Cryptography | no | No crypto in Phase 6. |

### Known Threat Patterns for the tuner

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via a pubkey/label value | Tampering | All binds parameterized (`params![]`); the `tune` join + weight UPDATE use `?N` only — matches the existing `writer.rs` / `export.rs` discipline. |
| A bad fit silently adopting harmful weights | Tampering (integrity of verdicts) | The backtest gate (TUNE-05) is the control: weights are written only on PASS; a regression blocks and is reported. This is a *correctness* interlock with a security flavor (it protects the integrity of the suspected-spammer list). |
| Label poisoning (operator enters wrong labels) | Tampering | Out of trust scope (operator is trusted); `backpropagation.source`/`note` give an audit trail (leakage/poisoning forensics). |
| Logging event content during tune | Information disclosure | The tuner reads only sub-scores + labels (numbers + pubkeys), never event content; the report names pubkeys + counts, not content (consistent with T-05-07). |

## Sources

### Primary (HIGH confidence)
- `cargo info linfa@0.8.1` / `linfa-logistic@0.8.1` / `ndarray@0.16` — versions, repos, features
  (`default = []`, BLAS optional). `[VERIFIED]`
- `cargo add linfa@0.8 linfa-logistic@0.8 ndarray@0.16` throwaway resolution — confirmed the compatible
  set: linfa 0.8.1, linfa-logistic 0.8.1, ndarray 0.16.1, argmin 0.11.0. `[VERIFIED]`
- `gsd-tools query package-legitimacy check --ecosystem crates linfa linfa-logistic ndarray` — all `OK`,
  real rust-ml / rust-ndarray repos. `[VERIFIED]`
- rust-ml/linfa master `algorithms/linfa-logistic/src/lib.rs` — builder methods (`alpha`,
  `gradient_tolerance`, `max_iterations`, `with_intercept`, `initial_params`), L-BFGS + More–Thuente,
  **no RNG / deterministic**, `params()`/`intercept()` extraction, `Dataset::new`. `[VERIFIED]`
- The codebase itself (`schema.rs`, `detect/mod.rs`, `run.rs`, `store/mod.rs`, `export.rs`, `model.rs`,
  `config.rs`, `main.rs`) — read in full; the rename refs, the existing `combine` math, the TUNE-03
  snapshot, and the `weight` provenance columns. `[VERIFIED: codebase grep + read]`

### Secondary (MEDIUM confidence)
- docs.rs/linfa-logistic/0.8.1 (FittedLogisticRegression: `params() -> &Array1<F>`, `intercept() -> F`,
  `set_threshold`, `predict_probabilities`). `[CITED]`
- crates.io API for linfa-logistic (version 0.8.1, publish dates). `[CITED]`
- WebSearch: linfa 0.8 ↔ ndarray 0.16 compatibility; linfa default features pure-Rust. `[CITED]`

### Tertiary (LOW confidence)
- LogRocket "Machine learning in Rust using Linfa" — general usage shape only; superseded by the
  verified source for exact signatures.

## Metadata

**Confidence breakdown:**
- Standard stack (linfa/ndarray versions + compatibility): HIGH — verified by actual cargo resolution + legitimacy seam.
- Architecture (`tune` as a sync SQLite+math subcommand; fit→gate→write): HIGH — derived from the read codebase + linfa source.
- Rename (the four exact references + empty-table fact): HIGH — exhaustive grep of `src/`.
- TUNE-03 (already implemented): HIGH — read `run.rs` snapshot code + the passing `snapshot_records_tau_and_weights` test.
- Hyperparameters (alpha/max_iterations) + sample size: MEDIUM — defaults are reasonable but D-05 leaves them to discretion (logged as assumptions).
- Gate strictness + τ-not-refit: MEDIUM — strong textual support in D-06, flagged as Open Questions for plan confirmation.

**Research date:** 2026-06-26
**Valid until:** 2026-07-26 (stable crates; linfa releases are infrequent — re-verify ndarray pin if linfa bumps to 0.9).
