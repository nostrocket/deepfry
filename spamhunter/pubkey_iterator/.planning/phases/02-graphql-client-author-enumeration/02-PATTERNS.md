# Phase 2: GraphQL Client + Author Enumeration - Pattern Map

**Mapped:** 2026-06-25
**Files analyzed:** 9 new/modified files
**Analogs found:** 6 / 9 (3 net-new modules have no in-repo analog — transport/HTTP layer is greenfield)

## Orientation

Phase 1 (`pubkey_iterator`) is a **pure library crate** (`src/lib.rs` exposes `mod model; mod store;`). There is **no `main.rs`, no `src/bin/`, no `tests/` integration dir** yet, and **no `.claude/skills/`**. The store is strictly synchronous (`rusqlite` + a single `flume` writer thread). Phase 2's only genuinely new surface is the async HTTP/GraphQL transport and the run-state SQL the store doesn't have yet; everything persistence-shaped has a strong in-repo analog. Cite these analogs by file + line below — do not invent symbol names; the verified signatures are reproduced here.

**Project constraints carried into every plan** (from CLAUDE.md + RESEARCH §"Project Constraints"):
- Single-writer invariant is load-bearing — never open a second write connection (see `store/mod.rs` lines 9-13, 66-69).
- All SQL binding parameterized `?N` / `params![]` — never `format!`-interpolate (T-01-01; see `writer.rs` lines 8-9, 63-75).
- Dependencies land in their owning phase — Phase 2 adds `reqwest`/`tokio` (+ optional `thiserror`/`clap`); `rayon` stays for Phase 3 (Cargo.toml comment lines 6-8).
- Commit to main, no feature branches.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `src/graphql/mod.rs` | module-root | — | `src/store/mod.rs` (module-root layout) | role-match (layout only) |
| `src/graphql/client.rs` | service (transport) | request-response | none (HTTP is greenfield) | no analog → RESEARCH Pattern 2 |
| `src/graphql/envelope.rs` | model | transform (deserialize) | `src/model.rs` (serde row-structs) | role-match |
| `src/graphql/queries.rs` | model + const | transform (deserialize) | `src/store/queries.rs` (const + struct conventions) | partial (naming/const idiom) |
| `src/enumerate.rs` | service (orchestrator) | streaming / cursor-walk | none (walk loop is greenfield) | no analog → RESEARCH §Pagination Loop |
| `src/store/mod.rs` (MODIFY) | store | CRUD (run-state read/update) | `Store::begin_run` lines 82-100 + `reader()` lines 129-132 | exact (same file) |
| `src/store/writer.rs` (MODIFY) | store | event-driven (writer actor) | `writer_loop` lines 45-81 + `UPSERT_PUBKEY` line 35 | exact (same file) |
| `src/store/queries.rs` (MODIFY, if reader route) | store | CRUD (read helper) | `read_scores` lines 9-17 | exact (same file) |
| `src/model.rs` (MODIFY, if enum-message route) | model | — | `Persist` lines 91-103 | exact (same file) |
| `src/main.rs` OR `src/bin/enumerate.rs` (NEW) | binary entry | request-response | none (no binary exists) | no analog → RESEARCH §entry point |
| `src/lib.rs` (MODIFY) | module-root | — | lines 6-15 (`pub mod` declarations) | exact (same file) |

## Pattern Assignments

### `src/store/mod.rs` — ADD run-state helpers (store, CRUD)

**Analog:** the same file. Two existing patterns to copy verbatim in shape.

