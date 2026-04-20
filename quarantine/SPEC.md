# Specification: Quarantine Routing for Non-Whitelisted Events

Status: Draft for review
Scope: MVP — data-gathering routing path. Classifier, review UI, and parallel-WoT detection are covered as future extensions but are NOT implemented by this spec.

**Home directory.** This document lives at `quarantine/SPEC.md`. The `quarantine/` directory is the eventual home of operational scripts, retention jobs, and follow-up specs for the quarantine subsystem. No code lives there in the MVP; the router plugin and new packages live under `whitelist-plugin/` (see §6.1).

---

## 1. Background and motivation

### 1.1 Current behaviour

DeepFry surrounds a stock StrFry relay with a `writePolicy` plugin that enforces web-of-trust membership. The path today:

```
Upstream relays ──► event-forwarder ──► StrFry main (port 7777) ──► whitelist plugin
                                                                      │
                                                                      ├─ pubkey ∈ WoT ─► accept → LMDB
                                                                      └─ pubkey ∉ WoT ─► reject (event discarded)
```

Code references:
- Plugin entry loop: `whitelist-plugin/cmd/whitelist/main.go:44-89`
- Decision logic: `whitelist-plugin/pkg/handler/whitelist_handler.go:16-34`
- Whitelist check: `whitelist-plugin/pkg/whitelist/whitelist.go:22-36`
- StrFry config: `config/strfry/strfry.conf:96-102`

Rejected events are dropped without ever being inspected, written, or counted beyond a log line.

### 1.2 Problem

Because rejected events are lost, DeepFry cannot answer any of these questions:
1. What fraction of rejected pubkeys *should* have been whitelisted?
2. Is there a parallel web of trust (a connected follow-cluster) the crawler's seed never reached?
3. What are common spam patterns hitting the relay?
4. Does our whitelist's freshness lag cause false negatives during crawl cycles?

Without a persisted corpus of rejected events, every future decision about the whitelist, the crawler seed, or a spam/review pipeline is blind.

### 1.3 Goal

Capture non-whitelisted events in a queryable form so the operator can **observe** them, and build a foundation the later classifier / review / WoT analyser work will plug into. The MVP does not classify, does not promote, does not present a review UI. It just routes.

---

## 2. Goals and non-goals

### 2.1 Goals (in scope for this spec)

- G1. All non-whitelisted events that pass a minimal garbage filter are persisted to a queryable Nostr relay ("quarantine").
- G2. Main StrFry continues to reject non-whitelisted events at the NIP-01 layer, unchanged from today's behaviour.
- G3. Existing `whitelist` plugin binary keeps building, keeps passing its current tests, and remains a drop-in replacement for the router plugin if we need to roll back.
- G4. Plugin decision latency is not materially affected — the quarantine publish is strictly off the critical path.
- G5. Quarantine publish failures (connection loss, queue full, upstream errors) must never cause the plugin to return an error to StrFry.
- G6. Zero event payloads persisted outside StrFry's LMDB (consistent with `CLAUDE.md` data-separation rule).

### 2.2 Non-goals (explicitly deferred)

- N1. Spam classification beyond trivial garbage rejection.
- N2. Human review UI or API.
- N3. Parallel-WoT detection / cluster analysis.
- N4. Decision logic for "promote this pubkey to the whitelist" — we are gathering data first.
- N5. Long-term retention guarantees for quarantine events (7-day rolling window is acceptable).
- N6. Horizontal scaling / HA — single-instance quarantine is fine for MVP.
- N7. Public access to the quarantine relay — it is internal-only.

---

## 3. Functional requirements

