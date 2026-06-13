# Phase 8: Frontier Prioritization, Timeout & Observability - Context

**Gathered:** 2026-06-13
**Status:** Ready for planning

<domain>
## Phase Boundary

Make the crawler spend batch capacity on pubkeys most likely to return kind-3 events, exit batches early once enough relays have responded, and report accurate progress. Concretely: order the stale frontier by incoming follower count, back off chronic-miss stubs exponentially (without ever abandoning them), cut the per-batch relay timeout to 15s with an EOSE-quorum early exit, and fix the always-zero `staleRemaining` metric.

**The problem this solves:** the 40-batch production run had a **0.76% event hit rate** — ~99% of every batch was spent querying pubkeys that return no kind-3 event. PERF-01 front-loads likely-productive pubkeys; PERF-02 deprioritises chronic misses; TIMEOUT-01/02 stop waiting on slow relays; METRIC-01 makes the progress readout honest so the above is observable.

Requirements: PERF-01, PERF-02, TIMEOUT-01, TIMEOUT-02, METRIC-01.

**Out of scope (unchanged):** relay health/ejection, classification, filter-cap persistence, log collapse (all Phase 7, shipped); relay re-discovery (DISC-01, future); per-relay metrics endpoint (OBS-01, future); persistence of in-memory relay state across restarts; anything touching StrFry, event payloads, or breaking the `Profile` schema (additive predicates only).

</domain>

<decisions>
## Implementation Decisions

### PERF-02: Exponential backoff for chronic-miss stubs
- **D-01:** Add **two new indexed predicates** to the `Profile` schema (additive, schema-compatible): `next_attempt: int @index(int)` (the do-not-query-before timestamp) and `miss_count: int` (consecutive-miss counter used to compute the exponent). Chosen over the "advance `last_attempt` forward" trick because the base interval (2h) is *below* the existing 24h `StalePubkeyThreshold` — bolting a 2h interval onto a 24h floor would require stamping `last_attempt` into the past, making the field lie about when we actually tried.
- **D-02:** The aged-selection phase of `GetStalePubkeys` filters on **`lt(next_attempt, now)`** instead of `lt(last_attempt, olderThanUnix)`. The flat 24h `StalePubkeyThreshold` is repurposed as the **hit-refresh cadence** (below), not the universal floor.
- **D-03:** **HIT** (pubkey returned a kind-3 event this batch): set `next_attempt = now + 24h` (StalePubkeyThreshold — normal refresh to catch updated contact lists) and reset `miss_count = 0`.
- **D-04:** **MISS** (queried, no event): set `next_attempt = now + min(2h × 2^miss_count, 7d)` and increment `miss_count`. Backoff starts immediately on the first miss (first miss → ~2h retry). Schedule is **geometric ×2 from a 2h base** (2h, 4h, 8h, 16h, 32h, 64h, 128h, then capped at the **7-day** ceiling) so chronic-miss stubs are queried ever less often but **never permanently abandoned** (criterion 2 / core-value: keep expanding the WoT).
- **D-05:** `FetchAndUpdateFollows` already tracks `pubkeysWithEvents` (hits). The hit/miss split must flow into the stamp path — `MarkAttempted` (or a hit/miss-aware sibling) needs per-pubkey hit/miss to apply D-03 vs D-04. Keep the existing VALID-03 recover-or-purge logic in `MarkAttempted` intact.
- **D-06:** **One-time backfill** for existing attempted nodes (those with `last_attempt` but no `next_attempt`): set `next_attempt = last_attempt + 24h`, `miss_count = 0`. Never-attempted frontier nodes get neither predicate and are still selected by the frontier phase.
- **D-07:** Base interval (2h), ratio (×2), cap (7d), and the hit-refresh cadence should be **config-driven** with these values as defaults.

