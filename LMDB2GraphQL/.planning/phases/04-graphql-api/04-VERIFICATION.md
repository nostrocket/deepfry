---
phase: 04-graphql-api
verified: 2026-06-13T00:00:00Z
status: passed
score: 8/8 must-haves verified
overrides_applied: 0
re_verification: false
---

# Phase 4: GraphQL API Verification Report

**Phase Goal:** The query engine is exposed as a read-only GraphQL endpoint with all v1 query types, a hard limit ceiling, cursor pagination, and no mutation surface.
**Verified:** 2026-06-13
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| #  | Truth                                                                                                                                                                     | Status     | Evidence                                                                                                                                                                      |
|----|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1  | A consumer can send a GraphQL query for `events()` filtered by ids, authors, kinds, since, until, and limit and receive matching events                                    | VERIFIED   | `events` resolver in `resolvers.rs:60–106`; `build_nostr_filter` maps all filter fields; `test_events_query_basic` passes end-to-end through fixture LMDB                    |
| 2  | A consumer can query `events()` with a tag filter (name + values) and receive matching events                                                                             | VERIFIED   | `TagFilterInput` input type in `types.rs:170–177`; `build_nostr_filter:280–285` maps single tag to `NostrFilter.tags`; wired to engine's `QRY-02` tag scan path             |
| 3  | A consumer can query `latestPerAuthor(kind, perAuthor, authors)` and receive the latest N events per pubkey                                                                | VERIFIED   | `latest_per_author` resolver in `resolvers.rs:121–176`; calls engine `latest_per_author`; maps `HashMap<String, Vec<DecodedEvent>>` to `Vec<AuthorGroup>`                   |
| 4  | A consumer can query `stats` and receive event count, max levId, and dbVersion                                                                                            | VERIFIED   | `stats` resolver in `resolvers.rs:187–196`; `read_stats` helper reads `EventPayload` stat + last key; `test_stats_query` asserts `dbVersion == 3`                           |
| 5  | Queries exceeding the hard limit ceiling are capped, not rejected; cursor pagination on `(created_at, lev_id)` allows traversal without scanning the full DB              | VERIFIED   | Clamp: `limit.map(|l| (l.max(1) as usize).min(500)).unwrap_or(100)` at `resolvers.rs:73–75`; `PageCursor::decode/encode` at `resolvers.rs:78–81, 104`; tests pass           |
| 6  | The GraphQL schema exposes no mutations                                                                                                                                    | VERIFIED   | `Schema::build(Query, EmptyMutation, EmptySubscription)` at `schema.rs:68`; `test_no_mutation_in_schema_sdl` asserts SDL contains no `type Mutation` and no `mutation:` root |

**Score:** 6/6 ROADMAP success criteria verified

### Plan-level must-have truths (merged, deduplicated)

| #  | Truth (plan 04-01 or 04-02)                                                                                                                                                   | Status     | Evidence                                                                                                                      |
|----|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------|-------------------------------------------------------------------------------------------------------------------------------|
| 7  | The four GraphQL crates resolve at pinned versions and the crate compiles                                                                                                     | VERIFIED   | `Cargo.toml`: `async-graphql = "7.2.1"`, `async-graphql-axum = "7.2.1"`, `axum = "0.8.9"`, `tokio = "1.52.3"`; `cargo build --all-targets` exits 0 |
| 8  | Config exposes a `bind_address` field with a loopback default when omitted from YAML                                                                                          | VERIFIED   | `config.rs:41` `pub bind_address: String`, default `"127.0.0.1:8080"` (CR-01 security fix applied post-plan); test `test_bind_address_default` passes |

**Note on truth 8:** The plan specified the default as `"0.0.0.0:8080"`. The code defaults to `"127.0.0.1:8080"` (loopback) per CR-01, a security improvement applied during code review. The stricter default is better than the plan required — this is not a failure.

