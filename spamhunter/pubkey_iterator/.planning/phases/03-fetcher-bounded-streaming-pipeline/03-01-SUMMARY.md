---
phase: 03-fetcher-bounded-streaming-pipeline
plan: 01
subsystem: pubkey_iterator (fetch slice)
tags: [graphql, fetch, latestPerAuthor, batching, 413, store-read]
requires:
  - "Phase-2 GraphQlClient (generic query<T>, ClientError taxonomy, authors/stats wrappers)"
  - "Phase-2 enumerate::retry bounded-backoff helper"
  - "Phase-1 Store (insert_pubkeys, reader, pubkey table)"
provides:
  - "graphql::latest_per_author(kind, perAuthor, authors) -> Vec<AuthorGroup>"
  - "graphql::{Event, AuthorGroup, LATEST_PER_AUTHOR_QUERY}"
  - "fetch::match_groups (match-by-author, omitted-author-safe)"
  - "fetch::fetch_batch (413 recursive shrink-retry over enumerate::retry)"
  - "store::queries::read_pubkeys (D-07 enumeration source)"
affects:
  - "Plan 03-02 bounded-channel pipeline (consumes fetch_batch + match_groups + read_pubkeys)"
tech-stack:
  added: []
  patterns:
    - "Additive D-11 client wrapper: new query const + serde structs + thin wrapper, no transport change"
    - "Match-by-author HashMap drain (never zip-by-index) for omitted-author safety"
    - "Recursive batch halving on non-retryable 413, distinct from the 503 retry policy"
    - "pub(crate) reuse of enumerate::retry — single backoff implementation"
key-files:
  created:
    - src/fetch.rs
  modified:
    - src/graphql/queries.rs
    - src/graphql/client.rs
    - src/graphql/mod.rs
    - src/store/queries.rs
    - src/enumerate.rs
    - src/lib.rs
decisions:
  - "Task 1 committed as one atomic feat (structs + tests interdependent; a standalone RED commit would not compile)"
  - "i64 for Event.kind/created_at per contract §8 (T-03-01 truncation mitigation), grep-gated"
  - "authors passed as GraphQL $authors variable via json!, never interpolated (T-03-02)"
  - "fetch_batch is the production impl; Plan 02 owns the mock-fetch injection seam at the pipeline boundary"
metrics:
  duration: ~25m
  completed: 2026-06-25
  tasks: 2
  files: 6
  tests-added: 8
  tests-total: 40
status: complete
---

# Phase 3 Plan 01: Fetcher Data-Layer Slice Summary

Additive `latestPerAuthor` fetch path on the Phase-2 GraphQL client: the query + serde structs + client wrapper, the author→pubkey matching that defuses the INGEST-04 zip-by-index landmine, the 413 recursive shrink-and-retry batch policy, and the `read_pubkeys` store read that is the pipeline's enumeration source (D-07).

## What Was Built

