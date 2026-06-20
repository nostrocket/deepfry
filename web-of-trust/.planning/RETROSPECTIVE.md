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

## Milestone: v1.5 — Dgraph Follow-Update Timeout Resilience

**Shipped:** 2026-06-18
**Phases:** 1 (12) | **Plans:** 1 | **Requirements:** 6/6

### What Was Built
- Dgraph follow updates now fail transiently per pubkey instead of aborting the whole crawler batch. `FetchAndUpdateFollows` returns `FetchResult{Hits, SkipAttempt}`; main excludes `SkipAttempt` from `MarkAttempted`, preserving retry eligibility for transient write failures.
- `AddFollowers` keeps one all-or-nothing transaction for kind-3 replacement semantics while bounding each internal Dgraph query/mutation/commit window and logging progress diagnostics.
- `dgraph.IsTransientError` became the shared classifier for Dgraph/gRPC codes: `DeadlineExceeded` and `Unavailable` are transient, `ResourceExhausted` remains fatal.
- Deterministic short tests cover timeout classification, progress accounting, transient skip-and-continue, fatal passthrough, and attempt filtering.

### What Worked
- The phase stayed tightly scoped to the production abort condition instead of expanding into broader crawl throughput tuning.
- The existing v1.3/v1.4 decisions transferred cleanly: fatal `ResourceExhausted` classification from v1.3 and bounded dispatcher behavior from v1.4 framed the fix.
- Fake Dgraph writer tests plus signed go-nostr kind-3 events proved the crawler behavior without requiring live relay or Dgraph dependencies.

### What Was Inefficient
- The local `~/.codex/gsd-core` CLI copy was missing package metadata; the cached npm package copy was needed for GSD helper queries.
- The integration-tag package initially failed to build because older tests still used a stale `BackfillNextAttempt(ctx)` signature. Fixing that was a small but necessary close-out deviation.
- A separate milestone audit artifact was not present for v1.5; the milestone close relied on the phase verification and clean review artifacts.

### Patterns Established
- For write-path failures that should retry later but not stall a batch, return a structured result with a skip set rather than retrying inline.
- Keep replaceable-event writes atomic even when internal work is chunked or windowed; bounded child contexts are compatible with one final transaction.
- Use signed event fixtures in crawler tests so signature validation remains part of the tested path.

### Key Lessons
1. A transient write failure is not the same operational class as a transient read/bookkeeping outage: the former should often be scheduled by omission from attempt stamping, not hidden behind more in-batch retry.
2. If a plan requires an integration-tag verification command, keep unrelated integration tests compiling against current APIs; stale guarded tests can block targeted verification before the target test runs.
3. Milestone close should archive phase directories promptly so the active `.planning/phases/` tree stays reserved for current work.

### Cost Observations
- Model mix: orchestration and inline execution in Codex; no parallel agents used because this runtime did not expose the Claude-style Agent API for execute-phase.
- Sessions: 1 autonomous run for execute → review → verify → complete.
- Notable: single-plan coarse milestone was enough; no closure phase was needed after verification.

---

## Milestone: v1.6 — Crawl Throughput Optimization

**Shipped:** 2026-06-20
**Phases:** 2 (13–14) | **Plans:** 2 | **Requirements:** 16/16

### What Was Built
- Phase 13 decoupled the Dgraph frontier-selection batch size from the relay filter cap (`frontier_batch_size`) and throttled the per-batch `CountPubkeys`/`CountStalePubkeys` queries (`count_sample_interval`), while keeping exact batch accounting and proving the Phase 6 relay-filter safety model intact.
- Phase 14 eliminated the dominant read-path cost: `GetStalePubkeys` recomputing `count(~follows)` over the entire ~1.3M-node frontier on every call. A stored, int-indexed `follower_count` predicate plus an `uncrawled` frontier marker let both selection blocks enter via an index (frontier `eq(uncrawled,1)`, aged `ge(follower_count,0)`) ordered by the stored value.
- `follower_count` is maintained cheaply (±1 delta) inside `AddFollowers`' existing transaction; a new idempotent uid-cursor backfill CLI seeded the full 1.38M-node graph (~2.5 min, exact accuracy).
- **Live-verified on the production Dgraph:** `GetStalePubkeys` ~119s → ~1.3s (frontier 69s→0.01s; aged 50s→1.3s).

### What Worked
- Measurement-first scoping paid off: the 2026-06-20 batch-metrics analysis localized the overhead to the frontier sort (~39s/batch) and showed `MarkAttempted` ≈ 0.07s, which let Phase 14 be **redefined** mid-milestone from a write-path decision (DWRITE) to the read-path `follower_count` fix (DSCALE) — and closed the write-path investigation as not-dominant rather than speculatively optimizing it.
- Live verification on the actual production graph (not a synthetic fixture) gave a real before/after number and caught that an early read-path query wasn't actually index-driven.
- Maintaining the counter inside the existing `AddFollowers` transaction kept the predicate correct without a second write path.