**Note on plan 04-02 truth about depth/complexity limiters:** The plan stated "The schema registers NO async-graphql depth or complexity limiter" (D-06). The code has `.limit_depth(12)` and `.limit_complexity(2000)` added as WR-01 review fix. This contradicts the plan text but is a security improvement that supersedes it. The ROADMAP success criteria (the authoritative contract) do not mention this constraint — all 6 ROADMAP SCs are VERIFIED.

**Overall score:** 8/8 must-haves verified (6 ROADMAP SCs + 2 plan-level truths)

---

### Required Artifacts

| Artifact                         | Expected                                              | Status     | Details                                                                                     |
|----------------------------------|-------------------------------------------------------|------------|---------------------------------------------------------------------------------------------|
| `src/graphql/types.rs`           | Event/EventsPage/AuthorGroup/StatsResult + input types + mapping fn | VERIFIED   | 4 `SimpleObject` types, 2 `InputObject` types, `decoded_event_to_gql` fn all present and substantive |
| `src/graphql/mod.rs`             | Module root declaring types, schema, resolvers        | VERIFIED   | Declares `pub mod resolvers; pub mod schema; pub mod types;`                                |
| `src/graphql/schema.rs`          | AppState, AppSchema, build_schema                     | VERIFIED   | `pub struct AppState`, `pub type AppSchema`, `pub fn build_schema` — all present            |
| `src/graphql/resolvers.rs`       | Query root with all three resolvers                   | VERIFIED   | `pub struct Query`, `#[Object] impl Query` with `events`, `latest_per_author`, `stats`      |
| `src/server.rs`                  | build_router fn mounting POST/GET /graphql            | VERIFIED   | `pub fn build_router`, `get(graphiql).post_service(GraphQL::new(schema))`                   |
| `src/main.rs`                    | async tokio main with server startup after gates      | VERIFIED   | `#[tokio::main] async fn main`, `axum::serve`, `build_schema`, `bind_address`               |
| `src/config.rs`                  | bind_address field with default                       | VERIFIED   | `pub bind_address: String` with `#[serde(default = "default_bind_address")]`                |
| `Cargo.toml`                     | Four GraphQL crates at pinned versions                | VERIFIED   | async-graphql 7.2.1, async-graphql-axum 7.2.1, axum 0.8.9, tokio 1.52.3                    |
| `src/lib.rs`                     | pub mod graphql + pub mod server                      | VERIFIED   | Both `pub mod graphql` and `pub mod server` declared                                        |

---

### Key Link Verification

| From                         | To                                       | Via                                    | Status   | Details                                                                                           |
|------------------------------|------------------------------------------|----------------------------------------|----------|---------------------------------------------------------------------------------------------------|
| `src/graphql/resolvers.rs`   | `src/query/engine.rs` (execute_query)    | `tokio::task::spawn_blocking`          | WIRED    | `resolvers.rs:91` calls `execute_query`; result mapped to `EventsPage`                           |
| `src/graphql/resolvers.rs`   | `src/query/engine.rs` (latest_per_author)| `tokio::task::spawn_blocking`          | WIRED    | `resolvers.rs:159` calls `latest_per_author`; result mapped to `Vec<AuthorGroup>`                |
| `src/graphql/resolvers.rs`   | `EventPayload` sub-DB via read_stats     | `heed::Database.open()` (never create) | WIRED    | `resolvers.rs:311–376` opens EventPayload read-only, reads stat + last key                       |
| `src/server.rs`              | async-graphql-axum GraphQL service       | `post_service(GraphQL::new(schema))`   | WIRED    | `server.rs:53` — `get(graphiql).post_service(GraphQL::new(schema))`                              |
| `src/main.rs`                | `src/server.rs build_router`             | `build_router(schema)`                 | WIRED    | `main.rs:87` — `let router = build_router(schema);`                                              |
| `src/main.rs`                | `src/graphql/schema.rs build_schema`     | `build_schema(app_state)` after gates  | WIRED    | `main.rs:86` — after all 6 startup gates pass; reuses `env.clone()` and `meta.clone()`           |
| `src/graphql/types.rs`       | `src/lmdb/types.rs` (DecodedEvent)       | `decoded_event_to_gql` mapping fn      | WIRED    | `types.rs:190–202` — maps all fields; `raw` uses `from_utf8_lossy(&d.raw_json)`, never re-serializes |

