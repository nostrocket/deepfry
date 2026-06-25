---
phase: 02-graphql-client-author-enumeration
plan: 03
subsystem: api
tags: [graphql, reqwest, tokio, sqlite, rusqlite, pagination, cursor, resume, backoff]

# Dependency graph
requires:
  - phase: 02-01-graphql-client-author-enumeration
    provides: "Store single-writer API (insert_pubkeys, latest_unfinished_run, set_run_cursor, set_run_max_lev_start/end, mark_run_aborted, mark_run_done, begin_run, close)"
  - phase: 02-02-graphql-client-author-enumeration
    provides: "GraphQlClient (injectable endpoint, authors()/stats() wrappers, two-layer ClientError taxonomy)"
provides:
  - "enumerate::run — the authors opaque-cursor walk with bounded retry, abort, drift probe, and flush-before-cursor ordering"
  - "Store::flush — a WriteMsg::Flush ack barrier that makes the prior pubkey batch durable before the run cursor advances"
  - "minimal --resume binary entry point (src/main.rs) driving the connectivity-proving vertical slice"
affects: [03-fetch-pipeline, 04-scoring, 05-cli]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Opaque-cursor pagination loop (pass endCursor verbatim as after; null endCursor ⇒ clean termination)"
    - "Bounded exponential backoff retry (3 attempts, 250ms→2s) with a retryable/non-retryable error classifier"
    - "Flush-before-cursor durability barrier via a WriteMsg::Flush ack through the single writer actor"
    - "Scripted multi-request loopback HTTP stub for sequential-walk unit tests (no wiremock dep)"

key-files:
  created:
    - src/enumerate.rs
    - src/main.rs
  modified:
    - src/lib.rs
    - src/model.rs
    - src/store/mod.rs
    - src/store/writer.rs
    - Cargo.toml

key-decisions:
  - "Added Store::flush() + WriteMsg::Flush ack barrier (Rule 2) — set_run_cursor uses a separate short-lived connection that could otherwise commit the cursor before the async writer flushed the pubkey batch, violating the D-07 flush-before-cursor durability invariant on crash"
  - "Dropped PartialEq from WriteMsg (flume::Sender is not PartialEq); no caller depended on it"
  - "LIMIT=500 page size (contract §6.4 ceiling, Discretion A4) — minimizes round-trips"
  - "On resume keep the original max_lev_id_start when set; only record it when null (RESEARCH Open Q2)"
  - "INVALID_CURSOR handled by a distinct restart branch (after=None, continue) before the retry helper — coded Graphql errors are non-retryable in is_retryable, so they surface immediately (Pitfall 4)"

patterns-established:
  - "Pagination: fetch_page_with_retry → validate pubkeys → insert_pubkeys → flush → set_run_cursor → advance after"
  - "Error classification: is_retryable maps 503/transport/codeless-Graphql → retry; 413/coded-Graphql → fail-fast"

requirements-completed: [INGEST-01, INGEST-04]

# Metrics
duration: ~35min
completed: 2026-06-25
status: complete
---

# Phase 2 Plan 3: Authors Opaque-Cursor Walk + Minimal --resume Binary Summary

**Resumable opaque-cursor `authors` enumeration with bounded 503 backoff, abort-preserves-cursor, INVALID_CURSOR restart, snapshot-drift recording, and a crash-durable flush-before-cursor barrier — proven end-to-end against the live LMDB2GraphQL adapter (3500→5500 real pubkeys across a resume).**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-06-25T22:45:43+08:00
- **Tasks:** 2 (Task 1 via TDD: RED → GREEN)
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments
- `enumerate::run`: the strictly-sequential pagination loop composing the store (plan 01) and the GraphQL client (plan 02) into the connectivity-proving vertical slice (INGEST-01 / INGEST-04).
- Crash-durable flush-before-cursor ordering: a new `WriteMsg::Flush` ack barrier + `Store::flush()` guarantee the page's pubkeys are committed before the run cursor is made durable (closes the async-writer-vs-cursor-connection race the store API otherwise had).
- Minimal `#[tokio::main]` `--resume` binary (`src/main.rs`) — first binary in the previously library-only crate.
- Live connectivity proof PASSED: a bounded walk against `http://192.168.149.21:8080/graphql` enumerated 3500 real distinct pubkeys, the hand-written query + serde structs deserialized the real v1.2 response, `max_lev_id_start` matched live `stats.maxLevId` (47928105), and `--resume` reused the same run from the stored cursor (no `--run-id`) growing the persisted set to 5500 with the run-row count staying at 1.

