# LMDB2GraphQL crash report — debugging handoff

**Observed:** 2026-06-26, from the `pubkey_iterator` host during perf work.
**Adapter under test:** `http://192.168.149.21:8080/graphql` (LMDB2GraphQL — read-only GraphQL lens over strfry's LMDB).
**Reporter:** observations only; the LMDB2GraphQL project itself was NOT inspected (separate project, remote host). No adapter logs, container status, or LMDB internals were examined.

> This separates **observed facts** from **hypotheses**. Treat the hypotheses as leads, not conclusions.

---

## TL;DR

The adapter **serves light queries fine but crashes on heavy `latestPerAuthor` queries** (large result sets, ~3.4 MB / ~5,268 events per request). Once tipped, it enters a **crash-loop**: heavy requests hang (~30 s) → the process stops accepting connections (`HTTP 000`, connection refused/reset) → it returns `HTTP 503` while restarting → recovers enough to answer light probes → repeats. The **same heavy queries worked earlier the same day** (a `--limit 2000` run completed in 2.25 s), so this is a **runtime degradation**, not a static misconfiguration.

---

## What "crashing" means here (evidence)

Three distinct HTTP signatures were observed, in order of severity:

| Signal | curl `%{http_code}` | Meaning | Interpretation |
|---|---|---|---|
| Healthy | `200` | normal response | adapter ready |
| Not-ready | `503` | Service Unavailable, returned **instantly (~3 ms)** | readiness/health gate tripped — process up but not serving (loading / LMDB env not ready) |
| Down | `000` | **no HTTP response** — connection refused, reset, or timeout | the server process is **not accepting connections** → crashed / restarting |

`HTTP 000` is the key crash evidence: curl got no HTTP response at all. Combined with the 503s, the process is dying and restarting.

### From `pubkey_iterator` runs
Bounded runs (`run --limit N`) against the live adapter failed with:
```
Error: Client(Unavailable)              # limit=2000 — failed after ~7–13 s (during scoring fetch)
Error: Enumerate(Client(Unavailable))   # limit=5000 — failed immediately (during serial enumerate)
```
`Client(Unavailable)` is `pubkey_iterator`'s mapping of **HTTP 503** (see `src/graphql/client.rs` — 503 → `ClientError::Unavailable`). The `Enumerate(...)` variant means even the **serial author-pagination path** (one request at a time, not the new concurrent fetch) hit 503 — i.e. the failure is not specific to concurrency.

### Controlled curl test — SERIAL heavy load (the smoking gun)
After polling until the adapter returned `200` three times in a row ("stable"), I fired **8 sequential** heavy queries (one at a time, NOT concurrent), each `latestPerAuthor(kind:1, perAuthor:100, authors:[250 pubkeys])` (~3.4 MB response):

```
s1: HTTP 000 t=30.007s     <- hung to the 30s curl timeout, then no response
s2: HTTP 000 t=23.594s     <- hung ~23s
s3: HTTP 000 t=0.004s      <- connection refused (instant) — process down
s4: HTTP 000 t=0.004s
s5: HTTP 000 t=0.004s
s6: HTTP 000 t=0.003s
s7: HTTP 000 t=0.003s
s8: HTTP 503 t=1.005s      <- back up but not ready (restarting/loading)
post-serial probe (__typename): HTTP 503
```
**The very first heavy serial request (`s1`) hung for 30 s and the process stopped responding** — a single heavy query was enough to take it down in this degraded state.

### Light queries keep working
Throughout, the trivial query succeeded whenever the process was up:
```
POST {"query":"{__typename}"}                  -> HTTP 200 (~3–11 ms) in good windows, 503 in bad windows
POST authors(after:null, limit:10){pubkeys cursor}  -> HTTP 503 only while crash-looping
```
The split is **by result-set size**, not by query validity: small queries are fine, large `latestPerAuthor` responses crash it.

---

## The trigger query (reproduction)

The query `pubkey_iterator` issues per batch — and the one that crashes the adapter:

```graphql
query($kind:Int!, $perAuthor:Int!, $authors:[String!]!) {
  latestPerAuthor(kind:$kind, perAuthor:$perAuthor, authors:$authors) {
    author
    events { id pubkey kind createdAt content tags }
  }
}
```
Variables in the failing case: `kind=1`, `perAuthor=100`, `authors=[250 distinct 64-hex pubkeys]`.
Response when healthy: **HTTP 200, ~3,375,549 bytes (~3.4 MB), ~5,268 events**.

**Reproduce** (replace the array with 250 real pubkeys from the relay; a handful won't stress it):
```bash
# Build a body with 250 authors, then:
curl -s -m30 -o /dev/null -w "HTTP %{http_code} t=%{time_total}s bytes=%{size_download}\n" \
  -X POST http://192.168.149.21:8080/graphql \
  -H 'Content-Type: application/json' \
  --data-binary @q250.json
```
To get 250 real pubkeys quickly, a prior `pubkey_iterator` run's SQLite has them:
```bash
sqlite3 <run-db>.sqlite "SELECT pubkey FROM pubkey LIMIT 250;"
```

A single heavy request may reproduce it once the adapter is in the degraded state; from a fully-fresh restart it may take several (or a concurrent burst — see below).

---

## Circumstances / pattern

- **Degradation over time, not from boot.** Earlier the same day the adapter served the identical heavy queries reliably: per-250-author chunk responses were 0.14–0.85 s / HTTP 200, and a full `run --limit 2000` (which issues these queries for all 2,000 pubkeys) completed in **2.25 s**. The crashing began later in the session. So something accumulates or gets into a bad state at runtime.
- **Flapping.** It oscillates: answers `200` to light probes, `503`/`000` under heavy load. Polling showed it returning to `200` for light probes within tens of seconds after a crash, then crashing again on the next heavy query.
- **Heavy-response-size correlated.** Light queries (`__typename`, `authors limit:10`) never triggered it; only the multi-MB `latestPerAuthor` responses did.

---

## Hypotheses (UNVERIFIED — leads for debugging)

1. **Memory pressure from serializing large result sets.** Each heavy response materializes ~5,268 events / ~3.4 MB. If the adapter buffers the whole response in memory (and/or holds the LMDB read txn open across serialization), repeated or concurrent heavy queries could OOM or thrash, killing the process. The 30 s hang before death is consistent with heavy allocation/GC or a stalled read txn.
2. **LMDB reader-slot exhaustion / `MDB_BAD_RSLOT`.** This project has a documented `MDB_BAD_RSLOT` history (LMDB reused read-slot crashes under concurrent read transactions; it's why the adapter must run in-container, not native macOS). **Timeline note:** the crash-looping began right after the first run that fired **8 concurrent** multi-MB fetches at the adapter. I could NOT prove causation — by the time I tested, it was already crashing on *serial* load too — but concurrent read transactions are a prime suspect for the **initial** trigger. Worth checking `max_readers`, stale reader slots, and whether read txns are correctly scoped/closed per request.
3. **A healthcheck or supervisor restarting it.** The fast `503`→`200` recovery for light queries suggests something restarts the process (container healthcheck / orchestrator). If a heavy query trips a liveness probe (timeout) the supervisor may kill+restart mid-request, which would present exactly as the `000`→`503`→`200` cycle.

---

## Suggested debugging steps

On the adapter host (`g@192.168.149.21`) / its container:
- **Logs around a crash:** container/stderr logs at the moment a heavy `latestPerAuthor` runs — look for panics, OOM-kills, `MDB_BAD_RSLOT`, `MDB_READERS_FULL`, or txn errors.
- **Restart/liveness:** `docker ps` restart count, healthcheck config and timeout, dmesg/`oom-kill` for the container, supervisor logs. Is the process being killed or exiting on its own?
- **Memory:** container memory limit vs RSS while serving a heavy query; whether response serialization is streamed or fully buffered.
- **LMDB readers:** `mdb_stat`/env info for `max_readers` and active/stale reader slots; confirm each request opens and **closes** its read txn; test with 1 vs 2 vs N concurrent heavy reads to see the reader-slot ceiling.
- **Isolate the variable:** from a freshly restarted adapter, run (a) one heavy serial query, (b) a slow drip of serial heavy queries, (c) a concurrent burst — note which first triggers the crash. That distinguishes "memory per heavy query" from "concurrent reader slots."
- **Capture a 503 body:** the probes discarded bodies (`-o /dev/null`). Re-run without `-o /dev/null` — the 503 body may state the reason (e.g. "loading").

---

## Impact on `pubkey_iterator` (context)

- Blocks live throughput re-measurement of the fetch pipeline; functional `run` against the live adapter currently fails with `Client(Unavailable)`.
- `pubkey_iterator` behaves correctly under the outage: 503 → retryable (3 attempts, bounded backoff) → surfaces the error; client now has 3 s connect / 30 s request timeouts so it can't hang indefinitely.
- Concurrent fetch was set to a conservative `DEFAULT_FETCH_CONCURRENCY = 2` partly to avoid hammering this adapter with concurrent LMDB reads until it's confirmed safe. `FC=1` reproduces strictly-serial fetch.
- A **restart of the adapter** will likely clear the current crash-loop and restore the healthy behavior seen earlier in the day — but won't fix the underlying trigger.
