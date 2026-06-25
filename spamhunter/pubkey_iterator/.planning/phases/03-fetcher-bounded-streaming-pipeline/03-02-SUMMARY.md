---
phase: 03-fetcher-bounded-streaming-pipeline
plan: 02
subsystem: api
tags: [tokio, flume, rayon, bounded-channel, back-pressure, async-sync, streaming-pipeline, graphql]

# Dependency graph
requires:
  - phase: 03-fetcher-bounded-streaming-pipeline (Plan 01)
    provides: "fetch_batch (match-by-author + 413 shrink-retry), AuthorGroup/Event structs, latest_per_author client wrapper, read_pubkeys enumeration source (D-07)"
  - phase: 02-graphql-client-author-enumeration
    provides: "GraphQlClient, ClientError taxonomy (Unavailable/Transport/PayloadTooLarge/Graphql), enumerate::retry"
provides:
  - "run_pipeline: the bounded-memory async->sync streaming pipeline (tokio fetch -> flume::bounded -> std::thread/rayon drain) with an injected fetch source AND an injected consumer closure"
  - "consume_noop: the Phase-3 no-op pass-through consumer (D-06), and run_pipeline_noop convenience wrapper"
  - "DEFAULT_CHANNEL_CAP=64, DEFAULT_AUTHORS_PER_CALL=250 named tuning constants"
  - "rayon 1.12 dependency (Phase-3-owned add)"
  - "The Phase-4 consumer seam: swap the Layer/combiner stage at run_pipeline's consumer parameter with no channel/fetcher change"
affects: [04-detection-layers, phase-5-cli, scoring, combiner]

# Tech tracking
tech-stack:
  added: [rayon-1.12]
  patterns:
    - "Bounded back-pressure via flume::bounded + tx.send_async().await (the await IS the back-pressure)"
    - "async<->sync boundary: producer send_async on the tokio runtime, consumer blocking recv() on a dedicated std::thread joined after drop(tx)"
    - "Injected fetch source + injected consumer closure (generics) so tests mock without HTTP and Phase 4 swaps the consumer additively"
    - "Structural watermark proof (in-flight = sent - consumed) instead of OS-RSS measurement"

key-files:
  created:
    - src/pipeline.rs
  modified:
    - src/lib.rs
    - Cargo.toml
    - Cargo.lock

key-decisions:
  - "channel_cap default = 64 (RESEARCH Open Q2); authors_per_call default = 250 (<=1000 contract cap, backstopped by Plan-01 413 split)"
  - "Consumer drain runs on a dedicated std::thread (not rayon::spawn) so it owns a JoinHandle the async fn joins after drop(tx) — mirrors Store::close()'s writer join, closing the dangling-consumer trap (Pitfall 3)"
  - "run_pipeline is parameterized over BOTH the fetch source (F) and the consumer (C): mock fetch for tests, fetch_batch for prod; no-op for Phase 3, Layer stage for Phase 4"
  - "Watermark test asserts the STRUCTURAL bound (sent - consumed <= cap + authors_per_call), not OS RSS — deterministic and CI-safe"

patterns-established:
  - "Pattern: bounded streaming pipeline — flume::bounded is load-bearing; the unbounded writer channel pattern (store/mod.rs) is deliberately NOT copied for the group channel"
  - "Pattern: live integration check self-skips on ClientError::Unavailable/Transport with an eprintln deferred-manual note (D-09), never failing CI"

requirements-completed: [INGEST-03, INGEST-02]

# Metrics
duration: 6min
completed: 2026-06-25
status: complete
---

# Phase 03 Plan 02: Bounded Streaming Pipeline Summary

**run_pipeline wires enumerated pubkeys through a tokio fetcher -> flume::bounded(64) channel -> a std::thread/rayon drain applying an injected consumer closure, with a 100k-author structural watermark proof that peak in-flight memory is bounded by channel capacity (not corpus size) and an end-to-end no-drop count.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-06-25T15:33:38Z
- **Completed:** 2026-06-25T15:39:56Z
- **Tasks:** 3
- **Files modified:** 4 (1 created, 3 modified)

