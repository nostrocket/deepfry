# Phase 6: Filter Size & Per-Relay Cap Detection - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning

<domain>
## Phase Boundary

Reduce the default relay filter batch size from 500 to 100, expose it as a configurable YAML field, and make the crawler detect and adapt to per-relay filter caps at runtime — so no relay rejects or drops a connection due to an oversized filter REQ.

Requirements: FILTER-01, FILTER-02.

**Out of scope (unchanged):** Relay health persistence across restarts, relay ejection (Phase 7); frontier prioritization, EOSE-quorum, metric fixes (Phase 8); anything touching StrFry, Dgraph schema, or event payloads.

</domain>

<decisions>
## Implementation Decisions

### FILTER-01: Batch size exposure
- **D-01:** The batch size is exposed as `relay_filter_batch_size` in `~/deepfry/web-of-trust.yaml` (top-level YAML config field, snake_case, matching existing key conventions). Default: 100. Add to the `Config` struct in `pkg/config/config.go` with a `mapstructure:"relay_filter_batch_size"` tag and a Viper default of 100.
- **D-02:** `relay_filter_batch_size` serves dual purpose: it is both the outbound batch size (how many authors `GetStalePubkeys` returns per cycle) and the initial per-relay cap assumption. Every relay starts with `filterCap = relay_filter_batch_size`.

### FILTER-02: Per-relay cap storage
- **D-03:** Add `filterCap int` field to the `relayState` struct in `pkg/crawler/crawler.go`. Initialized to `relay_filter_batch_size` at crawler startup. In-memory only — lost on restart (caps are re-learned from real relay behavior on each run). No YAML persistence, no Dgraph writes.
- **D-04:** Cap update on NOTICE: when `queryRelay` intercepts a NOTICE message containing "filter item too large" (or equivalent), the relay's `filterCap` is halved (`filterCap = filterCap / 2`). No attempt to parse an exact limit from the NOTICE text — relay NOTICE formats are not standardised.
- **D-05:** Cap floor: `filterCap` is never reduced below 10. If `filterCap / 2` would go below 10, set `filterCap = 10`. If a relay rejects a 10-author filter (either via NOTICE or connection drop within the attribution window), mark the relay dead — same as today's transport error path.

### FILTER-02: Small-cap relay query strategy
- **D-06:** When `queryRelay` is called with a filter whose `Authors` length exceeds the relay's `filterCap`, split the authors slice into sequential chunks of `filterCap` size. Each chunk is sent as a separate `Subscribe` call to the same relay. Results from all chunks feed the same `eventsChan`. The existing `processedEventIDs` deduplication in `FetchAndUpdateFollows` handles any duplicate events across chunks.
- **D-07:** Chunks run sequentially within `queryRelay` (not parallel goroutines). Simple, no additional cancellation complexity, consistent with the single-relay scope of `queryRelay`.

### FILTER-02: NOTICE interception hookup
- **D-08:** Use `nostr.WithNoticeHandler(func(string))` as a relay option passed to `nostr.RelayConnect`. The handler fires for all NOTICEs from that relay. When "filter item too large" is detected, it updates `rs.filterCap` on the corresponding `relayState`. The NOTICE handler needs access to `rs` — wire it at connect time in `New()` and `reconnectDeadRelays()` where `nostr.RelayConnect` is called.

### FILTER-02: Connection-drop-on-REQ attribution
- **D-09:** Temporal heuristic: if a relay's connection drops within 500ms of calling `Subscribe()`, classify the drop as a filter-rejection (not a genuine transport failure). Record the cap halving (D-04/D-05 logic) and retry the same authors chunk at the smaller cap within `queryRelay`. Longer-lived connections that later drop remain transport errors and mark the relay dead as today.
- **D-10:** After halving and retrying: if the retry succeeds, the relay stays alive with the new lower cap. If the retry also drops within 500ms, halve again (down to floor=10). If the 10-author floor is hit and it still drops within 500ms, mark the relay dead.

### Claude's Discretion
- Exact name and signature of the NOTICE-matching helper (inline string check vs. extracted function).
- Whether the timer for the 500ms attribution window uses `time.Since(subscribeStart)` on error return or a separate goroutine.
- Whether D-06 chunking is extracted into a separate helper function or inlined in `queryRelay`.
- Test file layout: whether FILTER-01 and FILTER-02 tests share a file or are split.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase scope
- `.planning/ROADMAP.md` § "Phase 6" — goal + 3 success criteria
- `.planning/REQUIREMENTS.md` § FILTER-01, FILTER-02 — formal requirement text

### Code under change
- `cmd/crawler/main.go:109` — `const batchSize = 500`: replace with `cfg.RelayFilterBatchSize` from loaded config
- `pkg/crawler/crawler.go:39` — `relayState` struct: add `filterCap int`
- `pkg/crawler/crawler.go:82` — `New()`: pass `WithNoticeHandler` option to all `nostr.RelayConnect` calls; initialize `filterCap` from config
- `pkg/crawler/crawler.go:215,241` — `reconnectDeadRelays()`: same `WithNoticeHandler` wiring on reconnect
- `pkg/crawler/crawler.go:260` — `FetchAndUpdateFollows`: read `relay_filter_batch_size` from config (already flowing via `Crawler` struct); no per-relay cap logic here — D-06 lives in `queryRelay`
- `pkg/crawler/crawler.go:434` — `queryRelay`: add chunked sub-REQ loop (D-06/D-07) and connection-drop attribution (D-09/D-10)
- `pkg/config/config.go` — add `RelayFilterBatchSize int` field with `mapstructure:"relay_filter_batch_size"` tag and Viper default of 100

