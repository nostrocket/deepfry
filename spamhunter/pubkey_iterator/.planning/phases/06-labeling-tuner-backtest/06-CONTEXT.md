# Phase 6: Labeling + Logistic Tuner + Backtest Gate - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Source:** Front-loaded decisions (autonomous run)
**Mode:** mvp

<domain>
## Phase Boundary

The **correctability loop closes**: humans label confirmed spam and false
positives, an offline tuner re-fits layer weights from those labels, the next
run consumes them reproducibly, and **no weight change is adopted unless it
survives a backtest** against the full labeled set.

**In scope:** the `backpropagation` label table (renamed from `label`), a
`tune` subcommand that fits a logistic model and writes new weights, run-time
weight loading + snapshotting, the negative-sampling review queue, and the
backtest adoption gate.

**Out of scope:** any new detection layer (v2), live enforcement, and an
incremental service mode (SVC-01, v2).
</domain>

<decisions>
## Implementation Decisions

### Label capture (TUNE-01) — direct SQLite, self-explanatory naming
- **D-01:** **Rename the Phase-1 `label` table → `backpropagation`** (the name
  says what it does; the project standard is self-explanatory names). It keeps
  the well-named shape: `pubkey` (TEXT), `is_spam` (INTEGER — 1 spam / 0 ham),
  `labeled_at` (INTEGER unix ts); the nullable `source` and `note` columns are
  retained for leakage-audit / provenance (optional, may be left NULL). Update
  `src/store/schema.rs` and every reference. No real data exists yet, so this
  is a `CREATE TABLE` rename, not a data migration.
- **D-02:** Humans enter labels by **INSERTing directly into `backpropagation`
  with any SQLite client** (pubkey, is_spam, labeled_at). **There is no `label`
  CLI subcommand** — this is a deliberate, recorded deviation from OPS-01's
  literal "label subcommand" wording (the user chose direct-SQL entry). The
  engine only **reads** `backpropagation`.

### Tuner (TUNE-02) — linfa-logistic
- **D-03:** A **`tune` subcommand** fits a logistic model with
  **`linfa-logistic`** over the stored **`signal × backpropagation` join**
  (per-layer sub-scores as features, `is_spam` as label) and writes new layer
  **weights + bias + threshold** to the `weight` table with **provenance**
  recorded (`tuned_at`, `tuned_from_run`). `linfa-logistic` (+ `linfa`,
  `ndarray`) are the Phase-6-owned dependencies.

### Reproducibility (TUNE-03)
- **D-04:** Each **run reads the latest weights at startup** and **snapshots
  them into run metadata** (the Phase-5 run snapshot), so any past score is
  traceable to the exact weights that produced it.

### Negative sampling (TUNE-04)
- **D-05:** The review/labeling queue **includes a random sample of *unflagged*
  pubkeys** to counter selection bias — materialized as a **SQLite table/view**
  (self-explanatory name, e.g. `review_queue`) the human reads before entering
  labels. Sample size is Claude's discretion (a sensible default, config-able).

### Backtest gate (TUNE-05) — block on regression (FP-averse)
- **D-06:** Before **adopting** new weights, **backtest against the full
  `backpropagation` set**: confirmed-spam pubkeys must **remain flagged** (no
  new false negatives) AND confirmed-non-spam pubkeys must **remain unflagged**
  (no new false positives). A regression is **surfaced and BLOCKS adoption** of
  the new weights (the conservative, false-positive-averse default — matching
  the project's dominant-risk stance). The old weights stay in force on a
  blocked adoption; the regression detail is reported.

### Output medium
- **D-07:** **SQLite only.** Labels in `backpropagation`, weights in `weight`,
  review queue + backtest results in SQLite. No flat files.

### Verification posture
- **D-08:** Tuner + backtest are verified with **synthetic labeled fixtures**
  (seed `signal` + `backpropagation` rows, run `tune`, assert weights written
  with provenance; seed a known regression, assert the gate blocks). No real
  human labels are needed for the autonomous run. Determinism: the logistic fit
  is seeded so the same fixture yields the same weights.

### Claude's Discretion
- Negative-sample size, `linfa-logistic` hyperparameters (regularization,
  max-iter), the exact `review_queue` columns, and how "adoption" is gated
  mechanically (e.g. a staging weight set promoted only on a passing backtest).
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase 1 schema (to modify + read)
- `src/store/schema.rs` — **rename `label` → `backpropagation`** (D-01); read
  `signal` (features), `weight` (target of the tuner, with `tuned_at` /
  `tuned_from_run` provenance + `_bias`/`_threshold` sentinel rows), `score`
  (backtest inputs), `run` (weight snapshot per TUNE-03).
- `src/store/{mod,writer,queries}.rs` — single-writer store API; the tuner
  writes `weight` rows through it.

### Foundations
- Phase 4 combiner (`sigmoid(Σwᵢxᵢ + b)`) — the model the tuner re-fits and the
  backtest re-scores against. Phase 5 `run`/snapshot — where TUNE-03 weight
  loading hooks in.

### Project planning
- `.planning/ROADMAP.md` Phase 6 — goal + 4 success criteria.
- `.planning/REQUIREMENTS.md` — TUNE-01..05.
</canonical_refs>

<code_context>
## Existing Code Insights

- The `weight` table already has `tuned_at` + `tuned_from_run` provenance
  columns — purpose-built for TUNE-02/TUNE-03.
- The `label` table (Phase 1) is currently unused — renaming it to
  `backpropagation` now is free (no data). Its shape already matches the
  human-insert contract (pubkey / is_spam / labeled_at).
- `signal` EAV rows are the tuner's feature matrix (one column per layer);
  joined to `backpropagation` on pubkey gives the labeled training set.
- Determinism (OPS-02 spirit) extends here: a seeded logistic fit makes `tune`
  reproducible.
</code_context>

<specifics>
## Specific Ideas

- The backtest gate is the safety interlock: it exists specifically to stop a
  re-fit from quietly introducing false positives — the project's dominant
  risk. Block-on-regression, not warn-and-adopt.
- Negative sampling matters because labels are otherwise drawn only from
  flagged pubkeys (selection bias); the random unflagged sample keeps the
  logistic fit honest.
</specifics>

<deferred>
## Deferred Ideas

- **Live enforcement / feeding the whitelist or quarantine** — out of scope
  (separate later concern, high blast radius).
- **Incremental service mode (SVC-01)** and **direct LMDB reads (PERF-01)** —
  v2.
- **A `label` CLI subcommand** — intentionally dropped (D-02); labels are
  direct SQLite inserts.
</deferred>

---

*Phase: 6-Labeling + Logistic Tuner + Backtest Gate*
*Context front-loaded: 2026-06-25 (autonomous run)*
