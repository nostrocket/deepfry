# Project Retrospective

*A living document updated after each milestone. Lessons feed forward into future planning.*

## Milestone: v1.2 — Crawler Reliability & Efficiency

**Shipped:** 2026-06-15
**Phases:** 5 (05–09) | **Plans:** 11 | **Requirements:** 21/21

### What Was Built
- Pubkey validation hardening — replaced the misused `nostr.GetPublicKey` "validator" with a hex-regex validator, with inline recover-or-purge so the 19 garbage nodes self-clean (Phase 05).
- Filter-size + per-relay cap detection — `relay_filter_batch_size` default 100, NOTICE-based cap halving, connection-drop-on-REQ attribution, chunked sub-REQ loop (Phase 06).
- Relay health management — three-class failure counters with halving decay, probe-up filter-cap recovery, threshold auto-ejection, and one-line-per-state-change logging (Phase 07).
- Frontier prioritization + timeout + observability — follower-count-ordered frontier, geometric backoff for chronic-miss stubs, 15s timeout, EOSE-quorum early-exit, honest `staleRemaining` (Phase 08).
- Hardening & resilience follow-ups — paginated `BackfillNextAttempt`, `MarkAttempted` recovery-txn hygiene, bounded `forwardEvent` publish, documented large-frontier sort-cap guarantee, and transient-Dgraph-error retry in the main loop (Phase 09).

### What Worked
- The full GSD chain (context → pattern-map → plan → plan-check → execute → verify) caught the right things: the plan-checker confirmed dependency correctness pre-execution, and the verifier cleanly separated statically-verifiable from live-host-only behavior.
- Closing deferred code-review warnings as a dedicated follow-up phase (Phase 09) at the milestone-close gate — rather than carrying them as tech debt — kept the milestone honest while respecting the already-verified Phase 08.
- Live-host human-verify checkpoints (Phase 08 and 09) correctly gated runtime behavior that unit tests can't reach (Dgraph-bounce recovery, forward-publish stall).

### What Was Inefficient
- Worktree agents forked a **stale merge-base**, so the 09-02 merge spuriously conflicted even though the branch content was a clean superset — required a manual file-level integration. (Known environment quirk; see memory note.)
- Concurrent monorepo work (`spam/` module committing to `main` mid-run) meant every wave merge had to be checked for path disjointness before integrating.
- Documentation drift surfaced at close: a stale `FILTER-01` checkbox and a quick-task missing its `status: complete` both tripped the close audit and needed correcting.

### Patterns Established
- At a milestone-close audit, "resolve first" on a deferred-todo means opening a follow-up phase (sequential or decimal) with full discuss→plan→execute→verify — not a `--force` replan of a verified phase.
- For Dgraph mutations that can grow unbounded, mirror the `AddFollowers` `batchSize=200` chunking and re-query `offset:0` after each commit when the predicate filter shrinks the result set.
- Classify Dgraph gRPC errors via `google.golang.org/grpc/status`: `Unavailable`/`DeadlineExceeded`/`ResourceExhausted` are transient (retry with backoff); everything else exits loudly.

### Key Lessons
1. In a shared monorepo with concurrent sessions, treat `main` as moving: capture the base, confirm path-disjointness, and verify the merge result builds before tagging.
2. Trust the verifier's `human_needed` status — don't self-promote runtime behavior to `passed` without the live-host confirmation it flags.
3. Keep planning checkboxes/traceability honest during phases, not just at close — stale `[ ]` marks cost an audit cycle.

### Cost Observations
- Model mix: orchestration on Opus; subagents (pattern-mapper, planner spawned on Opus, plan-checker, executors, verifier) primarily on Sonnet.
- Notable: the stale-merge-base quirk added one manual integration cycle per multi-file wave — worth scripting a base-pin check if worktree execution continues.

---

## Milestone: v1.3 — Unbounded Dgraph Retry Resilience

**Shipped:** 2026-06-15
**Phases:** 1 (10) | **Plans:** 1 | **Requirements:** 8/8

