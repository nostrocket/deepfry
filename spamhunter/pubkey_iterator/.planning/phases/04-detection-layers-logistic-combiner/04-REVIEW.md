---
phase: 04-detection-layers-logistic-combiner
reviewed: 2026-06-26T00:00:00Z
depth: deep
files_reviewed: 11
files_reviewed_list:
  - src/config.rs
  - src/detect/mod.rs
  - src/detect/whitelist.rs
  - src/detect/near_duplicate.rs
  - src/detect/content_entropy.rs
  - src/detect/link_mention.rs
  - src/pipeline.rs
  - src/store/schema.rs
  - src/store/writer.rs
  - src/store/mod.rs
  - src/store/queries.rs
findings:
  blocker: 0
  high: 0
  medium: 2
  low: 4
  total: 6
status: resolved
resolution:
  MD-01: fixed — deterministic total-ordered L4 top_domain tie-break (commit fix(04))
  MD-02: accepted/deferred — by-design Phase-5 wiring point, not a Phase-4 defect
  LW-01: accepted — heuristic semantics nit
  LW-02: accepted — heuristic semantics nit (output stays clamped [0,1])
  LW-03: accepted — heuristic accuracy nit (output stays clamped [0,1])
  LW-04: accepted — no correctness impact on bounded (≤100-event) input
---

# Phase 4: Code Review Report — Detection Layers + Logistic Combiner

**Reviewed:** 2026-06-26
**Depth:** deep (cross-file: combiner ↔ layers ↔ store ↔ pipeline)
**Files Reviewed:** 11
**Status:** issues_found (no blockers)

## Summary

I attacked the two load-bearing invariants hardest — **determinism (OPS-02)** and
**L0-is-not-a-gate (D-03)** — and both hold under the code as written:

- **Determinism of the score path is sound.** The combiner sums `wᵢ·xᵢ` over a
  positional `Vec<Box<dyn Layer>>` in index order (`detect/mod.rs:202-216`); there
  is no HashMap in the value computation. `from_config` builds the layer Vec in a
  fixed `if enabled` order (L0, L1, L3, L4) and resolves weights by linear `find`
  over the `ORDER BY layer` weight rows — positionally paired, deterministic.
  SimHash is hand-rolled FNV-1a with a fixed `acc == 0 → bit 0` tie-break and no
  randomized-hasher crate. The L3 `HashMap<char,u64>` and L4 `host_event_counts`
  HashMaps feed only commutative reductions (`.values().sum()`, `max_by_key`),
  never an order-dependent value. The per-run whitelist cache is no-TTL.
- **L0 is genuinely not a gate.** `WhitelistAbsenceLayer::score` returns `0.0` on
  presence / `absence_subscore` on absence and touches nothing else
  (`whitelist.rs:147-157`); every other layer ignores the `whitelisted` arg. A
  whitelisted pubkey still flows through L1/L3/L4. The fail-toward-not-flagging
  path (`is_whitelisted` → `true` on any transport/decode error, not cached) is
  correct and prevents the mass-FP outage mode.
- **No SSRF.** L4 only `Url::parse`s tokens for `host_str()`; it never issues a
  request (`link_mention.rs:71-80`).
- **SQL is fully parameterized.** Every score/signal/fingerprint/weight write
  binds via `params![]`/`?N`; the only `format!`-into-SQL is a test-only
  `count(table)` helper (`store/mod.rs:829`), not production.
- **Single-writer invariant preserved.** `weight_write_conn` is the sanctioned
  short-lived second connection and touches only the `weight` table, which the
  actor never writes.

The findings below are real but none are blockers. The most material is **MD-01**
(non-deterministic *evidence* JSON on a domain-count tie), which weakens the
"byte-identical re-run" guarantee for the `signal.evidence` column specifically —
the `score`/`value` columns stay deterministic.

---

## Medium

### MD-01: L4 `top_domain` evidence is non-deterministic on a host-count tie (weakens OPS-02 for the evidence column)

**File:** `src/detect/link_mention.rs:119-123`
**Issue:** `top_host` / `top_domain_share` are derived from
`host_event_counts.iter().max_by_key(|(_, c)| **c)`. When two or more hosts tie
on event count, `max_by_key` returns the **last maximal element in iteration
order**, and `HashMap` iteration order is not stable across runs/builds (the
documented OPS-02 hazard). The selected `top_domain` (and its `top_domain_share`
when ties exist at different shares is not possible, but the *host string* is)
can therefore differ between two otherwise-identical runs. The module header
(`link_mention.rs:22-23`) explicitly claims "no HashMap iteration feeds the
score … the host-count HashMap is only summed / max-reduced," but `max_by_key`
over a HashMap *is* an iteration-order-sensitive reduction for tie resolution.

