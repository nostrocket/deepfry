---
quick_id: 260620-t87
slug: clean-up-redundant-per-batch-crawler-log
date: 2026-06-20
---

# Quick Task: Trim redundant per-batch crawler logging at interval=100

## Problem

After raising `count_sample_interval` to 100 (task 260620-srw), two human-readable
log lines repeat frozen, count-derived values on every batch — they only refresh
on sampled batches (1 in 100):

1. **`Avg Dgraph call duration (cumulative)`** — prints `CountPubkeys` and
   `CountStalePubkeys` averages every batch. These only change when counts are
   sampled, so 99/100 batches log identical values
   (`CountPubkeys=8.586183833s CountStalePubkeys=24.815410041s`). `GetStalePubkeys`
   and `MarkAttempted` genuinely run every batch.
2. **`Batch complete`** — prints `N total in DB` every batch; frozen between
   samples (same `1383150` repeated 100x). `stale remaining` decrements each batch
   (via `applyMarked`) so it's a useful estimate, but it's derived from the cached
   base, not a fresh count.

## Change (`cmd/crawler/main.go`)

- Avg-duration line: on cached batches, log only `GetStalePubkeys` + `MarkAttempted`
  (the calls that actually ran). Log the count durations only when
  `countSnapshot.countsSampled`.
- Batch-complete line: when sampled, log `N stale remaining | N total in DB`.
  When cached, log `~N stale remaining (est, count age=K batches)` and drop the
  frozen total — the `~` and `est` make the staleness explicit.

## Out of scope / preserved

- `BATCH_METRICS` JSON line — machine-read, written to `crawler-metrics.jsonl`.
  Schema left fully intact (downstream tooling depends on stable fields).
- `new_pubkeys: null` on cached batches — correct (can't compute net-new without a
  fresh count), not redundant.

## Verification

- `go build ./...` clean
- `go vet ./cmd/crawler/` clean
- `go test ./cmd/crawler/ -short` pass
- Live: restart crawler; confirm cached batches no longer repeat the frozen
  Count* durations or the frozen total-in-DB number.
