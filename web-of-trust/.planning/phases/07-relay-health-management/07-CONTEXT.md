# Phase 7: Relay Health Management - Context

**Gathered:** 2026-06-12
**Status:** Ready for planning

<domain>
## Phase Boundary

Relays that repeatedly fail are automatically removed from the config without manual intervention; failure tracking and learned filter caps survive reconnects (no more reset-to-zero / reset-to-default); and relay lifecycle logging collapses to one line per state change instead of per-relay/per-event spam.

Requirements: RELAY-01, RELAY-02, RELAY-03, LOG-01, LOG-02, LOG-03.

**Out of scope (unchanged):** Frontier prioritization, EOSE-quorum early exit, timeout reduction, `staleRemaining` metric (Phase 8); persistence of relay state across process *restarts* (explicitly decided against — see D-04/D-12); relay re-discovery (DISC-01, future); per-relay metrics endpoint (OBS-01, future); anything touching StrFry, Dgraph schema, or event payloads.

</domain>

<decisions>
## Implementation Decisions

### RELAY-01: Failure counting & decay
- **D-01:** On successful reconnect, the relay's failure counter is **halved** (`failures = failures / 2`), not reset to zero. Replaces `rs.failures.Store(0)` at `pkg/crawler/crawler.go:239`. A flapping relay (+1, +1, /2, +1, …) trends upward past its threshold.
- **D-02:** A **successful query** (subscribed, drained events, returned without error) **resets the counter to 0** — keep the semantics of `crawler.go:323` but only for genuine completed work. A bare reconnect is not proof of health; a completed query is.
- **D-03:** A **failed reconnect attempt no longer removes the relay from config immediately** (current behavior at `crawler.go:226`). Instead it counts as a transport-class failure, increments the counter, and re-schedules with backoff. Ejection happens only via the per-class threshold — one unified ejection path for all failure modes.
- **D-04:** Failure counters are **in-memory only**. They persist across reconnects within a run and are lost on process restart. No YAML or sidecar-file persistence; a genuinely bad relay re-accumulates within a few batches after restart.

### RELAY-02: Classification & ejection policy
- **D-05:** Each `relayState` tracks **one counter per failure class** (transport, filter_rejection, subscription_flap). The decay rules (D-01 halve on reconnect, D-02 reset on good query) apply to all class counters.
- **D-06:** Per-class thresholds are configured as a **nested YAML map** with these defaults:
  ```yaml
  relay_ejection_thresholds:
    transport: 10
    filter_rejection: 3
    subscription_flap: 5
  ```
  Rationale: transport gets slack (networks blip); filter-rejection ejects fast (a relay still rejecting at floor cap 10 is useless); flap sits between. Single viper key with `SetDefault`, `mapstructure` tag per existing config conventions.
- **D-07:** Classification **maps the existing error paths** — no new timing heuristics:
  - NOTICE-driven cap halvings and cap-floor-reached deaths → **filter_rejection**
  - `subscriptionError` (Subscribe refused, not attributed to filter size) → **subscription_flap**
  - `transportError` mid-drain / connection lost / timeout → **transport**
- **D-08:** On ejection, the relay is removed from `relay_urls` **and appended to an `ejected_relays` list** in `web-of-trust.yaml` (URL only; reason class, count, and timestamp go in the log line, not the YAML). Future `discover-relays` runs can skip ejected relays; ops can manually rehabilitate by moving the URL back. Small extension to `RemoveRelayURL` in `pkg/config/config.go`.

### RELAY-03: Filter-cap persistence & recovery
- **D-09:** Learned `filterCap` values **survive reconnects** — delete the reset `rs.filterCap.Store(int32(c.filterBatchSize))` at `crawler.go:240` (reverses Phase 6 WR-03). The 50→25→12→10 halving cascade no longer re-runs per batch.
- **D-10:** Recovery is **probe-up by doubling**: after **10 consecutive successful batches** at cap C (success streak per relay), the next real batch uses `min(C*2, relay_filter_batch_size)` for that relay. If the probe is rejected (NOTICE or drop), the existing halving logic puts the cap straight back and the streak resets. No separate probe traffic — the probe is just a larger chunk size on a normal batch.
- **D-11:** **Probe-induced rejections are exempt from ejection counting.** A rejection while probing above the last-known-good cap halves the cap and resets the streak but does NOT increment the filter_rejection counter. Only rejections at or below the learned cap (the relay got worse) count toward ejection. This prevents a permanently capped but healthy relay from accumulating 1 ejection point per probe cycle into the low filter_rejection threshold of 3.
- **D-12:** Caps are **in-memory only** (consistent with D-04). After a restart, caps start at `relay_filter_batch_size` and re-learn — now with one-line logging (D-14) instead of the old cascade spam.

