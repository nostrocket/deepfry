---
quick_id: 260620-t87
slug: clean-up-redundant-per-batch-crawler-log
date: 2026-06-20
status: complete
commit: 2dbc561
---

# Summary: Trim redundant per-batch crawler logging at interval=100

## Outcome

Removed the per-batch repetition of frozen, count-derived values that appeared
after `count_sample_interval` rose to 100 (task 260620-srw). The DB counts now
refresh once per 100 batches, so the human log lines that echoed them every batch
were pure noise.

## Changes (`cmd/crawler/main.go`)

- **Avg Dgraph call duration line**: on cached batches, logs only `GetStalePubkeys`
  + `MarkAttempted` (the calls that actually run every batch). `CountPubkeys` /
  `CountStalePubkeys` averages are logged only on sampled batches, where they
  actually change.
- **Batch complete line**: sampled batches log `N stale remaining | N total in DB`;
  cached batches log `~N stale remaining (est, count age=K batches)` and drop the
  frozen total-in-DB number. The `~`/`est` markers make the estimate explicit.

## Preserved

- `BATCH_METRICS` JSON schema unchanged — machine-read, feeds
  `~/deepfry/crawler-metrics.jsonl`; downstream tooling depends on stable fields.
- `new_pubkeys: null` on cached batches retained (correct: net-new can't be
  computed without a fresh count).

## Verification

- `go build ./...` — clean.
- `go vet ./cmd/crawler/` — clean.
- `go test ./cmd/crawler/ -short` — pass.
- Pending live verification: restart crawler; confirm cached batches no longer
  repeat the frozen `Count*` durations or the frozen total-in-DB number.

## Notes

Source-only change; no new binary deployed yet. Folds into the same crawler
restart already pending for task 260620-srw and the Phase 14 production cutover.
