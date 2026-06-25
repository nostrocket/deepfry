---
phase: 2
slug: graphql-client-author-enumeration
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-25
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust built-in `#[test]` + `#[tokio::test]` (async transport tests) |
| **Config file** | none — `cargo test` (matches Phase 1 convention) |
| **Quick run command** | `cargo test --lib` |
| **Full suite command** | `cargo test` |
| **Estimated runtime** | ~10 seconds (mocked adapter; no network) |

---

## Sampling Rate

- **After every task commit:** Run `cargo test --lib`
- **After every plan wave:** Run `cargo test`
- **Before `/gsd-verify-work`:** Full suite must be green
- **Phase gate:** full suite green **+** one manual live-adapter run completing a full walk (connectivity proof)
- **Max feedback latency:** ~10 seconds

---

## Per-Task Verification Map

| Criterion / Req | Behavior | Test Type | Automated Command | File Exists |
|---------|----------|-----------|-------------------|-------------|
| C1 / INGEST-01 — clean termination | Loop terminates when `hasMore` is false / `endCursor` null; visits each page once | unit (mock returns 3 pages then null) | `cargo test enumerate::tests::terminates_on_null_cursor` | ❌ W0 |
| C1 / INGEST-01 — resumability | Resume continues from stored `last_cursor`; no `--run-id` required | unit (seed `run` row w/ cursor, assert walk starts at `after`) | `cargo test enumerate::tests::resume_from_last_cursor` | ❌ W0 |
| C1 / D-04 — pubkey persistence | Each enumerated pubkey lands in `pubkey` via INSERT OR IGNORE; resume overlap idempotent | unit (count `pubkey` rows after overlapping resume) | `cargo test enumerate::tests::pubkeys_idempotent_on_resume` | ❌ W0 |
| C3 / INGEST-04 — `503` retry, no advance | `503` triggers bounded retry; cursor unchanged on abort | unit (mock 503×N then success / then exhaust→abort) | `cargo test enumerate::tests::retry_503_no_cursor_advance` | ❌ W0 |
| C3 / INGEST-04 — `INVALID_CURSOR` restart | in-body `INVALID_CURSOR` resets `after=null`, restarts page 1 | unit (mock returns INVALID_CURSOR once) | `cargo test enumerate::tests::invalid_cursor_restarts` | ❌ W0 |
| C3 / INGEST-04 — in-body errors not dropped | `200` body with `errors[]` surfaced, never treated as success | unit (envelope parse test) | `cargo test graphql::tests::inbody_errors_surface` | ❌ W0 |
| C3 / D-07 — abort marks `aborted`, preserves cursor | On retry exhaustion: `status='aborted'`, `last_cursor` unchanged | unit (assert run row after forced abort) | `cargo test enumerate::tests::abort_preserves_cursor` | ❌ W0 |
| C4 / D-09 — drift probe | `max_lev_id_start`/`_end` recorded; differing values do NOT abort | unit (mock `stats` returns differing maxLevId start vs end) | `cargo test enumerate::tests::records_drift_does_not_abort` | ❌ W0 |
| C1 — resume-boundary union (property) | Arbitrary page splits + resume cut at any boundary ⇒ union of persisted pubkeys == full mocked set | property / held-out | `cargo test enumerate::tests::resume_boundary_union_complete` | ❌ W0 |
| Connectivity proof (vertical slice) | Real walk against live adapter completes, real v1.2 response deserializes | manual / integration (gated on live adapter) | `LMDB2GRAPHQL_URL=… cargo run -- <walk subcommand>` (manual) | ❌ requires live adapter |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky — all rows ⬜ pending at plan time.*

---

## Wave 0 Requirements

- [ ] `src/enumerate.rs` `#[cfg(test)]` — the 8 unit behaviors + 1 property test above (cursor loop, termination, resume, retry/backoff, error-taxonomy mapping, drift recording, abort/cursor-preservation, pubkey idempotency, resume-boundary union)
- [ ] `src/graphql/` `#[cfg(test)]` — envelope + query parse tests using fixture JSON from contract §7 / §6.4 examples
- [ ] HTTP mock harness — **decide:** add `wiremock = "0.6"` dev-dep (verify version via `cargo search`) **OR** a hand-rolled `tokio` stub serving canned JSON. Prefer hand-rolled if a single canned-response server suffices (dep discipline)
- [ ] `run`-state store helper tests — extend `src/store/mod.rs` `#[cfg(test)]` for `latest_unfinished_run`, `set_run_cursor`, `mark_run_aborted`/`done`
- [ ] **Injectability:** `GraphQlClient` must take the endpoint URL as a field (no hardcoded URL) so tests point it at the mock

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Connectivity proof — full walk against live adapter | Phase goal (vertical slice) | Requires a reachable live LMDB2GraphQL adapter; cannot run in CI | Point `LMDB2GRAPHQL_URL` at a live adapter, run the walk subcommand, confirm it completes, terminates on `hasMore: false`, and the hand-written query string + serde structs deserialize the real v1.2 response. Mark as a manual `must_have`. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
