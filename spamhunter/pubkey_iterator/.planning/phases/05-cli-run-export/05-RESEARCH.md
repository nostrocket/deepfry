# Phase 5: CLI `run` + `export` - Research

**Researched:** 2026-06-26
**Domain:** Rust CLI orchestration (clap derive), end-to-end batch wiring (tokio↔flume↔std::thread↔rusqlite), SQLite materialized snapshot table, reproducibility snapshotting
**Confidence:** HIGH (the foundations are all in-tree and read directly; the only new external dep, `clap`, is the de-facto Rust standard verified against crates.io)

## Summary

Phase 5 is almost entirely a **wiring + surface** phase, not a new-capability phase. Every hard part already exists in the codebase and is unit-tested: `enumerate::run` (the resumable `authors` walk), `pipeline::run_pipeline` + `production_fetch_with_whitelist` (the bounded tokio→flume→std::thread fetch path with L0 resolution), `detect::ScoringStage` (the combiner + persist consumer), `config::load`, and the `Store` single-writer with `score`/`signal`/`run`/`weight` tables. The Phase-4 tests already demonstrate the **exact `run` consumer body** — `let p = stage.score(run_id, &input.group.author, &input.group.events, input.whitelisted); store.persist(p)` — but only inside `#[cfg(test)]`. This phase's headline job (the MD-02 fix) is to lift that wiring out of tests into `main.rs` so a human can actually drive it.

The CLI is `clap` 4.6.1 (derive API), a top-level `#[derive(Parser)]` with a `#[command(subcommand)]` enum carrying `Run { resume: bool }` and `Export { run_id: Option<i64> }`, plus two global args (`--config`, `--db`). `export` is a pure-SQLite operation: a `CREATE TABLE IF NOT EXISTS suspected_spammer` keyed by `(run_id, pubkey)` populated by one `INSERT … SELECT … FROM score WHERE run_id=?1 AND suspected=1`, stamping the τ and a weight-snapshot reference read from the chosen run's `run.config_json`. Per-layer evidence is **never duplicated** — it stays JOINable from `signal`.

**Primary recommendation:** Add `clap = { version = "4", features = ["derive"] }` (the only Phase-5 dep), build a 2-subcommand derive CLI in `src/main.rs`, add a `src/run.rs` module that composes the four existing subsystems into one `run_batch(store, config) -> Result<i64, …>` (returning the `run_id`), and a `src/export.rs` module with `materialize_suspected(store, run_id) -> Result<usize, …>`. Snapshot τ+weights into `run.config_json` at `begin_run` time (replacing the current `"{}"` placeholder). Use **plain `eprintln!` progress** (a periodic "processed N pubkeys" counter) — do NOT add `indicatif`; the corpus is a long unattended batch where a stderr counter is sufficient and avoids a dep that fights the existing `eprintln!` logging.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| CLI arg parsing / subcommand dispatch | CLI (`main.rs`) | — | Entry point; owns process exit codes |
| Config resolution (path → `Config`) | CLI (`main.rs`) | `config::load` | CLI resolves the default `~/deepfry/...` path; the lib parses |
| `run` orchestration (enumerate→fetch→score→persist) | Orchestration (`run.rs`, new) | `enumerate` + `pipeline` + `detect` + `store` | Composes existing subsystems; owns the tokio runtime + consumer thread lifecycle |
| Async fetch + L0 whitelist resolution | I/O (tokio, `pipeline::production_fetch_with_whitelist`) | `graphql` + `detect::whitelist` | Already built; HTTP stays off the CPU consumer |
| CPU scoring + persist | CPU (`std::thread` consumer, `detect::ScoringStage`) | `store` writer actor | Already built; deterministic positional-Vec sum |
| τ + weight snapshot recording | Storage (`run.config_json`) | `detect::read_weights` | Reproducibility contract; written once at run start |
| `export` materialization | Storage (`export.rs`, new) | `Store` reader + a short-lived write conn | Pure SQL `INSERT…SELECT`; no compute |
| Progress reporting | CLI/Orchestration (`eprintln!`) | — | Operator feedback only; not persisted |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `clap` | 4.6.1 | Derive-API CLI parser (subcommands, global args, `--help`/`--version`) | The de-facto Rust CLI crate; derive API is idiomatic, zero-boilerplate, used by cargo itself [VERIFIED: crates.io / `cargo info clap`] |

**Installation:**
```bash
cargo add clap --features derive
# → clap = { version = "4", features = ["derive"] }
```

`clap` 4.6.1 `rust-version` (MSRV) is **1.85** per `cargo info clap` [VERIFIED: `cargo info clap`]. This repo targets Go… no — the workspace CLAUDE.md notes Go for sibling projects; this Rust crate is `edition = "2021"`. The build host already compiles the existing deps; confirm `rustc --version` ≥ 1.85 in Wave 0 (it almost certainly is on a 2026 toolchain). If an older pinned toolchain is in use, `clap = "=4.5.x"` (MSRV 1.74) is the fallback. [ASSUMED — toolchain version not probed this session; see Assumptions A1]

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| (none new) | — | — | All other needs are met by in-tree deps (`tokio`, `flume`, `rusqlite`, `serde_json`, `toml`) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `clap` derive | `clap` builder API | Builder is more verbose for no benefit here; derive is the documented default [CITED: docs.rs/clap] |
| `clap` | `argh` / `pico-args` | Lighter, but CONTEXT D-01 **locks `clap`** — not a research question |
| `eprintln!` progress | `indicatif` 0.18.4 | `indicatif` is excellent for interactive TTY bars but adds a dep + a draw-target/throughput concern for a long unattended batch that already logs via `eprintln!`; **recommend plain `eprintln!`** (dep discipline, D-01 says clap is the *only* Phase-5 dep). [VERIFIED: crates.io for the version; recommendation is a judgment call — see Discretion] |

