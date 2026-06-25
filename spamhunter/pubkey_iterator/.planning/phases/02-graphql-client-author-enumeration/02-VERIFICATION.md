---
phase: 02-graphql-client-author-enumeration
verified: 2026-06-25T00:00:00Z
status: human_needed
score: 4/4 must-haves verified (mocked-CI tier)
behavior_unverified: 0
overrides_applied: 0
deferred:
  - truth: "INGEST-04 empty-group omission — match fetched author groups back by `author` field, never zip by index"
    addressed_in: "Phase 3"
    evidence: "ROADMAP Phase 3 SC2: 'Fetched author groups are matched back to requested pubkeys by the author field (never zipped by index), and authors omitted because they have zero matching events are handled without misalignment.' No per-author event fetch (latestPerAuthor) exists in Phase 2 — only authors-keyspace enumeration, so this INGEST-04 sub-clause is structurally a Phase-3 (INGEST-02/03) concern."
human_verification:
  - test: "Live-adapter full-walk connectivity proof: point LMDB2GRAPHQL_URL at a reachable LMDB2GraphQL adapter and run `cargo run --bin pubkey_iterator` (then `cargo run --bin pubkey_iterator -- --resume`)."
    expected: "Walk enumerates real distinct pubkeys, the hand-written AUTHORS_QUERY/STATS_QUERY + serde structs deserialize the real v1.2 response with no shape mismatch, max_lev_id_start equals live stats.maxLevId, --resume reuses the same run (run-table count stays 1) and grows the persisted set, status becomes 'done' on a full walk. Reported satisfied during execution against http://192.168.149.21:8080/graphql (3500 → 5500 pubkeys, maxLevId 47928105 matched)."
    why_human: "Requires a reachable live adapter; cannot run in CI. This is the phase's documented manual/integration must_have (02-VALIDATION.md Manual-Only Verifications), satisfied during execution but not re-runnable in this verification environment."
---

# Phase 2: GraphQL Client + Author Enumeration Verification Report

**Phase Goal:** The engine can enumerate every distinct pubkey in the live corpus through the LMDB2GraphQL adapter, resumably and terminating cleanly, while handling the adapter's real failure modes — proving connectivity against the actual contract before any analysis exists.
**Verified:** 2026-06-25
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

The phase is declared `mode: mvp`, but the ROADMAP goal is a capability statement, not a User Story (`As a …, I want …, so that …`). Per the MVP-mode guard, the User-Flow-Coverage narrowing is not applied to a non-User-Story goal; verification proceeds against the four explicit ROADMAP Success Criteria (the roadmap contract), which is the standard goal-backward path.

### Observable Truths

| # | Truth (ROADMAP Success Criterion) | Status | Evidence |
| - | --------------------------------- | ------ | -------- |
| 1 | Walk enumerates the entire `authors` keyspace via cursor pagination, each distinct pubkey once, terminates cleanly when `hasMore` is false | ✓ VERIFIED | `src/enumerate.rs:140-189` — opaque-cursor loop passes `endCursor` verbatim as `after`, `None ⇒ break` (line 187), then `mark_run_done`. Behavioral tests `enumerate::tests::terminates_on_null_cursor` (3 pages → null, asserts all 6 pubkeys persisted once + status `done`) and `pubkeys_idempotent_on_resume` (overlap → one row each) PASS. |
| 2 | Resumable: latest `endCursor` + `stats.maxLevId` persisted per batch into the `run` row; `--resume` continues from the stored cursor | ✓ VERIFIED | Per-page `store.set_run_cursor` (`enumerate.rs:183`); `set_run_max_lev_start/end` (`store/mod.rs:153-170`). Resume selection via `latest_unfinished_run` (`enumerate.rs:123-130`). Tests `resume_from_last_cursor` (no `--run-id`, reuses run id, keeps original `max_lev_id_start`), `latest_unfinished_run_selection`, `set_run_cursor_roundtrip`, `set_run_max_lev_roundtrip` PASS. |
| 3 | `503` → backoff-retry without advancing cursor; `INVALID_CURSOR` → restart from page 1; in-body `errors[]`/`extensions.code` parsed not ignored | ✓ VERIFIED | `fetch_page_with_retry` (3 attempts, 250ms→2s, `enumerate.rs:74-90`); 503-exhaustion → `mark_run_aborted` with cursor untouched (`mark_run_aborted` never writes `last_cursor`, `store/mod.rs:175-182`); `INVALID_CURSOR` distinct branch resets `after=None`, no retry/abort (`enumerate.rs:145-148`). Client checks `env.errors` BEFORE `env.data` (`graphql/client.rs:104-119`). Tests `retry_503_no_cursor_advance`, `abort_preserves_cursor` (status=aborted, cursor==prior page endCursor), `invalid_cursor_restarts` (request-count==4 asserts no retry/sleep), `inbody_errors_surface` (INVALID_CURSOR → `Err(Graphql)`), `http_503_maps_unavailable`, `http_413_maps_payload_too_large`, `null_data_no_errors_is_error` PASS. |
| 4 | `maxLevId` recorded at run start + end as drift probe; mid-pagination corpus change does not abort | ✓ VERIFIED | Start probe `set_run_max_lev_start` (`enumerate.rs:134-137`), end probe via `mark_run_done(run_id, max_end)` (`enumerate.rs:192-193`). Drift is not classified as an error anywhere in the loop. Test `records_drift_does_not_abort` (start=100, end=150, status `done`) PASS. |