| ID | Requirement |
|----|-------------|
| FR-1 | A new StrFry `writePolicy` plugin binary (`router`) shall read events from stdin and emit accept/reject decisions on stdout using the same JSONL protocol as the current `whitelist` plugin. |
| FR-2 | For each event, the router shall query the whitelist server (reusing `whitelist-plugin/pkg/client`) and emit `accept` iff the pubkey is whitelisted. |
| FR-3 | For each non-whitelisted event, the router shall apply a filter (§6.3) that keeps only kinds 0, 1, and 3 and drops malformed or oversized events. Events that pass shall be enqueued for publication to the quarantine relay. |
| FR-4 | Enqueueing shall be non-blocking. If the internal queue is full, the event shall be dropped and a counter shall be incremented. The plugin response to StrFry shall not be delayed. |
| FR-5 | A single background goroutine shall maintain a persistent WS connection to the quarantine relay and publish queued events. It shall reconnect with exponential backoff on failure (cap: 30s). |
| FR-6 | A second StrFry instance (`strfry-quarantine`) shall run on the docker network at port 7778, without a writePolicy plugin (accept-all), not exposed to the host's public interface. |
| FR-7 | The router plugin's decision and side-effects shall be logged to stderr with enough detail to reconstruct per-event outcomes: event id, pubkey prefix, decision, quarantined (y/n), dropped (y/n). |
| FR-8 | The existing `whitelist` plugin binary shall continue to build and function identically to today. No removal, no breaking changes to its packages. |
| FR-9 | The router plugin shall have a `QUARANTINE_ENABLED=false` escape hatch that makes it behave byte-identically to the existing whitelist plugin (for emergency parity rollback without swapping binaries). |
| FR-10 | The quarantine StrFry container MUST refuse to start if its configured `db` path matches the mainline StrFry's `db` path or resolves to the same on-disk location. This is a hard safety guarantee to protect mainline data. |
| FR-11 | The quarantine StrFry container MUST NOT have the mainline StrFry's data volume mounted into it under any path. Defense in depth against accidental misconfiguration. |

---

## 4. Non-functional requirements

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1 | Router plugin p99 response latency (stdin line in → stdout line out) | ≤ 2 ms beyond whitelist server round-trip (i.e. the quarantine path adds essentially no latency) |
| NFR-2 | Memory: in-flight quarantine queue | Bounded; default 10,000 events × ~8 KiB avg = ~80 MiB worst case |
| NFR-3 | Quarantine relay disk usage | Bounded by LMDB + retention (see §9) |
| NFR-4 | Rollback time from router → whitelist | ≤ 1 minute (single config line + restart) |
| NFR-5 | Test coverage for new packages | ≥ 80% line coverage on `pkg/heuristics`, `pkg/quarantine`, new handler |

---

## 5. Architecture

### 5.1 Data-flow diagram

```
                ┌────────────────────┐
 Upstream relays │                   │
      │         │                   │
      ▼         │                   ▼
event-forwarder ├─────────► StrFry main :7777 ──(stdin JSONL)──► router plugin ──(stdout JSONL)──►┐
      │         │                                                      │                          │
Direct clients  │                                                      │ 1. check whitelist        │
      │         │                                                      │ 2. if whitelisted: accept │
      ▼         │                                                      │ 3. else run heuristics    │
                │                                                      │ 4. if pass: enqueue       │
                │                                                      │ 5. reject (reason: NotInWoT)
                │                                                      ▼
                │                                           ┌──────────────────┐
                │                                           │ publisher (async)│
                │                                           │ bounded chan     │
                │                                           │ one goroutine    │
                │                                           └────────┬─────────┘
                │                                                    │ go-nostr Publish
                │                                                    ▼
                │                                           StrFry quarantine :7778
                │                                           (docker-network only,
                │                                            no writePolicy plugin)
                │
                └── Dgraph (whitelist source)  ◄── whitelist-server ◄── router plugin (reads)
```

### 5.2 Component inventory

| Component | State | Location |
|-----------|-------|----------|
| event-forwarder | Unchanged | `event-forwarder/` |
| StrFry main | Unchanged code; config line changed | `config/strfry/strfry.conf:99` |
| whitelist plugin (existing) | Unchanged, retained | `whitelist-plugin/cmd/whitelist/` |
| **router plugin (new)** | New binary | `whitelist-plugin/cmd/router/` |
| whitelist-server | Unchanged | `whitelist-plugin/pkg/server/` |
| Dgraph | Unchanged schema | `config/dgraph/schema.graphql` |
| web-of-trust crawler | Unchanged | `web-of-trust/` |
| **strfry-quarantine (new)** | New docker service | `docker-compose.strfry.yml` |

