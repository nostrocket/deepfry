---
phase: 07-relay-health-management
reviewed: 2026-06-12T00:00:00Z
depth: standard
files_reviewed: 5
files_reviewed_list:
  - cmd/crawler/main.go
  - pkg/config/config.go
  - pkg/config/config_test.go
  - pkg/crawler/crawler.go
  - pkg/crawler/crawler_filter_test.go
findings:
  critical: 3
  warning: 6
  info: 5
  total: 14
status: issues_found
---

# Phase 7: Code Review Report

**Reviewed:** 2026-06-12
**Depth:** standard
**Files Reviewed:** 5
**Status:** issues_found

## Summary

Phase 7 (relay health management) adds per-class failure counters, threshold-driven ejection, counter decay on reconnect, probe-up cap recovery, and persistent `ejected_relays` config. `go vet` is clean and all unit tests pass — but the tests largely re-implement the logic inline rather than exercising it, and tracing the actual failure paths reveals three Critical defects: a filter-rejection cascade that ejects a relay within milliseconds of a single rejection (defeating the entire threshold design), unsynchronized concurrent mutation of `c.relays` (data race between relay goroutines and the main loop), and a startup path that permanently ejects relays on a single transient connect failure — capable of emptying `relay_urls` entirely and bricking the next start.

## Narrative Findings (AI reviewer)

## Critical Issues

### CR-01: Filter-rejection path keeps using a connection markRelayDead just closed — single rejection cascades to ejection in milliseconds

**File:** `pkg/crawler/crawler.go:643-668` (esp. 656-664)
**Issue:** In `queryRelay`, when a connection-drop-on-REQ is attributed as an at-cap filter rejection, the code calls `c.markRelayDead(relayURL, classFilterRej)` — which closes `rs.conn`, sets `rs.conn = nil`, and marks the relay dead — and then does `authors = append(chunk, authors...)` and `continue`. The loop's local `relay` variable (captured at line 595) still points at the now-closed connection, so the next `relay.Subscribe(...)` fails instantly (well within the 500ms window) with a "not connected"/"failed to write"-class error, re-entering the same branch: cap halved again, `markRelayDead(classFilterRej)` called again. With the default `filter_rejection` threshold of 3, a relay that drops one real over-sized REQ gets its `failFilterRej` counter driven from 0 to 3 within milliseconds and is **permanently ejected from the config** (`EjectRelayURL` fires via `onConnectFail`). If go-nostr's error string instead reads "context canceled"/other, the loop exits via `subscriptionError`, incorrectly incrementing `classSubFlap` on top of the `classFilterRej` increment. Either way, per-class accounting is corrupted and the D-05/D-06 threshold design is defeated. The probe-rejection branch (`isProbing`) has the same flaw: the connection has already dropped per the error, yet the loop continues re-subscribing on it.
**Fix:** After learning the cap and (for non-probe) incrementing the filter-rejection counter, **return** from `queryRelay` instead of continuing — the relay is dead and must go through `ReconnectRelays`:
```go
if isProbing {
    log.Printf("Relay %s: probe-up to %d rejected, reverting to %d", relayURL, batchCap, newVal)
    return nil // or a sentinel that doesn't increment counters; conn is gone, reconnect sweep will restore it
}
log.Printf("Relay %s: filter rejection at cap %d, halved to %d", relayURL, old, newVal)
c.markRelayDead(relayURL, classFilterRej)
return nil // markRelayDead already handled dead-state; do NOT reuse the closed conn
```
The remaining (un-fetched) authors are inherently retried next cycle because `MarkAttempted`/staleness governs re-selection; alternatively return a dedicated error type consumed silently by the dispatcher.

### CR-02: `c.relays` mutated concurrently without synchronization — data race between relay goroutines and main loop

