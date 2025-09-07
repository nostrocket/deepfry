# Deepfry Whitelist Plugin

## Purpose

The Whitelist Plugin enforces write-policies in StrFry by checking if an event’s pubkey exists in the Web-of-Trust (WoT) allowlist. It integrates via StrFry’s JSON stdin/stdout plugin interface and must support a fake provider until the Dgraph WoT service is live.

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
| FR-08 | Async periodic refresh of cache. Interval configurable. Safe swap with no read stalls. | Functional | `DF_REFRESH_SECONDS` governs refresh cadence. Hot-swap uses RW lock; no blocked lookups >1ms p95 in benchmark at 10k EPS. Backoff on provider error with jitter. | Subsys-06 |
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
| NFR-09 | Configurable refresh and timeouts. | Non-Functional | `DF_REFRESH_SECONDS`, `DF_REFRESH_JITTER_SECONDS`, `DF_PROVIDER_TIMEOUT_MS` honored; misconfig falls back to sane defaults. | Arch-01 |
| NFR-10 | Resilience during refresh failure. | Non-Functional | On provider error cache is unchanged; logs warning; exponential backoff with jitter; next success atomically replaces snapshot. | Arch-01 |

---

## Config Parameters

- `DF_REFRESH_SECONDS` (default 60)  
- `DF_REFRESH_JITTER_SECONDS` (default 15)  
- `DF_PROVIDER_TIMEOUT_MS` (default 1000)

---

## Bench Plan

- `BenchmarkProcess_Accept_Cached`: pre-seed cache, measure ops/sec and allocs/op.  
- `BenchmarkProcess_Reject_Cached`: same for negative path.  
- `BenchmarkRefresh_Swap`: concurrent 10k rps lookups while rotating cache snapshots; assert p95 latency and zero panics.  
- Export bench results in CI artifact.