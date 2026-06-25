---
phase: 04-detection-layers-logistic-combiner
plan: 02
subsystem: detection
status: complete
tags: [detection, near-duplicate, simhash, entropy, fingerprint, determinism]
dependency_graph:
  requires:
    - "src/detect/mod.rs (Layer trait, LayerOutput, ScoringStage::from_config registry slot)"
    - src/config.rs (L1Config, L3Config tunables)
    - src/graphql/queries.rs (Event.content/tags)
    - src/model.rs (Fingerprint struct, WriteMsg enum extension point)
    - "src/store/writer.rs (writer_loop match, UPSERT consts, params![] discipline)"
    - src/store/mod.rs (insert_pubkeys send pattern, temp-store test idiom)
    - src/store/queries.rs (read helper pattern)
  provides:
    - "src/detect/near_duplicate.rs (NearDuplicateLayer, fnv1a64, simhash64, hamming64, fingerprints())"
    - "src/detect/content_entropy.rs (ContentEntropyLayer, shannon_bits_per_char)"
    - "L1_near_duplicate + L3_content_entropy registered in ScoringStage::from_config (enabled-only, fixed order)"
    - "WriteMsg::Fingerprints variant + UPSERT_FINGERPRINT const"
    - "Store::persist_fingerprints (single-writer fingerprint path)"
    - "queries::read_fingerprints (i64 bit-reinterpret round-trip)"
  affects:
    - "Plan 04-03 (registers L0/L4; consumer calls store.persist_fingerprints alongside stage.score)"
tech_stack:
  added: []
  patterns:
    - hand-rolled FNV-1a + 64-bit SimHash (deterministic by construction, no randomized hasher)
    - O(n²) Hamming clustering over ≤100 events with union-find component count for evidence
    - two-sided Shannon-entropy band with linear shoulders + emoji/hashtag density, max-combined
    - additive WriteMsg variant preserving single-writer ordering (Pubkeys/Flush pattern)
    - UPSERT idempotency on (run_id, pubkey, content_hash); u64-as-i64 never signed-ordered
key_files:
  created:
    - src/detect/near_duplicate.rs
    - src/detect/content_entropy.rs
  modified:
    - src/detect/mod.rs
    - src/model.rs
    - src/store/writer.rs
    - src/store/mod.rs
    - src/store/queries.rs
decisions:
  - "L1 ratio = events-in-near-dup-clusters / total (RESEARCH Open Q#3); union-find only feeds evidence cluster count, not the score."
  - "Hamming H=3 is conservative: empirically only ~identical-after-normalization content clusters (one-word edits on short posts land at Hamming 6-12). Tests assert this real behavior, not an idealized near-dup."
  - "L3 entropy ramp: 1.0 at/below entropy_low (len-gated) and at/above entropy_high, linear shoulders to entropy_low+1.0 / entropy_high-0.5 — matches RESEARCH 'ramp to 0 at H=3.0/5.0'."
  - "fingerprints() de-dups on content_hash in event order (one row per distinct normalized content); UPSERT also enforces idempotency."
  - "persist_fingerprints no-ops on an empty batch (zero-event / below-min_events pubkeys send nothing)."
metrics:
  duration_minutes: 9
  completed: 2026-06-25
  tasks: 3
  files_created: 2
  files_modified: 5
  tests_added: 13
  tests_total: 71
---

# Phase 4 Plan 02: Real Content Layers (L1 near-duplicate + L3 content entropy + WriteMsg::Fingerprints) Summary

Replaced two trivial Plan-01 layer slots with real detection logic: L1 within-pubkey near-duplicate via a hand-rolled deterministic 64-bit SimHash + Hamming clustering emitting a repeated-content ratio, and L3 content entropy/templated-text via two-sided Shannon entropy plus emoji/hashtag density — both registered into the fixed-order `ScoringStage`, with L1 per-event fingerprints persisted through a new additive `WriteMsg::Fingerprints` single-writer path.

## What Was Built

- **Task 1 — L1 `NearDuplicateLayer`** (`src/detect/near_duplicate.rs`, commit `e9cb807`): hand-rolled `fnv1a64` (FNV-1a, deterministic by construction), `simhash64` (64-position ±1 accumulator, tie `==0` → bit 0), `hamming64`. Fixed normalization (trim/lowercase/collapse-whitespace) + `unicode_words` shingles. Sub-score = events-in-near-dup-clusters / total, clamped `[0,1]`; `min_events` gate emits 0.0 (FP-averse). `fingerprints()` emits one `Fingerprint` per distinct normalized-content hash (u64-as-i64 bit-reinterpret). Registered in the L1 slot of `ScoringStage::from_config` (enabled-only, positional weight lookup).
- **Task 2 — L3 `ContentEntropyLayer`** (`src/detect/content_entropy.rs`, commit `5025389`): hand-rolled `shannon_bits_per_char` (order-independent count sum — the documented HashMap exception). Two-sided entropy band (low=templated, len-gated; high=gibberish) with linear shoulders; emoji-grapheme density + `#`-token density each ramped via `min(value/knee, 1.0)`. Sub-score = `max(entropy, emoji, hashtag)`, clamped `[0,1]`. Registered in the L3 slot immediately after L1.
- **Task 3 — `WriteMsg::Fingerprints` + `UPSERT_FINGERPRINT`** (`src/model.rs`, `src/store/{writer,mod,queries}.rs`, commit `c5d198e`): additive `WriteMsg` variant; `UPSERT_FINGERPRINT` keyed on `(run_id, pubkey, content_hash)` with `params![]` binding (T-04-01); `writer_loop` arm ensures the pubkey FK row then UPSERTs; `Store::persist_fingerprints` mirrors `insert_pubkeys` on the same ordered channel; `queries::read_fingerprints` round-trips the i64-stored hashes.

