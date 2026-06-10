---
phase: 05-pubkey-validation-hardening
reviewed: 2026-06-10T00:00:00Z
depth: standard
files_reviewed: 4
files_reviewed_list:
  - pkg/crawler/crawler.go
  - pkg/dgraph/dgraph.go
  - pkg/dgraph/validate_test.go
  - pkg/dgraph/dgraph_validation_test.go
findings:
  critical: 3
  warning: 4
  info: 3
  total: 10
status: issues_found
---

# Phase 05: Code Review Report

**Reviewed:** 2026-06-10T00:00:00Z
**Depth:** standard
**Files Reviewed:** 4
**Status:** issues_found

## Summary

This phase hardens pubkey validation: it introduces `validate.go` (`ValidatePubkey` / `isValidHexPubkey`), threads that gate through the crawler's follow-list parser and `AddFollowers`, adds a recover-or-purge path in `MarkAttempted`, and adds unit + integration tests. The validation logic itself (`validate.go`) is correct. The main risks are concentrated in `RemoveFollower` (DQL injection, missing validation), an infinite-spin loop when the relay context times out, and a suppressed error return from `TouchLastDBUpdate`.

---

## Critical Issues

### CR-01: DQL injection in `RemoveFollower` — unvalidated pubkeys interpolated into raw DQL

**File:** `pkg/dgraph/dgraph.go:402-412`
**Issue:** `RemoveFollower` builds its Dgraph query and n-quads strings by raw string concatenation of the `signerPubkey` and `followee` parameters. The only guards are empty-string checks (lines 389-394). A caller that passes a value containing a `"` character, a newline, or other DQL-special characters can break out of the string literal and inject arbitrary DQL predicates or mutations. This is particularly dangerous in the n-quads `setNquads` block where the pubkey value is placed between un-escaped quotes.

Example: `signerPubkey = 'x" . <0x1> * * .\n_:x <pubkey> "garbage'` would corrupt the mutation.

