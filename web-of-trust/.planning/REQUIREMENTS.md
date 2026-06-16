# Requirements: Web-of-Trust Crawler — v1.4 Crawler Hang Fix (Relay-Query Liveness)

**Defined:** 2026-06-16
**Core Value:** The crawler must continuously expand the web of trust — and to do that it must keep making progress every batch, never wedging indefinitely on a single misbehaving relay.

## v1.4 Requirements

A production run hung for ~48 minutes at 0% CPU. A SIGQUIT goroutine dump confirmed the root cause: go-nostr v0.52.0's `Subscription.Fire()` blocks on a context-ignoring channel receive, and `FetchAndUpdateFollows` gates batch exit on `wg.Wait()` / `eventsChan` close — so one stuck per-relay goroutine freezes the whole dispatcher. The relay-query timeout fires but cannot unblock it. Full analysis, evidence, and fix options: `web-of-trust/HANG-FINDINGS.md`.

### Liveness

- [x] **HANG-01**: When one or more per-relay query goroutines never return, `FetchAndUpdateFollows` still returns within a small bounded multiple of its relay-query timeout (`c.timeout`). The dispatcher must not gate batch completion on every query goroutine finishing (`wg.Wait()` / `eventsChan` close). _(fix #1 — core)_
- [x] **HANG-02**: A per-relay query (`queryRelay`) returns when the relay-query context expires, even when the underlying `relay.Subscribe` / go-nostr `Fire()` ignores that context and blocks on its relay write queue. _(fix #2)_

### Hardening

- [x] **HANG-03**: Relay connections enforce a bounded write deadline (or equivalent keepalive/ping timeout) so a half-open peer cannot park a relay's single write-loop goroutine — and thereby its subscriptions — indefinitely. _(fix #3 — hardening)_

### Testing & Verification

- [x] **TEST-02**: The existing regression test `TestFetchAndUpdateFollows_ReturnsWhenRelayQueryBlocks` (pkg/crawler/crawler_hang_test.go) passes, and the full `make test` suite for the web-of-trust module is green. The test injects a relay query that blocks while ignoring its context (faithfully reproducing go-nostr's `Fire()`) and asserts `FetchAndUpdateFollows` returns within budget. It is RED on pre-fix code and must go GREEN as the milestone's acceptance gate.

## Future Requirements

Deferred to a future milestone. Tracked but not in the current roadmap.

- **TUNE-01**: Config-driven retry backoff (carried from v1.3).
- **DISC / SEC / TEST-05 / IN-01/02/04**: v1.2 backlog, still deferred.

## Out of Scope

| Feature | Reason |
|---------|--------|
| Forking go-nostr / upstreaming a context-aware `Fire()` | We work around the library behavior locally (fix #1/#2); an upstream fix is out of this module's control and not required to stop the hang. |
| Reworking the EOSE-quorum early-exit logic (TIMEOUT-02) | The quorum path is orthogonal; the hang is the `wg.Wait()` gate, not quorum. Touch only if the liveness fix requires it. |
| Changing relay health / auto-ejection thresholds (RELAY-01/02/03) | Ejection already works; liveness is a separate dispatcher concern. A permanently-stuck relay may additionally warrant ejection, but that is a follow-up, not a v1.4 gate. |
| Any change to StrFry | Protocol rule: StrFry stays unmodified. |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| HANG-01 | Phase 11 | Complete |
| HANG-02 | Phase 11 | Complete |
| HANG-03 | Phase 11 | Complete |
| TEST-02 | Phase 11 | Complete |

**Coverage:**

- v1.4 requirements: 4 total
- Mapped to phases: 4 / 4
- Unmapped: 0

---
*Requirements defined: 2026-06-16*