### LOG-01/02/03: Log summary format
- **D-13 (LOG-01):** The reconnect sweep emits its summary line (`Reconnected 96/103 relays, 1 removed, 6 still dead`) **only when something changed** — at least one relay reconnected, was ejected, or newly stayed dead past its retry window. Quiet sweeps (everything alive or still backing off) log nothing. Per-relay reconnect detail only under `c.debug`.
- **D-14 (LOG-02):** Cap negotiation is **silent when stable**: one line only when the cap changed this batch — `cap learned at N`, `floor reached`, or `probe-up to N`. A relay chunking happily at its known cap logs nothing; individual halving steps are debug-only.
- **D-15 (LOG-03):** State-change lines are **plain text** (e.g. `Relay X dead (transport 3/10), retry in 1m` — failure class, per-class count/threshold, next retry). The duplicate `WARN: Connection timed out` + `marked dead` pair collapses into this single line, and filter-cap failures are never worded as timeouts. The existing `RELAY_ERROR:` JSON blobs from `logRelayError` are **demoted to debug-only** — they duplicate the dead-marking pair LOG-03 collapses.

### Claude's Discretion
- Exact wording/format of each log line (as long as required fields — class, count, threshold, next retry — are present).
- Where the per-relay success-streak counter for probe-up lives (field on `relayState` vs derived).
- Whether per-class counters are an array indexed by class constant or named fields; atomicity approach (Phase 6 precedent: `atomic.Int32`).
- How "probe in progress" is flagged so D-11's exemption can tell probe rejections from at-cap rejections.
- Whether `ejected_relays` handling lives in `RemoveRelayURL` or a new `EjectRelayURL` function.
- Forward-relay handling: the forward relay is config-critical and should presumably be exempt from ejection (it has its own reconnect path at `crawler.go:247-270`); confirm and document during planning.
- Test layout for the state-machine logic (unit-testable decay/classification helpers vs integration).

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope
- `.planning/ROADMAP.md` § "Phase 7" — goal + 6 success criteria
- `.planning/REQUIREMENTS.md` § RELAY-01, RELAY-02, RELAY-03, LOG-01, LOG-02, LOG-03 — formal requirement text

### Code under change
- `pkg/crawler/crawler.go:33-37` — `initialBackoff`, `maxBackoff`, `maxConsecutiveFailures = 5` constants; the hardcoded threshold is replaced by per-class config (D-06)
- `pkg/crawler/crawler.go:39-47` — `relayState` struct: `failures atomic.Int32` becomes per-class counters (D-05); add success-streak state for probe-up (D-10)
- `pkg/crawler/crawler.go:180-209` — `markRelayDead`: classification entry point; duplicate log pair to collapse (D-15)
- `pkg/crawler/crawler.go:211-270` — `ReconnectRelays`: failure halving (D-01), cap-reset deletion (D-09), failed-reconnect counting (D-03), sweep summary (D-13)
- `pkg/crawler/crawler.go:300-330` — `FetchAndUpdateFollows` goroutine fan-out: `rs.failures.Store(0)` on success (D-02 keeps, per-class)
- `pkg/crawler/crawler.go:490-560` — `queryRelay`: cap halving/floor paths feed filter_rejection class (D-07); probe-up chunk sizing (D-10); per-step halving logs become debug (D-14)
- `pkg/crawler/crawler.go:660-690` — `handleFilterNotice`: NOTICE-driven halving, CAS loop (Phase 6 CR-01); probe-rejection exemption hook (D-11)
- `pkg/crawler/crawler.go` — `logRelayError` (`RELAY_ERROR:` JSON blobs): demote to debug-only (D-15)
- `pkg/config/config.go:16-23,52-80` — `Config` struct + viper defaults: add `relay_ejection_thresholds` nested map (D-06) and `ejected_relays` list handling (D-08)
- `pkg/config/config.go:155-168` — `RemoveRelayURL`: extend or sibling function for move-to-ejected semantics (D-08)

### Prior phase decisions affected
- `.planning/phases/06-filter-size-per-relay-cap-detection/06-CONTEXT.md` — D-03 (in-memory cap), D-04/D-05 (halving + floor 10), D-09/D-10 (500ms drop attribution): all still in force EXCEPT the cap reset-on-reconnect (WR-03), which D-09 reverses. Phase 6's filter-rejection classification is the input to RELAY-02's filter_rejection bucket.