## Task Commits

Each task was committed atomically:

1. **Task 1 (RED): failing tests for the authors walk** - `90c17de` (test)
2. **Task 1 (GREEN): implement the authors opaque-cursor walk** - `c662359` (feat)
3. **Task 2: minimal --resume binary entry point** - `0681bab` (feat)

_Task 1 followed TDD: a RED test commit then a GREEN implementation commit._

## Files Created/Modified
- `src/enumerate.rs` (created) - `enumerate::run` walk + `fetch_page_with_retry` + `is_retryable` + `is_valid_pubkey` + `EnumerateError`; the 8-behavior unit suite + resume-boundary union property test against a scripted multi-request loopback stub.
- `src/main.rs` (created) - minimal `#[tokio::main]` entry point: parses `--resume`, resolves `LMDB2GRAPHQL_URL` (default loopback), opens the store, drives `enumerate::run`, `close()`s to flush durably.
- `src/lib.rs` (modified) - registered `pub mod enumerate;`.
- `src/model.rs` (modified) - added `WriteMsg::Flush(flume::Sender<()>)`; dropped `PartialEq` derive from `WriteMsg`.
- `src/store/mod.rs` (modified) - added `Store::flush()` barrier + a `flush_makes_prior_pubkeys_durable` test.
- `src/store/writer.rs` (modified) - the writer collects `Flush` acks and releases them only after `tx.commit()`.
- `Cargo.toml` (modified) - declared the `[[bin]] pubkey_iterator` target.

## Decisions Made
- **Store::flush() barrier (Rule 2, see Deviations).** Required for the D-07 flush-before-cursor durability invariant to hold on crash, not just in source order.
- **Page LIMIT = 500** (contract §6.4 ceiling): round-trips dominate for O(distinct authors) seek-skip; 500 minimizes them. ~35 KB response, far under the 256 KiB request limit.
- **Resume keeps the original `max_lev_id_start`** when already set; only records it when null (RESEARCH Open Q2) — faithful to "start of this run's walk".
- **Hand-rolled scripted stub server** (no `wiremock` dev-dep): the walk is sequential, so a multi-request FIFO loopback stub serving canned responses suffices, with a request counter to assert no-retry on INVALID_CURSOR.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added a flush-before-cursor durability barrier (`Store::flush` + `WriteMsg::Flush`)**
- **Found during:** Task 1 (implementing the walk loop)
- **Issue:** The plan's loop calls `insert_pubkeys` (async writer actor, batch-committed lazily) then `set_run_cursor` (a separate short-lived connection via `run_write_conn`). The plan text asserted "both route through the single writer," but `set_run_cursor` does NOT — it opens its own connection. Without a barrier, the cursor write could commit before the writer flushed the pubkey batch, so a crash between them would resume PAST unwritten pubkeys → silent gaps. This violates `must_haves` truth #3 and D-07 ("a failure never advances the cursor past unwritten pubkeys"), and is exactly RESEARCH A5 / Pitfall 2's flagged MEDIUM-risk choice.
- **Fix:** Added `WriteMsg::Flush(flume::Sender<()>)` — the writer commits its current transaction then acks. Added `Store::flush()` which sends a Flush and blocks on the ack. The walk calls `store.flush()?` between `insert_pubkeys` and `set_run_cursor`, so the page's pubkeys are durably committed before the cursor advances. Preserves the single-writer invariant (the barrier routes through the one writer thread; no second write connection for the actor's tables).
- **Files modified:** src/model.rs, src/store/writer.rs, src/store/mod.rs, src/enumerate.rs
- **Verification:** New `store::tests::flush_makes_prior_pubkeys_durable` proves a pubkey is visible to a fresh reader connection after `flush()` returns WITHOUT a `close()`. All 30 lib tests + clippy `-D warnings` green.
- **Committed in:** c662359 (Task 1 GREEN commit)