---

### Data-Flow Trace (Level 4)

| Artifact                       | Data Variable       | Source                             | Produces Real Data | Status   |
|--------------------------------|---------------------|------------------------------------|--------------------|----------|
| `src/graphql/resolvers.rs`     | `decoded_events`    | `execute_query` → LMDB EventPayload| Yes — DB query     | FLOWING  |
| `src/graphql/resolvers.rs`     | `groups`            | `latest_per_author` → LMDB pubkeyKind index | Yes — DB scan | FLOWING  |
| `src/graphql/resolvers.rs`     | `event_count`, `max_lev_id` | `db.stat()` + `db.last()` on EventPayload | Yes — mdb_stat | FLOWING |
| `src/graphql/types.rs`         | `raw: String`       | `d.raw_json` bytes passthrough     | Yes — strfry bytes | FLOWING  |

No hollow props. All data variables traced to real LMDB read operations.

---

### Behavioral Spot-Checks

| Behavior                                     | Command                                                             | Result          | Status  |
|----------------------------------------------|---------------------------------------------------------------------|-----------------|---------|
| events limit clamp test                      | `cargo test --all-targets graphql::resolvers::tests::test_events_limit_clamp` | 9999→500, None→100, 0→1 | PASS    |
| perAuthor clamp test                         | `cargo test --all-targets graphql::resolvers::tests::test_per_author_clamp`  | 9999→500, 0→1   | PASS    |
| no mutation in SDL                           | `cargo test --all-targets graphql::resolvers::tests::test_no_mutation_in_schema_sdl` | SDL has no `type Mutation` | PASS |
| EventsPage cursor shape                      | `cargo test --all-targets graphql::resolvers::tests::test_events_page_shape_with_cursor` | has_more=true, end_cursor=Some | PASS |
| EventsPage no-cursor shape                   | `cargo test --all-targets graphql::resolvers::tests::test_events_page_shape_without_cursor` | has_more=false, end_cursor=None | PASS |
| events() end-to-end query through fixture    | `cargo test --all-targets graphql::resolvers::tests::test_events_query_basic` | no errors       | PASS    |
| stats() end-to-end query through fixture     | `cargo test --all-targets graphql::resolvers::tests::test_stats_query`        | dbVersion==3    | PASS    |
| Full test suite                              | `cargo test --all-targets`                                         | 107 passed, 0 failed | PASS  |
| Build                                        | `cargo build --all-targets`                                        | exit 0          | PASS    |

---

### Requirements Coverage

| Requirement | Source Plan | Description                                                                            | Status     | Evidence                                                                              |
|-------------|-------------|----------------------------------------------------------------------------------------|------------|---------------------------------------------------------------------------------------|
| API-01      | 04-01, 04-02 | Consumer can query `events()` filtered by ids, authors, kinds, since, until, limit    | SATISFIED  | `events` resolver + `build_nostr_filter` + engine `execute_query`                    |
| API-02      | 04-01, 04-02 | Consumer can query `events()` with a tag filter                                        | SATISFIED  | `TagFilterInput`; `build_nostr_filter` maps single tag to `NostrFilter.tags`          |
| API-03      | 04-02       | Consumer can query `latestPerAuthor(kind, perAuthor, authors)` for latest N per pubkey | SATISFIED  | `latest_per_author` resolver calls engine; maps HashMap to `Vec<AuthorGroup>`         |
| API-04      | 04-02       | Consumer can query `stats` (event count, max levId, dbVersion)                         | SATISFIED  | `stats` resolver + `read_stats`; `test_stats_query` asserts `dbVersion == 3`          |
| API-05      | 04-01, 04-02 | Hard limit ceiling + cursor pagination; no full-DB scans                               | SATISFIED  | Limit clamped ≤500, default 100; `PageCursor::decode/encode` at resolver boundary     |
| API-06      | 04-02       | API is read-only — no mutations                                                        | SATISFIED  | `Schema::build(Query, EmptyMutation, EmptySubscription)`; SDL test asserts no mutation|

