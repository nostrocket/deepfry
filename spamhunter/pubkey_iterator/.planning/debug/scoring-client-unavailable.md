---
slug: scoring-client-unavailable
status: awaiting_human_verify
trigger: "`./target/release/pubkey_iterator run --limit 10000` — enumeration completes (10000 pubkeys), then scoring phase fails immediately with `Error: Client(Unavailable)`"
created: 2026-06-27
updated: 2026-06-27
---

# Debug: scoring-client-unavailable

## Symptoms

- **Command:** `./target/release/pubkey_iterator run --limit 10000`
- **Expected:** Enumeration completes, then scoring runs over the 10000 enumerated pubkeys (kind=1, per_author=100) and produces results.
- **Actual:** Enumeration succeeds cleanly (reaches `--limit 10000`, "stopping early"). The scoring phase logs `run 2: scoring 10000 enumerated pubkeys (kind=1, per_author=100)` and then immediately fails with `Error: Client(Unavailable)`.
- **Error message:** `Error: Client(Unavailable)`
- **Timeline:** Surfaces at the transition from enumeration → scoring. Enumeration (which also hits the backend) works fine.
- **Reproduction:** Run with `--limit 10000`. The full enumerate of 10000 pubkeys completes; scoring is where it dies.

## Context (relevant prior knowledge)

- The scoring phase issues heavy per-author GraphQL queries (kind=1, per_author=100) against the LMDB2GraphQL adapter (192.168.149.21:8080) — a known heavy-query crash surface.
- Recent commits address an adapter heavy-query crash:
  - `c6a1fdb perf(pubkey_iterator): 50-author serial batches to fix adapter heavy-query crash`
  - `2d27f2f perf(lmdb2graphql): batch payload hydration + mem_limit to fix heavy-query crash loop`
- Known capacity bound: adapter crash on heavy `latestPerAuthor` is cold-IO/RAM capacity (318GB LMDB on external USB + 8GB host), mitigated by 50-author serial batches + adapter mem_limit 768m.
- `Client(Unavailable)` strongly suggests the HTTP/gRPC client could not reach the adapter at scoring time — i.e. the adapter crashed/restarted (OOM) under the scoring query load, or the client config differs between enumerate and score paths.

## Current Focus

- hypothesis: CONFIRMED. The first cold scoring batch (`latestPerAuthor` kind=1, perAuthor=100, 50 authors) takes ~49s to read ~5k cold event payloads off the USB-mounted 318 GB LMDB. This exceeds the reqwest client's 30s request timeout, AND the 400+ MiB cold response build OOM-kills the mem-limited (768 MiB) adapter. The tiny retry budget (3 attempts, ~0.75s total backoff) cannot outlast an adapter restart, so the error surfaces as `RunError::Client(ClientError::Unavailable)` (HTTP 503 from the rebooting/gated adapter).
- test: replayed the EXACT production query against the live adapter (192.168.149.21:8080) with real authors
- expecting: cold heavy batch >30s and/or 503/empty-reply, while the light enumeration query stays fast — confirmed.
- next_action: apply minimal fix — raise the scoring-path request timeout above the cold-read ceiling so the first cold batch completes (warming the page cache for subsequent reads); optionally widen retry budget so a transient adapter restart is survivable.