---

## 6. Detailed design

### 6.1 Module layout (inside `whitelist-plugin/`)

```
cmd/
  whitelist/main.go               ← unchanged
  router/main.go                  ← NEW: router plugin entry point

pkg/
  client/                         ← unchanged (whitelist server HTTP client)
  config/
    config.go                     ← unchanged
    router_config.go              ← NEW
  handler/
    handler.go                    ← unchanged
    jsonl_io_adapter.go           ← unchanged
    messages.go                   ← additive change (new RouterInputMsg type)
    whitelist_handler.go          ← unchanged
    router_handler.go             ← NEW
  heuristics/                     ← NEW package
    heuristics.go
    heuristics_test.go
  quarantine/                     ← NEW package
    publisher.go
    publisher_test.go
  repository/                     ← unchanged
  server/                         ← unchanged
  whitelist/                      ← unchanged
```

### 6.2 Router plugin entry point (`cmd/router/main.go`)

Structurally a copy of `cmd/whitelist/main.go:15-42` with three additions:
1. Load `RouterConfig` instead of `ClientConfig`.
2. Construct a `quarantine.Publisher`, start its background goroutine, defer graceful shutdown on signal.
3. Instantiate `RouterHandler` wrapping the whitelist checker + publisher.

Signal handling: on SIGINT/SIGTERM the main goroutine cancels context; `runEventLoop` exits; publisher drains its buffer with a bounded timeout (e.g. 2 s) then closes the WS connection. This matches current plugin shutdown semantics.

### 6.3 Heuristics package (`pkg/heuristics/`)

**Purpose:** pre-quarantine garbage gate. Drops obvious junk so quarantine LMDB does not fill with noise. Intentionally *not* a spam classifier — this is the "is this even a meaningful event" filter.

```go
// heuristics.go
package heuristics

type Result struct {
    Keep   bool
    Reason string // empty when Keep == true; otherwise e.g. "content_too_large"
}

// Filter applies the MVP garbage gate to a fully-parsed Nostr event.
func Filter(evt nostr.Event) Result
```

MVP rules:
- `evt.Kind ∉ {0, 1, 3}` → drop, `kind_not_allowed`
- `len(evt.Content) > 256*1024` → drop, `content_too_large`
- `evt.PubKey == "" || evt.ID == ""` → drop, `missing_required_fields`
- Otherwise → keep.

**Kind allowlist rationale.** Quarantine is a data-gathering substrate for later parallel-WoT analysis and community discovery. Only three kinds carry signal for that purpose:
- `0` — profile metadata (who the pubkey claims to be)
- `1` — short text note (what the pubkey publishes)
- `3` — contacts / follow list (how the pubkey connects the graph)

Every other kind (reactions, zaps, deletions, DMs, etc.) is noise for this exercise and would bloat LMDB without contributing to the open questions in §1.3. Tightening can still happen after we look at the kept data; widening requires revisiting this allowlist.

Tests: table-driven, one row per rule + an "everything normal → keep" case.

### 6.4 Quarantine publisher (`pkg/quarantine/`)

```go
// publisher.go
package quarantine

type Publisher struct {
    relayURL  string
    queue     chan *nostr.Event  // buffered
    metrics   struct {
        enqueued atomic.Uint64
        dropped  atomic.Uint64
        published atomic.Uint64
        publishErrors atomic.Uint64
    }
    // ...
}

func NewPublisher(relayURL string, bufferSize int, logger *log.Logger) *Publisher
func (p *Publisher) Start(ctx context.Context)
func (p *Publisher) Enqueue(evt *nostr.Event)   // non-blocking; drops when full
func (p *Publisher) Stop(timeout time.Duration) // drains then closes
func (p *Publisher) Metrics() Metrics           // snapshot for logging
```

