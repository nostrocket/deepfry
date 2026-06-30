# Phase 3: Bloom Gate Plugin - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-06-30
**Phase:** 3-Bloom Gate Plugin
**Areas discussed:** Config layout, Cold-start order, Persist strategy, Refresh failure

---

## Config layout

### Config file location

| Option | Description | Selected |
|--------|-------------|----------|
| Dedicated bloom.yaml | New `LoadBloomConfig()` binding `SetConfigName("bloom")`; clean namespace, no collision | |
| Reuse whitelist.yaml | Add `BloomConfig` struct + keys to the shared file; one file for ops to manage | ✓ |

**User's choice:** Reuse whitelist.yaml
**Notes:** Bloom plugin talks to the same whitelist server, so it shares the existing `server_url` key (D-02).

### Key names

| Option | Description | Selected |
|--------|-------------|----------|
| `bloom_`-prefixed keys | `bloom_refresh_interval` / `bloom_path` / `bloom_fetch_timeout`; unambiguous in shared file | ✓ |
| Bare keys | `refresh_interval` / `filter_path` / `fetch_timeout`; collides with server's Dgraph `refresh_interval` | |
| You decide | Planner picks names following existing style | |

**User's choice:** `bloom_`-prefixed keys
**Notes:** Avoids collision with the server's existing `refresh_interval`.

---

## Cold-start order

### Startup source order

| Option | Description | Selected |
|--------|-------------|----------|
| Disk-first, then bg refresh | Load persisted filter immediately (instant ready), then background conditional-GET swap | ✓ |
| Server-first, then disk | Synchronous `/bloom` fetch at startup; fall back to disk only if unreachable; blocks on network each restart | |

**User's choice:** Disk-first, then background refresh
**Notes:** Fastest to-ready; survives slow/down server on restart.

### Block semantics (neither source available)

| Option | Description | Selected |
|--------|-------------|----------|
| Wait-until-ready, hold events | Don't consume/respond to stdin until a filter arrives; retry server with backoff; fail-closed by withholding decisions | ✓ |
| Exit nonzero | Log fatal + exit; relies on config-dependent StrFry crash behavior | |
| Reject-all until ready | Actively reject every event; drops legit events during outage | |

**User's choice:** Wait-until-ready, hold events
**Notes:** Matches GATE-06 "emits no decisions"; no event decided without a filter.

---

## Persist strategy

### Write safety

| Option | Description | Selected |
|--------|-------------|----------|
| Temp + atomic rename | Write `.tmp` then `os.Rename`; crash leaves stale-valid old file or orphan tmp, never torn; persist on 200 only | ✓ |
| Direct overwrite | Write straight to `bloom.dfbf`; crash corrupts the only copy | |

**User's choice:** Temp + atomic rename

### Validate before persist

| Option | Description | Selected |
|--------|-------------|----------|
| Parse, then swap+persist | `ReadFilter` validates framing first; only clean parse → swap in-memory + write disk | ✓ |
| Persist raw, then parse | Write bytes first then parse; risks persisting a corrupt file | |

**User's choice:** Parse, then swap+persist
**Notes:** Guarantees the persisted file is always loadable (protects GATE-05).

---

## Refresh failure

### Staleness policy on failed mid-life refresh

| Option | Description | Selected |
|--------|-------------|----------|
| Keep last, serve forever | Retain last good in-memory filter indefinitely; log + retry next cycle; mirrors server refresher D-02 | ✓ |
| Keep last + staleness ceiling | Same, plus configurable age ceiling that escalates logging | |
| Keep last, decisions never degrade | Explicit: data ages but decisions never start rejecting on staleness | |

**User's choice:** Keep last, serve forever
**Notes:** ~6h whitelist staleness already tolerated system-wide; decisions never degrade.

### Retry cadence

| Option | Description | Selected |
|--------|-------------|----------|
| Few quick retries, then next cycle | Short-backoff retries (mirror server `refresh_retry_count`), else wait next interval | ✓ |
| Wait for next full interval | No intra-cycle retry; a blip costs up to 6h extra staleness | |
| You decide | Planner picks count/backoff following existing conventions | |

**User's choice:** Few quick retries, then next cycle

---

## Claude's Discretion

- Exact type/method/field names (`BloomChecker`, atomic-pointer holder, fetcher struct, `LoadBloomConfig` shape).
- Exact retry count / backoff durations (follow server refresher conventions).
- Implementation of the D-06 "ready" gate (channel / `sync.Once` / atomic).
- Whether the fetcher lives in `cmd/bloom` or a small reusable `pkg/`.
- Logging surface/cadence beyond the failures called out in D-10.

## Deferred Ideas

- Faster (minutes-scale) refresh — GATE-F1 (v2/future).
- Bloom metrics endpoint / hit-miss-accept counters / generation-age (and the optional staleness-ceiling escalation) — GATE-F2 (v2/future).
- Makefile / Docker / `strfry.conf` / README — Phase 4 (OPS-01..03).
