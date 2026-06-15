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

## Cross-Milestone Trends

### Process Evolution

| Milestone | Phases | Key Change |
|-----------|--------|------------|
| v1.1 | 1–4 | Write-path correctness + regression coverage established |
| v1.2 | 5–9 | Operational reliability; first use of live-host human-verify checkpoints and a milestone-close follow-up phase |

### Cumulative Quality

| Milestone | Tests | Notes |
|-----------|-------|-------|
| v1.1 | unit + integration (chunk/version-guard) | write-path covered |
| v1.2 | unit + `//go:build integration` (validator, filter-cap, frontier order, recover/purge) | runtime behavior live-verified; broad non-write-path coverage (TEST-05) still open |

### Top Lessons (Verified Across Milestones)

1. Live Dgraph + relay behavior is only provable on the strfry host — bake the manual verification step into every phase that touches the event loop.
2. Coarse phase granularity tied to real coupling clusters keeps plan counts minimal without losing dependency correctness.
