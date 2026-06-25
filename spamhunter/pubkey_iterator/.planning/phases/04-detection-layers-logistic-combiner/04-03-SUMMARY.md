---
phase: 04-detection-layers-logistic-combiner
plan: 03
subsystem: detection
status: complete
tags: [detection, whitelist, link-mention, combiner, multi-signal, no-enforcement, determinism]
dependency_graph:
  requires:
    - "src/detect/mod.rs (Layer trait, LayerOutput, ScoringStage::from_config registry, ScoredInput carrier, weight seed/read)"
    - src/config.rs (L0Config, L4Config tunables)
    - src/graphql/queries.rs (Event.content/tags, AuthorGroup)
    - src/graphql/client.rs (reqwest::Client reuse + loopback stub idiom)
    - "src/pipeline.rs (run_pipeline F-source seam, match_groups wiring, live self-skip idiom)"
    - src/fetch.rs (match_groups attribute-by-author)
    - src/store/queries.rs (read_scores, read_signals)
  provides:
    - "src/detect/whitelist.rs (WhitelistAbsenceLayer + async WhitelistClient: GET /check/{pubkey}, per-run no-TTL cache, fail-toward-not-flagging)"
    - "src/detect/link_mention.rs (LinkMentionLayer: url ratio, repeated-domain concentration, mean p-tag/t-tag, url-crate hosts)"
    - "L0_whitelist_absence + L4_link_mention registered in ScoringStage::from_config — completes the fixed L0,L1,L3,L4 order"
    - "production_fetch_with_whitelist (event fetch + L0 membership resolution in one tokio stage) replacing production_fetch"
    - "run_pipeline F-source now yields ScoredInput directly (whitelist resolved in the fetch stage, no placeholder)"
    - "detect::combiner integration tests: multi-signal agreement, disabled-layer-omitted, no-enforcement"
  affects:
    - "Plan 05 (CLI run): assembles the production consumer that calls stage.score with the carrier whitelist bool + store.persist[_fingerprints]"
    - "Phase 6 (tuner): overwrites the seeded weight rows; the combiner reads stored weights"
tech_stack:
  added: []
  patterns:
    - "pre-resolve L0 membership in the tokio fetch stage; CPU layers stay HTTP-free (Pitfall 5)"
    - "per-run no-TTL HashMap cache for whitelist (OPS-02 determinism; A7) guarded by a Mutex"
    - "fail-toward-not-flagging on whitelist transport/decode error → true (L0 0.0), fail-safe value NOT cached (Pitfall 2/T-04-07)"
    - "WHATWG host parsing via url::Url::parse(token).host_str() — never a regex, never fetched (no SSRF; T-04-09)"
    - "max-of-components sub-score (FP-averse), each via min(value/knee,1.0); min_events gate to 0.0"
    - "disabled layer omitted at build time (writes no signal row), not evaluated-then-zeroed"
key_files:
  created:
    - src/detect/whitelist.rs
    - src/detect/link_mention.rs
  modified:
    - src/detect/mod.rs
    - src/pipeline.rs
decisions:
  - "run_pipeline's F source now yields Vec<ScoredInput> (not Vec<AuthorGroup>): the fetch stage is the single whitelist-resolution point, the carrier/channel/consumer signatures are unchanged, and the whitelisted=false placeholder is gone. mock_fetch in tests wraps with whitelisted=false (synthetic); production_fetch_with_whitelist resolves the real bool."
  - "production_fetch was replaced by production_fetch_with_whitelist (it had no caller — main.rs uses enumerate::run; the pipeline tests were the only consumers), avoiding a dead near-duplicate fetch helper."
  - "Whitelist transport/decode failure fails toward NOT flagging (returns true → L0 0.0) and is NOT cached, so a transient outage neither inflates spam scores (Pitfall 2) nor poisons the per-run cache; a later retry can still resolve the real value."
  - "The whitelist client trims a trailing slash on base and path-segments the (already 64-hex) pubkey defensively (V5) so {base}/check/{pubkey} is well-formed."
  - "L4 counts a host once per event (distinct-within-event) for the concentration share, so one event spamming a host 10× cannot dominate the (max events sharing one host)/(events-with-URLs) ratio."
  - "Multi-signal test drives the REAL from_config four-layer stage over the committed example config seeded into the weight table — exercising the actual §L7 weight budget, not the trivial stand-in."
metrics:
  duration_minutes: 14
  completed: 2026-06-26
  tasks: 3
  files_created: 2
  files_modified: 2
  tests_added: 15
  tests_total: 86
---

# Phase 4 Plan 03: L0 whitelist-absence + L4 link/mention + full-combiner verification Summary

Completes the four-layer P1 detection set and proves the load-bearing combiner behaviours end-to-end. L0 (`WhitelistAbsenceLayer`) consumes a fetch-stage-resolved `whitelisted` bool — absence emits `absence_subscore`, presence clears only this layer (never a gate) — backed by an async `WhitelistClient` that calls the code-verified `GET /check/{pubkey}` → `{"whitelisted":bool}` on a reused `reqwest::Client` with a per-run no-TTL cache and fail-toward-not-flagging on outage. L4 (`LinkMentionLayer`) emits `max(url_ratio, domain_concentration, mean_p_tags, mean_t_tags)` in `[0,1]` using `url`-crate host parsing (never fetched). The fetch stage now resolves membership and fills the existing Plan-01 `ScoredInput.whitelisted` carrier (no new plumbing), and three integration tests prove multi-signal agreement (single strong layer < τ, two-plus > τ), config enable/disable (disabled layer writes no signal row), and the SCORE-04 no-enforcement guarantee.