**Pattern A — short-lived write connection** (`begin_run`, lines 82-100). This is the template for `set_run_cursor`, `set_run_max_lev_start/end`, `mark_run_aborted`, `mark_run_done` IF the planner chooses the short-lived-connection route (RESEARCH Open Q1 / A5). Note the load-bearing PRAGMA + busy_timeout + epoch-seconds pattern:
```rust
pub fn begin_run(&self, config_json: &str) -> rusqlite::Result<i64> {
    let conn = Connection::open(&self.path)?;
    conn.pragma_update(None, "foreign_keys", "ON")?;
    conn.busy_timeout(Duration::from_secs(5))?;
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    conn.execute(
        "INSERT INTO run (started_at, config_json, status) VALUES (?1, ?2, 'running')",
        params![now, config_json],
    )?;
    Ok(conn.last_insert_rowid())
}
```
New helpers mirror this: `UPDATE run SET last_cursor=?2 WHERE run_id=?1` (set_run_cursor); `UPDATE run SET status='aborted', finished_at=?2 WHERE run_id=?1` (mark_run_aborted — never touch `last_cursor`, D-07); `UPDATE run SET status='done', finished_at=?2, max_lev_id_end=?3 WHERE run_id=?1` (mark_run_done). Use `params![]` for every bind (T-01-01).

> **CAUTION (RESEARCH Pitfall 2 / A5):** short-lived write connections plus the batched actor (`BATCH=8192`) can let a `set_run_cursor` commit *before* the writer flushes the pubkey batch → cursor advances past unwritten data. If you take the short-lived route you MUST flush before each cursor write, OR (preferred, D-11-aligned) route both pubkey inserts and run-state updates through the single writer actor as ordered messages. Decide deliberately and document.

**Pattern B — read via `reader()`** (lines 129-132) for `latest_unfinished_run` (D-01):
```rust
pub fn reader(&self) -> rusqlite::Result<Connection> {
    Connection::open(&self.path)
}
```
New `latest_unfinished_run(&self) -> rusqlite::Result<Option<Run>>` opens `reader()` and runs:
```sql
SELECT run_id, started_at, finished_at, max_lev_id_start, max_lev_id_end,
       last_cursor, config_json, status
FROM run WHERE status != 'done' ORDER BY run_id DESC LIMIT 1
```
Map the row into `model::Run` (struct verified below), returning `None` → `--resume` starts fresh (D-02). Use `query_row` + `Option` (or `.optional()`).

**Single-writer doc invariant to preserve** (lines 9-13, 66-69): the module comment explicitly states "NO second write connection." `begin_run` is the *sanctioned* exception (one INSERT, documented). New run-state writers either follow that documented short-lived exception (with the flush caveat) or — cleaner — go through the actor.

---

### `src/store/writer.rs` — ADD pubkey-only insert path (store, event-driven)

**Analog:** the same file. The actor loop + the exact UPSERT const already exist.

**`UPSERT_PUBKEY` const** (line 35) — this is already the D-04 `INSERT OR IGNORE` semantics; reuse it, do not rewrite:
```rust
pub(crate) const UPSERT_PUBKEY: &str =
    "INSERT INTO pubkey (pubkey) VALUES (?1) ON CONFLICT(pubkey) DO NOTHING";
```

**Actor loop + batched transaction** (lines 45-81) — the template for routing pubkey-only writes through the single writer. The existing loop binds `up_pubkey.execute([&p.pubkey])?` (line 64) for every `Persist`. The Phase-2 problem: `Persist` (model.rs lines 91-103) carries `score`/`whitelisted`/`suspected`/`subscores` that enumeration has no value for, so reusing `Persist` would force dummy score/signal rows (RESEARCH lines 269, 276). Two sanctioned approaches:

- **Recommended (enum message, D-11-aligned):** widen the channel payload from `flume::Sender<Persist>` to an enum, e.g. `enum WriteMsg { Score(Persist), Pubkeys(Vec<String>), RunUpdate{...} }`, and `match` in the drain loop — `Pubkeys` runs only `up_pubkey` (line 60/64), preserving single-writer ordering and making Phase 3's fetch payload additive too. This touches `model.rs` (the payload type) and `mod.rs` (`tx: Option<flume::Sender<WriteMsg>>`, the `persist`/new `send` methods) in lockstep.
- **Simpler (short-lived conn):** a `Store::insert_pubkeys(&[String])` batched INSERT OR IGNORE through a short-lived write connection — allowed for Phase 2's sequential walk, with the flush-before-cursor-advance caveat above.