**File:** `pkg/crawler/crawler.go:226-270` (markRelayDead), call sites `crawler.go:660` and `crawler.go:535-547`
**Issue:** `markRelayDead` rebuilds `c.relays` in place (`kept := c.relays[:0]` … `c.relays = kept`) with no mutex. It is called from two concurrent contexts: (a) inside `queryRelay` at line 660, which runs in one goroutine **per relay** (launched at lines 407-425), and (b) from the `errorsChan` handler in `FetchAndUpdateFollows`' main select loop (lines 535-547), which also reads `len(c.relays)` at line 468. Two relays hitting the filter-rejection path simultaneously, or one relay goroutine racing the main loop's error dispatch, produce concurrent read/write and write/write access to the `c.relays` slice header and backing array. Consequences range from a relay silently vanishing from the pool (lost update during the in-place compaction) to inconsistent slice state. The Phase 7 plan explicitly required "go test -race must stay clean" — the race is in a production path the unit tests never exercise.
**Fix:** Either guard `c.relays` with a mutex in `markRelayDead`/`ReconnectRelays`/the launch loop, or (cleaner, and also fixes CR-01) never call `markRelayDead` from relay goroutines: have `queryRelay` return a typed `filterRejectionError` and let the single-threaded `errorsChan` dispatcher in `FetchAndUpdateFollows` map it to `markRelayDead(url, classFilterRej)`:
```go
case errors.As(re.err, &filterRejErr):
    c.markRelayDead(re.url, classFilterRej)
```

### CR-03: Startup connect failure permanently ejects relays, bypassing all thresholds — can empty relay_urls and brick the next start

**File:** `pkg/crawler/crawler.go:130-137`, `cmd/crawler/main.go:80-85`
**Issue:** In `New()`, a single failed `RelayConnect` immediately invokes `cfg.OnConnectFail(url)`, which main.go now wires to `config.EjectRelayURL` — removing the relay from `relay_urls` and recording it in `ejected_relays` with **zero** counter accrual. This contradicts the phase's own threat model (config.go:130-131 guards against "eject relays on the first failure", STRIDE T-07-DOS) and D-03 (reconnect failures must accrue toward the transport threshold of 10). Worse: if the crawler starts while the network is briefly unavailable (boot ordering, DNS not yet up), `New()` ejects **all** configured relays, persists the now-empty `relay_urls` to YAML, then returns "failed to connect to any relays". Every subsequent start fails at `LoadConfig` ("at least one relay URL is required") until the operator hand-edits the config. A transient failure becomes permanent, self-inflicted denial of service.
**Fix:** Do not eject at startup. Treat initial connect failure like a reconnect failure: keep the `relayState` in the pool with `alive=false`, increment `failTransport`, set `retryAt`, and let `ReconnectRelays`/threshold logic govern ejection:
```go
relay, err := nostr.RelayConnect(context.Background(), url, noticeHandler)
if err != nil {
    log.Printf("WARN: initial connect to %s failed: %v (will retry)", url, err)
    rs.failTransport.Add(1)
    rs.retryAt = time.Now().Add(rs.backoff)
    relays = append(relays, rs) // keep in pool, dead
    continue
}
```

## Warnings

### WR-01: Cap-at-floor rejection is logged as filter_rejection but counted as transport

**File:** `pkg/crawler/crawler.go:666-668`, dispatch at `crawler.go:540-541`
**Issue:** When the filter cap is already at the floor (10), queryRelay logs "marking dead (filter_rejection)" but returns a `&transportError{...}`. The `errors.As` dispatch in `FetchAndUpdateFollows` then calls `markRelayDead(url, classTransport)`, so the failure increments `failTransport` (threshold 10) instead of `failFilterRej` (threshold 3), and the authoritative dead/ejected log line says `(transport N/10)` — contradicting the line just emitted. A relay that persistently rejects floor-sized filters takes ~10 cycles to eject instead of 3, and the per-class accounting (D-05) is wrong.
**Fix:** Introduce a `filterRejectionError` type (or reuse `subscriptionError` with a class field) and dispatch it to `classFilterRej`:
```go
return &filterRejectionError{err: fmt.Errorf("relay %s: filter cap floor reached", relayURL)}
// ...in FetchAndUpdateFollows:
case errors.As(re.err, &filterRejErr): c.markRelayDead(re.url, classFilterRej)
```

### WR-02: Probe-success cap update races the NOTICE handler and can overwrite a freshly learned lower cap