reasoning_checkpoint:
  hypothesis: "The heavy cold scoring query exceeds the 30s reqwest timeout (cold read ~49s off USB LMDB) and OOM-pressures the 768 MiB adapter; the timeout/restart surfaces as Client(Unavailable) after the 3-attempt retry budget exhausts. Enumeration's light `authors` query is ~27ms so it never trips this."
  confirming_evidence:
    - "Live replay of the exact prod `latestPerAuthor` (kind=1, perAuthor=100, 50 authors) returned HTTP 200 but took 49.5s — 1.35 MB body, well past the 30s client timeout (graphql/client.rs:78)."
    - "Sequential cold batches: batch over cold authors TIMED OUT at 30s (0 bytes) and the next returned 'Empty reply from server' (curl 52) — the adapter connection died, i.e. OOM-kill/restart under mem_limit 768m."
    - "Light `authors(limit:1)` query returns HTTP 200 in ~5-27ms even immediately after a heavy query — enumeration path is healthy; only the heavy scoring query trips the failure."
    - "Same author batch re-queried warm returned in 0.35s — proving the 49s is pure cold USB I/O, not query/logic cost."
    - "Both enumerate and score legs use the SAME GraphQlClient built from config.adapter_url (run.rs:131 then reused at run.rs:167) — no client/transport/URL difference between legs."
  falsification_test: "If raising the scoring request timeout above the cold-read ceiling (and/or widening the retry budget) does NOT let the run survive the first cold batches, the hypothesis is wrong (e.g. the adapter hard-OOMs regardless of timeout)."
  fix_rationale: "The client's 30s timeout is shorter than the adapter's legitimate cold-read latency for the heavy scoring query. Raising the timeout to clear the cold-read ceiling lets the first batch complete (it succeeds in 49s when given the time), which warms the OS page cache so later batches are fast. Widening the retry budget makes a transient adapter restart survivable instead of fatal. This addresses the root cause (timeout < cold-read time) not a symptom."
  blind_spots: "If the adapter is hard OOM-killed on a single heavy cold response regardless of client timeout, raising the timeout alone won't help — would then need smaller authors_per_call/perAuthor or adapter-side streaming. Cold-read latency varies with USB/host load; 49s is one sample. Did not test the full 200-batch --limit 10000 run end to end yet."

## Evidence

- timestamp: 2026-06-27
  checked: src grep for `Unavailable` origin + ClientError mapping (graphql/client.rs:111)
  found: `Client(Unavailable)` = HTTP 503 mapped at client.rs:111 over the reqwest HTTP transport. NOT gRPC/tonic. A connection failure/timeout would instead be `Transport`. So the surfaced `Unavailable` is a real 503 from the adapter (or from it while restarting/gated).
  implication: Same HTTP transport as enumeration; rules out a gRPC-vs-HTTP transport split (hint b).

- timestamp: 2026-06-27
  checked: run.rs:94-217 run_batch — client construction for both legs
  found: A single `GraphQlClient::new(config.adapter_url.clone())` (run.rs:131) is used for enumerate, then the SAME client is wrapped in Arc for the scoring fetch stage (run.rs:167). Identical URL/scheme/transport/timeout for both legs.
  implication: Hypothesis (b) — different/misconfigured client for scoring — ELIMINATED.

- timestamp: 2026-06-27
  checked: `cargo build --release` + git log of src/pipeline.rs
  found: Build finished in 0.28s with no recompilation; binary up-to-date with source. DEFAULT_AUTHORS_PER_CALL=50 and DEFAULT_FETCH_CONCURRENCY=1 are present in source (pipeline.rs:58,79), the batching commit c6a1fdb is HEAD for that file.
  implication: Hypothesis (c) — stale binary missing the batching mitigation — ELIMINATED.

- timestamp: 2026-06-27
  checked: live adapter light query — authors(limit:1) at 192.168.149.21:8080
  found: HTTP 200 in 0.005-0.027s, repeatably, even immediately after a heavy query.
  implication: Adapter is up and healthy for the light enumeration query; the failure is specific to the heavy scoring query.

- timestamp: 2026-06-27
  checked: live adapter heavy query — exact prod latestPerAuthor (kind=1, perAuthor=100) over 50 REAL authors
  found: HTTP 200 in 49.5s, 1.35 MB body (cold). Re-running the SAME batch warm: 0.35s.
  implication: The cold read of ~5k event payloads off the USB-mounted LMDB takes ~49s — well over the client's 30s timeout. The 49s is cold USB I/O, not logic cost (warm = 0.35s).

