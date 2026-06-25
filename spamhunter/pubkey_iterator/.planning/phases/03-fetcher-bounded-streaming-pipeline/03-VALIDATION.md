---
phase: 3
slug: fetcher-bounded-streaming-pipeline
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-25
---

# Phase 3 — Validation Strategy

> Per-phase validation contract. Source: 03-RESEARCH.md §Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `cargo test` (built-in) + `#[tokio::test]` for async paths |
| **Config file** | none — Cargo's built-in harness |
| **Quick run command** | `cargo test --lib` |
| **Full suite command** | `cargo test` (lib + live integration; live test self-skips when adapter unreachable) |
| **Estimated runtime** | ~10s (mocked); live check adds one network round-trip |

Reuse the existing hand-rolled loopback `stub_server` + `block_on` idiom (no `wiremock` dep). For the pipeline test, a mocked fetcher closure is cleaner than a stub server.

---

## Sampling Rate

- **After every task commit:** `cargo test --lib` (mocked, fast, deterministic)
- **After every wave:** `cargo test` (includes live check when adapter reachable)
- **Phase gate:** full suite green + manual confirmation the live `latestPerAuthor` deserialized against `http://192.168.149.21:8080/graphql`
- **Max feedback latency:** ~10s

---

## Phase Requirements → Test Map

| Req / Criterion | Behavior | Test Type | Automated Command | File Exists |
|--------|----------|-----------|-------------------|-------------|
| C1 / INGEST-02 | `latest_per_author` builds the query + deserializes a real-shaped `[AuthorGroup]` response | unit (stub_server) | `cargo test --lib latest_per_author` | ❌ W0 |
| C1 / D-02 | 413 → recursive batch split, no loss | unit (mock fetcher) | `cargo test --lib fetch_413_split` | ❌ W0 |
| C2 / D-04 | omitted (zero-match) author does not shift others; match by `author` | unit (pure fn) | `cargo test --lib match_groups_no_shift` | ❌ W0 |
| C3 / INGEST-03 / D-08 | bounded watermark ≤ CAP + batch over a large synthetic set; all groups consumed once | unit (mock fetcher + slow consumer + atomic watermark) | `cargo test --lib bounded_memory_watermark` | ❌ W0 |
| C4 / D-06 | no-op consumer drains end-to-end; count == synthetic total | unit (mock fetcher) | `cargo test --lib pipeline_endtoend_count` | ❌ W0 |
| D-09 | real adapter response deserializes into `[AuthorGroup]` | live integration (`#[tokio::test]`, self-skip if unreachable) | `cargo test --lib live_latest_per_author` | ❌ W0 |

---

## Wave 0 Requirements

- [ ] `src/fetch.rs` `#[cfg(test)]` — `latest_per_author` stub test, `match_groups` test, `fetch_413_split` test (C1, C2)
- [ ] `src/pipeline.rs` `#[cfg(test)]` — `bounded_memory_watermark`, `pipeline_endtoend_count` (C3, C4 / D-08)
- [ ] live integration test (env-gated / `#[ignore]`) — D-09
- [ ] shared test fixture: synthetic-author generator + mock-fetcher closure type
- [ ] framework install: none — `cargo test` + tokio macros already present

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live `latestPerAuthor` deserialization against the real adapter | INGEST-02 / D-09 | Requires a reachable live adapter; self-skips in CI | Point at `http://192.168.149.21:8080/graphql`, fetch a real author batch, confirm `[AuthorGroup]` deserializes. The green suite alone does NOT satisfy D-09 — act on the deferred note. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
