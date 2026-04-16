# Deepfry Whitelist Plugin

## Purpose

The Whitelist Plugin enforces write-policies in StrFry by checking if an event’s pubkey exists in the Web-of-Trust (WoT) allowlist. It integrates via StrFry’s JSON stdin/stdout plugin interface and must support a fake provider until the Dgraph WoT service is live.

## How It Works

The plugin runs as a long-lived process attached to StrFry via its stdin/stdout JSON plugin protocol. StrFry sends one JSON line per incoming event; the plugin replies with `accept` or `reject`.

### Event Loop

On each incoming event, the handler extracts the pubkey and performs a single hash-map lookup against an in-memory whitelist. This lookup is **lock-free** (`atomic.Pointer.Load`) and takes nanoseconds, so StrFry is never blocked waiting on the plugin.

### Background Refresh

The whitelist is populated from Dgraph on a separate goroutine:

1. **Startup** — a single blocking refresh runs before the event loop begins. No events are processed until the initial whitelist is loaded.
2. **Periodic** — a background goroutine wakes on a configurable ticker (default `5m`) and re-fetches all pubkeys from Dgraph via paginated GraphQL queries.
3. **Atomic swap** — once the new set is fully built, it replaces the old one in a single `atomic.Pointer.Store`. There is no partial-update window; readers either see the old complete set or the new complete set.

Because reads and writes use Go's `sync/atomic.Pointer`, the event loop and the refresh goroutine run fully concurrently with **no locks and no read stalls**. During a refresh (which may take seconds due to Dgraph pagination), all incoming events continue to be evaluated against the previous snapshot.

### Staleness

Between refreshes, the whitelist may be up to one `refresh_interval` behind Dgraph. A pubkey added to the web-of-trust graph will not be accepted until the next refresh completes.

### Failure Handling

If a refresh fails (Dgraph unreachable, timeout, etc.), the existing in-memory whitelist is preserved — the plugin continues accepting/rejecting based on the last successful snapshot. Failures are logged to stderr as structured JSON.

---

## Requirements Table

| Req ID | Description | Category | Acceptance Criteria | Trace |
|--------|-------------|----------|---------------------|-------|
| FR-01 | The plugin must implement StrFry’s stdin/stdout JSON plugin protocol. | Functional | For each input message with `type:new`, plugin returns JSON with `id`, `action`, and optional `msg`. Output is line-buffered JSONL. | StrFry docs |
| FR-02 | Only events from pubkeys on the whitelist are accepted. | Functional | Unit test: given `req.event.pubkey` ∉ whitelist, response `{action:"reject",msg:"blocked: not on whitelist"}`. | Subsys-06 |
| FR-03 | Whitelist provider must be an abstraction (interface) to allow fake/test and Dgraph-backed implementations. | Functional | Go interface defined (`GetAll() ([]string,error)`). Fake impl seeded in tests. Swap provider without changing handler logic. | Arch-01 |
| FR-04 | Provider errors must not crash plugin. Fallback: treat all as rejected. | Functional | Simulate provider failure; plugin logs warning; rejects new events. No panic. | QA |
| FR-05 | Unit tests for positive, negative, malformed input. | Functional | Coverage ≥90% lines/branches. CI pipeline enforces threshold. | QA |
| FR-06 | Structured logging for observability. | Functional | Logs to stderr JSON lines: `{level,time,msg}`. Errors in provider clearly marked. | Arch-01 |
| FR-07 | Cache whitelist in memory. | Functional | On startup, provider snapshot loaded into an in-process map; lookups are O(1). Unit test seeds provider and asserts cache hits after first call. | Subsys-06 |
| FR-08 | Async periodic refresh of cache. Interval configurable. Safe swap with no read stalls. | Functional | `refresh_interval` in config governs refresh cadence. Hot-swap uses RW lock; no blocked lookups >1ms p95 in benchmark at 10k EPS. Backoff on provider error with jitter. | Subsys-06 |
| FR-09 | Expose last-refresh metadata for observability. | Functional | `/metrics` or stderr log includes `whitelist_last_refresh_unix`, `whitelist_entries`. | Arch-01 |
| FR-10 | Benchmark tests for handler path and cache. | Functional | `go test -bench .` runs microbenchmarks for parse→authorize→emit and provider cache. CI stores results. | QA |
| NFR-01 | Plugin must be reloadable by StrFry when binary is replaced. | Non-Functional | Run integration: replace binary, next event triggers reload, no crash. | StrFry docs |
| NFR-02 | Must handle malformed JSON gracefully. | Non-Functional | Input: invalid JSON → stderr log, reject response. No crash. | QA |
| NFR-03 | Startup time <1s. | Non-Functional | Measure cold start with fake provider; <1000ms. | Perf test |
| NFR-04 | Fail closed by default. | Non-Functional | With empty whitelist, all events rejected. | Security |
| NFR-05 | No dependency on external DB until provider abstraction is swapped. | Non-Functional | Build/test binary links only stdlib and provider interface. | Arch-01 |
| NFR-06 | Throughput: handle 10,000 events/sec end-to-end in handler path (excluding stdio). | Non-Functional | Benchmark shows ≥10k ops/sec on target instance class for `Handler.Process` with cached provider. Note: strfry currently serializes plugin requests over a single stdin stream; true relay-level 10k EPS requires multi-process or future concurrent plugin IO. | Arch-01 |
| NFR-07 | Minimize CPU. | Non-Functional | `Handler.Process` allocs ≤2 per op; CPU ≤1 vCPU @ 10k ops/sec in benchmark on target sku. No regex; reuse buffers; precompute JSON encoder. | QA |
| NFR-08 | Minimize memory. | Non-Functional | RSS ≤128 MiB at steady state with 1M keys cached; memory usage linear with key count and documented (bytes/key). | QA |
| NFR-09 | Configurable refresh and timeouts. | Non-Functional | YAML config file (`~/deepfry/whitelist.yaml`) with viper; all timing parameters configurable with sane defaults. See Config Parameters below. | Arch-01 |
| NFR-10 | Resilience during refresh failure. | Non-Functional | On provider error cache is unchanged; logs warning; exponential backoff with jitter; next success atomically replaces snapshot. | Arch-01 |

---

## Configuration

Configuration is loaded from `~/deepfry/whitelist.yaml`. If no config file is found, a default one is created automatically.

### Parameters

```yaml
dgraph_graphql_url: "http://localhost:8080/graphql"  # Dgraph GraphQL endpoint
refresh_interval: "5m"                                # How often to refresh the whitelist from Dgraph
refresh_retry_count: 3                                # Number of retries on refresh failure
http_timeout: "30s"                                   # HTTP client timeout per request
idle_conn_timeout: "90s"                              # How long idle HTTP connections are kept alive
query_timeout: "2m"                                   # Overall timeout for a full GetAll query (may span multiple pages)
```

---

## Bench Plan

- `BenchmarkProcess_Accept_Cached`: pre-seed cache, measure ops/sec and allocs/op.  
- `BenchmarkProcess_Reject_Cached`: same for negative path.  
- `BenchmarkRefresh_Swap`: concurrent 10k rps lookups while rotating cache snapshots; assert p95 latency and zero panics.  
- Export bench results in CI artifact.