**Score:** 4/4 truths verified (mocked-CI tier), 0 present-behavior-unverified.

> A 5th, manual must_have (live-adapter connectivity proof) is tracked in the Human Verification section — it is the phase's documented vertical-slice gate and is not CI-runnable.

### Deferred Items

| # | Item | Addressed In | Evidence |
| - | ---- | ------------ | -------- |
| 1 | INGEST-04 empty-group omission (match by author, never zip by index) | Phase 3 | ROADMAP Phase 3 SC2 owns author-field matching / zero-event omission. No `latestPerAuthor` event fetch exists in Phase 2; the `authors` keyspace walk has no per-author group to zip, so this INGEST-04 sub-clause is structurally a Phase-3 concern. Not an actionable Phase-2 gap. |

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `src/model.rs` | `WriteMsg` enum (Score / Pubkeys / Flush) | ✓ VERIFIED | `enum WriteMsg` present (line 112); `Pubkeys(Vec<String>)`, `Score(Persist)`, plus deviation `Flush(flume::Sender<()>)`. `PartialEq` dropped (flume::Sender not PartialEq) — documented, no caller depended on it. |
| `src/store/writer.rs` | Single-writer actor handling WriteMsg via UPSERT_PUBKEY | ✓ VERIFIED | `writer_loop` matches all three variants; `Pubkeys` uses existing `UPSERT_PUBKEY` const (line 87-91); `Flush` acks only AFTER `tx.commit()` (lines 93, 97, 100-102). All binds `params![]` — no `format!` SQL. |
| `src/store/mod.rs` | run-state helpers + pubkey send + flush barrier | ✓ VERIFIED | `latest_unfinished_run`, `set_run_cursor`, `set_run_max_lev_start/end`, `mark_run_aborted`, `mark_run_done`, `insert_pubkeys`, `flush` all present and `pub`. |
| `src/graphql/client.rs` | GraphQlClient { http, endpoint } + query<T>/authors/stats + ClientError taxonomy | ✓ VERIFIED | `struct GraphQlClient` with `endpoint: String` field; `new`, generic `query<T>`, `authors`, `stats`. `ClientError` { Unavailable, PayloadTooLarge, Transport, Graphql }. No hardcoded URL outside test code. |
| `src/graphql/envelope.rs` | GraphQlResponse<T>, GraphQlError, Extensions | ✓ VERIFIED | All three present; `#[serde(default)]` on `errors` and `extensions` so both `{data}` and `{data:null,errors}` parse. |
| `src/graphql/queries.rs` | AUTHORS_QUERY / STATS_QUERY consts + serde page structs | ✓ VERIFIED | Both consts + `AuthorsPage`/`AuthorsData`/`StatsResult`/`StatsData` with `rename_all="camelCase"`. |
| `Cargo.toml` | reqwest + tokio (+ thiserror) deps | ✓ VERIFIED | reqwest 0.13 (no TLS), tokio 1 (rt-multi-thread/macros/time), thiserror 2. No `rayon`. `[[bin]] pubkey_iterator` declared. |
| `src/enumerate.rs` | authors walk: `pub async fn run` | ✓ VERIFIED | Full walk + retry + abort + drift + flush-before-cursor; 8 unit tests + property test. |
| `src/main.rs` | minimal `#[tokio::main]` parsing `--resume` | ✓ VERIFIED | Parses `--resume` from `std::env::args()` (no clap); resolves `LMDB2GRAPHQL_URL` default loopback; Store::open → GraphQlClient::new → enumerate::run → store.close() in order. |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| `enumerate.rs` | `graphql/client.rs` | `client.authors(after, LIMIT)` + `client.stats()`; branches on `ClientError` | ✓ WIRED (lines 80, 134, 192; ClientError match 145-156) |
| `enumerate.rs` | `store/mod.rs` | `insert_pubkeys` → `flush` → `set_run_cursor`; `latest_unfinished_run`; `mark_run_aborted`; `set_run_max_lev_start`/`mark_run_done` | ✓ WIRED — cursor-durability ordering present: `insert_pubkeys`(171) → `flush()?`(182) → `set_run_cursor`(183) |
| `main.rs` | `enumerate.rs` | `enumerate::run(&store, &client, resume).await` | ✓ WIRED (line 36) |
| `client.rs` | `envelope.rs` | `query<T>` decodes `GraphQlResponse<T>`, checks `errors` before `data` | ✓ WIRED (client.rs:105-119) |
| `client.rs` | `queries.rs` | `authors`/`stats` wrap `query<T>` over AUTHORS_QUERY/STATS_QUERY | ✓ WIRED (client.rs:130, 137) |
| `store/mod.rs` | `writer.rs` | `WriteMsg::Pubkeys`/`Flush` funnel through single `flume::Sender<WriteMsg>` | ✓ WIRED — Flush ack released only after `tx.commit()` |