### Conventions / constraints
- `.planning/codebase/CONCERNS.md` § "Config mutation via global Viper singleton" — `RemoveRelayURL` operates on the package-global viper loaded by `LoadConfig`; D-08's `ejected_relays` write must go through the same instance; never edit live `~/deepfry/web-of-trust.yaml` in tests (temp `HOME`)
- `.planning/codebase/CONCERNS.md` § "In-memory relay state lost on restart" — slice-reuse invariant in `markRelayDead`/`ReconnectRelays` (`c.relays[:0]` trick) must be preserved when editing the pruning loops
- `.planning/codebase/CONCERNS.md` § "go-nostr error-string coupling" — classification relies on substring matching (`"not connected"`, `"failed to write"`); D-07 builds on the existing `subscriptionError`/`transportError` types rather than adding new string matches
- `web-of-trust/CLAUDE.md` and root `CLAUDE.md` — data-separation rule, temp-`HOME` for config tests, StrFry unmodified

### Test references
- `pkg/dgraph/dgraph_stale_test.go` — integration test conventions (build tag, helpers)
- `.planning/codebase/TESTING.md` — build-tag gating, `make test` vs `make test-integration`; note `go test -race` precedent from Phase 6 CR-01

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `relayState.filterCap atomic.Int32` (Phase 6) — the model for per-class failure counters: lock-free atomic fields on the relay state, CAS loop in `handleFilterNotice` for concurrent NOTICE handlers.
- `subscriptionError` / `transportError` types with `Error()`/`Unwrap()` — D-07 classification maps directly onto these; no new error taxonomy needed, just a class tag.
- `onConnectFail func(url string)` callback — the existing ejection hook wired to `config.RemoveRelayURL` in `cmd/crawler/main.go`; D-08 changes what the callback does (move-to-ejected), and D-03 changes *when* it fires (threshold only, never on first failed reconnect).
- Nested-map config precedent: `clusterscan` settings already use a struct group with `mapstructure` tags — model for `relay_ejection_thresholds`.

### Established Patterns
- Slice-reuse pruning (`kept := c.relays[:0]`) in `markRelayDead` and `ReconnectRelays` — preserve when restructuring; do not hold references across these calls.
- `if c.debug { log.Printf(...) }` guard — the convention for demoting per-relay detail (D-13/D-14/D-15 debug paths).
- Backoff doubling with `maxBackoff` clamp — unchanged; D-01's counter halving is orthogonal to the backoff timer.

### Integration Points
- `cmd/crawler/main.go` — wires `onConnectFail` to `config.RemoveRelayURL`; will need the ejected-list variant and possibly the failure-class in the callback signature.
- `ReconnectRelays` is called from the main loop every iteration — natural place to accumulate D-13 sweep counters and emit the single summary line.
- Phase 8 will touch the same `FetchAndUpdateFollows` timeout/EOSE logic — keep classification changes localized so the phases don't collide.

</code_context>

<specifics>
## Specific Ideas

- The probe-up (D-10) is deliberately zero-cost: it is not separate probe traffic, just letting the next real batch use a doubled chunk size. A failed probe self-corrects through the exact same NOTICE/drop halving path Phase 6 built.
- The filter_rejection threshold of 3 is intentionally low because by the time a rejection counts (at or below the learned cap, per D-11), the cap system has already done everything it can — a relay rejecting at floor 10 has no remaining value.
- The `Reconnected 96/103 relays, 1 removed, 6 still dead` example line from REQUIREMENTS.md LOG-01 is the target shape for the sweep summary.

</specifics>

<deferred>
## Deferred Ideas

- **Relay state persistence across restarts** (counters and caps) — explicitly decided against for this phase (D-04, D-12); revisit only if restart-time re-learning proves costly in production.
- **`ejected_relays` metadata in YAML** (reason/timestamp/count per entry) — URL-only list chosen; richer audit trail can come with DISC-01 relay re-discovery work.
- **Structured JSON relay-event logging** for metrics scraping — belongs with OBS-01 (per-relay metrics endpoint), future milestone.
- **discover-relays skipping `ejected_relays`** — the list is written in this phase, but teaching `discover-relays` to respect it lands with DISC-01.

None of the discussion expanded phase scope.

</deferred>

---

*Phase: 7-Relay-Health-Management*
*Context gathered: 2026-06-12*