---

**Total deviations:** 1 auto-fixed (1 missing-critical durability requirement)
**Impact on plan:** The flush barrier is essential for the D-07 crash-safety invariant the plan's `must_haves` explicitly require. No scope creep — the addition is minimal and routes through the existing single-writer seam. `WriteMsg` lost its `PartialEq` derive (flume::Sender is not PartialEq); no caller depended on it (verified by grep).

## Issues Encountered
- macOS has no `timeout`/`gtimeout` by default — ran the live connectivity walk as a backgrounded process with a manual `sleep` + `kill` to bound it. The corpus is large (`maxLevId` ~47.9M), so a full walk is long; a bounded walk + a `--resume` continuation is the documented connectivity proof.
- Clippy flagged a `useless_vec` in the property test (`vec!` over a sliced/iterated literal) — switched to an array.

## Manual Connectivity Proof (the phase's vertical-slice gate)

This `must_have` requires a reachable live adapter and cannot run in CI. To reproduce:

1. Ensure the LMDB2GraphQL adapter is reachable (warm — `POST /graphql` returns data, not 503).
2. From a writable working directory (the binary writes `spamhunter.sqlite` in the cwd):
   ```
   LMDB2GRAPHQL_URL=http://<adapter-host>:8080/graphql cargo run --bin pubkey_iterator
   ```
   Watch the `enumerate: run N enumerated M distinct pubkeys` progress on stderr.
3. Interrupt mid-walk (large corpus), then resume:
   ```
   LMDB2GRAPHQL_URL=http://<adapter-host>:8080/graphql cargo run --bin pubkey_iterator -- --resume
   ```
4. Confirm in `spamhunter.sqlite`: `pubkey` rows grow across the resume, the `run` table has exactly one row (resume reused the run, no `--run-id`), `max_lev_id_start` equals the live `stats.maxLevId`, and on a full walk `status` becomes `done`.

**Result this session (against `http://192.168.149.21:8080/graphql`):** PASSED — 3500 pubkeys on the first bounded leg (`status=running`, cursor preserved, `max_lev_id_start=47928105` == live `stats.maxLevId`), then `--resume` continued the same run (run-row count stayed 1) to 5500 persisted pubkeys. The hand-written `AUTHORS_QUERY`/`STATS_QUERY` strings + serde structs deserialized the real v1.2 response with no shape mismatch.

## User Setup Required
None for the build/tests. The manual connectivity proof needs a reachable LMDB2GraphQL adapter (operator-supplied via `LMDB2GRAPHQL_URL`, default loopback `http://127.0.0.1:8080/graphql`).

## Next Phase Readiness
- INGEST-01 (resumable cursor enumeration, clean termination) and INGEST-04 (graceful adapter-error handling: 503 backoff-no-advance, INVALID_CURSOR restart, in-body errors parsed, drift record-and-continue) are satisfied, with the live connectivity proof documented.
- Phase 3 (fetch pipeline) can read the persisted `pubkey` table or extend the GraphQL client additively (D-11). The `WriteMsg` enum + `Store::flush()` seam are ready for concurrent fetchers feeding the single writer.
- No blockers.

## Self-Check: PASSED

- Files: `src/enumerate.rs`, `src/main.rs`, `02-03-SUMMARY.md` all present.
- Commits: `90c17de` (RED), `c662359` (GREEN), `0681bab` (binary) all in history.
- `cargo test` 30 passed / 0 failed; `cargo clippy --all-targets -- -D warnings` clean; `cargo build --bin pubkey_iterator` produces a runnable binary.

---
*Phase: 02-graphql-client-author-enumeration*
*Completed: 2026-06-25*
