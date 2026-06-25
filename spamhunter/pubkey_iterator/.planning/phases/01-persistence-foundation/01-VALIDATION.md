---
phase: 1
slug: persistence-foundation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-25
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | cargo test (Rust built-in) |
| **Config file** | none — `Cargo.toml` `[dev-dependencies]` installs `tempfile` |
| **Quick run command** | `cargo test` |
| **Full suite command** | `cargo test --all` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run `cargo test`
- **After every plan wave:** Run `cargo test --all`
- **Before `/gsd-verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

> Filled by the planner / gsd-nyquist-auditor from the RESEARCH.md `## Validation Architecture`
> section (the 5-test contract: schema creation + WAL, score UPSERT idempotency,
> signal UPSERT idempotency, zero-migration new-layer insert, batched round-trip identity).

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 01-01-T1 | 01 | 1 | SCORE-02 (criteria 1–4) | T-01-01 | Parameterized inline test SQL (`params![]`, never `format!`) | integration (RED) | `cargo test --lib store::` (5 tests fail) | ❌ W0 → created here | ⬜ pending |
| 01-01-T2 | 01 | 1 | SCORE-02 (criteria 1–4) | T-01-01, T-01-02 | Bound params in every write/read; no raw event bodies in schema | integration (GREEN) | `cargo test --lib store::` (all 5 green) | ✅ after T1 | ⬜ pending |
| 01-01-T3 | 01 | 1 | SCORE-02 (determinism, criterion 2 across batches) | T-01-01 | Fixed-order writes; single write connection; parameterized SQL | integration + lint gate | `cargo test` + `cargo clippy --all-targets` | ✅ after T2 | ⬜ pending |

**Criterion → test mapping (the 5-test contract, all in `src/store/mod.rs` `#[cfg(test)]`):**
- criterion 1 → `open_creates_wal_and_schema`
- criterion 2 → `upsert_is_idempotent` (+ across-batch hardening in T3)
- criterion 3 → `new_layer_no_migration`
- criterion 4 → `batch_roundtrip_identity`
- determinism → `rerun_is_deterministic`

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `Cargo.toml` — `tempfile = "3.27"` dev-dependency for temp-file DB fixtures (created in Task 1)
- [ ] `src/store/mod.rs` `#[cfg(test)] mod tests` — the 5 named tests covering SCORE-02 criteria 1–4 + determinism (created RED in Task 1, turned GREEN in Task 2)
- [ ] Synthetic `Persist`/`Score`/`SubScore` fixtures in the test module (Task 1)
- [ ] No framework install needed — `cargo test` is built in

*If none: "Existing infrastructure covers all phase requirements."*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| — | — | — | All phase behaviors have automated verification. |

*If none: "All phase behaviors have automated verification."*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