### What Was Built
- Generic `retryDgraph[T]` helper replacing four near-identical bounded 5-attempt retry blocks in the crawler main loop: indefinite transient-error retry, 1m→2m→4m→5m capped backoff, `select`-based ctx-cancel-aware sleep (clean SIGINT/SIGTERM shutdown mid-backoff), and a `callMetrics` accumulator logging per-call-type cumulative-average duration each batch (Phase 10).
- Deterministic `package main` unit tests via an injected sleep function — backoff sequence, ctx-cancel interruption, fatal passthrough, transient-then-success — runnable under `make test` without a live Dgraph.

### What Worked
- Single coarse phase for all 8 tightly-coupled requirements (all touching the same main-loop retry code) kept plan count at 1 with no dependency loss — the granularity decision recorded at roadmap time held up.
- The auto code-review→fix loop earned its keep: it caught a genuine **bounded→indefinite regression** (WR-01: `ResourceExhausted` would livelock forever on the permanent ~4MB gRPC limit) that neither the executor nor the verifier flagged, plus a real flaky test (WR-04: wall-clock assertion truncating to 0ns).
- Injected-clock testing made the backoff/cancel behavior fully deterministic — 50/50 stable runs after the WR-04 fix.

### What Was Inefficient
- The initial implementation carried `ResourceExhausted` as transient straight from v1.2's RESIL-01 classification without re-examining it against the now-indefinite loop — a context-shift the plan should have flagged. Caught at review, not at plan time.
- A test asserted on `avg > 0` over a no-op function (wall-clock dependent) — a flakiness class that a "assert on count, not duration" rule would have prevented up front.

### Patterns Established
- When converting a *bounded* retry to an *unbounded* one, re-audit the transient/fatal error classification: an error that was tolerable to retry a fixed number of times may be a permanent livelock under indefinite retry. `ResourceExhausted` (message-size limit) is fatal, not transient.
- Interrupt-safe waits use `select { case <-sleepFn(delay): case <-ctx.Done(): return ctx.Err() }` with a loop-top `ctx.Err()` short-circuit — never a bare `time.Sleep`.
- Test recorded effects by count/identity, not wall-clock duration, to stay deterministic on fast machines.

### Key Lessons
1. The cheapest place to catch a regression introduced by a behavior-broadening refactor (bounded → indefinite) is an adversarial code-review pass, not verification — verification confirms the goal, review questions the side effects.
2. A no-op function under test takes 0ns; assert on what was recorded (count), not how long it took (duration).

### Cost Observations
- Model mix: orchestration on Opus; executor / reviewer / fixer / verifier / integration-checker subagents on Sonnet.
- Sessions: 1 autonomous run (discuss+plan pre-existing; execute → review → fix×2 → re-review → audit → complete).
- Notable: 2 fix iterations sufficed to converge clean; the 3-iteration cap was not reached.

---

## Milestone: v1.4 — Crawler Hang Fix (Relay-Query Liveness)

**Shipped:** 2026-06-16
**Phases:** 1 (11) | **Plans:** 1 | **Requirements:** 4/4

### What Was Built
- `FetchAndUpdateFollows` dispatcher now exits independently on its relay-query timeout — non-blocking drain of buffered events, no `wg.Wait()`/`eventsChan`-close gating (HANG-01). `queryRelay` bounds `relay.Subscribe` in a child goroutine + ctx-select so it returns on timeout even though go-nostr v0.52's `Fire()` ignores the per-call context (HANG-02). Outstanding relays are closed + marked-dead on the timeout path and closed-without-penalty on the quorum-cancel path, reaping the leaked Subscribe child + cleanup goroutine (HANG-03).
- A per-batch generation token (`batchSeq`/`completedGen`) replaced a reset-to-false marker boolean, removing a cross-batch race.
- Four `-race` unit tests via the `queryRelayFn` seam: regression (returns under a stuck relay), partial-progress, close-on-timeout, and quorum-exit close.

### What Worked
- Writing the RED regression test *first* (during the diagnosis session, before the milestone) gave the whole milestone an unambiguous acceptance gate — the fix was "make this test green" and verification was mechanical.
- The SIGQUIT goroutine dump turned a vague "process stalls" into an exact root cause (go-nostr `Fire()` parked on a context-less channel receive; dispatcher gated on `wg.Wait()`), which made the fix scope tight and the plan single-phase.
- The adversarial code-review loop paid for itself heavily: the first implementation passed verification AND `-race`, yet review found **two critical concurrency defects invisible to the test suite** — a slice-mutation-during-range that re-skipped relays (partially reintroducing the very leak), and a quorum-path subscription leak. Both fixed before close.