Implementation notes:
- Single goroutine drains `queue` and calls `relay.Publish(ctx, evt)`. Single goroutine + single persistent WS keeps ordering and avoids connection churn.
- Connect uses `go-nostr` (already a project dependency via `event-forwarder` and `web-of-trust`).
- Reconnect on publish error: close relay, exponential backoff starting at 500 ms, cap 30 s, infinite retries. While disconnected, events continue to enqueue (or drop when full).
- `Enqueue` uses `select { case queue <- evt: ...enqueued++... ; default: ...dropped++... }` — never blocks the plugin.
- Periodic (every 60 s) log line with metrics snapshot so operators can eyeball queue health from `docker logs`.

Tests:
- Happy path: enqueue N events → publisher publishes N to fake relay.
- Backpressure: fill buffer, confirm `dropped` counter increments and `Enqueue` returns immediately.
- Reconnect: kill the fake relay mid-run, restart, confirm subsequent events publish.

### 6.5 Router handler (`pkg/handler/router_handler.go`)

```go
type RouterHandler struct {
    checker    Checker            // same interface as WhitelistHandler
    publisher  *quarantine.Publisher
    logger     *log.Logger
    qEnabled   bool               // mirrors QuarantineEnabled config
}

func (h *RouterHandler) Handle(input RouterInputMsg) (OutputMsg, error) {
    evt, err := input.ParseFullEvent()
    if err != nil {
        return RejectMalformed(), nil
    }
    if h.checker.IsWhitelisted(evt.PubKey) {
        return Accept(evt.ID), nil
    }
    if h.qEnabled {
        if res := heuristics.Filter(evt); res.Keep {
            h.publisher.Enqueue(&evt)
        } else {
            h.logger.Printf("drop id=%s reason=%s", evt.ID, res.Reason)
        }
    }
    return Reject(evt.ID, RejectReasonNotInWoT), nil
}
```

### 6.6 Message type extension (`pkg/handler/messages.go`)

Additive only — existing `InputMsg` is not modified:

```go
// RouterInputMsg mirrors the StrFry plugin input but keeps the full event
// payload so the router can forward it to quarantine. The whitelist plugin
// continues to use InputMsg (id + pubkey only).
type RouterInputMsg struct {
    Type       string          `json:"type"`
    Event      json.RawMessage `json:"event"`
    ReceivedAt int64           `json:"receivedAt"`
    SourceType SourceType      `json:"sourceType"`
    SourceInfo string          `json:"sourceInfo"`
}

func (i *RouterInputMsg) ParseFullEvent() (nostr.Event, error)
```

`ParseFullEvent` unmarshals the raw event into a `go-nostr` `Event` (the project already has `go-nostr` on the dependency graph). The router handler uses this. The existing whitelist handler continues to use `InputMsg.ParseEvent()` unchanged.

A new `IOAdapter` variant may be needed (`jsonl_io_adapter.go` currently returns `InputMsg`). Cleanest approach: parameterise the adapter over the message type via generics, or add a sibling `RouterIOAdapter` implementing the same `Input/Output` shape. Either is fine; the adapter logic is trivial.

### 6.7 Router config (`pkg/config/router_config.go`)

Loaded from `~/deepfry/router.yaml` (following the convention in `CLAUDE.md`).

```yaml
# router.yaml
server_url: "http://whitelist-server:8081"   # whitelist HTTP server
check_timeout: "500ms"

quarantine:
  enabled: true
  relay_url: "ws://strfry-quarantine:7778"
  buffer_size: 10000
  publish_timeout: "5s"
```

Env overrides (following existing pattern): `ROUTER_QUARANTINE_ENABLED`, `ROUTER_QUARANTINE_RELAY_URL`, etc.

### 6.8 Quarantine StrFry service

#### 6.8.1 Config file

New file: `config/strfry/strfry-quarantine.conf`. Derived from `config/strfry/strfry.conf` with these overrides:

```
db = "/app/strfry-quarantine-db/"     # MUST differ from mainline's db path
relay {
    bind = "0.0.0.0"                  # docker network only; no host port published
    port = 7778
    info {
        name = "deepfry-quarantine"
        description = "Internal quarantine for non-whitelisted events. Not for public use."
    }
    writePolicy {
        plugin = ""                   # accept-all
        lookbackSeconds = 0
    }
}
```

