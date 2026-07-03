---
phase: quick-260703-qxq
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - whitelist-plugin/pkg/quarantine/publisher.go
  - whitelist-plugin/pkg/quarantine/publisher_test.go
autonomous: true
requirements: [QUAR-RECONNECT-FIX]
tags: [quarantine, publisher, reconnect, go-nostr, whitelist-plugin]

must_haves:
  truths:
    - "A stream of application-level OK:false rejections does NOT increment ReconnectCount and keeps the same live connection serving events"
    - "A genuine transport drop (connection lost) still triggers reconnect"
    - "A publish timeout (context.DeadlineExceeded) still triggers reconnect"
    - "publishErrors counts only genuine transport/timeout failures; a new Rejected counter carries benign OK:false outcomes"
    - "published still counts only relay-accepted (OK:true) events; happy path, backpressure/drop, and Stop idempotency are unchanged"
  artifacts:
    - path: "whitelist-plugin/pkg/quarantine/publisher.go"
      provides: "reconnect-only-on-unusable-connection logic + Rejected counter"
      contains: "rejected"
    - path: "whitelist-plugin/pkg/quarantine/publisher_test.go"
      provides: "regression test proving OK:false does not churn the connection"
      contains: "Rejected"
  key_links:
    - from: "whitelist-plugin/pkg/quarantine/publisher.go"
      to: "github.com/nbd-wtf/go-nostr Relay.IsConnected"
      via: "runDrain classifies publishOne errors with relay.IsConnected() + errors.Is(err, context.DeadlineExceeded)"
      pattern: "relay\\.IsConnected\\(\\)"
    - from: "whitelist-plugin/pkg/quarantine/publisher_test.go"
      to: "whitelist-plugin/pkg/quarantine/publisher.go"
      via: "fakeRelay configurable OK:false reply drives the no-reconnect assertion"
      pattern: "Metrics\\(\\)\\.ReconnectCount"
---

<objective>
Stop the quarantine publisher from tearing down and reconnecting its relay connection on benign application-level `OK:false` rejections (the dominant case: "replaced: have newer event" for replaceable kind 0/kind 3, plus "blocked", "duplicate", "created_at too late"). Reconnect ONLY when the connection is genuinely unusable — a lost connection (`!relay.IsConnected()`) or a publish timeout (`errors.Is(err, context.DeadlineExceeded)`). Split observability so benign rejections are counted in a new `Rejected` counter rather than `publishErrors`.

Purpose: Production metrics show reconnect churn (`reconnects ≈ publishErrors`, ~10/min) that is pure waste — the connection is healthy in every OK:false case. This publisher is the REUSED component shared by BOTH the bloom and router whitelist plugins, so the fix benefits both call paths.

Output: Updated `publisher.go` (classification logic + `Rejected` counter wired through `Metrics` struct, `Metrics()`, and the `runMetrics` log line) and extended `publisher_test.go` (fakeRelay can reply `OK:false`; new regression test asserts OK:false does not churn the connection; existing reconnect/happy-path/backpressure/idempotency tests unchanged and passing).
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md

@whitelist-plugin/pkg/quarantine/publisher.go
@whitelist-plugin/pkg/quarantine/publisher_test.go
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Reconnect only on unusable connection; add Rejected counter</name>
  <files>whitelist-plugin/pkg/quarantine/publisher.go</files>
  <behavior>
    - publishOne returns nil (OK:true): published increments; connection retained.
    - publishOne returns an error AND relay.IsConnected() is true AND NOT errors.Is(err, context.DeadlineExceeded): treat as benign application rejection — increment rejected, log at a lower/benign level, DO NOT close the relay, continue to next event on the SAME connection.
    - publishOne returns an error AND (relay.IsConnected() is false OR errors.Is(err, context.DeadlineExceeded)): treat as transport/timeout failure — increment publishErrors, log, close relay + set relay=nil + connected.Store(false) so the next loop iteration reconnects.
    - Metrics() snapshot includes Rejected reflecting the rejected atomic counter.
    - runMetrics log line includes a rejected=%d field.
  </behavior>
  <action>
    Import the standard `errors` package alongside the existing imports.

    Add an atomic counter field `rejected atomic.Uint64` to the Publisher struct, placed next to `publishErrors`. Add a `Rejected uint64` field to the Metrics struct next to `PublishErrors`, and populate it in `Metrics()` from `p.rejected.Load()`.

    In `runDrain`, replace the current post-publishOne error handling (the branch that unconditionally closes the relay) with classification. When `publishOne` returns a non-nil error, compute whether the connection is unusable using the condition: connection lost OR deadline exceeded — i.e. `!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)`. If unusable: increment `publishErrors`, log the failure, then `relay.Close()`, set `relay = nil`, and `connected.Store(false)` (preserving the existing force-reconnect-on-next-iteration behavior). Otherwise (benign application-level OK:false on a live connection): increment `rejected`, log at a benign/quieter level distinct from the transport-failure log, and do NOT touch the connection — fall through to the next loop iteration so the same relay keeps serving. Keep the OK:true path incrementing `published` as-is.

    Add a short comment documenting the accepted tradeoff: reconnecting on every publish timeout can itself add churn if the relay is merely slow under load, but this is the operator decision (a stuck/slow connection is worth recycling) and must not be silently changed. Classify a benign rejection as exactly the complement of the reconnect condition so the two branches stay consistent.

    In `runMetrics`, add a `rejected=%d` field to the periodic metrics log line, wiring `m.Rejected`.

    Do not change: connect(), publishOne() signature/body, backpressure/Enqueue, Stop/idempotency, reconnect backoff, or the reconnectCount increment on successful (re)connect.
  </action>
  <verify>
    <automated>cd /Users/g/git/deepfry/whitelist-plugin && go build ./... && go vet ./pkg/quarantine/</automated>
  </verify>
  <done>Package builds and vets clean. Publisher struct has a `rejected atomic.Uint64`; Metrics struct has `Rejected uint64` populated in Metrics(); runDrain reconnects only when `!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)` and otherwise increments `rejected` without closing the relay; runMetrics log line includes `rejected=%d`.</done>
