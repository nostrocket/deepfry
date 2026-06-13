---
phase: 04-graphql-api
fixed_at: 2026-06-13T00:00:00Z
review_path: .planning/phases/04-graphql-api/04-REVIEW.md
iteration: 2
findings_in_scope: 5
fixed: 4
skipped: 1
status: partial
---

# Phase 4: Code Review Fix Report

**Fixed at:** 2026-06-13
**Source review:** .planning/phases/04-graphql-api/04-REVIEW.md
**Iteration:** 2

**Summary:**
- Findings in scope: 5 (Critical: 0, Warning: 5)
- Fixed: 4
- Skipped: 1 (deferred to Phase 5)

All fixes were applied in an isolated git worktree, each verified with `cargo build`
+ full `cargo test` (107 unit + integration tests) before an atomic commit. The
worktree was fast-forwarded onto the working branch on completion.

## Fixed Issues

### WR-05: Negative `kind`/`kinds` silently wrap to giant `u64`

**Files modified:** `src/graphql/resolvers.rs`
**Commit:** fa4ad2b
**Applied fix:** Reused the WR-04 non-negative rejection pattern on the two missed sibling
inputs. In `latest_per_author`, `kind as u64` is replaced with `u64::try_from(kind)`,
returning `"kind must be a non-negative integer"` on a negative value rather than wrapping
`-1` to `18446744073709551615` and silently returning empty buckets. In `build_nostr_filter`,
the `f.kinds.map(|ks| ks.into_iter().map(|k| k as u64))` cast is replaced with a loop that
`u64::try_from`s each kind and returns the same client error on the first negative. This
closes the partial-fix inconsistency the reviewer flagged as the headline regression.

### WR-06: No `since > until` validation — silently returns an empty page

**Files modified:** `src/graphql/resolvers.rs`
**Commit:** aea76a2
**Applied fix:** After the existing non-negative checks in `build_nostr_filter`, added an
inverted-range guard: if both `since` and `until` are present and `since > until`, return
`"since must be <= until"` instead of passing the transposed bounds to the engine (which
would scan `created_at <= until` with a per-stream `since` floor and silently return zero
results).

### WR-07: `read_stats` silently coerces a non-8-byte key to `max_lev_id = 0`

**Files modified:** `src/graphql/resolvers.rs`, `src/lmdb/indexes.rs`
**Commit:** e424131
**Applied fix:** Added a new `IndexError::MalformedKey { name, expected, actual }` variant.
`read_stats` now matches on `db.last()` and, on a non-8-byte last `EventPayload` key, returns
`QueryError::Lmdb(MalformedKey)` — which maps to an opaque client "internal error" while being
logged internally — rather than `unwrap_or([0u8; 8])` masking corruption as `max_lev_id = 0`
(indistinguishable from an empty DB). The genuinely-empty case (`db.last()` is `None`) still
returns 0. Both the `usize -> i64` (`event_count`) and `u64 -> i64` (`max_lev_id`) casts now
saturate via `i64::try_from(...).unwrap_or(i64::MAX)` instead of wrapping to a negative value
above `i64::MAX`.

### WR-02-LAYER: body-size cap did not bite on the `post_service` GraphQL path

**Files modified:** `src/server.rs`, `Cargo.toml`, `Cargo.lock`, `tests/body_limit_test.rs` (new)
**Commit:** 044bf30
**Applied fix:** First wrote the integration test the reviewer asked for; it empirically
confirmed the defect — a 300 KiB POST to `/graphql` returned **200 OK** under axum's
`DefaultBodyLimit`, proving the cap was effectively unenforced on the async-graphql
`.post_service(...)` Tower-service path (DefaultBodyLimit relies on the `Bytes`/`String`
extractors, which a raw Tower service bypasses). Replaced `DefaultBodyLimit::max(...)` with
`tower-http`'s `RequestBodyLimitLayer::new(MAX_REQUEST_BODY_BYTES)`, which enforces at the
`Content-Length` / body-stream level regardless of extractor. Added `tower-http = { version
= "0.6", features = ["limit"] }` and dev-deps `tower` (`util`) + `http-body-util` for the
test. The test now asserts a small body is accepted (200) and an oversized body with a
`Content-Length` over the cap is rejected with **413 Payload Too Large**. WR-02 is now
genuinely enforced on the one route that matters.

## Skipped Issues

### WR-01-RESIDUAL: complexity limit uses default weight=1, so it does not bound per-resolver LMDB scan cost

**File:** `src/graphql/schema.rs:77`
**Reason:** skipped — deferred to Phase 5 hardening. The fix requires a genuine cost-model
design decision, not a mechanical change: choosing per-resolver complexity weights
(proportional to `limit` for `events`, and `per_author × authors.len()` for
`latest_per_author`) and a corresponding `limit_complexity` ceiling, plus documenting the
intended max concurrent scans per request. async-graphql's `#[graphql(complexity = "...")]`
attribute does support referencing field arguments, so the mechanism exists, but the
correct weights/ceiling (and validating the arg-expression evaluation across `Option<i32>`
and `Vec<String>` argument types) is a tuning exercise where a wrong value would either
reject legitimate queries or fail to bound real cost. The reviewer themselves classified
this as Warning-not-Blocker and explicitly tied "Document the intended max concurrent scans
per request" to the Phase-5 observability work already referenced for the fan-out risk.
Per the fix-pass guidance (apply WR-01-RESIDUAL only if well-defined and low-risk; otherwise
document as skipped-deferred), this is deferred rather than forcing a speculative change.
The existing `.limit_depth(12)` + `.limit_complexity(2000)` still reduce the attack surface
from "unbounded" to "bounded" in the interim.

**Original issue:** `.limit_complexity(2000)` charges every field the default weight of 1, so
a scan-bearing resolver costs only ~3 complexity and a single request can still alias ~600+
independent `events`/`latestPerAuthor` resolvers under the ceiling, each spawning its own
blocking LMDB scan.

---

_Fixed: 2026-06-13_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 2_