The path `/app/strfry-quarantine-db/` is deliberately distinct from mainline's `/app/strfry-db/` (or whichever path mainline uses — to be read from `config/strfry/strfry.conf` at implementation time and hardcoded into the guard script in §6.8.3).

#### 6.8.2 Volume layout

The quarantine container is mounted with **only its own quarantine volume**. The mainline volume is never referenced in the quarantine service definition. If an operator were to copy the wrong `db = ...` path into the config, the container would still be unable to touch mainline data because the mainline volume is not present inside it.

```yaml
services:
  strfry-quarantine:
    image: <same strfry image>
    restart: unless-stopped
    entrypoint: ["/usr/local/bin/quarantine-db-guard.sh"]
    command: ["strfry", "--config=/etc/strfry.conf", "relay"]
    environment:
      QUARANTINE_EXPECTED_DB: "/app/strfry-quarantine-db/"
      MAINLINE_DB_PATH: "/app/strfry-db/"   # (or whichever path mainline uses)
    volumes:
      - ./config/strfry/strfry-quarantine.conf:/etc/strfry.conf:ro
      - ./config/strfry/quarantine-db-guard.sh:/usr/local/bin/quarantine-db-guard.sh:ro
      - strfry-quarantine-db:/app/strfry-quarantine-db
      # NOTE: the mainline strfry-db volume is intentionally NOT listed here.
      # Adding it here would be a destructive misconfiguration.
    expose:
      - "7778"                          # NOT `ports:` — internal only
    networks: [deepfry]

volumes:
  strfry-quarantine-db:                 # distinct from the mainline volume name
```

The router plugin container joins the same `deepfry` network and reaches the quarantine strfry via service-DNS.

#### 6.8.3 DB isolation guard (fail-fast on misconfig)

Per FR-10, the container MUST refuse to start if the configured DB path could collide with mainline. Implemented as an entrypoint script that runs before strfry:

New file: `config/strfry/quarantine-db-guard.sh`

```sh
#!/bin/sh
set -eu

CONF=/etc/strfry.conf
EXPECTED="${QUARANTINE_EXPECTED_DB:?QUARANTINE_EXPECTED_DB is required}"
FORBIDDEN="${MAINLINE_DB_PATH:?MAINLINE_DB_PATH is required}"

# Extract the db = "..." value (first uncommented match) and normalize trailing slash.
CONFIGURED=$(
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*db[[:space:]]*=/ {
      match($0, /"[^"]*"/)
      if (RSTART > 0) {
        print substr($0, RSTART+1, RLENGTH-2)
        exit
      }
    }
  ' "$CONF"
)

if [ -z "$CONFIGURED" ]; then
  echo "FATAL: could not determine db path from $CONF" >&2
  exit 1
fi

# Normalize both sides: ensure trailing slash, resolve ../ etc.
normalize() {
  p="$1"
  case "$p" in */) ;; *) p="$p/" ;; esac
  # Resolve to an absolute canonical path if possible.
  d=$(dirname "$p")
  b=$(basename "$p")
  if [ -d "$d" ]; then
    printf '%s/%s/\n' "$(cd "$d" && pwd -P)" "$b"
  else
    printf '%s\n' "$p"
  fi
}

CONFIGURED_N=$(normalize "$CONFIGURED")
EXPECTED_N=$(normalize "$EXPECTED")
FORBIDDEN_N=$(normalize "$FORBIDDEN")

if [ "$CONFIGURED_N" = "$FORBIDDEN_N" ]; then
  echo "FATAL: quarantine db path ($CONFIGURED_N) matches mainline db path ($FORBIDDEN_N). Refusing to start." >&2
  exit 2
fi

if [ "$CONFIGURED_N" != "$EXPECTED_N" ]; then
  echo "FATAL: quarantine db path ($CONFIGURED_N) does not match expected ($EXPECTED_N). Refusing to start." >&2
  exit 3
fi

# Also bail if the forbidden path exists inside the container — it should not be mounted here at all.
if [ -e "$FORBIDDEN_N" ]; then
  echo "FATAL: mainline db path ($FORBIDDEN_N) is visible inside the quarantine container. Refusing to start." >&2
  exit 4
fi

exec "$@"
```