</task>

<task type="auto">
  <name>Task 2: Extend fakeRelay + regression test for no-churn on OK:false</name>
  <files>whitelist-plugin/pkg/quarantine/publisher_test.go</files>
  <action>
    Extend the existing `fakeRelay` harness so it can reply with `OK:false` and a rejection reason. Add a configurable field (mirror the existing `rejectAll atomic.Bool` idiom) that, when set, makes `handle` write an OK reply of the form `["OK", evt.ID, false, "replaced: have newer event"]` while keeping the connection open and continuing the read loop. Preserve the existing default OK:true behavior when the flag is unset, and keep the existing `rejectAll` path working (it already writes `ok=false` with an empty reason; the new capability adds a non-empty reason and, importantly, exercises the code path where the publisher must NOT reconnect). When replying OK:false, do NOT push onto `received` (matching the existing `if ok` guard).

    Add a new test `TestPublisher_NoReconnectOnBenignRejection` following the file's existing idioms (httptest fakeRelay via `newFakeRelay`, `NewPublisher` with `MetricsInterval: time.Hour`, `p.Start(ctx)` / `defer p.Stop(time.Second)`, and the `waitFor` poll helper). Configure the relay to reply OK:false. Record the initial `Metrics().ReconnectCount` after the first connection is established (poll until `Metrics().Connected` is true or ReconnectCount reaches 1). Enqueue a stream of several events. Assert with `waitFor` that `Metrics().Rejected` rises to at least the number enqueued. Then assert that `Metrics().ReconnectCount` has NOT increased beyond its post-initial-connect value (the core regression guard: benign rejections keep the SAME connection). Optionally assert `Metrics().PublishErrors` stayed at 0 and `Metrics().Connected` remained true to prove the connection was retained.

    Do not modify the existing tests except as strictly required by the fakeRelay field addition; `TestPublisher_ReconnectAfterRelayDrops`, `TestPublisher_HappyPath`, `TestPublisher_BackpressureDrops`, `TestPublisher_StopIsIdempotent`, `TestPublisher_MetricsTickerAndContextCancel`, and `TestPublisher_DefaultsApplied` must remain valid and passing.
  </action>
  <verify>
    <automated>cd /Users/g/git/deepfry/whitelist-plugin && go test ./pkg/quarantine/ -count=1 -race -run 'TestPublisher' -v</automated>
  </verify>
  <done>fakeRelay can reply OK:false with a reason while keeping the connection open. `TestPublisher_NoReconnectOnBenignRejection` passes: Rejected rises to >= events enqueued while ReconnectCount does not increase past the initial connect and PublishErrors stays 0. All pre-existing publisher tests still pass under -race.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| publisher → quarantine relay (WS) | Publisher sends untrusted-origin quarantined events to a relay it does not control; relay responses (OK:true/false, connection state) are the only inputs back |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-qxq-01 | Denial of Service | runDrain reconnect loop | mitigate | Fix removes self-inflicted reconnect churn on OK:false; genuine drops/timeouts still reconnect with existing backoff (nextBackoff up to maxReconnectDelay), so no new unbounded reconnect path is introduced |
| T-qxq-02 | Tampering | relay OK-reply reason string | mitigate | Classification relies on `relay.IsConnected()` + `errors.Is(err, context.DeadlineExceeded)`, NOT on string-matching the "msg:" reason, so a malicious/garbage reason cannot force or suppress reconnects |
| T-qxq-03 | Information disclosure | benign-rejection log line | accept | Log line contains event ID + reason only (no secrets/keys); event IDs are public Nostr identifiers |
| T-qxq-SC | Tampering | npm/pip/cargo installs | accept | No new dependencies added; only the stdlib `errors` package is imported |
</threat_model>

<verification>
- `cd /Users/g/git/deepfry/whitelist-plugin && go build ./...` clean.
- `cd /Users/g/git/deepfry/whitelist-plugin && go test ./pkg/quarantine/ -count=1 -race` passes (all tests, including the new regression test and the preserved drop-triggers-reconnect test).
- Manual read-through confirms reconnect happens only under `!relay.IsConnected() || errors.Is(err, context.DeadlineExceeded)`.
</verification>

<success_criteria>
- Benign OK:false stream does not increase ReconnectCount and keeps the same connection (new test proves it).
- Real transport drop and publish timeout still reconnect (existing TestPublisher_ReconnectAfterRelayDrops still passes).
- `publishErrors` counts only transport/timeout failures; `rejected` counts benign OK:false; `published` counts OK:true — all three surfaced in Metrics() and the runMetrics log line.
- All work confined to whitelist-plugin/pkg/quarantine/; no other subproject touched; no new dependencies beyond stdlib `errors`.
</success_criteria>

<output>
Create `.planning/quick/260703-qxq-fix-quarantine-publisher-only-reconnect-/260703-qxq-SUMMARY.md` when done.
</output>