## Package Legitimacy Audit

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `clap` | crates.io | ~8 yrs (4.6.1 pub 2026-04-15) | ~400M+ all-time (top-10 crate) | github.com/clap-rs/clap | OK | Approved — the one Phase-5 dep |
| `indicatif` | crates.io | mature (0.18.4 pub 2026-02-14) | very high | github.com/console-rs/indicatif | OK | NOT recommended (dep discipline) — listed only as the considered alternative |

**Packages removed due to [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

Both crates are long-established, MIT/Apache-licensed, with verified GitHub source repos and massive download counts. `clap` is the Rust ecosystem's standard CLI parser (the verdict is OK with high confidence). `indicatif` is also clean but is **not** being recommended for installation.

## Architecture Patterns

### System Architecture Diagram

```
                                  ┌─────────────────────────────────────┐
  argv ──► clap Parser ──► Cli ──►│ match cmd                            │
                                  │   ├─ Run { resume }                  │
                                  │   └─ Export { run_id }               │
                                  └───────────────┬─────────────────────┘
                                                  │
        ┌─────────────────────────────────────────┼──────────────────────────────────┐
        │  RUN path (run.rs)                       │   EXPORT path (export.rs)         │
        │                                          │                                   │
config::load(~/deepfry/..config.toml)        Store::open(db)                           │
        │                                          │                                   │
   Store::open(db)                          pick run_id (arg or latest done run)       │
        │                                          │                                   │
   seed_weights_if_empty(store,cfg)         read run.config_json  (τ + weight snapshot) │
        │                                          │                                   │
   read_weights ─► snapshot JSON {τ, weights}      │                                   │
        │                                   CREATE TABLE IF NOT EXISTS suspected_spammer│
   begin_run(snapshot_json) ─► run_id              │                                   │
        │                                   DELETE FROM suspected_spammer               │
   ┌────▼─────────── tokio runtime ───────────┐      WHERE run_id=?  (idempotent)       │
   │ enumerate::run(store,client,resume)      │            │                            │
   │   walks authors → pubkey table (durable) │   INSERT INTO suspected_spammer         │
   │                                          │     SELECT pubkey,score,τ,run_id …      │
   │ read_pubkeys(conn)  (durable source)     │     FROM score                          │
   │            │                             │     WHERE run_id=? AND suspected=1      │
   │   run_pipeline(                          │            │                            │
   │     fetch = production_fetch_with_       │   COMMIT ─► N rows materialized          │
   │              whitelist(client,wl,…),     │            │                            │
   │     pubkeys, CAP, APC,                   │   eprintln "exported N pubkeys for run R"│
   │     consumer = |inp| store.persist(      │   (reviewer: SELECT … JOIN signal …)    │
   │        stage.score(run_id, author,       │                                         │
   │        events, inp.whitelisted)))        │                                         │
   │     │  tokio fetch ─► bounded flume ─►   │                                         │
   │     │  std::thread consumer ─► score     │                                         │
   │     ▼                                    │                                         │
   │  score + signal rows (Store writer actor)│                                         │
   └──────────┬───────────────────────────────┘                                        │
              │                                                                          │
   mark_run_done(run_id, max_lev_end)   (enumerate already does this)                    │
   store.close()  (flush + join writer)                                                  │
        └──────────────────────────────────────────────────────────────────────────────┘
```

### Recommended Project Structure
```
src/
├── main.rs          # clap Cli + Commands enum; dispatch; process exit codes (REPLACES current --resume stub)
├── run.rs           # NEW: run_batch() — composes enumerate + pipeline + detect + store into one batch
├── export.rs        # NEW: materialize_suspected() — the INSERT…SELECT into suspected_spammer
├── lib.rs           # add `pub mod run; pub mod export;`
├── config.rs        # (unchanged) — load() already path-arg based
├── enumerate.rs     # (unchanged) — enumerate::run is reused as-is
├── pipeline.rs      # (unchanged) — run_pipeline + production_fetch_with_whitelist reused
├── detect/          # (unchanged) — ScoringStage::from_config, seed_weights_if_empty, read_weights
└── store/
    ├── schema.rs    # ADD `suspected_spammer` CREATE TABLE to SCHEMA_DDL (8th table)
    └── …            # (otherwise unchanged)
```

### Pattern 1: clap derive — top-level Parser + Subcommand enum + global args
**What:** A `#[derive(Parser)]` struct holding global options and a `#[command(subcommand)]` enum field. Each subcommand is an enum variant whose fields are its args.
**When to use:** Any multi-subcommand CLI (this is the canonical clap-4 shape).
**Example:**
```rust
// Source pattern: docs.rs/clap derive tutorial (verified shape; field-level details cited)
use clap::{Parser, Subcommand};
use std::path::PathBuf;

/// pubkey_iterator — the Nostr pubkey spam classifier.
#[derive(Parser, Debug)]
#[command(version, about, long_about = None)]
struct Cli {
    /// Path to the TOML config (default: ~/deepfry/pubkey_iterator_config.toml).
    #[arg(long, global = true)]
    config: Option<PathBuf>,

    /// Path to the SQLite store (default: spamhunter.sqlite).
    #[arg(long, global = true, default_value = "spamhunter.sqlite")]
    db: PathBuf,

    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand, Debug)]
enum Commands {
    /// Run a full batch: enumerate → fetch → score → persist.
    Run {
        /// Resume the latest unfinished run from its stored cursor.
        #[arg(long)]
        resume: bool,
    },
    /// Materialize the suspected-spammer snapshot for a run into SQLite.
    Export {
        /// Which run to export (default: the latest completed run).
        #[arg(long)]
        run_id: Option<i64>,
    },
}
```
- `global = true` makes `--config`/`--db` accepted before OR after the subcommand [CITED: docs.rs/clap `Arg::global`].
- `--resume` is a bare bool flag folded into `Run`, threading straight through to `enumerate::run(store, client, resume)` (the existing Phase-2 semantic — D-03). No `--run-id` on `run`; resume reuses the latest unfinished run automatically (see `enumerate::run` resume branch).
- The default config path is resolved in `main` (clap can't expand `~`); use `dirs`-free manual `std::env::var("HOME")` join, or accept that the operator passes `--config`. **Recommend** resolving `~/deepfry/pubkey_iterator_config.toml` via `std::env::home_dir`-equivalent (`std::env::var_os("HOME")`) since no `dirs` dep is wanted. [ASSUMED — home resolution approach; see A2]

### Pattern 2: `run` orchestration — lift the Phase-4 test wiring into production
**What:** A single `async fn run_batch` (or a sync fn that builds a `tokio` runtime) that strings the four subsystems together. The **exact consumer closure already exists** in `pipeline.rs` tests (`normal_pubkey_persists_score_and_evidence`, `zero_event_pubkey_gets_score_row`, `rerun_endtoend_is_deterministic`) — this phase makes it the production path (the MD-02 fix: scoring was only reachable from tests).
**When to use:** The `Run` command body.
**Example:**
```rust
// Source: composed from pipeline.rs tests (zero_event_pubkey_gets_score_row,
// rerun_endtoend_is_deterministic) — the production analogue of the tested wiring.
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use pubkey_iterator::{config::Config, store::Store};
use pubkey_iterator::detect::{ScoringStage, read_weights, seed_weights_if_empty, ScoredInput};
use pubkey_iterator::graphql::GraphQlClient;
use pubkey_iterator::detect::whitelist::WhitelistClient;
use pubkey_iterator::pipeline::{run_pipeline, production_fetch_with_whitelist,
    DEFAULT_CHANNEL_CAP, DEFAULT_AUTHORS_PER_CALL};
use pubkey_iterator::store::queries::read_pubkeys;

/// One full end-to-end batch. Returns the run_id so `export` (or the caller) can
/// stamp/select against it. Drives the tokio runtime + the std::thread consumer
/// already encapsulated inside run_pipeline.
pub async fn run_batch(store: &Store, config: &Config, resume: bool) -> Result<i64, RunError> {
    // 1. Seed the weight table from config on first run (no-op if already seeded),
    //    then read the live weights → snapshot τ + weights into run.config_json.
    seed_weights_if_empty(store, config)?;
    let weights = read_weights(&store.reader()?)?;
    let snapshot = serde_json::json!({
        "tau": /* from _threshold row or config.tau */,
        "bias": /* from _bias row or config.bias */,
        "weights": weights,            // serde-serializable model::Weight rows
        "adapter_url": config.adapter_url,
        "channel_cap": DEFAULT_CHANNEL_CAP,
        "authors_per_call": DEFAULT_AUTHORS_PER_CALL,
    }).to_string();

    // 2. enumerate leg — reuses Phase-2 resume semantics (D-03). NOTE: enumerate::run
    //    calls begin_run("{}") itself today; see Pitfall 1 for the run_id/snapshot seam.
    let client = GraphQlClient::new(config.adapter_url.clone());
    pubkey_iterator::enumerate::run(store, &client, resume).await?; // pubkey table durable

    // 3. The scoring run_id — carries the snapshot. (begin_run with the snapshot JSON.)
    let run_id = store.begin_run(&snapshot)?;

    // 4. Build the stage from config + the seeded weights.
    let stage = Arc::new(ScoringStage::from_config(config, &weights));

    // 5. The bounded pipeline: tokio fetch (events + L0) → flume → std::thread consumer.
    let whitelist = Arc::new(WhitelistClient::new(config.whitelist_url.clone()));
    let pubkeys = read_pubkeys(&store.reader()?)?;          // durable enumeration source (D-07)
    let processed = Arc::new(AtomicU64::new(0));

    let store_c = /* an Arc<Store> or &Store usable in the 'static consumer */;
    let stage_c = Arc::clone(&stage);
    let counter = Arc::clone(&processed);
    let consumer = move |input: &ScoredInput| {
        let p = stage_c.score(run_id, &input.group.author, &input.group.events, input.whitelisted);
        store_c.persist(p);                                  // single-writer actor
        let n = counter.fetch_add(1, Ordering::Relaxed) + 1; // progress (see Pattern 3)
        if n % 1000 == 0 { eprintln!("run {run_id}: scored {n} pubkeys"); }
    };

    let client_f = Arc::new(client);
    let wl_f = Arc::clone(&whitelist);
    let fetch = move |batch: Vec<String>| {
        let client_f = Arc::clone(&client_f);
        let wl_f = Arc::clone(&wl_f);
        async move { production_fetch_with_whitelist(&client_f, &wl_f, /*kind*/1, /*per_author*/100, &batch).await }
    };

    run_pipeline(fetch, pubkeys, DEFAULT_CHANNEL_CAP, DEFAULT_AUTHORS_PER_CALL, consumer).await?;
    eprintln!("run {run_id}: scored {} pubkeys total", processed.load(Ordering::Relaxed));
    Ok(run_id)
}
```
**Load-bearing notes:**
- The consumer must be `Send + Sync + 'static` (it runs on the dedicated `std::thread`, see `run_pipeline` bound). The `Store` must therefore be shared as `Arc<Store>` into the closure, **or** the closure must capture a clone of the `flume::Sender` rather than the `Store`. The Phase-4 tests use `Arc<Store>` and then `Arc::try_unwrap(store)…close()` after the pipeline returns (the sole-ref unwrap works because the consumer thread is joined inside `run_pipeline` before it returns). **Reuse that exact pattern** — see `zero_event_pubkey_gets_score_row` lines ~692–745.
- `store.close()` MUST be called after `run_pipeline` returns to flush+join the writer actor (durability). With `Arc<Store>`, unwrap then close.
- `kind`/`per_author` for `production_fetch_with_whitelist`: the Phase-4 tests pass `(1, 5)`; production should fetch kind-1 with `per_author ≈ 100` per INGEST-02 ("most-recent ~100 events"). **These are not in `Config`** — they are pipeline constants today. Decide whether to hardcode (kind=1, per_author=100) or add to config; recommend hardcode in Phase 5 (config additions are deferrable). [ASSUMED — see A3]

### Pattern 3: Progress to completion via `eprintln!` counter
**What:** An `AtomicU64` incremented in the consumer, with a modulus-gated `eprintln!` and a final total. No new dependency.
**When to use:** The `run` command (long unattended batch over the full corpus).
**Why not `indicatif`:** A progress *bar* needs a known total to render a meaningful percentage; the corpus size is only known after enumeration (a `SELECT count(*) FROM pubkey`). A counter that prints "scored N pubkeys" every 1000 (and a final total) gives the operator liveness without a TTY-redraw dependency, matches the codebase's existing `eprintln!` logging idiom (enumerate.rs:206, main.rs:29), and honors D-01 ("clap is the *only* Phase-5 dep"). If a bar is later wanted, `indicatif` 0.18.4 with `ProgressBar::new(count_from_pubkey_table)` is the drop-in. [Recommendation — see Discretion]

### Anti-Patterns to Avoid
- **Double `begin_run`:** `enumerate::run` *itself* calls `store.begin_run("{}")` (enumerate.rs:141–145). If `run_batch` ALSO calls `begin_run` for the scoring leg you get TWO run rows, and the scoring `run_id` differs from the enumeration `run_id`. See Pitfall 1 for the resolution — do NOT blindly `begin_run` twice without a deliberate decision about which run_id carries the snapshot and which the scores reference.
- **Blocking the tokio reactor with the consumer:** never run `store.persist`/scoring on a tokio worker. `run_pipeline` already isolates the consumer on a `std::thread` — keep it there.
- **Duplicating evidence into `suspected_spammer`:** the snapshot table holds the verdict (pubkey, score, τ, run_id), NOT the per-layer evidence. Evidence stays in `signal` and is JOINed at read time (D-05).
- **`format!`-interpolating values into SQL:** the codebase mandate (T-01-01, writer.rs/queries.rs) is `params![]`/`?N` binding. The `export` `INSERT…SELECT` binds `run_id` and the τ literal as `?N`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CLI arg parsing, `--help`, `--version`, subcommands | A hand `std::env::args()` matcher (the Phase-2 stub) | `clap` derive | The stub (`main.rs:23`) is explicitly a placeholder; clap gives help/usage/error UX for free (D-01) |
| Bounded fetch→score concurrency | A new tokio/channel/thread setup | `pipeline::run_pipeline` | Already built, watermark-tested for bounded memory (INGEST-03) |
| Whitelist L0 resolution + caching | A new HTTP client | `production_fetch_with_whitelist` + `WhitelistClient` | Already built, fail-safe, per-run cached (DETECT-01) |
| Scoring + persist | New combiner/writer | `ScoringStage` + `Store::persist` | Already built, deterministic (SCORE-01/02) |
| Snapshot selection of "above τ" | New scoring at export time | `SELECT … WHERE suspected=1` | `score.suspected` already records "score > τ at run time" (schema.rs:34) — export just materializes it |

**Key insight:** The `score` table already has a `suspected` boolean column computed against τ at run time. `export` is a **materialize of an already-computed flag**, not a recomputation. This is why the snapshot is point-in-time-correct (D-06): the run that produced the row used the τ snapshotted in `run.config_json`.

## Runtime State Inventory

> This is NOT a rename/refactor/migration phase. It adds a CLI surface and one new table. No external runtime state (datastores, OS registrations, secrets, build artifacts) is renamed or migrated.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | The existing SQLite `spamhunter.sqlite` gains a new `suspected_spammer` table via `CREATE TABLE IF NOT EXISTS` (additive; existing data untouched) | Schema add only — no migration |
| Live service config | None — `run` reads the adapter (:8080) and whitelist (:8081) but writes nothing to them (read-only by design, OPS Out-of-Scope) | None |
| OS-registered state | None | None |
| Secrets/env vars | `LMDB2GRAPHQL_URL` (Phase-2 override) becomes superseded by `config.adapter_url`; `WHITELIST_URL` likewise by `config.whitelist_url`. Decide whether to keep env overrides for the live test path | Code decision only — see A4 |
| Build artifacts | Adding `clap` triggers a `Cargo.lock` update + a recompile; the `[[bin]]` stays `pubkey_iterator` | `cargo build` regenerates |

**Verified by:** reading `main.rs`, `Cargo.toml`, `store/schema.rs`, and the OPS "Out of Scope" table (no strfry mutation).

## Common Pitfalls

### Pitfall 1: Two `run` rows — enumerate's `begin_run` vs the scoring `begin_run`
**What goes wrong:** `enumerate::run` creates/continues its OWN run row (`begin_run("{}")` or `latest_unfinished_run`, enumerate.rs:138–145) for the enumeration leg and stamps `config_json="{}"`. If `run_batch` separately calls `begin_run(snapshot)` for scoring, you have two runs; the snapshot lands on a run with no scores, and the scored rows reference a run with `config_json="{}"` — breaking the reproducibility contract (D-04/D-06).
**Why it happens:** `enumerate::run` was written for Phase 2 (enumerate-only) where it legitimately owns the run. Phase 5 wants ONE run that spans enumerate+score with the snapshot attached.
**How to avoid — recommended approach:** Use **one run_id for the whole batch.** Two viable designs:
  1. **Snapshot-then-enumerate-into-it:** add a `store.set_run_config_json(run_id, json)` updater (mirrors `set_run_cursor`) OR change `enumerate::run` to accept a pre-made `run_id`/snapshot string instead of always `begin_run("{}")`. Then enumerate, then score into the SAME run_id, then `mark_run_done`.
  2. **Update after enumerate:** let `enumerate::run` create the run as today, then in `run_batch` read back that `run_id` (`latest_unfinished_run` or the just-finished run), `UPDATE run SET config_json=?` with the snapshot, score into it. Simpler but enumerate currently `mark_run_done`s the run on clean termination — a *done* run can't be the scoring target cleanly.
  **Recommend approach (1):** thread a snapshot string into the enumerate leg. Minimal change: add `Store::set_run_config_json` and call it right after `begin_run`, OR widen `enumerate::run`'s signature to take `config_json: &str` (it already passes `"{}"` to `begin_run` at line 141/144 — change those two call sites). This keeps a single run_id from enumerate through scoring through done. **This is a real design decision for the planner — flag it as a Task-0 spike or an explicit plan decision.** [ASSUMED — current code couples enumerate to its own begin_run; see A5]
**Warning signs:** `export` finds zero `suspected` rows because scores went to a different run_id than the one the operator exports; or `run.config_json` reads `"{}"` for the scored run.

### Pitfall 2: `enumerate::run` marks the run `done` before scoring happens
**What goes wrong:** On clean enumeration, `enumerate::run` calls `mark_run_done` (enumerate.rs:241). If scoring uses that same run_id afterward, the run is already `done` — and a later `--resume` would skip it. If scoring uses a NEW run_id, see Pitfall 1.
**Why it happens:** Phase-2 `enumerate::run` is a complete lifecycle (begin→walk→done) on its own.
**How to avoid:** Either (a) the scoring run_id is distinct and `run_batch` calls `mark_run_done` on it after scoring (then enumerate's run is just the "enumeration provenance" — but that revives Pitfall 1's two-run problem), or (b) refactor so `done` is marked only after BOTH enumerate and score complete. **Recommend:** the cleanest model is that enumerate populates the durable `pubkey` table (its real job, D-07) and the SCORING run is the canonical run carrying the snapshot + scores + `done`. The relationship between the enumeration walk's run-state (cursor/drift/resume) and the scoring run is the key wiring decision. The planner should treat "unify the run lifecycle across enumerate+score" as the central `run` task.
**Warning signs:** `--resume` re-enumerates a corpus that was already fully walked; or scores attach to a `done` run.

### Pitfall 3: Consumer closure ownership of `Store` (the `'static` bound)
**What goes wrong:** `run_pipeline`'s `C: Fn(&ScoredInput) + Send + Sync + 'static` means the consumer can't borrow a `&Store` from the stack. A naive `move |inp| store.persist(...)` moves the `Store` into the thread and you can't `close()` it afterward.
**Why it happens:** The consumer runs on a detached `std::thread` joined inside `run_pipeline`.
**How to avoid:** Wrap the store as `Arc<Store>`, clone into the consumer, and after `run_pipeline().await` returns (consumer thread already joined) do `Arc::try_unwrap(store).ok().expect("sole ref").close()?`. This is **exactly** the pattern in `pipeline.rs:743` (`zero_event_pubkey_gets_score_row`) and `:844` (`rerun_endtoend_is_deterministic`). Copy it.
**Warning signs:** Borrow-checker error `closure may outlive the current function`, or a panic in `Arc::try_unwrap` (means a stray clone survived — ensure the `fetch` closure does NOT also hold the store).

### Pitfall 4: `export` against a run with no scores / not-yet-run
**What goes wrong:** `export --run-id 7` where run 7 was aborted mid-enumeration (no scores) silently materializes zero rows; the reviewer thinks "no spammers" when really "no run."
**Why it happens:** `INSERT…SELECT` over an empty source is valid and produces 0 rows.
**How to avoid:** Before the `INSERT…SELECT`, check the run exists and is `done` (or at least has score rows); print a clear message ("run 7 has 0 scored pubkeys — did `run` complete?"). For the default "latest" selection, pick `SELECT max(run_id) FROM run WHERE status='done'` (not just `max(run_id)`, which could be a half-finished run). Return the materialized count so the CLI can report it.
**Warning signs:** export reports "0 suspected" for a corpus known to contain spam.

### Pitfall 5: Idempotent re-export
**What goes wrong:** Running `export --run-id 7` twice inserts duplicate rows if the table isn't keyed/cleared.
**How to avoid:** Key `suspected_spammer` on `(run_id, pubkey)` and either `DELETE FROM suspected_spammer WHERE run_id=?1` before the INSERT, or use `INSERT … ON CONFLICT(run_id,pubkey) DO UPDATE` (mirrors the codebase's UPSERT idiom). Recommend DELETE-then-INSERT inside one transaction for a clean point-in-time snapshot.

## Code Examples

### `suspected_spammer` schema (add to `store/schema.rs` SCHEMA_DDL)
```sql
-- Source: designed for this phase; mirrors the existing schema's FK + key conventions.
CREATE TABLE IF NOT EXISTS suspected_spammer (
  run_id        INTEGER NOT NULL REFERENCES run(run_id),
  pubkey        TEXT    NOT NULL REFERENCES pubkey(pubkey),
  score         REAL    NOT NULL,          -- the fused score at run time (≥ τ)
  tau           REAL    NOT NULL,          -- the τ this run used (snapshot, D-06)
  rank          INTEGER NOT NULL,          -- 1 = highest score (review ordering)
  exported_at   INTEGER NOT NULL,          -- unix secs of materialization
  PRIMARY KEY (run_id, pubkey)
);
CREATE INDEX IF NOT EXISTS idx_suspected_run ON suspected_spammer(run_id, rank);
```
**Design rationale:**
- **Keyed by `(run_id, pubkey)`** — it is a per-run materialization, NOT a separate table per run (D-05/D-06: "stamped with the run_id", "point-in-time per run"). One table, many runs, queryable by run.
- `score` + `tau` are **denormalized into the row** so a reviewer sees the verdict and its threshold without joining `run.config_json`. The full weight snapshot is NOT duplicated — it lives in `run.config_json`; the row carries `run_id` as the reference to it. (CONTEXT Discretion: "JSON column vs join to a per-run weight snapshot" — recommend **τ inline + weight snapshot by run_id join**, the minimal-duplication choice.)
- `rank` is convenience (highest-score-first review); computed at export via `ROW_NUMBER() OVER (ORDER BY score DESC)`.
- Per-layer reasons are **NOT here** — `signal` is JOINed: `SELECT s.pubkey, s.score, sig.layer, sig.value, sig.evidence FROM suspected_spammer s JOIN signal sig USING (run_id, pubkey) WHERE s.run_id = ?`.

### `export` materialization (`export.rs`)
```rust
// Source: designed for this phase; uses the run_write-conn template + params![] binding.
use rusqlite::{params, Connection};

/// Materialize the suspected-spammer snapshot for `run_id` into `suspected_spammer`.
/// Idempotent: clears the run's prior rows, then re-inserts from `score WHERE suspected=1`.
/// Returns the number of rows materialized.
pub fn materialize_suspected(conn: &mut Connection, run_id: i64) -> rusqlite::Result<usize> {
    // τ snapshot for this run: read the run's config_json (the D-06 snapshot).
    let tau: f64 = read_tau_from_run_snapshot(conn, run_id)?; // parse run.config_json.tau

    let tx = conn.transaction()?;
    tx.execute("DELETE FROM suspected_spammer WHERE run_id = ?1", params![run_id])?;
    let n = tx.execute(
        "INSERT INTO suspected_spammer (run_id, pubkey, score, tau, rank, exported_at)
         SELECT run_id, pubkey, score, ?2,
                ROW_NUMBER() OVER (ORDER BY score DESC) AS rank,
                ?3
         FROM score
         WHERE run_id = ?1 AND suspected = 1",
        params![run_id, tau, now_epoch_secs()],
    )?;
    tx.commit()?;
    Ok(n)
}

/// Default run selection: the latest COMPLETED run (Pitfall 4 — not just max(run_id)).
pub fn latest_done_run(conn: &Connection) -> rusqlite::Result<Option<i64>> {
    conn.query_row(
        "SELECT max(run_id) FROM run WHERE status = 'done'",
        [], |r| r.get::<_, Option<i64>>(0),
    )
}
```
- `ROW_NUMBER() OVER (...)` is a SQLite window function — available since SQLite 3.25 (2018); the bundled `rusqlite` SQLite is far newer. [VERIFIED: rusqlite bundled SQLite >> 3.25; the codebase already uses modern SQLite features]
- `export` needs a **write connection**. The store's writer actor doesn't expose arbitrary SQL; use the `run_write_conn`/`weight_write_conn` pattern (a short-lived `Connection::open` with PRAGMAs) — recommend adding a sibling `Store::export_write_conn()` (or generalize the existing private helper) that touches only `suspected_spammer`, preserving the single-writer invariant for `score`/`signal`/`pubkey` (mirrors the `weight_write_conn` justification, store/mod.rs:204).

### `main.rs` dispatch skeleton
```rust
// Source: clap derive + the existing tokio::main pattern (main.rs:20).
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cli = Cli::parse();
    let config_path = cli.config.unwrap_or_else(default_config_path); // ~/deepfry/...
    match cli.command {
        Commands::Run { resume } => {
            let config = pubkey_iterator::config::load(&config_path)?;
            let store = Store::open(&cli.db)?;
            let rt = tokio::runtime::Runtime::new()?;
            let run_id = rt.block_on(run::run_batch_arc(store_arc, &config, resume))?;
            eprintln!("run complete: run_id={run_id}");
        }
        Commands::Export { run_id } => {
            let store = Store::open(&cli.db)?;
            let mut conn = store.export_write_conn()?;
            let rid = run_id
                .or(export::latest_done_run(&conn)?.into())
                .ok_or("no completed run to export")?;
            let n = export::materialize_suspected(&mut conn, rid)?;
            eprintln!("exported {n} suspected pubkeys for run {rid}");
            store.close()?;
        }
    }
    Ok(())
}
```
Note `export` does NOT need the tokio runtime (pure SQLite); only `run` does. Build the runtime inside the `Run` arm (or keep `#[tokio::main]` and make `export` a sync call within the async main — either works; building the runtime only for `run` is marginally cleaner).

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand `std::env::args().any(\|a\| a=="--resume")` (Phase-2 stub) | `clap` 4 derive (`Parser`/`Subcommand`) | This phase | Real `--help`, subcommand UX, typed args |
| Scoring reachable only from `#[cfg(test)]` (MD-02) | `run_batch` drives scoring in production | This phase | The deliverable actually runs |
| `begin_run("{}")` placeholder config_json | `begin_run(snapshot_json)` with τ+weights | This phase | Reproducibility contract (D-04/D-06) |

**Deprecated/outdated:** none relevant — `clap` 4.x is current (4.6.1, Apr 2026).

## Project Constraints (from CLAUDE.md)

- **Single project scope:** all work stays in `spamhunter/pubkey_iterator` — do not touch sibling deepfry projects.
- **Commit to `main`, no feature branches** (workspace memory + CLAUDE.md).
- **Never write to `~/deepfry/` files in tests** — config-loading tests use `tempfile::TempDir` (config.rs honors this via path-arg `load`).
- **Read-only toward strfry/adapter** — `run` never mutates the adapter or whitelist; it only reads (OPS Out-of-Scope: no strfry mutation, no live enforcement).
- **Rust deps land in their owning phase** (Cargo.toml header comment) — `clap` is the Phase-5-owned add; do NOT add `linfa`/`gaoya` (Phase 6) here.
- **`params![]`/`?N` SQL binding only** (T-01-01) — the `export` INSERT…SELECT binds run_id/τ as `?N`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The build toolchain is ≥ Rust 1.85 (clap 4.6.1 MSRV) | Standard Stack | Build fails; mitigation: pin `clap = "=4.5"` (MSRV 1.74). Verify `rustc --version` in Wave 0. |
| A2 | `~` config path resolved via `std::env::var_os("HOME")` join (no `dirs` dep) | Pattern 1 | Wrong path on non-HOME envs; mitigation: require `--config` or document HOME assumption |
| A3 | `run` fetches kind=1, per_author≈100 hardcoded (not config) | Pattern 2 | If other kinds matter, scores miss events; INGEST-02 says "~100 events" so 100 is reasonable; planner may add to config |
| A4 | Env overrides (`LMDB2GRAPHQL_URL`/`WHITELIST_URL`) superseded by config; may keep for live tests | Runtime State Inventory | Live D-09/D-14 tests use those env vars (enumerate.rs:467, whitelist.rs:331) — keep them for tests, use config for the CLI |
| A5 | `enumerate::run` currently owns `begin_run` and `mark_run_done`; unifying one run_id across enumerate+score requires a signature/seam change | Pitfall 1/2 | The central `run` design decision; if mis-handled, scores and snapshot land on different runs. **Highest-risk item — planner must resolve explicitly.** |
| A6 | `clap = "4"` resolves to 4.6.1 and the derive feature is `derive` | Standard Stack | Verified via `cargo info clap`; low risk |

## Open Questions (RESOLVED)

1. **One run_id or two across enumerate+score?** (A5)
   - What we know: `enumerate::run` creates+finishes its own run; scoring needs a run carrying the τ/weight snapshot.
   - What's unclear: whether to refactor `enumerate::run` to accept a pre-made run_id/snapshot, or to update the run row post-enumerate, or to use a distinct scoring run.
   - Recommendation: Make the SCORING run canonical; thread the snapshot JSON into the run that scores write to, and unify `mark_run_done` to fire after scoring. Treat as the first `run` task (possibly a tiny spike). Lowest-churn concrete option: widen `enumerate::run(store, client, resume, config_json: &str)` and move `mark_run_done` out of enumerate into `run_batch` after scoring.

2. **Does `score.whitelisted`/`suspected` semantics need any export filtering beyond `suspected=1`?**
   - What we know: `suspected = score > τ` is computed at run time (detect/mod.rs:218).
   - What's unclear: whether whitelisted pubkeys should be excluded from the suspected list. D-03 says whitelist presence "clears only L0", and a whitelisted pubkey can still be suspected via content layers — so the list SHOULD include them (no special exclusion). Confirm with the planner; default = `WHERE suspected=1` with no whitelist filter.

3. **Should `export` also stamp the weight-snapshot hash inline** (vs only the run_id join)?
   - Recommendation: run_id join is sufficient (the snapshot is immutable in `run.config_json`). A redundant inline hash is optional polish; skip in Phase 5.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `clap` crate | CLI parsing | ✓ (crates.io) | 4.6.1 | pin `=4.5` if MSRV < 1.85 |
| Rust toolchain | build | ✓ (assumed; A1) | ≥1.85 needed for clap 4.6.1 | older clap pin |
| LMDB2GraphQL adapter | live `run` | ✓ per CONTEXT | `http://192.168.149.21:8080/graphql` | live test self-skips on outage (existing pattern) |
| whitelist-plugin | live `run` (L0) | ✓ per CONTEXT | `http://127.0.0.1:8081` | `WhitelistClient` fails toward not-flagging |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** the adapter/whitelist — a transient outage degrades the live test to a deferred manual check (D-07 in CONTEXT), never a block. The unit/integration tests use loopback stubs and need neither service.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Rust built-in `#[test]` (`cargo test`) + `tempfile` dev-dep (temp-FILE DBs) |
| Config file | none — `cargo test` |
| Quick run command | `cargo test --lib export` (and `run`-module unit tests) |
| Full suite command | `cargo test` |

The codebase's established test idiom is loopback `TcpListener` HTTP stubs (StubServer in enumerate.rs, omitting_stub/whitelist_stub in pipeline.rs) + `tempfile::TempDir` temp-FILE SQLite. Phase 5 reuses these verbatim — no new test infrastructure.

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SCORE-03 / SC (export) | `export` materializes pubkeys with `suspected=1` into `suspected_spammer`, keyed by run_id, stamped with τ | unit | `cargo test --lib export::tests::materialize_selects_suspected` | ❌ Wave 0 |
| SCORE-03 (idempotent) | Re-export of a run leaves one row per `(run_id,pubkey)`, second wins | unit | `cargo test --lib export::tests::reexport_is_idempotent` | ❌ Wave 0 |
| SCORE-03 (evidence joinable) | A `suspected_spammer` row JOINs to its `signal` evidence (no duplication) | unit | `cargo test --lib export::tests::evidence_joinable_from_signal` | ❌ Wave 0 |
| SCORE-03 (default selection) | `export` with no `--run-id` picks the latest `done` run, not a half-finished one | unit | `cargo test --lib export::tests::default_picks_latest_done_run` | ❌ Wave 0 |
| OPS-01 / SC (run e2e) | `run` over a small seeded/mocked corpus enumerates→fetches→scores→persists `score`+`signal`, marks the run `done`, snapshot in `config_json` | integration | `cargo test --lib run::tests::run_batch_endtoend_mocked` | ❌ Wave 0 |
| OPS-01 (CLI parse) | clap parses `run --resume`, `export --run-id N`, global `--config`/`--db` | unit | `cargo test --lib cli::tests::parses_subcommands` (use `Cli::try_parse_from`) | ❌ Wave 0 |
| OPS-01 (reproducibility) | `run.config_json` for the scored run contains τ + the weight set | unit | `cargo test --lib run::tests::snapshot_records_tau_and_weights` | ❌ Wave 0 |
| OPS-01 (live, must_have) | A live full `run` against adapter :8080 + whitelist :8081 scores ≥1 pubkey; self-skips on outage (deferred manual) | integration (self-skipping) | `cargo test --lib run::tests::live_run_self_skipping -- --ignored` or env-gated | ❌ Wave 0 |

**Mock corpus for the `run` integration test:** reuse `omitting_stub` (adapter) + `whitelist_stub` from pipeline.rs tests, seed a handful of pubkeys via `store.insert_pubkeys`, point `run_batch` at the stub URLs, assert `score`/`signal`/`suspected_spammer` row counts and the `run.config_json` snapshot. The Phase-4 test `zero_event_pubkey_gets_score_row` is the direct template.

### Sampling Rate
- **Per task commit:** `cargo test --lib <module>::tests` (the touched module's tests, < 5s).
- **Per wave merge:** `cargo test` (full suite, all in-tree unit/integration tests).
- **Phase gate:** `cargo test` green + a manual live `run` against :8080/:8081 producing a non-empty `suspected_spammer` (the D-07 live proof; self-skip degrades to deferred).

### Wave 0 Gaps
- [ ] `src/export.rs` — module + `export::tests` (4 unit tests above)
- [ ] `src/run.rs` — module + `run::tests` (e2e mocked, snapshot, live-self-skipping)
- [ ] `src/main.rs` — `cli::tests` for `Cli::try_parse_from` argument parsing
- [ ] `store/schema.rs` — add `suspected_spammer` table (and its index) to `SCHEMA_DDL`; the existing `open_creates_wal_and_schema` test should be extended to assert the 8th table
- [ ] `Store::export_write_conn()` (or generalized short-lived write conn) — covered indirectly by the export tests
- [ ] Decide + implement the run-lifecycle unification (Open Q1 / A5) — the e2e test pins the contract

## Security Domain

> `security_enforcement` posture: this is a local single-operator batch CLI reading two operator-trusted services and writing a local SQLite file. ASVS L1 is the appropriate floor (per the project's existing threat-model tags T-01-*/T-04-*).

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | No auth surface; local CLI, no network listener |
| V3 Session Management | no | No sessions |
| V4 Access Control | no | Single local operator; SQLite file perms are the OS's |
| V5 Input Validation | yes | clap validates arg types; pubkeys already 64-hex-validated at the enumerate trust boundary (enumerate.rs `is_valid_pubkey`); config is operator-trusted (T-04-09) |
| V6 Cryptography | no | No crypto in this phase |
| V12/V13 (SQLi / API) | yes | `params![]`/`?N` binding everywhere (T-01-01) — the `export` INSERT…SELECT binds run_id/τ as parameters, never `format!` |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via run_id / τ | Tampering | `params![]` binding (existing codebase mandate T-01-01) — never interpolate |
| Path traversal via `--db`/`--config` | Tampering | Operator-supplied paths are trusted (local CLI); no untrusted-network path input |
| Malformed pubkey reaching the DB | Tampering | Already mitigated upstream — enumerate validates 64-hex before persisting (defense-in-depth, T-02-08) |
| Whitelist outage → mass false-positive | Availability/Repudiation | Already mitigated — `WhitelistClient` fails toward not-flagging (T-04-07) |
| Adapter snapshot drift mid-run | Repudiation | Already recorded — `run.max_lev_id_start/end` (D-09); reproducibility snapshot in `config_json` |

No new attack surface is introduced beyond a local CLI parsing operator args and adding one local table.

## Sources

### Primary (HIGH confidence)
- In-tree source read directly: `main.rs`, `enumerate.rs`, `pipeline.rs`, `detect/mod.rs`, `detect/whitelist.rs`, `config.rs`, `store/{mod,schema,queries,writer}.rs`, `model.rs`, `lib.rs`, `Cargo.toml`, `pubkey_iterator_config.example.toml` — the authoritative behavior of every subsystem `run`/`export` compose.
- `cargo info clap` → 4.6.1, MSRV 1.85, `derive` feature, repo github.com/clap-rs/clap [VERIFIED]
- `cargo info indicatif` → 0.18.4, repo github.com/console-rs/indicatif [VERIFIED]
- crates.io API (clap 4.6.1 pub 2026-04-15; indicatif 0.18.4 pub 2026-02-14) [VERIFIED]
- CONTEXT.md D-01..D-07 (locked decisions); REQUIREMENTS.md SCORE-03 / OPS-01

### Secondary (MEDIUM confidence)
- docs.rs/clap derive tutorial (Parser/Subcommand shape, `derive` feature, `global=true`) [CITED: docs.rs/clap]

### Tertiary (LOW confidence)
- HOME-based `~` config-path resolution approach (A2) — judgment, not verified against a doc

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — clap is locked by CONTEXT and verified against crates.io; no other dep needed.
- Architecture (run wiring): HIGH — the exact consumer/fetch/store wiring already exists and is tested in `pipeline.rs`; this phase lifts it to production.
- Export schema/SQL: HIGH — `score.suspected` is pre-computed; export is a materialize, and the SQL uses standard SQLite (window function, params binding).
- Run-lifecycle unification (one vs two run_ids): MEDIUM — a real design decision the planner must resolve (Open Q1 / A5); the code currently couples enumerate to its own begin_run/mark_run_done.
- Pitfalls: HIGH — derived directly from reading the existing code's invariants (`'static` consumer bound, double begin_run, done-before-score).

**Research date:** 2026-06-26
**Valid until:** 2026-07-26 (stable — clap 4.x is mature; the only volatility is the toolchain MSRV check, A1)