### go-nostr API
- `go-nostr@v0.52.0/relay.go:100-102` — `WithNoticeHandler` option: `type WithNoticeHandler func(notice string)` with `ApplyRelayOption(r *Relay)`. Passed as variadic to `nostr.RelayConnect(ctx, url, opts...)`.

### Test references
- `pkg/dgraph/dgraph_stale_test.go` — template for integration tests (build tag, `mustMutate` pattern, timestamp-based fixture pubkeys)
- `.planning/codebase/TESTING.md` — test framework, build-tag gating, `make test` vs `go test -tags=integration`

### Conventions / constraints
- `web-of-trust/CLAUDE.md` and root `CLAUDE.md` — data-separation rule, temp-`HOME` for config tests, `Profile` schema compatibility
- Phase 5 CONTEXT.md (`.planning/phases/05-pubkey-validation-hardening/05-CONTEXT.md`) — D-02: skip-and-log convention at crawler call sites; D-04: inline recovery pattern; integration test placement precedents

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/crawler/crawler.go:39` — `relayState` struct: `failures atomic.Int32` is the model for adding `filterCap int` — an in-memory field on the relay state, no locking needed for reads from a single goroutine per relay.
- `nostr.RelayConnect(ctx, url, opts ...RelayOption)` — already accepts variadic options; `WithNoticeHandler` is a first-class option. No relay connection refactoring needed.
- `pkg/crawler/crawler.go:590` — `cleanSubscribeError` helper: existing error string processing for `Subscribe()` errors — same pattern for stripping verbose filter dumps from NOTICE parsing.
- `pkg/config/config.go` — existing `Config` struct with Viper defaults: `viper.SetDefault("stale_pubkey_threshold", 86400)` pattern is the exact model for adding `relay_filter_batch_size`.

### Established Patterns
- Per-relay goroutine model: `FetchAndUpdateFollows` launches one goroutine per alive relay via `go func(rs *relayState)`. Each goroutine calls `queryRelay` and sends to a shared `eventsChan`. The chunking loop (D-06) is contained within `queryRelay` — no change to the goroutine fan-out structure.
- Error type hierarchy: `subscriptionError` (subscription failed) vs `transportError` (connection lost) already exists. The connection-drop-on-REQ attribution (D-09) adds a third classification path: "filter-rejection drop" → halve cap + retry, not a new error type.
- `processedEventIDs map[string]struct{}` in `FetchAndUpdateFollows` already deduplicates events from multiple relays. The same deduplication covers events returned by multiple chunks of the same relay.

### Integration Points
- `cmd/crawler/main.go:109` — `batchSize` constant flows to `GetStalePubkeys(ctx, threshold, batchSize)`. Replace with `cfg.RelayFilterBatchSize`. The batch size passed to `GetStalePubkeys` and the per-relay filter size can diverge if per-relay caps are smaller; `FetchAndUpdateFollows` always gets the full 100-author batch, and `queryRelay` handles chunking per relay.
- `New()` at `crawler.go:82` — the `nostr.RelayConnect` call receives no options today. Adding `WithNoticeHandler` here requires the handler to close over `rs` (the relay state pointer), available in the same `for _, url := range cfg.RelayURLs` loop.

</code_context>

<specifics>
## Specific Ideas

- The `WithNoticeHandler` closure must capture `rs *relayState` by pointer so that when the handler fires (potentially after `New()` returns), it updates the correct relay's `filterCap`. Care needed in the loop: use a loop-local variable (`rs := rs`) to avoid the loop-variable capture bug — the same idiom used in `go func(rs *relayState)` goroutines throughout the file.
- The 500ms attribution window for connection-drop-on-REQ is a pragmatic heuristic. Some relays may legitimately be slow to respond without rejecting the filter. The timeout is intentionally conservative (500ms vs. a typical TCP handshake + TLS + one WebSocket message ≈ 50–200ms).
- `relay_filter_batch_size` in config serves two roles: it sets the `GetStalePubkeys` batch size (how many pubkeys to pull from Dgraph per cycle) and the initial per-relay cap. These two roles are currently the same number but could diverge in a future phase if the Dgraph batch size needs separate control.

</specifics>

<deferred>
## Deferred Ideas

- **Cap persistence across restarts:** Saving per-relay caps to `web-of-trust.yaml` — deferred. In-memory rediscovery is simpler and acceptable for now; Phase 7 relay health work may revisit config mutation patterns.
- **Parsing exact limit from NOTICE text:** Some relays embed their max filter size in the NOTICE message. Deferred — NOTICE formats are not standardised; halving is deterministic and sufficient.
- **Bloom filter per relay for "seen authors":** DISC-02 from Future Requirements — skip querying relay X for pubkey Y if that relay has never returned an event for similar pubkeys. Out of scope for Phase 6.

</deferred>

---

*Phase: 6-Filter-Size-Per-Relay-Cap-Detection*
*Context gathered: 2026-06-11*