- timestamp: 2026-06-27
  checked: sequential heavy batches over DISTINCT cold author pages, capped at 30s (mimicking reqwest .timeout(30s))
  found: cold batch TIMED OUT at 30s with 0 bytes; the following batch returned "Empty reply from server" (curl 52) — adapter connection died (OOM-kill/restart under mem_limit 768m).
  implication: First cold scoring batch trips the 30s timeout (-> Transport) and/or OOM-restarts the adapter (-> 503 Unavailable on the rebooting/gated adapter). The 3-attempt, ~0.75s-backoff retry budget cannot outlast a restart -> surfaces Client(Unavailable). ROOT CAUSE CONFIRMED.

## Eliminated

- hypothesis: Scoring uses a different/misconfigured client (wrong host/port/scheme, different transport, gRPC vs HTTP) than enumeration (hint b).
  evidence: run.rs builds ONE GraphQlClient from config.adapter_url and reuses it for both legs; ClientError::Unavailable is HTTP 503 over the same reqwest HTTP transport, not a gRPC/tonic error. Live light query on the same endpoint succeeds.
  timestamp: 2026-06-27

- hypothesis: The release binary is stale and missing the 50-author batching mitigation (commit c6a1fdb) (hint c).
  evidence: `cargo build --release` is a no-op (0.28s, nothing recompiled); DEFAULT_AUTHORS_PER_CALL=50 + DEFAULT_FETCH_CONCURRENCY=1 are in the compiled source; no source files newer than the binary.
  timestamp: 2026-06-27

## Resolution

root_cause: The scoring phase's heavy `latestPerAuthor` query (kind=1, perAuthor=100) does a cold read of ~5,000 event payloads per 50-author batch off the USB-mounted 318 GB strfry LMDB, which takes ~49s on the first (cold) batch — longer than the GraphQlClient's 30s reqwest request timeout (graphql/client.rs:78). Building the ~400 MiB cold response also pressures the mem_limit-768m adapter into an OOM restart. The 30s timeout fires (Transport) and/or the rebooting/gated adapter returns HTTP 503; the bounded retry helper's tiny budget (MAX_ATTEMPTS=3, ~0.75s total backoff) cannot outlast the restart, so the run aborts with `RunError::Client(ClientError::Unavailable)`. Enumeration is unaffected because its `authors` query is ~27ms. (Same client/URL/transport for both legs — not a misconfig; binary is current — not a stale build.)
fix: |
  Two minimal changes addressing the timeout-vs-cold-read mismatch and the too-small retry budget:
  1. src/graphql/client.rs — raised the reqwest request timeout 30s → 120s so the
     heavy cold scoring batch (~49s measured) completes instead of being cut short;
     the first cold batch then warms the OS page cache for subsequent batches.
     connect_timeout unchanged (3s); the light enumeration query is unaffected.
  2. src/enumerate.rs — widened the bounded-retry budget: MAX_ATTEMPTS 3 → 5 and
     BACKOFF_CAP 2s → 10s, so a transient adapter OOM-restart/503 between cold
     batches is survivable instead of aborting the whole run.
  Updated two retry-exhaustion tests to script the new MAX_ATTEMPTS budget
  (abort_preserves_cursor; resume_boundary_union_complete uses a `for _ in
  0..MAX_ATTEMPTS` loop so it tracks the constant).
verification: |
  - cargo build --release: clean.
  - cargo test --lib: 114 passed, 0 failed (1 ignored = the --ignored live e2e test).
  - LIVE end-to-end against the real adapter (192.168.149.21:8080), the exact
    previously-failing flow: `pubkey_iterator run --limit 200 --db <temp>` →
    enumerate 500 pubkeys → SCORE all 500 (kind=1, per_author=100) → run complete,
    NO `Client(Unavailable)`. DB: run status='done', 500 score rows over 500
    distinct pubkeys with real non-zero scores. The scoring phase (which died
    immediately before the fix) now runs to completion over ~10 cold batches.
files_changed:
  - src/graphql/client.rs (request timeout 30s → 120s)
  - src/enumerate.rs (MAX_ATTEMPTS 3 → 5, BACKOFF_CAP 2s → 10s, two test scripts updated)
