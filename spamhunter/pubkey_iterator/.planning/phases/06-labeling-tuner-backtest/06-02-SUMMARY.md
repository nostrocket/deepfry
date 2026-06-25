---
phase: 06-labeling-tuner-backtest
plan: 02
subsystem: ml-tuning
tags: [linfa, linfa-logistic, ndarray, logistic-regression, backtest, sqlite, clap, rust]

# Dependency graph
requires:
  - phase: 06-01
    provides: "backpropagation table, weight table (+ tuned_at/tuned_from_run provenance, _bias/_threshold sentinels, read_weights/seed_weights_if_empty/weight_write_conn), detect::combine() shared logistic combiner, linfa 0.8.1 / linfa-logistic 0.8.1 / ndarray 0.16.1 deps, export::latest_done_run + seed_scored_pubkey fixture pattern"
provides:
  - "src/tune.rs: run_tune (the offline logistic tuner) — signal × backpropagation join read → fixed-order feature matrix → deterministic linfa-logistic fit → STRICT backtest gate → provenance write-on-PASS"
  - "labeled_features: sparse EAV pivot (missing (pubkey,layer) → 0.0)"
  - "backtest: STRICT zero-new-FN-AND-zero-new-FP gate re-scoring via the shared detect::combine"
  - "TuneReport (Adopted / BlockedByRegression / BlockedByPrecondition) + summary()"
  - "sign-canonicalized fit (weights always oriented toward the spam class)"
  - "[tune] config section (alpha / max_iterations / review_sample_size, serde(default))"
  - "sync Tune clap arm (no tokio)"
affects: [06-03, review-queue, negative-sampling, weight-tuning, enforcement]

# Tech tracking
tech-stack:
  added: []  # all deps (linfa/linfa-logistic/ndarray) were added in 06-01
  patterns:
    - "Sign-canonical logistic extraction: read model.labels().pos.class and flip (weights,bias) so the fit always predicts the spam (==1) class — order-independent, combine() == predict_probabilities of spam"
    - "Staging-then-gate adoption: fit into an in-memory staging weight set; touch the weight table ONLY on a strict backtest PASS (a blocked adoption is a pure no-op on live state)"
    - "Shared-combiner backtest: the gate re-scores via detect::combine (never a private sigmoid) so it measures the exact function live scoring applies"
    - "Sync (no-tokio) CLI arm mirroring Export for pure-SQLite + in-process-math subcommands"

key-files:
  created:
    - "src/tune.rs"
  modified:
    - "src/lib.rs"
    - "src/main.rs"
    - "src/config.rs"
    - "pubkey_iterator_config.example.toml"

key-decisions:
  - "Sign-orient the fitted weights to the spam class via model.labels().pos.class — linfa-logistic assigns its POSITIVE_LABEL to the first-counted class (NOT the value-sorted larger class as the docstring claims), so naive params() extraction over a balanced fixture is row-order-dependent and could write sign-inverted weights"
  - "Default tune.alpha = 0.01 (NOT RESEARCH A1's 1.0): L2=1.0 over a handful of labeled rows over-shrinks the coefficients so far that even strongly-separable classes collapse below τ; 0.01 separates a synthetic fixture while still penalising over-large weights"
  - "τ (_threshold) is NOT re-fit (Open Q1) — logistic regression fits layer weights + bias only; the backtest reads the live τ from the _threshold row, which is never rewritten"
  - "STRICT backtest gate (Open Q2 / D-06): zero new FN AND zero new FP, absolute — any regression blocks adoption"

patterns-established:
  - "Sign-canonical logistic extraction (see tech-stack)"
  - "Staging-then-gate weight adoption (no-op on block)"
  - "Shared-combiner backtest (single-source scoring math)"

requirements-completed: [TUNE-02, TUNE-05]

# Metrics
duration: 11min
completed: 2026-06-25
status: complete
---

# Phase 6 Plan 02: Logistic Tuner + Strict Backtest Gate Summary

