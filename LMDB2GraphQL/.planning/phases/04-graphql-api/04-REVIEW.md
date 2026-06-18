---
phase: 04-graphql-api
reviewed: 2026-06-13T00:00:00Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - src/config.rs
  - src/graphql/mod.rs
  - src/graphql/resolvers.rs
  - src/graphql/schema.rs
  - src/graphql/types.rs
  - src/lib.rs
  - src/main.rs
  - src/server.rs
  - src/lmdb/indexes.rs
  - Cargo.toml
findings:
  critical: 0
  warning: 2
  info: 4
  total: 6
status: issues_found
---

# Phase 4: Code Review Report

**Reviewed:** 2026-06-13
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Final re-review (iteration 3) of the Phase-4 GraphQL API layer after two fix passes.
I verified that the four iteration-2 fixes are correctly implemented and introduced no
behavioral regressions:

- **WR-05** (negative `kind`/`kinds` rejection): `u64::try_from` is used at both call
  sites — `latest_per_author` (resolvers.rs:148) and `build_nostr_filter` (resolvers.rs:253).
  A negative value yields a clean client error rather than wrapping to a giant `u64`. Correct.
- **WR-06** (inverted-range guard): the guard is `if s > u` (resolvers.rs:274), so the
  legitimate `since == until` point-query is still allowed. Matches the engine's
  `since <= created_at <= until` semantics. No false rejection. Correct.
- **WR-07** (non-8-byte stats key + saturating casts): `read_stats` now fails loud with
  `MalformedKey` on a non-8-byte last key (resolvers.rs:355) instead of masking corruption
  as `max_lev_id = 0`, and both `usize→i64` and `u64→i64` casts saturate (resolvers.rs:341,364).
  The new `IndexError::MalformedKey` variant (indexes.rs:73) is well-formed. Correct.
- **WR-02-LAYER** (RequestBodyLimitLayer): the body cap now uses
  `tower_http::limit::RequestBodyLimitLayer` (server.rs:61), which enforces at the
  Content-Length / body-stream level and bites on the `.post_service(...)` Tower path —
  unlike the prior `DefaultBodyLimit`. `tests/body_limit_test.rs` exercises both the accept
  and 413-reject paths. Correct.

The security invariants hold: read-only LMDB (no `write_txn`/`.create()` in any reviewed
path), short per-query txns, hard limit/per-author ceilings (≤500) plus the new `MAX_AUTHORS`
cap, no mutation surface (`EmptyMutation`), and opaque client errors via `map_query_error`.

Remaining findings are two WARNINGs (a chunked-body bypass gap in the new body limit, and a
saturating-cast value-corruption edge that survives WR-07) and four INFO items (stale doc
comments left behind by the fix passes, plus the known WR-01-RESIDUAL Phase-5 deferral).
WR-01-RESIDUAL (complexity cost-model weighting) is treated as a known deferral, not a finding.

## Warnings

### WR-01: RequestBodyLimitLayer does not provably cap chunked / unknown-length request bodies

**File:** `src/server.rs:61`
**Issue:** `RequestBodyLimitLayer::new(256 KiB)` reliably rejects requests that declare an
oversized `Content-Length` — the case `tests/body_limit_test.rs::test_oversized_body_rejected`
covers (it explicitly sets the `content-length` header). However, a client that sends
`Transfer-Encoding: chunked` with **no** `Content-Length` is not rejected up-front; the layer
can only enforce the cap as the body stream is consumed, and that is only effective if the
downstream async-graphql `post_service` actually reads the body through the wrapped
(length-limited) body. The single integration test only proves the Content-Length fast-path; it
does not prove the streaming path is bounded, so the residual amplification risk WR-02 was raised
to close is unverified for chunked uploads.
**Fix:** Add a chunked-body test that omits `Content-Length` and streams > 256 KiB, asserting it
is not buffered to completion (413 or an early error). If it fails, pair the layer with an
explicit limited-body guard or a global request timeout/concurrency bound:
```rust
// test: no content-length, chunked, > cap → must not 200 after full buffering
let req = Request::builder()
    .method("POST").uri("/graphql")
    .header("content-type", "application/json")
    .body(Body::from_stream(oversized_chunk_stream())) // no content-length
    .unwrap();
let resp = router.oneshot(req).await.unwrap();
assert_ne!(resp.status().as_u16(), 200); // currently unverified
```

