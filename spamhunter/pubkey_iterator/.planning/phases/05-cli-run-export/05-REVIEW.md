---
phase: 05-cli-run-export
reviewed: 2026-06-25T17:40:42Z
depth: deep
files_reviewed: 7
files_reviewed_list:
  - src/main.rs
  - src/run.rs
  - src/export.rs
  - src/enumerate.rs
  - src/store/mod.rs
  - src/store/schema.rs
  - src/store/queries.rs
findings:
  blocker: 0
  high: 1
  medium: 2
  low: 2
  total: 5
status: resolved
resolution: HIGH-01 + MED-01 fixed (2026-06-26); MED-02 + LOW-01 + LOW-02 accepted/deferred
---

# Phase 5: Code Review Report — CLI / run / export

**Reviewed:** 2026-06-25T17:40:42Z
**Depth:** deep (cross-file: run_batch → enumerate → pipeline → store → export)
**Files Reviewed:** 7
**Status:** issues_found

## Summary

The two load-bearing invariants hold under trace:

- **Run-lifecycle unification — sound.** `enumerate::run` (src/enumerate.rs:142) does NOT mark the run done; it returns the `run_id` after the terminal flush barrier (src/enumerate.rs:250) and `set_run_max_lev_end` (src/enumerate.rs:262). `mark_run_done` fires exactly once, in `run_batch` after scoring (src/run.rs:190). One `run_id` spans `begin_run` → enumerate → score → done. The snapshot (τ + weights) is written to the SAME run row scoring persists to: fresh runs get it via `begin_run(config_json)` (src/enumerate.rs:160), resumed runs via `set_run_config_json` (src/enumerate.rs:154), and scoring persists to that returned `run_id`. A scoring failure (`run_pipeline` Err at src/run.rs:168) returns before `mark_run_done`, leaving the run `running` with `last_cursor` preserved — a later `--resume` re-selects it via `latest_unfinished_run` (status != 'done') and re-walks idempotently. Verified recoverable.

- **Export idempotency — mostly sound, one real determinism defect.** `materialize_suspected` (src/export.rs:75) is DELETE-then-INSERT per `run_id` inside one transaction, PK `(run_id, pubkey)`, predicate `suspected = 1`, fully `params!`-bound (no `format!` into SQL). τ is read from the run's own `config_json` snapshot (src/export.rs:37–62), not a stale global — and a missing τ errors loudly instead of materializing against a bogus threshold. The one real defect: the `rank` ordering has no tiebreaker, so it is NOT deterministic across re-exports when scores tie (HIGH-01).

τ-consistency cross-check passed: `ScoringStage::from_config` (src/detect/mod.rs:121) reads τ the same way `run_batch`'s snapshot does (src/run.rs:96), `suspected = score > tau` (src/detect/mod.rs:218), and export reads that same snapshot τ — no drift between scoring τ and exported τ.

Single-writer invariant holds: `export_write_conn`/`set_run_config_json`/`set_run_max_lev_end` are short-lived conns touching only `suspected_spammer` / the `run` row — never the actor's `score`/`signal`/`pubkey` tables. `Arc::try_unwrap` in src/run.rs:182 is safe in the success path: the consumer/fetch closures (the only other `Arc<Store>` clones) are dropped when `run_pipeline` returns (consumer thread joined at src/pipeline.rs:143, dropping its captured `store_c`).

## High

### HIGH-01: Export `rank` is non-deterministic on score ties (no tiebreaker)

**File:** `src/export.rs:87`
**Issue:** `ROW_NUMBER() OVER (ORDER BY score DESC)` has no secondary sort key. Score ties are not hypothetical here: `ScoringStage::score` (src/detect/mod.rs:217) produces a deterministic sigmoid, so any two pubkeys with identical layer outputs — e.g. every zero-event non-whitelisted pubkey (the D-15 path that `run_batch` deliberately scores) — get byte-identical scores. SQLite then assigns `ROW_NUMBER` among the tied rows in an unspecified internal order, which can differ between two `materialize_suspected` calls on the same data (different statement plan / page order) and is not guaranteed stable across SQLite versions. The module doc (src/export.rs:9, "score-DESC review rank") and the `reexport_is_idempotent` test only exercise a single-row case, so the tie instability is untested. `rank` is the headline ordering reviewers triage by; an unstable rank means two exports of one run can present a different "top of the list."

**Fix:** Add the PK column as a deterministic tiebreaker so the window is total-ordered:
```sql
ROW_NUMBER() OVER (ORDER BY score DESC, pubkey ASC) AS rank
```
Consider a regression test that seeds ≥2 suspected pubkeys at the same score and asserts a stable rank assignment across two `materialize_suspected` calls.