**Task 1 — additive `latestPerAuthor` query (commit `4f478b3`)**
- `LATEST_PER_AUTHOR_QUERY` const selecting `latestPerAuthor(kind:$kind,perAuthor:$perAuthor,authors:$authors){ author events{ id pubkey kind createdAt content tags } }` — `raw`/`sig` deliberately excluded (contract §9 #6).
- `Event { id, pubkey, kind: i64, created_at: i64, content, tags }`, `AuthorGroup { author, events }`, `LatestPerAuthorData { latest_per_author: Vec<AuthorGroup> }` (top-level is a LIST). `i64` for kind/createdAt per contract §8.
- `GraphQlClient::latest_per_author(kind, per_author, authors)` — thin wrapper over the generic `query::<LatestPerAuthorData>`, authors passed as a `json!` GraphQL variable. No transport change.
- Exported `Event`, `AuthorGroup`, `LATEST_PER_AUTHOR_QUERY` from the graphql module.
- Tests: deserialization (+ an i32-overflow `createdAt` proving the 64-bit decode is load-bearing), happy path through the wrapper, 413 surfacing `PayloadTooLarge`.

**Task 2 — `fetch_batch`, `match_groups`, `read_pubkeys` (commit `1379813`)**
- `fetch::match_groups(requested, groups)` — drains a `HashMap` keyed on `group.author`, mapping the requested list to `(pubkey, events_or_empty)`. One entry per requested author in request order; an omitted middle author yields an empty Vec, never a positional shift.
- `fetch::fetch_batch(client, kind, per_author, batch)` — normal path through `crate::enumerate::retry` (503/transport/codeless backoff); on `PayloadTooLarge` with `batch.len() > 1`, splits at the midpoint, recursively `Box::pin`s both halves, returns the union. A singleton 413 (or any other error) surfaces.
- `store::queries::read_pubkeys(conn)` — `SELECT pubkey FROM pubkey ORDER BY pubkey`, the deterministic D-07 enumeration source.
- `enumerate::retry` promoted to `pub(crate)` so `fetch_batch` reuses the single backoff helper rather than writing a new loop.
- `pub mod fetch` registered in lib.rs.
- Tests: `match_groups_no_shift`, `fetch_413_split` (size-gated loopback stub that 413s on oversized batches), `fetch_batch_happy`, `read_pubkeys_roundtrip`.

## Verification

- `cargo test --lib`: **40 passed**, 0 failed (32 baseline preserved + 8 new).
- `cargo build`: succeeds.
- `cargo clippy --all-targets -- -D warnings`: clean (fixed two lints introduced during dev — `cloned_ref_to_slice_refs` in a test, `elided_lifetimes` on `match_groups`).
- `cargo fmt --check`: all newly-authored/modified code is fmt-clean. Pre-existing fmt diffs across untouched files (enumerate.rs, store/mod.rs, writer.rs, main.rs, and the pre-existing `authors_response_deserializes` long-string line in queries.rs) were left as-is — the project does not enforce `cargo fmt --check` (lint discipline is "warns only") and reformatting untouched files is out of scope.

## Acceptance Criteria

| Criterion | Status |
|-----------|--------|
| `latest_per_author` issues correct query + deserializes `[AuthorGroup]` (INGEST-02) | met |
| D-02: 413 → recursive shrink-and-retry, no hard failure, no author lost | met (`fetch_413_split`) |
| D-04 / INGEST-04: match by `author`, omitted authors don't misalign | met (`match_groups_no_shift`) |
| D-07: persisted `pubkey` table readable as enumeration source | met (`read_pubkeys_roundtrip`) |
| T-03-01: kind/createdAt are i64 | met (grep-gated + overflow test) |
| T-03-02: authors as GraphQL variable, not interpolated | met |

## Deviations from Plan

**Auto-fixed issues (Rule 1 — build/lint blockers in own new code):**

1. **[Rule 3 - Blocking] `cloned_ref_to_slice_refs` clippy error.** The happy-path test passed `&[pk.clone()]`; clippy (`-D warnings`) rejected it. Replaced with `std::slice::from_ref(&pk)`. Files: `src/graphql/client.rs`. Folded into commit `4f478b3`.
2. **[Rule 3 - Blocking] `elided_lifetimes` clippy error.** `match_groups<'a>` had an explicit lifetime clippy flagged as elidable (single input reference). Simplified the signature to `match_groups(requested: &[String], ...) -> Vec<(&str, Vec<Event>)>`. Files: `src/fetch.rs`. Folded into commit `1379813`.

Both were lint failures in code authored within this plan (not pre-existing, not scope-creep). No architectural changes, no auth gates, no package installs.

## Known Stubs

None. `match_groups`, `fetch_batch`, `read_pubkeys`, and `latest_per_author` are fully wired against the real Phase-2 client and store. The Plan-02 mock-fetch injection seam is intentional and documented at the `fetch_batch` doc-comment — Plan 02 owns the pipeline-boundary injection.

## Threat Flags

None. No new network endpoint, auth path, or schema change beyond the `latestPerAuthor` query already accounted for in the plan's `<threat_model>` (T-03-01 i64, T-03-02 variable parameterization, T-03-03 413 shrink, T-03-04 error discipline reused unchanged).

## Self-Check: PASSED

- `src/fetch.rs` — FOUND
- `src/graphql/queries.rs`, `src/graphql/client.rs`, `src/graphql/mod.rs` — FOUND (modified)
- `src/store/queries.rs`, `src/enumerate.rs`, `src/lib.rs` — FOUND (modified)
- Commit `4f478b3` (Task 1) — FOUND
- Commit `1379813` (Task 2) — FOUND