### WR-02: WR-07 saturating cast silently corrupts `max_lev_id`/`event_count` instead of erroring

**File:** `src/graphql/resolvers.rs:341,364`
**Issue:** WR-07 replaced the wrapping casts with `i64::try_from(...).unwrap_or(i64::MAX)`. This
trades a wrap (negative value) for a *silent clamp* on a stats/health endpoint. If a real `levId`
ever exceeds `i64::MAX` (≈9.2e18) or `stat.entries` exceeds `i64::MAX`, the endpoint reports
`i64::MAX` — a plausible-looking but wrong value — with no error and no log line. On the very
`stats`/health surface WR-07's own comment says must not mask anomalies (resolvers.rs:352), a
silent clamp re-introduces misreporting for the overflow case while the adjacent malformed-key
branch correctly fails loud. The two overflow branches are inconsistent with the fail-loud
rationale they sit beside.
**Fix:** For symmetry with the `MalformedKey` branch, log (or error) when saturation actually
fires so the wrong value is not silent:
```rust
let event_count: i64 = i64::try_from(stat.entries).unwrap_or_else(|_| {
    tracing::error!(entries = stat.entries, "event_count exceeds i64::MAX — clamping (stats inaccurate)");
    i64::MAX
});
```

## Info

### IN-01: Stale doc comment in body_limit_test.rs references the replaced DefaultBodyLimit

**File:** `tests/body_limit_test.rs:4-7,63`
**Issue:** The fix pass swapped `DefaultBodyLimit` for `RequestBodyLimitLayer` in `server.rs`,
but the test's module doc still says the cap "is applied to the axum `Router` via
`DefaultBodyLimit::max(...)`" (line 4) and `test_oversized_body_rejected`'s doc-comment says
"proving DefaultBodyLimit is enforced" (line 63). Both are now false and misdescribe which
mechanism is under test.
**Fix:** Update both comments to reference `RequestBodyLimitLayer` (matching `server.rs:55-61`).

### IN-02: server.rs MAX_REQUEST_BODY_BYTES doc does not name the enforcement mechanism

**File:** `src/server.rs:43-46`
**Issue:** The `MAX_REQUEST_BODY_BYTES` doc block (lines 43-46) describes the cap generically and
does not mention that a Tower layer (not an extractor limit) enforces it; the mechanism note
lives only in the separate WR-02-LAYER comment on the `.layer(...)` call (lines 55-60). A reader
skimming the constant's doc alone would not know how the cap is enforced.
**Fix:** Cross-reference the layer comment from the constant's doc, or fold the mechanism note in.

### IN-03: `state_from` helper is applied to only one of three resolvers

**File:** `src/graphql/resolvers.rs:203-205`
**Issue:** `state_from` is documented as reducing repetition (comment line 200:
"reduces repetition in latest_per_author"), but `events` (line 69) and `stats` (line 188) call
`ctx.data_unchecked::<AppState>()` directly while only `latest_per_author` (line 154) uses the
helper. The helper neither reduces repetition nor is consistently applied — it is a single-call
indirection. Minor inconsistency, not a bug.
**Fix:** Either route all three resolvers through `state_from`, or inline it and delete the helper.

### IN-04: WR-01-RESIDUAL — complexity limit uses uniform field cost (Phase-5 deferral, noted)

**File:** `src/graphql/schema.rs:74-77`
**Issue:** `.limit_complexity(2000)` applies a uniform per-field cost. A single request with many
aliased `events`/`latestPerAuthor` resolvers — each scheduling its own blocking LMDB scan — is
bounded only by flat field count, not by the relative cost of a scan resolver vs. a scalar field.
The schema comment (lines 59-64) already documents this as the intended vector to bound; a
weighted cost model is the proper mitigation. Per the review brief this is an **intentional
Phase-5 deferral**, recorded here for traceability, not as a blocker.
**Fix:** Phase 5 — assign per-resolver weights via `#[graphql(complexity = ...)]` so each blocking
scan resolver costs proportionally more than a scalar field, and lower the aggregate ceiling.

---

_Reviewed: 2026-06-13_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