Guard contract:
- Exit 2 → configured path equals the known mainline path (identity check).
- Exit 3 → configured path does not equal the expected quarantine path (typo / drift check).
- Exit 4 → the mainline path exists inside the container filesystem, meaning someone added the wrong volume mount (defense-in-depth check).
- Exit 0 (via `exec`) only when all three checks pass.

The compose service's `restart: unless-stopped` will attempt to restart the container on a nonzero exit, but each attempt will re-fail. Operators see the reason loud and clear in `docker logs strfry-quarantine`.

#### 6.8.4 Test matrix for the guard

These go in `config/strfry/quarantine-db-guard_test.sh` (a small shellcheck/bats suite), exercised in CI:

1. Normal config → exit 0, exec reached (assert via a mock `exec`).
2. Config's `db = "/app/strfry-db/"` (collision) → exit 2.
3. Config's `db = "/app/some-other-path/"` → exit 3.
4. Touch `${MAINLINE_DB_PATH}/data.mdb` before running → exit 4.
5. Missing `QUARANTINE_EXPECTED_DB` env var → exit 1 (set -u).
6. Commented-out `db` line + real one below → the uncommented one wins.

---

## 7. Message contracts

### 7.1 StrFry → plugin (stdin, unchanged)

StrFry already sends the full event JSON; we are just parsing more of it. Sample line:

```json
{"type":"new","event":{"id":"...","pubkey":"...","created_at":1735000000,"kind":1,"tags":[],"content":"hello","sig":"..."},"receivedAt":1735000000,"sourceType":"IP4","sourceInfo":"1.2.3.4"}
```

### 7.2 Plugin → StrFry (stdout, unchanged)

Same JSONL schema the existing plugin emits — see `whitelist-plugin/pkg/handler/messages.go:61-65`:

```json
{"id":"<eventId>","action":"accept","msg":""}
{"id":"<eventId>","action":"reject","msg":"rejected: not in web of trust"}
```

### 7.3 Router → quarantine relay (new)

Standard NIP-01 `EVENT` message over a persistent WS connection:

```json
["EVENT", {"id":"...","pubkey":"...","kind":1, ... "sig":"..."}]
```

`go-nostr`'s `Relay.Publish` handles the wire format.

---

## 8. Failure modes and handling

| Failure | Detection | Handling |
|---------|-----------|----------|
| Whitelist server unreachable | Client returns error from `IsWhitelisted` | Router returns `Reject` with `RejectReasonInternal` — same as current whitelist plugin. No quarantine publish. |
| Quarantine relay unreachable | Publisher's WS dial fails | Publisher retries with backoff; events continue to enqueue; drop when full. Main strfry's decision path is unaffected. |
| Queue full | `Enqueue` `default:` branch | Event dropped, `dropped` counter++, periodic log line surfaces the rate. |
| Malformed event JSON | `ParseFullEvent` returns error | Router returns `RejectMalformed` (existing helper). |
| Quarantine strfry container dies | `docker ps` / container health | Compose restarts it; publisher reconnects on next retry. |
| Quarantine config drift → DB path collision | Guard script exits 2/3/4 on startup | Container crashloops with explicit error in `docker logs`. Mainline data is never touched. See §6.8.3. |
| StrFry main dies | n/a | Out of scope — same story as today. |
| Plugin process dies | StrFry restarts it | Publisher's in-flight queue is lost. Acceptable for MVP — this is data-gathering, not an audit log. |

**Design principle:** every failure in the quarantine path degrades quarantine freshness or completeness, but never affects the main-strfry accept/reject decision.

---

## 9. Operational concerns

### 9.1 Deployment

Both plugins ship in the strfry container image at `/app/plugins/whitelist` and `/app/plugins/router`. Swap by editing `config/strfry/strfry.conf:99`:

```diff
-plugin = "/app/plugins/whitelist"
+plugin = "/app/plugins/router"
```