Copy the transaction/`prepare_cached`/`drain` shape exactly (lines 58-78) — `let tx = conn.transaction()?;` … `tx.prepare_cached(UPSERT_PUBKEY)?` … `for ... { up.execute([&pk])? }` … `tx.commit()?`. The `BATCH = 8192` const (line 15) and the greedy-drain idiom (lines 49-56) are the batching machinery to reuse, not reinvent.

---

### `src/store/queries.rs` — (only if `latest_unfinished_run` lives here) (store, CRUD read)

**Analog:** `read_scores` (lines 9-17). Copy the `prepare` → `query_map` → `collect()` shape and the parameterized-`?N` discipline (file header lines 4-5):
```rust
pub fn read_scores(conn: &Connection, run_id: i64) -> rusqlite::Result<Vec<(String, f64)>> {
    let mut stmt =
        conn.prepare("SELECT pubkey, score FROM score WHERE run_id = ?1 ORDER BY pubkey")?;
    let rows = stmt.query_map([run_id], |r| {
        Ok((r.get::<_, String>(0)?, r.get::<_, f64>(1)?))
    })?;
    rows.collect()
}
```
A `latest_unfinished_run` reader maps all 8 `run` columns into `model::Run` via the same `query_map`/`get::<_, T>(i)` idiom (use `query_row(...).optional()` for the single-row `Option<Run>`).

---

### `src/model.rs` — `Run` struct already complete; (maybe) add `WriteMsg` enum (model)

**Analog / verified type:** `Run` (lines 19-31) already mirrors every `run` column the resume flow needs — **no new fields, no migration** (D-03):
```rust
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Run {
    pub run_id: i64,
    pub started_at: i64,
    pub finished_at: Option<i64>,
    pub max_lev_id_start: Option<i64>,
    pub max_lev_id_end: Option<i64>,
    pub last_cursor: Option<String>,
    pub config_json: String,
    pub status: String,   // running | done | aborted
}
```
**`Persist` payload** (lines 91-103) is the analog for any new `WriteMsg` variant — note it derives `Debug, Clone, PartialEq` (model.rs header lines 4-5: "Every struct derives … so tests can assert value equality"). Match that derive set on new payload types. The doc comment on `Persist` (lines 92-94) already anticipates "Phase 3/4 will add …" — the enum-message refactor is the sanctioned extension point.

**Boolean/int convention** (model.rs lines 7-9): SQLite has no bool; write with `as i64`, as `writer.rs` line 69-70 does (`p.whitelisted as i64`). Irrelevant to pubkey/run-cursor writes but applies if any new column is boolean.

---

### `src/graphql/envelope.rs` — generic response envelope (model, transform)

**Analog:** `src/model.rs` serde conventions (lines 16, 19, 47) — `use serde::{Deserialize, Serialize};`, `#[derive(... Deserialize)]` on row/wire structs, `Option<T>` for nullable columns. Apply the same derive idiom to the wire types. The concrete shapes are pinned by RESEARCH Pattern 1 (lines 166-188) and the contract §3/§7 — `GraphQlResponse<T> { data: Option<T>, errors: Option<Vec<GraphQlError>> }`, `GraphQlError { message, extensions }`, `Extensions { code: Option<String> }`. Critical: `#[serde(default)]` on `errors` so both `{data}` and `{data:null, errors}` shapes parse (RESEARCH Pitfall 5).

**No in-repo analog for the HTTP wire layer** — model.rs structs mirror DB rows, not JSON-over-HTTP envelopes, so this is the closest *idiom* match (serde derive + `Option` nullability), not a structural copy.

---

### `src/graphql/queries.rs` — query consts + page structs (model + const, transform)

**Analog (idiom only):** `src/store/queries.rs` for the "module of read helpers" naming and `src/store/writer.rs` lines 17-36 for the `pub(crate) const QUERY: &str = "..."` const-string convention (the store keeps its SQL in named consts; mirror that with `AUTHORS_QUERY` / `STATS_QUERY` GraphQL consts). The serde page structs (`AuthorsPage`, `AuthorsData`, `StatsResult`, `StatsData`) follow model.rs's derive style plus `#[serde(rename_all = "camelCase")]` to map `hasMore`/`endCursor`/`maxLevId` (RESEARCH Code Examples lines 404-431, field names verified against contract §4/§6.4). Note GraphQL nests under the query field name (`data.authors.authors`) → deserialize `T = AuthorsData` then `.authors`.

