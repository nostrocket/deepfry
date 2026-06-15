# Phase 10: Unbounded Retry & Backoff Hardening - Context

**Gathered:** 2026-06-15
**Status:** Ready for planning

<domain>
## Phase Boundary

Replace v1.2's bounded 5-attempt Dgraph retry (RESIL-01) with indefinite transient-error retry across all four main-loop Dgraph calls in `cmd/crawler/main.go` (`GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, `MarkAttempted`). Backoff changes from 5s→2min to 1min→5min (doubling). Context cancellation (SIGINT/SIGTERM) must still interrupt an in-progress wait immediately; non-transient (fatal) errors on the read path still exit loudly. Adds per-call-type average-duration observability and a unit-tested retry/backoff helper.

**Scope anchor:** `cmd/crawler/main.go` only. The `isDgraphTransient` classifier (lines 37-51) already exists and stays. No new config, no Dgraph client re-creation, no relay-path changes.

</domain>

<decisions>
## Implementation Decisions

### Retry helper structure (RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, TEST-01)
- **D-01:** Extract the four near-identical retry blocks (`main.go:147-314`) into a single generic helper, signature shaped like `retryDgraph[T any](ctx, callName string, fn func() (T, error), metrics *..., sleepFn func(time.Duration) <-chan time.Time) (T, error)`. All four call sites collapse to one line each.
- **D-02:** The helper classifies transient vs fatal internally via the existing `isDgraphTransient`. On transient: retry indefinitely (no attempt cap). On fatal or ctx-cancel: return so the caller can decide (read calls `break mainLoop`; MarkAttempted warns + continues — see D-09).
- **D-03:** Sleep is injectable. Production passes `time.After`; unit tests inject a fake clock/sleep so the 1m→2m→4m→5m(cap) sequence is verified instantly without real waits. The wait must be a `select` over the sleep channel and `ctx.Done()` so SHUTDOWN-01 holds mid-backoff.
- **D-04:** Backoff constants become: initial = 1 min, cap = 5 min, doubling. The `dgraphRetryAttempts` constant (currently 5) is removed — retry is indefinite for transient errors.

### Observability (OBS-01)
- **D-05:** Emit one average-call-duration line per batch, immediately after the existing `Batch complete:` log (`main.go:319`). No separate ticker goroutine.
- **D-06:** Average is **cumulative since process start**, per call type (`GetStalePubkeys`, `CountPubkeys`, `CountStalePubkeys`, `MarkAttempted`). Running sum + count → avg = sum/count. (One sample per call per batch means cross-batch accumulation is required to have a meaningful average.)
- **D-07:** Measure **successful calls only** — the duration of the call that returned `nil`. Retried/failed attempt durations during an outage are excluded so the metric reflects normal-op latency, not outage stalls.
- **D-08:** Duration accumulation lives in a small metrics struct threaded into the helper (so timing happens once, inside `retryDgraph`, keyed by `callName`).

### MarkAttempted error policy (RETRY-02)
- **D-09:** MarkAttempted retries **transient** errors indefinitely like the read calls (honors RETRY-02 uniformity). On a **fatal/non-transient** error it logs a WARN and continues to the next batch (preserves v1.2 best-effort write semantics) — it does NOT `break mainLoop`. A persistent fatal condition will be caught by the next iteration's `GetStalePubkeys` read-path call, which exits loudly per RETRY-03.

### Claude's Discretion
- Exact helper parameter ordering, struct field names, and log-line wording (formatting only — must keep the existing "Transient Dgraph error … retrying in <dur>" style so the 1m/2m/4m/5m sequence is observable per Success Criterion #2).
- Whether the metrics struct is a named type in `main` or inline — planner/executor call.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Requirements & roadmap
- `.planning/REQUIREMENTS.md` — v1.3 requirements RETRY-01/02/03, BACKOFF-01/02, SHUTDOWN-01, OBS-01, TEST-01; the exact backoff sequence and out-of-scope items (TUNE-01 config tunability, Dgraph client re-creation, relay retries).
- `.planning/ROADMAP.md` §"Phase 10" — goal + five Success Criteria (the observable acceptance bar).

### Code under change
- `cmd/crawler/main.go` — the only file in scope. Lines 23-29 (retry constants), 31-51 (`isDgraphTransient`, keep as-is), 147-314 (four retry blocks to collapse), 319 (Batch complete log to extend with the metrics line).

### Project context
- `.planning/PROJECT.md` §"Key Decisions" — the two v1.3 rationale rows (retry forever; 1min→5min backoff) that justify the locked values.

No external ADRs/specs beyond the above — requirements are fully captured in REQUIREMENTS.md and the decisions here.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `isDgraphTransient(err error) bool` (`main.go:37-51`) — already classifies `codes.Unavailable / DeadlineExceeded / ResourceExhausted` as transient, everything else fatal. The new helper reuses it verbatim.
- The existing per-block `select { case <-time.After(retryDelay): case <-ctx.Done(): }` pattern (`main.go:162-166`) is the correct shutdown-safe wait; the helper generalizes it.

### Established Patterns
- Relay backoff constants in `pkg/crawler/crawler.go` (`initialBackoff`, `maxBackoff=5m`) — the new Dgraph cap aligns at 5m. Constants stay as package-level consts in `cmd/crawler` (TUNE-01 config-driven is out of scope).
- Logging style is `log.Printf` info/WARN lines; metrics line should match (single line, human-readable).

### Integration Points
- Four call sites in `mainLoop` (`main.go:147-314`) become single `retryDgraph(...)` calls; `break mainLoop` stays at the call site for the three read calls (helper returns fatal/cancel as an error the caller checks).
- New metrics line inserted at `main.go:319` after `Batch complete:`.

</code_context>

<specifics>
## Specific Ideas

- Success Criterion #2 requires the retry log lines to literally show waits of "1 min, 2 min, 4 min, then 5 min (capped)" — keep the `retrying in %v` log so the sequence is visible in the console.
- Success Criterion #4 (Ctrl-C mid-backoff exits within seconds) is the SHUTDOWN-01 acceptance test — the injected-sleep helper must select on `ctx.Done()`.

</specifics>

<deferred>
## Deferred Ideas

- **TUNE-01** — make retry backoff (initial/cap) config-driven via `web-of-trust.yaml`. Explicitly out of scope for v1.3 (fixed values per operator request); tracked in REQUIREMENTS.md "Future Requirements".
- Dgraph gRPC client re-creation/reconnect on `Unavailable` — out of scope; existing client recovers on retry.

</deferred>

---

*Phase: 10-unbounded-retry-backoff-hardening*
*Context gathered: 2026-06-15*
