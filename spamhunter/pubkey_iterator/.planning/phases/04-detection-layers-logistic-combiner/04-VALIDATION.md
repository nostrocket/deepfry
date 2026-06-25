---
phase: 4
slug: detection-layers-logistic-combiner
status: ready
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-25
---

# Phase 4 — Validation Strategy

> Per-phase validation contract. Source: 04-RESEARCH.md §Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Rust built-in `#[test]` + `cargo test` |
| **Config file** | none — Cargo convention; `tempfile` dev-dep for temp DB/config dirs |
| **Quick run command** | `cargo test --lib detect::` |
| **Full suite command** | `cargo test` |
| **Estimated runtime** | ~12s (mocked); live L0 check adds one round-trip |

---

## Sampling Rate

- **After every task commit:** `cargo test --lib detect::` (fast, no network)
- **After every wave:** `cargo test` (full; live L0 test self-skips if `:8081` down)
- **Phase gate:** full suite green before `/gsd-verify-work`
- **Max feedback latency:** ~12s

---

## Phase Requirements → Test Map (5 ROADMAP success criteria)

| Req / Criterion | Behavior | Test Type | Automated Command | File Exists |
|--------|----------|-----------|-------------------|-------------|
| SC1 / DETECT-01..05 | Each layer emits `xᵢ∈[0,1]` on synthetic fixtures; bound asserted | unit | `cargo test --lib detect::` | ❌ W0 |
| SC1 / DETECT-01 | L0 absence→sub-score, presence→0; integration vs live `:8081` (self-skip on outage) | integration | `cargo test --lib detect::whitelist::live_check` | ❌ W0 |
| SC1 / DETECT-02 | L1 SimHash determinism + repeated-content ratio on duplicate-heavy fixture | unit | `cargo test --lib detect::near_duplicate` | ❌ W0 |
| SC1 / DETECT-03/04 | L3 entropy band + density; L4 url/domain/tag ratios on fixtures | unit | `cargo test --lib detect::content_entropy detect::link_mention` | ❌ W0 |
| SC2 / SCORE-01 | combiner: single strong layer < τ, multi-signal ≥ τ (multi-signal-agreement) | unit | `cargo test --lib detect::combiner::multi_signal` | ❌ W0 |
| SC3 / SCORE-05 | scored pubkey → `score` row + per-layer `signal` rows w/ non-empty evidence JSON | integration | `cargo test --lib detect::persists_score_and_evidence` | ❌ W0 |
| SC4 / SCORE-04/OPS-03 | enable/disable a layer via config omits its signal row; no enforcement side-effects | unit | `cargo test --lib config:: detect::disabled_layer_omitted` | ❌ W0 |
| SC5 / OPS-02 | re-run same fixture corpus twice → byte-identical score/signal rows | integration | `cargo test --lib detect::rerun_deterministic` | ❌ W0 |
| D-15 | a zero-event pubkey still gets a `score` row | integration | `cargo test --lib fetch::zero_event_gets_score` | ❌ W0 |

---

## Wave 0 Requirements

- [ ] `src/detect/mod.rs` + per-layer modules with unit tests over synthetic `Event` fixtures (reuse the `ev(author, idx)` helper pattern from pipeline.rs/fetch.rs tests)
- [ ] `src/config.rs` with a path-arg loader + temp-dir tests (config-loading tests use temp dirs, NOT the real `~/deepfry/` file)
- [ ] L0 live integration test with self-skip on transport error (mirror `live_latest_per_author`)
- [ ] determinism test mirroring `store::tests::rerun_is_deterministic`
- [ ] `WriteMsg::Fingerprints` + `UPSERT_FINGERPRINT` (if persisting fingerprints) + idempotency test
- [ ] framework install: none — `#[test]` + `tempfile` already present

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| L0 against the live whitelist server | DETECT-01 | Requires the live whitelist `:8081`; self-skips in CI | Confirm `GET /check/{pubkey}` against `http://127.0.0.1:8081` returns `{"whitelisted": bool}` and L0 maps absence→sub-score, presence→0. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Determinism test present (OPS-02)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