---

### `src/graphql/client.rs` — async transport + two-layer error dispatch (service, request-response)

**No in-repo analog** — this is the genuinely greenfield transport (no HTTP client exists anywhere in the crate). Follow RESEARCH Pattern 2 (lines 194-223) and the error-mapping table (RESEARCH §"Two-Layer Error Handling", lines 336-345) directly:
- `POST` JSON `{query, variables}` via `reqwest::Client`.
- HTTP-status layer: `503 → Unavailable` (retry), `413 → PayloadTooLarge` (abort) — contract §7 transport.
- Body layer: check `env.errors` **before** trusting `env.data` (RESEARCH Pitfall 5; criterion 3 / D-08 "never ignored").
- `ClientError` enum (`Unavailable | PayloadTooLarge | Transport | Graphql{code,message}`) — `thiserror` optional/recommended (RESEARCH Standard Stack line 79).
- **Injectability:** endpoint is a struct field (`GraphQlClient { http, endpoint }`), never hardcoded — required so unit tests point at a mock (RESEARCH Validation lines 520).
- D-11 extensibility: `query<T: DeserializeOwned>` generic; `authors()`/`stats()` are thin typed wrappers; Phase 3's `latest_per_author` is additive (RESEARCH Pattern 3, lines 230-238).

---

### `src/graphql/mod.rs` — module root (module-root)

**Analog:** `src/store/mod.rs` lines 18-22 — the submodule-declaration + selective re-export idiom (`mod schema; pub mod queries; mod writer;`). Mirror with `mod client; mod envelope; mod queries;` and `pub use` the public surface (`GraphQlClient`, `ClientError`, the page structs Phase 3 needs).

---

### `src/enumerate.rs` — the authors walk (service, streaming / cursor-walk)

**No in-repo analog** — orchestration logic is greenfield. Follow RESEARCH §"Opaque-Cursor Pagination Loop" (lines 299-326) exactly, which is itself pinned to contract §6.4. Load-bearing ordering rules:
- `stats().max_lev_id` at start → `set_run_max_lev_start` (D-09 drift probe).
- Loop: `client.authors(after, LIMIT)` → on `INVALID_CURSOR` set `after=None`, `continue` (D-08, no abort/no retry); on other errors → bounded retry then `mark_run_aborted` (D-06/D-07); persist pubkeys, **then** `set_run_cursor(endCursor)` (NEVER before — RESEARCH Pitfall 2); `endCursor==None` → clean termination (INGEST-01).
- `stats()` again at end → `mark_run_done(max_lev_id_end)`.
- Bounded retry helper: 3 attempts, exponential 250ms→2s, `tokio::time::sleep` (RESEARCH §Bounded Retry, lines 356-373; Discretion — planner may tune).
- **Sync/async boundary:** persist calls are plain synchronous calls inside the async fn; do NOT wrap the store in `tokio::sync::Mutex`; do NOT spawn the walk across tasks (RESEARCH Pitfall 1).

---

### `src/main.rs` or `src/bin/enumerate.rs` — minimal entry point (binary, request-response)

**No in-repo analog** — the crate is library-only today (`lib.rs` has no `main`). Follow RESEARCH §"Minimal entry point" (lines 434-447): `#[tokio::main]`, parse `--resume` from `std::env::args()` (clap optional per D-12 — do NOT build the full CLI, that is Phase 5), endpoint from env `LMDB2GRAPHQL_URL` defaulting to `http://127.0.0.1:8080/graphql` (RESEARCH A2; contract §10 plain loopback HTTP), `Store::open` → `GraphQlClient::new` → `enumerate::run(&store, &client, resume).await` → `store.close()`. Adding `[[bin]]` (or `src/main.rs`) is the first binary in this crate — keep the library API (`lib.rs`) as the surface the binary calls into.

---

### `src/lib.rs` — module registration (module-root)

