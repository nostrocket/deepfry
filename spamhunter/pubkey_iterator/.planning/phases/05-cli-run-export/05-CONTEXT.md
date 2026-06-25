# Phase 5: CLI `run` + `export` - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Source:** Front-loaded decisions (autonomous run)
**Mode:** mvp

<domain>
## Phase Boundary

A human can drive a **full batch** and get back the **suspected-spammer list** —
the first shippable, reviewable artifact — with each flagged pubkey accompanied
by its per-layer reasons and evidence. This is the CLI surface deferred from
Phase 2 (D-12) built out for real.

**In scope:** a `run` subcommand (full end-to-end batch:
enumerate → fetch → score → persist, with progress), an `export` subcommand
that materializes the suspected-spammer list into SQLite, and τ + weight-
snapshot recording for reproducibility.

**Out of scope (later phases):** labeling, tuning, and the backtest gate
(Phase 6); any new detection layers (v2).
</domain>

<decisions>
## Implementation Decisions

### CLI framework
- **D-01:** Use **`clap`** (derive API) for the CLI — the standard Rust choice;
  this is the phase that owns the full CLI surface (Phase 2 shipped only a
  minimal `--resume` entry point). `clap` is the Phase-5-owned dependency.
- **D-02:** Subcommands this phase: **`run`** and **`export`**. (`tune` is
  Phase 6; there is **no `label` subcommand** — see Phase 6, labels are entered
  by direct SQLite insert.) Names self-explanatory.

### `run` (OPS-01, SCORE-03 setup)
- **D-03:** `run` executes a **full batch over the corpus end-to-end** —
  enumerate (Phase 2) → fetch through the bounded pipeline (Phase 3) → score
  via the layers+combiner (Phase 4) → persist `score`/`signal` — and reports
  **progress to completion**. It reuses `--resume` (Phase 2 resume semantics)
  for the enumeration leg.
- **D-04:** `run` reads its config from
  `~/deepfry/pubkey_iterator_config.toml` (OPS-03): adapter URL, whitelist URL,
  weights, thresholds, τ. The run snapshots **τ + the weight set** into run
  metadata so any verdict is reproducible.

### `export` — SQLite only (SCORE-03/05)
- **D-05:** **SQLite only — no flat file.** `export` writes the suspected-
  spammer list into a **materialized per-run snapshot table** (self-explanatory
  name, e.g. `suspected_spammer`): the pubkeys whose score exceeds τ, stamped
  with the `run_id`, the τ used, and the weight snapshot. Per-layer reasons
  stay **joinable from the `signal` table** (which already holds sub-scores +
  evidence JSON per layer) — `export` does not duplicate evidence, it makes the
  flagged set queryable and reproducible.
- **D-06:** A reviewer reads the result with **any SQLite client** (query the
  snapshot table joined to `signal`). The snapshot is point-in-time per run, so
  τ/weight drift between runs never corrupts a past list.

### Verification posture
- **D-07:** `export` logic is unit-tested against a **seeded in-DB fixture**
  (insert synthetic `score`/`signal` rows, run export, assert the snapshot
  table contents). A **live end-to-end `run`** against the live adapter +
  whitelist is the integration proof; live services are reachable
  automatically, and a transient outage degrades to a deferred manual check,
  never a block.

### Claude's Discretion
- Exact `suspected_spammer` snapshot-table columns and how the weight snapshot
  is encoded (e.g. JSON column vs join to a per-run weight snapshot), progress-
  reporting style (counter/bar), and how `run` wires the three prior phases.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Foundations to wire together
- `src/enumerate.rs` (Phase 2 walk + `--resume`), the Phase-3 pipeline, the
  Phase-4 Layer/combiner stage, and `src/store/` — `run` orchestrates all four.
- `src/store/schema.rs` — `score` (has `suspected` flag + score), `signal`
  (per-layer evidence), `run` (metadata + config_json for τ/weight snapshot),
  `weight` (the snapshot source).

### Config & live services
- `~/deepfry/pubkey_iterator_config.toml` (OPS-03) — τ, weights, URLs.
- Adapter `http://192.168.149.21:8080/graphql`; whitelist
  `http://127.0.0.1:8081`.

### Project planning
- `.planning/ROADMAP.md` Phase 5 — goal + 3 success criteria.
- `.planning/REQUIREMENTS.md` — SCORE-03, OPS-01.
</canonical_refs>

<code_context>
## Existing Code Insights

- `score.suspected` already records "score > τ at run time" — `export` reads
  flagged rows directly; the snapshot table is a per-run materialization.
- `run.config_json` is the natural home for the τ + weight snapshot per run.
- The minimal Phase-2 binary (`--resume`) is upgraded to a full `clap` CLI
  here — `run`/`export` subcommands wrap the existing entry logic.
</code_context>

<specifics>
## Specific Ideas

- "Export" means **materialize into SQLite**, not write a file — the whole
  system is SQLite-native; reviewers and the Phase-6 tuner both read SQLite.
- Reproducibility is the contract: a `suspected_spammer` row must be traceable
  to the exact τ and weights that produced it (snapshot, not live view).
</specifics>

<deferred>
## Deferred Ideas

- **`label` / `tune` subcommands + backtest** — Phase 6 (labels via direct
  SQLite insert, no `label` subcommand).
- **Flat-file / JSON / CSV export** — explicitly out: SQLite only.
</deferred>

---

*Phase: 5-CLI run + export*
*Context front-loaded: 2026-06-25 (autonomous run)*
