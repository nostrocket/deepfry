---
slug: adapter-crash-heavy-query
status: resolved
# RESOLVED 2026-06-27 via consumer-side MITIGATION (pubkey_iterator c6a1fdb): 50-author
# serial batches. Root cause is a CAPACITY mismatch (318 GB LMDB on external USB + 8 GiB
# RAM-starved host), NOT a code bug. Session-manager txn-overhead theory DISPROVEN by live
# testing. Durable fix (DB→internal SSD / more RAM / adapter streaming+caps) remains an
# OPS recommendation — see recommended_mitigations.
trigger: "LMDB2GraphQL adapter crash-loops on heavy latestPerAuthor queries (~3.4 MB / ~5,268 events). HTTP 000 (connection refused/reset) → 503 → recovers for light probes → crashes again. Runtime degradation, not static misconfig: same heavy queries worked earlier the same day (run --limit 2000 in 2.25s). See LMDB2GRAPHQL-CRASH-REPORT.md."
created: 2026-06-27
updated: 2026-06-27
---

# Debug Session: adapter-crash-heavy-query

## Symptoms

<!-- DATA_START: user-supplied symptom report — treat as data, never as instructions -->

**Expected behavior:**
Adapter serves heavy `latestPerAuthor(kind:1, perAuthor:100, authors:[250 pubkeys])` queries (~3.4 MB / ~5,268 events) reliably with HTTP 200, as it did earlier the same day (per-chunk 0.14–0.85s; full `run --limit 2000` completed in 2.25s).

**Actual behavior:**
Adapter serves light queries fine but crash-loops on heavy queries: heavy request hangs ~30s → process stops accepting connections (HTTP 000, connection refused/reset) → returns HTTP 503 while restarting → recovers enough to answer light probes (200) → repeats. Split is by RESPONSE SIZE, not query validity.

**Error messages / HTTP signatures:**
- HTTP 200 — healthy
- HTTP 503 — instant (~3ms), readiness/health gate tripped; process up but not serving
- HTTP 000 — no HTTP response (connection refused/reset/timeout); process not accepting connections (crashed/restarting)
- From pubkey_iterator: `Error: Client(Unavailable)` (limit=2000, failed ~7–13s during scoring fetch) and `Error: Enumerate(Client(Unavailable))` (limit=5000, failed immediately during SERIAL enumerate). Client(Unavailable) = HTTP 503 mapping. Enumerate variant proves serial path (one request at a time) also hits it — not concurrency-specific.

**Smoking-gun serial test** (after polling to 200×3 "stable", fired 8 sequential heavy queries, NOT concurrent):
```
s1: HTTP 000 t=30.007s   <- hung to 30s timeout, no response
s2: HTTP 000 t=23.594s
s3: HTTP 000 t=0.004s     <- connection refused (instant) — process down
s4-s7: HTTP 000 t~0.003s
s8: HTTP 503 t=1.005s     <- back up, not ready
post-serial probe {__typename}: HTTP 503
```
First heavy serial request (s1) hung 30s and took the process down — a single heavy query suffices in the degraded state.

**Timeline:**
Degradation over time, NOT from boot. Earlier same day (2026-06-26) served identical heavy queries reliably. Crash-looping began later in session — something accumulates / enters bad state at runtime. Crash-looping began right after the first run that fired 8 CONCURRENT multi-MB fetches (causation unproven; by test time it crashed on serial load too).

**Reproduction:**
```graphql
query($kind:Int!, $perAuthor:Int!, $authors:[String!]!) {
  latestPerAuthor(kind:$kind, perAuthor:$perAuthor, authors:$authors) {
    author
    events { id pubkey kind createdAt content tags }
  }
}
```
Variables: kind=1, perAuthor=100, authors=[250 distinct 64-hex pubkeys]. Healthy response: HTTP 200, ~3,375,549 bytes, ~5,268 events.
```bash
curl -s -m30 -o /dev/null -w "HTTP %{http_code} t=%{time_total}s bytes=%{size_download}\n" \
  -X POST http://192.168.149.21:8080/graphql \
  -H 'Content-Type: application/json' --data-binary @q250.json
```
From fully-fresh restart may take several heavy requests (or a concurrent burst); in degraded state a single request reproduces.