Restart the strfry container. Rollback = revert the line, restart. NFR-4 (≤ 1 minute) is met trivially.

### 9.2 Quarantine retention

LMDB grows unbounded by default. Add a simple cron-style cleanup: a sidecar container (or host cron) that runs `strfry delete --age=604800` (7 days) nightly against the quarantine db. Out of scope to automate in this spec — document as a day-2 operational task.

### 9.3 Observability

- Router stderr: one log line per event (already the pattern in the existing plugin — `cmd/whitelist/main.go:92`). Add decision + quarantine outcome fields.
- Publisher metrics log: every 60 s, one line with `enqueued / dropped / published / publish_errors` counters.
- StrFry's own stats unchanged.
- No new metrics endpoint for MVP. If/when Prometheus is added project-wide, expose the publisher counters there.

### 9.4 Security

- Quarantine relay is not exposed to the public internet — `expose:` rather than `ports:` in compose, and binds to the docker-internal network.
- Quarantine events are unsigned-from-our-side (we forward the original signed event verbatim). No impersonation risk.
- No new secrets. The quarantine relay URL is not sensitive; it is docker-network DNS.
- Plugin stderr must not log full event content — pubkey prefix (8 chars) and event ID only, to avoid accidentally leaking content that would otherwise be rejected. Content is persisted in quarantine, not in logs.

### 9.5 Capacity

At expected traffic (reject rate × avg event size × 7-day retention):
- If reject rate = 10/s and avg event = 2 KiB → ~12 GiB / week in quarantine LMDB. Provision accordingly.
- Publisher buffer 10,000 events × ~8 KiB worst-case = ~80 MiB RAM ceiling.

These numbers are guesses; one job on day 2 is to revisit after a week of real traffic.

---

## 10. Test strategy

### 10.1 Unit tests (new)

- `pkg/heuristics/heuristics_test.go` — one test case per rule + happy path; target ≥ 80% line coverage.
- `pkg/quarantine/publisher_test.go` — uses a fake relay (in-process WS server) to assert enqueue → publish, backpressure drop, reconnect on disconnect. ≥ 80% line coverage.
- `pkg/handler/router_handler_test.go` — four cases: whitelisted → accept + no enqueue; non-whitelisted + heuristic keep → reject + enqueue; non-whitelisted + heuristic drop → reject + no enqueue; malformed → RejectMalformed.

### 10.2 Integration test

Runs against a real docker-compose stack (strfry main + strfry quarantine + whitelist server + dgraph + router plugin).

Scenarios:
1. Seeded-whitelisted pubkey publishes kind 1 → present on main (:7777), absent from quarantine (:7778).
2. Non-whitelisted pubkey publishes kind 1 → main rejects, quarantine has the event (verify with `nak req` or equivalent against :7778).
3. Non-whitelisted pubkey publishes 300 KB content event → both relays empty (heuristic drop).
4. Bring down strfry-quarantine container; publish 50 non-whitelisted events over 60 s; bring quarantine back up. Publisher reconnects; new events reach quarantine. (Accepted: events during outage may be lost — MVP is fire-and-forget.)
5. Flip `QUARANTINE_ENABLED=false`; behaviour is byte-identical to the existing whitelist plugin.
6. DB isolation guard (§6.8.4): run the guard script test matrix — normal, collision, drift, volume-leak, missing env. All six cases produce the expected exit codes.

### 10.3 Rollback test

Revert `config/strfry/strfry.conf:99` from `/app/plugins/router` to `/app/plugins/whitelist`; restart; assert current behaviour is preserved (existing whitelist integration tests all pass).

### 10.4 Regression

`whitelist-plugin`'s existing `make test` suite must still pass — the `cmd/whitelist/` binary and its packages are unchanged.

---

## 11. Rollout plan

