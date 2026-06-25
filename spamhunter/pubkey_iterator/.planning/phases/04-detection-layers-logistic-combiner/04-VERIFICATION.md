---
phase: 04-detection-layers-logistic-combiner
verified: 2026-06-26T00:00:00Z
status: passed
score: 5/5 ROADMAP success criteria verified (10/10 requirements satisfied)
behavior_unverified: 0
overrides_applied: 0
---

# Phase 4: Detection Layers + Logistic Combiner Verification Report

**Phase Goal:** The pipeline produces a real per-pubkey spam score — each P1 detection layer emits a normalized sub-score through a common Layer contract, the logistic combiner fuses them, and the score plus per-layer evidence is persisted — the first end-to-end runnable verdict.
**Verified:** 2026-06-26
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (5 ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Each P1 layer (L0/L1/L3/L4) emits xᵢ∈[0,1] through ONE shared `Layer` trait | ✓ VERIFIED | `src/detect/mod.rs:49-58` object-safe `trait Layer: Send + Sync { name(); score(events, whitelisted) -> LayerOutput }`. All four real layers `impl Layer`: `whitelist.rs:138`, `near_duplicate.rs:210`, `content_entropy.rs:108`, `link_mention.rs:82`. Bound enforced by `debug_assert!((0.0..=1.0))` at `mod.rs:204` plus per-layer `subscore_always_bounded`/`bound_and_max_combine`/`min_events_gate_and_bound` tests. All four registered in `from_config` (`mod.rs:140-181`). |
| 2 | Combiner = sigmoid(Σwᵢxᵢ+b) with conservative weights; requires MULTI-signal agreement (test: single strong layer < τ, multi ≥ τ) | ✓ VERIFIED | `mod.rs:199-227` accumulates `z = bias + Σ weights[i]·xᵢ` then `score = 1/(1+e^-z)`, weights read from `weight` table via `from_config`. `detect::combiner::multi_signal_agreement` (REAL four layers from committed defaults) asserts single content layer < τ AND two-plus > τ AND whitelist-absence-alone < τ — PASSED. Also `detect::tests::multi_signal_agreement_property` (synthetic). Defaults L0=0.8/L1=2.0/L3=1.5/L4=1.5, bias=-4.0, τ=0.5. |
| 3 | score row + per-layer signal rows persisted; each flagged pubkey carries per-layer evidence (signal.evidence JSON) — SCORE-05 | ✓ VERIFIED | `Persist` built with `SubScore { layer, value, evidence: Some(out.evidence.to_string()) }` (`mod.rs:211-215`); writer persists via `UPSERT_SCORE`/`UPSERT_SIGNAL` in fixed subscore order (`writer.rs:79-93`). `pipeline::tests::normal_pubkey_persists_score_and_evidence` asserts `count(signal WHERE evidence IS NULL OR '') == 0` — PASSED. Each layer emits structured JSON evidence (repeated_ratio/clusters, entropy_bits/densities, url_ratio/top_domain, whitelisted). |
| 4 | pubkey-level only, NO enforcement; each layer enable/disable + tunable weight/threshold from config WITHOUT recompile (config disable test omits the signal row) | ✓ VERIFIED | `detect::combiner::disabled_layer_omitted` builds stage from config with `L4.enabled=false`, persists, asserts NO `L4_link_mention` signal row + exactly 3 rows + deterministic re-score — PASSED. Disabled layers omitted at build time (`mod.rs:140-181`), never evaluated-then-zeroed. `detect::combiner::no_enforcement_side_effect` asserts only score/signal/weight tables mutate, `label`/`pubkey` untouched — PASSED. Weights/τ/bias seeded from TOML into `weight` table (`seed_weights_if_empty`), read back at `from_config` — no recompile. |
| 5 | Deterministic: re-run same snapshot+weights → identical verdicts (FNV-1a SimHash, fixed layer-sum order, UPSERT(run_id,pubkey)) — test exists and passes | ✓ VERIFIED | Hand-rolled `fnv1a64` + `simhash64` with fixed tie-break (`near_duplicate.rs:27-58`), NO randomized hasher. Fixed positional `Vec<Box<dyn Layer>>` sum (no HashMap in score path, `mod.rs:9-13,199-216`). `detect::near_duplicate::tests::simhash_is_deterministic`, `detect::tests::score_is_deterministic`, and end-to-end `pipeline::tests::rerun_endtoend_is_deterministic` (two fresh temp DBs, byte-identical score+signal) — all PASSED. UPSERT on `(run_id,pubkey)` / `(run_id,pubkey,layer)`. |

**Score:** 5/5 ROADMAP success criteria verified (0 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/config.rs` | serde TOML Config + path-arg loader (D-09) | ✓ VERIFIED | 265 lines. `pub fn load(&Path) -> Result<Config, ConfigError>` (thiserror Read/Parse, never panics). All 4 config-loading tests write to `tempfile::TempDir`, never `~/deepfry`. `loader_is_path_argument_based` asserts the path lives under temp dir. |
| `src/detect/mod.rs` | Layer trait, LayerOutput, ScoringStage, score(), ScoredInput, weight helpers | ✓ VERIFIED | 867 lines. Trait + combiner + `from_config`/`from_layers` + `read_weights`/`seed_weights_if_empty` + `ScoredInput` carrier all present and substantive. |
| `src/detect/whitelist.rs` | L0 + async WhitelistClient (GET /check/{pubkey}, per-run cache) | ✓ VERIFIED | 371 lines. `WhitelistClient` reuses one `reqwest::Client`, no-TTL `Mutex<HashMap>` cache, fail-toward-not-flagging on transport error (not cached). Live self-skipping test PASSED against reachable :8081. |
| `src/detect/near_duplicate.rs` | L1 hand-rolled SimHash + Hamming + ratio | ✓ VERIFIED | 402 lines. `fnv1a64`/`simhash64`/`hamming64` hand-rolled deterministic; union-find clustering; repeated-content ratio in [0,1]. |
| `src/detect/content_entropy.rs` | L3 Shannon entropy + emoji/hashtag density | ✓ VERIFIED | 321 lines. `shannon_bits_per_char` (order-independent sum), two-sided entropy band, max-combine. |
| `src/detect/link_mention.rs` | L4 url ratio/domain concentration/tag means via url crate | ✓ VERIFIED | 315 lines. `url::Url::parse(...).host_str()` (no regex, never fetches). max-combine, min_events gate. |
| `src/pipeline.rs` | ScoredInput channel carrier + production fetch wiring | ✓ VERIFIED | `run_pipeline` channel is `flume::bounded::<ScoredInput>`; `production_fetch_with_whitelist` wires `match_groups` (D-15) + resolves L0 membership in the fetch stage, attaching the real bool to the carrier (no placeholder). |
| `src/store/writer.rs` | UPSERT_FINGERPRINT + WriteMsg::Fingerprints | ✓ VERIFIED | `UPSERT_FINGERPRINT` (writer.rs:41, ON CONFLICT idempotent) + `WriteMsg::Fingerprints` arm (writer.rs:104, params![]). |
| `pubkey_iterator_config.example.toml` | committed example with all 4 layers | ✓ VERIFIED | Repo root, 47 lines, whitelist_url + tau + bias + four layer entries with conservative D-08 defaults. |
| `Cargo.toml` | Phase-4 deps unicode-segmentation, url, toml only | ✓ VERIFIED | All three present (1.13/2.5/1.1). NO gaoya/linfa/simhash/xxhash/ahash (only mentioned in dep-discipline comments documenting their exclusion). |

### Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| `detect/mod.rs` | `store/mod.rs` | ScoringStage → Persist → store.persist → WriteMsg::Score | ✓ WIRED (consumer closures call stage.score + store.persist; verified in pipeline tests) |
| `config.rs` | `detect/mod.rs` | from_config / seed_weights_if_empty read Config layer entries | ✓ WIRED |
| `detect/whitelist.rs` | whitelist-plugin GET /check/{pubkey} | reqwest GET → {whitelisted:bool} | ✓ WIRED (live test parsed real :8081 response) |
| `pipeline.rs` | `detect/mod.rs` | fetch stage sets ScoredInput.whitelisted from is_whitelisted; consumer forwards to stage.score | ✓ WIRED (`production_fetch_with_whitelist:205-212`) |
| `near_duplicate.rs`/etc. | `detect/mod.rs` | impl Layer; registered in from_config L0/L1/L3/L4 slots | ✓ WIRED |
| `store/mod.rs` | `store/writer.rs` | persist_fingerprints → WriteMsg::Fingerprints → UPSERT_FINGERPRINT | ⚠️ WIRED at store level; see Notes (mechanism + store test exist; not invoked by the production scoring consumer) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full lib test suite | `cargo test --lib` | 86 passed; 0 failed; 0 ignored | ✓ PASS |
| Build | `cargo build` | Finished clean | ✓ PASS |
| Multi-signal agreement (real 4 layers) | `cargo test -- detect::combiner::multi_signal_agreement` | ok | ✓ PASS |
| Disabled layer omits signal row | `cargo test -- detect::combiner::disabled_layer_omitted` | ok | ✓ PASS |
| No enforcement side-effect | `cargo test -- detect::combiner::no_enforcement_side_effect` | ok | ✓ PASS |
| End-to-end determinism | `cargo test -- pipeline::tests::rerun_endtoend_is_deterministic` | ok | ✓ PASS |
| Zero-event pubkey gets score (D-15) | `cargo test -- pipeline::tests::zero_event_pubkey_gets_score_row` | ok | ✓ PASS |
| SimHash deterministic (FNV-1a) | `cargo test -- detect::near_duplicate::tests::simhash_is_deterministic` | ok | ✓ PASS |
| Live whitelist L0 (:8081) | `cargo test -- detect::whitelist::tests::live_check_self_skipping` | ok (server reachable, parsed) | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| DETECT-01 (L0 whitelist absence→sub-score, presence clears only L0) | 04-03 | ✓ SATISFIED | `whitelist.rs` — presence→0.0, absence→absence_subscore; flows through other layers. |
| DETECT-02 (L1 SimHash+Hamming near-dup) | 04-02 | ✓ SATISFIED | `near_duplicate.rs` — hand-rolled SimHash, Hamming clustering, repeated ratio. |
| DETECT-03 (L3 entropy + density) | 04-02 | ✓ SATISFIED | `content_entropy.rs` — two-sided entropy + emoji/hashtag density. |
| DETECT-04 (L4 link/mention) | 04-03 | ✓ SATISFIED | `link_mention.rs` — url ratio, domain concentration, p/t-tag means. |
| DETECT-05 (shared contract, enable/disable, tunable) | 04-01 | ✓ SATISFIED | `Layer` trait; disabled-omitted; weight/threshold from config. |
| SCORE-01 (sigmoid(Σwᵢxᵢ+b)) | 04-01 | ✓ SATISFIED | `ScoringStage::score`. |
| SCORE-04 (pubkey-level, no enforcement) | 04-01/04-03 | ✓ SATISFIED | `no_enforcement_side_effect` test; only score/signal/weight mutate. |
| SCORE-05 (per-layer evidence persisted) | 04-01 | ✓ SATISFIED | non-empty evidence JSON asserted; per-layer signal rows. |
| OPS-02 (deterministic) | 04-01/04-02 | ✓ SATISFIED | FNV-1a, fixed sum order, byte-identical re-run. |
| OPS-03 (configurable without recompile) | 04-01 | ✓ SATISFIED | TOML → weight table seed/read. |

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| (none) | TODO/FIXME/XXX/TBD/HACK/unimplemented!/placeholder scan over src/detect/, config.rs, pipeline.rs | — | NONE found. The Plan-01 "empty from_config layers Vec" stub documented in 04-01-SUMMARY was resolved by Plans 02/03 (all four layers now register). |

### Human Verification Required

None. The phase deliverable is verified by automated tests including a live whitelist integration test that passed against the reachable :8081 server. (The 04-VALIDATION manual-only item for the live whitelist is now covered by the passing `live_check_self_skipping` test.)

### Gaps Summary

No blocking gaps. All 5 ROADMAP success criteria and all 10 phase requirements are satisfied with substantive, wired implementations and passing tests (86/86).

**Non-blocking observations (informational, not gaps against this phase's goal):**

1. **Fingerprint persistence is built and store-tested but not invoked by the production scoring consumer.** `WriteMsg::Fingerprints`, `UPSERT_FINGERPRINT`, `Store::persist_fingerprints`, and `NearDuplicateLayer::fingerprints()` all exist and are exercised end-to-end at the store layer (`store::tests::l1_emits_one_fingerprint_per_distinct_content`, idempotency + round-trip tests pass). However, the scoring consumer closures in `pipeline.rs` and `production_fetch_with_whitelist` call only `stage.score` + `store.persist` — they never call `store.persist_fingerprints`. This does NOT block any of the 5 ROADMAP SCs: fingerprints are not required by SCORE-05 (the L1 *signal* row carries the required evidence), and the `fingerprint.minhash BLOB` column is explicitly a Phase-7 precursor. The mechanism is ready for the consumer/CLI to wire in Phase 5. Plan 04-02's truth "fingerprint rows persist via WriteMsg::Fingerprints" is satisfied at the persistence-mechanism level it scoped.

2. **No runnable CLI command yet exercises scoring.** `src/main.rs` is still the Phase-2 enumerate-only `--resume` entry point (D-12 explicitly defers the `run`/`export` clap surface to Phase 5). The "first end-to-end runnable verdict" is achieved and proven at the library/integration-test level (`run_pipeline` + `production_fetch_with_whitelist` + scoring consumer → persisted score/signal, verified in `zero_event_pubkey_gets_score_row`, `normal_pubkey_persists_score_and_evidence`, `rerun_endtoend_is_deterministic`). A human-invocable command is Phase-5 territory by design. This matches the phase's own framing as "the integration seam every later layer plugs into."

Both observations are consistent with the milestone roadmap's phase boundaries (Phase 5 owns the CLI) and do not reduce the achieved Phase-4 goal.

---

_Verified: 2026-06-26_
_Verifier: Claude (gsd-verifier)_