All 6 requirements declared for this phase are SATISFIED. No orphaned requirements found — REQUIREMENTS.md traceability table marks all 6 Complete for Phase 4.

---

### Anti-Patterns Found

| File                              | Line | Pattern              | Severity | Impact                                                  |
|-----------------------------------|------|----------------------|----------|---------------------------------------------------------|
| `tests/body_limit_test.rs`        | 4, 63 | Stale doc comments referencing `DefaultBodyLimit` | INFO | Comments misdescribe the enforcement mechanism (now `RequestBodyLimitLayer`); no behavioral impact. Flagged as IN-01 by code review. |
| `src/server.rs`                   | 43–46 | `MAX_REQUEST_BODY_BYTES` doc does not name the enforcement layer | INFO | Cosmetic clarity gap only; no behavioral impact. Flagged as IN-02 by code review. |
| `src/graphql/resolvers.rs`        | 203–205 | `state_from` helper used only in one of three resolvers | INFO | Minor inconsistency; no behavioral impact. Flagged as IN-03 by code review. |

No TBD, FIXME, or XXX markers found in any phase-4 modified files.

No stub returns (`return null`, `return []`, empty implementations) found in resolver, schema, server, or types files.

The two WARNINGs from the final code review (WR-01: chunked-body bypass; WR-02: saturating cast on stats) are known, accepted residuals — not unresolved debt markers, not blockers for the phase goal.

**WR-01 (chunked body):** `RequestBodyLimitLayer` reliably rejects oversized `Content-Length` requests (test verified). The chunked streaming path is unverified — residual risk accepted per the review. The endpoint is an internal DeepFry sidecar co-located with strfry; the Docker compose network controls external access.

**WR-02 (saturating cast):** `i64::try_from(...).unwrap_or(i64::MAX)` on stats fields silently clamps rather than logging on overflow. Practically unreachable (would require > 9.2e18 events or levIds). Asymmetry with the adjacent `MalformedKey` loud-fail branch is a cosmetic inconsistency, not a correctness issue for any realistic database.

---

### Human Verification Required

None. All phase-4 success criteria are machine-verifiable and confirmed by:
- `cargo build --all-targets` exit 0
- 107 tests passing including 11 graphql-specific tests (7 in resolvers.rs + 4 in types.rs)
- SDL mutation-absence assertion running against the actual built schema

---

### Gaps Summary

No gaps. All 6 ROADMAP success criteria are VERIFIED by concrete codebase evidence:

1. `events()` resolver fully wired to engine, filter types defined, end-to-end test green.
2. Tag filter input type defined; single tag mapped to `NostrFilter.tags` in `build_nostr_filter`.
3. `latestPerAuthor` resolver fully wired to engine; per-author grouping preserved.
4. `stats` resolver reads real `mdb_stat` + last key from EventPayload; `dbVersion` from Meta.
5. Hard limit ceiling clamped at API layer (≤500, default 100); `PageCursor` encode/decode wired.
6. `EmptyMutation` structural guarantee; SDL test asserts no mutation root in the generated SDL.

Two deviations from plan text were found, both beneficial improvements applied during code review:
- `bind_address` default changed from `0.0.0.0:8080` to `127.0.0.1:8080` (CR-01 security hardening).
- Depth (`12`) and complexity (`2000`) limiters added to schema (WR-01 security hardening); plan D-06 said no limiters, but the ROADMAP contract does not mention this constraint.

Neither deviation undermines the phase goal. The phase goal is fully achieved.

---

_Verified: 2026-06-13_
_Verifier: Claude (gsd-verifier)_