**A `tune` subcommand that fits a deterministic linfa-logistic model over the signal × backpropagation join, then writes new layer weights + bias to the `weight` table with provenance ONLY when a strict zero-new-FN/zero-new-FP backtest (re-scored through the shared `detect::combine`) passes — a regression blocks adoption as a pure no-op on live state.**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-25T18:10:01Z
- **Completed:** 2026-06-25T18:21:10Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 5 (1 created: src/tune.rs; 4 modified)

## Accomplishments
- `labeled_features` pivots the sparse `signal × backpropagation` EAV join for one run into dense feature vectors in the fixed `LAYERS` order; a missing `(pubkey, layer)` signal defaults to 0.0 (Pitfall 4).
- Deterministic `linfa-logistic` fit over the fixed feature-column order with a single-class precondition guard; the fit is sign-canonicalized to the spam class so the output weights always orient the way live scoring expects.
- STRICT backtest gate (TUNE-05 / D-06): re-scores every labeled pubkey with the STAGING weights via the SHARED `detect::combine` (identical math to live scoring); any new false negative OR new false positive blocks adoption.
- Provenance write-on-PASS: on a clean backtest, UPDATEs the four layer rows + `_bias` row with `tuned_at`/`tuned_from_run` in one transaction on `weight_write_conn`; `_threshold` (τ) is never touched. A blocked adoption leaves every weight row byte-unchanged.
- Sync `Tune` clap arm (no tokio) dispatching `run_tune`; optional `[tune]` config hyperparameters.

## Task Commits

Each task was committed atomically (TDD: test → feat):

1. **Task 1 (RED): failing tests for fit + sparse features + single-class guard** - `c2841f8` (test)
2. **Task 1 (GREEN): labeled_features pivot + deterministic linfa fit + single-class guard** - `5ddbd54` (feat)
3. **Task 2 (RED): failing tests for backtest gate + provenance write + no-op-on-block** - `45e50ab` (test)
4. **Task 2 (GREEN): backtest gate + provenance write-on-pass + sync Tune clap arm** - `488c9c6` (feat)
5. **Docs: document the optional [tune] section in the example config** - `2b75c2e` (docs)

## Files Created/Modified
- `src/tune.rs` (created, ~420 lines incl. tests) - `run_tune`, `labeled_features`, `fit` (sign-canonical), `backtest`, `write_weights`, `TuneReport`/`Regression`, `LAYERS` const, 6 tests.
- `src/lib.rs` - declares `pub mod tune` with a module doc.
- `src/main.rs` - sync `Tune { run_id }` clap arm + a parse test.
- `src/config.rs` - `TuneConfig` (`alpha`/`max_iterations`/`review_sample_size`) wired onto `Config.tune` with `#[serde(default)]`.
- `pubkey_iterator_config.example.toml` - documents the optional `[tune]` section.