### What Was Inefficient
- The first cut fixed the timeout path but missed that the EOSE-quorum exit path also needed to close stuck relays — found in review iteration 3, not at plan time. The plan's HANG-03 framing said "on timeout" and the quorum path was an unconsidered second exit.
- The original `completedThisBatch` boolean shipped with a latent cross-batch race that only surfaced in the third review pass; a generation token would have been the right primitive from the start for a per-batch marker.

### Patterns Established
- A regression test that reproduces a concurrency hang via an injected, context-ignoring work function (the `queryRelayFn` seam) is far more reliable than trying to reproduce the real network condition (half-open TCP). Inject the *behavior* (blocks, ignores ctx), not the environment.
- When a fix has two exit paths (timeout vs early-cancel), audit cleanup/teardown on BOTH — a leak plugged on one path leaks on the other.
- Per-batch state that a late goroutine might stamp: use a monotonic generation token, never a reset-to-false boolean (the reset is the race).
- `go test -race` confirms the goal but does not exercise timing-dependent cleanup branches whose triggers the test never reaches — adversarial review is the catch for those.

### Key Lessons
1. A passing race-tested suite is necessary, not sufficient, for concurrency correctness: the defects that survive tests are exactly the ones whose triggering interleaving the tests don't reach. Budget an adversarial review pass for any concurrency fix.
2. Diagnose-then-fix across two sessions worked well: the goroutine-dump diagnosis + RED regression test produced a crisp, single-phase milestone with a built-in acceptance gate.
3. When the upstream library is the root cause (go-nostr ignoring ctx) but forking is out of scope, satisfy the requirement's intent ("or equivalent") at your own layer — here, connection-close to cancel the library's own context.

### Cost Observations
- Model mix: orchestration on Opus; executor / reviewer / fixer / verifier subagents on Sonnet; planner on Opus.
- Sessions: 1 diagnosis session (goroutine dump + RED test + findings doc) + 1 autonomous run (discuss → plan → execute → review → fix×2 → re-review×2 → audit → debt-closure → complete).
- Notable: the review→fix loop ran its full 3 iterations here (vs 2 in v1.3) because each fix surfaced a deeper latent issue; the user elected to close the 2 residual tech-debt items before completing rather than defer.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Phases | Key Change |
|-----------|--------|------------|
| v1.1 | 1–4 | Write-path correctness + regression coverage established |
| v1.2 | 5–9 | Operational reliability; first use of live-host human-verify checkpoints and a milestone-close follow-up phase |
| v1.3 | 10 | Single coarse phase; auto code-review→fix loop caught a refactor regression + flaky test the verifier missed |
| v1.4 | 11 | Diagnose-then-fix across two sessions; RED regression test written before the milestone as the acceptance gate; review→fix loop ran full 3 iterations, each surfacing a deeper concurrency defect |

### Cumulative Quality

| Milestone | Tests | Notes |
|-----------|-------|-------|
| v1.1 | unit + integration (chunk/version-guard) | write-path covered |
| v1.2 | unit + `//go:build integration` (validator, filter-cap, frontier order, recover/purge) | runtime behavior live-verified; broad non-write-path coverage (TEST-05) still open |
| v1.3 | unit (`package main`, injected-clock retry/backoff/cancel) | retry helper deterministically covered without a live Dgraph; first `cmd/crawler` unit-test file |
| v1.4 | unit `-race` (4 dispatcher-liveness tests via `queryRelayFn` seam) | concurrency hang reproduced by injecting context-ignoring behavior, not the network condition; `pkg/crawler` coverage 25→27% |

### Top Lessons (Verified Across Milestones)

1. Live Dgraph + relay behavior is only provable on the strfry host — bake the manual verification step into every phase that touches the event loop.
2. Coarse phase granularity tied to real coupling clusters keeps plan counts minimal without losing dependency correctness.
3. The auto code-review→fix loop repeatedly catches defects (regressions, flaky tests, concurrency hazards) that pass both the executor and the goal-backward verifier — it is the highest-leverage gate for refactors and concurrency work.