The sub-score `value` is unaffected (it uses `top_host_events`, a count, not the
host string), so flag decisions stay deterministic. But the persisted
`signal.evidence` JSON for L4 can vary run-to-run on ties, contradicting the
"a re-run … yields byte-identical `score`/`signal` rows" guarantee in
`detect/mod.rs:10-13`.

**Fix:** Make the tie-break deterministic, e.g. break ties on the host string:
```rust
let (top_host, top_host_events) = host_event_counts
    .iter()
    .max_by(|(ha, ca), (hb, cb)| ca.cmp(cb).then_with(|| hb.cmp(ha)))
    .map(|(h, c)| (h.clone(), *c))
    .unwrap_or_default();
```
(or collect into a `BTreeMap`). Then tighten the header comment to say the
reduction is total-ordered.

### MD-02: Phase-4 scoring path is entirely unwired from the binary — `production_fetch_with_whitelist`, `persist_fingerprints`, `seed_weights_if_empty`, and `ScoringStage` have no production caller

**File:** `src/main.rs:20-41` (and `src/pipeline.rs:190`, `src/store/mod.rs:246`,
`src/detect/mod.rs:270`)
**Issue:** `main.rs` still only runs the Phase-2 `enumerate::run` walk; it never
opens a `ScoringStage`, never calls `seed_weights_if_empty`, never runs
`run_pipeline` with `production_fetch_with_whitelist`, and never calls
`persist_fingerprints`. Every Phase-4 production entry point is reachable **only
from tests**. `grep` confirms the sole non-test callers of `persist_fingerprints`
and `.fingerprints(...)` are in `#[cfg(test)]` blocks. This is consistent with
the walking-slice framing (the binary is a deliberate `--resume` slice, D-12, and
the real CLI is Phase 5), so it is not a correctness defect *in the reviewed code*
— but it means none of the determinism/idempotency/no-enforcement guarantees are
exercised by the shipping binary, only by the test harness. A reader expecting
Phase 4 to "do scoring" when run will get a no-op beyond enumeration.

**Fix:** No code change required for this phase if the Phase-5 CLI is the intended
wiring point; document explicitly in the SUMMARY that the binary does not yet
score (so it is not mistaken for a regression), and ensure Phase 5 wires
`seed_weights_if_empty` → `from_config` → `run_pipeline(production_fetch_with_whitelist, …)`
→ `persist` + `persist_fingerprints`. If any integration test is meant to stand
in for the binary, add one that drives the *real* `production_fetch_with_whitelist`
+ `persist_fingerprints` together (current tests exercise them separately).

---

## Low

### LW-01: L3 `min_len_for_low` measured on the concatenated corpus, not per-event — a high-volume pubkey of short templated posts evades the low-entropy flag asymmetrically

**File:** `src/detect/content_entropy.rs:122-137`
**Issue:** `total_len` is `concat.chars().count()` over **all** events joined. The
low-entropy flag only applies when `total_len >= min_len_for_low` (default 200).
This means the low-entropy threshold is a property of the *concatenation*, so a
pubkey posting 50 identical 5-char templated messages (250 chars concatenated)
trips the gate while the same content as one 5-char post does not. That is
arguably intended (more volume = more signal), but it is undocumented and couples
the L3 flag to event count in a way the per-component doc comment
(`content_entropy.rs:85-86`, "short posts like 'gm'") does not describe. Not a
correctness bug; a clarity/semantics gap.

**Fix:** Document that `min_len_for_low` is evaluated on the concatenated corpus
(so it scales with volume), or decide whether per-event length is the intended
unit and adjust.

### LW-02: L3 hashtag density mixes two tokenizers (denominator `unicode_words`, numerator `split_whitespace`), so density can exceed 1.0 before clamping

