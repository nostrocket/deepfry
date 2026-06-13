---
phase: 08-frontier-prioritization-timeout-observability
plan: "01"
subsystem: database
tags: [dgraph, dql, frontier-prioritization, exponential-backoff, follower-count-ordering, config, viper, tdd]

dependency_graph:
  requires:
    - phase: 05-pubkey-validation-hardening
      provides: [MarkAttempted recover-or-purge (VALID-03), last_attempt predicate, isValidHexPubkey gate]
  provides:
    - "next_attempt + miss_count Dgraph predicates (additive to Profile)"
    - "BackoffInterval(missCount, base, ratio, cap) pure geometric-backoff helper (pkg/dgraph/backoff.go)"
    - "BackoffParams value struct + DefaultBackoffParams() (config-decoupled param threading)"
    - "GetStalePubkeys: both phases ordered by descending count(~follows) via var/val() pattern; aged phase keys on lt(next_attempt, now)"
    - "MarkAttempted(ctx, pubkeys, ts, hits, params) — hit/miss backoff stamping, VALID-03 preserved"
    - "BackfillNextAttempt(ctx) — one-time idempotent D-06 seeding"
    - "CountStalePubkeys(ctx) — frontier + aged-eligible honest count (METRIC-01)"
    - "config keys: relay_eose_quorum (0.70), miss_backoff group, timeout default 15s"
  affects: [08-02, crawler-main-loop, whitelist-plugin-readers]

tech_stack:
  added: []
  patterns:
    - "var/val()-aggregation-ordering (orderdesc: val(fc) where fc as count(~follows)) — Dgraph v25 rejects orderdesc: count(~follows) inline"
    - "config-decoupled-params-struct (BackoffParams passed as arg, pkg/dgraph never imports pkg/config — avoids import cycle)"
    - "overflow-clamp-before-shift (BackoffInterval clamps to cap before 1<<missCount can overflow int64)"
    - "post-unmarshal non-positive guard (malformed miss_backoff resets to defaults, mirrors EjectionThresholds)"

key_files:
  created:
    - pkg/dgraph/backoff.go
    - pkg/dgraph/backoff_test.go
  modified:
    - pkg/dgraph/dgraph.go
    - pkg/dgraph/dgraph_stale_test.go
    - pkg/dgraph/dgraph_validation_test.go
    - pkg/config/config.go
    - cmd/crawler/main.go

decisions:
  - "Follower-count ordering uses a var block computing `fc as count(~follows)` then `func: uid(fc), orderdesc: val(fc)` — Dgraph v25 rejects `orderdesc: count(~follows)` directly in the func line ('Expected val(). Got count() with order.'). Semantically equivalent; D-10 stored follower_count fallback NOT needed."
  - "D-09 verified live on the production graph (human checkpoint): the val()-ordered frontier returns the true top-N by follower count with first:N and is NOT silently truncated to 1000 before sorting. Confirmed — no D-10 fallback scheduled."
  - "BackoffParams is a value struct in pkg/dgraph/backoff.go constructed at the cmd/crawler call site from cfg.MissBackoff; pkg/dgraph stays free of pkg/config (no import cycle). DefaultBackoffParams() is the safe fallback."
  - "MarkAttempted keeps stamping last_attempt=ts for all valid pubkeys (preserves existing aging truthfulness) in addition to the new next_attempt/miss_count hit/miss stamping."
  - "olderThanUnix kept in GetStalePubkeys signature but deprecated/ignored by the aged phase (now lt(next_attempt, now)) — avoids a churny signature break this wave; Plan 02 call site unchanged."
  - "VALID-03 recover-or-purge block preserved verbatim; only the final nquad-builder branch changed for hit/miss stamping."

metrics:
  duration: "~2h wall (across a session-limit interruption; ~12 min active executor time)"
  completed: "2026-06-13"
  tasks: 3
  files: 7
---

# Phase 08 Plan 01: Dgraph + Config Foundation Summary

**Additive `next_attempt`/`miss_count` predicates, follower-count-ordered frontier selection (PERF-01), hit/miss exponential-backoff stamping with idempotent backfill (PERF-02), and the honest `CountStalePubkeys` metric (METRIC-01) — the data layer Wave 2's crawler wiring consumes.**

## Performance

- **Duration:** ~2h wall-clock (spanned a session/quota limit; ~12 min active executor time across two dispatches)
- **Tasks:** 3 of 3 implementation tasks complete (Task 4 was the live-verification checkpoint)
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments

- **PERF-01 — follower-count prioritization:** `GetStalePubkeys` now orders both the never-attempted frontier and the aged top-up by descending `count(~follows)`, so high-value pubkeys refresh first. The aged phase keys on `lt(next_attempt, now)` instead of `last_attempt`.
- **PERF-02 — exponential backoff:** `MarkAttempted` stamps hits (`next_attempt = now+24h`, `miss_count=0`) vs misses (`next_attempt = now + min(2h·2^miss_count, 7d)`, `miss_count++`), with a pure `BackoffInterval` helper that clamps to cap before the bit-shift can overflow. `BackfillNextAttempt` idempotently seeds existing attempted nodes (D-06).
- **METRIC-01 — honest stale count:** `CountStalePubkeys` returns frontier + aged-eligible, matching `GetStalePubkeys` selection semantics, so Wave 2 can replace the always-zero `staleRemaining`.
- **Config foundation:** `relay_eose_quorum` (0.70), `miss_backoff` group (base 2h / ratio 2 / cap 7d / hit cadence 24h), and timeout default cut 30s→15s — with a non-positive guard resetting malformed values to defaults.

## Task Commits

1. **Task 1: Schema predicates, config keys, backoff math helper** — `1db9e65` (feat)
2. **Task 2: Follower-ordered GetStalePubkeys + CountStalePubkeys** — `650e748` (feat)
3. **Task 3: Hit/miss MarkAttempted stamping + BackfillNextAttempt** — `3bf1327` (feat)

_Tasks 2 and 3 are TDD with integration tests gated on live Dgraph (`-tags integration`)._

## Files Created/Modified

- `pkg/dgraph/backoff.go` (created) — `BackoffInterval` pure helper + `BackoffParams` struct + `DefaultBackoffParams()`
- `pkg/dgraph/backoff_test.go` (created) — table-driven backoff math incl. overflow-clamp + ratio-3 cases
- `pkg/dgraph/dgraph.go` — schema predicates, ordered `GetStalePubkeys`, extended `MarkAttempted` + `resolveUIDsWithMissCount`, `BackfillNextAttempt`, `CountStalePubkeys`
- `pkg/dgraph/dgraph_stale_test.go` — integration tests for ordering, count, hit/miss, backfill
- `pkg/dgraph/dgraph_validation_test.go` — VALID-03 recover/purge test updated for new `MarkAttempted` signature
- `pkg/config/config.go` — `MissBackoffParams` struct, `RelayEOSEQuorum`, defaults + non-positive guard, timeout 15s
- `cmd/crawler/main.go` — `MarkAttempted` call site updated (TODO placeholder for Plan 02 hit-set + BackoffParams wiring)

## Decisions Made

See frontmatter `decisions`. Headline: the Dgraph-v25 `var/val()` ordering workaround replaces the inline `orderdesc: count(~follows)` the plan originally assumed, and D-09 live verification confirmed it is not pre-truncated — so the D-10 stored-`follower_count` fallback is **not** needed.

## Deviations from Plan

Minor, in-scope: the executor updated the `cmd/crawler/main.go` `MarkAttempted` call site with a TODO placeholder so the tree builds in sequential mode (Plan 02 owns the real hit-set + `BackoffParams` wiring). No scope creep.

## Issues Encountered

The first executor dispatch was interrupted by a session/quota limit mid-Task-2 (work uncommitted but build+unit-green). A fresh continuation executor reviewed the uncommitted Task 2 diff, committed it, and completed Task 3 — no work lost.

## D-09 Verification Outcome

**CONFIRMED (live, production graph).** `make test` and `make test-integration` both green (dgraph integration ~36s against live Dgraph: ordering, count, hit/miss, backfill, and VALID-03 recover/purge all pass). The val()-aggregation frontier query returns the true top-N by follower count without 1000-row pre-truncation. No D-10 fallback required.

## Next Phase Readiness

Plan 02 (Wave 2) can now wire the live crawler: change `FetchAndUpdateFollows` to return the `hits` set, construct `dgraph.BackoffParams` from `cfg.MissBackoff` at the `cmd/crawler/main.go` call site, call `BackfillNextAttempt` once at startup after `EnsureSchema`, and replace `staleRemaining` with `CountStalePubkeys`. Final signature: `MarkAttempted(ctx, pubkeys []string, ts int64, hits map[string]struct{}, params BackoffParams) error`.

---
*Phase: 08-frontier-prioritization-timeout-observability*
*Completed: 2026-06-13*