## Medium

### MED-01: `read_max_lev_end` collapses "run row missing" into the same `0` as "drift unrecorded"

**File:** `src/run.rs:189`
**Issue:** `read_max_lev_end(&store, run_id)?.unwrap_or(0)` flattens two distinct conditions to `0`: (a) the `run` row exists but `max_lev_id_end` is NULL (drift probe genuinely unrecorded), and (b) the `run` row does not exist at all (`.optional()` → `None` at src/run.rs:205). Case (b) is a "should never happen" — `run_batch` created the row — but if it ever did (e.g. an out-of-band delete, or a future refactor that changes the `run_id` between enumerate and mark), `mark_run_done` would silently UPDATE zero rows (src/store/mod.rs:208 `WHERE run_id = ?1` matches nothing) and `run_batch` would still return `Ok(run_id)` reporting "run complete" for a run that was never marked `done`. The error would surface only later as `export` finding no `done` run. This is a latent silent-failure, not a live bug.

**Fix:** Have `mark_run_done` assert it updated exactly one row, or have `run_batch` verify the run row exists before marking:
```rust
let rows = conn.execute("UPDATE run SET status='done', ... WHERE run_id = ?1", ...)?;
if rows != 1 { return Err(/* run row vanished */); }
```

### MED-02: `set_run_max_lev_end` / `mark_run_done` race the writer actor's `run`-row provenance via independent connections

**File:** `src/run.rs:189-191`, `src/store/mod.rs:206`
**Issue:** `mark_run_done` opens its own short-lived `run_write_conn` (src/store/mod.rs:207) and UPDATEs the `run` row. This is documented as safe because the writer actor "never writes the `run` table." That is true today, but the read-back in `read_max_lev_end` (src/run.rs:199 `store.reader()`) opens yet another connection and reads `max_lev_id_end` that was written by enumerate's `set_run_max_lev_end` on a THIRD connection (src/enumerate.rs:262). All three are sequential in `run_batch` (no concurrency between them here), so it works — but the `set_run_max_lev_end`-then-read-then-`mark_run_done`-rewrites-the-same-value dance (acknowledged "harmless" at src/run.rs:188 and src/store/mod.rs:181) is redundant write traffic that exists only to thread a value enumerate already wrote. If a future change makes the end-probe and done-mark concurrent, the last-writer-wins UPDATE has no guard.

**Fix:** Low-urgency. Either drop the enumerate-side `set_run_max_lev_end` and let `mark_run_done` be the sole writer of `max_lev_id_end` (the read-back in `run_batch` then becomes unnecessary), or have `mark_run_done` use `COALESCE(max_lev_id_end, ?3)` so it never clobbers a value enumerate recorded. Document that all `run`-row writes are strictly sequential within a single `run_batch`.

## Low

### LOW-01: `--db` default is a bare relative path, silently CWD-dependent

**File:** `src/main.rs:26`
**Issue:** `#[arg(long, global = true, default_value = "spamhunter.sqlite")]` resolves relative to the process CWD. `run` from one directory and `export` from another silently operate on two different SQLite files, producing a confusing "no completed run to export" with no hint that the path differs. The config path (src/main.rs:52) is anchored to `$HOME`; the DB path is not, so the two defaults have inconsistent anchoring.

**Fix:** Either anchor the default DB next to the config (`$HOME/deepfry/spamhunter.sqlite`) for consistency, or `eprintln!` the resolved absolute DB path at startup so an operator can see which file was opened.

### LOW-02: `read_tau_from_run_snapshot` reuses `InvalidColumnType` for non-column errors

**File:** `src/export.rs:44-49`, `src/export.rs:53-61`
**Issue:** A bad/absent τ snapshot is surfaced as `rusqlite::Error::InvalidColumnType(0, msg, ...)`. The error is semantically a JSON/data-integrity problem, not a column-type mismatch, and the column index `0` is fabricated. The `missing_tau_snapshot_errors` test only matches on the message substring, so the misused variant is invisible — but a caller that pattern-matches on `Error::InvalidColumnType` to handle real type mismatches would catch this too. Works today (message is correct); the variant is a misfit.

**Fix:** Prefer a domain error type for the export module (a `thiserror` enum with a `BadSnapshot { run_id, detail }` variant), or at minimum `rusqlite::Error::FromSqlConversionFailure` which is closer in intent. Cosmetic — the operator-facing message is already clear.

---

_Reviewed: 2026-06-25T17:40:42Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: deep_
