# Phase 10: Unbounded Retry & Backoff Hardening - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-15
**Phase:** 10-unbounded-retry-backoff-hardening
**Areas discussed:** Retry helper shape, Metrics cadence & format, Metric semantics, MarkAttempted policy

---

## Retry helper shape

| Option | Description | Selected |
|--------|-------------|----------|
| Generic helper + injectable sleep | Extract one `retryDgraph[T](ctx, callName, fn, *metrics, sleepFn) (T, error)`; classifies transient/fatal internally; sleepFn injected for instant backoff tests | ✓ |
| Closure helper, error-only | Non-generic `retryDgraph(ctx, callName, fn func() error, ...)`; results assigned via closure | |
| Inline edits per block | Update each of the four blocks in place; no isolated unit for TEST-01 | |

**User's choice:** Generic helper + injectable sleep
**Notes:** Go 1.24 generics; cleanest collapse of the four call sites; directly testable for the 1m→2m→4m→5m sequence.

---

## Metrics cadence & format

| Option | Description | Selected |
|--------|-------------|----------|
| Per-batch, after 'Batch complete' | One metrics line right after main.go:319 every batch (~38s); no ticker goroutine | ✓ |
| Every N batches | Emit only every N batches to reduce log volume | |
| Separate time-ticker | time.Ticker (e.g. every 5 min) with its own goroutine + ctx handling | |

**User's choice:** Per-batch, after 'Batch complete'
**Notes:** Reuses existing logging cadence; no extra goroutine.

---

## Metric semantics

| Option | Description | Selected |
|--------|-------------|----------|
| Cumulative since start, successful only | Running sum + count per call type over the whole run; excludes retried/failed attempts | ✓ |
| Rolling window (last N batches) | Sliding-window average reflecting current health; more state | |
| Include all attempts | Count retried/failed durations too; mixes outage latency in | |

**User's choice:** Cumulative since start, successful only
**Notes:** One sample per call per batch means cross-batch accumulation is required to form a real average; successful-only keeps it normal-op latency.

---

## MarkAttempted policy

| Option | Description | Selected |
|--------|-------------|----------|
| Indefinite transient retry, fatal warns+continues | Transient retries forever like reads; fatal logs WARN and continues (best-effort write preserved) | ✓ |
| Fully uniform with reads | Transient retries forever AND fatal breaks mainLoop | |
| Unchanged bounded best-effort | Leave the 5-attempt cap; violates RETRY-02 uniformity | |

**User's choice:** Indefinite transient retry, fatal warns+continues
**Notes:** Honors RETRY-02's "all four calls"; a persistent fatal will be caught by the next GetStalePubkeys read-path call and exit loudly per RETRY-03.

---

## Claude's Discretion

- Exact helper parameter ordering, struct field names, and log-line wording (must preserve the "retrying in <dur>" style for Success Criterion #2).
- Whether the metrics struct is a named type or inline.

## Deferred Ideas

- TUNE-01 — config-driven retry backoff (out of scope for v1.3; tracked in REQUIREMENTS.md Future Requirements).
- Dgraph gRPC client re-creation/reconnect on `Unavailable` (out of scope; client recovers on retry).