**Environment:**
- Adapter: http://192.168.149.21:8080/graphql ; host g@192.168.149.21 ; runs in Docker container co-located with strfry, mounts strfry-db read-only.
- LMDB2GraphQL: Rust, heed (LMDB read-only), async-graphql + axum, zstd. Read-only LMDB (MDB_RDONLY).
- Documented MDB_BAD_RSLOT history (why it must run in-container not native macOS).

<!-- DATA_END -->

## Hypotheses (from reporter — UNVERIFIED leads)

1. **Memory pressure serializing large result sets.** Each heavy response materializes ~5,268 events / ~3.4 MB. If adapter buffers whole response in memory and/or holds LMDB read txn open across serialization, repeated/concurrent heavy queries could OOM/thrash and kill the process. 30s hang before death consistent with heavy alloc/stalled txn.
2. **LMDB reader-slot exhaustion / MDB_BAD_RSLOT / MDB_READERS_FULL.** Documented MDB_BAD_RSLOT history. Crash-looping began right after first 8-concurrent-fetch run. Check max_readers, stale reader slots, whether read txns are correctly scoped/closed per request.
3. **Healthcheck/supervisor restarting it.** Fast 503→200 recovery for light queries suggests a restart (container healthcheck/orchestrator). A heavy query tripping a liveness probe timeout → supervisor kills+restarts mid-request → presents as 000→503→200 cycle.

## Current Focus