## Decisions Made
- **Sign canonicalization (correctness, not cosmetic):** linfa-logistic orients `params()`/`intercept()` toward `labels().pos.class`, which is the *first-counted* class (its `label_classes` tiebreak is count-based, contradicting the "smaller class" docstring). For a balanced both-class fixture the positive class is therefore row-order-dependent. `fit` reads `model.labels().pos.class` and negates `(weights, bias)` when the positive class is not spam (==1), so the returned weights always predict spam and `combine()` equals `predict_probabilities` of the spam class.
- **`tune.alpha` default 0.01** (deviation from RESEARCH A1's 1.0) — L2=1.0 over a few labeled rows over-regularizes the fit flat; 0.01 lets a separable set separate. Exposed as config so it can rise as the labeled corpus grows.
- **τ not re-fit / strict gate** — resolves RESEARCH Open Q1 (weights + bias only) and Open Q2 (zero-tolerance gate), both as the plan specified.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Sign-canonicalize the logistic fit to the spam class**
- **Found during:** Task 2 (the `tune_writes_weights_with_provenance` test surfaced a fully-inverted fit — spam scored ~0.01, ham ~0.99).
- **Issue:** The plan's and RESEARCH's `model.params()`/`intercept()` extraction takes the coefficients at face value. linfa-logistic 0.8.1's `label_classes` assigns `POSITIVE_LABEL` to the *first-counted* class with a count-based tiebreak (the source contradicts its own "smaller class by PartialOrd" docstring). With a balanced fixture the positive class depends on row order, so over the pubkey-sorted join (ham rows first) the raw `params()` were oriented toward ham — sign-inverted relative to live scoring, which would write weights that flag ham and clear spam.
- **Fix:** Read `model.labels().pos.class`; when it is not the spam label (==1), negate the weights and bias before returning, so the tuner's output is always spam-oriented and order-independent.
- **Files modified:** src/tune.rs (`fit`).
- **Verification:** `tune_writes_weights_with_provenance` + `backtest_passes_and_adopts` now pass; `fit_is_deterministic` still passes (the orientation is deterministic). Confirmed empirically against linfa source (`label_classes`, `predict_probabilities`).
- **Committed in:** `488c9c6` (Task 2 GREEN commit).

**2. [Rule 2 - Missing Critical] Lower the default L2 (`tune.alpha`) from 1.0 to 0.01**
- **Found during:** Task 2 (the pass-case fixtures regressed all rows under alpha=1.0).
- **Issue:** RESEARCH A1's suggested `alpha = 1.0` over-shrinks the coefficients of a small labeled set so far that strongly-separable classes all fall below τ — the backtest can never pass on a realistic synthetic set, defeating the adoption path.
- **Fix:** Default `tune.alpha = 0.01` (documented as a deliberate departure from A1) exposed via the `[tune]` config section so it remains tunable.
- **Files modified:** src/config.rs, pubkey_iterator_config.example.toml.
- **Verification:** `backtest_passes_and_adopts` + `tune_writes_weights_with_provenance` pass (separable fixture adopts); config tests still green.
- **Committed in:** `488c9c6` (Task 2 GREEN) + `2b75c2e` (docs).

---

**Total deviations:** 2 auto-fixed (1 correctness bug, 1 missing-critical hyperparameter).
**Impact on plan:** Both were necessary for the adoption path to function correctly and safely. The sign fix is load-bearing for verdict integrity (T-06-03). No scope creep — both stay within `src/tune.rs` + `config.rs`.

## Issues Encountered
- The linfa-logistic class-label sign convention (above) was the only non-obvious obstacle; resolved by reading the crate source rather than trusting the docstring.

## User Setup Required
None - `tune` is a pure-SQLite + in-process-math subcommand. No external service configuration required. Operators run `pubkey_iterator tune [--run-id N]`; the optional `[tune]` config section is documented in `pubkey_iterator_config.example.toml`.

## Next Phase Readiness
- The complete, safe re-tuning mechanism (fit + strict backtest interlock) is in place. Plan 06-03 can build the `review_queue` negative-sampling population on top (the `tune.review_sample_size` config field is already provisioned for it).
- `weight` rows now carry real tuned provenance after a passing `tune`; a subsequent `run` will snapshot the retuned weights (TUNE-03 traceability, already implemented in `run_batch`).
- No blockers.

## Threat Surface
- T-06-02 (Tampering, tune SQL): mitigated — every value bound with `params![]`/`?N`; the join SELECT + weight UPDATE interpolate nothing. Verified `grep` shows no `format!` inside any SQL string.
- T-06-03 (Tampering, verdict integrity): mitigated — the strict backtest gate is the control; the sign-canonicalization fix is part of this control (a sign-inverted fit would have silently corrupted verdicts).

No new threat surface beyond the plan's `<threat_model>`.

## Self-Check: PASSED

- Created files verified on disk: `src/tune.rs`, `06-02-SUMMARY.md`.
- All task commits verified in git history: `c2841f8`, `5ddbd54`, `45e50ab`, `488c9c6`, `2b75c2e`.
- Full gate green: `cargo test` (108 lib + 2 bin, 0 failed), `cargo clippy --all-targets -- -D warnings` (clean), `cargo build` (ok). 102 pre-existing tests stay green.

---
*Phase: 06-labeling-tuner-backtest*
*Completed: 2026-06-25*