## Tasks Completed

| Task | Name | Commit | Key files |
|------|------|--------|-----------|
| 1 | L0 WhitelistAbsenceLayer + async WhitelistClient (DETECT-01) | `2140205` | src/detect/whitelist.rs, src/detect/mod.rs, src/pipeline.rs |
| 2 | L4 LinkMentionLayer — url/domain/tag ratios (DETECT-04) | `4590cb2` | src/detect/link_mention.rs, src/detect/mod.rs |
| 3 | Full-combiner multi-signal + config disable + no-enforcement | `9bae79a` | src/detect/mod.rs |

## What was built

- **L0 — `src/detect/whitelist.rs`:** `WhitelistAbsenceLayer` (CPU-only; `whitelisted` pre-resolved, `score` does no HTTP) and `WhitelistClient` (one pooled `reqwest::Client`, `GET {base}/check/{pubkey}`, serde `CheckResponse{whitelisted}`, per-run no-TTL `Mutex<HashMap>` cache, fail-toward-not-flagging that does not cache the fail-safe). 7 tests: presence→0.0, absence→subscore, both-boolean parse over a loopback stub, cache-avoids-second-roundtrip (connection-counted), transport-error fail-safe (uncached), outage→L0 0.0, and the live self-skipping `:8081` check (D-14).
- **L4 — `src/detect/link_mention.rs`:** four FP-averse components via `min(value/knee,1.0)`, max-combined, clamped `[0,1]`; `min_events` gate to 0.0; hosts via `url::Url::parse(token).host_str()` (lowercased), counted once per event. 5 tests: url-ratio knee, repeated-domain concentration (same vs diverse hosts), mass p-tags, hashtag stuffing, min_events gate + bound across arbitrary fixtures.
- **Registration + wiring — `src/detect/mod.rs`, `src/pipeline.rs`:** L0 and L4 registered in `ScoringStage::from_config` completing the fixed `L0,L1,L3,L4` order; `run_pipeline`'s `F` source yields `ScoredInput` directly; `production_fetch_with_whitelist` resolves L0 membership (incl. zero-event pubkeys) in the tokio fetch stage and fills the carrier.
- **Combiner verification — `detect::combiner`:** `multi_signal_agreement`, `disabled_layer_omitted`, `no_enforcement_side_effect` over the real four-layer stage seeded from the committed config.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `run_pipeline` F-source + `production_fetch` signature change**
- **Found during:** Task 1.
- **Issue:** The `whitelisted: false` placeholder was hardcoded inside `run_pipeline`'s producer (not in an injectable closure), and `production_fetch` returned `Vec<AuthorGroup>` with no whitelist info. Filling the carrier without "new channel/payload/consumer plumbing" required the fetch source to own resolution.
- **Fix:** Changed `run_pipeline`/`run_pipeline_noop`'s `F` to yield `Vec<ScoredInput>` (carrier unchanged, channel type unchanged, consumer signature unchanged); replaced `production_fetch` with `production_fetch_with_whitelist` (its only callers were pipeline tests — main.rs uses `enumerate::run`). Updated `mock_fetch` to wrap with `whitelisted=false` and the D-15 zero-event test to drive the whitelist-aware fetch through a loopback `whitelist_stub`.
- **Files modified:** src/pipeline.rs.
- **Commit:** `2140205`.

No architectural (Rule 4) changes. No new dependencies (`url` was already added in Cargo.toml under Phase-4 ownership).

## Authentication Gates

None — the whitelist `:8081` and adapter are loopback, no creds. The live L0 test self-skips on transport error (D-14).

## Verification

- `cargo test` — 86 pass, 0 fail (71 baseline + 7 L0 + 5 L4 + 3 combiner).
- `cargo build` — clean.
- `cargo clippy --all-targets -- -D warnings` — clean.
- Live L0 test self-skips when `:8081` is down (eprintln, never fails CI).
- L0: presence→0.0, absence→subscore, transport error→0.0 (not absence).
- L4: `url`-crate hosts, never fetched; min_events gates to 0.0; components ramp at their knees.
- Multi-signal: single strong layer < τ, two-plus > τ; whitelist-absence alone < τ.
- Disabling a layer omits its signal row; a full run mutates only score/signal/weight (label/pubkey untouched).

## Threat Surface

No new surface beyond the plan's `<threat_model>`. T-04-07 (whitelist-unreachable mass-FP) mitigated by fail-toward-not-flagging + uncached fail-safe; T-04-08 (response logging) mitigated — evidence stores only the boolean, no raw body logged; T-04-09 (SSRF) accepted — L4 parses URLs but never fetches them; T-04-04 (enforcement side-effect) mitigated and asserted by `no_enforcement_side_effect`; T-04-02 (cache-TTL non-determinism) mitigated by the no-TTL per-run cache.

## Known Stubs

None. Both layers wire real signals; no placeholder/TODO/empty-data paths.

## Self-Check: PASSED

- Files: `src/detect/whitelist.rs`, `src/detect/link_mention.rs`, `04-03-SUMMARY.md` all exist on disk.
- Commits: `2140205`, `4590cb2`, `9bae79a` all present in git history.
