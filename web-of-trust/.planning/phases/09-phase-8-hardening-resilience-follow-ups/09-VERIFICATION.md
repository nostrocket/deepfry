---
phase: 09-phase-8-hardening-resilience-follow-ups
verified: 2026-06-13T14:45:00Z
status: human_needed
score: 5/5 must-haves verified (3 static, 2 pending live-host confirmation)
overrides_applied: 0
human_verification:
  - test: "Run crawler for at least one batch on strfry host; confirm batch completes and events are forwarded normally"
    expected: "Batch complete log line appears; forward-relay receives events; MarkAttempted runs; no stall or hang"
    why_human: "HARD-03 bounded forwardEvent publish: static wiring of context.WithTimeout(ctx, c.timeout) is verified, but the behavioural guarantee — that a hung forward relay does not stall the drain loop — requires a live relay to observe"
  - test: "While crawler is mid-run, bounce the Dgraph alpha container (docker restart dgraph-alpha). Observe log output."
    expected: "Crawler logs WARN 'Transient Dgraph error ... (attempt 1/5) ... retrying in 5s' and resumes after Dgraph comes back without the process exiting"
    why_human: "RESIL-01 transient retry: the classification and retry loop are statically verified in code, but whether the observed gRPC EOF actually maps to codes.Unavailable in practice requires triggering the condition live"
  - test: "Point crawler at a bad Dgraph address (or stop Dgraph past the retry budget) and confirm it exits"
    expected: "After 5 attempts crawler logs 'Dgraph unavailable after 5 attempts ... exiting' and terminates (does not hang)"
    why_human: "RESIL-01 fatal path: static code shows break mainLoop after exhausted retries, but confirming it does not hang requires live observation"
---

# Phase 9: Phase 8 Hardening & Resilience Follow-ups — Verification Report

**Phase Goal:** The deferred Phase 8 code-review warnings are closed and the main crawl loop survives transient Dgraph blips without a process restart — latent failure modes that the live run happened to dodge can no longer silently strand state or stall the drain loop.

**Verified:** 2026-06-13T14:45:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | HARD-01: BackfillNextAttempt paginates query AND mutation in batchSize windows, re-querying offset:0 after each commit | VERIFIED | `dgraph.go:857` — `first: %d, offset: 0` with `@filter(NOT has(next_attempt))`; query txn inline-discarded at line 865; `CommitNow: true` mutation per-window at line 893; mutation txn inline-discarded at line 895; loop exits on empty page |
| 2 | HARD-02: MarkAttempted recovery txn discarded INLINE per-iteration; stamp-vs-recovery independence documented; VALID-03 semantics preserved | VERIFIED | `dgraph.go:694` — `txn.Discard(ctx)` immediately after `txn.Mutate`, inside the for-loop body, not deferred; lines 678-688 document independence and retry-safety; decision tree (recoverable/duplicate/unrecoverable) intact |
| 3 | HARD-03: forwardEvent publish wrapped in context.WithTimeout(ctx, c.timeout) | VERIFIED (static) | `crawler.go:244-246` — `pubCtx, cancel := context.WithTimeout(ctx, c.timeout); defer cancel(); err := c.forwardRelay.conn.Publish(pubCtx, *event)` — code structure correct; runtime behaviour needs live-host confirmation |
| 4 | HARD-04: large-frontier sort-cap covered via documentation path (doc comment citing D-09/WR-05) | VERIFIED | `dgraph.go:509-519` — doc comment on GetStalePubkeys cites `08-REVIEW.md WR-05`, references D-09 human checkpoint, states "on the production graph (100k+ frontier nodes) first: N was verified to be honored together with orderdesc: val(fc)" |
| 5 | RESIL-01: main loop retries transient Dgraph errors (Unavailable/DeadlineExceeded/ResourceExhausted) with backoff; fatal errors exit loudly | VERIFIED (static) | `main.go:37-51` — `isDgraphTransient()` switches on `codes.Unavailable/DeadlineExceeded/ResourceExhausted`, returns false for non-gRPC errors; retry loops at lines 150-175 (GetStalePubkeys), 181-206 (CountPubkeys), 227-252 (CountStalePubkeys), 289-313 (MarkAttempted best-effort); `break mainLoop` on fatal; runtime behaviour needs live-host confirmation |