**File:** `pkg/crawler/crawler.go:689-694`, `crawler.go:810-833`
**Issue:** The probe-success path does a non-atomic check-then-store: `if isProbing && batchCap > int(rs.filterCap.Load()) { rs.filterCap.Store(int32(batchCap)) }`. The NOTICE handler (`handleFilterNotice`) runs on go-nostr's goroutine and can halve `filterCap` via CAS at any moment — including between the drain returning EOSE and the probe-success store. A relay that accepts the REQ but emits a "filter too large" NOTICE alongside EOSE will have its NOTICE-learned lower cap immediately stomped back up to the probe value, re-triggering rejections. Additionally, `rs.probing` is not cleared on probe success (only the local `isProbing` is reset); it stays stale-true until the function-exit defer fires, so the relay-visible probing state disagrees with reality for the remainder of the queryRelay call — the D-11 docs say the ejection exemption is "based on rs.probing", which is not what the code does.
**Fix:** Use CAS for the probe-success raise so a concurrent halving wins, and clear `probing` at the same point:
```go
if isProbing {
    rs.filterCap.CompareAndSwap(int32(oldCap), int32(batchCap)) // only raise if unchanged since probe start
    rs.successStreak.Store(0)
    rs.probing.Store(false)
    isProbing = false
}
```

### WR-03: `defer rs.probing.Store(false)` registered inside the chunk loop

