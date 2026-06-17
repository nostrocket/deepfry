# Spike Manifest

## Idea

Make the web-of-trust crawl process faster and more efficient. Rather than
optimizing against a simulation, instrument the **real production loop** so each
optimization round produces comparable speed metrics — then iterate, measure,
and compare round-over-round to drive decisions with real numbers.

## Requirements

Design decisions that emerged during spiking. Non-negotiable for follow-on work.

- **Measure in production, not simulation.** Speed signal must come from real runs
  against live Dgraph + relays, not modeled latencies.
- **Every run is tagged by a round identifier.** Default = git commit (compiled in
  via `pkg/version` ldflags); override with the `WOT_ROUND` env var for named
  experiments (e.g. `WOT_ROUND=batch-1000`).
- **Comparable records persist to a file.** One JSON record per run appended to
  `~/deepfry/crawler-metrics.jsonl` (append-only — never overwrite the config dir),
  carrying a config snapshot (timeout, batch size, quorum, relay count) so each
  round's numbers are self-describing.
- **Instrumentation stays out of the concurrency-critical path.** All timing lives
  in `cmd/crawler` (the single-threaded main loop); `pkg/crawler/crawler.go` is
  untouched to avoid disturbing the hard-won HANG/WR race fixes.
- **The relay-vs-overhead split is the primary signal.** Per batch, separate the
  relay fetch (`fetch_ms`) from everything else (`overhead_ms`) — this is the
  diagnostic that tells us whether to optimize the DB path or the relay path.

## Spikes

| # | Name | Type | Validates | Verdict | Tags |
|---|------|------|-----------|---------|------|
| 001 | crawl-speed-instrumentation | standard | Given the live crawl loop, when a run completes, then per-batch + per-run speed metrics are emitted and a comparable round-tagged record is appended to `~/deepfry/crawler-metrics.jsonl` | ✅ VALIDATED | metrics, observability, throughput |

## Optimization backlog (candidate rounds to measure)

These are the levers identified from reading the hot path. Each becomes a future
round: change one variable, run, compare the JSONL record against the baseline.

1. **frontier-batch-decouple** — `GetStalePubkeys` fetches only `RelayFilterBatchSize`
   (default 100) pubkeys per loop, coupling the frontier batch to the relay filter
   cap. Fetching N× more per loop (chunking filters independently) amortizes the
   per-loop Dgraph overhead. Compare `overhead_ms` and `pubkeys_per_sec`.
2. **drop/throttle per-batch counts** — `CountPubkeys` + `CountStalePubkeys` run every
   batch only for log lines. Throttle to every N batches or run async. Compare
   `overhead_ms`, `avg_countpubkeys_ms`, `avg_countstale_ms`.
3. **pipeline-prefetch** — prefetch the next frontier concurrently with the current
   relay fetch to hide DB latency. Compare `overhead_ms` vs `fetch_ms`.
4. **quorum-timeout-sweep** — sweep `relay_eose_quorum` and `timeout`. Compare
   `avg_fetch_ms` (latency) vs `hit_rate` (completeness).
</content>