## Accomplishments
- `run_pipeline` — the async<->sync concurrency heart of Phase 3 (INGEST-03 / D-05): producer awaits an injected fetch and `tx.send_async()`s into a BOUNDED flume channel (the await is the back-pressure); a dedicated `std::thread` blocking-`recv()`s and applies an injected `Fn(&AuthorGroup)` consumer OFF the tokio runtime, joined after `drop(tx)`.
- `consume_noop` (D-06) + `run_pipeline_noop` wrapper — the Phase-3 pass-through that proves end-to-end flow; `run_pipeline`'s consumer parameter is the single Phase-4 seam.
- Headline bounded-memory proof `bounded_memory_watermark`: 100k synthetic authors + slow consumer, in-flight watermark (sent - consumed) asserted `<= channel_cap + authors_per_call`, structural not RSS (success criterion #3 / D-08), and proven non-vacuous (watermark > cap).
- `pipeline_endtoend_count` (no drops, count == exact total — #4 / D-06) and `injected_consumer_seam` (caller closure observes exactly the produced groups).
- `live_latest_per_author` (D-09): a real `latest_per_author` against the CONTEXT adapter deserialized into `Vec<AuthorGroup>` (confirmed reachable, 1 group), self-skipping on Unavailable/Transport with a deferred note — never blocks CI.
- rayon 1.12 declared (Phase-3-owned); manifest reservation comment updated.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the rayon dependency** - `93f4743` (chore)
2. **Task 2 + Task 3: run_pipeline pipeline + watermark/count/seam tests + live D-09 check** - `11cf1f8` (feat)

_Task 2 (pipeline + tests) and Task 3 (live D-09 test) share `src/pipeline.rs` and its test module, so they landed in one GREEN feat commit. Tests and implementation were authored together (TDD: the four behavior tests were the spec; the watermark instrumentation was iterated to a passing structural bound before commit)._

**Plan metadata:** committed separately (this SUMMARY) under `docs(03-02)`.

## Files Created/Modified
- `src/pipeline.rs` - NEW. `run_pipeline` (tokio fetch -> flume::bounded -> std::thread drain with injected consumer), `consume_noop`, `run_pipeline_noop`, the tuning consts, and the four tests (watermark / count / seam / live D-09).
- `src/lib.rs` - registered `pub mod pipeline` with a doc comment describing the bounded-memory pipeline and the Phase-4 seam.
- `Cargo.toml` - added `rayon = "1.12"`; updated the dep-reservation comment so Phase 3 owns rayon (clap/gaoya/linfa reservations left intact).
- `Cargo.lock` - resolved rayon 1.12.0 (+ rayon-core, crossbeam-*).

## Decisions Made
- **channel_cap = 64, authors_per_call = 250** as named constants (`DEFAULT_CHANNEL_CAP`, `DEFAULT_AUTHORS_PER_CALL`), documented as empirically tunable; 250 is well under the contract §12 ≤1000 cap and the Plan-01 413 split is the safety net.
- **Dedicated std::thread consumer** (over `rayon::spawn`) so it carries a `JoinHandle` the async fn joins after `drop(tx)`, mirroring `Store::close()` — the cleanest fix to Pitfall 3 (dangling consumer). Per-group rayon fan-out remains available inside the drain body for Phase 4.
- **Both fetch and consumer injected** as generic parameters — the watermark/count/seam tests need no HTTP, and Phase 4 swaps the Layer stage at the consumer seam with no transport change.

## Deviations from Plan
None - plan executed exactly as written. All three tasks and four tests landed as specified; the watermark instrumentation lives in the test (a send-side counter + a difference-reading consumer), keeping the production `run_pipeline` path clean as the plan required.

## Issues Encountered
- **Watermark test instrumentation iterated once.** The first cut incremented an in-flight counter inside the mock `fetch` and decremented in the consumer using a paired inc/dec; under a 1µs consumer sleep the watermark read the full 100k corpus (the inc/dec pair was observed at the wrong instant and back-pressure was not forced). Resolved by (a) tracking two monotonic counters (`sent`, `consumed`) and computing the live difference `sent - consumed` at both the send and consume sites, and (b) using a real 50µs consumer sleep so `send_async` genuinely suspends. The structural bound (`<= cap + authors_per_call`) then held and is proven non-vacuous (`watermark > cap`). This is a test-harness refinement, not a production-code change.

## User Setup Required
None - no external service configuration required. The live D-09 test targets the operator-supplied adapter at `http://192.168.149.21:8080/graphql` (override via `LMDB2GRAPHQL_URL`) and self-skips when unreachable.

## Verification
- `cargo build` — clean (rayon resolved).
- `cargo test --lib` — 44 passed / 0 failed (40 pre-existing + 4 new: watermark, count, seam, live).
- `cargo clippy --all-targets -- -D warnings` — clean (no warnings).
- `cargo fmt --check` on `src/pipeline.rs` — clean (0 diffs). NOTE: the repo has pre-existing rustfmt drift in other files (enumerate.rs, store/*, graphql/queries.rs, main.rs) from a prior rustfmt version; those are out of scope for this plan and were not touched.
- Live D-09 phase-gate confirmation: `cargo test --lib live_latest_per_author -- --nocapture` deserialized 1 real `AuthorGroup` from the adapter (D-09 OK).

## Next Phase Readiness
- The async<->sync boundary and the Phase-4 consumer seam are locked: Phase 4 replaces the `consumer: C` closure at `run_pipeline` with the Layer/combiner stage (and may fan out per-group with `rayon::par_iter`/`rayon::spawn` inside the drain body) with no channel or fetcher change.
- Production wiring (a closure calling `fetch::fetch_batch` over a real `GraphQlClient`, sourced from `read_pubkeys` on `Store::reader()`) is the remaining glue; the structural pipeline it plugs into is proven.
- No blockers.

## Self-Check: PASSED
- FOUND: src/pipeline.rs
- FOUND: .planning/phases/03-fetcher-bounded-streaming-pipeline/03-02-SUMMARY.md
- FOUND commit: 93f4743 (Task 1, rayon)
- FOUND commit: 11cf1f8 (Task 2+3, pipeline + tests + live D-09)

---
*Phase: 03-fetcher-bounded-streaming-pipeline*
*Completed: 2026-06-25*
