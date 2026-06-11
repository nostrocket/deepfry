---
phase: 03-query-engine
verified: 2026-06-12T00:00:00Z
status: gaps_found
score: 2/5 must-haves verified
overrides_applied: 0
gaps:
  - truth: "An events() filter (ids, authors, kinds, since/until) selects the most selective applicable index and returns correctly ordered results hydrated to full event JSON"
    status: failed
    reason: |
      Four empirically-reproduced correctness bugs make execute_query return silently wrong results.
      (1) CR-01: reverse scans walk past the filter prefix — wrong-kind/wrong-author/wrong-id events
          returned. merge_prefixes has no prefix boundary enforcement; scan walks into lexicographically
          smaller prefixes. Proven: kinds=[2] limit=10 returns 7 kind-1 events and 1 duplicate.
      (2) CR-02: filter.since is never enforced. build_start_keys only uses since for Forward scans;
          execute_query always uses Reverse. No stop-bound applied in the merge loop or post-hydration.
          Proven: kinds=[1] since=1715000000 returned 5 events older than since.
      (3) CR-03: DUPSORT batch-boundary windowing uses Included restart, re-implementing the broken
          pattern the Phase-2 scan.rs fix was specifically designed to avoid. Events in dup groups
          straddling a batch boundary can be silently dropped.
      (4) CR-04: stuck-window advance keeps stale lev_id — window_boundary.map(|(ts, lev)| (ts-1, lev))
          wrongly excludes events at ts-1 with lev_id >= stale boundary lev_id.
      Root cause: execute_query_internal applies no residual predicate on kinds/authors/ids
      after hydration (confirmed by grep: filter.kinds, filter.authors, filter.ids never read
      inside execute_query_internal).
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Lines 203-292: merge_prefixes called with no prefix-bound check; no since enforcement; broken DUPSORT windowing restart (line 200: uses wb_ts for restart instead of exclusive resume); stuck-window lev_id preserved at line 250"
      - path: "src/query/merge.rs"
        issue: "Lines 103-157: merge_prefixes iterates per-prefix scan results with no starts_with prefix guard; cross-prefix contamination propagates to engine"
    missing:
      - "In merge_prefixes (or engine.rs pre-merge), enforce that each per-prefix stream only yields entries whose key bytes start with the prefix (start_key[..start_key.len()-8]); use take_while not filter"
      - "In execute_query_internal, enforce filter.since as a stop bound on the merged stream (break when ts < since)"
      - "Replace the Included-restart windowing with key-granular exclusive resume (proven pattern already in scan.rs collect_window / scan_index_windowed)"
      - "When advancing stuck window to ts-1, reset lev_id to u64::MAX to avoid wrongly excluding events at the new ts"
      - "Add a post-hydration residual predicate on kinds/authors/ids in execute_query_internal"

  - truth: "A tag filter (Event__tag) returns events matching the given tag name and values"
    status: failed
    reason: |
      Two bugs prevent correct tag filter semantics.
      (1) CR-07: Event__tag key hex-decode is too broad. router.rs line 236 hex-decodes any value
          that decode_hex accepts (any even-length hex string). strfry's rule is 64-char lowercase hex
          only (32-byte ids). A tag value like "beef", "face", or "decade" is a valid topic tag stored
          as raw UTF-8 by strfry but hex-decoded by this code, placing the scan start key at the wrong
          position. Confirmed: the SUMMARY acknowledges the fix was specifically for 64-char hex but the
          implementation still decodes any even-length hex string (router.rs:236 uses decode_hex with no
          length or case restriction).
      (2) CR-06: Multiple tag filters evaluated with OR semantics; NIP-01 requires AND across fields.
          engine.rs lines 271-285: 'outer loop breaks on first matching TagFilter — any single tag
          matching causes passes=true. A query {#e: [X], #p: [Y]} returns events that have X OR are
          tagged to Y, not both. Also, the router builds scan keys for all tag filters' values combined,
          scanning events that match any tag.
    artifacts:
      - path: "src/query/router.rs"
        issue: "Line 236: decode_hex applied to any even-length hex value, not restricted to 64-char lowercase — diverges from strfry's 64-char rule; also line 233: tag name silently truncated to first byte (WR-04)"
      - path: "src/query/engine.rs"
        issue: "Lines 271-285: tag residual uses OR semantics (break 'outer on first match) instead of AND across distinct TagFilter fields (NIP-01)"
    missing:
      - "Restrict Event__tag hex decode to exactly 64-char lowercase hex only (value.len() == 64 && value.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f')))"
      - "Change tag residual from OR (break 'outer on first match) to AND (tags_filter.iter().all(|tf| ...)) across distinct filter fields"
      - "Build scan keys from a single TagFilter's values only (values within one filter are ORed; use the most selective tag as the scan driver)"

  - truth: "execute_query emits an opaque PageCursor for the last result and resuming with that cursor returns the next page with no overlap"
    status: failed
    reason: |
      CR-05: When hydrate_lev_ids skips a corrupt payload (the designed-for D-11 path), its output
      is shorter than the input lev_ids. engine.rs line 261 then zips filtered_batch.iter() with
      hydrated.into_iter() positionally — after the first skip every subsequent event is paired with
      the previous entry's (ts, lev_id). The PageCursor built from valid.last() carries the wrong
      (created_at, lev_id), so the next page either re-emits or skips events. The unit test
      (test_execute_query_cursor_resume) passes only because the fixture has no corrupt payloads
      (skip path is never triggered). The design intent (D-11 degrades gracefully) is violated.
    artifacts:
      - path: "src/query/engine.rs"
        issue: "Line 261: positional zip of filtered_batch and hydrated produces key misalignment when hydrate_lev_ids skips a payload; cursor built from wrong (ts, lev_id)"
      - path: "src/query/hydrate.rs"
        issue: "hydrate_lev_ids signature returns Vec<DecodedEvent> with no levId association; enables caller misalignment when length differs from input"
    missing:
      - "Change hydrate_lev_ids to return Vec<(LevId, DecodedEvent)> or Vec<Option<DecodedEvent>> (slot-for-slot) so positional association is impossible to break"
      - "In engine.rs, join hydrated results on lev_id rather than positional zip"
deferred: []
---

# Phase 3: Query Engine Verification Report

**Phase Goal:** Queries are resolved against strfry's live indexes with correct filter routing, tag scans, latestPerAuthor semantics, NIP-40 expiration filtering, and cursor pagination
**Verified:** 2026-06-12
**Status:** GAPS_FOUND
**Re-verification:** No — initial verification

## Goal Achievement

The SUMMARY claims the goal is complete and all 79 tests pass. The code review (03-REVIEW.md) empirically reproduced three correctness bugs that directly contradict the goal's "correct filter routing" requirement. Verification confirms the bugs are present in the committed code and the passing test suite is insufficient to detect them (tests only exercise `kinds=[1]`, masking the cross-prefix contamination).

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | events() filter selects correct index and returns correctly ordered results hydrated to full event JSON | FAILED | CR-01 (cross-prefix contamination, empirically proven: kinds=[2] returns kind-1 events); CR-02 (since never enforced, empirically proven); no residual kind/author/id predicate in execute_query_internal; `filter.kinds`, `filter.authors`, `filter.ids` never read inside the loop |
| 2 | Tag filter (Event__tag) returns events matching given tag name and values | FAILED | CR-07: hex-decode too broad (any even-length hex, not 64-char lowercase only); CR-06: multi-tag AND semantics not implemented ('outer breaks on first match → OR) |
| 3 | latestPerAuthor returns latest N events per pubkey via Event__pubkeyKind prefix scans | VERIFIED | latest_per_author correctly filters to exact (pubkey, kind) key prefix at lines 411-419; 3 fixture-backed tests pass; no cross-kind contamination |
| 4 | Events with expiration != 0 && expiration <= now are excluded from all query results | VERIFIED | is_expired correctly implemented (engine.rs:49-64); applied in both execute_query and latest_per_author; 6-case unit test passes including malformed/short-tag edge cases |
| 5 | Cursor pagination returns the next non-overlapping page | FAILED | CR-05: zip misalignment when hydrate skips a corrupt payload; cursor built from wrong (ts, lev_id); unit test passes only because fixture has no corrupt payloads (D-11 degraded path untested end-to-end) |

**Score:** 2/5 truths verified

### Deferred Items

None.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `src/query/filter.rs` | NostrFilter, TagFilter, PageCursor, QueryError | VERIFIED | Exists, 236 lines; all four types present; PageCursor encode/decode correct; 4 unit tests pass |
| `src/query/router.rs` | select_index (D-02), build_start_keys (D-03), created_at_from_key | VERIFIED (structural) | Exists, 496+ lines; D-02 priority chain correct; key layouts correct. CR-07 hex-decode rule is a correctness bug but the file is substantive and wired. |
| `src/query/merge.rs` | MergeCandidate + Ord, merge_prefixes | VERIFIED (structural) | Exists, 340+ lines; Ord impl correct; merge algorithm correct within-prefix. Missing: cross-prefix boundary enforcement (CR-01 root location). |
| `src/query/hydrate.rs` | hydrate_lev_ids with skip-warn-count | VERIFIED (structural) | Exists, 173 lines; correct implementation. CR-05 is an engine.rs misuse of the return type, not a hydrate.rs bug. |
| `src/query/engine.rs` | execute_query, latest_per_author, is_expired | STUB (partial) | Exists, 820 lines; latest_per_author and is_expired correct. execute_query has CR-01/CR-02/CR-03/CR-04/CR-05 bugs causing silently wrong results. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `src/lib.rs` | `src/query/mod.rs` | `pub mod query` | WIRED | Confirmed: lib.rs line 11 `pub mod query;` |
| `src/query/mod.rs` | all five submodules | `pub mod engine/filter/hydrate/merge/router` | WIRED | All 5 declared at lines 4-8 |
| `src/query/engine.rs` | `merge_prefixes` | candidate stream | WIRED | engine.rs:203 `merge_prefixes(env, ...)` |
| `src/query/engine.rs` | `hydrate_lev_ids` | post-merge hydration | WIRED | engine.rs:257 `hydrate_lev_ids(env, ...)` |
| `src/query/engine.rs` | `is_expired` | NIP-40 residual | WIRED | engine.rs:263 `is_expired(&decoded.event)` |
| `execute_query` | `filter.since` | since enforcement | NOT_WIRED | `filter.since` never read in execute_query_internal; only used in build_start_keys for Forward direction (unused in Reverse queries) |
| `execute_query` | prefix boundary | cross-prefix guard | NOT_WIRED | merge_prefixes has no `starts_with` prefix filter; cross-prefix contamination unchecked |

### Data-Flow Trace (Level 4)

`execute_query` is the central dynamic-data artifact.

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|--------------|--------|--------------------|--------|
| `execute_query` | `valid` (DecodedEvent vec) | `merge_prefixes` → `hydrate_lev_ids` → LMDB EventPayload | Real LMDB data flows | HOLLOW — data flows but is contaminated (wrong-kind events, missing since bound) |
| `latest_per_author` | per-author buckets | `scan_index_bounded` → `hydrate_lev_ids` → LMDB | Real LMDB data flows | FLOWING |
| `hydrate_lev_ids` | `results` (DecodedEvent vec) | `get_event_payload` → LMDB EventPayload | Real LMDB data flows | FLOWING |

### Behavioral Spot-Checks

The review author ran the empirical reproduction harness outside this verification. I confirm the bugs exist in the committed code by static analysis:

| Behavior | Evidence | Status |
|----------|---------|--------|
| kinds=[2] limit=10 returns only kind=2 events | merge_prefixes has no prefix guard; reverse scan from kind=2 start key walks into kind=1 entries; execute_query has no kind residual predicate | FAIL (static + review proof) |
| kinds=[1] since=1715000000 respects since bound | `filter.since` never read in execute_query_internal; only used in build_start_keys for Forward; Reverse always uses until | FAIL (static) |
| authors=[pk1, pk1] limit=6 returns 3 unique events | WR-01: duplicate start keys produce duplicate streams; merge emits each event twice | FAIL (static + review proof) |
| Cursor resume: page1 + page2 == limit=4 first four (no corrupt payloads) | test_execute_query_cursor_resume passes against clean fixture | PASS (fixture only) |
| is_expired correctly excludes past-expiration events | Direct unit test of predicate with 6 cases | PASS |

### Probe Execution

No probe scripts declared or present in scripts/ for this phase.

Step 7c: SKIPPED (no probes).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| QRY-01 | 03-02, 03-04 | events() routes to most selective index, returns correctly ordered results | BLOCKED | CR-01 (cross-prefix contamination), CR-02 (since not enforced) — correct filter routing is the stated crux and is broken |
| QRY-02 | 03-02, 03-04 | Tag filters resolved via Event__tag | BLOCKED | CR-07 (hex-decode too broad), CR-06 (AND/OR semantics wrong for multi-tag) |
| QRY-03 | 03-04 | latestPerAuthor returns latest N per pubkey via Event__pubkeyKind | SATISFIED | latest_per_author correctly implemented with exact prefix key guard |
| QRY-04 | 03-03 | Hydrates full event JSON by point-looking-up EventPayload[levId] | PARTIALLY SATISFIED | hydrate_lev_ids correctly implemented; CR-05 is a misuse by the engine caller (zip misalignment on skip), not a hydrate.rs defect |
| QRY-05 | 03-04 | NIP-40 expiration filtered at query time | SATISFIED | is_expired correctly implemented and applied in both execute_query and latest_per_author |

REQUIREMENTS.md traceability marks QRY-01 and QRY-02 as "Complete" and QRY-03/QRY-04/QRY-05 as "Pending". Post-execution, QRY-03 and QRY-05 are actually satisfied; QRY-01 and QRY-02 are blocked by correctness bugs.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| engine.rs | 134-138 | `filter.limit == 0` treated as DEFAULT_WINDOW_SIZE (256) but filter.rs doc says "unbounded" | Warning (IN-01) | Doc/implementation contract mismatch; Phase-4 callers reading docs will expect 0=unbounded |
| engine.rs | 153 | `cursor.unwrap()` after `cursor.is_some()` | Warning (IN-04) | Unnecessary unwrap; clippy::unnecessary_unwrap |
| merge.rs | 329-339 | `assert!(true)` compile-time proof test | Info (IN-03) | Vacuous test; clippy::assertions_on_constants |
| engine.rs / router.rs | multiple | Duplicate hex-decode helpers (decode_hex_32/nibble in engine.rs, decode_hex/nibble in router.rs) | Info (IN-02) | Two code paths to keep byte-identical |
| engine.rs | 261 | `filtered_batch.iter().zip(hydrated.into_iter())` | BLOCKER (CR-05) | Positional zip misaligns keys when hydrate skips — corrupts PageCursor |
| engine.rs | 250 | `window_boundary.map(|(ts, lev)| (ts.saturating_sub(1), lev))` | BLOCKER (CR-04) | Keeps stale lev_id when advancing ts; wrongly excludes valid events |
| merge.rs | 103-157 | No `starts_with` prefix guard on per-stream scan results | BLOCKER (CR-01 root) | Cross-prefix contamination for all non-lowest-prefix filter values |
| engine.rs | internal | filter.since never read in execute_query_internal | BLOCKER (CR-02) | Since bound completely unenforced in reverse queries |
| router.rs | 236 | `decode_hex(value)` on any even-length hex, not 64-char lowercase only | BLOCKER (CR-07) | Wrong index position for literal even-length-hex-looking tag values |
| engine.rs | 271-285 | Tag residual uses OR ('outer breaks on first TagFilter match) | BLOCKER (CR-06) | Multi-tag queries return wrong events (OR instead of AND semantics) |

### Human Verification Required

None — all identified gaps are statically verifiable from code analysis and the review's empirical reproduction. No items require running the full application.

## Gaps Summary

Three root-cause clusters block the phase goal:

**Cluster 1 (CR-01 + CR-02 + CR-04): execute_query returns silently wrong events.**
The core defect is that `merge_prefixes` performs no prefix boundary enforcement — each per-prefix reverse scan walks into lexicographically smaller prefixes, returning events from wrong kinds/authors/ids. `execute_query_internal` compounds this by having no residual predicate on kinds/authors/ids after hydration, and by never reading `filter.since` as a stop bound. `latest_per_author` avoids this via its own explicit key-byte prefix check (lines 411-419), proving the fix is straightforward. The stuck-window advance (CR-04) is a secondary corruption in the same windowing loop.

**Cluster 2 (CR-05): Cursor pagination corrupted when hydrate skips a payload.**
The positional zip between `filtered_batch` (keyed by ts/lev_id) and `hydrated` (Vec of DecodedEvents, possibly shorter) shifts all keys after any skip. This is a latent bug in the D-11 degraded path — invisible against the clean fixture, but will corrupt real production queries with any corrupt payload.

**Cluster 3 (CR-06 + CR-07): Tag filter semantics and key construction incorrect.**
Event__tag key hex-decode applies to any even-length hex string (not only 64-char lowercase ids), silently missing literal topic tags. Multi-tag queries use OR semantics across distinct filter fields, violating NIP-01's AND requirement.

The test suite (26 query unit tests, all passing) does not detect any of these bugs because: (1) kind=1 is the lowest kind in the fixture, so the reverse scan cannot contaminate it from below; (2) all filter tests use since=None; (3) the fixture has no corrupt payloads; (4) all tag filter tests use a single TagFilter.

---

_Verified: 2026-06-12_
_Verifier: Claude (gsd-verifier)_