### Cursor-Durability Invariant (executor deviation review)

The executor added a `WriteMsg::Flush` ack barrier (deviation, auto-fixed Rule 2) because `set_run_cursor` uses a separate short-lived connection (`run_write_conn`, `store/mod.rs:201-206`) — it does NOT route through the writer actor, contrary to the plan's "both route through the single writer" assertion. Without a barrier a crash between the lazy pubkey-batch commit and the cursor write could resume past unwritten pubkeys.

Verified present and correct:
- `store/mod.rs:244-255` — `flush()` sends `WriteMsg::Flush(ack)` and blocks on the ack.
- `writer.rs:61, 93, 97-102` — the writer collects Flush acks and `ack.send(())` ONLY after `tx.commit()`.
- `enumerate.rs:182-184` — `store.flush()?` is called between `insert_pubkeys` and `set_run_cursor` on the cursor-advance arm.
- Test `store::tests::flush_makes_prior_pubkeys_durable` proves a pubkey is visible to a fresh reader after `flush()` returns WITHOUT `close()`.

This closes the cursor-durability invariant on crash, not just in source order. Deviation is sound and within scope (routes through the existing single-writer seam, no second write connection for the actor's tables).

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Full build clean | `cargo build` | Finished, no warnings | ✓ PASS |
| Full test suite | `cargo test` | 30 passed / 0 failed | ✓ PASS |
| Lint clean | `cargo clippy --all-targets -- -D warnings` | Finished, no warnings | ✓ PASS |
| Binary target builds | `[[bin]] pubkey_iterator` | builds | ✓ PASS |

All 8 named enumerate behaviors + the resume-boundary property test are present and green: `terminates_on_null_cursor`, `resume_from_last_cursor`, `pubkeys_idempotent_on_resume`, `retry_503_no_cursor_advance`, `invalid_cursor_restarts`, `abort_preserves_cursor`, `records_drift_does_not_abort`, `resume_boundary_union_complete`.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| INGEST-01 | 02-01, 02-02, 02-03 | Enumerate all distinct pubkeys via `authors` cursor pagination, resumable, terminating cleanly | ✓ SATISFIED | SC1 + SC2 verified; clean termination on null cursor + `--resume` continuation tested. |
| INGEST-04 | 02-01, 02-02, 02-03 | Graceful adapter conditions: 503 (no cursor advance), INVALID_CURSOR (restart), empty-group omission (match by author), snapshot drift (record start/end, no abort) | ✓ SATISFIED (Phase-2 scope) | 503, INVALID_CURSOR, in-body errors, and drift all verified (SC3 + SC4). Empty-group/zip-by-index sub-clause structurally deferred to Phase 3 (no per-author event fetch in Phase 2) — see Deferred Items. |

Both declared requirement IDs are accounted for; REQUIREMENTS.md maps no additional Phase-2 IDs (no orphans).

### Anti-Patterns Found

None. No `TBD`/`FIXME`/`XXX`/`TODO`/`unimplemented!`/`PLACEHOLDER` markers in `src/`. No stub returns; no hardcoded empty data flowing to output. All SQL binds parameterized (no `format!`-built SQL). No `rayon` / Phase-3 deps leaked into Cargo.toml. No `tokio::sync::Mutex` around the store (Pitfall 1 honored — store is the sync boundary).

### Human Verification Required

#### 1. Live-adapter full-walk connectivity proof (the phase's vertical-slice gate)

**Test:** Point `LMDB2GRAPHQL_URL` at a reachable LMDB2GraphQL adapter and run `cargo run --bin pubkey_iterator`, then `cargo run --bin pubkey_iterator -- --resume`.
**Expected:** Real distinct pubkeys enumerate; hand-written queries + serde structs deserialize the real v1.2 response; `max_lev_id_start` equals live `stats.maxLevId`; `--resume` reuses the same run (run-table count stays 1) and grows the persisted set; status `done` on a full walk.
**Why human:** Requires a reachable live adapter — not CI-runnable. Documented as satisfied during execution (3500 → 5500 pubkeys against `http://192.168.149.21:8080/graphql`, `maxLevId` 47928105 matched). Re-confirmation is a manual integration check, not a code gap.

### Gaps Summary

No code gaps. All four ROADMAP Success Criteria are verified TRUE against the codebase with passing behavioral tests (30/30, clippy clean, build clean). The executor's `WriteMsg::Flush` cursor-durability deviation is present, correct, and tested. The single non-CI item is the live-adapter connectivity proof — a documented manual must_have, reported satisfied during execution, routed to human re-confirmation. The INGEST-04 empty-group/zip-by-index sub-clause is structurally deferred to Phase 3 (no per-author event fetch exists yet). Status is `human_needed` solely because the manual connectivity-proof item is non-empty; no truth FAILED.

---

_Verified: 2026-06-25_
_Verifier: Claude (gsd-verifier)_