### What Was Inefficient
- The first live-verify pass found gaps (read query not index-driven; backfill too slow) and required a second fix cycle — the index-entry semantics of DQL (`eq(uncrawled,1)` / `ge(follower_count,0)`) weren't fully pinned down at plan time.
- No `v1.6-MILESTONE-AUDIT.md` was produced; close relied on phase verification + the clean `audit-open` query.

### Patterns Established
- When sorting on an aggregate over a virtual reverse edge (`count(~follows)`) dominates a hot read path, materialize it as a stored indexed scalar maintained by deltas at write time, and add a boolean/marker predicate so selection queries can *enter* through an index instead of scanning to compute the sort key.
- Use a uid-cursor (lexical UID floor) for full-graph backfills so the operation is idempotent and resumable on a multi-million-node graph.
- Let production batch metrics decide phase scope: a planned phase can be legitimately redefined or closed when measurement contradicts the premise.

### Key Lessons
1. "Order by `count(~follows)`" silently means "recompute the aggregate over the whole frontier every call" in DQL — there's no index entry through a virtual aggregate, so the sort key must be materialized to be cheap.
2. Live-verify on the real graph, not a fixture: the production 1.38M-node scale is what exposed both the index-entry gap and the backfill-speed problem.
3. A redeploy is not a planning artifact — the optimization is verified on Dgraph but won't help production until the binary ships; track that explicitly as a deferred operational item at close.

### Cost Observations
- Sessions: execute → review → live-verify (2 fix cycles) → complete.
- Notable: ~28 commits / 27 files (+3954/-224); two-phase coarse milestone, no closure phase needed.

---

## Cross-Milestone Trends

### Process Evolution

| Milestone | Phases | Key Change |
|-----------|--------|------------|
| v1.1 | 1–4 | Write-path correctness + regression coverage established |
| v1.2 | 5–9 | Operational reliability; first use of live-host human-verify checkpoints and a milestone-close follow-up phase |
| v1.3 | 10 | Single coarse phase; auto code-review→fix loop caught a refactor regression + flaky test the verifier missed |
| v1.4 | 11 | Diagnose-then-fix across two sessions; RED regression test written before the milestone as the acceptance gate; review→fix loop ran full 3 iterations, each surfacing a deeper concurrency defect |
| v1.5 | 12 | Production write-path abort fixed as a per-pubkey retry scheduling problem; single coarse phase with no closure gaps |
| v1.6 | 13–14 | Measurement-driven scoping; a planned phase (14) was redefined mid-milestone from write-path to read-path after batch metrics contradicted the premise; live-verified on the real 1.38M-node graph |

### Cumulative Quality

| Milestone | Tests | Notes |
|-----------|-------|-------|
| v1.1 | unit + integration (chunk/version-guard) | write-path covered |
| v1.2 | unit + `//go:build integration` (validator, filter-cap, frontier order, recover/purge) | runtime behavior live-verified; broad non-write-path coverage (TEST-05) still open |
| v1.3 | unit (`package main`, injected-clock retry/backoff/cancel) | retry helper deterministically covered without a live Dgraph; first `cmd/crawler` unit-test file |
| v1.4 | unit `-race` (4 dispatcher-liveness tests via `queryRelayFn` seam) | concurrency hang reproduced by injecting context-ignoring behavior, not the network condition; `pkg/crawler` coverage 25→27% |
| v1.5 | unit + guarded integration-tag command | Dgraph write classification/progress and crawler retry scheduling covered without live services; integration-tag package build kept current |
| v1.6 | unit (config/loop-accounting/`follower_count` delta) + integration-tag (ordering/backfill) + live production verification | read-path latency proven on the 1.38M-node production Dgraph (~119s → ~1.3s) with an exact-accuracy spot-check; live-verify caught a non-index-driven query the unit/integration tests passed |

### Top Lessons (Verified Across Milestones)

1. Live Dgraph + relay behavior is only provable on the strfry host — bake the manual verification step into every phase that touches the event loop.
2. Coarse phase granularity tied to real coupling clusters keeps plan counts minimal without losing dependency correctness.
3. The auto code-review→fix loop repeatedly catches defects (regressions, flaky tests, concurrency hazards) that pass both the executor and the goal-backward verifier — it is the highest-leverage gate for refactors and concurrency work.
4. For crawler batch liveness, preserving retry eligibility can be cleaner than retrying inline: omit transient-failed pubkeys from attempt stamping and let frontier selection retry later.
5. Let production measurement, not the original plan, decide phase scope: v1.6's Phase 14 was redefined (write-path → read-path) and a sibling investigation closed once batch metrics localized the real cost — and "order by an aggregate over a virtual edge" must be materialized to an indexed scalar to be cheap.