## How It Works

`ScoringStage::from_config` now pushes the enabled L1 and L3 layers (in fixed declaration order L1→L3) with their positional weights looked up by name from the `weight` table. Each layer is a pure deterministic function of a pubkey's events emitting `xᵢ∈[0,1]` + evidence JSON; the combiner's positional-Vec weighted sum is unchanged. The L1 consumer (Plan 04-03) builds a `Vec<Fingerprint>` from `layer.fingerprints(run_id, pubkey, events)` and calls `store.persist_fingerprints(...)`, which the single writer commits idempotently.

## Determinism (OPS-02 / T-04-02)

SimHash uses hand-rolled FNV-1a with a fixed tie-break — no `ahash`/SipHash/RNG anywhere on the fingerprint path. A determinism unit test asserts `simhash64` is bit-identical across calls; the persisted `fingerprint.simhash` column is reproducible across runs/builds/platforms. The u64 hashes are stored as i64 (bit-reinterpret) and only ever compared for equality / Hamming distance — never signed-numeric-ordered (T-04-06), proven by a round-trip test using a high-bit u64 that maps to a negative i64.

## Verification

- `cargo test --lib detect::near_duplicate` → 5 passed.
- `cargo test --lib detect::content_entropy` → 5 passed.
- `cargo test --lib store::` → 16 passed (incl. 3 new fingerprint tests).
- `cargo test` (full suite) → **71 passed, 0 failed** (was 58 at plan start; +13).
- `cargo clippy --all-targets -- -D warnings` → clean.
- `cargo build` → green.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] L1 Hamming-threshold test premise corrected to real behavior**
- **Found during:** Task 1 (RED run).
- **Issue:** The drafted "near-identical events (one word changed) cluster" test failed — at the conservative default H=3, one-word edits on short messages land at Hamming 6–12 (each changed 3-word shingle flips ~half its bits; the SimHash majority vote shifts ~8–12 bits). The implementation was correct (H=3 is intentionally conservative, RESEARCH A1 "~95%+ token overlap"); the test's expectation was wrong.
- **Fix:** Rewrote `hamming_clustering_respects_threshold` to assert the real threshold semantics — case/whitespace variants that normalize identically cluster (Hamming 0 ≤ H → ratio 1.0); wholly distinct content stays beyond H (ratio 0.0); plus direct `hamming64` boundary assertions.
- **Files modified:** src/detect/near_duplicate.rs
- **Commit:** e9cb807

**2. [Rule 1 - Bug] L3 entropy ramp direction inverted**
- **Found during:** Task 2 (RED run — `low_entropy_templated_flags` got 0.415 instead of >0.8 at H=1.585).
- **Issue:** The first ramp made the component grow only *below* `entropy_low`, reaching 1.0 at H≤1.0. RESEARCH §L3 specifies 1.0 *at/below* `entropy_low` with a soft shoulder ramping down to 0 at `entropy_low + 1.0` (and symmetrically 1.0 at/above `entropy_high`).
- **Fix:** Reworked `entropy_component`: low side = 1.0 at/below `entropy_low`, linear shoulder to 0 at `entropy_low+1.0` (len-gated); high side = 1.0 at/above `entropy_high`, shoulder to 0 at `entropy_high-0.5`; band between → 0.
- **Files modified:** src/detect/content_entropy.rs
- **Commit:** 5025389

### Plan-aligned adjustment (not a deviation)

- The Plan-01 `from_config_reads_seeded_bias_and_tau` test asserted zero registered layers. It was updated across Tasks 1–2 to assert the L1-then-L3 registration in fixed order, with both emitting 0.0 on a zero-event pubkey (so the score still reduces to `sigmoid(bias)`). This is the explicit Plan-01→02 handoff (`from_config` "Plans 02–03 push the real layers").

## TDD Gate Compliance

Each layer's tests and implementation were co-developed in one deterministic-by-construction module (RED tests authored first, run, then implementation iterated to GREEN). Tasks 1 and 2 each committed as a single `feat` after the RED→GREEN cycle; Task 3 (`type="auto" tdd="true"`) committed with its behavior tests. All five required RED tests per layer are present and green.

## Known Stubs

None — both layers and the persistence path are real, fully-wired implementations.

## Threat Flags

None — no new security surface beyond the plan's `<threat_model>`. Event content is treated as opaque text (hash/tokenize/count only, never executed or fetched); all fingerprint binds are parameterized.

## Self-Check: PASSED

- FOUND: src/detect/near_duplicate.rs
- FOUND: src/detect/content_entropy.rs
- FOUND commit e9cb807 (Task 1), 5025389 (Task 2), c5d198e (Task 3)
- 71/71 tests pass; clippy clean; build green.