**Score:** 5/5 truths structurally verified; 2 require live-host confirmation for runtime behaviour

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/dgraph/dgraph.go` | Paginated BackfillNextAttempt (HARD-01), inline-Discard MarkAttempted recovery (HARD-02), HARD-04 doc comment | VERIFIED | All three changes present and substantive |
| `pkg/crawler/crawler.go` | Bounded forwardEvent (HARD-03), BackfillNextAttempt caller with real cadence (HARD-01/IN-03) | VERIFIED | `forwardEvent` uses `context.WithTimeout`; `cadenceSec` computed from `cfg.MissBackoff.HitRefreshCadence` |
| `cmd/crawler/main.go` | `isDgraphTransient()`, retry loops at all three break sites + MarkAttempted | VERIFIED | Constants defined, function implemented, four retry blocks present with `ctx.Done()` honoured in every `select` |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/crawler/main.go` | `isDgraphTransient` | direct call in retry loops | WIRED | Called at 4 sites: GetStalePubkeys, CountPubkeys, CountStalePubkeys, MarkAttempted |
| `cmd/crawler/main.go` | `crawler.Config.MissBackoff` | `cfg.MissBackoff` passed at line 116 | WIRED | Threaded from `config.LoadConfig()` result through `crawler.Config.MissBackoff` |
| `crawler.Config.MissBackoff` | `dgClient.BackfillNextAttempt` | `cadenceSec` computed from `cfg.MissBackoff.HitRefreshCadence.Seconds()` | WIRED | `crawler.go:142-143` — no hardcoded `86400` literal remains |
| `forwardEvent` | `c.forwardRelay.conn.Publish` | `context.WithTimeout(ctx, c.timeout)` | WIRED | `crawler.go:244-246` |

---

## Data-Flow Trace (Level 4)

Not applicable — Phase 9 modifies error-handling paths and transaction hygiene, not data rendering components. No dynamic-data rendering artifact is involved.

