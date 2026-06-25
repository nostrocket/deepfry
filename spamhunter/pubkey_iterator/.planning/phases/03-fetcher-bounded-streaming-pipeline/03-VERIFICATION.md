---
phase: 03-fetcher-bounded-streaming-pipeline
verified: 2026-06-25T00:00:00Z
status: passed
score: 4/4 success criteria verified (10/10 plan must-have truths)
behavior_unverified: 0
overrides_applied: 0
---

# Phase 3: Fetcher + Bounded Streaming Pipeline Verification Report

**Phase Goal:** Enumerated pubkeys flow through a bounded-memory streaming pipeline (tokio fetch → bounded channel → rayon analysis) that fetches each pubkey's ~100 recent events without ever buffering the corpus — locking the structural decision before any layer depends on it.
**Verified:** 2026-06-25
**Status:** passed
**Re-verification:** No — initial verification
**Mode note:** ROADMAP marks this phase `mode: mvp`, but the goal is an engineering-capability statement, not an `As a … I want … so that …` User Story. Verified against the 4 ROADMAP Success Criteria (the binding contract) using standard goal-backward methodology rather than the User-Flow-Coverage table, since no User Story slot exists to drive that table.

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
| - | ----- | ------ | -------- |
| 1 | Fetcher retrieves ~100 recent events via `latestPerAuthor(kind:1, perAuthor:100)` at ≤1000 authors/call, respecting 256 KiB body + ≤500 page clamp | ✓ VERIFIED | `LATEST_PER_AUTHOR_QUERY` (queries.rs:37) is fully parameterized (`$kind:Int!,$perAuthor:Int!,$authors:[String!]!`), selects `author events{id pubkey kind createdAt content tags}`, omits `raw`/`sig`. `DEFAULT_AUTHORS_PER_CALL = 250` ≤ 1000 (pipeline.rs:44); pipeline chunks `pubkeys.chunks(authors_per_call)`. 256 KiB handled reactively via 413 `PayloadTooLarge` shrink-retry (`fetch_batch`, fetch.rs:77-84), proven by `fetch_413_split`. Live adapter confirmed real `latestPerAuthor` deserialization with `kind=1` (live test printed "deserialized 1 group(s) … D-09 OK"). See note below on perAuthor literal. |
| 2 | Author groups matched back by `author` field (never zipped by index); zero-match omitted authors handled without misalignment | ✓ VERIFIED | `match_groups` (fetch.rs:40-47) builds a `HashMap<String,Vec<Event>>` keyed on `group.author`, maps the requested list to `(pk, by_author.remove(pk).unwrap_or_default())`. Test `match_groups_no_shift` (fetch.rs:139) proves omitted PK2 → empty Vec, PK3's events stay on PK3 (no positional shift). PASS. |
| 3 | Pipeline holds memory bounded by channel capacity (not corpus size); fetch back-pressures; CPU analysis off the tokio threads; `bounded_memory_watermark` is a real structural assertion (watermark ≤ cap+batch), not RSS | ✓ VERIFIED | `flume::bounded::<AuthorGroup>(channel_cap)` (pipeline.rs:88); producer uses `tx.send_async().await` (back-pressure); consumer `rx.recv()` runs on a `std::thread::spawn` drain (pipeline.rs:94, off the tokio reactor), joined after `drop(tx)` (pipeline.rs:123-126). `bounded_memory_watermark` runs 100_000 authors + slow consumer, asserts `peak <= CAP + APC` AND `peak > CAP` (non-vacuous — back-pressure exercised). PASS. Zero `unbounded` in code (only in a doc comment). |
| 4 | No-op pass-through consumer proves end-to-end flow with no unbounded buffering | ✓ VERIFIED | `consume_noop` (pipeline.rs:51) + `run_pipeline_noop`; `pipeline_endtoend_count` asserts final count == N*EVENTS_PER_AUTHOR (5000*3 = 15000), nothing dropped under back-pressure. Injected-consumer seam (`injected_consumer_seam`) proves Phase-4 swap point. Both PASS. |

