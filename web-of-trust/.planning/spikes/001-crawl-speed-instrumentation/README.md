---
spike: 001
name: crawl-speed-instrumentation
type: standard
validates: "Given the live crawl loop, when a run completes, then per-batch and per-run speed metrics are emitted and a comparable round-tagged record is appended to ~/deepfry/crawler-metrics.jsonl"
verdict: VALIDATED
related: []
tags: [metrics, observability, throughput]
---

# Spike 001: Crawl Speed Instrumentation

## What This Validates

Given the live web-of-trust crawl loop, when a run completes (or is interrupted
with SIGINT), then:
- each batch emits a structured `BATCH_METRICS:` JSON log line with the
  relay-vs-overhead timing split and throughput, and
- the whole run appends one comparable, round-tagged `runRecord` to
  `~/deepfry/crawler-metrics.jsonl`.

This makes "is round N faster than round N-1?" answerable from real production
numbers rather than a model.

## Research

No external libraries needed — pure Go stdlib (`encoding/json`, `os`, `time`).
Key findings from reading the existing code before building:

- **The Makefile ldflags were dangling.** `LDFLAGS` referenced
  `web-of-trust/pkg/version.{Version,Commit,Built}` but no `pkg/version` package
  existed, so the linker silently dropped the `-X` injections — the crawler had
  **no** compiled-in commit. Creating `pkg/version/version.go` makes them bind,
  giving a free automatic round identifier.
- **`callMetrics` already existed** in `cmd/crawler/main.go` (cumulative avg
  duration per Dgraph call) but **never timed `FetchAndUpdateFollows`** (the relay
  fetch — the long pole) or the total batch wall-clock. The new instrumentation
  reuses `callMetrics` for those two timers.
- **A `METRICS:`/`DEBUG_METRICS:` JSON log convention** already exists in
  `pkg/crawler/crawler.go`; the new `BATCH_METRICS:` line follows it.

## How to Run

Build with the Makefile so the version ldflags bind (gives commit-based round_id):

```bash
cd web-of-trust
make build-crawler          # -> bin/crawler, Commit injected via ldflags

# Baseline round (round_id = git commit):
./bin/crawler

# Named experiment round:
WOT_ROUND=batch-1000 ./bin/crawler
```

Requires live Dgraph + relays (run on the strfry host, per project convention).

Compare rounds afterward:

```bash
# Pretty-print all recorded runs:
cat ~/deepfry/crawler-metrics.jsonl | jq .

# Compare the headline throughput across rounds:
jq -r '[.round_id, .pubkeys_per_sec, .new_pubkeys_per_sec, .avg_fetch_ms, .avg_batch_ms] | @tsv' \
  ~/deepfry/crawler-metrics.jsonl
```

## What to Expect

Per batch, on stdout:

```
BATCH_METRICS: {"round_id":"ec0a26b","batch":7,"queried":100,"hits":42,
  "hit_rate":0.42,"new_pubkeys":318,"batch_ms":15230,"fetch_ms":15010,
  "overhead_ms":220,"pubkeys_per_sec":6.566,"component":"web-of-trust-crawler",
  "metric_type":"batch_speed"}
```

`fetch_ms` ≫ `overhead_ms` means the run is **relay-bound** (optimize the relay
path: quorum/timeout). `overhead_ms` being a large fraction means it is
**DB/bookkeeping-bound** (optimize the frontier batch size / per-batch counts).

At shutdown, one line appended to `~/deepfry/crawler-metrics.jsonl`:

```json
{"round_id":"ec0a26b","commit":"ec0a26b","version":"dev","started_at":"...",
 "ended_at":"...","runtime_sec":904.2,"batches":58,"total_queried":5800,
 "total_hits":2470,"hit_rate":0.426,"pubkeys_start":100000,"pubkeys_end":118400,
 "net_new_pubkeys":18400,"pubkeys_per_sec":6.414,"new_pubkeys_per_sec":20.35,
 "avg_batch_ms":15590,"avg_fetch_ms":15010,"avg_getstale_ms":120,
 "avg_countpubkeys_ms":300,"avg_countstale_ms":140,"avg_markattempted_ms":90,
 "timeout_sec":15,"batch_size":100,"quorum":0.7,"relays":5}
```

`new_pubkeys_per_sec` is the headline metric for the crawler's core value (WoT
expansion rate); `pubkeys_per_sec` measures raw query throughput.

## Investigation Trail

1. Read `pkg/crawler/crawler.go` + `cmd/crawler/main.go` to map the hot path.
   Found the main loop is fully serial: GetStalePubkeys → CountPubkeys →
   CountStalePubkeys → FetchAndUpdateFollows → MarkAttempted, with the two count
   queries running every batch only to print a log line.
2. Pivoted from the originally-proposed simulation harnesses to real production
   instrumentation (user direction): measure the actual loop, compare rounds.
3. Discovered the dangling `pkg/version` ldflags — fixed as the round-identity
   mechanism rather than inventing a new one.
4. Kept all timing in `cmd/crawler` (single-threaded loop) to avoid touching the
   race-sensitive concurrency code in `crawler.go`.
5. Used a fresh bounded context for the shutdown ending-count — the main ctx is
   cancelled at SIGINT, which would otherwise zero out `net_new_pubkeys`.
6. Unit-tested rate math, the `WOT_ROUND` override, zero-runtime safety, and the
   append-only JSONL writer (against a temp HOME — never the real `~/deepfry`).

## Results

**VALIDATED.** `make build-crawler` binds the ldflags (`Commit=ec0a26b` now
compiled in), `go vet` clean, all crawler tests pass including 6 new metrics
tests. The instrumentation is wired end-to-end:

- `pkg/version/version.go` — build metadata, fixes dangling ldflags.
- `cmd/crawler/metrics.go` — `runStats`, `BATCH_METRICS` emitter, `runRecord`,
  `buildRunRecord`, append-only `writeRunRecord`, `resolveRoundID`.
- `cmd/crawler/main.go` — times the relay fetch + batch wall-clock, emits the
  per-batch line, appends the per-run record at shutdown.

**Surprise:** the version ldflags had been a no-op for the entire project history
— any prior attempt to read a build commit from the crawler would have gotten the
empty/default string.

**Remaining (needs the strfry host):** capture a real baseline round to confirm
the live `BATCH_METRICS` stream and the first JSONL record, then start working the
optimization backlog in MANIFEST.md one variable at a time.
</content>
