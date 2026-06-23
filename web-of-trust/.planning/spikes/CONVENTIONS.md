# Spike Conventions

Patterns established across spike sessions. New spikes follow these unless the
question requires otherwise.

## Stack

- Go (the project module) — stdlib only for instrumentation; no new deps.
- Build via `make build-crawler` so `pkg/version` ldflags bind (commit-based round_id).

## The optimization-round protocol

This is a measurement-driven optimization effort. Each round:

1. Change **one** variable (config or code) — see the backlog in `MANIFEST.md`.
2. Tag the round: `WOT_ROUND=<name>` for a named experiment, else the git commit
   is used automatically.
3. Run the crawler against the live Dgraph + public relays (on any host with access to them).
4. Let it append a `runRecord` to `~/deepfry/crawler-metrics.jsonl` (Ctrl-C is fine
   — the record is written on graceful shutdown).
5. Compare against the prior round's record (`jq` over the JSONL).
6. Keep the change only if `pubkeys_per_sec` / `new_pubkeys_per_sec` improved
   without an unacceptable `hit_rate` regression.

## Patterns

- **Instrumentation lives in `cmd/crawler`, never `pkg/crawler/crawler.go`** — the
  latter holds race-sensitive concurrency fixes (HANG-*, WR-*) that must not be
  disturbed for measurement.
- **Append-only to `~/deepfry/`** — never overwrite or delete files there
  (project rule). Metrics use `O_APPEND|O_CREATE`.
- **Rates are normalized per-second** so runs of different lengths compare directly.
- **`fetch_ms` vs `overhead_ms`** is the first thing to read: it says whether the
  bottleneck is the relay path or the DB/bookkeeping path.

## Tools & Libraries

- `jq` for analyzing `~/deepfry/crawler-metrics.jsonl`.
- Go stdlib `encoding/json`, `os`, `time` for the metrics layer.
</content>
