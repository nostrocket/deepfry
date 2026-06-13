---
phase: 08-frontier-prioritization-timeout-observability
verified: 2026-06-13T00:00:00Z
status: human_needed
score: 9/9 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Run crawler against live Dgraph + relays for several batches and confirm batches complete well under 30s (target ~12–15s). Verify the live log line shows staleRemaining as a real non-zero number, not 0."
    expected: "Batches complete noticeably faster than 30s; staleRemaining is a real changing value (live run observed ~594.9k per SUMMARY)."
    why_human: "TIMEOUT-01/02 real-batch timing and METRIC-01 live log output cannot be verified by grep/static analysis alone; requires observing running crawler output."
  - test: "On a fresh start after the code is deployed, confirm the BackfillNextAttempt log line reports a node count >= 0, then restart and confirm it reports 0 (idempotent)."
    expected: "First run: 'BackfillNextAttempt: seeded N nodes with initial next_attempt'. Second run: 'seeded 0 nodes'."
    why_human: "Idempotency requires a live Dgraph with existing last_attempt nodes; cannot be confirmed statically."
  - test: "Confirm the follower-count ordering surfaces genuinely high-follower pubkeys first in a batch — compare the first few returned pubkeys against known high-follower accounts."
    expected: "Top pubkeys in the batch have many followers; ordering is not random."
    why_human: "GetStalePubkeys ordering is verified by integration tests against a test fixture, but correctness against production-scale data requires the D-09 live check (already approved in human checkpoint per SUMMARY, but the verifier cannot independently confirm)."
---

# Phase 8: Frontier Prioritization, Timeout & Observability — Verification Report