### PERF-01: Frontier ordering by follower count
- **D-08:** Order **both** selection phases — the never-attempted `frontier` and the aged top-up — by `count(~follows) DESC` (`orderdesc`), so high-follower stubs are front-loaded regardless of which phase fills the batch.
- **D-09:** Use `orderdesc` with an **explicit `first: N`**. `count(~follows)` is a value-bearing sort key (unlike `last_attempt`'s missing-value problem the docstring warns about), so explicit `first:N` should return the true top-N. **⚠ Verification item (researcher/planner MUST confirm against live Dgraph):** that Dgraph's 1000-row cap on unbounded *sorted* queries does not truncate the candidate set *before* the sort is applied. If it does, fall back to D-10.
- **D-10:** **Fallback if D-09 fails verification:** maintain a stored indexed `follower_count` predicate (updated on follow-edge writes) and sort on that instead of computing `count(~follows)` at query time. Not the default — adds write-path bookkeeping — but the safety net.

### TIMEOUT-01 / TIMEOUT-02: Timeout reduction + EOSE-quorum early exit
- **D-11:** Lower the existing `timeout` config default from **30s → 15s** (`pkg/config/config.go` `SetDefault("timeout", "15s")`). Note: live `~/deepfry/web-of-trust.yaml` may already have an explicit 30s — the default change only affects fresh/unset configs.
- **D-12:** Add a **`relay_eose_quorum` config key** (default **0.70**). Both the timeout and the quorum are runtime-tunable (consistent with Phase 7's config-driven thresholds).
- **D-13:** Quorum signal = a **shared atomic counter** (`atomic.Int32`, the established Phase 6/7 pattern). Each relay goroutine increments it once when it either returns on **EOSE** or **errors**. When `done >= ceil(quorum × queried)`, the batch calls `cancel()` on the relay query context. Events already received continue to drain (the existing post-cancel drain path handles this).
- **D-14:** Denominator = **relays actually queried** this batch (the alive relays the goroutine loop launched). A relay dying mid-batch counts toward `done`, moving the batch toward quorum rather than stalling it.

### METRIC-01: staleRemaining fix
- **D-15:** The bug: `staleRemaining := totalStale - len(pubkeys)` where `totalStale = len(pubkeys)` → always 0 (`cmd/crawler/main.go:162`). Replace with a real `CountStalePubkeys(ctx)` query result minus the current batch size.
- **D-16:** `CountStalePubkeys` counts **frontier + aged**, matching `GetStalePubkeys` selection semantics exactly: `count(NOT has(last_attempt))` (never-attempted) **plus** `count(has(next_attempt) AND lt(next_attempt, now))` (aged-eligible under the new PERF-02 model). So `staleRemaining` honestly means "crawlable pubkeys still awaiting coverage."
- **D-17:** Run the count **every batch** (one indexed read-only `count()` query per ~batch is negligible). Verify the cost is trivial on the production graph during planning; cache/periodic-refresh only if it proves measurably expensive.

### Claude's Discretion
- Exact config key names and YAML structure for the PERF-02 backoff params (D-07) and quorum (D-12) — follow existing `mapstructure`/`SetDefault` conventions.
- Whether the hit/miss stamp split (D-05) extends `MarkAttempted`'s signature, adds a sibling function, or passes a hit-set map.
- Exact DQL shape of the ordered frontier/aged queries (D-08) and the `CountStalePubkeys` query (D-16).
- How the atomic quorum counter and `cancel()` wiring sit inside the existing `FetchAndUpdateFollows` select loop (D-13) — keep changes localized so they don't disturb shipped Phase 7 state-machine code.
- Test layout (unit helpers for backoff-interval math; integration for query ordering / count / backfill against live Dgraph).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope
- `.planning/ROADMAP.md` § "Phase 8: Frontier Prioritization, Timeout & Observability" — goal + 5 success criteria
- `.planning/REQUIREMENTS.md` § PERF-01, PERF-02, TIMEOUT-01, TIMEOUT-02, METRIC-01 — formal requirement text
- `.planning/PROJECT.md` § "Current Milestone v1.2" + "Context" — the 40-batch production-run analysis (0.76% hit rate, 172 relays, 38s avg batch) that motivates this phase

### Code under change
- `pkg/dgraph/dgraph.go:490-535` — `GetStalePubkeys` (frontier + aged phases): add `count(~follows) DESC` ordering (D-08/D-09) and switch the aged filter to `lt(next_attempt, now)` (D-02)
- `pkg/dgraph/dgraph.go:537-563` — `collectStale` helper (shared query runner; may need ordering-aware reuse)
- `pkg/dgraph/dgraph.go:565-...` — `MarkAttempted`: hit/miss-aware `next_attempt`/`miss_count` stamping (D-03/D-04/D-05); preserve VALID-03 recover-or-purge
- `pkg/dgraph/dgraph.go:60-72` — schema string: add `next_attempt: int @index(int) .` and `miss_count: int .` predicates (D-01); add to the `EnsureSchema`/predicate list
- `cmd/crawler/main.go:111` — `GetStalePubkeys` call site (batch sizing via `RelayFilterBatchSize`)
- `cmd/crawler/main.go:142,154-160` — `FetchAndUpdateFollows` returns `hadEvents`; hit/miss set must reach the stamp path (D-05)
- `cmd/crawler/main.go:162-164` — the broken `staleRemaining` line (D-15); wire in `CountStalePubkeys` (D-16/D-17)
- `pkg/crawler/crawler.go:418-560` — `FetchAndUpdateFollows` relay fan-out + event-processing select loop: 15s timeout context (D-11), atomic quorum counter + early `cancel()` (D-13/D-14)
- `pkg/crawler/crawler.go:568-600` — `drainSubscription` (EOSE detection per relay) — the EOSE-return signal for the quorum counter
- `pkg/config/config.go:29-31,74,77` — `Config` struct + `SetDefault`: `timeout` 30s→15s (D-11), new `relay_eose_quorum` (D-12) and PERF-02 backoff keys (D-07)

### Prior phase decisions affected
- `.planning/phases/05-pubkey-validation-hardening/05-CONTEXT.md` — D-03/D-04: VALID-03 recover-or-purge lives in `MarkAttempted`; PERF-02's hit/miss stamping must coexist with it (do not regress the validation gate)
- `.planning/phases/07-relay-health-management/07-CONTEXT.md` — Phase 7 rewrote the `FetchAndUpdateFollows` / relay state machine and per-class counters; TIMEOUT-01/02 touch the same event loop — keep changes localized to avoid colliding with shipped logic. Note Phase 7's explicit "Phase 8 will touch the same FetchAndUpdateFollows timeout/EOSE logic — keep classification changes localized."

### Conventions / constraints
- `pkg/dgraph/dgraph.go:35` (and `GetStalePubkeys` docstring `:490-499`) — the documented Dgraph hazard: unbounded *sorted* queries cap at 1000 rows; missing-value nodes sort last. D-09's verification item is specifically about whether this truncates the follower-count sort.
- `.planning/codebase/CONCERNS.md` — relay state in-memory/restart caveats; config mutation via global viper singleton
- `.planning/codebase/TESTING.md` — build-tag gating (`//go:build integration`), `make test` vs `make test-integration`, `go test -race` precedent (Phase 6 CR-01); atomic-field race-testing convention
- `web-of-trust/CLAUDE.md` + root `CLAUDE.md` — ID-only graph / data separation, `Profile` schema compatibility (predicates are additive), temp-`HOME` for config tests (never edit live `~/deepfry/web-of-trust.yaml`), StrFry unmodified

### Test references
- `pkg/dgraph/dgraph_stale_test.go` — frontier/aged selection test conventions (`mustMutate`, `countFrontier`, timestamp-based fixture pubkeys); the natural home for PERF-01 ordering, PERF-02 backoff/backfill, and METRIC-01 count integration tests

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `relayState` `atomic.Int32` fields (`filterCap`, per-class fail counters from Phases 6/7) — the established lock-free pattern; reuse for the D-13 quorum `done` counter.
- `FetchAndUpdateFollows` already builds `pubkeysWithEvents map[string]struct{}` (hits) — the hit/miss split for D-05 is derivable here without new tracking.
- `GetStalePubkeys` two-phase structure (frontier `NOT has(last_attempt)` + aged) and the `collectStale` shared runner — ordering and the `next_attempt` filter slot directly into the existing query blocks.
- `MarkAttempted`'s existing UID-based nquad mutation pattern (`<uid> <last_attempt> "ts" .`) — same shape extends to `<uid> <next_attempt> "..." .` and `<uid> <miss_count> "..." .`.
- `timeout` is already a `Config` field threaded through to `c.timeout` and used at `crawler.go:424` — TIMEOUT-01 is a default change, not new plumbing.

### Established Patterns
- `context.WithTimeout(relayContext, c.timeout)` + `defer cancel()` at `crawler.go:424` — the cancel handle the quorum early-exit (D-13) reuses; the post-cancel drain already handles "continue processing received events."
- `if c.debug { log.Printf(...) }` guard for verbose per-relay detail.
- Config via `viper.SetDefault` + `mapstructure` tags; nested-map precedent (`relay_ejection_thresholds`, clusterscan group) for the PERF-02 backoff param group.
- Additive Dgraph predicates registered in the schema string + `EnsureSchema` (Phase 5 added `last_attempt` the same way).

### Integration Points
- `cmd/crawler/main.go` main loop: `GetStalePubkeys` → `FetchAndUpdateFollows` → `MarkAttempted`; the hit-set (D-05), `CountStalePubkeys` (D-16), and the staleRemaining log line (D-15) all wire here.
- `drainSubscription` EOSE-return is the per-relay "done" signal for the quorum counter (D-13).
- Schema migration / backfill (D-06) runs at crawler startup alongside `EnsureSchema`.

</code_context>

<specifics>
## Specific Ideas

- Backoff schedule with the locked params: 2h, 4h, 8h, 16h, 32h, 64h, 128h, then **flat at 7 days** (168h) for any further misses — a stub silent for ~a week of attempts is rechecked weekly forever, never dropped.
- The 24h `StalePubkeyThreshold` is deliberately *retained in meaning* — it becomes the re-crawl cadence for pubkeys that DO publish (HIT path, D-03), so active accounts' contact-list updates are still caught daily.
- Quorum at 0.70 of queried relays: with ~172 relays in production, the batch stops waiting once ~120 have sent EOSE or errored, instead of blocking on the slow 44% that historically exceed the old 30s timeout.

</specifics>

<deferred>
## Deferred Ideas

- **Stored `follower_count` predicate as the primary sort key** — only adopted if D-09's live-Dgraph verification shows `orderdesc count(~follows)` is truncated by the 1000-row sort cap; otherwise it stays a documented fallback (D-10), not built.
- **Caching / periodic refresh of `CountStalePubkeys`** — only if the per-batch count proves measurably expensive (D-17); not built by default.
- **Per-relay metrics endpoint / structured observability** (OBS-01) — future milestone.
- **Relay re-discovery** (DISC-01) — future milestone.

None — discussion stayed within phase scope.

</deferred>

---

*Phase: 8-Frontier-Prioritization-Timeout-Observability*
*Context gathered: 2026-06-13*