By contrast, `AddFollowers` uses `fmt.Sprintf` with `%q` (which applies Go's `strconv.Quote` escaping) for all DQL string literals. `RemoveFollower` does not, and does not call `isValidHexPubkey` / `ValidatePubkey` to rule out non-hex input before interpolation.

**Fix:**
```go
func (c *Client) RemoveFollower(
    ctx context.Context,
    signerPubkey string,
    kind3createdAt int64,
    followee string,
) error {
    if !isValidHexPubkey(signerPubkey) {
        return fmt.Errorf("invalid signerPubkey %q: must be 64 hex chars", signerPubkey)
    }
    if !isValidHexPubkey(followee) {
        return fmt.Errorf("invalid followee %q: must be 64 hex chars", followee)
    }
    if kind3createdAt == 0 {
        return fmt.Errorf("kind3createdAt must be specified (non-zero)")
    }
    // Use %q (Go-quoted) for all string literals in DQL
    q := fmt.Sprintf(`query {
        f as var(func: eq(pubkey, %q))
        e as var(func: eq(pubkey, %q))
    }`, signerPubkey, followee)
    ...
```
Replace every bare `"` + variable concatenation in `setNquads` with `fmt.Sprintf(...%q...)` for string values.

---

### CR-02: Infinite spin after relay context `DeadlineExceeded`

**File:** `pkg/crawler/crawler.go:327-340`
**Issue:** When `relayQueryContext` fires `DeadlineExceeded` the `select` branch at line 327 logs and falls through — it does **not** break or return. The outer `for {}` loop then immediately re-enters `select`. Because the deadline has already passed, `relayQueryContext.Done()` is permanently closed and will be chosen by `select` on every iteration that does not find a ready event or error. The loop will spin at CPU speed until `eventsChan` is eventually closed (when the relay goroutines drain) or until `relayContext` is cancelled.

In a scenario where goroutines are slow to drain (e.g., blocked on a relay write), this causes 100% CPU on the calling goroutine with no bound.

**Fix:** After deciding to "continue processing buffered events", break out of the `select` and drain only `eventsChan` in a separate, simpler loop — or unconditionally return once the deadline is exceeded (events already in `eventsChan` at that point are the responsibility of the goroutine closer, not this loop):
```go
case <-relayQueryContext.Done():
    if relayQueryContext.Err() == context.DeadlineExceeded {
        if c.debug {
            log.Printf("Relay query timeout reached; draining buffered events")
        }
        // Drain whatever is already buffered, then return.
        for {
            select {
            case event, ok := <-eventsChan:
                if !ok {
                    return len(pubkeysWithEvents), nil
                }
                // process event (same body as the main case)...
            default:
                return len(pubkeysWithEvents), nil
            }
        }
    }
    return len(pubkeysWithEvents), relayQueryContext.Err()
```

---

### CR-03: `TouchLastDBUpdate` return values silently discarded at call site

**File:** `pkg/crawler/crawler.go:391`
**Issue:** `TouchLastDBUpdate` returns `(bool, error)`. The call site discards both:
```go
c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey)
```
A Dgraph error during the touch (network blip, transaction conflict) is silently swallowed. The function is called inside the mutex-held event-processing path; if it fails consistently for a pubkey, that pubkey will keep re-entering the stale frontier and the crawler will re-fetch and re-discard its kind-3 event every loop, wasting relay quota.

This is a correctness issue: the `last_db_update` timestamp is the mechanism that prevents redundant re-crawls, so silent failures here degrade the core stale-feed model.

**Fix:**
```go
if _, err := c.dgClient.TouchLastDBUpdate(relayContext, event.PubKey); err != nil {
    log.Printf("WARN: failed to touch last_db_update for %s: %v", event.PubKey, err)
    // Non-fatal: continue processing; the pubkey will age out and be re-queued.
}
```

---

## Warnings

### WR-01: `forwardEvent` called while holding `dbUpdateMutex`, can block under relay failure

**File:** `pkg/crawler/crawler.go:383`
**Issue:** `forwardEvent` is called at line 383 while `dbUpdateMutex` is held (locked at line 350). If the forward relay is alive, `forwardEvent` calls `relay.conn.Publish(...)`, which can block on a slow or hung relay connection until the OS TCP timeout fires. During that time no other event can be processed. This partially defeats the purpose of the mutex (protecting Dgraph writes) and introduces an unbounded stall in the critical path.

**Fix:** Either (a) call `forwardEvent` before acquiring the mutex, or (b) run it in a fire-and-forget goroutine. Option (a) is simpler since `forwardEvent` does not touch shared Dgraph state:
```go
c.forwardEvent(relayContext, event)  // move before c.dbUpdateMutex.Lock()
pubkeysWithEvents[event.PubKey] = struct{}{}
c.dbUpdateMutex.Lock()
// ... Dgraph operations only below
```

---

### WR-02: `RemoveFollower` does not validate `kind3createdAt <= 0`

**File:** `pkg/dgraph/dgraph.go:395-397`
**Issue:** The guard checks `kind3createdAt == 0` but a negative timestamp (e.g., from a clock-skewed or malicious event) passes through unchecked and is written directly to Dgraph as a quoted int. Negative timestamps will corrupt the version-ordering logic that prevents older events from overwriting newer ones.

**Fix:**
```go
if kind3createdAt <= 0 {
    return fmt.Errorf("kind3createdAt must be a positive unix timestamp, got %d", kind3createdAt)
}
```
`AddFollowers` does not have this guard either, but since it receives the value from a validated Nostr event (`event.CreatedAt` is a `nostr.Timestamp` which is `int64` and cannot be negative from a valid event), the risk there is lower. `RemoveFollower` is a direct API call with no upstream guard.

---

### WR-03: `markRelayDead` corrupts `c.relays` slice via aliased append

**File:** `pkg/crawler/crawler.go:171-199`
**Issue:** The function uses the in-place filter idiom (`kept := c.relays[:0]`) to rebuild `c.relays`. This reuses the underlying array of the original slice. When a relay is not dead, it is appended back with `kept = append(kept, rs)` — fine. But when a relay IS dead and `failures < maxConsecutiveFailures`, the code appends `rs` **after** having mutated its fields (conn, alive, retryAt, backoff). The append to `kept` still writes to the original backing array at the correct position, so there is no data corruption here in practice.

However, the more subtle issue is: `ReconnectRelays` uses the same pattern (`kept := c.relays[:0]`) and both functions are called from the main loop without synchronization. If `markRelayDead` is ever called concurrently with `ReconnectRelays` (currently they are not, both run from the single-threaded main loop, but the goroutine at line 302 calls `rs.failures.Store(0)` on the `rs` struct — a different field — without the mutex), the aliased slice reuse is a latent race hazard. The `failures` field on a relay being appended-to by `markRelayDead` could be observed mid-increment by `ReconnectRelays`.

**Fix:** Move to a safe copy pattern:
```go
kept := make([]*relayState, 0, len(c.relays))
```

---

### WR-04: `MarkAttempted` recovery path does not commit the mutation transaction

**File:** `pkg/dgraph/dgraph.go:621-630`
**Issue:** In the "RECOVERABLE uppercase" branch, a new `txn` is created (line 621), a mutation is issued with `CommitNow: true` (line 623), but if `txn.Mutate` returns an error, the code calls `txn.Discard(ctx)` — correct. However, on **success**, `txn.Discard` is never called either. The `defer txn.Discard(ctx)` pattern is not used here. While `CommitNow: true` commits the transaction server-side, the client-side `txn` object is leaked without a `Discard`. In the dgo client, a non-deferred `Discard` after a committed transaction is a no-op, but the absence is inconsistent with the rest of the file and may cause issues if the dgo client ever adds reference-counting or connection-pool accounting.

**Fix:**
```go
txn := c.dg.NewTxn()
defer txn.Discard(ctx)
mu := &api.Mutation{
    SetNquads: []byte(fmt.Sprintf("<%s> <pubkey> %q .\n", garbageUID, lower)),
    CommitNow: true,
}
if _, err := txn.Mutate(ctx, mu); err != nil {
    log.Printf("WARN: recover uppercase pubkey %q (uid %s) to %q failed: %v", pk, garbageUID, lower, err)
} else {
    log.Printf("INFO: recovered uppercase pubkey %q (uid %s) → %q", pk, garbageUID, lower)
}
```

---

## Info

### IN-01: `fmt.Println` debug artifact inside mutex-held critical path

**File:** `pkg/crawler/crawler.go:389`
**Issue:** `fmt.Println("already have newer event for " + event.PubKey)` is guarded by `c.debug` but writes to `os.Stdout` rather than using `log.Printf`. All other debug output in this file uses `log.Printf`. This is inconsistent and also writes pubkey data to stdout (instead of the structured logger), which may interfere with any process that reads the binary's stdout.

**Fix:**
```go
log.Printf("DEBUG: already have newer event for %s", event.PubKey)
```

---

### IN-02: `TestMarkAttemptedRecoverOrPurge` cleanup does not delete short-hex and relay-blob garbage nodes when they still exist before `MarkAttempted` runs

**File:** `pkg/dgraph/dgraph_validation_test.go:129-132`
**Issue:** The cleanup block only deletes the `recoveredUID` (the successfully recovered lowercase node). If the test fails before `MarkAttempted` is called (e.g., the schema assertion fails), the `shortHex` and `relayBlob` nodes inserted by `mustMutate` (line 61) are never removed. Over repeated test runs on a shared Dgraph instance this creates accumulating garbage stubs. The cleanup should unconditionally attempt to remove all three fixture nodes.

**Fix:** Expand cleanup to resolve and delete all three inserted keys before test return (or use `t.Cleanup` at the top with a closure that resolves all three).

---

### IN-03: `validate_test.go` does not test the `npub`/bech32 input class

**File:** `pkg/dgraph/validate_test.go`
**Issue:** The test covers lowercase hex, uppercase hex, mixed-case hex, short hex, relay-URL blobs, and empty strings. It does not test `npub1...` bech32-encoded pubkeys, which are a common user-entry mistake and are present in the wild. The regex `^[0-9a-f]{64}$` correctly rejects them (they are not 64 chars of lowercase hex), but there is no test case confirming this for documentation and regression purposes.

**Fix:** Add a test case:
```go
{
    name:    "bech32 npub (not hex)",
    input:   "npub1sgdm3a76cj6acpjrxmq4p2k97h2w8p3n5m9e4kf7drz0c9h5xasq7zjyyl",
    wantErr: true,
},
```

---

_Reviewed: 2026-06-10T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