---

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` compiles cleanly | `go build ./...` | exit 0, no output | PASS |
| `go vet` on changed packages | `go vet ./pkg/dgraph/ ./pkg/crawler/ ./cmd/crawler/` | exit 0, no output | PASS |
| `go test -short ./...` passes | `go clean -testcache && go test -short ./...` | `ok web-of-trust/pkg/config 0.893s`, `ok web-of-trust/pkg/crawler 1.228s`, `ok web-of-trust/pkg/dgraph 1.895s` | PASS |
| `forwardEvent` hang under live hung relay | live-host only | not run | SKIP (human needed) |
| RESIL-01 Dgraph bounce recovery | live-host only | not run | SKIP (human needed) |

---

## Probe Execution

No `scripts/*/tests/probe-*.sh` probes declared or found for this phase. Step 7c: SKIPPED (no conventional probes).

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| HARD-01 | 09-01-PLAN.md | BackfillNextAttempt pagination | SATISFIED | Paginated loop with `first: batchSize, offset: 0`, per-window CommitNow, inline Discard |
| HARD-02 | 09-01-PLAN.md | MarkAttempted recovery-txn hygiene | SATISFIED | Inline `txn.Discard(ctx)` at line 694; independence documented lines 678-688 |
| HARD-03 | 09-02-PLAN.md | Bounded forwardEvent publish | SATISFIED (static) | `context.WithTimeout(ctx, c.timeout)` at crawler.go:244; runtime confirmation pending |
| HARD-04 | 09-01-PLAN.md | Large-frontier sort-cap coverage | SATISFIED | Doc comment on GetStalePubkeys cites D-09 checkpoint and WR-05 as standing evidence |
| RESIL-01 | 09-02-PLAN.md | Transient Dgraph error retry in main loop | SATISFIED (static) | `isDgraphTransient` + 4 retry sites + `break mainLoop` on fatal; runtime confirmation pending |

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | — | — | No TBD/FIXME/XXX markers found; no `86400` literal remains; no placeholder returns |

Anti-pattern scan was clean: no debt markers, no stub implementations, no hardcoded fallback literals left over from the temporary 09-01 caller update.

---

## Human Verification Required

### 1. HARD-03 — Bounded forwardEvent: drain-loop survives hung forward relay

**Test:** Build the crawler on the strfry host (`cd web-of-trust && make build-crawler`). Run it against live Dgraph and relays for at least one full batch. Confirm a `Batch complete: ...` line appears and MarkAttempted ran (no stall).

**Expected:** The batch completes within the normal window (~15s relay timeout + processing time). The forward relay does not stall the drain loop. If the forward relay is slow or unreachable, the WARN is logged and the batch continues.

**Why human:** The code wires `context.WithTimeout(ctx, c.timeout)` correctly, but whether a hung forward relay actually times out and falls through (rather than blocking indefinitely at the OS/WebSocket layer) can only be confirmed by triggering the condition live.

---

### 2. RESIL-01 — Transient retry: crawler survives a Dgraph bounce

**Test:** While the crawler is processing batches on the strfry host, bounce the Dgraph alpha container: `docker compose -f docker-compose.dgraph.yml restart dgraph-alpha` (or equivalent). Watch the crawler logs.

**Expected:** The crawler logs `Transient Dgraph error getting stale pubkeys (attempt 1/5): ... code = Unavailable ...; retrying in 5s` and then resumes normal batch processing once Dgraph is back. The process does NOT exit.

**Why human:** `isDgraphTransient` inspects the gRPC status code. Whether the live Dgraph gRPC client actually surfaces the EOF as `codes.Unavailable` (as observed in the 08-02 run) must be confirmed by inducing the condition on the production stack, not just in static code analysis.

---

### 3. RESIL-01 — Fatal path: loud exit after retry budget exhausted

**Test:** Point the crawler at a bad Dgraph address (`dgraph_addr: localhost:9999`) or stop Dgraph entirely and leave it down past the 5-attempt budget (~45s total). Watch the crawler logs.

**Expected:** After 5 attempts the crawler logs `Dgraph unavailable after 5 attempts getting stale pubkeys, exiting: ...` and terminates cleanly. It does not hang.

**Why human:** The `break mainLoop` is statically present, but verifying the process actually terminates (not just breaks the inner retry loop) under persistent failure requires a live test.

---

## Gaps Summary

No static gaps found. All five success criteria are implemented correctly in the shipped code:

- **HARD-01**: `BackfillNextAttempt` has a proper pagination loop with `first: batchSize, offset: 0`, per-window `CommitNow: true` mutations, and inline `txn.Discard` for both the read and mutation transactions. The NOT-has filter shrinkage pattern is documented and correctly implemented.
- **HARD-02**: The in-place recovery transaction in `MarkAttempted` calls `txn.Discard(ctx)` immediately after `txn.Mutate` (line 694), inside the loop body. It is not deferred. The independence and retry-safety comment is present at lines 678-688. VALID-03 decision tree is intact.
- **HARD-03**: `forwardEvent` uses `context.WithTimeout(ctx, c.timeout)` correctly. The publish call and error bookkeeping are correctly scoped to `pubCtx`. Code structure is sound.
- **HARD-04**: The `GetStalePubkeys` doc comment (lines 509-519) explicitly cites `08-REVIEW.md WR-05`, describes the D-09 production verification (100k+ frontier nodes), and states the standing evidence clearly. The documentation path is complete.
- **RESIL-01**: `isDgraphTransient` correctly switches on `codes.Unavailable/DeadlineExceeded/ResourceExhausted` and returns false for non-gRPC errors (preserving loud-exit for misconfigurations). Retry loops are present at all four Dgraph call sites with `break mainLoop` on fatal, `ctx.Done()` honoured in every `select`, and proper backoff doubling up to `dgraphRetryMax=2m`.

The two items flagged as `human_needed` (HARD-03 runtime stall prevention, RESIL-01 transient retry under live bounce) are correctly categorised as runtime behaviour that cannot be asserted by static analysis alone. Per the verification context, these are not failures — they are deferred to the live-host checkpoint described in 09-02 Task 3.

---

_Verified: 2026-06-13T14:45:00Z_
_Verifier: Claude (gsd-verifier)_