| Phase | Action | Gate |
|-------|--------|------|
| P0 | Land code (router plugin + tests + compose changes). `cmd/whitelist/` unaffected. | CI green on all modules. |
| P1 | Deploy new compose: `strfry-quarantine` service comes up; router binary present in image. Main strfry still runs old plugin. | `strfry-quarantine` health OK; quarantine NIP-01 handshake works from another container. |
| P2 | Swap `config/strfry/strfry.conf:99` to `router`, restart main strfry. | For 1 hour, compare per-minute accept/reject rates against baseline. Rollback if deviation. |
| P3 | Observe. Let quarantine fill for a week. | Qualitative review of captured events informs classifier / analyser design. |

No schema migrations, no data migrations, no dependency upgrades. Every phase is independently revertable.

---

## 12. Open questions (non-blocking for MVP)

These do not gate this spec but should be captured before Phase P3 ends:

1. Quarantine retention: 7 days good enough, or do we need 30?
2. Do we want a separate reject reason in the main-strfry response so clients know "your event was quarantined for review" vs "flat-out rejected"? (Currently both return `rejected: not in web of trust`.) Changing this is visible to clients.
3. Publisher: should we log-rotate the publisher's 60 s metrics line, or emit to Prometheus once available?
4. Quarantine strfry's `maxFilterLimit` / `dbMaxSize` tuning — keep defaults for MVP?

---

## 13. Future extensions (out of scope, for context)

These follow naturally once quarantine has real data:

1. **Spam classifier service.** Sidecar that subscribes to quarantine :7778 with `since:now`, runs a rules-based or LLM classifier, writes classification results to a review queue. Classifier interface: `Classify(nostr.Event) (verdict, score)`.
2. **Human review queue + HTTP endpoint.** On-disk append log of `{event_id, pubkey, verdict, score, ts}`; `GET /review/pending`; `POST /review/:id/decide`. Events fetched by ID from quarantine strfry so payloads stay in LMDB.
3. **Parallel-WoT analyser.** Cron job using the existing `web-of-trust/pkg/crawler` to BFS from candidate pubkeys (those with recurring ham verdicts) to depth 2; compute connected-component overlap with the main whitelist set; flag low-overlap high-size clusters as parallel WoTs.
4. **Promote decision.** Once we have classification + review data, decide whether "promote" adds the pubkey to Dgraph's `Profile` type (reusing `web-of-trust` write path) or just single-event pass-through (requires a side-door that bypasses the plugin — likely via an admin tag on a known reviewer pubkey).
5. **Crawler seed expansion.** If parallel-WoT analyser finds a disconnected cluster, feed its seed pubkeys back into `web-of-trust/pkg/config/config.go`'s seed list.

Each of these is a separate spec.

---

## 14. Critical files

### New
- `quarantine/SPEC.md` — this document
- `config/strfry/quarantine-db-guard.sh` — fail-fast DB isolation guard (§6.8.3)
- `config/strfry/quarantine-db-guard_test.sh` — guard test matrix (§6.8.4)
- `whitelist-plugin/cmd/router/main.go`
- `whitelist-plugin/pkg/handler/router_handler.go`
- `whitelist-plugin/pkg/handler/router_handler_test.go`
- `whitelist-plugin/pkg/heuristics/heuristics.go`
- `whitelist-plugin/pkg/heuristics/heuristics_test.go`
- `whitelist-plugin/pkg/quarantine/publisher.go`
- `whitelist-plugin/pkg/quarantine/publisher_test.go`
- `whitelist-plugin/pkg/config/router_config.go`
- `config/strfry/strfry-quarantine.conf`

### Modified (additive only)
- `whitelist-plugin/pkg/handler/messages.go` — add `RouterInputMsg` type; `InputMsg` untouched
- `whitelist-plugin/pkg/handler/jsonl_io_adapter.go` — add router-message variant (or generic)
- `whitelist-plugin/Makefile` — add `build-router`, `build-router-alpine` targets
- `docker-compose.strfry.yml` — add `strfry-quarantine` service + volume

### Deploy-time only (not code)
- `config/strfry/strfry.conf:99` — swap plugin path at rollout

### Unchanged
- `whitelist-plugin/cmd/whitelist/` (and all of its dependent packages)
- `event-forwarder/`, `web-of-trust/`, `config/dgraph/`