**Score:** 4/4 ROADMAP success criteria verified (0 present, behavior-unverified). 10/10 plan must-have truths verified.

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/graphql/queries.rs` | LATEST_PER_AUTHOR_QUERY const + Event/AuthorGroup/LatestPerAuthorData structs (i64, camelCase) | ✓ VERIFIED | Const present; structs `Event{kind:i64,created_at:i64}`, `AuthorGroup`, `LatestPerAuthorData{latest_per_author:Vec<AuthorGroup>}`. 2 deser tests incl. i32-overflow guard. |
| `src/fetch.rs` | fetch_batch (413 shrink-retry) + match_groups (match-by-author) | ✓ VERIFIED | 317 lines; `pub fn match_groups`, `pub async fn fetch_batch` reusing `crate::enumerate::retry`; 4 tests pass. |
| `src/store/queries.rs` | read_pubkeys -> Vec<String> ORDER BY pubkey | ✓ VERIFIED | `pub fn read_pubkeys` (queries.rs:14), SQL `SELECT pubkey FROM pubkey ORDER BY pubkey`; `read_pubkeys_roundtrip` PASS. |
| `src/pipeline.rs` | run_pipeline (tokio → flume::bounded → drain) + no-op + watermark/count/live tests | ✓ VERIFIED | 446 lines; `pub async fn run_pipeline` with injected fetch + consumer; bounded channel; std::thread drain joined after drop(tx). 4 tests pass. |
| `Cargo.toml` | rayon = "1.12" (Phase-3-owned) | ✓ VERIFIED | `rayon = "1.12"` (Cargo.toml:41); reservation comment updated; no gaoya/linfa/clap leaked into Cargo.lock. |

### Key Link Verification

| From | To | Via | Status | Details |
| ---- | -- | --- | ------ | ------- |
| client.rs | queries.rs | latest_per_author deserializes LatestPerAuthorData | ✓ WIRED | client.rs:157-169 calls `self.query::<LatestPerAuthorData>` with `json!` variables, `.map(\|d\| d.latest_per_author)`. |
| fetch.rs | client.rs | fetch_batch calls latest_per_author, catches PayloadTooLarge | ✓ WIRED | fetch.rs:72,77 — retry over `client.latest_per_author`, 413 arm splits batch. |
| pipeline.rs | fetch.rs | run_pipeline injected fetch calls fetch_batch (production seam) | ✓ WIRED (seam) | run_pipeline takes injected `fetch: F`; production passes a closure calling `fetch_batch` (documented seam). Tests inject mock fetcher. Not yet bound at a production call site — Phase-4 consumer wiring (see note). |
| pipeline.rs | flume::bounded | producer send_async / consumer recv | ✓ WIRED | pipeline.rs:88,110,95. |
| pipeline.rs | store/queries.rs | read_pubkeys as enumeration source | ⚠️ DOC-ONLY (deferred) | read_pubkeys exists and is tested; `run_pipeline` takes `pubkeys: Vec<String>` (caller reads). Not invoked from a production driver yet — Phase-4 wires the full enumerate→read_pubkeys→run_pipeline path. Not a Phase-3 gap (phase locks structure). |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Full lib suite | `cargo test --lib` | 44 passed; 0 failed | ✓ PASS |
| Bounded-memory structural proof | `cargo test --lib bounded_memory_watermark` | watermark ≤ cap+batch over 100k authors, non-vacuous | ✓ PASS |
| End-to-end no-drop count | `cargo test --lib pipeline_endtoend_count` | count == exact synthetic total | ✓ PASS |
| Live D-09 deserialization | `cargo test --lib live_latest_per_author -- --nocapture` | "deserialized 1 group(s) from http://192.168.149.21:8080/graphql (D-09 OK)" | ✓ PASS (live reachable) |
| Build | `cargo build` | Finished, clean | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| INGEST-02 | 03-01, 03-02 | Fetch ~100 events/pubkey via batched latestPerAuthor (≤1000/call, 256 KiB, ≤500 clamp) | ✓ SATISFIED | SC#1; live deserialization confirmed; 413 shrink-retry tested. |
| INGEST-03 | 03-02 | Fetch + analysis as bounded-memory streaming pipeline (tokio → bounded → rayon), never buffers corpus | ✓ SATISFIED | SC#3/#4; watermark proof + bounded flume channel + off-reactor drain. |

### Anti-Patterns Found

None. No TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER in any phase-modified file. No empty stub returns in production paths. Zero `unbounded` in pipeline code (the one match was inside a Pitfall-2 doc comment).

### Notes (non-blocking)

1. **`perAuthor:100` / `kind:1` literal defaults not bound at a production call site.** `kind`/`per_author` are correctly *parameters* of `latest_per_author`/`fetch_batch` (D-01: not hardcoded magic), and the query const is fully parameterized. `run_pipeline` deliberately dropped these params in Plan 02 — they live in the injected `fetch` closure. No production driver yet binds them to `1`/`100`; that wiring is the Phase-4 consumer/`run_pipeline` production call site. This is consistent with the phase's stated goal ("locking the structural decision before any layer depends on it"). The capability to issue `kind:1, perAuthor:100` is proven (live test used kind=1; perAuthor is server-clamped to [1,500] so 100 is valid). Not a gap; flagged for Phase-4 to bind the production defaults.

2. **Consumer drain uses `std::thread`, not `rayon::spawn`.** The goal phrase "rayon analysis" and the Phase-3 deliverable diverge intentionally: the Phase-3 no-op consumer runs on a plain `std::thread` (off the tokio reactor — satisfies SC#3 "CPU analysis runs off the tokio threads"). `rayon` is declared and reserved for Phase-4 per-group scoring fan-out. The structural intent (CPU off the reactor) is fully met; rayon's per-group parallelism is a Phase-4 concern.

3. **D-09 live check passed against the real adapter** at http://192.168.149.21:8080/graphql (1 AuthorGroup deserialized) — the manual/integration must-have is satisfied, not merely self-skipped. The test self-skips gracefully when unreachable, so it is not a CI blocker.

### Gaps Summary

No gaps. All 4 ROADMAP success criteria and all 10 plan must-have truths are verified against the actual codebase. The build is clean, the full 44-test suite is green, the bounded-memory watermark assertion is structural and non-vacuous, the INGEST-04 match-by-author landmine is provably closed, the channel is `flume::bounded` (never unbounded), CPU drain runs off the tokio reactor, and rayon is the only new dependency with no Phase-4 detection deps leaked. The two structural-lock items deferred to Phase-4 (binding perAuthor/kind production defaults, swapping the no-op consumer for the Layer stage, wiring read_pubkeys→run_pipeline in a driver) are by design — this phase locks the seam, and the seam is proven.

---

_Verified: 2026-06-25_
_Verifier: Claude (gsd-verifier)_