**Analog:** the same file, lines 6-15. Copy the `/// doc + pub mod` idiom already used for `model` and `store`; add `pub mod graphql;` and `pub mod enumerate;`.

## Shared Patterns

### Parameterized SQL (T-01-01) — apply to ALL new store helpers
**Source:** `src/store/writer.rs` lines 8-9 (header) + lines 63-75; `src/store/queries.rs` lines 4-5.
Every bind uses `params![]` / `?N` / a positional array — no `format!` into SQL. New `run`-state UPDATEs and the pubkey insert MUST follow this.
```rust
up_score.execute(params![p.run_id, p.pubkey, p.score, p.whitelisted as i64, p.suspected as i64])?;
```

### Single-writer invariant — apply to ALL persistence in enumerate/store
**Source:** `src/store/mod.rs` lines 9-13 (module doc) + 66-69.
"This module deliberately exposes NO second write connection." The only sanctioned short-lived-write exception is `begin_run` (lines 82-100), explicitly documented. New writers either reuse the actor (preferred) or copy `begin_run`'s documented short-lived pattern with the flush-before-cursor-advance caveat (RESEARCH A5/Pitfall 2).

### WAL/PRAGMA bootstrap on any new connection — apply to short-lived write conns
**Source:** `src/store/mod.rs` `bootstrap` lines 32-47 + `begin_run` lines 88-90.
Short-lived write connections re-assert `foreign_keys=ON` + `busy_timeout(5s)` (the `run` FK targets are inert otherwise; line 42 comment). Readers via `reader()` (lines 129-132) inherit WAL from disk.

### serde derive + camelCase mapping — apply to ALL graphql wire structs
**Source:** `src/model.rs` lines 16, 19, 47 (`use serde::{Deserialize, Serialize}` + `#[derive(..., Deserialize)]`, `Option<T>` for nullable).
Wire structs add `#[serde(rename_all = "camelCase")]` (DB structs don't need it; JSON does) and `#[serde(default)]` on optional collections (RESEARCH Pitfall 5).

### Temp-FILE test DB convention — apply to ALL new store/enumerate tests
**Source:** `src/store/mod.rs` `temp_db()` lines 155-159 + `#[cfg(test)]` block; `lib.rs` lines 13-14 doc.
Tests use `tempfile::TempDir` + a real on-disk `.sqlite` path, NEVER `:memory:` (WAL sidecars `-wal`/`-shm` don't replicate in-memory). New run-state helper tests and enumerate resume/idempotency tests follow this. `tempfile` is already a dev-dependency (Cargo.toml line 25).

## No Analog Found

Files with no close in-repo match — planner should use RESEARCH.md patterns (cited) instead:

| File | Role | Data Flow | Reason | Use Instead |
|------|------|-----------|--------|-------------|
| `src/graphql/client.rs` | service (transport) | request-response | No HTTP client exists anywhere in the crate (persistence-only Phase 1) | RESEARCH Pattern 2 (lines 194-223) + error table (lines 336-345); contract §3/§7 |
| `src/enumerate.rs` | service (orchestrator) | streaming / cursor-walk | No walk/pagination loop exists | RESEARCH §Pagination Loop (lines 299-326); contract §6.4/§8 |
| `src/main.rs` / `src/bin/*` | binary entry | request-response | Crate is library-only (`lib.rs` has no `main`, no `src/bin/`, no `[[bin]]`) | RESEARCH §entry point (lines 434-447); D-12 |

## Metadata

**Analog search scope:** `/Users/g/git/deepfry/spamhunter/pubkey_iterator/src/` (entire crate — `lib.rs`, `model.rs`, `store/{mod,writer,queries,schema}.rs`) + `Cargo.toml`.
**Files scanned:** 7 source files (all read in full — each ≤ 445 lines), 1 manifest.
**No `.claude/skills/` or `.agents/skills/` present.** No CLAUDE.md inside `pubkey_iterator/` (the governing one is the monorepo root `/Users/g/git/deepfry/CLAUDE.md`, already in context).
**Pattern extraction date:** 2026-06-25