**Phase Goal:** Order the stale frontier by follower count, apply exponential backoff to long-miss stubs, cut relay timeout to 15s, add EOSE-quorum early exit, and fix the staleRemaining metric.
**Verified:** 2026-06-13
**Status:** human_needed (all 9 automated must-haves verified; 3 behavioral checks require a live run)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | GetStalePubkeys returns frontier and aged pubkeys ordered by incoming follower count descending | VERIFIED | `dgraph.go:527-554`: both frontier and aged queries use `var` block computing `fc as count(~follows)` and `orderdesc: val(fc)` with explicit `first:N`; D-09 confirmed on live production graph per SUMMARY |
| 2 | A pubkey that misses gets next_attempt advanced by geometric backoff (2h base, x2, capped at 7d); a hit gets next_attempt = now+24h and miss_count reset to 0 | VERIFIED | `dgraph.go:714-728`: HIT branch writes `next_attempt = ts+HitRefreshCadence, miss_count=0`; MISS branch calls `BackoffInterval(n.MissCount, params.Base, params.Ratio, params.Cap)` and writes `next_attempt = ts+interval, miss_count++`. Config defaults: base=2h, ratio=2, cap=168h, hit cadence=24h confirmed in `config.go:130-135` and `backoff.go:24-31` |
| 3 | Existing attempted nodes (last_attempt set, next_attempt absent) are backfilled to next_attempt = last_attempt + 24h, miss_count = 0 | VERIFIED | `dgraph.go:814-861`: `BackfillNextAttempt` queries `has(last_attempt) AND NOT has(next_attempt)`, writes `next_attempt = last_attempt + 86400, miss_count = 0`. Called non-fatally at startup in `crawler.go:140-144` immediately after `EnsureSchema` |
| 4 | CountStalePubkeys returns frontier count plus aged-eligible count, matching GetStalePubkeys selection semantics | VERIFIED | `dgraph.go:903-944`: uses `frontier(func: has(pubkey)) @filter(NOT has(last_attempt)) { count(uid) }` + `aged(func: has(next_attempt)) @filter(lt(next_attempt, now)) { count(uid) }` — identical predicate semantics to GetStalePubkeys. Returns `frontierCount + agedCount` |
| 5 | The Dgraph 1000-row sorted-query cap is verified not to truncate the ordered frontier (D-09); D-10 fallback documented if it fails | VERIFIED | D-09 confirmed at human checkpoint (per SUMMARY): val()-aggregation pattern `uid(fc), orderdesc: val(fc)` was tested on the production graph and returns the true top-N without pre-truncation. D-10 fallback explicitly noted as not needed in `dgraph.go:506-507` |
| 6 | The per-batch relay query timeout fires at 15s for fresh/unset configs (TIMEOUT-01) | VERIFIED | `config.go:97`: `viper.SetDefault("timeout", "15s")`. `crawler.go:458`: `context.WithTimeout(relayContext, c.timeout)` — config-driven, no hardcode. Default 15s flows from config → `crawler.Config.Timeout` → `c.timeout` |
| 7 | A batch whose alive queried relays reach >=70% EOSE-or-error cancels the relay query context early (TIMEOUT-02) | VERIFIED | `crawler.go:415-420`: `quorumReached` helper; `crawler.go:484-503`: `done.Add(1)` on both error path and success path, `cancel()` when `quorumReached`. `config.go:127`: default quorum 0.70. Zero-fraction guard at `crawler.go:416` prevents premature cancel. 8/8 `TestQuorumReached_*` unit tests pass |
| 8 | FetchAndUpdateFollows returns the actual hit-set (pubkeys with events), which flows into MarkAttempted for PERF-02 hit/miss stamping | VERIFIED | `crawler.go:425`: return type `(map[string]struct{}, error)`. `main.go:150`: `hitSet, err := crawler.FetchAndUpdateFollows(...)`. `main.go:176`: `dgraphClient.MarkAttempted(ctx, batchKeys, time.Now().Unix(), hitSet, backoffParams)` |
| 9 | staleRemaining in the progress log reflects CountStalePubkeys() minus the batch size, not zero (METRIC-01) | VERIFIED | `main.go:140`: `totalStale, err := dgraphClient.CountStalePubkeys(ctx)`. `main.go:182`: `staleRemaining := max(0, totalStale-len(pubkeys))` (WR-01 clamp applied). Log line at `main.go:183`: reports `staleRemaining` and `len(hitSet)` |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/dgraph/backoff.go` | Pure backoff-interval math helper + BackoffParams struct | VERIFIED | 85 lines; `BackoffInterval`, `BackoffParams`, `DefaultBackoffParams`, `min64`. CR-01 fix applied: iterative multiply with `result > (cap-1)/r` overflow guard. Non-aligned cap test (`TestBackoffIntervalNonAlignedCap`) locks the fix |
| `pkg/dgraph/dgraph.go` | next_attempt + miss_count schema, ordered GetStalePubkeys, hit/miss MarkAttempted, CountStalePubkeys, BackfillNextAttempt | VERIFIED | Schema at lines 62-79 (both predicates + Profile type block). GetStalePubkeys at 518-561. MarkAttempted at 607-740. CountStalePubkeys at 903-944. BackfillNextAttempt at 814-861 |
| `pkg/config/config.go` | MissBackoffParams struct, RelayEOSEQuorum field, miss_backoff + relay_eose_quorum SetDefaults, timeout 15s | VERIFIED | `MissBackoffParams` struct at lines 27-38. `RelayEOSEQuorum float64` at line 64. `MissBackoff MissBackoffParams` at line 67. SetDefault("timeout","15s") at line 97. SetDefault("relay_eose_quorum", 0.70) at line 127. SetDefault("miss_backoff", ...) at lines 130-135. Non-positive guards at lines 178-189 |
| `pkg/crawler/crawler.go` | quorum field, RelayEOSEQuorum in Config, quorumReached helper, done atomic counter, FetchAndUpdateFollows hit-set return, BackfillNextAttempt in New() | VERIFIED | `quorum float64` at line 107. `RelayEOSEQuorum float64` in Config at line 120. `quorumReached` at lines 415-421. `done atomic.Int32` at line 470. `done.Add(1)` on error path (484) and success path (498). Return type `(map[string]struct{}, error)` at line 425. `BackfillNextAttempt` at lines 140-144 |
| `cmd/crawler/main.go` | CountStalePubkeys-based staleRemaining, RelayEOSEQuorum threaded, hit-set MarkAttempted call | VERIFIED | `CountStalePubkeys` at line 140. `RelayEOSEQuorum: cfg.RelayEOSEQuorum` at line 81. `hitSet` at line 150. `MarkAttempted(..., hitSet, backoffParams)` at line 176. `staleRemaining := max(0, totalStale-len(pubkeys))` at line 182 |
| `pkg/crawler/crawler_quorum_test.go` | quorumReached unit tests | VERIFIED | 8 test cases covering threshold/zero-fraction/zero-queried/ceil-rounding/full-fraction/single-relay; all pass; `-race` clean |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `GetStalePubkeys` aged phase | `next_attempt` predicate | `lt(next_attempt, now)` filter | WIRED | `dgraph.go:547`: `var(func: has(next_attempt)) @filter(lt(next_attempt, %d))` with `time.Now().Unix()` |
| `MarkAttempted` | `miss_count / next_attempt` nquads | hit/miss branch on hits set | WIRED | `dgraph.go:715-728`: `if _, isHit := hits[n.Pubkey]; isHit { ... } else { ... }` with explicit `next_attempt` and `miss_count` nquad writes on both branches |
| `FetchAndUpdateFollows` goroutine | `relayQueryContext cancel()` | quorum counter reaching ceil(quorum*queried) | WIRED | `crawler.go:484,498`: `quorumReached(done.Add(1), queriedRelays, c.quorum)` → `cancel()` on both error and success goroutine paths |
| `main.go` main loop | `MarkAttempted(... hitSet)` | hit-set returned by FetchAndUpdateFollows | WIRED | `main.go:150`: `hitSet, err := crawler.FetchAndUpdateFollows(...)` → `main.go:176`: `dgraphClient.MarkAttempted(ctx, batchKeys, ..., hitSet, backoffParams)` |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `cmd/crawler/main.go` staleRemaining log | `totalStale` | `dgraphClient.CountStalePubkeys(ctx)` | Yes — queries Dgraph with `count(uid)` over real predicate filters | FLOWING |
| `cmd/crawler/main.go` log `had events` | `len(hitSet)` | `crawler.FetchAndUpdateFollows(ctx, pubkeys)` | Yes — map built from real relay events at `crawler.go:589` | FLOWING |
| `MarkAttempted` backoff stamp | `n.MissCount` | `resolveUIDsWithMissCount(ctx, valid)` → DQL `eq(pubkey, [...]) { miss_count }` | Yes — queries live Dgraph node data | FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| BackoffInterval math (miss 0..7, overflow, non-aligned cap) | `go test ./pkg/dgraph/ -run TestBackoffInterval -v -short` | 3 test functions PASS | PASS |
| quorumReached threshold math (8 cases) | `go test ./pkg/crawler/ -run TestQuorumReached -race -v` | 8 tests PASS, race-clean | PASS |
| Full unit suite | `go test ./... -short -race` | all packages ok, 0 failures | PASS |
| Build + vet | `go build ./... && go vet ./...` | both clean | PASS |
| Live batch timing (TIMEOUT-01/02) | requires running crawler | per SUMMARY: ~12–14s batches | SKIP (live only) |
| staleRemaining non-zero (METRIC-01) | requires running crawler | per SUMMARY: ~594.9k | SKIP (live only) |
| BackfillNextAttempt idempotency | requires live Dgraph | per SUMMARY: 0 on restart, 0 stranded nodes | SKIP (live only) |

### Probe Execution

No `scripts/*/tests/probe-*.sh` probes declared or conventional for this phase. Step 7c: SKIPPED (no probe files).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| PERF-01 | 08-01 | GetStalePubkeys orders frontier by follower count descending | SATISFIED | `dgraph.go:527-554`: var/val() orderdesc on both frontier and aged phases |
| PERF-02 | 08-01, 08-02 | Exponential backoff for chronic-miss pubkeys; hit resets | SATISFIED | `backoff.go`: BackoffInterval; `dgraph.go:714-728`: hit/miss nquad branches; `main.go:176`: hitSet flows in |
| TIMEOUT-01 | 08-02 | Per-batch timeout reduced to 15s | SATISFIED | `config.go:97`: default "15s"; `crawler.go:458`: config-driven context timeout |
| TIMEOUT-02 | 08-02 | Early cancel at >=70% EOSE-or-error | SATISFIED | `crawler.go:415-503`: quorumReached + done.Add(1) on both paths + cancel() |
| METRIC-01 | 08-01, 08-02 | staleRemaining from CountStalePubkeys, not zero | SATISFIED | `main.go:140,182`: CountStalePubkeys + max(0, totalStale-len(pubkeys)) |

Note: REQUIREMENTS.md traceability table still shows all five IDs as `[ ] Pending` — a documentation gap. The status rows (lines 101-111) were not updated to `Complete`. This is a WARN-level doc debt, not a code defect. The implementation is present and verified.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None found | — | Scanned for TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER | — | Clean: no unresolved debt markers in any Phase 8 modified file |

No empty implementations, no hardcoded-empty data sources, no console.log stubs.

Known deferred issues from code review (tracked in `.planning/todos/pending/phase08-code-review-hardening-followups.md`):

- WR-02: MarkAttempted multi-transaction non-atomicity (hardening, not goal-blocking)
- WR-03: BackfillNextAttempt unbounded single mutation (production-scale risk; deferred since live backfill confirmed 0 stranded nodes at time of execution)
- WR-04: forwardEvent publish timeout missing
- WR-05: GetStalePubkeys frontier large-scale sort-cap (D-09 verified live; test coverage outstanding)
- IN-01..04: minor robustness notes

These are tracked deferred items, not phase-goal blockers.

### Human Verification Required

#### 1. Batch Timing (TIMEOUT-01 + TIMEOUT-02)

**Test:** Run `make build-crawler && ./bin/crawler` against live Dgraph + relays for 5+ batches. Observe the elapsed time per batch.
**Expected:** Batches complete in approximately 12–15s — well under the old 30s ceiling. The log should show early quorum-triggered cancellation on most batches. The hard 15s cap fires as a backstop when fewer than 70% of relays respond.
**Why human:** Batch timing is a runtime observable. Static analysis confirms the timeout is 15s and the quorum counter wires cancel() correctly, but the actual timing requires running the crawler.

#### 2. staleRemaining Metric is Non-Zero (METRIC-01)

**Test:** In the same live run, inspect the log line: `Batch complete: queried N pubkeys (M had events) | K stale remaining | T total in DB`. Confirm K is a real non-zero number that changes across batches.
**Expected:** staleRemaining > 0 and changes over the session. The value approximates the production graph's stale queue depth.
**Why human:** CountStalePubkeys returns a live Dgraph count; confirming it is non-zero and meaningful requires the running crawler.

#### 3. BackfillNextAttempt Idempotency (D-06)

**Test:** On the first start with the new binary, note the log line `BackfillNextAttempt: seeded N nodes`. Restart and confirm it logs `seeded 0 nodes`.
**Expected:** N > 0 on first run (seeds pre-existing attempted nodes), 0 on subsequent runs.
**Why human:** Requires a live Dgraph with existing last_attempt nodes to confirm actual seeding count; static analysis shows the query and mutation are correct but cannot confirm N > 0 without production data.

### Gaps Summary

No gaps. All 9 must-haves are verified in the codebase. The 3 human verification items are behavioral confirmations of correct wiring (timing, live metric values, live idempotency) — the code evidence for all three is complete and correct.

The REQUIREMENTS.md traceability table has a documentation gap (rows still show `[ ] Pending` for PERF-01/02/TIMEOUT-01/02/METRIC-01) but this does not affect phase goal achievement.

---

_Verified: 2026-06-13_
_Verifier: Claude (gsd-verifier)_