**File:** `pkg/crawler/crawler.go:622-623`
**Issue:** The `defer` sits inside `for len(authors) > 0`, so one deferred call accumulates **per chunk iteration**, unconditionally (even when not probing). With a large author set at the floor cap (10), that is hundreds of stacked defers per queryRelay call. Functionally they all run at return, but defers-in-loops are a known Go pitfall (the project's own Go conventions flag `defer` in loops): it wastes memory, and it misleads readers into thinking the flag is cleared per-iteration when in fact it stays set across iterations (see WR-02).
**Fix:** Register the defer once, before the loop:
```go
defer rs.probing.Store(false)
for len(authors) > 0 { ... }
```

### WR-04: Re-queue `append(chunk, authors...)` writes into the filter.Authors backing array shared across concurrent relay goroutines

**File:** `pkg/crawler/crawler.go:662`, shared filter at `crawler.go:386-390, 407-425`
**Issue:** All per-relay goroutines receive the same `filter` value; the `Authors` slice header is copied but the backing array is shared. `chunk` is a sub-slice of that array with spare capacity, so `append(chunk, authors...)` performs element writes into the shared backing array (a self-copy of identical values, but writes nonetheless). Concurrent with other relay goroutines reading the same memory (`chunk := authors[:batchCap]`, and go-nostr serializing `chunkFilter.Authors`), this is a data race under the Go memory model and will be flagged by `-race` whenever the rejection/re-queue path fires on one relay while another relay's goroutine is mid-query.
**Fix:** Give each queryRelay call its own copy of the authors slice before chunking:
```go
authors := append([]string(nil), filter.Authors...)
```
(If CR-01's fix makes the rejection path return instead of re-queue, the `append` disappears — but the per-goroutine copy is still the safe pattern.)

### WR-05: Phase 7 tests re-implement the production logic inline instead of exercising it — they cannot catch regressions

**File:** `pkg/crawler/crawler_filter_test.go:62-98, 104-124, 217-379`
**Issue:** `TestSplitAuthorsChunks`, `TestDecayCounters_HalveOnReconnect`, `TestProbeUp_StreakThreshold`, `TestProbeUp_NoProbeBeforeStreak`, `TestProbeUp_CapClampedToMax`, and `TestProbeRejection_ExemptFromEjection` all copy the algorithm into the test body and assert on the copy — `queryRelay` and `ReconnectRelays` are never called. `TestProbeRejection_ExemptFromEjection` Scenario A is fully tautological: it hard-codes `isProbing := true` and then asserts the branch guarded by `if !isProbing` did not run. These tests pass today and would continue passing after any regression in the real code (including CR-01, which lives precisely in the un-tested `queryRelay` path). This is exactly the false confidence that let CR-01/CR-02 ship.
**Fix:** Refactor the probe/chunk decision into pure, testable helpers (e.g., `func nextBatchCap(rs *relayState, maxBatch int) (cap int, probing bool)` and a rejection-handling func) called by `queryRelay`, and point the tests at those. `markRelayDead` is already directly testable — the probe exemption test should drive the real rejection handler, not a transcript of it.

### WR-06: Events for pubkeys never requested are accepted and written to the graph (pre-existing)

**File:** `pkg/crawler/crawler.go:489-514`
**Issue:** The event-processing loop validates the signature but never checks that `event.PubKey` is one of the requested `authors`. A misbehaving or malicious relay can return any validly-signed kind-3 event (kind isn't re-checked either) for pubkeys outside the stale/frontier set; `pubkeys[event.PubKey]` yields 0 for unknown keys, so the `CreatedAt` guard passes and `updateFollowsFromEvent` writes the foreign follow-list into Dgraph — letting a single relay inject arbitrary spam-cluster subgraphs into the web of trust on its own schedule, bypassing the crawler's frontier discipline. Pre-existing behavior, but Phase 7's relay-health work is the right place to harden relay-input trust.
**Fix:** Drop events whose pubkey was not requested (and assert kind):
```go
if _, requested := pubkeys[event.PubKey]; !requested || event.Kind != 3 {
    if c.debug { log.Printf("Dropping unsolicited event %s (pubkey %s)", event.ID, event.PubKey) }
    c.dbUpdateMutex.Unlock()
    continue
}
```

## Info

### IN-01: `staleRemaining` is always zero

**File:** `cmd/crawler/main.go:136, 162-164`
**Issue:** `totalStale := len(pubkeys)` followed by `staleRemaining := totalStale - len(pubkeys)` is identically 0, so every batch logs "0 stale remaining" regardless of actual backlog — misleading operational telemetry.
**Fix:** Either query the actual remaining-stale count or drop the field from the log line.

### IN-02: Crawler-side threshold fallback uses 10 for every class, diverging from per-class defaults

**File:** `pkg/crawler/crawler.go:250-252, 297-300` vs `pkg/config/config.go:132-140`
**Issue:** The defense-in-depth guard in `markRelayDead`/`ReconnectRelays` substitutes 10 for any non-positive threshold, while the documented per-class defaults are Transport=10, FilterRej=3, SubFlap=5. A caller constructing `crawler.Config` directly (bypassing `LoadConfig`) silently gets 10/10/10.
**Fix:** Centralize the defaults (e.g., a `defaultThreshold(class)` helper or exported config constants) and use them in both guards.

### IN-03: `EjectRelayURL` never dedupes and is inconsistent with `RemoveRelayURL`'s no-op behavior

**File:** `pkg/config/config.go:216-233`
**Issue:** `RemoveRelayURL` returns early without writing when the URL is absent; `EjectRelayURL` unconditionally appends to `ejected_relays` and writes, even if the URL was not in `relay_urls` or is already in `ejected_relays`. Across restarts (operator re-adds a relay, it gets re-ejected) the list accumulates duplicates. `LoadConfig` also never cross-checks `relay_urls` against `ejected_relays`, so a URL can live in both lists.
**Fix:** Skip the append when the URL is already present in `ejected_relays`; optionally warn at load time when a URL appears in both lists.

### IN-04: At-cap rejection emits two log lines, violating the stated LOG-03 single-line invariant

**File:** `pkg/crawler/crawler.go:659-660`
**Issue:** `markRelayDead`'s doc comment (lines 224-225) declares it the "single authoritative dead-state log line" and forbids callers from pre-logging, yet the at-cap rejection path logs "filter rejection at cap %d, halved to %d" immediately before calling `markRelayDead`, which logs "Relay %s dead (filter_rejection ...)" — two lines for one incident.
**Fix:** Fold the cap-halving detail into the class context markRelayDead already prints, or demote the pre-line to `if c.debug`.

### IN-05: Filter-cap floor `10` is a magic number repeated at four sites; `Limit` uses pre-validation count

**File:** `pkg/crawler/crawler.go:129, 288, 606, 643-668` and `crawler.go:389`
**Issue:** The floor value 10 appears as a literal in both `handleFilterNotice` call sites, the `batchCap <= 0` guard, and the halving clamp in `queryRelay`; drift between them would silently break the floor semantics. Separately, `filter.Limit` is set to `len(pubkeys)` rather than `len(authors)`, over-asking when invalid pubkeys were skipped.
**Fix:** `const minFilterCap = 10` at package level; use `Limit: len(authors)`.

---

_Reviewed: 2026-06-12_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