**File:** `src/detect/content_entropy.rs:151-160`
**Issue:** `n_words = concat.unicode_words().count()` but
`n_hashtags = concat.split_whitespace().filter(starts_with('#')).count()`.
`unicode_words` strips punctuation and can split or drop tokens differently than
whitespace splitting, so `hashtag_density = n_hashtags / n_words` can exceed 1.0
for hashtag-dominated content (e.g. `"#a #b #c"` → 3 hashtags / 3 words is fine,
but pathological punctuation could skew it). It is saved by the `.min(1.0)` knee
clamp and the final `.clamp(0.0,1.0)`, so the output stays in `[0,1]` — hence
low severity — but the ratio is not a true density and the inconsistency is a
latent foot-gun if the knee logic is ever refactored.

**Fix:** Use one tokenizer for both numerator and denominator (e.g. count
`split_whitespace()` tokens for the denominator too) so the ratio is a genuine
`[0,1]` density before clamping.

### LW-03: `is_emoji_grapheme` only inspects the FIRST codepoint and includes variation selectors as standalone emoji

**File:** `src/detect/content_entropy.rs:46-56`
**Issue:** The test `g.chars().next()` checks only the first scalar of a grapheme,
and the `(0xFE00..=0xFE0F)` (variation selectors) branch will classify a grapheme
*beginning* with a bare variation selector as an emoji. In practice VS-16 follows
a base char inside one grapheme so the first-codepoint check usually sees the
base, but a malformed/stray VS at a grapheme boundary would mis-count. This only
perturbs the emoji-density ratio (clamped to `[0,1]`), so it cannot produce an
out-of-range value or a panic — purely a heuristic-accuracy nit, acknowledged as
"NOT exhaustive" in the doc comment.

**Fix:** Drop the variation-selector range from the standalone-emoji test (VS-16
is a modifier, never an emoji on its own), or document it as an accepted
over-count.

### LW-04: `i64`-truncating `as` casts on `created_at`/length arithmetic are unguarded (no overflow on realistic input, but unchecked)

**File:** `src/detect/mod.rs:663` (test `created_at: 1_700_000_000 + idx as i64`),
`src/detect/content_entropy.rs:143,155` (`n_emoji as f64`, `n_hashtags as f64`)
**Issue:** Per the prompt's i64-truncation concern: the layers convert `usize`
counts to `f64` (`as f64`) for ratio math. For corpora bounded at ≤100 events
(T-04-05) and Nostr content sizes these never approach `f64`'s 2^53 exact-integer
limit, so there is no realistic precision loss — but the casts are unchecked and
there is no documented bound enforcing the ≤100-event cap at the layer boundary
(the cap is asserted only in `repeated_ratio`'s comment, `near_duplicate.rs:124`).
No panic or wrong result on valid input; flagged for completeness.

**Fix:** None required for correctness. Optionally assert/clamp event-slice length
at the `Layer::score` boundary so the O(n²) and cast bounds are enforced rather
than assumed.

---

## Notes (verified safe — not findings)

- **Float-sum order:** fixed positional `Vec` iteration; deterministic. ✓
- **Sigmoid stability:** `1.0/(1.0+(-z).exp())` — for large negative `z`, `(-z).exp()`
  can overflow to `+inf`, yielding `score = 0.0` (correct limit, no NaN); for large
  positive `z`, `(-z).exp() → 0`, `score → 1.0`. No `NaN` reachable. ✓
- **Sub-score bounds:** every layer clamps to `[0,1]`; entropy/knee divisions guard
  zero via `.max(f64::EPSILON)` / `knee > 0.0`; empty content → early `0.0`; single
  event below `min_events` → `0.0`. No NaN/inf path found. ✓
- **`debug_assert` on layer output:** the `(0.0..=1.0)` guard in `score`
  (`detect/mod.rs:204-209`) is debug-only — a release build with a buggy future
  layer would silently admit an out-of-range value, but all current layers clamp,
  so no live defect. ✓
- **Idempotency:** `UPSERT_FINGERPRINT` on `(run_id,pubkey,content_hash)` and the
  score/signal UPSERTs are idempotent; the writer iterates `subscores` in Vec order
  and ensures the FK pubkey row before each fingerprint. ✓
- **`u64↔i64` reinterpret:** content_hash/simhash stored via `as i64`, read via
  `as i64`, compared only for equality/Hamming — never signed-ordered. ✓
- **Whitelist URL injection:** operator-supplied base + `trim_matches('/')` on the
  already-validated 64-hex pubkey before path interpolation; no slash injection. ✓
- **Single-writer:** `weight_write_conn` touches only the `weight` table; the actor
  never writes `weight`. No race on the actor's tables. ✓

---

_Reviewed: 2026-06-26_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: deep_