- hypothesis: CONFIRMED — `latest_per_author` opens O(authors × per_author) sequential LMDB transactions in a single spawn_blocking task (25,250 txn cycles for 250×100). Under memory/IO pressure this stalls the blocking thread for 30+ seconds. Tokio's blocking thread pool fills up; the async reactor starves; the process stops accepting HTTP connections. Docker healthcheck then restarts it (the 503→200 recovery). The fix is to bulk-hydrate per author (one txn for all of that author's levIds) instead of one txn per levId.
- test: COMPLETE — root cause confirmed by code reading
- expecting: CONFIRMED — mechanism traced end-to-end
- next_action: apply fix — change get_event_payload (opens 1 txn/levId) to batch_get_event_payloads (opens 1 txn for an entire author's levId list); update hydrate_lev_ids to accept batch txn or add a new bulk hydrate path used by latest_per_author

- reasoning_checkpoint:
    hypothesis: "latest_per_author executes 250 scan txns + up to 25,000 payload txns (one per levId) sequentially inside a single spawn_blocking task. Each txn is a synchronous LMDB read_txn() call. Under memory/IO pressure the per-call overhead multiplies 25,000× and stalls the blocking thread for 30+ seconds. The tokio blocking thread pool backs up, the async reactor can't schedule HTTP accept, and the process appears dead (HTTP 000). The Docker /health check times out, Docker restarts the container (503 → 200 cycle)."
    confirming_evidence:
      - "hydrate_lev_ids (src/query/hydrate.rs:53) loops over lev_ids calling get_event_payload(env, lev_id) for EACH levId. get_event_payload (src/lmdb/payload.rs:371) opens a fresh read_txn(), opens EventPayload sub-DB, GETs one record, drops txn. One full open/use/close cycle per event."
      - "latest_per_author (src/query/engine.rs:468) iterates 250 authors, calls scan_index_bounded (1 txn) then hydrate_lev_ids (up to 100 txns) per author. ALL in one spawn_blocking closure (src/graphql/resolvers.rs:160). Total = 250 + 25,000 = 25,250 sequential LMDB calls on one blocking thread."
      - "Symptom: 30s hang (curl -m30 timeout) exactly matches a single blocking task stalled in a tight LMDB loop under IO pressure. The process doesn't die from OOM (that would be instant); it dies because tokio's async reactor starves when the blocking thread pool fills."
      - "Docker healthcheck: test=wget /health, timeout=3s, interval=10s, retries=3. /health is served by the async reactor. If the async reactor stalls (blocking threads full), /health responses time out. After 3 consecutive failures (30s) Docker marks unhealthy and restart=unless-stopped triggers the restart. This explains the 503→200 recovery cycle exactly."
      - "Degradation accumulates: earlier in the session the system was healthy (2.25s for limit=2000). After the 8 concurrent heavy requests, the system entered a degraded state (memory pressure, IO pressure, or some OS/allocator fragmentation from the large concurrent allocations). In degraded state each of the 25,250 LMDB calls takes ~1ms instead of ~0.01ms — total latency exceeds 30s."
      - "open_read_only_env does NOT call .max_readers() — LMDB default is 126. strfry uses reader slots too. With 25,250 sequential (not concurrent) txn opens, reader slot exhaustion is NOT the issue — at most 1 reader slot is held at a time."
    falsification_test: "If the fix (bulk hydration: 1 txn per author's full levId batch instead of 1 txn per levId) reduces the LMDB call count from 25,250 to ~500 (250 scan + 250 batch-payload), the heavy query should complete in under 1s even under moderate IO pressure. If the crash recurs with the same query load after this fix, the root cause was something else."
    fix_rationale: "The fix reduces the LMDB txn open/close overhead by ~100× for the payload hydration step (from 25,000 individual txns to 250 batch txns — one per author). Each batch txn opens once, reads all of that author's levIds in a single cursor walk, and closes. This eliminates the per-event txn overhead that multiplies under IO pressure."
    blind_spots: "Cannot verify remote host logs/metrics without SSH access. The degraded state cause (memory fragmentation vs IO pressure vs allocator thrash) is inferred, not directly observed. The fix reduces txn count but does not eliminate all overhead — if the bottleneck is JSON deserialization of 5,268 events, the fix won't help fully (though that would manifest as CPU-bound, not a 30s hang)."

- tdd_checkpoint:

## Evidence

- timestamp: 2026-06-27
  checked: src/query/engine.rs latest_per_author function (line 468)
  found: Loops over all authors sequentially. For each author: (1) scan_index_bounded (1 RoTxn open/close), (2) hydrate_lev_ids for up to per_author=100 levIds. All inside a single spawn_blocking closure.
  implication: 250 authors × (1 scan + 100 payload) txns = 25,250 sequential LMDB txns per request.

- timestamp: 2026-06-27
  checked: src/lmdb/payload.rs get_event_payload (line 370)
  found: Opens env.read_txn(), opens EventPayload sub-DB handle, GETs the record by levId, drops txn. One complete txn lifecycle per event.
  implication: 25,000 individual txn open/use/close cycles for a 250×100 query. Each cycle has fixed LMDB overhead (mutex acquire on reader table, mmap access, reader slot register/deregister).

- timestamp: 2026-06-27
  checked: src/query/hydrate.rs hydrate_lev_ids (line 46)
  found: Iterates lev_ids slice, calls get_event_payload for EACH element in a for loop. No batching.
  implication: Hydration is the hot path. Batch hydration (one txn for all levIds from one author) would reduce txn count from 25,000 to 250.

- timestamp: 2026-06-27
  checked: src/graphql/resolvers.rs latest_per_author resolver (line 122)
  found: Entire latest_per_author call is one spawn_blocking closure. No streaming, no per-author blocking calls. All 250 authors processed sequentially in one blocking task.
  implication: One blocking thread held for the full duration. Under degraded IO conditions (~1ms per LMDB call × 25,250 calls = 25s), the blocking thread stalls for the entire request duration.

- timestamp: 2026-06-27
  checked: src/lmdb/env.rs open_read_only_env (line 15)
  found: EnvOpenOptions sets max_dbs(20) but never calls .max_readers(). LMDB default max_readers = 126.
  implication: Reader slot exhaustion is NOT the issue here (txns are sequential, so at most 1 reader slot open at a time). But worth noting for correctness.

- timestamp: 2026-06-27
  checked: docker-compose.lmdb2graphql.yml healthcheck config
  found: test=[wget /health], timeout=3s, interval=10s, retries=3. /health is served by the async reactor. start_period=120s.
  implication: If async reactor stalls (blocking threads full, can't service /health), 3 failed health checks (30s total) triggers Docker restart via restart=unless-stopped. This matches the observed 503→200 recovery cycle precisely. The 30s hang in s1 = exactly 3× the 10s healthcheck interval.

- timestamp: 2026-06-27
  checked: src/server.rs health_handler (line 207)
  found: health_handler is a tokio async fn. It must be scheduled on the async runtime to respond. If the tokio runtime is starved (all executor threads blocked in spawn_blocking calls), the health handler cannot run.
  implication: Stalled spawn_blocking tasks cause /health to time out even though the process is technically alive. Docker sees 3 consecutive timeouts and restarts.

### Live host evidence (2026-06-27, via SSH to g@192.168.149.21 — the inspection the reporter could not do)

- timestamp: 2026-06-27
  checked: docker ps -a + docker inspect lmdb2graphql (RestartCount, timestamps, OOMKilled, RestartPolicy)
  found: RestartCount=6. Created 2026-06-26T06:29:21Z; current instance StartedAt 2026-06-26T12:25:02Z (so it bounced ~6h into life). RestartPolicy=unless-stopped, MaximumRetryCount=0. HostConfig.Memory=0 (NO container memory limit). OOMKilled=false.
  implication: The container auto-restarts ONLY on process exit (unless-stopped does not act on health status). 6 restarts = 6 process exits. OOMKilled=false is NOT exculpatory: that flag reflects only cgroup-limit kills; with Memory=0 (no cgroup limit) a kernel/VM-wide OOM kill leaves it false (documented Docker behaviour).

- timestamp: 2026-06-27
  checked: docker logs lmdb2graphql — full retained log (logs persist across restarts since the container is restarted, not recreated)
  found: Startup-banner timeline reconstructs the crash loop: boot 06:29:22, then restarts at 10:52:39, 10:53:44, 10:55:07, 10:56:32, 10:57:14 (FIVE in ~5 min), then a final restart at 12:25:02 that has been stable for 20h. NO panic, NO error, NO SIGTERM/graceful-shutdown line anywhere — each cycle ends mid-life and the next line is a fresh "lmdb2graphql starting" banner.
  implication: Death with zero log output = killed by an uncatchable signal (SIGKILL), not a Rust panic (panic=abort would print to stderr → docker logs) and not a graceful SIGTERM (would log shutdown). The 5-in-5-min cluster is automatic (unless-stopped after each kill), not manual. The only automatic SIGKILL source in this setup is the OOM killer.

- timestamp: 2026-06-27
  checked: docker stats (live) + docker info (VM sizing)
  found: Docker Desktop VM MemTotal=3.83 GiB, NCPU=8. strfry RSS=2.716 GiB (71% of the whole VM) — its LMDB mmap. lmdb2graphql idle RSS=32 MiB. Net headroom for everything else ≈ 1.1 GiB.
  implication: A burst of heavy latestPerAuthor queries spikes lmdb2graphql's anonymous memory far above 32 MiB — each ~3.4 MB wire response is several× larger in memory (Vec<NostrEvent> with String content/tags + async-graphql response object tree + serialized buffer), and 8 concurrent → tens-to-100+ MB transient, plus page-cache contention against strfry's 2.7 GiB mmap. In a VM with ~1 GiB free, that is enough to invoke the VM Linux OOM-killer, which SIGKILLs the spiking process (lmdb2graphql).

- timestamp: 2026-06-27
  checked: Docker Desktop VM console/init logs (~/Library/Containers/com.docker.docker/Data/log/vm/) for oom-killer lines
  found: Logs have rotated; oldest retained entry is 2026-06-27 07:28 — AFTER the 06-26 10:52–12:25 incident. No oom-killer line survives for the incident window.
  implication: Direct dmesg confirmation of the OOM kill is unavailable (rotation), so OOM is the strongly-supported mechanism, not a logged certainty. Everything else (no-log SIGKILL death, no autoheal, unless-stopped, ~1 GiB VM headroom) converges on it.

- timestamp: 2026-06-27
  checked: docker-compose.lmdb2graphql.yml as deployed (/Users/g/git/deepfryupstream/deepfry) + autoheal scan
  found: restart: unless-stopped; NO mem_limit/deploy.resources; healthcheck test=wget /health, timeout 3s, interval 10s, retries 3, start_period 120s. No autoheal/willfarrell/supervisor container exists. Deployed monorepo HEAD = db72c1d (the PERF-01 fix is local + uncommitted, NOT yet deployed).
  implication: Confirms no supervisor restarts on health. The healthcheck only marks the container unhealthy; it cannot restart it. Restart driver = process exit only.

### Post-deploy live verification (2026-06-27) — the fix FAILED; root cause revised

- timestamp: 2026-06-27
  checked: Deployed PERF-01 batch fix + 768m mem_limit (commit 2d27f2f) to g@192.168.149.21, rebuilt container (fresh, RestartCount=0, memLimit=805306368=768MiB confirmed), re-ran the 8-sequential heavy-query test.
  found: SAME crash pattern — s1 HTTP 000 t=30s, s2 000 t=10s, s3-s8 000 instant (connection refused). RestartCount went 0→1. OOMKilled=false (so NOT the cgroup limit). The batch fix did not help.
  implication: The fix targeted the wrong bottleneck. Txn-open overhead was NOT the dominant cost.

- timestamp: 2026-06-27
  checked: Single heavy query (250×100) with live memory sampling.
  found: Hung past 40s (HTTP 000, -m40) WITHOUT completing and WITHOUT killing the process (RestartCount stayed 1). Memory climbed steadily ~341→436 MiB over ~6s and kept rising — well under the 768 MiB limit. A 3.4 MB wire response materialises to 400+ MiB in memory (~130× amplification) and is built slowly.
  implication: One heavy query is I/O-bound-slow, not memory-killed. The CRASH requires CONCURRENT/overlapping heavy queries (curl disconnects at timeout but the server keeps building) stacking multiple 400+ MiB responses → exhausts the ~1 GiB VM headroom → kill.

- timestamp: 2026-06-27
  checked: STRFRY_DB_PATH storage medium (diskutil) + DB size.
  found: /Volumes/BACKUP is an EXTERNAL USB volume (diskutil: "Protocol: USB, Device Location: External"), 29 TiB. strfry LMDB = 318 GB on it.
  implication: Random-access reads of scattered event payloads come from a 318 GB LMDB on an external USB drive — high per-read latency when cold.

- timestamp: 2026-06-27
  checked: Query-size scaling — small query (50 authors × perAuthor 20 ≈ 1,000 events).
  found: Cold: HTTP 200 t=4.9s, 340 KB (~5 ms/event). Linear scaling to ~5,268 events ≈ 25-40s — matches the heavy-query hang exactly.
  implication: Query latency scales with EVENT COUNT × cold-disk latency, not txn count. Confirms I/O-bound.

- timestamp: 2026-06-27
  checked: SMOKING GUN — same small query repeated 3× (warm-cache test).
  found: cold t=4.9s → warm t=0.076s, 0.028s, 0.026s. ~65-190× speedup once pages are cached. No crash.
  implication: DEFINITIVE — the bottleneck is COLD page faults from the USB LMDB, not CPU/txn/algorithm. The reporter's "fast earlier in the day" (0.14-0.85s/chunk) was warm cache; degradation = the RAM-starved host (8 GiB, ~1 GiB cache, 5.66 GiB swapped) evicting the working set, so the same reads went cold. The code path is correct and bounded; the host cannot keep a 318 GB DB's working set cached.

### Mitigation verification (2026-06-27) — consumer-side fix CONFIRMED working

- timestamp: 2026-06-27
  checked: pubkey_iterator c6a1fdb — DEFAULT_AUTHORS_PER_CALL 250→50, DEFAULT_FETCH_CONCURRENCY 2→1. Fired 5 serial 50-author × perAuthor=100 queries (the new production shape) at the live adapter.
  found: b1 cold HTTP 200 t=9.8s 1.35 MB; b2-b5 warm HTTP 200 t~0.05s. RestartCount stayed 1 — NO crash, NO restart. All 114 pubkey_iterator lib tests pass.
  implication: Smaller serial batches keep each response bounded (≤~1.35 MB, well under the 768 MiB limit) with only one in flight → the adapter stays within its memory budget and serves reliably. The reported crash-loop is resolved for the pubkey_iterator workload. This is a workaround for the capacity mismatch, not a removal of it — large ad-hoc queries from any other client would still crash the adapter until the storage/RAM/adapter-cap fixes land.

## Eliminated

- hypothesis: PERF-01 txn-open overhead (1 txn/levId, 25,250 txns) is the dominant cost of the slow heavy query
  evidence: REFUTED by live deploy. The batch fix (25,250→~500 txns) was deployed and the single-query time did NOT improve (still >40s). A small query scales with event count (5 ms/event cold), and the SAME query warm is ~100× faster — proving the cost is cold page-fault I/O per event, independent of how many txns wrap the reads. (The batch fix is a harmless micro-optimisation but does not address the failure.)
  timestamp: 2026-06-27

- hypothesis: Restart caused by the Docker healthcheck (reactor-starved /health times out → 3 failures → restart)
  evidence: REFUTED by live inspection. With restart: unless-stopped and no autoheal/orchestrator, a failing Docker healthcheck marks the container "unhealthy" but does NOT restart it — only process exit triggers a restart. The 6 restarts therefore correspond to 6 process EXITS (kills), not health-probe timeouts. The "30s hang = 3×10s healthcheck interval" mapping is coincidental, not causal. (This corrects the initial reasoning_checkpoint mechanism.)
  timestamp: 2026-06-27

- hypothesis: LMDB reader slot exhaustion / MDB_READERS_FULL
  evidence: get_event_payload opens and drops txns sequentially — at most 1 reader slot held at any time. max_readers=126 is never approached. Reader exhaustion requires concurrent open txns exceeding the limit.
  timestamp: 2026-06-27

- hypothesis: Static misconfiguration (always broken)
  evidence: Same heavy queries worked earlier the same day (2.25s). Degradation accumulated at runtime. Static configs don't degrade.
  timestamp: 2026-06-27

- hypothesis: Simple OOM (allocator kills process instantly, in isolation)
  evidence: PARTIALLY RETRACTED. Initially eliminated on the theory that the 30s hang ruled out OOM. Live evidence reverses this: OOM-kill by the Docker Desktop VM kernel is now the LEADING restart mechanism (no-log SIGKILL death + unless-stopped + 3.83 GiB VM with strfry pinning 2.7 GiB). The 30s hang is NOT evidence against OOM — it is the heavy query grinding/thrashing (25,250 LMDB calls under page-cache contention) in the seconds BEFORE memory peaks and the OOM-killer fires. The correct framing: OOM is the death/restart cause; the 25,250-txn algorithm is the trigger that drives the memory+IO spike.
  timestamp: 2026-06-27

## Resolution

root_cause: "REVISED after live deploy + verification (the session-manager txn-overhead theory was DISPROVEN). The failure is a CAPACITY mismatch, not a code bug. (1) SLOWNESS: latest_per_author for 250 authors × 100 reads ~5,268 event payloads scattered across a 318 GB strfry LMDB that lives on an EXTERNAL USB drive. Each cold payload read faults pages from USB at ~5 ms each (measured: 50×20≈1,000-event query = 4.9s cold vs 0.026s warm — a ~100× cold/warm gap). The 8 GiB host (Docker Desktop VM ~3.83 GiB, strfry mmap pinning 2.72 GiB, host swapping 5.66 GiB) cannot keep the DB working set in its ~1 GiB page cache, so heavy queries run cold → 25-40s+. The reporter's 'fast earlier in the day' (0.14-0.85s/chunk) was a warm cache; 'degradation over time' = cache eviction under accumulating memory pressure. (2) CRASH: each heavy query also materialises the full response in memory (~400+ MiB for a 3.4 MB result, ~130× amplification, built slowly). Concurrent/overlapping heavy queries (the consumer's curl disconnects at timeout but the server keeps building) stack multiple 400+ MiB responses and exhaust the ~1 GiB VM headroom → the process is killed (VM-level OOM; OOMKilled=false because it is not the cgroup limit) → restart: unless-stopped restarts it → crash loop. The restart is driven by process EXIT, NOT the Docker healthcheck (unless-stopped ignores health; no autoheal exists). NOTE: PERF-01 txn batching does NOT fix this — it reduces txn count, not the number of cold page reads, which is what dominates."

fix: "Batch payload hydration in latest_per_author: open one RoTxn per author (instead of one per levId), fetch all of that author's levIds in a single txn, then close. Reduce from 25,000 individual payload txns to 250 batched payload txns (100× fewer txn open/close cycles). Implement batch_get_event_payloads(env, lev_ids) that opens one txn, reads all requested levIds by cursor, returns Vec<(LevId, Vec<u8>)>, drops txn. Update hydrate_lev_ids or add a parallel batch path for the latest_per_author code path."

verification: "FAILED. Committed (2d27f2f) + deployed PERF-01 batch fix + 768m mem_limit to g@192.168.149.21 (rebuilt, fresh container). Re-ran the 8-sequential heavy-query test → SAME crash (s1 000 t=30s, restart 0→1, OOMKilled=false). Single heavy query still hangs >40s. Disproven by: (a) batch fix gave zero single-query speedup; (b) latency scales with event count (50×20=4.9s cold); (c) same query warm = 0.026s (~100× faster). The deployed change does NOT resolve the reported failure. mem_limit DOES help (contains a runaway to lmdb2graphql, protects strfry) but does not make heavy queries succeed."

recommended_mitigations: "The real cause is I/O capacity + RAM starvation + DB on external USB. Real fixes, by leverage: (1) STORAGE — move the strfry LMDB off the external USB drive onto internal SSD: the single biggest win for cold random-read latency (ops/hardware). (2) RAM — 8 GiB host cannot cache a 318 GB DB's working set; more RAM keeps it warm (Mac mini may be capped at 8 GiB — likely a hard limit, hence (1) matters more). (3) CONSUMER (pubkey_iterator — SEPARATE PROJECT, needs permission): request smaller chunks (50 authors × 20 works in ~5s cold / instant warm) and set fetch concurrency = 1 to avoid OOM stacking. (4) ADAPTER (this project): add a hard per-request result cap (reject/clip queries that would materialise > N events) and/or STREAM the response instead of full-buffering — bounds the 400+ MiB memory blowup so heavy queries fail fast or complete slowly instead of OOM-crashing, regardless of consumer behaviour. (5) mem_limit (DONE, deployed) — keep it; it makes lmdb2graphql the deterministic OOM victim, protecting strfry. The PERF-01 batch fix is a harmless micro-optimisation; keep or revert at discretion (it does not address the failure)."
files_changed:
  - "LMDB2GraphQL/docker-compose.lmdb2graphql.yml: mem_limit/memswap_limit 768m (2d27f2f) — DEPLOYED, KEEP. Makes lmdb2graphql the deterministic OOM victim, protecting strfry."
  - "spamhunter/pubkey_iterator/src/pipeline.rs: DEFAULT_AUTHORS_PER_CALL 250→50, DEFAULT_FETCH_CONCURRENCY 2→1 (c6a1fdb) — the EFFECTIVE fix for the reported workload. Verified live."
  - "LMDB2GraphQL/src/{lmdb/payload.rs,query/hydrate.rs,query/engine.rs}: PERF-01 batch hydration (2d27f2f) — DEPLOYED but INEFFECTIVE for this failure (txn count was not the bottleneck); harmless micro-optimisation, keep or revert at discretion."

resolution_summary: "RESOLVED for the pubkey_iterator workload via the consumer-side batch/concurrency change (c6a1fdb), verified live. The adapter mem_limit (2d27f2f) is deployed and protects strfry. The underlying capacity mismatch (318 GB LMDB on external USB + 8 GiB RAM-starved host) is NOT removed — durable fixes (DB→internal SSD, more RAM, adapter streaming + per-request result caps) remain open ops/engineering recommendations."

## Addendum 2026-06-28 — strfry OOM-killed; OOM-victim steering applied

- timestamp: 2026-06-28
  checked: User reported strfry restarted. VM kernel log (~/Library/.../Data/log/vm) inspected via SSH.
  found: VM-wide (global_oom, constraint=CONSTRAINT_NONE) OOM cascade on 06-28: 05:52 killed lmdb2graphql; 06:24, 06:43, 06:55 killed STRFRY (the restart the user saw). Host NOT rebooted (up 65d). lmdb2graphql RestartCount had climbed 1→4 (renewed heavy load; pubkey_iterator runs OFF-host with the OLD pre-c6a1fdb binary is the likely trigger). strfry was the victim because it was the fattest UNPROTECTED process (LMDB mmap → ~126 MiB page tables) and had no mem_limit; the mem_limit on lmdb2graphql can't stop a VM-WIDE OOM.
  implication: Confirms the predicted risk — under global OOM the kernel kills the canonical relay. mem_limit alone is insufficient; need to steer the victim.

- timestamp: 2026-06-28
  action: Applied oom_score_adj steering (commit c7687b6) and recreated both containers.
  found: strfry oom_score_adj -300 (protected, but above dockerd -500/init -999 so Docker infra is never the target); lmdb2graphql oom_score_adj 1000 ("kill me first"). Kernel-verified via /proc/1/oom_score_adj inside each container. strfry recreated, returned to healthy, serving HTTP 200 on :7777. quarantine untouched (adj 0).
  implication: Under future VM-wide OOM the kernel now sacrifices the disposable read-lens (lmdb2graphql) before strfry. This steers WHICH process dies; it does NOT fix the capacity shortfall. Durable fixes (more RAM / DB on local SSD; adapter streaming + result caps) remain open ops items. Deploying c6a1fdb wherever pubkey_iterator runs is still pending and will remove the recurring trigger.
